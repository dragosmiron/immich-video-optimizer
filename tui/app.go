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
	ViewTestingConnection ViewState = iota
	ViewConnectionSetup
	ViewResumePrompt
	ViewScanPrompt
	ViewScanning
	ViewBrowser
	ViewSettings
	ViewDashboard
	ViewSummary
)

// Async messages
type ConnectionTestedMsg struct {
	Err error
}

type ScanCompleteMsg struct {
	Result *immich.QueryResult
	Err    error
}

func testConnectionCmd(pipeline *orchestrator.Pipeline) tea.Cmd {
	return func() tea.Msg {
		err := pipeline.ValidateConnection()
		return ConnectionTestedMsg{Err: err}
	}
}

func scanVideosCmd(pipeline *orchestrator.Pipeline, thresholdBytes int64) tea.Cmd {
	return func() tea.Msg {
		res, err := pipeline.ScanVideos(thresholdBytes)
		return ScanCompleteMsg{Result: res, Err: err}
	}
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

func logFailure(msg string) {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".config", "immich-video-optimizer", "failures.log")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		f.WriteString(time.Now().Format(time.RFC3339) + " - " + msg + "\n")
		f.Close()
	}
}

type AppModel struct {
	state    ViewState
	cfg      *config.Config
	session  *state.Session
	pipeline *orchestrator.Pipeline
	width    int
	height   int

	// Connection Setup
	urlInput        textinput.Model
	tokenInput      textinput.Model
	setupFocus      int // 0: url, 1: token, 2: submit button
	connectionError string

	// Scan
	scanResult *immich.QueryResult
	scanError  string

	// Browser
	videos         []immich.Asset
	filteredVideos []immich.Asset
	selectedVideos map[string]bool
	table          table.Model
	searchInput    textinput.Model
	isSearching    bool

	// Settings
	settingCursor  int
	resolutions    []string
	presets        []string
	thresholdInput textinput.Model // typeable threshold in Settings

	// Dashboard
	ctx                     context.Context
	cancelCtx               context.CancelFunc
	pipelineChan            chan interface{}
	overallProgress         progress.Model
	videoProgress           progress.Model
	logMessages             []string
	totalVideos             int
	processedVideos         int
	skippedCount            int
	failedCount             int
	totalSavedBytes         int64
	currentVideoID          string
	currentVideoName        string
	currentVideoOriginalSize int64
	currentVideoResolution  string
	fps                     float64
	speed                   string
	eta                     string
	failedVideos            []string
}

func NewAppModel(cfg *config.Config, pipeline *orchestrator.Pipeline) *AppModel {
	sess, _ := state.LoadSession()

	// Initial State:
	// If URL or APIKey is missing, start at ViewConnectionSetup.
	// Otherwise, start at ViewTestingConnection to verify credentials first.
	initialState := ViewTestingConnection
	if strings.TrimSpace(cfg.URL) == "" || strings.TrimSpace(cfg.APIKey) == "" {
		initialState = ViewConnectionSetup
	}

	// Setup inputs (connection only)
	ui := textinput.New()
	ui.Placeholder = "http://192.168.1.100:2283"
	ui.SetValue(cfg.URL)
	ui.CharLimit = 256
	ui.Width = 50

	ti := textinput.New()
	ti.Placeholder = "Your Immich API Key"
	ti.SetValue(cfg.APIKey)
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '•'
	ti.CharLimit = 256
	ti.Width = 50

	if initialState == ViewConnectionSetup {
		ui.Focus()
	}

	// Threshold input used in Settings
	if cfg.SizeThreshold == "" {
		cfg.SizeThreshold = "500"
	}
	thi := textinput.New()
	thi.Placeholder = "e.g. 500"
	thi.SetValue(cfg.SizeThreshold)
	thi.CharLimit = 10
	thi.Width = 15

	// Search input
	si := textinput.New()
	si.Placeholder = "Filter by filename..."
	si.CharLimit = 156
	si.Width = 40

	// Video table
	t := table.New(
		table.WithColumns([]table.Column{
			{Title: "[ ]", Width: 4},
			{Title: "Filename", Width: 40},
			{Title: "Size", Width: 12},
			{Title: "Resolution", Width: 14},
		}),
		table.WithFocused(true),
		table.WithHeight(10),
	)

	ts := table.DefaultStyles()
	ts.Header = ts.Header.BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("240")).BorderBottom(true).Bold(true)
	ts.Selected = ts.Selected.Foreground(lipgloss.Color("229")).Background(lipgloss.Color("57")).Bold(false)
	t.SetStyles(ts)

	op := progress.New(progress.WithDefaultGradient())
	vp := progress.New(progress.WithDefaultGradient())

	return &AppModel{
		state:          initialState,
		cfg:            cfg,
		session:        sess,
		pipeline:       pipeline,
		urlInput:       ui,
		tokenInput:     ti,
		thresholdInput: thi,
		setupFocus:     0,
		table:          t,
		searchInput:    si,
		selectedVideos: make(map[string]bool),
		resolutions:    []string{"Original", "4K", "1440p", "1080p", "720p"},
		presets:        []string{"Ultrafast", "Fast", "Medium", "Slow"},
		overallProgress: op,
		videoProgress:  vp,
	}
}

