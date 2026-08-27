package file

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

func TestImportBatchIsBoundedAndPreservesPairs(t *testing.T) {
	total := 2*importUploadBatchSize + 1
	wantPaths := make([]string, total)
	wantParents := make([]string, total)
	gotPaths := make([]string, 0, total)
	gotParents := make([]string, 0, total)
	batch := newImportBatch()

	consume := func() {
		if batch.len() > importUploadBatchSize {
			t.Fatalf("batch length = %d, exceeds bound %d", batch.len(), importUploadBatchSize)
		}
		if cap(batch.paths) > importUploadBatchSize || cap(batch.parents) > importUploadBatchSize {
			t.Fatalf("batch capacity = (%d, %d), exceeds bound %d", cap(batch.paths), cap(batch.parents), importUploadBatchSize)
		}
		gotPaths = append(gotPaths, batch.paths...)
		gotParents = append(gotParents, batch.parents...)
		batch.reset()
		if batch.len() != 0 {
			t.Fatalf("batch length after reset = %d, want 0", batch.len())
		}
	}

	for i := 0; i < total; i++ {
		wantPaths[i] = fmt.Sprintf("path-%03d", i)
		wantParents[i] = fmt.Sprintf("parent-%03d", i)
		if full := batch.add(wantPaths[i], wantParents[i]); full {
			consume()
		}
		if batch.len() > importUploadBatchSize {
			t.Fatalf("batch length after add = %d, exceeds bound %d", batch.len(), importUploadBatchSize)
		}
	}
	if batch.len() > 0 {
		consume()
	}

	if len(gotPaths) != total || len(gotParents) != total {
		t.Fatalf("consumed pairs = (%d, %d), want (%d, %d)", len(gotPaths), len(gotParents), total, total)
	}
	for i := 0; i < total; i++ {
		if gotPaths[i] != wantPaths[i] || gotParents[i] != wantParents[i] {
			t.Fatalf("pair %d = (%q, %q), want (%q, %q)", i, gotPaths[i], gotParents[i], wantPaths[i], wantParents[i])
		}
	}
}

func contextTestFolderCreator(db *sql.DB) CreateFolderFunc {
	var mu sync.Mutex
	var folderNumber, messageID int64 = 0, 70000
	return func(ctx context.Context, channelID int64, name, parentID string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		mu.Lock()
		defer mu.Unlock()
		folderNumber++
		messageID++
		id := fmt.Sprintf("%sctximp%d", projection.FolderIDPrefix, folderNumber)
		op := projection.Op{Type: projection.OpMkdir, Obj: id, Parent: parentID, Name: name}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return "", err
		}
		if err := projection.ApplyOp(tx, channelID, messageID, op, 7); err != nil {
			_ = tx.Rollback()
			return "", err
		}
		if err := tx.Commit(); err != nil {
			return "", err
		}
		return id, nil
	}
}

func TestRunImportPassesContextAndStopsOnCancellation(t *testing.T) {
	type contextKey struct{}
	const marker = "import-context"

	svc, _, fakeTG, _ := newTestService(t)
	root := filepath.Join(t.TempDir(), "Project")
	mkfile(t, filepath.Join(root, "file.txt"), "must not upload")
	base := context.WithValue(context.Background(), contextKey{}, marker)
	ctx, cancel := context.WithCancel(base)
	defer cancel()
	called := false
	svc.CreateFolder = func(folderCtx context.Context, _ int64, _, _ string) (string, error) {
		called = true
		if got := folderCtx.Value(contextKey{}); got != marker {
			t.Fatalf("folder context marker = %v, want %q", got, marker)
		}
		cancel()
		return projection.FolderIDPrefix + "cancelled", nil
	}

	err := svc.RunImport(ctx, personalChannelID, []string{root}, "", false, false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunImport error = %v, want context.Canceled", err)
	}
	if !called {
		t.Fatal("CreateFolder was not called")
	}
	if sent := fakeTG.SentFiles(); len(sent) != 0 {
		t.Fatalf("sent files after cancellation = %d, want 0", len(sent))
	}
}

type importEventRecord struct {
	name string
	args []any
}

type importTestEventRecorder struct {
	mu      sync.Mutex
	records []importEventRecord
}

func (r *importTestEventRecorder) Emit(name string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, importEventRecord{name: name, args: append([]any(nil), args...)})
}

