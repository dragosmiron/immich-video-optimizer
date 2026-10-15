package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	dryRunMu      sync.Mutex
	dryRunAlbumID string

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

func (p *Pipeline) ScanVideos(mode string, threshold float64) (*immich.QueryResult, error) {
	return p.ScanVideosWithContext(context.Background(), mode, threshold)
}

func (p *Pipeline) ScanVideosWithContext(ctx context.Context, mode string, threshold float64) (*immich.QueryResult, error) {
	opts := immich.ScanOptions{}
	if p.cfg != nil {
		opts.ExcludeOptimized = p.cfg.ExcludeOptimized
		opts.DateAfter = p.cfg.DateAfter
		opts.DateBefore = p.cfg.DateBefore
	}
	return p.client.ScanVideosWithOptions(ctx, mode, threshold, opts)
}

func (p *Pipeline) ValidateConnection() error {
	return p.client.ValidateConnection()
}

func (p *Pipeline) Client() *immich.Client {
	return p.client
}
// CleanDryRunAlbum deletes any previously created dry-run tracking album in Immich.
func (p *Pipeline) CleanDryRunAlbum() {
	p.dryRunMu.Lock()
	albumID := p.dryRunAlbumID
	p.dryRunAlbumID = ""
	p.dryRunMu.Unlock()

	if albumID != "" && p.client != nil {
		_ = p.client.DeleteAlbum(albumID)
	}
}

// SetDryRunAlbumID sets or updates the dry run album ID.
func (p *Pipeline) SetDryRunAlbumID(albumID string) {
	p.dryRunMu.Lock()
	p.dryRunAlbumID = albumID
	p.dryRunMu.Unlock()
}

// DryRunAlbumID returns the current dry run album ID.
func (p *Pipeline) DryRunAlbumID() string {
	p.dryRunMu.Lock()
	defer p.dryRunMu.Unlock()
	return p.dryRunAlbumID
}

func (p *Pipeline) renameAlbumToCompleted(albumID string) {
	if albumID == "" || p.client == nil {
		return
	}
	timestamp := time.Now().Format("20060102-150405")
	completedName := fmt.Sprintf("Immich-Optimizer-Completed-%s", timestamp)
	if err := p.client.UpdateAlbumName(albumID, completedName); err != nil {
		logger.Error("Failed to rename tracking album %s to completed %s: %v", albumID, completedName, err)
	}
}

func (p *Pipeline) rollbackAsset(assetID string) {
	if assetID == "" || p.client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.client.DeleteAssetWithContext(ctx, assetID); err != nil {
		logger.Error("Failed to rollback replacement asset %s: %v", assetID, err)
	}
}

