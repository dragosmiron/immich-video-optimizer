package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"immich-video-optimizer/config"
	"immich-video-optimizer/engine"
	"immich-video-optimizer/immich"
	"immich-video-optimizer/logger"
	"immich-video-optimizer/state"
)

type Pipeline struct {
	cfg      *config.Config
	client   *immich.Client
	binaries *engine.BinaryManager
	encoder  string
	tempDir  string

	runMu     sync.Mutex
	isRunning bool
}

func NewPipeline(cfg *config.Config) (*Pipeline, error) {
	return NewPipelineWithBinaries(cfg, nil)
}

// NewPipelineWithBinaries initializes Pipeline with custom BinaryManager (useful for testing or overrides).
func NewPipelineWithBinaries(cfg *config.Config, binaries *engine.BinaryManager) (*Pipeline, error) {
	client := immich.NewClient(cfg.URL, cfg.APIKey)
	client.SetMaxRetries(cfg.MaxRetries)
	if binaries == nil {
		var err error
		binaries, err = engine.NewBinaryManager()
		if err != nil {
			return nil, fmt.Errorf("failed to init binaries: %w", err)
		}
	}
	encoder := engine.DetectHEVCEncoderDefault()
	tempDir := filepath.Join(os.TempDir(), "immich-optimizer")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	return &Pipeline{
		cfg:      cfg,
		client:   client,
		binaries: binaries,
		encoder:  encoder,
		tempDir:  tempDir,
	}, nil
}

func (p *Pipeline) SetClient(client *immich.Client) {
	if client != nil {
		p.client = client
	}
}

func (p *Pipeline) UpdateConfig(cfg *config.Config) {
	p.cfg = cfg
	p.client = immich.NewClient(cfg.URL, cfg.APIKey)
	p.client.SetMaxRetries(cfg.MaxRetries)
}

func (p *Pipeline) QueryVideos(thresholdBytes int64) ([]immich.Asset, error) {
	return p.client.QueryVideoAssets(thresholdBytes)
}

func (p *Pipeline) ScanVideos(thresholdBytes int64) (*immich.QueryResult, error) {
	return p.client.ScanVideos(thresholdBytes)
}

func (p *Pipeline) ValidateConnection() error {
	return p.client.ValidateConnection()
}

func (p *Pipeline) Client() *immich.Client {
	return p.client
}

