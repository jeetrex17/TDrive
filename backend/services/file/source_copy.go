package file

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"TDrive/backend/channelsource"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
	"github.com/google/uuid"
)

const SourceCopyBatchLimit = 50

type SourceCopyInput struct {
	Source channelsource.CopySource `json:"source"`
	Name   string                   `json:"name"`
}

type SourceCopyResult struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parent_id"`
	Status   string `json:"status"`
	FileID   int64  `json:"file_id"`
	Message  string `json:"message"`
}

// SourceCopyPreflightError guarantees no copy intent was recorded.
type SourceCopyPreflightError struct{ error }

func (err SourceCopyPreflightError) Unwrap() error { return err.error }

type SourceCopyDependencies struct {
	Resolve func(context.Context, channelsource.CopySource) (tgclient.DocumentRef, error)
	// Sync must reconcile the destination projection in Telegram message order.
	Sync func(context.Context, int64) error
	// SyncShared establishes the shared drive projection before reading its
	// file revision. Reconciliation of accepted copies uses only My Drive.
	SyncShared func(context.Context, int64) error
}

func NormalizeSourceCopyName(name string) string {
	return projection.PortableFileName(strings.TrimSpace(name), 1)
}

// StartSourceCopies records the selection before attempting any remote writes.
// It only creates plaintext copies. The caller must first confirm that mode.
func (s *Service) StartSourceCopies(ctx context.Context, channelID, accountID int64, parent string, items []SourceCopyInput, deps SourceCopyDependencies, batchID string) ([]SourceCopyResult, error) {
	if !validSourceCopyBatchID(batchID) {
		return nil, SourceCopyPreflightError{errors.New("Invalid copy batch")}
	}
	if err := validateSourceCopySelection(accountID, items); err != nil {
		return nil, SourceCopyPreflightError{err}
	}
	release, err := s.acquireSourceCopySlot(ctx)
	if err != nil {
		return nil, SourceCopyPreflightError{err}
	}
	defer release()
	worker, err := s.newSourceCopyWorker(ctx, channelID, accountID, deps)
	if err != nil {
		return nil, SourceCopyPreflightError{err}
	}
	if err := worker.sync(ctx); err != nil {
		return nil, SourceCopyPreflightError{err}
	}
	parent, err = s.validParent(channelID, parent, "target")
	if err != nil {
		return nil, SourceCopyPreflightError{err}
	}
	jobs, err := s.createSourceCopyJobs(ctx, channelID, accountID, parent, items, batchID)
	if err != nil {
		return nil, err
	}
	results := make([]SourceCopyResult, 0, len(jobs))
	for _, job := range jobs {
		result, err := worker.run(ctx, job)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

func (s *Service) ListSourceCopies(ctx context.Context, channelID, accountID int64) ([]SourceCopyResult, error) {
	if err := s.checkSourceCopyScope(ctx, channelID, accountID); err != nil {
		return nil, err
	}
	return s.listSourceCopyJobs(ctx, channelID, accountID)
}

// ListSourceCopyBatch recovers receipts for one attempted save.
func (s *Service) ListSourceCopyBatch(ctx context.Context, channelID, accountID int64, batchID string) ([]SourceCopyResult, error) {
	if !validSourceCopyBatchID(batchID) {
		return nil, errors.New("Invalid copy batch")
	}
	if err := s.checkSourceCopyScope(ctx, channelID, accountID); err != nil {
		return nil, err
	}
	return s.listSourceCopyBatch(ctx, channelID, accountID, batchID)
}

func validSourceCopyBatchID(batchID string) bool {
	id, err := uuid.Parse(batchID)
	return err == nil && id != uuid.Nil && id.String() == batchID
}

func (s *Service) RetrySourceCopy(ctx context.Context, channelID, accountID int64, id string, deps SourceCopyDependencies) (SourceCopyResult, error) {
	if strings.TrimSpace(id) != id || len(id) != 36 {
		return SourceCopyResult{}, errors.New("Invalid copy operation")
	}
	release, err := s.acquireSourceCopySlot(ctx)
	if err != nil {
		return SourceCopyResult{}, err
	}
	defer release()
	worker, err := s.newSourceCopyWorker(ctx, channelID, accountID, deps)
	if err != nil {
		return SourceCopyResult{}, err
	}
	job, err := s.loadSourceCopyJob(ctx, channelID, accountID, id)
	if err != nil {
		return SourceCopyResult{}, err
	}
	if job.Result.Status != sourceCopyPending {
		return job.result(), nil
	}
	if err := worker.sync(ctx); err != nil {
		return job.result(), err
	}
	return worker.run(ctx, job)
}

func validateSourceCopySelection(accountID int64, items []SourceCopyInput) error {
	if len(items) == 0 || len(items) > SourceCopyBatchLimit {
		return fmt.Errorf("Select between 1 and %d files to save", SourceCopyBatchLimit)
	}
	first := items[0].Source
	seen := make(map[int64]struct{}, len(items))
	for _, item := range items {
		source := item.Source
		if source.AccountID != accountID || !validSourceCopyKind(source, first) ||
			source.PeerID <= 0 || source.PeerID != first.PeerID || source.PeerKind != first.PeerKind ||
			source.Generation == "" || len(source.Generation) > 128 ||
			source.MsgID <= 0 || source.Identity == "" || len(source.Identity) > 128 || len(item.Name) > 4096 {
			return errors.New("Select valid files from one source")
		}
		if _, exists := seen[source.MsgID]; exists {
			return errors.New("Select each file only once")
		}
		seen[source.MsgID] = struct{}{}
	}
	return nil
}

func validSourceCopyKind(source, first channelsource.CopySource) bool {
	if source.PeerKind == SharedCopyPeerKind {
		return ValidateSharedCopySource(source) == nil
	}
	return tgclient.PeerKind(source.PeerKind).Valid() && source.Generation == first.Generation
}

func (s *Service) acquireSourceCopySlot(ctx context.Context) (func(), error) {
	if ctx == nil {
		return nil, errors.New("Copy requires a context")
	}
	s.sourceCopyOnce.Do(func() { s.sourceCopySlot = make(chan struct{}, 1) })
	select {
	case s.sourceCopySlot <- struct{}{}:
		return func() { <-s.sourceCopySlot }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Service) checkSourceCopyScope(ctx context.Context, channelID, accountID int64) error {
	if ctx == nil || s.DB == nil || s.ActorID == nil {
		return errors.New("Save to My Drive is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	actor, err := s.ActorID(ctx)
	if err != nil || accountID <= 0 || actor != accountID {
		return errors.New("The signed-in Telegram account changed")
	}
	if s.PersonalChannelID != nil && s.PersonalChannelID() != channelID {
		return errors.New("The selected My Drive changed")
	}
	channel, err := projection.GetChannel(s.DB, channelID)
	if err != nil || channel.Kind != projection.KindPersonal {
		return errors.New("My Drive is unavailable")
	}
	return nil
}
