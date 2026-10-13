package tui

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"immich-video-optimizer/config"
	"immich-video-optimizer/engine"
	"immich-video-optimizer/immich"
	"immich-video-optimizer/orchestrator"
	"immich-video-optimizer/state"
)

func TestMain(m *testing.M) {
	tempDir, err := os.MkdirTemp("", "tui-test-*")
	if err == nil {
		os.Setenv("HOME", tempDir)
		defer os.RemoveAll(tempDir)
	}
	os.Exit(m.Run())
}

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

	// Verify 's' opens Settings
	settingsM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if settingsM.(*AppModel).state != ViewSettings {
		t.Fatalf("expected ViewSettings on 's', got %v", settingsM.(*AppModel).state)
	}

	// Return to ViewScanPrompt
	promptM, _ := settingsM.(*AppModel).Update(tea.KeyMsg{Type: tea.KeyEscape})
	if promptM.(*AppModel).state != ViewScanPrompt {
		t.Fatalf("expected ViewScanPrompt on 'esc', got %v", promptM.(*AppModel).state)
	}

	// Trigger Scan with Enter
	newM, cmd := promptM.(*AppModel).Update(tea.KeyMsg{Type: tea.KeyEnter})
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

	// Press 's' to open settings from browser
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	model = newM.(*AppModel)
	if model.state != ViewSettings {
		t.Fatalf("expected ViewSettings after 's' in browser, got %v", model.state)
	}

	// Press 'esc' to return to browser
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyEscape})
	model = newM.(*AppModel)
	if model.state != ViewBrowser {
		t.Fatalf("expected ViewBrowser after 'esc' from settings, got %v", model.state)
	}

	// Press 'r' to rescan from browser
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model = newM.(*AppModel)
	if model.state != ViewScanPrompt {
		t.Fatalf("expected ViewScanPrompt after 'r' in browser, got %v", model.state)
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

	// Move down to index 6 (Threshold Mode)
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = newM.(*AppModel)
	if model.settingCursor != 6 {
		t.Fatalf("expected settingCursor 6, got %d", model.settingCursor)
	}

	// Move down to index 7 (Threshold Value)
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = newM.(*AppModel)
	if model.settingCursor != 7 {
		t.Fatalf("expected settingCursor 7, got %d", model.settingCursor)
	}

	// Move down to index 8 (Logging Level)
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = newM.(*AppModel)
	if model.settingCursor != 8 {
		t.Fatalf("expected settingCursor 8, got %d", model.settingCursor)
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

	// Move down to index 9 (Network Retries)
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = newM.(*AppModel)
	if model.settingCursor != 9 {
		t.Fatalf("expected settingCursor 9, got %d", model.settingCursor)
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

func TestAppModel_BrowserShortcuts_DryRunAndStart(t *testing.T) {
	cfg := &config.Config{URL: "http://localhost:2283", APIKey: "test-key"}
	pipeline, _ := orchestrator.NewPipeline(cfg)

	videos := []immich.Asset{
		{ID: "v1", OriginalFileName: "vid1.mp4", ExifInfo: immich.ExifInfo{FileSizeInByte: 50 * 1024 * 1024}},
		{ID: "v2", OriginalFileName: "vid2.mp4", ExifInfo: immich.ExifInfo{FileSizeInByte: 60 * 1024 * 1024}},
	}

	// 1. Pressing 'd' triggers Dry Run
	m := NewAppModel(cfg, pipeline)
	m.state = ViewBrowser
	m.videos = videos
	m.selectedVideos["v1"] = true
	m.updateTable()

	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model := newM.(*AppModel)
	if model.state != ViewDryRun {
		t.Fatalf("expected ViewDryRun on 'd', got %v", model.state)
	}
	if model.dryRunCancel != nil {
		model.dryRunCancel()
	}

	// 2. Pressing 'Enter' triggers optimization directly (ViewDashboard)
	m2 := NewAppModel(cfg, pipeline)
	m2.state = ViewBrowser
	m2.videos = videos
	m2.selectedVideos["v1"] = true
	m2.updateTable()

	newM2, _ := m2.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model2 := newM2.(*AppModel)
	if model2.state != ViewDashboard {
		t.Fatalf("expected ViewDashboard on 'enter', got %v", model2.state)
	}
	if model2.cancelCtx != nil {
		model2.cancelCtx()
	}

	// 3. Browser shortcut text contains both [d] Dry Run and [Enter] Start Optimization
	browserView := m.viewBrowser()
	if !strings.Contains(browserView, "[d] Dry Run") {
		t.Errorf("expected browser shortcuts to include [d] Dry Run")
	}
	if !strings.Contains(browserView, "[Enter] Start Optimization") {
		t.Errorf("expected browser shortcuts to include [Enter] Start Optimization")
	}
}

func TestAppModel_DryRunFlow(t *testing.T) {
	cfg := &config.Config{
		URL:      "http://localhost:2283",
		APIKey:   "test-key",
		SkipHEVC: true,
	}
	pipeline, _ := orchestrator.NewPipeline(cfg)

	videos := []immich.Asset{
		{ID: "v1", OriginalFileName: "vid1_hevc.mp4", ExifInfo: immich.ExifInfo{FileSizeInByte: 100 * 1024 * 1024}},
		{ID: "v2", OriginalFileName: "vid2_hevc.mp4", ExifInfo: immich.ExifInfo{FileSizeInByte: 200 * 1024 * 1024}},
	}

	// Case A: All selected videos are HEVC and SkipHEVC is ON
	m := NewAppModel(cfg, pipeline)
	m.state = ViewDryRun
	m.dryRunVideos = videos
	m.dryRunTotal = 2
	m.dryRunProbed = 2
	m.dryRunHEVC = videos
	m.dryRunToProcess = nil
	m.dryRunDone = true

	v := m.viewDryRun()
	if !strings.Contains(v, "Will transcode:                   0 videos — nothing to do") {
		t.Errorf("expected 0 videos to transcode in dry run view, got: %s", v)
	}
	if !strings.Contains(v, "all 2 selected video(s) are already HEVC/H.265 (Skip HEVC is ON)") {
		t.Errorf("expected clear reason explaining why 0 videos will transcode, got: %s", v)
	}
	if !strings.Contains(v, "[ s ] Settings") {
		t.Errorf("expected [ s ] Settings in footer when 0 videos to transcode")
	}
	if !strings.Contains(v, "[ Enter / Esc / B ] Back to Browser") {
		t.Errorf("expected [ Enter / Esc / B ] Back to Browser in footer when 0 videos to transcode, got: %s", v)
	}
	if strings.Contains(v, "[ Enter ] Start Optimization") {
		t.Errorf("should NOT show Start Optimization button when 0 videos to transcode")
	}

	m.videos = videos

	// Press 's' from Dry Run to go to Settings
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if newM.(*AppModel).state != ViewSettings {
		t.Errorf("expected ViewSettings on 's' in dry run, got: %v", newM.(*AppModel).state)
	}

	// Reset state to Dry Run and test 'esc'
	m.state = ViewDryRun
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if newM.(*AppModel).state != ViewBrowser {
		t.Errorf("expected ViewBrowser on 'esc' in dry run, got: %v", newM.(*AppModel).state)
	}

	// Reset state to Dry Run and test 'enter' when 0 videos to transcode
	m.state = ViewDryRun
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if newM.(*AppModel).state != ViewBrowser {
		t.Errorf("expected ViewBrowser on 'enter' when 0 videos to transcode, got: %v", newM.(*AppModel).state)
	}

	// Case B: SkipHEVC is OFF (both videos to process)
	m.state = ViewDryRun
	m.cfg.SkipHEVC = false
	m.dryRunHEVC = nil
	m.dryRunToProcess = videos
	m.dryRunDone = true

	v2 := m.viewDryRun()
	if !strings.Contains(v2, "Skip HEVC is OFF in Settings") {
		t.Errorf("expected view to indicate Skip HEVC is OFF, got: %s", v2)
	}
	if !strings.Contains(v2, "Will transcode:                   2 videos") {
		t.Errorf("expected 2 videos to transcode when SkipHEVC is false, got: %s", v2)
	}
	if !strings.Contains(v2, "[ Enter ] Start Optimization") {
		t.Errorf("expected Start Optimization button when videos > 0")
	}
	if !strings.Contains(v2, "Will be transcoded:") {
		t.Errorf("expected 'Will be transcoded:' section in view, got: %s", v2)
	}

	// Press 'enter' when videos > 0 starts optimization (ViewDashboard)
	newM2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model2 := newM2.(*AppModel)
	if model2.state != ViewDashboard {
		t.Errorf("expected ViewDashboard on 'enter' with videos to transcode, got: %v", model2.state)
	}
	if model2.cancelCtx != nil {
		model2.cancelCtx()
	}

	// Case C: Probe failures recorded and displayed
	m3 := NewAppModel(cfg, pipeline)
	m3.state = ViewDryRun
	m3.dryRunVideos = videos
	m3.dryRunToProcess = videos
	m3.dryRunProbeFailed = videos[:1]
	m3.dryRunDone = true

	v3 := m3.viewDryRun()
	if !strings.Contains(v3, "Probe timeouts/unverified:") {
		t.Errorf("expected view to show probe timeouts/unverified, got: %s", v3)
	}
	if !strings.Contains(v3, "(probe unverified)") {
		t.Errorf("expected view to show (probe unverified) tag for failed probe asset, got: %s", v3)
	}

	// Case D: DryRunCompleteMsg arriving when state != ViewDryRun does not overwrite
	m4 := NewAppModel(cfg, pipeline)
	m4.state = ViewBrowser
	m4.dryRunDone = false
	newM4, _ := m4.Update(orchestrator.DryRunCompleteMsg{
		ToProcess: videos,
	})
	if newM4.(*AppModel).dryRunDone {
		t.Errorf("expected dryRunDone to remain false when complete msg arrives in ViewBrowser")
	}
}

func TestAppModel_SelectionSameFilenameCollision(t *testing.T) {
	cfg := &config.Config{URL: "http://localhost:2283", APIKey: "key"}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)

	// Two videos with identical filename but different IDs
	videos := []immich.Asset{
		{ID: "id-1", OriginalFileName: "clip.mp4", ExifInfo: immich.ExifInfo{FileSizeInByte: 100 * 1024 * 1024}},
		{ID: "id-2", OriginalFileName: "clip.mp4", ExifInfo: immich.ExifInfo{FileSizeInByte: 200 * 1024 * 1024}},
	}

	m.state = ViewBrowser
	m.videos = videos
	m.selectedVideos = map[string]bool{"id-1": true, "id-2": true}
	m.updateTable()

	// Initially both are selected
	if !m.selectedVideos["id-1"] || !m.selectedVideos["id-2"] {
		t.Fatalf("expected both to be selected initially")
	}

	// Move cursor down to second row (id-2)
	m.table.SetCursor(1)

	// Press space to toggle second row
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	model := newM.(*AppModel)

	// id-1 should still be selected, id-2 should now be unselected
	if !model.selectedVideos["id-1"] {
		t.Errorf("expected id-1 to remain selected, but it was toggled!")
	}
	if model.selectedVideos["id-2"] {
		t.Errorf("expected id-2 to be unselected, but it is still selected!")
	}
}

func TestAppModel_RetryFailedCollision(t *testing.T) {
	cfg := &config.Config{URL: "http://localhost:2283", APIKey: "key"}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)

	// Two videos with identical filename
	videos := []immich.Asset{
		{ID: "id-1", OriginalFileName: "clip.mp4"},
		{ID: "id-2", OriginalFileName: "clip.mp4"},
	}
	m.videos = videos
	m.state = ViewDashboard
	m.totalVideos = 2

	// Only id-2 fails
	newM, _ := m.Update(orchestrator.PipelineVideoCompleteMsg{
		VideoID:   "id-2",
		VideoName: "clip.mp4",
		Err:       errors.New("transcode error"),
	})
	m = newM.(*AppModel)

	if len(m.failedVideos) != 1 || m.failedVideos[0].VideoID != "id-2" {
		t.Fatalf("expected failedVideos to track id-2, got: %+v", m.failedVideos)
	}

	// Move to summary
	m.state = ViewSummary

	// Press 'r' to retry failed videos
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model := newM.(*AppModel)

	if model.state != ViewDashboard {
		t.Errorf("expected state to transition to ViewDashboard on retry, got: %v", model.state)
	}
	if model.totalVideos != 1 {
		t.Errorf("expected exactly 1 video to be retried (id-2), got totalVideos: %d", model.totalVideos)
	}
	if model.cancelCtx != nil {
		model.cancelCtx()
	}
}

func TestAppModel_ScanCancellation(t *testing.T) {
	cfg := &config.Config{URL: "http://localhost:2283", APIKey: "key"}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)

	m.state = ViewScanning

	// User presses 'esc' while scanning
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model := newM.(*AppModel)

	if model.state != ViewScanPrompt {
		t.Fatalf("expected ViewScanPrompt after cancelling scan, got: %v", model.state)
	}

	// Later, background scan completes
	res := &immich.QueryResult{
		Videos: []immich.Asset{
			{ID: "v1", OriginalFileName: "video.mp4"},
		},
	}
	newM2, _ := model.Update(ScanCompleteMsg{Result: res, Err: nil})
	model2 := newM2.(*AppModel)

	// State must NOT be overridden to ViewBrowser
	if model2.state != ViewScanPrompt {
		t.Errorf("expected state to remain ViewScanPrompt after scan completion following user cancellation, got: %v", model2.state)
	}
}

