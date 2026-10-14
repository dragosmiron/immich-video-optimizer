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
	"strconv"
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
	maxRetries int
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

// MaxRetries returns the configured maximum retry attempts.
func (c *Client) MaxRetries() int {
	return c.maxRetries
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

// HTTPClient returns the underlying HTTP client.
func (c *Client) HTTPClient() *http.Client {
	return c.httpClient
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
	Videos                  []Asset
	TotalAssetsScanned      int
	SkippedNonVideo         int
	SkippedBelowSize        int
	SkippedAlreadyOptimized int
}

// Tag represents an Immich tag entity
type Tag struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ExifInfo struct {
	FileSizeInByte  int64           `json:"fileSizeInByte"`
	ExifImageWidth  int             `json:"exifImageWidth,omitempty"`
	ExifImageHeight int             `json:"exifImageHeight,omitempty"`
	Duration        json.RawMessage `json:"duration,omitempty"`
}

// Asset represents an Immich asset
type Asset struct {
	ID               string          `json:"id"`
	Type             string          `json:"type"`
	Size             int64           `json:"size"`
	Duration         json.RawMessage `json:"duration,omitempty"`
	DeviceAssetID    string          `json:"deviceAssetId,omitempty"`
	DeviceID         string          `json:"deviceId,omitempty"`
	FileCreatedAt    string          `json:"fileCreatedAt,omitempty"`
	FileModifiedAt   string          `json:"fileModifiedAt,omitempty"`
	OriginalFileName string          `json:"originalFileName,omitempty"`
	IsFavorite       bool            `json:"isFavorite"`
	ExifInfo         ExifInfo        `json:"exifInfo,omitempty"`
}

// GetSize returns the asset size, falling back to exifInfo.fileSizeInByte if Size is 0
func (a *Asset) GetSize() int64 {
	if a.Size > 0 {
		return a.Size
	}
	return a.ExifInfo.FileSizeInByte
}

// parseISO8601Duration parses ISO 8601 duration strings like "PT1H2M3.45S" or "PT45S".
func parseISO8601Duration(s string) (float64, bool) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if !strings.HasPrefix(s, "P") {
		return 0, false
	}
	s = s[1:]
	var days, hours, mins, secs float64
	timePart := ""
	datePart := s
	if idx := strings.Index(s, "T"); idx != -1 {
		datePart = s[:idx]
		timePart = s[idx+1:]
	}
	if idx := strings.Index(datePart, "D"); idx != -1 {
		d, err := strconv.ParseFloat(datePart[:idx], 64)
		if err == nil {
			days = d
		}
	}
	cur := ""
	for i := 0; i < len(timePart); i++ {
		ch := timePart[i]
		if (ch >= '0' && ch <= '9') || ch == '.' || ch == ',' {
			cur += string(ch)
		} else if ch == 'H' {
			cur = strings.ReplaceAll(cur, ",", ".")
			h, err := strconv.ParseFloat(cur, 64)
			if err == nil {
				hours = h
			}
			cur = ""
		} else if ch == 'M' {
			cur = strings.ReplaceAll(cur, ",", ".")
			m, err := strconv.ParseFloat(cur, 64)
			if err == nil {
				mins = m
			}
			cur = ""
		} else if ch == 'S' {
			cur = strings.ReplaceAll(cur, ",", ".")
			sec, err := strconv.ParseFloat(cur, 64)
			if err == nil {
				secs = sec
			}
			cur = ""
		}
	}
	total := days*86400 + hours*3600 + mins*60 + secs
	return total, total > 0
}

// ParseDurationSeconds parses duration from raw JSON bytes (string interval, ISO-8601, or numeric).
// Supported formats:
// - ISO-8601 durations (e.g. "PT1M20S", "PT1H2M3.45S", "PT45S")
// - "HH:MM:SS" / "HH:MM:SS.micro" (e.g. "0:01:23.456000")
// - "MM:SS" / "MM:SS.micro" (e.g. "01:23.45")
// - Numeric seconds or formatted seconds string (e.g. "83.45", 83.45, "83.45s")
// - Milliseconds if specified with "ms" or integer > 86400 (e.g. 120000 -> 120s)
// Returns duration in seconds, or 0 if unparseable / missing / non-positive.
func ParseDurationSeconds(raw json.RawMessage) float64 {
	if len(raw) == 0 {
		return 0
	}
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return 0
	}
	if strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) {
		s = strings.Trim(s, `"`)
		s = strings.TrimSpace(s)
	}
	if s == "" || s == "null" {
		return 0
	}

	// 1. ISO-8601 duration
	if strings.HasPrefix(strings.ToUpper(s), "P") {
		if dur, ok := parseISO8601Duration(s); ok && dur > 0 {
			return dur
		}
	}

	// 2. Colon-separated interval (HH:MM:SS or MM:SS)
	if strings.Contains(s, ":") {
		sClean := strings.ReplaceAll(s, ",", ".")
		parts := strings.Split(sClean, ":")
		if len(parts) == 4 {
			d, err0 := strconv.ParseFloat(parts[0], 64)
			h, err1 := strconv.ParseFloat(parts[1], 64)
			m, err2 := strconv.ParseFloat(parts[2], 64)
			sec, err3 := strconv.ParseFloat(parts[3], 64)
			if err0 == nil && err1 == nil && err2 == nil && err3 == nil {
				total := d*86400 + h*3600 + m*60 + sec
				if total > 0 {
					return total
				}
			}
		} else if len(parts) == 3 {
			h, err1 := strconv.ParseFloat(parts[0], 64)
			m, err2 := strconv.ParseFloat(parts[1], 64)
			sec, err3 := strconv.ParseFloat(parts[2], 64)
			if err1 == nil && err2 == nil && err3 == nil {
				total := h*3600 + m*60 + sec
				if total > 0 {
					return total
				}
			}
		} else if len(parts) == 2 {
			m, err1 := strconv.ParseFloat(parts[0], 64)
			sec, err2 := strconv.ParseFloat(parts[1], 64)
			if err1 == nil && err2 == nil {
				total := m*60 + sec
				if total > 0 {
					return total
				}
			}
		}
	}

	// 3. Milliseconds suffix (e.g. "120000ms")
	lower := strings.ToLower(s)
	if strings.HasSuffix(lower, "ms") {
		numStr := strings.TrimSpace(strings.TrimSuffix(lower, "ms"))
		numStr = strings.ReplaceAll(numStr, ",", ".")
		if val, err := strconv.ParseFloat(numStr, 64); err == nil && val > 0 {
			return val / 1000.0
		}
	}

	// 4. Seconds suffix (e.g. "83.45s", "83.45sec")
	if strings.HasSuffix(lower, "sec") {
		s = strings.TrimSpace(strings.TrimSuffix(lower, "sec"))
	} else if strings.HasSuffix(lower, "s") {
		s = strings.TrimSpace(strings.TrimSuffix(lower, "s"))
	}

	s = strings.ReplaceAll(s, ",", ".")
	val, err := strconv.ParseFloat(s, 64)
	if err == nil && val > 0 {
		if val > 86400 {
			return val / 1000.0
		}
		return val
	}
	return 0
}

