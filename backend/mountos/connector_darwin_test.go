//go:build darwin

package mountos

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingRunner struct {
	mu    sync.Mutex
	plans []commandPlan
	errAt map[int]error
}

func (r *recordingRunner) Run(ctx context.Context, plan commandPlan) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("command context is not bounded")
	}
	index := len(r.plans)
	r.plans = append(r.plans, commandPlan{Path: plan.Path, Args: append([]string(nil), plan.Args...)})
	return r.errAt[index]
}

func (r *recordingRunner) snapshot() []commandPlan {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]commandPlan(nil), r.plans...)
}

type probeResult struct {
	mounted  bool
	readOnly bool
	source   string
	err      error
}

type sequenceProbe struct {
	mu      sync.Mutex
	results []probeResult
	targets []string
}

func (p *sequenceProbe) inspect(target string) (darwinMountInspection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.targets = append(p.targets, target)
	if len(p.results) == 0 {
		return darwinMountInspection{}, errors.New("unexpected probe")
	}
	result := p.results[0]
	p.results = p.results[1:]
	source := result.source
	if source == "" && result.mounted {
		source = testEndpoint
	}
	return darwinMountInspection{mounted: result.mounted, readOnly: result.readOnly, source: source}, result.err
}

func newTestDarwinConnector(cacheDir string, runner commandRunner, probe *sequenceProbe) *darwinConnector {
	return newDarwinConnector(darwinDependencies{
		runner:       runner,
		userCacheDir: func() (string, error) { return cacheDir, nil },
		inspectMount: probe.inspect,
	})
}

func TestDarwinAttachConfirmOpenAndDetach(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{}
	probe := &sequenceProbe{results: []probeResult{
		{mounted: true, readOnly: true},
		{mounted: true, readOnly: true},
		{mounted: true, readOnly: true},
		{mounted: false},
	}}
	connector := newTestDarwinConnector(t.TempDir(), runner, probe)

	attachment, err := connector.Attach(context.Background(), Config{
		Endpoint: testEndpoint,
		Label:    "Tdrive personal",
	})
	if err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	if attachment.Kind() != KindDarwin || !strings.Contains(attachment.Location(), filepath.Join("TDrive", "mounts")) {
		t.Fatalf("Attach() = kind %q, location %q", attachment.Kind(), attachment.Location())
	}
	info, err := os.Lstat(attachment.Location())
	if err != nil {
		t.Fatalf("Lstat(mountpoint): %v", err)
	}
	if info.Mode().Perm() != 0o700 || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("mountpoint mode = %v; want private directory", info.Mode())
	}
	entries, err := os.ReadDir(attachment.Location())
	if err != nil || len(entries) != 0 {
		t.Fatalf("mountpoint entries = %v, %v; want empty", entries, err)
	}

	if err := connector.Open(context.Background(), attachment); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := connector.Detach(context.Background(), attachment); err != nil {
		t.Fatalf("Detach() error = %v", err)
	}
	if _, err := os.Lstat(attachment.Location()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mountpoint still exists after detach: %v", err)
	}

	plans := runner.snapshot()
	if len(plans) != 3 {
		t.Fatalf("commands = %#v; want attach, open, detach", plans)
	}
	if got := strings.Join(plans[0].Args, "\x00"); !strings.Contains(got, "-v\x00Tdrive personal") || !strings.Contains(got, "rdonly,noexec,nosuid,nodev") {
		t.Fatalf("attach arguments = %#v", plans[0].Args)
	}
}

func TestDarwinAttachOpenAndDetachAcceptVerifiedLegacySourceTruncation(t *testing.T) {
	t.Parallel()

	if len(testEndpoint) <= darwinLegacyMountSourceMaxBytes {
		t.Fatalf(
			"test endpoint length = %d, want more than %d",
			len(testEndpoint),
			darwinLegacyMountSourceMaxBytes,
		)
	}
	runner := &recordingRunner{}
	truncated := testEndpoint[:darwinLegacyMountSourceMaxBytes]
	probe := &sequenceProbe{results: []probeResult{
		{
			mounted:  true,
			readOnly: true,
			source:   truncated,
		},
		{
			mounted:  true,
			readOnly: true,
			source:   truncated,
		},
		{
			mounted:  true,
			readOnly: true,
			source:   truncated,
		},
		{mounted: false},
	}}
	connector := newTestDarwinConnector(t.TempDir(), runner, probe)

	attachment, err := connector.Attach(context.Background(), Config{
		Endpoint: testEndpoint,
		Label:    "Tdrive personal",
	})
	if err != nil {
		t.Fatalf("Attach() with truncated macOS source error = %v", err)
	}
	if err := connector.Open(context.Background(), attachment); err != nil {
		t.Fatalf("Open() after truncated-source attach error = %v", err)
	}
	if err := connector.Detach(context.Background(), attachment); err != nil {
		t.Fatalf("Detach() after truncated-source attach error = %v", err)
	}
}

