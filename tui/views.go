package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"

	"immich-video-optimizer/engine"
	"immich-video-optimizer/tui/styles"
)

func (m *AppModel) View() string {
	var s string
	switch m.state {
	case ViewTestingConnection:
		s = m.viewTestingConnection()
	case ViewConnectionSetup:
		s = m.viewConnectionSetup()
	case ViewResumePrompt:
		s = m.viewResumePrompt()
	case ViewScanPrompt:
		s = m.viewScanPrompt()
	case ViewScanning:
		s = m.viewScanning()
	case ViewBrowser:
		s = m.viewBrowser()
	case ViewDryRun:
		s = m.viewDryRun()
	case ViewSettings:
		s = m.viewSettings()
	case ViewDashboard:
		s = m.viewDashboard()
	case ViewSummary:
		s = m.viewSummary()
	}

	content := styles.Base.Render(s)
	if m.width > 0 && m.height > 0 {
		cw := lipgloss.Width(content)
		ch := lipgloss.Height(content)
		targetW := m.width
		targetH := m.height
		if cw > targetW {
			targetW = cw
		}
		if ch > targetH {
			targetH = ch
		}
		return lipgloss.Place(targetW, targetH, lipgloss.Center, lipgloss.Center, content)
	}
	return content
}

func (m *AppModel) viewTestingConnection() string {
	b := strings.Builder{}
	b.WriteString(styles.Title.Render("🎬 Immich Video Optimizer"))
	b.WriteString("\n\n")
	b.WriteString(styles.SectionHeader.Render("Testing connection to Immich server..."))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("  Server URL: %s\n", styles.Value.Render(m.cfg.URL)))
	b.WriteString("  Validating API credentials with server...\n\n")
	b.WriteString(styles.Subtext.Render("Please wait..."))
	return b.String()
}

func (m *AppModel) viewConnectionSetup() string {
	b := strings.Builder{}
	b.WriteString(styles.Title.Render("🎬 Immich Server Connection Setup"))
	b.WriteString("\n\n")

	if m.connectionError != "" {
		b.WriteString(styles.ErrorText.Render("❌ " + m.connectionError))
		b.WriteString("\n\n")
	} else {
		b.WriteString("Enter your Immich server connection details to connect:\n\n")
	}

	renderInput := func(label string, input textinput.Model, isFocused bool) string {
		prefix := "  "
		if isFocused {
			prefix = "> "
		}
		return fmt.Sprintf("%s%s:\n%s%s\n\n", prefix, label, prefix, input.View())
	}

	b.WriteString(renderInput("Immich Server URL", m.urlInput, m.setupFocus == 0))
	b.WriteString(renderInput("Immich API Key / Token", m.tokenInput, m.setupFocus == 1))

	btn := "[ Connect & Test Server ]"
	if m.setupFocus == 2 {
		b.WriteString(styles.ActiveButton.Render(btn))
	} else {
		b.WriteString(styles.Button.Render(btn))
	}
	b.WriteString("\n\n")
	b.WriteString(styles.Subtext.Render("Navigation: [Tab / ↓ / ↑] Move  |  [Enter] Next / Submit  |  [q] Quit"))

	return b.String()
}

func (m *AppModel) viewScanPrompt() string {
	b := strings.Builder{}
	b.WriteString(styles.Title.Render("🎬 Immich Video Optimizer"))
	b.WriteString("\n\n")

	if m.scanError != "" {
		b.WriteString(styles.ErrorText.Render("❌ " + m.scanError))
		b.WriteString("\n\n")
	}

	b.WriteString(styles.SectionHeader.Render("Server Status:"))
	b.WriteString(fmt.Sprintf("\n  • Immich Server:  %s  %s\n", styles.Value.Render(m.cfg.URL), styles.SuccessText.Render("[Connected ✓]")))
	b.WriteString(fmt.Sprintf("  • MB/Min Threshold: %s (Videos denser than this will be scanned)\n", styles.Value.Render(m.cfg.SizeThreshold+" MB/min")))
	b.WriteString(fmt.Sprintf("  • Resolution:     %s\n", styles.Value.Render(m.cfg.Resolution)))
	b.WriteString(fmt.Sprintf("  • Transcode Speed:%s\n", styles.Value.Render(m.cfg.Preset)))
	sgText := "Enabled (skips replacement if transcoded is larger)"
	if !m.cfg.SizeGuard {
		sgText = "Disabled"
	}
	b.WriteString(fmt.Sprintf("  • Size Guard:     %s\n", styles.Value.Render(sgText)))
	hevcText := "Enabled (skips videos already in HEVC format)"
	if !m.cfg.SkipHEVC {
		hevcText = "Disabled"
	}
	b.WriteString(fmt.Sprintf("  • Skip HEVC:      %s\n", styles.Value.Render(hevcText)))
	prefetchText := "Enabled (prefetch next video in background)"
	if !m.cfg.EnablePrefetch {
		prefetchText = "Disabled (sequential)"
	}
	b.WriteString(fmt.Sprintf("  • Prefetch Next:  %s\n", styles.Value.Render(prefetchText)))
	b.WriteString(fmt.Sprintf("  • Encoders:       %s worker(s)\n\n", styles.Value.Render(fmt.Sprintf("%d", m.cfg.ConcurrentEncoders))))

	b.WriteString(styles.SectionHeader.Render("Ready to Scan:"))
	b.WriteString("\nNo videos loaded yet. Start a scan to discover large videos on your server.\n\n")

	b.WriteString(styles.ActiveButton.Render("[ Enter ] Scan Immich Library") + "\n\n")
	b.WriteString(styles.Button.Render("[ S ] Settings") + "  " + styles.Button.Render("[ E ] Edit Server Credentials") + "  " + styles.Button.Render("[ Q ] Quit") + "\n")

	return b.String()
}

