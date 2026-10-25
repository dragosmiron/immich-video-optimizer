package immich

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// APIError represents an error returned by the Immich API
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error %d: %s", e.StatusCode, e.Message)
}

// QueryResult holds queried videos and statistics on skipped items
type QueryResult struct {
	Videos                  []Asset
	TotalAssetsScanned      int
	SkippedNonVideo         int
	SkippedBelowSize        int
	SkippedAlreadyOptimized int
	SkippedExternal         int
	SkippedDateFilter       int
	SkippedNotOwned         int
}

// ScanOptions defines filtering options when scanning assets
type ScanOptions struct {
	ExcludeOptimized bool
	DateAfter        string // YYYY-MM-DD
	DateBefore       string // YYYY-MM-DD
}

// Tag represents an Immich tag entity
type Tag struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// AssetStack represents an Immich stack grouping
type AssetStack struct {
	ID             string `json:"id"`
	PrimaryAssetID string `json:"primaryAssetId"`
}

type ExifInfo struct {
	FileSizeInByte  int64           `json:"fileSizeInByte"`
	ExifImageWidth  int             `json:"exifImageWidth,omitempty"`
	ExifImageHeight int             `json:"exifImageHeight,omitempty"`
	Duration        json.RawMessage `json:"duration,omitempty"`
}

// AssetOwner represents an asset owner
type AssetOwner struct {
	ID string `json:"id"`
}

// User represents an Immich user
type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// Asset represents an Immich asset
type Asset struct {
	ID               string          `json:"id"`
	OwnerID          string          `json:"ownerId,omitempty"`
	Owner            *AssetOwner     `json:"owner,omitempty"`
	Type             string          `json:"type"`
	Size             int64           `json:"size"`
	Duration         json.RawMessage `json:"duration,omitempty"`
	DeviceAssetID    string          `json:"deviceAssetId,omitempty"`
	DeviceID         string          `json:"deviceId,omitempty"`
	FileCreatedAt    string          `json:"fileCreatedAt,omitempty"`
	FileModifiedAt   string          `json:"fileModifiedAt,omitempty"`
	OriginalFileName string          `json:"originalFileName,omitempty"`
	Description      string          `json:"description,omitempty"`
	IsFavorite       bool            `json:"isFavorite"`
	IsReadOnly       bool            `json:"isReadOnly"`
	IsExternal       bool            `json:"isExternal"`
	IsTrashed        bool            `json:"isTrashed"`
	Visibility       string          `json:"visibility,omitempty"`
	LivePhotoVideoID *string         `json:"livePhotoVideoId,omitempty"`
	LibraryID        *string         `json:"libraryId,omitempty"`
	ExifInfo         ExifInfo        `json:"exifInfo,omitempty"`
	Tags             []Tag           `json:"tags,omitempty"`
	Stack            *AssetStack     `json:"stack,omitempty"`
}

// GetOwnerID returns the asset's owner ID from OwnerID or Owner.ID.
func (a *Asset) GetOwnerID() string {
	if a == nil {
		return ""
	}
	if a.OwnerID != "" {
		return a.OwnerID
	}
	if a.Owner != nil && a.Owner.ID != "" {
		return a.Owner.ID
	}
	return ""
}

// IsNotOwnedBy returns true if the asset has a known owner ID, userID is non-empty,
// and they do not match (i.e. the asset is partner-shared and not owned by userID).
func (a *Asset) IsNotOwnedBy(userID string) bool {
	if a == nil {
		return false
	}
	ownerID := a.GetOwnerID()
	return ownerID != "" && userID != "" && ownerID != userID
}

// IsLivePhoto returns true if the asset is a Live Photo still image or its hidden motion video component.
func (a *Asset) IsLivePhoto() bool {
	return a.Visibility == "hidden" || (a.LivePhotoVideoID != nil && *a.LivePhotoVideoID != "")
}

// HasTag returns true if the asset has a tag with the specified name (case-insensitive).
func (a *Asset) HasTag(name string) bool {
	for _, t := range a.Tags {
		if strings.EqualFold(strings.TrimSpace(t.Name), name) {
			return true
		}
	}
	return false
}

// IsExternalLibrary returns true if the asset belongs to an external library
// (e.g. read-only mount or external storage path) rather than internal uploads.
func (a *Asset) IsExternalLibrary() bool {
	if a.IsReadOnly || a.IsExternal {
		return true
	}
	if a.LibraryID != nil && *a.LibraryID != "" {
		return true
	}
	return false
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
		// shortcut: values >86400 assumed milliseconds; videos longer than 24h 0m 1s are misread as ~86s
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
