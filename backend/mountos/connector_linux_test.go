//go:build linux

package mountos

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type linuxFakeExecutor struct {
	mu         sync.Mutex
	plans      []commandPlan
	outputs    [][]byte
	outputErrs []error
	runErr     error
}

func (e *linuxFakeExecutor) Run(ctx context.Context, plan commandPlan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("unbounded context")
	}
	e.plans = append(e.plans, commandPlan{Path: plan.Path, Args: append([]string(nil), plan.Args...)})
	return e.runErr
}

func (e *linuxFakeExecutor) Output(ctx context.Context, plan commandPlan, limit int) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := ctx.Deadline(); !ok || limit != maxGIOListBytes {
		return nil, errors.New("invalid output boundary")
	}
	if len(e.outputs) == 0 {
		return nil, errors.New("unexpected output command")
	}
	e.plans = append(e.plans, commandPlan{Path: plan.Path, Args: append([]string(nil), plan.Args...)})
	if len(e.outputErrs) > 0 {
		err := e.outputErrs[0]
		e.outputErrs = e.outputErrs[1:]
		if err != nil {
			return nil, err
		}
	}
	result := append([]byte(nil), e.outputs[0]...)
	e.outputs = e.outputs[1:]
	return result, nil
}

func TestLinuxAttachRetriesUntilGVfsPublishesMount(t *testing.T) {
	executor := &linuxFakeExecutor{outputs: [][]byte{
		[]byte("no mounts"),
		[]byte("no mounts"),
		[]byte("still publishing"),
		[]byte("Mount(0): TDrive -> " + testLinuxURI),
	}}
	delayCalls := 0
	delay := func(ctx context.Context, interval time.Duration) error {
		delayCalls++
		deadline, ok := ctx.Deadline()
		if !ok {
			return errors.New("verification delay has no deadline")
		}
		if remaining := time.Until(deadline); remaining <= 0 || remaining > time.Second {
			return errors.New("verification delay has an invalid deadline")
		}
		if interval <= 0 {
			return errors.New("verification interval is not positive")
		}
		return nil
	}
	connector := newLinuxConnector(linuxDependencies{
		runner:              executor,
		outputRunner:        executor,
		delay:               delay,
		verificationTimeout: time.Second,
	})

	attachment, err := connector.Attach(context.Background(), Config{
		Endpoint: testEndpoint,
		Label:    "Tdrive personal",
	})
	if err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	if attachment.Kind() != KindLinux {
		t.Fatalf("Attach() kind = %q, want %q", attachment.Kind(), KindLinux)
	}
	if delayCalls != 2 {
		t.Fatalf("verification delay calls = %d, want 2", delayCalls)
	}
}

func TestLinuxAttachClassifiesDesktopInspectionFailure(t *testing.T) {
	inspectionErr := errors.New("GIO cannot reach the desktop mount service at " + testLinuxURI)
	executor := &linuxFakeExecutor{
		outputs:    [][]byte{nil},
		outputErrs: []error{inspectionErr},
	}
	connector := newLinuxConnector(linuxDependencies{runner: executor, outputRunner: executor})

	_, err := connector.Attach(context.Background(), Config{Endpoint: testEndpoint, Label: "Tdrive personal"})
	if !errors.Is(err, ErrLinuxDesktopUnavailable) {
		t.Fatalf("Attach(inspection failure) = %v; want Linux desktop unavailable", err)
	}
	if strings.Contains(err.Error(), testLinuxURI) || strings.Contains(err.Error(), testEndpoint) {
		t.Fatalf("Attach(inspection failure) leaked capability: %q", err)
	}
	if len(executor.plans) != 1 {
		t.Fatalf("inspection failure commands = %#v; want only mount inspection", executor.plans)
	}
}

