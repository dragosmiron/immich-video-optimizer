package engine

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os/exec"
	"regexp"
	"strconv"
)

// TranscodeOptions holds settings for transcoding
type TranscodeOptions struct {
	Input     string
	Output    string
	Encoder   string // hevc_videotoolbox, hevc_nvenc, hevc_vaapi, libx265
	AudioCopy bool   // true for '-c:a copy', false for aac
	CRF       int    // CRF or bitrate equiv
	Preset    string // Preset string, e.g., "medium", "slow"
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

	if opts.Preset != "" {
		args = append(args, "-preset", opts.Preset)
	}

	if opts.CRF > 0 {
		if opts.Encoder == "hevc_videotoolbox" {
			args = append(args, "-q:v", strconv.Itoa(opts.CRF))
		} else {
			args = append(args, "-crf", strconv.Itoa(opts.CRF))
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

// ScanFFmpegOutput scans an io.Reader and calls the callback with progress info
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

	for scanner.Scan() {
		line := scanner.Text()
		if p, ok := ParseProgress(line); ok {
			cb(*p)
		}
	}
}

// Transcode executes ffmpeg to transcode a video.
func Transcode(ctx context.Context, ffmpegPath string, opts TranscodeOptions, onProgress ProgressCallback) error {
	args := BuildFFmpegArgs(opts)
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	if onProgress != nil {
		ScanFFmpegOutput(stderr, onProgress)
	}

	return cmd.Wait()
}
