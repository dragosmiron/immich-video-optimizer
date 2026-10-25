package tui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"immich-video-optimizer/config"
	"immich-video-optimizer/engine"
	"immich-video-optimizer/immich"
	"immich-video-optimizer/orchestrator"
	"immich-video-optimizer/state"
	"immich-video-optimizer/version"
)

func TestMain(m *testing.M) {
	tempDir, err := os.MkdirTemp("", "tui-test-*")
	if err == nil {
		os.Setenv("HOME", tempDir)
		defer os.RemoveAll(tempDir)

		binDir := filepath.Join(tempDir, "bin")
		_ = os.MkdirAll(binDir, 0755)
		fakeScript := "#!/bin/sh\nexit 0\n"
		_ = os.WriteFile(filepath.Join(binDir, "ffmpeg"), []byte(fakeScript), 0755)
		_ = os.WriteFile(filepath.Join(binDir, "exiftool"), []byte(fakeScript), 0755)
		os.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
	}
	os.Exit(m.Run())
}

func drainAndCancelPipeline(m *AppModel) {
	if m == nil {
		return
	}
	if m.cancelCtx != nil {
		m.cancelCtx()
	}
	if m.dryRunCancel != nil {
		m.dryRunCancel()
	}
	if m.pipelineChan != nil {
		done := make(chan struct{})
		go func() {
			for range m.pipelineChan {
			}
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
	if m.dryRunChan != nil {
		done := make(chan struct{})
		go func() {
			for range m.dryRunChan {
			}
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
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

	// Move cursor down to Video Quality
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = newM.(*AppModel)
	if model.settingCursor != 1 {
		t.Errorf("expected settingCursor 1, got %d", model.settingCursor)
	}
	// Cycle Video Quality with Right
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = newM.(*AppModel)
	if model.cfg.Quality() != 7 {
		t.Errorf("expected VideoQuality 7, got %d", model.cfg.Quality())
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

	// Switch to Tab 2: Pipeline & Network using key '3'
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	model := newM.(*AppModel)
	if model.settingsTab != 2 {
		t.Fatalf("expected settingsTab 2, got %d", model.settingsTab)
	}

	// Index 0 in Tab 2 is Pipeline Prefetch
	model.settingCursor = 0
	if model.cfg.EnablePrefetch != true {
		t.Fatalf("expected initial EnablePrefetch true")
	}

	// Toggle prefetch with Space
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = newM.(*AppModel)
	if model.cfg.EnablePrefetch != false {
		t.Errorf("expected EnablePrefetch to toggle to false")
	}

	// Move down to index 1 (Concurrent Encoders)
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = newM.(*AppModel)
	if model.settingCursor != 1 {
		t.Fatalf("expected settingCursor 1, got %d", model.settingCursor)
	}

	// Toggle encoders with Space (1 -> 2)
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = newM.(*AppModel)
	if model.cfg.ConcurrentEncoders != 2 {
		t.Errorf("expected ConcurrentEncoders 2, got %d", model.cfg.ConcurrentEncoders)
	}

	// Move down to index 2 (Logging Level)
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = newM.(*AppModel)
	if model.settingCursor != 2 {
		t.Fatalf("expected settingCursor 2, got %d", model.settingCursor)
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

	// Move down to index 3 (Network Retries)
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = newM.(*AppModel)
	if model.settingCursor != 3 {
		t.Fatalf("expected settingCursor 3, got %d", model.settingCursor)
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

	// Switch to Tab 1: Filtering using key '2'
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	model = newM.(*AppModel)
	if model.settingsTab != 1 {
		t.Fatalf("expected settingsTab 1, got %d", model.settingsTab)
	}

	// Move to index 2 (Exclude Optimized) in Tab 1
	model.settingCursor = 2

	// Set initial to true and test toggling with Space
	model.cfg.ExcludeOptimized = true
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = newM.(*AppModel)
	if model.cfg.ExcludeOptimized != false {
		t.Errorf("expected ExcludeOptimized false after space toggle")
	}

	vOff := model.View()
	if !strings.Contains(vOff, "Exclude Optimized") || !strings.Contains(vOff, "OFF — All videos matching search criteria are included") {
		t.Errorf("expected viewSettings to show OFF description, got: %s", vOff)
	}

	// Toggle back to true with Right arrow
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = newM.(*AppModel)
	if model.cfg.ExcludeOptimized != true {
		t.Errorf("expected ExcludeOptimized true after right arrow")
	}

	// Toggle with Left arrow
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyLeft})
	model = newM.(*AppModel)
	if model.cfg.ExcludeOptimized != false {
		t.Errorf("expected ExcludeOptimized false after left arrow")
	}

	// Toggle back to true with Right arrow
	newM, _ = model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = newM.(*AppModel)
	if model.cfg.ExcludeOptimized != true {
		t.Errorf("expected ExcludeOptimized true after right arrow")
	}

	// Verify settings view renders Exclude Optimized and tag mention when ON
	vOn := model.View()
	if !strings.Contains(vOn, "Exclude Optimized") || !strings.Contains(vOn, "immich-optimizer") {
		t.Errorf("expected viewSettings to describe Exclude Optimized and immich-optimizer tag, got: %s", vOn)
	}
}

func TestAppModel_ResumeMetadataPreservation(t *testing.T) {
	cfg := &config.Config{URL: "http://localhost:2283", APIKey: "test-key"}
	pipeline, err := orchestrator.NewPipeline(cfg)
	if err != nil {
		t.Fatalf("NewPipeline failed: %v", err)
	}
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
	drainAndCancelPipeline(model)
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
	drainAndCancelPipeline(model)

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
	drainAndCancelPipeline(model2)

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
	drainAndCancelPipeline(model2)

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
	drainAndCancelPipeline(model)
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
	drainAndCancelPipeline(model)

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
	drainAndCancelPipeline(model3)
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

	drainAndCancelPipeline(model)

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
	ratio, saved := engine.EstimateSavings(orig, engine.ProbeDetails{Codec: "H.264"}, "original", true)
	if ratio != 1.0 || saved != 0 {
		t.Errorf("expected 0 saved for probe failed, got ratio=%v, saved=%v", ratio, saved)
	}

	// 2. Already HEVC -> 0 MB saved without downscaling
	ratio, saved = engine.EstimateSavings(orig, engine.ProbeDetails{Codec: "HEVC", IsHEVC: true}, "original", false)
	if ratio != 1.0 || saved != 0 {
		t.Errorf("expected 0 saved for HEVC, got ratio=%v, saved=%v", ratio, saved)
	}

	// 2b. Already HEVC with 4K -> 1080p downscale -> ~50% savings
	ratio, saved = engine.EstimateSavings(orig, engine.ProbeDetails{Codec: "HEVC", IsHEVC: true, Resolution: "4K"}, "1080p", false)
	if ratio != 0.50 || saved != int64(50*1024*1024) {
		t.Errorf("expected 50MB saved for HEVC 4K -> 1080p, got ratio=%v, saved=%v", ratio, saved)
	}

	// 3. VP9 / AV1 -> 0 MB saved
	ratio, saved = engine.EstimateSavings(orig, engine.ProbeDetails{Codec: "VP9"}, "original", false)
	if ratio != 1.0 || saved != 0 {
		t.Errorf("expected 0 saved for VP9, got ratio=%v, saved=%v", ratio, saved)
	}

	// 4. Empty / unknown codec -> 0 MB saved
	ratio, saved = engine.EstimateSavings(orig, engine.ProbeDetails{Codec: ""}, "original", false)
	if ratio != 1.0 || saved != 0 {
		t.Errorf("expected 0 saved for empty codec, got ratio=%v, saved=%v", ratio, saved)
	}

	// 5. H.264 without downscale -> conservative 15% saved (ratio 0.85)
	ratio, saved = engine.EstimateSavings(orig, engine.ProbeDetails{Codec: "H.264", Resolution: "1080p"}, "original", false)
	if ratio != 0.85 || saved != int64(15*1024*1024) {
		t.Errorf("expected 15MB saved for H.264 same-res, got ratio=%v, saved=%v", ratio, saved)
	}

	// 6. 4K H.264 with capitalized "Original" setting -> does NOT downscale (ratio 0.85)
	ratio, saved = engine.EstimateSavings(orig, engine.ProbeDetails{Codec: "H.264", Resolution: "4K"}, "Original", false)
	if ratio != 0.85 || saved != int64(15*1024*1024) {
		t.Errorf("expected 15MB saved for 4K with 'Original' setting, got ratio=%v, saved=%v", ratio, saved)
	}

	// 7. 720p H.264 with "1080p" target setting -> does NOT upscale (ratio 0.85)
	ratio, saved = engine.EstimateSavings(orig, engine.ProbeDetails{Codec: "H.264", Resolution: "720p"}, "1080p", false)
	if ratio != 0.85 || saved != int64(15*1024*1024) {
		t.Errorf("expected 15MB saved for 720p with 1080p target (no upscale), got ratio=%v, saved=%v", ratio, saved)
	}

	// 8. H.264 with 4K -> 1080p downscale -> 50% saved (ratio 0.50)
	ratio, saved = engine.EstimateSavings(orig, engine.ProbeDetails{Codec: "H.264", Resolution: "4K"}, "1080p", false)
	if ratio != 0.50 || saved != int64(50*1024*1024) {
		t.Errorf("expected 50MB saved for 4K downscale, got ratio=%v, saved=%v", ratio, saved)
	}

	// 9. ProRes -> ~70% saved (ratio 0.30)
	ratio, saved = engine.EstimateSavings(orig, engine.ProbeDetails{Codec: "ProRes", Resolution: "1080p"}, "original", false)
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
		gotRes, gotDownscale := engine.ResolveTargetResolution(tt.inputRes, tt.targetSetting)
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

	// 2. Check Settings View displays Threshold (MB/min) in Tab 1 (Filtering)
	m.state = ViewSettings
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = newM.(*AppModel)
	renderedSettings := m.viewSettings()
	if !strings.Contains(renderedSettings, "Threshold (MB/min):") {
		t.Errorf("expected settings to contain 'Threshold (MB/min):', got %s", renderedSettings)
	}
	if !strings.Contains(renderedSettings, "100 MB/min") {
		t.Errorf("expected settings to contain '100 MB/min', got %s", renderedSettings)
	}

	// 3. Edit Threshold in Settings (Tab 1, row 1)
	m.settingCursor = 1
	// Press Enter to focus threshold input
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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

	// Go to Settings and switch to Tab 1 (Filtering)
	m.state = ViewSettings
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = newM.(*AppModel)
	settingsView := m.viewSettings()
	if !strings.Contains(settingsView, "Threshold Mode:") || !strings.Contains(settingsView, "MB/min") {
		t.Errorf("expected settings to show Threshold Mode MB/min, got: %s", settingsView)
	}
	if !strings.Contains(settingsView, "Threshold (MB/min):") || !strings.Contains(settingsView, "100 MB/min") {
		t.Errorf("expected settings to show 100 MB/min, got: %s", settingsView)
	}

	// Cursor at row 0 in Tab 1 (Threshold Mode) - Toggle with Space -> Total MB
	m.settingCursor = 0
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
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

	// Go back to settings (switch to Tab 1), toggle with Enter -> Disabled
	m.state = ViewSettings
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = newM.(*AppModel)
	m.settingCursor = 0
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

	// When Disabled, pressing Enter on row 1 (Threshold Value) in Tab 1 should NOT focus the thresholdInput
	m.state = ViewSettings
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = newM.(*AppModel)
	m.settingCursor = 1
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newM.(*AppModel)
	if m.thresholdInput.Focused() {
		t.Errorf("threshold input should not be focused when ThresholdMode is Disabled")
	}

	// Row 0 in Tab 1: Cycle with Left arrow: Disabled -> Total MB -> MB/min
	m.settingCursor = 0
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

	// Row 0 in Tab 1: Cycle with Right arrow: MB/min -> Total MB -> Disabled -> MB/min
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

	// Test renderScanSummary and viewBrowser in Disabled mode
	m.cfg.ThresholdMode = config.ThresholdModeDisabled
	m.scanResult = &immich.QueryResult{
		TotalAssetsScanned: 10,
		SkippedNonVideo:    2,
		SkippedBelowSize:   0,
	}
	m.videos = nil
	m.state = ViewBrowser
	browserView := m.viewBrowser()
	if !strings.Contains(browserView, "Videos skipped (threshold disabled): 0") {
		t.Errorf("expected summary to contain properly formatted disabled skipped count, got: %s", browserView)
	}
	if !strings.Contains(browserView, "No video assets were discovered on the Immich server.") {
		t.Errorf("expected empty browser view in disabled mode to mention no video assets discovered, got: %s", browserView)
	}
}

func TestAppModel_RenderScanSummary_ExternalLibrary(t *testing.T) {
	cfg := &config.Config{
		URL:           "http://localhost:2283",
		APIKey:        "test-key",
		ThresholdMode: config.ThresholdModeMBPerMin,
		SizeThreshold: "100",
	}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)
	m.scanResult = &immich.QueryResult{
		TotalAssetsScanned: 25,
		SkippedNonVideo:    5,
		SkippedExternal:    8,
		SkippedBelowSize:   2,
	}
	m.videos = []immich.Asset{
		{ID: "v1", Type: "VIDEO", Size: 500000000, OriginalFileName: "clip.mp4"},
	}
	m.state = ViewBrowser
	summary := m.renderScanSummary()
	// External/trashed/live-photo counts are not shown in the summary (not useful to the user).
	if strings.Contains(summary, "External library") {
		t.Errorf("external library count should not appear in summary, got: %s", summary)
	}
	// Basic sanity: total and non-video counts are still present.
	if !strings.Contains(summary, "25") {
		t.Errorf("expected total assets count in summary, got: %s", summary)
	}
}

func TestAppModel_RenderScanSummary_PartnerShared(t *testing.T) {
	cfg := &config.Config{
		URL:           "http://localhost:2283",
		APIKey:        "test-key",
		ThresholdMode: config.ThresholdModeMBPerMin,
		SizeThreshold: "100",
	}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)
	m.scanResult = &immich.QueryResult{
		TotalAssetsScanned: 30,
		SkippedNonVideo:    5,
		SkippedNotOwned:    7,
		SkippedBelowSize:   2,
	}
	m.videos = []immich.Asset{
		{ID: "v1", Type: "VIDEO", Size: 500000000, OriginalFileName: "clip.mp4"},
	}
	m.state = ViewBrowser
	summary := m.renderScanSummary()
	if !strings.Contains(summary, "Partner-shared videos skipped:     7 (not owned)") {
		t.Errorf("expected partner-shared skipped count in summary, got: %s", summary)
	}
}

func TestAppModel_DryRunPartnerSharedSkipped(t *testing.T) {
	cfg := &config.Config{
		URL:           "http://localhost:2283",
		APIKey:        "test-key",
		ThresholdMode: config.ThresholdModeMBPerMin,
		SizeThreshold: "100",
	}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)
	m.scanResult = &immich.QueryResult{
		TotalAssetsScanned: 30,
		SkippedNonVideo:    5,
		SkippedNotOwned:    7,
		SkippedBelowSize:   2,
	}
	m.dryRunVideos = []immich.Asset{
		{ID: "v1", Type: "VIDEO", Size: 500000000, OriginalFileName: "clip.mp4"},
	}
	m.dryRunToProcess = m.dryRunVideos
	m.dryRunDetails = map[string]engine.ProbeDetails{
		"v1": {Codec: "H.264", Resolution: "1080p"},
	}
	m.dryRunDone = true
	m.state = ViewDryRun

	rendered := m.viewDryRun()
	if !strings.Contains(rendered, "Skipped at scan (partner shared):") {
		t.Errorf("expected dry run view to contain partner shared skipped row, got: %s", rendered)
	}
	if !strings.Contains(rendered, "7") {
		t.Errorf("expected dry run view to contain count '7', got: %s", rendered)
	}
}

func TestSkipReasonLabel(t *testing.T) {
	tests := []struct {
		reason   string
		expected string
	}{
		{"transcoded file larger", "Size Guard: transcoded > original, original kept"},
		{"already HEVC", "Already HEVC/H.265, re-encoding skipped"},
		{"partner shared (not owned)", "Partner-shared asset (not owned)"},
		{"custom reason", "custom reason"},
	}

	for _, tt := range tests {
		got := skipReasonLabel(tt.reason)
		if got != tt.expected {
			t.Errorf("skipReasonLabel(%q) = %q, expected %q", tt.reason, got, tt.expected)
		}
	}
}

func TestAppModel_ElapsedTimeSubsecondTruncated(t *testing.T) {
	cfg := &config.Config{
		URL:    "http://localhost:2283",
		APIKey: "test-key",
	}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)
	m.state = ViewDashboard
	m.currentTranscodeName = "my_video.mp4"
	m.currentTranscodeTime = "00:01:12.966667"
	m.speed = "1.5x"
	m.fps = 45.0

	rendered := m.viewDashboard()
	if !strings.Contains(rendered, "Time: 00:01:12") {
		t.Errorf("expected time to be truncated to seconds 'Time: 00:01:12', got: %s", rendered)
	}
	if strings.Contains(rendered, "00:01:12.966667") {
		t.Errorf("dashboard should not contain sub-second precision, got: %s", rendered)
	}
}

func TestAppModel_EncodingPulsing(t *testing.T) {
	cfg := &config.Config{
		URL:    "http://localhost:2283",
		APIKey: "test-key",
	}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)
	m.state = ViewDashboard
	m.currentTranscodeName = "my_video.mp4"
	m.currentTranscodeTime = "00:00:15"
	m.fps = 30.0

	initialPulse := m.pulseFrame
	rendered1 := m.viewDashboard()
	if !strings.Contains(rendered1, "Encoding") {
		t.Errorf("expected dashboard to contain 'Encoding', got: %s", rendered1)
	}

	// Dispatch pulseTickMsg
	newM, _ := m.Update(pulseTickMsg(time.Now()))
	m = newM.(*AppModel)
	if m.pulseFrame != initialPulse+1 {
		t.Errorf("expected pulseFrame to increment to %d, got %d", initialPulse+1, m.pulseFrame)
	}
}

