package orchestrator

import (
	"os"
	"sync"

	"immich-video-optimizer/immich"
)

// WorkItem represents a video asset undergoing the optimization pipeline,
// with self-cleaning temporary file management.
type WorkItem struct {
	Asset          immich.Asset
	OrigPath       string
	TranscodedPath string
	OptimizedSize  int64
	SavedBytes     int64
	Skipped        bool
	SkipReason     string
	Err            error

	cleanupOnce sync.Once
}

// NewWorkItem initializes a WorkItem for a given asset.
func NewWorkItem(asset immich.Asset, origPath string) *WorkItem {
	return &WorkItem{
		Asset:    asset,
		OrigPath: origPath,
	}
}

// Cleanup ensures temporary files (orig_* and opt_*) are deleted exactly once.
func (w *WorkItem) Cleanup() {
	if w == nil {
		return
	}
	w.cleanupOnce.Do(func() {
		if w.OrigPath != "" {
			_ = os.Remove(w.OrigPath)
			w.OrigPath = ""
		}
		if w.TranscodedPath != "" {
			_ = os.Remove(w.TranscodedPath)
			w.TranscodedPath = ""
		}
	})
}
