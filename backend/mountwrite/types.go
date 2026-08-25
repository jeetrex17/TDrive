package mountwrite

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type MutationKind string

const (
	MutationPut    MutationKind = "put"
	MutationMkdir  MutationKind = "mkdir"
	MutationMove   MutationKind = "move"
	MutationDelete MutationKind = "delete"
)

type JournalState string

const (
	StateReceiving         JournalState = "receiving"
	StateStaged            JournalState = "staged"
	StateUploading         JournalState = "uploading"
	StateUploaded          JournalState = "uploaded"
	StateCommitting        JournalState = "committing"
	StateReconciling       JournalState = "reconciling"
	StateRemoteCommitted   JournalState = "remote_committed"
	StateProjectionPending JournalState = "projection_pending"
	StateCleanupPending    JournalState = "cleanup_pending"
	StateDone              JournalState = "done"
	StateAborted           JournalState = "aborted"
)

type Mutation struct {
	Kind                   MutationKind  `json:"kind"`
	DriveID                int64         `json:"drive_id"`
	ObjectID               string        `json:"object_id,omitempty"`
	SourceParentID         string        `json:"source_parent_id,omitempty"`
	DestinationParentID    string        `json:"destination_parent_id,omitempty"`
	DestinationName        string        `json:"destination_name,omitempty"`
	ExpectedRevision       uint64        `json:"expected_revision,omitempty"`
	CreateOnly             bool          `json:"create_only,omitempty"`
	OverwriteTargetID      string        `json:"overwrite_target_id,omitempty"`
	ExpectedTargetRevision uint64        `json:"expected_target_revision,omitempty"`
	Recursive              bool          `json:"recursive,omitempty"`
	TrashRetention         time.Duration `json:"trash_retention,omitempty"`
	ContentLength          int64         `json:"content_length,omitempty"`
	MaxBytes               int64         `json:"max_bytes,omitempty"`
}

func (m Mutation) AffectedParents() []string {
	parents := make([]string, 0, 2)
	switch m.Kind {
	case MutationMove:
		parents = append(parents, m.SourceParentID, m.DestinationParentID)
	default:
		parents = append(parents, m.DestinationParentID)
	}
	return sortedUnique(parents, true)
}

func (m Mutation) AffectedObjects(result MutationResult) []string {
	objects := []string{m.ObjectID, m.OverwriteTargetID, result.ObjectID}
	return sortedUnique(objects, false)
}

func (m Mutation) lockKeys() []string {
	keys := make([]string, 0, 4)
	if m.ObjectID != "" {
		keys = append(keys, objectLockKey(m.DriveID, m.ObjectID))
	}
	if m.OverwriteTargetID != "" {
		keys = append(keys, objectLockKey(m.DriveID, m.OverwriteTargetID))
	}
	keys = append(keys, namespaceLockKey(m.DriveID, m.DestinationParentID, m.DestinationName))
	return sortedUnique(keys, false)
}

type PutRequest struct {
	OperationID      string
	DriveID          int64
	ParentID         string
	Name             string
	ExistingObjectID string
	ExpectedRevision uint64
	CreateOnly       bool
	ContentLength    int64
	MaxBytes         int64
}

func (r PutRequest) Validate() error {
	if !validOperationID(r.OperationID) || r.DriveID == 0 || !validName(r.Name) || r.ContentLength < -1 || r.MaxBytes < 0 {
		return ErrInvalidRequest
	}
	if r.ContentLength >= 0 && r.MaxBytes > 0 && r.ContentLength > r.MaxBytes {
		return ErrQuotaExceeded
	}
	if r.CreateOnly && r.ExistingObjectID != "" {
		return ErrInvalidRequest
	}
	return nil
}

func (r PutRequest) mutation() Mutation {
	return Mutation{
		Kind:                MutationPut,
		DriveID:             r.DriveID,
		ObjectID:            r.ExistingObjectID,
		DestinationParentID: r.ParentID,
		DestinationName:     r.Name,
		ExpectedRevision:    r.ExpectedRevision,
		CreateOnly:          r.CreateOnly,
		ContentLength:       r.ContentLength,
		MaxBytes:            r.MaxBytes,
	}
}

type MkdirRequest struct {
	OperationID string
	DriveID     int64
	ParentID    string
	Name        string
}

func (r MkdirRequest) Validate() error {
	if !validOperationID(r.OperationID) || r.DriveID == 0 || !validName(r.Name) {
		return ErrInvalidRequest
	}
	return nil
}

func (r MkdirRequest) mutation() Mutation {
	return Mutation{
		Kind:                MutationMkdir,
		DriveID:             r.DriveID,
		DestinationParentID: r.ParentID,
		DestinationName:     r.Name,
		CreateOnly:          true,
	}
}

