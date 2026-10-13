package engine

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"immich-video-optimizer/logger"
)

var resRegex = regexp.MustCompile(`\b(\d{3,5})x(\d{3,5})\b`)

// ProbeDetails contains video codec and resolution metadata extracted from ffmpeg output.
type ProbeDetails struct {
	Codec      string // e.g. "H.264", "HEVC", "VP9", "MPEG-4", "ProRes"
	Resolution string // e.g. "1080p", "4K", "720p", "1920x1080"
	IsHEVC     bool
}

// ParseVideoDetails extracts codec and resolution metadata from ffmpeg -i output.
func ParseVideoDetails(output string) ProbeDetails {
	details := ProbeDetails{
		IsHEVC: CheckHEVCOutput(output),
	}

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		lower := strings.ToLower(line)
		idx := strings.Index(lower, "video:")
		if idx == -1 {
			continue
		}

		afterVideo := strings.TrimSpace(line[idx+len("video:"):])
		codecToken := afterVideo
		for _, sep := range []string{" ", ",", "(", "["} {
			if pos := strings.Index(codecToken, sep); pos != -1 {
				codecToken = codecToken[:pos]
			}
		}
		codecToken = strings.TrimSpace(strings.ToLower(codecToken))

		switch {
		case codecToken == "h264" || codecToken == "avc1" || strings.Contains(codecToken, "264"):
			details.Codec = "H.264"
		case codecToken == "hevc" || codecToken == "h265" || codecToken == "hvc1" || codecToken == "hev1" || strings.Contains(codecToken, "265"):
			details.Codec = "HEVC"
			details.IsHEVC = true
		case codecToken == "vp9":
			details.Codec = "VP9"
		case codecToken == "vp8":
			details.Codec = "VP8"
		case codecToken == "av1":
			details.Codec = "AV1"
		case codecToken == "mpeg4" || codecToken == "mp4v":
			details.Codec = "MPEG-4"
		case codecToken == "mjpeg":
			details.Codec = "MJPEG"
		case strings.HasPrefix(codecToken, "prores"):
			details.Codec = "ProRes"
		default:
			if len(codecToken) > 0 {
				details.Codec = strings.ToUpper(codecToken)
			}
		}

		matches := resRegex.FindStringSubmatch(line)
		if len(matches) == 3 {
			w, _ := strconv.Atoi(matches[1])
			h, _ := strconv.Atoi(matches[2])
			if (w >= 3800 && h >= 2100) || (w >= 2100 && h >= 3800) {
				details.Resolution = "4K"
			} else if (w == 2560 && h == 1440) || (w == 1440 && h == 2560) {
				details.Resolution = "1440p"
			} else if (w == 1920 && h == 1080) || (w == 1080 && h == 1920) {
				details.Resolution = "1080p"
			} else if (w == 1280 && h == 720) || (w == 720 && h == 1280) {
				details.Resolution = "720p"
			} else if h == 480 || w == 480 {
				details.Resolution = "480p"
			} else {
				details.Resolution = fmt.Sprintf("%dx%d", w, h)
			}
		}

		if details.Codec != "" {
			break
		}
	}

	if details.IsHEVC && details.Codec == "" {
		details.Codec = "HEVC"
	}
	return details
}

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
	if err != nil && len(out) == 0 {
		logger.Error("Local probe failed to execute for %s: %v", inputPath, err)
		return false, fmt.Errorf("failed to run ffmpeg probe: %w", err)
	}
	isHEVC := CheckHEVCOutput(string(out))
	logger.Debug("Local probe on %s: isHEVC=%v", inputPath, isHEVC)
	return isHEVC, nil
}

// ProbeRemoteDetails queries remote video headers via HTTP range requests and extracts video details.
func ProbeRemoteDetails(ctx context.Context, ffmpegPath string, assetURL string, apiKey string) (ProbeDetails, error) {
	headers := fmt.Sprintf("x-api-key: %s\r\n", apiKey)
	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-headers", headers,
		"-i", assetURL,
	)
	out, err := cmd.CombinedOutput()
	outStr := string(out)
	details := ParseVideoDetails(outStr)
	if details.Codec != "" {
		logger.Debug("Remote probe on %s: details=%+v", assetURL, details)
		return details, nil
	}
	if err != nil {
		logger.Debug("Remote probe on %s: failed (%v)", assetURL, err)
		return details, fmt.Errorf("remote probe failed: %w", err)
	}
	logger.Debug("Remote probe on %s: details=%+v", assetURL, details)
	return details, nil
}

// ProbeRemoteHEVC attempts a fast probe of a remote video via HTTP range request
// to determine if it is already HEVC without downloading the entire file.
func ProbeRemoteHEVC(ctx context.Context, ffmpegPath string, assetURL string, apiKey string) (bool, error) {
	details, err := ProbeRemoteDetails(ctx, ffmpegPath, assetURL, apiKey)
	return details.IsHEVC, err
}
