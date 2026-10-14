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

	// Test case-insensitive normalization
	cases := []struct {
		input    string
		expected string
	}{
		{"disabled", ThresholdModeDisabled},
		{"DISABLED", ThresholdModeDisabled},
		{"total mb", ThresholdModeTotalMB,},
		{"TOTAL MB", ThresholdModeTotalMB},
		{"mb/min", ThresholdModeMBPerMin},
		{"MB/MIN", ThresholdModeMBPerMin},
	}
	for _, tc := range cases {
		c := &Config{
			URL:           "http://localhost:8080",
			APIKey:        "key",
			ThresholdMode: tc.input,
			SizeThreshold: "100",
		}
		if err := SaveConfig(c); err != nil {
			t.Fatalf("SaveConfig failed for %q: %v", tc.input, err)
		}
		loaded, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig failed for %q: %v", tc.input, err)
		}
		if loaded.ThresholdMode != tc.expected {
			t.Errorf("expected normalized mode %q for input %q, got %q", tc.expected, tc.input, loaded.ThresholdMode)
		}
	}
}

func TestConfig_VideoQualityMapping(t *testing.T) {
	expectedCRF := map[int]int{
		1: 32, 2: 31, 3: 30, 4: 29, 5: 28,
		6: 26, 7: 24, 8: 22, 9: 20, 10: 18,
	}
	expectedVT := map[int]int{
		1: 50, 2: 53, 3: 56, 4: 59, 5: 62,
		6: 66, 7: 72, 8: 76, 9: 80, 10: 85,
	}
	expectedLabel := map[int]string{
		1: "Max Savings", 2: "High Compression", 3: "High Compression",
		4: "Balanced", 5: "Balanced", 6: "Balanced",
		7: "High Quality", 8: "High Quality",
		9: "Visually Lossless", 10: "Visually Lossless",
	}

	for lvl := 1; lvl <= 10; lvl++ {
		cfg := &Config{VideoQuality: lvl}
		if got := cfg.Quality(); got != lvl {
			t.Errorf("level %d: Quality() = %d, expected %d", lvl, got, lvl)
		}
		if got := cfg.QualityCRF(); got != expectedCRF[lvl] {
			t.Errorf("level %d: QualityCRF() = %d, expected %d", lvl, got, expectedCRF[lvl])
		}
		if got := cfg.QualityVideoToolbox(); got != expectedVT[lvl] {
			t.Errorf("level %d: QualityVideoToolbox() = %d, expected %d", lvl, got, expectedVT[lvl])
		}
		if got := cfg.QualityLabel(); got != expectedLabel[lvl] {
			t.Errorf("level %d: QualityLabel() = %q, expected %q", lvl, got, expectedLabel[lvl])
		}

		desc := cfg.QualityDescription()
		switch {
		case lvl <= 3:
			if desc != "High Compression (~70-80% space savings, noticeable softening for surveillance/dashcams)" {
				t.Errorf("level %d: unexpected description %q", lvl, desc)
			}
		case lvl <= 6:
			if desc != "Balanced (Default, ~40-50% savings, imperceptible quality loss)" {
				t.Errorf("level %d: unexpected description %q", lvl, desc)
			}
		case lvl <= 8:
			if desc != "High Quality (~25-35% savings, preserves fine film grain and high-motion details)" {
				t.Errorf("level %d: unexpected description %q", lvl, desc)
			}
		default:
			if desc != "Visually Lossless (~15-20% savings, archival preservation)" {
				t.Errorf("level %d: unexpected description %q", lvl, desc)
			}
		}
	}

	// Boundary and clamping tests
	clampTests := []struct {
		name       string
		cfg        *Config
		expectedQ  int
		expectedC  int
		expectedVT int
	}{
		{"nil config", nil, 6, 26, 66},
		{"zero quality", &Config{VideoQuality: 0}, 6, 26, 66},
		{"negative quality", &Config{VideoQuality: -3}, 1, 32, 50},
		{"too high quality", &Config{VideoQuality: 15}, 10, 18, 85},
	}

	for _, tt := range clampTests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.Quality(); got != tt.expectedQ {
				t.Errorf("Quality() = %d, expected %d", got, tt.expectedQ)
			}
			if got := tt.cfg.QualityCRF(); got != tt.expectedC {
				t.Errorf("QualityCRF() = %d, expected %d", got, tt.expectedC)
			}
			if got := tt.cfg.QualityVideoToolbox(); got != tt.expectedVT {
				t.Errorf("QualityVideoToolbox() = %d, expected %d", got, tt.expectedVT)
			}
		})
	}
}

func TestConfig_VideoQualityPersistence(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HOME", tempDir)

	cfg := &Config{
		URL:           "http://localhost:2283",
		APIKey:        "my-key",
		VideoQuality:  8,
		SizeThreshold: "100",
	}

	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if loaded.VideoQuality != 8 {
		t.Errorf("expected loaded VideoQuality 8, got %d", loaded.VideoQuality)
	}
	if loaded.QualityCRF() != 22 {
		t.Errorf("expected loaded QualityCRF 22, got %d", loaded.QualityCRF())
	}
	if loaded.QualityVideoToolbox() != 76 {
		t.Errorf("expected loaded QualityVideoToolbox 76, got %d", loaded.QualityVideoToolbox())
	}
}


