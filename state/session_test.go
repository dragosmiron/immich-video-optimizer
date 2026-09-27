package state

import (
	"testing"
	"time"
)

func setupTestEnv(t *testing.T) string {
	// Create a temporary home directory
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome) // This sets HOME for the current test
	return tempHome
}

func TestSessionSaveLoadClear(t *testing.T) {
	setupTestEnv(t)

	// Test LoadSession when file doesn't exist
	session, err := LoadSession()
	if err != nil {
		t.Fatalf("Expected nil error when session doesn't exist, got %v", err)
	}
	if session != nil {
		t.Fatalf("Expected nil session when file doesn't exist, got %v", session)
	}

	// Create and save a new session
	newSession := &Session{
		SessionID:       "test-session-123",
		Date:            time.Now(),
		TrackingAlbumID: "album-456",
		Videos: map[string]*VideoState{
			"vid1": {
				ID:            "vid1",
				OriginalName:  "video1.mp4",
				OriginalSize:  104857600,
				Status:        StatusPending,
				Width:         3840,
				Height:        2160,
				CreatedAt:     "2026-01-01T12:00:00Z",
				ModifiedAt:    "2026-01-01T12:00:00Z",
				DeviceAssetID: "dev-asset-1",
				DeviceID:      "phone-1",
			},
		},
		TotalVideos: 1,
	}

	err = SaveSession(newSession)
	if err != nil {
		t.Fatalf("Failed to save session: %v", err)
	}

	// Test LoadSession when file exists
	loadedSession, err := LoadSession()
	if err != nil {
		t.Fatalf("Failed to load session: %v", err)
	}
	if loadedSession == nil {
		t.Fatalf("Expected loaded session to not be nil")
	}
	if loadedSession.SessionID != newSession.SessionID {
		t.Errorf("Expected SessionID %s, got %s", newSession.SessionID, loadedSession.SessionID)
	}
	if len(loadedSession.Videos) != 1 {
		t.Errorf("Expected 1 video, got %d", len(loadedSession.Videos))
	}
	v1 := loadedSession.Videos["vid1"]
	if v1.Width != 3840 || v1.Height != 2160 || v1.CreatedAt != "2026-01-01T12:00:00Z" || v1.DeviceAssetID != "dev-asset-1" {
		t.Errorf("Metadata fields were not preserved in loaded session: %+v", v1)
	}

	// Test ClearSession
	err = ClearSession()
	if err != nil {
		t.Fatalf("Failed to clear session: %v", err)
	}

	// Verify it's cleared
	session, err = LoadSession()
	if err != nil {
		t.Fatalf("Expected nil error after clear, got %v", err)
	}
	if session != nil {
		t.Fatalf("Expected nil session after clear, got %v", session)
	}
}
