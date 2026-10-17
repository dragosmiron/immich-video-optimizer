package engine

import (
	"testing"
)

func TestParseVideoDetails(t *testing.T) {
	tests := []struct {
		name         string
		output       string
		expectedCodec string
		expectedRes  string
		expectedHEVC bool
	}{
		{
			name: "H.264 1080p stream",
			output: `Input #0, mov,mp4,m4a,3gp,3g2,mj2, from 'sample.mp4':
  Duration: 00:00:30.00, start: 0.000000, bitrate: 15420 kb/s
  Stream #0:0[0x1](und): Video: h264 (High) (avc1 / 0x31637661), yuv420p(tv, bt709), 1920x1080 [SAR 1:1 DAR 16:9], 15000 kb/s, 29.97 fps`,
			expectedCodec: "H.264",
			expectedRes:  "1080p",
			expectedHEVC: false,
		},
		{
			name: "HEVC 4K stream",
			output: `Input #0, mov,mp4,m4a,3gp,3g2,mj2, from 'sample.mp4':
  Stream #0:0: Video: hevc (Main), yuv420p(tv), 3840x2160 [SAR 1:1 DAR 16:9], 60 fps`,
			expectedCodec: "HEVC",
			expectedRes:  "4K",
			expectedHEVC: true,
		},
		{
			name: "Vertical phone 1080p video",
			output: `Stream #0:0: Video: h264, yuv420p, 1080x1920, 30 fps`,
			expectedCodec: "H.264",
			expectedRes:  "1080p",
			expectedHEVC: false,
		},
		{
			name: "VP9 720p stream",
			output: `Stream #0:0: Video: vp9, yuv420p, 1280x720, 25 fps`,
			expectedCodec: "VP9",
			expectedRes:  "720p",
			expectedHEVC: false,
		},
		{
			name: "ProRes 1440p stream",
			output: `Stream #0:0: Video: prores, yuv422p10le, 2560x1440, 24 fps`,
			expectedCodec: "ProRes",
			expectedRes:  "1440p",
			expectedHEVC: false,
		},
		{
			name: "Cover art thumbnail before HEVC stream",
			output: `Input #0, mov,mp4,m4a,3gp,3g2,mj2, from 'sample.mp4':
  Stream #0:0: Video: mjpeg (Baseline), yuvj420p(pc, bt470bg/unknown/unknown), 320x240 [attached pic]
  Stream #0:1: Video: hevc (Main), yuv420p(tv), 3840x2160 [SAR 1:1 DAR 16:9], 60 fps`,
			expectedCodec: "HEVC",
			expectedRes:  "4K",
			expectedHEVC: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			details := ParseVideoDetails(tt.output)
			if details.Codec != tt.expectedCodec {
				t.Errorf("expected codec %q, got %q", tt.expectedCodec, details.Codec)
			}
			if details.Resolution != tt.expectedRes {
				t.Errorf("expected resolution %q, got %q", tt.expectedRes, details.Resolution)
			}
			if details.IsHEVC != tt.expectedHEVC {
				t.Errorf("expected isHEVC %v, got %v", tt.expectedHEVC, details.IsHEVC)
			}
		})
	}
}

func TestCheckAudioStream(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		hasAudio bool
		isAAC    bool
	}{
		{
			name:     "AAC stream",
			output:   "Stream #0:1(und): Audio: aac (LC) (mp4a / 0x6134706D), 48000 Hz, stereo, fltp, 256 kb/s",
			hasAudio: true,
			isAAC:    true,
		},
		{
			name:     "PCM audio",
			output:   "Stream #0:1: Audio: pcm_s16le, 44100 Hz, 2 channels, s16, 1411 kb/s",
			hasAudio: true,
			isAAC:    false,
		},
		{
			name:     "MP3 audio",
			output:   "Stream #0:1: Audio: mp3, 44100 Hz, stereo, fltp, 320 kb/s",
			hasAudio: true,
			isAAC:    false,
		},
		{
			name:     "No audio stream",
			output:   "Stream #0:0: Video: h264, yuv420p, 1920x1080, 30 fps",
			hasAudio: false,
			isAAC:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hasAudio, isAAC := CheckAudioStream(tt.output)
			if hasAudio != tt.hasAudio || isAAC != tt.isAAC {
				t.Errorf("CheckAudioStream() = (%v, %v), want (%v, %v)", hasAudio, isAAC, tt.hasAudio, tt.isAAC)
			}
		})
	}
}

