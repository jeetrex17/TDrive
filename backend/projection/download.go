package projection

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
)

var (
	// ErrInvalidFolderDownload identifies an invalid folder snapshot request.
	ErrInvalidFolderDownload = errors.New("projection: invalid folder download")
	// ErrFolderDownloadNotFound identifies a folder that is not live in the
	// authoritative portable namespace.
	ErrFolderDownloadNotFound = errors.New("projection: folder download not found")
	// ErrInvalidFileDownload identifies an invalid logical file lookup.
	ErrInvalidFileDownload = errors.New("projection: invalid file download")
)

// DownloadDirectory is one live portable directory in a folder-download
// snapshot. Depth is relative to the requested root, which is always zero.
type DownloadDirectory struct {
	ID       string
	Name     string
	ParentID string
	Revision int64
	Depth    int
}

// DownloadFile pins the immutable Telegram-backed body for one logical file
// revision. ContentMsgID is used for a single-message body; Parts is populated
// for multipart bodies. Legacy files may have ContentMsgID zero and fall back
// to LogicalMsgID.
type DownloadFile struct {
	LogicalMsgID      int64
	Name              string
	ParentID          string
	Revision          int64
	ContentMsgID      int64
	ContentHash       string
	UploadUUID        string
	PartCount         int
	Parts             []FilePart
	StoredSize        int64
	OutputSize        int64
	Encrypted         bool
	EncryptionVersion int
}

// FolderDownloadManifest is a short-lived, revision-pinned projection
// snapshot. The SQLite read transaction is closed before callers perform any
// network or filesystem work.
type FolderDownloadManifest struct {
	Root             DownloadDirectory
	Folders          []DownloadDirectory
	Files            []DownloadFile
	TotalStoredBytes int64
	TotalOutputBytes int64
}

