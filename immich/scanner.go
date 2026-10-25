package immich

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"immich-video-optimizer/logger"
)

// ScanVideos returns videos filtered by threshold and mode ("MB/min", "Total MB", "Disabled") along with bulk stats on excluded assets
func (c *Client) ScanVideos(mode string, threshold float64, excludeOptimized ...bool) (*QueryResult, error) {
	return c.ScanVideosWithContext(context.Background(), mode, threshold, excludeOptimized...)
}

// ScanVideosWithContext returns videos filtered by threshold and mode ("MB/min", "Total MB", "Disabled") along with bulk stats on excluded assets, supporting context cancellation
func (c *Client) ScanVideosWithContext(ctx context.Context, mode string, threshold float64, excludeOptimized ...bool) (*QueryResult, error) {
	isExclude := false
	if len(excludeOptimized) > 0 {
		isExclude = excludeOptimized[0]
	}
	return c.ScanVideosWithOptions(ctx, mode, threshold, ScanOptions{ExcludeOptimized: isExclude})
}

// ScanVideosWithOptions returns videos filtered by threshold, mode, exclude tag, and date constraints
func (c *Client) ScanVideosWithOptions(ctx context.Context, mode string, threshold float64, opts ScanOptions) (*QueryResult, error) {
	if c == nil {
		return nil, errors.New("immich client is nil")
	}
	res := &QueryResult{}
	page := 1
	size := 1000

	isExclude := opts.ExcludeOptimized

	var tAfter, tBeforeEnd time.Time
	if opts.DateAfter != "" {
		if t, err := time.ParseInLocation("2006-01-02", opts.DateAfter, time.Local); err == nil {
			tAfter = t
		}
	}
	if opts.DateBefore != "" {
		if t, err := time.ParseInLocation("2006-01-02", opts.DateBefore, time.Local); err == nil {
			tBeforeEnd = t.Add(24*time.Hour - time.Nanosecond)
		}
	}

	var taggedIDs map[string]bool
	if isExclude {
		ids, err := c.GetOptimizerTaggedAssetIDs(ctx)
		if err == nil {
			taggedIDs = ids
		} else {
			logger.Debug("Could not fetch tags for exclusion: %v", err)
		}
	}

	currentUserID := c.CurrentUserID()
	if currentUserID == "" {
		if me, err := c.GetMyUserWithContext(ctx); err == nil && me != nil {
			currentUserID = me.ID
		} else {
			logger.Debug("Could not fetch current user for ownership check: %v", err)
		}
	}

	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		payload := map[string]interface{}{
			"type":     "VIDEO",
			"page":     page,
			"size":     size,
			"withExif": true,
		}
		if !tAfter.IsZero() {
			payload["takenAfter"] = tAfter.Format(time.RFC3339)
		}
		if !tBeforeEnd.IsZero() {
			payload["takenBefore"] = tBeforeEnd.Format(time.RFC3339)
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

			if a.IsNotOwnedBy(currentUserID) {
				res.SkippedNotOwned++
				continue
			}

			if a.IsExternalLibrary() {
				res.SkippedExternal++
				continue
			}
			if a.IsTrashed || a.IsLivePhoto() {
				continue
			}

			if isExclude && ((taggedIDs != nil && taggedIDs[a.ID]) || a.HasTag("immich-optimizer")) {
				res.SkippedAlreadyOptimized++
				continue
			}

			if !tAfter.IsZero() || !tBeforeEnd.IsZero() {
				dateStr := a.FileCreatedAt
				if dateStr != "" {
					var parsedTime time.Time
					var parseErr error
					parsedTime, parseErr = time.Parse(time.RFC3339Nano, dateStr)
					if parseErr != nil {
						parsedTime, parseErr = time.Parse(time.RFC3339, dateStr)
					}
					if parseErr == nil {
						parsedTime = parsedTime.In(time.Local)
						if !tAfter.IsZero() && parsedTime.Before(tAfter) {
							res.SkippedDateFilter++
							continue
						}
						if !tBeforeEnd.IsZero() && parsedTime.After(tBeforeEnd) {
							res.SkippedDateFilter++
							continue
						}
					}
				}
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

// QueryVideoAssets returns video assets that exceed the given size threshold in bytes (legacy helper).
func (c *Client) QueryVideoAssets(sizeThresholdBytes int64) ([]Asset, error) {
	res, err := c.ScanVideosWithOptions(context.Background(), "total mb", float64(sizeThresholdBytes)/(1024*1024), ScanOptions{})
	if err != nil {
		return nil, err
	}
	return res.Videos, nil
}
