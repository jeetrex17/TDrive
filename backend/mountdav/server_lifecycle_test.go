package mountdav

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"TDrive/backend/mountfs"
)

func TestValidateWindowsDrive(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		input string
		want  string
		ok    bool
	}{
		{input: "", want: "T:", ok: true},
		{input: "  ", want: "T:", ok: true},
		{input: "t", want: "T:", ok: true},
		{input: "t:", want: "T:", ok: true},
		{input: " Z: ", want: "Z:", ok: true},
		{input: "AA", ok: false},
		{input: "C:/", ok: false},
		{input: "1:", ok: false},
		{input: "é:", ok: false},
	} {
		got, err := validateWindowsDrive(test.input)
		if test.ok && (err != nil || got != test.want) {
			t.Errorf("validateWindowsDrive(%q) = (%q, %v), want (%q, nil)", test.input, got, err, test.want)
		}
		if !test.ok && err == nil {
			t.Errorf("validateWindowsDrive(%q) unexpectedly returned %q", test.input, got)
		}
	}
}

func TestValidateStartConfigRejectsTypedNilReadFilesystem(t *testing.T) {
	t.Parallel()

	var filesystem *mountfs.Aggregate
	if _, err := validateStartConfig(context.Background(), StartConfig{FS: filesystem, DriveID: 1}); err == nil {
		t.Fatal("validateStartConfig() accepted a typed nil filesystem")
	}
}

func TestServerRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()
	fs := testFS(t, nil)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name string
		ctx  context.Context
		cfg  StartConfig
	}{
		{name: "nil context", cfg: StartConfig{FS: fs.fs, DriveID: 1}},
		{name: "cancelled context", ctx: cancelled, cfg: StartConfig{FS: fs.fs, DriveID: 1}},
		{name: "nil filesystem", ctx: context.Background(), cfg: StartConfig{DriveID: 1}},
		{name: "zero drive", ctx: context.Background(), cfg: StartConfig{FS: fs.fs}},
		{name: "negative drive", ctx: context.Background(), cfg: StartConfig{FS: fs.fs, DriveID: -2}},
		{name: "bad windows drive", ctx: context.Background(), cfg: StartConfig{FS: fs.fs, DriveID: 1, WindowsDrive: "T:/"}},
	}
	for _, test := range tests {
		if _, err := NewServer().Start(test.ctx, test.cfg); err == nil {
			t.Errorf("%s Start unexpectedly succeeded", test.name)
		}
	}
}

func TestServerStartIsIdempotentOnlyForSameConfig(t *testing.T) {
	fs := testFS(t, nil)
	server := NewServer()
	config := StartConfig{FS: fs.fs, DriveID: 42, DriveTitle: "Drive", WindowsDrive: "x"}
	first, err := server.Start(context.Background(), config)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = server.Stop(context.Background()) })
	second, err := server.Start(context.Background(), config)
	if err != nil || second.URL != first.URL {
		t.Fatalf("same-config Start = (%+v, %v), want existing status", second, err)
	}
	if _, err := server.Start(context.Background(), StartConfig{FS: fs.fs, DriveID: 42, DriveTitle: "Other", WindowsDrive: "X:"}); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("different-config Start error = %v, want ErrAlreadyRunning", err)
	}
	if status := server.Status(); !status.Running || status.WindowsDrive != "X:" || status.URL != first.URL {
		t.Fatalf("Status = %+v", status)
	}
	if err := server.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if status := server.Status(); status != (Status{}) {
		t.Fatalf("Status after Stop = %+v", status)
	}
	if err := server.Stop(nil); err != nil {
		t.Fatalf("idempotent Stop(nil): %v", err)
	}
}

func TestUnexpectedServeFailureIsSanitizedAndGenerationSafe(t *testing.T) {
	t.Parallel()
	failed := &http.Server{}
	newer := &http.Server{}
	server := &Server{
		server: failed,
		status: Status{Running: true, URL: "http://127.0.0.1:1/secret/"},
	}
	server.recordServeFailure(newer)
	if server.server != failed || !server.status.Running {
		t.Fatal("stale Serve goroutine cleared the active server")
	}
	server.recordServeFailure(failed)
	if server.server != nil || server.status.Running || server.status.Error != sanitizedServeError || server.status.URL != "" || server.status.Commands != (Commands{}) {
		t.Fatalf("failed server state = %+v", server.status)
	}
	if strings.Contains(server.status.Error, "secret") {
		t.Fatalf("status error leaked capability: %q", server.status.Error)
	}
}