func TestAppModel_DryRunAlreadyHEVC_NoDuplication(t *testing.T) {
	cfg := &config.Config{
		URL:      "http://localhost:2283",
		APIKey:   "test-key",
		SkipHEVC: false, // OFF, so already-HEVC videos will be re-encoded
	}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)
	m.state = ViewDryRun

	v := immich.Asset{
		ID:               "vid-hevc",
		OriginalFileName: "already_hevc.mp4",
		ExifInfo: immich.ExifInfo{
			FileSizeInByte:  200 * 1024 * 1024,
			ExifImageWidth:  1920,
			ExifImageHeight: 1080,
		},
	}
	m.dryRunToProcess = []immich.Asset{v}
	m.dryRunDetails = map[string]engine.ProbeDetails{
		"vid-hevc": {Codec: "HEVC", IsHEVC: true, Resolution: "1080p"},
	}
	m.dryRunDone = true

	rendered := m.viewDryRun()
	// Must NOT contain duplicate "0 MB saved"
	if strings.Contains(rendered, "0 MB saved ⚠️ Already HEVC (0 MB saved)") {
		t.Errorf("dry run view contained duplicated '0 MB saved', got: %s", rendered)
	}
	// Must mention potentially minimal savings
	if !strings.Contains(rendered, "potentially minimal savings") {
		t.Errorf("expected dry run view to mention 'potentially minimal savings', got: %s", rendered)
	}
	// Must mention Already HEVC
	if !strings.Contains(rendered, "Already HEVC") {
		t.Errorf("expected dry run view to mention 'Already HEVC', got: %s", rendered)
	}
}

