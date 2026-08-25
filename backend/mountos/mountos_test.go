package mountos

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

const testEndpoint = "http://127.0.0.1:49152/tdrive-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/"
const testLinuxURI = "dav://127.0.0.1:49152/tdrive-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/"

func TestValidateEndpoint(t *testing.T) {
	t.Parallel()

	if got, err := validateEndpoint(testEndpoint); err != nil || got != testEndpoint {
		t.Fatalf("validateEndpoint(valid) = %q, %v", got, err)
	}

	invalid := []string{
		"",
		"https://127.0.0.1:49152/tdrive-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/",
		"http://localhost:49152/tdrive-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/",
		"http://127.0.0.1/tdrive-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/",
		"http://127.0.0.1:0/tdrive-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/",
		"http://127.0.0.1:65536/tdrive-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/",
		"http://user@127.0.0.1:49152/tdrive-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/",
		"http://127.0.0.1:49152/tdrive-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdeF/",
		"http://127.0.0.1:49152/tdrive-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"http://127.0.0.1:49152/tdrive-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/?x=1",
		"http://127.0.0.1:49152/tdrive-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/#x",
		"http://127.0.0.1:49152/prefix/tdrive-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/",
		"http://127.0.0.1:49152/tdrive-%300123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/",
		" " + testEndpoint,
	}
	for _, value := range invalid {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			if _, err := validateEndpoint(value); err == nil {
				t.Fatalf("validateEndpoint(%q) unexpectedly succeeded", value)
			}
		})
	}
}

func TestValidateLabel(t *testing.T) {
	t.Parallel()

	for _, label := range []string{"Tdrive personal", "TDrive – Personal", strings.Repeat("界", 64)} {
		if got, err := validateLabel(label); err != nil || got != label {
			t.Fatalf("validateLabel(%q) = %q, %v", label, got, err)
		}
	}

	for _, label := range []string{"", " Tdrive", "Tdrive ", "Tdrive\nPersonal", "Tdrive\x00Personal", "Tdrive\u202ePersonal", strings.Repeat("x", 65)} {
		if _, err := validateLabel(label); err == nil {
			t.Fatalf("validateLabel(%q) unexpectedly succeeded", label)
		}
	}
}

func TestNormalizeWindowsDrive(t *testing.T) {
	t.Parallel()

	for input, want := range map[string]string{"": "T:", "t:": "T:", "Q:": "Q:"} {
		got, err := normalizeWindowsDrive(input)
		if err != nil || got != want {
			t.Fatalf("normalizeWindowsDrive(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"T", "TT:", "1:", "T:/", " T:"} {
		if _, err := normalizeWindowsDrive(input); err == nil {
			t.Fatalf("normalizeWindowsDrive(%q) unexpectedly succeeded", input)
		}
	}
}

func TestCommandPlansUseFixedExecutablesAndSeparateArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  commandPlan
		path string
		args []string
	}{
		{
			name: "darwin attach",
			got:  darwinAttachPlan(testEndpoint, "Tdrive personal", "/tmp/tdrive-mount"),
			path: "/sbin/mount_webdav",
			args: []string{"-S", "-v", "Tdrive personal", "-o", "rdonly,noexec,nosuid,nodev", testEndpoint, "/tmp/tdrive-mount"},
		},
		{
			name: "darwin detach",
			got:  darwinDetachPlan("/tmp/tdrive-mount"),
			path: "/usr/sbin/diskutil",
			args: []string{"unmount", "/tmp/tdrive-mount"},
		},
		{
			name: "darwin open",
			got:  darwinOpenPlan("/tmp/tdrive-mount"),
			path: "/usr/bin/open",
			args: []string{"/tmp/tdrive-mount"},
		},
		{
			name: "linux attach",
			got:  linuxAttachPlan(testLinuxURI),
			path: "/usr/bin/gio",
			args: []string{"mount", "--anonymous", testLinuxURI},
		},
		{
			name: "linux detach",
			got:  linuxDetachPlan(testLinuxURI),
			path: "/usr/bin/gio",
			args: []string{"mount", "-u", testLinuxURI},
		},
		{
			name: "linux open",
			got:  linuxOpenPlan(testLinuxURI),
			path: "/usr/bin/gio",
			args: []string{"open", testLinuxURI},
		},
		{
			name: "windows attach",
			got:  windowsAttachPlan(`C:\Windows\System32\net.exe`, "T:", testEndpoint),
			path: `C:\Windows\System32\net.exe`,
			args: []string{"use", "T:", testEndpoint, "/persistent:no"},
		},
		{
			name: "windows detach",
			got:  windowsDetachPlan(`C:\Windows\System32\net.exe`, "T:"),
			path: `C:\Windows\System32\net.exe`,
			args: []string{"use", "T:", "/delete", "/yes"},
		},
		{
			name: "windows open",
			got:  windowsOpenPlan(`C:\Windows\explorer.exe`, `T:\`),
			path: `C:\Windows\explorer.exe`,
			args: []string{`T:\`},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.got.Path != tt.path {
				t.Fatalf("path = %q; want %q", tt.got.Path, tt.path)
			}
			if strings.Join(tt.got.Args, "\x00") != strings.Join(tt.args, "\x00") {
				t.Fatalf("args = %#v; want %#v", tt.got.Args, tt.args)
			}
			if tt.got.Path == "/bin/sh" || tt.got.Path == "/bin/bash" || strings.HasSuffix(strings.ToLower(tt.got.Path), "cmd.exe") {
				t.Fatalf("unsafe command interpreter: %q", tt.got.Path)
			}
		})
	}
}

func TestAttachmentExposesOnlySafeLocationAndKind(t *testing.T) {
	t.Parallel()

	attachment := Attachment{owner: &ownerMarker{}, id: 1, kind: "darwin", location: "/tmp/Tdrive personal"}
	if attachment.Kind() != "darwin" || attachment.Location() != "/tmp/Tdrive personal" {
		t.Fatalf("unexpected attachment view: kind=%q location=%q", attachment.Kind(), attachment.Location())
	}
	if strings.Contains(attachment.Location(), "tdrive-") {
		t.Fatal("safe location leaked the capability path")
	}
	for _, formatted := range []string{
		fmt.Sprintf("%v", attachment),
		fmt.Sprintf("%+v", attachment),
		fmt.Sprintf("%#v", attachment),
	} {
		if strings.Contains(formatted, "tdrive-") || strings.Contains(formatted, "127.0.0.1") {
			t.Fatalf("formatted attachment leaked capability: %q", formatted)
		}
	}
}

func TestLimitedBufferEnforcesHardLimit(t *testing.T) {
	t.Parallel()

	buffer := &limitedBuffer{remaining: 3}
	written, err := buffer.Write([]byte("abcd"))
	if err == nil || written != 3 || buffer.String() != "abc" {
		t.Fatalf("Write(over limit) = %d, %v, %q; want 3, error, abc", written, err, buffer.String())
	}
}

func TestValidateConfigReportsBoundaryErrors(t *testing.T) {
	t.Parallel()

	if _, err := validateConfig(Config{Endpoint: testEndpoint, Label: "bad\nlabel"}); !errors.Is(err, ErrInvalidLabel) {
		t.Fatalf("validateConfig(label) = %v", err)
	}
	if _, err := validateConfig(Config{Endpoint: testEndpoint, Label: "Tdrive personal", WindowsDrive: "bad"}); !errors.Is(err, ErrInvalidDrive) {
		t.Fatalf("validateConfig(drive) = %v", err)
	}
	if New() == nil {
		t.Fatal("New() returned nil")
	}
}
