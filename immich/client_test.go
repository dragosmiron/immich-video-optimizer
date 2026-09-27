package immich

import (
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
				{ID: "1", Type: "VIDEO", Size: 0, ExifInfo: struct{ FileSizeInByte int64 `json:"fileSizeInByte"`; ExifImageWidth int `json:"exifImageWidth,omitempty"`; ExifImageHeight int `json:"exifImageHeight,omitempty"` }{FileSizeInByte: 1000}},
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

		if r.FormValue("deviceAssetId") != "dev-1" {
			t.Errorf("Expected deviceAssetId=dev-1, got %s", r.FormValue("deviceAssetId"))
		}
		if r.FormValue("deviceId") != "dev-id" {
			t.Errorf("Expected deviceId=dev-id, got %s", r.FormValue("deviceId"))
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

	orig := Asset{DeviceAssetID: "dev-1", DeviceID: "dev-id"}
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
