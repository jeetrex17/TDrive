package projection

const (
	RootParent = ""

	FolderIDPrefix = "d:"
	FileIDPrefix   = "f:"
)

const (
	KindPersonal = "personal"
	KindShared   = "shared"

	ObjectKindFile   = "file"
	ObjectKindFolder = "folder"
)

type OpType string

const (
	OpFileUpload OpType = "f"
	OpMkdir      OpType = "mkdir"
	OpRename     OpType = "rename"
	OpMove       OpType = "move"
	OpRmdir      OpType = "rmdir"
	OpTomb       OpType = "tomb"
	OpMeta       OpType = "meta"
	OpEncConfig  OpType = "encfg"

	// Multipart large files. A file too big for one Telegram message is stored
	// as N OpFilePart document messages followed by one OpFileManifest text op
	// whose msg_id becomes the logical file's identity. Parts never enter the
	// files table, so they never surface as files or orphans.
	OpFilePart     OpType = "part"
	OpFileManifest OpType = "manifest"

	// Writable-mount operations are versioned, carry a durable operation id,
	// and project as one SQLite transaction. Telegram body messages stay
	// hidden; OpFileCommit is the visibility boundary for a new logical file.
	OpFileCommit   OpType = "fcommit"
	OpFileReplace  OpType = "freplace"
	OpFolderCommit OpType = "dcommit"
	OpRelocate     OpType = "relocate"
	OpTrashTree    OpType = "trash"
)

type Op struct {
	Type OpType

	// ProtocolVersion versions the payload of atomic writable operations. It
	// is intentionally independent of the outer TDX wire envelope.
	ProtocolVersion int
	// OpID is the durable idempotency key. Retries may be different Telegram
	// messages, but the same operation is applied at most once per channel.
	OpID string

	Obj    string
	Parent string
	Name   string

	// ExpectedRevision provides compare-and-swap semantics for mutations of
	// existing objects. New file commits always start at revision one.
	ExpectedRevision            int64
	ExpectedDestinationRevision int64

	// A committed file references either one hidden Telegram body message or
	// the existing UploadUUID/PartCount multipart representation.
	ContentMsgID int64
	ContentHash  string

	// Relocate may atomically replace one exact destination object. DeletedAt
	// is also used by TrashTree and is supplied on the wire for deterministic
	// replay (the projector never reads the local clock for domain state).
	Overwrite      bool
	DestinationObj string
	DeletedAt      int64
	PurgeAfter     int64
	RetainedUntil  int64

	FileSize       int64
	FileUploadTime int64

	// Encryption metadata. Set on file uploads when the personal drive's
	// encrypted mode is active. EncryptionVersion is reserved for future
	// crypto-format upgrades; v1 = 1.
	Encrypted         bool
	PlaintextSize     int64
	EncryptionVersion int

	// Multipart large-file metadata. UploadUUID groups the parts and manifest
	// of one logical file. PartIndex is the 0-based position of a part;
	// PartCount is the total number of parts (carried on the manifest).
	UploadUUID string
	PartIndex  int
	PartCount  int

	// Personal-drive encryption password metadata. This is safe to store in
	// Telegram because the master key stays wrapped under the user's password.
	KDFSalt          []byte
	KDFParamsJSON    string
	WrappedMasterKey []byte
	KeyCheck         []byte
	Hint             string
	ConfigVersion    int
}

type Channel struct {
	ChannelID            int64
	AccessHash           int64
	Title                string
	Kind                 string
	InviteLink           string
	JoinedAt             int64
	LastSyncedMsg        int64
	LastViewedMsg        int64
	HasUnseenContent     bool
	InitialSyncDone      bool
	PersonalBackfillDone bool
}

type Folder struct {
	ChannelID  int64
	ID         string
	Name       string
	ParentID   string
	Tombstoned bool
}

type Dirent struct {
	ChannelID   int64
	ObjectID    string
	ObjectKind  string
	ParentID    string
	DisplayName string
	NameKey     string
	Revision    int64
	Tombstoned  bool
}

type File struct {
	ChannelID         int64
	MsgID             int64
	Name              string
	Size              int64
	ParentID          string
	UploadTime        int64
	UploaderUserID    int64
	Tombstoned        bool
	Encrypted         bool
	PlaintextSize     int64
	EncryptionVersion int
	ContentMsgID      int64
	ContentHash       string
	Revision          int64
	UploadUUID        string
	PartCount         int
}

const (
	OperationApplied  = "applied"
	OperationRejected = "rejected"
)

// ProjectionOperation is the durable result of a versioned operation. It is
// useful to reconcile an uncertain Telegram send without applying it twice.
type ProjectionOperation struct {
	ChannelID int64
	OpID      string
	MsgID     int64
	OpType    OpType
	Outcome   string
	Error     string
}

// FileRevisionRef identifies an immutable Telegram-backed file body that is
// no longer among the retained latest revisions and may be physically purged.
type FileRevisionRef struct {
	ChannelID     int64
	FileMsgID     int64
	Revision      int64
	ContentMsgID  int64
	UploadUUID    string
	PartCount     int
	RetainedUntil int64
}

type ReplayLogRow struct {
	ChannelID     int64
	MsgID         int64
	OpType        OpType
	OpPayloadJSON string
	RawHeader     string
	FirstSeenHash string
	ActorUserID   int64
	SeenAt        int64
}

type PendingJoin struct {
	InviteHash    string
	InviteLink    string
	Title         string
	RequestedAt   int64
	LastCheckedAt int64
	Status        string
	LastError     string
}

func IsFolderID(id string) bool {
	return len(id) > len(FolderIDPrefix) && id[:len(FolderIDPrefix)] == FolderIDPrefix
}

func IsFileID(id string) bool {
	return len(id) > len(FileIDPrefix) && id[:len(FileIDPrefix)] == FileIDPrefix
}

func IsRoot(id string) bool {
	return id == RootParent
}