func (p *Pipeline) Run(ctx context.Context, videos []immich.Asset, ch chan<- interface{}) {
	defer close(ch)

	p.runMu.Lock()
	if p.isRunning {
		p.runMu.Unlock()
		ch <- PipelineDoneMsg{Err: fmt.Errorf("pipeline already running")}
		return
	}
	p.isRunning = true
	p.runMu.Unlock()

	defer func() {
		p.runMu.Lock()
		p.isRunning = false
		p.runMu.Unlock()
	}()

	if len(videos) == 0 {
		ch <- PipelineDoneMsg{}
		return
	}

	ch <- PipelineStartMsg{Total: len(videos)}

	sess, _ := state.LoadSession()
	var albumID string
	if sess != nil && sess.TrackingAlbumID != "" {
		albumID = sess.TrackingAlbumID
	} else {
		timestamp := time.Now().Format("20060102-150405")
		albumName := fmt.Sprintf("Immich-Optimizer-InProgress-%s", timestamp)
		album, err := p.client.CreateTrackingAlbum(albumName)
		if err == nil && album != nil {
			albumID = album.ID
		}
	}

	var videoIDs []string
	for _, v := range videos {
		videoIDs = append(videoIDs, v.ID)
	}
	if albumID != "" {
		_ = p.client.AddAssetsToAlbum(albumID, videoIDs)
	}

	if sess == nil {
		timestamp := time.Now().Format("20060102-150405")
		sess = &state.Session{
			SessionID:       timestamp,
			Date:            time.Now(),
			TrackingAlbumID: albumID,
			Videos:          make(map[string]*state.VideoState),
			TotalVideos:     len(videos),
		}
	} else if sess.TrackingAlbumID == "" && albumID != "" {
		sess.TrackingAlbumID = albumID
	}

	for _, v := range videos {
		sess.AddOrUpdateVideo(&state.VideoState{
			ID:            v.ID,
			OriginalName:  v.OriginalFileName,
			OriginalSize:  v.GetSize(),
			Status:        state.StatusPending,
			Width:         v.ExifInfo.ExifImageWidth,
			Height:        v.ExifInfo.ExifImageHeight,
			CreatedAt:     v.FileCreatedAt,
			ModifiedAt:    v.FileModifiedAt,
			DeviceAssetID: v.DeviceAssetID,
			DeviceID:      v.DeviceID,
		})
	}
	_ = state.SaveSession(sess)

	// Filter out already-completed or skipped videos from previous sessions
	var pendingVideos []immich.Asset
	for _, v := range videos {
		if sess.IsVideoDone(v.ID) {
			continue
		}
		pendingVideos = append(pendingVideos, v)
	}

	if len(pendingVideos) == 0 {
		if sess.PendingCount() == 0 && sess.FailedCount() == 0 {
			_ = state.ClearSession()
		}
		ch <- PipelineDoneMsg{TotalSavedBytes: sess.GetTotalSavedBytes()}
		return
	}

	// Phase A: Parallel Codec Pre-Scanning (inspects small HTTP range headers in parallel)
	if p.cfg.SkipHEVC {
		ch <- PipelineStageMsg{Stage: "probing", VideoName: fmt.Sprintf("Scanning %d videos for HEVC format in parallel...", len(pendingVideos))}
		pendingVideos = p.filterHEVCParallel(ctx, pendingVideos, sess, ch)
	}

	if len(pendingVideos) == 0 || ctx.Err() != nil {
		if ctx.Err() != nil {
			_ = state.SaveSession(sess)
			ch <- PipelineDoneMsg{TotalSavedBytes: sess.GetTotalSavedBytes(), Paused: true}
			return
		}
		if sess.PendingCount() == 0 && sess.FailedCount() == 0 {
			_ = state.ClearSession()
		}
		ch <- PipelineDoneMsg{TotalSavedBytes: sess.GetTotalSavedBytes()}
		return
	}

	// Phase B: Assembly Line Pipelining (Prefetch Downloader -> Transcoder -> Background Uploader)
	queueCap := 1
	if !p.cfg.EnablePrefetch {
		queueCap = 0 // unbuffered: synchronous handoff
	}

	transcodeQueue := make(chan *WorkItem, queueCap)
	uploadQueue := make(chan *WorkItem, queueCap)

	// Worker 1: Prefetch Downloader
	var downloadWg sync.WaitGroup
	downloadWg.Add(1)
	go func() {
		defer downloadWg.Done()
		defer close(transcodeQueue)
		for _, v := range pendingVideos {
			select {
			case <-ctx.Done():
				return
			default:
			}

			ch <- PipelineStageMsg{Stage: "downloading", VideoName: v.OriginalFileName}
			origPath := filepath.Join(p.tempDir, fmt.Sprintf("orig_%s.mp4", v.ID))
			item := NewWorkItem(v, origPath)

			if err := p.client.DownloadAssetWithContext(ctx, v.ID, origPath); err != nil {
				item.Cleanup()
				if ctx.Err() != nil {
					return
				}
				sess.MarkFailed(v.ID, err.Error())
				_ = state.SaveSession(sess)
				ch <- PipelineVideoCompleteMsg{VideoID: v.ID, VideoName: v.OriginalFileName, Err: err}
				continue
			}

			// Local fallback check if SkipHEVC
			if p.cfg.SkipHEVC {
				isHEVC, err := engine.IsHEVC(ctx, p.binaries.FFmpegPath, origPath)
				if err == nil && isHEVC {
					item.Cleanup()
					sess.MarkSkipped(v.ID, "already HEVC")
					_ = state.SaveSession(sess)
					ch <- PipelineVideoCompleteMsg{VideoID: v.ID, VideoName: v.OriginalFileName, Skipped: true, SkipReason: "already HEVC"}
					continue
				}
			}

			select {
			case transcodeQueue <- item:
			case <-ctx.Done():
				item.Cleanup()
				return
			}
		}
	}()

	// Worker 2: Transcoder(s)
	numEncoders := p.cfg.ConcurrentEncoders
	if numEncoders <= 0 {
		numEncoders = 1
	} else if numEncoders > 2 {
		numEncoders = 2
	}

	var transcodeWg sync.WaitGroup
	for w := 0; w < numEncoders; w++ {
		transcodeWg.Add(1)
		go func() {
			defer transcodeWg.Done()
			for item := range transcodeQueue {
				if ctx.Err() != nil {
					item.Cleanup()
					continue
				}

				resStr := fmt.Sprintf("%dx%d", item.Asset.ExifInfo.ExifImageWidth, item.Asset.ExifInfo.ExifImageHeight)
				ch <- PipelineVideoStartMsg{
					VideoID:    item.Asset.ID,
					VideoName:  item.Asset.OriginalFileName,
					Size:       item.Asset.GetSize(),
					Resolution: resStr,
				}
				ch <- PipelineStageMsg{Stage: "transcoding", VideoName: item.Asset.OriginalFileName}

				transcodedPath := filepath.Join(p.tempDir, fmt.Sprintf("opt_%s.mp4", item.Asset.ID))
				item.TranscodedPath = transcodedPath

				opts := engine.TranscodeOptions{
					Input:      item.OrigPath,
					Output:     transcodedPath,
					Encoder:    p.encoder,
					AudioCopy:  false,
					CRF:        28,
					Preset:     p.cfg.Preset,
					Resolution: p.cfg.Resolution,
				}

				onProgress := func(prog engine.Progress) {
					select {
					case ch <- PipelineProgressMsg{VideoID: item.Asset.ID, EngineProgress: prog}:
					default:
					}
				}

				if err := engine.Transcode(ctx, p.binaries.FFmpegPath, opts, onProgress); err != nil {
					item.Cleanup()
					if ctx.Err() != nil {
						logger.Debug("Transcode aborted for %s due to cancellation", item.Asset.OriginalFileName)
						continue
					}
					logger.Error("Transcode failed for %s: %v", item.Asset.OriginalFileName, err)
					sess.MarkFailed(item.Asset.ID, err.Error())
					_ = state.SaveSession(sess)
					ch <- PipelineVideoCompleteMsg{VideoID: item.Asset.ID, VideoName: item.Asset.OriginalFileName, Err: fmt.Errorf("transcode failed: %w", err)}
					continue
				}

				if err := engine.CopyMetadata(ctx, p.binaries.ExifToolPath, item.OrigPath, transcodedPath); err != nil {
					item.Cleanup()
					if ctx.Err() != nil {
						logger.Debug("Metadata copy aborted for %s due to cancellation", item.Asset.OriginalFileName)
						continue
					}
					logger.Error("Metadata copy failed for %s: %v", item.Asset.OriginalFileName, err)
					sess.MarkFailed(item.Asset.ID, err.Error())
					_ = state.SaveSession(sess)
					ch <- PipelineVideoCompleteMsg{VideoID: item.Asset.ID, VideoName: item.Asset.OriginalFileName, Err: fmt.Errorf("metadata copy failed: %w", err)}
					continue
				}

				optStat, err := os.Stat(transcodedPath)
				if err != nil {
					item.Cleanup()
					logger.Error("Failed to stat transcoded file for %s: %v", item.Asset.OriginalFileName, err)
					sess.MarkFailed(item.Asset.ID, err.Error())
					_ = state.SaveSession(sess)
					ch <- PipelineVideoCompleteMsg{VideoID: item.Asset.ID, VideoName: item.Asset.OriginalFileName, Err: fmt.Errorf("failed to stat transcoded file: %w", err)}
					continue
				}

				if p.cfg.SizeGuard && optStat.Size() >= item.Asset.GetSize() {
					item.Cleanup()
					logger.Info("Size guard: skipped %s (transcoded %d >= original %d bytes)", item.Asset.OriginalFileName, optStat.Size(), item.Asset.GetSize())
					sess.MarkSkipped(item.Asset.ID, "transcoded file larger")
					_ = state.SaveSession(sess)
					ch <- PipelineVideoCompleteMsg{VideoID: item.Asset.ID, VideoName: item.Asset.OriginalFileName, Skipped: true, SkipReason: "transcoded file larger"}
					continue
				}

				item.SavedBytes = item.Asset.GetSize() - optStat.Size()
				logger.Debug("Transcode success for %s: saved %d bytes", item.Asset.OriginalFileName, item.SavedBytes)

				select {
				case uploadQueue <- item:
				case <-ctx.Done():
					item.Cleanup()
					continue
				}
			}
		}()
	}

	go func() {
		transcodeWg.Wait()
		close(uploadQueue)
	}()

	// Worker 3: Uploader & Finalizer
	for item := range uploadQueue {
		if ctx.Err() != nil {
			item.Cleanup()
			continue
		}

		ch <- PipelineStageMsg{Stage: "uploading", VideoName: item.Asset.OriginalFileName}

		newAsset, err := p.client.UploadReplacementAssetWithContext(ctx, item.TranscodedPath, item.Asset)
		if err != nil {
			item.Cleanup()
			if ctx.Err() != nil {
				// Paused mid-upload; leave as StatusPending for clean resume
				continue
			}
			logger.Error("Upload failed for %s: %v", item.Asset.OriginalFileName, err)
			sess.MarkFailed(item.Asset.ID, fmt.Sprintf("upload failed: %v", err))
			_ = state.SaveSession(sess)
			ch <- PipelineVideoCompleteMsg{VideoID: item.Asset.ID, VideoName: item.Asset.OriginalFileName, Err: fmt.Errorf("upload failed: %w", err)}
			continue
		}

		if newAsset != nil && newAsset.ID != "" && albumID != "" {
			_ = p.client.AddAssetsToAlbum(albumID, []string{newAsset.ID})
		}

		_ = p.client.DeleteAsset(item.Asset.ID)

		item.Cleanup()

		sess.MarkCompleted(item.Asset.ID, item.SavedBytes)
		_ = state.SaveSession(sess)

		logger.Info("Completed %s (Saved %.2f MB)", item.Asset.OriginalFileName, float64(item.SavedBytes)/1024/1024)
		ch <- PipelineVideoCompleteMsg{VideoID: item.Asset.ID, VideoName: item.Asset.OriginalFileName, SavedBytes: item.SavedBytes}
	}

	// Guarantee all workers have completely drained and terminated before proceeding
	downloadWg.Wait()
	transcodeWg.Wait()

	ch <- PipelineStageMsg{Stage: "idle", VideoName: ""}

	if ctx.Err() != nil {
		_ = state.SaveSession(sess)
		ch <- PipelineDoneMsg{TotalSavedBytes: sess.GetTotalSavedBytes(), Paused: true}
		return
	}

	if sess.PendingCount() == 0 && sess.FailedCount() == 0 {
		_ = state.ClearSession()
	}

	ch <- PipelineDoneMsg{TotalSavedBytes: sess.GetTotalSavedBytes()}
}