func (r *importTestEventRecorder) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, len(r.records))
	for i, record := range r.records {
		names[i] = record.name
	}
	return names
}

func (r *importTestEventRecorder) lastPayload(name string) (map[string]any, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for index := len(r.records) - 1; index >= 0; index-- {
		record := r.records[index]
		if record.name != name || len(record.args) != 1 {
			continue
		}
		payload, ok := record.args[0].(map[string]any)
		return payload, ok
	}
	return nil, false
}

type importPeerResolverFunc func(context.Context, int64) (tgclient.InputPeer, error)

func (resolve importPeerResolverFunc) ResolvePeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error) {
	return resolve(ctx, channelID)
}

func TestRunImportEmitsAggregateEventsOnly(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	svc.CreateFolder = contextTestFolderCreator(db)
	events := &importTestEventRecorder{}
	svc.Events = events
	root := filepath.Join(t.TempDir(), "Project")
	mkfile(t, filepath.Join(root, "a.txt"), "a")
	mkfile(t, filepath.Join(root, "b.txt"), "b")

	if err := svc.RunImport(context.Background(), personalChannelID, []string{root}, "", false, false); err != nil {
		t.Fatalf("RunImport: %v", err)
	}

	names := events.names()
	if len(names) == 0 || names[0] != "import_start" {
		t.Fatalf("event names = %v, want import_start first", names)
	}
	if names[len(names)-1] != "import_complete" {
		t.Fatalf("event names = %v, want import_complete last", names)
	}
	for _, want := range []string{"import_uploading", "import_upload_progress"} {
		found := false
		for _, name := range names {
			if name == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("event names = %v, missing %s", names, want)
		}
	}
	for _, name := range names {
		if strings.HasPrefix(name, "upload_") {
			t.Errorf("event names = %v, import leaked per-file event %q", names, name)
		}
	}
}

func TestImportProgressEventsStayBoundedAtHighCardinality(t *testing.T) {
	const total = 10_000
	events := &importTestEventRecorder{}
	svc := &Service{Events: events}
	observer := newImportUploadObserver(svc, total)
	observer.EmitInitial()
	for id := 0; id < total; id++ {
		observer.Completed(id, "")
	}

	events.mu.Lock()
	records := append([]importEventRecord(nil), events.records...)
	events.mu.Unlock()
	if len(records) > 101 {
		t.Fatalf("aggregate progress events = %d, want at most 101", len(records))
	}
	lastProgress := -1.0
	for index, record := range records {
		if record.name != "import_upload_progress" || len(record.args) != 1 {
			t.Fatalf("record %d = %+v, want one aggregate progress payload", index, record)
		}
		payload, ok := record.args[0].(map[string]any)
		if !ok {
			t.Fatalf("record %d payload = %T, want map", index, record.args[0])
		}
		progress, ok := payload["progress"].(float64)
		if !ok || progress < lastProgress || progress < 0 || progress > 100 {
			t.Fatalf("record %d progress = %v after %.2f", index, payload["progress"], lastProgress)
		}
		lastProgress = progress
	}
	if lastProgress != 100 {
		t.Fatalf("final progress = %.2f, want 100", lastProgress)
	}
}

func TestRunImportStopsSchedulingAfterProjectionFailure(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.CreateFolder = contextTestFolderCreator(db)
	root := filepath.Join(t.TempDir(), "Project")
	for index := 0; index < importUploadBatchSize+1; index++ {
		mkfile(t, filepath.Join(root, fmt.Sprintf("file-%03d.txt", index)), "x")
	}
	if _, err := db.Exec(`
		CREATE TRIGGER fail_import_file_projection
		BEFORE INSERT ON files
		BEGIN
			SELECT RAISE(ABORT, 'injected import projection failure');
		END;
	`); err != nil {
		t.Fatalf("create projection trigger: %v", err)
	}

	err := svc.RunImport(context.Background(), personalChannelID, []string{root}, "", false, false)
	if err == nil || !strings.Contains(err.Error(), "injected import projection failure") {
		t.Fatalf("RunImport error = %v, want injected projection failure", err)
	}
	if sent := len(fakeTG.SentFiles()); sent != importUploadBatchSize {
		t.Fatalf("sent files after projection failure = %d, want first bounded window %d", sent, importUploadBatchSize)
	}
}

