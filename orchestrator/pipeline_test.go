package orchestrator

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

	ch := make(chan interface{}, 100)
	
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var skipped bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for msg := range ch {
			if cmsg, ok := msg.(PipelineVideoCompleteMsg); ok {
				if cmsg.Err != nil {
					t.Logf("Video error: %v", cmsg.Err)
				}
				skipped = cmsg.Skipped
			}
		}
	}()

	pipeline.Run(ctx, videos, ch)
	wg.Wait()
	
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

	ch := make(chan interface{}, 100)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var gotError bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for msg := range ch {
			if cmsg, ok := msg.(PipelineVideoCompleteMsg); ok {
				if cmsg.Err != nil {
					gotError = true
				}
			}
		}
	}()

	pipeline.Run(ctx, videos, ch)
	wg.Wait()

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

		ch := make(chan interface{}, 100)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		var completeMsg DryRunCompleteMsg
		var videoMsgs []DryRunVideoMsg
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for msg := range ch {
				switch m := msg.(type) {
				case DryRunVideoMsg:
					videoMsgs = append(videoMsgs, m)
				case DryRunCompleteMsg:
					completeMsg = m
				}
			}
		}()

		p.RunDryRun(ctx, videos, ch)
		wg.Wait()

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

		ch := make(chan interface{}, 100)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		var completeMsg DryRunCompleteMsg
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for msg := range ch {
				if m, ok := msg.(DryRunCompleteMsg); ok {
					completeMsg = m
				}
			}
		}()

		p.RunDryRun(ctx, videos, ch)
		wg.Wait()

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
		ch := make(chan interface{}, 100)
		var gotComplete bool
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for msg := range ch {
				if _, ok := msg.(DryRunCompleteMsg); ok {
					gotComplete = true
				}
			}
		}()
		p.RunDryRun(context.Background(), nil, ch)
		wg.Wait()
		if !gotComplete {
			t.Errorf("expected DryRunCompleteMsg on empty videos list")
		}
	})

	// 4. Context Cancelled early exits cleanly
	t.Run("ContextCancelled", func(t *testing.T) {
		cfg := &config.Config{URL: "http://localhost:2283", APIKey: "key", SkipHEVC: true}
		p, _ := NewPipelineWithBinaries(cfg, binaries)
		ch := make(chan interface{}, 100)
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

	ch := make(chan interface{}, 100)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var gotError bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for msg := range ch {
			if cmsg, ok := msg.(PipelineVideoCompleteMsg); ok {
				if cmsg.Err != nil {
					gotError = true
				}
			}
		}
	}()

	p.Run(ctx, videos, ch)
	wg.Wait()

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

	ch := make(chan interface{}, 100)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var gotError bool
	var totalSaved int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
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
	}()

	p.Run(ctx, videos, ch)
	wg.Wait()

	if !gotError {
		t.Errorf("Expected error when DeleteAsset fails")
	}
	if totalSaved != 0 {
		t.Errorf("Expected 0 saved bytes when deletion fails, got %d", totalSaved)
	}
}

func TestPipeline_AlbumDoesNotIncludeSkippedVideos(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	var addedToAlbum []string
	var mu sync.Mutex

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/albums":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "album-only-transcoded"}`))
			return
		case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/api/albums/"):
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			addedToAlbum = append(addedToAlbum, string(body))
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		case r.Method == "GET" && r.URL.Path == "/api/assets/asset-hevc/original":
			// Handled by range probe or download
			w.Header().Set("Content-Type", "video/mp4")
			w.Write([]byte("SIMULATED_HEVC"))
			return
		case r.Method == "GET" && r.URL.Path == "/api/assets/asset-h264/original":
			w.Header().Set("Content-Type", "video/mp4")
			w.Write([]byte("SIMULATED_H264"))
			return
		case r.Method == "POST" && r.URL.Path == "/api/assets":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "asset-replacement-new"}`))
			return
		case r.Method == "DELETE" && r.URL.Path == "/api/assets":
			w.WriteHeader(http.StatusOK)
			return
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer ts.Close()

	fakeFFmpeg := filepath.Join(tempHome, "fake_ffmpeg")
	// If asked for info, print hevc for asset-hevc, h264 for asset-h264. If transcoding, write 5 bytes output.
	script := `#!/bin/sh
