package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"immich-video-optimizer/config"
	"immich-video-optimizer/engine"
	"immich-video-optimizer/immich"
	"immich-video-optimizer/tui/styles"
)

func (m *AppModel) viewBrowser() string {
	b := strings.Builder{}
	b.WriteString(styles.Title.Render("🎬 Immich Video Optimizer - Video Browser"))
	b.WriteString("\n\n")

	b.WriteString(m.renderScanSummary())
	b.WriteString("\n")

	if len(m.videos) == 0 {
		var noVideosMsg string
		var subMsg string
		switch m.cfg.ThresholdMode {
		case config.ThresholdModeDisabled:
			noVideosMsg = "ℹ No videos found in your library."
			subMsg = "No video assets were discovered on the Immich server."
		case config.ThresholdModeTotalMB:
			noVideosMsg = fmt.Sprintf("ℹ No videos found exceeding the %s MB threshold.", m.cfg.SizeThreshold)
			subMsg = "All videos in your library are already within the target size limit."
		default:
			noVideosMsg = fmt.Sprintf("ℹ No videos found exceeding the %s MB/min threshold.", m.cfg.SizeThreshold)
			subMsg = "All videos in your library are already within the target size limit."
		}
		b.WriteString(styles.WarningText.Render(noVideosMsg))
		b.WriteString("\n\n" + subMsg + "\n\n")
		b.WriteString("Actions:\n")
		b.WriteString("  [ S ] Adjust Filtering Settings\n")
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

func (m *AppModel) resizeTable(width, height int) {
	// Base style has Padding(1, 2) which uses 2 spaces left and 2 spaces right (total 4 chars).
	// Leave margin so text never wraps against terminal edges.
	availWidth := width - 4
	if availWidth < 36 {
		availWidth = 36
	}

	selectColWidth := 4
	sizeColWidth := 9
	mbMinColWidth := 10
	resColWidth := 10

	if availWidth < 60 {
		sizeColWidth = 8
		mbMinColWidth = 9
		resColWidth = 9
	}

	// bubbles/table adds default cell padding of 1 on each side (2 per column = 10 total for 5 columns)
	tablePadding := 10
	availFilenameWidth := availWidth - selectColWidth - sizeColWidth - mbMinColWidth - resColWidth - tablePadding
	if availFilenameWidth < 12 {
		availFilenameWidth = 12
	}

	m.table.SetColumns([]table.Column{
		{Title: "[ ]", Width: selectColWidth},
		{Title: "Filename", Width: availFilenameWidth},
		{Title: "Size", Width: sizeColWidth},
		{Title: "MB/min", Width: mbMinColWidth},
		{Title: "Resolution", Width: resColWidth},
	})

	totalTableWidth := selectColWidth + availFilenameWidth + sizeColWidth + mbMinColWidth + resColWidth + tablePadding
	m.table.SetWidth(totalTableWidth)

	if height > 0 {
		availHeight := height - 16
		if availHeight < 5 {
			availHeight = 5
		} else if availHeight > 25 {
			availHeight = 25
		}
		m.table.SetHeight(availHeight)
	}
}

func (m *AppModel) updateTable() {
	m.filteredVideos = nil
	q := strings.ToLower(m.searchInput.Value())
	for _, v := range m.videos {
		if q == "" || strings.Contains(strings.ToLower(v.OriginalFileName), q) {
			m.filteredVideos = append(m.filteredVideos, v)
		}
	}

	var rows []table.Row
	for _, v := range m.filteredVideos {
		selected := "[ ]"
		if m.selectedVideos[v.ID] {
			selected = "[x]"
		}
		sizeStr := fmt.Sprintf("%.1f MB", float64(v.GetSize())/(1024*1024))
		mbMin := v.MBPerMin()
		var mbMinStr string
		if mbMin >= 10 {
			mbMinStr = fmt.Sprintf("%.0f MB/min", mbMin)
		} else if mbMin > 0 {
			mbMinStr = fmt.Sprintf("%.1f MB/min", mbMin)
		} else {
			mbMinStr = "N/A"
		}
		resStr := fmt.Sprintf("%dx%d", v.ExifInfo.ExifImageWidth, v.ExifInfo.ExifImageHeight)
		rows = append(rows, table.Row{selected, v.OriginalFileName, sizeStr, mbMinStr, resStr})
	}
	m.table.SetRows(rows)
}

func (m *AppModel) updateBrowser(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.isSearching {
		switch msg.String() {
		case "enter", "esc":
			m.isSearching = false
			m.searchInput.Blur()
		default:
			var cmd tea.Cmd
			m.searchInput, cmd = m.searchInput.Update(msg)
			m.updateTable()
			return m, cmd
		}
		return m, nil
	}

	switch msg.String() {
	case "q", "esc":
		return m, tea.Quit

	case "s", "S":
		m.state = ViewSettings
		if len(m.videos) == 0 {
			m.settingsTab = 1 // Tab 1: Filtering
		} else {
			m.settingsTab = 0
		}
		m.settingCursor = 0
		return m, nil

	case "e", "E":
		if len(m.videos) == 0 {
			m.state = ViewConnectionSetup
			m.setupFocus = 0
			m.updateSetupFocus()
			return m, nil
		}

	case "r", "R":
		m.state = ViewScanPrompt
		return m, nil

	case "a", "ctrl+a":
		for _, v := range m.filteredVideos {
			m.selectedVideos[v.ID] = true
		}
		m.updateTable()

	case "n", "ctrl+d":
		m.selectedVideos = make(map[string]bool)
		m.updateTable()

	case " ":
		cursor := m.table.Cursor()
		if cursor >= 0 && cursor < len(m.filteredVideos) {
			targetID := m.filteredVideos[cursor].ID
			m.selectedVideos[targetID] = !m.selectedVideos[targetID]
			m.updateTable()
		}

	case "/":
		m.isSearching = true
		m.searchInput.Focus()
		return m, textinput.Blink

	case "d", "D":
		var toProcess []immich.Asset
		for _, v := range m.videos {
			if m.selectedVideos[v.ID] {
				toProcess = append(toProcess, v)
			}
		}
		if len(toProcess) > 0 {
			m.state = ViewDryRun
			m.dryRunVideos = toProcess
			m.dryRunTotal = len(toProcess)
			m.dryRunProbed = 0
			m.dryRunToProcess = nil
			m.dryRunHEVC = nil
			m.dryRunProbeFailed = nil
			m.dryRunDetails = make(map[string]engine.ProbeDetails)
			m.dryRunDone = false
			m.dryRunCtx, m.dryRunCancel = context.WithCancel(context.Background())
			m.dryRunChan = make(chan interface{}, 200)
			if m.pipeline != nil {
				go m.pipeline.RunDryRun(m.dryRunCtx, toProcess, m.dryRunChan)
			} else {
				close(m.dryRunChan)
			}
			return m, waitForDryRun(m.dryRunChan)
		}
		return m, nil

	case "enter":
		var toProcess []immich.Asset
		for _, v := range m.videos {
			if m.selectedVideos[v.ID] {
				toProcess = append(toProcess, v)
			}
		}
		if len(toProcess) > 0 {
			if m.pipeline != nil && m.dryRunAlbumID != "" {
				m.pipeline.CleanDryRunAlbum()
				m.dryRunAlbumID = ""
				m.dryRunAlbumName = ""
			}
			return m.startOptimization(toProcess)
		}
		return m, nil

	default:
		var cmd tea.Cmd
		m.table, cmd = m.table.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *AppModel) startOptimization(toProcess []immich.Asset) (tea.Model, tea.Cmd) {
	if m.pipeline != nil && m.dryRunAlbumID != "" {
		m.pipeline.SetDryRunAlbumID(m.dryRunAlbumID)
	}
	m.dryRunAlbumName = ""
	m.dryRunAlbumID = ""
	m.state = ViewDashboard
	m.totalVideos = len(toProcess)
	m.processedVideos = 0
	m.skippedCount = 0
	m.failedCount = 0
	m.totalSavedBytes = 0
	m.failedVideos = nil
	m.skippedVideos = nil
	m.transcodedVideos = nil
	m.ctx, m.cancelCtx = context.WithCancel(context.Background())
	m.pipelineChan = make(chan interface{}, 200)
	if m.pipeline != nil {
		go m.pipeline.Run(m.ctx, toProcess, m.pipelineChan)
	} else {
		close(m.pipelineChan)
	}
	return m, tea.Batch(waitForPipeline(m.pipelineChan), tickPulseCmd())
}
