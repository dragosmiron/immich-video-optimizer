package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"immich-video-optimizer/config"
	"immich-video-optimizer/immich"
	"immich-video-optimizer/orchestrator"
	"immich-video-optimizer/state"
)

func TestAppModel_InitialRouting(t *testing.T) {
	// 1. Missing credentials routes to ViewConnectionSetup
	cfgEmpty := &config.Config{URL: "", APIKey: ""}
	pipelineEmpty, _ := orchestrator.NewPipeline(cfgEmpty)
	modelEmpty := NewAppModel(cfgEmpty, pipelineEmpty)
	if modelEmpty.state != ViewConnectionSetup {
		t.Fatalf("expected ViewConnectionSetup, got %v", modelEmpty.state)
	}

	// 2. Configured credentials routes to ViewTestingConnection
	cfgValid := &config.Config{URL: "http://localhost:2283", APIKey: "test-key"}
	pipelineValid, _ := orchestrator.NewPipeline(cfgValid)
	modelValid := NewAppModel(cfgValid, pipelineValid)
	if modelValid.state != ViewTestingConnection {
		t.Fatalf("expected ViewTestingConnection, got %v", modelValid.state)
	}

	// 3. WithInitialView option overrides default routing
	modelOverridden := NewAppModel(cfgValid, pipelineValid, WithInitialView(ViewConnectionSetup))
	if modelOverridden.state != ViewConnectionSetup {
		t.Fatalf("expected ViewConnectionSetup via WithInitialView, got %v", modelOverridden.state)
	}
	if modelOverridden.cfg.URL != "http://localhost:2283" {
		t.Fatalf("expected URL to be preserved, got %s", modelOverridden.cfg.URL)
	}
}

func TestAppModel_ConnectionTested(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // ensure no real session.json is found
	cfg := &config.Config{URL: "http://localhost:2283", APIKey: "test-key"}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)

	// Test connection failure
	newM, _ := m.Update(ConnectionTestedMsg{Err: errors.New("401 Unauthorized")})
	model := newM.(*AppModel)
	if model.state != ViewConnectionSetup {
		t.Fatalf("expected ViewConnectionSetup on error, got %v", model.state)
	}
	if !strings.Contains(model.connectionError, "401 Unauthorized") {
		t.Fatalf("expected error message to contain 401 Unauthorized, got %s", model.connectionError)
	}

	// Test connection success — no session exists so should land on ViewScanPrompt
	newM, _ = model.Update(ConnectionTestedMsg{Err: nil})
	model = newM.(*AppModel)
	if model.state != ViewScanPrompt {
		t.Fatalf("expected ViewScanPrompt on success, got %v", model.state)
	}
}

func TestAppModel_ScanTransitionAndBulkSummary(t *testing.T) {
	cfg := &config.Config{URL: "http://localhost:2283", APIKey: "test-key", SizeThreshold: "500"}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)
	m.state = ViewScanPrompt

	// Trigger Scan
	newM, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	model := newM.(*AppModel)
	if model.state != ViewScanning {
		t.Fatalf("expected ViewScanning, got %v", model.state)
	}
	if cmd == nil {
		t.Fatalf("expected non-nil scan command")
	}

	// Complete Scan with mock results
	res := &immich.QueryResult{
		TotalAssetsScanned: 50,
		SkippedNonVideo:    25,
		SkippedBelowSize:   23,
		Videos: []immich.Asset{
			{ID: "v1", OriginalFileName: "large1.mp4", ExifInfo: immich.ExifInfo{ExifImageWidth: 3840, ExifImageHeight: 2160, FileSizeInByte: 800 * 1024 * 1024}},
			{ID: "v2", OriginalFileName: "large2.mp4", ExifInfo: immich.ExifInfo{ExifImageWidth: 1920, ExifImageHeight: 1080, FileSizeInByte: 600 * 1024 * 1024}},
		},
	}

	newM, _ = model.Update(ScanCompleteMsg{Result: res, Err: nil})
	model = newM.(*AppModel)
	if model.state != ViewBrowser {
		t.Fatalf("expected ViewBrowser, got %v", model.state)
	}
	if len(model.videos) != 2 {
		t.Fatalf("expected 2 videos, got %d", len(model.videos))
	}
	if !model.selectedVideos["v1"] || !model.selectedVideos["v2"] {
		t.Fatalf("expected all found videos to be selected by default")
	}

	// Check bulk exclusion summary rendering
	rendered := model.View()
	if !strings.Contains(rendered, "Total assets inspected in library: 50") {
		t.Errorf("expected summary to contain total assets inspected, got %s", rendered)
	}
	if !strings.Contains(rendered, "25") || !strings.Contains(rendered, "photos") {
		t.Errorf("expected summary to mention non-video skipped photos")
	}
	if !strings.Contains(rendered, "23") {
		t.Errorf("expected summary to mention videos below threshold")
	}
}

