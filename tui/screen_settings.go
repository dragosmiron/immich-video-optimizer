package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"immich-video-optimizer/config"
	"immich-video-optimizer/engine"
	"immich-video-optimizer/logger"
	"immich-video-optimizer/tui/styles"
)

func (m *AppModel) viewSettings() string {
	b := strings.Builder{}
	b.WriteString(styles.Title.Render("⚙ Settings & Preferences"))
	b.WriteString("\n\n")

	// Tab headers
	tabNames := []string{"1. Encoding", "2. Filtering", "3. Pipeline & Network", "4. Advanced"}
	activeTabStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color("#2563EB")).
		Padding(0, 2)

	inactiveTabStyle := lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#64748B", Dark: "#94A3B8"}).
		Background(lipgloss.AdaptiveColor{Light: "#E2E8F0", Dark: "#1E293B"}).
		Padding(0, 2)

	var renderedTabs []string
	for i, name := range tabNames {
		if i == m.settingsTab {
			renderedTabs = append(renderedTabs, activeTabStyle.Render(name))
		} else {
			renderedTabs = append(renderedTabs, inactiveTabStyle.Render(name))
		}
	}
	b.WriteString(strings.Join(renderedTabs, " "))
	b.WriteString("\n\n")
	b.WriteString(styles.Subtext.Render("Tabs: [Tab] or [1, 2, 3, 4]  •  Navigate: [↑/↓]  •  Change: [←/→]  •  Action/Toggle: [Space/Enter]") + "\n\n")

	isFocused := func(idx int) bool { return m.settingCursor == idx }

	cursor := func(idx int) string {
		if isFocused(idx) {
			return "> "
		}
		return "  "
	}

	writeRow := func(idx int, label, valueStr, desc string) {
		b.WriteString(cursor(idx) + label + styles.Value.Render(valueStr) + "\n")
		if isFocused(idx) && desc != "" {
			b.WriteString(styles.Subtext.Render("    └ "+desc) + "\n")
		}
		b.WriteString("\n")
	}

	switch m.settingsTab {
	case 0:
		// Tab 0: Encoding
		// Row 0 — Resolution
		writeRow(0,
			"Target Resolution:   ",
			m.cfg.Resolution+"  ← / → to change  [Original, 4K, 1440p, 1080p, 720p]",
			"Controls the maximum output resolution.\n"+
				"  • Original — keeps the same dimensions as the source (recommended)\n"+
				"  • 4K / 1440p / 1080p / 720p — downscales if the source is larger; never upscales",
		)

		// Row 1 — Video Quality
		writeRow(1,
			"Video Quality:       ",
			fmt.Sprintf("[%d] %s  ← / → to change  [1: Max Savings ➔ 10: Max Quality]", m.cfg.Quality(), m.cfg.QualityLabel()),
			m.cfg.QualityDescription(),
		)

		// Row 2 — Size Guard
		sgState := "[✓] ON"
		if !m.cfg.SizeGuard {
			sgState = "[ ] OFF"
		}
		sgDesc := "OFF — The transcoded file will always replace the original, even if it is larger.\n" +
			"       Use this only if you need a specific codec regardless of file size."
		if m.cfg.SizeGuard {
			sgDesc = "ON  — If the transcoded file ends up larger than the original, the replacement\n" +
				"       is skipped and the original is kept untouched. Prevents accidental bloat."
		}
		writeRow(2,
			"Size Guard:          ",
			sgState+"  [Space / Enter] to toggle",
			sgDesc,
		)

		// Row 3 — Skip HEVC
		hevcState := "[✓] ON"
		if !m.cfg.SkipHEVC {
			hevcState = "[ ] OFF"
		}
		hevcDesc := "OFF — All eligible videos will be processed and re-encoded, even if already HEVC."
		if m.cfg.SkipHEVC {
			hevcDesc = "ON  — Videos already encoded in HEVC/H.265 format are skipped to avoid\n" +
				"       unnecessary re-encoding."
		}
		writeRow(3,
			"Skip HEVC Videos:    ",
			hevcState+"  [Space / Enter] to toggle",
			hevcDesc,
		)

	case 1:
		// Tab 1: Filtering
		// Row 0 — Threshold Mode
		modeStr := m.cfg.ThresholdMode
		if modeStr == "" {
			modeStr = config.ThresholdModeMBPerMin
		}
		writeRow(0,
			"Threshold Mode:      ",
			modeStr+"  ← / → / [Space / Enter] to change  [MB/min, Total MB, Disabled]",
			"Strategy used to filter videos before optimization.\n"+
				"  • MB/min   — Filters videos by bitrate density (skips low-bitrate videos)\n"+
				"  • Total MB — Filters videos by total file size threshold\n"+
				"  • Disabled — Includes all videos without any size/density filtering",
		)

		// Row 1 — Threshold Value
		thresholdLabel := "Threshold (MB/min):  "
		thresholdDisplay := m.cfg.SizeThreshold + " MB/min"
		threshDesc := "Minimum size per minute of video (MB/min). Videos with a bitrate density below\n" +
			"  this threshold are skipped entirely (e.g. 100 MB/min).\n" +
			"  Filters out low-bitrate videos regardless of video duration."
		switch m.cfg.ThresholdMode {
		case config.ThresholdModeDisabled:
			thresholdLabel = "Threshold:           "
			thresholdDisplay = "Disabled (All videos scanned)"
			threshDesc = "Threshold filtering is disabled. All video assets in your library will be scanned."
		case config.ThresholdModeTotalMB:
			thresholdLabel = "Threshold (Total MB): "
			thresholdDisplay = m.cfg.SizeThreshold + " MB"
			threshDesc = "Minimum total file size in megabytes (MB). Videos smaller than this\n" +
				"  threshold are skipped entirely (e.g. 500 MB)."
		}

		b.WriteString(cursor(1) + thresholdLabel)
		if m.cfg.ThresholdMode == config.ThresholdModeDisabled {
			b.WriteString(styles.Value.Render(thresholdDisplay) + "\n")
			if isFocused(1) {
				b.WriteString(styles.Subtext.Render("    └ "+threshDesc) + "\n")
			}
		} else if isFocused(1) && m.thresholdInput.Focused() {
			b.WriteString(m.thresholdInput.View() + "  " + styles.Subtext.Render("[Enter] to confirm") + "\n")
			b.WriteString(styles.Subtext.Render("    └ "+threshDesc) + "\n")
		} else if isFocused(1) {
			b.WriteString(styles.Value.Render(thresholdDisplay) + "  " + styles.Subtext.Render("[Enter] to edit") + "\n")
			b.WriteString(styles.Subtext.Render("    └ "+threshDesc) + "\n")
		} else {
			b.WriteString(styles.Value.Render(thresholdDisplay) + "\n")
		}
		b.WriteString("\n")

		// Row 2 — Exclude Optimized
		excludeOptState := "[✓] ON"
		if !m.cfg.ExcludeOptimized {
			excludeOptState = "[ ] OFF"
		}
		eoDesc := "OFF — All videos matching search criteria are included, even if previously\n" +
			"       tagged as optimized."
		if m.cfg.ExcludeOptimized {
			eoDesc = "ON  — Uses an Immich tag ('immich-optimizer') to track and exclude already\n" +
				"       optimized videos from search. Ideal for recurring runs as your library\n" +
				"       grows on remaining videos (works silently in the background)."
		}
		writeRow(2,
			"Exclude Optimized:   ",
			excludeOptState+"  [Space / Enter] to toggle",
			eoDesc,
		)

		// Row 3 — Start Date (After)
		dateAfterLabel := "Start Date (After):  "
		dateAfterDisplay := m.cfg.DateAfter
		if dateAfterDisplay == "" {
			dateAfterDisplay = "Unconstrained (All past)"
		}
		dateAfterDesc := "Filter videos taken on or after this date (format: YYYY-MM-DD).\n" +
			"  Leave empty to include from the beginning of your library."
		b.WriteString(cursor(3) + dateAfterLabel)
		if isFocused(3) && m.dateAfterInput.Focused() {
			b.WriteString(m.dateAfterInput.View() + "  " + styles.Subtext.Render("[Enter] confirm  [Esc] cancel") + "\n")
			if m.dateError != "" {
				b.WriteString(styles.ErrorText.Render("    ⚠ "+m.dateError) + "\n")
			}
			b.WriteString(styles.Subtext.Render("    └ "+dateAfterDesc) + "\n")
		} else if isFocused(3) {
			b.WriteString(styles.Value.Render(dateAfterDisplay) + "  " + styles.Subtext.Render("[Enter] to edit") + "\n")
			if m.dateError != "" {
				b.WriteString(styles.ErrorText.Render("    ⚠ "+m.dateError) + "\n")
			}
			b.WriteString(styles.Subtext.Render("    └ "+dateAfterDesc) + "\n")
		} else {
			b.WriteString(styles.Value.Render(dateAfterDisplay) + "\n")
		}
		b.WriteString("\n")

		// Row 4 — End Date (Before)
		dateBeforeLabel := "End Date (Before):   "
		dateBeforeDisplay := m.cfg.DateBefore
		if dateBeforeDisplay == "" {
			dateBeforeDisplay = "Unconstrained (Up to present)"
		}
		dateBeforeDesc := "Filter videos taken on or before this date (format: YYYY-MM-DD).\n" +
			"  Inclusive of the whole day (up to 23:59:59 in your local timezone).\n" +
			"  Leave empty to include up to the newest videos."
		b.WriteString(cursor(4) + dateBeforeLabel)
		if isFocused(4) && m.dateBeforeInput.Focused() {
			b.WriteString(m.dateBeforeInput.View() + "  " + styles.Subtext.Render("[Enter] confirm  [Esc] cancel") + "\n")
			if m.dateError != "" {
				b.WriteString(styles.ErrorText.Render("    ⚠ "+m.dateError) + "\n")
			}
			b.WriteString(styles.Subtext.Render("    └ "+dateBeforeDesc) + "\n")
		} else if isFocused(4) {
			b.WriteString(styles.Value.Render(dateBeforeDisplay) + "  " + styles.Subtext.Render("[Enter] to edit") + "\n")
			if m.dateError != "" {
				b.WriteString(styles.ErrorText.Render("    ⚠ "+m.dateError) + "\n")
			}
			b.WriteString(styles.Subtext.Render("    └ "+dateBeforeDesc) + "\n")
		} else {
			b.WriteString(styles.Value.Render(dateBeforeDisplay) + "\n")
		}
		b.WriteString("\n")

	case 2:
		// Tab 2: Pipeline & Network
		// Row 0 — Pipeline Prefetch
		prefetchState := "[✓] ON"
		if !m.cfg.EnablePrefetch {
			prefetchState = "[ ] OFF"
		}
		prefetchDesc := "OFF — Sequential mode. Downloads, transcodes, and uploads strictly one at a time."
		if m.cfg.EnablePrefetch {
			prefetchDesc = "ON  — Downloads the next video in background while transcoding current video.\n" +
				"       Eliminates download wait time without exceeding disk bounds (max 1 file)."
		}
		writeRow(0,
			"Pipeline Prefetch:   ",
			prefetchState+"  [Space / Enter] to toggle",
			prefetchDesc,
		)

		// Row 1 — Concurrent Encoders
		encCount := m.cfg.ConcurrentEncoders
		if encCount <= 0 {
			encCount = 1
		}
		writeRow(1,
			"Concurrent Encoders: ",
			fmt.Sprintf("%d  ← / → to change  [1, 2]", encCount),
			"Controls how many ffmpeg instances encode simultaneously.\n"+
				"  • 1 — Default. Best for hardware encoders (VideoToolbox / NVENC) and CPU thermals\n"+
				"  • 2 — Parallel encoding. High throughput on multi-core systems",
		)

		// Row 2 — Logging Level
		logLevelStr := "INFO"
		if m.cfg.IsDebug() {
			logLevelStr = "DEBUG"
		}
		writeRow(2,
			"Logging Level:       ",
			logLevelStr+"  ← / → / [Space / Enter] to toggle  [Info, Debug]",
			"Controls detail of output logs. Set to Debug for full FFmpeg & ExifTool command lines,\n"+
				"  stderr outputs, and diagnostic traces to identify error root causes.\n"+
				"  Logs written to: ~/.config/immich-video-optimizer/optimizer.log",
		)

		// Row 3 — Network Retries
		retryStr := fmt.Sprintf("%d", m.cfg.MaxRetries)
		if m.cfg.MaxRetries == 0 {
			retryStr = "0 (Disabled)"
		}
		writeRow(3,
			"Network Retries:     ",
			retryStr+"  ← / → / [Space / Enter] to change  [0, 1, 2, 3, 5]",
			"Number of automatic retry attempts with backoff for failed downloads and uploads.\n"+
				"  • 0 — Disabled. Fails immediately on first error\n"+
				"  • 1 to 5 — Retries automatically with exponential backoff on transient network drops",
		)

	case 3:
		// Tab 3: Advanced
		boxWidth := 84
		if m.width > 12 {
			boxWidth = m.width - 8
			if boxWidth > 86 {
				boxWidth = 86
			} else if boxWidth < 48 {
				boxWidth = 48
			}
		}

		flagsLabel := "Custom FFmpeg Flags:"
		flagsDisplay := m.cfg.CustomFFmpegFlags
		if flagsDisplay == "" {
			flagsDisplay = "(None — using defaults)"
		}
		flagsDesc := "Specify custom FFmpeg options to merge or override defaults.\n" +
			"  • Overrides: -crf, -preset, -c:v, -vf, -c:a, etc. replace defaults\n" +
			"  • Appends: extra flags (e.g. -tune animation) are added before output\n" +
			"  • Protected: -i, -y, -tag:v hvc1, plumbing cannot be overridden"

		contentWidth := boxWidth - 4
		if contentWidth < 40 {
			contentWidth = 40
		}

		if isFocused(0) && m.customFlagsInput.Focused() {
			m.customFlagsInput.Width = contentWidth
			b.WriteString(cursor(0) + flagsLabel + "  " + styles.Subtext.Render("[Enter] confirm  [Esc] cancel") + "\n")
			b.WriteString("    " + m.customFlagsInput.View() + "\n")
			if m.customFlagsError != "" {
				b.WriteString(styles.ErrorText.Render("    ⚠ "+m.customFlagsError) + "\n")
			}
			b.WriteString(styles.Subtext.Render("    └ "+flagsDesc) + "\n")
		} else if isFocused(0) {
			displayVal := truncateString(flagsDisplay, contentWidth)
			b.WriteString(cursor(0) + flagsLabel + "  " + styles.Subtext.Render("[Enter] to edit") + "\n")
			b.WriteString("    " + styles.Value.Render(displayVal) + "\n")
			if m.customFlagsError != "" {
				b.WriteString(styles.ErrorText.Render("    ⚠ "+m.customFlagsError) + "\n")
			}
			b.WriteString(styles.Subtext.Render("    └ "+flagsDesc) + "\n")
		} else {
			displayVal := truncateString(flagsDisplay, contentWidth)
			b.WriteString(cursor(0) + flagsLabel + "\n")
			b.WriteString("    " + styles.Value.Render(displayVal) + "\n")
		}

		effectiveFlags := m.cfg.CustomFFmpegFlags
		if isFocused(0) && m.customFlagsInput.Focused() {
			effectiveFlags = m.customFlagsInput.Value()
		}
		if effectiveFlags != "" {
			simBase := []string{"-y", "-i", "<input.mp4>", "-c:v", "hevc", "-tag:v", "hvc1", "<output.mp4>"}
			res, _ := engine.ParseAndMergeCustomArgs(simBase, effectiveFlags)
			if len(res.IgnoredProtected) > 0 {
				b.WriteString(styles.WarningText.Render(fmt.Sprintf("    ⚠ Protected parameters ignored: %s", strings.Join(res.IgnoredProtected, ", "))) + "\n")
			}
		}
		b.WriteString("\n")

		// Row 1 — Reset to Defaults
		writeRow(1,
			"Reset to Defaults:   ",
			"[Space / Enter] Reset custom flags to defaults",
			"Clears all custom flags and restores pure default encoding arguments.",
		)

		// Live FFmpeg Command Preview
		b.WriteString(m.renderFFmpegPreview())
		b.WriteString("\n")

	}

	b.WriteString("\n")
	b.WriteString(styles.Subtext.Render("[Esc] Save & Return"))
	return b.String()
}

