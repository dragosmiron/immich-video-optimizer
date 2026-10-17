package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"immich-video-optimizer/config"
	"immich-video-optimizer/immich"
	"immich-video-optimizer/state"
	"immich-video-optimizer/tui/styles"
)

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
	var threshDesc string
	switch m.cfg.ThresholdMode {
	case config.ThresholdModeDisabled:
		threshDesc = "Disabled (All videos)"
	case config.ThresholdModeTotalMB:
		threshDesc = fmt.Sprintf("%s MB (Total File Size)", m.cfg.SizeThreshold)
	default:
		threshDesc = fmt.Sprintf("%s MB/min", m.cfg.SizeThreshold)
	}
	b.WriteString(fmt.Sprintf("  • Threshold:      %s\n", styles.Value.Render(threshDesc)))
	b.WriteString(fmt.Sprintf("  • Resolution:     %s\n", styles.Value.Render(m.cfg.Resolution)))
	b.WriteString(fmt.Sprintf("  • Video Quality:  %s\n", styles.Value.Render(fmt.Sprintf("Level %d (%s)", m.cfg.Quality(), m.cfg.QualityLabel()))))
	sgText := "Enabled (skips replacement if transcoded is larger)"
	if !m.cfg.SizeGuard {
		sgText = "Disabled"
	}
	b.WriteString(fmt.Sprintf("  • Size Guard:     %s\n", styles.Value.Render(sgText)))
	hevcText := "Enabled (skips videos already in HEVC format)"
	if !m.cfg.SkipHEVC {
		hevcText = "Disabled"
	}
	b.WriteString(fmt.Sprintf("  • Skip HEVC:      %s\n", styles.Value.Render(hevcText)))
	prefetchText := "Enabled (prefetch next video in background)"
	if !m.cfg.EnablePrefetch {
		prefetchText = "Disabled (sequential)"
	}
	b.WriteString(fmt.Sprintf("  • Prefetch Next:     %s\n", styles.Value.Render(prefetchText)))
	excludeOptText := "Enabled (skips tagged 'immich-optimizer')"
	if !m.cfg.ExcludeOptimized {
		excludeOptText = "Disabled (includes all)"
	}
	b.WriteString(fmt.Sprintf("  • Exclude Optimized: %s\n", styles.Value.Render(excludeOptText)))

	var dateFilterDesc string
	if m.cfg.DateAfter != "" && m.cfg.DateBefore != "" {
		dateFilterDesc = fmt.Sprintf("%s to %s %s", m.cfg.DateAfter, m.cfg.DateBefore, styles.WarningText.Render("[Filter Active]"))
	} else if m.cfg.DateAfter != "" {
		dateFilterDesc = fmt.Sprintf("From %s onwards %s", m.cfg.DateAfter, styles.WarningText.Render("[Filter Active]"))
	} else if m.cfg.DateBefore != "" {
		dateFilterDesc = fmt.Sprintf("Up to %s %s", m.cfg.DateBefore, styles.WarningText.Render("[Filter Active]"))
	} else {
		dateFilterDesc = "All time"
	}
	b.WriteString(fmt.Sprintf("  • Date Filter:       %s\n", styles.Value.Render(dateFilterDesc)))
	b.WriteString(fmt.Sprintf("  • Encoders:          %s worker(s)\n\n", styles.Value.Render(fmt.Sprintf("%d", m.cfg.ConcurrentEncoders))))

	b.WriteString(styles.SectionHeader.Render("Ready to Scan:"))
	b.WriteString("\nNo videos loaded yet. Start a scan to discover large videos on your server.\n\n")

	b.WriteString(styles.ActiveButton.Render("[ Enter ] Scan Immich Library") + "\n\n")
	b.WriteString(styles.Button.Render("[ S ] Settings") + "  " + styles.Button.Render("[ E ] Edit Server Credentials") + "  " + styles.Button.Render("[ Q ] Quit") + "\n")

	return b.String()
}

func (m *AppModel) viewScanning() string {
	b := strings.Builder{}
	b.WriteString(styles.Title.Render("🎬 Immich Video Optimizer"))
	b.WriteString("\n\n")
	b.WriteString(styles.SectionHeader.Render("🔍 Scanning Immich Library..."))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("  Querying assets on %s\n", styles.Value.Render(m.cfg.URL)))
	switch m.cfg.ThresholdMode {
	case config.ThresholdModeDisabled:
		b.WriteString("  Inspecting all videos (threshold filtering disabled)...\n\n")
	case config.ThresholdModeTotalMB:
		b.WriteString(fmt.Sprintf("  Inspecting videos exceeding %s MB...\n\n", styles.Value.Render(m.cfg.SizeThreshold)))
	default:
		b.WriteString(fmt.Sprintf("  Inspecting videos exceeding %s MB/min...\n\n", styles.Value.Render(m.cfg.SizeThreshold)))
	}
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
	b.WriteString(fmt.Sprintf("  • Non-video assets skipped:          %d (photos, RAW, etc.)\n", r.SkippedNonVideo))

	if r.SkippedAlreadyOptimized > 0 {
		b.WriteString(fmt.Sprintf("  • Already optimized (tagged):        %d (skipped)\n", r.SkippedAlreadyOptimized))
	}
	if r.SkippedDateFilter > 0 {
		b.WriteString(fmt.Sprintf("  • Outside date filter range:         %d (skipped)\n", r.SkippedDateFilter))
	}
	var skippedLabel string
	switch m.cfg.ThresholdMode {
	case config.ThresholdModeDisabled:
		skippedLabel = "  • Videos skipped (threshold disabled): "
	case config.ThresholdModeTotalMB:
		skippedLabel = fmt.Sprintf("  • Videos skipped (below %s MB):      ", m.cfg.SizeThreshold)
	default:
		skippedLabel = fmt.Sprintf("  • Videos skipped (below %s MB/min):   ", m.cfg.SizeThreshold)
	}
	b.WriteString(fmt.Sprintf("%s%d\n", skippedLabel, r.SkippedBelowSize))
	b.WriteString(fmt.Sprintf("  • Eligible videos found:             %s\n", styles.SuccessText.Render(fmt.Sprintf("%d", len(m.videos)))))
	return styles.SummaryBox.Render(b.String())
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
		b.WriteString(fmt.Sprintf("  • Space Saved So Far: %.2f MB\n", float64(m.session.GetTotalSavedBytes())/(1024*1024)))
		b.WriteString(fmt.Sprintf("  • Pending Videos:     %s\n\n", styles.WarningText.Render(fmt.Sprintf("%d", m.session.PendingCount()))))
	}

	b.WriteString("Options:\n")
	b.WriteString("  [ R ] Resume Optimization (process remaining pending videos)\n")
	b.WriteString("  [ S ] Start New / Scan Library (discards paused session)\n")
	b.WriteString("  [ C ] Clear Saved State & Exit\n")
	b.WriteString("  [ Q ] Quit\n")

	return b.String()
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
