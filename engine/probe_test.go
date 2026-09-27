package engine

import (
	"testing"
)

func TestCheckHEVCOutput(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected bool
	}{
		{
			name: "HEVC main profile stream",
			output: `Input #0, mov,mp4,m4a,3gp,3g2,mj2, from 'input.mp4':
  Duration: 00:05:38.54, start: 0.000000, bitrate: 3119 kb/s
  Stream #0:0[0x1](eng): Video: hevc (Main) (hev1 / 0x31766568), yuv420p(tv, progressive), 1920x1080 [SAR 1:1 DAR 16:9], 2930 kb/s, 30 fps
  Stream #0:1[0x2](eng): Audio: aac (LC) (mp4a / 0x6134706D), 48000 Hz, stereo`,
			expected: true,
		},
		{
			name: "HEVC with hvc1 tag",
			output: `Stream #0:0: Video: hevc (Main 10) (hvc1 / 0x31637668), yuv420p10le(tv, bt2020nc/bt2020/arib-std-b67), 3840x2160 [SAR 1:1 DAR 16:9], 15000 kb/s, 60 fps`,
			expected: true,
		},
		{
			name: "H.264 stream (not HEVC)",
			output: `Input #0, mov,mp4,m4a,3gp,3g2,mj2, from 'input.mp4':
  Duration: 00:01:23.45, start: 0.000000, bitrate: 12000 kb/s
  Stream #0:0[0x1](eng): Video: h264 (High) (avc1 / 0x31637661), yuv420p, 1920x1080 [SAR 1:1 DAR 16:9], 11800 kb/s, 30 fps
  Stream #0:1[0x2](eng): Audio: aac`,
			expected: false,
		},
		{
			name: "VP9 stream (not HEVC)",
			output: `Stream #0:0: Video: vp9, yuv420p, 1920x1080, 25 fps, 25 tbr, 1k tbn`,
			expected: false,
		},
		{
			name: "No video stream",
			output: `Input #0, mp3, from 'audio.mp3':
  Stream #0:0: Audio: mp3, 44100 Hz, stereo`,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CheckHEVCOutput(tt.output)
			if result != tt.expected {
				t.Errorf("CheckHEVCOutput() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
