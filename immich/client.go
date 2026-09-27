package immich

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// APIError represents an error returned by the Immich API
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error %d: %s", e.StatusCode, e.Message)
}

// Client is a REST client for the Immich API
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient creates a new Immich client
func NewClient(baseURL, apiKey string) *Client {
	baseURL = strings.TrimSpace(baseURL)
	baseURL = strings.TrimRight(baseURL, "/")
	baseURL = strings.TrimSuffix(baseURL, "/api")
	return &Client{
		baseURL: baseURL,
		apiKey:  strings.TrimSpace(apiKey),
		httpClient: &http.Client{
			Timeout: 30 * time.Second, // Default timeout for network calls
		},
	}
}

// SetTimeout sets a custom timeout for the HTTP client
func (c *Client) SetTimeout(d time.Duration) {
	c.httpClient.Timeout = d
}

// BaseURL returns the configured base URL of the Immich server
func (c *Client) BaseURL() string {
	return c.baseURL
}

// APIKey returns the configured API key for authentication
func (c *Client) APIKey() string {
	return c.apiKey
}

// ValidateConnection checks if the server is reachable and API key is valid.
// Uses a short 8-second timeout so the user gets a quick error on bad URLs.
func (c *Client) ValidateConnection() error {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+"/api/users/me", nil)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	// Use a dedicated short-timeout client so the UI stays responsive.
	probeClient := &http.Client{Timeout: 8 * time.Second}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := probeClient.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach server: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return &APIError{StatusCode: resp.StatusCode, Message: string(body)}
	}
	return nil
}

// QueryResult holds queried videos and statistics on skipped items
type QueryResult struct {
	Videos              []Asset
	TotalAssetsScanned  int
	SkippedNonVideo     int
	SkippedBelowSize    int
}

type ExifInfo struct {
	FileSizeInByte  int64 `json:"fileSizeInByte"`
	ExifImageWidth  int   `json:"exifImageWidth,omitempty"`
	ExifImageHeight int   `json:"exifImageHeight,omitempty"`
}

// Asset represents an Immich asset
type Asset struct {
	ID               string   `json:"id"`
	Type             string   `json:"type"`
	Size             int64    `json:"size"`
	DeviceAssetID    string   `json:"deviceAssetId,omitempty"`
	DeviceID         string   `json:"deviceId,omitempty"`
	FileCreatedAt    string   `json:"fileCreatedAt,omitempty"`
	FileModifiedAt   string   `json:"fileModifiedAt,omitempty"`
	OriginalFileName string   `json:"originalFileName,omitempty"`
	ExifInfo         ExifInfo `json:"exifInfo,omitempty"`
}

// GetSize returns the asset size, falling back to exifInfo.fileSizeInByte if Size is 0
func (a *Asset) GetSize() int64 {
	if a.Size > 0 {
		return a.Size
	}
	return a.ExifInfo.FileSizeInByte
}

// Album represents an Immich album
type Album struct {
	ID        string `json:"id"`
	AlbumName string `json:"albumName"`
}

func (c *Client) doRequest(req *http.Request) (*http.Response, error) {
	req.Header.Set("x-api-key", c.apiKey)
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}

	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, &APIError{
			StatusCode: resp.StatusCode,
			Message:    string(body),
		}
	}

	return resp, nil
}

