package version

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type mockTransport struct {
	roundTrip func(req *http.Request) (*http.Response, error)
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.roundTrip(req)
}

func TestCompare(t *testing.T) {
	tests := []struct {
		v1       string
		v2       string
		expected int
	}{
		{"v0.2.1", "v0.2.2", -1},
		{"v0.2.2", "v0.2.1", 1},
		{"v0.2.2", "v0.2.2", 0},
		{"0.2.1", "v0.2.2", -1},
		{"v1.0.0", "v0.9.9", 1},
		{"v0.10.0", "v0.9.0", 1},
		{"v1.2", "v1.2.0", 0},
		{"v1.2.3", "v1.2.4", -1},
		{"v1.0.0-rc1", "v1.0.0", -1},
		{"v1.0.0", "v1.0.0-rc1", 1},
		{"v1.0.0-alpha", "v1.0.0-beta", -1},
	}

	for _, tt := range tests {
		got := Compare(tt.v1, tt.v2)
		if got != tt.expected {
			t.Errorf("Compare(%q, %q) = %d; want %d", tt.v1, tt.v2, got, tt.expected)
		}
	}
}

func TestIsNewer(t *testing.T) {
	tests := []struct {
		cur      string
		lat      string
		expected bool
	}{
		{"dev", "v0.2.2", false},
		{"unknown", "v0.2.2", false},
		{"", "v0.2.2", false},
		{"v0.2.1", "v0.2.2", true},
		{"v0.2.2", "v0.2.2", false},
		{"v0.2.3", "v0.2.2", false},
		{"0.2.1", "v0.2.2", true},
		// Pre-release exclusion for stable users
		{"v0.2.2", "v0.3.0-alpha", false},
		{"v0.2.2", "v0.3.0-beta.1", false},
		{"v0.2.2", "v0.3.0-rc1", false},
		// Pre-release upgrades for pre-release users
		{"v0.3.0-beta.1", "v0.3.0-beta.2", true},
		{"v0.3.0-rc1", "v0.3.0", true},
	}

	for _, tt := range tests {
		got := IsNewer(tt.cur, tt.lat)
		if got != tt.expected {
			t.Errorf("IsNewer(%q, %q) = %v; want %v", tt.cur, tt.lat, got, tt.expected)
		}
	}
}

func TestCheckLatestReleaseMock(t *testing.T) {
	client := &http.Client{
		Transport: &mockTransport{
			roundTrip: func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodHead {
					t.Errorf("expected HEAD request, got %s", req.Method)
				}
				header := make(http.Header)
				header.Set("Location", "https://github.com/dragosmiron/immich-video-optimizer/releases/tag/v0.3.0")
				return &http.Response{
					StatusCode: http.StatusFound,
					Header:     header,
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			},
		},
	}

	info, err := CheckLatestReleaseWithClient(context.Background(), client, "dragosmiron/immich-video-optimizer", "v0.2.2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if info.LatestVersion != "v0.3.0" {
		t.Errorf("expected LatestVersion v0.3.0, got %s", info.LatestVersion)
	}
	if !info.HasUpdate {
		t.Errorf("expected HasUpdate to be true")
	}
	if info.ReleaseURL != "https://github.com/dragosmiron/immich-video-optimizer/releases/tag/v0.3.0" {
		t.Errorf("unexpected ReleaseURL %s", info.ReleaseURL)
	}
}
