package immich

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func newTestClient(server *httptest.Server, apiKey string) *Client {
	c := NewClient(server.URL, apiKey)
	c.SetHTTPClient(&http.Client{
		Transport: &testRoundTripper{handler: server.Config.Handler},
	})
	return c
}

func TestNewClient(t *testing.T) {
	c := NewClient("http://localhost", "dummy-key")
	if c.baseURL != "http://localhost" {
		t.Errorf("Expected baseURL http://localhost, got %s", c.baseURL)
	}
}

func TestQueryVideoAssets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/api/search/metadata" {
			t.Errorf("Expected path /api/search/metadata, got %s", r.URL.Path)
		}

		var reqBody map[string]interface{}
		json.NewDecoder(r.Body).Decode(&reqBody)

		if reqBody["withExif"] != true {
			t.Errorf("Expected withExif=true, got %v", reqBody["withExif"])
		}

		var assets []Asset
		if reqBody["page"].(float64) == 1 {
			assets = []Asset{
				{ID: "1", Type: "VIDEO", Size: 0, ExifInfo: ExifInfo{FileSizeInByte: 1000}},
				{ID: "2", Type: "IMAGE", Size: 2000},
				{ID: "3", Type: "VIDEO", Size: 500},
			}
		} else {
			assets = []Asset{}
		}

		resp := map[string]interface{}{
			"assets": map[string]interface{}{
				"items": assets,
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := newTestClient(server, "secret")

	// Test success
	videos, err := client.QueryVideoAssets(800)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(videos) != 1 || videos[0].ID != "1" {
		t.Errorf("Expected 1 video with ID '1', got %v", videos)
	}

	// Test unauthorized
	badClient := newTestClient(server, "wrong")
	_, err = badClient.QueryVideoAssets(800)
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.StatusCode != 401 {
		t.Errorf("Expected APIError with 401, got %v", err)
	}
}

func TestCreateTrackingAlbum(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Expected POST, got %s", r.Method)
		}
		var req map[string]string
		json.NewDecoder(r.Body).Decode(&req)
		if req["albumName"] != "Test Album" {
			t.Errorf("Expected 'Test Album', got %s", req["albumName"])
		}

		album := Album{ID: "album-1", AlbumName: "Test Album"}
		json.NewEncoder(w).Encode(album)
	}))
	defer server.Close()

	client := newTestClient(server, "secret")
	album, err := client.CreateTrackingAlbum("Test Album")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if album.ID != "album-1" {
		t.Errorf("Expected 'album-1', got %s", album.ID)
	}
}

func TestAddAssetsToAlbum(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("Expected PUT, got %s", r.Method)
		}
		if r.URL.Path != "/api/albums/album-1/assets" {
			t.Errorf("Expected /api/albums/album-1/assets, got %s", r.URL.Path)
		}
		var req map[string][]string
		json.NewDecoder(r.Body).Decode(&req)
		if len(req["ids"]) != 2 || req["ids"][0] != "1" || req["ids"][1] != "2" {
			t.Errorf("Unexpected payload: %v", req)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newTestClient(server, "secret")
	err := client.AddAssetsToAlbum("album-1", []string{"1", "2"})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
}

func TestDeleteAlbum(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("Expected DELETE, got %s", r.Method)
		}
		if r.URL.Path != "/api/albums/album-dryrun-1" {
			t.Errorf("Expected /api/albums/album-dryrun-1, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newTestClient(server, "secret")
	err := client.DeleteAlbum("album-dryrun-1")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Empty albumID should be a no-op
	if err := client.DeleteAlbum(""); err != nil {
		t.Errorf("expected empty albumID to return nil, got: %v", err)
	}
}

func TestDeleteAlbum_404Ignored(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message": "Album not found"}`))
	}))
	defer server.Close()

	client := newTestClient(server, "secret")
	err := client.DeleteAlbum("nonexistent-album")
	if err != nil {
		t.Fatalf("Expected 404 to be ignored (return nil), got: %v", err)
	}
}

func TestUpdateAlbumName(t *testing.T) {
	var receivedMethod string
	var receivedPath string
	var receivedBody map[string]string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&receivedBody)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(Album{ID: "album-123", AlbumName: receivedBody["albumName"]})
	}))
	defer server.Close()

	client := newTestClient(server, "secret")

	// 1. Success case
	err := client.UpdateAlbumName("album-123", "Immich-Optimizer-InProgress-20261014")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if receivedMethod != http.MethodPatch {
		t.Errorf("Expected PATCH, got %s", receivedMethod)
	}
	if receivedPath != "/api/albums/album-123" {
		t.Errorf("Expected /api/albums/album-123, got %s", receivedPath)
	}
	if receivedBody["albumName"] != "Immich-Optimizer-InProgress-20261014" {
		t.Errorf("Expected albumName 'Immich-Optimizer-InProgress-20261014', got '%s'", receivedBody["albumName"])
	}

	// 2. Empty albumID is a no-op
	if err := client.UpdateAlbumName("", "New Name"); err != nil {
		t.Errorf("Expected nil error on empty albumID, got: %v", err)
	}
}

