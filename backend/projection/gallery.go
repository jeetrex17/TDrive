package projection

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"
)

const (
	GalleryPageSize     = 128
	GalleryMaxPageSize  = 512
	GalleryMaxNeighbors = 128
)

var (
	ErrGalleryStale         = errors.New("gallery snapshot is stale")
	ErrInvalidGalleryCursor = errors.New("invalid gallery cursor")
	ErrInvalidGalleryLimit  = errors.New("invalid gallery page limit")
	ErrGalleryNotFound      = errors.New("gallery image not found")
)

type GalleryBucket struct {
	Key        string `json:"key"`
	StartIndex int    `json:"start_index"`
	Count      int    `json:"count"`
	UploadTime int64  `json:"upload_time"`
}

type GalleryAnchor struct {
	StartIndex int    `json:"start_index"`
	Cursor     string `json:"cursor"`
}

type GalleryTimeline struct {
	ChannelID  int64           `json:"channel_id"`
	Generation string          `json:"generation"`
	TotalCount int             `json:"total_count"`
	PageSize   int             `json:"page_size"`
	Buckets    []GalleryBucket `json:"buckets"`
	Anchors    []GalleryAnchor `json:"anchors"`
}

type GalleryPage struct {
	Generation string
	// StartIndex is -1 for an ID-centered neighbor window whose absolute rank
	// was intentionally not counted. AnchorOffset locates its requested image.
	StartIndex   int
	AnchorOffset int
	Items        []FileSlim
	NextCursor   string
}

type GalleryLocation struct {
	Generation string `json:"generation"`
	Index      int    `json:"index"`
	Cursor     string `json:"cursor"`
}

// All gallery queries share the same eligibility predicate. A missing legacy
// dirent does not hide an orphaned file, but an explicitly tombstoned one does.
const galleryFrom = ` FROM files f
 LEFT JOIN dirents d ON d.channel_id=f.channel_id AND d.object_id='f:' || f.msg_id
 WHERE f.channel_id=? AND f.tombstoned=0 AND f.upload_uuid=''
 AND COALESCE(d.tombstoned,0)=0 AND (
  COALESCE(d.display_name,f.name) LIKE '%.jpg' COLLATE NOCASE
  OR COALESCE(d.display_name,f.name) LIKE '%.jpeg' COLLATE NOCASE
  OR COALESCE(d.display_name,f.name) LIKE '%.png' COLLATE NOCASE
  OR COALESCE(d.display_name,f.name) LIKE '%.gif' COLLATE NOCASE
  OR COALESCE(d.display_name,f.name) LIKE '%.webp' COLLATE NOCASE
  OR COALESCE(d.display_name,f.name) LIKE '%.bmp' COLLATE NOCASE)`

const gallerySelect = `SELECT f.msg_id,f.content_msg_id,f.content_hash,f.revision,f.upload_uuid,f.part_count,
 COALESCE(d.display_name,f.name),f.size,f.parent_id,f.upload_time,f.uploader_user_id,f.encrypted,f.plaintext_size`

// MediaTimeline scans only sort keys, retaining one seek anchor per page and
// one UTC month bucket. Detailed file metadata never accumulates here. This
// one-time O(n) key scan permits O(log n + page size) jumps even near the end.
func MediaTimeline(ctx context.Context, db *sql.DB, channelID int64) (GalleryTimeline, error) {
	tx, generation, err := beginGalleryRead(ctx, db, channelID, "")
	if err != nil {
		return GalleryTimeline{}, err
	}
	defer tx.Rollback()
	timeline := GalleryTimeline{ChannelID: channelID, Generation: generation, PageSize: GalleryPageSize, Buckets: []GalleryBucket{}, Anchors: []GalleryAnchor{}}
	rows, err := tx.QueryContext(ctx, `SELECT f.upload_time,f.msg_id`+galleryFrom+` ORDER BY f.upload_time DESC,f.msg_id DESC`, channelID)
	if err != nil {
		return GalleryTimeline{}, fmt.Errorf("projection: gallery timeline: %w", err)
	}
	defer rows.Close()
	var monthStart int64
	for rows.Next() {
		var timestamp, msgID int64
		if err := rows.Scan(&timestamp, &msgID); err != nil {
			return GalleryTimeline{}, fmt.Errorf("projection: gallery key: %w", err)
		}
		index := timeline.TotalCount
		if index%GalleryPageSize == 0 {
			timeline.Anchors = append(timeline.Anchors, GalleryAnchor{StartIndex: index, Cursor: encodeGalleryCursor(channelID, generation, timestamp, msgID, index)})
		}
		last := len(timeline.Buckets) - 1
		if last < 0 || timestamp < monthStart {
			date := time.Unix(timestamp, 0).UTC()
			monthStart = time.Date(date.Year(), date.Month(), 1, 0, 0, 0, 0, time.UTC).Unix()
			timeline.Buckets = append(timeline.Buckets, GalleryBucket{Key: date.Format("2006-01"), StartIndex: index, Count: 1, UploadTime: timestamp})
		} else {
			timeline.Buckets[last].Count++
		}
		timeline.TotalCount++
	}
	if err := rows.Err(); err != nil {
		return GalleryTimeline{}, fmt.Errorf("projection: gallery timeline: %w", err)
	}
	if err := rows.Close(); err != nil {
		return GalleryTimeline{}, err
	}
	if err := tx.Commit(); err != nil {
		return GalleryTimeline{}, err
	}
	return timeline, nil
}

// Each API call uses a short snapshot transaction. No read transaction remains
// open during user scrolling. A later call rejects an expired generation before
// returning metadata, allowing the UI to requery around its visible anchor.
func beginGalleryRead(ctx context.Context, db *sql.DB, channelID int64, expected string) (*sql.Tx, string, error) {
	if err := validateContext(ctx, "gallery"); err != nil {
		return nil, "", err
	}
	if db == nil {
		return nil, "", fmt.Errorf("projection: gallery database is nil")
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, "", fmt.Errorf("projection: begin gallery: %w", err)
	}
	var epoch string
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT epoch,COALESCE((SELECT revision FROM gallery_generations WHERE channel_id=?),0) FROM gallery_epoch WHERE id=1`, channelID).Scan(&epoch, &revision)
	if err != nil {
		_ = tx.Rollback()
		return nil, "", fmt.Errorf("projection: gallery generation: %w", err)
	}
	generation := epoch + ":" + strconv.FormatInt(channelID, 10) + ":" + strconv.FormatInt(revision, 10)
	if expected != "" && generation != expected {
		_ = tx.Rollback()
		return nil, "", ErrGalleryStale
	}
	return tx, generation, nil
}
