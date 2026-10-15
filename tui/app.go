package tui

import (
	"context"
	"errors"
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
	"immich-video-optimizer/engine"
	"immich-video-optimizer/immich"
	"immich-video-optimizer/logger"
	"immich-video-optimizer/orchestrator"
	"immich-video-optimizer/state"
	"immich-video-optimizer/version"
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

type UpdateAvailableMsg struct {
	Info *version.UpdateInfo
}

func checkVersionCmd(currentVersion string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		info, err := version.CheckLatestRelease(ctx, version.ReleaseRepo, currentVersion)
		if err != nil || info == nil || !info.HasUpdate {
			return nil
		}
		return UpdateAvailableMsg{Info: info}
	}
}

type ScanCompleteMsg struct {
	Seq    int
	Result *immich.QueryResult
	Err    error
}

func testConnectionCmd(pipeline *orchestrator.Pipeline) tea.Cmd {
	return func() tea.Msg {
		err := pipeline.ValidateConnection()
		return ConnectionTestedMsg{Err: err}
	}
}

func scanVideosCmd(ctx context.Context, pipeline *orchestrator.Pipeline, mode string, threshold float64, seq int) tea.Cmd {
	return func() tea.Msg {
		res, err := pipeline.ScanVideosWithContext(ctx, mode, threshold)
		return ScanCompleteMsg{Seq: seq, Result: res, Err: err}
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

type pulseTickMsg time.Time

func tickPulseCmd() tea.Cmd {
	return tea.Tick(350*time.Millisecond, func(t time.Time) tea.Msg {
		return pulseTickMsg(t)
	})
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

	// Version & Updates
	updateInfo *version.UpdateInfo

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
	settingsTab     int // 0: Encoding, 1: Filtering, 2: Pipeline & Network
	settingCursor   int
	resolutions     []string
	thresholdInput  textinput.Model // typeable threshold in Settings
	dateAfterInput  textinput.Model // Start Date (After) in Settings
	dateBeforeInput textinput.Model // End Date (Before) in Settings
	dateError       string          // inline validation error for date inputs

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
	currentMetadataName     string
	currentUploadName       string
	currentFinalizeName     string
	currentTranscodeTime    string
	probingStatus           string
	fps                     float64
	speed                   string
	transcodedVideos        []transcodedVideo // tracks each successfully transcoded video with space saved
	failedVideos            []failureEntry    // tracks each failed video with its error message
	skippedVideos           []skippedVideo    // tracks each skipped video with its reason

	// Scan
	scanCancel        context.CancelFunc
	scanSeq           int

	pulseFrame        int

	// Dry Run
	dryRunChan        chan interface{}
	dryRunCtx         context.Context
	dryRunCancel      context.CancelFunc
	dryRunTotal       int
	dryRunProbed      int
	dryRunToProcess   []immich.Asset
	dryRunHEVC        []immich.Asset
	dryRunProbeFailed []immich.Asset
	dryRunDetails     map[string]engine.ProbeDetails
	dryRunDone        bool
	dryRunVideos      []immich.Asset // the selection that triggered the dry run
	dryRunAlbumName   string
	dryRunAlbumID     string

	// Multi-Transcoder tracking
	transcoderWorkers map[int]*TranscoderWorkerStatus
}

// TranscoderWorkerStatus holds the live transcoding status of a dedicated worker slot.
type TranscoderWorkerStatus struct {
	WorkerID     int
	VideoID      string
	VideoName    string
	OriginalSize int64
	Resolution   string
	Stage        string // "transcoding", "metadata", "idle"
	Time         string
	Speed        string
	FPS          float64
	PulseFrame   int
}

// transcodedVideo holds details for a successfully transcoded and replaced video.
type transcodedVideo struct {
	VideoID      string
	Name         string
	OriginalSize int64
	NewSize      int64
	SavedBytes   int64
}

// skippedVideo holds the name and reason for a skipped video.
type skippedVideo struct {
	Name   string
	Reason string
}

// failureEntry holds the video ID, name and error message for a failed video.
type failureEntry struct {
	VideoID string
	Name    string
	Err     string
}

func (m *AppModel) getWorker(workerID int) *TranscoderWorkerStatus {
	if workerID <= 0 {
		workerID = 1
	}
	if m.transcoderWorkers == nil {
		m.transcoderWorkers = make(map[int]*TranscoderWorkerStatus)
	}
	w, ok := m.transcoderWorkers[workerID]
	if !ok {
		w = &TranscoderWorkerStatus{
			WorkerID: workerID,
			Stage:    "idle",
		}
		m.transcoderWorkers[workerID] = w
	}
	return w
}

func (m *AppModel) findWorker(workerID int, videoID string) *TranscoderWorkerStatus {
	if workerID > 0 {
		return m.getWorker(workerID)
	}
	if videoID != "" && m.transcoderWorkers != nil {
		for _, w := range m.transcoderWorkers {
			if w.VideoID == videoID {
				return w
			}
		}
	}
	return m.getWorker(1)
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
		cfg.SizeThreshold = "100"
	}
	thi := textinput.New()
	thi.Placeholder = "e.g. 100"
	thi.SetValue(cfg.SizeThreshold)
	thi.CharLimit = 10
	thi.Width = 15

	// Date filter inputs used in Settings
	dai := textinput.New()
	dai.Placeholder = "YYYY-MM-DD (empty for all)"
	dai.SetValue(cfg.DateAfter)
	dai.CharLimit = 10
	dai.Width = 28

	dbi := textinput.New()
	dbi.Placeholder = "YYYY-MM-DD (empty for all)"
	dbi.SetValue(cfg.DateBefore)
	dbi.CharLimit = 10
	dbi.Width = 28

	// Search input
	si := textinput.New()
	si.Placeholder = "Filter by filename..."
	si.CharLimit = 156
	si.Width = 40

	// Video table
	t := table.New(
		table.WithColumns([]table.Column{
			{Title: "[ ]", Width: 4},
			{Title: "Filename", Width: 35},
			{Title: "Size", Width: 9},
			{Title: "MB/min", Width: 10},
			{Title: "Resolution", Width: 10},
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
		dateAfterInput: dai,
		dateBeforeInput: dbi,
		setupFocus:     0,
		table:          t,
		searchInput:    si,
		selectedVideos: make(map[string]bool),
		resolutions:       []string{"Original", "4K", "1440p", "1080p", "720p"},
		overallProgress:   op,
		transcoderWorkers: make(map[int]*TranscoderWorkerStatus),
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
	cmds = append(cmds, checkVersionCmd(version.Current))
	return tea.Batch(cmds...)
}

func (m *AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case UpdateAvailableMsg:
		m.updateInfo = msg.Info
		return m, nil

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
		if m.state != ViewScanning {
			return m, nil
		}
		if msg.Seq != 0 && msg.Seq != m.scanSeq {
			return m, nil
		}
		if m.scanCancel != nil {
			m.scanCancel = nil
		}
		if msg.Err != nil {
			if errors.Is(msg.Err, context.Canceled) {
				return m, nil
			}
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
			if m.dryRunDetails == nil {
				m.dryRunDetails = make(map[string]engine.ProbeDetails)
			}
			m.dryRunDetails[msg.VideoID] = engine.ProbeDetails{
				Codec:      msg.Codec,
				Resolution: msg.Resolution,
				IsHEVC:     msg.IsHEVC,
			}
		}
		return m, waitForDryRun(m.dryRunChan)

	case orchestrator.DryRunCompleteMsg:
		if m.state == ViewDryRun {
			m.dryRunToProcess = msg.ToProcess
			m.dryRunHEVC = msg.HEVCSkipped
			m.dryRunProbeFailed = msg.ProbeFailed
			if msg.VideoDetails != nil {
				m.dryRunDetails = msg.VideoDetails
			}
			m.dryRunAlbumName = msg.DryRunAlbumName
			m.dryRunAlbumID = msg.DryRunAlbumID
			m.dryRunDone = true
		}
		return m, nil

	case orchestrator.PipelineStartMsg:
		m.totalVideos = msg.Total
		m.processedVideos = 0
		encCount := m.cfg.ConcurrentEncoders
		if encCount <= 0 {
			encCount = 1
		} else if encCount > 2 {
			encCount = 2
		}
		m.transcoderWorkers = make(map[int]*TranscoderWorkerStatus)
		for i := 1; i <= encCount; i++ {
			m.transcoderWorkers[i] = &TranscoderWorkerStatus{
				WorkerID: i,
				Stage:    "idle",
			}
		}
		return m, waitForPipeline(m.pipelineChan)

	case orchestrator.PipelineVideoStartMsg:
		w := m.findWorker(msg.WorkerID, msg.VideoID)
		w.VideoID = msg.VideoID
		w.OriginalSize = msg.Size
		w.Resolution = msg.Resolution
		w.Stage = "transcoding"
		w.Time = ""
		w.Speed = ""
		w.FPS = 0
		if msg.VideoName != "" {
			w.VideoName = msg.VideoName
		} else {
			for _, v := range m.videos {
				if v.ID == msg.VideoID {
					w.VideoName = v.OriginalFileName
					if w.Resolution == "" {
						w.Resolution = fmt.Sprintf("%dx%d", v.ExifInfo.ExifImageWidth, v.ExifInfo.ExifImageHeight)
					}
					break
				}
			}
		}

		m.currentVideoID = msg.VideoID
		m.currentVideoOriginalSize = msg.Size
		m.currentVideoName = w.VideoName
		m.currentTranscodeName = w.VideoName
		m.currentVideoResolution = w.Resolution
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
		case "download-idle":
			if msg.VideoName == "" || m.currentDownloadName == msg.VideoName {
				m.currentDownloadName = ""
			}
		case "transcoding":
			w := m.findWorker(msg.WorkerID, msg.VideoID)
			w.Stage = "transcoding"
			if msg.VideoName != "" {
				w.VideoName = msg.VideoName
			}
			m.currentTranscodeName = msg.VideoName
			m.currentMetadataName = ""
			m.currentFinalizeName = ""
			// Download is done for this item — clear the prefetch lane
			if m.currentDownloadName == msg.VideoName {
				m.currentDownloadName = ""
			}
		case "metadata":
			w := m.findWorker(msg.WorkerID, msg.VideoID)
			w.Stage = "metadata"
			if msg.VideoName != "" {
				w.VideoName = msg.VideoName
			}
			w.Speed = ""
			w.FPS = 0
			w.Time = ""
			m.currentMetadataName = msg.VideoName
			m.currentTranscodeName = ""
			m.speed = ""
			m.fps = 0
			m.currentTranscodeTime = ""
			m.addLog(fmt.Sprintf("%s - Preserving metadata for %s (ExifTool)", time.Now().Format("15:04:05"), msg.VideoName))
		case "uploading":
			m.currentUploadName = msg.VideoName
			m.currentMetadataName = ""
			m.currentTranscodeName = ""
			m.speed = ""
			m.fps = 0
			m.currentTranscodeTime = ""
			m.addLog(fmt.Sprintf("%s - Uploading replacement for %s", time.Now().Format("15:04:05"), msg.VideoName))
		case "finalizing":
			m.currentFinalizeName = msg.VideoName
			m.currentUploadName = ""
		case "idle":
			if msg.WorkerID > 0 {
				w := m.getWorker(msg.WorkerID)
				w.Stage = "idle"
				w.VideoID = ""
				w.VideoName = ""
				w.Speed = ""
				w.FPS = 0
				w.Time = ""
			} else {
				m.currentDownloadName = ""
				m.currentTranscodeName = ""
				m.currentMetadataName = ""
				m.currentUploadName = ""
				m.currentFinalizeName = ""
				m.probingStatus = ""
				m.speed = ""
				m.fps = 0
				m.currentTranscodeTime = ""
				for _, w := range m.transcoderWorkers {
					w.Stage = "idle"
					w.VideoID = ""
					w.VideoName = ""
					w.Speed = ""
					w.FPS = 0
					w.Time = ""
				}
			}
		}
		return m, waitForPipeline(m.pipelineChan)

	case orchestrator.PipelineProgressMsg:
		m.pulseFrame++
		w := m.findWorker(msg.WorkerID, msg.VideoID)
		w.PulseFrame++
		w.FPS = msg.EngineProgress.FPS
		w.Speed = msg.EngineProgress.Speed
		if msg.EngineProgress.Time != "" {
			w.Time = msg.EngineProgress.Time
		}
		m.fps = msg.EngineProgress.FPS
		m.speed = msg.EngineProgress.Speed
		if msg.EngineProgress.Time != "" {
			m.currentTranscodeTime = msg.EngineProgress.Time
		}
		return m, waitForPipeline(m.pipelineChan)

	case pulseTickMsg:
		if m.state == ViewDashboard {
			m.pulseFrame++
			for _, w := range m.transcoderWorkers {
				if w.Stage == "transcoding" {
					w.PulseFrame++
				}
			}
			return m, tickPulseCmd()
		}
		return m, nil

	case progress.FrameMsg:
		progressModel2, cmd2 := m.overallProgress.Update(msg)
		m.overallProgress = progressModel2.(progress.Model)
		return m, cmd2

	case orchestrator.PipelineVideoCompleteMsg:
		m.processedVideos++
		m.totalSavedBytes += msg.SavedBytes
		m.currentUploadName = ""
		m.currentFinalizeName = ""
		m.currentMetadataName = ""
		m.currentTranscodeName = ""
		m.speed = ""
		m.fps = 0
		m.currentTranscodeTime = ""

		w := m.findWorker(msg.WorkerID, msg.VideoID)
		if w != nil {
			w.Stage = "idle"
			w.VideoID = ""
			w.VideoName = ""
			w.Speed = ""
			w.FPS = 0
			w.Time = ""
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
			m.failedVideos = append(m.failedVideos, failureEntry{VideoID: msg.VideoID, Name: completedName, Err: msg.Err.Error()})
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
			origSize := msg.OriginalSize
			if origSize == 0 {
				for _, v := range m.videos {
					if v.ID == msg.VideoID {
						origSize = v.GetSize()
						break
					}
				}
			}
			newSize := msg.OptimizedSize
			if newSize == 0 && origSize > 0 {
				newSize = origSize - msg.SavedBytes
			}
			m.transcodedVideos = append(m.transcodedVideos, transcodedVideo{
				VideoID:      msg.VideoID,
				Name:         completedName,
				OriginalSize: origSize,
				NewSize:      newSize,
				SavedBytes:   msg.SavedBytes,
			})
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
				if m.scanCancel != nil {
					m.scanCancel()
					m.scanCancel = nil
				}
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
		threshStr := strings.TrimSpace(m.cfg.SizeThreshold)
		threshVal, err := strconv.ParseFloat(threshStr, 64)
		if err != nil || threshVal < 0 || threshStr == "" {
			threshVal = 100
		}
		m.scanSeq++
		seq := m.scanSeq
		var ctx context.Context
		ctx, m.scanCancel = context.WithCancel(context.Background())
		return m, scanVideosCmd(ctx, m.pipeline, m.cfg.ThresholdMode, threshVal, seq)

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
			a.IsFavorite = vs.IsFavorite
			toProcess = append(toProcess, a)
		}
		if len(toProcess) > 0 {
			m.videos = toProcess
			m.state = ViewDashboard
			m.totalVideos = len(toProcess)
			m.processedVideos = 0
			m.skippedCount = 0
			m.failedCount = 0
			m.failedVideos = nil
			m.skippedVideos = nil
			m.transcodedVideos = nil
			if m.session != nil {
				for _, vs := range m.session.Videos {
					if vs.Status == state.StatusCompleted {
						m.transcodedVideos = append(m.transcodedVideos, transcodedVideo{
							VideoID:      vs.ID,
							Name:         vs.OriginalName,
							OriginalSize: vs.OriginalSize,
							NewSize:      vs.OriginalSize - vs.SavedBytes,
							SavedBytes:   vs.SavedBytes,
						})
					} else if vs.Status == state.StatusSkipped {
						m.skippedCount++
						m.skippedVideos = append(m.skippedVideos, skippedVideo{
							Name:   vs.OriginalName,
							Reason: vs.ErrorMessage,
						})
					} else if vs.Status == state.StatusFailed {
						m.failedCount++
						m.failedVideos = append(m.failedVideos, failureEntry{
							VideoID: vs.ID,
							Name:    vs.OriginalName,
							Err:     vs.ErrorMessage,
						})
					}
				}
			}
			m.totalSavedBytes = m.session.GetTotalSavedBytes()
			m.ctx, m.cancelCtx = context.WithCancel(context.Background())
			m.pipelineChan = make(chan interface{}, 200)
			if m.pipeline != nil {
				go m.pipeline.Run(m.ctx, toProcess, m.pipelineChan)
			} else {
				close(m.pipelineChan)
			}
			return m, tea.Batch(waitForPipeline(m.pipelineChan), tickPulseCmd())
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

func (m *AppModel) settingsMaxCursor() int {
	switch m.settingsTab {
	case 1:
		return 4 // Tab 1 (Filtering): 0: Mode, 1: Value, 2: Exclude Optimized, 3: Start Date, 4: End Date
	case 2:
		return 3 // Tab 2 (Pipeline & Network): 0: Prefetch, 1: Encoders, 2: Log Level, 3: Retries
	default:
		return 3 // Tab 0 (Encoding): 0: Resolution, 1: Quality, 2: Size Guard, 3: Skip HEVC
	}
}

func (m *AppModel) switchSettingsTab(newTab int) {
	if newTab < 0 {
		newTab = 0
	} else if newTab > 2 {
		newTab = 2
	}
	if m.settingsTab != newTab {
		m.settingsTab = newTab
		m.settingCursor = 0
		if m.thresholdInput.Focused() {
			m.thresholdInput.Blur()
		}
		if m.dateAfterInput.Focused() {
			m.dateAfterInput.Blur()
		}
		if m.dateBeforeInput.Focused() {
			m.dateBeforeInput.Blur()
		}
		m.dateError = ""
	}
}

func (m *AppModel) handleSettingsOptionChange(dir int) {
	thresholdModes := []string{config.ThresholdModeMBPerMin, config.ThresholdModeTotalMB, config.ThresholdModeDisabled}
	retryOptions := []int{0, 1, 2, 3, 5}

	switch m.settingsTab {
	case 0: // Encoding
		switch m.settingCursor {
		case 0: // Resolution
			for i, r := range m.resolutions {
				if r == m.cfg.Resolution {
					target := i + dir
					if target >= 0 && target < len(m.resolutions) {
						m.cfg.Resolution = m.resolutions[target]
					}
					break
				}
			}
		case 1: // Video Quality
			q := m.cfg.Quality() + dir
			if q < 1 {
				q = 1
			} else if q > 10 {
				q = 10
			}
			m.cfg.VideoQuality = q
		case 2: // Size Guard
			m.cfg.SizeGuard = !m.cfg.SizeGuard
		case 3: // Skip HEVC
			m.cfg.SkipHEVC = !m.cfg.SkipHEVC
		}

	case 1: // Filtering
		switch m.settingCursor {
		case 0: // Threshold Mode
			currIdx := 0
			for i, mode := range thresholdModes {
				if mode == m.cfg.ThresholdMode {
					currIdx = i
					break
				}
			}
			m.cfg.ThresholdMode = thresholdModes[(currIdx+dir+len(thresholdModes))%len(thresholdModes)]
		case 1: // Threshold Value
			// Focusing handled via enter / space
		case 2: // Exclude Optimized
			m.cfg.ExcludeOptimized = !m.cfg.ExcludeOptimized
		}

	case 2: // Pipeline & Network
		switch m.settingCursor {
		case 0: // Pipeline Prefetch
			m.cfg.EnablePrefetch = !m.cfg.EnablePrefetch
		case 1: // Concurrent Encoders
			if dir < 0 {
				m.cfg.ConcurrentEncoders = 1
			} else {
				m.cfg.ConcurrentEncoders = 2
			}
		case 2: // Logging Level
			if dir < 0 {
				m.cfg.LogLevel = "info"
			} else {
				m.cfg.LogLevel = "debug"
			}
			logger.SetLevelString(m.cfg.LogLevel)
		case 3: // Network Retries
			for i, val := range retryOptions {
				if val == m.cfg.MaxRetries {
					target := i + dir
					if target >= 0 && target < len(retryOptions) {
						m.cfg.MaxRetries = retryOptions[target]
						if m.pipeline != nil && m.pipeline.Client() != nil {
							m.pipeline.Client().SetMaxRetries(m.cfg.MaxRetries)
						}
					}
					break
				}
			}
		}
	}
}

func (m *AppModel) handleSettingsOptionToggle() (tea.Model, tea.Cmd) {
	thresholdModes := []string{config.ThresholdModeMBPerMin, config.ThresholdModeTotalMB, config.ThresholdModeDisabled}
	retryOptions := []int{0, 1, 2, 3, 5}

	switch m.settingsTab {
	case 0: // Encoding
		switch m.settingCursor {
		case 0: // Resolution
			for i, r := range m.resolutions {
				if r == m.cfg.Resolution {
					m.cfg.Resolution = m.resolutions[(i+1)%len(m.resolutions)]
					break
				}
			}
		case 1: // Video Quality
			q := m.cfg.Quality() + 1
			if q > 10 {
				q = 1
			}
			m.cfg.VideoQuality = q
		case 2: // Size Guard
			m.cfg.SizeGuard = !m.cfg.SizeGuard
		case 3: // Skip HEVC
			m.cfg.SkipHEVC = !m.cfg.SkipHEVC
		}

	case 1: // Filtering
		switch m.settingCursor {
		case 0: // Threshold Mode
			currIdx := 0
			for i, mode := range thresholdModes {
				if mode == m.cfg.ThresholdMode {
					currIdx = i
					break
				}
			}
			m.cfg.ThresholdMode = thresholdModes[(currIdx+1)%len(thresholdModes)]
		case 1: // Threshold Value
			if m.cfg.ThresholdMode != config.ThresholdModeDisabled {
				return m, m.thresholdInput.Focus()
			}
		case 2: // Exclude Optimized
			m.cfg.ExcludeOptimized = !m.cfg.ExcludeOptimized
		case 3: // Start Date (After)
			m.dateError = ""
			return m, m.dateAfterInput.Focus()
		case 4: // End Date (Before)
			m.dateError = ""
			return m, m.dateBeforeInput.Focus()
		}

	case 2: // Pipeline & Network
		switch m.settingCursor {
		case 0: // Pipeline Prefetch
			m.cfg.EnablePrefetch = !m.cfg.EnablePrefetch
		case 1: // Concurrent Encoders
			if m.cfg.ConcurrentEncoders <= 1 {
				m.cfg.ConcurrentEncoders = 2
			} else {
				m.cfg.ConcurrentEncoders = 1
			}
		case 2: // Logging Level
			if m.cfg.IsDebug() {
				m.cfg.LogLevel = "info"
			} else {
				m.cfg.LogLevel = "debug"
			}
			logger.SetLevelString(m.cfg.LogLevel)
		case 3: // Network Retries
			currIdx := 0
			for i, val := range retryOptions {
				if val == m.cfg.MaxRetries {
					currIdx = i
					break
				}
			}
			m.cfg.MaxRetries = retryOptions[(currIdx+1)%len(retryOptions)]
			if m.pipeline != nil && m.pipeline.Client() != nil {
				m.pipeline.Client().SetMaxRetries(m.cfg.MaxRetries)
			}
		}

	}
	return m, nil
}

func (m *AppModel) updateSettings(msg tea.KeyMsg) (tea.Model, tea.Cmd) {

	// When the threshold row (Tab 1, index 1) is focused and user is typing into it,
	// Enter confirms the value; Esc cancels editing; everything else feeds the input.
	if m.settingsTab == 1 && m.settingCursor == 1 && m.thresholdInput.Focused() {
		switch msg.String() {
		case "enter":
			m.thresholdInput.Blur()
			thresh := strings.TrimSpace(m.thresholdInput.Value())
			if thresh == "" {
				thresh = "100"
				m.thresholdInput.SetValue(thresh)
			}
			m.cfg.SizeThreshold = thresh
			return m, nil
		case "esc":
			m.thresholdInput.Blur()
			return m, nil
		default:
			var cmd tea.Cmd
			m.thresholdInput, cmd = m.thresholdInput.Update(msg)
			return m, cmd
		}
	}

	// Start Date (After) input
	if m.settingsTab == 1 && m.settingCursor == 3 && m.dateAfterInput.Focused() {
		switch msg.String() {
		case "enter":
			val := strings.TrimSpace(m.dateAfterInput.Value())
			if val != "" {
				t, err := time.Parse("2006-01-02", val)
				if err != nil {
					m.dateError = "Invalid date format. Expected YYYY-MM-DD"
					return m, nil
				}
				if m.cfg.DateBefore != "" {
					if tEnd, err := time.Parse("2006-01-02", m.cfg.DateBefore); err == nil {
						if t.After(tEnd) {
							m.dateError = "Start date cannot be after end date"
							return m, nil
						}
					}
				}
			}
			m.dateError = ""
			m.cfg.DateAfter = val
			m.dateAfterInput.SetValue(val)
			m.dateAfterInput.Blur()
			if m.pipeline != nil {
				m.pipeline.UpdateConfig(m.cfg)
			}
			_ = config.SaveConfig(m.cfg)
			return m, nil
		case "esc":
			m.dateError = ""
			m.dateAfterInput.SetValue(m.cfg.DateAfter)
			m.dateAfterInput.Blur()
			return m, nil
		default:
			m.dateError = ""
			var cmd tea.Cmd
			m.dateAfterInput, cmd = m.dateAfterInput.Update(msg)
			return m, cmd
		}
	}

	// End Date (Before) input
	if m.settingsTab == 1 && m.settingCursor == 4 && m.dateBeforeInput.Focused() {
		switch msg.String() {
		case "enter":
			val := strings.TrimSpace(m.dateBeforeInput.Value())
			if val != "" {
				t, err := time.Parse("2006-01-02", val)
				if err != nil {
					m.dateError = "Invalid date format. Expected YYYY-MM-DD"
					return m, nil
				}
				if m.cfg.DateAfter != "" {
					if tStart, err := time.Parse("2006-01-02", m.cfg.DateAfter); err == nil {
						if t.Before(tStart) {
							m.dateError = "End date cannot be before start date"
							return m, nil
						}
					}
				}
			}
			m.dateError = ""
			m.cfg.DateBefore = val
			m.dateBeforeInput.SetValue(val)
			m.dateBeforeInput.Blur()
			if m.pipeline != nil {
				m.pipeline.UpdateConfig(m.cfg)
			}
			_ = config.SaveConfig(m.cfg)
			return m, nil
		case "esc":
			m.dateError = ""
			m.dateBeforeInput.SetValue(m.cfg.DateBefore)
			m.dateBeforeInput.Blur()
			return m, nil
		default:
			m.dateError = ""
			var cmd tea.Cmd
			m.dateBeforeInput, cmd = m.dateBeforeInput.Update(msg)
			return m, cmd
		}
	}

	switch msg.String() {
	case "q", "esc":
		_ = config.SaveConfig(m.cfg)
		if len(m.videos) > 0 {
			m.state = ViewBrowser
		} else {
			m.state = ViewScanPrompt
		}
		return m, nil

	case "tab":
		m.switchSettingsTab((m.settingsTab + 1) % 3)
		return m, nil

	case "shift+tab":
		m.switchSettingsTab((m.settingsTab - 1 + 3) % 3)
		return m, nil

	case "1":
		m.switchSettingsTab(0)
		return m, nil

	case "2":
		m.switchSettingsTab(1)
		return m, nil

	case "3":
		m.switchSettingsTab(2)
		return m, nil

	case "up", "k":
		if m.settingCursor > 0 {
			m.settingCursor--
		}

	case "down", "j":
		if m.settingCursor < m.settingsMaxCursor() {
			m.settingCursor++
		}

	case "left", "h":
		m.handleSettingsOptionChange(-1)

	case "right", "l":
		m.handleSettingsOptionChange(1)

	case " ", "enter":
		return m.handleSettingsOptionToggle()
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



func RunTUI(cfg *config.Config, pipeline *orchestrator.Pipeline, opts ...Option) error {
	m := NewAppModel(cfg, pipeline, opts...)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
