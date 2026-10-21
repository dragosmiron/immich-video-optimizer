package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

var userHomeDir = os.UserHomeDir

const (
	ThresholdModeMBPerMin = "MB/min"
	ThresholdModeTotalMB  = "Total MB"
	ThresholdModeDisabled = "Disabled"
)

type Config struct {
	URL                string `mapstructure:"url"`
	APIKey             string `mapstructure:"api_key"`
	ThresholdMode      string `mapstructure:"threshold_mode"`
	SizeThreshold      string `mapstructure:"size_threshold"`
	Resolution         string `mapstructure:"resolution"`
	Preset             string `mapstructure:"preset"`
	VideoQuality       int    `mapstructure:"video_quality"`
	SizeGuard          bool   `mapstructure:"size_guard"`
	SkipHEVC           bool   `mapstructure:"skip_hevc"`
	ConcurrentEncoders int    `mapstructure:"concurrent_encoders"`
	EnablePrefetch     bool   `mapstructure:"enable_prefetch"`
	LogLevel           string `mapstructure:"log_level"`
	MaxRetries         int    `mapstructure:"max_retries"`
	ExcludeOptimized   bool   `mapstructure:"exclude_optimized"`
	DateAfter          string `mapstructure:"date_after"`
	DateBefore         string `mapstructure:"date_before"`
	CustomFFmpegFlags  string `mapstructure:"custom_ffmpeg_flags"`
	LogMaxSizeMB       int    `mapstructure:"log_max_size_mb"`
	LogMaxBackups      int    `mapstructure:"log_max_backups"`
	LogCompress        bool   `mapstructure:"log_compress"`
}

// AllConfigKeys lists every valid configuration key for validation and auto-healing.
var AllConfigKeys = []string{
	"url",
	"api_key",
	"threshold_mode",
	"size_threshold",
	"resolution",
	"preset",
	"video_quality",
	"size_guard",
	"skip_hevc",
	"concurrent_encoders",
	"enable_prefetch",
	"log_level",
	"max_retries",
	"exclude_optimized",
	"date_after",
	"date_before",
	"custom_ffmpeg_flags",
	"log_max_size_mb",
	"log_max_backups",
	"log_compress",
}

// IsDebug returns true if the log level is set to debug.
func (c *Config) IsDebug() bool {
	if c == nil {
		return false
	}
	return strings.ToLower(strings.TrimSpace(c.LogLevel)) == "debug"
}


// Quality returns the clamped video quality level between 1 and 10 (default: 6).
func (c *Config) Quality() int {
	if c == nil || c.VideoQuality == 0 {
		return 6
	}
	if c.VideoQuality < 1 {
		return 1
	}
	if c.VideoQuality > 10 {
		return 10
	}
	return c.VideoQuality
}

// QualityCRF returns the x264/x265 CRF value corresponding to the configured VideoQuality.
// Range: 1 (CRF 32) to 10 (CRF 18). Default (6): CRF 26.
func (c *Config) QualityCRF() int {
	switch c.Quality() {
	case 1:
		return 32
	case 2:
		return 31
	case 3:
		return 30
	case 4:
		return 29
	case 5:
		return 28
	case 6:
		return 26
	case 7:
		return 24
	case 8:
		return 22
	case 9:
		return 20
	case 10:
		return 18
	default:
		return 26
	}
}

// QualityVideoToolbox returns the VideoToolbox -q:v quality value corresponding to the configured VideoQuality.
// Range: 1 (VT 50) to 10 (VT 85). Default (6): VT 66.
func (c *Config) QualityVideoToolbox() int {
	switch c.Quality() {
	case 1:
		return 50
	case 2:
		return 53
	case 3:
		return 56
	case 4:
		return 59
	case 5:
		return 62
	case 6:
		return 66
	case 7:
		return 72
	case 8:
		return 76
	case 9:
		return 80
	case 10:
		return 85
	default:
		return 66
	}
}

// QualityLabel returns the human-readable tier name for the current video quality level.
func (c *Config) QualityLabel() string {
	q := c.Quality()
	switch {
	case q == 1:
		return "Max Savings"
	case q <= 3:
		return "High Compression"
	case q <= 6:
		return "Balanced"
	case q <= 8:
		return "High Quality"
	default:
		return "Visually Lossless"
	}
}

