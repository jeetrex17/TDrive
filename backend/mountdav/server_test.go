package mountdav

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
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

func TestServerLocalHostnameKeepsLoopbackAndExactAuthority(t *testing.T) {
	fs := testFS(t, &memoryOpener{data: []byte("named mount bytes")})
	server := NewServer()
	config := StartConfig{FS: fs.fs, DriveID: 123, LocalHostname: "TDrive.localhost"}
	status, err := server.Start(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Stop(context.Background()); err != nil {
			t.Error(err)
		}
	})
	endpoint, err := url.Parse(status.URL)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.Hostname() != "TDrive.localhost" {
		t.Fatalf("hostname = %q", endpoint.Hostname())
	}
	address := server.listener.Addr().String()
	if host, _, err := net.SplitHostPort(address); err != nil || host != "127.0.0.1" {
		t.Fatalf("listener = %q, %v; want IPv4 loopback", address, err)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport}
	for _, tc := range []struct {
		name string
		host string
		path string
		want int
	}{
		{"fixed alias", endpoint.Host, endpoint.Path, http.StatusOK},
		{"numeric host", address, endpoint.Path, http.StatusForbidden},
		{"foreign host", "other.localhost:" + endpoint.Port(), endpoint.Path, http.StatusForbidden},
		{"wrong port", "TDrive.localhost:1", endpoint.Path, http.StatusForbidden},
		{"wrong token", endpoint.Host, "/tdrive-wrong/", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+endpoint.Host+tc.path+"Docs/note.txt", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Host = tc.host
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", response.StatusCode, tc.want)
			}
			if tc.want == http.StatusOK && string(body) != "named mount bytes" {
				t.Fatalf("body = %q", body)
			}
		})
	}
}

func TestServerRejectsArbitraryLocalHostname(t *testing.T) {
	fs := testFS(t, &memoryOpener{})
	for _, hostname := range []string{"example.com", "localhost", "127.0.0.1", "tdrive.localhost", "TDrive.localhost.", "TDrive.localhost:80"} {
		if _, err := validateStartConfig(t.Context(), StartConfig{FS: fs.fs, DriveID: 123, LocalHostname: hostname}); err == nil {
			t.Errorf("accepted hostname %q", hostname)
		}
	}
}
