package projection

import (
	"context"
	"database/sql"
	"fmt"
)

const GalleryPreparationMaxSourceBytes int64 = 30 << 20

// An omitted actor preserves the internal projection-only reader. Application
// callers always pass an authenticated actor, limiting shared-drive candidates
// to files that actor owns; the mutation service rechecks before every transfer.
const galleryPreparationVisibility = ` AND (?=0 OR EXISTS(SELECT 1 FROM channels c WHERE c.channel_id=f.channel_id AND c.kind='personal') OR f.uploader_user_id=?)`

func galleryPreparationActor(actors []int64) (int64, error) {
	if len(actors) > 1 || (len(actors) == 1 && actors[0] <= 0) {
		return 0, fmt.Errorf("projection: invalid preparation actor")
	}
	if len(actors) == 1 {
		return actors[0], nil
	}
	return 0, nil
}

type GalleryPreparationEstimate struct {
	Total      int   `json:"total"`
	BytesTotal int64 `json:"bytes_total"`
}

// Match the current body and encryption state, including legacy rows that use
// the logical message as their body. Obsolete revisions cannot mark a file
// ready, and duplicate receipts for one kind cannot stand in for the other.
const galleryPreparationMissing = ` AND f.size>0 AND f.size<=?
 AND NOT EXISTS(SELECT 1 FROM gallery_preparation_skips skip WHERE skip.channel_id=f.channel_id
  AND skip.file_msg_id=f.msg_id AND skip.content_msg_id=COALESCE(NULLIF(f.content_msg_id,0),f.msg_id)
  AND skip.content_hash=f.content_hash AND skip.generator_version=1 AND skip.decoder_profile=?) AND (
 NOT EXISTS (SELECT 1 FROM file_renditions r WHERE
  r.channel_id=f.channel_id AND r.file_msg_id=f.msg_id AND r.kind='thumbnail'
  AND r.version=1 AND r.upload_uuid=f.upload_uuid AND r.encrypted=f.encrypted
  AND NOT EXISTS(SELECT 1 FROM unavailable_renditions missing WHERE missing.channel_id=r.channel_id AND missing.msg_id=r.msg_id)
  AND (EXISTS(SELECT 1 FROM channels c WHERE c.channel_id=r.channel_id AND c.kind='personal') OR (r.actor_user_id>0 AND r.actor_user_id=f.uploader_user_id))
  AND r.content_msg_id=COALESCE(NULLIF(f.content_msg_id,0),f.msg_id))
 OR NOT EXISTS (SELECT 1 FROM file_renditions r WHERE
  r.channel_id=f.channel_id AND r.file_msg_id=f.msg_id AND r.kind='preview'
  AND r.version=1 AND r.upload_uuid=f.upload_uuid AND r.encrypted=f.encrypted
  AND NOT EXISTS(SELECT 1 FROM unavailable_renditions missing WHERE missing.channel_id=r.channel_id AND missing.msg_id=r.msg_id)
  AND (EXISTS(SELECT 1 FROM channels c WHERE c.channel_id=r.channel_id AND c.kind='personal') OR (r.actor_user_id>0 AND r.actor_user_id=f.uploader_user_id))
  AND r.content_msg_id=COALESCE(NULLIF(f.content_msg_id,0),f.msg_id)))`

// GalleryPreparationSummary estimates source download bytes without allocating
// metadata for all candidates. Permission and network-policy checks belong to
// the explicit preparation job, before any source is downloaded.
func GalleryPreparationSummary(ctx context.Context, db *sql.DB, channelID int64, actors ...int64) (GalleryPreparationEstimate, error) {
	if err := validateContext(ctx, "gallery preparation summary"); err != nil {
		return GalleryPreparationEstimate{}, err
	}
	if db == nil {
		return GalleryPreparationEstimate{}, fmt.Errorf("projection: gallery database is nil")
	}
	actor, err := galleryPreparationActor(actors)
	if err != nil {
		return GalleryPreparationEstimate{}, err
	}
	var estimate GalleryPreparationEstimate
	err = db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(f.size),0)`+galleryFrom+galleryPreparationVisibility+galleryPreparationMissing,
		channelID, actor, actor, GalleryPreparationMaxSourceBytes, galleryPreparationDecoderProfile).Scan(&estimate.Total, &estimate.BytesTotal)
	if err != nil {
		return GalleryPreparationEstimate{}, fmt.Errorf("projection: gallery preparation summary: %w", err)
	}
	return estimate, nil
}

// GalleryPreparationPage advances by immutable logical message ID. Completing a
// rendition removes that candidate without shifting later pages. The job can
// persist its last ID and continue after restart, with at most 128 rows resident.
func GalleryPreparationPage(ctx context.Context, db *sql.DB, channelID, afterMsgID int64, limit int, actors ...int64) ([]FileSlim, error) {
	if err := validateContext(ctx, "gallery preparation page"); err != nil {
		return nil, err
	}
	if db == nil {
		return nil, fmt.Errorf("projection: gallery database is nil")
	}
	if afterMsgID < 0 {
		return nil, ErrInvalidGalleryCursor
	}
	if limit == 0 {
		limit = 64
	}
	if limit < 1 || limit > 128 {
		return nil, ErrInvalidGalleryLimit
	}
	actor, err := galleryPreparationActor(actors)
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("projection: gallery preparation page: %w", err)
	}
	defer tx.Rollback()
	items, err := galleryFiles(ctx, tx, limit, gallerySelect+galleryFrom+galleryPreparationVisibility+galleryPreparationMissing+
		` AND f.msg_id>? ORDER BY f.msg_id ASC LIMIT ?`, channelID, actor, actor, GalleryPreparationMaxSourceBytes, galleryPreparationDecoderProfile, afterMsgID, limit)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return items, nil
}

// RecordGalleryPreparationSkip persists a local decoder failure only if the
// source still has the revision observed before the attempt. Replacements must
// never inherit a late failure. Rename-only changes keep existing skip hints.
func RecordGalleryPreparationSkip(ctx context.Context, db *sql.DB, source File) error {
	if err := validateContext(ctx, "record gallery preparation skip"); err != nil {
		return err
	}
	if db == nil || source.ChannelID == 0 || source.MsgID <= 0 {
		return fmt.Errorf("projection: invalid preparation skip")
	}
	_, err := db.ExecContext(ctx, `INSERT INTO gallery_preparation_skips
  (channel_id,file_msg_id,content_msg_id,content_hash,decoder_profile,generator_version)
  SELECT channel_id,msg_id,COALESCE(NULLIF(content_msg_id,0),msg_id),content_hash,?,1 FROM files
  WHERE channel_id=? AND msg_id=? AND revision=? AND content_msg_id=? AND content_hash=? AND tombstoned=0
  ON CONFLICT(channel_id,file_msg_id) DO UPDATE SET content_msg_id=excluded.content_msg_id,
  content_hash=excluded.content_hash,decoder_profile=excluded.decoder_profile,generator_version=excluded.generator_version`,
		galleryPreparationDecoderProfile, source.ChannelID, source.MsgID, source.Revision, source.ContentMsgID, source.ContentHash)
	return err
}
