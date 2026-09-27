package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"

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
	b.WriteString(fmt.Sprintf("  • Size Threshold: %s (Videos larger than this will be scanned)\n", styles.Value.Render(m.cfg.SizeThreshold+" MB")))
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

	b.WriteString(styles.ActiveButton.Render("[ Enter / S ] Scan Immich Library") + "\n\n")
	b.WriteString(styles.Button.Render("[ C ] Settings") + "  " + styles.Button.Render("[ E ] Edit Server Credentials") + "  " + styles.Button.Render("[ Q ] Quit") + "\n")

	return b.String()
}

func (m *AppModel) viewScanning() string {
	b := strings.Builder{}
	b.WriteString(styles.Title.Render("🎬 Immich Video Optimizer"))
	b.WriteString("\n\n")
	b.WriteString(styles.SectionHeader.Render("🔍 Scanning Immich Library..."))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("  Querying assets on %s\n", styles.Value.Render(m.cfg.URL)))
	b.WriteString(fmt.Sprintf("  Inspecting videos exceeding %s MB...\n\n", styles.Value.Render(m.cfg.SizeThreshold)))
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
	b.WriteString(fmt.Sprintf("  • Videos skipped (below %s MB):      %d\n", m.cfg.SizeThreshold, r.SkippedBelowSize))
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
		b.WriteString(styles.WarningText.Render("ℹ No videos found exceeding the " + m.cfg.SizeThreshold + " MB threshold."))
		b.WriteString("\n\nAll videos in your library are already within the target size limit.\n\n")
		b.WriteString("Actions:\n")
		b.WriteString("  [ C ] Adjust Size Threshold in Settings\n")
		b.WriteString("  [ S ] Rescan Library\n")
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

	b.WriteString(styles.Subtext.Render("Shortcuts: [Space] Toggle | [a] All | [n] None | [/] Search\n           [c] Settings   | [s] Rescan | [Enter] Start | [q] Quit"))
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
		// 6 — Size Threshold
		"Minimum file size a video must exceed before it is included in the scan.\n" +
			"  Videos smaller than this threshold are skipped entirely.\n" +
			"  A good starting point is 500 MB. Raise it to skip already-small videos.",
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

	// Row 6 — Size Threshold (typeable)
	b.WriteString(cursor(6) + "Size Threshold (MB): ")
	if isFocused(6) && m.thresholdInput.Focused() {
		b.WriteString(m.thresholdInput.View() + "  " + styles.Subtext.Render("[Enter] to confirm") + "\n")
		b.WriteString(styles.Subtext.Render("    └ "+descriptions[6]) + "\n")
	} else if isFocused(6) {
		b.WriteString(styles.Value.Render(m.cfg.SizeThreshold+" MB") + "  " + styles.Subtext.Render("[Enter] to edit") + "\n")
		b.WriteString(styles.Subtext.Render("    └ "+descriptions[6]) + "\n")
	} else {
		b.WriteString(styles.Value.Render(m.cfg.SizeThreshold+" MB") + "\n")
	}

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
	if tName == "" {
		tName = m.currentVideoName
	}
	if tName != "" {
		b.WriteString(fmt.Sprintf("  ⚙ Transcoding: %s", styles.Value.Render(tName)))
		if m.currentVideoOriginalSize > 0 || m.currentVideoResolution != "" {
			b.WriteString(fmt.Sprintf("  (%.1f MB, %s)", float64(m.currentVideoOriginalSize)/(1024*1024), m.currentVideoResolution))
		}
		b.WriteString("\n")
		if m.speed != "" {
			b.WriteString(fmt.Sprintf("                 Speed: %s  |  FPS: %.1f\n", styles.Value.Render(m.speed), m.fps))
		}
	} else {
		b.WriteString("  ⚙ Transcoding: idle / preparing...\n")
	}

	if m.cfg.EnablePrefetch {
		if m.currentDownloadName != "" {
			b.WriteString(fmt.Sprintf("  ↓ Prefetching: %s\n", styles.Value.Render(m.currentDownloadName)))
		} else {
			b.WriteString("  ↓ Prefetching: idle\n")
		}
	}

	if m.currentUploadName != "" {
		b.WriteString(fmt.Sprintf("  ↑ Uploading:   %s\n", styles.Value.Render(m.currentUploadName)))
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

	b.WriteString(styles.SectionHeader.Render("Batch Run Results:"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  • Videos Processed:    %d\n", m.processedVideos))
	b.WriteString(fmt.Sprintf("  • Size Guard Skipped:  %d (transcoded video was larger than original)\n", m.skippedCount))
	b.WriteString(fmt.Sprintf("  • Failures / Errors:   %d\n", m.failedCount))
	b.WriteString(fmt.Sprintf("  • Total Space Saved:   %s\n\n",
		styles.SuccessText.Render(fmt.Sprintf("%.2f MB", float64(m.totalSavedBytes)/(1024*1024))),
	))

	if len(m.failedVideos) > 0 {
		b.WriteString(styles.ErrorText.Render("⚠️ Failed Videos:"))
		b.WriteString("\n")
		for _, f := range m.failedVideos {
			b.WriteString("  • " + f + "\n")
		}
		b.WriteString(styles.Subtext.Render("Check ~/.config/immich-video-optimizer/failures.log for details.") + "\n\n")
		b.WriteString("[ R ] Retry Failed Videos  |  [ Enter / Q ] Exit")
	} else {
		b.WriteString(styles.SuccessText.Render("✓ All items optimized successfully without errors!"))
		b.WriteString("\n\n[ Enter / Q ] Exit")
	}

	return b.String()
}