func TestDarwinSourceMatchRejectsUnverifiedPrefixes(t *testing.T) {
	t.Parallel()

	truncated := testEndpoint[:darwinLegacyMountSourceMaxBytes]
	changed := truncated[:darwinLegacyMountSourceMaxBytes-1] + "0"
	if changed == truncated {
		changed = truncated[:darwinLegacyMountSourceMaxBytes-1] + "1"
	}

	tests := []struct {
		name     string
		source   string
		endpoint string
		want     bool
	}{
		{name: "exact", source: testEndpoint, endpoint: testEndpoint, want: true},
		{name: "trailing slash", source: strings.TrimSuffix(testEndpoint, "/"), endpoint: testEndpoint, want: true},
		{name: "legacy truncation", source: truncated, endpoint: testEndpoint, want: true},
		{name: "short prefix", source: testEndpoint[:darwinLegacyMountSourceMaxBytes-1], endpoint: testEndpoint},
		{name: "changed legacy prefix", source: changed, endpoint: testEndpoint},
		{name: "foreign source", source: "http://127.0.0.1:49152/foreign", endpoint: testEndpoint},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := darwinSourceMatchesEndpoint(test.source, test.endpoint); got != test.want {
				t.Fatalf("darwinSourceMatchesEndpoint() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestDarwinAttachRollsBackFailedVerificationAndSanitizesError(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{}
	truncated := testEndpoint[:darwinLegacyMountSourceMaxBytes]
	probe := &sequenceProbe{results: []probeResult{
		{mounted: true, readOnly: false, source: truncated},
		{mounted: true, readOnly: false, source: truncated},
	}}
	connector := newTestDarwinConnector(t.TempDir(), runner, probe)

	attachment, err := connector.Attach(context.Background(), Config{Endpoint: testEndpoint, Label: "Tdrive personal"})
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("Attach() error = %v; want verification error", err)
	}
	if attachment != (Attachment{}) {
		t.Fatalf("Attach() returned partial attachment: %#v", attachment)
	}
	if strings.Contains(err.Error(), "tdrive-") || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("error leaked endpoint capability: %q", err)
	}
	plans := runner.snapshot()
	if len(plans) != 2 || plans[1].Path != "/usr/sbin/diskutil" {
		t.Fatalf("rollback commands = %#v", plans)
	}
}

func TestDarwinAttachRejectsUnsafeMountBase(t *testing.T) {
	t.Parallel()

	cache := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(cache, "TDrive")); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	connector := newTestDarwinConnector(cache, runner, &sequenceProbe{})

	_, err := connector.Attach(context.Background(), Config{Endpoint: testEndpoint, Label: "Tdrive personal"})
	if !errors.Is(err, ErrAttachFailed) {
		t.Fatalf("Attach() error = %v; want attach failure", err)
	}
	if len(runner.snapshot()) != 0 {
		t.Fatal("mount command ran with a symlinked app mount base")
	}
}

func TestDarwinRollbackNeverUnmountsDifferentSource(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{}
	probe := &sequenceProbe{results: []probeResult{
		{mounted: true, readOnly: true, source: "http://127.0.0.1:49152/tdrive-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/"},
		{mounted: true, readOnly: true, source: "http://127.0.0.1:49152/tdrive-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/"},
	}}
	connector := newTestDarwinConnector(t.TempDir(), runner, probe)

	_, err := connector.Attach(context.Background(), Config{Endpoint: testEndpoint, Label: "Tdrive personal"})
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("Attach() = %v; want verification failure", err)
	}
	plans := runner.snapshot()
	if len(plans) != 1 || plans[0].Path != "/sbin/mount_webdav" {
		t.Fatalf("rollback unmounted a different source: %#v", plans)
	}
}

