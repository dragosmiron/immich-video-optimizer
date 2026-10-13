package orchestrator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"immich-video-optimizer/config"
	"immich-video-optimizer/engine"
	"immich-video-optimizer/immich"
)

func TestMain(m *testing.M) {
	tempDir, err := os.MkdirTemp("", "orchestrator-test-*")
	if err == nil {
		os.Setenv("HOME", tempDir)
		defer os.RemoveAll(tempDir)
	}
	os.Exit(m.Run())
}

type testRoundTripper struct {
	handler http.Handler
}

func (tr *testRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	done := make(chan struct{})
	rec := httptest.NewRecorder()

	go func() {
		tr.handler.ServeHTTP(rec, req)
		close(done)
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-done:
		return rec.Result(), nil
	}
}

func TestSizeGuard(t *testing.T) {
	// We will create a dummy httptest server that serves a tiny original video
	// and accepts the replacement upload. If size guard works, the replacement
	// will be aborted because the transcoded file will be larger.
	
	// Create dummy video file
	dummyVideo := []byte("dummy video content")
	
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/api/assets/asset-1/original" {
			w.Write(dummyVideo)
			return
		}
		if r.Method == "POST" && r.URL.Path == "/api/search/metadata" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"assets": {"items": [{"id": "asset-1", "type": "VIDEO", "originalFileName": "test.mp4", "exifInfo": {"fileSizeInByte": 19}}]}}`))
			return
		}
		if r.Method == "POST" && r.URL.Path == "/api/albums" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "album-1"}`))
			return
		}
		if r.Method == "PUT" && r.URL.Path == "/api/albums/album-1/assets" {
			w.WriteHeader(http.StatusOK)
			return
		}
		// Fails if we try to upload (since size guard should prevent it)
		if r.Method == "POST" && r.URL.Path == "/api/assets" {
			t.Errorf("Should not upload because of size guard")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	tempDir := t.TempDir()
	fakeFFmpeg := filepath.Join(tempDir, "fake_ffmpeg")
	// Shell script that writes 100 bytes to the last argument (output file)
	script := "#!/bin/sh\nfor arg do last_arg=\"$arg\"; done\necho 'this is a much larger transcoded output file that exceeds the original size' > \"$last_arg\"\nexit 0\n"
	if err := os.WriteFile(fakeFFmpeg, []byte(script), 0755); err != nil {
		t.Fatalf("Failed to create fake ffmpeg script: %v", err)
	}
	
	fakeExifTool := filepath.Join(tempDir, "exiftool")
	if err := os.WriteFile(fakeExifTool, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("Failed to create fake exiftool script: %v", err)
	}

	cfg := &config.Config{
		URL:           ts.URL,
		APIKey:        "test-key",
		SizeThreshold: "0",
		SizeGuard:     true,
		Preset:        "ultrafast",
		Resolution:    "Original",
	}

	binaries := &engine.BinaryManager{
		FFmpegPath:   fakeFFmpeg,
		ExifToolPath: fakeExifTool,
	}

	pipeline, err := NewPipelineWithBinaries(cfg, binaries)
	if err != nil {
		t.Fatalf("Failed to create pipeline: %v", err)
	}
	pipeline.Client().SetHTTPClient(&http.Client{
		Transport: &testRoundTripper{handler: ts.Config.Handler},
	})
	
	videos, err := pipeline.QueryVideos(0)
	if err != nil {
		t.Fatalf("Failed to query videos: %v", err)
	}

	ch := make(chan interface{}, 10)
	
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	pipeline.Run(ctx, videos, ch)

	// Validate events
	var skipped bool
	for msg := range ch {
		if cmsg, ok := msg.(PipelineVideoCompleteMsg); ok {
			if cmsg.Err != nil {
				t.Logf("Video error: %v", cmsg.Err)
			}
			skipped = cmsg.Skipped
		}
	}
	
	if !skipped {
		t.Errorf("Expected video to be skipped due to size guard")
	}
}

