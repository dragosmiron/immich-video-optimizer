package logger

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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

// RotationConfig controls log rotation, compression, and retention.
type RotationConfig struct {
	MaxSizeMB  int  // Max size in MB before rotating (default: 10)
	MaxBackups int  // Maximum number of archives to retain (default: 5)
	Compress   bool // Whether to gzip compress rotated archives (default: true)
}

// DefaultRotationConfig returns the standard log rotation settings.
func DefaultRotationConfig() RotationConfig {
	return RotationConfig{
		MaxSizeMB:  10,
		MaxBackups: 5,
		Compress:   true,
	}
}

var (
	mu             sync.Mutex
	fileMu         sync.Mutex
	currentLevel   Level          = LevelInfo
	tuiCallback    func(level Level, msg string)
	userHomeDir                   = os.UserHomeDir
	rotationConfig                = DefaultRotationConfig()
	timeNow                       = time.Now
	testMaxLogBytes int64         = 0
)

// SetRotationConfig configures log rotation size, retention, and compression.
func SetRotationConfig(cfg RotationConfig) {
	fileMu.Lock()
	defer fileMu.Unlock()
	if cfg.MaxSizeMB <= 0 {
		cfg.MaxSizeMB = 10
	}
	if cfg.MaxBackups < 0 {
		cfg.MaxBackups = 5
	}
	rotationConfig = cfg
}

// GetRotationConfig returns the current log rotation configuration.
func GetRotationConfig() RotationConfig {
	fileMu.Lock()
	defer fileMu.Unlock()
	return rotationConfig
}

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

func compressGzip(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer out.Close()

	gw := gzip.NewWriter(out)
	if _, err := io.Copy(gw, in); err != nil {
		_ = gw.Close()
		return err
	}
	return gw.Close()
}

func rotateAndArchive(path, filename string, cfg RotationConfig) {
	dir := filepath.Dir(path)
	ext := filepath.Ext(filename)
	prefix := strings.TrimSuffix(filename, ext)

	rotatingPath := path + ".rotating"
	_ = os.Remove(rotatingPath)
	if err := os.Rename(path, rotatingPath); err != nil {
		return
	}

	ts := timeNow().Format("2006-01-02T15-04-05")
	var archiveName string
	if cfg.Compress {
		archiveName = fmt.Sprintf("%s-%s%s.gz", prefix, ts, ext)
	} else {
		archiveName = fmt.Sprintf("%s-%s%s", prefix, ts, ext)
	}

	archivePath := filepath.Join(dir, archiveName)
	if _, err := os.Stat(archivePath); err == nil {
		// Collision avoidance for rapid rotations within the same second
		if cfg.Compress {
			archiveName = fmt.Sprintf("%s-%s-%d%s.gz", prefix, ts, timeNow().UnixNano()%1000000, ext)
		} else {
			archiveName = fmt.Sprintf("%s-%s-%d%s", prefix, ts, timeNow().UnixNano()%1000000, ext)
		}
		archivePath = filepath.Join(dir, archiveName)
	}

	if cfg.Compress {
		if err := compressGzip(rotatingPath, archivePath); err != nil {
			// If compression fails, fall back to uncompressed rename
			_ = os.Rename(rotatingPath, filepath.Join(dir, fmt.Sprintf("%s-%s%s", prefix, ts, ext)))
		} else {
			_ = os.Remove(rotatingPath)
		}
	} else {
		_ = os.Rename(rotatingPath, archivePath)
	}

	// Clean up legacy .1 uncompressed log if present
	_ = os.Remove(path + ".1")

	// Enforce MaxBackups retention limit
	pruneOldArchives(dir, prefix, ext, cfg.MaxBackups)
}

func pruneOldArchives(dir, prefix, ext string, maxBackups int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	prefixMatch := prefix + "-"
	var archives []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, prefixMatch) && (strings.HasSuffix(name, ext+".gz") || strings.HasSuffix(name, ext)) {
			archives = append(archives, filepath.Join(dir, name))
		}
	}

	// Lexicographical sorting on ISO-8601 timestamps (2006-01-02T15-04-05) orders oldest to newest
	sort.Strings(archives)

	if len(archives) > maxBackups {
		toDelete := len(archives) - maxBackups
		for i := 0; i < toDelete; i++ {
			_ = os.Remove(archives[i])
		}
	}
}

func writeToFile(filename, line string) {
	fileMu.Lock()
	defer fileMu.Unlock()

	path := getLogPath(filename)
	if path == "" {
		return
	}

	maxBytes := int64(rotationConfig.MaxSizeMB) * 1024 * 1024
	if testMaxLogBytes > 0 {
		maxBytes = testMaxLogBytes
	}
	if maxBytes <= 0 {
		maxBytes = 10 * 1024 * 1024
	}

	if fi, err := os.Stat(path); err == nil && fi.Size() >= maxBytes {
		rotateAndArchive(path, filename, rotationConfig)
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
