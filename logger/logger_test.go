package logger

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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

	Info("info message %d", 1)
	Debug("debug message %d", 2)
	Error("error message %d", 3)

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

func TestLogger_Rotation(t *testing.T) {
	tempDir := t.TempDir()
	userHomeDir = func() (string, error) {
		return tempDir, nil
	}
	defer func() {
		userHomeDir = os.UserHomeDir
		testMaxLogBytes = 0
		SetRotationConfig(DefaultRotationConfig())
	}()

	optLogPath := filepath.Join(tempDir, ".config", "immich-video-optimizer", "optimizer.log")
	_ = os.MkdirAll(filepath.Dir(optLogPath), 0755)

	// Set small byte threshold for fast rotation
	testMaxLogBytes = 250
	SetRotationConfig(RotationConfig{
		MaxSizeMB:  10,
		MaxBackups: 5,
		Compress:   true,
	})

	// Fill file past threshold
	for i := 0; i < 5; i++ {
		Info("pre-rotation initial log line number %d with extra padding padding", i)
	}

	// Trigger rotation with a new message
	Info("new message after rotation")
	WaitForRotation()

	logDir := filepath.Dir(optLogPath)
	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("failed to read log directory: %v", err)
	}

	var gzArchives []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "optimizer-") && strings.HasSuffix(e.Name(), ".log.gz") {
			gzArchives = append(gzArchives, filepath.Join(logDir, e.Name()))
		}
	}

	if len(gzArchives) == 0 {
		t.Fatalf("expected at least one optimizer-*.log.gz archive to exist in %s", logDir)
	}

	// Verify gzip archive decompresses cleanly and has original log lines
	gzFile, err := os.Open(gzArchives[0])
	if err != nil {
		t.Fatalf("failed to open gz archive: %v", err)
	}
	defer gzFile.Close()

	gzReader, err := gzip.NewReader(gzFile)
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	defer gzReader.Close()

	decompressed, err := io.ReadAll(gzReader)
	if err != nil {
		t.Fatalf("failed to read decompressed gzip data: %v", err)
	}

	if !strings.Contains(string(decompressed), "pre-rotation initial log line") {
		t.Errorf("expected archive to contain pre-rotation log line, got: %s", string(decompressed))
	}

	// Verify active log file is small and contains the newest message
	activeData, err := os.ReadFile(optLogPath)
	if err != nil {
		t.Fatalf("failed to read active optimizer.log: %v", err)
	}
	if !strings.Contains(string(activeData), "new message after rotation") {
		t.Errorf("expected active log to contain new message, got: %s", string(activeData))
	}
	if len(activeData) > 500 {
		t.Errorf("expected active optimizer.log to be small, got %d bytes", len(activeData))
	}
}

func TestLogger_ArchiveRetentionPruning(t *testing.T) {
	tempDir := t.TempDir()
	userHomeDir = func() (string, error) {
		return tempDir, nil
	}
	defer func() {
		userHomeDir = os.UserHomeDir
		testMaxLogBytes = 0
		timeNow = time.Now
		SetRotationConfig(DefaultRotationConfig())
	}()

	testMaxLogBytes = 100
	maxBackups := 3
	SetRotationConfig(RotationConfig{
		MaxSizeMB:  1,
		MaxBackups: maxBackups,
		Compress:   true,
	})

	baseTime := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

	// Perform 6 rotations with distinct simulated timestamps
	for i := 0; i < 6; i++ {
		idx := i
		timeNow = func() time.Time {
			return baseTime.Add(time.Duration(idx) * time.Hour)
		}
		// Write enough to exceed 100 bytes
		Info("message chunk A for rotation index %d with sufficient length", i)
		Info("message chunk B for rotation index %d to trigger threshold check", i)
		WaitForRotation()
	}

	logDir := filepath.Join(tempDir, ".config", "immich-video-optimizer")
	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("failed to read log directory: %v", err)
	}

	var archives []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "optimizer-") && strings.HasSuffix(e.Name(), ".log.gz") {
			archives = append(archives, e.Name())
		}
	}

	if len(archives) != maxBackups {
		t.Errorf("expected exactly %d retained archives, got %d: %v", maxBackups, len(archives), archives)
	}
}

func TestLogger_DisabledCompression(t *testing.T) {
	tempDir := t.TempDir()
	userHomeDir = func() (string, error) {
		return tempDir, nil
	}
	defer func() {
		userHomeDir = os.UserHomeDir
		testMaxLogBytes = 0
		SetRotationConfig(DefaultRotationConfig())
	}()

	testMaxLogBytes = 150
	SetRotationConfig(RotationConfig{
		MaxSizeMB:  1,
		MaxBackups: 5,
		Compress:   false,
	})

	for i := 0; i < 4; i++ {
		Info("uncompressed log line %d with additional text to exceed threshold", i)
	}
	WaitForRotation()

	logDir := filepath.Join(tempDir, ".config", "immich-video-optimizer")
	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("failed to read log dir: %v", err)
	}

	foundUncompressed := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "optimizer-") && strings.HasSuffix(e.Name(), ".log") {
			foundUncompressed = true
			data, err := os.ReadFile(filepath.Join(logDir, e.Name()))
			if err != nil {
				t.Fatalf("failed to read uncompressed archive: %v", err)
			}
			if !strings.Contains(string(data), "uncompressed log line") {
				t.Errorf("uncompressed archive missing expected content: %s", string(data))
			}
		}
	}

	if !foundUncompressed {
		t.Errorf("expected to find uncompressed optimizer-*.log archive")
	}
}

func TestLogger_ConcurrentWrites(t *testing.T) {
	tempDir := t.TempDir()
	userHomeDir = func() (string, error) {
		return tempDir, nil
	}
	defer func() {
		userHomeDir = os.UserHomeDir
		testMaxLogBytes = 0
		SetRotationConfig(DefaultRotationConfig())
	}()

	testMaxLogBytes = 300
	SetRotationConfig(RotationConfig{
		MaxSizeMB:  1,
		MaxBackups: 4,
		Compress:   true,
	})
	SetLevelString("debug")

	var wg sync.WaitGroup
	goroutines := 15
	iterations := 25

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				Info("worker %d info iteration %d with long padding padding padding", id, i)
				Debug("worker %d debug iteration %d with diagnostic data payload", id, i)
				if i%5 == 0 {
					Error("worker %d error iteration %d failure details report", id, i)
				}
			}
		}(g)
	}

	wg.Wait()
	WaitForRotation()

	// Verify no panics or file corruption
	optLogPath := filepath.Join(tempDir, ".config", "immich-video-optimizer", "optimizer.log")
	if _, err := os.Stat(optLogPath); err != nil {
		t.Errorf("expected optimizer.log to exist after concurrent writes: %v", err)
	}

	failLogPath := filepath.Join(tempDir, ".config", "immich-video-optimizer", "failures.log")
	if _, err := os.Stat(failLogPath); err != nil {
		t.Errorf("expected failures.log to exist after concurrent writes: %v", err)
	}
}
