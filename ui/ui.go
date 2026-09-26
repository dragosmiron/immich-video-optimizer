package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"immich-video-optimizer/config"
)

func ValidateURL(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("url cannot be empty")
	}
	if !strings.HasPrefix(s, "http") {
		return fmt.Errorf("must start with http:// or https://")
	}
	return nil
}

func ValidateAPIKey(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("api key cannot be empty")
	}
	return nil
}

func ValidateThreshold(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("threshold cannot be empty")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return fmt.Errorf("must be a valid integer (e.g. 500)")
		}
	}
	return nil
}

func RunSetup(cfg *config.Config) error {
	if cfg == nil {
		cfg = &config.Config{
			SizeThreshold: "500", // default 500 MB
		}
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Immich Server URL").
				Description("e.g. https://immich.example.com/api").
				Value(&cfg.URL).
				Validate(ValidateURL),

			huh.NewInput().
				Title("Immich API Key").
				Description("Create one in your Immich account settings").
				EchoMode(huh.EchoModePassword).
				Value(&cfg.APIKey).
				Validate(ValidateAPIKey),

			huh.NewInput().
				Title("Size Threshold (MB)").
				Description("Videos larger than this will be optimized").
				Value(&cfg.SizeThreshold).
				Validate(ValidateThreshold),
		),
	).WithTheme(huh.ThemeBase()).WithAccessible(os.Getenv("TU_TEST") == "1")

	return form.Run()
}
