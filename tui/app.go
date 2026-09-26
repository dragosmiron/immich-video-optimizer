package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"immich-video-optimizer/config"
	"immich-video-optimizer/immich"
	"immich-video-optimizer/orchestrator"
	"immich-video-optimizer/state"
	"immich-video-optimizer/tui/styles"
)

type ViewState int

const (
	ViewResumePrompt ViewState = iota
	ViewBrowser
	ViewSettings
	ViewDashboard
	ViewSummary
)

// Pipeline events mapped to Tea messages
type PipelineMsg interface{}

type AppModel struct {
	state          ViewState
	cfg            *config.Config
	session        *state.Session
	videos         []immich.Asset
	filteredVideos []immich.Asset
	selectedVideos map[string]bool
	pipeline       *orchestrator.Pipeline
	width          int
	height         int

	// Browser
	table     table.Model
	searchInput textinput.Model
	isSearching bool

	// Settings
	settingCursor int
	resolutions   []string
	presets       []string

	// Dashboard
	ctx             context.Context
	cancelCtx       context.CancelFunc
	pipelineChan    chan interface{}
	overallProgress progress.Model
	videoProgress   progress.Model
	logMessages     []string
	done            bool
	
	// Stats
	totalVideos     int
	processedVideos int
	totalSavedBytes int64
	currentVideoID  string
	currentVideoName string
	fps             float64
	speed           string
	eta             string
}

func logFailure(msg string) {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".config", "immich-video-optimizer", "failures.log")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		f.WriteString(time.Now().Format(time.RFC3339) + " - " + msg + "\n")
		f.Close()
	}
}

func NewAppModel(cfg *config.Config, pipeline *orchestrator.Pipeline) *AppModel {
	sess, _ := state.LoadSession()
	initialState := ViewBrowser
	if sess != nil {
		initialState = ViewResumePrompt
	}

	ti := textinput.New()
	ti.Placeholder = "Search filename..."
	ti.CharLimit = 156
	ti.Width = 40

	t := table.New(
		table.WithColumns([]table.Column{
			{Title: "[ ]", Width: 3},
			{Title: "Filename", Width: 40},
			{Title: "Size", Width: 10},
			{Title: "Resolution", Width: 12},
		}),
		table.WithFocused(true),
		table.WithHeight(10),
	)

	s := table.DefaultStyles()
	s.Header = s.Header.BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("240")).BorderBottom(true).Bold(false)
	s.Selected = s.Selected.Foreground(lipgloss.Color("229")).Background(lipgloss.Color("57")).Bold(false)
	t.SetStyles(s)

	op := progress.New(progress.WithDefaultGradient())
	vp := progress.New(progress.WithDefaultGradient())

	return &AppModel{
		state:          initialState,
		cfg:            cfg,
		session:        sess,
		selectedVideos: make(map[string]bool),
		pipeline:       pipeline,
		table:          t,
		searchInput:    ti,
		resolutions:    []string{"Original", "4K", "1440p", "1080p", "720p"},
		presets:        []string{"Ultrafast", "Fast", "Medium", "Slow"},
		overallProgress: op,
		videoProgress:   vp,
	}
}

type VideosLoadedMsg struct {
	videos []immich.Asset
	err    error
}

func (m *AppModel) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		func() tea.Msg {
			thresholdMB, _ := strconv.ParseInt(m.cfg.SizeThreshold, 10, 64)
			thresholdBytes := thresholdMB * 1024 * 1024
			videos, err := m.pipeline.QueryVideos(thresholdBytes)
			return VideosLoadedMsg{videos: videos, err: err}
		},
	)
}

func waitForPipeline(ch chan interface{}) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

