//go:build windows

package daemon

import (
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestSocketPathUsesCurrentUserWindowsNamedPipe(t *testing.T) {
	t.Parallel()

	path, err := SocketPath()
	if err != nil {
		t.Fatalf("SocketPath() error = %v", err)
	}
	currentUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatalf("GetTokenUser() error = %v", err)
	}
	want := `\\.\pipe\TDrive-daemon-` + currentUser.User.Sid.String()
	if path != want {
		t.Fatalf("SocketPath() = %q, want %q", path, want)
	}
}

func TestWindowsNamedPipeListenAndDial(t *testing.T) {
	t.Parallel()

	path := fmt.Sprintf(`\\.\pipe\TDrive-daemon-test-%d-%d`, os.Getpid(), time.Now().UnixNano())
	listener, err := listenSocket(path)
	if err != nil {
		t.Fatalf("listenSocket() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	type acceptResult struct {
		conn net.Conn
		err  error
	}
	accepted := make(chan acceptResult, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		accepted <- acceptResult{conn: conn, err: acceptErr}
	}()

	client, err := dialSocket(path)
	if err != nil {
		t.Fatalf("dialSocket() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	var result acceptResult
	select {
	case result = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("Accept() did not complete after dial")
	}
	if result.err != nil {
		t.Fatalf("Accept() error = %v", result.err)
	}
	server := result.conn
	t.Cleanup(func() { _ = server.Close() })
	deadline := time.Now().Add(5 * time.Second)
	if err := client.SetDeadline(deadline); err != nil {
		t.Fatalf("client.SetDeadline() error = %v", err)
	}
	if err := server.SetDeadline(deadline); err != nil {
		t.Fatalf("server.SetDeadline() error = %v", err)
	}

	const payload = "tdrive-pipe-test"
	if _, err := client.Write([]byte(payload)); err != nil {
		t.Fatalf("client.Write() error = %v", err)
	}
	received := make([]byte, len(payload))
	if _, err := io.ReadFull(server, received); err != nil {
		t.Fatalf("server read error = %v", err)
	}
	if string(received) != payload {
		t.Fatalf("server received %q, want %q", received, payload)
	}
}

func TestCurrentUserPipeSecurityDescriptorIsValid(t *testing.T) {
	t.Parallel()

	descriptor, err := currentUserPipeSecurityDescriptor()
	if err != nil {
		t.Fatalf("currentUserPipeSecurityDescriptor() error = %v", err)
	}
	currentUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatalf("GetTokenUser() error = %v", err)
	}
	want := "D:P(A;;GA;;;" + currentUser.User.Sid.String() + ")"
	if descriptor != want {
		t.Fatalf("currentUserPipeSecurityDescriptor() = %q, want %q", descriptor, want)
	}
}
