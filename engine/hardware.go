package engine

import (
	"os"
	"os/exec"
	"runtime"
)

// EnvChecker defines methods to check system capabilities.
type EnvChecker interface {
	HasCommand(cmd string) bool
	HasFile(path string) bool
}

// DefaultEnvChecker implements EnvChecker using the real OS.
type DefaultEnvChecker struct{}

func (d DefaultEnvChecker) HasCommand(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}

func (d DefaultEnvChecker) HasFile(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// DetectHEVCEncoder detects the most efficient HEVC encoder for a given OS and environment.
func DetectHEVCEncoder(osName string, checker EnvChecker) string {
	switch osName {
	case "darwin":
		return "hevc_videotoolbox"
	case "linux":
		// Nvidia GPU Check
		if checker.HasCommand("nvidia-smi") {
			return "hevc_nvenc"
		}
		// Intel/AMD GPU Check
		if checker.HasFile("/dev/dri") {
			// fallback to vaapi or qsv
			return "hevc_vaapi"
		}
		// CPU fallback
		return "libx265"
	default:
		// Windows or other OS fallback
		return "libx265"
	}
}

// DetectHEVCEncoderDefault runs detection using the current system environment.
func DetectHEVCEncoderDefault() string {
	return DetectHEVCEncoder(runtime.GOOS, DefaultEnvChecker{})
}