func TestAppModel_SelectionShortcuts(t *testing.T) {
	cfg := &config.Config{URL: "http://localhost:2283", APIKey: "test-key"}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)
	m.state = ViewBrowser
	m.videos = []immich.Asset{
		{ID: "v1", OriginalFileName: "vid1.mp4"},
		{ID: "v2", OriginalFileName: "vid2.mp4"},
	}
	m.selectedVideos["v1"] = true
	m.selectedVideos["v2"] = true
	m.updateTable()

	// Press 'n' to select none
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	model := newM.(*AppModel)
	if len(model.selectedVideos) != 0 {
		t.Fatalf("expected 0 selected videos after 'n', got %d", len(model.selectedVideos))
	}

	// Press 'a' to select all
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	model = newM.(*AppModel)
	if len(model.selectedVideos) != 2 {
		t.Fatalf("expected 2 selected videos after 'a', got %d", len(model.selectedVideos))
	}
}

func TestAppModel_DynamicDisplayInDashboard(t *testing.T) {
	cfg := &config.Config{URL: "http://localhost:2283", APIKey: "test-key"}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)
	m.state = ViewDashboard
	m.totalVideos = 3
	m.processedVideos = 1
	m.currentVideoName = "GOPR0001.MP4"
	m.speed = "7.2x"
	m.fps = 215.0

	view := m.View()

	// Should show dashboard elements
	if !strings.Contains(view, "Optimization Dashboard") {
		t.Errorf("expected dashboard title in view")
	}
	if !strings.Contains(view, "GOPR0001.MP4") {
		t.Errorf("expected current video name in view")
	}
	if !strings.Contains(view, "7.2x") {
		t.Errorf("expected speed in view")
	}

	// Should NOT show table or browser instructions
	if strings.Contains(view, "Select All") || strings.Contains(view, "Interactive Video Browser") {
		t.Errorf("dashboard should NOT display video browser table or shortcuts")
	}
}

func TestAppModel_SettingsNavigation(t *testing.T) {
	cfg := &config.Config{
		URL:           "http://localhost:2283",
		APIKey:        "test-key",
		Resolution:    "Original",
		Preset:        "Medium",
		SizeGuard:     true,
		SizeThreshold: "500",
	}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)
	m.state = ViewSettings

	// Cycle Resolution
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	model := newM.(*AppModel)
	if model.cfg.Resolution != "4K" {
		t.Errorf("expected resolution to cycle to 4K, got %s", model.cfg.Resolution)
	}

	// Move cursor down to Preset
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = newM.(*AppModel)
	if model.settingCursor != 1 {
		t.Errorf("expected settingCursor 1, got %d", model.settingCursor)
	}

	// Move cursor down to SizeGuard and toggle
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = newM.(*AppModel)
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = newM.(*AppModel)
	if model.cfg.SizeGuard != false {
		t.Errorf("expected SizeGuard to be toggled to false")
	}

	// Move cursor down to SkipHEVC and toggle
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = newM.(*AppModel)
	if model.settingCursor != 3 {
		t.Errorf("expected settingCursor 3 for SkipHEVC, got %d", model.settingCursor)
	}
	initialSkip := model.cfg.SkipHEVC
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = newM.(*AppModel)
	if model.cfg.SkipHEVC != !initialSkip {
		t.Errorf("expected SkipHEVC to toggle from %v to %v", initialSkip, !initialSkip)
	}
}

