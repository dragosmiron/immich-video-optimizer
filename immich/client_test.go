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

	client := NewClient(server.URL, "secret")

	// Test success
	videos, err := client.QueryVideoAssets(800)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(videos) != 1 || videos[0].ID != "1" {
		t.Errorf("Expected 1 video with ID '1', got %v", videos)
	}

	// Test unauthorized
	badClient := NewClient(server.URL, "wrong")
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

	client := NewClient(server.URL, "secret")
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

	client := NewClient(server.URL, "secret")
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

	client := NewClient(server.URL, "secret")

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

	client := NewClient(server.URL, "secret")

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

	client := NewClient(server.URL, "secret")
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

	client := NewClient(server.URL, "secret")
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

	client := NewClient(server.URL, "secret")
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
