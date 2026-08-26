package file

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