func (m *AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.overallProgress.Width = msg.Width - 4
		m.videoProgress.Width = msg.Width - 4
		return m, nil
	case VideosLoadedMsg:
		if msg.err == nil {
			m.videos = msg.videos
			m.updateTable()
		} else {
			m.logMessages = append(m.logMessages, "Error loading videos: "+msg.err.Error())
		}
		return m, nil
	case orchestrator.PipelineStartMsg:
		m.totalVideos = msg.Total
		m.processedVideos = 0
		return m, waitForPipeline(m.pipelineChan)
	case orchestrator.PipelineVideoStartMsg:
		m.currentVideoID = msg.VideoID
		for _, v := range m.videos {
			if v.ID == msg.VideoID {
				m.currentVideoName = v.OriginalFileName
				break
			}
		}
		cmd := m.videoProgress.SetPercent(0)
		m.logMessages = append(m.logMessages, fmt.Sprintf("Started processing %s", m.currentVideoName))
		return m, tea.Batch(cmd, waitForPipeline(m.pipelineChan))
	case orchestrator.PipelineProgressMsg:
		m.fps = msg.EngineProgress.FPS
		m.speed = msg.EngineProgress.Speed
		m.eta = msg.EngineProgress.Time
		// In a real app we'd parse time vs duration, but let's fake a bump
		cmd := m.videoProgress.SetPercent(0.5) // dummy progress
		return m, tea.Batch(cmd, waitForPipeline(m.pipelineChan))
	case progress.FrameMsg:
		progressModel, cmd := m.videoProgress.Update(msg)
		m.videoProgress = progressModel.(progress.Model)
		
		progressModel2, cmd2 := m.overallProgress.Update(msg)
		m.overallProgress = progressModel2.(progress.Model)
		return m, tea.Batch(cmd, cmd2)
	case orchestrator.PipelineVideoCompleteMsg:
		m.processedVideos++
		m.totalSavedBytes += msg.SavedBytes
		if msg.Err != nil {
			msgStr := fmt.Sprintf("Failed %s: %v", msg.VideoID, msg.Err)
			m.logMessages = append(m.logMessages, msgStr)
			logFailure(msgStr)
		} else if msg.Skipped {
			m.logMessages = append(m.logMessages, fmt.Sprintf("Skipped %s (Size Guard)", m.currentVideoName))
		} else {
			m.logMessages = append(m.logMessages, fmt.Sprintf("Completed %s (Saved %.2f MB)", m.currentVideoName, float64(msg.SavedBytes)/1024/1024))
		}
		
		percent := 0.0
		if m.totalVideos > 0 {
			percent = float64(m.processedVideos) / float64(m.totalVideos)
		}
		cmd := m.overallProgress.SetPercent(percent)
		return m, tea.Batch(cmd, waitForPipeline(m.pipelineChan))
	case orchestrator.PipelineDoneMsg:
		m.done = true
		m.state = ViewSummary
		return m, nil
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			if m.session != nil {
				_ = state.SaveSession(m.session)
			}
			if m.cancelCtx != nil {
				m.cancelCtx()
			}
			return m, tea.Quit
		}

		switch m.state {
		case ViewResumePrompt:
			return m.updateResumePrompt(msg)
		case ViewBrowser:
			return m.updateBrowser(msg)
		case ViewSettings:
			return m.updateSettings(msg)
		case ViewDashboard:
			return m.updateDashboard(msg)
		case ViewSummary:
			return m.updateSummary(msg)
		}
	}
	return m, nil
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
		selected := " "
		if m.selectedVideos[v.ID] {
			selected = "x"
		}
		sizeStr := fmt.Sprintf("%.1f MB", float64(v.GetSize())/(1024*1024))
		resStr := fmt.Sprintf("%dx%d", v.ExifInfo.ExifImageWidth, v.ExifInfo.ExifImageHeight)
		rows = append(rows, table.Row{selected, v.OriginalFileName, sizeStr, resStr})
	}
	m.table.SetRows(rows)
}

func (m *AppModel) updateResumePrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "r", "R":
		m.state = ViewDashboard
		m.ctx, m.cancelCtx = context.WithCancel(context.Background())
		// In a real resume, we would re-run with pending videos.
		// For now, we skip to dashboard.
	case "s", "S":
		_ = state.ClearSession()
		m.session = nil
		m.state = ViewBrowser
	case "c", "C":
		_ = state.ClearSession()
		m.session = nil
		return m, tea.Quit
	case "q", "esc":
		return m, tea.Quit
	}
	return m, nil
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
	case "c":
		m.state = ViewSettings
	case "a", "ctrl+a":
		for _, v := range m.filteredVideos {
			m.selectedVideos[v.ID] = true
		}
		m.updateTable()
	case "n", "ctrl+d":
		m.selectedVideos = make(map[string]bool)
		m.updateTable()
	case " ":
		row := m.table.SelectedRow()
		if row != nil {
			for _, v := range m.filteredVideos {
				if v.OriginalFileName == row[1] {
					m.selectedVideos[v.ID] = !m.selectedVideos[v.ID]
					break
				}
			}
			m.updateTable()
		}
	case "/":
		m.isSearching = true
		m.searchInput.Focus()
		return m, textinput.Blink
	case "enter":
		var toProcess []immich.Asset
		for _, v := range m.videos {
			if m.selectedVideos[v.ID] {
				toProcess = append(toProcess, v)
			}
		}
		if len(toProcess) > 0 {
			m.state = ViewDashboard
			m.ctx, m.cancelCtx = context.WithCancel(context.Background())
			m.pipelineChan = make(chan interface{})
			go m.pipeline.Run(m.ctx, toProcess, m.pipelineChan)
			return m, waitForPipeline(m.pipelineChan)
		}
	default:
		var cmd tea.Cmd
		m.table, cmd = m.table.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *AppModel) updateSettings(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		_ = config.SaveConfig(m.cfg)
		m.state = ViewBrowser
	case "up", "k":
		if m.settingCursor > 0 {
			m.settingCursor--
		}
	case "down", "j":
		if m.settingCursor < 2 {
			m.settingCursor++
		}
	case "left", "h":
		if m.settingCursor == 0 {
			for i, r := range m.resolutions {
				if r == m.cfg.Resolution && i > 0 {
					m.cfg.Resolution = m.resolutions[i-1]
					break
				}
			}
		} else if m.settingCursor == 1 {
			for i, p := range m.presets {
				if p == m.cfg.Preset && i > 0 {
					m.cfg.Preset = m.presets[i-1]
					break
				}
			}
		}
	case "right", "l":
		if m.settingCursor == 0 {
			for i, r := range m.resolutions {
				if r == m.cfg.Resolution && i < len(m.resolutions)-1 {
					m.cfg.Resolution = m.resolutions[i+1]
					break
				}
			}
		} else if m.settingCursor == 1 {
			for i, p := range m.presets {
				if p == m.cfg.Preset && i < len(m.presets)-1 {
					m.cfg.Preset = m.presets[i+1]
					break
				}
			}
		}
	case " ", "enter":
		if m.settingCursor == 2 {
			m.cfg.SizeGuard = !m.cfg.SizeGuard
		}
	}
	return m, nil
}

