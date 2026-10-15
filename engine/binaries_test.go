package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindOrExtractBinary_IgnoresEmptyOrCorruptedFile(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("PATH", t.TempDir())

	// Simulate an interrupted download leaving an empty or truncated binary
	corruptPath := filepath.Join(cacheDir, "ffmpeg")
	if err := os.WriteFile(corruptPath, []byte(""), 0755); err != nil {
		t.Fatalf("Failed to create dummy file: %v", err)
	}

	// findOrExtractBinary should not accept an empty/corrupted binary
	_, err := findOrExtractBinary("ffmpeg", cacheDir)
	if err == nil {
		t.Fatalf("Expected error when cached binary is empty/corrupt, but got success")
	}
}
