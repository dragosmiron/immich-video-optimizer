package orchestrator

import "immich-video-optimizer/engine"

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
	VideoID    string
	VideoName  string
	SavedBytes int64
	Skipped    bool
	SkipReason string
	Err        error
}

type PipelineDoneMsg struct {
	TotalSavedBytes int64
	Err             error
	Paused          bool // true when the user cancelled via Q; session is preserved for resume
}