func TestPipeline_DownloadErrorShortCircuit(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/api/assets/asset-fail/original" {
			// Fail download with 500 error
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if r.Method == "POST" && r.URL.Path == "/api/search/metadata" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"assets": {"items": [{"id": "asset-fail", "type": "VIDEO", "originalFileName": "fail.mp4", "exifInfo": {"fileSizeInByte": 1000}}]}}`))
			return
		}
		if r.Method == "POST" && r.URL.Path == "/api/albums" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "album-fail"}`))
			return
		}
		if r.Method == "PUT" && r.URL.Path == "/api/albums/album-fail/assets" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	fakeFFmpeg := filepath.Join(tempHome, "fake_ffmpeg")
	_ = os.WriteFile(fakeFFmpeg, []byte("#!/bin/sh\nexit 0\n"), 0755)
	fakeExifTool := filepath.Join(tempHome, "fake_exiftool")
	_ = os.WriteFile(fakeExifTool, []byte("#!/bin/sh\nexit 0\n"), 0755)

	cfg := &config.Config{
		URL:           ts.URL,
		APIKey:        "test-key",
		SizeThreshold: "0",
	}

	binaries := &engine.BinaryManager{
		FFmpegPath:   fakeFFmpeg,
		ExifToolPath: fakeExifTool,
	}

	pipeline, err := NewPipelineWithBinaries(cfg, binaries)
	if err != nil {
		t.Fatalf("Failed to create pipeline: %v", err)
	}
	pipeline.Client().SetHTTPClient(&http.Client{
		Transport: &testRoundTripper{handler: ts.Config.Handler},
	})

	videos, err := pipeline.QueryVideos(0)
	if err != nil {
		t.Fatalf("Failed to query videos: %v", err)
	}

	ch := make(chan interface{}, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	pipeline.Run(ctx, videos, ch)

	var gotError bool
	for msg := range ch {
		if cmsg, ok := msg.(PipelineVideoCompleteMsg); ok {
			if cmsg.Err != nil {
				gotError = true
			}
		}
	}

	if !gotError {
		t.Errorf("Expected video error from failed download")
	}
}

