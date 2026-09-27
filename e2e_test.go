package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"immich-video-optimizer/config"
	"immich-video-optimizer/immich"
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

// TestE2EWorkflow tests the full end-to-end integration between configuration,
// Immich mock API server, asset discovery, album creation, download, upload, and deletion.
func TestE2EWorkflow(t *testing.T) {
	// 1. Mock Immich Server
	var uploadedAssetID string
	var deletedAssetID string
	var addedAlbumID string
	var addedAssetIDs []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify auth header
		if r.Header.Get("x-api-key") != "secret-api-key-xyz" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/search/metadata":
			// Return one large video (>500MB) and one small video (<500MB)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"assets": {
					"items": [
						{
							"id": "asset-large-1",
							"type": "VIDEO",
							"size": 1048576000,
							"deviceAssetId": "dev-asset-1",
							"deviceId": "iPhone-15",
							"fileCreatedAt": "2026-09-01T12:00:00.000Z"
						},
						{
							"id": "asset-small-2",
							"type": "VIDEO",
							"size": 52428800,
							"deviceAssetId": "dev-asset-2",
							"deviceId": "iPhone-15",
							"fileCreatedAt": "2026-09-02T12:00:00.000Z"
						}
					]
				}
			}`))

		case r.Method == http.MethodGet && r.URL.Path == "/api/users/me":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "user-1", "email": "test@immich.app"}`))

		case r.Method == http.MethodPost && r.URL.Path == "/api/albums":
			// Create tracking album
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "tracking-album-123", "albumName": "Immich Optimizer - In Progress"}`))

		case r.Method == http.MethodPut && r.URL.Path == "/api/albums/tracking-album-123/assets":
			// Add assets to tracking album
			addedAlbumID = "tracking-album-123"
			body, _ := io.ReadAll(r.Body)
			addedAssetIDs = append(addedAssetIDs, string(body))
			w.WriteHeader(http.StatusOK)

		case r.Method == http.MethodGet && r.URL.Path == "/api/assets/asset-large-1/original":
			// Download original video stream
			w.Header().Set("Content-Type", "video/mp4")
			w.Write([]byte("SIMULATED_LARGE_ORIGINAL_VIDEO_DATA_STREAM"))

		case r.Method == http.MethodPost && r.URL.Path == "/api/assets":
			// Upload replacement asset
			err := r.ParseMultipartForm(10 << 20)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			uploadedAssetID = "asset-optimized-replacement-1"
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id": "asset-optimized-replacement-1", "type": "VIDEO", "size": 250000000}`))

		case r.Method == http.MethodDelete && r.URL.Path == "/api/assets":
			// Delete original asset
			deletedAssetID = "asset-large-1"
			w.WriteHeader(http.StatusOK)

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// 2. Setup isolated Config directory
	tempDir := t.TempDir()

	// Save test config
	cfg := &config.Config{
		URL:           server.URL,
		APIKey:        "secret-api-key-xyz",
		SizeThreshold: "500", // 500 MB
	}

	// Verify saving & loading config
	os.Setenv("HOME", tempDir)
	defer os.Unsetenv("HOME")

	err := config.SaveConfig(cfg)
	if err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	loadedCfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	if loadedCfg.URL != server.URL || loadedCfg.APIKey != cfg.APIKey || loadedCfg.SizeThreshold != "500" {
		t.Fatalf("Loaded config does not match: %+v", loadedCfg)
	}

	// 3. Immich Client Integration Workflow
	client := immich.NewClient(loadedCfg.URL, loadedCfg.APIKey)
	client.SetHTTPClient(&http.Client{
		Transport: &testRoundTripper{handler: server.Config.Handler},
	})
	client.SetTimeout(5 * time.Second)

	// Step A: Query large videos exceeding 500 MB (500 * 1024 * 1024 bytes)
	thresholdBytes := int64(500 * 1024 * 1024)
	videos, err := client.QueryVideoAssets(thresholdBytes)
	if err != nil {
		t.Fatalf("QueryVideoAssets failed: %v", err)
	}
	if len(videos) != 1 || videos[0].ID != "asset-large-1" {
		t.Fatalf("Expected 1 large video with ID 'asset-large-1', got: %+v", videos)
	}

	// Step B: Create tracking album
	album, err := client.CreateTrackingAlbum("Immich Optimizer - In Progress")
	if err != nil {
		t.Fatalf("CreateTrackingAlbum failed: %v", err)
	}
	if album.ID != "tracking-album-123" {
		t.Fatalf("Unexpected album ID: %s", album.ID)
	}

	// Step C: Add large video to tracking album
	err = client.AddAssetsToAlbum(album.ID, []string{videos[0].ID})
	if err != nil {
		t.Fatalf("AddAssetsToAlbum failed: %v", err)
	}
	if addedAlbumID != "tracking-album-123" {
		t.Fatalf("Album ID mismatch in AddAssetsToAlbum")
	}

	// Step D: Download original asset
	downloadPath := filepath.Join(tempDir, "downloaded.mp4")
	err = client.DownloadAsset(videos[0].ID, downloadPath)
	if err != nil {
		t.Fatalf("DownloadAsset failed: %v", err)
	}
	data, err := os.ReadFile(downloadPath)
	if err != nil || string(data) != "SIMULATED_LARGE_ORIGINAL_VIDEO_DATA_STREAM" {
		t.Fatalf("Downloaded content invalid: %s", string(data))
	}

	// Step E: Simulate optimized video file and upload
	optimizedPath := filepath.Join(tempDir, "optimized.mp4")
	err = os.WriteFile(optimizedPath, []byte("SIMULATED_OPTIMIZED_HEVC_DATA"), 0644)
	if err != nil {
		t.Fatalf("Failed to create simulated optimized file: %v", err)
	}

	newAsset, err := client.UploadReplacementAsset(optimizedPath, videos[0])
	if err != nil {
		t.Fatalf("UploadReplacementAsset failed: %v", err)
	}
	if newAsset.ID != "asset-optimized-replacement-1" || uploadedAssetID != "asset-optimized-replacement-1" {
		t.Fatalf("Unexpected uploaded asset ID: %s, server got: %s", newAsset.ID, uploadedAssetID)
	}

	// Step F: Delete original asset
	err = client.DeleteAsset(videos[0].ID)
	if err != nil {
		t.Fatalf("DeleteAsset failed: %v", err)
	}
	if deletedAssetID != "asset-large-1" {
		t.Fatalf("Expected deleted asset 'asset-large-1', got: %s", deletedAssetID)
	}

	fmt.Println("✅ Complete End-to-End Workflow Test Passed Successfully!")
}