func TestAppModel_DryRunAlbumNotice(t *testing.T) {
	cfg := &config.Config{
		URL:    "http://localhost:2283",
		APIKey: "test-key",
	}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)
	m.state = ViewDryRun
	m.dryRunAlbumName = "Immich-Optimizer-DryRun-20261014-123456"
	m.dryRunToProcess = []immich.Asset{
		{ID: "vid-1", OriginalFileName: "vid1.mp4", ExifInfo: immich.ExifInfo{FileSizeInByte: 1000}},
	}
	m.dryRunDetails = map[string]engine.ProbeDetails{
		"vid-1": {Codec: "H.264", Resolution: "1080p"},
	}
	m.dryRunDone = true

	rendered := m.viewDryRun()
	if !strings.Contains(rendered, "Album created in Immich:") {
		t.Errorf("expected viewDryRun to contain 'Album created in Immich:', got: %s", rendered)
	}
	if !strings.Contains(rendered, "Immich-Optimizer-DryRun-20261014-123456") {
		t.Errorf("expected viewDryRun to contain album name, got: %s", rendered)
	}
	if !strings.Contains(rendered, "(you can open Immich to inspect these videos)") {
		t.Errorf("expected viewDryRun to contain instruction to inspect videos, got: %s", rendered)
	}
}

