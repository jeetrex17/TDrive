package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"TDrive/backend/galleryprepare"
	"TDrive/backend/projection"
	fileservice "TDrive/backend/services/file"
	"TDrive/backend/thumbnail"
)

// GetGalleryPreparation estimates only pending compatible files. Completed
// progress is retained for the current drive, while newly missing derivatives
// are reflected without holding the entire candidate list in memory.
func (a *App) GetGalleryPreparation() (galleryprepare.State, error) {
	svc, err := a.requireReadService()
	if err != nil {
		return galleryprepare.State{}, err
	}
	channelID := a.ActiveChannelID()
	state := galleryprepare.State{ChannelID: channelID}
	if runner := a.galleryPreparationRunner(false); runner != nil {
		snapshot := runner.Snapshot()
		if snapshot.ChannelID == channelID {
			state = snapshot
			if state.Running {
				return state, nil
			}
		}
	}
	if channelID == 0 {
		return state, nil
	}
	ctx, cancel := a.galleryReadContext()
	defer cancel()
	files, err := a.requireFileService()
	if err != nil {
		return galleryprepare.State{}, err
	}
	actor, err := galleryPreparationActor(ctx, files)
	if err != nil {
		return galleryprepare.State{}, err
	}
	estimate, err := projection.GalleryPreparationSummary(ctx, svc.DB, channelID, actor)
	if err != nil {
		return galleryprepare.State{}, err
	}
	state.Total = state.Completed + state.Skipped + estimate.Total
	state.BytesTotal = state.BytesDone + estimate.BytesTotal
	return state, nil
}

// StartGalleryPreparation is an explicit data-consuming action. The per-file
// service rechecks ownership and encryption before downloading an original.
func (a *App) StartGalleryPreparation(channelID int64) OperationResult {
	if channelID == 0 || channelID != a.ActiveChannelID() {
		return operationFailure(fmt.Errorf("preview preparation requires the active drive"))
	}
	svc, err := a.requireFileService()
	if err != nil {
		return operationFailure(err)
	}

	a.galleryPreparationMu.Lock()
	epoch := a.galleryPreparationEpoch
	a.galleryPreparationMu.Unlock()
	ctx, cancel := a.galleryReadContext()
	defer cancel()
	actor, err := galleryPreparationActor(ctx, svc)
	if err != nil {
		return operationFailure(err)
	}
	candidates, err := projection.GalleryPreparationPage(ctx, svc.DB, channelID, 0, 1, actor)
	if err != nil {
		return operationFailure(err)
	}
	if len(candidates) > 0 {
		if err := svc.ValidateRenditionPreparation(ctx, channelID, candidates[0].MsgID); err != nil {
			return operationFailure(err)
		}
	}
	release, err := a.acquireMountLifecycle(ctx)
	if err != nil {
		return operationFailure(err)
	}
	defer release()
	runner := a.galleryPreparationRunner(true)
	a.galleryPreparationMu.Lock()
	defer a.galleryPreparationMu.Unlock()
	if epoch != a.galleryPreparationEpoch || channelID != a.ActiveChannelID() {
		return operationFailure(context.Canceled)
	}
	source := galleryPreparationSource{db: svc.DB, files: svc, actorID: actor}
	if err := runner.Start(a.appContext(), channelID, source); err != nil {
		return operationFailure(err)
	}
	return operationSuccess()
}

func (a *App) StopGalleryPreparation() OperationResult {
	a.stopGalleryPreparation()
	return operationSuccess()
}

// stopGalleryPreparation is shared by user cancellation, drive switches, vault
// lock, background transitions and shutdown. It never creates an idle worker.
func (a *App) stopGalleryPreparation() {
	a.galleryPreparationMu.Lock()
	a.galleryPreparationEpoch++
	runner := a.galleryPreparation
	a.galleryPreparationMu.Unlock()
	if runner != nil {
		runner.Stop()
	}
}

func (a *App) galleryPreparationRunner(create bool) *galleryprepare.Runner {
	a.galleryPreparationMu.Lock()
	defer a.galleryPreparationMu.Unlock()
	if a.galleryPreparation == nil && create {
		a.galleryPreparation = galleryprepare.New(func(state galleryprepare.State) { a.emit("gallery_preparation_progress", state) })
	}
	return a.galleryPreparation
}

type galleryPreparationSource struct {
	actorID int64
	db      *sql.DB
	files   *fileservice.Service
}

func (s galleryPreparationSource) Estimate(ctx context.Context, channelID int64) (galleryprepare.Estimate, error) {
	estimate, err := projection.GalleryPreparationSummary(ctx, s.db, channelID, s.actorID)
	return galleryprepare.Estimate{Total: estimate.Total, BytesTotal: estimate.BytesTotal}, err
}

func (s galleryPreparationSource) Page(ctx context.Context, channelID, after int64, limit int) ([]galleryprepare.Item, error) {
	files, err := projection.GalleryPreparationPage(ctx, s.db, channelID, after, limit, s.actorID)
	if err != nil {
		return nil, err
	}
	items := make([]galleryprepare.Item, 0, len(files))
	for _, file := range files {
		items = append(items, galleryprepare.Item{MsgID: file.MsgID, Size: file.Size})
	}
	return items, nil
}

func (s galleryPreparationSource) Prepare(ctx context.Context, channelID, msgID, budget int64) (int64, error) {
	source, found, err := projection.FileByID(s.db, channelID, msgID)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, fmt.Errorf("preview source is no longer available")
	}
	written, err := s.files.PrepareRemoteRenditionsWithinBudget(ctx, channelID, msgID, budget)
	if !errors.Is(err, thumbnail.ErrUnsupported) && !errors.Is(err, thumbnail.ErrTooLarge) {
		return written, err
	}
	if err := projection.RecordGalleryPreparationSkip(ctx, s.db, source); err != nil {
		return written, err
	}
	return written, galleryprepare.ErrSkipped
}

func galleryPreparationActor(ctx context.Context, svc *fileservice.Service) (int64, error) {
	if svc.ActorID == nil {
		return 0, fmt.Errorf("preview preparation needs an authenticated account")
	}
	actor, err := svc.ActorID(ctx)
	if err != nil {
		return 0, err
	}
	if actor <= 0 {
		return 0, fmt.Errorf("preview preparation needs an authenticated account")
	}
	return actor, nil
}