func TestUnexpectedClosedListenerClearsRunningStatus(t *testing.T) {
	httpServer := &http.Server{}
	listener := errorListener{err: net.ErrClosed}
	server := &Server{
		server:   httpServer,
		listener: listener,
		status: Status{
			Running: true,
			URL:     "http://127.0.0.1:1/secret/",
		},
	}
	server.serve(httpServer, listener)
	status := server.Status()
	if status.Running || status.URL != "" || status.Error != sanitizedServeError {
		t.Fatalf("closed-listener status = %+v", status)
	}
}

func TestUnexpectedServeFailureClosesActiveConnections(t *testing.T) {
	serverConnection, clientConnection := net.Pipe()
	defer clientConnection.Close()
	listener := newFailingListener(serverConnection)
	requestEntered := make(chan struct{})
	requestCanceled := make(chan struct{})
	httpServer := &http.Server{Handler: http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(requestEntered)
		<-request.Context().Done()
		close(requestCanceled)
	})}
	server := &Server{
		server:   httpServer,
		listener: listener,
		status:   Status{Running: true, URL: "http://127.0.0.1:1/secret/"},
	}
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		server.serve(httpServer, listener)
	}()
	go func() {
		_, _ = io.WriteString(clientConnection, "GET / HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n")
	}()
	select {
	case <-requestEntered:
	case <-time.After(time.Second):
		t.Fatal("request did not reach the test server")
	}
	listener.Fail()
	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("unexpected Serve failure left an active connection alive")
	}
	select {
	case <-serveDone:
	case <-time.After(time.Second):
		t.Fatal("Serve failure cleanup did not finish")
	}
	status := server.Status()
	if status.Running || status.URL != "" || status.Error != sanitizedServeError {
		t.Fatalf("failed status = %+v", status)
	}
}

type failingListener struct {
	connection net.Conn
	fail       chan struct{}
	failOnce   sync.Once
	accepted   bool
}

func newFailingListener(connection net.Conn) *failingListener {
	return &failingListener{connection: connection, fail: make(chan struct{})}
}

func (listener *failingListener) Accept() (net.Conn, error) {
	if !listener.accepted {
		listener.accepted = true
		return listener.connection, nil
	}
	<-listener.fail
	return nil, errors.New("injected listener failure")
}

func (listener *failingListener) Close() error {
	listener.Fail()
	return nil
}

func (listener *failingListener) Addr() net.Addr {
	return testNetworkAddress("test-listener")
}

func (listener *failingListener) Fail() {
	listener.failOnce.Do(func() { close(listener.fail) })
}

type testNetworkAddress string

func (address testNetworkAddress) Network() string { return "test" }
func (address testNetworkAddress) String() string  { return string(address) }

type errorListener struct {
	err error
}

func (listener errorListener) Accept() (net.Conn, error) { return nil, listener.err }
func (errorListener) Close() error                       { return nil }
func (errorListener) Addr() net.Addr                     { return testNetworkAddress("error-listener") }

func TestStopForcesClosedConnectionsAfterDefaultBound(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	httpServer := &http.Server{Handler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		once.Do(func() { close(entered) })
		<-release
		_, _ = response.Write([]byte("done"))
	})}
	server := NewServer()
	server.server = httpServer
	server.listener = listener
	server.status = Status{Running: true}
	server.shutdownTimeout = 25 * time.Millisecond
	go server.serve(httpServer, listener)

	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		response, requestErr := http.Get("http://" + listener.Addr().String() + "/")
		if requestErr == nil {
			_ = response.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("blocking request did not reach server")
	}

	started := time.Now()
	err = server.Stop(context.Background())
	elapsed := time.Since(started)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop error = %v, want deadline exceeded", err)
	}
	if elapsed > time.Second {
		t.Fatalf("Stop took %s despite bounded shutdown", elapsed)
	}
	close(release)
	select {
	case <-clientDone:
	case <-time.After(time.Second):
		t.Fatal("forced close did not release client")
	}
}