// QualityDescription returns the detailed summary description for the current quality tier.
func (c *Config) QualityDescription() string {
	q := c.Quality()
	switch {
	case q <= 3:
		return "High Compression (~70-80% space savings, noticeable softening for surveillance/dashcams)"
	case q <= 6:
		return "Balanced (Default, ~40-50% savings, imperceptible quality loss)"
	case q <= 8:
		return "High Quality (~25-35% savings, preserves fine film grain and high-motion details)"
	default:
		return "Visually Lossless (~15-20% savings, archival preservation)"
	}
}

func DefaultConfig() *Config {
	return &Config{
		ThresholdMode:      ThresholdModeMBPerMin,
		SizeThreshold:      "100",
		Resolution:         "Original",
		Preset:             "Medium",
		VideoQuality:       6,
		SizeGuard:          true,
		SkipHEVC:           true,
		ConcurrentEncoders: 1,
		EnablePrefetch:     true,
		LogLevel:           "info",
		MaxRetries:         2,
		ExcludeOptimized:   true,
		DateAfter:          "",
		DateBefore:         "",
		CustomFFmpegFlags:  "",
		LogMaxSizeMB:       10,
		LogMaxBackups:      5,
		LogCompress:        true,
	}
}

// NormalizeThresholdMode standardizes threshold mode string handling case-insensitively.
func NormalizeThresholdMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "total mb", "totalmb", "total_mb":
		return ThresholdModeTotalMB
	case "disabled", "disable", "off", "none":
		return ThresholdModeDisabled
	default:
		return ThresholdModeMBPerMin
	}
}

// Normalize clamps configuration fields to valid ranges and populates defaults.
// It returns true if any field value was adjusted.
func (c *Config) Normalize() bool {
	if c == nil {
		return false
	}
	changed := false
	trimmedURL := strings.TrimSpace(c.URL)
	if trimmedURL != c.URL {
		c.URL = trimmedURL
		changed = true
	}
	trimmedKey := strings.TrimSpace(c.APIKey)
	if trimmedKey != c.APIKey {
		c.APIKey = trimmedKey
		changed = true
	}
	normThreshold := NormalizeThresholdMode(c.ThresholdMode)
	if normThreshold != c.ThresholdMode {
		c.ThresholdMode = normThreshold
		changed = true
	}
	trimmedThreshold := strings.TrimSpace(c.SizeThreshold)
	if trimmedThreshold == "" {
		trimmedThreshold = "100"
	}
	if trimmedThreshold != c.SizeThreshold {
		c.SizeThreshold = trimmedThreshold
		changed = true
	}
	if c.Resolution == "" {
		c.Resolution = "Original"
		changed = true
	}
	if c.Preset == "" {
		c.Preset = "Medium"
		changed = true
	}
	if c.VideoQuality == 0 {
		c.VideoQuality = 6
		changed = true
	} else if c.VideoQuality < 1 {
		c.VideoQuality = 1
		changed = true
	} else if c.VideoQuality > 10 {
		c.VideoQuality = 10
		changed = true
	}
	if c.ConcurrentEncoders <= 0 {
		c.ConcurrentEncoders = 1
		changed = true
	}
	logLvl := strings.ToLower(strings.TrimSpace(c.LogLevel))
	if logLvl != "debug" && logLvl != "info" {
		logLvl = "info"
	}
	if logLvl != c.LogLevel {
		c.LogLevel = logLvl
		changed = true
	}
	if c.MaxRetries < 0 {
		c.MaxRetries = 2
		changed = true
	}
	if c.LogMaxSizeMB <= 0 {
		c.LogMaxSizeMB = 10
		changed = true
	}
	if c.LogMaxBackups < 0 {
		c.LogMaxBackups = 5
		changed = true
	}
	trimmedAfter := strings.TrimSpace(c.DateAfter)
	if trimmedAfter != c.DateAfter {
		c.DateAfter = trimmedAfter
		changed = true
	}
	trimmedBefore := strings.TrimSpace(c.DateBefore)
	if trimmedBefore != c.DateBefore {
		c.DateBefore = trimmedBefore
		changed = true
	}
	trimmedFlags := strings.TrimSpace(c.CustomFFmpegFlags)
	if trimmedFlags != c.CustomFFmpegFlags {
		c.CustomFFmpegFlags = trimmedFlags
		changed = true
	}
	return changed
}

