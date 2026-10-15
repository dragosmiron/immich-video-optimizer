package engine

import (
	"compress/gzip"
	"embed"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

//go:embed bin/*
var EmbeddedBinaries embed.FS

// BinaryManager handles finding required binaries
type BinaryManager struct {
	FFmpegPath   string
	ExifToolPath string
}

func getCacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".cache", "immich-video-optimizer", "bin")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

func extractBinary(name string, cacheDir string) (string, error) {
	outPath := filepath.Join(cacheDir, name)
	if info, err := os.Stat(outPath); err == nil && info.Size() > 0 {
		return outPath, nil // Already extracted
	}

	data, err := EmbeddedBinaries.ReadFile("bin/" + name)
	if err != nil {
		return "", err
	}

	if err := os.WriteFile(outPath, data, 0755); err != nil {
		return "", err
	}

	return outPath, nil
}

func findOrExtractBinary(name string, cacheDir string) (string, error) {
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}

	// Try extracting embedded binary
	if path, err := extractBinary(name, cacheDir); err == nil {
		return path, nil
	}

	// Fallback to check if it's already in cache without embedding
	path := filepath.Join(cacheDir, name)
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		return path, nil
	}
	if runtime.GOOS == "windows" {
		exePath := filepath.Join(cacheDir, name+".exe")
		if info, err := os.Stat(exePath); err == nil && info.Size() > 0 {
			return exePath, nil
		}
	}

	// If it's ffmpeg and missing, attempt transparent download for macOS/Linux
	if name == "ffmpeg" {
		if downloadedPath, err := downloadStaticFFmpeg(cacheDir); err == nil {
			return downloadedPath, nil
		}
	}

	return "", fmt.Errorf("%s not found in PATH or cache, and extraction failed", name)
}

func downloadStaticFFmpeg(cacheDir string) (string, error) {
	var downloadURL string
	switch runtime.GOOS + "_" + runtime.GOARCH {
	case "darwin_arm64":
		downloadURL = "https://github.com/eugeneware/ffmpeg-static/releases/latest/download/ffmpeg-darwin-arm64.gz"
	case "darwin_amd64":
		downloadURL = "https://github.com/eugeneware/ffmpeg-static/releases/latest/download/ffmpeg-darwin-x64.gz"
	case "linux_amd64":
		downloadURL = "https://github.com/eugeneware/ffmpeg-static/releases/latest/download/ffmpeg-linux-x64.gz"
	case "linux_arm64":
		downloadURL = "https://github.com/eugeneware/ffmpeg-static/releases/latest/download/ffmpeg-linux-arm64.gz"
	default:
		return "", fmt.Errorf("unsupported platform for auto-download: %s_%s", runtime.GOOS, runtime.GOARCH)
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(downloadURL)
	if err != nil {
		return "", fmt.Errorf("failed to download ffmpeg: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	gzReader, err := gzip.NewReader(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzReader.Close()

	targetPath := filepath.Join(cacheDir, "ffmpeg")
	tmpPath := filepath.Join(cacheDir, fmt.Sprintf("ffmpeg.tmp-%d", os.Getpid()))
	outFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return "", fmt.Errorf("failed to create target binary: %w", err)
	}
	defer func() {
		_ = outFile.Close()
		_ = os.Remove(tmpPath)
	}()

	if _, err := io.Copy(outFile, gzReader); err != nil {
		return "", fmt.Errorf("failed to extract gzip stream: %w", err)
	}
	if err := outFile.Close(); err != nil {
		return "", fmt.Errorf("failed to close binary: %w", err)
	}

	if err := os.Rename(tmpPath, targetPath); err != nil {
		return "", fmt.Errorf("failed to rename binary: %w", err)
	}

	return targetPath, nil
}

// NewBinaryManager creates a manager by looking up binaries in the host PATH.
// Fallbacks to runtime extraction caches if needed.
func NewBinaryManager() (*BinaryManager, error) {
	cacheDir, err := getCacheDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get cache dir: %w", err)
	}

	ffmpeg, err := findOrExtractBinary("ffmpeg", cacheDir)
	if err != nil {
		return nil, err
	}

	exiftool, err := findOrExtractBinary("exiftool", cacheDir)
	if err != nil {
		return nil, err
	}

	return &BinaryManager{
		FFmpegPath:   ffmpeg,
		ExifToolPath: exiftool,
	}, nil
}
