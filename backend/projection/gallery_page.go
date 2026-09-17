package projection

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
		predicate = ` AND (f.upload_time,f.msg_id)<=(?,?)`
		args = append(args, cursor.UploadTime, cursor.MsgID)
	}
	args = append(args, limit+1)
	items, err := galleryFiles(ctx, tx, limit+1, gallerySelect+galleryFrom+predicate+` ORDER BY f.upload_time DESC,f.msg_id DESC LIMIT ?`, args...)
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
	item, index, err := locateGalleryItem(ctx, tx, channelID, msgID)
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

func locateGalleryItem(ctx context.Context, tx *sql.Tx, channelID, msgID int64) (FileSlim, int, error) {
	item, err := galleryItem(ctx, tx, channelID, msgID)
	if err != nil {
		return FileSlim{}, 0, err
	}
	var index int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*)`+galleryFrom+` AND (f.upload_time,f.msg_id)>(?,?)`, channelID, item.UploadTime, item.MsgID).Scan(&index); err != nil {
		return FileSlim{}, 0, err
	}
	return item, index, nil
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
	preceding, err := galleryFiles(ctx, tx, before, gallerySelect+galleryFrom+` AND (f.upload_time,f.msg_id)>(?,?) ORDER BY f.upload_time ASC,f.msg_id ASC LIMIT ?`, channelID, item.UploadTime, item.MsgID, before)
	if err != nil {
		return GalleryPage{}, err
	}
	following, err := galleryFiles(ctx, tx, after, gallerySelect+galleryFrom+` AND (f.upload_time,f.msg_id)<(?,?) ORDER BY f.upload_time DESC,f.msg_id DESC LIMIT ?`, channelID, item.UploadTime, item.MsgID, after)
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
