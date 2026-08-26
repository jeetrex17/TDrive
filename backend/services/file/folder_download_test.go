package file

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

type capturedEvent struct {
	name string
	args []any
}

type argumentEventRecorder struct {
	mu     sync.Mutex
	events []capturedEvent
}

func (r *argumentEventRecorder) Emit(name string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, capturedEvent{name: name, args: append([]any(nil), args...)})
}

func (r *argumentEventRecorder) last(name string) (capturedEvent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.events) - 1; i >= 0; i-- {
		if r.events[i].name == name {
			return r.events[i], true
		}
	}
	return capturedEvent{}, false
}

func (r *argumentEventRecorder) named(name string) []capturedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	events := make([]capturedEvent, 0, len(r.events))
	for _, event := range r.events {
		if event.name == name {
			events = append(events, event)
		}
	}
	return events
}

func TestDownloadFolderRestoresMixedNestedTreeAndEmptyFolders(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	svc.MaxUploadBytes = 1000
	masterKey := bytes.Repeat([]byte{0x4a}, 32)
	configureEncryptedUpload(t, svc, masterKey)

	project(t, db, personalChannelID, 10, 7, projection.Op{
		Type: projection.OpMkdir, Obj: "d:project", Parent: projection.RootParent, Name: "Project",
	})
	project(t, db, personalChannelID, 11, 7, projection.Op{
		Type: projection.OpMkdir, Obj: "d:data", Parent: "d:project", Name: "Data",
	})
	project(t, db, personalChannelID, 12, 7, projection.Op{
		Type: projection.OpMkdir, Obj: "d:empty", Parent: "d:project", Name: "Empty",
	})

	fixtures := []struct {
		name      string
		body      []byte
		parentID  string
		encrypted bool
	}{
		{name: "readme.txt", body: []byte("hello"), parentID: "d:project"},
		{name: "plain-large.bin", body: bigBody(3503), parentID: "d:data"},
		{name: "secret.txt", body: []byte("classified"), parentID: "d:project", encrypted: true},
		{name: "secret-large.bin", body: bigBody(2500), parentID: "d:data", encrypted: true},
	}

	var totalStored int64
	for _, fixture := range fixtures {
		path := writeTempNamedFile(t, fixture.name, fixture.body)
		files, err := svc.Upload(
			context.Background(), personalChannelID,
			[]string{path}, []string{fixture.parentID}, fixture.encrypted,
		)
		if err != nil {
			t.Fatalf("upload %s: %v", fixture.name, err)
		}
		if len(files) != 1 {
			t.Fatalf("upload %s returned %d files, want 1", fixture.name, len(files))
		}
		totalStored += files[0].Size
	}

	keyCalls := 0
	requireKey := svc.RequireEncryptionKey
	svc.RequireEncryptionKey = func(encrypted bool) ([]byte, error) {
		keyCalls++
		return requireKey(encrypted)
	}
	recorder := &argumentEventRecorder{}
	svc.Events = recorder

	destinationParent := t.TempDir()
	result := svc.DownloadFolder(context.Background(), personalChannelID, "d:project", func(defaultName string) (string, error) {
		if defaultName != "Project" {
			t.Fatalf("default folder name = %q, want Project", defaultName)
		}
		return destinationParent, nil
	})
	if result.Status != "success" {
		t.Fatalf("DownloadFolder = %+v", result)
	}
	if keyCalls != 1 {
		t.Fatalf("encryption key calls = %d, want one folder-wide acquisition", keyCalls)
	}

	root := filepath.Join(destinationParent, "Project")
	if result.SavedPath != root {
		t.Fatalf("saved path = %q, want %q", result.SavedPath, root)
	}
	if info, err := os.Stat(filepath.Join(root, "Empty")); err != nil || !info.IsDir() {
		t.Fatalf("empty directory was not restored: info=%v err=%v", info, err)
	}
	for _, fixture := range fixtures {
		wantPath := filepath.Join(root, fixture.name)
		if fixture.parentID == "d:data" {
			wantPath = filepath.Join(root, "Data", fixture.name)
		}
		got, err := os.ReadFile(wantPath)
		if err != nil {
			t.Fatalf("read %s: %v", wantPath, err)
		}
		if !bytes.Equal(got, fixture.body) {
			t.Fatalf("%s bytes differ: got %d, want %d", fixture.name, len(got), len(fixture.body))
		}
	}

	terminal, ok := recorder.last("folder_download_progress")
	if !ok || len(terminal.args) != 1 {
		t.Fatalf("terminal folder progress = %+v, want one payload", terminal)
	}
	payload, ok := terminal.args[0].(map[string]any)
	if !ok {
		t.Fatalf("terminal progress payload type = %T, want map", terminal.args[0])
	}
	if payload["files_completed"] != len(fixtures) || payload["files_total"] != len(fixtures) {
		t.Fatalf("terminal file counts = %+v", payload)
	}
	if payload["bytes_completed"] != totalStored || payload["bytes_total"] != totalStored || payload["percent"] != 100.0 {
		t.Fatalf("terminal byte progress = %+v", payload)
	}
}

