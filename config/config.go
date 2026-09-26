package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

var userHomeDir = os.UserHomeDir

type Config struct {
	URL           string `mapstructure:"url"`
	APIKey        string `mapstructure:"api_key"`
	SizeThreshold string `mapstructure:"size_threshold"`
}

func LoadConfig() (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")

	if home, err := userHomeDir(); err == nil {
		v.AddConfigPath(filepath.Join(home, ".config", "immich-video-optimizer"))
	}

	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func SaveConfig(cfg *Config) error {
	cfg.URL = strings.TrimSpace(cfg.URL)
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.SizeThreshold = strings.TrimSpace(cfg.SizeThreshold)

	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.Set("url", cfg.URL)
	v.Set("api_key", cfg.APIKey)
	v.Set("size_threshold", cfg.SizeThreshold)

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