func TestPipeline_RunDryRun_SkipHEVC(t *testing.T) {
	tempHome := t.TempDir()

	fakeFFmpeg := filepath.Join(tempHome, "fake_ffmpeg")
	script := `#!/bin/sh
for arg do
  case "$arg" in
    *asset-hevc*)
      echo "Stream #0:0: Video: hevc (Main), yuv420p"
      exit 0
      ;;
    *asset-h264*)
      echo "Stream #0:0: Video: h264 (High), yuv420p"
      exit 0
      ;;
    *asset-err*)
      exit 1
      ;;
  esac
done
echo "Stream #0:0: Video: h264"
exit 0
`
	if err := os.WriteFile(fakeFFmpeg, []byte(script), 0755); err != nil {
		t.Fatalf("Failed to write fake ffmpeg: %v", err)
	}

	binaries := &engine.BinaryManager{
		FFmpegPath: fakeFFmpeg,
	}

	videos := []immich.Asset{
		{ID: "asset-hevc", OriginalFileName: "hevc_video.mp4"},
		{ID: "asset-h264", OriginalFileName: "h264_video.mp4"},
		{ID: "asset-err", OriginalFileName: "err_video.mp4"},
	}

	// 1. SkipHEVC = true
	t.Run("SkipHEVC_True", func(t *testing.T) {
		cfg := &config.Config{
			URL:      "http://localhost:2283",
			APIKey:   "key",
			SkipHEVC: true,
		}
		p, err := NewPipelineWithBinaries(cfg, binaries)
		if err != nil {
			t.Fatalf("NewPipelineWithBinaries error: %v", err)
		}

		ch := make(chan interface{}, 20)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		p.RunDryRun(ctx, videos, ch)

		var completeMsg DryRunCompleteMsg
		var videoMsgs []DryRunVideoMsg
		for msg := range ch {
			switch m := msg.(type) {
			case DryRunVideoMsg:
				videoMsgs = append(videoMsgs, m)
			case DryRunCompleteMsg:
				completeMsg = m
			}
		}

		if len(videoMsgs) != 3 {
			t.Fatalf("expected 3 video messages, got %d", len(videoMsgs))
		}

		// When SkipHEVC = true:
		// asset-hevc should be in HEVCSkipped
		if len(completeMsg.HEVCSkipped) != 1 || completeMsg.HEVCSkipped[0].ID != "asset-hevc" {
			t.Errorf("expected 1 HEVCSkipped asset (asset-hevc), got: %+v", completeMsg.HEVCSkipped)
		}
		// asset-h264 and asset-err should be in ToProcess
		if len(completeMsg.ToProcess) != 2 {
			t.Errorf("expected 2 ToProcess assets, got: %d", len(completeMsg.ToProcess))
		}
		// asset-err should be in ProbeFailed
		if len(completeMsg.ProbeFailed) != 1 || completeMsg.ProbeFailed[0].ID != "asset-err" {
			t.Errorf("expected 1 ProbeFailed asset (asset-err), got: %+v", completeMsg.ProbeFailed)
		}
	})

	// 2. SkipHEVC = false
	t.Run("SkipHEVC_False", func(t *testing.T) {
		cfg := &config.Config{
			URL:      "http://localhost:2283",
			APIKey:   "key",
			SkipHEVC: false, // OFF!
		}
		p, err := NewPipelineWithBinaries(cfg, binaries)
		if err != nil {
			t.Fatalf("NewPipelineWithBinaries error: %v", err)
		}

		ch := make(chan interface{}, 20)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		p.RunDryRun(ctx, videos, ch)

		var completeMsg DryRunCompleteMsg
		for msg := range ch {
			if m, ok := msg.(DryRunCompleteMsg); ok {
				completeMsg = m
			}
		}

		// When SkipHEVC = false:
		// No videos should be in HEVCSkipped!
		if len(completeMsg.HEVCSkipped) != 0 {
			t.Errorf("expected 0 HEVCSkipped when SkipHEVC is false, got: %+v", completeMsg.HEVCSkipped)
		}
		// All 3 videos must be in ToProcess!
		if len(completeMsg.ToProcess) != 3 {
			t.Errorf("expected 3 ToProcess assets when SkipHEVC is false, got: %d", len(completeMsg.ToProcess))
		}
	})

	// 3. Empty video list
	t.Run("EmptyVideos", func(t *testing.T) {
		cfg := &config.Config{URL: "http://localhost:2283", APIKey: "key"}
		p, _ := NewPipelineWithBinaries(cfg, binaries)
		ch := make(chan interface{}, 5)
		p.RunDryRun(context.Background(), nil, ch)
		var gotComplete bool
		for msg := range ch {
			if _, ok := msg.(DryRunCompleteMsg); ok {
				gotComplete = true
			}
		}
		if !gotComplete {
			t.Errorf("expected DryRunCompleteMsg on empty videos list")
		}
	})

	// 4. Context Cancelled early exits cleanly
	t.Run("ContextCancelled", func(t *testing.T) {
		cfg := &config.Config{URL: "http://localhost:2283", APIKey: "key", SkipHEVC: true}
		p, _ := NewPipelineWithBinaries(cfg, binaries)
		ch := make(chan interface{}, 20)
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // immediately cancel
		p.RunDryRun(ctx, videos, ch)

		var completeMsgReceived bool
		for msg := range ch {
			if _, ok := msg.(DryRunCompleteMsg); ok {
				completeMsgReceived = true
			}
		}
		if completeMsgReceived {
			t.Errorf("expected RunDryRun to terminate early on cancelled context without emitting DryRunCompleteMsg")
		}
	})
}

