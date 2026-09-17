package read

import (
	"context"

	"TDrive/backend/projection"
)

type GalleryPage struct {
	Generation   string
	StartIndex   int
	AnchorOffset int
	Items        []File
	NextCursor   string
}

// MediaTimeline returns sparse seek keys and counts, never all file records.
func (s *Service) MediaTimeline(ctx context.Context, channelID int64) (projection.GalleryTimeline, error) {
	if err := s.ready(); err != nil {
		return projection.GalleryTimeline{}, err
	}
	return projection.MediaTimeline(ctx, s.DB, channelID)
}

func (s *Service) MediaTimelineSummary(ctx context.Context, channelID int64) (projection.GalleryTimeline, error) {
	if err := s.ready(); err != nil {
		return projection.GalleryTimeline{}, err
	}
	return projection.MediaTimelineSummary(ctx, s.DB, channelID)
}

func (s *Service) MediaTimelineAnchors(ctx context.Context, channelID int64, generation string) (projection.GalleryTimeline, error) {
	if err := s.ready(); err != nil {
		return projection.GalleryTimeline{}, err
	}
	return projection.MediaTimelineAnchors(ctx, s.DB, channelID, generation)
}

func (s *Service) MediaPage(ctx context.Context, channelID int64, cursor string, limit int) (GalleryPage, error) {
	if err := s.ready(); err != nil {
		return GalleryPage{}, err
	}
	page, err := projection.MediaPage(ctx, s.DB, channelID, cursor, limit)
	if err != nil {
		return GalleryPage{}, err
	}
	return galleryPageFromProjection(page), nil
}

func (s *Service) LocateMedia(ctx context.Context, channelID, msgID int64, generation string) (projection.GalleryLocation, error) {
	if err := s.ready(); err != nil {
		return projection.GalleryLocation{}, err
	}
	return projection.LocateMedia(ctx, s.DB, channelID, msgID, generation)
}

func (s *Service) MediaNeighbors(ctx context.Context, channelID, msgID int64, before, after int, generation string) (GalleryPage, error) {
	if err := s.ready(); err != nil {
		return GalleryPage{}, err
	}
	page, err := projection.MediaNeighbors(ctx, s.DB, channelID, msgID, before, after, generation)
	if err != nil {
		return GalleryPage{}, err
	}
	return galleryPageFromProjection(page), nil
}

func galleryPageFromProjection(page projection.GalleryPage) GalleryPage {
	files := make([]File, 0, len(page.Items))
	for _, item := range page.Items {
		files = append(files, fileFromProjection(item))
	}
	return GalleryPage{Generation: page.Generation, StartIndex: page.StartIndex, AnchorOffset: page.AnchorOffset, Items: files, NextCursor: page.NextCursor}
}
