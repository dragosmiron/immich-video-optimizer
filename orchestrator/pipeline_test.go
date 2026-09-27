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
)

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