func (m *AppModel) updateDashboard(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		if m.cancelCtx != nil {
			m.cancelCtx()
		}
		return m, tea.Quit
	}
	return m, nil
}

func (m *AppModel) updateSummary(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "enter":
		return m, tea.Quit
	}
	return m, nil
}

func (m *AppModel) View() string {
	var s string
	switch m.state {
	case ViewResumePrompt:
		s = m.viewResumePrompt()
	case ViewBrowser:
		s = m.viewBrowser()
	case ViewSettings:
		s = m.viewSettings()
	case ViewDashboard:
		s = m.viewDashboard()
	case ViewSummary:
		s = m.viewSummary()
	}
	return styles.Base.Render(s)
}

func (m *AppModel) viewResumePrompt() string {
	b := strings.Builder{}
	b.WriteString(styles.Title.Render("Resume Previous Session"))
	b.WriteString("\n\n")
	if m.session != nil {
		b.WriteString(fmt.Sprintf("Progress: %d bytes saved\n", m.session.TotalSavedBytes))
	}
	b.WriteString("\n[R] Resume Session\n[S] Start New / Rescan Library\n[C] Clear State Only\n[Q] Quit")
	return b.String()
}

func (m *AppModel) viewBrowser() string {
	b := strings.Builder{}
	b.WriteString(styles.Title.Render("Interactive Video Browser"))
	b.WriteString("\n\n")
	if m.isSearching {
		b.WriteString(m.searchInput.View() + "\n\n")
	} else {
		b.WriteString("Press '/' to search. Select videos to optimize:\n\n")
	}
	
	b.WriteString(m.table.View() + "\n\n")
	b.WriteString("Shortcuts: [a] Select All | [n] Select None | [space] Toggle | [c] Settings | [enter] Start | [q] Quit")
	return b.String()
}

func (m *AppModel) viewSettings() string {
	b := strings.Builder{}
	b.WriteString(styles.Title.Render("Settings View"))
	b.WriteString("\n\nUse Up/Down to navigate, Left/Right to change options.\n\n")

	cursor := func(idx int) string {
		if m.settingCursor == idx {
			return "> "
		}
		return "  "
	}

	b.WriteString(cursor(0) + "Resolution: " + m.cfg.Resolution + " (Original, 4K, 1440p, 1080p, 720p)\n")
	b.WriteString(cursor(1) + "Preset: " + m.cfg.Preset + " (Ultrafast, Fast, Medium, Slow)\n")
	sg := "[ ]"
	if m.cfg.SizeGuard {
		sg = "[x]"
	}
	b.WriteString(cursor(2) + "Size Guard (Skip if transcoded is larger): " + sg + "\n")

	b.WriteString("\n[esc] Back")
	return b.String()
}

func (m *AppModel) viewDashboard() string {
	b := strings.Builder{}
	b.WriteString(styles.Title.Render("Live Optimization Dashboard"))
	b.WriteString("\n\nOverall Progress: ")
	b.WriteString(m.overallProgress.View())
	b.WriteString(fmt.Sprintf("\nVideos: %d/%d\n\n", m.processedVideos, m.totalVideos))

	b.WriteString("Current Video: " + m.currentVideoName + "\n")
	b.WriteString(m.videoProgress.View())
	b.WriteString(fmt.Sprintf("\nSpeed: %s | FPS: %.1f | ETA: %s\n\n", m.speed, m.fps, m.eta))

	b.WriteString("Activity Log:\n")
	start := 0
	if len(m.logMessages) > 5 {
		start = len(m.logMessages) - 5
	}
	for i := start; i < len(m.logMessages); i++ {
		b.WriteString(m.logMessages[i] + "\n")
	}

	b.WriteString("\n[q] Abort")
	return b.String()
}

func (m *AppModel) viewSummary() string {
	b := strings.Builder{}
	b.WriteString(styles.Title.Render("Optimization Summary"))
	b.WriteString(fmt.Sprintf("\n\nTotal Space Saved: %.2f MB\n", float64(m.totalSavedBytes)/1024/1024))
	b.WriteString("Check failures.log for any errors.\n")
	b.WriteString("\n[q] Quit")
	return b.String()
}

func RunTUI(cfg *config.Config, pipeline *orchestrator.Pipeline) error {
	m := NewAppModel(cfg, pipeline)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
