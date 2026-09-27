package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoadConfig(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HOME", tempDir)

	// Ensure the config file doesn't exist initially
	_, err := LoadConfig()
	if err == nil {
		t.Errorf("Expected error loading non-existent config")
	}

	cfg := &Config{
		URL:                "http://localhost:8080",
		APIKey:             "secret-key",
		SizeThreshold:      "1024",
		SkipHEVC:           true,
		ConcurrentEncoders: 2,
		EnablePrefetch:     true,
	}

	// Test SaveConfig
	err = SaveConfig(cfg)
	if err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	// Verify file was created
	configPath := filepath.Join(tempDir, ".config", "immich-video-optimizer", "config.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatalf("Config file was not created at expected path: %s", configPath)
	}

	// Test LoadConfig
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if loaded.URL != cfg.URL || loaded.APIKey != cfg.APIKey || loaded.SizeThreshold != cfg.SizeThreshold || loaded.SkipHEVC != cfg.SkipHEVC || loaded.ConcurrentEncoders != cfg.ConcurrentEncoders || loaded.EnablePrefetch != cfg.EnablePrefetch {
		t.Errorf("Loaded config mismatch: got %v, expected %v", loaded, cfg)
	}
}

func TestSaveConfig_UserHomeDirError(t *testing.T) {
	orig := userHomeDir
	defer func() { userHomeDir = orig }()

	userHomeDir = func() (string, error) {
		return "", os.ErrNotExist
	}

	cfg := &Config{}
	err := SaveConfig(cfg)
	if err == nil {
		t.Errorf("Expected error when userHomeDir fails")
	}
}

func TestSaveConfig_MkdirError(t *testing.T) {
	tempDir := t.TempDir()

	orig := userHomeDir
	defer func() { userHomeDir = orig }()

	userHomeDir = func() (string, error) {
		return filepath.Join(tempDir, "readonly"), nil
	}

	// Create a read-only file where the home dir would be, so MkdirAll fails
	filePath := filepath.Join(tempDir, "readonly")
	err := os.WriteFile(filePath, []byte("data"), 0644)
	if err != nil {
		t.Fatalf("Failed to create file for test: %v", err)
	}

	cfg := &Config{}
	err = SaveConfig(cfg)
	if err == nil {
		t.Errorf("Expected error when MkdirAll fails")
	}
}