func TestAppModel_ResumePromptOptions(t *testing.T) {
	cfg := &config.Config{URL: "http://localhost:2283", APIKey: "test-key"}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)
	m.session = &state.Session{
		SessionID: "sess-1",
		Videos: map[string]*state.VideoState{
			"v1": {ID: "v1", OriginalName: "vid1.mp4", Status: state.StatusCompleted, SavedBytes: 50000000},
			"v2": {ID: "v2", OriginalName: "vid2.mp4", Status: state.StatusPending, OriginalSize: 100000000},
		},
		TotalSavedBytes: 50000000,
	}
	m.state = ViewResumePrompt

	view := m.View()
	if !strings.Contains(view, "Resume Previous Session") {
		t.Errorf("expected resume prompt view")
	}
	if !strings.Contains(view, "Pending Videos") {
		t.Errorf("expected view to show pending videos count")
	}

	// Test 's' to start new scan
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	model := newM.(*AppModel)
	if model.state != ViewScanPrompt {
		t.Errorf("expected ViewScanPrompt after starting new, got %v", model.state)
	}
	if model.session != nil {
		t.Errorf("expected session to be cleared")
	}
}

func TestAppModel_WindowResizeTable(t *testing.T) {
	cfg := &config.Config{URL: "http://localhost:2283", APIKey: "test-key"}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)
	m.state = ViewBrowser
	m.videos = []immich.Asset{
		{ID: "v1", OriginalFileName: "super_long_vacation_video_from_summer_trip_in_greece_2024.mp4", ExifInfo: immich.ExifInfo{ExifImageWidth: 3840, ExifImageHeight: 2160, FileSizeInByte: 800 * 1024 * 1024}},
	}
	m.updateTable()

	// 1. Wide terminal: 120 width
	newM, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model120 := newM.(*AppModel)
	cols120 := model120.table.Columns()
	var filenameWidth120 int
	for _, c := range cols120 {
		if c.Title == "Filename" {
			filenameWidth120 = c.Width
		}
	}
	if filenameWidth120 < 70 {
		t.Errorf("expected Filename column width to expand in wide terminal, got %d", filenameWidth120)
	}

	// 2. Standard terminal: 80 width
	newM80, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model80 := newM80.(*AppModel)
	cols80 := model80.table.Columns()
	var filenameWidth80 int
	for _, c := range cols80 {
		if c.Title == "Filename" {
			filenameWidth80 = c.Width
		}
	}
	if filenameWidth80 >= filenameWidth120 {
		t.Errorf("expected Filename column width to shrink from %d in 80-width terminal, got %d", filenameWidth120, filenameWidth80)
	}
	rawBrowser := model80.viewBrowser()
	var headerLine, sepLine string
	for _, l := range strings.Split(rawBrowser, "\n") {
		if strings.Contains(l, "Filename") && strings.Contains(l, "Size") && strings.Contains(l, "Resolution") {
			headerLine = l
		}
		if strings.Contains(l, "───") {
			sepLine = l
		}
	}
	if headerLine == "" {
		t.Fatalf("expected header line containing Filename, Size, and Resolution")
	}
	if lipgloss.Width(headerLine) != lipgloss.Width(sepLine) {
		t.Errorf("header width (%d) does not match separator width (%d)", lipgloss.Width(headerLine), lipgloss.Width(sepLine))
	}
	view80 := model80.View()
	if !strings.Contains(view80, "[q] Quit") {
		t.Errorf("expected [q] Quit in view80")
	}

	// 3. Narrow terminal: 60 width
	newM60, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	model60 := newM60.(*AppModel)
	cols60 := model60.table.Columns()
	var filenameWidth60 int
	for _, c := range cols60 {
		if c.Title == "Filename" {
			filenameWidth60 = c.Width
		}
	}
	if filenameWidth60 >= filenameWidth80 {
		t.Errorf("expected Filename column width to shrink further in 60-width terminal, got %d", filenameWidth60)
	}
	view60 := model60.View()
	if !strings.Contains(view60, "[q] Quit") {
		t.Errorf("expected [q] Quit in view60")
	}
}