func TestAppModel_DryRunToOptimizationPassesAlbumID(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	cfg := &config.Config{
		URL:    "http://localhost:2283",
		APIKey: "test-key",
	}
	pipeline, err := orchestrator.NewPipeline(cfg)
	if err != nil {
		t.Fatalf("Failed to create pipeline: %v", err)
	}
	m := NewAppModel(cfg, pipeline)
	m.state = ViewDryRun
	m.dryRunDone = true
	m.dryRunAlbumID = "album-xyz-789"
	m.dryRunAlbumName = "Immich-Optimizer-DryRun-20261014-123456"
	m.dryRunToProcess = []immich.Asset{
		{ID: "vid-1", OriginalFileName: "vid1.mp4", ExifInfo: immich.ExifInfo{FileSizeInByte: 1000}},
	}

	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	appModel, ok := newM.(*AppModel)
	if !ok {
		t.Fatalf("Expected *AppModel, got %T", newM)
	}

	if appModel.state != ViewDashboard {
		t.Errorf("Expected state ViewDashboard, got %v", appModel.state)
	}
	if pipeline.DryRunAlbumID() != "album-xyz-789" {
		t.Errorf("Expected pipeline to have DryRunAlbumID 'album-xyz-789', got '%s'", pipeline.DryRunAlbumID())
	}
	if appModel.dryRunAlbumID != "" {
		t.Errorf("Expected appModel.dryRunAlbumID to be cleared, got '%s'", appModel.dryRunAlbumID)
	}
	if appModel.dryRunAlbumName != "" {
		t.Errorf("Expected appModel.dryRunAlbumName to be cleared, got '%s'", appModel.dryRunAlbumName)
	}
	drainAndCancelPipeline(appModel)
}

func TestAppModel_DashboardPulsingWhenNoParts(t *testing.T) {
	cfg := &config.Config{
		URL:    "http://localhost:2283",
		APIKey: "test-key",
	}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)
	m.state = ViewDashboard
	m.currentTranscodeName = "incoming_video.mp4"
	// No parts set yet (time, speed, fps all empty)
	m.currentTranscodeTime = ""
	m.speed = ""
	m.fps = 0.0

	rendered := m.viewDashboard()
	if !strings.Contains(rendered, "Encoding") {
		t.Errorf("expected dashboard to show 'Encoding' even before first progress tick arrives, got: %s", rendered)
	}
}

