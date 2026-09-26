package main

import (
	"context"
	"fmt"
	"os"

	"immich-video-optimizer/config"
	"immich-video-optimizer/orchestrator"
	"immich-video-optimizer/ui"
)

func main() {
	fmt.Println("🎬 Immich Video Optimizer CLI")

	cfg, err := config.LoadConfig()
	if err != nil || cfg.URL == "" || cfg.APIKey == "" {
		fmt.Println("Configuration not found or incomplete. Starting setup...")
		if cfg == nil {
			cfg = &config.Config{
				SizeThreshold: "500",
			}
		}

		err := ui.RunSetup(cfg)
		if err != nil {
			fmt.Printf("Setup cancelled or failed: %v\n", err)
			os.Exit(1)
		}

		err = config.SaveConfig(cfg)
		if err != nil {
			fmt.Printf("Failed to save configuration: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Configuration saved successfully!")
	} else {
		fmt.Printf("Loaded configuration.\nServer URL: %s\nThreshold: %s MB\n", cfg.URL, cfg.SizeThreshold)
	}

	// Ready for Phase 2
	fmt.Println("Setup complete! Starting optimization pipeline...")

	pipeline, err := orchestrator.NewPipeline(cfg)
	if err != nil {
		fmt.Printf("Failed to initialize pipeline: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	if err := pipeline.Run(ctx); err != nil {
		fmt.Printf("Pipeline failed: %v\n", err)
		os.Exit(1)
	}
}
