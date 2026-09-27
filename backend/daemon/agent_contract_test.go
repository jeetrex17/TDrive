package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"TDrive/backend/mountcontroller"
)

func TestPingDoesNotCheckEncryptionPolicy(t *testing.T) {
	configureDaemonPolicyTestHome(t)
	const activeID int64 = 8_800_001
	const otherID int64 = 8_800_002
	engine := newDaemonMountEngineWithPolicyRefresh(t, activeID, otherID, func(_ context.Context, _ int64) error {
		t.Fatal("ping refreshed encryption policy")
		return nil
	})
	server := &Server{engine: engine, state: newState()}

	frame := server.handleRequest(t.Context(), Request{Version: ProtocolVersion, Command: CommandPing})
	if !frame.OK {
		t.Fatalf("ping failed: %s", frame.Error)
	}
	var response PingResponse
	if err := json.Unmarshal(frame.Payload, &response); err != nil {
		t.Fatalf("decode ping: %v", err)
	}
	if response.PID <= 0 {
		t.Fatalf("ping PID = %d", response.PID)
	}
}

func TestExplicitDriveListDoesNotMutateActiveDrive(t *testing.T) {
	configureDaemonPolicyTestHome(t)
	const activeID int64 = 8_900_001
	const selectedID int64 = 8_900_002
	engine := newDaemonMountEngine(t, activeID, selectedID)
	state := newState()
	state.CurrentDriveID = activeID
	state.setCWD(activeID, "/prior")
	server := &Server{engine: engine, state: state}

	req, err := NewRequest(CommandList, PathRequest{Path: "/", DriveID: selectedID})
	if err != nil {
		t.Fatal(err)
	}
	frame := server.handleRequest(t.Context(), req)
	if !frame.OK {
		t.Fatalf("explicit-drive list failed: %s", frame.Error)
	}
	var response ListResponse
	if err := json.Unmarshal(frame.Payload, &response); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if response.Drive.ID != selectedID || response.Path != "/" {
		t.Fatalf("list response = %#v", response)
	}
	if got := engine.ActiveChannelID(); got != activeID {
		t.Fatalf("active drive changed to %d", got)
	}
	if state.CurrentDriveID != activeID || state.cwd(activeID) != "/prior" {
		t.Fatalf("saved CLI state changed: %#v", state)
	}

	req, err = NewRequest(CommandList, PathRequest{Path: "relative", DriveID: selectedID})
	if err != nil {
		t.Fatal(err)
	}
	if frame := server.handleRequest(t.Context(), req); frame.OK {
		t.Fatal("explicit-drive request accepted a relative path")
	}
}

func TestMountPasswordErrorHasStableCode(t *testing.T) {
	frame := ErrorResponse("request", fmt.Errorf("mount: %w", mountcontroller.ErrEncryptionPasswordRequired))
	if frame.ErrorCode != "encryption_password_required" {
		t.Fatalf("error code = %q", frame.ErrorCode)
	}
	var remote *RemoteError
	if err := errorFromFrame(frame); !errors.As(err, &remote) || remote.Code != frame.ErrorCode {
		t.Fatalf("remote error = %v", err)
	}
}

func TestExplicitDriveRejectsRelativePaths(t *testing.T) {
	configureDaemonPolicyTestHome(t)
	const activeID int64 = 9_000_001
	const selectedID int64 = 9_000_002
	engine := newDaemonMountEngine(t, activeID, selectedID)
	server := &Server{engine: engine, state: newState()}
	localPath := filepath.Join(t.TempDir(), "upload.txt")
	if err := os.WriteFile(localPath, []byte("sample"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name    string
		command string
		payload any
	}{
		{"list", CommandList, PathRequest{Path: "relative", DriveID: selectedID}},
		{"mkdir", CommandMkdir, MkdirRequest{Path: "relative", DriveID: selectedID}},
		{"remove", CommandRemove, RemoveRequest{Path: "relative", DriveID: selectedID}},
		{"move source", CommandMove, MoveRequest{Source: "relative", Destination: "/target", DriveID: selectedID}},
		{"move destination", CommandMove, MoveRequest{Source: "/source", Destination: "relative", DriveID: selectedID}},
		{"download", CommandDownload, DownloadRequest{RemotePath: "relative", LocalPath: filepath.Join(t.TempDir(), "out"), DriveID: selectedID}},
		{"upload", CommandUpload, UploadRequest{LocalPath: localPath, RemotePath: "relative", DriveID: selectedID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := NewRequest(tc.command, tc.payload)
			if err != nil {
				t.Fatal(err)
			}
			frame := server.handleRequest(t.Context(), req)
			if frame.OK || frame.ErrorCode != "invalid_request" {
				t.Fatalf("response = %#v, want invalid_request", frame)
			}
		})
	}
}

func TestCDRejectsDriveIDInsteadOfIgnoringIt(t *testing.T) {
	req, err := NewRequest(CommandCD, PathRequest{Path: "/", DriveID: 123})
	if err != nil {
		t.Fatal(err)
	}
	frame := (&Server{}).handleRequest(t.Context(), req)
	if frame.OK || frame.ErrorCode != "invalid_request" {
		t.Fatalf("response = %#v, want invalid_request", frame)
	}
}

func TestClientTimeoutCoversCallAndStream(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*Client) error
	}{
		{"call", func(client *Client) error { _, err := client.Ping(); return err }},
		{"stream", func(client *Client) error { _, err := client.Login("+10000000000", nil); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clientConn, serverConn := net.Pipe()
			serverDone := make(chan struct{})
			go func() {
				defer close(serverDone)
				_, _ = io.Copy(io.Discard, serverConn)
			}()
			t.Cleanup(func() {
				_ = serverConn.Close()
				<-serverDone
			})

			base := &Client{dial: func() (net.Conn, error) { return clientConn, nil }}
			timed := base.WithTimeout(50 * time.Millisecond)
			if base.timeout != 0 || timed == base {
				t.Fatal("WithTimeout changed the original client")
			}
			err := tc.run(timed)
			var netErr net.Error
			if !errors.As(err, &netErr) || !netErr.Timeout() {
				t.Fatalf("request error = %v, want network timeout", err)
			}
		})
	}
}
