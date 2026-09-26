package main

import (
	"context"
	"fmt"

	"TDrive/backend/galleryimage"
	fileservice "TDrive/backend/services/file"
)

// OpenGalleryImages creates one capability for a gallery and its viewer. The
// expected channel prevents a late bridge response from opening a new drive's
// images under an old UI. Individual binary requests carry their own cancel.
func (a *App) OpenGalleryImages(channelID int64) (galleryimage.OpenResult, error) {
	// Use the shared lifecycle gate so a queued open cannot recreate a scope
	// after logout has revoked sessions and marked the process terminal.
	release, err := a.acquireMountLifecycle(a.appContext())
	if err != nil {
		return galleryimage.OpenResult{}, err
	}
	defer release()
	if channelID == 0 || channelID != a.ActiveChannelID() {
		return galleryimage.OpenResult{}, fmt.Errorf("gallery drive changed")
	}
	svc, err := a.requireFileService()
	if err != nil {
		return galleryimage.OpenResult{}, err
	}
	a.galleryImagesMu.Lock()
	defer a.galleryImagesMu.Unlock()
	if a.galleryImages == nil {
		a.galleryImages = galleryimage.NewServer()
	}
	return a.galleryImages.Open(a.appContext(), channelID, func(ctx context.Context, channel, msgID, revision int64, kind string) (fileservice.Rendition, error) {
		if channel != a.ActiveChannelID() {
			return fileservice.Rendition{}, fileservice.ErrRenditionStale
		}
		return svc.Rendition(ctx, channel, msgID, revision, kind)
	})
}
func (a *App) CloseGalleryImages(token string) error {
	a.galleryImagesMu.Lock()
	defer a.galleryImagesMu.Unlock()
	if a.galleryImages != nil {
		a.galleryImages.CloseSession(token)
	}
	return nil
}
func (a *App) revokeGalleryImages() {
	a.galleryImagesMu.Lock()
	defer a.galleryImagesMu.Unlock()
	if a.galleryImages != nil {
		a.galleryImages.Revoke()
	}
}
func (a *App) closeGalleryImages() {
	a.galleryImagesMu.Lock()
	defer a.galleryImagesMu.Unlock()
	if a.galleryImages != nil {
		_ = a.galleryImages.Close()
		a.galleryImages = nil
	}
}
