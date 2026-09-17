package projection

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

type galleryCursor struct {
	Version    int    `json:"v"`
	ChannelID  int64  `json:"c"`
	Generation string `json:"g"`
	UploadTime int64  `json:"t"`
	MsgID      int64  `json:"m"`
	Index      int    `json:"i"`
}

func encodeGalleryCursor(channelID int64, generation string, timestamp, msgID int64, index int) string {
	bytes, _ := json.Marshal(galleryCursor{Version: 1, ChannelID: channelID, Generation: generation, UploadTime: timestamp, MsgID: msgID, Index: index})
	return base64.RawURLEncoding.EncodeToString(bytes)
}

func decodeGalleryCursor(encoded string, channelID int64) (galleryCursor, error) {
	if len(encoded) > 1024 {
		return galleryCursor{}, ErrInvalidGalleryCursor
	}
	bytes, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return galleryCursor{}, ErrInvalidGalleryCursor
	}
	var cursor galleryCursor
	decoder := json.NewDecoder(strings.NewReader(string(bytes)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cursor) != nil || cursor.Version != 1 || cursor.ChannelID != channelID || cursor.Generation == "" || len(cursor.Generation) > 128 || cursor.MsgID <= 0 || cursor.Index < 0 || cursor.Index > int(^uint(0)>>1)-GalleryMaxPageSize {
		return galleryCursor{}, ErrInvalidGalleryCursor
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return galleryCursor{}, ErrInvalidGalleryCursor
	}
	return cursor, nil
}

