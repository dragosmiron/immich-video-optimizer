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
		case r.Method == "GET" && r.URL.Path == "/api/assets/asset-h264":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "asset-h264", "type": "VIDEO"}`))
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

func TestPipeline_DryRunToOptimizationAlbumReuseAndRename(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	var createdAlbums []string
	var deletedAlbums []string
	var renamedAlbums []string
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
		if r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/api/albums/") {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			renamedAlbums = append(renamedAlbums, body["albumName"])
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/assets") {
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
			w.Header().Set("Content-Type", "video/mp4")
			w.Write([]byte("fake video content"))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/assets" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "new-asset-456"}`))
			return
		}
		if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/assets/") {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	fakeFFmpeg := filepath.Join(tempHome, "fake_ffmpeg")
	script := `#!/bin/sh
if [ "$1" = "-i" ] || [ "$2" = "-i" ] || [ "$3" = "-i" ]; then
    echo "Stream #0:0: Video: h264, 1920x1080"
    # If output file specified, write a dummy file
    last="${@: -1}"
    if [ "$last" != "" ]; then
        echo "fake transcoded" > "$last"
    fi
fi
exit 0
`
	_ = os.WriteFile(fakeFFmpeg, []byte(script), 0755)

	fakeExifTool := filepath.Join(tempHome, "fake_exiftool")
	_ = os.WriteFile(fakeExifTool, []byte("#!/bin/sh\nexit 0\n"), 0755)

	cfg := &config.Config{
		URL:           ts.URL,
		APIKey:        "test-key",
		SizeThreshold: "0",
		SkipHEVC:      false,
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
			Size:             5000,
			ExifInfo: immich.ExifInfo{
				FileSizeInByte:  5000,
				ExifImageWidth:  1920,
				ExifImageHeight: 1080,
			},
		},
	}

	// 1. Run dry run
	chDry := make(chan interface{}, 100)
	var wgDry sync.WaitGroup
	wgDry.Add(1)
	go func() {
		defer wgDry.Done()
		for range chDry {
		}
	}()
	p.RunDryRun(context.Background(), videos, chDry)
	wgDry.Wait()

	mu.Lock()
	if len(createdAlbums) != 1 {
		t.Fatalf("Expected 1 dry run album created, got %d", len(createdAlbums))
	}
	if !strings.HasPrefix(createdAlbums[0], "Immich-Optimizer-DryRun-") {
		t.Errorf("Expected album name to start with Immich-Optimizer-DryRun-, got %s", createdAlbums[0])
	}
	mu.Unlock()

	// 2. Run optimization using the same pipeline instance (which retains dryRunAlbumID)
	chOpt := make(chan interface{}, 200)
	var wgOpt sync.WaitGroup
	wgOpt.Add(1)
	go func() {
		defer wgOpt.Done()
		for range chOpt {
		}
	}()
	p.Run(context.Background(), videos, chOpt)
	wgOpt.Wait()

	mu.Lock()
	defer mu.Unlock()

	// Dry run album must NOT have been deleted
	if len(deletedAlbums) != 0 {
		t.Errorf("Expected dry run album NOT to be deleted, got deleted: %v", deletedAlbums)
	}
	// No second album should have been created (only the 1 from dry run)
	if len(createdAlbums) != 1 {
		t.Errorf("Expected only 1 album created total (dry-run reused), got %d: %v", len(createdAlbums), createdAlbums)
	}
	// Album must have been renamed to InProgress, and then Completed!
	if len(renamedAlbums) != 2 {
		t.Fatalf("Expected 2 renames (InProgress and Completed), got %d: %v", len(renamedAlbums), renamedAlbums)
	}
	if !strings.HasPrefix(renamedAlbums[0], "Immich-Optimizer-InProgress-") {
		t.Errorf("Expected first rename to start with Immich-Optimizer-InProgress-, got: %s", renamedAlbums[0])
	}
	if !strings.HasPrefix(renamedAlbums[1], "Immich-Optimizer-Completed-") {
		t.Errorf("Expected second rename to start with Immich-Optimizer-Completed-, got: %s", renamedAlbums[1])
	}
}

