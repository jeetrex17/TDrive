package app

import (
	"context"
	"log/slog"
	"runtime"
	"time"

	"TDrive/backend/photobackup"
	fileservice "TDrive/backend/services/file"
)

func (a *App) runPhotoBackupCycle(ctx context.Context, engine *photobackup.Engine, scope photobackup.Scope) {
	logPhotoBackupRunStatus(ctx, engine, scope, "starting", 0)
	svc, err := a.requireFileService()
	if err != nil {
		slog.Warn("photo backup: file service unavailable", "error", err)
		return
	}
	timings := &photoBackupTimings{}
	runCtx := fileservice.WithBackupUploadTiming(ctx, timings.observeUpload)
	renditions := svc.NewBackupRenditionWorker(runCtx, scope.DriveID)
	defer renditions.Close()
	budget := newPhotoBackupBudget()
	started := time.Now()
	uploaded := 0
	for ctx.Err() == nil {
		// A native background lease may finish the item that was already in
		// flight, but a suspended WebView cannot safely discover or stage the
		// next one. Foreground resume restarts this durable queue.
		if !a.photoBackupMayStartNextJob() {
			break
		}
		done, runErr := runPhotoBackupQueue(runCtx, photoBackupConcurrency, a.photoBackupMayStartNextJob, func(workerCtx context.Context) (photobackup.RunResult, error) {
			jobStarted := time.Now()
			var uploadElapsed time.Duration
			var size int64
			result, err := engine.RunNext(workerCtx, scope, func(uploadCtx context.Context, request photobackup.UploadRequest) (photobackup.UploadResult, error) {
				uploadStarted := time.Now()
				size = request.Asset.Size
				defer func() { uploadElapsed = time.Since(uploadStarted) }()
				return a.uploadPhotoBackupWithRenditions(uploadCtx, request, renditions, budget, timings)
			})
			timings.observe(backupLedger, max(0, time.Since(jobStarted)-uploadElapsed))
			if result.Completed {
				timings.mu.Lock()
				timings.completedBytes += max(0, size)
				timings.mu.Unlock()
			}
			return result, err
		})
		uploaded += done
		if runErr != nil {
			slog.Warn("photo backup: run stopped", "drive_id", scope.DriveID, "uploaded", uploaded, "error", runErr)
			break
		}
		if !a.photoBackupMayStartNextJob() || runtime.GOOS == "ios" || runtime.GOOS == "android" || !a.discoverDesktopPhotoBackup(ctx) {
			break
		}
	}
	originalsElapsed := time.Since(started)
	// Original receipts are already durable. Drain optional work separately;
	// cancellation on pause/lock/logout still joins both rendition workers.
	renditions.Wait()
	timings.log(originalsElapsed, uploaded)
	slog.Info("photo backup: resource summary", "staging_peak_reserved_bytes", budget.peak, "with_previews_elapsed_ms", time.Since(started).Milliseconds())
	logPhotoBackupRunStatus(ctx, engine, scope, "finished", uploaded)
}
