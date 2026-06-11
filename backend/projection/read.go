package projection

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type FolderSlim struct {
	ID       string
	Name     string
	ParentID string
}

type FileSlim struct {
	MsgID         int64
	Name          string
	Size          int64
	ParentID      string
	UploadTime    int64
	UploaderID    int64
	Encrypted     bool
	PlaintextSize int64
}

type SearchHit struct {
	Type       string
	ID         string
	Name       string
	ParentID   string
	Size       int64
	Time       int64
	MsgID      int64
	UploaderID int64
}

func ListFolderContents(db *sql.DB, channelID int64, parentID string) ([]FolderSlim, []FileSlim, error) {
	folders, err := listChildFolders(db, channelID, parentID)
	if err != nil {
		return nil, nil, err
	}
	files, err := listChildFiles(db, channelID, parentID)
	if err != nil {
		return nil, nil, err
	}
	return folders, files, nil
}

func listChildFolders(db *sql.DB, channelID int64, parentID string) ([]FolderSlim, error) {
	rows, err := db.Query(`
		SELECT id, name, parent_id FROM folders
		WHERE channel_id = ? AND parent_id = ? AND tombstoned = 0
		ORDER BY name
	`, channelID, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FolderSlim
	for rows.Next() {
		var f FolderSlim
		if err := rows.Scan(&f.ID, &f.Name, &f.ParentID); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func listChildFiles(db *sql.DB, channelID int64, parentID string) ([]FileSlim, error) {
	rows, err := db.Query(`
		SELECT msg_id, name, size, parent_id, upload_time, uploader_user_id, encrypted, plaintext_size FROM files
		WHERE channel_id = ? AND parent_id = ? AND tombstoned = 0
		ORDER BY upload_time DESC
	`, channelID, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FileSlim
	for rows.Next() {
		var f FileSlim
		var enc int
		if err := rows.Scan(&f.MsgID, &f.Name, &f.Size, &f.ParentID, &f.UploadTime, &f.UploaderID, &enc, &f.PlaintextSize); err != nil {
			return nil, err
		}
		f.Encrypted = enc == 1
		out = append(out, f)
	}
	return out, rows.Err()
}

// OrphanedFiles returns files whose path back to root is broken — that
// is, any ancestor folder is tombstoned or missing. Root files
// (parent_id == "") are never orphans. Returned in upload-time-desc order.
//
// Implemented as a recursive CTE that walks each file's parent chain. A
// file is an orphan if any link in the chain points to a folder that's
// tombstoned or no longer exists. This catches the case where you delete
// folder A, and files several levels deeper inside A (e.g. A/B/C/file.txt)
// would otherwise look "fine" — their immediate parent C is alive, but
// the chain is broken at A.
//
// Step 4 makes DeleteFolder only emit rmdir, so files inside (at any depth)
// become orphans rather than being destroyed. Step 5 will let users move
// them out of the bucket.
func OrphanedFiles(db *sql.DB, channelID int64) ([]FileSlim, error) {
	const q = `
WITH RECURSIVE chain(file_msg_id, cur_id, broken) AS (
    -- Seed: every non-root, non-tombstoned file in this channel.
    SELECT f.msg_id, f.parent_id, 0
    FROM files f
    WHERE f.channel_id = ?1
      AND f.tombstoned = 0
      AND f.parent_id != ''

    UNION ALL

    -- Walk one step toward root. broken=1 if cur_id is tombstoned or
    -- doesn't exist; once broken, stay broken until we reach root.
    SELECT
      c.file_msg_id,
      COALESCE(p.parent_id, ''),
      CASE
        WHEN c.broken = 1 THEN 1
        WHEN p.id IS NULL THEN 1                  -- missing folder
        WHEN p.tombstoned = 1 THEN 1              -- tombstoned folder
        ELSE 0
      END
    FROM chain c
    LEFT JOIN folders p
      ON p.channel_id = ?1 AND p.id = c.cur_id
    WHERE c.cur_id != ''
)
SELECT f.msg_id, f.name, f.size, f.parent_id, f.upload_time, f.uploader_user_id
FROM files f
WHERE f.channel_id = ?1
  AND f.tombstoned = 0
  AND f.parent_id != ''
  AND f.msg_id IN (
    SELECT file_msg_id FROM chain WHERE cur_id = '' AND broken = 1
  )
ORDER BY f.upload_time DESC
`
	rows, err := db.Query(q, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FileSlim
	for rows.Next() {
		var f FileSlim
		if err := rows.Scan(&f.MsgID, &f.Name, &f.Size, &f.ParentID, &f.UploadTime, &f.UploaderID); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ListAllFiles returns every non-tombstoned file in the channel, newest
// first. Used by the gallery, which filters the result down to images in the
// service layer. Returns metadata only (no bodies), so it stays cheap even
// for large drives.
func ListAllFiles(db *sql.DB, channelID int64) ([]FileSlim, error) {
	// msg_id is the tiebreaker so the gallery order is stable across refreshes
	// when several files share an upload_time (batch uploads collide on the
	// second). msg_id is monotonic and unique per channel.
	rows, err := db.Query(`
		SELECT msg_id, name, size, parent_id, upload_time, uploader_user_id, encrypted, plaintext_size FROM files
		WHERE channel_id = ? AND tombstoned = 0
		ORDER BY upload_time DESC, msg_id DESC
	`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FileSlim
	for rows.Next() {
		var f FileSlim
		var enc int
		if err := rows.Scan(&f.MsgID, &f.Name, &f.Size, &f.ParentID, &f.UploadTime, &f.UploaderID, &enc, &f.PlaintextSize); err != nil {
			return nil, err
		}
		f.Encrypted = enc == 1
		out = append(out, f)
	}
	return out, rows.Err()
}

func ListAllFolders(db *sql.DB, channelID int64) ([]FolderSlim, error) {
	rows, err := db.Query(`
		SELECT id, name, parent_id FROM folders
		WHERE channel_id = ? AND tombstoned = 0
	`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FolderSlim
	for rows.Next() {
		var f FolderSlim
		if err := rows.Scan(&f.ID, &f.Name, &f.ParentID); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func Search(db *sql.DB, channelID int64, query string, limit int) ([]SearchHit, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 500 {
		limit = 500
	}
	pattern := "%" + query + "%"

	results := make([]SearchHit, 0, limit*2)

	folderRows, err := db.Query(`
		SELECT id, name, parent_id FROM folders
		WHERE channel_id = ? AND tombstoned = 0 AND name LIKE ? COLLATE NOCASE
		ORDER BY name LIMIT ?
	`, channelID, pattern, limit)
	if err != nil {
		return nil, err
	}
	for folderRows.Next() {
		var h SearchHit
		h.Type = "folder"
		if err := folderRows.Scan(&h.ID, &h.Name, &h.ParentID); err != nil {
			_ = folderRows.Close()
			return nil, err
		}
		results = append(results, h)
	}
	if err := folderRows.Err(); err != nil {
		_ = folderRows.Close()
		return nil, err
	}
	_ = folderRows.Close()

	fileRows, err := db.Query(`
		SELECT msg_id, name, size, parent_id, upload_time, uploader_user_id FROM files
		WHERE channel_id = ? AND tombstoned = 0 AND name LIKE ? COLLATE NOCASE
		ORDER BY upload_time DESC LIMIT ?
	`, channelID, pattern, limit)
	if err != nil {
		return nil, err
	}
	for fileRows.Next() {
		var h SearchHit
		h.Type = "file"
		if err := fileRows.Scan(&h.MsgID, &h.Name, &h.Size, &h.ParentID, &h.Time, &h.UploaderID); err != nil {
			_ = fileRows.Close()
			return nil, err
		}
		results = append(results, h)
	}
	if err := fileRows.Err(); err != nil {
		_ = fileRows.Close()
		return nil, err
	}
	_ = fileRows.Close()

	return results, nil
}

func AllFileMsgIDs(db *sql.DB, channelID int64) ([]int64, error) {
	rows, err := db.Query(`
		SELECT msg_id FROM files
		WHERE channel_id = ? AND tombstoned = 0
	`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]int64, 0, 256)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func StorageUsed(db *sql.DB, channelID int64) (int64, error) {
	var total int64
	err := db.QueryRow(`
		SELECT COALESCE(SUM(size), 0) FROM files
		WHERE channel_id = ? AND tombstoned = 0
	`, channelID).Scan(&total)
	return total, err
}

func FolderSize(db *sql.DB, channelID int64, folderID string) (int64, error) {
	folderID = strings.TrimSpace(folderID)
	if folderID == "" {
		return 0, fmt.Errorf("projection: invalid folder id")
	}
	if !FolderExists(db, channelID, folderID) {
		return 0, fmt.Errorf("projection: folder not found")
	}

	var total int64
	const q = `
WITH RECURSIVE descendants(id, path) AS (
    SELECT ?, ',' || ? || ','
    UNION ALL
    SELECT f.id, d.path || f.id || ','
    FROM folders f
    JOIN descendants d ON f.parent_id = d.id
    WHERE f.channel_id = ? AND f.tombstoned = 0 AND instr(d.path, ',' || f.id || ',') = 0
)
SELECT COALESCE(SUM(files.size), 0)
FROM files
JOIN descendants d ON files.parent_id = d.id
WHERE files.channel_id = ? AND files.tombstoned = 0;
`
	err := db.QueryRow(q, folderID, folderID, channelID, channelID).Scan(&total)
	return total, err
}

func FolderExists(db *sql.DB, channelID int64, folderID string) bool {
	var tmp int
	err := db.QueryRow(`
		SELECT 1 FROM folders
		WHERE id = ? AND channel_id = ? AND tombstoned = 0 LIMIT 1
	`, folderID, channelID).Scan(&tmp)
	return err == nil
}

func FileExists(db *sql.DB, channelID int64, msgID int64) bool {
	var tmp int
	err := db.QueryRow(`
		SELECT 1 FROM files
		WHERE channel_id = ? AND msg_id = ? AND tombstoned = 0 LIMIT 1
	`, channelID, msgID).Scan(&tmp)
	return err == nil
}

func FileParent(db *sql.DB, channelID int64, msgID int64) (string, error) {
	var parent string
	err := db.QueryRow(`
		SELECT parent_id FROM files
		WHERE channel_id = ? AND msg_id = ?
	`, channelID, msgID).Scan(&parent)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("projection: file not found")
	}
	return parent, err
}

// FileUploader returns the uploader_user_id stored for the file. Used by
// shared-drive delete gating: only the uploader (or, later, an admin) may
// tomb a shared-drive file.
//
// Returns 0 with nil error if uploader is unknown (legacy rows from before
// Step 3 don't carry this).
// FileEncryptionMeta returns the encryption flags for a file row. Returns
// (false, 0, 0, nil) for plaintext or unknown rows; the caller may treat
// those identically (no decrypt path).
func FileEncryptionMeta(db *sql.DB, channelID, msgID int64) (encrypted bool, plaintextSize int64, version int, err error) {
	var enc int
	row := db.QueryRow(`
		SELECT encrypted, plaintext_size, encryption_version FROM files
		WHERE channel_id = ? AND msg_id = ?
	`, channelID, msgID)
	if err = row.Scan(&enc, &plaintextSize, &version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, 0, 0, fmt.Errorf("projection: file not found")
		}
		return false, 0, 0, err
	}
	return enc == 1, plaintextSize, version, nil
}

func FileUploader(db *sql.DB, channelID int64, msgID int64) (int64, error) {
	var uploader int64
	err := db.QueryRow(`
		SELECT uploader_user_id FROM files
		WHERE channel_id = ? AND msg_id = ?
	`, channelID, msgID).Scan(&uploader)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("projection: file not found")
	}
	return uploader, err
}

func FolderParent(db *sql.DB, channelID int64, folderID string) (string, error) {
	var parent string
	err := db.QueryRow(`
		SELECT parent_id FROM folders
		WHERE id = ? AND channel_id = ?
	`, folderID, channelID).Scan(&parent)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("projection: folder not found")
	}
	return parent, err
}

func FolderSiblingHasName(db *sql.DB, channelID int64, parentID, name string) (bool, error) {
	var tmp int
	err := db.QueryRow(`
		SELECT 1 FROM folders
		WHERE channel_id = ? AND parent_id = ? AND name = ? AND tombstoned = 0
		LIMIT 1
	`, channelID, parentID, name).Scan(&tmp)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func LookupFileName(db *sql.DB, channelID int64, msgID int64) string {
	var name string
	err := db.QueryRow(`
		SELECT name FROM files
		WHERE channel_id = ? AND msg_id = ?
	`, channelID, msgID).Scan(&name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(name)
}

func CollectDescendants(db *sql.DB, channelID int64, folderID string) (folderIDs []string, msgIDs []int64, err error) {
	visited := make(map[string]bool)
	queue := []string{folderID}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == "" || visited[cur] {
			continue
		}
		visited[cur] = true
		folderIDs = append(folderIDs, cur)

		fileRows, err := db.Query(`
			SELECT msg_id FROM files
			WHERE channel_id = ? AND parent_id = ? AND tombstoned = 0
		`, channelID, cur)
		if err != nil {
			return nil, nil, err
		}
		for fileRows.Next() {
			var id int64
			if err := fileRows.Scan(&id); err != nil {
				_ = fileRows.Close()
				return nil, nil, err
			}
			msgIDs = append(msgIDs, id)
		}
		if err := fileRows.Err(); err != nil {
			_ = fileRows.Close()
			return nil, nil, err
		}
		_ = fileRows.Close()

		folderRows, err := db.Query(`
			SELECT id FROM folders
			WHERE channel_id = ? AND parent_id = ? AND tombstoned = 0
		`, channelID, cur)
		if err != nil {
			return nil, nil, err
		}
		for folderRows.Next() {
			var id string
			if err := folderRows.Scan(&id); err != nil {
				_ = folderRows.Close()
				return nil, nil, err
			}
			if id != "" && !visited[id] {
				queue = append(queue, id)
			}
		}
		if err := folderRows.Err(); err != nil {
			_ = folderRows.Close()
			return nil, nil, err
		}
		_ = folderRows.Close()
	}

	return folderIDs, msgIDs, nil
}

func IsAncestor(db *sql.DB, channelID int64, ancestor, candidate string) (bool, error) {
	cur := candidate
	visited := make(map[string]bool)
	for cur != RootParent {
		if cur == ancestor {
			return true, nil
		}
		if visited[cur] {
			return false, nil
		}
		visited[cur] = true

		next, err := FolderParent(db, channelID, cur)
		if err != nil {
			return false, nil
		}
		cur = next
	}
	return false, nil
}
