package tui

import (
	"context"
	"errors"
	"fmt"
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
	customFlagsInput textinput.Model // Custom FFmpeg flags in Settings (Tab 3: Advanced)
	customFlagsError string          // inline validation error for custom flags

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

// skippedVideo holds the name, reason and size details for a skipped video.
type skippedVideo struct {
	Name          string
	Reason        string
	OriginalSize  int64
	OptimizedSize int64
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
	logger.SetRotationConfig(logger.RotationConfig{
		MaxSizeMB:  cfg.LogMaxSizeMB,
		MaxBackups: cfg.LogMaxBackups,
		Compress:   cfg.LogCompress,
	})
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

	cfi := textinput.New()
	cfi.Placeholder = "e.g. -preset slow -tune animation"
	cfi.SetValue(cfg.CustomFFmpegFlags)
	cfi.CharLimit = 256
	cfi.Width = 60

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
		customFlagsInput: cfi,
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
			origSize := msg.OriginalSize
			if origSize == 0 {
				for _, v := range m.videos {
					if v.ID == msg.VideoID {
						origSize = v.GetSize()
						break
					}
				}
			}
			m.skippedVideos = append(m.skippedVideos, skippedVideo{
				Name:          completedName,
				Reason:        reason,
				OriginalSize:  origSize,
				OptimizedSize: msg.OptimizedSize,
			})
			if reason == "transcoded file larger" && msg.OptimizedSize >= origSize && origSize > 0 {
				diff := msg.OptimizedSize - origSize
				m.addLog(fmt.Sprintf("%s - Skipped %s (Size Guard: transcoded %s > original %s, +%s)",
					timeStr, completedName, formatByteSize(msg.OptimizedSize), formatByteSize(origSize), formatByteSize(diff)))
			} else {
				m.addLog(fmt.Sprintf("%s - Skipped %s (%s)", timeStr, completedName, reason))
			}
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

func RunTUI(cfg *config.Config, pipeline *orchestrator.Pipeline, opts ...Option) error {
	m := NewAppModel(cfg, pipeline, opts...)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
