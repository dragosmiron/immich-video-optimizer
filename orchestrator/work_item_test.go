package orchestrator

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"immich-video-optimizer/immich"
)

func TestWorkItem_Cleanup(t *testing.T) {
	tempDir := t.TempDir()
	origFile := filepath.Join(tempDir, "orig_test.mp4")
	optFile := filepath.Join(tempDir, "opt_test.mp4")

	if err := os.WriteFile(origFile, []byte("original"), 0644); err != nil {
		t.Fatalf("failed to write origFile: %v", err)
	}
	if err := os.WriteFile(optFile, []byte("optimized"), 0644); err != nil {
		t.Fatalf("failed to write optFile: %v", err)
	}

	item := NewWorkItem(immich.Asset{ID: "test-1"}, origFile)
	item.TranscodedPath = optFile

	// Concurrently invoke Cleanup to verify sync.Once
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			item.Cleanup()
		}()
	}
	wg.Wait()

	if _, err := os.Stat(origFile); !os.IsNotExist(err) {
		t.Fatalf("expected origFile to be removed after Cleanup")
	}
	if _, err := os.Stat(optFile); !os.IsNotExist(err) {
		t.Fatalf("expected optFile to be removed after Cleanup")
	}
	if item.OrigPath != "" || item.TranscodedPath != "" {
		t.Errorf("expected paths to be cleared on Cleanup")
	}
}
