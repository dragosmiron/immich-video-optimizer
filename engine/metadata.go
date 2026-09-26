package engine

import (
	"context"
	"fmt"
	"os/exec"
)

// BuildExiftoolArgs constructs the arguments for copying metadata safely.
// It preserves camera make/model, GPS coords, and shooting timestamps
// but excludes internal track/rotation headers to prevent container corruption.
func BuildExiftoolArgs(original, transcoded string) []string {
	return []string{
		"-TagsFromFile", original,
		"-all:all",
		"-unsafe",
		"--Track*",
		"--Rotation",
		"-overwrite_original",
		transcoded,
	}
}

// CopyMetadata executes exiftool to copy metadata from original to transcoded.
func CopyMetadata(ctx context.Context, exiftoolPath string, original, transcoded string) error {
	args := BuildExiftoolArgs(original, transcoded)
	cmd := exec.CommandContext(ctx, exiftoolPath, args...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("exiftool failed: %w, output: %s", err, string(out))
	}
	return nil
}
