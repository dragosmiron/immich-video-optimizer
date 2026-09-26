package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"immich-video-optimizer/config"
	"immich-video-optimizer/engine"
	"immich-video-optimizer/immich"
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

func (p *Pipeline) QueryVideos(thresholdBytes int64) ([]immich.Asset, error) {
	return p.client.QueryVideoAssets(thresholdBytes)
}

func (p *Pipeline) Run(ctx context.Context, videos []immich.Asset, ch chan<- interface{}) {
	defer close(ch)
	if len(videos) == 0 {
		ch <- PipelineDoneMsg{0, nil}
		return
	}

	ch <- PipelineStartMsg{Total: len(videos)}

	timestamp := time.Now().Format("20060102-150405")
	albumName := fmt.Sprintf("Immich-Optimizer-InProgress-%s", timestamp)
	album, err := p.client.CreateTrackingAlbum(albumName)
	if err != nil {
		ch <- PipelineDoneMsg{0, fmt.Errorf("tracking album error: %w", err)}
		return
	}

	var videoIDs []string
	for _, v := range videos {
		videoIDs = append(videoIDs, v.ID)
	}
	_ = p.client.AddAssetsToAlbum(album.ID, videoIDs)

	var totalSavedBytes int64

	for _, v := range videos {
		select {
		case <-ctx.Done():
			ch <- PipelineDoneMsg{totalSavedBytes, ctx.Err()}
			return
		default:
		}

		ch <- PipelineVideoStartMsg{VideoID: v.ID, Size: v.GetSize()}
		saved, skipped, err := p.processVideo(ctx, v, album.ID, ch)
		ch <- PipelineVideoCompleteMsg{VideoID: v.ID, SavedBytes: saved, Skipped: skipped, Err: err}
		totalSavedBytes += saved
	}

	ch <- PipelineDoneMsg{TotalSavedBytes: totalSavedBytes, Err: nil}
}

func (p *Pipeline) processVideo(ctx context.Context, v immich.Asset, albumID string, ch chan<- interface{}) (int64, bool, error) {
	origPath := filepath.Join(p.tempDir, fmt.Sprintf("orig_%s.mp4", v.ID))
	transcodedPath := filepath.Join(p.tempDir, fmt.Sprintf("opt_%s.mp4", v.ID))
	defer os.Remove(origPath)
	defer os.Remove(transcodedPath)

	if err := p.client.DownloadAsset(v.ID, origPath); err != nil {
		return 0, false, fmt.Errorf("download failed: %w", err)
	}

	opts := engine.TranscodeOptions{
		Input:      origPath,
		Output:     transcodedPath,
		Encoder:    p.encoder,
		AudioCopy:  false,
		CRF:        28,
		Preset:     p.cfg.Preset,
		Resolution: p.cfg.Resolution,
	}

	onProgress := func(prog engine.Progress) {
		ch <- PipelineProgressMsg{VideoID: v.ID, EngineProgress: prog}
	}

	if err := engine.Transcode(ctx, p.binaries.FFmpegPath, opts, onProgress); err != nil {
		return 0, false, fmt.Errorf("transcode failed: %w", err)
	}

	if err := engine.CopyMetadata(ctx, p.binaries.ExifToolPath, origPath, transcodedPath); err != nil {
		return 0, false, fmt.Errorf("metadata copy failed: %w", err)
	}

	optStat, err := os.Stat(transcodedPath)
	if err != nil {
		return 0, false, fmt.Errorf("failed to stat transcoded file: %w", err)
	}

	if p.cfg.SizeGuard && optStat.Size() >= v.GetSize() {
		return 0, true, nil
	}

	newAsset, err := p.client.UploadReplacementAsset(transcodedPath, v)
	if err != nil {
		return 0, false, fmt.Errorf("upload failed: %w", err)
	}

	if newAsset != nil && newAsset.ID != "" && albumID != "" {
		_ = p.client.AddAssetsToAlbum(albumID, []string{newAsset.ID})
	}

	if err := p.client.DeleteAsset(v.ID); err != nil {
		return 0, false, fmt.Errorf("delete original failed: %w", err)
	}

	savedBytes := v.GetSize() - optStat.Size()
	return savedBytes, false, nil
}
