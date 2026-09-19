// Package backend holds the two things nearly every other package depends on
// and nothing else: the JSON structs that cross the Wails boundary to the
// frontend, and the process-wide SQLite handle.
//
// It is small because it is a dependency sink. Anything with behaviour placed
// here would have to import core, projection or daemon and would close an
// import cycle, which is why the schema and migration logic live in
// backend/projection and only the globals live here.
//
// The connection pool is pinned to exactly one connection, so every SQLite
// access in the process is serialized at the driver level; the daemon's write
// lock sits on top of that to keep the Telegram-send plus projection sequence
// single-writer. PRAGMAs are applied to that one connection and are hard
// failures, with the exception of mmap_size, which is best-effort so startup
// never depends on mmap support.
//
// Ensuring the schema deliberately does not create the files and folders
// tables; those come from the personal-channel migration, so callers must run
// it as soon as the personal channel id is known. The data directory is
// resolved when the database is opened, so a mobile caller must set it first.
package backend

type Folder struct {
	Name     string `json:"name"`
	ID       string `json:"id"`
	ParentID string `json:"parent_id"`
}

type FileMetaData struct {
	Name          string `json:"name"`
	Size          int64  `json:"size"`
	TgMsgID       int    `json:"msg_id"`
	ParentID      string `json:"parent_id"`
	UploadTime    int64  `json:"upload_time"`
	UploaderID    int64  `json:"uploader_id"`
	Encrypted     bool   `json:"encrypted,omitempty"`
	PlaintextSize int64  `json:"plaintext_size,omitempty"`
	Revision      int64  `json:"revision,omitempty"`
}

type FileSystem struct {
	Folders []Folder       `json:"folders"`
	Files   []FileMetaData `json:"files"`
}

type SearchResult struct {
	Type          string `json:"type"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	ParentID      string `json:"parent_id"`
	Size          int64  `json:"size"`
	UploadTime    int64  `json:"upload_time"`
	UploaderID    int64  `json:"uploader_id"`
	Encrypted     bool   `json:"encrypted,omitempty"`
	PlaintextSize int64  `json:"plaintext_size,omitempty"`
	Path          string `json:"path"`
}