func TestDarwinDetachRejectsForeignOrChangedAttachment(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{}
	connector := newTestDarwinConnector(t.TempDir(), runner, &sequenceProbe{results: []probeResult{{mounted: true, readOnly: false}}})

	if err := connector.Detach(context.Background(), NewAttachment(KindDarwin, "/tmp/foreign")); !errors.Is(err, ErrInvalidAttachment) {
		t.Fatalf("Detach(foreign) = %v; want invalid attachment", err)
	}
	owned := Attachment{owner: connector.owner, id: 1, kind: KindDarwin, location: "/tmp/owned"}
	connector.active[owned.location] = darwinActive{id: owned.id, endpoint: testEndpoint}
	if err := connector.Detach(context.Background(), owned); !errors.Is(err, ErrAttachmentChanged) {
		t.Fatalf("Detach(changed) = %v; want changed attachment", err)
	}
	if len(runner.snapshot()) != 0 {
		t.Fatal("detach command ran without ownership verification")
	}
}

func TestDarwinOperationsRejectNilContext(t *testing.T) {
	t.Parallel()

	connector := newTestDarwinConnector(t.TempDir(), &recordingRunner{}, &sequenceProbe{})
	if _, err := connector.Attach(nil, Config{Endpoint: testEndpoint, Label: "Tdrive personal"}); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("Attach(nil) = %v", err)
	}
	if err := connector.Open(nil, Attachment{}); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("Open(nil) = %v", err)
	}
	if err := connector.Detach(nil, Attachment{}); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("Detach(nil) = %v", err)
	}
}

func TestDarwinOpenRevalidatesAttachment(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{}
	probe := &sequenceProbe{results: []probeResult{{mounted: false}}}
	connector := newTestDarwinConnector(t.TempDir(), runner, probe)
	attachment := Attachment{owner: connector.owner, id: 1, kind: KindDarwin, location: "/tmp/owned"}
	connector.active[attachment.location] = darwinActive{id: attachment.id, endpoint: testEndpoint}

	if err := connector.Open(context.Background(), attachment); !errors.Is(err, ErrAttachmentChanged) {
		t.Fatalf("Open(detached) = %v; want attachment changed", err)
	}
	if len(runner.snapshot()) != 0 {
		t.Fatal("open command ran after attachment disappeared")
	}
}

func TestDarwinCommandFailureDoesNotLeakCapability(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{errAt: map[int]error{0: errors.New("failed " + testEndpoint)}}
	connector := newTestDarwinConnector(t.TempDir(), runner, &sequenceProbe{})
	_, err := connector.Attach(context.Background(), Config{Endpoint: testEndpoint, Label: "Tdrive personal"})
	if !errors.Is(err, ErrAttachFailed) {
		t.Fatalf("Attach() = %v; want attach failure", err)
	}
	if strings.Contains(err.Error(), "tdrive-") || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("error leaked endpoint capability: %q", err)
	}
}

func TestBoundedContextHonorsEarlierParentDeadline(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ctx, boundedCancel, err := boundedContext(parent, attachTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer boundedCancel()
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 2*time.Second {
		t.Fatalf("bounded context ignored parent deadline: %v, %v", deadline, ok)
	}
}

func TestDarwinMountInspectionRejectsOrdinaryDirectory(t *testing.T) {
	t.Parallel()

	inspection, err := inspectDarwinMount(t.TempDir())
	if err != nil || inspection.mounted || inspection.readOnly || inspection.source != "" {
		t.Fatalf("inspectDarwinMount(temp dir) = %#v, %v", inspection, err)
	}
	if got := nulTerminatedString([]byte{'a', 'b', 0, 'c'}); got != "ab" {
		t.Fatalf("nulTerminatedString() = %q", got)
	}
	if got := nulTerminatedString([]byte{'a', 'b'}); got != "ab" {
		t.Fatalf("nulTerminatedString(no NUL) = %q", got)
	}
}

func TestDarwinMountpointCreationRejectsInvalidCacheAndNonEmptyCleanup(t *testing.T) {
	t.Parallel()

	connector := newDarwinConnector(darwinDependencies{
		userCacheDir: func() (string, error) { return "relative", nil },
	})
	if _, err := connector.createMountpoint(); err == nil {
		t.Fatal("createMountpoint() accepted a relative cache path")
	}

	directory := t.TempDir()
	file, err := os.Create(filepath.Join(directory, "keep"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := removeEmptyDirectory(directory); !errors.Is(err, ErrDetachFailed) {
		t.Fatalf("removeEmptyDirectory(non-empty) = %v", err)
	}
}