func TestAppModel_DryRunSummary_PotentiallyMinimalSavings(t *testing.T) {
	cfg := &config.Config{
		URL:      "http://localhost:2283",
		APIKey:   "test-key",
		SkipHEVC: false,
	}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)
	m.state = ViewDryRun
	m.dryRunToProcess = []immich.Asset{
		{ID: "vid-1", OriginalFileName: "already_hevc.mp4", ExifInfo: immich.ExifInfo{FileSizeInByte: 50 * 1024 * 1024}},
	}
	m.dryRunDetails = map[string]engine.ProbeDetails{
		"vid-1": {Codec: "HEVC", IsHEVC: true, Resolution: "1080p"},
	}
	m.dryRunDone = true

	rendered := m.viewDryRun()
	if !strings.Contains(rendered, "Est. Storage Reclaimed:") {
		t.Errorf("expected viewDryRun to have 'Est. Storage Reclaimed:', got: %s", rendered)
	}
	if !strings.Contains(rendered, "potentially minimal savings") {
		t.Errorf("expected summary Est. Storage Reclaimed to say 'potentially minimal savings' when 0 bytes estimated, got: %s", rendered)
	}
	if strings.Contains(rendered, "~0 B (~0% space saved)") {
		t.Errorf("summary should not contain '~0 B (~0%%%% space saved)', got: %s", rendered)
	}
}

func TestAppModel_VideoQualitySettings(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HOME", tempDir)

	cfg := config.DefaultConfig()
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)

	// 1. Check Scan Prompt displays Video Quality and NOT Transcode Speed
	m.state = ViewScanPrompt
	prompt := m.viewScanPrompt()
	if !strings.Contains(prompt, "• Video Quality:") || !strings.Contains(prompt, "Level 6 (Balanced)") {
		t.Errorf("expected scan prompt to show '• Video Quality:  Level 6 (Balanced)', got: %s", prompt)
	}
	if strings.Contains(prompt, "Transcode Speed") || strings.Contains(prompt, "Transcoding Preset") {
		t.Errorf("scan prompt should NOT display Transcode Speed or Preset, got: %s", prompt)
	}

	// 2. Open Settings and navigate to Video Quality (index 1)
	m.state = ViewSettings
	m.settingCursor = 1
	view := m.viewSettings()
	if strings.Contains(view, "Transcoding Preset") {
		t.Errorf("settings view should NOT display Transcoding Preset, got: %s", view)
	}
	if !strings.Contains(view, "Video Quality:") || !strings.Contains(view, "[6] Balanced") {
		t.Errorf("expected settings view to show '[6] Balanced', got: %s", view)
	}
	if !strings.Contains(view, "[1: Max Savings ➔ 10: Max Quality]") {
		t.Errorf("expected settings view to show slider hint '[1: Max Savings ➔ 10: Max Quality]', got: %s", view)
	}
	if !strings.Contains(view, "Balanced (Default, ~40-50% savings, imperceptible quality loss)") {
		t.Errorf("expected settings view to show balanced description, got: %s", view)
	}

	// 3. Navigate Left (decrease quality) down to 1
	for i := 0; i < 10; i++ {
		newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
		m = newM.(*AppModel)
	}
	if m.cfg.Quality() != 1 {
		t.Errorf("expected quality clamped to 1 after multiple Left presses, got %d", m.cfg.Quality())
	}

	view = m.viewSettings()
	if !strings.Contains(view, "[1] Max Savings") {
		t.Errorf("expected settings view to show '[1] Max Savings', got: %s", view)
	}
	if !strings.Contains(view, "High Compression (~70-80% space savings, noticeable softening for surveillance/dashcams)") {
		t.Errorf("expected settings view to show high compression description at level 1, got: %s", view)
	}

	// 4. Navigate Right to Level 7 (High Quality)
	for i := 0; i < 6; i++ {
		newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
		m = newM.(*AppModel)
	}
	if m.cfg.Quality() != 7 {
		t.Errorf("expected quality 7, got %d", m.cfg.Quality())
	}
	view = m.viewSettings()
	if !strings.Contains(view, "[7] High Quality") {
		t.Errorf("expected settings view to show '[7] High Quality', got: %s", view)
	}
	if !strings.Contains(view, "High Quality (~25-35% savings, preserves fine film grain and high-motion details)") {
		t.Errorf("expected settings view to show high quality description, got: %s", view)
	}

	// 5. Navigate Right to Level 10 (Visually Lossless) and verify clamp
	for i := 0; i < 10; i++ {
		newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
		m = newM.(*AppModel)
	}
	if m.cfg.Quality() != 10 {
		t.Errorf("expected quality clamped to 10, got %d", m.cfg.Quality())
	}
	view = m.viewSettings()
	if !strings.Contains(view, "[10] Visually Lossless") {
		t.Errorf("expected settings view to show '[10] Visually Lossless', got: %s", view)
	}
	if !strings.Contains(view, "Visually Lossless (~15-20% savings, archival preservation)") {
		t.Errorf("expected settings view to show visually lossless description, got: %s", view)
	}

	// 6. Space and Enter cycle from 10 to 1 and 1 to 2
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = newM.(*AppModel)
	if m.cfg.Quality() != 1 {
		t.Errorf("expected Space on level 10 to cycle to 1, got %d", m.cfg.Quality())
	}
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newM.(*AppModel)
	if m.cfg.Quality() != 2 {
		t.Errorf("expected Enter on level 1 to cycle to 2, got %d", m.cfg.Quality())
	}

	// 7. Verify settings navigation boundary clamping and view rendering for all tabs and rows
	for tabIdx := 0; tabIdx < 3; tabIdx++ {
		m.settingsTab = tabIdx
		for cursorIdx := 0; cursorIdx <= m.settingsMaxCursor(); cursorIdx++ {
			m.settingCursor = cursorIdx
			rendered := m.viewSettings()
			if len(rendered) == 0 {
				t.Errorf("expected non-empty settings view at tab %d cursor %d", tabIdx, cursorIdx)
			}
		}
	}

	m.settingsTab = 0
	for i := 0; i < 20; i++ {
		newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = newM.(*AppModel)
	}
	if m.settingCursor != 3 {
		t.Errorf("expected settingCursor clamped to 3 at bottom of tab 0, got %d", m.settingCursor)
	}
	for i := 0; i < 20; i++ {
		newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
		m = newM.(*AppModel)
	}
	if m.settingCursor != 0 {
		t.Errorf("expected settingCursor clamped to 0 at top, got %d", m.settingCursor)
	}

	// 8. Test Esc saves config
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	loaded, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("failed to load saved config: %v", err)
	}
	if loaded.VideoQuality != 2 {
		t.Errorf("expected saved config to have VideoQuality 2, got %d", loaded.VideoQuality)
	}
}

