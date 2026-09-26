package engine

import (
	"testing"
)

type MockEnvChecker struct {
	Commands map[string]bool
	Files    map[string]bool
}

func (m MockEnvChecker) HasCommand(cmd string) bool {
	return m.Commands[cmd]
}

func (m MockEnvChecker) HasFile(path string) bool {
	return m.Files[path]
}

func TestDetectHEVCEncoder(t *testing.T) {
	tests := []struct {
		name     string
		osName   string
		commands map[string]bool
		files    map[string]bool
		expected string
	}{
		{
			name:     "macOS uses videotoolbox",
			osName:   "darwin",
			expected: "hevc_videotoolbox",
		},
		{
			name:     "Linux with Nvidia uses nvenc",
			osName:   "linux",
			commands: map[string]bool{"nvidia-smi": true},
			expected: "hevc_nvenc",
		},
		{
			name:     "Linux with Intel/AMD uses vaapi",
			osName:   "linux",
			files:    map[string]bool{"/dev/dri": true},
			expected: "hevc_vaapi",
		},
		{
			name:     "Linux without hardware uses libx265",
			osName:   "linux",
			expected: "libx265",
		},
		{
			name:     "Windows uses libx265 fallback",
			osName:   "windows",
			expected: "libx265",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := MockEnvChecker{
				Commands: tt.commands,
				Files:    tt.files,
			}
			result := DetectHEVCEncoder(tt.osName, checker)
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}