func TestDownloadFolderRestoresCompletelyEmptyTree(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	project(t, db, personalChannelID, 10, 7, projection.Op{
		Type: projection.OpMkdir, Obj: "d:project", Parent: projection.RootParent, Name: "Project",
	})
	project(t, db, personalChannelID, 11, 7, projection.Op{
		Type: projection.OpMkdir, Obj: "d:nested", Parent: "d:project", Name: "Nested",
	})
	recorder := &argumentEventRecorder{}
	svc.Events = recorder
	// An empty tree is entirely local and must not require Telegram readiness.
	svc.TG = nil
	svc.Peers = nil

	parent := t.TempDir()
	result := svc.DownloadFolder(context.Background(), personalChannelID, "d:project", func(string) (string, error) {
		return parent, nil
	})
	if result.Status != "success" {
		t.Fatalf("DownloadFolder = %+v", result)
	}
	if info, err := os.Stat(filepath.Join(parent, "Project", "Nested")); err != nil || !info.IsDir() {
		t.Fatalf("empty nested directory was not restored: info=%v err=%v", info, err)
	}
	terminal, ok := recorder.last("folder_download_progress")
	if !ok || len(terminal.args) != 1 {
		t.Fatalf("terminal folder progress = %+v", terminal)
	}
	payload, ok := terminal.args[0].(map[string]any)
	if !ok || payload["percent"] != 100.0 || payload["files_total"] != 0 || payload["bytes_total"] != int64(0) {
		t.Fatalf("empty-tree terminal progress = %+v", terminal.args)
	}
}

func TestFolderDownloadProgressIsMonotonicAcrossRetryLikeUpdates(t *testing.T) {
	recorder := &argumentEventRecorder{}
	progress := newFolderDownloadProgress(
		&Service{Events: recorder}, "d:project", "Project",
		[]folderDownloadFile{
			{source: projection.DownloadFile{StoredSize: 100}, relativePath: "a.bin"},
			{source: projection.DownloadFile{StoredSize: 50}, relativePath: "b.bin"},
		},
		150, 150,
	)

	progress.emitInitial()
	progress.report(0, "a.bin", 60)
	progress.report(0, "a.bin", 10)  // a retry restarted at zero
	progress.report(0, "a.bin", 120) // an overshoot is clamped
	progress.complete(0, "a.bin")
	progress.report(1, "b.bin", 25)
	progress.report(1, "b.bin", 5)
	progress.complete(1, "b.bin")
	progress.emitTerminal()

	var previousBytes int64
	var previousPercent float64
	terminalEvents := 0
	for _, event := range recorder.named("folder_download_progress") {
		payload, ok := event.args[0].(map[string]any)
		if !ok {
			t.Fatalf("progress payload type = %T", event.args[0])
		}
		bytesCompleted := payload["bytes_completed"].(int64)
		percent := payload["percent"].(float64)
		if bytesCompleted < previousBytes || bytesCompleted > 150 {
			t.Fatalf("bytes regressed or overflowed: previous=%d current=%d", previousBytes, bytesCompleted)
		}
		if percent < previousPercent || percent > 100 {
			t.Fatalf("percent regressed or overflowed: previous=%f current=%f", previousPercent, percent)
		}
		if percent == 100 {
			terminalEvents++
		}
		previousBytes = bytesCompleted
		previousPercent = percent
	}
	if previousBytes != 150 || previousPercent != 100 || terminalEvents != 1 {
		t.Fatalf("terminal progress = bytes %d percent %.1f events %d", previousBytes, previousPercent, terminalEvents)
	}
}