type cmdArg struct {
	flag     string
	val      string
	hasVal   bool
	isCustom bool
}

func (m *AppModel) renderFFmpegPreview() string {
	encoder := engine.DetectHEVCEncoderDefault()
	if m.pipeline != nil && m.pipeline.Encoder() != "" {
		encoder = m.pipeline.Encoder()
	}

	opts := engine.TranscodeOptions{
		Input:               "<input.mp4>",
		Output:              "<output.mp4>",
		Encoder:             encoder,
		AudioCopy:           false,
		NoAudio:             false,
		CRF:                 m.cfg.QualityCRF(),
		VideoToolboxQuality: m.cfg.QualityVideoToolbox(),
		Preset:              m.cfg.Preset,
		Resolution:          m.cfg.Resolution,
	}

	baseArgs := engine.BuildFFmpegArgs(opts)
	fullBase := append([]string{"-nostats", "-progress", "pipe:1"}, baseArgs...)

	customInput := m.cfg.CustomFFmpegFlags
	if m.settingsTab == 3 && m.settingCursor == 0 && m.customFlagsInput.Focused() {
		customInput = m.customFlagsInput.Value()
	}

	res, _ := engine.ParseAndMergeCustomArgs(fullBase, customInput)

	customStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#F59E0B")).
		Bold(true)

	defaultStyle := lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#334155", Dark: "#CBD5E1"})

	ffmpegLabel := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#38BDF8")).Render("ffmpeg")

	// Group tokens into flag + value pairs so flags are never stranded alone
	var args []cmdArg
	for i := 0; i < len(res.Merged); i++ {
		token := res.Merged[i]
		custom := i < len(res.IsCustom) && res.IsCustom[i]

		if strings.HasPrefix(token, "-") {
			if i+1 < len(res.Merged) && !strings.HasPrefix(res.Merged[i+1], "-") {
				valCustom := (i+1 < len(res.IsCustom)) && res.IsCustom[i+1]
				args = append(args, cmdArg{
					flag:     token,
					val:      res.Merged[i+1],
					hasVal:   true,
					isCustom: custom || valCustom,
				})
				i++
			} else {
				args = append(args, cmdArg{
					flag:     token,
					isCustom: custom,
				})
			}
		} else {
			args = append(args, cmdArg{
				val:      token,
				hasVal:   true,
				isCustom: custom,
			})
		}
	}

	boxWidth := 84
	if m.width > 12 {
		boxWidth = m.width - 8
		if boxWidth > 86 {
			boxWidth = 86
		} else if boxWidth < 48 {
			boxWidth = 48
		}
	}
	innerWidth := boxWidth - 4 // minus borders (2) and padding (2)

	var lines []string
	var currentLine []string
	currentLineWidth := 0

	flushLine := func() {
		if len(currentLine) > 0 {
			lines = append(lines, strings.Join(currentLine, " "))
			currentLine = nil
			currentLineWidth = 0
		}
	}

	// Start with "ffmpeg"
	currentLine = append(currentLine, ffmpegLabel)
	currentLineWidth = len("ffmpeg")

	for _, a := range args {
		st := defaultStyle
		if a.isCustom {
			st = customStyle
		}

		if !a.hasVal {
			// Flag without value: e.g. "-nostats", "-y", "-an"
			flagLen := len(a.flag)
			if currentLineWidth > 0 && currentLineWidth+1+flagLen > innerWidth {
				flushLine()
				currentLine = append(currentLine, "  "+st.Render(a.flag))
				currentLineWidth = 2 + flagLen
			} else {
				currentLine = append(currentLine, st.Render(a.flag))
				currentLineWidth += 1 + flagLen
			}
			continue
		}

		if a.flag == "" {
			// Standalone value: e.g. "<output.mp4>"
			valLen := len(a.val)
			if currentLineWidth > 0 && currentLineWidth+1+valLen > innerWidth {
				flushLine()
				currentLine = append(currentLine, "  "+st.Render(a.val))
				currentLineWidth = 2 + valLen
			} else {
				currentLine = append(currentLine, st.Render(a.val))
				currentLineWidth += 1 + valLen
			}
			continue
		}

		// Flag with value: e.g. "-preset medium", "-vf <filter>"
		quotedVal := a.val
		if strings.ContainsAny(a.val, " ,;:\"'") && !strings.HasPrefix(a.val, "\"") && !strings.HasPrefix(a.val, "'") {
			quotedVal = fmt.Sprintf("%q", a.val)
		}

		fullLen := len(a.flag) + 1 + len(quotedVal)

		// 1. Fits on current line
		if currentLineWidth > 0 && currentLineWidth+1+fullLen <= innerWidth {
			currentLine = append(currentLine, st.Render(a.flag)+" "+st.Render(quotedVal))
			currentLineWidth += 1 + fullLen
			continue
		}

		// 2. Fits on a fresh line together
		if 2+fullLen <= innerWidth {
			flushLine()
			currentLine = append(currentLine, "  "+st.Render(a.flag)+" "+st.Render(quotedVal))
			currentLineWidth = 2 + fullLen
			continue
		}

		// 3. Even on a fresh line, it's too long! Split at delimiters (comma, colon, space)
		flushLine()
		indentLen := 2 + len(a.flag) + 1
		contIndent := strings.Repeat(" ", indentLen)
		if indentLen > 8 {
			contIndent = "      "
			indentLen = 6
		}

		firstMax := innerWidth - (2 + len(a.flag) + 1)
		contMax := innerWidth - indentLen

		valPieces := splitValueAtDelimiters(a.val, firstMax, contMax)
		if len(valPieces) <= 1 {
			lines = append(lines, "  "+st.Render(a.flag)+" "+st.Render(quotedVal))
			continue
		}

		// First line: "  -flag piece0"
		lines = append(lines, "  "+st.Render(a.flag)+" "+st.Render(valPieces[0]))
		// Continuation lines: "      piece1", "      piece2"
		for j := 1; j < len(valPieces); j++ {
			lines = append(lines, contIndent+st.Render(valPieces[j]))
		}
	}

	flushLine()

	commandStr := strings.Join(lines, "\n")

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.AdaptiveColor{Light: "#94A3B8", Dark: "#475569"}).
		Padding(0, 1).
		Width(boxWidth)

	legendStyle := styles.Subtext.Copy().Width(innerWidth)
	legend := legendStyle.Render("🔒 Protected (immutable): -nostats, -progress pipe:1, -y, -i, <input>, <output>, -tag:v hvc1")

	boxContent := lipgloss.NewStyle().Bold(true).Render("FFmpeg Command Preview:") + "\n\n" +
		commandStr + "\n\n" +
		legend

	return boxStyle.Render(boxContent)
}