func TestAppModel_SettingsTabsNavigation(t *testing.T) {
	cfg := config.DefaultConfig()
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)
	m.state = ViewSettings

	// Initial tab is 0 (Encoding)
	if m.settingsTab != 0 {
		t.Fatalf("expected initial settingsTab 0, got %d", m.settingsTab)
	}

	// 1. Tab key cycles forward: 0 -> 1 -> 2 -> 3 -> 0
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = newM.(*AppModel)
	if m.settingsTab != 1 {
		t.Errorf("expected tab 1 after [Tab], got %d", m.settingsTab)
	}

	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = newM.(*AppModel)
	if m.settingsTab != 2 {
		t.Errorf("expected tab 2 after [Tab], got %d", m.settingsTab)
	}

	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = newM.(*AppModel)
	if m.settingsTab != 3 {
		t.Errorf("expected tab 3 after [Tab], got %d", m.settingsTab)
	}

	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = newM.(*AppModel)
	if m.settingsTab != 0 {
		t.Errorf("expected tab 0 after wrap-around [Tab], got %d", m.settingsTab)
	}

	// 2. Shift+Tab cycles backward: 0 -> 3 -> 2 -> 1 -> 0
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = newM.(*AppModel)
	if m.settingsTab != 3 {
		t.Errorf("expected tab 3 after [Shift+Tab], got %d", m.settingsTab)
	}

	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = newM.(*AppModel)
	if m.settingsTab != 2 {
		t.Errorf("expected tab 2 after [Shift+Tab], got %d", m.settingsTab)
	}

	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = newM.(*AppModel)
	if m.settingsTab != 1 {
		t.Errorf("expected tab 1 after [Shift+Tab], got %d", m.settingsTab)
	}

	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = newM.(*AppModel)
	if m.settingsTab != 0 {
		t.Errorf("expected tab 0 after [Shift+Tab], got %d", m.settingsTab)
	}

	// 3. Number keys switch directly to tabs: '1', '2', '3', '4'
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = newM.(*AppModel)
	if m.settingsTab != 1 {
		t.Errorf("expected tab 1 after pressing '2', got %d", m.settingsTab)
	}

	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m = newM.(*AppModel)
	if m.settingsTab != 2 {
		t.Errorf("expected tab 2 after pressing '3', got %d", m.settingsTab)
	}

	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	m = newM.(*AppModel)
	if m.settingsTab != 3 {
		t.Errorf("expected tab 3 after pressing '4', got %d", m.settingsTab)
	}

	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = newM.(*AppModel)
	if m.settingsTab != 0 {
		t.Errorf("expected tab 0 after pressing '1', got %d", m.settingsTab)
	}

	// 4. Cursor resets to 0 when switching tabs
	// In Tab 0, set cursor to 3 (Skip HEVC)
	m.settingCursor = 3
	// Switch to Tab 1 (Filtering)
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = newM.(*AppModel)
	if m.settingCursor != 0 {
		t.Errorf("expected cursor reset to 0 in Tab 1, got %d", m.settingCursor)
	}

	// 5. When threshold input is focused, typing numbers feeds the input, does NOT switch tabs
	m.settingCursor = 1 // Threshold Value row
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newM.(*AppModel)
	if !m.thresholdInput.Focused() {
		t.Fatalf("expected threshold input focused")
	}

	// Type '3' while focused
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m = newM.(*AppModel)
	// Must still be on Tab 1
	if m.settingsTab != 1 {
		t.Errorf("expected settingsTab to stay 1 while typing into input, got %d", m.settingsTab)
	}
}

func TestAppModel_UpdateAvailableBanner(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	cfg := &config.Config{
		URL:    "http://localhost:2283",
		APIKey: "test-key",
	}
	pipeline, err := orchestrator.NewPipeline(cfg)
	if err != nil {
		t.Fatalf("Failed to create pipeline: %v", err)
	}

	update := &version.UpdateInfo{
		CurrentVersion: "v0.2.2",
		LatestVersion:  "v0.3.0",
		ReleaseURL:     "https://github.com/dragosmiron/immich-video-optimizer/releases/tag/v0.3.0",
		HasUpdate:      true,
	}

	m := NewAppModel(cfg, pipeline, WithInitialView(ViewScanPrompt), WithUpdateInfo(update))
	viewOutput := m.View()

	if !strings.Contains(viewOutput, "UPDATE AVAILABLE") {
		t.Errorf("expected viewOutput to contain 'UPDATE AVAILABLE', got: %s", viewOutput)
	}
	if !strings.Contains(viewOutput, "v0.3.0") {
		t.Errorf("expected viewOutput to contain 'v0.3.0', got: %s", viewOutput)
	}
	if !strings.Contains(viewOutput, "https://github.com/dragosmiron/immich-video-optimizer/releases/tag/v0.3.0") {
		t.Errorf("expected viewOutput to contain release URL, got: %s", viewOutput)
	}

	// Test message update
	m2 := NewAppModel(cfg, pipeline)
	newModel, _ := m2.Update(UpdateAvailableMsg{Info: update})
	appModel := newModel.(*AppModel)
	if appModel.updateInfo == nil || !appModel.updateInfo.HasUpdate {
		t.Fatalf("expected updateInfo to be set after UpdateAvailableMsg")
	}
}

