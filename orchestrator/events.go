package orchestrator

import "immich-video-optimizer/engine"

type PipelineStartMsg struct {
	Total int
}

type PipelineVideoStartMsg struct {
	VideoID string
	Size    int64
}

type PipelineProgressMsg struct {
	VideoID string
	EngineProgress engine.Progress
}

type PipelineVideoCompleteMsg struct {
	VideoID    string
	SavedBytes int64
	Skipped    bool
	Err        error
}

type PipelineDoneMsg struct {
	TotalSavedBytes int64
	Err             error
}
