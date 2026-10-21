package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
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
	ID            string      `json:"id"`
	OriginalName  string      `json:"original_name"`
	OriginalSize  int64       `json:"original_size"`
	Duration      string      `json:"duration"`
	Status        VideoStatus `json:"status"`
	SavedBytes    int64       `json:"saved_bytes,omitempty"`
	ErrorMessage  string      `json:"error_message,omitempty"`
	Width         int         `json:"width,omitempty"`
	Height        int         `json:"height,omitempty"`
	CreatedAt     string      `json:"created_at,omitempty"`
	ModifiedAt    string      `json:"modified_at,omitempty"`
	DeviceAssetID string      `json:"device_asset_id,omitempty"`
	DeviceID      string      `json:"device_id,omitempty"`
	IsFavorite    bool        `json:"is_favorite,omitempty"`
}

type Session struct {
	mu              sync.RWMutex
	SessionID       string                 `json:"session_id"`
	Date            time.Time              `json:"date"`
	TrackingAlbumID string                 `json:"tracking_album_id"`
	Videos          map[string]*VideoState `json:"videos"`
	TotalVideos     int                    `json:"total_videos"`
	TotalSavedBytes int64                  `json:"total_saved_bytes"`
}

func GetSessionFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "immich-video-optimizer")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	_ = os.Chmod(dir, 0700)
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

	if len(data) == 0 {
		return nil, nil
	}

	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func SaveSession(session *Session) error {
	if session == nil {
		return ClearSession()
	}

	path, err := GetSessionFilePath()
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0700)

	session.mu.RLock()
	data, err := json.MarshalIndent(session, "", "  ")
	session.mu.RUnlock()
	if err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(dir, "session-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}

	return os.Rename(tmpName, path)
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

// AddOrUpdateVideo adds a new video state (defaulting to StatusPending) or resets a failed video to pending.
func (s *Session) AddOrUpdateVideo(v *VideoState) {
	if s == nil || v == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Videos == nil {
		s.Videos = make(map[string]*VideoState)
	}

	if existing, exists := s.Videos[v.ID]; exists {
		if existing.Status == StatusFailed {
			existing.Status = StatusPending
			existing.ErrorMessage = ""
		}
	} else {
		copyState := *v
		if copyState.Status == "" || copyState.Status == StatusFailed {
			copyState.Status = StatusPending
			copyState.ErrorMessage = ""
		}
		s.Videos[v.ID] = &copyState
	}
}

// MarkCompleted marks a video as completed and adds savedBytes to session total.
func (s *Session) MarkCompleted(videoID string, savedBytes int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if v, exists := s.Videos[videoID]; exists {
		v.Status = StatusCompleted
		v.SavedBytes = savedBytes
		s.TotalSavedBytes += savedBytes
	}
}

// MarkFailed marks a video as failed with an error message.
func (s *Session) MarkFailed(videoID string, errMsg string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if v, exists := s.Videos[videoID]; exists {
		v.Status = StatusFailed
		v.ErrorMessage = errMsg
	}
}

// MarkSkipped marks a video as skipped with a reason.
func (s *Session) MarkSkipped(videoID string, reason string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if v, exists := s.Videos[videoID]; exists {
		v.Status = StatusSkipped
		v.ErrorMessage = reason
	}
}

// GetVideo returns a copy of the VideoState for the given ID.
func (s *Session) GetVideo(videoID string) (*VideoState, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	if v, exists := s.Videos[videoID]; exists {
		copyState := *v
		return &copyState, true
	}
	return nil, false
}

// IsVideoDone returns true if video is already completed or skipped.
func (s *Session) IsVideoDone(videoID string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.Videos == nil {
		return false
	}
	if v, exists := s.Videos[videoID]; exists {
		return v.Status == StatusCompleted || v.Status == StatusSkipped
	}
	return false
}

// GetTotalSavedBytes returns the current total saved bytes thread-safely.
func (s *Session) GetTotalSavedBytes() int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.TotalSavedBytes
}

// PendingCount returns the number of pending videos.
func (s *Session) PendingCount() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.Videos == nil {
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

// CompletedCount returns the number of completed videos.
func (s *Session) CompletedCount() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.Videos == nil {
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

// FailedCount returns the number of failed videos.
func (s *Session) FailedCount() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.Videos == nil {
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

// PendingVideos returns a list of pending videos.
func (s *Session) PendingVideos() []*VideoState {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.Videos == nil {
		return nil
	}
	var list []*VideoState
	for _, v := range s.Videos {
		if v.Status == StatusPending {
			copyV := *v
			list = append(list, &copyV)
		}
	}
	return list
}
