package mountwrite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

type JournalRecord struct {
	OperationID string
	Mutation    Mutation
	State       JournalState
	Staged      *StagedObject
	Body        *RemoteBody
	Result      *MutationResult
	ErrorCode   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type JournalPatch struct {
	Staged    *StagedObject
	Body      *RemoteBody
	Result    *MutationResult
	ErrorCode string
	UpdatedAt time.Time
}

type Journal interface {
	Create(ctx context.Context, record JournalRecord) error
	Get(ctx context.Context, operationID string) (JournalRecord, bool, error)
	Transition(
		ctx context.Context,
		operationID string,
		expected, next JournalState,
		patch JournalPatch,
	) (JournalRecord, error)
	ListRecoverable(ctx context.Context) ([]JournalRecord, error)
}

type SQLiteJournal struct {
	db *sql.DB
}

// EnsureJournalSchema installs the additive mount-write journal schema.
func EnsureJournalSchema(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return ErrInvalidRequest
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS mount_write_journal (
			operation_id TEXT PRIMARY KEY NOT NULL,
			kind TEXT NOT NULL,
			state TEXT NOT NULL,
			drive_id INTEGER NOT NULL,
			mutation_json BLOB NOT NULL,
			staged_json BLOB,
			body_json BLOB,
			result_json BLOB,
			error_code TEXT NOT NULL DEFAULT '',
			created_at_ns INTEGER NOT NULL,
			updated_at_ns INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_mount_write_journal_recovery
			ON mount_write_journal(state, updated_at_ns, operation_id)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("ensure mount write journal schema: %w", err)
		}
	}
	return nil
}

// NewSQLiteJournal binds a journal to an initialized database.
func NewSQLiteJournal(db *sql.DB) (*SQLiteJournal, error) {
	if db == nil {
		return nil, ErrInvalidRequest
	}
	return &SQLiteJournal{db: db}, nil
}