func TestUpdateAlbumName_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"message": "Album not editable"}`))
	}))
	defer server.Close()

	client := newTestClient(server, "secret")
	err := client.UpdateAlbumName("album-123", "New Name")
	if err == nil {
		t.Fatalf("Expected error on 400 Bad Request, got nil")
	}
}

func TestDownloadAsset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/api/assets/asset-1/original" {
			t.Errorf("Expected /api/assets/asset-1/original, got %s", r.URL.Path)
		}
		w.Write([]byte("video data"))
	}))
	defer server.Close()

	client := newTestClient(server, "secret")

	tmpFile := filepath.Join(t.TempDir(), "out.mp4")
	err := client.DownloadAsset("asset-1", tmpFile)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	data, _ := os.ReadFile(tmpFile)
	if string(data) != "video data" {
		t.Errorf("Expected 'video data', got %s", string(data))
	}
}

func TestUploadReplacementAsset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Expected POST, got %s", r.Method)
		}

		err := r.ParseMultipartForm(10 << 20)
		if err != nil {
			t.Fatalf("ParseMultipartForm failed: %v", err)
		}

		if !strings.HasPrefix(r.FormValue("deviceAssetId"), "opt-asset-1-") {
			t.Errorf("Expected deviceAssetId starting with 'opt-asset-1-', got %s", r.FormValue("deviceAssetId"))
		}
		if r.FormValue("deviceId") != "immich-video-optimizer" {
			t.Errorf("Expected deviceId=immich-video-optimizer, got %s", r.FormValue("deviceId"))
		}

		file, _, err := r.FormFile("assetData")
		if err != nil {
			t.Fatalf("FormFile failed: %v", err)
		}
		defer file.Close()

		data, _ := io.ReadAll(file)
		if string(data) != "dummy file" {
			t.Errorf("Expected 'dummy file', got %s", string(data))
		}

		newAsset := Asset{ID: "new-1", Type: "VIDEO", Size: 10}
		json.NewEncoder(w).Encode(newAsset)
	}))
	defer server.Close()

	client := newTestClient(server, "secret")

	tmpFile := filepath.Join(t.TempDir(), "dummy.mp4")
	os.WriteFile(tmpFile, []byte("dummy file"), 0644)

	orig := Asset{ID: "asset-1", DeviceAssetID: "dev-1", DeviceID: "dev-id"}
	asset, err := client.UploadReplacementAsset(tmpFile, orig)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if asset.ID != "new-1" {
		t.Errorf("Expected 'new-1', got %s", asset.ID)
	}
}

func TestDeleteAsset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("Expected DELETE, got %s", r.Method)
		}
		var req map[string][]string
		json.NewDecoder(r.Body).Decode(&req)
		if len(req["ids"]) != 1 || req["ids"][0] != "asset-1" {
			t.Errorf("Unexpected payload: %v", req)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newTestClient(server, "secret")
	err := client.DeleteAsset("asset-1")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
}

func TestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newTestClient(server, "secret")
	client.SetTimeout(10 * time.Millisecond)

	_, err := client.QueryVideoAssets(100)
	if err == nil {
		t.Fatal("Expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "network error") {
		t.Errorf("Expected network error, got %v", err)
	}
}

func TestErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer server.Close()

	client := newTestClient(server, "secret")
	err := client.DeleteAsset("asset-1")
	if err == nil {
		t.Fatal("Expected error, got nil")
	}

	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("Expected APIError, got %T", err)
	}
	if apiErr.StatusCode != 500 {
		t.Errorf("Expected 500, got %d", apiErr.StatusCode)
	}
	if apiErr.Message != "internal error" {
		t.Errorf("Expected 'internal error', got %s", apiErr.Message)
	}
	expectedStr := "API error 500: internal error"
	if apiErr.Error() != expectedStr {
		t.Errorf("Expected Error() to return '%s', got '%s'", expectedStr, apiErr.Error())
	}
}

func TestDownloadAsset_RetrySuccess(t *testing.T) {
	origBackoff := retryBackoff
	retryBackoff = func(attempt int) time.Duration { return 5 * time.Millisecond }
	defer func() { retryBackoff = origBackoff }()

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte("gateway timeout"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("video stream data"))
	}))
	defer server.Close()

	client := newTestClient(server, "secret")
	client.SetMaxRetries(3)

	tmpOut := filepath.Join(t.TempDir(), "downloaded.mp4")
	err := client.DownloadAsset("asset-123", tmpOut)
	if err != nil {
		t.Fatalf("expected download to succeed after retries, got %v", err)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}

	data, err := os.ReadFile(tmpOut)
	if err != nil || string(data) != "video stream data" {
		t.Errorf("unexpected downloaded content: %s (err: %v)", string(data), err)
	}
}

func TestDownloadAsset_NoRetryOn404(t *testing.T) {
	origBackoff := retryBackoff
	retryBackoff = func(attempt int) time.Duration { return 5 * time.Millisecond }
	defer func() { retryBackoff = origBackoff }()

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	}))
	defer server.Close()

	client := newTestClient(server, "secret")
	client.SetMaxRetries(3)

	tmpOut := filepath.Join(t.TempDir(), "downloaded.mp4")
	err := client.DownloadAsset("asset-missing", tmpOut)
	if err == nil {
		t.Fatal("expected error on 404, got nil")
	}
	// 404 should abort retries immediately (only 1 attempt)
	if attempts != 1 {
		t.Errorf("expected exactly 1 attempt for 404, got %d", attempts)
	}
}

func TestUploadAsset_RetrySuccess(t *testing.T) {
	origBackoff := retryBackoff
	retryBackoff = func(attempt int) time.Duration { return 5 * time.Millisecond }
	defer func() { retryBackoff = origBackoff }()

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("busy"))
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(Asset{ID: "new-uploaded-asset"})
	}))
	defer server.Close()

	client := newTestClient(server, "secret")
	client.SetMaxRetries(2)

	tmpFile := filepath.Join(t.TempDir(), "upload.mp4")
	os.WriteFile(tmpFile, []byte("transcoded video content"), 0644)

	orig := Asset{ID: "orig-123", OriginalFileName: "sample.mp4"}
	asset, err := client.UploadReplacementAsset(tmpFile, orig)
	if err != nil {
		t.Fatalf("expected upload to succeed on retry, got: %v", err)
	}
	if asset.ID != "new-uploaded-asset" {
		t.Errorf("expected ID 'new-uploaded-asset', got '%s'", asset.ID)
	}
	if attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts)
	}
}

func TestDeleteAsset_RetrySuccess(t *testing.T) {
	origBackoff := retryBackoff
	retryBackoff = func(attempt int) time.Duration { return 5 * time.Millisecond }
	defer func() { retryBackoff = origBackoff }()

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("busy"))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newTestClient(server, "secret")
	client.SetMaxRetries(2)

	err := client.DeleteAssetWithContext(context.Background(), "del-123")
	if err != nil {
		t.Fatalf("expected delete to succeed on retry, got: %v", err)
	}
	if attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts)
	}
}

func TestParseDurationSeconds(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		expected float64
	}{
		{"HH:MM:SS format", `"01:02:03"`, 3723},
		{"HH:MM:SS with fractional seconds", `"0:01:23.456000"`, 83.456},
		{"MM:SS format", `"02:30"`, 150},
		{"MM:SS fractional", `"01:23.500"`, 83.5},
		{"Numeric float in quotes", `"83.456"`, 83.456},
		{"Numeric float unquoted", `83.456`, 83.456},
		{"Numeric integer", `60`, 60},
		{"Numeric integer in quotes", `"60"`, 60},
		{"Milliseconds large integer", `120000`, 120},
		{"Milliseconds with ms suffix", `"15000ms"`, 15},
		{"Seconds with s suffix", `"83.45s"`, 83.45},
		{"Seconds with sec suffix", `"90sec"`, 90},
		{"Comma decimal separator", `"0:01:23,456"`, 83.456},
		{"ISO-8601 PT1H2M3S", `"PT1H2M3S"`, 3723},
		{"ISO-8601 PT1M20S", `"PT1M20S"`, 80},
		{"ISO-8601 PT45.5S", `"PT45.5S"`, 45.5},
		{"ISO-8601 with P0DT0H1M20S", `"P0DT0H1M20S"`, 80},
		{"Null json", `null`, 0},
		{"Empty string", `""`, 0},
		{"Empty bytes", ``, 0},
		{"Invalid format", `"invalid"`, 0},
		{"Negative number", `-50`, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseDurationSeconds(json.RawMessage(tt.raw))
			diff := got - tt.expected
			if diff < -0.001 || diff > 0.001 {
				t.Errorf("ParseDurationSeconds(%s) = %f, expected %f", tt.raw, got, tt.expected)
			}
		})
	}
}

func TestAsset_MBPerMin(t *testing.T) {
	// 1. 600 MB video over 2 minutes (120 seconds) = 300 MB/min
	a1 := Asset{
		Size:     600 * 1024 * 1024,
		Duration: json.RawMessage(`"0:02:00.000"`),
	}
	if diff := a1.MBPerMin() - 300; diff < -0.1 || diff > 0.1 {
		t.Errorf("expected ~300 MB/min, got %f", a1.MBPerMin())
	}

	// 2. 500 MB video over 10 seconds (0.1667 min) = 3000 MB/min
	a2 := Asset{
		Size:     500 * 1024 * 1024,
		Duration: json.RawMessage(`"0:00:10.000"`),
	}
	if diff := a2.MBPerMin() - 3000; diff < -0.1 || diff > 0.1 {
		t.Errorf("expected ~3000 MB/min, got %f", a2.MBPerMin())
	}

	// 3. 500 MB video over 10 minutes = 50 MB/min
	a3 := Asset{
		Size:     500 * 1024 * 1024,
		Duration: json.RawMessage(`"0:10:00.000"`),
	}
	if diff := a3.MBPerMin() - 50; diff < -0.1 || diff > 0.1 {
		t.Errorf("expected ~50 MB/min, got %f", a3.MBPerMin())
	}

	// 4. Missing top-level duration, but ExifInfo has duration (1 minute)
	a4 := Asset{
		Size: 120 * 1024 * 1024,
		ExifInfo: ExifInfo{
			Duration: json.RawMessage(`"0:01:00"`),
		},
	}
	if diff := a4.MBPerMin() - 120; diff < -0.1 || diff > 0.1 {
		t.Errorf("expected ExifInfo fallback ~120 MB/min, got %f", a4.MBPerMin())
	}

	// 5. Missing duration completely fallback (assumes 1 minute) -> 250 MB total = 250 MB/min
	a5 := Asset{
		Size: 250 * 1024 * 1024,
	}
	if diff := a5.MBPerMin() - 250; diff < -0.1 || diff > 0.1 {
		t.Errorf("expected fallback ~250 MB/min, got %f", a5.MBPerMin())
	}
}

func TestScanVideos_MBPerMinFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/search/metadata" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var reqBody map[string]interface{}
		json.NewDecoder(r.Body).Decode(&reqBody)

		var assets []Asset
		if reqBody["page"].(float64) == 1 {
			assets = []Asset{
				// Dense video: 500 MB over 1 minute = 500 MB/min -> should be included
				{ID: "v-dense", Type: "VIDEO", Size: 500 * 1024 * 1024, Duration: json.RawMessage(`"0:01:00"`)},
				// Lean video: 500 MB over 10 minutes = 50 MB/min -> should be skipped below 100 MB/min
				{ID: "v-lean", Type: "VIDEO", Size: 500 * 1024 * 1024, Duration: json.RawMessage(`"0:10:00"`)},
				// Short dense clip: 30 MB over 5 seconds = 360 MB/min -> should be included
				{ID: "v-short", Type: "VIDEO", Size: 30 * 1024 * 1024, Duration: json.RawMessage(`"0:00:05"`)},
				// Photo -> skipped non-video
				{ID: "photo", Type: "IMAGE", Size: 10 * 1024 * 1024},
			}
		} else {
			assets = []Asset{}
		}

		resp := map[string]interface{}{
			"assets": map[string]interface{}{
				"items": assets,
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := newTestClient(server, "secret")

	// Scan with 100 MB/min threshold
	res, err := client.ScanVideos("MB/min", 100.0)
	if err != nil {
		t.Fatalf("ScanVideos failed: %v", err)
	}

	if res.TotalAssetsScanned != 4 {
		t.Errorf("expected 4 scanned, got %d", res.TotalAssetsScanned)
	}
	if res.SkippedNonVideo != 1 {
		t.Errorf("expected 1 non-video skipped, got %d", res.SkippedNonVideo)
	}
	if res.SkippedBelowSize != 1 {
		t.Errorf("expected 1 video skipped below density, got %d", res.SkippedBelowSize)
	}
	if len(res.Videos) != 2 {
		t.Fatalf("expected 2 dense videos included, got %d", len(res.Videos))
	}
	if res.Videos[0].ID != "v-dense" || res.Videos[1].ID != "v-short" {
		t.Errorf("unexpected videos returned: %+v", res.Videos)
	}

	// Scan with Total MB threshold (e.g. 100 MB flat: v-dense (500MB) and v-lean (500MB) included, v-short (30MB) skipped)
	resMB, err := client.ScanVideos("Total MB", 100.0)
	if err != nil {
		t.Fatalf("ScanVideos Total MB failed: %v", err)
	}
	if resMB.TotalAssetsScanned != 4 {
		t.Errorf("expected 4 scanned, got %d", resMB.TotalAssetsScanned)
	}
	if resMB.SkippedNonVideo != 1 {
		t.Errorf("expected 1 non-video skipped, got %d", resMB.SkippedNonVideo)
	}
	if resMB.SkippedBelowSize != 1 {
		t.Errorf("expected 1 video skipped below total size, got %d", resMB.SkippedBelowSize)
	}
	if len(resMB.Videos) != 2 {
		t.Fatalf("expected 2 videos included, got %d", len(resMB.Videos))
	}
	if resMB.Videos[0].ID != "v-dense" || resMB.Videos[1].ID != "v-lean" {
		t.Errorf("unexpected videos returned for Total MB: %+v", resMB.Videos)
	}

	// Scan with Disabled mode (all 3 videos included, 0 skipped below size)
	resDis, err := client.ScanVideos("Disabled", 0)
	if err != nil {
		t.Fatalf("ScanVideos Disabled failed: %v", err)
	}
	if resDis.TotalAssetsScanned != 4 {
		t.Errorf("expected 4 scanned, got %d", resDis.TotalAssetsScanned)
	}
	if resDis.SkippedNonVideo != 1 {
		t.Errorf("expected 1 non-video skipped, got %d", resDis.SkippedNonVideo)
	}
	if resDis.SkippedBelowSize != 0 {
		t.Errorf("expected 0 videos skipped below size when disabled, got %d", resDis.SkippedBelowSize)
	}
	if len(resDis.Videos) != 3 {
		t.Fatalf("expected 3 videos included when disabled, got %d", len(resDis.Videos))
	}

	// Case-insensitive checks
	resDisLower, err := client.ScanVideos("disabled", 0)
	if err != nil || len(resDisLower.Videos) != 3 {
		t.Fatalf("expected 3 videos for lowercase 'disabled', got %d (err: %v)", len(resDisLower.Videos), err)
	}

	resMBLower, err := client.ScanVideos("total mb", 100.0)
	if err != nil || len(resMBLower.Videos) != 2 {
		t.Fatalf("expected 2 videos for lowercase 'total mb', got %d (err: %v)", len(resMBLower.Videos), err)
	}
}

func TestGetAssetAlbums(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/api/albums" {
			t.Errorf("Unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}

		assetID := r.URL.Query().Get("assetId")
		if assetID == "asset-none" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
			return
		}
		if assetID == "asset-empty-body" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if assetID == "asset-null" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("null"))
			return
		}
		if assetID == "asset-err" {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"message": "internal error"}`))
			return
		}

		if assetID != "asset-123" {
			t.Errorf("Expected query assetId=asset-123, got %s", assetID)
		}

		albums := []Album{
			{ID: "album-1", AlbumName: "Trip 2024"},
			{ID: "album-2", AlbumName: "Favorites"},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(albums)
	}))
	defer server.Close()

	client := newTestClient(server, "secret")

	// 1. Success case
	albums, err := client.GetAssetAlbumsWithContext(context.Background(), "asset-123")
	if err != nil {
		t.Fatalf("GetAssetAlbumsWithContext failed: %v", err)
	}
	if len(albums) != 2 {
		t.Fatalf("Expected 2 albums, got %d", len(albums))
	}
	if albums[0].ID != "album-1" || albums[0].AlbumName != "Trip 2024" {
		t.Errorf("Unexpected album 0: %+v", albums[0])
	}
	if albums[1].ID != "album-2" || albums[1].AlbumName != "Favorites" {
		t.Errorf("Unexpected album 1: %+v", albums[1])
	}

	// 2. Convenience wrapper
	albumsConv, err := client.GetAssetAlbums("asset-123")
	if err != nil || len(albumsConv) != 2 {
		t.Fatalf("GetAssetAlbums wrapper failed: %v, len=%d", err, len(albumsConv))
	}

	// 3. Empty albums list
	emptyAlbums, err := client.GetAssetAlbums("asset-none")
	if err != nil {
		t.Fatalf("GetAssetAlbums failed for empty list: %v", err)
	}
	if len(emptyAlbums) != 0 {
		t.Errorf("Expected 0 albums, got %d", len(emptyAlbums))
	}

	// 4. Empty body (200 with no content)
	emptyBodyAlbums, err := client.GetAssetAlbums("asset-empty-body")
	if err != nil {
		t.Fatalf("GetAssetAlbums failed on empty body: %v", err)
	}
	if len(emptyBodyAlbums) != 0 {
		t.Errorf("Expected 0 albums on empty body, got %d", len(emptyBodyAlbums))
	}

	// 5. Null body response ("null")
	nullBodyAlbums, err := client.GetAssetAlbums("asset-null")
	if err != nil {
		t.Fatalf("GetAssetAlbums failed on null body: %v", err)
	}
	if nullBodyAlbums == nil || len(nullBodyAlbums) != 0 {
		t.Errorf("Expected non-nil empty slice on null body, got %v", nullBodyAlbums)
	}

	// 5. Empty assetID validation
	if _, err := client.GetAssetAlbums(""); err == nil {
		t.Errorf("Expected error for empty assetID")
	}

	// 6. Server error handling
	if _, err := client.GetAssetAlbums("asset-err"); err == nil {
		t.Errorf("Expected error for 500 response")
	}
}

