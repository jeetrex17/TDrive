package app

import (
	"errors"

	fileservice "TDrive/backend/services/file"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// ImportRetryResult adds reusable session sources to the operation envelope.
type ImportRetryResult struct {
	Result     OperationResult               `json:"result"`
	RetryFiles []fileservice.ImportRetryFile `json:"retryFiles"`
}

// ImportPathsWithRetryForDrive runs the queued import and returns the eligible
// local failures for Activity. Mobile imports retain their existing behavior.
func (a *App) ImportPathsWithRetryForDrive(paths []string, parentID string, encrypt, extractArchives bool, expectedChannelID int64, fileConflict string) ImportRetryResult {
	var result ImportRetryResult
	var retryFiles *[]fileservice.ImportRetryFile
	if !application.System.IsMobile() {
		retryFiles = &result.RetryFiles
	}
	result.Result = a.importPathsForDrive(paths, parentID, encrypt, extractArchives, expectedChannelID, fileConflict, retryFiles)
	return result
}

// RetryImportedFilesForDrive runs a fresh batch of checked local sources in the
// original drive, using each entry's resolved destination folder.
func (a *App) RetryImportedFilesForDrive(sources []fileservice.ImportRetryFile, encrypt bool, expectedChannelID int64) UploadResult {
	if application.System.IsMobile() {
		return UploadResult{Result: operationFailure(errors.New("folder upload retry is only available on desktop"))}
	}
	if expectedChannelID == 0 || a.ActiveChannelID() != expectedChannelID {
		err := errors.New("upload destination changed")
		return importRetryNotSent(sources, operationFailureMessage(err, "Switch back to the original drive before retrying these files"))
	}
	svc, err := a.requireFileService()
	if err != nil {
		return importRetryNotSent(sources, operationFailure(err))
	}
	ctx, finish, err := a.transfers.resumeUpload(a.ctx)
	if err != nil {
		return importRetryNotSent(sources, operationFailure(err))
	}
	defer finish()
	files, failures, err := svc.RetryImportedFiles(ctx, expectedChannelID, sources, encrypt)
	a.sweepOrphanParts(ctx, expectedChannelID)
	return uploadOperationResult(files, failures, err)
}

func importRetryNotSent(sources []fileservice.ImportRetryFile, result OperationResult) UploadResult {
	paths := make([]string, len(sources))
	for i, source := range sources {
		paths[i] = source.Path
	}
	return UploadResult{Result: result, Failures: fileservice.NotSentFailures(paths, result.Err())}
}
