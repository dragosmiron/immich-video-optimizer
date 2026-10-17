package tui

import (
	"fmt"

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
	case ViewDryRun:
		s = m.viewDryRun()
	case ViewSettings:
		s = m.viewSettings()
	case ViewDashboard:
		s = m.viewDashboard()
	case ViewSummary:
		s = m.viewSummary()
	}

	if banner := m.renderUpdateBanner(); banner != "" {
		s = banner + s
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

func (m *AppModel) renderUpdateBanner() string {
	if m.updateInfo == nil || !m.updateInfo.HasUpdate {
		return ""
	}

	badge := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.Color("#F59E0B")).
		Padding(0, 1).
		Render("🔔 UPDATE AVAILABLE")

	info := lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#0F172A", Dark: "#F8FAFC"}).
		Bold(true).
		Render(fmt.Sprintf("%s → %s", m.updateInfo.CurrentVersion, m.updateInfo.LatestVersion))

	url := lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#0284C7", Dark: "#38BDF8"}).
		Render(m.updateInfo.ReleaseURL)

	return fmt.Sprintf("%s  %s  (%s)\n\n", badge, info, url)
}