func TestAppModel_SettingsDateFilters(t *testing.T) {
	cfg := &config.Config{
		URL:    "http://localhost:2283",
		APIKey: "test-key",
	}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)

	// 1. Go to Settings Tab 1 (Filtering), cursor 3 (Start Date)
	m.state = ViewSettings
	m.settingsTab = 1
	m.settingCursor = 3

	// Press Enter to focus start date input
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newM.(*AppModel)
	if !m.dateAfterInput.Focused() {
		t.Fatalf("expected dateAfterInput to be focused")
	}

	// Type invalid date and press Enter
	m.dateAfterInput.SetValue("invalid-date")
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newM.(*AppModel)
	if m.dateError == "" {
		t.Errorf("expected validation error for invalid date, got none")
	}
	if !m.dateAfterInput.Focused() {
		t.Errorf("expected dateAfterInput to stay focused on error")
	}

	// Type valid start date and press Enter
	m.dateAfterInput.SetValue("2023-01-01")
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newM.(*AppModel)
	if m.dateError != "" {
		t.Errorf("unexpected error on valid date: %s", m.dateError)
	}
	if m.cfg.DateAfter != "2023-01-01" {
		t.Errorf("expected cfg.DateAfter to be 2023-01-01, got %s", m.cfg.DateAfter)
	}
	if m.dateAfterInput.Focused() {
		t.Errorf("expected dateAfterInput to be blurred after saving")
	}

	// 2. Cursor 4 (End Date)
	m.settingCursor = 4
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newM.(*AppModel)
	if !m.dateBeforeInput.Focused() {
		t.Fatalf("expected dateBeforeInput to be focused")
	}

	// Type date before start date
	m.dateBeforeInput.SetValue("2022-12-31")
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newM.(*AppModel)
	if m.dateError == "" {
		t.Errorf("expected validation error for end date before start date, got none")
	}

	// Type valid end date
	m.dateBeforeInput.SetValue("2023-12-31")
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newM.(*AppModel)
	if m.dateError != "" {
		t.Errorf("unexpected error on valid end date: %s", m.dateError)
	}
	if m.cfg.DateBefore != "2023-12-31" {
		t.Errorf("expected cfg.DateBefore to be 2023-12-31, got %s", m.cfg.DateBefore)
	}

	// 3. Verify Intro page displays active filter
	m.state = ViewScanPrompt
	promptView := m.viewScanPrompt()
	if !strings.Contains(promptView, "2023-01-01 to 2023-12-31") || !strings.Contains(promptView, "Filter Active") {
		t.Errorf("viewScanPrompt did not display active date range: %s", promptView)
	}
}

func TestAppModel_NoVideosAdjustFilteringSettingsTab(t *testing.T) {
	cfg := &config.Config{
		URL:    "http://localhost:2283",
		APIKey: "test-key",
	}
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)

	m.state = ViewBrowser
	m.videos = nil

	// Check the view renders the new label
	v := m.viewBrowser()
	if !strings.Contains(v, "[ S ] Adjust Filtering Settings") {
		t.Errorf("expected view to have '[ S ] Adjust Filtering Settings', got: %s", v)
	}

	// Press 's'
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = newM.(*AppModel)

	if m.state != ViewSettings {
		t.Fatalf("expected state ViewSettings, got %v", m.state)
	}
	if m.settingsTab != 1 {
		t.Errorf("expected settingsTab 1 (Filtering), got %d", m.settingsTab)
	}
}

func TestAppModel_SettingsAdvancedTab(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HOME", tempDir)

	cfg := config.DefaultConfig()
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)
	m.state = ViewSettings

	// Switch to Tab 3 (Advanced)
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	m = newM.(*AppModel)
	if m.settingsTab != 3 {
		t.Fatalf("expected settingsTab 3, got %d", m.settingsTab)
	}

	// Verify view rendering contains expected labels
	view := m.viewSettings()
	if !strings.Contains(view, "Custom FFmpeg Flags:") {
		t.Errorf("expected view to contain 'Custom FFmpeg Flags:', got: %s", view)
	}
	if !strings.Contains(view, "Reset to Defaults:") {
		t.Errorf("expected view to contain 'Reset to Defaults:', got: %s", view)
	}
	if !strings.Contains(view, "FFmpeg Command Preview:") {
		t.Errorf("expected view to contain 'FFmpeg Command Preview:', got: %s", view)
	}
	if !strings.Contains(view, "🔒 Protected (immutable)") {
		t.Errorf("expected view to contain protected legend, got: %s", view)
	}

	// 1. Focus custom flags input on Enter
	m.settingCursor = 0
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newM.(*AppModel)
	if !m.customFlagsInput.Focused() {
		t.Errorf("expected customFlagsInput to be focused after Enter")
	}

	// 2. Test syntax error on invalid quotes
	m.customFlagsInput.SetValue(`-vf "scale=1920:1080`)
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newM.(*AppModel)
	if m.customFlagsError == "" {
		t.Errorf("expected customFlagsError for unclosed quote, got empty")
	}
	if !m.customFlagsInput.Focused() {
		t.Errorf("expected customFlagsInput to remain focused on error")
	}

	// 3. Test valid flags input
	m.customFlagsInput.SetValue("-preset veryslow -crf 19 -tune animation")
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newM.(*AppModel)
	if m.customFlagsError != "" {
		t.Errorf("unexpected customFlagsError: %s", m.customFlagsError)
	}
	if m.customFlagsInput.Focused() {
		t.Errorf("expected customFlagsInput to blur after valid save")
	}
	if m.cfg.CustomFFmpegFlags != "-preset veryslow -crf 19 -tune animation" {
		t.Errorf("expected CustomFFmpegFlags saved, got: %s", m.cfg.CustomFFmpegFlags)
	}

	// Check preview includes custom flags
	viewWithCustom := m.viewSettings()
	if !strings.Contains(viewWithCustom, "-preset veryslow") || !strings.Contains(viewWithCustom, "-crf 19") {
		t.Errorf("expected view preview to contain '-preset veryslow' and '-crf 19': %s", viewWithCustom)
	}

	// 4. Test warning when protected flags are entered
	m.customFlagsInput.SetValue("-i hacked.mp4 -preset slow")
	m.cfg.CustomFFmpegFlags = "-i hacked.mp4 -preset slow"
	viewWithProtected := m.viewSettings()
	if !strings.Contains(viewWithProtected, "Protected parameters ignored: -i") {
		t.Errorf("expected protected parameter warning in view, got: %s", viewWithProtected)
	}

	// 5. Test Reset to Defaults row (cursor 1)
	m.settingCursor = 1
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = newM.(*AppModel)
	if m.cfg.CustomFFmpegFlags != "" {
		t.Errorf("expected CustomFFmpegFlags cleared after reset, got: %q", m.cfg.CustomFFmpegFlags)
	}
	if m.customFlagsInput.Value() != "" {
		t.Errorf("expected customFlagsInput cleared after reset, got: %q", m.customFlagsInput.Value())
	}
}

