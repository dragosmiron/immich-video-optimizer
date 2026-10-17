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
	// Remove any stale temp files left by a previous hard-killed run.
	// Only touch files older than 1 hour so we don't interfere with an
	// active or just-finished pipeline in the same process.
	for _, pattern := range []string{"orig_*.mp4", "opt_*.mp4"} {
		matches, _ := filepath.Glob(filepath.Join(tempDir, pattern))
		for _, f := range matches {
			if info, err := os.Stat(f); err == nil && time.Since(info.ModTime()) > time.Hour {
				_ = os.Remove(f)
			}
		}
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
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = p.client.DeleteAlbumWithContext(ctx, albumID)
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
			if err := p.client.UpdateAlbumNameWithContext(ctx, albumID, inProgressName); err != nil {
				logger.Error("Failed to rename dry-run album %s to %s: %v", albumID, inProgressName, err)
			}
		}
	} else if dryRunAlbum != "" && dryRunAlbum != albumID {
		if p.client != nil {
			_ = p.client.DeleteAlbumWithContext(ctx, dryRunAlbum)
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
		album, err := p.client.CreateTrackingAlbumWithContext(ctx, albumName)
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
		_ = p.client.AddAssetsToAlbumWithContext(ctx, albumID, origIDs)
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
	p.startDownloader(ctx, pendingVideos, transcodeQueue, sess, emit, &downloadWg)

	// Worker 2: Transcoder(s)
	var transcodeWg sync.WaitGroup
	p.startTranscoders(ctx, numEncoders, transcodeQueue, uploadQueue, sess, emit, ch, &transcodeWg)

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
	p.runUploader(ctx, uploadQueue, &albumID, optimizerTagID, sess, emit)

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