func TestRunImportStopsSchedulingAfterFatalUploadWindow(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.CreateFolder = contextTestFolderCreator(db)
	svc.MaxConcurrentUploads = 1
	svc.FloodWaitRetry = tgclient.FloodWaitRetryPolicy{
		Sleep: func(context.Context, time.Duration) error { return nil },
	}
	fakeTG.InjectFloodWaits(1)
	root := filepath.Join(t.TempDir(), "Project")
	for index := 0; index < importUploadBatchSize+1; index++ {
		mkfile(t, filepath.Join(root, fmt.Sprintf("file-%03d.txt", index)), "x")
	}

	err := svc.RunImport(context.Background(), personalChannelID, []string{root}, "", false, false)
	if !errors.Is(err, tgclient.ErrFloodWait) {
		t.Fatalf("RunImport error = %v, want FLOOD_WAIT", err)
	}
	if sent := len(fakeTG.SentFiles()); sent != importUploadBatchSize-1 {
		t.Fatalf("sent files after fatal upload window = %d, want first window minus failed send %d", sent, importUploadBatchSize-1)
	}
}

func TestRunImportCompletionReportsFatalFolderOnlyFailure(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	events := &importTestEventRecorder{}
	svc.Events = events
	fatalErr := fmt.Errorf("folder projection failed: %w", tgclient.ErrSendOutcomeUnknown)
	svc.CreateFolder = func(context.Context, int64, string, string) (string, error) {
		return "", fatalErr
	}

	folder := filepath.Join(t.TempDir(), "Empty folder")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatalf("create empty folder: %v", err)
	}

	err := svc.RunImport(context.Background(), personalChannelID, []string{folder}, "", false, false)
	if !errors.Is(err, tgclient.ErrSendOutcomeUnknown) {
		t.Fatalf("RunImport error = %v, want ErrSendOutcomeUnknown", err)
	}
	if files := fakeTG.SentFiles(); len(files) != 0 {
		t.Fatalf("sent files = %d, want 0 for folder-only failure", len(files))
	}
	payload, ok := events.lastPayload("import_complete")
	if !ok {
		t.Fatal("import_complete payload missing")
	}
	if got := payload["status"]; got != "failed" {
		t.Fatalf("completion status = %v, want failed", got)
	}
	if got := payload["error"]; got == nil || !strings.Contains(fmt.Sprint(got), "folder projection failed") {
		t.Fatalf("completion error = %v, want folder projection failure", got)
	}
	if got := payload["uploaded"]; got != 0 {
		t.Fatalf("completion uploaded = %v, want 0", got)
	}
	if got := payload["failed"]; got != 0 {
		t.Fatalf("completion failed files = %v, want 0", got)
	}
}

func TestRunImportStopsAfterNestedControlProjectionFailure(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	createCalls := 0
	svc.CreateFolder = func(context.Context, int64, string, string) (string, error) {
		createCalls++
		if createCalls == 1 {
			return projection.FolderIDPrefix + "top", nil
		}
		return "", fmt.Errorf("nested folder projection failed: %w", projection.ErrControlProjection)
	}

	selectionRoot := t.TempDir()
	folder := filepath.Join(selectionRoot, "Project")
	if err := os.MkdirAll(filepath.Join(folder, "nested"), 0o755); err != nil {
		t.Fatalf("create nested folder: %v", err)
	}
	laterFile := filepath.Join(selectionRoot, "later.txt")
	if err := os.WriteFile(laterFile, []byte("must not upload"), 0o600); err != nil {
		t.Fatalf("create later file: %v", err)
	}

	err := svc.RunImport(
		context.Background(),
		personalChannelID,
		[]string{folder, laterFile},
		"",
		false,
		false,
	)
	if !errors.Is(err, projection.ErrControlProjection) {
		t.Fatalf("RunImport error = %v, want ErrControlProjection", err)
	}
	if createCalls != 2 {
		t.Fatalf("folder creation calls = %d, want top-level and failing nested folder", createCalls)
	}
	if files := fakeTG.SentFiles(); len(files) != 0 {
		t.Fatalf("sent files after nested projection failure = %d, want 0", len(files))
	}
}