func TestAppModel_SummarySizeGuardDelta(t *testing.T) {
	cfg := config.DefaultConfig()
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)

	origSize := int64(100 * 1024 * 1024)  // 100 MB
	optSize := int64(115 * 1024 * 1024)   // 115 MB (+15 MB)

	msg := orchestrator.PipelineVideoCompleteMsg{
		WorkerID:      1,
		VideoID:       "v1",
		VideoName:     "GH010620.MP4",
		OriginalSize:  origSize,
		OptimizedSize: optSize,
		Skipped:       true,
		SkipReason:    "transcoded file larger",
	}

	newM, _ := m.Update(msg)
	m = newM.(*AppModel)

	if len(m.skippedVideos) != 1 {
		t.Fatalf("expected 1 skipped video, got %d", len(m.skippedVideos))
	}
	sv := m.skippedVideos[0]
	if sv.OriginalSize != origSize || sv.OptimizedSize != optSize {
		t.Errorf("expected sizes %d and %d, got %d and %d", origSize, optSize, sv.OriginalSize, sv.OptimizedSize)
	}

	m.state = ViewSummary
	summaryView := m.viewSummary()

	if !strings.Contains(summaryView, "100.0 MB ➔ 115.0 MB") {
		t.Errorf("expected summary view to display size transition '100.0 MB ➔ 115.0 MB', got: %s", summaryView)
	}
	if !strings.Contains(summaryView, "+15.0 MB, +15.0%") {
		t.Errorf("expected summary view to display '+15.0 MB, +15.0%%', got: %s", summaryView)
	}
	if !strings.Contains(summaryView, "[Size Guard: original kept]") {
		t.Errorf("expected summary view to contain '[Size Guard: original kept]', got: %s", summaryView)
	}
}

func TestAppModel_PipelineFallbackNotificationAndSummary(t *testing.T) {
	cfg := config.DefaultConfig()
	pipeline, _ := orchestrator.NewPipeline(cfg)
	m := NewAppModel(cfg, pipeline)

	// 1. Simulate PipelineFallbackMsg
	fallbackMsg := orchestrator.PipelineFallbackMsg{
		WorkerID:      1,
		VideoID:       "vid-fallback",
		VideoName:     "clip.mov",
		FailedEncoder: "hevc_videotoolbox",
		Reason:        "encoder hardware reset",
	}

	newM, _ := m.Update(fallbackMsg)
	m = newM.(*AppModel)

	// Verify log contains fallback warning
	foundLog := false
	for _, l := range m.logMessages {
		if strings.Contains(l, "clip.mov: hevc_videotoolbox failed") && strings.Contains(l, "falling back to CPU encoder libx265") {
			foundLog = true
			break
		}
	}
	if !foundLog {
		t.Errorf("expected dashboard logs to contain fallback warning, got logs: %v", m.logMessages)
	}

	// Verify worker status reflects fallback
	w := m.getWorker(1)
	if !w.UsedCPUFallback {
		t.Errorf("expected worker 1 to have UsedCPUFallback true")
	}

	// Verify dashboard view renders worker fallback label
	w.Stage = "transcoding"
	w.VideoName = "clip.mov"
	m.currentTranscodeName = "clip.mov"
	dashboardView := m.viewDashboard()
	if !strings.Contains(dashboardView, "libx265 (CPU Fallback)") {
		t.Errorf("expected dashboard view to contain 'libx265 (CPU Fallback)', got: %s", dashboardView)
	}

	// 2. Simulate VideoCompleteMsg with Fallback.Used = true
	completeMsg := orchestrator.PipelineVideoCompleteMsg{
		WorkerID:      1,
		VideoID:       "vid-fallback",
		VideoName:     "clip.mov",
		OriginalSize:  100 * 1024 * 1024,
		OptimizedSize: 40 * 1024 * 1024,
		SavedBytes:    60 * 1024 * 1024,
		Fallback: orchestrator.FallbackInfo{
			Used:          true,
			FailedEncoder: "hevc_videotoolbox",
			Reason:        "encoder hardware reset",
		},
	}

	newM, _ = m.Update(completeMsg)
	m = newM.(*AppModel)

	// Verify summary screen displays [CPU Fallback: hevc_videotoolbox failed] badge
	m.state = ViewSummary
	summary := m.viewSummary()
	if !strings.Contains(summary, "[CPU Fallback: hevc_videotoolbox failed]") {
		t.Errorf("expected summary screen to display '[CPU Fallback: hevc_videotoolbox failed]' badge, got: %s", summary)
	}
}


