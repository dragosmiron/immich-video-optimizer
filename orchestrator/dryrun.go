package orchestrator

import (
	"context"
	"fmt"
	"time"

	"immich-video-optimizer/engine"
	"immich-video-optimizer/immich"
	"immich-video-optimizer/logger"
	"immich-video-optimizer/state"
)

func emitOnContext(ch chan<- interface{}, ctx context.Context, msg interface{}) bool {
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

type probeJob struct {
	asset   immich.Asset
	details engine.ProbeDetails
	err     error
}

// probeRemotePool probes all videos in parallel, one HTTP-range probe per video
// with a 4s timeout, and streams the results on the returned channel.
func (p *Pipeline) probeRemotePool(ctx context.Context, videos []immich.Asset, probe func(context.Context, string) (engine.ProbeDetails, error)) <-chan probeJob {
	numWorkers := 8
	if len(videos) < numWorkers {
		numWorkers = len(videos)
	}
	jobs := make(chan immich.Asset, len(videos))
	// results is buffered to hold every job's output so a consumer that returns
	// early on ctx cancel never blocks the worker goroutines on send.
	results := make(chan probeJob, len(videos))
	for w := 0; w < numWorkers; w++ {
		go func() {
			for asset := range jobs {
				select {
				case <-ctx.Done():
					results <- probeJob{asset: asset, err: ctx.Err()}
					continue
				default:
				}

				url := fmt.Sprintf("%s/api/assets/%s/original", p.client.BaseURL(), asset.ID)
				probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
				details, err := probe(probeCtx, url)
				cancel()

				results <- probeJob{asset: asset, details: details, err: err}
			}
		}()
	}
	for _, v := range videos {
		jobs <- v
	}
	close(jobs)
	return results
}

// RunDryRun performs a read-only pre-flight check on the selected videos.
// It probes each video's codec via HTTP range headers (no downloading) and
// emits a DryRunVideoMsg per video as results arrive, followed by a single
// DryRunCompleteMsg when all probes are complete.
// The channel is closed when this function returns.
func (p *Pipeline) RunDryRun(ctx context.Context, videos []immich.Asset, ch chan<- interface{}) {
	defer close(ch)

	emit := func(msg interface{}) bool { return emitOnContext(ch, ctx, msg) }

	p.CleanDryRunAlbum()

	total := len(videos)
	if total == 0 {
		emit(DryRunCompleteMsg{})
		return
	}

	results := p.probeRemotePool(ctx, videos, func(probeCtx context.Context, url string) (engine.ProbeDetails, error) {
		return engine.ProbeRemoteDetails(probeCtx, p.binaries.FFmpegPath, url, p.client.APIKey())
	})

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
				logger.Debug("Dry run remote probe failed for %s (%s): %v", res.asset.OriginalFileName, res.asset.ID, res.err)
				probeFailed = append(probeFailed, res.asset)
			} else if p.cfg.SkipHEVC && res.details.IsHEVC {
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
		album, err := p.client.CreateTrackingAlbumWithContext(ctx, albumName)
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
			_ = p.client.AddAssetsToAlbumWithContext(ctx, albumID, ids)
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
	results := p.probeRemotePool(ctx, videos, func(probeCtx context.Context, url string) (engine.ProbeDetails, error) {
		isHEVC, err := engine.ProbeRemoteHEVC(probeCtx, p.binaries.FFmpegPath, url, p.client.APIKey())
		return engine.ProbeDetails{IsHEVC: isHEVC}, err
	})

	var toProcess []immich.Asset
	for i := 0; i < len(videos); i++ {
		select {
		case <-ctx.Done():
			return toProcess
		case res := <-results:
			if res.err == nil && res.details.IsHEVC {
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