func TestLinuxAttachClassifiesPostAttachInspectionFailureAndRollsBack(t *testing.T) {
	inspectionErr := errors.New("GVfs desktop session disappeared at " + testLinuxURI)
	executor := &linuxFakeExecutor{
		outputs:    [][]byte{[]byte("no mounts"), nil},
		outputErrs: []error{nil, inspectionErr},
	}
	connector := newLinuxConnector(linuxDependencies{runner: executor, outputRunner: executor})

	_, err := connector.Attach(context.Background(), Config{Endpoint: testEndpoint, Label: "Tdrive personal"})
	if !errors.Is(err, ErrLinuxDesktopUnavailable) {
		t.Fatalf("Attach(post-attach inspection failure) = %v; want Linux desktop unavailable", err)
	}
	if strings.Contains(err.Error(), testLinuxURI) || strings.Contains(err.Error(), testEndpoint) {
		t.Fatalf("Attach(post-attach inspection failure) leaked capability: %q", err)
	}
	if len(executor.plans) != 4 {
		t.Fatalf("post-attach inspection failure plans = %#v; want inspect, attach, verify, rollback", executor.plans)
	}
	if got := strings.Join(executor.plans[3].Args, " "); got != "mount -u "+testLinuxURI {
		t.Fatalf("rollback command = %q", got)
	}
}

func TestLinuxAttachClassifiesWebDAVBackendFailureWithoutRollback(t *testing.T) {
	executor := &linuxFakeExecutor{
		outputs: [][]byte{[]byte("no mounts")},
		runErr:  errors.New("GIO WebDAV backend rejected " + testLinuxURI),
	}
	connector := newLinuxConnector(linuxDependencies{runner: executor, outputRunner: executor})

	_, err := connector.Attach(context.Background(), Config{Endpoint: testEndpoint, Label: "Tdrive personal"})
	if !errors.Is(err, ErrLinuxWebDAVUnavailable) {
		t.Fatalf("Attach(command failure) = %v; want Linux WebDAV unavailable", err)
	}
	if strings.Contains(err.Error(), testLinuxURI) || strings.Contains(err.Error(), testEndpoint) {
		t.Fatalf("Attach(command failure) leaked capability: %q", err)
	}
	if len(executor.plans) != 2 {
		t.Fatalf("command failure plans = %#v; rollback must not unmount an unverified mount", executor.plans)
	}
	if got := strings.Join(executor.plans[1].Args, " "); got != "mount --anonymous "+testLinuxURI {
		t.Fatalf("attach command = %q", got)
	}
}

func TestLinuxAttachPreservesCancellationDuringVerification(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	executor := &linuxFakeExecutor{outputs: [][]byte{
		[]byte("no mounts"),
		[]byte("not published yet"),
	}}
	delay := func(delayCtx context.Context, _ time.Duration) error {
		cancel()
		<-delayCtx.Done()
		return delayCtx.Err()
	}
	connector := newLinuxConnector(linuxDependencies{
		runner:              executor,
		outputRunner:        executor,
		delay:               delay,
		verificationTimeout: time.Second,
	})

	_, err := connector.Attach(ctx, Config{Endpoint: testEndpoint, Label: "Tdrive personal"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Attach(canceled) = %v; want context canceled", err)
	}
	if strings.Contains(err.Error(), testLinuxURI) || strings.Contains(err.Error(), testEndpoint) {
		t.Fatalf("Attach(canceled) leaked capability: %q", err)
	}
	if len(executor.plans) != 4 {
		t.Fatalf("canceled attach plans = %#v; want inspect, attach, verify, owned rollback", executor.plans)
	}
	if got := strings.Join(executor.plans[3].Args, " "); got != "mount -u "+testLinuxURI {
		t.Fatalf("rollback command = %q", got)
	}
}

func TestLinuxAttachBoundsMountPublicationVerification(t *testing.T) {
	executor := &linuxFakeExecutor{outputs: [][]byte{
		[]byte("no mounts"),
		[]byte("not published yet"),
	}}
	const verificationTimeout = 20 * time.Millisecond
	delay := func(ctx context.Context, _ time.Duration) error {
		<-ctx.Done()
		return ctx.Err()
	}
	connector := newLinuxConnector(linuxDependencies{
		runner:              executor,
		outputRunner:        executor,
		delay:               delay,
		verificationTimeout: verificationTimeout,
	})

	started := time.Now()
	_, err := connector.Attach(context.Background(), Config{Endpoint: testEndpoint, Label: "Tdrive personal"})
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("Attach(publication timeout) = %v; want verification failed", err)
	}
	if elapsed := time.Since(started); elapsed > 10*verificationTimeout {
		t.Fatalf("Attach(publication timeout) took %v; want a small bounded wait", elapsed)
	}
	if len(executor.plans) != 4 {
		t.Fatalf("publication timeout plans = %#v; want inspect, attach, verify, rollback", executor.plans)
	}
}

