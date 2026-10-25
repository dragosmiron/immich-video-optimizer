package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"immich-video-optimizer/config"
	"immich-video-optimizer/engine"
	"immich-video-optimizer/tui/styles"
)

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
		_, saved := engine.EstimateSavings(orig, detail, m.cfg.Resolution, failedMap[v.ID])
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
		var belowText string
		switch m.cfg.ThresholdMode {
		case config.ThresholdModeTotalMB:
			belowText = fmt.Sprintf("  • Skipped at scan (below %s MB):     %s  (never shown in browser)\n",
				m.cfg.SizeThreshold,
				styles.WarningText.Render(fmt.Sprintf("%d", belowThreshold)))
		default:
			belowText = fmt.Sprintf("  • Skipped at scan (below %s MB/min): %s  (never shown in browser)\n",
				m.cfg.SizeThreshold,
				styles.WarningText.Render(fmt.Sprintf("%d", belowThreshold)))
		}
		b.WriteString(belowText)
	}

	// Row: partner-shared (filtered at scan time — not in the selection)
	if m.scanResult != nil && m.scanResult.SkippedNotOwned > 0 {
		b.WriteString(fmt.Sprintf("  • Skipped at scan (partner shared):  %s  (never shown in browser)\n",
			styles.WarningText.Render(fmt.Sprintf("%d", m.scanResult.SkippedNotOwned))))
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
		if estSavedBytes > 0 {
			b.WriteString(fmt.Sprintf("  💾 Est. Storage Reclaimed:         %s  (~%.0f%% space saved)\n",
				styles.SuccessText.Render(fmt.Sprintf("~%s", formatByteSize(estSavedBytes))),
				pct,
			))
			b.WriteString(fmt.Sprintf("  📦 Est. Final Storage:             %s\n",
				styles.Value.Render(fmt.Sprintf("~%s", formatByteSize(estFinalBytes))),
			))
		} else {
			b.WriteString(fmt.Sprintf("  💾 Est. Storage Reclaimed:         %s\n",
				styles.Subtext.Render("potentially minimal savings"),
			))
			b.WriteString(fmt.Sprintf("  📦 Est. Final Storage:             %s\n",
				styles.Value.Render(fmt.Sprintf("~%s", formatByteSize(processBytes))),
			))
		}
		if m.dryRunAlbumName != "" {
			b.WriteString(fmt.Sprintf("  📁 Album created in Immich:        %s  %s\n",
				styles.Value.Render(m.dryRunAlbumName),
				styles.Subtext.Render("(you can open Immich to inspect these videos)"),
			))
		}
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
			targetRes, _ := engine.ResolveTargetResolution(res, m.cfg.Resolution)

			_, itemSaved := engine.EstimateSavings(orig, detail, m.cfg.Resolution, failedMap[v.ID])

			transcodeBadge := fmt.Sprintf("[%s • %s] ➔ [HEVC • %s]", codec, res, targetRes)
			var itemEst string
			var extra string

			if detail.IsHEVC && !m.cfg.SkipHEVC {
				if itemSaved > 0 {
					itemEst = styles.SuccessText.Render(fmt.Sprintf("~%s saved", formatByteSize(itemSaved)))
				} else {
					itemEst = styles.Subtext.Render("potentially minimal savings")
				}
				extra = " " + styles.WarningText.Render("⚠️ Already HEVC")
			} else {
				if itemSaved > 0 {
					itemEst = styles.SuccessText.Render(fmt.Sprintf("~%s saved", formatByteSize(itemSaved)))
				} else {
					itemEst = styles.Subtext.Render("0 MB saved")
				}
				if failedMap[v.ID] {
					extra = " " + styles.WarningText.Render("(probe unverified)")
				}
			}

			b.WriteString(fmt.Sprintf("  • %-26s  %-9s  %-26s  %s%s\n",
				truncate(v.OriginalFileName, 26),
				styles.Subtext.Render(formatByteSize(orig)),
				styles.Value.Render(transcodeBadge),
				itemEst,
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

func (m *AppModel) updateDryRun(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "b", "B":
		// Cancel the in-progress probe and go back to the browser
		if m.dryRunCancel != nil {
			m.dryRunCancel()
		}
		m.state = ViewBrowser
		return m, nil

	case "s", "S":
		if m.dryRunCancel != nil {
			m.dryRunCancel()
		}
		m.state = ViewSettings
		m.settingsTab = 0
		m.settingCursor = 0
		return m, nil

	case "enter":
		// Only allow proceeding once the dry run probe is complete
		if !m.dryRunDone {
			return m, nil
		}
		toProcess := m.dryRunToProcess
		if len(toProcess) == 0 {
			// Nothing to transcode — go back to browser
			m.state = ViewBrowser
			return m, nil
		}
		return m.startOptimization(toProcess)
	}
	return m, nil
}