func TestPipeline_DirectRunAlbumLifecycle(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	var createdAlbums []string
	var renamedAlbums []string
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
			_ = json.NewEncoder(w).Encode(immich.Album{ID: "album-direct-123", AlbumName: name})
			return
		}
		if r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/api/albums/") {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			renamedAlbums = append(renamedAlbums, body["albumName"])
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/assets") {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/original") {
			w.Header().Set("Content-Type", "video/mp4")
			w.Write([]byte("fake video content"))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/assets" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "new-asset-direct"}`))
			return
		}
		if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/assets/") {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	fakeFFmpeg := filepath.Join(tempHome, "fake_ffmpeg")
	script := `#!/bin/sh
if [ "$1" = "-i" ] || [ "$2" = "-i" ] || [ "$3" = "-i" ]; then
    echo "Stream #0:0: Video: h264, 1920x1080"
    last="${@: -1}"
    if [ "$last" != "" ]; then
        echo "fake transcoded" > "$last"
    fi
fi
exit 0
`
	_ = os.WriteFile(fakeFFmpeg, []byte(script), 0755)

	fakeExifTool := filepath.Join(tempHome, "fake_exiftool")
	_ = os.WriteFile(fakeExifTool, []byte("#!/bin/sh\nexit 0\n"), 0755)

	cfg := &config.Config{
		URL:           ts.URL,
		APIKey:        "test-key",
		SizeThreshold: "0",
		SkipHEVC:      false,
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
			ID:               "asset-direct-1",
			OriginalFileName: "video_direct.mp4",
			Size:             5000,
			ExifInfo: immich.ExifInfo{
				FileSizeInByte:  5000,
				ExifImageWidth:  1920,
				ExifImageHeight: 1080,
			},
		},
	}

	ch := make(chan interface{}, 200)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range ch {
		}
	}()
	p.Run(context.Background(), videos, ch)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	// Direct run creates InProgress album
	if len(createdAlbums) != 1 || !strings.HasPrefix(createdAlbums[0], "Immich-Optimizer-InProgress-") {
		t.Fatalf("Expected 1 InProgress album created, got: %v", createdAlbums)
	}
	// On completion, renamed to Completed
	if len(renamedAlbums) != 1 || !strings.HasPrefix(renamedAlbums[0], "Immich-Optimizer-Completed-") {
		t.Fatalf("Expected 1 Completed album rename, got: %v", renamedAlbums)
	}
}

func TestPipeline_CancelledRunDoesNotRenameToCompleted(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	var renamedAlbums []string
	var mu sync.Mutex

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if r.Method == http.MethodPost && r.URL.Path == "/api/albums" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(immich.Album{ID: "album-cancel-123", AlbumName: "Immich-Optimizer-InProgress-test"})
			return
		}
		if r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/api/albums/") {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			renamedAlbums = append(renamedAlbums, body["albumName"])
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	p, err := NewPipelineWithBinaries(&config.Config{
		URL:           ts.URL,
		APIKey:        "test-key",
		SizeThreshold: "0",
		SkipHEVC:      false,
	}, &engine.BinaryManager{})
	if err != nil {
		t.Fatalf("Failed to initialize pipeline: %v", err)
	}
	p.Client().SetHTTPClient(&http.Client{
		Transport: &testRoundTripper{handler: ts.Config.Handler},
	})

	videos := []immich.Asset{
		{
			ID:               "asset-cancel-1",
			OriginalFileName: "video_cancel.mp4",
			Size:             5000,
		},
	}

	ch := make(chan interface{}, 200)
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

	// Should NOT have renamed to Completed
	for _, name := range renamedAlbums {
		if strings.HasPrefix(name, "Immich-Optimizer-Completed-") {
			t.Errorf("Cancelled pipeline should NOT rename album to Completed, got: %s", name)
		}
	}
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

