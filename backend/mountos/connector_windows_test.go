//go:build windows

package mountos

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/windows"
)

type windowsRecordingRunner struct {
	mu        sync.Mutex
	plans     []commandPlan
	errAt     map[int]error
	beforeRun func()
}

func (r *windowsRecordingRunner) Run(ctx context.Context, plan commandPlan) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.beforeRun != nil {
		r.beforeRun()
	}
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("unbounded context")
	}
	index := len(r.plans)
	r.plans = append(r.plans, commandPlan{Path: plan.Path, Args: append([]string(nil), plan.Args...)})
	return r.errAt[index]
}

type remoteResult struct {
	remote string
	mapped bool
	err    error
}

type windowsFakeSystem struct {
	mu            sync.Mutex
	drives        uint32
	remoteResults []remoteResult
	remoteDrive   bool
}

func (s *windowsFakeSystem) logicalDrives() (uint32, error) { return s.drives, nil }

func (s *windowsFakeSystem) remoteFor(string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.remoteResults) == 0 {
		return "", false, errors.New("unexpected remote lookup")
	}
	result := s.remoteResults[0]
	s.remoteResults = s.remoteResults[1:]
	return result.remote, result.mapped, result.err
}

func (s *windowsFakeSystem) isRemoteDrive(string) (bool, error) { return s.remoteDrive, nil }

func newTestWindowsConnector(runner commandRunner, system *windowsFakeSystem) *windowsConnector {
	return newWindowsConnector(windowsDependencies{
		runner: runner,
		systemPaths: func() (string, string, error) {
			return `C:\Windows\System32\net.exe`, `C:\Windows\explorer.exe`, nil
		},
		logicalDrives:     system.logicalDrives,
		remoteFor:         system.remoteFor,
		isRemoteDrive:     system.isRemoteDrive,
		prepareWebClient:  func(context.Context) error { return nil },
		verificationDelay: func(context.Context) error { return context.DeadlineExceeded },
	})
}

func TestWindowsAttachPreparesWebClientBeforeMapping(t *testing.T) {
	remote := `\\127.0.0.1@49152\DavWWWRoot\tdrive-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef`
	system := &windowsFakeSystem{remoteDrive: true, remoteResults: []remoteResult{{remote: remote, mapped: true}}}
	prepared := false
	runner := &windowsRecordingRunner{beforeRun: func() {
		if !prepared {
			t.Fatal("mapping ran before the WebClient preflight")
		}
	}}
	connector := newWindowsConnector(windowsDependencies{
		runner: runner,
		systemPaths: func() (string, string, error) {
			return `C:\Windows\System32\net.exe`, `C:\Windows\explorer.exe`, nil
		},
		logicalDrives: system.logicalDrives,
		remoteFor:     system.remoteFor,
		isRemoteDrive: system.isRemoteDrive,
		prepareWebClient: func(context.Context) error {
			prepared = true
			return nil
		},
		verificationDelay: func(context.Context) error { return context.DeadlineExceeded },
	})

	if _, err := connector.Attach(context.Background(), Config{Endpoint: testEndpoint, Label: "Tdrive personal"}); err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	if !prepared {
		t.Fatal("WebClient preflight did not run")
	}
}

func TestWindowsAttachStopsBeforeMappingWhenWebClientIsUnavailable(t *testing.T) {
	system := &windowsFakeSystem{}
	runner := &windowsRecordingRunner{}
	connector := newWindowsConnector(windowsDependencies{
		runner: runner,
		systemPaths: func() (string, string, error) {
			return `C:\Windows\System32\net.exe`, `C:\Windows\explorer.exe`, nil
		},
		logicalDrives:     system.logicalDrives,
		remoteFor:         system.remoteFor,
		isRemoteDrive:     system.isRemoteDrive,
		prepareWebClient:  func(context.Context) error { return errors.New("service disabled") },
		verificationDelay: func(context.Context) error { return context.DeadlineExceeded },
	})

	_, err := connector.Attach(context.Background(), Config{Endpoint: testEndpoint, Label: "Tdrive personal"})
	if !errors.Is(err, ErrWindowsWebDAVUnavailable) {
		t.Fatalf("Attach() = %v, want Windows WebDAV unavailable", err)
	}
	if len(runner.plans) != 0 {
		t.Fatalf("mapping ran after failed WebClient preflight: %#v", runner.plans)
	}
}

func TestWindowsAttachCommandFailureIsCapabilityFree(t *testing.T) {
	system := &windowsFakeSystem{remoteResults: []remoteResult{{mapped: false}}}
	runner := &windowsRecordingRunner{errAt: map[int]error{0: errors.New("failed for " + testEndpoint)}}
	connector := newTestWindowsConnector(runner, system)

	_, err := connector.Attach(context.Background(), Config{Endpoint: testEndpoint, Label: "Tdrive personal"})
	if !errors.Is(err, ErrWindowsWebDAVUnavailable) {
		t.Fatalf("Attach() = %v, want Windows WebDAV unavailable", err)
	}
	if strings.Contains(err.Error(), "tdrive-") || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("Attach() leaked capability endpoint: %q", err)
	}
}

