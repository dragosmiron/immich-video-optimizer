package state

import (
	"fmt"
	"sync"
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

func TestSessionTransitionsAndConcurrency(t *testing.T) {
	setupTestEnv(t)

	sess := &Session{
		SessionID: "concurrent-sess",
		Videos:    make(map[string]*VideoState),
	}

	// Add videos
	sess.AddOrUpdateVideo(&VideoState{ID: "v1", OriginalName: "video1.mp4", Status: StatusPending})
	sess.AddOrUpdateVideo(&VideoState{ID: "v2", OriginalName: "video2.mp4", Status: StatusPending})
	sess.AddOrUpdateVideo(&VideoState{ID: "v3", OriginalName: "video3.mp4", Status: StatusFailed, ErrorMessage: "previous error"})

	// Verify v3 reset to Pending
	v3, ok := sess.GetVideo("v3")
	if !ok || v3.Status != StatusPending || v3.ErrorMessage != "" {
		t.Errorf("expected v3 status pending and empty error message, got: %+v", v3)
	}

	if sess.PendingCount() != 3 {
		t.Errorf("expected 3 pending, got %d", sess.PendingCount())
	}

	// Test transitions
	sess.MarkCompleted("v1", 1000)
	sess.MarkSkipped("v2", "already HEVC")
	sess.MarkFailed("v3", "transcode failed")

	if sess.CompletedCount() != 1 {
		t.Errorf("expected 1 completed, got %d", sess.CompletedCount())
	}
	if sess.FailedCount() != 1 {
		t.Errorf("expected 1 failed, got %d", sess.FailedCount())
	}
	if sess.PendingCount() != 0 {
		t.Errorf("expected 0 pending, got %d", sess.PendingCount())
	}
	if sess.TotalSavedBytes != 1000 {
		t.Errorf("expected 1000 total saved bytes, got %d", sess.TotalSavedBytes)
	}

	// Test concurrent access (reads, transitions, saves)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			vID := fmt.Sprintf("cv-%d", id)
			sess.AddOrUpdateVideo(&VideoState{ID: vID, OriginalName: vID + ".mp4", Status: StatusPending})
			_ = sess.PendingCount()
			_ = sess.CompletedCount()
			sess.MarkCompleted(vID, int64(id*10))
			_ = sess.PendingVideos()
			_ = SaveSession(sess)
		}(i)
	}
	wg.Wait()

	if len(sess.Videos) < 20 {
		t.Fatalf("expected at least 20 videos in memory, got %d", len(sess.Videos))
	}

	if err := SaveSession(sess); err != nil {
		t.Fatalf("failed to save final session: %v", err)
	}

	loaded, err := LoadSession()
	if err != nil {
		t.Fatalf("failed to load session after concurrent operations: %v", err)
	}
	if loaded == nil || len(loaded.Videos) < 20 {
		t.Fatalf("expected at least 20 videos, got %v", loaded)
	}
}