func TestSetAssetFavorite(t *testing.T) {
	var lastReceivedBody map[string]interface{}
	var lastPath string
	var lastMethod string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		lastMethod = r.Method
		lastPath = r.URL.Path

		if r.URL.Path == "/api/assets/asset-err" {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"message": "update failed"}`))
			return
		}

		_ = json.NewDecoder(r.Body).Decode(&lastReceivedBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id": "asset-123"}`))
	}))
	defer server.Close()

	client := newTestClient(server, "secret")

	// 1. Success set favorite = true
	err := client.SetAssetFavoriteWithContext(context.Background(), "asset-123", true)
	if err != nil {
		t.Fatalf("SetAssetFavoriteWithContext failed: %v", err)
	}
	if lastMethod != http.MethodPut {
		t.Errorf("Expected method PUT, got %s", lastMethod)
	}
	if lastPath != "/api/assets/asset-123" {
		t.Errorf("Expected path /api/assets/asset-123, got %s", lastPath)
	}
	if lastReceivedBody["isFavorite"] != true {
		t.Errorf("Expected isFavorite=true in payload, got %v", lastReceivedBody["isFavorite"])
	}

	// 2. Convenience wrapper with favorite = false
	err = client.SetAssetFavorite("asset-123", false)
	if err != nil {
		t.Fatalf("SetAssetFavorite failed: %v", err)
	}
	if lastReceivedBody["isFavorite"] != false {
		t.Errorf("Expected isFavorite=false in payload, got %v", lastReceivedBody["isFavorite"])
	}

	// 3. Empty assetID
	if err := client.SetAssetFavorite("", true); err == nil {
		t.Errorf("Expected error for empty assetID")
	}

	// 4. Server error
	if err := client.SetAssetFavorite("asset-err", true); err == nil {
		t.Errorf("Expected error on server failure")
	}
}