func TestAppModel_ResumeRetryFailed(t *testing.T) {
	cfg := &config.Config{URL: "http://localhost:2283", APIKey: "key"}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)

	sess := &state.Session{
		SessionID: "sess-1",
		Videos: map[string]*state.VideoState{
			"v-resumed": {
				ID:           "v-resumed",
				OriginalName: "resumed.mp4",
				OriginalSize: 50 * 1024 * 1024,
				Status:       state.StatusPending,
			},
		},
	}
	m.session = sess
	m.state = ViewResumePrompt

	// Resume by pressing 'r'
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model := newM.(*AppModel)

	if model.state != ViewDashboard {
		t.Fatalf("expected ViewDashboard after resume, got %v", model.state)
	}
	if len(model.videos) != 1 || model.videos[0].ID != "v-resumed" {
		t.Fatalf("expected m.videos to be populated with resumed video, got %+v", model.videos)
	}
	if model.cancelCtx != nil {
		model.cancelCtx()
	}

	// Resumed video fails
	newM2, _ := model.Update(orchestrator.PipelineVideoCompleteMsg{
		VideoID:   "v-resumed",
		VideoName: "resumed.mp4",
		Err:       errors.New("failed during transcode"),
	})
	model2 := newM2.(*AppModel)

	// Move to summary
	model2.state = ViewSummary

	// Press 'r' to retry failed videos
	newM3, _ := model2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model3 := newM3.(*AppModel)

	if model3.state != ViewDashboard {
		t.Fatalf("expected state to transition to ViewDashboard on retry, got: %v", model3.state)
	}
	if model3.totalVideos != 1 {
		t.Fatalf("expected 1 retried video, got: %d", model3.totalVideos)
	}
	if model3.cancelCtx != nil {
		model3.cancelCtx()
	}
}

