package engine

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"immich-video-optimizer/logger"
)

// TranscodeOptions holds settings for transcoding
type TranscodeOptions struct {
	Input               string
	Output              string
	Encoder             string // hevc_videotoolbox, hevc_nvenc, hevc_vaapi, libx265
	AudioCopy           bool   // true for '-c:a copy', false for aac
	CRF                 int    // CRF or bitrate equiv
	VideoToolboxQuality int    // Explicit VideoToolbox -q:v quality override (1..100)
	Preset              string // Preset string, e.g., "medium", "slow"
	Resolution          string
}

// Progress represents the transcoding progress parsed from ffmpeg output
type Progress struct {
	Frame   int
	FPS     float64
	Time    string
	Bitrate string
	Speed   string
}

type ProgressCallback func(p Progress)

// BuildFFmpegArgs constructs the command-line arguments for ffmpeg
func BuildFFmpegArgs(opts TranscodeOptions) []string {
	var args []string

	if opts.Encoder == "hevc_vaapi" {
		vaapiDev := "/dev/dri/renderD128"
		if _, err := os.Stat(vaapiDev); os.IsNotExist(err) {
			if _, err := os.Stat("/dev/dri/card0"); err == nil {
				vaapiDev = "/dev/dri/card0"
			}
		}
		args = append(args, "-vaapi_device", vaapiDev)
	}

	args = append(args,
		"-y", // Overwrite output files without asking
		"-i", opts.Input,
		"-c:v", opts.Encoder,
	)

	if opts.Encoder == "hevc_videotoolbox" {
		args = append(args, "-tag:v", "hvc1", "-allow_sw", "1")
	}

	if opts.Preset != "" && opts.Encoder != "hevc_vaapi" {
		args = append(args, "-preset", strings.ToLower(opts.Preset))
	}

	switch opts.Encoder {
	case "hevc_videotoolbox":
		vtQuality := opts.VideoToolboxQuality
		if vtQuality <= 0 && opts.CRF > 0 {
			vtQuality = VideoToolboxQuality(opts.CRF)
		}
		if vtQuality > 0 {
			args = append(args, "-q:v", strconv.Itoa(vtQuality))
		}
	case "hevc_vaapi":
		if opts.CRF > 0 {
			args = append(args, "-qp", strconv.Itoa(opts.CRF))
		}
	case "hevc_nvenc":
		if opts.CRF > 0 {
			args = append(args, "-cq", strconv.Itoa(opts.CRF), "-b:v", "0")
		}
	default:
		if opts.CRF > 0 {
			args = append(args, "-crf", strconv.Itoa(opts.CRF))
		}
	}

	var scale string
	if opts.Resolution != "" && opts.Resolution != "Original" {
		switch opts.Resolution {
		case "4K":
			scale = "-2:'min(ih,2160)'"
		case "1440p":
			scale = "-2:'min(ih,1440)'"
		case "1080p":
			scale = "-2:'min(ih,1080)'"
		case "720p":
			scale = "-2:'min(ih,720)'"
		}
	}

	if opts.Encoder == "hevc_vaapi" {
		if scale != "" {
			args = append(args, "-vf", fmt.Sprintf("scale=%s,format=nv12,hwupload", scale))
		} else {
			args = append(args, "-vf", "format=nv12,hwupload")
		}
	} else if scale != "" {
		args = append(args, "-vf", "scale="+scale)
	}

	if opts.AudioCopy {
		args = append(args, "-c:a", "copy")
	} else {
		args = append(args, "-c:a", "aac", "-b:a", "192k")
	}

	args = append(args, opts.Output)
	return args
}

var (
	frameRe   = regexp.MustCompile(`frame=\s*(\d+)`)
	fpsRe     = regexp.MustCompile(`fps=\s*([\d\.]+)`)
	timeRe    = regexp.MustCompile(`time=([\d:\.]+)`)
	bitrateRe = regexp.MustCompile(`bitrate=\s*([^ ]+)`)
	speedRe   = regexp.MustCompile(`speed=\s*([\d\.]+)x`)
)

// ParseProgress parses a standard ffmpeg stderr output string
func ParseProgress(line string) (*Progress, bool) {
	var p Progress
	matched := false

	if m := frameRe.FindStringSubmatch(line); len(m) > 1 {
		p.Frame, _ = strconv.Atoi(m[1])
		matched = true
	}
	if m := fpsRe.FindStringSubmatch(line); len(m) > 1 {
		p.FPS, _ = strconv.ParseFloat(m[1], 64)
		matched = true
	}
	if m := timeRe.FindStringSubmatch(line); len(m) > 1 {
		p.Time = m[1]
		matched = true
	}
	if m := bitrateRe.FindStringSubmatch(line); len(m) > 1 {
		p.Bitrate = m[1]
		matched = true
	}
	if m := speedRe.FindStringSubmatch(line); len(m) > 1 {
		p.Speed = m[1]
		matched = true
	}

	if matched {
		return &p, true
	}
	return nil, false
}