func TestUpdateAssetDescription(t *testing.T) {
	var lastReceivedBody map[string]interface{}
	var lastPath string
	var lastMethod string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		lastMethod = r.Method
		lastPath = r.URL.Path

		if r.URL.Path == "/api/assets/asset-err" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		_ = json.NewDecoder(r.Body).Decode(&lastReceivedBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id": "asset-123"}`))
	}))
	defer server.Close()

	client := newTestClient(server, "secret")

	// 1. Success update description
	err := client.UpdateAssetDescriptionWithContext(context.Background(), "asset-123", "Vacation video")
	if err != nil {
		t.Fatalf("UpdateAssetDescriptionWithContext failed: %v", err)
	}
	if lastMethod != http.MethodPut {
		t.Errorf("Expected method PUT, got %s", lastMethod)
	}
	if lastPath != "/api/assets/asset-123" {
		t.Errorf("Expected path /api/assets/asset-123, got %s", lastPath)
	}
	if lastReceivedBody["description"] != "Vacation video" {
		t.Errorf("Expected description='Vacation video', got %v", lastReceivedBody["description"])
	}

	// 2. Convenience wrapper
	err = client.UpdateAssetDescription("asset-123", "New caption")
	if err != nil {
		t.Fatalf("UpdateAssetDescription failed: %v", err)
	}
	if lastReceivedBody["description"] != "New caption" {
		t.Errorf("Expected description='New caption', got %v", lastReceivedBody["description"])
	}

	// 3. Empty assetID
	if err := client.UpdateAssetDescription("", "text"); err == nil {
		t.Errorf("Expected error for empty assetID")
	}

	// 4. Server error
	if err := client.UpdateAssetDescription("asset-err", "text"); err == nil {
		t.Errorf("Expected error on server failure")
	}
}


