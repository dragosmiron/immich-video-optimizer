package engine

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"immich-video-optimizer/logger"
)

// CheckHEVCOutput scans ffmpeg -i output for HEVC/H.265 video stream signatures.
func CheckHEVCOutput(output string) bool {
	lower := strings.ToLower(output)
	lines := strings.Split(lower, "\n")
	for _, line := range lines {
		if strings.Contains(line, "video:") {
			if strings.Contains(line, "hevc") ||
				strings.Contains(line, "h.265") ||
				strings.Contains(line, "h265") ||
				strings.Contains(line, "hev1") ||
				strings.Contains(line, "hvc1") {
				return true
			}
		}
	}
	return false
}

// IsHEVC checks if a local video file is already encoded in HEVC / H.265 format.
func IsHEVC(ctx context.Context, ffmpegPath string, inputPath string) (bool, error) {
	cmd := exec.CommandContext(ctx, ffmpegPath, "-i", inputPath)
	out, err := cmd.CombinedOutput()
	// ffmpeg returns exit status 1 when -i is run with no output file.
	// We only treat it as an error if the process could not be launched.
	if err != nil && len(out) == 0 {
		logger.Error("Local probe failed to execute for %s: %v", inputPath, err)
		return false, fmt.Errorf("failed to run ffmpeg probe: %w", err)
	}
	isHEVC := CheckHEVCOutput(string(out))
	logger.Debug("Local probe on %s: isHEVC=%v", inputPath, isHEVC)
	return isHEVC, nil
}

// ProbeRemoteHEVC attempts a fast probe of a remote video via HTTP range request
// to determine if it is already HEVC without downloading the entire file.
func ProbeRemoteHEVC(ctx context.Context, ffmpegPath string, assetURL string, apiKey string) (bool, error) {
	headers := fmt.Sprintf("x-api-key: %s\r\n", apiKey)
	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-headers", headers,
		"-i", assetURL,
	)
	out, err := cmd.CombinedOutput()
	outStr := string(out)
	if CheckHEVCOutput(outStr) {
		logger.Debug("Remote probe on %s: isHEVC=true", assetURL)
		return true, nil
	}
	// If output contains a Video: stream but it's not HEVC (e.g. h264), it's definitely not HEVC.
	if strings.Contains(strings.ToLower(outStr), "video:") {
		logger.Debug("Remote probe on %s: isHEVC=false (non-HEVC video track detected)", assetURL)
		return false, nil
	}
	// If no video stream was detected (network error, auth error, timeout), return error so caller can fallback
	if err != nil {
		logger.Debug("Remote probe on %s: failed (%v), falling back to download", assetURL, err)
		return false, fmt.Errorf("remote probe failed: %w", err)
	}
	logger.Debug("Remote probe on %s: isHEVC=false", assetURL)
	return false, nil
}
