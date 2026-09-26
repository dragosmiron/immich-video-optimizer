package main

import (
	"flag"
	"fmt"
	"os"

	"immich-video-optimizer/config"
	"immich-video-optimizer/orchestrator"
	"immich-video-optimizer/tui"
	"immich-video-optimizer/ui"
)

func main() {
	configFlag := flag.Bool("config", false, "Open the interactive terminal configuration form")
	flag.BoolVar(configFlag, "c", false, "Open the interactive terminal configuration form (shorthand)")
	flag.Parse()

	fmt.Println("🎬 Immich Video Optimizer CLI")

	cfg, err := config.LoadConfig()
	needsSetup := *configFlag || err != nil || cfg == nil || cfg.URL == "" || cfg.APIKey == ""

	if needsSetup {
		if *configFlag {
			fmt.Println("Opening configuration setup...")
		} else {
			fmt.Println("Configuration not found or incomplete. Starting setup...")
		}
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

	fmt.Println("Setup complete! Starting interactive TUI...")

	pipeline, err := orchestrator.NewPipeline(cfg)
	if err != nil {
		fmt.Printf("Failed to initialize pipeline: %v\n", err)
		os.Exit(1)
	}

	if err := tui.RunTUI(cfg, pipeline); err != nil {
		fmt.Printf("TUI failed: %v\n", err)
		os.Exit(1)
	}
}