func (p *Pipeline) filterHEVCParallel(ctx context.Context, videos []immich.Asset, sess *state.Session, ch chan<- interface{}) []immich.Asset {
	numWorkers := 8
	if len(videos) < numWorkers {
		numWorkers = len(videos)
	}
	if numWorkers <= 0 {
		return videos
	}

	jobs := make(chan immich.Asset, len(videos))
	type probeResult struct {
		asset  immich.Asset
		isHEVC bool
	}
	results := make(chan probeResult, len(videos))

	for w := 0; w < numWorkers; w++ {
		go func() {
			for asset := range jobs {
				select {
				case <-ctx.Done():
					results <- probeResult{asset: asset, isHEVC: false}
					continue
				default:
				}

				remoteURL := fmt.Sprintf("%s/api/assets/%s/original", p.client.BaseURL(), asset.ID)
				probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
				isHEVC, err := engine.ProbeRemoteHEVC(probeCtx, p.binaries.FFmpegPath, remoteURL, p.client.APIKey())
				cancel()

				if err == nil && isHEVC {
					results <- probeResult{asset: asset, isHEVC: true}
				} else {
					results <- probeResult{asset: asset, isHEVC: false}
				}
			}
		}()
	}

	for _, v := range videos {
		jobs <- v
	}
	close(jobs)

	var toProcess []immich.Asset
	for i := 0; i < len(videos); i++ {
		res := <-results
		if res.isHEVC {
			sess.MarkSkipped(res.asset.ID, "already HEVC")
			ch <- PipelineVideoCompleteMsg{
				VideoID:    res.asset.ID,
				VideoName:  res.asset.OriginalFileName,
				Skipped:    true,
				SkipReason: "already HEVC",
			}
		} else {
			toProcess = append(toProcess, res.asset)
		}
	}
	_ = state.SaveSession(sess)
	return toProcess
}