func TestFolderDownloadDeadlineIsReportedAsCanceled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	result := folderDownloadFailure(ctx, context.DeadlineExceeded)
	if result.Status != "canceled" {
		t.Fatalf("deadline result = %+v, want canceled", result)
	}
}

func TestBuildFolderDownloadPlanRejectsCanonicalPathCollisions(t *testing.T) {
	manifest := projection.FolderDownloadManifest{
		Root: projection.DownloadDirectory{ID: "d:root", Name: "Root", ParentID: projection.RootParent, Revision: 1},
		Folders: []projection.DownloadDirectory{
			{ID: "d:root", Name: "Root", ParentID: projection.RootParent, Revision: 1, Depth: 0},
			{ID: "d:docs-a", Name: "Docs", ParentID: "d:root", Revision: 1, Depth: 1},
			{ID: "d:docs-b", Name: "docs", ParentID: "d:root", Revision: 1, Depth: 1},
		},
	}

	_, err := buildFolderDownloadPlan(manifest)
	if err == nil || !strings.Contains(err.Error(), "duplicate path") {
		t.Fatalf("buildFolderDownloadPlan error = %v, want duplicate path", err)
	}
}

type recordingDownloadClient struct {
	*tgclient.Fake
	mu        sync.Mutex
	requested []int64
}

func (c *recordingDownloadClient) DownloadFileAt(
	ctx context.Context,
	peer tgclient.InputPeer,
	msgID int64,
	dst io.WriterAt,
	baseOffset int64,
	progress func(int64, int64),
) error {
	c.mu.Lock()
	c.requested = append(c.requested, msgID)
	c.mu.Unlock()
	return c.Fake.DownloadFileAt(ctx, peer, msgID, dst, baseOffset, progress)
}

func (c *recordingDownloadClient) requestedIDs() []int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int64(nil), c.requested...)
}

