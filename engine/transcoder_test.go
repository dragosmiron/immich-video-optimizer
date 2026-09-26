package engine

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildFFmpegArgs(t *testing.T) {
	tests := []struct {
		name     string
		opts     TranscodeOptions
		expected []string
	}{
		{
			name: "Basic libx265",
			opts: TranscodeOptions{
				Input:     "in.mp4",
				Output:    "out.mp4",
				Encoder:   "libx265",
				AudioCopy: false,
			},
			expected: []string{"-y", "-i", "in.mp4", "-c:v", "libx265", "-c:a", "aac", "-b:a", "192k", "out.mp4"},
		},
		{
			name: "Videotoolbox with CRF",
			opts: TranscodeOptions{
				Input:     "in.mp4",
				Output:    "out.mp4",
				Encoder:   "hevc_videotoolbox",
				AudioCopy: true,
				CRF:       28,
				Preset:    "medium",
			},
			expected: []string{"-y", "-i", "in.mp4", "-c:v", "hevc_videotoolbox", "-preset", "medium", "-q:v", "28", "-c:a", "copy", "out.mp4"},
		},
		{
			name: "libx265 with CRF",
			opts: TranscodeOptions{
				Input:     "in.mp4",
				Output:    "out.mp4",
				Encoder:   "libx265",
				AudioCopy: true,
				CRF:       23,
			},
			expected: []string{"-y", "-i", "in.mp4", "-c:v", "libx265", "-crf", "23", "-c:a", "copy", "out.mp4"},
		},
		{
			name: "With 1080p Resolution limit",
			opts: TranscodeOptions{
				Input:      "in.mp4",
				Output:     "out.mp4",
				Encoder:    "hevc_videotoolbox",
				AudioCopy:  true,
				Resolution: "1080p",
			},
			expected: []string{"-y", "-i", "in.mp4", "-c:v", "hevc_videotoolbox", "-vf", "scale=-2:'min(ih,1080)'", "-c:a", "copy", "out.mp4"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := BuildFFmpegArgs(tt.opts)
			if !reflect.DeepEqual(args, tt.expected) {
				t.Errorf("expected %v, got %v", tt.expected, args)
			}
		})
	}
}

func TestParseProgress(t *testing.T) {
	line := "frame=  123 fps= 30.5 q=28.0 size=N/A time=00:00:05.12 bitrate=123.4kbits/s speed=1.2x"
	p, ok := ParseProgress(line)
	if !ok {
		t.Fatalf("expected to parse progress")
	}

	if p.Frame != 123 {
		t.Errorf("expected frame 123, got %d", p.Frame)
	}
	if p.FPS != 30.5 {
		t.Errorf("expected fps 30.5, got %f", p.FPS)
	}
	if p.Time != "00:00:05.12" {
		t.Errorf("expected time 00:00:05.12, got %s", p.Time)
	}
	if p.Bitrate != "123.4kbits/s" {
		t.Errorf("expected bitrate 123.4kbits/s, got %s", p.Bitrate)
	}
	if p.Speed != "1.2" {
		t.Errorf("expected speed 1.2, got %s", p.Speed)
	}
}

func TestScanFFmpegOutput(t *testing.T) {
	output := "frame=  100 fps= 30\rframe=  200 fps= 30\nframe=  300 fps= 30\r"
	reader := strings.NewReader(output)

	var frames []int
	ScanFFmpegOutput(reader, func(p Progress) {
		frames = append(frames, p.Frame)
	})

	expected := []int{100, 200, 300}
	if !reflect.DeepEqual(frames, expected) {
		t.Errorf("expected frames %v, got %v", expected, frames)
	}
}
