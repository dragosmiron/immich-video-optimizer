package immich

import (
	"context"
	"encoding/json"
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