func (m *AppModel) Init() tea.Cmd {
	cmds := []tea.Cmd{textinput.Blink}
	if m.state == ViewTestingConnection {
		cmds = append(cmds, testConnectionCmd(m.pipeline))
	}
	return tea.Batch(cmds...)
}

func (m *AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.overallProgress.Width = msg.Width - 6
		m.videoProgress.Width = msg.Width - 6

		availFilenameWidth := msg.Width - 4 - 12 - 14 - 8
		if availFilenameWidth > 20 {
			m.table.SetColumns([]table.Column{
				{Title: "[ ]", Width: 4},
				{Title: "Filename", Width: availFilenameWidth},
				{Title: "Size", Width: 12},
				{Title: "Resolution", Width: 14},
			})
		}
		return m, nil

	case ConnectionTestedMsg:
		if msg.Err != nil {
			m.connectionError = fmt.Sprintf("Connection failed: %v", msg.Err)
			m.state = ViewConnectionSetup
			m.setupFocus = 0
			m.urlInput.Focus()
			m.tokenInput.Blur()
			m.thresholdInput.Blur()
			return m, nil
		}
		m.connectionError = ""
		_ = config.SaveConfig(m.cfg)

		// Connected successfully! Check if a paused session exists
		sess, _ := state.LoadSession()
		if sess != nil && sess.PendingCount() > 0 {
			m.session = sess
			m.state = ViewResumePrompt
		} else {
			m.state = ViewScanPrompt
		}
		return m, nil

	case ScanCompleteMsg:
		if msg.Err != nil {
			m.scanError = fmt.Sprintf("Scan failed: %v", msg.Err)
			m.state = ViewScanPrompt
			return m, nil
		}
		m.scanError = ""
		m.scanResult = msg.Result
		m.videos = msg.Result.Videos
		m.selectedVideos = make(map[string]bool)
		for _, v := range m.videos {
			m.selectedVideos[v.ID] = true
		}
		m.updateTable()
		m.state = ViewBrowser
		return m, nil

	case orchestrator.PipelineStartMsg:
		m.totalVideos = msg.Total
		m.processedVideos = 0
		return m, waitForPipeline(m.pipelineChan)

	case orchestrator.PipelineVideoStartMsg:
		m.currentVideoID = msg.VideoID
		m.currentVideoOriginalSize = msg.Size
		for _, v := range m.videos {
			if v.ID == msg.VideoID {
				m.currentVideoName = v.OriginalFileName
				m.currentVideoResolution = fmt.Sprintf("%dx%d", v.ExifInfo.ExifImageWidth, v.ExifInfo.ExifImageHeight)
				break
			}
		}
		cmd := m.videoProgress.SetPercent(0)
		m.logMessages = append(m.logMessages, fmt.Sprintf("%s - Started processing %s", time.Now().Format("15:04:05"), m.currentVideoName))
		return m, tea.Batch(cmd, waitForPipeline(m.pipelineChan))

	case orchestrator.PipelineProgressMsg:
		m.fps = msg.EngineProgress.FPS
		m.speed = msg.EngineProgress.Speed
		m.eta = msg.EngineProgress.Time
		cmd := m.videoProgress.SetPercent(0.5)
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

		timeStr := time.Now().Format("15:04:05")
		if msg.Err != nil {
			m.failedCount++
			m.failedVideos = append(m.failedVideos, m.currentVideoName)
			msgStr := fmt.Sprintf("%s - Failed %s: %v", timeStr, m.currentVideoName, msg.Err)
			m.logMessages = append(m.logMessages, msgStr)
			logFailure(msgStr)
		} else if msg.Skipped {
			m.skippedCount++
			m.logMessages = append(m.logMessages, fmt.Sprintf("%s - Skipped %s (Size Guard: transcoded was larger)", timeStr, m.currentVideoName))
		} else {
			m.logMessages = append(m.logMessages, fmt.Sprintf("%s - Completed %s (Saved %.2f MB)", timeStr, m.currentVideoName, float64(msg.SavedBytes)/1024/1024))
		}

		percent := 0.0
		if m.totalVideos > 0 {
			percent = float64(m.processedVideos) / float64(m.totalVideos)
		}
		cmd := m.overallProgress.SetPercent(percent)
		return m, tea.Batch(cmd, waitForPipeline(m.pipelineChan))

	case orchestrator.PipelineDoneMsg:
		m.state = ViewSummary
		return m, nil

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			if m.cancelCtx != nil {
				m.cancelCtx()
			}
			return m, tea.Quit
		}

		switch m.state {
		case ViewTestingConnection:
			if msg.String() == "q" || msg.String() == "esc" {
				return m, tea.Quit
			}
		case ViewConnectionSetup:
			return m.updateConnectionSetup(msg)
		case ViewResumePrompt:
			return m.updateResumePrompt(msg)
		case ViewScanPrompt:
			return m.updateScanPrompt(msg)
		case ViewScanning:
			if msg.String() == "q" || msg.String() == "esc" {
				m.state = ViewScanPrompt
				return m, nil
			}
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

func (m *AppModel) updateScanPrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter", "s", "S":
		m.state = ViewScanning
		thresholdMB, _ := strconv.ParseInt(m.cfg.SizeThreshold, 10, 64)
		if thresholdMB <= 0 {
			thresholdMB = 500
		}
		thresholdBytes := thresholdMB * 1024 * 1024
		return m, scanVideosCmd(m.pipeline, thresholdBytes)

	case "c", "C":
		m.state = ViewSettings
		m.settingCursor = 0
		return m, nil

	case "e", "E":
		m.state = ViewConnectionSetup
		m.setupFocus = 0
		m.updateSetupFocus()
		return m, nil

	case "q", "esc":
		return m, tea.Quit
	}
	return m, nil
}

