package engine

import (
	"context"
	"os"
	"path/filepath"
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
			expected: []string{"-y", "-i", "in.mp4", "-c:v", "libx265", "-tag:v", "hvc1", "-vf", "scale='trunc(iw/2)*2':'trunc(ih/2)*2'", "-c:a", "aac", "-b:a", "192k", "out.mp4"},
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
			expected: []string{"-y", "-i", "in.mp4", "-c:v", "hevc_videotoolbox", "-tag:v", "hvc1", "-allow_sw", "1", "-preset", "medium", "-q:v", "65", "-vf", "scale='trunc(iw/2)*2':'trunc(ih/2)*2'", "-c:a", "copy", "out.mp4"},
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
			expected: []string{"-y", "-i", "in.mp4", "-c:v", "libx265", "-tag:v", "hvc1", "-crf", "23", "-vf", "scale='trunc(iw/2)*2':'trunc(ih/2)*2'", "-c:a", "copy", "out.mp4"},
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
			expected: []string{"-y", "-i", "in.mp4", "-c:v", "hevc_videotoolbox", "-tag:v", "hvc1", "-allow_sw", "1", "-vf", "scale='if(gt(iw,ih),-2,trunc(min(iw,1080)/2)*2)':'if(gt(iw,ih),trunc(min(ih,1080)/2)*2,-2)'", "-c:a", "copy", "out.mp4"},
		},
		{
			name: "hevc_nvenc with CRF and Preset",
			opts: TranscodeOptions{
				Input:     "in.mp4",
				Output:    "out.mp4",
				Encoder:   "hevc_nvenc",
				AudioCopy: true,
				CRF:       28,
				Preset:    "medium",
			},
			expected: []string{"-y", "-i", "in.mp4", "-c:v", "hevc_nvenc", "-tag:v", "hvc1", "-preset", "medium", "-cq", "28", "-b:v", "0", "-vf", "scale='trunc(iw/2)*2':'trunc(ih/2)*2'", "-c:a", "copy", "out.mp4"},
		},
		{
			name: "hevc_vaapi with CRF",
			opts: TranscodeOptions{
				Input:     "in.mp4",
				Output:    "out.mp4",
				Encoder:   "hevc_vaapi",
				AudioCopy: true,
				CRF:       28,
			},
			expected: []string{"-vaapi_device", "/dev/dri/renderD128", "-y", "-i", "in.mp4", "-c:v", "hevc_vaapi", "-tag:v", "hvc1", "-qp", "28", "-vf", "scale='trunc(iw/2)*2':'trunc(ih/2)*2',format=nv12,hwupload", "-c:a", "copy", "out.mp4"},
		},
		{
			name: "No audio option passes -an",
			opts: TranscodeOptions{
				Input:   "in.mp4",
				Output:  "out.mp4",
				Encoder: "libx265",
				NoAudio: true,
				CRF:     26,
			},
			expected: []string{"-y", "-i", "in.mp4", "-c:v", "libx265", "-tag:v", "hvc1", "-crf", "26", "-vf", "scale='trunc(iw/2)*2':'trunc(ih/2)*2'", "-an", "out.mp4"},
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

func TestScanFFmpegOutput_KeyValueProgress(t *testing.T) {
	output := "frame=150\nfps=45.5\nout_time=00:00:05.000\nbitrate=2500kbits/s\nspeed=2.5x\nprogress=continue\n" +
		"frame=300\nfps=46.0\nout_time=00:00:10.000\nbitrate=2400kbits/s\nspeed=2.6x\nprogress=end\n"
	reader := strings.NewReader(output)

	var reports []Progress
	ScanFFmpegOutput(reader, func(p Progress) {
		reports = append(reports, p)
	})

	if len(reports) != 2 {
		t.Fatalf("expected 2 progress reports, got %d", len(reports))
	}
	if reports[0].Frame != 150 || reports[0].FPS != 45.5 || reports[0].Time != "00:00:05.000" || reports[0].Bitrate != "2500kbits/s" || reports[0].Speed != "2.5" {
		t.Errorf("unexpected first report: %+v", reports[0])
	}
	if reports[1].Frame != 300 || reports[1].FPS != 46.0 || reports[1].Time != "00:00:10.000" || reports[1].Bitrate != "2400kbits/s" || reports[1].Speed != "2.6" {
		t.Errorf("unexpected second report: %+v", reports[1])
	}
}

func TestBuildFFmpegArgs_VAAPI(t *testing.T) {
	opts := TranscodeOptions{
		Input:      "in.mp4",
		Output:     "out.mp4",
		Encoder:    "hevc_vaapi",
		AudioCopy:  true,
		CRF:        28,
		Preset:     "medium",
		Resolution: "1080p",
	}
	args := BuildFFmpegArgs(opts)
	if len(args) < 2 || args[0] != "-vaapi_device" {
		t.Errorf("expected -vaapi_device as first arg, got: %v", args)
	}
	for i, arg := range args {
		if arg == "-preset" {
			t.Errorf("hevc_vaapi should not receive -preset, found at index %d", i)
		}
		if arg == "-crf" {
			t.Errorf("hevc_vaapi should not receive -crf, found at index %d", i)
		}
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-qp 28") {
		t.Errorf("expected -qp 28 in args, got: %s", joined)
	}
	if !strings.Contains(joined, "scale='if(gt(iw,ih),-2,trunc(min(iw,1080)/2)*2)':'if(gt(iw,ih),trunc(min(ih,1080)/2)*2,-2)',format=nv12,hwupload") {
		t.Errorf("expected VAAPI filter with scale and hwupload, got: %s", joined)
	}
}

func TestBuildFFmpegArgs_NVENC(t *testing.T) {
	opts := TranscodeOptions{
		Input:      "in.mp4",
		Output:     "out.mp4",
		Encoder:    "hevc_nvenc",
		AudioCopy:  true,
		CRF:        28,
		Preset:     "medium",
		Resolution: "Original",
	}
	args := BuildFFmpegArgs(opts)
	for i, arg := range args {
		if arg == "-crf" {
			t.Errorf("hevc_nvenc should not receive -crf, found at index %d", i)
		}
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-cq 28") {
		t.Errorf("expected -cq 28 in args, got: %s", joined)
	}
}

func TestTranscode_HardwareFallbackToCPU(t *testing.T) {
	tempDir := t.TempDir()
	fakeFFmpeg := filepath.Join(tempDir, "fake_ffmpeg")

	// Script that fails if hevc_vaapi is passed, but succeeds if libx265 is passed
	script := `#!/bin/sh
for arg do
	if [ "$arg" = "hevc_vaapi" ]; then
		echo "Option preset not found / hevc_vaapi failed" >&2
		exit 1
	fi
	last_arg="$arg"
done
echo "libx265 success" > "$last_arg"
exit 0
`
	if err := os.WriteFile(fakeFFmpeg, []byte(script), 0755); err != nil {
		t.Fatalf("failed to create fake ffmpeg: %v", err)
	}

	inFile := filepath.Join(tempDir, "input.mp4")
	_ = os.WriteFile(inFile, []byte("input"), 0644)
	outFile := filepath.Join(tempDir, "output.mp4")

	opts := TranscodeOptions{
		Input:   inFile,
		Output:  outFile,
		Encoder: "hevc_vaapi",
	}

	err := Transcode(context.Background(), fakeFFmpeg, opts, nil)
	if err != nil {
		t.Fatalf("expected Transcode to fallback to libx265 and succeed, got error: %v", err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}
	if !strings.Contains(string(data), "libx265 success") {
		t.Errorf("expected fallback output from libx265, got: %s", string(data))
	}
}

func TestVideoToolboxQuality(t *testing.T) {
	tests := []struct {
		crf      int
		expected int
	}{
		{crf: 0, expected: 65},
		{crf: 28, expected: 65},
		{crf: 23, expected: 75},
		{crf: 20, expected: 81},
		{crf: 32, expected: 57},
		{crf: 70, expected: 70},   // already 1-100 scale
		{crf: 120, expected: 100}, // clamped to 100
	}

	for _, tt := range tests {
		got := VideoToolboxQuality(tt.crf)
		if got != tt.expected {
			t.Errorf("VideoToolboxQuality(%d) = %d, expected %d", tt.crf, got, tt.expected)
		}
	}
}

func TestBuildFFmpegArgs_VideoToolboxQualityOverride(t *testing.T) {
	opts := TranscodeOptions{
		Input:               "input.mp4",
		Output:              "output.mp4",
		Encoder:             "hevc_videotoolbox",
		AudioCopy:           true,
		CRF:                 26,
		VideoToolboxQuality: 66,
	}

	args := BuildFFmpegArgs(opts)
	foundQ := false
	for i, a := range args {
		if a == "-q:v" && i+1 < len(args) {
			foundQ = true
			if args[i+1] != "66" {
				t.Errorf("expected -q:v 66, got %s", args[i+1])
			}
		}
	}
	if !foundQ {
		t.Errorf("expected -q:v argument in FFmpeg args, got %v", args)
	}
}

func TestBuildFFmpegArgs_VideoToolboxQualityOnly_DoesNotEmitZeroCRFForOtherEncoders(t *testing.T) {
	encoders := []struct {
		encoder  string
		badFlags []string
	}{
		{"libx265", []string{"-crf"}},
		{"hevc_vaapi", []string{"-qp"}},
		{"hevc_nvenc", []string{"-cq"}},
	}

	for _, tt := range encoders {
		opts := TranscodeOptions{
			Input:               "input.mp4",
			Output:              "output.mp4",
			Encoder:             tt.encoder,
			VideoToolboxQuality: 66,
			CRF:                 0,
		}
		args := BuildFFmpegArgs(opts)
		for _, bad := range tt.badFlags {
			for _, a := range args {
				if a == bad {
					t.Errorf("encoder %s should not have emitted %s when CRF=0, got args: %v", tt.encoder, bad, args)
				}
			}
		}
	}

	// Verify VideoToolbox still gets -q:v 66 even when CRF=0
	vtOpts := TranscodeOptions{
		Input:               "input.mp4",
		Output:              "output.mp4",
		Encoder:             "hevc_videotoolbox",
		VideoToolboxQuality: 66,
		CRF:                 0,
	}
	vtArgs := BuildFFmpegArgs(vtOpts)
	foundQ := false
	for i, a := range vtArgs {
		if a == "-q:v" && i+1 < len(vtArgs) && vtArgs[i+1] == "66" {
			foundQ = true
			break
		}
	}
	if !foundQ {
		t.Errorf("expected VideoToolbox to have -q:v 66 when VideoToolboxQuality=66 and CRF=0, got %v", vtArgs)
	}
}