for arg do
  if [ "$arg" = "output.mp4" ] || echo "$arg" | grep -q "opt_"; then
    echo "opt" > "$arg"
    exit 0
  fi
  last_arg="$arg"
done
if echo "$*" | grep -q "asset-hevc"; then
  echo "Stream #0:0: Video: hevc, 1920x1080"
  exit 0
fi
if echo "$*" | grep -q "asset-h264"; then
  echo "Stream #0:0: Video: h264, 1920x1080"
  exit 0
fi
echo "dummy" > "$last_arg"
exit 0
`
	_ = os.WriteFile(fakeFFmpeg, []byte(script), 0755)

	fakeExifTool := filepath.Join(tempHome, "fake_exiftool")
	_ = os.WriteFile(fakeExifTool, []byte("#!/bin/sh\nexit 0\n"), 0755)

	cfg := &config.Config{
		URL:           ts.URL,
		APIKey:        "test-key",
		SizeThreshold: "0",
		SkipHEVC:      true,
	}

	binaries := &engine.BinaryManager{
		FFmpegPath:   fakeFFmpeg,
		ExifToolPath: fakeExifTool,
	}

	p, err := NewPipelineWithBinaries(cfg, binaries)
	if err != nil {
		t.Fatalf("Failed to initialize pipeline: %v", err)
	}
	p.Client().SetHTTPClient(&http.Client{
		Transport: &testRoundTripper{handler: ts.Config.Handler},
	})

	videos := []immich.Asset{
		{
			ID:               "asset-hevc",
			OriginalFileName: "already_hevc.mp4",
			ExifInfo: immich.ExifInfo{
				FileSizeInByte:  2000,
				ExifImageWidth:  1920,
				ExifImageHeight: 1080,
			},
		},
		{
			ID:               "asset-h264",
			OriginalFileName: "convert_me.mp4",
			ExifInfo: immich.ExifInfo{
				FileSizeInByte:  2000,
				ExifImageWidth:  1920,
				ExifImageHeight: 1080,
			},
		},
	}

	ch := make(chan interface{}, 100)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range ch {
		}
	}()

	p.Run(ctx, videos, ch)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	foundOriginalQueued := false
	for _, reqBody := range addedToAlbum {
		if strings.Contains(reqBody, "asset-hevc") {
			t.Errorf("Skipped HEVC asset 'asset-hevc' was erroneously added to tracking album! Body: %s", reqBody)
		}
		if strings.Contains(reqBody, "asset-h264") {
			foundOriginalQueued = true
		}
	}
	if !foundOriginalQueued {
		t.Errorf("Expected original queued asset 'asset-h264' to be added to album before optimization, but got bodies: %v", addedToAlbum)
	}

	foundReplacement := false
	for _, reqBody := range addedToAlbum {
		if strings.Contains(reqBody, "asset-replacement-new") {
			foundReplacement = true
		}
	}
	if !foundReplacement {
		t.Errorf("Expected replacement asset 'asset-replacement-new' to be added to album, but got bodies: %v", addedToAlbum)
	}
}

func TestPipeline_AllSkippedDoesNotCreateAlbum(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	var albumCreated bool
	var mu sync.Mutex

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "POST" && r.URL.Path == "/api/albums" {
			albumCreated = true
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "album-should-not-exist"}`))
			return
		}
		if r.Method == "GET" && strings.Contains(r.URL.Path, "/original") {
			w.Header().Set("Content-Type", "video/mp4")
			w.Write([]byte("SIMULATED_HEVC"))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	fakeFFmpeg := filepath.Join(tempHome, "fake_ffmpeg")
	script := `#!/bin/sh
echo "Stream #0:0: Video: hevc, 1920x1080"
exit 0
`
	_ = os.WriteFile(fakeFFmpeg, []byte(script), 0755)

	fakeExifTool := filepath.Join(tempHome, "fake_exiftool")
	_ = os.WriteFile(fakeExifTool, []byte("#!/bin/sh\nexit 0\n"), 0755)

	cfg := &config.Config{
		URL:           ts.URL,
		APIKey:        "test-key",
		SizeThreshold: "0",
		SkipHEVC:      true,
	}

	binaries := &engine.BinaryManager{
		FFmpegPath:   fakeFFmpeg,
		ExifToolPath: fakeExifTool,
	}

	p, err := NewPipelineWithBinaries(cfg, binaries)
	if err != nil {
		t.Fatalf("Failed to initialize pipeline: %v", err)
	}
	p.Client().SetHTTPClient(&http.Client{
		Transport: &testRoundTripper{handler: ts.Config.Handler},
	})

	videos := []immich.Asset{
		{
			ID:               "asset-hevc-1",
			OriginalFileName: "already_hevc_1.mp4",
			ExifInfo: immich.ExifInfo{
				FileSizeInByte: 2000,
			},
		},
		{
			ID:               "asset-hevc-2",
			OriginalFileName: "already_hevc_2.mp4",
			ExifInfo: immich.ExifInfo{
				FileSizeInByte: 2000,
			},
		},
	}

	ch := make(chan interface{}, 100)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var wg2 sync.WaitGroup
	wg2.Add(1)
	go func() {
		defer wg2.Done()
		for range ch {
		}
	}()

	p.Run(ctx, videos, ch)
	wg2.Wait()

	mu.Lock()
	defer mu.Unlock()
	if albumCreated {
		t.Errorf("Expected NO tracking album to be created when all videos are skipped, but CreateTrackingAlbum was invoked!")
	}
}

