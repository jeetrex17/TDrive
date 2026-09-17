package projection

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	RenditionThumbnail       = "thumbnail"
	RenditionPreview         = "preview"
	RenditionVersion         = 1
	MaxRenditionBytes  int64 = 4 << 20
)

// FileRendition binds one immutable image derivative to its source bytes. A
// rename advances the namespace revision but preserves this identity; replacing
// content makes the old derivative ineligible without destructive cache edits.
// ChannelID is included inside encrypted envelopes to prevent cross-drive swaps.
type FileRendition struct {
	ChannelID     int64  `json:"c"`
	MsgID         int64  `json:"-"`
	FileMsgID     int64  `json:"f"`
	ContentMsgID  int64  `json:"m,omitempty"`
	UploadUUID    string `json:"u,omitempty"`
	Kind          string `json:"k"`
	Version       int    `json:"v"`
	Size          int64  `json:"s"`
	PlaintextSize int64  `json:"p"`
	Width         int    `json:"w"`
	Height        int    `json:"h"`
	Encrypted     bool   `json:"e,omitempty"`
}

// Validate checks only portable descriptor invariants. Projection additionally
// checks the channel and read queries join the current parent content/encryption.
func (r FileRendition) Validate() error {
	maxEdge := 512
	maxBytes := int64(1 << 20)
	if r.Kind == RenditionPreview {
		maxEdge = 1600
		maxBytes = MaxRenditionBytes
	}
	if r.ChannelID == 0 || r.FileMsgID <= 0 || r.Version != RenditionVersion ||
		(r.Kind != RenditionThumbnail && r.Kind != RenditionPreview) ||
		r.Width <= 0 || r.Height <= 0 || r.Width > maxEdge || r.Height > maxEdge ||
		r.Size <= 0 || r.Size > maxBytes || r.PlaintextSize <= 0 || r.PlaintextSize > maxBytes ||
		r.ContentMsgID < 0 || (r.ContentMsgID == 0) == (r.UploadUUID == "") || len(r.UploadUUID) > 128 {
		return fmt.Errorf("%w: invalid image rendition descriptor", ErrBadOp)
	}
	return nil
}

// EnsureRenditionSchema adds replayed references and a bounded local outbox.
// Outbox bytes are already encrypted for encrypted drives. Keeping the exact
// payload and random ID makes retries after process death safe and idempotent.
func EnsureRenditionSchema(db *sql.DB) error {
	for _, query := range []string{
		`CREATE TABLE IF NOT EXISTS file_renditions (
   channel_id INTEGER NOT NULL, msg_id INTEGER NOT NULL, file_msg_id INTEGER NOT NULL,
   content_msg_id INTEGER NOT NULL DEFAULT 0, upload_uuid TEXT NOT NULL DEFAULT '',
   kind TEXT NOT NULL, version INTEGER NOT NULL, size INTEGER NOT NULL,
   plaintext_size INTEGER NOT NULL, width INTEGER NOT NULL, height INTEGER NOT NULL,
   encrypted INTEGER NOT NULL, actor_user_id INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(channel_id,msg_id))`,
		`CREATE INDEX IF NOT EXISTS idx_renditions_content ON file_renditions
   (channel_id,file_msg_id,content_msg_id,upload_uuid,kind,version,msg_id)`,
		`CREATE TABLE IF NOT EXISTS unavailable_renditions (
		 channel_id INTEGER NOT NULL, msg_id INTEGER NOT NULL, PRIMARY KEY(channel_id,msg_id))`,
		`CREATE TABLE IF NOT EXISTS pending_rendition_uploads (
   channel_id INTEGER NOT NULL, job_id TEXT NOT NULL, random_id INTEGER NOT NULL,
   header TEXT NOT NULL, payload BLOB NOT NULL, created_at INTEGER NOT NULL,
   PRIMARY KEY(channel_id,job_id))`,
	} {
		if _, err := db.Exec(query); err != nil {
			return fmt.Errorf("projection: ensure renditions: %w", err)
		}
	}
	return nil
}