func TestRandomCapabilityAndCommandHints(t *testing.T) {
	t.Parallel()
	first, err := randomCapability()
	if err != nil {
		t.Fatalf("randomCapability: %v", err)
	}
	second, err := randomCapability()
	if err != nil {
		t.Fatalf("randomCapability second: %v", err)
	}
	if first == second || !regexp.MustCompile(`^tdrive-[0-9a-f]{64}$`).MatchString(first) {
		t.Fatalf("capabilities = %q and %q", first, second)
	}
	commands := commandHints("http://127.0.0.1:1/token/", "Q:")
	if commands.WindowsMap != "net use Q: http://127.0.0.1:1/token/ /persistent:no" || commands.WindowsUnmap != "net use Q: /delete" {
		t.Fatalf("commands = %+v", commands)
	}
	if commands.MacFinder != "Finder > Go > Connect to Server... > http://127.0.0.1:1/token/" || commands.LinuxMount != "gio mount http://127.0.0.1:1/token/" || commands.LinuxUnmount != "gio mount -u http://127.0.0.1:1/token/" {
		t.Fatalf("user-level mount hints = %+v", commands)
	}
	switch runtime.GOOS {
	case "windows":
		if commands.ActiveOSMount != commands.WindowsMap {
			t.Fatal("wrong active Windows command")
		}
	case "darwin":
		if commands.ActiveOSMount != commands.MacFinder {
			t.Fatal("wrong active macOS command")
		}
	case "linux":
		if commands.ActiveOSMount != commands.LinuxMount {
			t.Fatal("wrong active Linux command")
		}
	}
}

func TestServerReadProtocolSurface(t *testing.T) {
	opener := &memoryOpener{data: []byte("server bytes")}
	fs := testFS(t, opener)
	server := NewServer()
	status, err := server.Start(context.Background(), StartConfig{FS: fs.fs, DriveID: 123})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = server.Stop(context.Background()) })

	headRequest, err := http.NewRequest(http.MethodHead, status.URL+"Docs/note.txt", nil)
	if err != nil {
		t.Fatalf("NewRequest(HEAD): %v", err)
	}
	headResponse, err := http.DefaultClient.Do(headRequest)
	if err != nil {
		t.Fatalf("HEAD: %v", err)
	}
	_ = headResponse.Body.Close()
	if headResponse.StatusCode != http.StatusOK || headResponse.ContentLength != 12 || opener.calls != 0 {
		t.Fatalf("HEAD = status %d, length %d, content opens %d", headResponse.StatusCode, headResponse.ContentLength, opener.calls)
	}

	request, err := http.NewRequest(http.MethodGet, status.URL+"Docs/note.txt", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	request.Header.Set("Range", "bytes=2-5")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("range GET: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusPartialContent || string(body) != "rver" || response.Header.Get("Content-Range") != "bytes 2-5/12" {
		t.Fatalf("range response = %d/%q/%q/%v", response.StatusCode, response.Header.Get("Content-Range"), body, readErr)
	}
	if response.Header.Get("ETag") == "" || response.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("metadata headers = %#v", response.Header)
	}
	contentOpensBeforePropfind := opener.calls

	request, _ = http.NewRequest("PROPFIND", status.URL+"Docs/", bytes.NewBufferString(`<?xml version="1.0"?><propfind xmlns="DAV:"><allprop/></propfind>`))
	request.Header.Set("Depth", "1")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("PROPFIND: %v", err)
	}
	body, readErr = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil || response.StatusCode != 207 || !bytes.Contains(body, []byte("note.txt")) {
		t.Fatalf("PROPFIND = %d/%q/%v", response.StatusCode, body, readErr)
	}
	if opener.calls != contentOpensBeforePropfind {
		t.Fatalf("PROPFIND opened content: before=%d after=%d", contentOpensBeforePropfind, opener.calls)
	}

	request, _ = http.NewRequest(http.MethodOptions, status.URL, nil)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("OPTIONS: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Allow") != allowedMethodsHeader {
		t.Fatalf("OPTIONS = %d/%#v", response.StatusCode, response.Header)
	}

	request, _ = http.NewRequest(http.MethodPut, status.URL+"new.txt", strings.NewReader("no"))
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("PUT status = %d, want 405", response.StatusCode)
	}
}
