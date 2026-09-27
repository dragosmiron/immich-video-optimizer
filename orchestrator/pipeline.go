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
	"immich-video-optimizer/state"
)

type Pipeline struct {
	cfg       *config.Config
	client    *immich.Client
	binaries  *engine.BinaryManager
	encoder   string
	tempDir   string
}

func NewPipeline(cfg *config.Config) (*Pipeline, error) {
	client := immich.NewClient(cfg.URL, cfg.APIKey)
	binaries, err := engine.NewBinaryManager()
	if err != nil {
		return nil, fmt.Errorf("failed to init binaries: %w", err)
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

func (p *Pipeline) UpdateConfig(cfg *config.Config) {
	p.cfg = cfg
	p.client = immich.NewClient(cfg.URL, cfg.APIKey)
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

type downloadedItem struct {
	asset      immich.Asset
	origPath   string
	skipped    bool
	skipReason string
	err        error
}

type transcodedItem struct {
	asset          immich.Asset
	origPath       string
	transcodedPath string
	savedBytes     int64
	skipped        bool
	skipReason     string
	err            error
}

func (p *Pipeline) Run(ctx context.Context, videos []immich.Asset, ch chan<- interface{}) {
	defer close(ch)
	defer func() {
		// Clean up any remaining temporary files in tempDir on exit
		entries, err := os.ReadDir(p.tempDir)
		if err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					_ = os.Remove(filepath.Join(p.tempDir, e.Name()))
				}
			}
		}
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
		if vState, exists := sess.Videos[v.ID]; !exists {
			sess.Videos[v.ID] = &state.VideoState{
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
			}
		} else if vState.Status == state.StatusFailed {
			vState.Status = state.StatusPending
			vState.ErrorMessage = ""
		}
	}
	_ = state.SaveSession(sess)

	// Filter out already-completed or skipped videos from previous sessions
	var pendingVideos []immich.Asset
	for _, v := range videos {
		if vState, ok := sess.Videos[v.ID]; ok && (vState.Status == state.StatusCompleted || vState.Status == state.StatusSkipped) {
			continue
		}
		pendingVideos = append(pendingVideos, v)
	}

	if len(pendingVideos) == 0 {
		if sess.PendingCount() == 0 && sess.FailedCount() == 0 {
			_ = state.ClearSession()
		}
		ch <- PipelineDoneMsg{TotalSavedBytes: sess.TotalSavedBytes}
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
			ch <- PipelineDoneMsg{TotalSavedBytes: sess.TotalSavedBytes, Paused: true}
			return
		}
		if sess.PendingCount() == 0 && sess.FailedCount() == 0 {
			_ = state.ClearSession()
		}
		ch <- PipelineDoneMsg{TotalSavedBytes: sess.TotalSavedBytes}
		return
	}

	// Phase B: Assembly Line Pipelining (Prefetch Downloader -> Transcoder -> Background Uploader)
	queueCap := 1
	if !p.cfg.EnablePrefetch {
		queueCap = 0 // unbuffered: synchronous handoff
	}

	transcodeQueue := make(chan downloadedItem, queueCap)
	uploadQueue := make(chan transcodedItem, queueCap)

	// Worker 1: Prefetch Downloader
	go func() {
		defer close(transcodeQueue)
		for _, v := range pendingVideos {
			select {
			case <-ctx.Done():
				return
			default:
			}

			ch <- PipelineStageMsg{Stage: "downloading", VideoName: v.OriginalFileName}
			origPath := filepath.Join(p.tempDir, fmt.Sprintf("orig_%s.mp4", v.ID))

			if err := p.client.DownloadAssetWithContext(ctx, v.ID, origPath); err != nil {
				if ctx.Err() != nil {
					_ = os.Remove(origPath)
					return
				}
				select {
				case transcodeQueue <- downloadedItem{asset: v, err: err}:
				case <-ctx.Done():
					_ = os.Remove(origPath)
					return
				}
				continue
			}

			// Local fallback check if SkipHEVC
			if p.cfg.SkipHEVC {
				isHEVC, err := engine.IsHEVC(ctx, p.binaries.FFmpegPath, origPath)
				if err == nil && isHEVC {
					_ = os.Remove(origPath)
					select {
					case transcodeQueue <- downloadedItem{asset: v, skipped: true, skipReason: "already HEVC"}:
					case <-ctx.Done():
						return
					}
					continue
				}
			}

			select {
			case transcodeQueue <- downloadedItem{asset: v, origPath: origPath}:
			case <-ctx.Done():
				_ = os.Remove(origPath)
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
					if item.origPath != "" {
						_ = os.Remove(item.origPath)
					}
					continue
				}

				if item.err != nil || item.skipped {
					select {
					case uploadQueue <- transcodedItem{
						asset:      item.asset,
						skipped:    item.skipped,
						skipReason: item.skipReason,
						err:        item.err,
					}:
					case <-ctx.Done():
						continue
					}
					continue
				}

				resStr := fmt.Sprintf("%dx%d", item.asset.ExifInfo.ExifImageWidth, item.asset.ExifInfo.ExifImageHeight)
				ch <- PipelineVideoStartMsg{
					VideoID:    item.asset.ID,
					VideoName:  item.asset.OriginalFileName,
					Size:       item.asset.GetSize(),
					Resolution: resStr,
				}
				ch <- PipelineStageMsg{Stage: "transcoding", VideoName: item.asset.OriginalFileName}

				transcodedPath := filepath.Join(p.tempDir, fmt.Sprintf("opt_%s.mp4", item.asset.ID))

				opts := engine.TranscodeOptions{
					Input:      item.origPath,
					Output:     transcodedPath,
					Encoder:    p.encoder,
					AudioCopy:  false,
					CRF:        28,
					Preset:     p.cfg.Preset,
					Resolution: p.cfg.Resolution,
				}

				onProgress := func(prog engine.Progress) {
					ch <- PipelineProgressMsg{VideoID: item.asset.ID, EngineProgress: prog}
				}

				if err := engine.Transcode(ctx, p.binaries.FFmpegPath, opts, onProgress); err != nil {
					_ = os.Remove(item.origPath)
					_ = os.Remove(transcodedPath)
					if ctx.Err() != nil {
						continue
					}
					select {
					case uploadQueue <- transcodedItem{asset: item.asset, err: fmt.Errorf("transcode failed: %w", err)}:
					case <-ctx.Done():
						continue
					}
					continue
				}

				if err := engine.CopyMetadata(ctx, p.binaries.ExifToolPath, item.origPath, transcodedPath); err != nil {
					_ = os.Remove(item.origPath)
					_ = os.Remove(transcodedPath)
					if ctx.Err() != nil {
						continue
					}
					select {
					case uploadQueue <- transcodedItem{asset: item.asset, err: fmt.Errorf("metadata copy failed: %w", err)}:
					case <-ctx.Done():
						continue
					}
					continue
				}

				optStat, err := os.Stat(transcodedPath)
				if err != nil {
					_ = os.Remove(item.origPath)
					_ = os.Remove(transcodedPath)
					select {
					case uploadQueue <- transcodedItem{asset: item.asset, err: fmt.Errorf("failed to stat transcoded file: %w", err)}:
					case <-ctx.Done():
						continue
					}
					continue
				}

				if p.cfg.SizeGuard && optStat.Size() >= item.asset.GetSize() {
					_ = os.Remove(item.origPath)
					_ = os.Remove(transcodedPath)
					select {
					case uploadQueue <- transcodedItem{asset: item.asset, skipped: true, skipReason: "transcoded file larger"}:
					case <-ctx.Done():
						continue
					}
					continue
				}

				savedBytes := item.asset.GetSize() - optStat.Size()
				select {
				case uploadQueue <- transcodedItem{
					asset:          item.asset,
					origPath:       item.origPath,
					transcodedPath: transcodedPath,
					savedBytes:     savedBytes,
				}:
				case <-ctx.Done():
					_ = os.Remove(item.origPath)
					_ = os.Remove(transcodedPath)
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
	var totalSavedBytes int64 = sess.TotalSavedBytes

	for item := range uploadQueue {
		if ctx.Err() != nil {
			if item.origPath != "" {
				_ = os.Remove(item.origPath)
			}
			if item.transcodedPath != "" {
				_ = os.Remove(item.transcodedPath)
			}
			continue
		}

		if item.err != nil {
			if vState, ok := sess.Videos[item.asset.ID]; ok {
				vState.Status = state.StatusFailed
				vState.ErrorMessage = item.err.Error()
				_ = state.SaveSession(sess)
			}
			ch <- PipelineVideoCompleteMsg{VideoID: item.asset.ID, Err: item.err}
			continue
		}

		if item.skipped {
			if vState, ok := sess.Videos[item.asset.ID]; ok {
				vState.Status = state.StatusSkipped
				_ = state.SaveSession(sess)
			}
			ch <- PipelineVideoCompleteMsg{VideoID: item.asset.ID, Skipped: true, SkipReason: item.skipReason}
			continue
		}

		ch <- PipelineStageMsg{Stage: "uploading", VideoName: item.asset.OriginalFileName}

		newAsset, err := p.client.UploadReplacementAssetWithContext(ctx, item.transcodedPath, item.asset)
		if err != nil {
			_ = os.Remove(item.origPath)
			_ = os.Remove(item.transcodedPath)
			if ctx.Err() != nil {
				// Paused mid-upload; leave as StatusPending for clean resume
				continue
			}
			if vState, ok := sess.Videos[item.asset.ID]; ok {
				vState.Status = state.StatusFailed
				vState.ErrorMessage = fmt.Sprintf("upload failed: %v", err)
				_ = state.SaveSession(sess)
			}
			ch <- PipelineVideoCompleteMsg{VideoID: item.asset.ID, Err: fmt.Errorf("upload failed: %w", err)}
			continue
		}

		if newAsset != nil && newAsset.ID != "" && albumID != "" {
			_ = p.client.AddAssetsToAlbum(albumID, []string{newAsset.ID})
		}

		_ = p.client.DeleteAsset(item.asset.ID)

		_ = os.Remove(item.origPath)
		_ = os.Remove(item.transcodedPath)

		if vState, ok := sess.Videos[item.asset.ID]; ok {
			vState.Status = state.StatusCompleted
			vState.SavedBytes = item.savedBytes
			sess.TotalSavedBytes += item.savedBytes
			_ = state.SaveSession(sess)
		}

		totalSavedBytes += item.savedBytes
		ch <- PipelineVideoCompleteMsg{VideoID: item.asset.ID, SavedBytes: item.savedBytes}
	}

	ch <- PipelineStageMsg{Stage: "idle", VideoName: ""}

	if ctx.Err() != nil {
		_ = state.SaveSession(sess)
		ch <- PipelineDoneMsg{TotalSavedBytes: totalSavedBytes, Paused: true}
		return
	}

	if sess.PendingCount() == 0 && sess.FailedCount() == 0 {
		_ = state.ClearSession()
	}

	ch <- PipelineDoneMsg{TotalSavedBytes: totalSavedBytes}
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
			if vState, ok := sess.Videos[res.asset.ID]; ok {
				vState.Status = state.StatusSkipped
			}
			ch <- PipelineVideoCompleteMsg{
				VideoID:    res.asset.ID,
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
