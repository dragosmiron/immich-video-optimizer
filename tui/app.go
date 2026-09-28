package tui

import (
	"context"
	"fmt"
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
	"immich-video-optimizer/logger"
	"immich-video-optimizer/orchestrator"
	"immich-video-optimizer/state"
)

type ViewState int

const (
	ViewTestingConnection ViewState = iota
	ViewConnectionSetup
	ViewResumePrompt
	ViewScanPrompt
	ViewScanning
	ViewBrowser
	ViewDryRun
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

func waitForDryRun(ch chan interface{}) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}


func logFailure(msg string) {
	logger.Error("%s", msg)
}

func (m *AppModel) addLog(msg string) {
	m.logMessages = append(m.logMessages, msg)
	if len(m.logMessages) > 200 {
		m.logMessages = m.logMessages[len(m.logMessages)-200:]
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
	currentDownloadName     string
	currentTranscodeName    string
	currentUploadName       string
	probingStatus           string
	fps                     float64
	speed                   string
	failedVideos            []failureEntry // tracks each failed video with its error message
	skippedVideos           []skippedVideo // tracks each skipped video with its reason

	// Dry Run
	dryRunChan        chan interface{}
	dryRunCtx         context.Context
	dryRunCancel      context.CancelFunc
	dryRunTotal       int
	dryRunProbed      int
	dryRunToProcess   []immich.Asset
	dryRunHEVC        []immich.Asset
	dryRunProbeFailed []immich.Asset
	dryRunDone        bool
	dryRunVideos      []immich.Asset // the selection that triggered the dry run
}


// skippedVideo holds the name and reason for a skipped video.
type skippedVideo struct {
	Name   string
	Reason string
}

// failureEntry holds the name and error message for a failed video.
type failureEntry struct {
	Name string
	Err  string
}

func NewAppModel(cfg *config.Config, pipeline *orchestrator.Pipeline, opts ...Option) *AppModel {
	logger.SetLevelString(cfg.LogLevel)
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
			{Title: "Size", Width: 11},
			{Title: "Resolution", Width: 12},
		}),
		table.WithFocused(true),
		table.WithHeight(10),
		table.WithWidth(75),
	)

	ts := table.DefaultStyles()
	ts.Header = ts.Header.BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("240")).BorderBottom(true).Bold(true)
	ts.Selected = ts.Selected.Foreground(lipgloss.Color("229")).Background(lipgloss.Color("57")).Bold(false)
	t.SetStyles(ts)

	op := progress.New(progress.WithDefaultGradient())

	m := &AppModel{
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
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
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
		progWidth := msg.Width - 10
		if progWidth > 70 {
			progWidth = 70
		} else if progWidth < 20 {
			progWidth = 20
		}
		m.overallProgress.Width = progWidth

		m.resizeTable(msg.Width, msg.Height)
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

	case orchestrator.DryRunVideoMsg:
		if m.state == ViewDryRun {
			m.dryRunProbed = msg.Probed
			m.dryRunTotal = msg.Total
		}
		return m, waitForDryRun(m.dryRunChan)

	case orchestrator.DryRunCompleteMsg:
		if m.state == ViewDryRun {
			m.dryRunToProcess = msg.ToProcess
			m.dryRunHEVC = msg.HEVCSkipped
			m.dryRunProbeFailed = msg.ProbeFailed
			m.dryRunDone = true
		}
		return m, nil

	case orchestrator.PipelineStartMsg:
		m.totalVideos = msg.Total
		m.processedVideos = 0
		return m, waitForPipeline(m.pipelineChan)

	case orchestrator.PipelineVideoStartMsg:
		m.currentVideoID = msg.VideoID
		m.currentVideoOriginalSize = msg.Size
		if msg.VideoName != "" {
			m.currentVideoName = msg.VideoName
			m.currentTranscodeName = msg.VideoName
		}
		if msg.Resolution != "" {
			m.currentVideoResolution = msg.Resolution
		}
		if m.currentVideoName == "" {
			for _, v := range m.videos {
				if v.ID == msg.VideoID {
					m.currentVideoName = v.OriginalFileName
					m.currentTranscodeName = v.OriginalFileName
					m.currentVideoResolution = fmt.Sprintf("%dx%d", v.ExifInfo.ExifImageWidth, v.ExifInfo.ExifImageHeight)
					break
				}
			}
		}
		m.addLog(fmt.Sprintf("%s - Transcoding %s", time.Now().Format("15:04:05"), m.currentVideoName))
		return m, waitForPipeline(m.pipelineChan)

	case orchestrator.PipelineStageMsg:
		switch msg.Stage {
		case "probing":
			m.probingStatus = msg.VideoName
			if msg.VideoName != "" {
				m.addLog(fmt.Sprintf("%s - %s", time.Now().Format("15:04:05"), msg.VideoName))
			}
		case "downloading":
			m.probingStatus = ""
			m.currentDownloadName = msg.VideoName
		case "transcoding":
			m.currentTranscodeName = msg.VideoName
			// Download is done for this item — clear the prefetch lane
			if m.currentDownloadName == msg.VideoName {
				m.currentDownloadName = ""
			}
		case "uploading":
			m.currentUploadName = msg.VideoName
		case "idle":
			m.currentDownloadName = ""
			m.currentTranscodeName = ""
			m.currentUploadName = ""
			m.probingStatus = ""
		}
		return m, waitForPipeline(m.pipelineChan)

	case orchestrator.PipelineProgressMsg:
		m.fps = msg.EngineProgress.FPS
		m.speed = msg.EngineProgress.Speed
		return m, waitForPipeline(m.pipelineChan)

	case progress.FrameMsg:
		progressModel2, cmd2 := m.overallProgress.Update(msg)
		m.overallProgress = progressModel2.(progress.Model)
		return m, cmd2

	case orchestrator.PipelineVideoCompleteMsg:
		m.processedVideos++
		m.totalSavedBytes += msg.SavedBytes
		if m.currentUploadName != "" {
			m.currentUploadName = ""
		}

		timeStr := time.Now().Format("15:04:05")
		completedName := msg.VideoName
		if completedName == "" {
			completedName = m.currentVideoName
			for _, v := range m.videos {
				if v.ID == msg.VideoID {
					completedName = v.OriginalFileName
					break
				}
			}
		}
		if completedName == "" {
			completedName = msg.VideoID
		}

		if msg.Err != nil {
			m.failedCount++
			m.failedVideos = append(m.failedVideos, failureEntry{Name: completedName, Err: msg.Err.Error()})
			msgStr := fmt.Sprintf("%s - Failed %s: %v", timeStr, completedName, msg.Err)
			m.addLog(msgStr)
			logFailure(msgStr)
		} else if msg.Skipped {
			m.skippedCount++
			reason := "already HEVC"
			if msg.SkipReason != "" {
				reason = msg.SkipReason
			}
			m.skippedVideos = append(m.skippedVideos, skippedVideo{Name: completedName, Reason: reason})
			m.addLog(fmt.Sprintf("%s - Skipped %s (%s)", timeStr, completedName, reason))
		} else {
			m.addLog(fmt.Sprintf("%s - Completed %s (Saved %.2f MB)", timeStr, completedName, float64(msg.SavedBytes)/1024/1024))
		}

		percent := 0.0
		if m.totalVideos > 0 {
			percent = float64(m.processedVideos) / float64(m.totalVideos)
		}
		cmd := m.overallProgress.SetPercent(percent)
		return m, tea.Batch(cmd, waitForPipeline(m.pipelineChan))

	case orchestrator.PipelineDoneMsg:
		if msg.Paused {
			// User pressed Q — reload fresh session state from disk so counts are accurate
			sess, _ := state.LoadSession()
			m.session = sess
			m.state = ViewResumePrompt
		} else {
			m.state = ViewSummary
		}
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
		case ViewDryRun:
			return m.updateDryRun(msg)
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
	case "enter":
		m.state = ViewScanning
		thresholdMB, _ := strconv.ParseInt(m.cfg.SizeThreshold, 10, 64)
		if thresholdMB <= 0 {
			thresholdMB = 500
		}
		thresholdBytes := thresholdMB * 1024 * 1024
		return m, scanVideosCmd(m.pipeline, thresholdBytes)

	case "s", "S":
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
		for _, vs := range m.session.PendingVideos() {
			var a immich.Asset
			a.ID = vs.ID
			a.OriginalFileName = vs.OriginalName
			a.ExifInfo.FileSizeInByte = vs.OriginalSize
			a.ExifInfo.ExifImageWidth = vs.Width
			a.ExifInfo.ExifImageHeight = vs.Height
			a.FileCreatedAt = vs.CreatedAt
			a.FileModifiedAt = vs.ModifiedAt
			a.DeviceAssetID = vs.DeviceAssetID
			a.DeviceID = vs.DeviceID
			toProcess = append(toProcess, a)
		}
		if len(toProcess) > 0 {
			m.state = ViewDashboard
			m.totalVideos = len(toProcess)
			m.processedVideos = 0
			m.skippedCount = 0
			m.failedCount = 0
			m.totalSavedBytes = m.session.GetTotalSavedBytes()
			m.ctx, m.cancelCtx = context.WithCancel(context.Background())
			m.pipelineChan = make(chan interface{}, 200)
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

func (m *AppModel) resizeTable(width, height int) {
	// Base style has Padding(1, 2) which uses 2 spaces left and 2 spaces right (total 4 chars).
	// Leave an extra 2 chars margin so text never wraps against terminal edges.
	availWidth := width - 6
	if availWidth < 36 {
		availWidth = 36
	}

	selectColWidth := 4
	sizeColWidth := 11
	resColWidth := 12

	if availWidth < 55 {
		sizeColWidth = 9
		resColWidth = 10
	}

	// bubbles/table adds default cell padding of 1 on each side (2 per column = 8 total for 4 columns)
	tablePadding := 8
	availFilenameWidth := availWidth - selectColWidth - sizeColWidth - resColWidth - tablePadding
	if availFilenameWidth < 12 {
		availFilenameWidth = 12
	}

	m.table.SetColumns([]table.Column{
		{Title: "[ ]", Width: selectColWidth},
		{Title: "Filename", Width: availFilenameWidth},
		{Title: "Size", Width: sizeColWidth},
		{Title: "Resolution", Width: resColWidth},
	})

	totalTableWidth := selectColWidth + availFilenameWidth + sizeColWidth + resColWidth + tablePadding
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
		m.state = ViewSettings
		m.settingCursor = 0
		return m, nil

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
			m.dryRunDone = false
			m.dryRunCtx, m.dryRunCancel = context.WithCancel(context.Background())
			m.dryRunChan = make(chan interface{}, 200)
			go m.pipeline.RunDryRun(m.dryRunCtx, toProcess, m.dryRunChan)
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
	m.state = ViewDashboard
	m.totalVideos = len(toProcess)
	m.processedVideos = 0
	m.skippedCount = 0
	m.failedCount = 0
	m.totalSavedBytes = 0
	m.failedVideos = nil
	m.skippedVideos = nil
	m.ctx, m.cancelCtx = context.WithCancel(context.Background())
	m.pipelineChan = make(chan interface{}, 200)
	go m.pipeline.Run(m.ctx, toProcess, m.pipelineChan)
	return m, waitForPipeline(m.pipelineChan)
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

func (m *AppModel) updateSettings(msg tea.KeyMsg) (tea.Model, tea.Cmd) {

	// When the threshold row (index 6) is focused and user is typing into it,
	// Enter confirms the value; everything else feeds the input.
	if m.settingCursor == 6 && m.thresholdInput.Focused() {
		if msg.String() == "enter" {
			m.thresholdInput.Blur()
			thresh := strings.TrimSpace(m.thresholdInput.Value())
			if thresh == "" {
				thresh = "500"
				m.thresholdInput.SetValue(thresh)
			}
			m.cfg.SizeThreshold = thresh
		} else {
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
		if m.settingCursor < 8 {
			m.settingCursor++
		}

	case "left", "h":
		retryOptions := []int{0, 1, 2, 3, 5}
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
		} else if m.settingCursor == 2 {
			m.cfg.SizeGuard = !m.cfg.SizeGuard
		} else if m.settingCursor == 3 {
			m.cfg.SkipHEVC = !m.cfg.SkipHEVC
		} else if m.settingCursor == 4 {
			m.cfg.EnablePrefetch = !m.cfg.EnablePrefetch
		} else if m.settingCursor == 5 {
			if m.cfg.ConcurrentEncoders > 1 {
				m.cfg.ConcurrentEncoders = 1
			}
		} else if m.settingCursor == 7 {
			m.cfg.LogLevel = "info"
			logger.SetLevelString(m.cfg.LogLevel)
		} else if m.settingCursor == 8 {
			for i, val := range retryOptions {
				if val == m.cfg.MaxRetries && i > 0 {
					m.cfg.MaxRetries = retryOptions[i-1]
					m.pipeline.Client().SetMaxRetries(m.cfg.MaxRetries)
					break
				}
			}
		}

	case "right", "l":
		retryOptions := []int{0, 1, 2, 3, 5}
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
		} else if m.settingCursor == 2 {
			m.cfg.SizeGuard = !m.cfg.SizeGuard
		} else if m.settingCursor == 3 {
			m.cfg.SkipHEVC = !m.cfg.SkipHEVC
		} else if m.settingCursor == 4 {
			m.cfg.EnablePrefetch = !m.cfg.EnablePrefetch
		} else if m.settingCursor == 5 {
			if m.cfg.ConcurrentEncoders < 2 {
				m.cfg.ConcurrentEncoders = 2
			}
		} else if m.settingCursor == 7 {
			m.cfg.LogLevel = "debug"
			logger.SetLevelString(m.cfg.LogLevel)
		} else if m.settingCursor == 8 {
			for i, val := range retryOptions {
				if val == m.cfg.MaxRetries && i < len(retryOptions)-1 {
					m.cfg.MaxRetries = retryOptions[i+1]
					m.pipeline.Client().SetMaxRetries(m.cfg.MaxRetries)
					break
				}
			}
		}

	case " ", "enter":
		if m.settingCursor == 2 {
			m.cfg.SizeGuard = !m.cfg.SizeGuard
		} else if m.settingCursor == 3 {
			m.cfg.SkipHEVC = !m.cfg.SkipHEVC
		} else if m.settingCursor == 4 {
			m.cfg.EnablePrefetch = !m.cfg.EnablePrefetch
		} else if m.settingCursor == 5 {
			if m.cfg.ConcurrentEncoders <= 1 {
				m.cfg.ConcurrentEncoders = 2
			} else {
				m.cfg.ConcurrentEncoders = 1
			}
		} else if m.settingCursor == 6 {
			// Activate the threshold text input
			return m, m.thresholdInput.Focus()
		} else if m.settingCursor == 7 {
			if m.cfg.IsDebug() {
				m.cfg.LogLevel = "info"
			} else {
				m.cfg.LogLevel = "debug"
			}
			logger.SetLevelString(m.cfg.LogLevel)
		} else if m.settingCursor == 8 {
			retryOptions := []int{0, 1, 2, 3, 5}
			currIdx := 0
			for i, val := range retryOptions {
				if val == m.cfg.MaxRetries {
					currIdx = i
					break
				}
			}
			m.cfg.MaxRetries = retryOptions[(currIdx+1)%len(retryOptions)]
			m.pipeline.Client().SetMaxRetries(m.cfg.MaxRetries)
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
		// The pipeline will emit PipelineDoneMsg{Paused:true} shortly;
		// that handler will transition to ViewResumePrompt.
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
					if v.OriginalFileName == f.Name {
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
				m.pipelineChan = make(chan interface{}, 200)
				go m.pipeline.Run(m.ctx, toRetry, m.pipelineChan)
				return m, waitForPipeline(m.pipelineChan)
			}
		}
	case "enter", "q", "esc":
		return m, tea.Quit
	}
	return m, nil
}



func RunTUI(cfg *config.Config, pipeline *orchestrator.Pipeline, opts ...Option) error {
	m := NewAppModel(cfg, pipeline, opts...)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