func TestDownloadFolderUsesCurrentImmutableContentMessage(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	project(t, db, personalChannelID, 10, 7, projection.Op{
		Type: projection.OpMkdir, Obj: "d:project", Parent: projection.RootParent, Name: "Project",
	})
	peer := tgclient.InputPeer{ChannelID: personalChannelID, AccessHash: 99}
	sendBody := func(name, body string) int64 {
		t.Helper()
		result, err := fakeTG.SendFile(context.Background(), peer, strings.NewReader(body), name, "", int64(len(body)), nil)
		if err != nil {
			t.Fatalf("seed body %s: %v", name, err)
		}
		return result.MsgID
	}
	oldBodyID := sendBody("report.txt", "old")
	currentBodyID := sendBody("report.txt", "new body")
	project(t, db, personalChannelID, 500, 7, projection.Op{
		Type: projection.OpFileCommit, ProtocolVersion: 1, OpID: "create-report",
		Parent: "d:project", Name: "report.txt", ContentMsgID: oldBodyID,
		ContentHash: "old", FileSize: 3,
	})
	project(t, db, personalChannelID, 501, 7, projection.Op{
		Type: projection.OpFileReplace, ProtocolVersion: 1, OpID: "replace-report",
		Obj: "f:500", ExpectedRevision: 1, ContentMsgID: currentBodyID,
		ContentHash: "current", FileSize: 8, RetainedUntil: 1000,
	})

	recording := &recordingDownloadClient{Fake: fakeTG}
	svc.TG = recording
	destinationParent := t.TempDir()
	result := svc.DownloadFolder(context.Background(), personalChannelID, "d:project", func(string) (string, error) {
		return destinationParent, nil
	})
	if result.Status != "success" {
		t.Fatalf("DownloadFolder = %+v", result)
	}
	got, err := os.ReadFile(filepath.Join(destinationParent, "Project", "report.txt"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if string(got) != "new body" {
		t.Fatalf("report = %q, want current body", got)
	}
	if requested := recording.requestedIDs(); len(requested) != 1 || requested[0] != currentBodyID {
		t.Fatalf("requested message IDs = %v, want only %d (old was %d)", requested, currentBodyID, oldBodyID)
	}
}

func TestDownloadFileUsesCurrentImmutableContentMessage(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	peer := tgclient.InputPeer{ChannelID: personalChannelID, AccessHash: 99}
	oldResult, err := fakeTG.SendFile(context.Background(), peer, strings.NewReader("old"), "report.txt", "", 3, nil)
	if err != nil {
		t.Fatalf("seed old body: %v", err)
	}
	currentResult, err := fakeTG.SendFile(context.Background(), peer, strings.NewReader("current"), "report.txt", "", 7, nil)
	if err != nil {
		t.Fatalf("seed current body: %v", err)
	}
	project(t, db, personalChannelID, 500, 7, projection.Op{
		Type: projection.OpFileCommit, ProtocolVersion: 1, OpID: "create-single-report",
		Name: "report.txt", ContentMsgID: oldResult.MsgID, ContentHash: "old", FileSize: 3,
	})
	project(t, db, personalChannelID, 501, 7, projection.Op{
		Type: projection.OpFileReplace, ProtocolVersion: 1, OpID: "replace-single-report",
		Obj: "f:500", ExpectedRevision: 1, ContentMsgID: currentResult.MsgID,
		ContentHash: "current", FileSize: 7, RetainedUntil: 1000,
	})

	recording := &recordingDownloadClient{Fake: fakeTG}
	svc.TG = recording
	savePath := filepath.Join(t.TempDir(), "report.txt")
	result := svc.Download(context.Background(), personalChannelID, 500, 500, func(string) (string, error) {
		return savePath, nil
	})
	if result.Status != "success" {
		t.Fatalf("Download = %+v", result)
	}
	got, err := os.ReadFile(savePath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if string(got) != "current" {
		t.Fatalf("report = %q, want current revision", got)
	}
	if requested := recording.requestedIDs(); len(requested) != 1 || requested[0] != currentResult.MsgID {
		t.Fatalf("requested message IDs = %v, want only %d", requested, currentResult.MsgID)
	}
}

func TestDownloadFolderChecksEncryptionBeforePicker(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	project(t, db, personalChannelID, 10, 7, projection.Op{
		Type: projection.OpMkdir, Obj: "d:locked", Parent: projection.RootParent, Name: "Locked",
	})
	project(t, db, personalChannelID, 20, 7, projection.Op{
		Type: projection.OpFileUpload, Parent: "d:locked", Name: "secret.bin",
		FileSize: 4, Encrypted: true, PlaintextSize: 1, EncryptionVersion: 1,
	})
	ownedKey := bytes.Repeat([]byte{0x6b}, 32)
	svc.RequireEncryptionKey = func(encrypted bool) ([]byte, error) {
		if !encrypted {
			t.Fatal("encrypted manifest requested a plaintext key path")
		}
		return ownedKey, errNeedPassword
	}
	pickerCalled := false
	result := svc.DownloadFolder(context.Background(), personalChannelID, "d:locked", func(string) (string, error) {
		pickerCalled = true
		return t.TempDir(), nil
	})
	if result.Status != "error" || !strings.Contains(result.Message, errNeedPassword.Error()) {
		t.Fatalf("DownloadFolder = %+v, want password error", result)
	}
	if pickerCalled {
		t.Fatal("destination picker opened before encryption readiness succeeded")
	}
	assertKeyZeroed(t, ownedKey)
}

func TestDownloadFolderPickerCancelAndExistingDestinationDoNotMutateDisk(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	project(t, db, personalChannelID, 10, 7, projection.Op{
		Type: projection.OpMkdir, Obj: "d:project", Parent: projection.RootParent, Name: "Project",
	})

	result := svc.DownloadFolder(context.Background(), personalChannelID, "d:project", func(string) (string, error) {
		return "", nil
	})
	if result.Status != "canceled" {
		t.Fatalf("picker cancel = %+v", result)
	}

	parent := t.TempDir()
	existing := filepath.Join(parent, "Project")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatalf("create existing destination: %v", err)
	}
	marker := filepath.Join(existing, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	result = svc.DownloadFolder(context.Background(), personalChannelID, "d:project", func(string) (string, error) {
		return parent, nil
	})
	if result.Status != "error" || !strings.Contains(strings.ToLower(result.Message), "already exists") {
		t.Fatalf("existing destination result = %+v", result)
	}
	got, err := os.ReadFile(marker)
	if err != nil || string(got) != "keep" {
		t.Fatalf("existing destination was modified: body=%q err=%v", got, err)
	}
	assertNoFolderDownloadStaging(t, parent)
}

type blockingDownloadClient struct {
	*tgclient.Fake
	started chan struct{}
	once    sync.Once
}

func (c *blockingDownloadClient) DownloadFileAt(
	ctx context.Context,
	_ tgclient.InputPeer,
	_ int64,
	_ io.WriterAt,
	_ int64,
	_ func(int64, int64),
) error {
	c.once.Do(func() { close(c.started) })
	<-ctx.Done()
	return ctx.Err()
}

func TestDownloadFolderCancellationKeepsFinalPathHiddenAndCleansStaging(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	project(t, db, personalChannelID, 10, 7, projection.Op{
		Type: projection.OpMkdir, Obj: "d:project", Parent: projection.RootParent, Name: "Project",
	})
	path := writeTempNamedFile(t, "blocked.bin", []byte("payload"))
	if _, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{"d:project"}, false); err != nil {
		t.Fatalf("upload: %v", err)
	}
	blocking := &blockingDownloadClient{Fake: fakeTG, started: make(chan struct{})}
	svc.TG = blocking

	parent := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan DownloadResult, 1)
	go func() {
		resultCh <- svc.DownloadFolder(ctx, personalChannelID, "d:project", func(string) (string, error) {
			return parent, nil
		})
	}()
	<-blocking.started
	if _, err := os.Stat(filepath.Join(parent, "Project")); !os.IsNotExist(err) {
		cancel()
		t.Fatalf("final folder became visible before completion: %v", err)
	}
	cancel()
	result := <-resultCh
	if result.Status != "canceled" {
		t.Fatalf("canceled folder download = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(parent, "Project")); !os.IsNotExist(err) {
		t.Fatalf("final folder exists after cancellation: %v", err)
	}
	assertNoFolderDownloadStaging(t, parent)
}

type failSecondDownloadClient struct {
	*tgclient.Fake
	mu    sync.Mutex
	calls int
}

func (c *failSecondDownloadClient) DownloadFileAt(
	ctx context.Context,
	peer tgclient.InputPeer,
	msgID int64,
	dst io.WriterAt,
	baseOffset int64,
	progress func(int64, int64),
) error {
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.mu.Unlock()
	if call == 2 {
		return errors.New("injected folder download failure")
	}
	return c.Fake.DownloadFileAt(ctx, peer, msgID, dst, baseOffset, progress)
}

func TestDownloadFolderFailureRemovesCompletedFilesAndStaging(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.FloodWaitRetry = tgclient.FloodWaitRetryPolicy{MaxTransientRetries: 0}
	project(t, db, personalChannelID, 10, 7, projection.Op{
		Type: projection.OpMkdir, Obj: "d:project", Parent: projection.RootParent, Name: "Project",
	})
	for _, name := range []string{"a.txt", "b.txt"} {
		path := writeTempNamedFile(t, name, []byte(name))
		if _, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{"d:project"}, false); err != nil {
			t.Fatalf("upload %s: %v", name, err)
		}
	}
	svc.TG = &failSecondDownloadClient{Fake: fakeTG}

	parent := t.TempDir()
	result := svc.DownloadFolder(context.Background(), personalChannelID, "d:project", func(string) (string, error) {
		return parent, nil
	})
	if result.Status != "error" || !strings.Contains(result.Message, "injected folder download failure") {
		t.Fatalf("failed folder download = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(parent, "Project")); !os.IsNotExist(err) {
		t.Fatalf("final folder exists after failure: %v", err)
	}
	assertNoFolderDownloadStaging(t, parent)
}

func assertNoFolderDownloadStaging(t *testing.T, parent string) {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("read destination parent: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".tdrive-folder-download-") {
			t.Fatalf("staging path leaked after download: %s", entry.Name())
		}
	}
}