// DurationSeconds returns the asset duration in seconds, checking Asset.Duration and ExifInfo.Duration.
func (a *Asset) DurationSeconds() float64 {
	if len(a.Duration) > 0 {
		if sec := ParseDurationSeconds(a.Duration); sec > 0 {
			return sec
		}
	}
	if len(a.ExifInfo.Duration) > 0 {
		if sec := ParseDurationSeconds(a.ExifInfo.Duration); sec > 0 {
			return sec
		}
	}
	return 0
}

// MBPerMin returns the bitrate density of the asset in megabytes per minute.
// If duration is missing or non-positive, it falls back to assuming 1 minute (so total MB is used),
// ensuring large videos without duration metadata are conservatively included.
func (a *Asset) MBPerMin() float64 {
	sizeMB := float64(a.GetSize()) / (1024.0 * 1024.0)
	durSec := a.DurationSeconds()
	if durSec <= 0 {
		return sizeMB
	}
	durMin := durSec / 60.0
	return sizeMB / durMin
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
		logger.Error("Immich API %s %s returned status %d: %s", req.Method, req.URL.String(), resp.StatusCode, string(body))
		return nil, &APIError{
			StatusCode: resp.StatusCode,
			Message:    string(body),
		}
	}

	return resp, nil
}

// ScanVideos returns videos filtered by threshold and mode ("MB/min", "Total MB", "Disabled") along with bulk stats on excluded assets
func (c *Client) ScanVideos(mode string, threshold float64, excludeOptimized ...bool) (*QueryResult, error) {
	return c.ScanVideosWithContext(context.Background(), mode, threshold, excludeOptimized...)
}

// ScanVideosWithContext returns videos filtered by threshold and mode ("MB/min", "Total MB", "Disabled") along with bulk stats on excluded assets, supporting context cancellation
func (c *Client) ScanVideosWithContext(ctx context.Context, mode string, threshold float64, excludeOptimized ...bool) (*QueryResult, error) {
	res := &QueryResult{}
	page := 1
	size := 1000

	isExclude := false
	if len(excludeOptimized) > 0 {
		isExclude = excludeOptimized[0]
	}

	var taggedIDs map[string]bool
	if isExclude {
		tags, err := c.GetTagsWithContext(ctx)
		if err == nil {
			for _, t := range tags {
				if strings.EqualFold(t.Name, "immich-optimizer") {
					ids, err := c.GetTaggedAssetIDsWithContext(ctx, t.ID)
					if err == nil {
						taggedIDs = ids
					}
					break
				}
			}
		} else {
			logger.Debug("Could not fetch tags for exclusion: %v", err)
		}
	}

	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		payload := map[string]interface{}{
			"page":     page,
			"size":     size,
			"withExif": true,
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
				continue
			}

			if isExclude && taggedIDs != nil && taggedIDs[a.ID] {
				res.SkippedAlreadyOptimized++
				continue
			}

			var skipBelow bool
			switch strings.ToLower(strings.TrimSpace(mode)) {
			case "disabled", "disable", "off", "none":
				skipBelow = false
			case "total mb", "totalmb", "total_mb":
				if threshold > 0 && float64(a.GetSize())/(1024*1024) < threshold {
					skipBelow = true
				}
			default: // MB/min
				if threshold > 0 && a.MBPerMin() < threshold {
					skipBelow = true
				}
			}

			if skipBelow {
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

// QueryVideoAssets returns video assets that exceed the given size threshold in bytes
func (c *Client) QueryVideoAssets(sizeThresholdBytes int64) ([]Asset, error) {
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
			} else if a.GetSize() <= sizeThresholdBytes {
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

// DeleteAlbum deletes the album with the specified ID
func (c *Client) DeleteAlbum(albumID string) error {
	if albumID == "" {
		return nil
	}
	url := fmt.Sprintf("%s/api/albums/%s", c.baseURL, albumID)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
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

// UpdateAlbumName updates the name of an existing album in Immich.
func (c *Client) UpdateAlbumName(albumID string, newName string) error {
	if albumID == "" {
		return nil
	}
	payload := map[string]string{"albumName": newName}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s/api/albums/%s", c.baseURL, albumID)
	req, err := http.NewRequest(http.MethodPatch, url, bytes.NewReader(body))
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
			if strings.EqualFold(t.Name, name) {
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
