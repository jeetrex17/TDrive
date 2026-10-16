package file

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sync"

	"TDrive/backend/tgclient"
)

// UploadFailure identifies one input in a batch, even when names repeat.
// Retryable permits a new upload only when no visible send remains unresolved.
type UploadFailure struct {
	Index     int    `json:"index"`
	Name      string `json:"name"`
	Message   string `json:"message"`
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
}

var (
	errUploadResumable = errors.New("upload has a resumable job")
	errUploadSource    = errors.New("upload source unavailable")
)

// NotSentFailures reports every input of a batch that failed before any send
// began. Nothing reached Telegram, so a fresh upload is safe.
func NotSentFailures(filePaths []string, err error) []UploadFailure {
	failures := make([]UploadFailure, 0, len(filePaths))
	for index, path := range filePaths {
		failures = append(failures, notSentFailure(index, filepath.Base(path), err))
	}
	return failures
}

func notSentFailure(index int, name string, err error) UploadFailure {
	return UploadFailure{Index: index, Name: name, Message: err.Error(), Code: "not_sent", Retryable: true}
}

// UploadWithFailures retains the ordinary upload contract and additionally
// reports every failed input. Its outcome tracking belongs to this call only;
// it does not create a durable ordinary-upload retry journal.
func (s *Service) UploadWithFailures(ctx context.Context, channelID int64, filePaths, parentIDs []string, encrypt bool) ([]Metadata, []UploadFailure, error) {
	observer := &failureUploadObserver{uploadObserver: detailedUploadObserver{service: s}, failures: make(map[int]UploadFailure), started: make(map[int]bool), completed: make(map[int]bool), ctx: ctx}
	files, err := s.upload(ctx, channelID, filePaths, parentIDs, encrypt, uploadOptions{observer: observer, resumable: true, pausable: true})
	failures := make([]UploadFailure, 0, len(observer.failures))
	for index, path := range filePaths {
		if failure, ok := observer.failures[index]; ok {
			failures = append(failures, failure)
		} else if err != nil && !observer.completed[index] {
			// Started precedes every send, so an input without it never left
			// the machine. One that started and reported nothing stays closed.
			failure := classifyUploadFailure(ctx, index, filepath.Base(path), err)
			failure.Retryable = false
			if !observer.started[index] {
				failure = notSentFailure(index, filepath.Base(path), err)
			}
			failures = append(failures, failure)
		}
	}
	return files, failures, err
}

type failureUploadObserver struct {
	uploadObserver
	ctx       context.Context
	mu        sync.Mutex
	failures  map[int]UploadFailure
	started   map[int]bool
	completed map[int]bool
}

func (o *failureUploadObserver) Started(id int, name string, size int64, parentID string) {
	o.mu.Lock()
	o.started[id] = true
	o.mu.Unlock()
	o.uploadObserver.Started(id, name, size, parentID)
}

func (o *failureUploadObserver) Accepted(id int) {
	if accepted, ok := o.uploadObserver.(uploadAcceptedObserver); ok {
		accepted.Accepted(id)
	}
}

func (o *failureUploadObserver) Completed(id int, name string) {
	o.mu.Lock()
	o.completed[id] = true
	delete(o.failures, id)
	o.mu.Unlock()
	o.uploadObserver.Completed(id, name)
}

func (o *failureUploadObserver) Failed(id int, name string, err error) {
	o.mu.Lock()
	o.failures[id] = classifyUploadFailure(o.ctx, id, name, err)
	o.mu.Unlock()
	o.uploadObserver.Failed(id, name, err)
}

func classifyUploadFailure(ctx context.Context, index int, name string, err error) UploadFailure {
	failure := UploadFailure{Index: index, Name: name, Message: err.Error(), Code: "upload_failed"}
	switch {
	case errors.Is(err, ErrEmptyFile):
		failure.Code = "empty_file"
	case errors.Is(err, errUploadProjection):
		failure.Code = "projection_pending"
	case errors.Is(err, errUploadResumable):
		failure.Code = "resumable_upload"
	case errors.Is(err, tgclient.ErrSendOutcomeUnknown):
		failure.Code = "outcome_unknown"
	case ctx.Err() != nil:
		failure.Code = "canceled"
	case errors.Is(err, errUploadSource) && (errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission)):
		failure.Code = "source_unavailable"
		failure.Retryable = true
	case tgclient.IsTransientTransport(err):
		failure.Code = "temporary_failure"
		failure.Retryable = true
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		failure.Code = "canceled"
	}
	return failure
}
