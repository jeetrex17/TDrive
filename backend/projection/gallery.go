package projection

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
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

// gallery_items materializes the shared eligibility predicate. Joining files
// supplies mutable metadata while the narrow order index drives every seek.
const galleryFrom = ` FROM gallery_items gi
 JOIN files f ON f.channel_id=gi.channel_id AND f.msg_id=gi.msg_id
 WHERE gi.channel_id=?`

const gallerySelect = `SELECT f.msg_id,f.content_msg_id,f.content_hash,f.revision,f.upload_uuid,f.part_count,
 gi.display_name,f.size,f.parent_id,gi.upload_time,f.uploader_user_id,f.encrypted,f.plaintext_size`

// MediaTimeline scans only sort keys, retaining one seek anchor per page and
// one UTC month bucket. Detailed file metadata never accumulates here. This
// one-time O(n) key scan permits O(log n + page size) jumps even near the end.
func MediaTimeline(ctx context.Context, db *sql.DB, channelID int64) (GalleryTimeline, error) {
	tx, generation, err := beginGalleryRead(ctx, db, channelID, "")
	if err != nil {
		return GalleryTimeline{}, err
	}
	defer tx.Rollback()
	timeline, err := mediaTimelineCache.load(ctx, generation, func() (GalleryTimeline, error) {
		return buildGalleryTimeline(ctx, tx, channelID, generation)
	})
	if err != nil {
		return GalleryTimeline{}, err
	}
	if err := tx.Commit(); err != nil {
		return GalleryTimeline{}, err
	}
	return timeline, nil
}

func buildGalleryTimeline(ctx context.Context, tx *sql.Tx, channelID int64, generation string) (GalleryTimeline, error) {
	timeline := GalleryTimeline{ChannelID: channelID, Generation: generation, PageSize: GalleryPageSize, Buckets: []GalleryBucket{}, Anchors: []GalleryAnchor{}}
	rows, err := tx.QueryContext(ctx, `SELECT month_key,item_count,latest_upload_time
FROM gallery_months WHERE channel_id=? ORDER BY month_key DESC`, channelID)
	if err != nil {
		return GalleryTimeline{}, fmt.Errorf("projection: gallery month timeline: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var bucket GalleryBucket
		if err := rows.Scan(&bucket.Key, &bucket.Count, &bucket.UploadTime); err != nil {
			return GalleryTimeline{}, fmt.Errorf("projection: gallery month: %w", err)
		}
		bucket.StartIndex = timeline.TotalCount
		timeline.TotalCount += bucket.Count
		timeline.Buckets = append(timeline.Buckets, bucket)
	}
	if err := rows.Err(); err != nil {
		return GalleryTimeline{}, fmt.Errorf("projection: gallery month timeline: %w", err)
	}
	if err := rows.Close(); err != nil {
		return GalleryTimeline{}, err
	}

	rows, err = tx.QueryContext(ctx, `SELECT upload_time,msg_id,ordinal-1 FROM (
  SELECT upload_time,msg_id,ROW_NUMBER() OVER (ORDER BY upload_time DESC,msg_id DESC) AS ordinal
  FROM gallery_items WHERE channel_id=?
) WHERE (ordinal-1)%?=0 ORDER BY ordinal`, channelID, GalleryPageSize)
	if err != nil {
		return GalleryTimeline{}, fmt.Errorf("projection: gallery anchors: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var timestamp, msgID int64
		var index int
		if err := rows.Scan(&timestamp, &msgID, &index); err != nil {
			return GalleryTimeline{}, fmt.Errorf("projection: gallery anchor: %w", err)
		}
		timeline.Anchors = append(timeline.Anchors, GalleryAnchor{
			StartIndex: index,
			Cursor:     encodeGalleryCursor(channelID, generation, timestamp, msgID, index),
		})
	}
	if err := rows.Err(); err != nil {
		return GalleryTimeline{}, fmt.Errorf("projection: gallery anchors: %w", err)
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
