package immich

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"immich-video-optimizer/logger"
)

var retryBackoff = func(attempt int) time.Duration {
	backoff := time.Duration(1<<(attempt-2)) * time.Second
	if backoff > 10*time.Second {
		backoff = 10 * time.Second
	}
	return backoff
}

// Client is a REST client for the Immich API
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	maxRetries int
}

// NewClient creates a new Immich client
func NewClient(baseURL, apiKey string) *Client {
	baseURL = strings.TrimSpace(baseURL)
	baseURL = strings.TrimRight(baseURL, "/")
	baseURL = strings.TrimSuffix(baseURL, "/api")
	return &Client{
		baseURL:    baseURL,
		apiKey:     strings.TrimSpace(apiKey),
		httpClient: &http.Client{
			// No global timeout so large multi-GB video downloads and uploads are not cut off.
			// Individual API calls and transfers manage deadlines via Context.
		},
		maxRetries: 2,
	}
}

// SetMaxRetries sets the maximum number of retry attempts for transient failures.
func (c *Client) SetMaxRetries(retries int) {
	if retries < 0 {
		retries = 0
	}
	c.maxRetries = retries
}

// SetTimeout sets a custom timeout for the HTTP client
func (c *Client) SetTimeout(d time.Duration) {
	c.httpClient.Timeout = d
}

// SetHTTPClient sets the underlying HTTP client (e.g. for custom transports or testing).
func (c *Client) SetHTTPClient(client *http.Client) {
	if client != nil {
		c.httpClient = client
	}
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
		logger.Error("Immich API %s %s returned status %d: %s", req.Method, req.URL.String(), resp.StatusCode, string(body))
		return nil, &APIError{
			StatusCode: resp.StatusCode,
			Message:    string(body),
		}
	}

	return resp, nil
}

