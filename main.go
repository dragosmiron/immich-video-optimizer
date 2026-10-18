package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"immich-video-optimizer/config"
	"immich-video-optimizer/logger"
	"immich-video-optimizer/orchestrator"
	"immich-video-optimizer/tui"
	"immich-video-optimizer/version"
)

func main() {
	configFlag := flag.Bool("config", false, "Open the interactive terminal configuration form")
	flag.BoolVar(configFlag, "c", false, "Open the interactive terminal configuration form (shorthand)")
	versionFlag := flag.Bool("version", false, "Print version information and check for updates")
	flag.BoolVar(versionFlag, "v", false, "Print version information and check for updates (shorthand)")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("immich-video-optimizer %s\n", version.Current)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		info, err := version.CheckLatestRelease(ctx, version.ReleaseRepo, version.Current)
		if err == nil && info != nil && info.HasUpdate {
			fmt.Printf("✨ New version available: %s (current: %s)\n", info.LatestVersion, info.CurrentVersion)
			fmt.Printf("Download at: %s\n", info.ReleaseURL)
		}
		return
	}

	cfg, err := config.LoadConfig()
	if err != nil || cfg == nil {
		cfg = config.DefaultConfig()
	}

	logger.SetLevelString(cfg.LogLevel)
	logger.SetRotationConfig(logger.RotationConfig{
		MaxSizeMB:  cfg.LogMaxSizeMB,
		MaxBackups: cfg.LogMaxBackups,
		Compress:   cfg.LogCompress,
	})

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
