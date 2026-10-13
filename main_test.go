package main

import (
	"os"
	"testing"

	"immich-video-optimizer/config"
)

func TestMain(m *testing.M) {
	tempDir, err := os.MkdirTemp("", "main-test-*")
	if err == nil {
		os.Setenv("HOME", tempDir)
		defer os.RemoveAll(tempDir)
	}
	os.Exit(m.Run())
}

func TestConfigOverwrite(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HOME", tempDir)

	cfg := &config.Config{
		URL:           "http://test1",
		APIKey:        "test-key1",
		SizeThreshold: "100",
	}

	err := config.SaveConfig(cfg)
	if err != nil {
		t.Fatalf("SaveConfig 1 failed: %v", err)
	}

	// Try saving again to test overwrite
	cfg.URL = "http://test2"
	err = config.SaveConfig(cfg)
	if err != nil {
		t.Fatalf("SaveConfig 2 failed: %v", err)
	}

	loaded, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if loaded.URL != "http://test2" {
		t.Errorf("URL mismatch after overwrite: expected http://test2, got %s", loaded.URL)
	}
}
