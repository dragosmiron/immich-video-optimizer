package engine

import (
	"testing"
)

func TestParseResolutionHeight(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"4K", 2160},
		{"3840x2160", 2160},
		{"1440p", 1440},
		{"2560x1440", 1440},
		{"1080p", 1080},
		{"1920x1080", 1080},
		{"720p", 720},
		{"1280x720", 720},
		{"480p", 480},
		{"640x480", 480},
		{"unknown", 0},
		{"", 0},
	}

	for _, tt := range tests {
		got := ParseResolutionHeight(tt.input)
		if got != tt.expected {
			t.Errorf("ParseResolutionHeight(%q) = %d; want %d", tt.input, got, tt.expected)
		}
	}
}

func TestResolveTargetResolution(t *testing.T) {
	// Downscaling from 4K to 1080p
	res, downscale := ResolveTargetResolution("4K", "1080p")
	if res != "1080p" || !downscale {
		t.Errorf("ResolveTargetResolution(4K, 1080p) = (%q, %v); want (1080p, true)", res, downscale)
	}

	// No upscaling from 720p to 1080p
	res, downscale = ResolveTargetResolution("720p", "1080p")
	if res != "720p" || downscale {
		t.Errorf("ResolveTargetResolution(720p, 1080p) = (%q, %v); want (720p, false)", res, downscale)
	}

	// Original setting preserves input
	res, downscale = ResolveTargetResolution("4K", "Original")
	if res != "4K" || downscale {
		t.Errorf("ResolveTargetResolution(4K, Original) = (%q, %v); want (4K, false)", res, downscale)
	}
}

func TestEstimateSavings(t *testing.T) {
	origSize := int64(100 * 1024 * 1024) // 100 MB

	// Already HEVC without downscaling -> 0 savings
	ratio, saved := EstimateSavings(origSize, ProbeDetails{Codec: "HEVC", IsHEVC: true, Resolution: "1080p"}, "Original", false)
	if ratio != 1.0 || saved != 0 {
		t.Errorf("EstimateSavings(HEVC, Original) = (%v, %d); want (1.0, 0)", ratio, saved)
	}

	// H.264 without downscaling -> ~15% savings
	ratio, saved = EstimateSavings(origSize, ProbeDetails{Codec: "H.264", Resolution: "1080p"}, "Original", false)
	if ratio != 0.85 || saved != int64(15*1024*1024) {
		t.Errorf("EstimateSavings(H.264, Original) = (%v, %d); want (0.85, %d)", ratio, saved, 15*1024*1024)
	}

	// ProRes without downscaling -> ~70% savings
	ratio, saved = EstimateSavings(origSize, ProbeDetails{Codec: "ProRes", Resolution: "1080p"}, "Original", false)
	if ratio != 0.30 || saved != int64(70*1024*1024) {
		t.Errorf("EstimateSavings(ProRes, Original) = (%v, %d); want (0.30, %d)", ratio, saved, 70*1024*1024)
	}

	// Probe failed -> 0 savings
	ratio, saved = EstimateSavings(origSize, ProbeDetails{}, "Original", true)
	if ratio != 1.0 || saved != 0 {
		t.Errorf("EstimateSavings(failed) = (%v, %d); want (1.0, 0)", ratio, saved)
	}
}