// splitValueAtDelimiters splits a long parameter value at natural delimiters (comma, colon, space).
func splitValueAtDelimiters(val string, firstMax, contMax int) []string {
	if firstMax <= 10 {
		firstMax = 40
	}
	if contMax <= 10 {
		contMax = 40
	}

	needsQuotes := strings.ContainsAny(val, " ,;:\"'")
	cleanVal := strings.Trim(val, "\"'")

	splitByDelim := func(s, delim string) []string {
		raw := strings.Split(s, delim)
		var out []string
		for i, p := range raw {
			if i < len(raw)-1 {
				out = append(out, p+delim)
			} else if p != "" {
				out = append(out, p)
			}
		}
		return out
	}

	// 1. Identify primary delimiter: comma (filters), then space (args/metadata), then colon (filter params)
	var chunks []string
	if strings.Contains(cleanVal, ",") {
		chunks = splitByDelim(cleanVal, ",")
	} else if strings.Contains(cleanVal, " ") {
		chunks = splitByDelim(cleanVal, " ")
	} else if strings.Contains(cleanVal, ":") {
		chunks = splitByDelim(cleanVal, ":")
	} else {
		chunks = []string{cleanVal}
	}

	// If any chunk is still larger than contMax, sub-split it by colon
	var finalChunks []string
	for _, ch := range chunks {
		if len(ch) > contMax && strings.Contains(ch, ":") {
			finalChunks = append(finalChunks, splitByDelim(ch, ":")...)
		} else {
			finalChunks = append(finalChunks, ch)
		}
	}

	// Pack chunks into lines using firstMax for line 0, and contMax for subsequent lines
	var lines []string
	var cur strings.Builder
	limit := firstMax

	for _, ch := range finalChunks {
		if cur.Len() == 0 {
			cur.WriteString(ch)
		} else if cur.Len()+len(ch) <= limit {
			cur.WriteString(ch)
		} else {
			lines = append(lines, cur.String())
			cur.Reset()
			limit = contMax
			cur.WriteString(ch)
		}
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}

	// Wrap in quotes if needed
	if needsQuotes && len(lines) > 0 {
		lines[0] = "\"" + lines[0]
		lines[len(lines)-1] = lines[len(lines)-1] + "\""
	}

	return lines
}

