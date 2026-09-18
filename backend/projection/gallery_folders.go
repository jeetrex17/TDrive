package projection

import (
	"context"
	"database/sql"
	"fmt"
)

// GalleryFolder is one tile in the album grid: a folder that directly holds
// media, how much of it, and the newest item to show as its cover.
type GalleryFolder struct {
	// FolderID is the drive folder id, or "" for the drive's own root.
	FolderID string `json:"folder_id"`
	Name     string `json:"name"`
	// ItemCount counts media directly in this folder, never its subfolders.
	ItemCount int `json:"item_count"`
	// LatestUploadTime is unix seconds; the grid sorts on it.
	LatestUploadTime int64 `json:"latest_upload_time"`
	// The newest item, which is what the tile shows. CoverRevision addresses
	// the thumbnail; a mismatched revision is refused as stale.
	CoverMsgID    int64  `json:"cover_msg_id"`
	CoverRevision int64  `json:"cover_revision"`
	CoverName     string `json:"cover_name"`
}

// MediaFolders returns every folder that directly holds media, newest first.
//
// Counts are of direct children only, and a folder holding nothing but
// subfolders gets no tile. That is the honest reading and the cheap one: a
// rolled-up count would need a recursive walk, and it would also be ambiguous
// against the folder's own listing, which shows the same direct children. In
// practice the leaves are what a person is looking for -- "Camera", "WhatsApp
// Images" -- while the branch above them ("Photo backup") is a route, not a
// place with photos in it.
//
// One query, not one per folder. It reads gallery_items, which the triggers in
// gallery_schema.go already keep filtered to media that is neither tombstoned
// nor a half-finished upload, so nothing here re-derives what counts as a
// picture. The join to files is on that table's primary key.
func MediaFolders(ctx context.Context, db *sql.DB, channelID int64) ([]GalleryFolder, error) {
	if channelID == 0 {
		return nil, fmt.Errorf("projection: media folders: channel id required")
	}
	tx, _, err := beginGalleryRead(ctx, db, channelID, "")
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// The bare g.msg_id / f.revision / g.display_name columns take their values
	// from the row that produced MAX(g.upload_time). That is SQLite's defined
	// behaviour for a query with exactly one min/max aggregate, and it is what
	// makes the cover the newest item without a second query or a window
	// function over the whole table.
	rows, err := tx.QueryContext(ctx, `
		SELECT f.parent_id,
		       COALESCE(folder.name, ''),
		       COUNT(*),
		       MAX(g.upload_time),
		       g.msg_id,
		       f.revision,
		       g.display_name
		FROM gallery_items g
		JOIN files f ON f.channel_id=g.channel_id AND f.msg_id=g.msg_id
		LEFT JOIN folders folder
		       ON folder.channel_id=g.channel_id
		      AND folder.id=f.parent_id
		      AND folder.tombstoned=0
		WHERE g.channel_id=?
		  AND (f.parent_id='' OR folder.id IS NOT NULL)
		GROUP BY f.parent_id
		ORDER BY MAX(g.upload_time) DESC, f.parent_id`, channelID)
	if err != nil {
		return nil, fmt.Errorf("projection: media folders: %w", err)
	}
	defer rows.Close()

	folders := make([]GalleryFolder, 0, 16)
	for rows.Next() {
		var folder GalleryFolder
		if err := rows.Scan(
			&folder.FolderID, &folder.Name, &folder.ItemCount,
			&folder.LatestUploadTime, &folder.CoverMsgID, &folder.CoverRevision, &folder.CoverName,
		); err != nil {
			return nil, fmt.Errorf("projection: scan media folder: %w", err)
		}
		folders = append(folders, folder)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("projection: iterate media folders: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("projection: media folders: %w", err)
	}
	return folders, nil
}

// MediaFolderTimeline describes one folder's media: how many, and the months
// they fall in, which is what the grid needs to lay out before it has any rows.
//
// The drive-wide timeline reads gallery_months, a rollup the triggers maintain
// because a million-photo library cannot be counted on every open. A folder is
// not that: it is a few hundred to a few thousand items, so its months are
// grouped on demand rather than given a second materialised table to keep in
// step with moves, deletes and restores.
func MediaFolderTimeline(ctx context.Context, db *sql.DB, channelID int64, folderID string) (GalleryTimeline, error) {
	if channelID == 0 {
		return GalleryTimeline{}, fmt.Errorf("projection: media folder timeline: channel id required")
	}
	tx, generation, err := beginGalleryRead(ctx, db, channelID, "")
	if err != nil {
		return GalleryTimeline{}, err
	}
	defer tx.Rollback()
	timeline := GalleryTimeline{
		ChannelID:  channelID,
		Generation: generation,
		PageSize:   GalleryPageSize,
		Buckets:    []GalleryBucket{},
		Anchors:    []GalleryAnchor{},
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT g.month_key, COUNT(*), MAX(g.upload_time)
		FROM gallery_items g
		JOIN files f ON f.channel_id=g.channel_id AND f.msg_id=g.msg_id
		WHERE g.channel_id=? AND f.parent_id=?
		GROUP BY g.month_key
		ORDER BY g.month_key DESC`, channelID, folderID)
	if err != nil {
		return GalleryTimeline{}, fmt.Errorf("projection: media folder months: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var bucket GalleryBucket
		if err := rows.Scan(&bucket.Key, &bucket.Count, &bucket.UploadTime); err != nil {
			return GalleryTimeline{}, fmt.Errorf("projection: scan media folder month: %w", err)
		}
		bucket.StartIndex = timeline.TotalCount
		timeline.TotalCount += bucket.Count
		timeline.Buckets = append(timeline.Buckets, bucket)
	}
	if err := rows.Err(); err != nil {
		return GalleryTimeline{}, fmt.Errorf("projection: iterate media folder months: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return GalleryTimeline{}, fmt.Errorf("projection: media folder timeline: %w", err)
	}
	return timeline, nil
}

// MediaFolderPage is one keyset page of a folder's media, newest first.
//
// It is the drive-wide MediaPage with one more predicate, sharing that
// function's select list, join and cursor encoding rather than restating them:
// a page of photos is the same page of photos whether a folder narrowed it or
// not, and two definitions of that would be free to disagree about what a
// gallery item is.
func MediaFolderPage(ctx context.Context, db *sql.DB, channelID int64, folderID, encoded string, limit int) (GalleryPage, error) {
	if limit < 1 || limit > GalleryMaxPageSize {
		return GalleryPage{}, ErrInvalidGalleryLimit
	}
	cursor := galleryCursor{ChannelID: channelID}
	var err error
	if encoded != "" {
		cursor, err = decodeGalleryCursor(encoded, channelID)
		if err != nil {
			return GalleryPage{}, err
		}
	}
	tx, generation, err := beginGalleryRead(ctx, db, channelID, cursor.Generation)
	if err != nil {
		return GalleryPage{}, err
	}
	defer tx.Rollback()

	predicate := ` AND f.parent_id=?`
	args := []any{channelID, folderID}
	if encoded != "" {
		predicate += ` AND (gi.upload_time,gi.msg_id)<=(?,?)`
		args = append(args, cursor.UploadTime, cursor.MsgID)
	}
	args = append(args, limit+1)
	items, err := galleryFiles(ctx, tx, limit+1,
		gallerySelect+galleryFrom+predicate+` ORDER BY gi.upload_time DESC,gi.msg_id DESC LIMIT ?`, args...)
	if err != nil {
		return GalleryPage{}, err
	}
	page := GalleryPage{Generation: generation, StartIndex: cursor.Index, Items: items}
	if len(items) > limit {
		next := items[limit]
		page.NextCursor = encodeGalleryCursor(channelID, generation, next.UploadTime, next.MsgID, cursor.Index+limit)
		page.Items = items[:limit]
	}
	if err := tx.Commit(); err != nil {
		return GalleryPage{}, err
	}
	return page, nil
}