func (j *SQLiteJournal) Create(ctx context.Context, record JournalRecord) error {
	if err := validateRecord(record); err != nil {
		return err
	}
	mutationJSON, err := json.Marshal(record.Mutation)
	if err != nil {
		return fmt.Errorf("encode mutation: %w", err)
	}
	stagedJSON, err := marshalOptional(record.Staged)
	if err != nil {
		return fmt.Errorf("encode staged object: %w", err)
	}
	bodyJSON, err := marshalOptional(record.Body)
	if err != nil {
		return fmt.Errorf("encode remote body: %w", err)
	}
	resultJSON, err := marshalOptional(record.Result)
	if err != nil {
		return fmt.Errorf("encode result: %w", err)
	}

	_, err = j.db.ExecContext(ctx, `
		INSERT INTO mount_write_journal (
			operation_id, kind, state, drive_id, mutation_json,
			staged_json, body_json, result_json, error_code,
			created_at_ns, updated_at_ns
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.OperationID,
		record.Mutation.Kind,
		record.State,
		record.Mutation.DriveID,
		mutationJSON,
		nullableJSON(stagedJSON),
		nullableJSON(bodyJSON),
		nullableJSON(resultJSON),
		record.ErrorCode,
		record.CreatedAt.UnixNano(),
		record.UpdatedAt.UnixNano(),
	)
	if err != nil {
		if isUniqueConstraint(err) {
			return ErrOperationExists
		}
		return fmt.Errorf("create mount write journal record: %w", err)
	}
	return nil
}

func (j *SQLiteJournal) Get(ctx context.Context, operationID string) (JournalRecord, bool, error) {
	if operationID == "" {
		return JournalRecord{}, false, ErrInvalidRequest
	}
	row := j.db.QueryRowContext(ctx, journalSelect+` WHERE operation_id = ?`, operationID)
	record, err := scanJournalRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return JournalRecord{}, false, nil
	}
	if err != nil {
		return JournalRecord{}, false, fmt.Errorf("get mount write journal record: %w", err)
	}
	return record, true, nil
}

func (j *SQLiteJournal) Transition(
	ctx context.Context,
	operationID string,
	expected, next JournalState,
	patch JournalPatch,
) (JournalRecord, error) {
	record, found, err := j.Get(ctx, operationID)
	if err != nil {
		return JournalRecord{}, err
	}
	if !found {
		return JournalRecord{}, ErrNotFound
	}
	if record.State != expected {
		return JournalRecord{}, ErrJournalConflict
	}
	if err := ValidateTransition(expected, next); err != nil {
		return JournalRecord{}, err
	}
	updated := applyPatch(record, next, patch)
	stagedJSON, err := marshalOptional(updated.Staged)
	if err != nil {
		return JournalRecord{}, fmt.Errorf("encode staged object: %w", err)
	}
	bodyJSON, err := marshalOptional(updated.Body)
	if err != nil {
		return JournalRecord{}, fmt.Errorf("encode remote body: %w", err)
	}
	resultJSON, err := marshalOptional(updated.Result)
	if err != nil {
		return JournalRecord{}, fmt.Errorf("encode result: %w", err)
	}

	result, err := j.db.ExecContext(ctx, `
		UPDATE mount_write_journal
		SET state = ?, staged_json = ?, body_json = ?, result_json = ?, error_code = ?, updated_at_ns = ?
		WHERE operation_id = ? AND state = ?`,
		next,
		nullableJSON(stagedJSON),
		nullableJSON(bodyJSON),
		nullableJSON(resultJSON),
		updated.ErrorCode,
		updated.UpdatedAt.UnixNano(),
		operationID,
		expected,
	)
	if err != nil {
		return JournalRecord{}, fmt.Errorf("transition mount write journal record: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return JournalRecord{}, fmt.Errorf("inspect journal transition: %w", err)
	}
	if affected != 1 {
		return JournalRecord{}, ErrJournalConflict
	}
	return cloneRecord(updated), nil
}

func (j *SQLiteJournal) ListRecoverable(ctx context.Context) ([]JournalRecord, error) {
	rows, err := j.db.QueryContext(ctx, journalSelect+`
		WHERE state NOT IN (?, ?)
		ORDER BY updated_at_ns, operation_id`, StateDone, StateAborted)
	if err != nil {
		return nil, fmt.Errorf("list recoverable mount writes: %w", err)
	}
	defer rows.Close()

	records := make([]JournalRecord, 0)
	for rows.Next() {
		record, err := scanJournalRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("scan recoverable mount write: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recoverable mount writes: %w", err)
	}
	return records, nil
}

const journalSelect = `
	SELECT operation_id, state, mutation_json, staged_json, body_json,
		result_json, error_code, created_at_ns, updated_at_ns
	FROM mount_write_journal`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanJournalRecord(row rowScanner) (JournalRecord, error) {
	var record JournalRecord
	var mutationJSON []byte
	var stagedJSON, bodyJSON, resultJSON []byte
	var createdAtNS, updatedAtNS int64
	if err := row.Scan(
		&record.OperationID,
		&record.State,
		&mutationJSON,
		&stagedJSON,
		&bodyJSON,
		&resultJSON,
		&record.ErrorCode,
		&createdAtNS,
		&updatedAtNS,
	); err != nil {
		return JournalRecord{}, err
	}
	if err := json.Unmarshal(mutationJSON, &record.Mutation); err != nil {
		return JournalRecord{}, fmt.Errorf("decode mutation: %w", err)
	}
	if err := unmarshalOptional(stagedJSON, &record.Staged); err != nil {
		return JournalRecord{}, fmt.Errorf("decode staged object: %w", err)
	}
	if err := unmarshalOptional(bodyJSON, &record.Body); err != nil {
		return JournalRecord{}, fmt.Errorf("decode remote body: %w", err)
	}
	if err := unmarshalOptional(resultJSON, &record.Result); err != nil {
		return JournalRecord{}, fmt.Errorf("decode result: %w", err)
	}
	record.CreatedAt = time.Unix(0, createdAtNS).UTC()
	record.UpdatedAt = time.Unix(0, updatedAtNS).UTC()
	return cloneRecord(record), nil
}

func applyPatch(record JournalRecord, state JournalState, patch JournalPatch) JournalRecord {
	updated := cloneRecord(record)
	updated.State = state
	if patch.Staged != nil {
		copyOfStaged := *patch.Staged
		updated.Staged = &copyOfStaged
	}
	if patch.Body != nil {
		copyOfBody := cloneBody(*patch.Body)
		updated.Body = &copyOfBody
	}
	if patch.Result != nil {
		copyOfResult := *patch.Result
		updated.Result = &copyOfResult
	}
	if patch.ErrorCode != "" {
		updated.ErrorCode = patch.ErrorCode
	}
	if !patch.UpdatedAt.IsZero() {
		updated.UpdatedAt = patch.UpdatedAt.UTC()
	}
	return updated
}

func cloneRecord(record JournalRecord) JournalRecord {
	copyOfRecord := record
	if record.Staged != nil {
		copyOfStaged := *record.Staged
		copyOfRecord.Staged = &copyOfStaged
	}
	if record.Body != nil {
		copyOfBody := cloneBody(*record.Body)
		copyOfRecord.Body = &copyOfBody
	}
	if record.Result != nil {
		copyOfResult := *record.Result
		copyOfRecord.Result = &copyOfResult
	}
	return copyOfRecord
}

func cloneBody(body RemoteBody) RemoteBody {
	copyOfBody := body
	copyOfBody.MessageIDs = append([]int64(nil), body.MessageIDs...)
	return copyOfBody
}

func validateRecord(record JournalRecord) error {
	if record.OperationID == "" || !validOperationID(record.OperationID) || record.CreatedAt.IsZero() || record.UpdatedAt.IsZero() {
		return ErrInvalidRequest
	}
	if err := validateMutation(record.Mutation); err != nil {
		return err
	}
	if !knownState(record.State) {
		return ErrInvalidRequest
	}
	return nil
}

func knownState(state JournalState) bool {
	switch state {
	case StateReceiving, StateStaged, StateUploading, StateUploaded, StateCommitting,
		StateReconciling, StateRemoteCommitted, StateProjectionPending,
		StateCleanupPending, StateDone, StateAborted:
		return true
	default:
		return false
	}
}

func marshalOptional(value any) ([]byte, error) {
	if value == nil || (reflect.ValueOf(value).Kind() == reflect.Pointer && reflect.ValueOf(value).IsNil()) {
		return nil, nil
	}
	return json.Marshal(value)
}

func nullableJSON(data []byte) any {
	if len(data) == 0 {
		return nil
	}
	return data
}

func unmarshalOptional[T any](data []byte, destination **T) error {
	if len(data) == 0 {
		*destination = nil
		return nil
	}
	value := new(T)
	if err := json.Unmarshal(data, value); err != nil {
		return err
	}
	*destination = value
	return nil
}

func isUniqueConstraint(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}

var _ Journal = (*SQLiteJournal)(nil)