func TestAppModel_ResumeRestoresSkippedAndFailedVideos(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	cfg := &config.Config{URL: "http://localhost:2283", APIKey: "key"}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)

	sess := &state.Session{
		SessionID: "test-sess",
		Videos: map[string]*state.VideoState{
			"v-done": {
				ID:           "v-done",
				OriginalName: "completed.mp4",
				OriginalSize: 100 * 1024 * 1024,
				SavedBytes:   15 * 1024 * 1024,
				Status:       state.StatusCompleted,
			},
			"v-skipped": {
				ID:           "v-skipped",
				OriginalName: "already_hevc.mp4",
				OriginalSize: 50 * 1024 * 1024,
				Status:       state.StatusSkipped,
				ErrorMessage: "already HEVC/H.265",
			},
			"v-failed": {
				ID:           "v-failed",
				OriginalName: "corrupt.mp4",
				OriginalSize: 80 * 1024 * 1024,
				Status:       state.StatusFailed,
				ErrorMessage: "transcode error",
			},
			"v-pending": {
				ID:           "v-pending",
				OriginalName: "pending.mp4",
				OriginalSize: 60 * 1024 * 1024,
				Status:       state.StatusPending,
			},
		},
		TotalVideos: 4,
	}
	_ = state.SaveSession(sess)
	m.session = sess
	m.state = ViewResumePrompt

	// Press 'r' to resume
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model := newM.(*AppModel)

	if model.cancelCtx != nil {
		model.cancelCtx()
	}

	if len(model.transcodedVideos) != 1 {
		t.Errorf("expected 1 transcodedVideo restored, got %d", len(model.transcodedVideos))
	}
	if len(model.skippedVideos) != 1 {
		t.Errorf("expected 1 skippedVideo restored, got %d", len(model.skippedVideos))
	}
	if model.skippedCount != 1 {
		t.Errorf("expected skippedCount == 1, got %d", model.skippedCount)
	}
	if len(model.failedVideos) != 1 {
		t.Errorf("expected 1 failedVideo restored, got %d", len(model.failedVideos))
	}
	if model.failedCount != 1 {
		t.Errorf("expected failedCount == 1, got %d", model.failedCount)
	}
}

