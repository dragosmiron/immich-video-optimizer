package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"immich-video-optimizer/tui/styles"
)

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

	numEncoders := m.cfg.ConcurrentEncoders
	if numEncoders <= 0 {
		numEncoders = 1
	} else if numEncoders > 2 {
		numEncoders = 2
	}

	if numEncoders > 1 {
		for wID := 1; wID <= numEncoders; wID++ {
			w := m.getWorker(wID)
			if w != nil && w.Stage == "transcoding" && w.VideoName != "" {
				b.WriteString(fmt.Sprintf("  ⚙ Transcoder %d: %s", wID, styles.Value.Render(w.VideoName)))
				if w.OriginalSize > 0 || w.Resolution != "" {
					b.WriteString(fmt.Sprintf("  (%.1f MB, %s)", float64(w.OriginalSize)/(1024*1024), w.Resolution))
				}
				if w.UsedCPUFallback {
					b.WriteString("  " + styles.WarningText.Render("libx265 (CPU Fallback)"))
				}
				b.WriteString("\n")
				var parts []string
				if w.Time != "" {
					tStr := w.Time
					if idx := strings.IndexAny(tStr, ".,"); idx != -1 {
						tStr = tStr[:idx]
					}
					parts = append(parts, fmt.Sprintf("Time: %s", styles.Value.Render(tStr)))
				}
				if w.Speed != "" {
					parts = append(parts, fmt.Sprintf("Speed: %s", styles.Value.Render(w.Speed)))
				}
				if w.FPS > 0 {
					parts = append(parts, fmt.Sprintf("FPS: %.1f", w.FPS))
				}
				pulse := m.renderEncodingPulseForFrame(w.PulseFrame)
				if len(parts) > 0 {
					b.WriteString(fmt.Sprintf("                   %s  %s\n", strings.Join(parts, "  |  "), pulse))
				} else {
					b.WriteString(fmt.Sprintf("                   %s\n", pulse))
				}
			} else if w != nil && w.Stage == "metadata" && w.VideoName != "" {
				b.WriteString(fmt.Sprintf("  🏷️ Metadata %d:   %s  %s\n",
					wID,
					styles.Value.Render(w.VideoName),
					styles.Subtext.Render("(copying GPS, timestamps & tags via ExifTool...)"),
				))
			} else {
				b.WriteString(fmt.Sprintf("  ⚙ Transcoder %d: idle\n", wID))
			}
		}
	} else {
		tName := m.currentTranscodeName
		if tName == "" && m.currentMetadataName == "" && m.currentUploadName == "" && m.currentFinalizeName == "" {
			tName = m.currentVideoName
		}
		if tName != "" {
			b.WriteString(fmt.Sprintf("  ⚙ Transcoding: %s", styles.Value.Render(tName)))
			if m.currentVideoOriginalSize > 0 || m.currentVideoResolution != "" {
				b.WriteString(fmt.Sprintf("  (%.1f MB, %s)", float64(m.currentVideoOriginalSize)/(1024*1024), m.currentVideoResolution))
			}
			w := m.getWorker(1)
			if w != nil && w.UsedCPUFallback {
				b.WriteString("  " + styles.WarningText.Render("libx265 (CPU Fallback)"))
			}
			b.WriteString("\n")
			var parts []string
			if m.currentTranscodeTime != "" {
				tStr := m.currentTranscodeTime
				if idx := strings.IndexAny(tStr, ".,"); idx != -1 {
					tStr = tStr[:idx]
				}
				parts = append(parts, fmt.Sprintf("Time: %s", styles.Value.Render(tStr)))
			}
			if m.speed != "" {
				parts = append(parts, fmt.Sprintf("Speed: %s", styles.Value.Render(m.speed)))
			}
			if m.fps > 0 {
				parts = append(parts, fmt.Sprintf("FPS: %.1f", m.fps))
			}
			if len(parts) > 0 {
				b.WriteString(fmt.Sprintf("                 %s  %s\n", strings.Join(parts, "  |  "), m.renderEncodingPulse()))
			} else {
				b.WriteString(fmt.Sprintf("                 %s\n", m.renderEncodingPulse()))
			}
		} else if m.currentMetadataName != "" {
			b.WriteString(fmt.Sprintf("  🏷️ Metadata:    %s  %s\n",
				styles.Value.Render(m.currentMetadataName),
				styles.Subtext.Render("(copying GPS, timestamps & tags via ExifTool...)"),
			))
		} else {
			b.WriteString("  ⚙ Transcoding: idle\n")
		}
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

func (m *AppModel) renderEncodingPulseForFrame(frame int) string {
	spinnerFrames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	pulseColors := []lipgloss.TerminalColor{
		lipgloss.AdaptiveColor{Light: "#15803D", Dark: "#22C55E"}, // Green
		lipgloss.AdaptiveColor{Light: "#047857", Dark: "#10B981"}, // Emerald
		lipgloss.AdaptiveColor{Light: "#0E7490", Dark: "#06B6D4"}, // Cyan
		lipgloss.AdaptiveColor{Light: "#0369A1", Dark: "#38BDF8"}, // Sky blue
		lipgloss.AdaptiveColor{Light: "#1D4ED8", Dark: "#0EA5E9"}, // Blue
	}
	frameIdx := frame % len(spinnerFrames)
	colorIdx := frame % len(pulseColors)

	icon := spinnerFrames[frameIdx]
	style := lipgloss.NewStyle().Foreground(pulseColors[colorIdx]).Bold(true)
	return style.Render(fmt.Sprintf("%s Encoding", icon))
}

func (m *AppModel) renderEncodingPulse() string {
	return m.renderEncodingPulseForFrame(m.pulseFrame)
}

func (m *AppModel) updateDashboard(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		if m.cancelCtx != nil {
			m.cancelCtx()
		}
		// The pipeline will emit PipelineDoneMsg{Paused:true} shortly;
		// that handler will transition to ViewResumePrompt.
	}
	return m, nil
}