// ScanVideos returns videos exceeding sizeThreshold along with bulk stats on excluded assets
func (c *Client) ScanVideos(sizeThreshold int64) (*QueryResult, error) {
	res := &QueryResult{}
	page := 1
	size := 1000

	for {
		payload := map[string]interface{}{
			"page":     page,
			"size":     size,
			"withExif": true,
		}
		body, _ := json.Marshal(payload)

		req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/search/metadata", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.doRequest(req)
		if err != nil {
			return nil, err
		}

		var searchResp struct {
			Assets struct {
				Items []Asset `json:"items"`
			} `json:"assets"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to decode response: %w", err)
		}
		resp.Body.Close()

		if len(searchResp.Assets.Items) == 0 {
			break
		}

		res.TotalAssetsScanned += len(searchResp.Assets.Items)

		for _, a := range searchResp.Assets.Items {
			if a.Type != "VIDEO" {
				res.SkippedNonVideo++
			} else if a.GetSize() <= sizeThreshold {
				res.SkippedBelowSize++
			} else {
				res.Videos = append(res.Videos, a)
			}
		}

		if len(searchResp.Assets.Items) < size {
			break
		}
		page++
	}

	return res, nil
}

// QueryVideoAssets returns video assets that exceed the given size threshold
func (c *Client) QueryVideoAssets(sizeThreshold int64) ([]Asset, error) {
	res, err := c.ScanVideos(sizeThreshold)
	if err != nil {
		return nil, err
	}
	return res.Videos, nil
}

// CreateTrackingAlbum creates a new album for tracking progress
func (c *Client) CreateTrackingAlbum(name string) (*Album, error) {
	payload := map[string]string{"albumName": name}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/albums", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var album Album
	if err := json.NewDecoder(resp.Body).Decode(&album); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return &album, nil
}

// AddAssetsToAlbum adds the given asset IDs to the specified album
func (c *Client) AddAssetsToAlbum(albumID string, assetIDs []string) error {
	payload := map[string][]string{"ids": assetIDs}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s/api/albums/%s/assets", c.baseURL, albumID)
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// DownloadAssetWithContext downloads the original video stream of an asset with context cancellation
func (c *Client) DownloadAssetWithContext(ctx context.Context, assetID string, outPath string) error {
	url := fmt.Sprintf("%s/api/assets/%s/original", c.baseURL, assetID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "*/*")

	resp, err := c.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// DownloadAsset downloads the original video stream of an asset to the specified path
func (c *Client) DownloadAsset(assetID string, outPath string) error {
	return c.DownloadAssetWithContext(context.Background(), assetID, outPath)
}

// UploadReplacementAssetWithContext uploads a replacement video asset via multipart form with context
func (c *Client) UploadReplacementAssetWithContext(ctx context.Context, filePath string, orig Asset) (*Asset, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	deviceAssetID := orig.DeviceAssetID
	if deviceAssetID == "" {
		deviceAssetID = fmt.Sprintf("opt-%s-%d", orig.ID, time.Now().Unix())
	}
	deviceID := orig.DeviceID
	if deviceID == "" {
		deviceID = "immich-video-optimizer"
	}
	fileCreatedAt := orig.FileCreatedAt
	if fileCreatedAt == "" {
		fileCreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	fileModifiedAt := orig.FileModifiedAt
	if fileModifiedAt == "" {
		fileModifiedAt = fileCreatedAt
	}

	_ = writer.WriteField("deviceAssetId", deviceAssetID)
	_ = writer.WriteField("deviceId", deviceID)
	_ = writer.WriteField("fileCreatedAt", fileCreatedAt)
	_ = writer.WriteField("fileModifiedAt", fileModifiedAt)

	uploadFileName := orig.OriginalFileName
	if uploadFileName == "" {
		uploadFileName = filepath.Base(filePath)
	}

	part, err := writer.CreateFormFile("assetData", uploadFileName)
	if err != nil {
		return nil, err
	}

	_, err = io.Copy(part, file)
	if err != nil {
		return nil, err
	}
	err = writer.Close()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/assets", body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var newAsset Asset
	if err := json.NewDecoder(resp.Body).Decode(&newAsset); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return &newAsset, nil
}

// UploadReplacementAsset uploads a replacement video asset via multipart form, preserving metadata
func (c *Client) UploadReplacementAsset(filePath string, orig Asset) (*Asset, error) {
	return c.UploadReplacementAssetWithContext(context.Background(), filePath, orig)
}

// DeleteAsset deletes the specified asset
func (c *Client) DeleteAsset(assetID string) error {
	payload := map[string][]string{"ids": {assetID}}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest(http.MethodDelete, c.baseURL+"/api/assets", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}