func (m *AppModel) updateResumePrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "r", "R":
		// Resume session
		var toProcess []immich.Asset
		for _, vs := range m.session.Videos {
			if vs.Status == state.StatusPending {
				var a immich.Asset
				a.ID = vs.ID
				a.OriginalFileName = vs.OriginalName
				a.ExifInfo.FileSizeInByte = vs.OriginalSize
				toProcess = append(toProcess, a)
			}
		}
		if len(toProcess) > 0 {
			m.state = ViewDashboard
			m.totalVideos = len(toProcess)
			m.processedVideos = 0
			m.skippedCount = 0
			m.failedCount = 0
			m.totalSavedBytes = m.session.TotalSavedBytes
			m.ctx, m.cancelCtx = context.WithCancel(context.Background())
			m.pipelineChan = make(chan interface{}, 50)
			go m.pipeline.Run(m.ctx, toProcess, m.pipelineChan)
			return m, waitForPipeline(m.pipelineChan)
		}
		m.state = ViewScanPrompt
		return m, nil

	case "s", "S":
		_ = state.ClearSession()
		m.session = nil
		m.state = ViewScanPrompt
		return m, nil

	case "c", "C":
		_ = state.ClearSession()
		m.session = nil
		return m, tea.Quit

	case "q", "esc":
		return m, tea.Quit
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
		selected := "[ ]"
		if m.selectedVideos[v.ID] {
			selected = "[x]"
		}
		sizeStr := fmt.Sprintf("%.1f MB", float64(v.GetSize())/(1024*1024))
		resStr := fmt.Sprintf("%dx%d", v.ExifInfo.ExifImageWidth, v.ExifInfo.ExifImageHeight)
		rows = append(rows, table.Row{selected, v.OriginalFileName, sizeStr, resStr})
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
		m.state = ViewScanPrompt
		return m, nil

	case "c", "C":
		m.state = ViewSettings
		m.settingCursor = 0
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
			m.totalVideos = len(toProcess)
			m.processedVideos = 0
			m.skippedCount = 0
			m.failedCount = 0
			m.totalSavedBytes = 0
			m.failedVideos = nil
			m.ctx, m.cancelCtx = context.WithCancel(context.Background())
			m.pipelineChan = make(chan interface{}, 50)
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
	// When the threshold row is focused and user is typing into it,
	// only allow Esc/Tab/Up/Down to leave — everything else feeds the input.
	if m.settingCursor == 3 && m.thresholdInput.Focused() {
		switch msg.String() {
		case "esc":
			// Save & return
			m.thresholdInput.Blur()
			thresh := strings.TrimSpace(m.thresholdInput.Value())
			if thresh == "" {
				thresh = "500"
				m.thresholdInput.SetValue(thresh)
			}
			m.cfg.SizeThreshold = thresh
			_ = config.SaveConfig(m.cfg)
			if len(m.videos) > 0 {
				m.state = ViewBrowser
			} else {
				m.state = ViewScanPrompt
			}
		case "tab", "down", "j":
			m.thresholdInput.Blur()
			thresh := strings.TrimSpace(m.thresholdInput.Value())
			if thresh == "" {
				thresh = "500"
				m.thresholdInput.SetValue(thresh)
			}
			m.cfg.SizeThreshold = thresh
		case "up", "k":
			m.thresholdInput.Blur()
			thresh := strings.TrimSpace(m.thresholdInput.Value())
			if thresh == "" {
				thresh = "500"
				m.thresholdInput.SetValue(thresh)
			}
			m.cfg.SizeThreshold = thresh
			if m.settingCursor > 0 {
				m.settingCursor--
			}
		default:
			var cmd tea.Cmd
			m.thresholdInput, cmd = m.thresholdInput.Update(msg)
			return m, cmd
		}
		return m, nil
	}

	switch msg.String() {
	case "q", "esc":
		_ = config.SaveConfig(m.cfg)
		if len(m.videos) > 0 {
			m.state = ViewBrowser
		} else {
			m.state = ViewScanPrompt
		}

	case "up", "k":
		if m.settingCursor > 0 {
			m.settingCursor--
		}

	case "down", "j":
		if m.settingCursor < 3 {
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
		} else if m.settingCursor == 3 {
			// Activate the threshold text input
			return m, m.thresholdInput.Focus()
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
		m.logMessages = append(m.logMessages, "Pausing pipeline safely...")
		return m, nil
	}
	return m, nil
}