func TestPipeline_PreservesAlbumsAndFavorite(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	var mu sync.Mutex
	var orderOfEvents []string
	var albumsAddedTo = make(map[string][]string)
	favoriteUpdated := false
	deletedIDs := make([]string, 0)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		// 1. Download original video
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/original") {
			w.Header().Set("Content-Type", "video/mp4")
			w.Write([]byte("original-test-video-content"))
			return
		}

		// 2. Query original asset's albums
		if r.Method == http.MethodGet && r.URL.Path == "/api/albums" {
			assetID := r.URL.Query().Get("assetId")
			if assetID == "asset-orig" {
				orderOfEvents = append(orderOfEvents, "query-albums")
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode([]immich.Album{
					{ID: "album-vacation", AlbumName: "Vacation"},
					{ID: "album-family", AlbumName: "Family"},
				})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
			return
		}

		if r.Method == http.MethodGet && r.URL.Path == "/api/assets/asset-orig" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(immich.Asset{ID: "asset-orig", Type: "VIDEO", IsFavorite: true})
			return
		}

		// 3. Create tracking album
		if r.Method == http.MethodPost && r.URL.Path == "/api/albums" {
			orderOfEvents = append(orderOfEvents, "create-tracking-album")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(immich.Album{ID: "album-tracking-99", AlbumName: "Immich-Optimizer-InProgress"})
			return
		}

		// 4. Upload replacement asset
		if r.Method == http.MethodPost && r.URL.Path == "/api/assets" {
			orderOfEvents = append(orderOfEvents, "upload-replacement")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(immich.Asset{ID: "asset-replacement-888", Type: "VIDEO"})
			return
		}

		// 5. Add asset to album (tracking album or user albums)
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/assets") && strings.HasPrefix(r.URL.Path, "/api/albums/") {
			albumID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/albums/"), "/assets")
			var payload struct {
				IDs []string `json:"ids"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			albumsAddedTo[albumID] = append(albumsAddedTo[albumID], payload.IDs...)
			orderOfEvents = append(orderOfEvents, "add-to-album:"+albumID)
			w.WriteHeader(http.StatusOK)
			return
		}

		// 6. Set favorite on replacement asset
		if r.Method == http.MethodPut && r.URL.Path == "/api/assets/asset-replacement-888" {
			var payload struct {
				IsFavorite bool `json:"isFavorite"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload.IsFavorite {
				favoriteUpdated = true
				orderOfEvents = append(orderOfEvents, "set-favorite")
			}
			w.WriteHeader(http.StatusOK)
			return
		}

		// 7. Delete original asset
		if r.Method == http.MethodDelete && r.URL.Path == "/api/assets" {
			var payload struct {
				IDs []string `json:"ids"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			deletedIDs = append(deletedIDs, payload.IDs...)
			orderOfEvents = append(orderOfEvents, "delete-original")
			w.WriteHeader(http.StatusOK)
			return
		}

		// 8. Rename album on finish
		if r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/api/albums/") {
			w.WriteHeader(http.StatusOK)
			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	fakeFFmpeg := filepath.Join(tempHome, "fake_ffmpeg")
	script := `#!/bin/sh
for arg do last_arg="$arg"; done
echo 'small' > "$last_arg"
exit 0
`
	if err := os.WriteFile(fakeFFmpeg, []byte(script), 0755); err != nil {
		t.Fatalf("Failed to write fake ffmpeg: %v", err)
	}

	fakeExifTool := filepath.Join(tempHome, "fake_exiftool")
	if err := os.WriteFile(fakeExifTool, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("Failed to write fake exiftool: %v", err)
	}

	cfg := &config.Config{
		URL:                ts.URL,
		APIKey:             "test-key",
		SkipHEVC:           false,
		ConcurrentEncoders: 1,
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

	videos := []immich.Asset{
		{
			ID:               "asset-orig",
			OriginalFileName: "orig.mp4",
			Type:             "VIDEO",
			Size:             10000,
			IsFavorite:       true,
		},
	}

	ch := make(chan interface{}, 20)
	var completeErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for msg := range ch {
			if vMsg, ok := msg.(PipelineVideoCompleteMsg); ok && vMsg.Err != nil {
				completeErr = vMsg.Err
			}
		}
	}()

	p.Run(context.Background(), videos, ch)
	wg.Wait()

	if completeErr != nil {
		t.Fatalf("Pipeline failed unexpectedly: %v", completeErr)
	}

	mu.Lock()
	defer mu.Unlock()

	// Verify replacement asset was added to user albums
	if ids, ok := albumsAddedTo["album-vacation"]; !ok || len(ids) == 0 || ids[len(ids)-1] != "asset-replacement-888" {
		t.Errorf("Expected asset-replacement-888 to be added to album-vacation, got %v", albumsAddedTo["album-vacation"])
	}
	if ids, ok := albumsAddedTo["album-family"]; !ok || len(ids) == 0 || ids[len(ids)-1] != "asset-replacement-888" {
		t.Errorf("Expected asset-replacement-888 to be added to album-family, got %v", albumsAddedTo["album-family"])
	}

	// Verify replacement asset was added to tracking album
	if ids, ok := albumsAddedTo["album-tracking-99"]; !ok || len(ids) == 0 || ids[len(ids)-1] != "asset-replacement-888" {
		t.Errorf("Expected asset-replacement-888 to be added to album-tracking-99, got %v", albumsAddedTo["album-tracking-99"])
	}

	// Verify favorite status was updated on replacement asset
	if !favoriteUpdated {
		t.Errorf("Expected favorite status to be transferred to replacement asset")
	}

	// Verify original asset was deleted
	if len(deletedIDs) == 0 || deletedIDs[0] != "asset-orig" {
		t.Errorf("Expected asset-orig to be deleted, got %v", deletedIDs)
	}

	// Verify order of operations:
	// query-albums -> upload-replacement -> add-to-album -> set-favorite -> delete-original
	var uploadIdx, addVacationIdx, addFamilyIdx, favIdx, deleteIdx int = -1, -1, -1, -1, -1
	for i, ev := range orderOfEvents {
		switch ev {
		case "upload-replacement":
			uploadIdx = i
		case "add-to-album:album-vacation":
			addVacationIdx = i
		case "add-to-album:album-family":
			addFamilyIdx = i
		case "set-favorite":
			favIdx = i
		case "delete-original":
			deleteIdx = i
		}
	}

	if uploadIdx == -1 || addVacationIdx == -1 || addFamilyIdx == -1 || favIdx == -1 || deleteIdx == -1 {
		t.Fatalf("Missing events in orderOfEvents: %v", orderOfEvents)
	}

	if !(uploadIdx < addVacationIdx && uploadIdx < addFamilyIdx && uploadIdx < favIdx) {
		t.Errorf("Album additions and favorite setting must occur after upload. Order: %v", orderOfEvents)
	}
	if !(addVacationIdx < deleteIdx && addFamilyIdx < deleteIdx && favIdx < deleteIdx) {
		t.Errorf("Delete must occur strictly AFTER album additions and favorite transfer! Order: %v", orderOfEvents)
	}
}

func TestPipeline_AlbumTransferFailureAbortsDeletion(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	var deleteCalled bool
	var mu sync.Mutex

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/original") {
			w.Header().Set("Content-Type", "video/mp4")
			w.Write([]byte("fake video content"))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/albums" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]immich.Album{
				{ID: "album-broken", AlbumName: "Broken Album"},
			})
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/albums" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(immich.Album{ID: "album-tracking-fail", AlbumName: "Immich-Optimizer"})
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/assets" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(immich.Asset{ID: "asset-replacement-123", Type: "VIDEO"})
			return
		}
		// Failure when adding to album-broken
		if r.Method == http.MethodPut && r.URL.Path == "/api/albums/album-broken/assets" {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"message": "failed to add to album"}`))
			return
		}
		if r.Method == http.MethodDelete && r.URL.Path == "/api/assets" {
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "asset-fail-album") {
				deleteCalled = true
			}
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
		URL:                ts.URL,
		APIKey:             "test-key",
		ConcurrentEncoders: 1,
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

	videos := []immich.Asset{
		{
			ID:               "asset-fail-album",
			OriginalFileName: "fail.mp4",
			Type:             "VIDEO",
			Size:             5000,
		},
	}

	ch := make(chan interface{}, 20)
	var gotError error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for msg := range ch {
			if vMsg, ok := msg.(PipelineVideoCompleteMsg); ok && vMsg.Err != nil {
				gotError = vMsg.Err
			}
		}
	}()

	p.Run(context.Background(), videos, ch)
	wg.Wait()

	if gotError == nil {
		t.Fatalf("Expected pipeline to report error when album transfer fails")
	}

	mu.Lock()
	defer mu.Unlock()

	if deleteCalled {
		t.Errorf("CRITICAL SAFETY BUG: original asset was deleted even though album transfer failed!")
	}
}

