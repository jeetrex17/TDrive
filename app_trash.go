package main

import (
	"errors"

	"TDrive/backend/projection"
)

// millisPerSecond converts the projection's unix-second timestamps into the
// unix millis the frontend's Date handling expects.
const millisPerSecond = 1000

// TrashEntry is one deleted object as the trash view needs it: what it was
// called, where it came from, and the two instants that bound how long it can
// still be brought back.
type TrashEntry struct {
	ObjectID   string `json:"object_id"`   // "f:2615" or "d:uuid"
	Kind       string `json:"kind"`        // "file" | "folder"
	Name       string `json:"name"`        // original display name
	ParentPath string `json:"parent_path"` // human path it came from, "" for the drive root
	Size       int64  `json:"size"`        // bytes, 0 for a folder
	DeletedAt  int64  `json:"deleted_at"`  // unix millis
	PurgeAfter int64  `json:"purge_after"` // unix millis
}

// ListTrash returns everything still restorable, most recently deleted first.
//
// It is a pure read: purging is the background sweep's job (app_trash_sweep.go)
// precisely so that opening the Trash panel can never block on Telegram. An
// entry whose window has closed but that the sweep has not reached yet is still
// listed, and is still restorable, which is the lenient side to err on.
func (a *App) ListTrash() ([]TrashEntry, error) {
	svc, err := a.requireFileService()
	if err != nil {
		return nil, err
	}
	listings, err := svc.ListTrash(a.ActiveChannelID())
	if err != nil {
		return nil, err
	}
	view := make([]TrashEntry, 0, len(listings))
	for _, listing := range listings {
		view = append(view, TrashEntry{
			ObjectID:   listing.ObjectID,
			Kind:       listing.ObjectKind,
			Name:       listing.OriginalName,
			ParentPath: listing.ParentPath,
			Size:       listing.Size,
			DeletedAt:  listing.DeletedAt * millisPerSecond,
			PurgeAfter: listing.PurgeAfter * millisPerSecond,
		})
	}
	return view, nil
}

// RestoreFromTrash puts one object back. It lands under its original parent, or
// under the drive root if that parent is gone, and takes a numbered name if the
// original is occupied -- never refusing outright, because a refusal would
// leave the user with no way to recover the object at all.
func (a *App) RestoreFromTrash(objectID string) OperationResult {
	svc, err := a.requireFileService()
	if err != nil {
		return operationFailure(err)
	}
	if err := svc.RestoreObject(a.ctx, a.ActiveChannelID(), objectID); err != nil {
		return operationFailure(err)
	}
	return operationSuccess()
}

// DeleteFromTrashPermanently destroys one object's Telegram bodies for good.
// This is the user's explicit instruction, so it does not wait for the object's
// retention window; that window only ever gates the automatic sweep.
func (a *App) DeleteFromTrashPermanently(objectID string) OperationResult {
	svc, err := a.requireFileService()
	if err != nil {
		return operationFailure(err)
	}
	if err := svc.PurgeObject(a.ctx, a.ActiveChannelID(), objectID); err != nil {
		return operationFailure(err)
	}
	return operationSuccess()
}

// EmptyTrash purges every entry. It stops at the first failure rather than
// carrying on, so the trash the user sees afterwards is exactly what is left to
// deal with instead of an arbitrary subset.
func (a *App) EmptyTrash() OperationResult {
	svc, err := a.requireFileService()
	if err != nil {
		return operationFailure(err)
	}
	channelID := a.ActiveChannelID()
	listings, err := svc.ListTrash(channelID)
	if err != nil {
		return operationFailure(err)
	}
	for _, listing := range listings {
		// Purging a folder also destroys anything trashed inside it, so an
		// entry can legitimately be gone by the time the loop reaches it.
		if err := svc.PurgeObject(a.ctx, channelID, listing.ObjectID); err != nil &&
			!errors.Is(err, projection.ErrTrashEntryNotFound) {
			return operationFailure(err)
		}
	}
	return operationSuccess()
}
