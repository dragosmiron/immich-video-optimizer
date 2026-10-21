package orchestrator

import (
	"context"
	"errors"
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

func markSessionFailed(sess *state.Session, videoID string, errMsg string) {
	if sess == nil {
		return
	}
	sess.MarkFailed(videoID, errMsg)
	_ = state.SaveSession(sess)
}

func markSessionSkipped(sess *state.Session, videoID string, reason string) {
	if sess == nil {
		return
	}
	sess.MarkSkipped(videoID, reason)
	_ = state.SaveSession(sess)
}

func markSessionCompleted(sess *state.Session, videoID string, savedBytes int64) {
	if sess == nil {
		return
	}
	sess.MarkCompleted(videoID, savedBytes)
	_ = state.SaveSession(sess)
}

// startDownloader launches a background worker that sequentially prefetches original video assets
// from the Immich server to temporary local files and pushes them onto transcodeQueue.
func (p *Pipeline) startDownloader(ctx context.Context, pendingVideos []immich.Asset, transcodeQueue chan<- *WorkItem, sess *state.Session, emit func(interface{}) bool, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(transcodeQueue)
		for _, v := range pendingVideos {
			select {
			case <-ctx.Done():
				return
			default:
			}

			client := p.Client()
			cfg := p.Config()

			emit(PipelineStageMsg{Stage: "downloading", VideoName: v.OriginalFileName, VideoID: v.ID})
			origPath := filepath.Join(p.tempDir, fmt.Sprintf("orig_%s.mp4", v.ID))
			item := NewWorkItem(v, origPath)

			if client == nil {
				item.Cleanup()
				emit(PipelineStageMsg{Stage: "download-idle", VideoID: v.ID})
				err := errors.New("immich client is nil")
				markSessionFailed(sess, v.ID, err.Error())
				if !emit(PipelineVideoCompleteMsg{VideoID: v.ID, VideoName: v.OriginalFileName, Err: err}) {
					return
				}
				continue
			}

			if err := client.DownloadAssetWithContext(ctx, v.ID, origPath); err != nil {
				item.Cleanup()
				emit(PipelineStageMsg{Stage: "download-idle", VideoID: v.ID})
				if ctx.Err() != nil {
					return
				}
				markSessionFailed(sess, v.ID, err.Error())
				if !emit(PipelineVideoCompleteMsg{VideoID: v.ID, VideoName: v.OriginalFileName, Err: err}) {
					return
				}
				continue
			}

			// Local fallback check if SkipHEVC
			if cfg != nil && cfg.SkipHEVC {
				isHEVC, err := engine.IsHEVC(ctx, p.binaries.FFmpegPath, origPath)
				if err == nil && isHEVC {
					item.Cleanup()
					emit(PipelineStageMsg{Stage: "download-idle", VideoID: v.ID})
					markSessionSkipped(sess, v.ID, "already HEVC")
					if !emit(PipelineVideoCompleteMsg{VideoID: v.ID, VideoName: v.OriginalFileName, OriginalSize: v.GetSize(), Skipped: true, SkipReason: "already HEVC"}) {
						return
					}
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
}

// startTranscoders launches numEncoders concurrent transcoder workers that re-encode videos from transcodeQueue,
// transfer ExifTool metadata, apply the size guard, and enqueue successfully transcoded items into uploadQueue.
func (p *Pipeline) startTranscoders(ctx context.Context, numEncoders int, transcodeQueue <-chan *WorkItem, uploadQueue chan<- *WorkItem, sess *state.Session, emit func(interface{}) bool, ch chan<- interface{}, wg *sync.WaitGroup) {
	for w := 0; w < numEncoders; w++ {
		workerID := w + 1
		wg.Add(1)
		go func(wID int) {
			defer wg.Done()
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

				cfg := p.Config()
				if cfg == nil {
					cfg = config.DefaultConfig()
				}
				preset := cfg.Preset
				if preset == "" {
					preset = config.DefaultConfig().Preset
				}
				crf := cfg.QualityCRF()
				vtQuality := cfg.QualityVideoToolbox()
				res := cfg.Resolution
				customFlags := cfg.CustomFFmpegFlags
				sizeGuard := cfg.SizeGuard
				hasAudio, isAAC := engine.ProbeAudioStream(ctx, p.binaries.FFmpegPath, item.OrigPath)
				opts := engine.TranscodeOptions{
					Input:               item.OrigPath,
					Output:              transcodedPath,
					Encoder:             p.encoder,
					AudioCopy:           isAAC,
					NoAudio:             !hasAudio,
					CRF:                 crf,
					VideoToolboxQuality: vtQuality,
					Preset:              preset,
					Resolution:          res,
					CustomFlags:         customFlags,
					OnFallback: func(failedEncoder string, err error) {
						reason := ""
						if err != nil {
							reason = err.Error()
						}
						item.Fallback = FallbackInfo{
							Used:          true,
							FailedEncoder: failedEncoder,
							Reason:        reason,
						}
						emit(PipelineFallbackMsg{
							WorkerID:      wID,
							VideoID:       item.Asset.ID,
							VideoName:     item.Asset.OriginalFileName,
							FailedEncoder: failedEncoder,
							Reason:        reason,
						})
					},
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
					markSessionFailed(sess, item.Asset.ID, err.Error())
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
					markSessionFailed(sess, item.Asset.ID, err.Error())
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
					markSessionFailed(sess, item.Asset.ID, err.Error())
					emit(PipelineVideoCompleteMsg{
						WorkerID:  wID,
						VideoID:   item.Asset.ID,
						VideoName: item.Asset.OriginalFileName,
						Err:       fmt.Errorf("failed to stat transcoded file: %w", err),
					})
					continue
				}

				if sizeGuard && optStat.Size() >= item.Asset.GetSize() {
					item.Cleanup()
					logger.Info("Size guard: skipped %s (transcoded %d >= original %d bytes)", item.Asset.OriginalFileName, optStat.Size(), item.Asset.GetSize())
					markSessionSkipped(sess, item.Asset.ID, "transcoded file larger")
					emit(PipelineVideoCompleteMsg{
						WorkerID:      wID,
						VideoID:       item.Asset.ID,
						VideoName:     item.Asset.OriginalFileName,
						OriginalSize:  item.Asset.GetSize(),
						OptimizedSize: optStat.Size(),
						Skipped:       true,
						SkipReason:    "transcoded file larger",
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
}

// runUploader processes the uploadQueue: uploads replacement assets, transfers metadata, deletes originals, and updates session state.
func (p *Pipeline) runUploader(ctx context.Context, uploadQueue <-chan *WorkItem, albumID *string, optimizerTagID string, sess *state.Session, emit func(interface{}) bool) {
	for item := range uploadQueue {
		if ctx.Err() != nil {
			item.Cleanup()
			continue
		}
		client := p.Client()
		if client == nil {
			item.Cleanup()
			err := errors.New("immich client is nil")
			markSessionFailed(sess, item.Asset.ID, err.Error())
			if !emit(PipelineVideoCompleteMsg{VideoID: item.Asset.ID, VideoName: item.Asset.OriginalFileName, Err: err}) {
				for rem := range uploadQueue {
					rem.Cleanup()
				}
				return
			}
			continue
		}

		// Query original asset's albums and favorite status before replacement/deletion
		origAlbums, err := client.GetAssetAlbumsWithContext(ctx, item.Asset.ID)
		if err != nil {
			item.Cleanup()
			if ctx.Err() != nil {
				continue
			}
			logger.Error("Failed to query albums for original asset %s (%s): %v", item.Asset.OriginalFileName, item.Asset.ID, err)
			markSessionFailed(sess, item.Asset.ID, fmt.Sprintf("failed to query albums: %v", err))
			emit(PipelineVideoCompleteMsg{VideoID: item.Asset.ID, VideoName: item.Asset.OriginalFileName, Err: fmt.Errorf("failed to query albums: %w", err)})
			continue
		}

		origAsset, err := client.GetAssetWithContext(ctx, item.Asset.ID)
		if err != nil || origAsset == nil {
			item.Cleanup()
			if ctx.Err() != nil {
				continue
			}
			logger.Error("Failed to read details for original asset %s (%s): %v", item.Asset.OriginalFileName, item.Asset.ID, err)
			markSessionFailed(sess, item.Asset.ID, fmt.Sprintf("failed to read original asset details: %v", err))
			emit(PipelineVideoCompleteMsg{VideoID: item.Asset.ID, VideoName: item.Asset.OriginalFileName, Err: fmt.Errorf("failed to read original asset details: %w", err)})
			continue
		}
		isFavorite := item.Asset.IsFavorite || origAsset.IsFavorite

		emit(PipelineStageMsg{Stage: "uploading", VideoName: item.Asset.OriginalFileName, VideoID: item.Asset.ID})

		newAsset, err := client.UploadReplacementAssetWithContext(ctx, item.TranscodedPath, item.Asset)
		if err != nil {
			item.Cleanup()
			if ctx.Err() != nil {
				// Paused mid-upload; leave as StatusPending for clean resume
				continue
			}
			logger.Error("Upload failed for %s: %v", item.Asset.OriginalFileName, err)
			markSessionFailed(sess, item.Asset.ID, fmt.Sprintf("upload failed: %v", err))
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
			markSessionFailed(sess, item.Asset.ID, errMsg)
			emit(PipelineVideoCompleteMsg{VideoID: item.Asset.ID, VideoName: item.Asset.OriginalFileName, Err: errors.New(errMsg)})
			continue
		}

		if albumID != nil && *albumID == "" {
			timestamp := time.Now().Format("20060102-150405")
			albumName := fmt.Sprintf("Immich-Optimizer-InProgress-%s", timestamp)
			album, err := client.CreateTrackingAlbumWithContext(ctx, albumName)
			if err != nil {
				logger.Error("Failed to create tracking album %s: %v", albumName, err)
			} else if album != nil {
				*albumID = album.ID
				if sess != nil {
					sess.TrackingAlbumID = *albumID
					_ = state.SaveSession(sess)
				}
			}
		}

		currentAlbumID := ""
		if albumID != nil {
			currentAlbumID = *albumID
		}
		if currentAlbumID != "" {
			_ = client.AddAssetsToAlbumWithContext(ctx, currentAlbumID, []string{newAsset.ID})
		}

		// Transfer metadata (albums, favorite, description, tags, stack)
		transferErr := p.transferAssetMetadata(ctx, origAsset, newAsset.ID, origAlbums, isFavorite, currentAlbumID)

		if ctx.Err() != nil && transferErr == nil {
			item.Cleanup()
			p.rollbackAsset(newAsset.ID)
			continue
		}

		// Safety guard: original asset is ONLY deleted after replacement is uploaded and all metadata transfers succeed
		if transferErr != nil {
			item.Cleanup()
			p.rollbackAsset(newAsset.ID)
			if ctx.Err() != nil {
				// Paused mid-transfer; leave as StatusPending for clean resume
				continue
			}
			logger.Error("Safety guard triggered for %s (%s): %v; aborting deletion of original", item.Asset.OriginalFileName, item.Asset.ID, transferErr)
			markSessionFailed(sess, item.Asset.ID, transferErr.Error())
			emit(PipelineVideoCompleteMsg{
				VideoID:   item.Asset.ID,
				VideoName: item.Asset.OriginalFileName,
				Err:       transferErr,
			})
			continue
		}

		emit(PipelineStageMsg{Stage: "finalizing", VideoName: item.Asset.OriginalFileName, VideoID: item.Asset.ID})

		// Use a detached short-timeout context so a pause during final deletion
		// completes cleanly rather than leaving duplicate assets or marking failed.
		deleteCtx := ctx
		var deleteCancel context.CancelFunc
		if ctx.Err() != nil {
			deleteCtx, deleteCancel = context.WithTimeout(context.Background(), 10*time.Second)
		}

		deleteErr := client.DeleteAssetWithContext(deleteCtx, item.Asset.ID)
		if deleteCancel != nil {
			deleteCancel()
		}
		if deleteErr != nil {
			item.Cleanup()
			p.rollbackAsset(newAsset.ID)
			if ctx.Err() != nil {
				continue
			}
			logger.Error("Failed to delete original asset %s (%s): %v", item.Asset.OriginalFileName, item.Asset.ID, deleteErr)
			markSessionFailed(sess, item.Asset.ID, fmt.Sprintf("failed to delete original asset: %v", deleteErr))
			emit(PipelineVideoCompleteMsg{VideoID: item.Asset.ID, VideoName: item.Asset.OriginalFileName, Err: fmt.Errorf("failed to delete original asset: %w", deleteErr)})
			continue
		}

		item.Cleanup()

		markSessionCompleted(sess, item.Asset.ID, item.SavedBytes)

		// Tag replacement asset with 'immich-optimizer' if enabled
		if optimizerTagID != "" {
			if err := client.TagAssetWithContext(ctx, optimizerTagID, newAsset.ID); err != nil {
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
			Fallback:      item.Fallback,
		})
	}
}
