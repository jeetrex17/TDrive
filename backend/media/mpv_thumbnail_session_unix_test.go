//go:build !windows

package media

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
)

func TestMPVThumbnailSessionCommandRedactsLoadfileURL(t *testing.T) {
	sourceURL := "http://127.0.0.1:49152/media/thumb-source/session-token-secret"
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	session := &mpvThumbnailSession{
		conn:   clientConn,
		reader: bufio.NewReader(clientConn),
	}

	replyDone := make(chan error, 1)
	go func() {
		defer close(replyDone)
		var payload struct {
			RequestID int64 `json:"request_id"`
		}
		if err := json.NewDecoder(serverConn).Decode(&payload); err != nil {
			replyDone <- err
			return
		}
		replyDone <- json.NewEncoder(serverConn).Encode(map[string]any{
			"request_id": payload.RequestID,
			"error":      "load failed for " + sourceURL,
		})
	}()

	err := session.command(context.Background(), []any{"loadfile", sourceURL, "replace"})
	if err == nil {
		t.Fatal("command unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), sourceURL) || strings.Contains(err.Error(), "session-token-secret") {
		t.Fatalf("persistent thumbnail command leaked source URL/token: %v", err)
	}
	if replyErr := <-replyDone; replyErr != nil {
		t.Fatalf("reply goroutine: %v", replyErr)
	}
}