func TestLinuxAttachOpenDetachUsesOneInjectedExecutor(t *testing.T) {
	davURI := testLinuxURI
	executor := &linuxFakeExecutor{outputs: [][]byte{
		[]byte("no mounts"),
		[]byte("Mount(0): TDrive -> " + davURI),
		[]byte("Mount(0): TDrive -> " + davURI),
		[]byte("Mount(0): TDrive -> " + davURI),
		[]byte("no mounts"),
	}}
	connector := newLinuxConnector(linuxDependencies{runner: executor, outputRunner: executor})

	attachment, err := connector.Attach(context.Background(), Config{Endpoint: testEndpoint, Label: "Tdrive personal"})
	if err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	if attachment.Kind() != KindLinux || attachment.Location() != "Tdrive personal" {
		t.Fatalf("attachment = %q, %q", attachment.Kind(), attachment.Location())
	}
	if err := connector.Open(context.Background(), attachment); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := connector.Detach(context.Background(), attachment); err != nil {
		t.Fatalf("Detach() error = %v", err)
	}

	var commands []commandPlan
	for _, plan := range executor.plans {
		if len(plan.Args) == 0 || plan.Args[len(plan.Args)-1] != "-l" {
			commands = append(commands, plan)
		}
	}
	if len(commands) != 3 {
		t.Fatalf("lifecycle commands = %#v", commands)
	}
}

func TestLinuxOpenRejectsMissingExactURI(t *testing.T) {
	executor := &linuxFakeExecutor{outputs: [][]byte{[]byte("Mount(0): unrelated")}}
	connector := newLinuxConnector(linuxDependencies{runner: executor, outputRunner: executor})
	attachment := Attachment{owner: connector.owner, id: 1, kind: KindLinux, location: "Tdrive personal"}
	connector.active[attachment.id] = testLinuxURI
	connector.byEndpoint[testLinuxURI] = attachment.id

	if err := connector.Open(context.Background(), attachment); !errors.Is(err, ErrAttachmentChanged) {
		t.Fatalf("Open(missing) = %v; want attachment changed", err)
	}
}

func TestLinuxAttachDoesNotUnmountPreexistingURI(t *testing.T) {
	executor := &linuxFakeExecutor{outputs: [][]byte{[]byte(testLinuxURI)}}
	connector := newLinuxConnector(linuxDependencies{runner: executor, outputRunner: executor})

	_, err := connector.Attach(context.Background(), Config{Endpoint: testEndpoint, Label: "Tdrive personal"})
	if !errors.Is(err, ErrAttachmentChanged) {
		t.Fatalf("Attach(preexisting) = %v; want attachment changed", err)
	}
	if len(executor.plans) != 1 || strings.Join(executor.plans[0].Args, " ") != "mount -l" {
		t.Fatalf("unexpected commands for preexisting mount: %#v", executor.plans)
	}
}

func TestLinuxMountInspectionRequiresExactURIBoundary(t *testing.T) {
	if !mountOutputContainsURI([]byte("Mount -> "+testLinuxURI+"\n"), testLinuxURI) {
		t.Fatal("exact GIO mount URI was not detected")
	}
	withoutSlash := strings.TrimSuffix(testLinuxURI, "/")
	if !mountOutputContainsURI([]byte("Mount -> "+withoutSlash+"\n"), testLinuxURI) {
		t.Fatal("normalized GIO mount URI was not detected")
	}
	if mountOutputContainsURI([]byte("Mount -> "+withoutSlash+"-different\n"), testLinuxURI) {
		t.Fatal("URI prefix for a different mount was accepted")
	}
	if mountOutputContainsURI([]byte("Mount -> x"+testLinuxURI+"\n"), testLinuxURI) {
		t.Fatal("URI embedded in a longer token was accepted")
	}
}