type MoveRequest struct {
	OperationID            string
	DriveID                int64
	ObjectID               string
	SourceParentID         string
	DestinationParentID    string
	DestinationName        string
	ExpectedSourceRevision uint64
	OverwriteTargetID      string
	ExpectedTargetRevision uint64
}

func (r MoveRequest) Validate() error {
	if !validOperationID(r.OperationID) || r.DriveID == 0 || r.ObjectID == "" || !validName(r.DestinationName) {
		return ErrInvalidRequest
	}
	if r.OverwriteTargetID == r.ObjectID && r.OverwriteTargetID != "" {
		return ErrInvalidRequest
	}
	if r.ExpectedTargetRevision != 0 && r.OverwriteTargetID == "" {
		return ErrInvalidRequest
	}
	return nil
}

func (r MoveRequest) mutation() Mutation {
	return Mutation{
		Kind:                   MutationMove,
		DriveID:                r.DriveID,
		ObjectID:               r.ObjectID,
		SourceParentID:         r.SourceParentID,
		DestinationParentID:    r.DestinationParentID,
		DestinationName:        r.DestinationName,
		ExpectedRevision:       r.ExpectedSourceRevision,
		OverwriteTargetID:      r.OverwriteTargetID,
		ExpectedTargetRevision: r.ExpectedTargetRevision,
	}
}

type DeleteRequest struct {
	OperationID      string
	DriveID          int64
	ObjectID         string
	ParentID         string
	ExpectedRevision uint64
	Recursive        bool
	TrashRetention   time.Duration
}

func (r DeleteRequest) Validate() error {
	if !validOperationID(r.OperationID) || r.DriveID == 0 || r.ObjectID == "" || r.TrashRetention < 0 {
		return ErrInvalidRequest
	}
	return nil
}

func (r DeleteRequest) mutation() Mutation {
	return Mutation{
		Kind:                MutationDelete,
		DriveID:             r.DriveID,
		ObjectID:            r.ObjectID,
		DestinationParentID: r.ParentID,
		ExpectedRevision:    r.ExpectedRevision,
		Recursive:           r.Recursive,
		TrashRetention:      r.TrashRetention,
	}
}

type StagedObject struct {
	Key    string            `json:"key"`
	Path   string            `json:"path"`
	Size   int64             `json:"size"`
	SHA256 [sha256.Size]byte `json:"sha256"`
}

type HiddenUpload struct {
	OperationID string
	DriveID     int64
	ParentID    string
	Name        string
	Size        int64
	SHA256      [sha256.Size]byte
	Encrypted   bool
}

type RemoteBody struct {
	ContentRef    string            `json:"content_ref,omitempty"`
	UploadUUID    string            `json:"upload_uuid,omitempty"`
	PartCount     int               `json:"part_count,omitempty"`
	PlaintextSize int64             `json:"plaintext_size"`
	StoredSize    int64             `json:"stored_size,omitempty"`
	Encrypted     bool              `json:"encrypted,omitempty"`
	SHA256        [sha256.Size]byte `json:"sha256"`
	MessageIDs    []int64           `json:"message_ids,omitempty"`
}

type CommitRequest struct {
	OperationID string
	Mutation    Mutation
	Body        *RemoteBody

	// CommitTime is the journaled operation timestamp. Adapters use it to
	// rebuild an identical idempotent control message after process restart.
	CommitTime time.Time

	// PersistCommitRef must be called synchronously after the remote visibility
	// commit is accepted and before any local projection work. Returning an
	// error tells the adapter to stop and report ErrCommitOutcomeUnknown.
	PersistCommitRef func(commitRef string) error
}

type MutationResult struct {
	OperationID string `json:"operation_id"`

	// CommitRef is an opaque, adapter-owned receipt for locating the exact
	// visibility commit after a crash. It is persisted only in the local
	// journal and is never part of the public mount status.
	CommitRef         string            `json:"commit_ref,omitempty"`
	ObjectID          string            `json:"object_id"`
	Revision          uint64            `json:"revision"`
	Created           bool              `json:"created"`
	Size              int64             `json:"size,omitempty"`
	SHA256            [sha256.Size]byte `json:"sha256,omitempty"`
	ProjectionPending bool              `json:"projection_pending,omitempty"`
}

type SnapshotInvalidation struct {
	OperationID string
	DriveID     int64
	ParentIDs   []string
	ObjectIDs   []string
}

type ReadSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}

