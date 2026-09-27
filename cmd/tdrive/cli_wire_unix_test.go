//go:build !windows

package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"TDrive/backend/daemon"
)

func TestRunListSendsExplicitDriveID(t *testing.T) {
	base, err := os.MkdirTemp("", "tdrive-cli-wire-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(base) }()
	t.Setenv("XDG_RUNTIME_DIR", base)
	socket, err := daemon.SocketPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	if err := listener.(*net.UnixListener).SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}

	requests := make(chan daemon.Request, 2)
	serverErr := make(chan error, 1)
	go func() {
		for range 2 {
			conn, err := listener.Accept()
			if err != nil {
				serverErr <- err
				return
			}
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			var req daemon.Request
			if err := json.NewDecoder(conn).Decode(&req); err != nil {
				_ = conn.Close()
				serverErr <- err
				return
			}
			requests <- req
			var result any = daemon.PingResponse{PID: os.Getpid()}
			if req.Command == daemon.CommandList {
				result = daemon.ListResponse{Drive: daemon.Drive{ID: 42}, Path: "/Photos"}
			}
			frame, err := daemon.Response(req.ID, result)
			if err == nil {
				err = json.NewEncoder(conn).Encode(frame)
			}
			_ = conn.Close()
			if err != nil {
				serverErr <- err
				return
			}
		}
		serverErr <- nil
	}()

	if err := run([]string{"ls", "/Photos", "--drive-id", "42"}); err != nil {
		t.Fatal(err)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
	if first := <-requests; first.Command != daemon.CommandPing {
		t.Fatalf("first command = %q, want ping", first.Command)
	}
	second := <-requests
	if second.Command != daemon.CommandList {
		t.Fatalf("second command = %q, want list", second.Command)
	}
	var pathRequest daemon.PathRequest
	if err := json.Unmarshal(second.Payload, &pathRequest); err != nil {
		t.Fatal(err)
	}
	if pathRequest.DriveID != 42 || pathRequest.Path != "/Photos" {
		t.Fatalf("list request = %+v", pathRequest)
	}
}
