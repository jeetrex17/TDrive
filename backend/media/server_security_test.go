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
