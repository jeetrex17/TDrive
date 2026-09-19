package galleryimage

import (
	"TDrive/backend/tgclient"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	file "TDrive/backend/services/file"
)

func TestBinarySessionLifecycle(t *testing.T) {
	s := NewServer()
	defer s.Close()
	session, err := s.Open(context.Background(), 7, func(_ context.Context, ch, id, rev int64, kind string) (file.Rendition, error) {
		if ch != 7 || id != 91 || rev != 2 || kind != "thumbnail" {
			t.Errorf("wrong scope %d %d %d %s", ch, id, rev, kind)
		}
		return file.Rendition{Bytes: []byte("jpeg"), MimeType: "image/jpeg", Width: 32, Height: 16, Encrypted: true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	url := session.BaseURL + "/91/thumbnail?revision=2"
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(raw) != "jpeg" || resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("X-Rendition-Width") != "32" {
		t.Fatalf("response=%+v %q", resp, raw)
	}
	s.CloseSession(session.Token)
	resp, err = http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("revoked status=%d", resp.StatusCode)
	}
}
func TestRevocationCancelsInflightLoad(t *testing.T) {
	s := NewServer()
	defer s.Close()
	started := make(chan struct{})
	canceled := make(chan struct{})
	session, err := s.Open(context.Background(), 7, func(ctx context.Context, _, _, _ int64, _ string) (file.Rendition, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return file.Rendition{}, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		resp, err := http.Get(session.BaseURL + "/1/thumbnail")
		if err == nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("not started")
	}
	s.CloseSession(session.Token)
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("revocation did not cancel")
	}
}
func TestServerRejectsRebindingAndUnscopedURLs(t *testing.T) {
	s := NewServer()
	defer s.Close()
	session, err := s.Open(context.Background(), 1, func(context.Context, int64, int64, int64, string) (file.Rendition, error) {
		t.Error("unexpected load")
		return file.Rendition{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"/0/thumbnail", "/1/../../x", "/1/bogus", "/1/thumbnail?revision=-1"} {
		resp, err := http.Get(session.BaseURL + suffix)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode < 400 {
			t.Errorf("accepted %s", suffix)
		}
	}
	req, _ := http.NewRequest("GET", session.BaseURL+"/1/thumbnail", nil)
	req.Host = "attacker.example"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("host status=%d", resp.StatusCode)
	}
}

func TestSessionLimitsAndRevocation(t *testing.T) {
	s := NewServer()
	defer s.Close()
	load := func(context.Context, int64, int64, int64, string) (file.Rendition, error) {
		return file.Rendition{}, file.ErrRenditionMissing
	}
	for range maxSessions {
		if _, err := s.Open(context.Background(), 7, load); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Open(context.Background(), 7, load); !errors.Is(err, file.ErrRenditionBusy) {
		t.Fatalf("session cap=%v", err)
	}
	s.Revoke()
	if _, err := s.Open(context.Background(), 7, load); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	for _, session := range s.sessions {
		session.touched = time.Now().Add(-3 * time.Hour)
	}
	s.mu.Unlock()
	if _, err := s.Open(context.Background(), 7, load); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	count := len(s.sessions)
	s.mu.Unlock()
	if count != 1 {
		t.Fatalf("sessions=%d", count)
	}
	if _, err := s.Open(nil, 7, load); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Open(ctx, 7, load); err == nil {
		t.Fatal("canceled context accepted")
	}
	s.Close()
	if _, err := s.Open(context.Background(), 7, load); err == nil {
		t.Fatal("closed server accepted session")
	}
}
func TestErrorsRetryDeadlineAndRequestLimit(t *testing.T) {
	s := NewServer()
	defer s.Close()
	session, err := s.Open(context.Background(), 7, func(context.Context, int64, int64, int64, string) (file.Rendition, error) {
		return file.Rendition{}, tgclient.NewFloodWaitError(3701 * time.Second)
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(session.BaseURL + "/91/thumbnail")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 429 || resp.Header.Get("Retry-After") != "3701" {
		t.Fatalf("status=%d retry=%s", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	for range cap(s.requests) {
		s.requests <- struct{}{}
	}
	resp, err = http.Get(session.BaseURL + "/91/thumbnail")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 429 || resp.Header.Get("Retry-After") != "1" {
		t.Fatalf("request limit response=%+v", resp)
	}
	for range cap(s.requests) {
		<-s.requests
	}
	for method, want := range map[string]int{"POST": 405, "OPTIONS": 204} {
		req, _ := http.NewRequest(method, session.BaseURL+"/91/thumbnail", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("method=%s status=%d", method, resp.StatusCode)
		}
	}
}
