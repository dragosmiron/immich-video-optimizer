package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/viper"
)

var userHomeDir = os.UserHomeDir

type Config struct {
	URL                string `mapstructure:"url"`
	APIKey             string `mapstructure:"api_key"`
	SizeThreshold      string `mapstructure:"size_threshold"`
	Resolution         string `mapstructure:"resolution"`
	Preset             string `mapstructure:"preset"`
	SizeGuard          bool   `mapstructure:"size_guard"`
	SkipHEVC           bool   `mapstructure:"skip_hevc"`
	ConcurrentEncoders int    `mapstructure:"concurrent_encoders"`
	EnablePrefetch     bool   `mapstructure:"enable_prefetch"`
	LogLevel           string `mapstructure:"log_level"`
	MaxRetries         int    `mapstructure:"max_retries"`
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

func DefaultConfig() *Config {
	return &Config{
		SizeThreshold:      "500",
		Resolution:         "Original",
		Preset:             "Medium",
		SizeGuard:          true,
		SkipHEVC:           true,
		ConcurrentEncoders: 1,
		EnablePrefetch:     true,
		LogLevel:           "info",
		MaxRetries:         3,
	}
}

func LoadConfig() (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")

	if home, err := userHomeDir(); err == nil {
		v.AddConfigPath(filepath.Join(home, ".config", "immich-video-optimizer"))
	}

	v.SetDefault("size_threshold", "500")
	v.SetDefault("resolution", "Original")
	v.SetDefault("preset", "Medium")
	v.SetDefault("size_guard", true)
	v.SetDefault("skip_hevc", true)
	v.SetDefault("concurrent_encoders", 1)
	v.SetDefault("enable_prefetch", true)
	v.SetDefault("log_level", "info")
	v.SetDefault("max_retries", 3)

	if err := v.ReadInConfig(); err != nil {
		return DefaultConfig(), err
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return DefaultConfig(), err
	}
	if cfg.ConcurrentEncoders <= 0 {
		cfg.ConcurrentEncoders = 1
	}
	if cfg.SizeThreshold == "" {
		cfg.SizeThreshold = "500"
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
	cfg.SizeThreshold = strings.TrimSpace(cfg.SizeThreshold)
	if cfg.Resolution == "" {
		cfg.Resolution = "Original"
	}
	if cfg.Preset == "" {
		cfg.Preset = "Medium"
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
	v.Set("size_threshold", cfg.SizeThreshold)
	v.Set("resolution", cfg.Resolution)
	v.Set("preset", cfg.Preset)
	v.Set("size_guard", cfg.SizeGuard)
	v.Set("skip_hevc", cfg.SkipHEVC)
	v.Set("concurrent_encoders", cfg.ConcurrentEncoders)
	v.Set("enable_prefetch", cfg.EnablePrefetch)
	v.Set("log_level", strings.ToLower(cfg.LogLevel))
	v.Set("max_retries", cfg.MaxRetries)

	home, err := userHomeDir()
	if err != nil {
		return err
	}

	configDir := filepath.Join(home, ".config", "immich-video-optimizer")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}

	configPath := filepath.Join(configDir, "config.yaml")
	return v.WriteConfigAs(configPath)
}