func TestAsset_IsFavoriteJSON(t *testing.T) {
	// JSON with isFavorite: true
	jsonTrue := `{"id": "v1", "type": "VIDEO", "isFavorite": true}`
	var a1 Asset
	if err := json.Unmarshal([]byte(jsonTrue), &a1); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if !a1.IsFavorite {
		t.Errorf("Expected a1.IsFavorite == true")
	}

	// JSON with isFavorite: false
	jsonFalse := `{"id": "v2", "type": "VIDEO", "isFavorite": false}`
	var a2 Asset
	if err := json.Unmarshal([]byte(jsonFalse), &a2); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if a2.IsFavorite {
		t.Errorf("Expected a2.IsFavorite == false")
	}

	// JSON with isFavorite omitted
	jsonOmitted := `{"id": "v3", "type": "VIDEO"}`
	var a3 Asset
	if err := json.Unmarshal([]byte(jsonOmitted), &a3); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if a3.IsFavorite {
		t.Errorf("Expected a3.IsFavorite == false when omitted")
	}
}

func TestGetAsset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/assets/asset-fav" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": "asset-fav", "type": "VIDEO", "isFavorite": true, "originalFileName": "clip.mp4"}`))
	}))
	defer server.Close()

	client := newTestClient(server, "secret")

	asset, err := client.GetAssetWithContext(context.Background(), "asset-fav")
	if err != nil {
		t.Fatalf("GetAssetWithContext failed: %v", err)
	}
	if asset.ID != "asset-fav" || !asset.IsFavorite || asset.OriginalFileName != "clip.mp4" {
		t.Errorf("Unexpected asset returned: %+v", asset)
	}

	assetConv, err := client.GetAsset("asset-fav")
	if err != nil || assetConv.ID != "asset-fav" || !assetConv.IsFavorite {
		t.Errorf("GetAsset wrapper failed: %v, %+v", err, assetConv)
	}

	if _, err := client.GetAsset(""); err == nil {
		t.Errorf("Expected error for empty assetID")
	}
}

func TestGetAssetRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		name         string
		responseBody string
		assetID      string
	}{
		{
			name:         "empty body",
			responseBody: "",
			assetID:      "asset-empty",
		},
		{
			name:         "whitespace body",
			responseBody: "   \n\t ",
			assetID:      "asset-space",
		},
		{
			name:         "null body",
			responseBody: "null",
			assetID:      "asset-null",
		},
		{
			name:         "empty json object",
			responseBody: "{}",
			assetID:      "asset-empty-obj",
		},
		{
			name:         "mismatched asset id",
			responseBody: `{"id": "different-id", "type": "VIDEO"}`,
			assetID:      "expected-id",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.responseBody))
			}))
			defer server.Close()

			client := newTestClient(server, "secret")
			asset, err := client.GetAssetWithContext(context.Background(), tc.assetID)
			if err == nil {
				t.Fatalf("expected error for %s, got asset: %+v", tc.name, asset)
			}
		})
	}
}

func TestCopyAssetStack(t *testing.T) {
	var capturedMethod, capturedPath, capturedContentType string
	var payload struct {
		SourceID    string `json:"sourceId"`
		TargetID    string `json:"targetId"`
		Albums      bool   `json:"albums"`
		Favorite    bool   `json:"favorite"`
		SharedLinks bool   `json:"sharedLinks"`
		Sidecar     bool   `json:"sidecar"`
		Stack       bool   `json:"stack"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		capturedContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := newTestClient(server, "secret")

	// 1. Successful stack copy
	err := client.CopyAssetStackWithContext(context.Background(), "src-1", "dst-2")
	if err != nil {
		t.Fatalf("CopyAssetStackWithContext failed: %v", err)
	}
	if capturedMethod != http.MethodPut || capturedPath != "/api/assets/copy" {
		t.Errorf("unexpected method/path: %s %s", capturedMethod, capturedPath)
	}
	if capturedContentType != "application/json" {
		t.Errorf("unexpected Content-Type: %s", capturedContentType)
	}
	if payload.SourceID != "src-1" || payload.TargetID != "dst-2" || !payload.Stack || payload.Albums || payload.Favorite || payload.SharedLinks || payload.Sidecar {
		t.Errorf("unexpected copy payload: %+v", payload)
	}

	// 2. Empty ID validation
	if err := client.CopyAssetStackWithContext(context.Background(), "", "dst-2"); err == nil {
		t.Error("expected error for empty sourceID")
	}
	if err := client.CopyAssetStackWithContext(context.Background(), "src-1", ""); err == nil {
		t.Error("expected error for empty targetID")
	}

	// 3. API error response
	errServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer errServer.Close()

	errClient := newTestClient(errServer, "secret")
	if err := errClient.CopyAssetStackWithContext(context.Background(), "src-1", "dst-2"); err == nil {
		t.Error("expected error on HTTP 500 response")
	}
}