func (p *Pipeline) Run(ctx context.Context, videos []immich.Asset, ch chan<- interface{}) {
	defer close(ch)

	emit := func(msg interface{}) bool {
		select {
		case ch <- msg:
			return true
		case <-ctx.Done():
			select {
			case ch <- msg:
				return true
			default:
				return false
			}
		}
	}

	p.runMu.Lock()
	if p.isRunning {
		p.runMu.Unlock()
		emit(PipelineDoneMsg{Err: fmt.Errorf("pipeline already running")})
		return
	}
	p.isRunning = true
	p.runMu.Unlock()

	p.dryRunMu.Lock()
	dryRunAlbum := p.dryRunAlbumID
	p.dryRunAlbumID = ""
	p.dryRunMu.Unlock()

	defer func() {
		p.runMu.Lock()
		p.isRunning = false
		p.runMu.Unlock()
	}()

	if len(videos) == 0 {
		emit(PipelineDoneMsg{})
		return
	}

	emit(PipelineStartMsg{Total: len(videos)})

	sess, _ := state.LoadSession()
	var albumID string
	if sess != nil && sess.TrackingAlbumID != "" {
		albumID = sess.TrackingAlbumID
	}

	if albumID == "" && dryRunAlbum != "" {
		albumID = dryRunAlbum
		timestamp := time.Now().Format("20060102-150405")
		inProgressName := fmt.Sprintf("Immich-Optimizer-InProgress-%s", timestamp)
		if p.client != nil {
			if err := p.client.UpdateAlbumName(albumID, inProgressName); err != nil {
				logger.Error("Failed to rename dry-run album %s to %s: %v", albumID, inProgressName, err)
			}
		}
	} else if dryRunAlbum != "" && dryRunAlbum != albumID {
		if p.client != nil {
			_ = p.client.DeleteAlbum(dryRunAlbum)
		}
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
		_ = state.SaveSession(sess)
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
			IsFavorite:    v.IsFavorite,
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
		p.renameAlbumToCompleted(albumID)
		emit(PipelineDoneMsg{TotalSavedBytes: sess.GetTotalSavedBytes()})
		return
	}

	// Phase A: Parallel Codec Pre-Scanning (inspects small HTTP range headers in parallel)
	if p.cfg.SkipHEVC {
		emit(PipelineStageMsg{Stage: "probing", VideoName: fmt.Sprintf("Scanning %d videos for HEVC format in parallel...", len(pendingVideos))})
		pendingVideos = p.filterHEVCParallel(ctx, pendingVideos, sess, emit)
	}

	if len(pendingVideos) == 0 || ctx.Err() != nil {
		if ctx.Err() != nil {
			_ = state.SaveSession(sess)
			emit(PipelineDoneMsg{TotalSavedBytes: sess.GetTotalSavedBytes(), Paused: true})
			return
		}
		if sess.PendingCount() == 0 && sess.FailedCount() == 0 {
			_ = state.ClearSession()
		}
		p.renameAlbumToCompleted(albumID)
		emit(PipelineDoneMsg{TotalSavedBytes: sess.GetTotalSavedBytes()})
		return
	}

	// Phase B: Assembly Line Pipelining (Prefetch Downloader -> Transcoder -> Background Uploader)
	if albumID == "" && len(pendingVideos) > 0 {
		timestamp := time.Now().Format("20060102-150405")
		albumName := fmt.Sprintf("Immich-Optimizer-InProgress-%s", timestamp)
		album, err := p.client.CreateTrackingAlbum(albumName)
		if err != nil {
			logger.Error("Failed to create tracking album %s: %v", albumName, err)
		} else if album != nil {
			albumID = album.ID
			sess.TrackingAlbumID = albumID
			_ = state.SaveSession(sess)
		}
	}

	if albumID != "" && len(pendingVideos) > 0 {
		var origIDs []string
		for _, v := range pendingVideos {
			origIDs = append(origIDs, v.ID)
		}
		_ = p.client.AddAssetsToAlbum(albumID, origIDs)
	}

	numEncoders := p.cfg.ConcurrentEncoders
	if numEncoders <= 0 {
		numEncoders = 1
	} else if numEncoders > 2 {
		numEncoders = 2
	}

	queueCap := numEncoders
	if !p.cfg.EnablePrefetch {
		queueCap = 0 // unbuffered: synchronous handoff
	}

	transcodeQueue := make(chan *WorkItem, queueCap)
	uploadCap := numEncoders + 2
	if !p.cfg.EnablePrefetch {
		uploadCap = numEncoders
	}
	uploadQueue := make(chan *WorkItem, uploadCap)

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

			emit(PipelineStageMsg{Stage: "downloading", VideoName: v.OriginalFileName, VideoID: v.ID})
			origPath := filepath.Join(p.tempDir, fmt.Sprintf("orig_%s.mp4", v.ID))
			item := NewWorkItem(v, origPath)

			if err := p.client.DownloadAssetWithContext(ctx, v.ID, origPath); err != nil {
				item.Cleanup()
				emit(PipelineStageMsg{Stage: "download-idle", VideoID: v.ID})
				if ctx.Err() != nil {
					return
				}
				sess.MarkFailed(v.ID, err.Error())
				_ = state.SaveSession(sess)
				emit(PipelineVideoCompleteMsg{VideoID: v.ID, VideoName: v.OriginalFileName, Err: err})
				continue
			}

			// Local fallback check if SkipHEVC
			if p.cfg.SkipHEVC {
				isHEVC, err := engine.IsHEVC(ctx, p.binaries.FFmpegPath, origPath)
				if err == nil && isHEVC {
					item.Cleanup()
					emit(PipelineStageMsg{Stage: "download-idle", VideoID: v.ID})
					sess.MarkSkipped(v.ID, "already HEVC")
					_ = state.SaveSession(sess)
					emit(PipelineVideoCompleteMsg{VideoID: v.ID, VideoName: v.OriginalFileName, OriginalSize: v.GetSize(), Skipped: true, SkipReason: "already HEVC"})
					continue
				}
			}

			select {
			case transcodeQueue <- item:
				emit(PipelineStageMsg{Stage: "download-idle", VideoID: v.ID})
			case <-ctx.Done():
				item.Cleanup()
				emit(PipelineStageMsg{Stage: "download-idle", VideoID: v.ID})
				return
			}
		}
	}()

	// Worker 2: Transcoder(s)
	var transcodeWg sync.WaitGroup
	for w := 0; w < numEncoders; w++ {
		workerID := w + 1
		transcodeWg.Add(1)
		go func(wID int) {
			defer transcodeWg.Done()
			for item := range transcodeQueue {
				if ctx.Err() != nil {
					item.Cleanup()
					continue
				}

				resStr := fmt.Sprintf("%dx%d", item.Asset.ExifInfo.ExifImageWidth, item.Asset.ExifInfo.ExifImageHeight)
				emit(PipelineVideoStartMsg{
					WorkerID:   wID,
					VideoID:    item.Asset.ID,
					VideoName:  item.Asset.OriginalFileName,
					Size:       item.Asset.GetSize(),
					Resolution: resStr,
				})
				emit(PipelineStageMsg{
					WorkerID:  wID,
					VideoID:   item.Asset.ID,
					Stage:     "transcoding",
					VideoName: item.Asset.OriginalFileName,
				})

				transcodedPath := filepath.Join(p.tempDir, fmt.Sprintf("opt_%s.mp4", item.Asset.ID))
				item.TranscodedPath = transcodedPath

				preset := p.cfg.Preset
				if preset == "" {
					preset = "Medium"
				}
				opts := engine.TranscodeOptions{
					Input:               item.OrigPath,
					Output:              transcodedPath,
					Encoder:             p.encoder,
					AudioCopy:           false,
					CRF:                 p.cfg.QualityCRF(),
					VideoToolboxQuality: p.cfg.QualityVideoToolbox(),
					Preset:              preset,
					Resolution:          p.cfg.Resolution,
				}

				onProgress := func(prog engine.Progress) {
					select {
					case ch <- PipelineProgressMsg{
						WorkerID:       wID,
						VideoID:        item.Asset.ID,
						EngineProgress: prog,
					}:
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
					emit(PipelineVideoCompleteMsg{
						WorkerID:  wID,
						VideoID:   item.Asset.ID,
						VideoName: item.Asset.OriginalFileName,
						Err:       fmt.Errorf("transcode failed: %w", err),
					})
					continue
				}

				emit(PipelineStageMsg{
					WorkerID:  wID,
					VideoID:   item.Asset.ID,
					Stage:     "metadata",
					VideoName: item.Asset.OriginalFileName,
				})

				if err := engine.CopyMetadata(ctx, p.binaries.ExifToolPath, item.OrigPath, transcodedPath); err != nil {
					item.Cleanup()
					if ctx.Err() != nil {
						logger.Debug("Metadata copy aborted for %s due to cancellation", item.Asset.OriginalFileName)
						continue
					}
					logger.Error("Metadata copy failed for %s: %v", item.Asset.OriginalFileName, err)
					sess.MarkFailed(item.Asset.ID, err.Error())
					_ = state.SaveSession(sess)
					emit(PipelineVideoCompleteMsg{
						WorkerID:  wID,
						VideoID:   item.Asset.ID,
						VideoName: item.Asset.OriginalFileName,
						Err:       fmt.Errorf("metadata copy failed: %w", err),
					})
					continue
				}

				// Clean up the local original file right away to free disk space immediately,
				// since only transcodedPath is needed for upload.
				if item.OrigPath != "" {
					_ = os.Remove(item.OrigPath)
					item.OrigPath = ""
				}

				optStat, err := os.Stat(transcodedPath)
				if err != nil {
					item.Cleanup()
					logger.Error("Failed to stat transcoded file for %s: %v", item.Asset.OriginalFileName, err)
					sess.MarkFailed(item.Asset.ID, err.Error())
					_ = state.SaveSession(sess)
					emit(PipelineVideoCompleteMsg{
						WorkerID:  wID,
						VideoID:   item.Asset.ID,
						VideoName: item.Asset.OriginalFileName,
						Err:       fmt.Errorf("failed to stat transcoded file: %w", err),
					})
					continue
				}

				if p.cfg.SizeGuard && optStat.Size() >= item.Asset.GetSize() {
					item.Cleanup()
					logger.Info("Size guard: skipped %s (transcoded %d >= original %d bytes)", item.Asset.OriginalFileName, optStat.Size(), item.Asset.GetSize())
					sess.MarkSkipped(item.Asset.ID, "transcoded file larger")
					_ = state.SaveSession(sess)
					emit(PipelineVideoCompleteMsg{
						WorkerID:     wID,
						VideoID:      item.Asset.ID,
						VideoName:    item.Asset.OriginalFileName,
						OriginalSize: item.Asset.GetSize(),
						Skipped:      true,
						SkipReason:   "transcoded file larger",
					})
					continue
				}

				item.OptimizedSize = optStat.Size()
				item.SavedBytes = item.Asset.GetSize() - optStat.Size()
				logger.Debug("Transcode success for %s: saved %d bytes", item.Asset.OriginalFileName, item.SavedBytes)

				emit(PipelineStageMsg{
					WorkerID:  wID,
					VideoID:   item.Asset.ID,
					Stage:     "idle",
					VideoName: "",
				})

				select {
				case uploadQueue <- item:
				case <-ctx.Done():
					item.Cleanup()
					continue
				}
			}
		}(workerID)
	}

	go func() {
		transcodeWg.Wait()
		close(uploadQueue)
	}()

	var optimizerTagID string
	if p.cfg != nil && p.cfg.ExcludeOptimized {
		tag, err := p.client.GetOrCreateTagWithContext(ctx, "immich-optimizer")
		if err != nil {
			logger.Error("Failed to get/create 'immich-optimizer' tag: %v (ensure Tags are enabled in Immich Account Settings)", err)
		} else if tag != nil {
			optimizerTagID = tag.ID
		}
	}

	// Worker 3: Uploader & Finalizer
	for item := range uploadQueue {
		if ctx.Err() != nil {
			item.Cleanup()
			continue
		}

		// Query original asset's albums and favorite status before replacement/deletion
		origAlbums, err := p.client.GetAssetAlbumsWithContext(ctx, item.Asset.ID)
		if err != nil {
			item.Cleanup()
			if ctx.Err() != nil {
				continue
			}
			logger.Error("Failed to query albums for original asset %s (%s): %v", item.Asset.OriginalFileName, item.Asset.ID, err)
			sess.MarkFailed(item.Asset.ID, fmt.Sprintf("failed to query albums: %v", err))
			_ = state.SaveSession(sess)
			emit(PipelineVideoCompleteMsg{VideoID: item.Asset.ID, VideoName: item.Asset.OriginalFileName, Err: fmt.Errorf("failed to query albums: %w", err)})
			continue
		}

		isFavorite := item.Asset.IsFavorite
		if !isFavorite {
			origAsset, err := p.client.GetAssetWithContext(ctx, item.Asset.ID)
			if err != nil {
				if ctx.Err() != nil {
					item.Cleanup()
					continue
				}
				logger.Debug("Could not query asset info for %s: %v", item.Asset.ID, err)
			} else if origAsset != nil {
				isFavorite = origAsset.IsFavorite
			}
		}

		emit(PipelineStageMsg{Stage: "uploading", VideoName: item.Asset.OriginalFileName, VideoID: item.Asset.ID})

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
			emit(PipelineVideoCompleteMsg{VideoID: item.Asset.ID, VideoName: item.Asset.OriginalFileName, Err: fmt.Errorf("upload failed: %w", err)})
			continue
		}

		if newAsset == nil || newAsset.ID == "" || newAsset.ID == item.Asset.ID {
			item.Cleanup()
			errMsg := "upload returned invalid or duplicate asset ID"
			if newAsset != nil && newAsset.ID == item.Asset.ID {
				errMsg = fmt.Sprintf("upload returned identical asset ID %s (potential duplicate collision)", item.Asset.ID)
			}
			logger.Error("Safety guard triggered for %s: %s; aborting deletion of original", item.Asset.OriginalFileName, errMsg)
			sess.MarkFailed(item.Asset.ID, errMsg)
			_ = state.SaveSession(sess)
			emit(PipelineVideoCompleteMsg{VideoID: item.Asset.ID, VideoName: item.Asset.OriginalFileName, Err: errors.New(errMsg)})
			continue
		}

		if albumID == "" {
			timestamp := time.Now().Format("20060102-150405")
			albumName := fmt.Sprintf("Immich-Optimizer-InProgress-%s", timestamp)
			album, err := p.client.CreateTrackingAlbum(albumName)
			if err != nil {
				logger.Error("Failed to create tracking album %s: %v", albumName, err)
			} else if album != nil {
				albumID = album.ID
				sess.TrackingAlbumID = albumID
				_ = state.SaveSession(sess)
			}
		}

		if albumID != "" {
			_ = p.client.AddAssetsToAlbumWithContext(ctx, albumID, []string{newAsset.ID})
		}

		// Add replacement asset to all existing albums of the original asset (excluding tracking album)
		var albumTransferErr error
		seenAlbums := make(map[string]bool)
		for _, alb := range origAlbums {
			if alb.ID == "" || alb.ID == albumID || strings.HasPrefix(alb.AlbumName, "Immich-Optimizer-") || seenAlbums[alb.ID] {
				continue
			}
			seenAlbums[alb.ID] = true
			if err := p.client.AddAssetsToAlbumWithContext(ctx, alb.ID, []string{newAsset.ID}); err != nil {
				logger.Error("Failed to add new asset %s to original album %s (%s): %v", newAsset.ID, alb.AlbumName, alb.ID, err)
				albumTransferErr = err
				break
			}
		}

		// Transfer favorite status if original was favorited
		var favTransferErr error
		if isFavorite {
			if err := p.client.SetAssetFavoriteWithContext(ctx, newAsset.ID, true); err != nil {
				logger.Error("Failed to transfer favorite status to new asset %s: %v", newAsset.ID, err)
				favTransferErr = err
			}
		}

		if ctx.Err() != nil && (albumTransferErr == nil && favTransferErr == nil) {
			item.Cleanup()
			p.rollbackAsset(newAsset.ID)
			continue
		}

		// Safety guard: original asset is ONLY deleted after replacement is uploaded,
		// added to all original albums, and favorite status is transferred.
		if albumTransferErr != nil || favTransferErr != nil {
			item.Cleanup()
			p.rollbackAsset(newAsset.ID)
			if ctx.Err() != nil {
				// Paused mid-transfer; leave as StatusPending for clean resume
				continue
			}
			errDesc := "failed to transfer album membership"
			if favTransferErr != nil && albumTransferErr == nil {
				errDesc = "failed to transfer favorite status"
			} else if albumTransferErr != nil && favTransferErr != nil {
				errDesc = "failed to transfer album membership and favorite status"
			}
			fullErr := fmt.Errorf("%s; original asset preserved", errDesc)
			logger.Error("Safety guard triggered for %s (%s): %v; aborting deletion of original", item.Asset.OriginalFileName, item.Asset.ID, fullErr)
			sess.MarkFailed(item.Asset.ID, fullErr.Error())
			_ = state.SaveSession(sess)
			emit(PipelineVideoCompleteMsg{
				VideoID:   item.Asset.ID,
				VideoName: item.Asset.OriginalFileName,
				Err:       fullErr,
			})
			continue
		}

		emit(PipelineStageMsg{Stage: "finalizing", VideoName: item.Asset.OriginalFileName, VideoID: item.Asset.ID})

		// Use a detached short-timeout context so a pause during final deletion
		// completes cleanly rather than leaving duplicate assets or marking failed.
		deleteCtx := ctx
		if ctx.Err() != nil {
			var cancel context.CancelFunc
			deleteCtx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
		}

		if err := p.client.DeleteAssetWithContext(deleteCtx, item.Asset.ID); err != nil {
			item.Cleanup()
			p.rollbackAsset(newAsset.ID)
			if ctx.Err() != nil {
				continue
			}
			logger.Error("Failed to delete original asset %s (%s): %v", item.Asset.OriginalFileName, item.Asset.ID, err)
			sess.MarkFailed(item.Asset.ID, fmt.Sprintf("failed to delete original asset: %v", err))
			_ = state.SaveSession(sess)
			emit(PipelineVideoCompleteMsg{VideoID: item.Asset.ID, VideoName: item.Asset.OriginalFileName, Err: fmt.Errorf("failed to delete original asset: %w", err)})
			continue
		}

		item.Cleanup()

		sess.MarkCompleted(item.Asset.ID, item.SavedBytes)
		_ = state.SaveSession(sess)

		// Tag replacement asset with 'immich-optimizer' if enabled
		if optimizerTagID != "" {
			if err := p.client.TagAssetWithContext(ctx, optimizerTagID, newAsset.ID); err != nil {
				logger.Error("Failed to tag %s with 'immich-optimizer': %v", newAsset.ID, err)
			} else {
				logger.Info("Tagged %s with 'immich-optimizer'", item.Asset.OriginalFileName)
			}
		}

		logger.Info("Completed %s (Saved %.2f MB)", item.Asset.OriginalFileName, float64(item.SavedBytes)/1024/1024)
		emit(PipelineVideoCompleteMsg{
			VideoID:       item.Asset.ID,
			VideoName:     item.Asset.OriginalFileName,
			OriginalSize:  item.Asset.GetSize(),
			OptimizedSize: item.OptimizedSize,
			SavedBytes:    item.SavedBytes,
		})
	}

	// Guarantee all workers have completely drained and terminated before proceeding
	downloadWg.Wait()
	transcodeWg.Wait()

	emit(PipelineStageMsg{Stage: "idle", VideoName: ""})

	if ctx.Err() != nil {
		_ = state.SaveSession(sess)
		emit(PipelineDoneMsg{TotalSavedBytes: sess.GetTotalSavedBytes(), Paused: true})
		return
	}

	if sess.PendingCount() == 0 && sess.FailedCount() == 0 {
		_ = state.ClearSession()
	}

	p.renameAlbumToCompleted(albumID)

	emit(PipelineDoneMsg{TotalSavedBytes: sess.GetTotalSavedBytes()})
}