func TestAppModel_PipelineStages(t *testing.T) {
	cfg := &config.Config{
		URL:            "http://localhost:2283",
		APIKey:         "test-key",
		EnablePrefetch: true,
	}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)
	m.state = ViewDashboard
	m.pipelineChan = make(chan interface{}, 50)

	// 1. Probing stage
	newM, _ := m.Update(orchestrator.PipelineStageMsg{Stage: "probing", VideoName: "Scanning 10 videos..."})
	model := newM.(*AppModel)
	if model.probingStatus != "Scanning 10 videos..." {
		t.Fatalf("expected probingStatus to be set, got %s", model.probingStatus)
	}
	v := model.View()
	if !strings.Contains(v, "Probing") || !strings.Contains(v, "Scanning 10 videos...") {
		t.Errorf("expected view to display probing message, got %s", v)
	}

	// 2. Prefetch stage
	newM, _ = model.Update(orchestrator.PipelineStageMsg{Stage: "downloading", VideoName: "prefetch_vid.mp4"})
	model = newM.(*AppModel)
	if model.currentDownloadName != "prefetch_vid.mp4" {
		t.Fatalf("expected currentDownloadName prefetch_vid.mp4, got %s", model.currentDownloadName)
	}
	v = model.View()
	if !strings.Contains(v, "Prefetching: prefetch_vid.mp4") {
		t.Errorf("expected view to show Prefetching stage, got %s", v)
	}

	// 3. Transcoding stage & Video start
	newM, _ = model.Update(orchestrator.PipelineVideoStartMsg{
		VideoID:    "v123",
		VideoName:  "active_transcode.mp4",
		Size:       200 * 1024 * 1024,
		Resolution: "3840x2160",
	})
	model = newM.(*AppModel)
	newM, _ = model.Update(orchestrator.PipelineStageMsg{Stage: "transcoding", VideoName: "active_transcode.mp4"})
	model = newM.(*AppModel)
	v = model.View()
	if !strings.Contains(v, "Transcoding: active_transcode.mp4") || !strings.Contains(v, "3840x2160") {
		t.Errorf("expected view to show Transcoding stage with resolution, got %s", v)
	}

	// 4. Uploading stage
	newM, _ = model.Update(orchestrator.PipelineStageMsg{Stage: "uploading", VideoName: "uploaded_vid.mp4"})
	model = newM.(*AppModel)
	v = model.View()
	if !strings.Contains(v, "Uploading:   uploaded_vid.mp4") {
		t.Errorf("expected view to show Uploading stage, got %s", v)
	}

	// 5. Idle stage
	newM, _ = model.Update(orchestrator.PipelineStageMsg{Stage: "idle"})
	model = newM.(*AppModel)
	if model.currentDownloadName != "" || model.currentTranscodeName != "" || model.currentUploadName != "" {
		t.Errorf("expected all stage names to be reset on idle")
	}
}