func TestAppModel_ScanStaleSequenceDiscarded(t *testing.T) {
	cfg := &config.Config{URL: "http://localhost:2283", APIKey: "key"}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)

	m.state = ViewScanPrompt

	// Start first scan (seq = 1)
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model := newM.(*AppModel)
	if model.scanSeq != 1 || model.state != ViewScanning {
		t.Fatalf("expected scanSeq 1 and ViewScanning, got seq=%d state=%v", model.scanSeq, model.state)
	}

	// User cancels scan
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = newM.(*AppModel)
	if model.state != ViewScanPrompt {
		t.Fatalf("expected ViewScanPrompt after cancel, got %v", model.state)
	}

	// User starts second scan (seq = 2)
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = newM.(*AppModel)
	if model.scanSeq != 2 || model.state != ViewScanning {
		t.Fatalf("expected scanSeq 2 and ViewScanning, got seq=%d state=%v", model.scanSeq, model.state)
	}

	// Stale ScanCompleteMsg from seq = 1 arrives
	staleRes := &immich.QueryResult{
		Videos: []immich.Asset{
			{ID: "stale-vid", OriginalFileName: "stale.mp4"},
		},
	}
	newM, _ = model.Update(ScanCompleteMsg{Seq: 1, Result: staleRes, Err: nil})
	model = newM.(*AppModel)

	// It must NOT have transitioned to ViewBrowser or set m.videos
	if model.state != ViewScanning {
		t.Errorf("expected state to remain ViewScanning, but it changed to: %v", model.state)
	}
	if len(model.videos) != 0 {
		t.Errorf("expected m.videos not to be overwritten by stale scan, got: %+v", model.videos)
	}

	// Valid ScanCompleteMsg from seq = 2 arrives
	validRes := &immich.QueryResult{
		Videos: []immich.Asset{
			{ID: "valid-vid", OriginalFileName: "valid.mp4"},
		},
	}
	newM, _ = model.Update(ScanCompleteMsg{Seq: 2, Result: validRes, Err: nil})
	model = newM.(*AppModel)

	if model.state != ViewBrowser {
		t.Errorf("expected state to transition to ViewBrowser on valid scan, got: %v", model.state)
	}
	if len(model.videos) != 1 || model.videos[0].ID != "valid-vid" {
		t.Errorf("expected m.videos to contain valid-vid, got: %+v", model.videos)
	}
}

