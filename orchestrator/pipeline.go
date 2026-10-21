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

type Pipeline struct {
	cfgMu    sync.RWMutex
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
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	copyCfg := *cfg
	client := immich.NewClient(copyCfg.URL, copyCfg.APIKey)
	client.SetMaxRetries(copyCfg.MaxRetries)
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
		cfg:      &copyCfg,
		client:   client,
		binaries: binaries,
		encoder:  encoder,
		tempDir:  tempDir,
	}, nil
}


func (p *Pipeline) UpdateConfig(cfg *config.Config) {
	if p == nil {
		return
	}
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	copyCfg := *cfg
	p.cfgMu.Lock()
	defer p.cfgMu.Unlock()
	p.cfg = &copyCfg
	p.client = immich.NewClient(copyCfg.URL, copyCfg.APIKey)
	p.client.SetMaxRetries(copyCfg.MaxRetries)
}

func (p *Pipeline) Config() *config.Config {
	if p == nil {
		return nil
	}
	p.cfgMu.RLock()
	defer p.cfgMu.RUnlock()
	if p.cfg == nil {
		return nil
	}
	copyCfg := *p.cfg
	return &copyCfg
}

func (p *Pipeline) ScanVideos(mode string, threshold float64) (*immich.QueryResult, error) {
	return p.ScanVideosWithContext(context.Background(), mode, threshold)
}

func (p *Pipeline) ScanVideosWithContext(ctx context.Context, mode string, threshold float64) (*immich.QueryResult, error) {
	if p == nil {
		return nil, errors.New("pipeline is nil")
	}
	cfg := p.Config()
	client := p.Client()
	if client == nil {
		return nil, errors.New("immich client is nil")
	}
	opts := immich.ScanOptions{}
	if cfg != nil {
		opts.ExcludeOptimized = cfg.ExcludeOptimized
		opts.DateAfter = cfg.DateAfter
		opts.DateBefore = cfg.DateBefore
	}
	return client.ScanVideosWithOptions(ctx, mode, threshold, opts)
}

func (p *Pipeline) ValidateConnection() error {
	if p == nil {
		return errors.New("pipeline is nil")
	}
	client := p.Client()
	if client == nil {
		return errors.New("immich client is nil")
	}
	return client.ValidateConnection()
}

func (p *Pipeline) Client() *immich.Client {
	if p == nil {
		return nil
	}
	p.cfgMu.RLock()
	defer p.cfgMu.RUnlock()
	return p.client
}

// Encoder returns the configured or auto-detected HEVC encoder.
func (p *Pipeline) Encoder() string {
	if p == nil || p.encoder == "" {
		return engine.DetectHEVCEncoderDefault()
	}
	return p.encoder
}

// CleanDryRunAlbum deletes any previously created dry-run tracking album in Immich.
func (p *Pipeline) CleanDryRunAlbum() {
	if p == nil {
		return
	}
	p.dryRunMu.Lock()
	albumID := p.dryRunAlbumID
	p.dryRunAlbumID = ""
	p.dryRunMu.Unlock()

	client := p.Client()
	if albumID != "" && client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.DeleteAlbumWithContext(ctx, albumID)
	}
}

// SetDryRunAlbumID sets or updates the dry run album ID.
func (p *Pipeline) SetDryRunAlbumID(albumID string) {
	if p == nil {
		return
	}
	p.dryRunMu.Lock()
	p.dryRunAlbumID = albumID
	p.dryRunMu.Unlock()
}

// DryRunAlbumID returns the current dry run album ID.
func (p *Pipeline) DryRunAlbumID() string {
	if p == nil {
		return ""
	}
	p.dryRunMu.Lock()
	defer p.dryRunMu.Unlock()
	return p.dryRunAlbumID
}

func (p *Pipeline) Run(ctx context.Context, videos []immich.Asset, ch chan<- interface{}) {
	defer close(ch)
	if p == nil {
		return
	}

	emit := func(msg interface{}) bool { return emitOnContext(ctx, ch, msg) }

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

	client := p.Client()
	cfg := p.Config()

	if albumID == "" && dryRunAlbum != "" {
		albumID = dryRunAlbum
		timestamp := time.Now().Format("20060102-150405")
		inProgressName := fmt.Sprintf("Immich-Optimizer-InProgress-%s", timestamp)
		if client != nil {
			if err := client.UpdateAlbumNameWithContext(ctx, albumID, inProgressName); err != nil {
				logger.Error("Failed to rename dry-run album %s to %s: %v", albumID, inProgressName, err)
			}
		}
	} else if dryRunAlbum != "" && dryRunAlbum != albumID {
		if client != nil {
			_ = client.DeleteAlbumWithContext(ctx, dryRunAlbum)
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
	if cfg != nil && cfg.SkipHEVC {
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
	if albumID == "" && len(pendingVideos) > 0 && client != nil {
		timestamp := time.Now().Format("20060102-150405")
		albumName := fmt.Sprintf("Immich-Optimizer-InProgress-%s", timestamp)
		album, err := client.CreateTrackingAlbumWithContext(ctx, albumName)
		if err != nil {
			logger.Error("Failed to create tracking album %s: %v", albumName, err)
		} else if album != nil {
			albumID = album.ID
			sess.TrackingAlbumID = albumID
			_ = state.SaveSession(sess)
		}
	}

	if albumID != "" && len(pendingVideos) > 0 && client != nil {
		var origIDs []string
		for _, v := range pendingVideos {
			origIDs = append(origIDs, v.ID)
		}
		_ = client.AddAssetsToAlbumWithContext(ctx, albumID, origIDs)
	}

	numEncoders := 1
	enablePrefetch := true
	if cfg != nil {
		numEncoders = cfg.ConcurrentEncoders
		enablePrefetch = cfg.EnablePrefetch
	}
	if numEncoders <= 0 {
		numEncoders = 1
	} else if numEncoders > 2 {
		numEncoders = 2
	}

	queueCap := numEncoders
	if !enablePrefetch {
		queueCap = 0 // unbuffered: synchronous handoff
	}

	transcodeQueue := make(chan *WorkItem, queueCap)
	uploadCap := numEncoders + 2
	if !enablePrefetch {
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
	if cfg != nil && cfg.ExcludeOptimized && client != nil {
		tag, err := client.GetOrCreateTagWithContext(ctx, "immich-optimizer")
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