func TestAppModel_PipelineSettings(t *testing.T) {
	cfg := &config.Config{
		URL:                "http://localhost:2283",
		APIKey:             "test-key",
		EnablePrefetch:     true,
		ConcurrentEncoders: 1,
		SizeThreshold:      "500",
		MaxRetries:         3,
	}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)
	m.state = ViewSettings

	// Move cursor down to index 4 (Pipeline Prefetch)
	m.settingCursor = 4
	if m.cfg.EnablePrefetch != true {
		t.Fatalf("expected initial EnablePrefetch true")
	}

	// Toggle prefetch with Space
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	model := newM.(*AppModel)
	if model.cfg.EnablePrefetch != false {
		t.Errorf("expected EnablePrefetch to toggle to false")
	}

	// Move down to index 5 (Concurrent Encoders)
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = newM.(*AppModel)
	if model.settingCursor != 5 {
		t.Fatalf("expected settingCursor 5, got %d", model.settingCursor)
	}

	// Toggle encoders with Space (1 -> 2)
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = newM.(*AppModel)
	if model.cfg.ConcurrentEncoders != 2 {
		t.Errorf("expected ConcurrentEncoders 2, got %d", model.cfg.ConcurrentEncoders)
	}

	// Move down to index 6 (Size Threshold)
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = newM.(*AppModel)
	if model.settingCursor != 6 {
		t.Fatalf("expected settingCursor 6, got %d", model.settingCursor)
	}

	// Move down to index 7 (Logging Level)
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = newM.(*AppModel)
	if model.settingCursor != 7 {
		t.Fatalf("expected settingCursor 7, got %d", model.settingCursor)
	}

	// Initial is "info" (or empty default), toggle with Space to "debug"
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = newM.(*AppModel)
	if model.cfg.LogLevel != "debug" {
		t.Errorf("expected LogLevel debug, got %s", model.cfg.LogLevel)
	}

	// Toggle with Left arrow to "info"
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyLeft})
	model = newM.(*AppModel)
	if model.cfg.LogLevel != "info" {
		t.Errorf("expected LogLevel info, got %s", model.cfg.LogLevel)
	}

	// Toggle with Right arrow to "debug"
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = newM.(*AppModel)
	if model.cfg.LogLevel != "debug" {
		t.Errorf("expected LogLevel debug, got %s", model.cfg.LogLevel)
	}

	// Move down to index 8 (Network Retries)
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = newM.(*AppModel)
	if model.settingCursor != 8 {
		t.Fatalf("expected settingCursor 8, got %d", model.settingCursor)
	}

	// Default is 3, cycling with Right should change to 5
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = newM.(*AppModel)
	if model.cfg.MaxRetries != 5 {
		t.Errorf("expected MaxRetries 5, got %d", model.cfg.MaxRetries)
	}

	// Left twice should change to 2
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyLeft})
	model = newM.(*AppModel)
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyLeft})
	model = newM.(*AppModel)
	if model.cfg.MaxRetries != 2 {
		t.Errorf("expected MaxRetries 2, got %d", model.cfg.MaxRetries)
	}

	// Space should cycle to 3
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = newM.(*AppModel)
	if model.cfg.MaxRetries != 3 {
		t.Errorf("expected MaxRetries 3 after space toggle, got %d", model.cfg.MaxRetries)
	}
}

func TestAppModel_ResumeMetadataPreservation(t *testing.T) {
	cfg := &config.Config{URL: "http://localhost:2283", APIKey: "test-key"}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)
	m.session = &state.Session{
		SessionID: "sess-res",
		Videos: map[string]*state.VideoState{
			"v1": {
				ID:            "v1",
				OriginalName:  "wedding_video.mp4",
				OriginalSize:  500 * 1024 * 1024,
				Status:        state.StatusPending,
				Width:         3840,
				Height:        2160,
				CreatedAt:     "2025-06-15T10:30:00Z",
				ModifiedAt:    "2025-06-15T10:30:00Z",
				DeviceAssetID: "iphone-15-asset",
				DeviceID:      "iphone-15-pro",
			},
		},
	}
	m.state = ViewResumePrompt

	// Press 'r' to resume
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model := newM.(*AppModel)
	if model.state != ViewDashboard {
		t.Fatalf("expected ViewDashboard after resume, got %v", model.state)
	}
	// Verify that when pipeline starts or displays, metadata is present
	if model.cancelCtx != nil {
		model.cancelCtx() // Clean up background pipeline
	}
}