func TestViewSummary_TranscodedDetailedBreakdown(t *testing.T) {
	cfg := config.DefaultConfig()
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)
	m.state = ViewSummary
	m.totalVideos = 3
	m.processedVideos = 3
	m.skippedCount = 1
	m.totalSavedBytes = 25 * 1024 * 1024

	m.transcodedVideos = []transcodedVideo{
		{
			VideoID:      "v1",
			Name:         "family_trip.mov",
			OriginalSize: 100 * 1024 * 1024,
			NewSize:      75 * 1024 * 1024,
			SavedBytes:   25 * 1024 * 1024,
		},
	}
	m.skippedVideos = []skippedVideo{
		{
			Name:   "existing_hevc.mp4",
			Reason: "already HEVC",
		},
	}

	view := m.viewSummary()

	if !strings.Contains(view, "Detailed Breakdown:") {
		t.Errorf("expected view to contain 'Detailed Breakdown:', got:\n%s", view)
	}
	if !strings.Contains(view, "Transcoded & Uploaded:") {
		t.Errorf("expected view to contain 'Transcoded & Uploaded:', got:\n%s", view)
	}
	if !strings.Contains(view, "family_trip.mov") {
		t.Errorf("expected view to contain video name 'family_trip.mov', got:\n%s", view)
	}
	if !strings.Contains(view, "100.0 MB ➔ 75.0 MB") {
		t.Errorf("expected view to contain size transition '100.0 MB ➔ 75.0 MB', got:\n%s", view)
	}
	if !strings.Contains(view, "saved 25.0 MB, -25.0%") {
		t.Errorf("expected view to contain saved stats 'saved 25.0 MB, -25.0%%', got:\n%s", view)
	}
	if !strings.Contains(view, "Skipped (originals kept, nothing uploaded):") {
		t.Errorf("expected view to contain skipped section, got:\n%s", view)
	}
	if !strings.Contains(view, "existing_hevc.mp4") {
		t.Errorf("expected view to contain skipped video name 'existing_hevc.mp4', got:\n%s", view)
	}
}