func (m *AppModel) viewScanning() string {
	b := strings.Builder{}
	b.WriteString(styles.Title.Render("🎬 Immich Video Optimizer"))
	b.WriteString("\n\n")
	b.WriteString(styles.SectionHeader.Render("🔍 Scanning Immich Library..."))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("  Querying assets on %s\n", styles.Value.Render(m.cfg.URL)))
	b.WriteString(fmt.Sprintf("  Inspecting videos exceeding %s MB/min...\n\n", styles.Value.Render(m.cfg.SizeThreshold)))
	b.WriteString(styles.Subtext.Render("Please wait, this may take a moment for large libraries..."))
	return b.String()
}

func (m *AppModel) renderScanSummary() string {
	if m.scanResult == nil {
		return ""
	}
	r := m.scanResult
	b := strings.Builder{}
	b.WriteString(styles.SectionHeader.Render("📊 Scan Results:"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  • Total assets inspected in library: %d\n", r.TotalAssetsScanned))
	b.WriteString(fmt.Sprintf("  • Non-video assets skipped:          %d (photos, RAW, etc.)\n", r.SkippedNonVideo))
	b.WriteString(fmt.Sprintf("  • Videos skipped (below %s MB/min):   %d\n", m.cfg.SizeThreshold, r.SkippedBelowSize))
	b.WriteString(fmt.Sprintf("  • Eligible videos found:             %s\n", styles.SuccessText.Render(fmt.Sprintf("%d", len(m.videos)))))
	return styles.SummaryBox.Render(b.String())
}

func (m *AppModel) viewBrowser() string {
	b := strings.Builder{}
	b.WriteString(styles.Title.Render("🎬 Immich Video Optimizer - Video Browser"))
	b.WriteString("\n\n")

	b.WriteString(m.renderScanSummary())
	b.WriteString("\n")

	if len(m.videos) == 0 {
		b.WriteString(styles.WarningText.Render("ℹ No videos found exceeding the " + m.cfg.SizeThreshold + " MB/min threshold."))
		b.WriteString("\n\nAll videos in your library are already within the target size limit.\n\n")
		b.WriteString("Actions:\n")
		b.WriteString("  [ S ] Adjust MB/min Threshold in Settings\n")
		b.WriteString("  [ R ] Rescan Library\n")
		b.WriteString("  [ E ] Edit Server Credentials\n")
		b.WriteString("  [ Q ] Quit\n")
		return b.String()
	}

	if m.isSearching {
		b.WriteString(fmt.Sprintf("Filter: %s  (Press [Enter] or [Esc] to exit search)\n\n", m.searchInput.View()))
	} else {
		b.WriteString(styles.Subtext.Render("Press [/] to filter filename  |  [Space] to toggle selection") + "\n\n")
	}

	b.WriteString(m.table.View() + "\n\n")

	var selectedCount int
	var selectedBytes int64
	for _, v := range m.videos {
		if m.selectedVideos[v.ID] {
			selectedCount++
			selectedBytes += v.GetSize()
		}
	}

	b.WriteString(fmt.Sprintf("Selection: %s / %d videos  (Total Size: %.2f GB)\n\n",
		styles.SuccessText.Render(fmt.Sprintf("%d", selectedCount)),
		len(m.videos),
		float64(selectedBytes)/(1024*1024*1024),
	))

	b.WriteString(styles.Subtext.Render("Shortcuts: [Space] Toggle | [a] All | [n] None | [/] Search | [s] Settings\n           [d] Dry Run    | [Enter] Start Optimization | [r] Rescan | [q] Quit"))
	return b.String()
}


func (m *AppModel) viewDryRun() string {
	b := strings.Builder{}
	b.WriteString(styles.Title.Render("🔍 Dry Run Preview"))
	b.WriteString("\n\n")

	// ── Probe progress bar ───────────────────────────────────────────────────
	if !m.dryRunDone {
		b.WriteString(styles.SectionHeader.Render("Probing video codecs via HTTP range headers..."))
		b.WriteString("\n")

		total := m.dryRunTotal
		probed := m.dryRunProbed
		if total <= 0 {
			total = 1
		}

		// ASCII progress bar (no external dependency)
		barWidth := 40
		filled := barWidth * probed / total
		bar := "[" + strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled) + "]"
		b.WriteString(fmt.Sprintf("  %s  %d / %d\n\n", styles.Value.Render(bar), probed, total))
		b.WriteString(styles.Subtext.Render("No files are downloaded. This reads only the first few bytes of each video."))
		b.WriteString("\n\n")
		b.WriteString(styles.Subtext.Render("[Esc / B] Cancel & Back to Browser"))
		return b.String()
	}

	// ── Results ───────────────────────────────────────────────────────────────
	selectedCount := len(m.dryRunVideos)
	hevcCount := len(m.dryRunHEVC)
	processCount := len(m.dryRunToProcess)
	probeFailedCount := len(m.dryRunProbeFailed)

	// Below-threshold count from the scan (already filtered before browser)
	belowThreshold := 0
	if m.scanResult != nil {
		belowThreshold = m.scanResult.SkippedBelowSize
	}

	failedMap := make(map[string]bool)
	for _, f := range m.dryRunProbeFailed {
		failedMap[f.ID] = true
	}

	// Total size and estimated savings for videos to process
	var processBytes int64
	var estSavedBytes int64
	for _, v := range m.dryRunToProcess {
		orig := v.GetSize()
		processBytes += orig
		detail := m.dryRunDetails[v.ID]
		_, saved := estimateSavings(orig, detail, m.cfg.Resolution, failedMap[v.ID])
		estSavedBytes += saved
	}
	estFinalBytes := processBytes - estSavedBytes
	if estFinalBytes < 0 {
		estFinalBytes = 0
	}

	b.WriteString(styles.SectionHeader.Render("Pre-flight Breakdown:"))
	b.WriteString("\n")

	// Row: total selected in browser
	b.WriteString(fmt.Sprintf("  • Videos selected in browser:      %s\n",
		styles.Value.Render(fmt.Sprintf("%d", selectedCount))))

	// Row: below-threshold (filtered at scan time — not in the selection)
	if belowThreshold > 0 {
		b.WriteString(fmt.Sprintf("  • Skipped at scan (below %s MB/min): %s  (never shown in browser)\n",
			m.cfg.SizeThreshold,
			styles.WarningText.Render(fmt.Sprintf("%d", belowThreshold))))
	}

	// Row: already HEVC — skipped by probe
	if m.cfg.SkipHEVC {
		hevcLabel := styles.WarningText.Render(fmt.Sprintf("%d", hevcCount))
		if hevcCount == 0 {
			hevcLabel = styles.Value.Render("0")
		}
		b.WriteString(fmt.Sprintf("  • Skipped (already HEVC/H.265):    %s\n", hevcLabel))
	} else {
		b.WriteString(fmt.Sprintf("  • Skipped (already HEVC/H.265):    %s  (Skip HEVC is OFF in Settings)\n",
			styles.Subtext.Render("0")))
	}

	// Row: probe timeouts/errors (if any)
	if probeFailedCount > 0 {
		b.WriteString(fmt.Sprintf("  • Probe timeouts/unverified:       %s  (will transcode / verify locally)\n",
			styles.WarningText.Render(fmt.Sprintf("%d", probeFailedCount))))
	}

	// Row: videos to transcode & storage estimates
	b.WriteString("\n")
	if processCount > 0 {
		pct := 0.0
		if processBytes > 0 {
			pct = (float64(estSavedBytes) / float64(processBytes)) * 100
		}
		b.WriteString(fmt.Sprintf("  ✓ Will transcode:                   %s  (%s total)\n",
			styles.SuccessText.Render(fmt.Sprintf("%d videos", processCount)),
			formatByteSize(processBytes),
		))
		b.WriteString(fmt.Sprintf("  💾 Est. Storage Reclaimed:         %s  (~%.0f%% space saved)\n",
			styles.SuccessText.Render(fmt.Sprintf("~%s", formatByteSize(estSavedBytes))),
			pct,
		))
		b.WriteString(fmt.Sprintf("  📦 Est. Final Storage:             %s\n",
			styles.Value.Render(fmt.Sprintf("~%s", formatByteSize(estFinalBytes))),
		))
	} else {
		var reason string
		if selectedCount == 0 {
			reason = "no videos selected"
		} else if m.cfg.SkipHEVC && hevcCount == selectedCount {
			reason = fmt.Sprintf("all %d selected video(s) are already HEVC/H.265 (Skip HEVC is ON)", selectedCount)
		} else if m.cfg.SkipHEVC && hevcCount > 0 {
			reason = fmt.Sprintf("%d video(s) skipped as already HEVC", hevcCount)
		} else {
			reason = "no eligible videos found"
		}

		b.WriteString(fmt.Sprintf("  ℹ Will transcode:                   %s\n",
			styles.WarningText.Render(fmt.Sprintf("0 videos — nothing to do (%s)", reason))))

		if m.cfg.SkipHEVC && hevcCount > 0 {
			b.WriteString(fmt.Sprintf("    %s\n",
				styles.Subtext.Render("💡 Tip: Press [s] to open Settings and turn OFF 'Skip HEVC' if you wish to re-encode them.")))
		}
	}

	// ── HEVC list (if any and SkipHEVC on) ───────────────────────────────────
	if m.cfg.SkipHEVC && len(m.dryRunHEVC) > 0 {
		b.WriteString("\n")
		b.WriteString(styles.SectionHeader.Render("Already HEVC — will be skipped (0 MB downloaded):"))
		b.WriteString("\n")
		limit := len(m.dryRunHEVC)
		shown := limit
		if shown > 8 {
			shown = 8
		}
		for i := 0; i < shown; i++ {
			v := m.dryRunHEVC[i]
			detail := m.dryRunDetails[v.ID]
			res := detail.Resolution
			if res == "" {
				res = "1080p"
			}
			b.WriteString(fmt.Sprintf("  • %-26s  %-9s  %-26s  %s\n",
				truncate(v.OriginalFileName, 26),
				styles.Subtext.Render(formatByteSize(v.GetSize())),
				styles.Value.Render(fmt.Sprintf("[HEVC • %s]", res)),
				styles.Subtext.Render("✓ Already HEVC (0 MB saved — skipped)"),
			))
		}
		if limit > shown {
			b.WriteString(fmt.Sprintf("  %s\n", styles.Subtext.Render(fmt.Sprintf("  … and %d more", limit-shown))))
		}
	}

	// ── Transcode list (if any videos to process) ───────────────────────────
	if processCount > 0 {
		b.WriteString("\n")
		b.WriteString(styles.SectionHeader.Render("Will be transcoded:"))
		b.WriteString("\n")
		limit := len(m.dryRunToProcess)
		shown := limit
		if shown > 8 {
			shown = 8
		}
		for i := 0; i < shown; i++ {
			v := m.dryRunToProcess[i]
			orig := v.GetSize()
			detail := m.dryRunDetails[v.ID]
			codec := detail.Codec
			if codec == "" {
				codec = "H.264"
			}
			res := detail.Resolution
			if res == "" {
				res = "1080p"
			}
			targetRes, _ := resolveTargetResolution(res, m.cfg.Resolution)

			_, itemSaved := estimateSavings(orig, detail, m.cfg.Resolution, failedMap[v.ID])

			transcodeBadge := fmt.Sprintf("[%s • %s] ➔ [HEVC • %s]", codec, res, targetRes)
			var itemEst string
			if itemSaved > 0 {
				itemEst = fmt.Sprintf("~%s saved", formatByteSize(itemSaved))
			} else {
				itemEst = "0 MB saved"
			}

			extra := ""
			if detail.IsHEVC && !m.cfg.SkipHEVC {
				extra = " " + styles.WarningText.Render("⚠️ Already HEVC (0 MB saved)")
			} else if failedMap[v.ID] {
				extra = " " + styles.WarningText.Render("(probe unverified)")
			}

			b.WriteString(fmt.Sprintf("  • %-26s  %-9s  %-26s  %s%s\n",
				truncate(v.OriginalFileName, 26),
				styles.Subtext.Render(formatByteSize(orig)),
				styles.Value.Render(transcodeBadge),
				styles.SuccessText.Render(itemEst),
				extra,
			))
		}
		if limit > shown {
			b.WriteString(fmt.Sprintf("  %s\n", styles.Subtext.Render(fmt.Sprintf("  … and %d more", limit-shown))))
		}
	}

	b.WriteString("\n")
	// ── Footer ────────────────────────────────────────────────────────────────
	if processCount > 0 {
		b.WriteString(styles.ActiveButton.Render("[ Enter ] Start Optimization") + "  " +
			styles.Button.Render("[ s ] Settings") + "  " +
			styles.Button.Render("[ Esc / B ] Back to Browser"))
	} else {
		b.WriteString(styles.Button.Render("[ s ] Settings") + "  " +
			styles.Button.Render("[ Enter / Esc / B ] Back to Browser"))
	}

	return b.String()
}

func (m *AppModel) viewResumePrompt() string {

	b := strings.Builder{}
	b.WriteString(styles.Title.Render("🎬 Resume Previous Session"))
	b.WriteString("\n\n")

	b.WriteString(styles.SectionHeader.Render("A paused optimization session was found:"))
	b.WriteString("\n")
	if m.session != nil {
		b.WriteString(fmt.Sprintf("  • Session Started:    %s\n", m.session.Date.Format("2006-01-02 15:04:05")))
		b.WriteString(fmt.Sprintf("  • Completed Videos:   %d\n", m.session.CompletedCount()))
		b.WriteString(fmt.Sprintf("  • Space Saved So Far: %.2f MB\n", float64(m.session.GetTotalSavedBytes())/(1024*1024)))
		b.WriteString(fmt.Sprintf("  • Pending Videos:     %s\n\n", styles.WarningText.Render(fmt.Sprintf("%d", m.session.PendingCount()))))
	}

	b.WriteString("Options:\n")
	b.WriteString("  [ R ] Resume Optimization (process remaining pending videos)\n")
	b.WriteString("  [ S ] Start New / Scan Library (discards paused session)\n")
	b.WriteString("  [ C ] Clear Saved State & Exit\n")
	b.WriteString("  [ Q ] Quit\n")

	return b.String()
}

func (m *AppModel) viewSettings() string {
	b := strings.Builder{}
	b.WriteString(styles.Title.Render("⚙ Settings & Preferences"))
	b.WriteString("\n\n")
	b.WriteString("Use [↑/↓] to navigate, [←/→] to change options, [Enter/Space] to toggle/select.\n\n")

	// descriptions shown under the focused row
	descriptions := []string{
		// 0 — Resolution
		"Controls the maximum output resolution.\n" +
			"  • Original — keeps the same dimensions as the source (recommended)\n" +
			"  • 4K / 1440p / 1080p / 720p — downscales if the source is larger; never upscales",
		// 1 — Preset
		"Controls the trade-off between encoding speed and output file size.\n" +
			"  • Ultrafast — finishes quickly; slightly larger files\n" +
			"  • Fast      — good balance for most cases\n" +
			"  • Medium    — default; solid compression\n" +
			"  • Slow      — best compression; takes longer",
		// 2 — Size Guard
		func() string {
			if m.cfg.SizeGuard {
				return "ON  — If the transcoded file ends up larger than the original, the replacement\n" +
					"       is skipped and the original is kept untouched. Prevents accidental bloat."
			}
			return "OFF — The transcoded file will always replace the original, even if it is larger.\n" +
				"       Use this only if you need a specific codec regardless of file size."
		}(),
		// 3 — Skip HEVC
		func() string {
			if m.cfg.SkipHEVC {
				return "ON  — Videos already encoded in HEVC/H.265 format are skipped to avoid\n" +
					"       unnecessary re-encoding."
			}
			return "OFF — All eligible videos will be processed and re-encoded, even if already HEVC."
		}(),
		// 4 — Pipeline Prefetch
		func() string {
			if m.cfg.EnablePrefetch {
				return "ON  — Downloads the next video in background while transcoding current video.\n" +
					"       Eliminates download wait time without exceeding disk bounds (max 1 file)."
			}
			return "OFF — Sequential mode. Downloads, transcodes, and uploads strictly one at a time."
		}(),
		// 5 — Concurrent Encoders
		"Controls how many ffmpeg instances encode simultaneously.\n" +
			"  • 1 — Default. Best for hardware encoders (VideoToolbox / NVENC) and CPU thermals\n" +
			"  • 2 — Parallel encoding. High throughput on multi-core systems",
		// 6 — Threshold (MB/min)
		"Minimum size per minute of video (MB/min). Videos with a bitrate density below\n" +
			"  this threshold are skipped entirely (e.g. 100 MB/min).\n" +
			"  Filters out low-bitrate videos regardless of video duration.",
		// 7 — Log Level
		"Controls detail of output logs. Set to Debug for full FFmpeg & ExifTool command lines,\n" +
			"  stderr outputs, and diagnostic traces to identify error root causes.\n" +
			"  Logs written to: ~/.config/immich-video-optimizer/optimizer.log",
		// 8 — Network Retries
		"Number of automatic retry attempts with backoff for failed downloads and uploads.\n" +
			"  • 0 — Disabled. Fails immediately on first error\n" +
			"  • 1 to 5 — Retries automatically with exponential backoff on transient network drops",
	}

	isFocused := func(idx int) bool { return m.settingCursor == idx }

	cursor := func(idx int) string {
		if isFocused(idx) {
			return "> "
		}
		return "  "
	}

	writeRow := func(idx int, label, valueStr string) {
		b.WriteString(cursor(idx) + label + styles.Value.Render(valueStr) + "\n")
		if isFocused(idx) {
			b.WriteString(styles.Subtext.Render("    └ "+descriptions[idx]) + "\n")
		}
		b.WriteString("\n")
	}

	// Row 0 — Resolution
	writeRow(0,
		"Target Resolution:   ",
		m.cfg.Resolution+"  ← / → to change  [Original, 4K, 1440p, 1080p, 720p]",
	)

	// Row 1 — Preset
	writeRow(1,
		"Transcoding Preset:  ",
		m.cfg.Preset+"  ← / → to change  [Ultrafast, Fast, Medium, Slow]",
	)

	// Row 2 — Size Guard
	sgState := "[✓] ON"
	if !m.cfg.SizeGuard {
		sgState = "[ ] OFF"
	}
	writeRow(2,
		"Size Guard:          ",
		sgState+"  [Space / Enter] to toggle",
	)

	// Row 3 — Skip HEVC
	hevcState := "[✓] ON"
	if !m.cfg.SkipHEVC {
		hevcState = "[ ] OFF"
	}
	writeRow(3,
		"Skip HEVC Videos:    ",
		hevcState+"  [Space / Enter] to toggle",
	)

	// Row 4 — Pipeline Prefetch
	prefetchState := "[✓] ON"
	if !m.cfg.EnablePrefetch {
		prefetchState = "[ ] OFF"
	}
	writeRow(4,
		"Pipeline Prefetch:   ",
		prefetchState+"  [Space / Enter] to toggle",
	)

	// Row 5 — Concurrent Encoders
	encCount := m.cfg.ConcurrentEncoders
	if encCount <= 0 {
		encCount = 1
	}
	writeRow(5,
		"Concurrent Encoders: ",
		fmt.Sprintf("%d  ← / → to change  [1, 2]", encCount),
	)

	// Row 6 — Threshold (MB/min) (typeable)
	b.WriteString(cursor(6) + "Threshold (MB/min):  ")
	if isFocused(6) && m.thresholdInput.Focused() {
		b.WriteString(m.thresholdInput.View() + "  " + styles.Subtext.Render("[Enter] to confirm") + "\n")
		b.WriteString(styles.Subtext.Render("    └ "+descriptions[6]) + "\n")
	} else if isFocused(6) {
		b.WriteString(styles.Value.Render(m.cfg.SizeThreshold+" MB/min") + "  " + styles.Subtext.Render("[Enter] to edit") + "\n")
		b.WriteString(styles.Subtext.Render("    └ "+descriptions[6]) + "\n")
	} else {
		b.WriteString(styles.Value.Render(m.cfg.SizeThreshold+" MB/min") + "\n")
	}
	b.WriteString("\n")

	// Row 7 — Log Level
	logLevelStr := "INFO"
	if m.cfg.IsDebug() {
		logLevelStr = "DEBUG"
	}
	writeRow(7,
		"Logging Level:       ",
		logLevelStr+"  ← / → / [Space / Enter] to toggle  [Info, Debug]",
	)

	// Row 8 — Network Retries
	retryStr := fmt.Sprintf("%d", m.cfg.MaxRetries)
	if m.cfg.MaxRetries == 0 {
		retryStr = "0 (Disabled)"
	}
	writeRow(8,
		"Network Retries:     ",
		retryStr+"  ← / → / [Space / Enter] to change  [0, 1, 2, 3, 5]",
	)

	b.WriteString("\n")
	b.WriteString(styles.Subtext.Render("[Esc] Save & Return"))
	return b.String()
}

func (m *AppModel) viewDashboard() string {
	b := strings.Builder{}
	b.WriteString(styles.Title.Render("🎬 Live Optimization Dashboard"))
	b.WriteString("\n\n")

	// Overall Progress
	b.WriteString(styles.SectionHeader.Render("Overall Batch Progress:"))
	b.WriteString("\n")
	b.WriteString(m.overallProgress.View())
	b.WriteString(fmt.Sprintf("\nVideos Completed: %d / %d   |   Total Disk Space Saved: %s\n\n",
		m.processedVideos,
		m.totalVideos,
		styles.SuccessText.Render(fmt.Sprintf("%.2f MB", float64(m.totalSavedBytes)/(1024*1024))),
	))

	// Pipeline Status
	b.WriteString(styles.SectionHeader.Render("Pipeline Status:"))
	b.WriteString("\n")
	if m.probingStatus != "" {
		b.WriteString(fmt.Sprintf("  🔎 %s: %s\n", styles.Value.Render("Probing"), m.probingStatus))
	}

	tName := m.currentTranscodeName
	if tName == "" && m.currentMetadataName == "" && m.currentUploadName == "" && m.currentFinalizeName == "" {
		tName = m.currentVideoName
	}
	if tName != "" {
		b.WriteString(fmt.Sprintf("  ⚙ Transcoding: %s", styles.Value.Render(tName)))
		if m.currentVideoOriginalSize > 0 || m.currentVideoResolution != "" {
			b.WriteString(fmt.Sprintf("  (%.1f MB, %s)", float64(m.currentVideoOriginalSize)/(1024*1024), m.currentVideoResolution))
		}
		b.WriteString("\n")
		var parts []string
		if m.currentTranscodeTime != "" {
			parts = append(parts, fmt.Sprintf("Time: %s", styles.Value.Render(m.currentTranscodeTime)))
		}
		if m.speed != "" {
			parts = append(parts, fmt.Sprintf("Speed: %s", styles.Value.Render(m.speed)))
		}
		if m.fps > 0 {
			parts = append(parts, fmt.Sprintf("FPS: %.1f", m.fps))
		}
		if len(parts) > 0 {
			b.WriteString(fmt.Sprintf("                 %s  ● Encoding\n", strings.Join(parts, "  |  ")))
		}
	} else if m.currentMetadataName != "" {
		b.WriteString(fmt.Sprintf("  🏷️ Metadata:    %s  %s\n",
			styles.Value.Render(m.currentMetadataName),
			styles.Subtext.Render("(copying GPS, timestamps & tags via ExifTool...)"),
		))
	} else {
		b.WriteString("  ⚙ Transcoding: idle\n")
	}

	if m.cfg.EnablePrefetch {
		if m.currentDownloadName != "" {
			b.WriteString(fmt.Sprintf("  ↓ Prefetching: %s\n", styles.Value.Render(m.currentDownloadName)))
		} else {
			b.WriteString("  ↓ Prefetching: idle\n")
		}
	}

	if m.currentUploadName != "" {
		b.WriteString(fmt.Sprintf("  ↑ Uploading:   %s  %s\n",
			styles.Value.Render(m.currentUploadName),
			styles.Subtext.Render("(streaming replacement to Immich server...)"),
		))
	} else if m.currentFinalizeName != "" {
		b.WriteString(fmt.Sprintf("  ✓ Finalizing:  %s  %s\n",
			styles.Value.Render(m.currentFinalizeName),
			styles.Subtext.Render("(replacing original asset on Immich server...)"),
		))
	} else {
		b.WriteString("  ↑ Uploading:   idle\n")
	}
	b.WriteString("\n")

	// Activity Log
	b.WriteString(styles.SectionHeader.Render("Recent Activity:"))
	b.WriteString("\n")
	start := 0
	if len(m.logMessages) > 6 {
		start = len(m.logMessages) - 6
	}
	for i := start; i < len(m.logMessages); i++ {
		b.WriteString("  " + m.logMessages[i] + "\n")
	}

	b.WriteString("\n")
	b.WriteString(styles.Subtext.Render("[q] Pause — cancels current video and saves progress for resume"))
	return b.String()
}

func (m *AppModel) viewSummary() string {
	b := strings.Builder{}
	b.WriteString(styles.Title.Render("🎉 Optimization Summary"))
	b.WriteString("\n\n")

	// ── Counts ──────────────────────────────────────────────────────────────
	successCount := m.processedVideos - m.failedCount - m.skippedCount
	if successCount < 0 {
		successCount = 0
	}

	b.WriteString(styles.SectionHeader.Render("Batch Run Results:"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  • Videos Selected:     %d\n", m.totalVideos))
	b.WriteString(fmt.Sprintf("  • Transcoded & Uploaded: %s  (re-encoded and replaced in Immich)\n",
		styles.SuccessText.Render(fmt.Sprintf("%d", successCount))))

	// ── Skip breakdown ───────────────────────────────────────────────────────
	if m.skippedCount > 0 {
		// Tally individual skip reasons
		reasonCounts := make(map[string]int)
		for _, sv := range m.skippedVideos {
			reasonCounts[sv.Reason]++
		}
		b.WriteString(fmt.Sprintf("  • Skipped (total):     %d\n", m.skippedCount))
		for reason, count := range reasonCounts {
			label := skipReasonLabel(reason)
			b.WriteString(fmt.Sprintf("      ↳ %-40s %d\n", label, count))
		}
	} else {
		b.WriteString(fmt.Sprintf("  • Skipped:             %d\n", m.skippedCount))
	}

	b.WriteString(fmt.Sprintf("  • Failures / Errors:   %d\n", m.failedCount))
	b.WriteString(fmt.Sprintf("  • Total Space Saved:   %s\n\n",
		styles.SuccessText.Render(fmt.Sprintf("%.2f MB", float64(m.totalSavedBytes)/(1024*1024))),
	))

	// ── Status line ─────────────────────────────────────────────────────────
	switch {
	case m.failedCount > 0 && successCount == 0 && m.skippedCount == 0:
		b.WriteString(styles.ErrorText.Render("✗ All videos failed to process."))
	case m.failedCount > 0:
		b.WriteString(styles.WarningText.Render(fmt.Sprintf("⚠ %d video(s) completed with errors.", m.failedCount)))
	case m.skippedCount > 0 && successCount == 0:
		b.WriteString(styles.WarningText.Render("ℹ All videos were skipped — no replacements were uploaded to Immich."))
	case m.skippedCount > 0:
		b.WriteString(styles.SuccessText.Render(fmt.Sprintf("✓ %d video(s) optimized. %d skipped (originals kept).", successCount, m.skippedCount)))
	default:
		b.WriteString(styles.SuccessText.Render(fmt.Sprintf("✓ All %d video(s) successfully transcoded and uploaded to Immich!", successCount)))
	}
	b.WriteString("\n\n")

	// ── Detailed breakdown ───────────────────────────────────────────────────
	hasDetails := len(m.transcodedVideos) > 0 || len(m.failedVideos) > 0 || len(m.skippedVideos) > 0
	if hasDetails {
		b.WriteString(styles.SectionHeader.Render("Detailed Breakdown:"))
		b.WriteString("\n")

		if len(m.transcodedVideos) > 0 {
			b.WriteString(styles.SuccessText.Render("  Transcoded & Uploaded:"))
			b.WriteString("\n")
			for _, tv := range m.transcodedVideos {
				pct := 0.0
				if tv.OriginalSize > 0 {
					pct = (float64(tv.SavedBytes) / float64(tv.OriginalSize)) * 100.0
				}
				var savedBadge string
				if tv.SavedBytes >= 0 {
					savedBadge = fmt.Sprintf("saved %s, -%.1f%%", formatByteSize(tv.SavedBytes), pct)
				} else {
					savedBadge = fmt.Sprintf("+%s, +%.1f%%", formatByteSize(-tv.SavedBytes), -pct)
				}
				b.WriteString(fmt.Sprintf("    • %-32s  %s ➔ %s  (%s)\n",
					truncate(tv.Name, 32),
					formatByteSize(tv.OriginalSize),
					formatByteSize(tv.NewSize),
					styles.SuccessText.Render(savedBadge),
				))
			}
			b.WriteString("\n")
		}

		if len(m.failedVideos) > 0 {
			b.WriteString(styles.ErrorText.Render("  Failures:"))
			b.WriteString("\n")
			for _, f := range m.failedVideos {
				errCategory := classifyFailureReason(f.Err)
				b.WriteString(fmt.Sprintf("    • %-45s  [%s]\n", truncate(f.Name, 45), errCategory))
			}
			b.WriteString(styles.Subtext.Render("  → Check ~/.config/immich-video-optimizer/optimizer.log for full error details."))
			b.WriteString("\n\n")
		}

		if len(m.skippedVideos) > 0 {
			b.WriteString(styles.WarningText.Render("  Skipped (originals kept, nothing uploaded):"))
			b.WriteString("\n")
			for _, sv := range m.skippedVideos {
				label := skipReasonLabel(sv.Reason)
				b.WriteString(fmt.Sprintf("    • %-45s  [%s]\n", truncate(sv.Name, 45), label))
			}
			b.WriteString("\n")
		}
	}

	// ── Footer ───────────────────────────────────────────────────────────────
	if len(m.failedVideos) > 0 {
		b.WriteString("[ R ] Retry Failed Videos  |  [ Enter / Q ] Exit")
	} else {
		b.WriteString("[ Enter / Q ] Exit")
	}

	return b.String()
}

// skipReasonLabel converts internal skip reason strings into human-readable labels.
func skipReasonLabel(reason string) string {
	switch reason {
	case "transcoded file larger":
		return "Size Guard: transcoded > original, original kept"
	case "already HEVC":
		return "Already HEVC/H.265, re-encoding skipped"
	default:
		return reason
	}
}

// classifyFailureReason maps pipeline error strings to short human-readable categories.
func classifyFailureReason(errMsg string) string {
	switch {
	case strings.Contains(errMsg, "download") || strings.Contains(errMsg, "DownloadAsset"):
		return "Download Error"
	case strings.Contains(errMsg, "transcode failed"):
		return "Transcode Error"
	case strings.Contains(errMsg, "metadata copy failed"):
		return "Metadata Error"
	case strings.Contains(errMsg, "upload failed"):
		return "Upload Error"
	case strings.Contains(errMsg, "stat transcoded"):
		return "Filesystem Error"
	default:
		return "Error"
	}
}

// truncate shortens s to at most n runes, appending "…" if truncated.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

func formatByteSize(b int64) string {
	if b < 0 {
		b = 0
	}
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	val := float64(b) / float64(div)
	switch exp {
	case 0:
		return fmt.Sprintf("%.1f KB", val)
	case 1:
		return fmt.Sprintf("%.1f MB", val)
	case 2:
		return fmt.Sprintf("%.2f GB", val)
	default:
		return fmt.Sprintf("%.2f TB", val)
	}
}

// parseResolutionHeight extracts vertical pixel height from resolution label.
func parseResolutionHeight(res string) int {
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

// resolveTargetResolution computes the actual target resolution string and whether
// genuine downscaling will occur given the input video resolution and user setting.
func resolveTargetResolution(inputRes, targetSetting string) (string, bool) {
	if inputRes == "" {
		inputRes = "1080p"
	}
	if targetSetting == "" || strings.EqualFold(targetSetting, "original") {
		return inputRes, false
	}
	inH := parseResolutionHeight(inputRes)
	outH := parseResolutionHeight(targetSetting)
	if inH > 0 && outH > 0 && inH > outH {
		return targetSetting, true
	}
	// Never upscales; if input height is <= target height, preserve input resolution
	return inputRes, false
}

// estimateSavings calculates a conservative, realistic ratio and saved byte count
// for a video asset based on probed codec, resolution, and target settings.
func estimateSavings(orig int64, detail engine.ProbeDetails, targetResSetting string, isProbeFailed bool) (float64, int64) {
	if isProbeFailed || detail.IsHEVC || detail.Codec == "" {
		return 1.0, 0
	}

	_, isDownscaling := resolveTargetResolution(detail.Resolution, targetResSetting)

	var ratio float64
	switch detail.Codec {
	case "ProRes":
		if isDownscaling {
			ratio = 0.20
		} else {
			ratio = 0.30
		}
	case "MJPEG":
		ratio = 0.40
	case "MPEG-4", "VP8":
		if isDownscaling {
			ratio = 0.50
		} else {
			ratio = 0.70
		}
	case "VP9", "AV1":
		ratio = 1.0
	case "H.264":
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