func TestRunImportArchiveFallbackUsesScheduledUploadCount(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.CreateFolder = contextTestFolderCreator(db)
	events := &importTestEventRecorder{}
	svc.Events = events
	archivePath := buildZip(t, map[string]string{
		"one.txt":   "one",
		"two.txt":   "two",
		"three.txt": "three",
	}, nil, nil)
	if plan := svc.PlanImport([]string{archivePath}, false, true); plan.Files != 3 {
		t.Fatalf("preflight files = %d, want 3 archive members", plan.Files)
	}

	baseResolver := svc.Peers
	svc.Peers = importPeerResolverFunc(func(ctx context.Context, channelID int64) (tgclient.InputPeer, error) {
		peer, err := baseResolver.ResolvePeer(ctx, channelID)
		if err != nil {
			return tgclient.InputPeer{}, err
		}
		if err := os.WriteFile(archivePath, []byte("corrupt after preflight"), 0o600); err != nil {
			return tgclient.InputPeer{}, err
		}
		return peer, nil
	})

	if err := svc.RunImport(context.Background(), personalChannelID, []string{archivePath}, "", false, true); err != nil {
		t.Fatalf("RunImport fallback: %v", err)
	}
	if files := fakeTG.SentFiles(); len(files) != 1 {
		t.Fatalf("sent files = %d, want one raw archive fallback", len(files))
	}
	payload, ok := events.lastPayload("import_complete")
	if !ok {
		t.Fatal("import_complete payload missing")
	}
	if got := payload["uploaded"]; got != 1 {
		t.Fatalf("completion uploaded = %v, want 1", got)
	}
	if got := payload["failed"]; got != 0 {
		t.Fatalf("completion failed = %v, want 0 for successful raw fallback", got)
	}
	if got := payload["status"]; got != "done" {
		t.Fatalf("completion status = %v, want done", got)
	}
	if got := payload["errorCount"]; got != 1 {
		t.Fatalf("completion errorCount = %v, want one extraction warning", got)
	}
}

func TestRunImportArchiveFallbackStopsSchedulingAfterFatalUploadWindow(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	events := &importTestEventRecorder{}
	svc.Events = events
	svc.MaxConcurrentUploads = 1
	svc.FloodWaitRetry = tgclient.FloodWaitRetryPolicy{
		Sleep: func(context.Context, time.Duration) error { return nil },
	}
	fakeTG.InjectFloodWaits(1)

	selectionRoot := t.TempDir()
	paths := make([]string, 0, importUploadBatchSize+1)
	for index := 0; index < importUploadBatchSize-1; index++ {
		filePath := filepath.Join(selectionRoot, fmt.Sprintf("file-%03d.txt", index))
		mkfile(t, filePath, "x")
		paths = append(paths, filePath)
	}
	archivePath := buildZip(t, map[string]string{"inside.txt": "inside"}, nil, nil)
	paths = append(paths, archivePath)
	laterFolder := filepath.Join(selectionRoot, "must-not-be-created")
	if err := os.Mkdir(laterFolder, 0o755); err != nil {
		t.Fatalf("create later folder: %v", err)
	}
	paths = append(paths, laterFolder)

	createCalls := 0
	svc.CreateFolder = func(context.Context, int64, string, string) (string, error) {
		createCalls++
		return "", fmt.Errorf("later folder creation: %w", projection.ErrControlProjection)
	}
	baseResolver := svc.Peers
	svc.Peers = importPeerResolverFunc(func(ctx context.Context, channelID int64) (tgclient.InputPeer, error) {
		peer, err := baseResolver.ResolvePeer(ctx, channelID)
		if err != nil {
			return tgclient.InputPeer{}, err
		}
		if err := os.WriteFile(archivePath, []byte("corrupt after preflight"), 0o600); err != nil {
			return tgclient.InputPeer{}, err
		}
		return peer, nil
	})

	err := svc.RunImport(context.Background(), personalChannelID, paths, "", false, true)
	if !errors.Is(err, tgclient.ErrFloodWait) {
		t.Fatalf("RunImport error = %v, want FLOOD_WAIT", err)
	}
	if createCalls != 0 {
		t.Fatalf("folder creation calls after fatal archive fallback = %d, want 0", createCalls)
	}
	payload, ok := events.lastPayload("import_complete")
	if !ok {
		t.Fatal("import_complete payload missing")
	}
	if got := payload["scheduled"]; got != importUploadBatchSize {
		t.Fatalf("completion scheduled = %v, want one bounded window %d", got, importUploadBatchSize)
	}
	if got := payload["status"]; got != "failed" {
		t.Fatalf("completion status = %v, want failed", got)
	}
	if got := fmt.Sprint(payload["error"]); !strings.Contains(got, "flood wait") {
		t.Fatalf("completion error = %q, want original FLOOD_WAIT", got)
	}
}