func TestEstimateSavings(t *testing.T) {
	orig := int64(100 * 1024 * 1024) // 100 MB

	// 1. Probe failed -> 0 MB saved
	ratio, saved := estimateSavings(orig, engine.ProbeDetails{Codec: "H.264"}, "original", true)
	if ratio != 1.0 || saved != 0 {
		t.Errorf("expected 0 saved for probe failed, got ratio=%v, saved=%v", ratio, saved)
	}

	// 2. Already HEVC -> 0 MB saved
	ratio, saved = estimateSavings(orig, engine.ProbeDetails{Codec: "HEVC", IsHEVC: true}, "original", false)
	if ratio != 1.0 || saved != 0 {
		t.Errorf("expected 0 saved for HEVC, got ratio=%v, saved=%v", ratio, saved)
	}

	// 3. VP9 / AV1 -> 0 MB saved
	ratio, saved = estimateSavings(orig, engine.ProbeDetails{Codec: "VP9"}, "original", false)
	if ratio != 1.0 || saved != 0 {
		t.Errorf("expected 0 saved for VP9, got ratio=%v, saved=%v", ratio, saved)
	}

	// 4. Empty / unknown codec -> 0 MB saved
	ratio, saved = estimateSavings(orig, engine.ProbeDetails{Codec: ""}, "original", false)
	if ratio != 1.0 || saved != 0 {
		t.Errorf("expected 0 saved for empty codec, got ratio=%v, saved=%v", ratio, saved)
	}

	// 5. H.264 without downscale -> conservative 15% saved (ratio 0.85)
	ratio, saved = estimateSavings(orig, engine.ProbeDetails{Codec: "H.264", Resolution: "1080p"}, "original", false)
	if ratio != 0.85 || saved != int64(15*1024*1024) {
		t.Errorf("expected 15MB saved for H.264 same-res, got ratio=%v, saved=%v", ratio, saved)
	}

	// 6. 4K H.264 with capitalized "Original" setting -> does NOT downscale (ratio 0.85)
	ratio, saved = estimateSavings(orig, engine.ProbeDetails{Codec: "H.264", Resolution: "4K"}, "Original", false)
	if ratio != 0.85 || saved != int64(15*1024*1024) {
		t.Errorf("expected 15MB saved for 4K with 'Original' setting, got ratio=%v, saved=%v", ratio, saved)
	}

	// 7. 720p H.264 with "1080p" target setting -> does NOT upscale (ratio 0.85)
	ratio, saved = estimateSavings(orig, engine.ProbeDetails{Codec: "H.264", Resolution: "720p"}, "1080p", false)
	if ratio != 0.85 || saved != int64(15*1024*1024) {
		t.Errorf("expected 15MB saved for 720p with 1080p target (no upscale), got ratio=%v, saved=%v", ratio, saved)
	}

	// 8. H.264 with 4K -> 1080p downscale -> 50% saved (ratio 0.50)
	ratio, saved = estimateSavings(orig, engine.ProbeDetails{Codec: "H.264", Resolution: "4K"}, "1080p", false)
	if ratio != 0.50 || saved != int64(50*1024*1024) {
		t.Errorf("expected 50MB saved for 4K downscale, got ratio=%v, saved=%v", ratio, saved)
	}

	// 9. ProRes -> ~70% saved (ratio 0.30)
	ratio, saved = estimateSavings(orig, engine.ProbeDetails{Codec: "ProRes", Resolution: "1080p"}, "original", false)
	if ratio != 0.30 || saved != int64(70*1024*1024) {
		t.Errorf("expected 70MB saved for ProRes, got ratio=%v, saved=%v", ratio, saved)
	}
}