func (m *AppModel) settingsMaxCursor() int {
	switch m.settingsTab {
	case 1:
		return 4 // Tab 1 (Filtering): 0: Mode, 1: Value, 2: Exclude Optimized, 3: Start Date, 4: End Date
	case 2:
		return 3 // Tab 2 (Pipeline & Network): 0: Prefetch, 1: Encoders, 2: Log Level, 3: Retries
	case 3:
		return 1 // Tab 3 (Advanced): 0: Custom Flags, 1: Reset to Defaults
	default:
		return 3 // Tab 0 (Encoding): 0: Resolution, 1: Quality, 2: Size Guard, 3: Skip HEVC
	}
}

func (m *AppModel) switchSettingsTab(newTab int) {
	if newTab < 0 {
		newTab = 0
	} else if newTab > 3 {
		newTab = 3
	}
	if m.settingsTab != newTab {
		m.settingsTab = newTab
		m.settingCursor = 0
		if m.thresholdInput.Focused() {
			m.thresholdInput.Blur()
		}
		if m.dateAfterInput.Focused() {
			m.dateAfterInput.Blur()
		}
		if m.dateBeforeInput.Focused() {
			m.dateBeforeInput.Blur()
		}
		if m.customFlagsInput.Focused() {
			m.customFlagsInput.Blur()
		}
		m.dateError = ""
		m.customFlagsError = ""
	}
}