// ScanFFmpegOutput scans an io.Reader and calls the callback with progress info.
// It parses deterministic key=value streams from FFmpeg's -progress pipe:1
// while remaining backward-compatible with legacy carriage-return delimited stderr lines.
func ScanFFmpegOutput(reader io.Reader, cb ProgressCallback) {
	scanner := bufio.NewScanner(reader)
	scanner.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
			return i + 1, data[0:i], nil
		}
		if atEOF {
			return len(data), data, nil
		}
		return 0, nil, nil
	})

	var current Progress
	hasData := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Fast deterministic key=value line parsing from -progress pipe:1
		if k, v, ok := strings.Cut(line, "="); ok && !strings.Contains(k, " ") && !strings.Contains(v, "=") {
			k = strings.TrimSpace(k)
			v = strings.TrimSpace(v)
			switch k {
			case "frame":
				if n, err := strconv.Atoi(v); err == nil {
					current.Frame = n
					hasData = true
				}
			case "fps":
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					current.FPS = f
					hasData = true
				}
			case "out_time", "time":
				current.Time = v
				hasData = true
			case "bitrate":
				current.Bitrate = v
				hasData = true
			case "speed":
				current.Speed = strings.TrimSuffix(v, "x")
				hasData = true
			case "progress":
				if hasData && cb != nil {
					cb(current)
				}
			}
			continue
		}

		if p, ok := ParseProgress(line); ok {
			if cb != nil {
				cb(*p)
			}
		}
	}
}

// Transcode executes ffmpeg to transcode a video.
func Transcode(ctx context.Context, ffmpegPath string, opts TranscodeOptions, onProgress ProgressCallback) error {
	args := []string{"-nostats", "-progress", "pipe:1"}
	args = append(args, BuildFFmpegArgs(opts)...)
	logger.Debug("FFmpeg executing: %s %s", ffmpegPath, strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	ringBuf := NewRingBuffer(8 * 1024)
	cmd.Stderr = ringBuf

	if err := cmd.Start(); err != nil {
		logger.Error("FFmpeg failed to start for %s: %v", opts.Input, err)
		return err
	}

	if onProgress != nil {
		ScanFFmpegOutput(stdout, onProgress)
	} else {
		_, _ = io.Copy(io.Discard, stdout)
	}

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		stderrOutput := strings.TrimSpace(ringBuf.String())
		if opts.Encoder != "libx265" {
			logger.Info("Hardware transcode with %s failed for %s (%v); falling back to CPU encoder libx265", opts.Encoder, opts.Input, err)
			_ = os.Remove(opts.Output)
			fallbackOpts := opts
			fallbackOpts.Encoder = "libx265"
			return Transcode(ctx, ffmpegPath, fallbackOpts, onProgress)
		}
		if stderrOutput != "" {
			logger.Error("FFmpeg transcode failed for %s (%v)\n[Command]: %s %s\n[Stderr]:\n%s", opts.Input, err, ffmpegPath, strings.Join(args, " "), stderrOutput)
			return fmt.Errorf("transcode failed (%w): %s", err, stderrOutput)
		}
		logger.Error("FFmpeg transcode failed for %s (%v)\n[Command]: %s %s", opts.Input, err, ffmpegPath, strings.Join(args, " "))
		return err
	}

	return nil
}

// VideoToolboxQuality maps a standard CRF (0..51) to VideoToolbox's -q:v scale (1..100).
// In VideoToolbox, 1 is the worst quality / lowest bitrate, while 100 is max quality.
// A CRF of 28 corresponds to roughly 65 on VideoToolbox to preserve visual fidelity.
func VideoToolboxQuality(crf int) int {
	if crf <= 0 {
		return 65
	}
	// If the value is already on VideoToolbox's 1-100 scale (e.g. > 51), clamp to 100.
	if crf > 51 {
		if crf > 100 {
			return 100
		}
		return crf
	}
	// Linear mapping around CRF 28 -> quality 65
	// CRF 20 -> 81, CRF 24 -> 73, CRF 28 -> 65, CRF 32 -> 57
	q := 65 + (28-crf)*2
	if q < 30 {
		q = 30
	}
	if q > 100 {
		q = 100
	}
	return q
}

// QualityLevelToCRF maps quality levels 1-10 to CRF values.
// Range: 1 (CRF 32) to 10 (CRF 18). Default (6): CRF 26.
func QualityLevelToCRF(level int) int {
	if level == 0 {
		level = 6
	} else if level < 1 {
		level = 1
	} else if level > 10 {
		level = 10
	}
	switch level {
	case 1:
		return 32
	case 2:
		return 31
	case 3:
		return 30
	case 4:
		return 29
	case 5:
		return 28
	case 6:
		return 26
	case 7:
		return 24
	case 8:
		return 22
	case 9:
		return 20
	case 10:
		return 18
	default:
		return 26
	}
}

// QualityLevelToVideoToolbox maps quality levels 1-10 to VideoToolbox -q:v values (1-100).
// Range: 1 (VT 50) to 10 (VT 85). Default (6): VT 66.
func QualityLevelToVideoToolbox(level int) int {
	if level == 0 {
		level = 6
	} else if level < 1 {
		level = 1
	} else if level > 10 {
		level = 10
	}
	switch level {
	case 1:
		return 50
	case 2:
		return 53
	case 3:
		return 56
	case 4:
		return 59
	case 5:
		return 62
	case 6:
		return 66
	case 7:
		return 72
	case 8:
		return 76
	case 9:
		return 80
	case 10:
		return 85
	default:
		return 66
	}
}