func TestPipeline_DuplicateAssetGuard(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	var deleteCalled bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/api/assets/asset-dup/original" {
			w.Write([]byte("dummy video"))
			return
		}
		if r.Method == "POST" && r.URL.Path == "/api/search/metadata" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"assets": {"items": [{"id": "asset-dup", "type": "VIDEO", "originalFileName": "dup.mp4", "exifInfo": {"fileSizeInByte": 1000}}]}}`))
			return
		}
		if r.Method == "POST" && r.URL.Path == "/api/albums" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "album-1"}`))
			return
		}
		if r.Method == "POST" && r.URL.Path == "/api/assets" {
			// Returns identical asset ID
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "asset-dup", "type": "VIDEO"}`))
			return
		}
		if r.Method == "DELETE" && r.URL.Path == "/api/assets" {
			deleteCalled = true
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	fakeFFmpeg := filepath.Join(tempHome, "fake_ffmpeg")
	script := "#!/bin/sh\nfor arg do last_arg=\"$arg\"; done\necho 'small' > \"$last_arg\"\nexit 0\n"
	_ = os.WriteFile(fakeFFmpeg, []byte(script), 0755)
	fakeExifTool := filepath.Join(tempHome, "fake_exiftool")
	_ = os.WriteFile(fakeExifTool, []byte("#!/bin/sh\nexit 0\n"), 0755)

	cfg := &config.Config{
		URL:           ts.URL,
		APIKey:        "test-key",
		SizeThreshold: "0",
	}

	binaries := &engine.BinaryManager{
		FFmpegPath:   fakeFFmpeg,
		ExifToolPath: fakeExifTool,
	}

	p, err := NewPipelineWithBinaries(cfg, binaries)
	if err != nil {
		t.Fatalf("Failed to create pipeline: %v", err)
	}
	p.Client().SetHTTPClient(&http.Client{
		Transport: &testRoundTripper{handler: ts.Config.Handler},
	})

	videos, err := p.QueryVideos(0)
	if err != nil {
		t.Fatalf("Failed to query videos: %v", err)
	}

	ch := make(chan interface{}, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	p.Run(ctx, videos, ch)

	var gotError bool
	for msg := range ch {
		if cmsg, ok := msg.(PipelineVideoCompleteMsg); ok {
			if cmsg.Err != nil {
				gotError = true
			}
		}
	}

	if !gotError {
		t.Errorf("Expected pipeline to report error when duplicate asset ID is returned")
	}
	if deleteCalled {
		t.Errorf("Expected original asset NOT to be deleted when duplicate asset ID is returned")
	}
}

func TestPipeline_DeleteFailureHandling(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/api/assets/asset-del/original" {
			w.Write([]byte("dummy video"))
			return
		}
		if r.Method == "POST" && r.URL.Path == "/api/search/metadata" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"assets": {"items": [{"id": "asset-del", "type": "VIDEO", "originalFileName": "del.mp4", "exifInfo": {"fileSizeInByte": 1000}}]}}`))
			return
		}
		if r.Method == "POST" && r.URL.Path == "/api/albums" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "album-1"}`))
			return
		}
		if r.Method == "POST" && r.URL.Path == "/api/assets" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "new-asset-id", "type": "VIDEO"}`))
			return
		}
		if r.Method == "PUT" && r.URL.Path == "/api/albums/album-1/assets" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == "DELETE" && r.URL.Path == "/api/assets" {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error": "delete failed"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	fakeFFmpeg := filepath.Join(tempHome, "fake_ffmpeg")
	script := "#!/bin/sh\nfor arg do last_arg=\"$arg\"; done\necho 'small' > \"$last_arg\"\nexit 0\n"
	_ = os.WriteFile(fakeFFmpeg, []byte(script), 0755)
	fakeExifTool := filepath.Join(tempHome, "fake_exiftool")
	_ = os.WriteFile(fakeExifTool, []byte("#!/bin/sh\nexit 0\n"), 0755)

	cfg := &config.Config{
		URL:           ts.URL,
		APIKey:        "test-key",
		SizeThreshold: "0",
	}

	binaries := &engine.BinaryManager{
		FFmpegPath:   fakeFFmpeg,
		ExifToolPath: fakeExifTool,
	}

	p, err := NewPipelineWithBinaries(cfg, binaries)
	if err != nil {
		t.Fatalf("Failed to create pipeline: %v", err)
	}
	p.Client().SetHTTPClient(&http.Client{
		Transport: &testRoundTripper{handler: ts.Config.Handler},
	})

	videos, err := p.QueryVideos(0)
	if err != nil {
		t.Fatalf("Failed to query videos: %v", err)
	}

	ch := make(chan interface{}, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	p.Run(ctx, videos, ch)

	var gotError bool
	var totalSaved int64
	for msg := range ch {
		if cmsg, ok := msg.(PipelineVideoCompleteMsg); ok {
			if cmsg.Err != nil {
				gotError = true
			}
		}
		if dmsg, ok := msg.(PipelineDoneMsg); ok {
			totalSaved = dmsg.TotalSavedBytes
		}
	}

	if !gotError {
		t.Errorf("Expected error when DeleteAsset fails")
	}
	if totalSaved != 0 {
		t.Errorf("Expected 0 saved bytes when deletion fails, got %d", totalSaved)
	}
}


