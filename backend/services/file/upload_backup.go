package file

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/thumbnail"
)

// ErrBackupUploadNotStarted accompanies cancellation only when no Telegram
// body-send call was attempted. The backup ledger may return that work to pending
// without treating it as an ambiguous remote outcome.
var ErrBackupUploadNotStarted = errors.New("backup upload did not start")

type backupSendAttemptKey struct{}

func markBackupSendAttempt(ctx context.Context) {
	if attempted, ok := ctx.Value(backupSendAttemptKey{}).(*atomic.Bool); ok {
		attempted.Store(true)
	}
}

// UploadBackup shares the upload pipeline and its global concurrency budget,
// but cancellation belongs to the durable backup worker. It deliberately does
// not register a manual-transfer row ID: both queues can be active at once.
// A positive receipt is returned even if local projection fails after Telegram
// commits. Callers must retain that receipt instead of uploading a second copy.
func (s *Service) UploadBackup(ctx context.Context, channelID int64, path, parentID string, encrypt bool, progress ...func(BackupUploadProgress)) (Metadata, error) {
	return s.uploadBackup(ctx, channelID, path, parentID, encrypt, nil, progress...)
}

// UploadBackupWithRenditions publishes the original without waiting for optional
// previews. The run-scoped worker owns accepted immutable snapshot copies.
func (s *Service) UploadBackupWithRenditions(ctx context.Context, channelID int64, path, parentID string, encrypt bool, worker *BackupRenditionWorker, progress ...func(BackupUploadProgress)) (Metadata, error) {
	return s.uploadBackup(ctx, channelID, path, parentID, encrypt, worker, progress...)
}

func (s *Service) uploadBackup(ctx context.Context, channelID int64, path, parentID string, encrypt bool, worker *BackupRenditionWorker, progress ...func(BackupUploadProgress)) (meta Metadata, err error) {
	var attempted atomic.Bool
	ctx = context.WithValue(ctx, backupSendAttemptKey{}, &attempted)
	defer func() {
		if meta.MsgID == 0 && (ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) && !attempted.Load() {
			err = errors.Join(ErrBackupUploadNotStarted, err)
		}
	}()
	if err := ctx.Err(); err != nil {
		return Metadata{}, err
	}
	if err := s.ready(); err != nil {
		return Metadata{}, err
	}
	if s.TG == nil || s.Peers == nil || s.ActorID == nil || channelID <= 0 {
		return Metadata{}, fmt.Errorf("backup upload: drive connection unavailable")
	}
	actorID, err := s.ActorID(ctx)
	if err != nil {
		return Metadata{}, err
	}
	peer, err := s.Peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return Metadata{}, err
	}
	var reservation *backupRenditionReservation
	if worker != nil && encrypt && thumbnail.IsImage(path) {
		info, err := os.Stat(path)
		if err != nil {
			return Metadata{}, err
		}
		if info.Size() > 0 && info.Size() <= backupRenditionSnapshotBytes {
			started := time.Now()
			reservation, err = worker.reserve(ctx, channelID, int(info.Size()))
			reportBackupTiming(ctx, "preview_admission", started)
			if err != nil {
				return Metadata{}, err
			}
			defer reservation.release()
		}
	}
	release, err := s.acquireUploadSlot(ctx)
	if err != nil {
		return Metadata{}, err
	}
	observer := &backupUploadObserver{}
	defer func() { clear(observer.snapshot) }()
	if len(progress) > 0 {
		observer.report = progress[0]
	}
	meta, op, header, uploadErr := s.uploadSingleWithObserver(ctx, 0, path, parentID, channelID, encrypt, peer, observer, false)
	release()
	if meta.MsgID == 0 {
		return meta, uploadErr
	}
	projectionStarted := time.Now()
	projectionErr := s.projectUploaded(channelID, actorID, []uploadedResult{{Meta: meta, Op: op, RawHeader: header}}, observer)
	reportBackupTiming(ctx, "projection", projectionStarted)
	if projectionErr != nil {
		return meta, projectionErr
	}
	// Keep the original receipt authoritative even if optional preview creation
	// fails. Use the still-local source so encrypted backups need no later
	// original download merely to populate a gallery tile.
	if encrypt && thumbnail.IsImage(meta.Name) && len(observer.snapshot) > 0 {
		if worker != nil {
			// Pin the exact uploaded body, not a lookup performed later after a
			// possible mounted replacement. Small originals use MsgID identity.
			source := projection.File{ChannelID: channelID, MsgID: int64(meta.MsgID), Name: meta.Name, Size: meta.Size, PlaintextSize: meta.PlaintextSize, Encrypted: meta.Encrypted, UploadUUID: op.UploadUUID}
			if source.UploadUUID == "" && op.Type == "" {
				// Multipart success already projected its manifest internally.
				if projected, found, err := projection.FileByID(s.DB, channelID, int64(meta.MsgID)); err == nil && found {
					source.UploadUUID = projected.UploadUUID
				}
			}
			if reservation != nil && reservation.handoff(source, observer.snapshot) {
				observer.snapshot = nil // worker now owns and clears the copy
			} else if ctx.Err() == nil {
				s.warnf("Backup preview deferred: source changed or preparation stopped for file %d\n", meta.MsgID)
			}
		} else if err := s.prepareBackupRenditions(ctx, channelID, int64(meta.MsgID), observer.snapshot); err != nil {
			s.warnf("Backup preview preparation failed for file %d: %v\n", meta.MsgID, err)
		}
	}
	return meta, nil
}

func (s *Service) prepareBackupRenditions(ctx context.Context, channelID, msgID int64, snapshot []byte) error {
	source, found, err := projection.FileByID(s.DB, channelID, msgID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("backup preview source unavailable")
	}
	return s.PrepareRenditions(ctx, source, bytes.NewReader(snapshot))
}

// BackupUploadProgress describes one active upload, independent of manual
// transfer IDs. Percent is transport progress; completion still needs a receipt.
type BackupUploadProgress struct {
	Name       string
	BytesTotal int64
	Percent    float64
}

type backupUploadObserver struct {
	snapshot []byte
	current  BackupUploadProgress
	report   func(BackupUploadProgress)
}

func (o *backupUploadObserver) Started(_ int, name string, size int64, _ string) {
	o.current = BackupUploadProgress{Name: name, BytesTotal: size}
	o.publish()
}
func (o *backupUploadObserver) Progress(_ int, percent float64) {
	o.current = BackupUploadProgress{Name: o.current.Name, BytesTotal: o.current.BytesTotal, Percent: percent}
	o.publish()
}
func (o *backupUploadObserver) publish() {
	if o.report != nil {
		o.report(o.current)
	}
}
func (*backupUploadObserver) Completed(int, string)     {}
func (*backupUploadObserver) Failed(int, string, error) {}

// The uploader clears its snapshot on return. This bounded owned copy survives
// just long enough to publish encrypted derivatives, then UploadBackup clears it.
func (o *backupUploadObserver) CapturePhotoSnapshot(snapshot []byte) {
	clear(o.snapshot)
	o.snapshot = bytes.Clone(snapshot)
}