func (m *AppModel) handleSettingsOptionChange(dir int) {
	thresholdModes := []string{config.ThresholdModeMBPerMin, config.ThresholdModeTotalMB, config.ThresholdModeDisabled}
	retryOptions := []int{0, 1, 2, 3, 5}

	switch m.settingsTab {
	case 0: // Encoding
		switch m.settingCursor {
		case 0: // Resolution
			for i, r := range m.resolutions {
				if r == m.cfg.Resolution {
					target := i + dir
					if target >= 0 && target < len(m.resolutions) {
						m.cfg.Resolution = m.resolutions[target]
					}
					break
				}
			}
		case 1: // Video Quality
			q := m.cfg.Quality() + dir
			if q < 1 {
				q = 1
			} else if q > 10 {
				q = 10
			}
			m.cfg.VideoQuality = q
		case 2: // Size Guard
			m.cfg.SizeGuard = !m.cfg.SizeGuard
		case 3: // Skip HEVC
			m.cfg.SkipHEVC = !m.cfg.SkipHEVC
		}

	case 1: // Filtering
		switch m.settingCursor {
		case 0: // Threshold Mode
			currIdx := 0
			for i, mode := range thresholdModes {
				if mode == m.cfg.ThresholdMode {
					currIdx = i
					break
				}
			}
			m.cfg.ThresholdMode = thresholdModes[(currIdx+dir+len(thresholdModes))%len(thresholdModes)]
		case 1: // Threshold Value
			// Focusing handled via enter / space
		case 2: // Exclude Optimized
			m.cfg.ExcludeOptimized = !m.cfg.ExcludeOptimized
		}

	case 2: // Pipeline & Network
		switch m.settingCursor {
		case 0: // Pipeline Prefetch
			m.cfg.EnablePrefetch = !m.cfg.EnablePrefetch
		case 1: // Concurrent Encoders
			if dir < 0 {
				m.cfg.ConcurrentEncoders = 1
			} else {
				m.cfg.ConcurrentEncoders = 2
			}
		case 2: // Logging Level
			if dir < 0 {
				m.cfg.LogLevel = "info"
			} else {
				m.cfg.LogLevel = "debug"
			}
			logger.SetLevelString(m.cfg.LogLevel)
		case 3: // Network Retries
			for i, val := range retryOptions {
				if val == m.cfg.MaxRetries {
					target := i + dir
					if target >= 0 && target < len(retryOptions) {
						m.cfg.MaxRetries = retryOptions[target]
						if m.pipeline != nil && m.pipeline.Client() != nil {
							m.pipeline.Client().SetMaxRetries(m.cfg.MaxRetries)
						}
					}
					break
				}
			}
		}
	}
}

