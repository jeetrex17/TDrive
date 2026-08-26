package media

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/thumbnail"
)

func TestMediaCORSPolicyAllowsAppOriginsAndExplicitDevOrigins(t *testing.T) {
	t.Setenv(mediaAllowedOriginsEnv, "http://localhost:5173, http://127.0.0.1:34115, http://[::1]:5173")

	for _, origin := range []string{
		"wails://wails",
		"http://wails.localhost",
		"http://localhost:5173",
		"http://127.0.0.1:34115",
		"http://[::1]:5173",
	} {
		req := httptest.NewRequest(http.MethodGet, "/media/file/token", nil)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()

		if !applyMediaCORS(rec, req, true) {
			t.Fatalf("%s rejected by CORS policy", origin)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Fatalf("%s Access-Control-Allow-Origin = %q, want echo", origin, got)
		}
		if got := rec.Header().Get("Vary"); got != "Origin" {
			t.Fatalf("%s Vary = %q, want Origin", origin, got)
		}
	}
}

func TestMediaCORSPolicyRejectsUnknownOriginsAndAllowsOriginlessNativeRequests(t *testing.T) {
	t.Setenv(mediaAllowedOriginsEnv, "http://localhost:5173")

	for _, origin := range []string{"https://example.com", "http://localhost:9999", "null"} {
		req := httptest.NewRequest(http.MethodGet, "/media/file/token", nil)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()

		if applyMediaCORS(rec, req, true) {
			t.Fatalf("%s unexpectedly accepted by CORS policy", origin)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("%s Access-Control-Allow-Origin = %q, want empty", origin, got)
		}
		if got := rec.Header().Get("Vary"); got != "Origin" {
			t.Fatalf("%s Vary = %q, want Origin", origin, got)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/media/file/token", nil)
	rec := httptest.NewRecorder()
	if !applyMediaCORS(rec, req, true) {
		t.Fatal("originless native request rejected")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("originless Access-Control-Allow-Origin = %q, want empty", got)
	}
}

func TestMediaCORSPolicyAllowsOnlyConfiguredWailsDevOrigins(t *testing.T) {
	t.Setenv(mediaAllowedOriginsEnv, "")

	tests := []struct {
		name           string
		frontendDevURL string
		wailsDevServer string
		origin         string
		wantAllow      bool
	}{
		{
			name:           "frontend server",
			frontendDevURL: "http://localhost:5173",
			origin:         "http://localhost:5173",
			wantAllow:      true,
		},
		{
			name:           "browser proxy",
			frontendDevURL: "http://localhost:5173",
			wailsDevServer: "localhost:34115",
			origin:         "http://localhost:34115",
			wantAllow:      true,
		},
		{
			name:           "Windows desktop proxy",
			frontendDevURL: "http://localhost:5173",
			wailsDevServer: "localhost:34115",
			origin:         "http://wails.localhost:34115",
			wantAllow:      true,
		},
		{
			name:           "macOS and Linux desktop proxy",
			frontendDevURL: "http://localhost:5173",
			wailsDevServer: "localhost:34115",
			origin:         "wails://wails.localhost:34115",
			wantAllow:      true,
		},
		{
			name:           "IPv4 browser proxy",
			frontendDevURL: "http://127.0.0.1:5173/app",
			wailsDevServer: "127.0.0.1:34115",
			origin:         "http://127.0.0.1:34115",
			wantAllow:      true,
		},
		{
			name:           "IPv6 browser proxy",
			frontendDevURL: "https://[::1]:5173/app",
			wailsDevServer: "[::1]:34115",
			origin:         "http://[::1]:34115",
			wantAllow:      true,
		},
		{
			name:           "default frontend port is canonicalized",
			frontendDevURL: "http://localhost:80/app",
			origin:         "http://localhost",
			wantAllow:      true,
		},
		{
			name:           "IPv6 default frontend port is canonicalized",
			frontendDevURL: "https://[::1]:443/app",
			origin:         "https://[::1]",
			wantAllow:      true,
		},
		{
			name:           "default browser proxy port is canonicalized",
			frontendDevURL: "http://localhost:5173",
			wailsDevServer: "localhost:80",
			origin:         "http://localhost",
			wantAllow:      true,
		},
		{
			name:           "IPv6 custom proxy keeps explicit port",
			frontendDevURL: "http://localhost:5173",
			wailsDevServer: "[::1]:80",
			origin:         "wails://wails.localhost:80",
			wantAllow:      true,
		},
		{
			name:           "different proxy port",
			frontendDevURL: "http://localhost:5173",
			wailsDevServer: "localhost:34115",
			origin:         "http://localhost:9999",
		},
		{
			name:           "different proxy host",
			frontendDevURL: "http://localhost:5173",
			wailsDevServer: "localhost:34115",
			origin:         "http://127.0.0.1:34115",
		},
		{
			name:           "remote frontend disables proxy origins",
			frontendDevURL: "https://example.com:5173",
			wailsDevServer: "localhost:34115",
			origin:         "http://localhost:34115",
		},
		{
			name:           "remote proxy host",
			frontendDevURL: "http://localhost:5173",
			wailsDevServer: "192.0.2.1:34115",
			origin:         "http://192.0.2.1:34115",
		},
		{
			name:           "wildcard proxy bind",
			frontendDevURL: "http://localhost:5173",
			wailsDevServer: "0.0.0.0:34115",
			origin:         "http://wails.localhost:34115",
		},
		{
			name:           "localhost lookalike frontend",
			frontendDevURL: "http://localhost.example.com:5173",
			wailsDevServer: "localhost:34115",
			origin:         "http://localhost:34115",
		},
		{
			name:           "frontend URL credentials",
			frontendDevURL: "http://developer@localhost:5173",
			wailsDevServer: "localhost:34115",
			origin:         "http://localhost:34115",
		},
		{
			name:           "non HTTP frontend scheme",
			frontendDevURL: "ftp://localhost:5173",
			wailsDevServer: "localhost:34115",
			origin:         "http://localhost:34115",
		},
		{
			name:           "malformed frontend port",
			frontendDevURL: "http://localhost:not-a-port",
			wailsDevServer: "localhost:34115",
			origin:         "http://localhost:34115",
		},
		{
			name:           "frontend URL missing host",
			frontendDevURL: "http:///app",
			wailsDevServer: "localhost:34115",
			origin:         "http://localhost:34115",
		},
		{
			name:           "proxy address credentials",
			frontendDevURL: "http://localhost:5173",
			wailsDevServer: "developer@localhost:34115",
			origin:         "http://wails.localhost:34115",
		},
		{
			name:           "malformed proxy port",
			frontendDevURL: "http://localhost:5173",
			wailsDevServer: "localhost:not-a-port",
			origin:         "http://wails.localhost:34115",
		},
		{
			name:           "missing proxy address",
			frontendDevURL: "http://localhost:5173",
			origin:         "http://localhost:34115",
		},
		{
			name:           "proxy port out of range",
			frontendDevURL: "http://localhost:5173",
			wailsDevServer: "localhost:65536",
			origin:         "http://wails.localhost:65536",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(mediaWailsFrontendEnv, tt.frontendDevURL)
			t.Setenv(mediaWailsDevServerEnv, tt.wailsDevServer)
			if got := isAllowedMediaOrigin(tt.origin); got != tt.wantAllow {
				t.Fatalf(
					"isAllowedMediaOrigin(%q) = %v with frontend URL %q and Wails server %q, want %v",
					tt.origin,
					got,
					tt.frontendDevURL,
					tt.wailsDevServer,
					tt.wantAllow,
				)
			}
		})
	}
}

func TestMediaCORSPolicyRejectsArbitraryLoopbackOriginsOutsideDev(t *testing.T) {
	t.Setenv(mediaAllowedOriginsEnv, "")
	t.Setenv(mediaWailsFrontendEnv, "")
	t.Setenv(mediaWailsDevServerEnv, "")

	for _, origin := range []string{
		"http://localhost:34115",
		"http://127.0.0.1:5173",
		"https://[::1]:5173",
		"http://wails.localhost:34115",
		"wails://wails.localhost:34115",
	} {
		if isAllowedMediaOrigin(origin) {
			t.Fatalf("%s unexpectedly accepted without a configured dev server", origin)
		}
	}
}

func TestPublicThumbnailResponsesRemainCacheable(t *testing.T) {
	db := newResolverTestDB(t)
	body := testBytes(512)
	mustApplyOp(t, db, 10, projection.Op{
		Type:     projection.OpFileUpload,
		Parent:   projection.RootParent,
		Name:     "public.mp4",
		FileSize: int64(len(body)),
	})
	ranges := newMediaRangeFake(map[int64][]byte{10: body})
	thumbGen := &fakeVideoThumbGenerator{available: true}
	svc := NewService(Config{
		DB:             db,
		Peers:          staticPeerResolver{peer: ranges.peer},
		Ranges:         ranges,
		Thumbs:         thumbnail.NewCache(t.TempDir(), 1<<20),
		ThumbGenerator: thumbGen,
	})
	defer svc.Close()

	opened, err := svc.Open(context.Background(), testChannelID, 10)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	thumbURL := opened.ThumbnailURL + "?t=0"
	_ = waitForThumbnail(t, thumbURL)
	resp, err := http.Get(thumbURL)
	if err != nil {
		t.Fatalf("thumbnail GET: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("thumbnail status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); !strings.Contains(got, "private") || !strings.Contains(got, "max-age=86400") {
		t.Fatalf("Cache-Control = %q, want private max-age cache header", got)
	}
}