func TestPipeline_FavoriteTransferFailureAbortsDeletion(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	var deleteCalled bool
	var mu sync.Mutex

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/original") {
			w.Header().Set("Content-Type", "video/mp4")
			w.Write([]byte("fake video content"))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/albums" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/albums" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(immich.Album{ID: "album-tracking-fav", AlbumName: "Immich-Optimizer"})
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/assets" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(immich.Asset{ID: "asset-replacement-fav", Type: "VIDEO"})
			return
		}
		// Failure when setting favorite status
		if r.Method == http.MethodPut && r.URL.Path == "/api/assets/asset-replacement-fav" {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"message": "failed to favorite asset"}`))
			return
		}
		if r.Method == http.MethodDelete && r.URL.Path == "/api/assets" {
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "asset-fav-fail") {
				deleteCalled = true
			}
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
		URL:                ts.URL,
		APIKey:             "test-key",
		ConcurrentEncoders: 1,
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

	videos := []immich.Asset{
		{
			ID:               "asset-fav-fail",
			OriginalFileName: "favfail.mp4",
			Type:             "VIDEO",
			Size:             5000,
			IsFavorite:       true,
		},
	}

	ch := make(chan interface{}, 20)
	var gotError error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for msg := range ch {
			if vMsg, ok := msg.(PipelineVideoCompleteMsg); ok && vMsg.Err != nil {
				gotError = vMsg.Err
			}
		}
	}()

	p.Run(context.Background(), videos, ch)
	wg.Wait()

	if gotError == nil {
		t.Fatalf("Expected pipeline to report error when favorite transfer fails")
	}

	mu.Lock()
	defer mu.Unlock()

	if deleteCalled {
		t.Errorf("CRITICAL SAFETY BUG: original asset was deleted even though favorite transfer failed!")
	}
}

func TestPipeline_UnfavoritedAsset_DoesNotSetFavorite(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	var setFavoriteCalled bool
	var mu sync.Mutex

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/original") {
			w.Header().Set("Content-Type", "video/mp4")
			w.Write([]byte("fake video content"))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/albums" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/assets/asset-unfav" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(immich.Asset{ID: "asset-unfav", Type: "VIDEO", IsFavorite: false})
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/albums" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(immich.Album{ID: "album-tracking-unfav", AlbumName: "Immich-Optimizer"})
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/assets" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(immich.Asset{ID: "asset-replacement-unfav", Type: "VIDEO"})
			return
		}
		if r.Method == http.MethodPut && r.URL.Path == "/api/assets/asset-replacement-unfav" {
			setFavoriteCalled = true
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodDelete && r.URL.Path == "/api/assets" {
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
		URL:                ts.URL,
		APIKey:             "test-key",
		ConcurrentEncoders: 1,
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

	videos := []immich.Asset{
		{
			ID:               "asset-unfav",
			OriginalFileName: "unfav.mp4",
			Type:             "VIDEO",
			Size:             5000,
			IsFavorite:       false,
		},
	}

	ch := make(chan interface{}, 20)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range ch {
		}
	}()

	p.Run(context.Background(), videos, ch)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	if setFavoriteCalled {
		t.Errorf("Expected SetAssetFavorite NOT to be called for unfavorited asset")
	}
}

func TestPipeline_FavoriteDiscoveredViaAssetInfo(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	var setFavoriteCalled bool
	var favoriteAssetID string
	var deleteCalled bool
	var mu sync.Mutex

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/original") {
			w.Header().Set("Content-Type", "video/mp4")
			w.Write([]byte("fake video content"))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/albums" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
			return
		}
		// Asset info query: search metadata lacked isFavorite (false), but server says true!
		if r.Method == http.MethodGet && r.URL.Path == "/api/assets/asset-meta-missing-fav" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(immich.Asset{
				ID:         "asset-meta-missing-fav",
				Type:       "VIDEO",
				IsFavorite: true,
			})
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/albums" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(immich.Album{ID: "album-tracking-fav-info", AlbumName: "Immich-Optimizer"})
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/assets" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(immich.Asset{ID: "asset-replacement-meta-fav", Type: "VIDEO"})
			return
		}
		if r.Method == http.MethodPut && r.URL.Path == "/api/assets/asset-replacement-meta-fav" {
			var payload struct {
				IsFavorite bool `json:"isFavorite"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload.IsFavorite {
				setFavoriteCalled = true
				favoriteAssetID = "asset-replacement-meta-fav"
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodDelete && r.URL.Path == "/api/assets" {
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
		URL:                ts.URL,
		APIKey:             "test-key",
		ConcurrentEncoders: 1,
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

	// Asset has IsFavorite: false in input (e.g. search metadata didn't populate it)
	videos := []immich.Asset{
		{
			ID:               "asset-meta-missing-fav",
			OriginalFileName: "missingfav.mp4",
			Type:             "VIDEO",
			Size:             5000,
			IsFavorite:       false,
		},
	}

	ch := make(chan interface{}, 20)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range ch {
		}
	}()

	p.Run(context.Background(), videos, ch)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	if !setFavoriteCalled || favoriteAssetID != "asset-replacement-meta-fav" {
		t.Errorf("Expected SetAssetFavorite to be called for replacement asset after querying asset info, called=%v, asset=%s", setFavoriteCalled, favoriteAssetID)
	}
	if !deleteCalled {
		t.Errorf("Expected original asset to be deleted after successful favorite transfer")
	}
}

