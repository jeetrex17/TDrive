package mountwrite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestSQLiteJournalPersistsRecordAndCompareAndSwapTransition(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "journal.db")
	db := openJournalDB(t, dbPath)
	if err := EnsureJournalSchema(ctx, db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	if err := EnsureJournalSchema(ctx, db); err != nil {
		t.Fatalf("ensure schema twice: %v", err)
	}
	journal, err := NewSQLiteJournal(db)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}

	createdAt := time.Unix(1_700_000_000, 123).UTC()
	record := JournalRecord{
		OperationID: "op-1",
		Mutation: Mutation{
			Kind:                MutationPut,
			DriveID:             42,
			DestinationParentID: "",
			DestinationName:     "notes.txt",
			ContentLength:       12,
		},
		State:     StateReceiving,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
	if err := journal.Create(ctx, record); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := journal.Create(ctx, record); !errors.Is(err, ErrOperationExists) {
		t.Fatalf("duplicate create error = %v, want ErrOperationExists", err)
	}

	staged := StagedObject{Key: "stage-key", Path: "/private/stage", PlaintextSize: 12, StoredSize: 12}
	updatedAt := createdAt.Add(time.Second)
	got, err := journal.Transition(ctx, "op-1", StateReceiving, StateStaged, JournalPatch{
		Staged:    &staged,
		UpdatedAt: updatedAt,
	})
	if err != nil {
		t.Fatalf("transition: %v", err)
	}
	if got.State != StateStaged || got.Staged == nil || got.Staged.Key != staged.Key {
		t.Fatalf("transitioned record = %#v", got)
	}

	if _, err := journal.Transition(ctx, "op-1", StateReceiving, StateUploading, JournalPatch{}); !errors.Is(err, ErrJournalConflict) {
		t.Fatalf("stale CAS error = %v, want ErrJournalConflict", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close first db: %v", err)
	}
	db = openJournalDB(t, dbPath)
	journal, err = NewSQLiteJournal(db)
	if err != nil {
		t.Fatalf("reopen journal: %v", err)
	}
	persisted, found, err := journal.Get(ctx, "op-1")
	if err != nil || !found {
		t.Fatalf("get persisted: found=%v err=%v", found, err)
	}
	if persisted.State != StateStaged || persisted.UpdatedAt != updatedAt || persisted.Mutation.DestinationName != "notes.txt" {
		t.Fatalf("persisted record = %#v", persisted)
	}
}

func TestSQLiteJournalListsOnlyRecoverableRecordsInStableOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openJournalDB(t, filepath.Join(t.TempDir(), "journal.db"))
	if err := EnsureJournalSchema(ctx, db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	journal, err := NewSQLiteJournal(db)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}

	states := []JournalState{StateDone, StateUploading, StateAborted, StateProjectionPending, StateStaged}
	for i, state := range states {
		at := time.Unix(int64(100+i), 0).UTC()
		record := JournalRecord{
			OperationID: string(rune('a' + i)),
			Mutation:    Mutation{Kind: MutationPut, DriveID: 42, DestinationParentID: "", DestinationName: "x"},
			State:       state,
			CreatedAt:   at,
			UpdatedAt:   at,
		}
		if err := journal.Create(ctx, record); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	records, err := journal.ListRecoverable(ctx)
	if err != nil {
		t.Fatalf("list recoverable: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("recoverable count = %d, want 3: %#v", len(records), records)
	}
	gotIDs := []string{records[0].OperationID, records[1].OperationID, records[2].OperationID}
	assertStringsEqual(t, gotIDs, []string{"b", "d", "e"})
}

func TestSQLiteJournalRejectsInvalidStateTransitionWithoutMutation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openJournalDB(t, filepath.Join(t.TempDir(), "journal.db"))
	if err := EnsureJournalSchema(ctx, db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	journal, err := NewSQLiteJournal(db)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	record := JournalRecord{
		OperationID: "op-invalid",
		Mutation:    Mutation{Kind: MutationPut, DriveID: 42, DestinationParentID: "", DestinationName: "x"},
		State:       StateReceiving,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	if err := journal.Create(ctx, record); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := journal.Transition(ctx, record.OperationID, StateReceiving, StateCommitting, JournalPatch{}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("invalid transition error = %v, want ErrInvalidTransition", err)
	}
	persisted, found, err := journal.Get(ctx, record.OperationID)
	if err != nil || !found || persisted.State != StateReceiving {
		t.Fatalf("record mutated: %#v found=%v err=%v", persisted, found, err)
	}
}

func TestSQLiteJournalValidatesBoundariesAndMissingRecords(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	if err := EnsureJournalSchema(ctx, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil schema db error = %v", err)
	}
	if _, err := NewSQLiteJournal(nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil journal db error = %v", err)
	}
	db := openJournalDB(t, filepath.Join(t.TempDir(), "journal.db"))
	if err := EnsureJournalSchema(ctx, db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	journal, err := NewSQLiteJournal(db)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	if _, _, err := journal.Get(ctx, ""); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("empty get error = %v", err)
	}
	if _, found, err := journal.Get(ctx, "missing"); err != nil || found {
		t.Fatalf("missing get found=%v err=%v", found, err)
	}
	if _, err := journal.Transition(ctx, "missing", StateReceiving, StateStaged, JournalPatch{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing transition error = %v", err)
	}

	now := time.Now().UTC()
	invalidRecords := []JournalRecord{
		{},
		{OperationID: "op", Mutation: Mutation{Kind: MutationPut, DriveID: 42, DestinationName: "file"}, State: StateReceiving, UpdatedAt: now},
		{OperationID: "op", Mutation: Mutation{Kind: MutationPut, DriveID: 0, DestinationName: "file"}, State: StateReceiving, CreatedAt: now, UpdatedAt: now},
		{OperationID: "op", Mutation: Mutation{Kind: MutationPut, DriveID: 42, DestinationName: "file"}, State: JournalState("unknown"), CreatedAt: now, UpdatedAt: now},
	}
	for _, record := range invalidRecords {
		if err := journal.Create(ctx, record); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("invalid record %#v error = %v", record, err)
		}
	}
}

func TestSQLiteJournalCopiesRemoteBodyAndDetectsCorruptRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openJournalDB(t, filepath.Join(t.TempDir(), "journal.db"))
	if err := EnsureJournalSchema(ctx, db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	journal, err := NewSQLiteJournal(db)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	now := time.Now().UTC()
	body := &RemoteBody{UploadUUID: "upload", MessageIDs: []int64{1, 2}}
	record := JournalRecord{
		OperationID: "immutable-body",
		Mutation:    Mutation{Kind: MutationPut, DriveID: 42, DestinationName: "file.txt"},
		State:       StateUploaded,
		Body:        body,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := journal.Create(ctx, record); err != nil {
		t.Fatalf("create: %v", err)
	}
	body.MessageIDs[0] = 99
	got, found, err := journal.Get(ctx, record.OperationID)
	if err != nil || !found || got.Body.MessageIDs[0] != 1 {
		t.Fatalf("persisted body = %#v found=%v err=%v", got.Body, found, err)
	}
	got.Body.MessageIDs[0] = 88
	again, _, err := journal.Get(ctx, record.OperationID)
	if err != nil || again.Body.MessageIDs[0] != 1 {
		t.Fatalf("journal returned mutable body: %#v err=%v", again.Body, err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO mount_write_journal
		(operation_id, kind, state, drive_id, mutation_json, error_code, created_at_ns, updated_at_ns)
		VALUES ('corrupt', 'put', 'receiving', 42, '{', '', 1, 1)`)
	if err != nil {
		t.Fatalf("insert corrupt row: %v", err)
	}
	if _, _, err := journal.Get(ctx, "corrupt"); err == nil {
		t.Fatal("corrupt row should fail decoding")
	}
}

func TestSQLiteJournalRejectsSemanticallyCorruptEncryptedMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openJournalDB(t, filepath.Join(t.TempDir(), "journal.db"))
	if err := EnsureJournalSchema(ctx, db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	journal, err := NewSQLiteJournal(db)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	mutationJSON := `{"kind":"put","drive_id":42,"destination_name":"private.txt","content_length":1,"encryption_version":1}`
	stagedJSON := `{"key":"safe.stage","path":"/private/safe.stage","plaintext_size":1,"stored_size":1,"encryption_version":1}`
	_, err = db.ExecContext(ctx, `INSERT INTO mount_write_journal
		(operation_id, kind, state, drive_id, mutation_json, staged_json, error_code, created_at_ns, updated_at_ns)
		VALUES (?, 'put', 'staged', 42, ?, ?, '', 1, 1)`, "corrupt-encrypted", mutationJSON, stagedJSON)
	if err != nil {
		t.Fatalf("insert corrupt encrypted row: %v", err)
	}
	if _, _, err := journal.Get(ctx, "corrupt-encrypted"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("corrupt encrypted metadata error = %v, want ErrInvalidRequest", err)
	}
}

func TestSQLiteJournalRejectsZeroLengthMutationWithNonEmptyStage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openJournalDB(t, filepath.Join(t.TempDir(), "journal.db"))
	if err := EnsureJournalSchema(ctx, db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	journal, err := NewSQLiteJournal(db)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	mutationJSON := `{"kind":"put","drive_id":42,"destination_name":"empty.txt","content_length":0}`
	stagedJSON := `{"key":"safe.stage","path":"/private/safe.stage","plaintext_size":1,"stored_size":1}`
	_, err = db.ExecContext(ctx, `INSERT INTO mount_write_journal
		(operation_id, kind, state, drive_id, mutation_json, staged_json, error_code, created_at_ns, updated_at_ns)
		VALUES (?, 'put', 'staged', 42, ?, ?, '', 1, 1)`, "corrupt-empty", mutationJSON, stagedJSON)
	if err != nil {
		t.Fatalf("insert corrupt zero-length row: %v", err)
	}
	if _, _, err := journal.Get(ctx, "corrupt-empty"); !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("corrupt zero-length metadata error = %v, want ErrLengthMismatch", err)
	}
}

func TestStagedObjectReadsLegacyPlaintextJournalShape(t *testing.T) {
	t.Parallel()

	var staged StagedObject
	if err := json.Unmarshal([]byte(`{"key":"legacy.stage","path":"/private/legacy.stage","size":12}`), &staged); err != nil {
		t.Fatalf("decode legacy stage: %v", err)
	}
	if staged.PlaintextSize != 12 || staged.StoredSize != 12 || staged.EncryptionVersion != EncryptionNone {
		t.Fatalf("legacy stage = %#v", staged)
	}
	encoded, err := json.Marshal(staged)
	if err != nil {
		t.Fatalf("encode current stage: %v", err)
	}
	if bytes.Contains(encoded, []byte(`"size"`)) || !bytes.Contains(encoded, []byte(`"plaintext_size":12`)) || !bytes.Contains(encoded, []byte(`"stored_size":12`)) {
		t.Fatalf("current stage JSON = %s", encoded)
	}
}

func TestSQLiteJournalRejectsTamperedPersistedHiddenCleanupReceipt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openJournalDB(t, filepath.Join(t.TempDir(), "journal.db"))
	if err := EnsureJournalSchema(ctx, db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	journal, err := NewSQLiteJournal(db)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	at := time.Unix(1_700_000_000, 0).UTC()
	record := JournalRecord{
		OperationID: "tampered-hidden-cleanup",
		Mutation: Mutation{
			Kind: MutationPut, DriveID: 42, DestinationName: "receipt.bin", ContentLength: 1,
		},
		State: StateCleanupPending,
		Body: &RemoteBody{
			UploadUUID: "hu-valid", PartCount: 1, PlaintextSize: 1, StoredSize: 1,
			MessageIDs: []int64{71},
		},
		CreatedAt: at,
		UpdatedAt: at,
	}
	if err := journal.Create(ctx, record); err != nil {
		t.Fatalf("create valid cleanup record: %v", err)
	}
	tamperedBody, err := json.Marshal(RemoteBody{
		UploadUUID: "hu-valid", PartCount: 2, PlaintextSize: 1, StoredSize: 1,
		MessageIDs: []int64{71, 71},
	})
	if err != nil {
		t.Fatalf("encode tampered body: %v", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`UPDATE mount_write_journal SET body_json = ? WHERE operation_id = ?`,
		tamperedBody,
		record.OperationID,
	); err != nil {
		t.Fatalf("tamper persisted body: %v", err)
	}
	if _, _, err := journal.Get(ctx, record.OperationID); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Get error = %v, want invalid request", err)
	}
}

func TestSQLiteJournalReportsClosedDatabaseFailures(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := EnsureJournalSchema(ctx, db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	journal, err := NewSQLiteJournal(db)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := EnsureJournalSchema(ctx, db); err == nil {
		t.Fatal("schema on closed database should fail")
	}
	now := time.Now().UTC()
	record := JournalRecord{
		OperationID: "closed",
		Mutation:    Mutation{Kind: MutationPut, DriveID: 42, DestinationName: "file.txt"},
		State:       StateReceiving,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := journal.Create(ctx, record); err == nil {
		t.Fatal("create on closed database should fail")
	}
	if _, _, err := journal.Get(ctx, record.OperationID); err == nil {
		t.Fatal("get on closed database should fail")
	}
	if _, err := journal.Transition(ctx, record.OperationID, StateReceiving, StateStaged, JournalPatch{}); err == nil {
		t.Fatal("transition on closed database should fail")
	}
	if _, err := journal.ListRecoverable(ctx); err == nil {
		t.Fatal("list on closed database should fail")
	}
}

func openJournalDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}