func LoadConfig() (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")

	if home, err := userHomeDir(); err == nil {
		v.AddConfigPath(filepath.Join(home, ".config", "immich-video-optimizer"))
	}

	v.SetDefault("url", "")
	v.SetDefault("api_key", "")
	v.SetDefault("threshold_mode", ThresholdModeMBPerMin)
	v.SetDefault("size_threshold", "100")
	v.SetDefault("resolution", "Original")
	v.SetDefault("preset", "Medium")
	v.SetDefault("video_quality", 6)
	v.SetDefault("size_guard", true)
	v.SetDefault("skip_hevc", true)
	v.SetDefault("concurrent_encoders", 1)
	v.SetDefault("enable_prefetch", true)
	v.SetDefault("log_level", "info")
	v.SetDefault("max_retries", 2)
	v.SetDefault("exclude_optimized", true)
	v.SetDefault("date_after", "")
	v.SetDefault("date_before", "")
	v.SetDefault("custom_ffmpeg_flags", "")
	v.SetDefault("log_max_size_mb", 10)
	v.SetDefault("log_max_backups", 5)
	v.SetDefault("log_compress", true)

	if err := v.ReadInConfig(); err != nil {
		return DefaultConfig(), err
	}

	needsSave := false
	for _, key := range AllConfigKeys {
		if !v.InConfig(key) {
			needsSave = true
			break
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return DefaultConfig(), err
	}

	if cfg.Normalize() {
		needsSave = true
	}

	if needsSave {
		if configFile := v.ConfigFileUsed(); configFile != "" {
			_ = SaveConfigToPath(&cfg, configFile)
		} else {
			_ = SaveConfig(&cfg)
		}
	}
	return &cfg, nil
}

func SaveConfig(cfg *Config) error {
	return SaveConfigToPath(cfg, "")
}

// SaveConfigToPath saves the configuration to a specific target file path, or defaults to ~/.config/immich-video-optimizer/config.yaml if targetPath is empty.
func SaveConfigToPath(cfg *Config, targetPath string) error {
	cfg.Normalize()

	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.Set("url", cfg.URL)
	v.Set("api_key", cfg.APIKey)
	v.Set("threshold_mode", cfg.ThresholdMode)
	v.Set("size_threshold", cfg.SizeThreshold)
	v.Set("resolution", cfg.Resolution)
	v.Set("preset", cfg.Preset)
	v.Set("video_quality", cfg.VideoQuality)
	v.Set("size_guard", cfg.SizeGuard)
	v.Set("skip_hevc", cfg.SkipHEVC)
	v.Set("concurrent_encoders", cfg.ConcurrentEncoders)
	v.Set("enable_prefetch", cfg.EnablePrefetch)
	v.Set("log_level", strings.ToLower(cfg.LogLevel))
	v.Set("max_retries", cfg.MaxRetries)
	v.Set("exclude_optimized", cfg.ExcludeOptimized)
	v.Set("date_after", cfg.DateAfter)
	v.Set("date_before", cfg.DateBefore)
	v.Set("custom_ffmpeg_flags", cfg.CustomFFmpegFlags)
	v.Set("log_max_size_mb", cfg.LogMaxSizeMB)
	v.Set("log_max_backups", cfg.LogMaxBackups)
	v.Set("log_compress", cfg.LogCompress)

	if targetPath == "" {
		home, err := userHomeDir()
		if err != nil {
			return err
		}
		targetPath = filepath.Join(home, ".config", "immich-video-optimizer", "config.yaml")
	}

	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0700)

	if err := v.WriteConfigAs(targetPath); err != nil {
		return err
	}
	_ = os.Chmod(targetPath, 0600)
	return nil
}