func TestWindowsAttachPreservesCanceledWebClientContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	connector := newWindowsConnector(windowsDependencies{})

	_, err := connector.Attach(ctx, Config{Endpoint: testEndpoint, Label: "Tdrive personal"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Attach() = %v, want context cancellation", err)
	}
}

func TestWindowsAttachRetriesColdMappingVerification(t *testing.T) {
	remote := `\\127.0.0.1@49152\DavWWWRoot\tdrive-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef`
	system := &windowsFakeSystem{remoteDrive: true, remoteResults: []remoteResult{
		{mapped: false},
		{remote: remote, mapped: true},
	}}
	retries := 0
	connector := newWindowsConnector(windowsDependencies{
		runner: &windowsRecordingRunner{},
		systemPaths: func() (string, string, error) {
			return `C:\Windows\System32\net.exe`, `C:\Windows\explorer.exe`, nil
		},
		logicalDrives:    system.logicalDrives,
		remoteFor:        system.remoteFor,
		isRemoteDrive:    system.isRemoteDrive,
		prepareWebClient: func(context.Context) error { return nil },
		verificationDelay: func(context.Context) error {
			retries++
			return nil
		},
	})

	if _, err := connector.Attach(context.Background(), Config{Endpoint: testEndpoint, Label: "Tdrive personal"}); err != nil {
		t.Fatalf("Attach() after cold mapping error = %v", err)
	}
	if retries != 1 {
		t.Fatalf("verification retries = %d, want 1", retries)
	}
}

