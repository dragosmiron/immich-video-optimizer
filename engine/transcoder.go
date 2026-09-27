package engine

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// TranscodeOptions holds settings for transcoding
type TranscodeOptions struct {
	Input      string
	Output     string
	Encoder    string // hevc_videotoolbox, hevc_nvenc, hevc_vaapi, libx265
	AudioCopy  bool   // true for '-c:a copy', false for aac
	CRF        int    // CRF or bitrate equiv
	Preset     string // Preset string, e.g., "medium", "slow"
	Resolution string
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
	args := []string{
		"-y", // Overwrite output files without asking
		"-i", opts.Input,
		"-c:v", opts.Encoder,
	}

	if opts.Encoder == "hevc_videotoolbox" {
		args = append(args, "-tag:v", "hvc1", "-allow_sw", "1")
	}

	if opts.Preset != "" {
		args = append(args, "-preset", strings.ToLower(opts.Preset))
	}

	if opts.CRF > 0 {
		if opts.Encoder == "hevc_videotoolbox" {
			args = append(args, "-q:v", strconv.Itoa(opts.CRF))
		} else {
			args = append(args, "-crf", strconv.Itoa(opts.CRF))
		}
	}

	if opts.Resolution != "" && opts.Resolution != "Original" {
		var scale string
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
		if scale != "" {
			args = append(args, "-vf", "scale="+scale)
		}
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
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	ringBuf := NewRingBuffer(8 * 1024)
	cmd.Stderr = ringBuf

	if err := cmd.Start(); err != nil {
		return err
	}

	if onProgress != nil {
		ScanFFmpegOutput(stdout, onProgress)
	} else {
		_, _ = io.Copy(io.Discard, stdout)
	}

	if err := cmd.Wait(); err != nil {
		stderrOutput := strings.TrimSpace(ringBuf.String())
		if stderrOutput != "" {
			return fmt.Errorf("transcode failed (%w): %s", err, stderrOutput)
		}
		return err
	}

	return nil
}
