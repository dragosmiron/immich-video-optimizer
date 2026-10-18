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
	NoAudio             bool   // true if input video has no audio stream
	CRF                 int    // CRF or bitrate equiv
	VideoToolboxQuality int    // Explicit VideoToolbox -q:v quality override (1..100)
	Preset              string // Preset string, e.g., "medium", "slow"
	Resolution          string
	CustomFlags         string                          // Custom user-specified FFmpeg arguments or overrides
	OnFallback          func(failedEncoder string, err error) // Optional callback triggered when hardware encoder fails and falls back to CPU
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
		"-tag:v", "hvc1",
	)

	if opts.Encoder == "hevc_videotoolbox" {
		args = append(args, "-allow_sw", "1")
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
			scale = "'if(gt(iw,ih),-2,trunc(min(iw,2160)/2)*2)':'if(gt(iw,ih),trunc(min(ih,2160)/2)*2,-2)'"
		case "1440p":
			scale = "'if(gt(iw,ih),-2,trunc(min(iw,1440)/2)*2)':'if(gt(iw,ih),trunc(min(ih,1440)/2)*2,-2)'"
		case "1080p":
			scale = "'if(gt(iw,ih),-2,trunc(min(iw,1080)/2)*2)':'if(gt(iw,ih),trunc(min(ih,1080)/2)*2,-2)'"
		case "720p":
			scale = "'if(gt(iw,ih),-2,trunc(min(iw,720)/2)*2)':'if(gt(iw,ih),trunc(min(ih,720)/2)*2,-2)'"
		case "480p":
			scale = "'if(gt(iw,ih),-2,trunc(min(iw,480)/2)*2)':'if(gt(iw,ih),trunc(min(ih,480)/2)*2,-2)'"
		default:
			scale = "'trunc(iw/2)*2':'trunc(ih/2)*2'"
		}
	} else {
		// Even for "Original" or unspecified resolution, guard against odd dimensions
		// which break HEVC encoders with YUV420p chroma subsampling.
		scale = "'trunc(iw/2)*2':'trunc(ih/2)*2'"
	}

	if opts.Encoder == "hevc_vaapi" {
		args = append(args, "-vf", fmt.Sprintf("scale=%s,format=nv12,hwupload", scale))
	} else if scale != "" {
		args = append(args, "-vf", "scale="+scale)
	}

	if opts.NoAudio {
		args = append(args, "-an")
	} else if opts.AudioCopy {
		args = append(args, "-c:a", "copy")
	} else {
		args = append(args, "-c:a", "aac", "-b:a", "192k")
	}

	args = append(args, opts.Output)

	if strings.TrimSpace(opts.CustomFlags) != "" {
		res, err := ParseAndMergeCustomArgs(args, opts.CustomFlags)
		if err != nil {
			logger.Error("Failed to parse custom FFmpeg flags %q: %v. Using defaults.", opts.CustomFlags, err)
			return args
		}
		return res.Merged
	}

	return args
}

// SplitShellArgs tokenizes a command-line string respecting single quotes, double quotes, and escapes.
func SplitShellArgs(raw string) ([]string, error) {
	var args []string
	var current strings.Builder
	inQuote := false
	quoteChar := rune(0)
	escaped := false

	for _, r := range raw {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}

		if r == '\\' && quoteChar != '\'' {
			escaped = true
			continue
		}

		if inQuote {
			if r == quoteChar {
				inQuote = false
				quoteChar = 0
			} else {
				current.WriteRune(r)
			}
			continue
		}

		switch r {
		case '"', '\'':
			inQuote = true
			quoteChar = r
		case ' ', '\t', '\n', '\r':
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}

	if inQuote {
		return nil, fmt.Errorf("unclosed quote (%c)", quoteChar)
	}
	if escaped {
		current.WriteRune('\\')
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}

	var splitTokens []string
	for _, tok := range args {
		if strings.HasPrefix(tok, "-") && strings.Contains(tok, "=") {
			eqIdx := strings.Index(tok, "=")
			flag := tok[:eqIdx]
			val := tok[eqIdx+1:]
			splitTokens = append(splitTokens, flag)
			if val != "" {
				splitTokens = append(splitTokens, val)
			}
		} else {
			splitTokens = append(splitTokens, tok)
		}
	}

	return splitTokens, nil
}

