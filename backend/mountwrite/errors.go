package mountwrite

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrInvalidRequest       = errors.New("invalid request")
	ErrForbidden            = errors.New("forbidden")
	ErrNotFound             = errors.New("not found")
	ErrConflict             = errors.New("conflict")
	ErrPreconditionFailed   = errors.New("precondition failed")
	ErrTooLarge             = errors.New("content too large")
	ErrLocked               = errors.New("locked")
	ErrQuotaExceeded        = errors.New("quota exceeded")
	ErrUnavailable          = errors.New("unavailable")
	ErrCanceled             = errors.New("canceled")
	ErrLengthMismatch       = errors.New("content length mismatch")
	ErrDraining             = errors.New("coordinator draining")
	ErrBusy                 = errors.New("coordinator busy")
	ErrOperationExists      = errors.New("operation already exists")
	ErrOperationInProgress  = errors.New("operation in progress")
	ErrJournalConflict      = errors.New("journal compare-and-swap conflict")
	ErrInvalidTransition    = errors.New("invalid journal transition")
	ErrCommitOutcomeUnknown = errors.New("remote commit outcome unknown")
)

type OperationError struct {
	OperationID string
	Kind        MutationKind
	code        error
}

func (e *OperationError) Error() string {
	if !validOperationID(e.OperationID) || e.OperationID == "" {
		return fmt.Sprintf("mount write %s %s", e.Kind, safeErrorLabel(e.code))
	}
	return fmt.Sprintf("mount write %s %s (operation %s)", e.Kind, safeErrorLabel(e.code), e.OperationID)
}

func (e *OperationError) Unwrap() error {
	return e.code
}

func newOperationError(operationID string, kind MutationKind, cause error) error {
	if cause == nil {
		return nil
	}
	return &OperationError{
		OperationID: operationID,
		Kind:        kind,
		code:        classifyError(cause),
	}
}

func classifyError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrCanceled) {
		return ErrCanceled
	}
	known := []error{
		ErrInvalidRequest,
		ErrForbidden,
		ErrNotFound,
		ErrConflict,
		ErrPreconditionFailed,
		ErrTooLarge,
		ErrLocked,
		ErrQuotaExceeded,
		ErrLengthMismatch,
		ErrDraining,
		ErrBusy,
		ErrOperationExists,
		ErrOperationInProgress,
		ErrJournalConflict,
		ErrInvalidTransition,
	}
	for _, candidate := range known {
		if errors.Is(err, candidate) {
			return candidate
		}
	}
	return ErrUnavailable
}

func safeErrorLabel(err error) string {
	switch {
	case errors.Is(err, ErrInvalidRequest):
		return "invalid"
	case errors.Is(err, ErrForbidden):
		return "forbidden"
	case errors.Is(err, ErrNotFound):
		return "not found"
	case errors.Is(err, ErrConflict):
		return "conflicted"
	case errors.Is(err, ErrPreconditionFailed):
		return "precondition failed"
	case errors.Is(err, ErrTooLarge):
		return "too large"
	case errors.Is(err, ErrLocked):
		return "locked"
	case errors.Is(err, ErrQuotaExceeded):
		return "out of space"
	case errors.Is(err, ErrCanceled):
		return "canceled"
	case errors.Is(err, ErrLengthMismatch):
		return "length mismatch"
	case errors.Is(err, ErrDraining):
		return "draining"
	case errors.Is(err, ErrBusy):
		return "busy"
	case errors.Is(err, ErrOperationExists), errors.Is(err, ErrOperationInProgress):
		return "already active"
	default:
		return "unavailable"
	}
}
