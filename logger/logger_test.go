package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogger_Levels(t *testing.T) {
	tempDir := t.TempDir()
	userHomeDir = func() (string, error) {
		return tempDir, nil
	}
	defer func() {
		userHomeDir = os.UserHomeDir
	}()

	SetLevelString("info")
	if IsDebug() {
		t.Fatalf("expected IsDebug() false for info level")
	}

	SetLevelString("DEBUG")
	if !IsDebug() {
		t.Fatalf("expected IsDebug() true for debug level")
	}
}

func TestLogger_CallbackAndFile(t *testing.T) {
	tempDir := t.TempDir()
	userHomeDir = func() (string, error) {
		return tempDir, nil
	}
	defer func() {
		userHomeDir = os.UserHomeDir
	}()

	SetLevelString("debug")

	var callbackLogs []string
	SetCallback(func(level Level, msg string) {
		callbackLogs = append(callbackLogs, msg)
	})

	Info("info message %d", 1)
	Debug("debug message %d", 2)
	Error("error message %d", 3)

	if len(callbackLogs) != 3 {
		t.Fatalf("expected 3 callback messages, got %d", len(callbackLogs))
	}

	optLogPath := filepath.Join(tempDir, ".config", "immich-video-optimizer", "optimizer.log")
	data, err := os.ReadFile(optLogPath)
	if err != nil {
		t.Fatalf("failed to read optimizer.log: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "[INFO] info message 1") {
		t.Errorf("optimizer.log missing info line: %s", content)
	}
	if !strings.Contains(content, "[DEBUG] debug message 2") {
		t.Errorf("optimizer.log missing debug line: %s", content)
	}
	if !strings.Contains(content, "[ERROR] error message 3") {
		t.Errorf("optimizer.log missing error line: %s", content)
	}

	failLogPath := filepath.Join(tempDir, ".config", "immich-video-optimizer", "failures.log")
	failData, err := os.ReadFile(failLogPath)
	if err != nil {
		t.Fatalf("failed to read failures.log: %v", err)
	}
	failContent := string(failData)
	if !strings.Contains(failContent, "[ERROR] error message 3") {
		t.Errorf("failures.log missing error line: %s", failContent)
	}
}
