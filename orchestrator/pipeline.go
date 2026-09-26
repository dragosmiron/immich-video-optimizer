package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"immich-video-optimizer/config"
	"immich-video-optimizer/engine"
	"immich-video-optimizer/immich"
)

// Pipeline orchestrates the video optimization process
type Pipeline struct {
	cfg       *config.Config
	client    *immich.Client
	binaries  *engine.BinaryManager
	encoder   string
	tempDir   string
}

// NewPipeline creates a new optimization pipeline
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

// Run executes the pipeline
func (p *Pipeline) Run(ctx context.Context) error {
	thresholdMB, err := strconv.ParseInt(p.cfg.SizeThreshold, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid size threshold: %w", err)
	}
	thresholdBytes := thresholdMB * 1024 * 1024

	fmt.Printf("🔍 Querying videos larger than %d MB...\n", thresholdMB)
	videos, err := p.client.QueryVideoAssets(thresholdBytes)
	if err != nil {
		return fmt.Errorf("failed to query videos: %w", err)
	}

	if len(videos) == 0 {
		fmt.Println("No videos found exceeding the size threshold.")
		return nil
	}

	fmt.Printf("Found %d videos to optimize.\n", len(videos))

	timestamp := time.Now().Format("20060102-150405")
	albumName := fmt.Sprintf("Immich-Optimizer-InProgress-%s", timestamp)
	album, err := p.client.CreateTrackingAlbum(albumName)
	if err != nil {
		return fmt.Errorf("failed to create tracking album: %w", err)
	}
	fmt.Printf("Created tracking album: %s\n", albumName)

	var videoIDs []string
	for _, v := range videos {
		videoIDs = append(videoIDs, v.ID)
	}
	if err := p.client.AddAssetsToAlbum(album.ID, videoIDs); err != nil {
		return fmt.Errorf("failed to add assets to album: %w", err)
	}

	var optimizedCount int
	var totalSavedBytes int64

	for i, v := range videos {
		fmt.Printf("\nProcessing video %d/%d (ID: %s)\n", i+1, len(videos), v.ID)
		
		savedBytes, err := p.processVideo(ctx, v, album.ID)
		if err != nil {
			fmt.Printf("❌ Failed to process video %s: %v\n", v.ID, err)
			continue
		}

		optimizedCount++
		totalSavedBytes += savedBytes
	}

	totalSavedMB := float64(totalSavedBytes) / (1024 * 1024)
	fmt.Printf("\n🎉 Optimization Complete!\n")
	fmt.Printf("Videos Optimized: %d\n", optimizedCount)
	fmt.Printf("Total Space Saved: %.2f MB\n", totalSavedMB)
	fmt.Printf("👉 Instructions: Check the album '%s' in Immich. Once verified, you can delete the album in Immich (choose 'Delete album only', NOT 'Delete assets') to keep your optimized videos.\n", albumName)

	return nil
}

func (p *Pipeline) processVideo(ctx context.Context, v immich.Asset, albumID string) (int64, error) {
	origPath := filepath.Join(p.tempDir, fmt.Sprintf("orig_%s.mp4", v.ID))
	transcodedPath := filepath.Join(p.tempDir, fmt.Sprintf("opt_%s.mp4", v.ID))

	defer os.Remove(origPath)
	defer os.Remove(transcodedPath)

	fmt.Printf("  📥 Downloading...\n")
	if err := p.client.DownloadAsset(v.ID, origPath); err != nil {
		return 0, fmt.Errorf("download failed: %w", err)
	}

	fmt.Printf("  🎞️ Transcoding with %s...\n", p.encoder)
	opts := engine.TranscodeOptions{
		Input:     origPath,
		Output:    transcodedPath,
		Encoder:   p.encoder,
		AudioCopy: false,
		CRF:       28, // High-efficiency HEVC default
		Preset:    "medium",
	}
	
	onProgress := func(prog engine.Progress) {
		if prog.Time != "" {
			fmt.Printf("\r    ⏳ Progress: %s (fps: %.1f, speed: %s) ", prog.Time, prog.FPS, prog.Speed)
		}
	}

	if err := engine.Transcode(ctx, p.binaries.FFmpegPath, opts, onProgress); err != nil {
		return 0, fmt.Errorf("transcode failed: %w", err)
	}
	fmt.Printf("\r  ✅ Transcoding complete!                     \n")

	fmt.Printf("  📝 Copying camera metadata...\n")
	if err := engine.CopyMetadata(ctx, p.binaries.ExifToolPath, origPath, transcodedPath); err != nil {
		return 0, fmt.Errorf("metadata copy failed: %w", err)
	}

	optStat, err := os.Stat(transcodedPath)
	if err != nil {
		return 0, fmt.Errorf("failed to stat transcoded file: %w", err)
	}

	fmt.Printf("  📤 Uploading replacement...\n")
	newAsset, err := p.client.UploadReplacementAsset(transcodedPath, v)
	if err != nil {
		return 0, fmt.Errorf("upload failed: %w", err)
	}

	// Add new replacement to tracking album
	if newAsset != nil && newAsset.ID != "" && albumID != "" {
		_ = p.client.AddAssetsToAlbum(albumID, []string{newAsset.ID})
	}

	fmt.Printf("  🗑️ Deleting original giant asset...\n")
	if err := p.client.DeleteAsset(v.ID); err != nil {
		return 0, fmt.Errorf("delete original failed: %w", err)
	}

	savedBytes := v.GetSize() - optStat.Size()
	origMB := float64(v.GetSize()) / (1024 * 1024)
	optMB := float64(optStat.Size()) / (1024 * 1024)
	savedMB := float64(savedBytes) / (1024 * 1024)
	pct := float64(savedBytes) / float64(v.GetSize()) * 100
	fmt.Printf("  ✨ Saved %.2f MB (%.1f%% reduction: %.2f MB -> %.2f MB)\n", savedMB, pct, origMB, optMB)

	return savedBytes, nil
}