func TestPipeline_DryRunAlbumCreationAndCleanup(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	var createdAlbums []string
	var deletedAlbums []string
	var addedToAlbum []string
	var mu sync.Mutex

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPost && r.URL.Path == "/api/albums" {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			name := body["albumName"]
			createdAlbums = append(createdAlbums, name)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(immich.Album{ID: "album-dryrun-123", AlbumName: name})
			return
		}
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/assets") {
			body, _ := io.ReadAll(r.Body)
			addedToAlbum = append(addedToAlbum, string(body))
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/albums/") {
			id := strings.TrimPrefix(r.URL.Path, "/api/albums/")
			deletedAlbums = append(deletedAlbums, id)
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/original") {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	fakeFFmpeg := filepath.Join(tempHome, "fake_ffmpeg")
	script := `#!/bin/sh
echo "Stream #0:0: Video: h264, 1920x1080"
exit 0
`
	_ = os.WriteFile(fakeFFmpeg, []byte(script), 0755)

	fakeExifTool := filepath.Join(tempHome, "fake_exiftool")
	_ = os.WriteFile(fakeExifTool, []byte("#!/bin/sh\nexit 0\n"), 0755)

	cfg := &config.Config{
		URL:           ts.URL,
		APIKey:        "test-key",
		SizeThreshold: "0",
		SkipHEVC:      true,
	}

	binaries := &engine.BinaryManager{
		FFmpegPath:   fakeFFmpeg,
		ExifToolPath: fakeExifTool,
	}

	p, err := NewPipelineWithBinaries(cfg, binaries)
	if err != nil {
		t.Fatalf("Failed to initialize pipeline: %v", err)
	}
	p.Client().SetHTTPClient(&http.Client{
		Transport: &testRoundTripper{handler: ts.Config.Handler},
	})

	videos := []immich.Asset{
		{
			ID:               "asset-1",
			OriginalFileName: "video1.mp4",
			ExifInfo: immich.ExifInfo{
				FileSizeInByte:  5000,
				ExifImageWidth:  1920,
				ExifImageHeight: 1080,
			},
		},
	}

	// 1. Run first dry run -> should create dry run album
	ch1 := make(chan interface{}, 100)
	var completeMsg DryRunCompleteMsg
	var wg1 sync.WaitGroup
	wg1.Add(1)
	go func() {
		defer wg1.Done()
		for msg := range ch1 {
			if c, ok := msg.(DryRunCompleteMsg); ok {
				completeMsg = c
			}
		}
	}()
	p.RunDryRun(context.Background(), videos, ch1)
	wg1.Wait()

	if completeMsg.DryRunAlbumID != "album-dryrun-123" {
		t.Errorf("Expected DryRunAlbumID 'album-dryrun-123', got '%s'", completeMsg.DryRunAlbumID)
	}
	if !strings.HasPrefix(completeMsg.DryRunAlbumName, "Immich-Optimizer-DryRun-") {
		t.Errorf("Expected DryRunAlbumName to start with Immich-Optimizer-DryRun-, got '%s'", completeMsg.DryRunAlbumName)
	}

	mu.Lock()
	if len(createdAlbums) != 1 {
		t.Errorf("Expected 1 created album, got %d", len(createdAlbums))
	}
	if len(addedToAlbum) != 1 || !strings.Contains(addedToAlbum[0], "asset-1") {
		t.Errorf("Expected asset-1 to be added to dry run album, got %v", addedToAlbum)
	}
	mu.Unlock()

	// 2. Run a second dry run -> should delete the first dry run album and create a new one
	ch2 := make(chan interface{}, 100)
	var wg2 sync.WaitGroup
	wg2.Add(1)
	go func() {
		defer wg2.Done()
		for range ch2 {
		}
	}()
	p.RunDryRun(context.Background(), videos, ch2)
	wg2.Wait()

	mu.Lock()
	if len(deletedAlbums) != 1 || deletedAlbums[0] != "album-dryrun-123" {
		t.Errorf("Expected previous dry run album 'album-dryrun-123' to be deleted on new dry run, got: %v", deletedAlbums)
	}
	mu.Unlock()

	// 3. CleanDryRunAlbum directly cleans up any remaining album
	p.CleanDryRunAlbum()
	mu.Lock()
	if len(deletedAlbums) != 2 || deletedAlbums[1] != "album-dryrun-123" {
		t.Errorf("Expected CleanDryRunAlbum to delete album, got: %v", deletedAlbums)
	}
	mu.Unlock()
}