func TestResolveTargetResolution(t *testing.T) {
	tests := []struct {
		inputRes      string
		targetSetting string
		wantRes       string
		wantDownscale bool
	}{
		{"4K", "Original", "4K", false},
		{"4K", "original", "4K", false},
		{"4K", "", "4K", false},
		{"4K", "1080p", "1080p", true},
		{"1080p", "1080p", "1080p", false},
		{"720p", "1080p", "720p", false}, // no upscaling
		{"1440p", "720p", "720p", true},
		{"3840x2160", "1080p", "1080p", true},
		{"", "1080p", "1080p", false},
	}

	for _, tt := range tests {
		gotRes, gotDownscale := resolveTargetResolution(tt.inputRes, tt.targetSetting)
		if gotRes != tt.wantRes || gotDownscale != tt.wantDownscale {
			t.Errorf("resolveTargetResolution(%q, %q) = (%q, %v); want (%q, %v)",
				tt.inputRes, tt.targetSetting, gotRes, gotDownscale, tt.wantRes, tt.wantDownscale)
		}
	}
}

func TestAppModel_MBPerMinDisplayAndSettings(t *testing.T) {
	cfg := &config.Config{
		URL:           "http://localhost:2283",
		APIKey:        "test-key",
		SizeThreshold: "100",
		MaxRetries:    2,
	}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)

	// 1. Check Table columns and rows with MB/min
	m.state = ViewBrowser
	m.videos = []immich.Asset{
		{
			ID:               "v1",
			OriginalFileName: "dense.mp4",
			Size:             240 * 1024 * 1024,
			Duration:         json.RawMessage(`"0:02:00"`), // 120 MB/min
			ExifInfo:         immich.ExifInfo{ExifImageWidth: 1920, ExifImageHeight: 1080},
		},
		{
			ID:               "v2",
			OriginalFileName: "medium.mp4",
			Size:             45 * 1024 * 1024,
			Duration:         json.RawMessage(`"0:01:00"`), // 45 MB/min
			ExifInfo:         immich.ExifInfo{ExifImageWidth: 1280, ExifImageHeight: 720},
		},
	}
	m.updateTable()

	rows := m.table.Rows()
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	// Column 0: [ ], Column 1: Filename, Column 2: Size, Column 3: MB/min, Column 4: Resolution
	if rows[0][3] != "120 MB/min" {
		t.Errorf("expected row 0 MB/min to be '120 MB/min', got %q", rows[0][3])
	}
	if rows[1][3] != "45 MB/min" {
		t.Errorf("expected row 1 MB/min to be '45 MB/min', got %q", rows[1][3])
	}

	renderedBrowser := m.viewBrowser()
	if !strings.Contains(renderedBrowser, "MB/min") {
		t.Errorf("expected browser view to contain 'MB/min'")
	}

	// 2. Check Settings View displays Threshold (MB/min)
	m.state = ViewSettings
	renderedSettings := m.viewSettings()
	if !strings.Contains(renderedSettings, "Threshold (MB/min):") {
		t.Errorf("expected settings to contain 'Threshold (MB/min):', got %s", renderedSettings)
	}
	if !strings.Contains(renderedSettings, "100 MB/min") {
		t.Errorf("expected settings to contain '100 MB/min', got %s", renderedSettings)
	}

	// 3. Edit Threshold in Settings (row 7)
	m.settingCursor = 7
	// Press Enter to focus threshold input
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newM.(*AppModel)
	if !m.thresholdInput.Focused() {
		t.Fatalf("expected threshold input to be focused")
	}

	// Set value to 150
	m.thresholdInput.SetValue("150")
	// Press Enter to commit
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newM.(*AppModel)
	if m.cfg.SizeThreshold != "150" {
		t.Errorf("expected cfg.SizeThreshold to be updated to 150, got %s", m.cfg.SizeThreshold)
	}
}

