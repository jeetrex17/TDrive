package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	fileservice "TDrive/backend/services/file"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// ListResumableDownloads returns only jobs for the requested drive. The file
// service also scopes its journal to the signed-in account.
func (a *App) ListResumableDownloads(channelID int64) ResumableDownloadsResult {
	if !validFrontendDownloadID(channelID) {
		return ResumableDownloadsResult{Result: operationFailure(errors.New("invalid drive ID"))}
	}
	svc, err := a.requireFileService()
	if err != nil {
		return ResumableDownloadsResult{Result: operationFailure(err)}
	}
	jobs, err := svc.ListResumableDownloads(a.appContext())
	if err != nil {
		return ResumableDownloadsResult{Result: operationFailure(err)}
	}
	visible := make([]fileservice.ResumableDownload, 0, len(jobs))
	for _, job := range jobs {
		if job.ChannelID == channelID {
			visible = append(visible, job)
		}
	}
	return ResumableDownloadsResult{Result: operationSuccess(), Jobs: visible}
}

// ResumeDownload continues the pinned source and destination of an existing job.
// The caller's current drive never retargets the transfer.
func (a *App) ResumeDownload(channelID int64, jobID, requestID string) DownloadResult {
	if !validFrontendDownloadID(channelID) || !validFrontendDownloadRequestID(jobID) || !validFrontendDownloadRequestID(requestID) {
		return DownloadResult{Result: operationFailure(errors.New("invalid download request identifiers"))}
	}
	svc, err := a.requireFileService()
	if err != nil {
		return DownloadResult{Result: operationFailure(err)}
	}
	job, err := downloadJobForChannel(a, svc, channelID, jobID)
	if err != nil {
		return DownloadResult{Result: operationFailure(err)}
	}
	downloadCtx, runID, err := a.beginDownload()
	if err != nil {
		return DownloadResult{Result: operationFailure(err), JobID: jobID}
	}
	defer a.endDownload(runID)
	if job.Status == "needs_destination" {
		path, err := a.chooseDownloadPath(job.Name)
		if err != nil {
			return DownloadResult{Result: operationFailure(err), JobID: jobID}
		}
		if path == "" {
			return DownloadResult{Result: operationFailure(context.Canceled), JobID: jobID}
		}
		if err := svc.ChangeResumableDownloadDestination(downloadCtx, jobID, path); err != nil {
			return DownloadResult{Result: operationFailure(err), JobID: jobID}
		}
	}
	ctx := fileservice.WithDownloadProgressID(downloadCtx, requestID)
	result := svc.ResumeDownload(ctx, jobID)
	shareCompletedIOSDownload(result)
	return downloadOperationResult(result)
}

// PauseResumableDownload retains verified bytes for a later attempt.
func (a *App) PauseResumableDownload(channelID int64, jobID string) OperationResult {
	svc, err := a.downloadJobService(channelID, jobID)
	if err != nil {
		return operationFailure(err)
	}
	if _, err := downloadJobForChannel(a, svc, channelID, jobID); err != nil {
		return operationFailure(err)
	}
	svc.PauseResumableDownload(jobID)
	return operationSuccess()
}

// DiscardResumableDownload removes the job and its private partial bytes.
func (a *App) DiscardResumableDownload(channelID int64, jobID string) OperationResult {
	svc, err := a.downloadJobService(channelID, jobID)
	if err != nil {
		return operationFailure(err)
	}
	if _, err := downloadJobForChannel(a, svc, channelID, jobID); err != nil {
		return operationFailure(err)
	}
	return operationFailure(svc.DiscardResumableDownload(a.appContext(), jobID))
}

func (a *App) downloadJobService(channelID int64, jobID string) (*fileservice.Service, error) {
	if !validFrontendDownloadID(channelID) || !validFrontendDownloadRequestID(jobID) {
		return nil, errors.New("invalid download job identifiers")
	}
	return a.requireFileService()
}

func downloadJobForChannel(a *App, svc *fileservice.Service, channelID int64, jobID string) (fileservice.ResumableDownload, error) {
	jobs, err := svc.ListResumableDownloads(a.appContext())
	if err != nil {
		return fileservice.ResumableDownload{}, err
	}
	for _, job := range jobs {
		if job.JobID == jobID && job.ChannelID == channelID {
			return job, nil
		}
	}
	return fileservice.ResumableDownload{}, fmt.Errorf("%w: download job", fs.ErrNotExist)
}

func shareCompletedIOSDownload(result fileservice.DownloadResult) {
	if result.Status != "success" || result.SavedPath == "" || !application.System.IsPlatform(application.PlatformIOS) {
		return
	}
	if err := shareFileNative(result.SavedPath); err != nil {
		fmt.Printf("Warning: share sheet failed: %v\n", err)
	}
}
