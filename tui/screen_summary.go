package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"immich-video-optimizer/immich"
	"immich-video-optimizer/tui/styles"
)

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
				fallbackTag := ""
				if tv.Fallback.Used {
					if tv.Fallback.FailedEncoder != "" {
						fallbackTag = "  " + styles.WarningText.Render(fmt.Sprintf("[CPU Fallback: %s failed]", tv.Fallback.FailedEncoder))
					} else {
						fallbackTag = "  " + styles.WarningText.Render("[CPU Fallback]")
					}
				}
				b.WriteString(fmt.Sprintf("    • %-32s  %s ➔ %s  (%s)%s\n",
					truncate(tv.Name, 32),
					formatByteSize(tv.OriginalSize),
					formatByteSize(tv.NewSize),
					styles.SuccessText.Render(savedBadge),
					fallbackTag,
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
				if sv.Reason == "transcoded file larger" && sv.OptimizedSize >= sv.OriginalSize && sv.OriginalSize > 0 {
					diff := sv.OptimizedSize - sv.OriginalSize
					pct := (float64(diff) / float64(sv.OriginalSize)) * 100.0
					diffBadge := styles.WarningText.Render(fmt.Sprintf("+%s, +%.1f%%", formatByteSize(diff), pct))
					b.WriteString(fmt.Sprintf("    • %-32s  %s ➔ %s  (%s)  [Size Guard: original kept]\n",
						truncate(sv.Name, 32),
						formatByteSize(sv.OriginalSize),
						formatByteSize(sv.OptimizedSize),
						diffBadge,
					))
				} else {
					label := skipReasonLabel(sv.Reason)
					if sv.OriginalSize > 0 {
						b.WriteString(fmt.Sprintf("    • %-38s  %s  [%s]\n", truncate(sv.Name, 38), formatByteSize(sv.OriginalSize), label))
					} else {
						b.WriteString(fmt.Sprintf("    • %-45s  [%s]\n", truncate(sv.Name, 45), label))
					}
				}
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

func (m *AppModel) updateSummary(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "r", "R":
		// Retry failed videos
		if len(m.failedVideos) > 0 {
			var toRetry []immich.Asset
			for _, v := range m.videos {
				for _, f := range m.failedVideos {
					if (f.VideoID != "" && v.ID == f.VideoID) || (f.VideoID == "" && v.OriginalFileName == f.Name) {
						toRetry = append(toRetry, v)
						break
					}
				}
			}
			if len(toRetry) > 0 {
				m.videos = toRetry
				m.state = ViewDashboard
				m.totalVideos = len(toRetry)
				m.processedVideos = 0
				m.failedCount = 0
				m.failedVideos = nil
				m.ctx, m.cancelCtx = context.WithCancel(context.Background())
				m.pipelineChan = make(chan interface{}, 200)
				if m.pipeline != nil {
					go m.pipeline.Run(m.ctx, toRetry, m.pipelineChan)
				} else {
					close(m.pipelineChan)
				}
				return m, tea.Batch(waitForPipeline(m.pipelineChan), tickPulseCmd())
			}
		}
	case "enter", "q", "esc":
		return m, tea.Quit
	}
	return m, nil
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