func TestPipeline_TagsReplacementAssetWhenExcludeOptimized(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	var mu sync.Mutex
	var tagCreated bool
	var assetTagged bool
	var taggedAssetID string
	var taggedTagID string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/original") {
			w.Header().Set("Content-Type", "video/mp4")
			w.Write([]byte("fake-video-data"))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/assets/asset-orig-to-tag" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(immich.Asset{ID: "asset-orig-to-tag", Type: "VIDEO"})
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/albums" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/albums" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(immich.Album{ID: "album-tag-test", AlbumName: "Immich-Optimizer-InProgress"})
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/assets" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(immich.Asset{ID: "new-tagged-asset-id", Type: "VIDEO"})
			return
		}
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/assets") && strings.HasPrefix(r.URL.Path, "/api/albums/") {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodDelete && r.URL.Path == "/api/assets" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/api/albums/") {
			w.WriteHeader(http.StatusOK)
			return
		}
		// Tag endpoints
		if r.Method == http.MethodGet && r.URL.Path == "/api/tags" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/tags" {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["name"] == "immich-optimizer" {
				tagCreated = true
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "tag-opt-123", "name": "immich-optimizer"}`))
			return
		}
		if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/tags/") && strings.HasSuffix(r.URL.Path, "/assets") {
			taggedTagID = strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/tags/"), "/assets")
			var body struct {
				IDs []string `json:"ids"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body.IDs) > 0 {
				taggedAssetID = body.IDs[0]
				assetTagged = true
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"id":"new-tagged-asset-id","success":true}]`))
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
		URL:              ts.URL,
		APIKey:           "test-key",
		SkipHEVC:         false,
		ExcludeOptimized: true,
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

	videos := []immich.Asset{
		{
			ID:               "asset-orig-to-tag",
			OriginalFileName: "tag_me.mp4",
			Type:             "VIDEO",
			Size:             5000,
		},
	}

	ch := make(chan interface{}, 20)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range ch {
		}
	}()

	p.Run(context.Background(), videos, ch)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	if !tagCreated {
		t.Errorf("Expected 'immich-optimizer' tag to be created")
	}
	if !assetTagged || taggedAssetID != "new-tagged-asset-id" || taggedTagID != "tag-opt-123" {
		t.Errorf("Expected replacement asset to be tagged: assetTagged=%v, assetID=%s, tagID=%s", assetTagged, taggedAssetID, taggedTagID)
	}
}

func TestRollbackReplacementOnAlbumTransferFailure(t *testing.T) {
	var mu sync.Mutex
	var replacementUploaded bool
	var replacementRolledBack bool

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if r.Method == "GET" && r.URL.Path == "/api/assets/asset-rollback/original" {
			w.Write([]byte("fake video bytes"))
			return
		}
		if r.Method == "GET" && r.URL.Path == "/api/assets/asset-rollback" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "asset-rollback", "type": "VIDEO"}`))
			return
		}
		if r.Method == "POST" && r.URL.Path == "/api/assets" {
			replacementUploaded = true
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "new-asset-leak", "originalFileName": "test.mp4"}`))
			return
		}
		if r.Method == "GET" && r.URL.Path == "/api/albums" {
			// Asset belongs to an existing album
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"id": "user-album-1", "albumName": "Summer Vacation"}]`))
			return
		}
		if r.Method == "PUT" && r.URL.Path == "/api/albums/user-album-1/assets" {
			// Album transfer fails!
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if r.Method == "DELETE" && r.URL.Path == "/api/assets" {
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "new-asset-leak") {
				replacementRolledBack = true
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	tempDir := t.TempDir()
	fakeFFmpeg := filepath.Join(tempDir, "fake_ffmpeg")
	if err := os.WriteFile(fakeFFmpeg, []byte("#!/bin/sh\nfor arg do last_arg=\"$arg\"; done\necho 'transcoded' > \"$last_arg\"\nexit 0\n"), 0755); err != nil {
		t.Fatalf("Failed to create fake ffmpeg: %v", err)
	}
	fakeExifTool := filepath.Join(tempDir, "exiftool")
	if err := os.WriteFile(fakeExifTool, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("Failed to create fake exiftool: %v", err)
	}

	cfg := &config.Config{
		URL:        ts.URL,
		APIKey:     "test-key",
		SizeGuard:  false,
		SkipHEVC:   false,
		Preset:     "fast",
		Resolution: "Original",
	}

	p, err := NewPipelineWithBinaries(cfg, &engine.BinaryManager{
		FFmpegPath:   fakeFFmpeg,
		ExifToolPath: fakeExifTool,
	})
	if err != nil {
		t.Fatalf("Failed to create pipeline: %v", err)
	}
	p.Client().SetHTTPClient(&http.Client{
		Transport: &testRoundTripper{handler: ts.Config.Handler},
	})

	videos := []immich.Asset{
		{
			ID:               "asset-rollback",
			OriginalFileName: "test.mp4",
			Type:             "VIDEO",
			Size:             5000,
		},
	}

	ch := make(chan interface{}, 20)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range ch {
		}
	}()

	p.Run(context.Background(), videos, ch)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	if !replacementUploaded {
		t.Fatalf("Expected replacement to be uploaded first")
	}
	if !replacementRolledBack {
		t.Errorf("Expected replacement asset 'new-asset-leak' to be rolled back via DELETE /api/assets when album transfer failed")
	}
}

