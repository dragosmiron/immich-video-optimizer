package main

import (
	"flag"
	"fmt"
	"os"

	"immich-video-optimizer/config"
	"immich-video-optimizer/orchestrator"
	"immich-video-optimizer/tui"
)

func main() {
	configFlag := flag.Bool("config", false, "Open the interactive terminal configuration form")
	flag.BoolVar(configFlag, "c", false, "Open the interactive terminal configuration form (shorthand)")
	flag.Parse()

	cfg, err := config.LoadConfig()
	if err != nil || cfg == nil {
		cfg = config.DefaultConfig()
	}

	pipeline, err := orchestrator.NewPipeline(cfg)
	if err != nil {
		fmt.Printf("Failed to initialize pipeline: %v\n", err)
		os.Exit(1)
	}

	var opts []tui.Option
	if *configFlag {
		opts = append(opts, tui.WithInitialView(tui.ViewConnectionSetup))
	}

	if err := tui.RunTUI(cfg, pipeline, opts...); err != nil {
		fmt.Printf("TUI failed: %v\n", err)
		os.Exit(1)
	}
}