func TestPipeline_UnbufferedChannel(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" && r.URL.Path == "/api/search/metadata" {
			w.Write([]byte(`{"assets": {"items": [{"id": "unbuf-1", "type": "VIDEO", "originalFileName": "unbuf.mp4", "exifInfo": {"fileSizeInByte": 5000}}]}}`))
			return
		}
		if r.Method == "GET" && strings.Contains(r.URL.Path, "/original") {
			w.Header().Set("Content-Type", "video/mp4")
			w.Write([]byte("fake video content"))
			return
		}
		if r.Method == "POST" && r.URL.Path == "/api/albums" {
			w.Write([]byte(`{"id": "album-unbuf"}`))
			return
		}
		if r.Method == "POST" && r.URL.Path == "/api/assets" {
			w.Write([]byte(`{"id": "new-unbuf-id"}`))
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

	// Test Run with unbuffered channel (cap = 0)
	chRun := make(chan interface{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var runCount int
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range chRun {
			runCount++
		}
	}()

	p.Run(ctx, videos, chRun)
	wg.Wait()

	if runCount == 0 {
		t.Errorf("Expected messages from pipeline run on unbuffered channel")
	}

	// Test RunDryRun with unbuffered channel (cap = 0)
	chDry := make(chan interface{})
	ctxDry, cancelDry := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelDry()

	var dryCount int
	var wgDry sync.WaitGroup
	wgDry.Add(1)
	go func() {
		defer wgDry.Done()
		for range chDry {
			dryCount++
		}
	}()

	p.RunDryRun(ctxDry, videos, chDry)
	wgDry.Wait()

	if dryCount == 0 {
		t.Errorf("Expected messages from pipeline dry run on unbuffered channel")
	}
}



