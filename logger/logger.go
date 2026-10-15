package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Level int

const (
	LevelInfo Level = iota
	LevelDebug
)

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	default:
		return "INFO"
	}
}

var (
	mu           sync.Mutex
	currentLevel Level = LevelInfo
	tuiCallback  func(level Level, msg string)
	userHomeDir  = os.UserHomeDir
)

// SetLevel sets the active logging level.
func SetLevel(lvl Level) {
	mu.Lock()
	defer mu.Unlock()
	currentLevel = lvl
}

// SetLevelString sets the active logging level from string ("debug" or "info").
func SetLevelString(lvlStr string) {
	if strings.ToLower(strings.TrimSpace(lvlStr)) == "debug" {
		SetLevel(LevelDebug)
	} else {
		SetLevel(LevelInfo)
	}
}

// GetLevel returns the current logging level.
func GetLevel() Level {
	mu.Lock()
	defer mu.Unlock()
	return currentLevel
}

// IsDebug returns true if current level is Debug.
func IsDebug() bool {
	return GetLevel() == LevelDebug
}

// SetCallback sets a listener for log events (e.g. for TUI integration).
func SetCallback(cb func(level Level, msg string)) {
	mu.Lock()
	defer mu.Unlock()
	tuiCallback = cb
}

func getLogPath(filename string) string {
	home, err := userHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, ".config", "immich-video-optimizer")
	_ = os.MkdirAll(dir, 0755)
	return filepath.Join(dir, filename)
}

const maxLogFileSize = 10 * 1024 * 1024 // 10MB

func writeToFile(filename, line string) {
	path := getLogPath(filename)
	if path == "" {
		return
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxLogFileSize {
		oldPath := path + ".1"
		_ = os.Remove(oldPath)
		_ = os.Rename(path, oldPath)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		defer f.Close()
		_, _ = f.WriteString(line + "\n")
	}
}

// Info logs informational messages.
func Info(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format("15:04:05")
	formatted := fmt.Sprintf("%s - %s", timestamp, msg)

	mu.Lock()
	cb := tuiCallback
	mu.Unlock()

	if cb != nil {
		cb(LevelInfo, formatted)
	}
	writeToFile("optimizer.log", time.Now().Format(time.RFC3339)+" [INFO] "+msg)
}

// Debug logs verbose diagnostic messages when log level is Debug.
func Debug(format string, args ...interface{}) {
	if !IsDebug() {
		return
	}
	msg := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format("15:04:05")
	formatted := fmt.Sprintf("%s - [DEBUG] %s", timestamp, msg)

	mu.Lock()
	cb := tuiCallback
	mu.Unlock()

	if cb != nil {
		cb(LevelDebug, formatted)
	}
	writeToFile("optimizer.log", time.Now().Format(time.RFC3339)+" [DEBUG] "+msg)
}

// Error logs error messages to both optimizer.log and failures.log.
func Error(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format("15:04:05")
	formatted := fmt.Sprintf("%s - %s", timestamp, msg)

	mu.Lock()
	cb := tuiCallback
	mu.Unlock()

	if cb != nil {
		cb(LevelInfo, formatted)
	}
	logLine := time.Now().Format(time.RFC3339) + " [ERROR] " + msg
	writeToFile("optimizer.log", logLine)
	writeToFile("failures.log", logLine)
}