// RunDryRun performs a read-only pre-flight check on the selected videos.
// It probes each video's codec via HTTP range headers (no downloading) and
// emits a DryRunVideoMsg per video as results arrive, followed by a single
// DryRunCompleteMsg when all probes are complete.
// The channel is closed when this function returns.
func (p *Pipeline) RunDryRun(ctx context.Context, videos []immich.Asset, ch chan<- interface{}) {
	defer close(ch)

	emit := func(msg interface{}) bool {
		select {
		case ch <- msg:
			return true
		case <-ctx.Done():
			select {
			case ch <- msg:
				return true
			default:
				return false
			}
		}
	}

	p.CleanDryRunAlbum()

	total := len(videos)
	if total == 0 {
		emit(DryRunCompleteMsg{})
		return
	}

	numWorkers := 8
	if total < numWorkers {
		numWorkers = total
	}

	type probeResult struct {
		asset   immich.Asset
		details engine.ProbeDetails
		err     error
	}

	jobs := make(chan immich.Asset, total)
	results := make(chan probeResult, total)

	for w := 0; w < numWorkers; w++ {
		go func() {
			for asset := range jobs {
				select {
				case <-ctx.Done():
					results <- probeResult{asset: asset, details: engine.ProbeDetails{}, err: ctx.Err()}
					continue
				default:
				}

				remoteURL := fmt.Sprintf("%s/api/assets/%s/original", p.client.BaseURL(), asset.ID)
				probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
				details, err := engine.ProbeRemoteDetails(probeCtx, p.binaries.FFmpegPath, remoteURL, p.client.APIKey())
				cancel()

				if err != nil {
					logger.Debug("Dry run remote probe failed for %s (%s): %v", asset.OriginalFileName, asset.ID, err)
				}

				results <- probeResult{
					asset:   asset,
					details: details,
					err:     err,
				}
			}
		}()
	}

	for _, v := range videos {
		jobs <- v
	}
	close(jobs)

	var toProcess []immich.Asset
	var hevcSkipped []immich.Asset
	var probeFailed []immich.Asset
	videoDetails := make(map[string]engine.ProbeDetails)
	probed := 0

	for i := 0; i < total; i++ {
		select {
		case <-ctx.Done():
			return
		case res := <-results:
			probed++
			videoDetails[res.asset.ID] = res.details

			if res.err != nil {
				probeFailed = append(probeFailed, res.asset)
			}

			if p.cfg.SkipHEVC && res.details.IsHEVC {
				hevcSkipped = append(hevcSkipped, res.asset)
			} else {
				toProcess = append(toProcess, res.asset)
			}
			emit(DryRunVideoMsg{
				VideoID:    res.asset.ID,
				VideoName:  res.asset.OriginalFileName,
				IsHEVC:     res.details.IsHEVC,
				Codec:      res.details.Codec,
				Resolution: res.details.Resolution,
				Probed:     probed,
				Total:      total,
				Err:        res.err,
			})
		}
	}

	var albumName string
	var albumID string
	if len(toProcess) > 0 {
		timestamp := time.Now().Format("20060102-150405")
		albumName = fmt.Sprintf("Immich-Optimizer-DryRun-%s", timestamp)
		album, err := p.client.CreateTrackingAlbum(albumName)
		if err != nil {
			logger.Error("Failed to create dry-run album %s: %v", albumName, err)
		} else if album != nil {
			albumID = album.ID
			p.dryRunMu.Lock()
			p.dryRunAlbumID = albumID
			p.dryRunMu.Unlock()

			var ids []string
			for _, a := range toProcess {
				ids = append(ids, a.ID)
			}
			_ = p.client.AddAssetsToAlbum(albumID, ids)
		}
	}

	emit(DryRunCompleteMsg{
		ToProcess:       toProcess,
		HEVCSkipped:     hevcSkipped,
		ProbeFailed:     probeFailed,
		VideoDetails:    videoDetails,
		DryRunAlbumName: albumName,
		DryRunAlbumID:   albumID,
	})
}

func (p *Pipeline) filterHEVCParallel(ctx context.Context, videos []immich.Asset, sess *state.Session, emit func(interface{}) bool) []immich.Asset {
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
		select {
		case <-ctx.Done():
			return toProcess
		case res := <-results:
			if res.isHEVC {
				sess.MarkSkipped(res.asset.ID, "already HEVC")
				if !emit(PipelineVideoCompleteMsg{
					VideoID:      res.asset.ID,
					VideoName:    res.asset.OriginalFileName,
					OriginalSize: res.asset.GetSize(),
					Skipped:      true,
					SkipReason:   "already HEVC",
				}) {
					return toProcess
				}
			} else {
				toProcess = append(toProcess, res.asset)
			}
		}
	}
	_ = state.SaveSession(sess)
	return toProcess
}