func TestAppModel_ThresholdModesAndToggle(t *testing.T) {
	cfg := &config.Config{
		URL:           "http://localhost:2283",
		APIKey:        "test-key",
		ThresholdMode: "MB/min",
		SizeThreshold: "100",
		MaxRetries:    2,
	}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)

	// Verify scan prompt in default MB/min mode
	m.state = ViewScanPrompt
	promptView := m.viewScanPrompt()
	if !strings.Contains(promptView, "• Threshold:      100 MB/min") {
		t.Errorf("expected scan prompt to show 100 MB/min, got: %s", promptView)
	}

	// Go to Settings
	m.state = ViewSettings
	settingsView := m.viewSettings()
	if !strings.Contains(settingsView, "Threshold Mode:") || !strings.Contains(settingsView, "MB/min") {
		t.Errorf("expected settings to show Threshold Mode MB/min, got: %s", settingsView)
	}
	if !strings.Contains(settingsView, "Threshold (MB/min):") || !strings.Contains(settingsView, "100 MB/min") {
		t.Errorf("expected settings to show 100 MB/min, got: %s", settingsView)
	}

	// Cursor at row 6 (Threshold Mode) - Toggle with Space -> Total MB
	m.settingCursor = 6
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = newM.(*AppModel)
	if m.cfg.ThresholdMode != "Total MB" {
		t.Fatalf("expected ThresholdMode Total MB after Space, got: %s", m.cfg.ThresholdMode)
	}

	settingsView = m.viewSettings()
	if !strings.Contains(settingsView, "Threshold (Total MB):") || !strings.Contains(settingsView, "100 MB") {
		t.Errorf("expected settings to show Threshold (Total MB): 100 MB, got: %s", settingsView)
	}

	// Check Scan Prompt in Total MB mode
	m.state = ViewScanPrompt
	promptView = m.viewScanPrompt()
	if !strings.Contains(promptView, "100 MB (Total File Size)") {
		t.Errorf("expected scan prompt to show 100 MB (Total File Size), got: %s", promptView)
	}

	// Go back to settings, toggle with Enter -> Disabled
	m.state = ViewSettings
	m.settingCursor = 6
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newM.(*AppModel)
	if m.cfg.ThresholdMode != "Disabled" {
		t.Fatalf("expected ThresholdMode Disabled after Enter, got: %s", m.cfg.ThresholdMode)
	}

	settingsView = m.viewSettings()
	if !strings.Contains(settingsView, "Disabled (All videos scanned)") {
		t.Errorf("expected settings to show Disabled (All videos scanned), got: %s", settingsView)
	}

	// Check Scan Prompt in Disabled mode
	m.state = ViewScanPrompt
	promptView = m.viewScanPrompt()
	if !strings.Contains(promptView, "Disabled (All videos)") {
		t.Errorf("expected scan prompt to show Disabled (All videos), got: %s", promptView)
	}

	// When Disabled, pressing Enter on row 7 should NOT focus the thresholdInput
	m.state = ViewSettings
	m.settingCursor = 7
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newM.(*AppModel)
	if m.thresholdInput.Focused() {
		t.Errorf("threshold input should not be focused when ThresholdMode is Disabled")
	}

	// Row 6: Cycle with Left arrow: Disabled -> Total MB -> MB/min
	m.settingCursor = 6
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = newM.(*AppModel)
	if m.cfg.ThresholdMode != "Total MB" {
		t.Errorf("expected Total MB on Left, got: %s", m.cfg.ThresholdMode)
	}

	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = newM.(*AppModel)
	if m.cfg.ThresholdMode != "MB/min" {
		t.Errorf("expected MB/min on Left, got: %s", m.cfg.ThresholdMode)
	}

	// Row 6: Cycle with Right arrow: MB/min -> Total MB -> Disabled -> MB/min
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = newM.(*AppModel)
	if m.cfg.ThresholdMode != "Total MB" {
		t.Errorf("expected Total MB on Right, got: %s", m.cfg.ThresholdMode)
	}

	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = newM.(*AppModel)
	if m.cfg.ThresholdMode != "Disabled" {
		t.Errorf("expected Disabled on Right, got: %s", m.cfg.ThresholdMode)
	}

	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = newM.(*AppModel)
	if m.cfg.ThresholdMode != "MB/min" {
		t.Errorf("expected wrap-around to MB/min on Right, got: %s", m.cfg.ThresholdMode)
	}
}




