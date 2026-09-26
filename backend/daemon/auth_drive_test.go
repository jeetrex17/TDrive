package daemon

import (
	"context"
	"testing"

	"TDrive/backend/auth"
)

func TestAuthStatusReusesVerifiedSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	engine := newDaemonMountEngine(t, 12345, 67890)
	if err := auth.SaveImpCredentials(123, "test-api-hash"); err != nil {
		t.Fatalf("save credentials: %v", err)
	}
	s := &Server{engine: engine, authReady: true, drivePrepared: true}

	for range 2 {
		response, err := s.authStatus(context.Background())
		if err != nil || !response.Status.LoggedIn {
			t.Fatalf("authStatus() = %+v, %v; want cached logged-in status", response, err)
		}
	}
}

func TestAuthLoginReusesVerifiedSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	engine := newDaemonMountEngine(t, 12345, 67890)
	s := &Server{engine: engine, authReady: true}

	response, err := s.authLogin(context.Background(), "+123456789")
	if err != nil {
		t.Fatalf("authLogin() with verified session: %v", err)
	}
	if !response.LoggedIn || response.ActiveChannelID != 12345 {
		t.Fatalf("authLogin() = %+v, want saved drive without another login", response)
	}
}
