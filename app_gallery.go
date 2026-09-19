package main

import (
	"context"
	"time"

	"TDrive/backend/projection"
	readservice "TDrive/backend/services/read"
)

// GalleryItem keeps existing file wire fields while adding immutable content
// identity. Pixel caches must use content identity, never a filename alone.
type GalleryItem struct {
	Name          string `json:"name"`
	Size          int64  `json:"size"`
	TgMsgID       int    `json:"msg_id"`
	ParentID      string `json:"parent_id"`
	UploadTime    int64  `json:"upload_time"`
	UploaderID    int64  `json:"uploader_id"`
	Encrypted     bool   `json:"encrypted,omitempty"`
	PlaintextSize int64  `json:"plaintext_size,omitempty"`
	ContentMsgID  int64  `json:"content_msg_id"`
	ContentHash   string `json:"content_hash"`
	Revision      int64  `json:"revision"`
}

type GalleryPage struct {
	Generation   string        `json:"generation"`
	StartIndex   int           `json:"start_index"`
	AnchorOffset int           `json:"anchor_offset"`
	Items        []GalleryItem `json:"items"`
	NextCursor   string        `json:"next_cursor"`
}

// GetMediaTimeline returns only compact layout metadata and sparse page keys.
// A cursor is tied to the account database, active drive, and projection epoch.
func (a *App) GetMediaTimeline() (projection.GalleryTimeline, error) {
	svc, err := a.requireReadService()
	if err != nil {
		return projection.GalleryTimeline{}, err
	}
	ctx, cancel := a.galleryReadContext()
	defer cancel()
	return svc.MediaTimeline(ctx, a.ActiveChannelID())
}

func (a *App) GetMediaTimelineSummary() (projection.GalleryTimeline, error) {
	svc, err := a.requireReadService()
	if err != nil {
		return projection.GalleryTimeline{}, err
	}
	ctx, cancel := a.galleryReadContext()
	defer cancel()
	return svc.MediaTimelineSummary(ctx, a.ActiveChannelID())
}

func (a *App) GetMediaTimelineAnchors(generation string) (projection.GalleryTimeline, error) {
	svc, err := a.requireReadService()
	if err != nil {
		return projection.GalleryTimeline{}, err
	}
	ctx, cancel := a.galleryReadContext()
	defer cancel()
	return svc.MediaTimelineAnchors(ctx, a.ActiveChannelID(), generation)
}

func (a *App) ListMediaPage(cursor string, limit int) (GalleryPage, error) {
	svc, err := a.requireReadService()
	if err != nil {
		return GalleryPage{}, err
	}
	ctx, cancel := a.galleryReadContext()
	defer cancel()
	page, err := svc.MediaPage(ctx, a.ActiveChannelID(), cursor, limit)
	if err != nil {
		return GalleryPage{}, err
	}
	return galleryPageToWire(page), nil
}

// ListMediaFolders returns the album grid: every folder that directly holds
// media, newest first, with its count and the newest item as a cover.
func (a *App) ListMediaFolders() ([]projection.GalleryFolder, error) {
	svc, err := a.requireReadService()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.galleryReadContext()
	defer cancel()
	return svc.MediaFolders(ctx, a.ActiveChannelID())
}

// GetMediaFolderTimeline is the scoped counterpart of GetMediaTimelineSummary.
// A folder has no anchor index: it is small enough to seek by paging, so this
// carries only the month buckets the grid lays out against.
func (a *App) GetMediaFolderTimeline(folderID string) (projection.GalleryTimeline, error) {
	svc, err := a.requireReadService()
	if err != nil {
		return projection.GalleryTimeline{}, err
	}
	ctx, cancel := a.galleryReadContext()
	defer cancel()
	return svc.MediaFolderTimeline(ctx, a.ActiveChannelID(), folderID)
}

func (a *App) ListMediaFolderPage(folderID, cursor string, limit int) (GalleryPage, error) {
	svc, err := a.requireReadService()
	if err != nil {
		return GalleryPage{}, err
	}
	ctx, cancel := a.galleryReadContext()
	defer cancel()
	page, err := svc.MediaFolderPage(ctx, a.ActiveChannelID(), folderID, cursor, limit)
	if err != nil {
		return GalleryPage{}, err
	}
	return galleryPageToWire(page), nil
}

func (a *App) LocateMedia(msgID int, generation string) (projection.GalleryLocation, error) {
	svc, err := a.requireReadService()
	if err != nil {
		return projection.GalleryLocation{}, err
	}
	ctx, cancel := a.galleryReadContext()
	defer cancel()
	return svc.LocateMedia(ctx, a.ActiveChannelID(), int64(msgID), generation)
}

func (a *App) GetMediaNeighbors(msgID, before, after int, generation string) (GalleryPage, error) {
	svc, err := a.requireReadService()
	if err != nil {
		return GalleryPage{}, err
	}
	ctx, cancel := a.galleryReadContext()
	defer cancel()
	page, err := svc.MediaNeighbors(ctx, a.ActiveChannelID(), int64(msgID), before, after, generation)
	if err != nil {
		return GalleryPage{}, err
	}
	return galleryPageToWire(page), nil
}

func galleryPageToWire(page readservice.GalleryPage) GalleryPage {
	items := make([]GalleryItem, 0, len(page.Items))
	for _, file := range page.Items {
		items = append(items, GalleryItem{Name: file.Name, Size: file.Size, TgMsgID: int(file.MsgID), ParentID: file.ParentID,
			UploadTime: file.UploadTime, UploaderID: file.UploaderID, Encrypted: file.Encrypted, PlaintextSize: file.PlaintextSize,
			ContentMsgID: file.ContentMsgID, ContentHash: file.ContentHash, Revision: file.Revision})
	}
	return GalleryPage{Generation: page.Generation, StartIndex: page.StartIndex, AnchorOffset: page.AnchorOffset, Items: items, NextCursor: page.NextCursor}
}

// Bound abandoned bridge reads even on hosts whose bridge cannot cancel a
// dispatched method. Shutdown cancellation propagates immediately to SQLite.
func (a *App) galleryReadContext() (context.Context, context.CancelFunc) {
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, 15*time.Second)
}