func (m *AppModel) updateSummary(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "r", "R":
		// Retry failed videos
		if len(m.failedVideos) > 0 {
			var toRetry []immich.Asset
			for _, v := range m.videos {
				for _, f := range m.failedVideos {
					if v.OriginalFileName == f {
						toRetry = append(toRetry, v)
					}
				}
			}
			if len(toRetry) > 0 {
				m.state = ViewDashboard
				m.totalVideos = len(toRetry)
				m.processedVideos = 0
				m.failedCount = 0
				m.failedVideos = nil
				m.ctx, m.cancelCtx = context.WithCancel(context.Background())
				m.pipelineChan = make(chan interface{}, 50)
				go m.pipeline.Run(m.ctx, toRetry, m.pipelineChan)
				return m, waitForPipeline(m.pipelineChan)
			}
		}
	case "enter", "q", "esc":
		return m, tea.Quit
	}
	return m, nil
}

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
	return styles.Base.Render(s)
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
	b.WriteString(fmt.Sprintf("  • Size Guard:     %s\n\n", styles.Value.Render(sgText)))

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
	b.WriteString(fmt.Sprintf("  • Non-video assets skipped:          %d (photos, live photos, RAW images)\n", r.SkippedNonVideo))
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
		b.WriteString(styles.Subtext.Render("Press [/] to filter filename  |  [Space] to toggle selection\n\n"))
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

	b.WriteString(styles.Subtext.Render("Shortcuts: [Space] Toggle | [a] Select All | [n] Select None | [/] Search | [c] Settings | [s] Rescan | [Enter] Start | [q] Quit"))
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
		b.WriteString(fmt.Sprintf("  • Space Saved So Far: %.2f MB\n", float64(m.session.TotalSavedBytes)/(1024*1024)))
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

	cursor := func(idx int) string {
		if m.settingCursor == idx {
			return "> "
		}
		return "  "
	}

	b.WriteString(cursor(0) + "Target Resolution:   " + styles.Value.Render(m.cfg.Resolution) + "  " + styles.Subtext.Render("[Original, 4K, 1440p, 1080p, 720p]") + "\n")
	b.WriteString(cursor(1) + "Transcoding Preset:  " + styles.Value.Render(m.cfg.Preset) + "  " + styles.Subtext.Render("[Ultrafast, Fast, Medium, Slow]") + "\n")

	sg := "[x] Enabled (skips replacing original if transcoded is larger)"
	if !m.cfg.SizeGuard {
		sg = "[ ] Disabled"
	}
	b.WriteString(cursor(2) + "Size Guard:          " + styles.Value.Render(sg) + "\n")

	// Size Threshold — typeable input
	threshLabel := cursor(3) + "Size Threshold (MB): "
	if m.settingCursor == 3 && m.thresholdInput.Focused() {
		b.WriteString(threshLabel + m.thresholdInput.View() + "  " + styles.Subtext.Render("[Type a number, then Tab/↓ or Esc]") + "\n")
	} else {
		hint := ""
		if m.settingCursor == 3 {
			hint = "  " + styles.Subtext.Render("[Press Enter to edit]")
		}
		b.WriteString(threshLabel + styles.Value.Render(m.cfg.SizeThreshold+" MB") + hint + "\n")
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

	// Active Video
	b.WriteString(styles.SectionHeader.Render("Current Video:"))
	b.WriteString("\n")
	if m.currentVideoName != "" {
		b.WriteString(fmt.Sprintf("  File:       %s\n", styles.Value.Render(m.currentVideoName)))
		if m.currentVideoOriginalSize > 0 {
			b.WriteString(fmt.Sprintf("  Size & Res: %.2f MB  |  %s\n",
				float64(m.currentVideoOriginalSize)/(1024*1024),
				m.currentVideoResolution,
			))
		}
	} else {
		b.WriteString("  Preparing next video...\n")
	}
	b.WriteString(m.videoProgress.View())
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  Speed: %s  |  FPS: %.1f  |  ETA: %s\n\n",
		styles.Value.Render(m.speed),
		m.fps,
		styles.Value.Render(m.eta),
	))

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
	b.WriteString(styles.Subtext.Render("[q] Pause & Save Session (safely stops after current task)"))
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
		b.WriteString(styles.Subtext.Render("Check ~/.config/immich-video-optimizer/failures.log for details.\n\n"))
		b.WriteString("[ R ] Retry Failed Videos  |  [ Enter / Q ] Exit")
	} else {
		b.WriteString(styles.SuccessText.Render("✓ All items optimized successfully without errors!"))
		b.WriteString("\n\n[ Enter / Q ] Exit")
	}

	return b.String()
}

func RunTUI(cfg *config.Config, pipeline *orchestrator.Pipeline) error {
	m := NewAppModel(cfg, pipeline)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
