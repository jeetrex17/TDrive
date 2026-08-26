package media

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