func TestRollbackReplacementOnDeleteOriginalFailure(t *testing.T) {
	var mu sync.Mutex
	var replacementRolledBack bool

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if r.Method == "GET" && r.URL.Path == "/api/assets/asset-delete-fail/original" {
			w.Write([]byte("fake video bytes"))
			return
		}
		if r.Method == "GET" && r.URL.Path == "/api/assets/asset-delete-fail" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "asset-delete-fail", "type": "VIDEO"}`))
			return
		}
		if r.Method == "POST" && r.URL.Path == "/api/assets" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "new-asset-to-delete", "originalFileName": "test.mp4"}`))
			return
		}
		if r.Method == "GET" && r.URL.Path == "/api/albums" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[]`))
			return
		}
		if r.Method == "DELETE" && r.URL.Path == "/api/assets" {
			body, _ := io.ReadAll(r.Body)
			// Fail deletion of original asset
			if strings.Contains(string(body), "asset-delete-fail") {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			// Succeed deletion of replacement rollback
			if strings.Contains(string(body), "new-asset-to-delete") {
				replacementRolledBack = true
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	tempDir := t.TempDir()
	fakeFFmpeg := filepath.Join(tempDir, "fake_ffmpeg")
	if err := os.WriteFile(fakeFFmpeg, []byte("#!/bin/sh\nfor arg do last_arg=\"$arg\"; done\necho 'transcoded' > \"$last_arg\"\nexit 0\n"), 0755); err != nil {
		t.Fatalf("Failed to create fake ffmpeg: %v", err)
	}
	fakeExifTool := filepath.Join(tempDir, "exiftool")
	if err := os.WriteFile(fakeExifTool, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("Failed to create fake exiftool: %v", err)
	}

	cfg := &config.Config{
		URL:        ts.URL,
		APIKey:     "test-key",
		SizeGuard:  false,
		SkipHEVC:   false,
		Preset:     "fast",
		Resolution: "Original",
	}

	p, err := NewPipelineWithBinaries(cfg, &engine.BinaryManager{
		FFmpegPath:   fakeFFmpeg,
		ExifToolPath: fakeExifTool,
	})
	if err != nil {
		t.Fatalf("Failed to create pipeline: %v", err)
	}
	p.Client().SetHTTPClient(&http.Client{
		Transport: &testRoundTripper{handler: ts.Config.Handler},
	})

	videos := []immich.Asset{
		{
			ID:               "asset-delete-fail",
			OriginalFileName: "test.mp4",
			Type:             "VIDEO",
			Size:             5000,
		},
	}

	ch := make(chan interface{}, 20)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range ch {
		}
	}()

	p.Run(context.Background(), videos, ch)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	if !replacementRolledBack {
		t.Errorf("Expected replacement asset 'new-asset-to-delete' to be rolled back when original deletion failed")
	}
}

func TestPipeline_ScanVideosWithDateFilter(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/api/search/metadata" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"assets": {
					"items": [
						{"id": "v-1", "type": "VIDEO", "size": 1000, "fileCreatedAt": "2023-05-01T12:00:00Z"},
						{"id": "v-2", "type": "VIDEO", "size": 1000, "fileCreatedAt": "2024-05-01T12:00:00Z"}
					]
				}
			}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	cfg := &config.Config{
		URL:        ts.URL,
		APIKey:     "test-key",
		DateAfter:  "2024-01-01",
		DateBefore: "2024-12-31",
	}
	p, err := NewPipelineWithBinaries(cfg, &engine.BinaryManager{})
	if err != nil {
		t.Fatalf("NewPipelineWithBinaries failed: %v", err)
	}
	p.Client().SetHTTPClient(&http.Client{
		Transport: &testRoundTripper{handler: ts.Config.Handler},
	})

	res, err := p.ScanVideosWithContext(context.Background(), "Disabled", 0)
	if err != nil {
		t.Fatalf("ScanVideosWithContext failed: %v", err)
	}

	if res.SkippedDateFilter != 1 {
		t.Errorf("expected 1 video skipped by date filter, got %d", res.SkippedDateFilter)
	}
	if len(res.Videos) != 1 || res.Videos[0].ID != "v-2" {
		t.Fatalf("expected only v-2, got %+v", res.Videos)
	}
}