func (m *AppModel) handleSettingsOptionToggle() (tea.Model, tea.Cmd) {
	thresholdModes := []string{config.ThresholdModeMBPerMin, config.ThresholdModeTotalMB, config.ThresholdModeDisabled}
	retryOptions := []int{0, 1, 2, 3, 5}

	switch m.settingsTab {
	case 0: // Encoding
		switch m.settingCursor {
		case 0: // Resolution
			for i, r := range m.resolutions {
				if r == m.cfg.Resolution {
					m.cfg.Resolution = m.resolutions[(i+1)%len(m.resolutions)]
					break
				}
			}
		case 1: // Video Quality
			q := m.cfg.Quality() + 1
			if q > 10 {
				q = 1
			}
			m.cfg.VideoQuality = q
		case 2: // Size Guard
			m.cfg.SizeGuard = !m.cfg.SizeGuard
		case 3: // Skip HEVC
			m.cfg.SkipHEVC = !m.cfg.SkipHEVC
		}

	case 1: // Filtering
		switch m.settingCursor {
		case 0: // Threshold Mode
			currIdx := 0
			for i, mode := range thresholdModes {
				if mode == m.cfg.ThresholdMode {
					currIdx = i
					break
				}
			}
			m.cfg.ThresholdMode = thresholdModes[(currIdx+1)%len(thresholdModes)]
		case 1: // Threshold Value
			if m.cfg.ThresholdMode != config.ThresholdModeDisabled {
				return m, m.thresholdInput.Focus()
			}
		case 2: // Exclude Optimized
			m.cfg.ExcludeOptimized = !m.cfg.ExcludeOptimized
		case 3: // Start Date (After)
			m.dateError = ""
			return m, m.dateAfterInput.Focus()
		case 4: // End Date (Before)
			m.dateError = ""
			return m, m.dateBeforeInput.Focus()
		}

	case 2: // Pipeline & Network
		switch m.settingCursor {
		case 0: // Pipeline Prefetch
			m.cfg.EnablePrefetch = !m.cfg.EnablePrefetch
		case 1: // Concurrent Encoders
			if m.cfg.ConcurrentEncoders <= 1 {
				m.cfg.ConcurrentEncoders = 2
			} else {
				m.cfg.ConcurrentEncoders = 1
			}
		case 2: // Logging Level
			if m.cfg.IsDebug() {
				m.cfg.LogLevel = "info"
			} else {
				m.cfg.LogLevel = "debug"
			}
			logger.SetLevelString(m.cfg.LogLevel)
		case 3: // Network Retries
			currIdx := 0
			for i, val := range retryOptions {
				if val == m.cfg.MaxRetries {
					currIdx = i
					break
				}
			}
			m.cfg.MaxRetries = retryOptions[(currIdx+1)%len(retryOptions)]
			if m.pipeline != nil && m.pipeline.Client() != nil {
				m.pipeline.Client().SetMaxRetries(m.cfg.MaxRetries)
			}
		}

	case 3: // Advanced
		switch m.settingCursor {
		case 0: // Custom Flags
			m.customFlagsError = ""
			return m, m.customFlagsInput.Focus()
		case 1: // Reset to Defaults
			m.cfg.CustomFFmpegFlags = ""
			m.customFlagsInput.SetValue("")
			m.customFlagsError = ""
			if m.pipeline != nil {
				m.pipeline.UpdateConfig(m.cfg)
			}
			_ = config.SaveConfig(m.cfg)
		}

	}
	return m, nil
}