func TestUpdateStackPrimaryAsset(t *testing.T) {
	var capturedMethod, capturedPath, capturedContentType string
	var payload struct {
		PrimaryAssetID string `json:"primaryAssetId"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		capturedContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newTestClient(server, "secret")

	// 1. Success
	if err := client.UpdateStackPrimaryAssetWithContext(context.Background(), "stack-123", "asset-456"); err != nil {
		t.Fatalf("UpdateStackPrimaryAssetWithContext failed: %v", err)
	}
	if capturedMethod != http.MethodPut || capturedPath != "/api/stacks/stack-123" {
		t.Errorf("unexpected method/path: %s %s", capturedMethod, capturedPath)
	}
	if capturedContentType != "application/json" {
		t.Errorf("unexpected Content-Type: %s", capturedContentType)
	}
	if payload.PrimaryAssetID != "asset-456" {
		t.Errorf("unexpected payload: %+v", payload)
	}

	// 2. Validation
	if err := client.UpdateStackPrimaryAssetWithContext(context.Background(), "", "asset-456"); err == nil {
		t.Error("expected error for empty stackID")
	}
	if err := client.UpdateStackPrimaryAssetWithContext(context.Background(), "stack-123", ""); err == nil {
		t.Error("expected error for empty primaryAssetID")
	}

	// 3. Error response
	errServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer errServer.Close()
	errClient := newTestClient(errServer, "secret")
	if err := errClient.UpdateStackPrimaryAssetWithContext(context.Background(), "stack-123", "asset-456"); err == nil {
		t.Error("expected error on HTTP 500 response")
	}
}

func TestGetTagsAndCreateTag(t *testing.T) {
	var createdName string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/tags" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"id": "tag-1", "name": "existing-tag"}]`))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/tags" {
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			createdName = body["name"].(string)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(fmt.Sprintf(`{"id": "tag-new", "name": "%s"}`, createdName)))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := newTestClient(server, "secret")

	// Get tags
	tags, err := client.GetTagsWithContext(context.Background())
	if err != nil {
		t.Fatalf("GetTagsWithContext failed: %v", err)
	}
	if len(tags) != 1 || tags[0].Name != "existing-tag" {
		t.Fatalf("unexpected tags: %+v", tags)
	}

	// GetOrCreateTag on existing
	t1, err := client.GetOrCreateTagWithContext(context.Background(), "existing-tag")
	if err != nil || t1.ID != "tag-1" {
		t.Fatalf("expected existing tag, got: %+v (err: %v)", t1, err)
	}

	// GetOrCreateTag on non-existing (creates it)
	t2, err := client.GetOrCreateTagWithContext(context.Background(), "immich-optimizer")
	if err != nil || t2.ID != "tag-new" || t2.Name != "immich-optimizer" {
		t.Fatalf("expected new tag created, got: %+v (err: %v)", t2, err)
	}
	if createdName != "immich-optimizer" {
		t.Errorf("expected tag created with name immich-optimizer, got: %s", createdName)
	}
}

