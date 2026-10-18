package config

import (
	"os"
	"path/filepath"
	"strconv"
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
}

// IsDebug returns true if the log level is set to debug.
func (c *Config) IsDebug() bool {
	if c == nil {
		return false
	}
	return strings.ToLower(strings.TrimSpace(c.LogLevel)) == "debug"
}

// SizeThresholdBytes returns the size threshold parsed as bytes.
func (c *Config) SizeThresholdBytes() int64 {
	if c == nil {
		return 0
	}
	mb, err := strconv.ParseInt(strings.TrimSpace(c.SizeThreshold), 10, 64)
	if err != nil || mb < 0 {
		return 0
	}
	return mb * 1024 * 1024
}

// SizeThresholdMBPerMin returns the bitrate density threshold parsed as MB/min.
func (c *Config) SizeThresholdMBPerMin() float64 {
	if c == nil {
		return 0
	}
	val, err := strconv.ParseFloat(strings.TrimSpace(c.SizeThreshold), 64)
	if err != nil || val < 0 {
		return 0
	}
	return val
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

func LoadConfig() (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")

	if home, err := userHomeDir(); err == nil {
		v.AddConfigPath(filepath.Join(home, ".config", "immich-video-optimizer"))
	}

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

	if err := v.ReadInConfig(); err != nil {
		return DefaultConfig(), err
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return DefaultConfig(), err
	}
	cfg.ThresholdMode = NormalizeThresholdMode(cfg.ThresholdMode)
	if cfg.Preset == "" {
		cfg.Preset = "Medium"
	}
	if cfg.VideoQuality == 0 {
		cfg.VideoQuality = 6
	} else if cfg.VideoQuality < 1 {
		cfg.VideoQuality = 1
	} else if cfg.VideoQuality > 10 {
		cfg.VideoQuality = 10
	}
	if cfg.ConcurrentEncoders <= 0 {
		cfg.ConcurrentEncoders = 1
	}
	if cfg.SizeThreshold == "" {
		cfg.SizeThreshold = "100"
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	return &cfg, nil
}

func SaveConfig(cfg *Config) error {
	cfg.URL = strings.TrimSpace(cfg.URL)
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.ThresholdMode = NormalizeThresholdMode(cfg.ThresholdMode)
	cfg.SizeThreshold = strings.TrimSpace(cfg.SizeThreshold)
	if cfg.Resolution == "" {
		cfg.Resolution = "Original"
	}
	if cfg.Preset == "" {
		cfg.Preset = "Medium"
	}
	if cfg.VideoQuality == 0 {
		cfg.VideoQuality = 6
	} else if cfg.VideoQuality < 1 {
		cfg.VideoQuality = 1
	} else if cfg.VideoQuality > 10 {
		cfg.VideoQuality = 10
	}
	if cfg.ConcurrentEncoders <= 0 {
		cfg.ConcurrentEncoders = 1
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}

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
	v.Set("date_after", strings.TrimSpace(cfg.DateAfter))
	v.Set("date_before", strings.TrimSpace(cfg.DateBefore))
	v.Set("custom_ffmpeg_flags", strings.TrimSpace(cfg.CustomFFmpegFlags))

	home, err := userHomeDir()
	if err != nil {
		return err
	}

	configDir := filepath.Join(home, ".config", "immich-video-optimizer")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return err
	}

	configPath := filepath.Join(configDir, "config.yaml")
	return v.WriteConfigAs(configPath)
}