func (m *AppModel) updateSettings(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// When the threshold row (Tab 1, index 1) is focused and user is typing into it,
	// Enter confirms the value; Esc cancels editing; everything else feeds the input.
	if m.settingsTab == 1 && m.settingCursor == 1 && m.thresholdInput.Focused() {
		switch msg.String() {
		case "enter":
			m.thresholdInput.Blur()
			thresh := strings.TrimSpace(m.thresholdInput.Value())
			if thresh == "" {
				thresh = "100"
				m.thresholdInput.SetValue(thresh)
			}
			m.cfg.SizeThreshold = thresh
			return m, nil
		case "esc":
			m.thresholdInput.Blur()
			return m, nil
		default:
			var cmd tea.Cmd
			m.thresholdInput, cmd = m.thresholdInput.Update(msg)
			return m, cmd
		}
	}

	// Start Date (After) input
	if m.settingsTab == 1 && m.settingCursor == 3 && m.dateAfterInput.Focused() {
		switch msg.String() {
		case "enter":
			val := strings.TrimSpace(m.dateAfterInput.Value())
			if val != "" {
				t, err := time.Parse("2006-01-02", val)
				if err != nil {
					m.dateError = "Invalid date format. Expected YYYY-MM-DD"
					return m, nil
				}
				if m.cfg.DateBefore != "" {
					if tEnd, err := time.Parse("2006-01-02", m.cfg.DateBefore); err == nil {
						if t.After(tEnd) {
							m.dateError = "Start date cannot be after end date"
							return m, nil
						}
					}
				}
			}
			m.dateError = ""
			m.cfg.DateAfter = val
			m.dateAfterInput.SetValue(val)
			m.dateAfterInput.Blur()
			if m.pipeline != nil {
				m.pipeline.UpdateConfig(m.cfg)
			}
			_ = config.SaveConfig(m.cfg)
			return m, nil
		case "esc":
			m.dateError = ""
			m.dateAfterInput.SetValue(m.cfg.DateAfter)
			m.dateAfterInput.Blur()
			return m, nil
		default:
			m.dateError = ""
			var cmd tea.Cmd
			m.dateAfterInput, cmd = m.dateAfterInput.Update(msg)
			return m, cmd
		}
	}

	// End Date (Before) input
	if m.settingsTab == 1 && m.settingCursor == 4 && m.dateBeforeInput.Focused() {
		switch msg.String() {
		case "enter":
			val := strings.TrimSpace(m.dateBeforeInput.Value())
			if val != "" {
				t, err := time.Parse("2006-01-02", val)
				if err != nil {
					m.dateError = "Invalid date format. Expected YYYY-MM-DD"
					return m, nil
				}
				if m.cfg.DateAfter != "" {
					if tStart, err := time.Parse("2006-01-02", m.cfg.DateAfter); err == nil {
						if t.Before(tStart) {
							m.dateError = "End date cannot be before start date"
							return m, nil
						}
					}
				}
			}
			m.dateError = ""
			m.cfg.DateBefore = val
			m.dateBeforeInput.SetValue(val)
			m.dateBeforeInput.Blur()
			if m.pipeline != nil {
				m.pipeline.UpdateConfig(m.cfg)
			}
			_ = config.SaveConfig(m.cfg)
			return m, nil
		case "esc":
			m.dateError = ""
			m.dateBeforeInput.SetValue(m.cfg.DateBefore)
			m.dateBeforeInput.Blur()
			return m, nil
		default:
			m.dateError = ""
			var cmd tea.Cmd
			m.dateBeforeInput, cmd = m.dateBeforeInput.Update(msg)
			return m, cmd
		}
	}

	// Custom FFmpeg Flags input
	if m.settingsTab == 3 && m.settingCursor == 0 && m.customFlagsInput.Focused() {
		switch msg.String() {
		case "enter":
			raw := strings.TrimSpace(m.customFlagsInput.Value())
			if raw != "" {
				if _, err := engine.SplitShellArgs(raw); err != nil {
					m.customFlagsError = "Invalid syntax: " + err.Error()
					return m, nil
				}
			}
			m.customFlagsError = ""
			m.cfg.CustomFFmpegFlags = raw
			m.customFlagsInput.SetValue(raw)
			m.customFlagsInput.Blur()
			if m.pipeline != nil {
				m.pipeline.UpdateConfig(m.cfg)
			}
			_ = config.SaveConfig(m.cfg)
			return m, nil
		case "esc":
			m.customFlagsError = ""
			m.customFlagsInput.SetValue(m.cfg.CustomFFmpegFlags)
			m.customFlagsInput.Blur()
			return m, nil
		default:
			m.customFlagsError = ""
			var cmd tea.Cmd
			m.customFlagsInput, cmd = m.customFlagsInput.Update(msg)
			return m, cmd
		}
	}

	switch msg.String() {
	case "q", "esc":
		_ = config.SaveConfig(m.cfg)
		if len(m.videos) > 0 {
			m.state = ViewBrowser
		} else {
			m.state = ViewScanPrompt
		}
		return m, nil

	case "tab":
		m.switchSettingsTab((m.settingsTab + 1) % 4)
		return m, nil

	case "shift+tab":
		m.switchSettingsTab((m.settingsTab - 1 + 4) % 4)
		return m, nil

	case "1":
		m.switchSettingsTab(0)
		return m, nil

	case "2":
		m.switchSettingsTab(1)
		return m, nil

	case "3":
		m.switchSettingsTab(2)
		return m, nil

	case "4":
		m.switchSettingsTab(3)
		return m, nil

	case "up", "k":
		if m.settingCursor > 0 {
			m.settingCursor--
		}

	case "down", "j":
		if m.settingCursor < m.settingsMaxCursor() {
			m.settingCursor++
		}

	case "left", "h":
		m.handleSettingsOptionChange(-1)

	case "right", "l":
		m.handleSettingsOptionChange(1)

	case " ", "enter":
		return m.handleSettingsOptionToggle()
	}

	return m, nil
}

func truncateString(s string, maxLen int) string {
	if maxLen <= 3 {
		if len(s) > maxLen && maxLen > 0 {
			return s[:maxLen]
		}
		return s
	}
	if lipgloss.Width(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	curWidth := 0
	targetWidth := maxLen - 3
	var result []rune
	for _, r := range runes {
		rw := lipgloss.Width(string(r))
		if curWidth+rw > targetWidth {
			break
		}
		result = append(result, r)
		curWidth += rw
	}
	return string(result) + "..."
}

