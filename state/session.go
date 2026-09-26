package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type VideoStatus string

const (
	StatusPending   VideoStatus = "pending"
	StatusCompleted VideoStatus = "completed"
	StatusFailed    VideoStatus = "failed"
	StatusSkipped   VideoStatus = "skipped"
)

type VideoState struct {
	ID           string      `json:"id"`
	OriginalName string      `json:"original_name"`
	OriginalSize int64       `json:"original_size"`
	Duration     string      `json:"duration"`
	Status       VideoStatus `json:"status"`
	SavedBytes   int64       `json:"saved_bytes,omitempty"`
	ErrorMessage string      `json:"error_message,omitempty"`
}

type Session struct {
	SessionID       string                `json:"session_id"`
	Date            time.Time             `json:"date"`
	TrackingAlbumID string                `json:"tracking_album_id"`
	Videos          map[string]*VideoState `json:"videos"`
	TotalVideos     int                   `json:"total_videos"`
	TotalSavedBytes int64                 `json:"total_saved_bytes"`
}

func GetSessionFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "immich-video-optimizer")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "session.json"), nil
}

func LoadSession() (*Session, error) {
	path, err := GetSessionFilePath()
	if err != nil {
		return nil, err
	}
	
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No active session
		}
		return nil, err
	}

	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func SaveSession(session *Session) error {
	path, err := GetSessionFilePath()
	if err != nil {
		return err
	}
	
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}
	
	return os.WriteFile(path, data, 0644)
}

func ClearSession() error {
	path, err := GetSessionFilePath()
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *Session) PendingCount() int {
	if s == nil || s.Videos == nil {
		return 0
	}
	count := 0
	for _, v := range s.Videos {
		if v.Status == StatusPending {
			count++
		}
	}
	return count
}

func (s *Session) CompletedCount() int {
	if s == nil || s.Videos == nil {
		return 0
	}
	count := 0
	for _, v := range s.Videos {
		if v.Status == StatusCompleted {
			count++
		}
	}
	return count
}

func (s *Session) FailedCount() int {
	if s == nil || s.Videos == nil {
		return 0
	}
	count := 0
	for _, v := range s.Videos {
		if v.Status == StatusFailed {
			count++
		}
	}
	return count
}

func (s *Session) PendingVideos() []*VideoState {
	if s == nil || s.Videos == nil {
		return nil
	}
	var list []*VideoState
	for _, v := range s.Videos {
		if v.Status == StatusPending {
			list = append(list, v)
		}
	}
	return list
}

