package orchestrator

import (
	"immich-video-optimizer/engine"
	"immich-video-optimizer/immich"
)


type PipelineStartMsg struct {
	Total int
}

type PipelineVideoStartMsg struct {
	VideoID    string
	VideoName  string
	Size       int64
	Resolution string
}

type PipelineStageMsg struct {
	Stage     string // "downloading", "transcoding", "uploading", "probing", "idle"
	VideoName string
}

type PipelineProgressMsg struct {
	VideoID        string
	EngineProgress engine.Progress
}

type PipelineVideoCompleteMsg struct {
	VideoID       string
	VideoName     string
	OriginalSize  int64
	OptimizedSize int64
	SavedBytes    int64
	Skipped       bool
	SkipReason    string
	Err           error
}

type PipelineDoneMsg struct {
	TotalSavedBytes int64
	Err             error
	Paused          bool // true when the user cancelled via Q; session is preserved for resume
}

// DryRunVideoMsg is sent once per video after its codec probe completes.
type DryRunVideoMsg struct {
	VideoID    string
	VideoName  string
	IsHEVC     bool
	Codec      string
	Resolution string
	Probed     int // running count of probed videos so far
	Total      int // total videos being probed
	Err        error
}

// DryRunCompleteMsg is sent when all probes have finished.
// ToProcess contains videos that will actually be transcoded.
// HEVCSkipped contains videos that will be skipped because they are already HEVC.
// ProbeFailed contains videos whose probe encountered an error or timeout.
// VideoDetails maps asset ID to parsed codec and resolution info.
type DryRunCompleteMsg struct {
	ToProcess       []immich.Asset
	HEVCSkipped     []immich.Asset
	ProbeFailed     []immich.Asset
	VideoDetails    map[string]engine.ProbeDetails
	DryRunAlbumName string
	DryRunAlbumID   string
}

