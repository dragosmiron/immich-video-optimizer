package version

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Current is the application version, set at build time via:
// -ldflags "-X 'immich-video-optimizer/version.Current=vX.Y.Z'"
var Current = "dev"

// ReleaseRepo is the GitHub repository in "owner/repo" format.
const ReleaseRepo = "dragosmiron/immich-video-optimizer"

// UpdateInfo holds details about an update check.
type UpdateInfo struct {
	CurrentVersion string
	LatestVersion  string
	ReleaseURL     string
	HasUpdate      bool
}

// CheckLatestRelease queries GitHub's latest release redirect without using the rate-limited REST API.
// It performs a HEAD request on https://github.com/<repo>/releases/latest and inspects the 302 Location header.
func CheckLatestRelease(ctx context.Context, repo, currentVersion string) (*UpdateInfo, error) {
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // Don't follow redirect; capture Location header
		},
		Timeout: 5 * time.Second,
	}
	return CheckLatestReleaseWithClient(ctx, client, repo, currentVersion)
}

// CheckLatestReleaseWithClient allows passing a custom HTTP client (e.g. for testing).
func CheckLatestReleaseWithClient(ctx context.Context, client *http.Client, repo, currentVersion string) (*UpdateInfo, error) {
	if repo == "" {
		repo = ReleaseRepo
	}

	cl := *client
	cl.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse // Don't follow redirect; capture Location header
	}

	url := fmt.Sprintf("https://github.com/%s/releases/latest", repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "immich-video-optimizer")

	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusMovedPermanently && resp.StatusCode != http.StatusSeeOther {
		return nil, fmt.Errorf("unexpected status %d from release redirect", resp.StatusCode)
	}

	location := resp.Header.Get("Location")
	if location == "" {
		return nil, fmt.Errorf("no Location header received")
	}

	tagMarker := "/releases/tag/"
	idx := strings.LastIndex(location, tagMarker)
	if idx == -1 {
		return nil, fmt.Errorf("location %q does not contain %s", location, tagMarker)
	}

	latestTag := strings.TrimSpace(location[idx+len(tagMarker):])
	if latestTag == "" {
		return nil, fmt.Errorf("empty tag in location: %s", location)
	}

	hasUpdate := IsNewer(currentVersion, latestTag)

	return &UpdateInfo{
		CurrentVersion: currentVersion,
		LatestVersion:  latestTag,
		ReleaseURL:     location,
		HasUpdate:      hasUpdate,
	}, nil
}

// IsNewer returns true if latest is strictly newer than current according to semantic versioning.
// If current is "dev", "unknown", or empty, IsNewer returns false.
// If latest is a pre-release (e.g., alpha, beta, rc) and current is stable, IsNewer returns false.
func IsNewer(current, latest string) bool {
	cur := strings.TrimSpace(current)
	lat := strings.TrimSpace(latest)

	if cur == "" || cur == "dev" || cur == "unknown" {
		return false
	}
	if lat == "" {
		return false
	}

	// Exclude pre-releases (alpha, beta, rc) for users on stable versions
	if IsPrerelease(lat) && !IsPrerelease(cur) {
		return false
	}

	return Compare(cur, lat) < 0
}

// IsPrerelease checks if a version string contains a pre-release identifier
// (e.g. "-alpha", "-beta", "-rc.1").
func IsPrerelease(v string) bool {
	return strings.Contains(cleanVersion(v), "-")
}

// Compare compares two version strings (e.g. "v0.2.1" and "v0.2.2").
// Returns -1 if v1 < v2, 0 if v1 == v2, and 1 if v1 > v2.
func Compare(v1, v2 string) int {
	v1Clean := cleanVersion(v1)
	v2Clean := cleanVersion(v2)

	v1Parts, v1Pre := splitVersionParts(v1Clean)
	v2Parts, v2Pre := splitVersionParts(v2Clean)

	maxLen := len(v1Parts)
	if len(v2Parts) > maxLen {
		maxLen = len(v2Parts)
	}

	for i := 0; i < maxLen; i++ {
		var n1, n2 int
		if i < len(v1Parts) {
			n1 = v1Parts[i]
		}
		if i < len(v2Parts) {
			n2 = v2Parts[i]
		}
		if n1 < n2 {
			return -1
		}
		if n1 > n2 {
			return 1
		}
	}

	// If numeric parts are equal, handle pre-release:
	// A version with a pre-release suffix (e.g. "v1.0.0-rc1") is older than the release version ("v1.0.0").
	if v1Pre != "" && v2Pre == "" {
		return -1
	}
	if v1Pre == "" && v2Pre != "" {
		return 1
	}
	if v1Pre < v2Pre {
		return -1
	}
	if v1Pre > v2Pre {
		return 1
	}

	return 0
}

func cleanVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	return v
}

func splitVersionParts(v string) ([]int, string) {
	var prerelease string
	if idx := strings.Index(v, "-"); idx != -1 {
		prerelease = v[idx+1:]
		v = v[:idx]
	}

	rawParts := strings.Split(v, ".")
	var parts []int
	for _, p := range rawParts {
		num, err := strconv.Atoi(p)
		if err != nil {
			num = 0
		}
		parts = append(parts, num)
	}
	return parts, prerelease
}