// CreateTrackingAlbumWithContext creates a new album for tracking progress with context cancellation
func (c *Client) CreateTrackingAlbumWithContext(ctx context.Context, name string) (*Album, error) {
	payload := map[string]string{"albumName": name}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/albums", bytes.NewReader(body))
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

// CreateTrackingAlbum creates a new album for tracking progress
func (c *Client) CreateTrackingAlbum(name string) (*Album, error) {
	return c.CreateTrackingAlbumWithContext(context.Background(), name)
}

// GetAssetAlbumsWithContext returns all albums containing the specified asset with context cancellation.
// Queries GET /api/albums?assetId={assetID}.
func (c *Client) GetAssetAlbumsWithContext(ctx context.Context, assetID string) ([]Album, error) {
	if assetID == "" {
		return nil, errors.New("assetID cannot be empty")
	}
	urlStr := fmt.Sprintf("%s/api/albums?assetId=%s", c.baseURL, url.QueryEscape(assetID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return []Album{}, nil
	}

	var albums []Album
	if err := json.Unmarshal(body, &albums); err != nil {
		return nil, fmt.Errorf("failed to decode albums: %w", err)
	}
	if albums == nil {
		albums = []Album{}
	}
	return albums, nil
}

// GetAssetAlbums returns all albums containing the specified asset.
func (c *Client) GetAssetAlbums(assetID string) ([]Album, error) {
	return c.GetAssetAlbumsWithContext(context.Background(), assetID)
}

// SetAssetFavoriteWithContext updates the favorite status of an asset with context cancellation.
// Immich API uses PUT /api/assets/{assetID} with payload {"isFavorite": isFavorite}.
func (c *Client) SetAssetFavoriteWithContext(ctx context.Context, assetID string, isFavorite bool) error {
	if assetID == "" {
		return errors.New("assetID cannot be empty")
	}
	payload := map[string]bool{"isFavorite": isFavorite}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	urlStr := fmt.Sprintf("%s/api/assets/%s", c.baseURL, url.PathEscape(assetID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, urlStr, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// SetAssetFavorite updates the favorite status of an asset.
func (c *Client) SetAssetFavorite(assetID string, isFavorite bool) error {
	return c.SetAssetFavoriteWithContext(context.Background(), assetID, isFavorite)
}

// UpdateAssetDescriptionWithContext updates the description of an asset with context cancellation.
// Immich API uses PUT /api/assets/{assetID} with payload {"description": description}.
func (c *Client) UpdateAssetDescriptionWithContext(ctx context.Context, assetID string, description string) error {
	if assetID == "" {
		return errors.New("assetID cannot be empty")
	}
	payload := map[string]string{"description": description}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	urlStr := fmt.Sprintf("%s/api/assets/%s", c.baseURL, url.PathEscape(assetID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, urlStr, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// UpdateAssetDescription updates the description of an asset.
func (c *Client) UpdateAssetDescription(assetID string, description string) error {
	return c.UpdateAssetDescriptionWithContext(context.Background(), assetID, description)
}

// GetAssetWithContext retrieves asset metadata by asset ID with context cancellation.
func (c *Client) GetAssetWithContext(ctx context.Context, assetID string) (*Asset, error) {
	if assetID == "" {
		return nil, errors.New("assetID cannot be empty")
	}
	urlStr := fmt.Sprintf("%s/api/assets/%s", c.baseURL, url.PathEscape(assetID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var asset Asset
	if err := json.NewDecoder(resp.Body).Decode(&asset); err != nil {
		return nil, fmt.Errorf("failed to decode asset: %w", err)
	}
	if asset.ID == "" || asset.ID != assetID {
		return nil, fmt.Errorf("invalid or missing asset details for %s", assetID)
	}
	return &asset, nil
}

// GetAsset retrieves asset metadata by asset ID.
func (c *Client) GetAsset(assetID string) (*Asset, error) {
	return c.GetAssetWithContext(context.Background(), assetID)
}

// AddAssetsToAlbumWithContext adds the given asset IDs to the specified album with context cancellation
func (c *Client) AddAssetsToAlbumWithContext(ctx context.Context, albumID string, assetIDs []string) error {
	if albumID == "" || len(assetIDs) == 0 {
		return nil
	}
	payload := map[string][]string{"ids": assetIDs}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	urlStr := fmt.Sprintf("%s/api/albums/%s/assets", c.baseURL, url.PathEscape(albumID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, urlStr, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// AddAssetsToAlbum adds the given asset IDs to the specified album
func (c *Client) AddAssetsToAlbum(albumID string, assetIDs []string) error {
	return c.AddAssetsToAlbumWithContext(context.Background(), albumID, assetIDs)
}

// DeleteAlbumWithContext deletes the album with the specified ID with context cancellation
func (c *Client) DeleteAlbumWithContext(ctx context.Context, albumID string) error {
	if albumID == "" {
		return nil
	}
	url := fmt.Sprintf("%s/api/albums/%s", c.baseURL, albumID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}

	resp, err := c.doRequest(req)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
			return nil
		}
		return err
	}
	defer resp.Body.Close()
	return nil
}

// DeleteAlbum deletes the album with the specified ID
func (c *Client) DeleteAlbum(albumID string) error {
	return c.DeleteAlbumWithContext(context.Background(), albumID)
}

// UpdateAlbumNameWithContext updates the name of an existing album in Immich with context cancellation.
func (c *Client) UpdateAlbumNameWithContext(ctx context.Context, albumID string, newName string) error {
	if albumID == "" {
		return nil
	}
	payload := map[string]string{"albumName": newName}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s/api/albums/%s", c.baseURL, albumID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(body))
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

// UpdateAlbumName updates the name of an existing album in Immich.
func (c *Client) UpdateAlbumName(albumID string, newName string) error {
	return c.UpdateAlbumNameWithContext(context.Background(), albumID, newName)
}

func (c *Client) downloadAssetOnce(ctx context.Context, assetID string, outPath string) error {
	url := fmt.Sprintf("%s/api/assets/%s/original", c.baseURL, assetID)
	logger.Debug("Downloading asset %s from %s -> %s", assetID, url, outPath)
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

	n, err := io.Copy(out, resp.Body)
	if err != nil {
		return err
	}
	logger.Debug("Finished download for asset %s (%d bytes written)", assetID, n)
	return nil
}

// DownloadAssetWithContext downloads the original video stream of an asset with context cancellation and automatic retries
func (c *Client) DownloadAssetWithContext(ctx context.Context, assetID string, outPath string) error {
	attempts := c.maxRetries + 1
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if attempt > 1 {
			backoff := retryBackoff(attempt)
			logger.Info("Retrying download for asset %s (attempt %d/%d) in %v...", assetID, attempt, attempts, backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		err := c.downloadAssetOnce(ctx, assetID, outPath)
		if err == nil {
			return nil
		}
		lastErr = err

		// Clean up partial file on failure so next attempt doesn't keep a corrupted file
		_ = os.Remove(outPath)

		if ctx.Err() != nil {
			return ctx.Err()
		}

		// If it's a 4xx client error (e.g. 404 Not Found, 401 Unauthorized), do not retry
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			logger.Error("Download failed with client error %d for asset %s; aborting retries", apiErr.StatusCode, assetID)
			return err
		}

		if attempt < attempts {
			logger.Error("Download attempt %d/%d failed for asset %s: %v", attempt, attempts, assetID, err)
		}
	}
	logger.Error("All %d download attempts failed for asset %s: %v", attempts, assetID, lastErr)
	return lastErr
}

// DownloadAsset downloads the original video stream of an asset to the specified path
func (c *Client) DownloadAsset(assetID string, outPath string) error {
	return c.DownloadAssetWithContext(context.Background(), assetID, outPath)
}

func (c *Client) uploadReplacementAssetOnce(ctx context.Context, filePath string, orig Asset) (*Asset, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	fileStat, err := file.Stat()
	if err != nil {
		return nil, err
	}

	deviceAssetID := fmt.Sprintf("opt-%s-%d", orig.ID, time.Now().UnixNano())
	deviceID := "immich-video-optimizer"
	fileCreatedAt := orig.FileCreatedAt
	if fileCreatedAt == "" {
		fileCreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	fileModifiedAt := orig.FileModifiedAt
	if fileModifiedAt == "" {
		fileModifiedAt = fileCreatedAt
	}

	uploadFileName := orig.OriginalFileName
	if uploadFileName == "" {
		uploadFileName = filepath.Base(filePath)
	}

	headerBuf := &bytes.Buffer{}
	writer := multipart.NewWriter(headerBuf)

	_ = writer.WriteField("deviceAssetId", deviceAssetID)
	_ = writer.WriteField("deviceId", deviceID)
	_ = writer.WriteField("fileCreatedAt", fileCreatedAt)
	_ = writer.WriteField("fileModifiedAt", fileModifiedAt)

	_, err = writer.CreateFormFile("assetData", uploadFileName)
	if err != nil {
		return nil, err
	}

	trailerBuf := bytes.NewBufferString(fmt.Sprintf("\r\n--%s--\r\n", writer.Boundary()))
	bodyReader := io.MultiReader(headerBuf, file, trailerBuf)

	logger.Debug("Uploading replacement asset for %s (%s, size %d bytes)", orig.OriginalFileName, orig.ID, fileStat.Size())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/assets", bodyReader)
	if err != nil {
		logger.Error("Failed to create upload request for %s: %v", orig.OriginalFileName, err)
		return nil, err
	}
	req.ContentLength = int64(headerBuf.Len()) + fileStat.Size() + int64(trailerBuf.Len())
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var newAsset Asset
	if err := json.NewDecoder(resp.Body).Decode(&newAsset); err != nil {
		logger.Error("Failed to decode upload response for %s: %v", orig.OriginalFileName, err)
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	logger.Debug("Successfully uploaded replacement asset %s (new ID: %s)", orig.OriginalFileName, newAsset.ID)
	return &newAsset, nil
}

// UploadReplacementAssetWithContext uploads a replacement video asset via multipart form with context, streaming without buffering into RAM, with automatic retries
func (c *Client) UploadReplacementAssetWithContext(ctx context.Context, filePath string, orig Asset) (*Asset, error) {
	attempts := c.maxRetries + 1
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if attempt > 1 {
			backoff := retryBackoff(attempt)
			logger.Info("Retrying upload for %s (attempt %d/%d) in %v...", orig.OriginalFileName, attempt, attempts, backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		newAsset, err := c.uploadReplacementAssetOnce(ctx, filePath, orig)
		if err == nil {
			return newAsset, nil
		}
		lastErr = err

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// Don't retry client errors (4xx) like 400 Bad Request or 401 Unauthorized
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			logger.Error("Upload failed with client error %d for %s; aborting retries", apiErr.StatusCode, orig.OriginalFileName)
			return nil, err
		}

		if attempt < attempts {
			logger.Error("Upload attempt %d/%d failed for %s: %v", attempt, attempts, orig.OriginalFileName, err)
		}
	}
	logger.Error("All %d upload attempts failed for %s: %v", attempts, orig.OriginalFileName, lastErr)
	return nil, lastErr
}

// UploadReplacementAsset uploads a replacement video asset via multipart form, preserving metadata
func (c *Client) UploadReplacementAsset(filePath string, orig Asset) (*Asset, error) {
	return c.UploadReplacementAssetWithContext(context.Background(), filePath, orig)
}

// DeleteAssetWithContext deletes the specified asset with context cancellation and automatic retries
func (c *Client) DeleteAssetWithContext(ctx context.Context, assetID string) error {
	attempts := c.maxRetries + 1
	if attempts < 1 {
		attempts = 1
	}

	payload := map[string][]string{"ids": {assetID}}
	body, _ := json.Marshal(payload)

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if attempt > 1 {
			backoff := retryBackoff(attempt)
			logger.Info("Retrying delete for asset %s (attempt %d/%d) in %v...", assetID, attempt, attempts, backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+"/api/assets", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.doRequest(req)
		if err == nil {
			resp.Body.Close()
			return nil
		}
		lastErr = err

		if ctx.Err() != nil {
			return ctx.Err()
		}

		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			logger.Error("Delete failed with client error %d for asset %s; aborting retries", apiErr.StatusCode, assetID)
			return err
		}

		if attempt < attempts {
			logger.Error("Delete attempt %d/%d failed for asset %s: %v", attempt, attempts, assetID, err)
		}
	}
	logger.Error("All %d delete attempts failed for asset %s: %v", attempts, assetID, lastErr)
	return lastErr
}

// DeleteAsset deletes the specified asset
func (c *Client) DeleteAsset(assetID string) error {
	return c.DeleteAssetWithContext(context.Background(), assetID)
}

// GetTagsWithContext fetches all tags from Immich
func (c *Client) GetTagsWithContext(ctx context.Context) ([]Tag, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var tags []Tag
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, fmt.Errorf("failed to decode tags response: %w", err)
	}
	return tags, nil
}

// CreateTagWithContext creates a new tag in Immich
func (c *Client) CreateTagWithContext(ctx context.Context, name string) (*Tag, error) {
	payload := map[string]interface{}{
		"name": name,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/tags", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var created Tag
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return nil, fmt.Errorf("failed to decode create tag response: %w", err)
	}
	return &created, nil
}

// GetOrCreateTagWithContext finds an existing tag by name or creates it if it doesn't exist
func (c *Client) GetOrCreateTagWithContext(ctx context.Context, name string) (*Tag, error) {
	tags, err := c.GetTagsWithContext(ctx)
	if err == nil {
		for _, t := range tags {
			if strings.EqualFold(strings.TrimSpace(t.Name), strings.TrimSpace(name)) {
				return &t, nil
			}
		}
	}
	return c.CreateTagWithContext(ctx, name)
}

// TagAssetWithContext adds the given tag to an asset
func (c *Client) TagAssetWithContext(ctx context.Context, tagID, assetID string) error {
	payload := map[string][]string{
		"ids": {assetID},
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, fmt.Sprintf("%s/api/tags/%s/assets", c.baseURL, tagID), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var results []struct {
		ID      string `json:"id"`
		Success bool   `json:"success"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return fmt.Errorf("failed to decode tag assignment response: %w", err)
	}
	if len(results) == 0 {
		return errors.New("tag assignment returned no results")
	}
	for _, result := range results {
		if !result.Success || result.ID != assetID {
			return fmt.Errorf("failed to assign tag %s to asset %s", tagID, result.ID)
		}
	}
	return nil
}

// CopyAssetStackWithContext copies an asset's stack membership without copying other asset state.
func (c *Client) CopyAssetStackWithContext(ctx context.Context, sourceID, targetID string) error {
	if sourceID == "" || targetID == "" {
		return errors.New("sourceID and targetID cannot be empty")
	}
	payload := map[string]interface{}{
		"sourceId":    sourceID,
		"targetId":    targetID,
		"albums":      false,
		"favorite":    false,
		"sharedLinks": false,
		"sidecar":     false,
		"stack":       true,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+"/api/assets/copy", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// UpdateStackPrimaryAssetWithContext updates the primary asset ID of a stack.
func (c *Client) UpdateStackPrimaryAssetWithContext(ctx context.Context, stackID, primaryAssetID string) error {
	if stackID == "" || primaryAssetID == "" {
		return errors.New("stackID and primaryAssetID cannot be empty")
	}
	payload := map[string]string{
		"primaryAssetId": primaryAssetID,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+"/api/stacks/"+url.PathEscape(stackID), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// GetTaggedAssetIDsWithContext retrieves all asset IDs that have the specified tag ID
func (c *Client) GetTaggedAssetIDsWithContext(ctx context.Context, tagID string) (map[string]bool, error) {
	tagged := make(map[string]bool)
	page := 1
	size := 1000

	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		payload := map[string]interface{}{
			"tagIds": []string{tagID},
			"page":   page,
			"size":   size,
		}
		body, _ := json.Marshal(payload)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/search/metadata", bytes.NewReader(body))
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
				Items []struct {
					ID string `json:"id"`
				} `json:"items"`
			} `json:"assets"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to decode tagged search response: %w", err)
		}
		resp.Body.Close()

		for _, item := range searchResp.Assets.Items {
			if item.ID != "" {
				tagged[item.ID] = true
			}
		}

		if len(searchResp.Assets.Items) < size {
			break
		}
		page++
	}

	return tagged, nil
}

// GetOptimizerTaggedAssetIDs retrieves all asset IDs associated with any tag named 'immich-optimizer'.
func (c *Client) GetOptimizerTaggedAssetIDs(ctx context.Context) (map[string]bool, error) {
	tags, err := c.GetTagsWithContext(ctx)
	if err != nil {
		return nil, err
	}
	tagged := make(map[string]bool)
	for _, t := range tags {
		if strings.EqualFold(strings.TrimSpace(t.Name), "immich-optimizer") {
			ids, err := c.GetTaggedAssetIDsWithContext(ctx, t.ID)
			if err == nil {
				for id := range ids {
					tagged[id] = true
				}
			}
		}
	}
	return tagged, nil
}