// MediaPage starts inclusively at the supplied key. Requesting one extra row
// produces the next cursor without counting or discarding preceding rows.
func MediaPage(ctx context.Context, db *sql.DB, channelID int64, encoded string, limit int) (GalleryPage, error) {
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
	predicate := ""
	args := []any{channelID}
	if encoded != "" {
		predicate = ` AND (gi.upload_time,gi.msg_id)<=(?,?)`
		args = append(args, cursor.UploadTime, cursor.MsgID)
	}
	args = append(args, limit+1)
	items, err := galleryFiles(ctx, tx, limit+1, gallerySelect+galleryFrom+predicate+` ORDER BY gi.upload_time DESC,gi.msg_id DESC LIMIT ?`, args...)
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

// LocateMedia is intentionally separate from sequential paging: its indexed
// rank count is paid only when restoring a named anchor after a refresh.
func LocateMedia(ctx context.Context, db *sql.DB, channelID, msgID int64, generation string) (GalleryLocation, error) {
	tx, current, err := beginGalleryRead(ctx, db, channelID, generation)
	if err != nil {
		return GalleryLocation{}, err
	}
	defer tx.Rollback()
	item, err := galleryItem(ctx, tx, channelID, msgID)
	if err != nil {
		return GalleryLocation{}, err
	}
	timeline, err := mediaTimelineCache.load(ctx, current, func() (GalleryTimeline, error) {
		return buildGalleryTimeline(ctx, tx, channelID, current)
	})
	if err != nil {
		return GalleryLocation{}, err
	}
	item, index, _, err := locateGalleryItemFromTimeline(ctx, tx, channelID, item, timeline)
	if err != nil {
		return GalleryLocation{}, err
	}
	location := GalleryLocation{Generation: current, Index: index, Cursor: encodeGalleryCursor(channelID, current, item.UploadTime, msgID, index)}
	if err := tx.Commit(); err != nil {
		return GalleryLocation{}, err
	}
	return location, nil
}

func galleryItem(ctx context.Context, tx *sql.Tx, channelID, msgID int64) (FileSlim, error) {
	if msgID <= 0 {
		return FileSlim{}, ErrGalleryNotFound
	}
	item, err := scanFileSlim(tx.QueryRowContext(ctx, gallerySelect+galleryFrom+` AND f.msg_id=?`, channelID, msgID))
	if errors.Is(err, sql.ErrNoRows) {
		return FileSlim{}, ErrGalleryNotFound
	}
	if err != nil {
		return FileSlim{}, fmt.Errorf("projection: locate gallery image: %w", err)
	}
	return item, nil
}

func locateGalleryItemFromTimeline(ctx context.Context, tx *sql.Tx, channelID int64, item FileSlim, timeline GalleryTimeline) (FileSlim, int, int, error) {
	if len(timeline.Anchors) == 0 {
		return FileSlim{}, 0, 0, ErrGalleryNotFound
	}
	anchorIndex := sort.Search(len(timeline.Anchors), func(i int) bool {
		cursor, err := decodeGalleryCursor(timeline.Anchors[i].Cursor, channelID)
		if err != nil {
			return false
		}
		return cursor.UploadTime < item.UploadTime || cursor.UploadTime == item.UploadTime && cursor.MsgID < item.MsgID
	}) - 1
	if anchorIndex < 0 {
		return FileSlim{}, 0, 0, fmt.Errorf("projection: locate gallery image: no preceding anchor")
	}
	anchor := timeline.Anchors[anchorIndex]
	cursor, err := decodeGalleryCursor(anchor.Cursor, channelID)
	if err != nil || cursor.Generation != timeline.Generation {
		return FileSlim{}, 0, 0, fmt.Errorf("projection: locate gallery image: invalid cached anchor")
	}
	items, err := galleryFiles(ctx, tx, GalleryPageSize, gallerySelect+galleryFrom+
		` AND (gi.upload_time,gi.msg_id)<=(?,?) ORDER BY gi.upload_time DESC,gi.msg_id DESC LIMIT ?`,
		channelID, cursor.UploadTime, cursor.MsgID, GalleryPageSize)
	if err != nil {
		return FileSlim{}, 0, 0, err
	}
	for offset, candidate := range items {
		if candidate.MsgID == item.MsgID {
			return candidate, anchor.StartIndex + offset, len(items), nil
		}
	}
	return FileSlim{}, 0, len(items), fmt.Errorf("projection: locate gallery image: item missing from anchor page")
}

// MediaNeighbors reads a bounded window around a stable file ID. Two keyset
// seeks avoid loading or counting the whole library when the viewer crosses a
// page edge. StartIndex is -1; AnchorOffset identifies the requested item.
func MediaNeighbors(ctx context.Context, db *sql.DB, channelID, msgID int64, before, after int, generation string) (GalleryPage, error) {
	if before < 0 || after < 0 || before > GalleryMaxNeighbors || after > GalleryMaxNeighbors {
		return GalleryPage{}, ErrInvalidGalleryLimit
	}
	tx, current, err := beginGalleryRead(ctx, db, channelID, generation)
	if err != nil {
		return GalleryPage{}, err
	}
	defer tx.Rollback()
	item, err := galleryItem(ctx, tx, channelID, msgID)
	if err != nil {
		return GalleryPage{}, err
	}
	preceding, err := galleryFiles(ctx, tx, before, gallerySelect+galleryFrom+` AND (gi.upload_time,gi.msg_id)>(?,?) ORDER BY gi.upload_time ASC,gi.msg_id ASC LIMIT ?`, channelID, item.UploadTime, item.MsgID, before)
	if err != nil {
		return GalleryPage{}, err
	}
	following, err := galleryFiles(ctx, tx, after, gallerySelect+galleryFrom+` AND (gi.upload_time,gi.msg_id)<(?,?) ORDER BY gi.upload_time DESC,gi.msg_id DESC LIMIT ?`, channelID, item.UploadTime, item.MsgID, after)
	if err != nil {
		return GalleryPage{}, err
	}
	items := make([]FileSlim, 0, len(preceding)+1+len(following))
	for i := len(preceding) - 1; i >= 0; i-- {
		items = append(items, preceding[i])
	}
	items = append(items, item)
	items = append(items, following...)
	if err := tx.Commit(); err != nil {
		return GalleryPage{}, err
	}
	return GalleryPage{Generation: current, StartIndex: -1, AnchorOffset: len(preceding), Items: items}, nil
}

func galleryFiles(ctx context.Context, tx *sql.Tx, limit int, query string, args ...any) ([]FileSlim, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("projection: gallery page: %w", err)
	}
	defer rows.Close()
	items := make([]FileSlim, 0, limit)
	for rows.Next() {
		item, err := scanFileSlim(rows)
		if err != nil {
			return nil, fmt.Errorf("projection: gallery image: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("projection: gallery page: %w", err)
	}
	return items, nil
}