type StagingStore interface {
	Stage(ctx context.Context, operationID string, contentLength, maxBytes int64, source io.Reader) (StagedObject, error)
	Open(staged StagedObject) (ReadSeekCloser, error)
	Remove(ctx context.Context, staged StagedObject) error
	RemoveOperation(ctx context.Context, operationID string) error
}

// Remote implements hidden content upload and the single visibility commit.
// UploadHidden must be idempotent for an OperationID. Commit may return
// ErrCommitOutcomeUnknown only when visibility is uncertain; a definite error
// promises that the mutation was not published. DiscardHidden must accept a
// nil body and clean deterministic upload artifacts using operationID alone.
type Remote interface {
	UploadHidden(ctx context.Context, request HiddenUpload, source io.ReadSeeker) (RemoteBody, error)
	Commit(ctx context.Context, request CommitRequest) (MutationResult, error)
	Reconcile(ctx context.Context, operationID string) (MutationResult, bool, error)
	DiscardHidden(ctx context.Context, operationID string, body *RemoteBody) error
}

// ReceiptReconciler is an optional Remote extension for adapters that can
// reconcile an exact durable commit receipt. Coordinators fall back to the
// operation-ID scan for journal records created by older adapters.
type ReceiptReconciler interface {
	ReconcileReceipt(ctx context.Context, request CommitRequest, commitRef string) (MutationResult, bool, error)
}

// SnapshotInvalidator invalidates exactly the parents and objects supplied
// after the corresponding remote commit is confirmed.
type SnapshotInvalidator interface {
	Invalidate(ctx context.Context, invalidation SnapshotInvalidation) error
}

type SnapshotInvalidatorFunc func(context.Context, SnapshotInvalidation) error

func (f SnapshotInvalidatorFunc) Invalidate(ctx context.Context, invalidation SnapshotInvalidation) error {
	return f(ctx, invalidation)
}

type IDGenerator interface {
	NewID() string
}

type Capabilities struct {
	Writable      bool
	PersonalOnly  bool
	PlaintextOnly bool
	OnlineOnly    bool
}

type Status struct {
	Accepting bool
	Active    int
	Executing int
	Queued    int
}

func ValidateTransition(from, to JournalState) error {
	if slices.Contains(allowedTransitions[from], to) {
		return nil
	}
	return ErrInvalidTransition
}

var allowedTransitions = map[JournalState][]JournalState{
	StateReceiving:         {StateStaged, StateCleanupPending, StateAborted},
	StateStaged:            {StateUploading, StateCommitting, StateCleanupPending, StateAborted},
	StateUploading:         {StateUploaded, StateCleanupPending, StateAborted},
	StateUploaded:          {StateCommitting, StateCleanupPending, StateAborted},
	StateCommitting:        {StateRemoteCommitted, StateReconciling, StateCleanupPending, StateAborted},
	StateReconciling:       {StateRemoteCommitted, StateCommitting, StateCleanupPending, StateAborted},
	StateRemoteCommitted:   {StateProjectionPending, StateCleanupPending, StateDone},
	StateProjectionPending: {StateCleanupPending, StateDone},
	StateCleanupPending:    {StateDone, StateAborted},
}

func validName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 240 || !utf8.ValidString(name) {
		return false
	}
	return !strings.ContainsAny(name, "/\\\x00")
}

func validOperationID(operationID string) bool {
	if operationID == "" {
		return true
	}
	if len(operationID) > 256 || !utf8.ValidString(operationID) {
		return false
	}
	for _, character := range operationID {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validCommitRef(commitRef string) bool {
	if commitRef == "" || len(commitRef) > 256 || !utf8.ValidString(commitRef) {
		return false
	}
	for _, character := range commitRef {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func sortedUnique(values []string, keepEmpty bool) []string {
	copyOfValues := append([]string(nil), values...)
	slices.Sort(copyOfValues)
	result := make([]string, 0, len(copyOfValues))
	for _, value := range copyOfValues {
		if (!keepEmpty && value == "") || (len(result) > 0 && result[len(result)-1] == value) {
			continue
		}
		result = append(result, value)
	}
	return result
}

func validateMutation(m Mutation) error {
	if m.DriveID == 0 {
		return ErrInvalidRequest
	}
	switch m.Kind {
	case MutationPut, MutationMkdir:
		if !validName(m.DestinationName) {
			return ErrInvalidRequest
		}
	case MutationMove:
		if m.ObjectID == "" || !validName(m.DestinationName) {
			return ErrInvalidRequest
		}
	case MutationDelete:
		if m.ObjectID == "" || m.TrashRetention < 0 {
			return ErrInvalidRequest
		}
	default:
		return ErrInvalidRequest
	}
	return nil
}

func isTerminal(state JournalState) bool {
	return state == StateDone || state == StateAborted
}

var _ = errors.Is