func parseRendition(value string) (*FileRendition, error) {
	if len(value) > 2048 {
		return nil, ErrWireMalformed
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, ErrWireMalformed
	}
	var r FileRendition
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, ErrWireMalformed
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return &r, nil
}
func formatRendition(r FileRendition) string {
	raw, _ := json.Marshal(r)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func applyRendition(tx *sql.Tx, channelID, msgID int64, op Op, actorID int64) error {
	r := op.Rendition
	if r == nil {
		return nil
	}
	if err := r.Validate(); err != nil {
		return err
	}
	if r.ChannelID != channelID || r.FileMsgID >= msgID || r.ContentMsgID >= msgID || op.PartIndex != 0 || op.FileSize != r.Size {
		return fmt.Errorf("%w: invalid rendition source binding", ErrBadOp)
	}
	var kind string
	if err := tx.QueryRow(`SELECT kind FROM channels WHERE channel_id=?`, channelID).Scan(&kind); err != nil {
		return err
	}
	if kind == KindShared {
		var uploader int64
		err := tx.QueryRow(`SELECT uploader_user_id FROM files WHERE channel_id=? AND msg_id=?`, channelID, r.FileMsgID).Scan(&uploader)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if actorID <= 0 || (err == nil && (uploader == 0 || uploader != actorID)) {
			return fmt.Errorf("%w: rendition must be published by the shared-file uploader", ErrBadOp)
		}
	}
	// Keep duplicate receipts too: first message wins reads, while hard deletion
	// knows every blob produced by two clients preparing the same photo.
	_, err := tx.Exec(`INSERT OR IGNORE INTO file_renditions
  (channel_id,msg_id,file_msg_id,content_msg_id,upload_uuid,kind,version,size,plaintext_size,width,height,encrypted,actor_user_id)
  VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, channelID, msgID, r.FileMsgID, r.ContentMsgID, r.UploadUUID, r.Kind, r.Version, r.Size, r.PlaintextSize, r.Width, r.Height, r.Encrypted, actorID)
	return err
}

// CurrentFileRendition returns only the current live content's matching image.
// It never follows an arbitrary message reference supplied by the frontend.
// sql.ErrNoRows means that a small rendition is not yet available.
func CurrentFileRendition(ctx context.Context, db *sql.DB, channelID, fileMsgID int64, kind string) (FileRendition, error) {
	var r FileRendition
	if ctx == nil || channelID == 0 || fileMsgID <= 0 || (kind != RenditionThumbnail && kind != RenditionPreview) {
		return r, fmt.Errorf("projection: invalid rendition lookup")
	}
	err := db.QueryRowContext(ctx, `SELECT r.channel_id,r.msg_id,r.file_msg_id,r.content_msg_id,r.upload_uuid,r.kind,r.version,r.size,r.plaintext_size,r.width,r.height,r.encrypted
  FROM file_renditions r JOIN files f ON f.channel_id=r.channel_id AND f.msg_id=r.file_msg_id
  WHERE r.channel_id=? AND r.file_msg_id=? AND r.kind=? AND r.version=1 AND f.tombstoned=0
   AND r.encrypted=f.encrypted AND r.upload_uuid=f.upload_uuid
   AND NOT EXISTS (SELECT 1 FROM unavailable_renditions missing WHERE missing.channel_id=r.channel_id AND missing.msg_id=r.msg_id)
   AND (EXISTS(SELECT 1 FROM channels c WHERE c.channel_id=r.channel_id AND c.kind='personal') OR (r.actor_user_id>0 AND r.actor_user_id=f.uploader_user_id))
   AND r.content_msg_id=CASE WHEN f.upload_uuid!='' THEN 0 ELSE COALESCE(NULLIF(f.content_msg_id,0),f.msg_id) END
  ORDER BY r.msg_id ASC LIMIT 1`, channelID, fileMsgID, kind).Scan(&r.ChannelID, &r.MsgID, &r.FileMsgID, &r.ContentMsgID, &r.UploadUUID, &r.Kind, &r.Version, &r.Size, &r.PlaintextSize, &r.Width, &r.Height, &r.Encrypted)
	return r, err
}

// RenditionMessageIDsForFiles includes obsolete revisions and duplicate remote
// receipts. Call before deleting the parent rows or their durable history.
func RenditionMessageIDsForFiles(db *sql.DB, channelID int64, fileIDs []int64) ([]int64, error) {
	var out []int64
	for start := 0; start < len(fileIDs); start += 500 {
		batch := fileIDs[start:min(start+500, len(fileIDs))]
		marks := make([]string, len(batch))
		args := []any{channelID}
		for i, id := range batch {
			marks[i] = "?"
			args = append(args, id)
		}
		rows, err := db.Query(`SELECT r.msg_id FROM file_renditions r JOIN files f ON f.channel_id=r.channel_id AND f.msg_id=r.file_msg_id
		WHERE r.channel_id=? AND r.file_msg_id IN (`+strings.Join(marks, ",")+`)
		AND (EXISTS(SELECT 1 FROM channels c WHERE c.channel_id=r.channel_id AND c.kind='personal') OR (r.actor_user_id>0 AND r.actor_user_id=f.uploader_user_id)) ORDER BY r.msg_id`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// InvalidateRendition quarantines a definitively missing or malformed receipt
// locally. It is not a remote mutation, survives projection rebuild, and lets a
// later prepared blob replace a corrupt first writer without altering history.
// Do not call it for cancellation, offline/network errors or a locked vault.
func InvalidateRendition(ctx context.Context, db *sql.DB, channelID, msgID int64) error {
	if ctx == nil || channelID == 0 || msgID <= 0 {
		return fmt.Errorf("projection: invalid rendition quarantine")
	}
	_, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO unavailable_renditions(channel_id,msg_id) SELECT channel_id,msg_id FROM file_renditions WHERE channel_id=? AND msg_id=?`, channelID, msgID)
	return err
}