func TestTagAsset(t *testing.T) {
	var taggedID string
	var assetID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/tags/") && strings.HasSuffix(r.URL.Path, "/assets") {
			taggedID = strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/tags/"), "/assets")
			var body struct {
				IDs []string `json:"ids"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body.IDs) > 0 {
				assetID = body.IDs[0]
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"id": "asset-456", "success": true}]`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := newTestClient(server, "secret")
	err := client.TagAssetWithContext(context.Background(), "tag-123", "asset-456")
	if err != nil {
		t.Fatalf("TagAssetWithContext failed: %v", err)
	}
	if taggedID != "tag-123" || assetID != "asset-456" {
		t.Errorf("TagAsset mismatch: tagID=%s, assetID=%s", taggedID, assetID)
	}
}

func TestTagAssetReportsPerAssetFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"id":"asset-456","success":false}]`))
	}))
	defer server.Close()

	client := newTestClient(server, "secret")
	if err := client.TagAssetWithContext(context.Background(), "tag-123", "asset-456"); err == nil {
		t.Fatal("expected per-asset tag failure to return an error")
	}
}

func TestScanVideos_ExcludeOptimizedFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/tags" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"id": "tag-opt", "name": "immich-optimizer"}]`))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/search/metadata" {
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)

			// Tagged search query
			if _, hasTags := body["tagIds"]; hasTags {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{
					"assets": {
						"items": [{"id": "v-already-opt"}]
					}
				}`))
				return
			}

			// Main video scan
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"assets": {
					"items": [
						{"id": "v-already-opt", "type": "VIDEO", "size": 300000000, "originalFileName": "clip1.mp4", "duration": "0:01:00"},
						{"id": "v-new", "type": "VIDEO", "size": 300000000, "originalFileName": "clip2.mp4", "duration": "0:01:00"}
					]
				}
			}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := newTestClient(server, "secret")

	// 1. Scan with excludeOptimized = true (default)
	res, err := client.ScanVideos("MB/min", 10.0, true)
	if err != nil {
		t.Fatalf("ScanVideos failed: %v", err)
	}
	if res.SkippedAlreadyOptimized != 1 {
		t.Errorf("expected 1 video skipped as already optimized, got %d", res.SkippedAlreadyOptimized)
	}
	if len(res.Videos) != 1 || res.Videos[0].ID != "v-new" {
		t.Fatalf("expected only v-new returned, got %+v", res.Videos)
	}

	// 2. Scan with excludeOptimized = false
	resAll, err := client.ScanVideos("MB/min", 10.0, false)
	if err != nil {
		t.Fatalf("ScanVideos failed: %v", err)
	}
	if resAll.SkippedAlreadyOptimized != 0 {
		t.Errorf("expected 0 skipped when excludeOptimized=false, got %d", resAll.SkippedAlreadyOptimized)
	}
	if len(resAll.Videos) != 2 {
		t.Fatalf("expected 2 videos returned when excludeOptimized=false, got %d", len(resAll.Videos))
	}
}

func TestScanVideos_DateFilter(t *testing.T) {
	var capturedPayload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/search/metadata" {
			_ = json.NewDecoder(r.Body).Decode(&capturedPayload)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"assets": {
					"items": [
						{"id": "v-past", "type": "VIDEO", "size": 300000000, "duration": "0:01:00", "fileCreatedAt": "2022-06-15T12:00:00Z"},
						{"id": "v-match", "type": "VIDEO", "size": 300000000, "duration": "0:01:00", "fileCreatedAt": "2023-06-15T12:00:00Z"},
						{"id": "v-future", "type": "VIDEO", "size": 300000000, "duration": "0:01:00", "fileCreatedAt": "2024-06-15T12:00:00Z"}
					]
				}
			}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := newTestClient(server, "secret")

	opts := ScanOptions{
		DateAfter:  "2023-01-01",
		DateBefore: "2023-12-31",
	}

	res, err := client.ScanVideosWithOptions(context.Background(), "MB/min", 10.0, opts)
	if err != nil {
		t.Fatalf("ScanVideosWithOptions failed: %v", err)
	}

	if _, ok := capturedPayload["takenAfter"]; !ok {
		t.Errorf("expected takenAfter in search payload, got none")
	}
	if _, ok := capturedPayload["takenBefore"]; !ok {
		t.Errorf("expected takenBefore in search payload, got none")
	}

	if res.SkippedDateFilter != 2 {
		t.Errorf("expected 2 videos skipped due to date filter, got %d", res.SkippedDateFilter)
	}
	if len(res.Videos) != 1 || res.Videos[0].ID != "v-match" {
		t.Fatalf("expected only v-match, got %+v", res.Videos)
	}
}

func TestScanVideos_DateFilter_EmptyFileCreatedAt(t *testing.T) {
	// An asset with no fileCreatedAt passes through the client-side check;
	// the server-side takenAfter/takenBefore already filtered it correctly.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/search/metadata" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"assets": {"items": [{"id": "v-no-date", "type": "VIDEO", "size": 300000000, "duration": "0:01:00"}]}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := newTestClient(server, "secret")
	res, err := client.ScanVideosWithOptions(context.Background(), "Disabled", 0, ScanOptions{
		DateAfter:  "2023-01-01",
		DateBefore: "2023-12-31",
	})
	if err != nil {
		t.Fatalf("ScanVideosWithOptions failed: %v", err)
	}
	if res.SkippedDateFilter != 0 {
		t.Errorf("expected 0 skipped for missing fileCreatedAt, got %d", res.SkippedDateFilter)
	}
	if len(res.Videos) != 1 || res.Videos[0].ID != "v-no-date" {
		t.Fatalf("expected v-no-date to pass through, got %+v", res.Videos)
	}
}

func TestScanVideos_DateFilter_LocalTimezone(t *testing.T) {
	// A video shot at 00:30 local time is stored as UTC. With a filter starting on that
	// same date, it must be included — not dropped because the UTC value shows the day before.
	localMidnight := time.Date(2023, 6, 15, 0, 30, 0, 0, time.Local)
	utcStr := localMidnight.UTC().Format(time.RFC3339)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/search/metadata" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"assets": {"items": [{"id": "v-boundary", "type": "VIDEO", "size": 300000000, "duration": "0:01:00", "fileCreatedAt": "` + utcStr + `"}]}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := newTestClient(server, "secret")
	res, err := client.ScanVideosWithOptions(context.Background(), "Disabled", 0, ScanOptions{
		DateAfter:  "2023-06-15",
		DateBefore: "2023-06-15",
	})
	if err != nil {
		t.Fatalf("ScanVideosWithOptions failed: %v", err)
	}
	if res.SkippedDateFilter != 0 {
		t.Errorf("expected boundary video (00:30 local) to be included, got skipped")
	}
	if len(res.Videos) != 1 || res.Videos[0].ID != "v-boundary" {
		t.Fatalf("expected v-boundary included, got %+v", res.Videos)
	}
}

func TestAsset_IsExternalLibrary(t *testing.T) {
	libID := "uuid-1234"
	emptyLibID := ""

	tests := []struct {
		name     string
		asset    Asset
		expected bool
	}{
		{
			name:     "standard internal asset",
			asset:    Asset{ID: "1", IsReadOnly: false, IsExternal: false, LibraryID: nil},
			expected: false,
		},
		{
			name:     "read-only asset (Immich v1.118+)",
			asset:    Asset{ID: "2", IsReadOnly: true, IsExternal: false, LibraryID: nil},
			expected: true,
		},
		{
			name:     "external asset flag (Immich legacy)",
			asset:    Asset{ID: "3", IsReadOnly: false, IsExternal: true, LibraryID: nil},
			expected: true,
		},
		{
			name:     "libraryId populated",
			asset:    Asset{ID: "4", IsReadOnly: false, IsExternal: false, LibraryID: &libID},
			expected: true,
		},
		{
			name:     "empty libraryId string",
			asset:    Asset{ID: "5", IsReadOnly: false, IsExternal: false, LibraryID: &emptyLibID},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.asset.IsExternalLibrary(); got != tc.expected {
				t.Errorf("IsExternalLibrary() = %v, expected %v", got, tc.expected)
			}
		})
	}
}

func TestScanVideos_ExternalLibraryFilter(t *testing.T) {
	libID := "external-lib-uuid"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/search/metadata" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"assets": {
					"items": [
						{"id": "v-internal", "type": "VIDEO", "size": 300000000, "originalFileName": "internal.mp4", "duration": "0:01:00", "isReadOnly": false, "isExternal": false},
						{"id": "v-readonly", "type": "VIDEO", "size": 300000000, "originalFileName": "ext1.mp4", "duration": "0:01:00", "isReadOnly": true},
						{"id": "v-external", "type": "VIDEO", "size": 300000000, "originalFileName": "ext2.mp4", "duration": "0:01:00", "isExternal": true},
						{"id": "v-libid", "type": "VIDEO", "size": 300000000, "originalFileName": "ext3.mp4", "duration": "0:01:00", "libraryId": "` + libID + `"}
					]
				}
			}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := newTestClient(server, "secret")

	res, err := client.ScanVideos("MB/min", 10.0, false)
	if err != nil {
		t.Fatalf("ScanVideos failed: %v", err)
	}

	if res.SkippedExternal != 3 {
		t.Errorf("expected 3 external library videos skipped, got %d", res.SkippedExternal)
	}
	if len(res.Videos) != 1 || res.Videos[0].ID != "v-internal" {
		t.Fatalf("expected only v-internal returned, got %+v", res.Videos)
	}
}

func TestQueryVideoAssets_ExternalLibraryFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/search/metadata" {
			var reqBody map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&reqBody)
			if reqBody["page"].(float64) == 1 {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{
					"assets": {
						"items": [
							{"id": "v-internal", "type": "VIDEO", "size": 5000, "isReadOnly": false},
							{"id": "v-external", "type": "VIDEO", "size": 5000, "isReadOnly": true}
						]
					}
				}`))
			} else {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"assets": {"items": []}}`))
			}
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := newTestClient(server, "secret")

	videos, err := client.QueryVideoAssets(1000)
	if err != nil {
		t.Fatalf("QueryVideoAssets failed: %v", err)
	}
	if len(videos) != 1 || videos[0].ID != "v-internal" {
		t.Fatalf("expected only v-internal returned, got %+v", videos)
	}
}

func TestAsset_HasTag(t *testing.T) {
	a := Asset{
		ID: "asset-1",
		Tags: []Tag{
			{ID: "t-1", Name: " immich-optimizer "},
			{ID: "t-2", Name: "nature"},
		},
	}

	if !a.HasTag("immich-optimizer") {
		t.Errorf("expected HasTag('immich-optimizer') to be true")
	}
	if !a.HasTag("IMMICH-OPTIMIZER") {
		t.Errorf("expected HasTag('IMMICH-OPTIMIZER') case-insensitive match")
	}
	if !a.HasTag("nature") {
		t.Errorf("expected HasTag('nature') to be true")
	}
	if a.HasTag("vacation") {
		t.Errorf("expected HasTag('vacation') to be false")
	}

	emptyAsset := Asset{ID: "asset-2"}
	if emptyAsset.HasTag("immich-optimizer") {
		t.Errorf("expected empty tags to return false")
	}
}

func TestAsset_IsLivePhoto(t *testing.T) {
	liveID := "video-123"
	photoWithCompanion := Asset{
		ID:               "photo-1",
		Type:             "IMAGE",
		LivePhotoVideoID: &liveID,
	}
	if !photoWithCompanion.IsLivePhoto() {
		t.Errorf("expected photo with LivePhotoVideoID to be recognized as Live Photo")
	}

	hiddenCompanionVideo := Asset{
		ID:         "video-123",
		Type:       "VIDEO",
		Visibility: "hidden",
	}
	if !hiddenCompanionVideo.IsLivePhoto() {
		t.Errorf("expected hidden companion video to be recognized as Live Photo")
	}

	normalVideo := Asset{
		ID:         "video-456",
		Type:       "VIDEO",
		Visibility: "timeline",
	}
	if normalVideo.IsLivePhoto() {
		t.Errorf("expected regular timeline video not to be recognized as Live Photo")
	}
}

func TestGetOptimizerTaggedAssetIDs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/tags" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[
				{"id": "tag-opt-1", "name": "immich-optimizer"},
				{"id": "tag-opt-2", "name": " immich-optimizer "},
				{"id": "tag-other", "name": "vacation"}
			]`))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/search/metadata" {
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			tagIDs, _ := body["tagIds"].([]interface{})
			w.Header().Set("Content-Type", "application/json")
			if len(tagIDs) > 0 && tagIDs[0] == "tag-opt-1" {
				w.Write([]byte(`{"assets": {"items": [{"id": "v-1"}, {"id": "v-2"}]}}`))
				return
			}
			if len(tagIDs) > 0 && tagIDs[0] == "tag-opt-2" {
				w.Write([]byte(`{"assets": {"items": [{"id": "v-3"}]}}`))
				return
			}
			w.Write([]byte(`{"assets": {"items": []}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	client := newTestClient(ts, "secret")
	ids, err := client.GetOptimizerTaggedAssetIDs(context.Background())
	if err != nil {
		t.Fatalf("GetOptimizerTaggedAssetIDs failed: %v", err)
	}
	if len(ids) != 3 || !ids["v-1"] || !ids["v-2"] || !ids["v-3"] {
		t.Errorf("expected 3 IDs (v-1, v-2, v-3), got %+v", ids)
	}
}
