package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"TDrive/backend/photobackup"
	fileservice "TDrive/backend/services/file"
)

func (a *App) uploadPhotoBackup(ctx context.Context, request photobackup.UploadRequest) (photobackup.UploadResult, error) {
	return a.uploadPhotoBackupWithRenditions(ctx, request, nil, newPhotoBackupBudget(), nil)
}

func (a *App) uploadPhotoBackupWithRenditions(ctx context.Context, request photobackup.UploadRequest, renditions *fileservice.BackupRenditionWorker, budget *photoBackupBudget, timings *photoBackupTimings) (result photobackup.UploadResult, resultErr error) {
	sendAttempted := false
	defer func() {
		if !sendAttempted && ctx.Err() != nil {
			resultErr = errors.Join(photobackup.ErrUploadNotStarted, resultErr, ctx.Err())
		}
	}()
	engine, err := a.photoBackupEngine()
	if err != nil {
		return photobackup.UploadResult{}, err
	}
	settings, err := engine.GetSettings(ctx, request.Scope)
	if err != nil {
		return photobackup.UploadResult{}, err
	}
	if err := a.photoBackupPolicyAllows(settings); err != nil {
		return photobackup.UploadResult{}, errors.Join(photobackup.ErrUploadNotStarted, err)
	}
	waitStarted := time.Now()
	releaseBudget, err := budget.acquire(ctx, request.Asset.Size)
	timings.observe(backupBudgetWait, time.Since(waitStarted))
	if err != nil {
		return photobackup.UploadResult{}, err
	}
	defer releaseBudget()
	if !a.photoBackupMayStartNextJob() {
		return photobackup.UploadResult{}, photobackup.ErrUploadNotStarted
	}
	progress, finishProgress := a.beginPhotoBackupProgress(ctx, request.Scope, request.Asset.Name, request.Asset.Size)
	defer finishProgress()
	stageStarted := time.Now()
	path := request.Asset.Path
	var token string
	if path == "" {
		token = randomPhotoBackupToken()
		wait := make(chan photoBackupMaterialization, 1)
		a.photoBackupMu.Lock()
		a.photoBackupWaiters[token] = wait
		a.photoBackupMu.Unlock()
		sourceDTO := photoBackupSourceDTO(request.Source)
		assetDTO := photoBackupAssetDTO(request.Asset)
		a.emit("photo-backup:materialize", map[string]any{"token": token, "source": sourceDTO, "asset": assetDTO})
		defer func() {
			a.emit("photo-backup:release", map[string]any{"token": token, "source": sourceDTO, "asset": assetDTO, "path": path})
		}()
		select {
		case <-ctx.Done():
			a.removePhotoBackupWaiter(token)
			return photobackup.UploadResult{}, ctx.Err()
		case result := <-wait:
			if result.err != "" {
				return photobackup.UploadResult{}, errors.New(result.err)
			}
			path = result.path
		case <-time.After(30 * time.Minute):
			a.removePhotoBackupWaiter(token)
			return photobackup.UploadResult{}, fmt.Errorf("photo backup: materialization timed out")
		}
	}
	if err := validatePhotoBackupPath(path, request.Asset, request.Source); err != nil {
		return photobackup.UploadResult{}, err
	}
	if request.Asset.Path != "" {
		staged, cleanup, stageErr := stagePhotoBackupFile(ctx, path, request.Asset)
		if stageErr != nil {
			return photobackup.UploadResult{}, stageErr
		}
		defer cleanup()
		path = staged
	}
	timings.observe(backupMaterialize, time.Since(stageStarted))
	svc, err := a.requireFileService()
	if err != nil {
		return photobackup.UploadResult{}, err
	}
	destinationStarted := time.Now()
	destinationParentID, err := a.resolvePhotoBackupUploadParent(ctx, request)
	timings.observe(backupDestination, time.Since(destinationStarted))
	if err != nil {
		return photobackup.UploadResult{}, err
	}
	// Photo backup is never allowed to inherit a legacy plaintext setting. The
	// upload boundary forces encryption even for work queued by an older build.
	if !a.photoBackupMayStartNextJob() {
		return photobackup.UploadResult{}, photobackup.ErrUploadNotStarted
	}
	if err := a.photoBackupPolicyAllows(settings); err != nil {
		return photobackup.UploadResult{}, errors.Join(photobackup.ErrUploadNotStarted, err)
	}
	sendAttempted = true
	uploadStarted := time.Now()
	meta, err := svc.UploadBackupWithRenditions(ctx, request.ChannelID, path, destinationParentID, photoBackupEncrypted, renditions, progress)
	timings.observe(backupOriginal, time.Since(uploadStarted))
	if meta.MsgID > 0 {
		return photobackup.UploadResult{RemoteMessageID: int64(meta.MsgID)}, nil
	}
	if errors.Is(err, fileservice.ErrBackupUploadNotStarted) {
		return photobackup.UploadResult{}, errors.Join(photobackup.ErrUploadNotStarted, err)
	}
	return photobackup.UploadResult{}, err
}