func TestWindowsAttachOpenDetach(t *testing.T) {
	remote := `\\127.0.0.1@49152\DavWWWRoot\tdrive-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef`
	system := &windowsFakeSystem{remoteDrive: true, remoteResults: []remoteResult{
		{remote: remote, mapped: true},
		{remote: remote, mapped: true},
		{remote: remote, mapped: true},
		{mapped: false},
	}}
	runner := &windowsRecordingRunner{}
	connector := newTestWindowsConnector(runner, system)

	attachment, err := connector.Attach(context.Background(), Config{Endpoint: testEndpoint, Label: "Tdrive personal"})
	if err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	if attachment.Kind() != KindWindows || attachment.Location() != `T:\` {
		t.Fatalf("attachment = %q, %q", attachment.Kind(), attachment.Location())
	}
	if strings.Contains(attachment.Location(), "tdrive-") {
		t.Fatal("attachment location leaked capability")
	}
	if err := connector.Open(context.Background(), attachment); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := connector.Detach(context.Background(), attachment); err != nil {
		t.Fatalf("Detach() error = %v", err)
	}
	if len(runner.plans) != 3 {
		t.Fatalf("commands = %#v", runner.plans)
	}
}

func TestWindowsAttachNeverOverwritesOccupiedDrive(t *testing.T) {
	system := &windowsFakeSystem{drives: 1 << ('T' - 'A')}
	runner := &windowsRecordingRunner{}
	connector := newTestWindowsConnector(runner, system)

	_, err := connector.Attach(context.Background(), Config{Endpoint: testEndpoint, Label: "Tdrive personal", WindowsDrive: "T:"})
	if !errors.Is(err, ErrDriveOccupied) {
		t.Fatalf("Attach() = %v; want drive occupied", err)
	}
	if len(runner.plans) != 0 {
		t.Fatal("mapping command ran for occupied drive")
	}
}

func TestWindowsAttachRejectsUnexpectedSystemRemoteWithoutDeletingIt(t *testing.T) {
	system := &windowsFakeSystem{remoteDrive: true, remoteResults: []remoteResult{{remote: `\\other\share`, mapped: true}}}
	runner := &windowsRecordingRunner{}
	connector := newTestWindowsConnector(runner, system)

	_, err := connector.Attach(context.Background(), Config{Endpoint: testEndpoint, Label: "Tdrive personal"})
	if !errors.Is(err, ErrAttachmentChanged) {
		t.Fatalf("Attach() = %v; want attachment changed", err)
	}
	if len(runner.plans) != 1 || runner.plans[0].Args[0] != "use" {
		t.Fatalf("unexpected remote was deleted: %#v", runner.plans)
	}
}

func TestWindowsRemoteMatchesOnlyRequestedEndpoint(t *testing.T) {
	want := `\\127.0.0.1@49152\DavWWWRoot\tdrive-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef`
	if !windowsRemoteMatchesEndpoint(want, testEndpoint) || !windowsRemoteMatchesEndpoint(testEndpoint, testEndpoint) {
		t.Fatal("expected Windows WebDAV representations did not match")
	}
	if windowsRemoteMatchesEndpoint(`\\other\share`, testEndpoint) {
		t.Fatal("unrelated Windows remote matched the endpoint")
	}
}

func TestWindowsDetachRequiresExactSystemRemote(t *testing.T) {
	system := &windowsFakeSystem{remoteDrive: true, remoteResults: []remoteResult{{remote: `\\other\share`, mapped: true}}}
	runner := &windowsRecordingRunner{}
	connector := newTestWindowsConnector(runner, system)
	attachment := Attachment{owner: connector.owner, id: 1, kind: KindWindows, location: `T:\`}
	connector.active["T:"] = windowsActive{id: attachment.id, remote: `\\expected\share`}

	if err := connector.Detach(context.Background(), attachment); !errors.Is(err, ErrAttachmentChanged) {
		t.Fatalf("Detach() = %v; want attachment changed", err)
	}
	if len(runner.plans) != 0 {
		t.Fatal("detach command ran for a different mapping")
	}
}

func TestWindowsAttachRollsBackUnverifiedMapping(t *testing.T) {
	remote := `\\127.0.0.1@49152\DavWWWRoot\tdrive-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef`
	system := &windowsFakeSystem{remoteDrive: false, remoteResults: []remoteResult{
		{remote: remote, mapped: true},
		{remote: remote, mapped: true},
		{mapped: false},
	}}
	runner := &windowsRecordingRunner{}
	connector := newTestWindowsConnector(runner, system)

	_, err := connector.Attach(context.Background(), Config{Endpoint: testEndpoint, Label: "Tdrive personal"})
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("Attach() = %v; want verification failure", err)
	}
	if len(runner.plans) != 2 || runner.plans[1].Args[2] != "/delete" {
		t.Fatalf("rollback commands = %#v", runner.plans)
	}
	if strings.Contains(err.Error(), "tdrive-") {
		t.Fatalf("error leaked capability: %q", err)
	}
}

func TestWindowsOpenRevalidatesSystemMapping(t *testing.T) {
	system := &windowsFakeSystem{remoteDrive: true, remoteResults: []remoteResult{{remote: `\\other\share`, mapped: true}}}
	runner := &windowsRecordingRunner{}
	connector := newTestWindowsConnector(runner, system)
	attachment := Attachment{owner: connector.owner, id: 1, kind: KindWindows, location: `T:\`}
	connector.active["T:"] = windowsActive{id: attachment.id, remote: `\\expected\share`}

	if err := connector.Open(context.Background(), attachment); !errors.Is(err, ErrAttachmentChanged) {
		t.Fatalf("Open(changed) = %v; want attachment changed", err)
	}
	if len(runner.plans) != 0 {
		t.Fatal("Explorer opened a drive rebound to another remote")
	}
}

func TestWindowsPostCommandLookupFailureAttemptsSafeRollback(t *testing.T) {
	remote := `\\127.0.0.1@49152\DavWWWRoot\tdrive-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef`
	system := &windowsFakeSystem{remoteDrive: true, remoteResults: []remoteResult{
		{err: errors.New("transient lookup failure")},
		{remote: remote, mapped: true},
		{remote: remote, mapped: true},
		{mapped: false},
	}}
	runner := &windowsRecordingRunner{}
	connector := newTestWindowsConnector(runner, system)

	_, err := connector.Attach(context.Background(), Config{Endpoint: testEndpoint, Label: "Tdrive personal"})
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("Attach() = %v; want verification failure", err)
	}
	if len(runner.plans) != 2 || runner.plans[1].Args[2] != "/delete" {
		t.Fatalf("safe rollback commands = %#v", runner.plans)
	}
}

func TestWindowsSystemDiscoveryUsesTrustedAbsoluteExecutables(t *testing.T) {
	netExecutable, explorerExecutable, err := windowsSystemPaths()
	if err != nil {
		t.Fatalf("windowsSystemPaths() error = %v", err)
	}
	if !filepath.IsAbs(netExecutable) || !strings.EqualFold(filepath.Base(netExecutable), "net.exe") {
		t.Fatalf("untrusted net executable path: %q", netExecutable)
	}
	if !filepath.IsAbs(explorerExecutable) || !strings.EqualFold(filepath.Base(explorerExecutable), "explorer.exe") {
		t.Fatalf("untrusted Explorer path: %q", explorerExecutable)
	}

	windowsDir, err := windows.GetWindowsDirectory()
	if err != nil {
		t.Fatal(err)
	}
	remote, err := windowsIsRemoteDrive(filepath.VolumeName(windowsDir) + `\`)
	if err != nil || remote {
		t.Fatalf("Windows system drive reported as remote: %v, %v", remote, err)
	}
}

func TestWindowsRemoteLookupReportsUnusedDriveAsUnmapped(t *testing.T) {
	drives, err := windows.GetLogicalDrives()
	if err != nil {
		t.Fatal(err)
	}
	for letter := byte('Z'); letter >= 'A'; letter-- {
		if drives&(uint32(1)<<uint(letter-'A')) != 0 {
			continue
		}
		remote, mapped, err := windowsRemoteFor(string([]byte{letter, ':'}))
		if err != nil || mapped || remote != "" {
			t.Fatalf("unused drive lookup = %q, %v, %v", remote, mapped, err)
		}
		return
	}
	t.Skip("all Windows drive letters are occupied")
}
