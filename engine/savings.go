package engine

import (
	"math"
	"strconv"
	"strings"
)

// ParseResolutionHeight extracts vertical pixel height from a resolution label.
func ParseResolutionHeight(res string) int {
	res = strings.ToLower(strings.TrimSpace(res))
	switch {
	case strings.Contains(res, "4k") || strings.Contains(res, "2160"):
		return 2160
	case strings.Contains(res, "1440"):
		return 1440
	case strings.Contains(res, "1080"):
		return 1080
	case strings.Contains(res, "720"):
		return 720
	case strings.Contains(res, "480"):
		return 480
	}
	if strings.Contains(res, "x") {
		parts := strings.Split(res, "x")
		if len(parts) == 2 {
			if h, err := strconv.Atoi(parts[1]); err == nil {
				return h
			}
		}
	}
	return 0
}

// ResolveTargetResolution computes the actual target resolution string and whether
// genuine downscaling will occur given the input video resolution and user setting.
func ResolveTargetResolution(inputRes, targetSetting string) (string, bool) {
	if inputRes == "" {
		inputRes = "1080p"
	}
	if targetSetting == "" || strings.EqualFold(targetSetting, "original") {
		return inputRes, false
	}
	inH := ParseResolutionHeight(inputRes)
	outH := ParseResolutionHeight(targetSetting)
	if inH > 0 && outH > 0 && inH > outH {
		return targetSetting, true
	}
	// Never upscales; if input height is <= target height, preserve input resolution
	return inputRes, false
}

// EstimateSavings calculates a conservative, realistic ratio and saved byte count
// for a video asset based on probed codec, resolution, and target settings.
func EstimateSavings(orig int64, detail ProbeDetails, targetResSetting string, isProbeFailed bool) (float64, int64) {
	if isProbeFailed || detail.Codec == "" {
		return 1.0, 0
	}

	_, isDownscaling := ResolveTargetResolution(detail.Resolution, targetResSetting)

	var ratio float64
	switch {
	case detail.IsHEVC || detail.Codec == "HEVC" || detail.Codec == "H.265":
		if isDownscaling {
			ratio = 0.50
		} else {
			return 1.0, 0
		}
	case detail.Codec == "ProRes":
		if isDownscaling {
			ratio = 0.20
		} else {
			ratio = 0.30
		}
	case detail.Codec == "MJPEG":
		ratio = 0.40
	case detail.Codec == "MPEG-4" || detail.Codec == "VP8":
		if isDownscaling {
			ratio = 0.50
		} else {
			ratio = 0.70
		}
	case detail.Codec == "VP9" || detail.Codec == "AV1":
		ratio = 1.0
	case detail.Codec == "H.264":
		if isDownscaling {
			ratio = 0.50
		} else {
			ratio = 0.85
		}
	default:
		if isDownscaling {
			ratio = 0.60
		} else {
			ratio = 0.88
		}
	}

	saved := int64(math.Round(float64(orig) * (1.0 - ratio)))
	if saved < 0 {
		saved = 0
	}
	return ratio, saved
}