// BuildFolderDownloadManifestContext materializes a deterministic, portable
// subtree snapshot. All reads share one transaction so concurrent projection
// updates cannot mix revisions or namespace states in one download job.
func BuildFolderDownloadManifestContext(
	ctx context.Context,
	db *sql.DB,
	channelID int64,
	folderID string,
) (FolderDownloadManifest, error) {
	if err := validateContext(ctx, "build folder download manifest"); err != nil {
		return FolderDownloadManifest{}, err
	}
	if db == nil {
		return FolderDownloadManifest{}, fmt.Errorf("%w: db is nil", ErrInvalidFolderDownload)
	}
	folderID = strings.TrimSpace(folderID)
	if channelID <= 0 || !IsFolderID(folderID) {
		return FolderDownloadManifest{}, ErrInvalidFolderDownload
	}

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return FolderDownloadManifest{}, fmt.Errorf("projection: begin folder download snapshot: %w", err)
	}
	defer tx.Rollback()

	folders, err := queryDownloadDirectories(ctx, tx, channelID, folderID)
	if err != nil {
		return FolderDownloadManifest{}, err
	}
	if len(folders) == 0 || folders[0].ID != folderID {
		return FolderDownloadManifest{}, ErrFolderDownloadNotFound
	}
	if err := validateDownloadDirectories(folders); err != nil {
		return FolderDownloadManifest{}, err
	}

	files, err := queryDownloadFiles(ctx, tx, channelID, folderID)
	if err != nil {
		return FolderDownloadManifest{}, err
	}
	partsByFile, err := queryDownloadParts(ctx, tx, channelID, folderID)
	if err != nil {
		return FolderDownloadManifest{}, err
	}

	var totalStored, totalOutput int64
	for i := range files {
		files[i].Parts = append([]FilePart(nil), partsByFile[files[i].LogicalMsgID]...)
		if err := validateDownloadFile(files[i]); err != nil {
			return FolderDownloadManifest{}, err
		}
		totalStored, err = checkedDownloadSize(totalStored, files[i].StoredSize)
		if err != nil {
			return FolderDownloadManifest{}, err
		}
		totalOutput, err = checkedDownloadSize(totalOutput, files[i].OutputSize)
		if err != nil {
			return FolderDownloadManifest{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return FolderDownloadManifest{}, fmt.Errorf("projection: commit folder download snapshot: %w", err)
	}
	return FolderDownloadManifest{
		Root:             folders[0],
		Folders:          folders,
		Files:            files,
		TotalStoredBytes: totalStored,
		TotalOutputBytes: totalOutput,
	}, nil
}

// FileDownloadRefContext pins the current body reference and portable name of
// one live logical file. It is shared by the existing single-file downloader so
// writable-mount replacements fetch ContentMsgID rather than a stale logical
// control message.
func FileDownloadRefContext(ctx context.Context, db *sql.DB, channelID, logicalMsgID int64) (DownloadFile, bool, error) {
	if err := validateContext(ctx, "resolve file download reference"); err != nil {
		return DownloadFile{}, false, err
	}
	if db == nil || channelID <= 0 || logicalMsgID <= 0 {
		return DownloadFile{}, false, ErrInvalidFileDownload
	}

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return DownloadFile{}, false, fmt.Errorf("projection: begin file download snapshot: %w", err)
	}
	defer tx.Rollback()

	file, found, err := queryDownloadFileByID(ctx, tx, channelID, logicalMsgID)
	if err != nil || !found {
		return DownloadFile{}, found, err
	}
	if file.UploadUUID != "" {
		file.Parts, err = queryPartsForDownloadFile(ctx, tx, channelID, file.UploadUUID)
		if err != nil {
			return DownloadFile{}, false, err
		}
	}
	if err := validateDownloadFile(file); err != nil {
		return DownloadFile{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return DownloadFile{}, false, fmt.Errorf("projection: commit file download snapshot: %w", err)
	}
	return file, true, nil
}

const downloadTreeCTE = `
WITH RECURSIVE download_tree(id, parent_id, display_name, revision, depth, ancestry) AS (
    SELECT d.object_id, d.parent_id, d.display_name, d.revision, 0,
           ',' || d.object_id || ','
    FROM dirents d
    JOIN folders f
      ON f.channel_id=d.channel_id AND f.id=d.object_id
     AND f.parent_id=d.parent_id AND f.tombstoned=0
    WHERE d.channel_id=?1 AND d.object_id=?2
      AND d.object_kind='folder' AND d.tombstoned=0

    UNION ALL

    SELECT d.object_id, d.parent_id, d.display_name, d.revision, tree.depth + 1,
           tree.ancestry || d.object_id || ','
    FROM download_tree tree
    JOIN dirents d
      ON d.channel_id=?1 AND d.parent_id=tree.id
     AND d.object_kind='folder' AND d.tombstoned=0
    JOIN folders f
      ON f.channel_id=d.channel_id AND f.id=d.object_id
     AND f.parent_id=d.parent_id AND f.tombstoned=0
    WHERE instr(tree.ancestry, ',' || d.object_id || ',')=0
)
`

func queryDownloadDirectories(ctx context.Context, tx *sql.Tx, channelID int64, folderID string) ([]DownloadDirectory, error) {
	rows, err := tx.QueryContext(ctx, downloadTreeCTE+`
		SELECT id, display_name, parent_id, revision, depth
		FROM download_tree
		ORDER BY depth ASC, display_name COLLATE NOCASE, id
	`, channelID, folderID)
	if err != nil {
		return nil, fmt.Errorf("projection: list folder download directories: %w", err)
	}
	defer rows.Close()

	folders := make([]DownloadDirectory, 0)
	for rows.Next() {
		var folder DownloadDirectory
		if err := rows.Scan(&folder.ID, &folder.Name, &folder.ParentID, &folder.Revision, &folder.Depth); err != nil {
			return nil, fmt.Errorf("projection: scan folder download directory: %w", err)
		}
		folders = append(folders, folder)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("projection: iterate folder download directories: %w", err)
	}
	return folders, nil
}

func queryDownloadFiles(ctx context.Context, tx *sql.Tx, channelID int64, folderID string) ([]DownloadFile, error) {
	rows, err := tx.QueryContext(ctx, downloadTreeCTE+`
		SELECT f.msg_id, d.display_name, d.parent_id, f.revision,
		       f.content_msg_id, f.content_hash, f.upload_uuid, f.part_count,
		       f.size, f.plaintext_size, f.encrypted, f.encryption_version,
		       CASE WHEN r.revision IS NULL THEN 0 ELSE 1 END
		FROM download_tree tree
		JOIN dirents d
		  ON d.channel_id=?1 AND d.parent_id=tree.id
		 AND d.object_kind='file' AND d.tombstoned=0
		JOIN files f
		  ON f.channel_id=d.channel_id AND 'f:' || f.msg_id=d.object_id
		 AND f.parent_id=d.parent_id AND f.revision=d.revision AND f.tombstoned=0
		LEFT JOIN file_revisions r
		  ON r.channel_id=f.channel_id AND r.file_msg_id=f.msg_id
		 AND r.revision=f.revision
		ORDER BY tree.depth ASC, d.display_name COLLATE NOCASE, f.msg_id
	`, channelID, folderID)
	if err != nil {
		return nil, fmt.Errorf("projection: list folder download files: %w", err)
	}
	defer rows.Close()

	files := make([]DownloadFile, 0)
	for rows.Next() {
		file, revisionFound, err := scanDownloadFile(rows)
		if err != nil {
			return nil, fmt.Errorf("projection: scan folder download file: %w", err)
		}
		if !revisionFound {
			return nil, fmt.Errorf("projection: file %d current revision %d is missing", file.LogicalMsgID, file.Revision)
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("projection: iterate folder download files: %w", err)
	}
	return files, nil
}

func queryDownloadFileByID(ctx context.Context, tx *sql.Tx, channelID, logicalMsgID int64) (DownloadFile, bool, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT f.msg_id, d.display_name, d.parent_id, f.revision,
		       f.content_msg_id, f.content_hash, f.upload_uuid, f.part_count,
		       f.size, f.plaintext_size, f.encrypted, f.encryption_version,
		       CASE WHEN r.revision IS NULL THEN 0 ELSE 1 END
		FROM files f
		JOIN dirents d
		  ON d.channel_id=f.channel_id AND d.object_id='f:' || f.msg_id
		 AND d.parent_id=f.parent_id AND d.revision=f.revision
		 AND d.object_kind='file' AND d.tombstoned=0
		LEFT JOIN file_revisions r
		  ON r.channel_id=f.channel_id AND r.file_msg_id=f.msg_id
		 AND r.revision=f.revision
		WHERE f.channel_id=? AND f.msg_id=? AND f.tombstoned=0
	`, channelID, logicalMsgID)
	file, revisionFound, err := scanDownloadFile(row)
	if errors.Is(err, sql.ErrNoRows) {
		return DownloadFile{}, false, nil
	}
	if err != nil {
		return DownloadFile{}, false, fmt.Errorf("projection: resolve file download reference: %w", err)
	}
	if !revisionFound {
		return DownloadFile{}, false, fmt.Errorf("projection: file %d current revision %d is missing", file.LogicalMsgID, file.Revision)
	}
	return file, true, nil
}

type downloadFileScanner interface {
	Scan(dest ...any) error
}

func scanDownloadFile(scanner downloadFileScanner) (DownloadFile, bool, error) {
	var file DownloadFile
	var encrypted, revisionFound int
	err := scanner.Scan(
		&file.LogicalMsgID, &file.Name, &file.ParentID, &file.Revision,
		&file.ContentMsgID, &file.ContentHash, &file.UploadUUID, &file.PartCount,
		&file.StoredSize, &file.OutputSize, &encrypted, &file.EncryptionVersion,
		&revisionFound,
	)
	file.Encrypted = encrypted != 0
	if !file.Encrypted {
		file.OutputSize = file.StoredSize
	}
	return file, revisionFound != 0, err
}

func queryDownloadParts(ctx context.Context, tx *sql.Tx, channelID int64, folderID string) (map[int64][]FilePart, error) {
	rows, err := tx.QueryContext(ctx, downloadTreeCTE+`
		SELECT f.msg_id, p.part_index, p.msg_id, p.size
		FROM download_tree tree
		JOIN files f
		  ON f.channel_id=?1 AND f.parent_id=tree.id AND f.tombstoned=0
		JOIN dirents d
		  ON d.channel_id=f.channel_id AND d.object_id='f:' || f.msg_id
		 AND d.parent_id=f.parent_id AND d.revision=f.revision
		 AND d.object_kind='file' AND d.tombstoned=0
		JOIN file_parts p
		  ON p.channel_id=f.channel_id AND p.upload_uuid=f.upload_uuid
		WHERE f.upload_uuid!=''
		ORDER BY f.msg_id, p.part_index
	`, channelID, folderID)
	if err != nil {
		return nil, fmt.Errorf("projection: list folder download parts: %w", err)
	}
	defer rows.Close()

	parts := make(map[int64][]FilePart)
	for rows.Next() {
		var logicalMsgID int64
		var part FilePart
		if err := rows.Scan(&logicalMsgID, &part.PartIndex, &part.MsgID, &part.Size); err != nil {
			return nil, fmt.Errorf("projection: scan folder download part: %w", err)
		}
		parts[logicalMsgID] = append(parts[logicalMsgID], part)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("projection: iterate folder download parts: %w", err)
	}
	return parts, nil
}

func queryPartsForDownloadFile(ctx context.Context, tx *sql.Tx, channelID int64, uploadUUID string) ([]FilePart, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT part_index, msg_id, size
		FROM file_parts
		WHERE channel_id=? AND upload_uuid=?
		ORDER BY part_index
	`, channelID, uploadUUID)
	if err != nil {
		return nil, fmt.Errorf("projection: list file download parts: %w", err)
	}
	defer rows.Close()

	parts := make([]FilePart, 0)
	for rows.Next() {
		var part FilePart
		if err := rows.Scan(&part.PartIndex, &part.MsgID, &part.Size); err != nil {
			return nil, fmt.Errorf("projection: scan file download part: %w", err)
		}
		parts = append(parts, part)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("projection: iterate file download parts: %w", err)
	}
	return parts, nil
}

func validateDownloadDirectories(folders []DownloadDirectory) error {
	seen := make(map[string]struct{}, len(folders))
	for i, folder := range folders {
		if !IsFolderID(folder.ID) || folder.Depth < 0 {
			return fmt.Errorf("%w: invalid directory identity", ErrInvalidFolderDownload)
		}
		if _, duplicate := seen[folder.ID]; duplicate {
			return fmt.Errorf("%w: duplicate directory %s", ErrInvalidFolderDownload, folder.ID)
		}
		if _, err := CanonicalNameKey(folder.Name); err != nil {
			return fmt.Errorf("projection: folder download directory %s: %w", folder.ID, err)
		}
		if i > 0 {
			if _, parentFound := seen[folder.ParentID]; !parentFound {
				return fmt.Errorf("%w: directory %s has missing parent %s", ErrInvalidFolderDownload, folder.ID, folder.ParentID)
			}
		}
		seen[folder.ID] = struct{}{}
	}
	// The recursive query suppresses revisits so a corrupt graph always
	// terminates. A reachable cycle can only close through this requested root
	// because each live dirent has one parent; reject it instead of silently
	// materializing a misleading partial tree.
	if len(folders) > 0 {
		if _, cyclic := seen[folders[0].ParentID]; cyclic {
			return fmt.Errorf("%w: directory cycle reaches root %s", ErrInvalidFolderDownload, folders[0].ID)
		}
	}
	return nil
}

func validateDownloadFile(file DownloadFile) error {
	if file.LogicalMsgID <= 0 || file.Revision <= 0 || file.StoredSize < 0 || file.OutputSize < 0 {
		return fmt.Errorf("%w: invalid file metadata for %d", ErrInvalidFileDownload, file.LogicalMsgID)
	}
	if _, err := CanonicalNameKey(file.Name); err != nil {
		return fmt.Errorf("projection: file download %d: %w", file.LogicalMsgID, err)
	}
	if file.UploadUUID == "" {
		if file.PartCount != 0 || len(file.Parts) != 0 {
			return fmt.Errorf("%w: single-message file %d has multipart metadata", ErrInvalidFileDownload, file.LogicalMsgID)
		}
		return nil
	}
	if file.ContentMsgID != 0 || file.PartCount <= 0 || len(file.Parts) != file.PartCount {
		return fmt.Errorf("%w: multipart file %d is incomplete", ErrInvalidFileDownload, file.LogicalMsgID)
	}
	var total int64
	for i, part := range file.Parts {
		if part.PartIndex != i || part.MsgID <= 0 || part.Size < 0 || part.Size > file.StoredSize-total {
			return fmt.Errorf("%w: multipart file %d has invalid part %d", ErrInvalidFileDownload, file.LogicalMsgID, i)
		}
		total += part.Size
	}
	if total != file.StoredSize {
		return fmt.Errorf("%w: multipart file %d parts total %d, want %d", ErrInvalidFileDownload, file.LogicalMsgID, total, file.StoredSize)
	}
	return nil
}

func checkedDownloadSize(total, size int64) (int64, error) {
	if size < 0 || total < 0 || size > math.MaxInt64-total {
		return 0, fmt.Errorf("%w: aggregate size overflow", ErrInvalidFolderDownload)
	}
	return total + size, nil
}
