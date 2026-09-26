package main

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"syscall"

	"TDrive/backend"
	"TDrive/backend/mountcontroller"
	"TDrive/backend/mountpolicy"
	encservice "TDrive/backend/services/encryption"
	fileservice "TDrive/backend/services/file"
	"TDrive/backend/tgclient"
)

// Operation error codes are stable control-plane values exposed over Wails.
// The accompanying message is display text and must never be used for branching.
type OperationErrorCode string

const (
	OperationCodeFailed                      = "operation_failed"
	OperationCodeBackendUnavailable          = "backend_unavailable"
	OperationCodeEncryptionPasswordRequired  = "encryption_password_required"
	OperationCodeInvalidEncryptionPassword   = "invalid_encryption_password"
	OperationCodeEncryptionPasswordExists    = "encryption_password_already_set"
	OperationCodeEncryptionPolicyUnavailable = "encryption_policy_unavailable"
	OperationCodeCanceled                    = "canceled"
	OperationCodeDeadlineExceeded            = "deadline_exceeded"
	OperationCodeNotFound                    = "not_found"
	OperationCodePermissionDenied            = "permission_denied"
	OperationCodeAlreadyExists               = "already_exists"
	OperationCodeInsufficientStorage         = "insufficient_storage"
	OperationCodeNetworkUnavailable          = "network_unavailable"
	OperationCodeFileTooLarge                = "file_too_large"
)

var errBackendUnavailable = errors.New("backend not ready")

// OperationError keeps a stable code separate from user-facing detail.
// cause is retained for native callers and deliberately omitted from JSON.
type OperationError struct {
	Code    OperationErrorCode `json:"code"`
	Message string             `json:"message"`
	cause   error
}

func (e *OperationError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *OperationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// OperationResult is the common Wails operation envelope.
type OperationResult struct {
	OK    bool            `json:"ok"`
	Error *OperationError `json:"error,omitempty"`
}

// Err exposes the native error without converting a nil pointer into a non-nil error interface.
func (r OperationResult) Err() error {
	if r.Error == nil {
		return nil
	}
	return r.Error
}

// UploadResult adds uploaded file data to the common operation envelope.
type UploadResult struct {
	Result OperationResult        `json:"result"`
	Files  []backend.FileMetaData `json:"files"`
}

// DownloadResult adds the chosen destination to the common operation envelope.
type DownloadResult struct {
	Result    OperationResult `json:"result"`
	SavedPath string          `json:"saved_path"`
}

// MountResult adds capability-free mount state to the common operation envelope.
type MountResult struct {
	Result OperationResult `json:"result"`
	Mount  MountView       `json:"mount"`
}

func (r MountResult) Err() error {
	return r.Result.Err()
}

func mountOperationResult(mount MountView, err error) MountResult {
	if err != nil {
		return MountResult{Result: operationFailure(err), Mount: mount}
	}
	return MountResult{Result: operationSuccess(), Mount: mount}
}

// PreviewResult adds preview bytes to the common operation envelope.
type PreviewResult struct {
	Result  OperationResult `json:"result"`
	Payload PreviewPayload  `json:"payload"`
}

func previewOperationResult(payload PreviewPayload, err error) PreviewResult {
	if err != nil {
		return PreviewResult{Result: operationFailure(err)}
	}
	return PreviewResult{Result: operationSuccess(), Payload: payload}
}

func operationSuccess() OperationResult {
	return OperationResult{OK: true}
}

func operationFailure(err error) OperationResult {
	if err == nil {
		return operationSuccess()
	}
	return operationFailureMessage(err, err.Error())
}

func operationFailureMessage(err error, message string) OperationResult {
	if err == nil {
		err = errors.New(message)
	}
	if message == "" {
		message = err.Error()
	}
	return OperationResult{
		OK: false,
		Error: &OperationError{
			Code:    operationErrorCode(err),
			Message: message,
			cause:   err,
		},
	}
}

func operationErrorCode(err error) OperationErrorCode {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errBackendUnavailable):
		return OperationCodeBackendUnavailable
	case errors.Is(err, encservice.ErrPasswordRequired), errors.Is(err, mountcontroller.ErrEncryptionPasswordRequired):
		return OperationCodeEncryptionPasswordRequired
	case errors.Is(err, encservice.ErrWrongPassword):
		return OperationCodeInvalidEncryptionPassword
	case errors.Is(err, encservice.ErrPasswordAlreadySet):
		return OperationCodeEncryptionPasswordExists
	case errors.Is(err, mountpolicy.ErrEncryptionPolicyUnavailable):
		return OperationCodeEncryptionPolicyUnavailable
	case errors.Is(err, context.Canceled):
		return OperationCodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return OperationCodeDeadlineExceeded
	case errors.Is(err, tgclient.ErrMessageNotFound), errors.Is(err, sql.ErrNoRows), errors.Is(err, fs.ErrNotExist):
		return OperationCodeNotFound
	case errors.Is(err, fs.ErrPermission):
		return OperationCodePermissionDenied
	case errors.Is(err, fs.ErrExist):
		return OperationCodeAlreadyExists
	case errors.Is(err, syscall.ENOSPC):
		return OperationCodeInsufficientStorage
	case errors.Is(err, fileservice.ErrFileTooLarge):
		return OperationCodeFileTooLarge
	case errors.Is(err, tgclient.ErrFloodWait), tgclient.IsTransientTransport(err):
		return OperationCodeNetworkUnavailable
	default:
		return OperationCodeFailed
	}
}

func downloadOperationResult(result fileservice.DownloadResult) DownloadResult {
	if result.Status == "success" {
		return DownloadResult{Result: operationSuccess(), SavedPath: result.SavedPath}
	}
	err := result.Err
	if err == nil {
		err = errors.New(result.Message)
	}
	return DownloadResult{
		Result:    operationFailureMessage(err, result.Message),
		SavedPath: result.SavedPath,
	}
}
