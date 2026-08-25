package mountdav

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestServerStartsTokenizedLoopbackWebDAV(t *testing.T) {
	opener := &memoryOpener{data: []byte("server bytes")}
	fs := testFS(t, opener)
	server := NewServer()
	status, err := server.Start(context.Background(), StartConfig{
		FS:         fs.fs,
		DriveID:    123,
		DriveTitle: "My Drive",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if err := server.Stop(context.Background()); err != nil {
			t.Fatalf("Stop: %v", err)
		}
	})

	if !status.Running || status.Mode != "read-only" {
		t.Fatalf("status = %+v, want read-only running", status)
	}
	if !strings.Contains(status.URL, "127.0.0.1:") || !strings.Contains(status.URL, "/tdrive-") {
		t.Fatalf("url = %q, want tokenized loopback URL", status.URL)
	}
	if !strings.HasPrefix(status.Commands.WindowsMap, "net use T: ") {
		t.Fatalf("windows map = %q, want T: hint", status.Commands.WindowsMap)
	}

	resp, err := http.Get(status.URL + "Docs/note.txt")
	if err != nil {
		t.Fatalf("GET mounted file: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "server bytes" {
		t.Fatalf("GET status/body = %d/%q", resp.StatusCode, body)
	}

	resp, err = http.Get(strings.Replace(status.URL, "/tdrive-", "/wrong-", 1))
	if err != nil {
		t.Fatalf("GET wrong token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("wrong token status = %d, want 404", resp.StatusCode)
	}
}
