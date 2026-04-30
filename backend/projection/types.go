package projection

const (
	RootParent = ""

	FolderIDPrefix = "d:"
	FileIDPrefix   = "f:"
)

const (
	KindPersonal = "personal"
	KindShared   = "shared"
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
)

type Op struct {
	Type OpType

	Obj    string
	Parent string
	Name   string

	FileSize       int64
	FileUploadTime int64

	// Encryption metadata. Set on file uploads when the personal drive's
	// encrypted mode is active. EncryptionVersion is reserved for future
	// crypto-format upgrades; v1 = 1.
	Encrypted         bool
	PlaintextSize     int64
	EncryptionVersion int
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
