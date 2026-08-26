package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Asset is one downloadable file attached to a release.
type Asset struct {
	Name string
	Size int64
	URL  string
}

// Release is the newest published (non-draft, non-prerelease) release.
type Release struct {
	Tag         string
	Version     Version
	PageURL     string
	PublishedAt time.Time
	Assets      []Asset
}

func (r Release) asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// Source resolves the newest release. The GitHub implementation is the only
// production source; tests substitute their own.
type Source interface {
	LatestRelease(ctx context.Context) (Release, error)
}

// ErrNoRelease means the repository has no published release yet.
var ErrNoRelease = errors.New("no published release")

// ErrRateLimited means GitHub refused the unauthenticated request for now.
var ErrRateLimited = errors.New("github rate limit reached")

const (
	githubAPIBase      = "https://api.github.com"
	githubMaxBodyBytes = 1 << 20
	githubTimeout      = 15 * time.Second
)

// GitHubSource reads /repos/{repo}/releases/latest, which by definition skips
// drafts and pre-releases — publishing the draft stays the release gate.
type GitHubSource struct {
	Repo      string // "owner/name"
	UserAgent string
	Client    *http.Client
	BaseURL   string // overridable for tests
}

// NewGitHubSource builds a source for repo. client may be nil.
func NewGitHubSource(repo, userAgent string, client *http.Client) *GitHubSource {
	if client == nil {
		client = &http.Client{}
	}
	return &GitHubSource{Repo: repo, UserAgent: userAgent, Client: client}
}

type githubRelease struct {
	TagName     string `json:"tag_name"`
	HTMLURL     string `json:"html_url"`
	PublishedAt string `json:"published_at"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	Assets      []struct {
		Name               string `json:"name"`
		Size               int64  `json:"size"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// LatestRelease fetches and validates the newest release.
func (g *GitHubSource) LatestRelease(ctx context.Context) (Release, error) {
	base := g.BaseURL
	if base == "" {
		base = githubAPIBase
	}
	ctx, cancel := context.WithTimeout(ctx, githubTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/repos/"+g.Repo+"/releases/latest", nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if g.UserAgent != "" {
		req.Header.Set("User-Agent", g.UserAgent)
	}

	resp, err := g.Client.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return Release{}, ErrNoRelease
	case http.StatusForbidden, http.StatusTooManyRequests:
		if resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.StatusCode == http.StatusTooManyRequests {
			return Release{}, ErrRateLimited
		}
		return Release{}, fmt.Errorf("github responded with status %d", resp.StatusCode)
	default:
		return Release{}, fmt.Errorf("github responded with status %d", resp.StatusCode)
	}

	var payload githubRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, githubMaxBodyBytes)).Decode(&payload); err != nil {
		return Release{}, fmt.Errorf("decode github release: %w", err)
	}
	if payload.Draft || payload.Prerelease {
		return Release{}, ErrNoRelease
	}
	version, err := ParseVersion(payload.TagName)
	if err != nil {
		return Release{}, fmt.Errorf("release tag %q is not a version", payload.TagName)
	}
	if !strings.HasPrefix(payload.HTMLURL, "https://github.com/") {
		return Release{}, fmt.Errorf("release page %q is not on github.com", payload.HTMLURL)
	}

	release := Release{
		Tag:     payload.TagName,
		Version: version,
		PageURL: payload.HTMLURL,
	}
	if payload.PublishedAt != "" {
		if t, err := time.Parse(time.RFC3339, payload.PublishedAt); err == nil {
			release.PublishedAt = t
		}
	}
	for _, a := range payload.Assets {
		if a.Name == "" || !strings.HasPrefix(a.BrowserDownloadURL, "https://") {
			continue
		}
		release.Assets = append(release.Assets, Asset{Name: a.Name, Size: a.Size, URL: a.BrowserDownloadURL})
	}
	return release, nil
}
