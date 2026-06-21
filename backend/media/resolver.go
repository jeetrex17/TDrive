package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"TDrive/backend/projection"
)

// Resolver maps a projected files row to the ordered stored byte segments
// backing that logical file.
type Resolver struct {
	db *sql.DB
}

func NewResolver(db *sql.DB) *Resolver {
	return &Resolver{db: db}
}

func (r *Resolver) Resolve(ctx context.Context, channelID, fileID int64) (LogicalFile, error) {
	if r == nil || r.db == nil {
		return LogicalFile{}, ErrDBNotReady
	}
	if channelID <= 0 {
		return LogicalFile{}, fmt.Errorf("media: invalid channel id")
	}
	if fileID <= 0 {
		return LogicalFile{}, fmt.Errorf("media: invalid file id")
	}

	row, err := r.lookupFile(ctx, channelID, fileID)
	if err != nil {
		return LogicalFile{}, err
	}

	out := LogicalFile{
		ChannelID:         channelID,
		FileID:            fileID,
		Name:              row.name,
		StoredSize:        row.size,
		PlaintextSize:     plaintextSize(row.size, row.encrypted, row.plaintextSize),
		Encrypted:         row.encrypted,
		EncryptionVersion: row.encryptionVersion,
	}

	if row.uploadUUID == "" {
		out.Segments = []Segment{{MsgID: fileID, Size: row.size}}
		return out, nil
	}

	parts, err := projection.MultipartParts(r.db, channelID, fileID)
	if err != nil {
		return LogicalFile{}, fmt.Errorf("media: load multipart parts: %w", err)
	}
	if err := projection.MultipartComplete(r.db, channelID, fileID, parts); err != nil {
		return LogicalFile{}, fmt.Errorf("%w: %v", ErrIncompleteMultipart, err)
	}

	out.Multipart = true
	out.Segments = make([]Segment, 0, len(parts))
	for _, p := range parts {
		out.Segments = append(out.Segments, Segment{MsgID: p.MsgID, Size: p.Size})
	}
	return out, nil
}

type fileRow struct {
	name              string
	size              int64
	encrypted         bool
	plaintextSize     int64
	encryptionVersion int
	uploadUUID        string
}

func (r *Resolver) lookupFile(ctx context.Context, channelID, fileID int64) (fileRow, error) {
	var row fileRow
	var encrypted int
	err := r.db.QueryRowContext(ctx, `
		SELECT name, size, encrypted, plaintext_size, encryption_version, upload_uuid
		FROM files
		WHERE channel_id = ? AND msg_id = ? AND tombstoned = 0
	`, channelID, fileID).Scan(
		&row.name,
		&row.size,
		&encrypted,
		&row.plaintextSize,
		&row.encryptionVersion,
		&row.uploadUUID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return fileRow{}, ErrFileNotFound
	}
	if err != nil {
		return fileRow{}, fmt.Errorf("media: lookup file: %w", err)
	}
	row.encrypted = encrypted == 1
	return row, nil
}

func plaintextSize(storedSize int64, encrypted bool, projectedPlaintextSize int64) int64 {
	if encrypted {
		return projectedPlaintextSize
	}
	return storedSize
}
