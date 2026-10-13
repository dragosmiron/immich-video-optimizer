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
		ThresholdMode:      ThresholdModeTotalMB,
		SizeThreshold:      "1024",
		SkipHEVC:           true,
		ConcurrentEncoders: 2,
		EnablePrefetch:     true,
		LogLevel:           "debug",
		MaxRetries:         5,
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

	if loaded.URL != cfg.URL || loaded.APIKey != cfg.APIKey || loaded.ThresholdMode != cfg.ThresholdMode || loaded.SizeThreshold != cfg.SizeThreshold || loaded.SkipHEVC != cfg.SkipHEVC || loaded.ConcurrentEncoders != cfg.ConcurrentEncoders || loaded.EnablePrefetch != cfg.EnablePrefetch || loaded.LogLevel != cfg.LogLevel || loaded.MaxRetries != cfg.MaxRetries {
		t.Errorf("Loaded config mismatch: got %v, expected %v", loaded, cfg)
	}
	if !loaded.IsDebug() {
		t.Errorf("expected IsDebug() true for debug level")
	}
	if loaded.MaxRetries != 5 {
		t.Errorf("expected MaxRetries 5, got %d", loaded.MaxRetries)
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

func TestConfig_SizeThresholdBytes(t *testing.T) {
	tests := []struct {
		threshold string
		expected  int64
	}{
		{"500", 500 * 1024 * 1024},
		{"1024", 1024 * 1024 * 1024},
		{"0", 0},
		{"", 0},
		{"invalid", 0},
		{"-10", 0},
	}

	for _, tt := range tests {
		cfg := &Config{SizeThreshold: tt.threshold}
		if got := cfg.SizeThresholdBytes(); got != tt.expected {
			t.Errorf("SizeThresholdBytes() for %q = %d, expected %d", tt.threshold, got, tt.expected)
		}
	}

	var nilCfg *Config
	if got := nilCfg.SizeThresholdBytes(); got != 0 {
		t.Errorf("nil Config SizeThresholdBytes() = %d, expected 0", got)
	}
}

func TestConfig_SizeThresholdMBPerMin(t *testing.T) {
	tests := []struct {
		threshold string
		expected  float64
	}{
		{"100", 100},
		{"120.5", 120.5},
		{"0", 0},
		{"", 0},
		{"invalid", 0},
		{"-10", 0},
	}

	for _, tt := range tests {
		cfg := &Config{SizeThreshold: tt.threshold}
		if got := cfg.SizeThresholdMBPerMin(); got != tt.expected {
			t.Errorf("SizeThresholdMBPerMin() for %q = %f, expected %f", tt.threshold, got, tt.expected)
		}
	}

	var nilCfg *Config
	if got := nilCfg.SizeThresholdMBPerMin(); got != 0 {
		t.Errorf("nil Config SizeThresholdMBPerMin() = %f, expected 0", got)
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.ThresholdMode != ThresholdModeMBPerMin {
		t.Errorf("expected default ThresholdMode %q, got %q", ThresholdModeMBPerMin, cfg.ThresholdMode)
	}
	if !cfg.SizeGuard {
		t.Errorf("expected default SizeGuard true")
	}
	if !cfg.SkipHEVC {
		t.Errorf("expected default SkipHEVC true")
	}
	if !cfg.EnablePrefetch {
		t.Errorf("expected default EnablePrefetch true")
	}
	if cfg.MaxRetries != 2 {
		t.Errorf("expected default MaxRetries 2, got %d", cfg.MaxRetries)
	}
	if cfg.SizeThreshold != "100" {
		t.Errorf("expected default SizeThreshold 100, got %s", cfg.SizeThreshold)
	}
}

func TestConfig_ThresholdModes(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HOME", tempDir)

	for _, mode := range []string{ThresholdModeMBPerMin, ThresholdModeTotalMB, ThresholdModeDisabled} {
		cfg := &Config{
			URL:           "http://localhost:8080",
			APIKey:        "key",
			ThresholdMode: mode,
			SizeThreshold: "250",
		}
		if err := SaveConfig(cfg); err != nil {
			t.Fatalf("SaveConfig failed for mode %s: %v", mode, err)
		}
		loaded, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig failed for mode %s: %v", mode, err)
		}
		if loaded.ThresholdMode != mode {
			t.Errorf("expected ThresholdMode %s, got %s", mode, loaded.ThresholdMode)
		}
	}

	// Test invalid mode falls back to MB/min
	invalidCfg := &Config{
		URL:           "http://localhost:8080",
		APIKey:        "key",
		ThresholdMode: "InvalidMode",
		SizeThreshold: "100",
	}
	if err := SaveConfig(invalidCfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if loaded.ThresholdMode != ThresholdModeMBPerMin {
		t.Errorf("expected fallback to %s on invalid mode, got %s", ThresholdModeMBPerMin, loaded.ThresholdMode)
	}
}

