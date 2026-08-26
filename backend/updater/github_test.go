package updater

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func githubServer(t *testing.T, status int, headers map[string]string, body string) *GitHubSource {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/releases/latest" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("missing Accept header")
		}
		if r.Header.Get("User-Agent") != "TDrive/test" {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	source := NewGitHubSource("owner/repo", "TDrive/test", server.Client())
	source.BaseURL = server.URL
	return source
}

func TestGitHubSourceParsesLatestRelease(t *testing.T) {
	source := githubServer(t, http.StatusOK, nil, `{
		"tag_name": "v1.7.0",
		"html_url": "https://github.com/owner/repo/releases/tag/v1.7.0",
		"published_at": "2026-08-25T09:18:39Z",
		"draft": false,
		"prerelease": false,
		"assets": [
			{"name": "TDrive-v1.7.0-macos-arm64.zip", "size": 42, "browser_download_url": "https://github.com/owner/repo/releases/download/v1.7.0/TDrive-v1.7.0-macos-arm64.zip"},
			{"name": "checksums.txt", "size": 5, "browser_download_url": "https://github.com/owner/repo/releases/download/v1.7.0/checksums.txt"},
			{"name": "checksums.txt.sig", "size": 173, "browser_download_url": "https://github.com/owner/repo/releases/download/v1.7.0/checksums.txt.sig"},
			{"name": "bad", "size": 1, "browser_download_url": "http://insecure.example/bad"}
		]
	}`)
	release, err := source.LatestRelease(context.Background())
	if err != nil {
		t.Fatalf("LatestRelease: %v", err)
	}
	if release.Tag != "v1.7.0" || release.Version.String() != "1.7.0" {
		t.Fatalf("release = %+v", release)
	}
	if release.PublishedAt.IsZero() {
		t.Fatalf("published_at not parsed")
	}
	if len(release.Assets) != 3 {
		t.Fatalf("assets = %+v, want the three https assets", release.Assets)
	}
}

func TestGitHubSourceErrors(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		headers map[string]string
		body    string
		want    error
	}{
		{"no release", http.StatusNotFound, nil, `{}`, ErrNoRelease},
		{"rate limited", http.StatusForbidden, map[string]string{"X-RateLimit-Remaining": "0"}, `{}`, ErrRateLimited},
		{"too many requests", http.StatusTooManyRequests, nil, `{}`, ErrRateLimited},
		{"prerelease", http.StatusOK, nil, `{"tag_name":"v2.0.0","html_url":"https://github.com/o/r","prerelease":true}`, ErrNoRelease},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := githubServer(t, tc.status, tc.headers, tc.body)
			_, err := source.LatestRelease(context.Background())
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}

	t.Run("bad tag", func(t *testing.T) {
		source := githubServer(t, http.StatusOK, nil, `{"tag_name":"nightly","html_url":"https://github.com/o/r"}`)
		if _, err := source.LatestRelease(context.Background()); err == nil {
			t.Fatalf("expected error for non-version tag")
		}
	})
	t.Run("foreign page url", func(t *testing.T) {
		source := githubServer(t, http.StatusOK, nil, `{"tag_name":"v1.0.0","html_url":"https://evil.example/x"}`)
		if _, err := source.LatestRelease(context.Background()); err == nil {
			t.Fatalf("expected error for non-github page url")
		}
	})
	t.Run("server error", func(t *testing.T) {
		source := githubServer(t, http.StatusInternalServerError, nil, ``)
		if _, err := source.LatestRelease(context.Background()); err == nil {
			t.Fatalf("expected error for 500")
		}
	})
}