// MergeResult holds the result of merging base args with custom flags.
type MergeResult struct {
	Merged           []string
	IsCustom         []bool
	IgnoredProtected []string
}

type customArgItem struct {
	flag   string
	val    string
	hasVal bool
	isUsed bool
}

var encoderQualityFlags = map[string][]string{
	"libx265":           {"-crf"},
	"hevc_videotoolbox": {"-q:v"},
	"hevc_vaapi":        {"-qp"},
	"hevc_nvenc":        {"-cq", "-b:v"},
}

// MergeFFmpegArgs merges baseArgs with customArgs, stripping protected flags and replacing base flags.
func MergeFFmpegArgs(baseArgs []string, customArgs []string) MergeResult {
	var ignoredProtected []string
	var items []*customArgItem

	isProtectedFlag := func(f string) bool {
		switch f {
		case "-y", "-n", "-nostats", "-progress", "-i":
			return true
		default:
			return strings.HasPrefix(f, "-tag")
		}
	}

	// 1. Parse custom args into items, collecting protected ones
	for i := 0; i < len(customArgs); i++ {
		token := customArgs[i]
		if !strings.HasPrefix(token, "-") {
			ignoredProtected = append(ignoredProtected, token)
			continue
		}

		if isProtectedFlag(token) {
			ignoredProtected = append(ignoredProtected, token)
			if token == "-progress" || token == "-i" || strings.HasPrefix(token, "-tag") {
				if i+1 < len(customArgs) && !strings.HasPrefix(customArgs[i+1], "-") {
					i++
				}
			}
			continue
		}

		item := &customArgItem{flag: token}
		if i+1 < len(customArgs) && !strings.HasPrefix(customArgs[i+1], "-") {
			item.val = customArgs[i+1]
			item.hasVal = true
			i++
		}
		items = append(items, item)
	}

	// Index custom flags by flag name for quick lookup
	customByFlag := make(map[string]*customArgItem)
	for _, it := range items {
		customByFlag[it.flag] = it
	}

	// Quality overrides: -crf, -q:v, -qp, -cq, -b:v
	isQualityFlag := func(f string) bool {
		return f == "-crf" || f == "-q:v" || f == "-qp" || f == "-cq" || f == "-b:v"
	}
	var customQuality *customArgItem
	for _, it := range items {
		if isQualityFlag(it.flag) {
			customQuality = it
			break
		}
	}

	// Audio overrides: -an, -c:a, -b:a
	var customAn *customArgItem
	var customCa *customArgItem
	var customBa *customArgItem
	for _, it := range items {
		if it.flag == "-an" {
			customAn = it
		} else if it.flag == "-c:a" {
			customCa = it
		} else if it.flag == "-b:a" {
			customBa = it
		}
	}

	// Determine effective video encoder
	customCv := customByFlag["-c:v"]
	effectiveEncoder := ""
	if customCv != nil && customCv.hasVal {
		effectiveEncoder = customCv.val
	}

	// 2. Process baseArgs
	var merged []string
	var isCustom []bool

	appendMerged := func(token string, custom bool) {
		merged = append(merged, token)
		isCustom = append(isCustom, custom)
	}

	isQualityCompatible := func(enc, flag string) bool {
		compatibleFlags, ok := encoderQualityFlags[enc]
		if !ok {
			return true
		}
		for _, f := range compatibleFlags {
			if f == flag {
				return true
			}
		}
		return false
	}

	outputPath := ""
	if len(baseArgs) > 0 {
		last := baseArgs[len(baseArgs)-1]
		isOutput := !strings.HasPrefix(last, "-")
		if isOutput && len(baseArgs) >= 2 {
			prev := baseArgs[len(baseArgs)-2]
			if strings.HasPrefix(prev, "-") && prev != "-y" && prev != "-n" && prev != "-nostats" && prev != "-an" {
				isOutput = false
			}
		}
		if isOutput {
			outputPath = last
			baseArgs = baseArgs[:len(baseArgs)-1]
		}
	}

	for i := 0; i < len(baseArgs); i++ {
		token := baseArgs[i]

		// Protected base arguments: -vaapi_device, -y, -i <input>, -tag:v hvc1
		if token == "-vaapi_device" {
			appendMerged(token, false)
			if i+1 < len(baseArgs) {
				i++
				appendMerged(baseArgs[i], false)
			}
			continue
		}
		if token == "-y" {
			appendMerged(token, false)
			continue
		}
		if token == "-i" {
			appendMerged(token, false)
			if i+1 < len(baseArgs) {
				i++
				appendMerged(baseArgs[i], false)
			}
			continue
		}
		if token == "-tag:v" {
			appendMerged(token, false)
			if i+1 < len(baseArgs) {
				i++
				appendMerged(baseArgs[i], false)
			}
			continue
		}

		// Video encoder: -c:v <encoder>
		if token == "-c:v" {
			if customCv != nil {
				appendMerged("-c:v", true)
				if customCv.hasVal {
					appendMerged(customCv.val, true)
				}
				customCv.isUsed = true
			} else {
				appendMerged("-c:v", false)
				if i+1 < len(baseArgs) && !strings.HasPrefix(baseArgs[i+1], "-") {
					effectiveEncoder = baseArgs[i+1]
					appendMerged(baseArgs[i+1], false)
				}
			}
			if i+1 < len(baseArgs) && !strings.HasPrefix(baseArgs[i+1], "-") {
				i++
			}
			continue
		}

		// -allow_sw 1 (VideoToolbox software fallback)
		if token == "-allow_sw" {
			val := ""
			if i+1 < len(baseArgs) && !strings.HasPrefix(baseArgs[i+1], "-") {
				i++
				val = baseArgs[i]
			}
			if effectiveEncoder == "hevc_videotoolbox" || (effectiveEncoder == "" && customCv == nil) {
				appendMerged("-allow_sw", false)
				if val != "" {
					appendMerged(val, false)
				}
			}
			continue
		}

		// Preset: -preset <val>
		if token == "-preset" {
			val := ""
			if i+1 < len(baseArgs) && !strings.HasPrefix(baseArgs[i+1], "-") {
				i++
				val = baseArgs[i]
			}
			if it, ok := customByFlag["-preset"]; ok {
				appendMerged("-preset", true)
				if it.hasVal {
					appendMerged(it.val, true)
				}
				it.isUsed = true
			} else {
				appendMerged("-preset", false)
				if val != "" {
					appendMerged(val, false)
				}
			}
			continue
		}

		// Quality flags: -crf, -q:v, -qp, -cq, -b:v
		if isQualityFlag(token) {
			val := ""
			if i+1 < len(baseArgs) && !strings.HasPrefix(baseArgs[i+1], "-") {
				i++
				val = baseArgs[i]
			}
			if token == "-b:v" && customQuality != nil {
				continue
			}

			if customQuality != nil {
				if !customQuality.isUsed {
					appendMerged(customQuality.flag, true)
					if customQuality.hasVal {
						appendMerged(customQuality.val, true)
					}
					customQuality.isUsed = true
				}
			} else {
				if customCv != nil && !isQualityCompatible(effectiveEncoder, token) {
					continue
				}
				appendMerged(token, false)
				if val != "" {
					appendMerged(val, false)
				}
			}
			continue
		}

		// Filter: -vf <val>
		if token == "-vf" {
			val := ""
			if i+1 < len(baseArgs) && !strings.HasPrefix(baseArgs[i+1], "-") {
				i++
				val = baseArgs[i]
			}
			if it, ok := customByFlag["-vf"]; ok {
				appendMerged("-vf", true)
				if it.hasVal {
					customVal := it.val
					if (effectiveEncoder == "hevc_vaapi" || strings.Contains(val, "hwupload")) && !strings.Contains(customVal, "hwupload") {
						if !strings.Contains(customVal, "format=nv12") {
							customVal += ",format=nv12"
						}
						customVal += ",hwupload"
					}
					appendMerged(customVal, true)
				}
				it.isUsed = true
			} else {
				appendMerged("-vf", false)
				if val != "" {
					appendMerged(val, false)
				}
			}
			continue
		}

		// Audio: -an or -c:a <codec> [-b:a <rate>]
		if token == "-an" {
			if customAn != nil {
				appendMerged("-an", true)
				customAn.isUsed = true
			} else if customCa != nil {
				appendMerged("-c:a", true)
				if customCa.hasVal {
					appendMerged(customCa.val, true)
				}
				customCa.isUsed = true
				if customBa != nil {
					appendMerged("-b:a", true)
					if customBa.hasVal {
						appendMerged(customBa.val, true)
					}
					customBa.isUsed = true
				}
			} else {
				appendMerged("-an", false)
			}
			continue
		}

		if token == "-c:a" {
			val := ""
			if i+1 < len(baseArgs) && !strings.HasPrefix(baseArgs[i+1], "-") {
				i++
				val = baseArgs[i]
			}
			if customAn != nil {
				appendMerged("-an", true)
				customAn.isUsed = true
			} else if customCa != nil {
				appendMerged("-c:a", true)
				if customCa.hasVal {
					appendMerged(customCa.val, true)
				}
				customCa.isUsed = true
			} else {
				appendMerged("-c:a", false)
				if val != "" {
					appendMerged(val, false)
				}
			}
			continue
		}

		if token == "-b:a" {
			val := ""
			if i+1 < len(baseArgs) && !strings.HasPrefix(baseArgs[i+1], "-") {
				i++
				val = baseArgs[i]
			}
			if customAn != nil {
				continue
			}
			if customBa != nil {
				appendMerged("-b:a", true)
				if customBa.hasVal {
					appendMerged(customBa.val, true)
				}
				customBa.isUsed = true
			} else if customCa != nil && customCa.val == "copy" {
				continue
			} else {
				appendMerged("-b:a", false)
				if val != "" {
					appendMerged(val, false)
				}
			}
			continue
		}

		// Generic flag in base: if custom overrides it, use custom
		if it, ok := customByFlag[token]; ok {
			appendMerged(it.flag, true)
			if it.hasVal {
				appendMerged(it.val, true)
			}
			it.isUsed = true
			if i+1 < len(baseArgs) && !strings.HasPrefix(baseArgs[i+1], "-") {
				i++
			}
			continue
		}

		// Pass through base token
		appendMerged(token, false)
	}

	// 3. Append remaining unused custom items before output
	for _, it := range items {
		if !it.isUsed {
			appendMerged(it.flag, true)
			if it.hasVal {
				appendMerged(it.val, true)
			}
			it.isUsed = true
		}
	}

	// 4. Output path at the end
	if outputPath != "" {
		appendMerged(outputPath, false)
	}

	return MergeResult{
		Merged:           merged,
		IsCustom:         isCustom,
		IgnoredProtected: ignoredProtected,
	}
}

// ParseAndMergeCustomArgs tokenizes customFlags and merges them into baseArgs.
func ParseAndMergeCustomArgs(baseArgs []string, customFlags string) (MergeResult, error) {
	trimmed := strings.TrimSpace(customFlags)
	if trimmed == "" {
		isCustom := make([]bool, len(baseArgs))
		return MergeResult{Merged: baseArgs, IsCustom: isCustom}, nil
	}
	customTokens, err := SplitShellArgs(trimmed)
	if err != nil {
		isCustom := make([]bool, len(baseArgs))
		return MergeResult{Merged: baseArgs, IsCustom: isCustom}, err
	}
	res := MergeFFmpegArgs(baseArgs, customTokens)
	return res, nil
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
			if opts.OnFallback != nil {
				opts.OnFallback(opts.Encoder, err)
			}
			logger.Error("Hardware transcode with %s failed for %s (%v); falling back to CPU encoder libx265", opts.Encoder, opts.Input, err)
			_ = os.Remove(opts.Output)
			fallbackOpts := opts
			fallbackOpts.Encoder = "libx265"
			fallbackOpts.CustomFlags = ""
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

