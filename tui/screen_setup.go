package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"immich-video-optimizer/tui/styles"
)

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

func (m *AppModel) updateConnectionSetup(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "tab", "down":
		m.setupFocus = (m.setupFocus + 1) % 3
		m.updateSetupFocus()
		return m, nil

	case "shift+tab", "up":
		m.setupFocus = (m.setupFocus + 2) % 3
		m.updateSetupFocus()
		return m, nil

	case "enter":
		if m.setupFocus == 0 {
			m.setupFocus = 1
			m.updateSetupFocus()
			return m, nil
		}
		// On token field (1) or button (2), submit
		return m.submitConnectionSetup()

	case "q", "esc":
		if m.cfg.URL != "" && m.cfg.APIKey != "" {
			m.state = ViewScanPrompt
			return m, nil
		}
		return m, tea.Quit

	default:
		var cmd tea.Cmd
		if m.setupFocus == 0 {
			m.urlInput, cmd = m.urlInput.Update(msg)
		} else if m.setupFocus == 1 {
			m.tokenInput, cmd = m.tokenInput.Update(msg)
		}
		return m, cmd
	}
}

func (m *AppModel) updateSetupFocus() {
	m.urlInput.Blur()
	m.tokenInput.Blur()
	switch m.setupFocus {
	case 0:
		m.urlInput.Focus()
	case 1:
		m.tokenInput.Focus()
	}
}

func (m *AppModel) submitConnectionSetup() (tea.Model, tea.Cmd) {
	url := strings.TrimSpace(m.urlInput.Value())
	key := strings.TrimSpace(m.tokenInput.Value())

	if url == "" {
		m.connectionError = "Immich Server URL cannot be empty."
		m.setupFocus = 0
		m.updateSetupFocus()
		return m, nil
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		m.connectionError = "Server URL must start with http:// or https://"
		m.setupFocus = 0
		m.updateSetupFocus()
		return m, nil
	}
	if key == "" {
		m.connectionError = "Immich API Key cannot be empty."
		m.setupFocus = 1
		m.updateSetupFocus()
		return m, nil
	}

	m.cfg.URL = url
	m.cfg.APIKey = key
	m.pipeline.UpdateConfig(m.cfg)

	m.state = ViewTestingConnection
	m.connectionError = ""
	return m, testConnectionCmd(m.pipeline)
}
