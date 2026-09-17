package file

import (
	"bytes"
	"context"
	"fmt"

	"TDrive/backend/projection"
	"TDrive/backend/thumbnail"
)

// UploadBackup shares the upload pipeline and its global concurrency budget,
// but cancellation belongs to the durable backup worker. It deliberately does
// not register a manual-transfer row ID: both queues can be active at once.
// A positive receipt is returned even if local projection fails after Telegram
// commits. Callers must retain that receipt instead of uploading a second copy.
func (s *Service) UploadBackup(ctx context.Context, channelID int64, path, parentID string, encrypt bool, progress ...func(BackupUploadProgress)) (Metadata, error) {
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
	release, err := s.acquireUploadSlot(ctx)
	if err != nil {
		return Metadata{}, err
	}
	defer release()
	observer := &backupUploadObserver{}
	defer func() { clear(observer.snapshot) }()
	if len(progress) > 0 {
		observer.report = progress[0]
	}
	meta, op, header, uploadErr := s.uploadSingleWithObserver(ctx, 0, path, parentID, channelID, encrypt, peer, observer)
	if meta.MsgID == 0 {
		return meta, uploadErr
	}
	if err := s.projectUploaded(channelID, actorID, []uploadedResult{{Meta: meta, Op: op, RawHeader: header}}, observer); err != nil {
		return meta, err
	}
	// Keep the original receipt authoritative even if optional preview creation
	// fails. Use the still-local source so encrypted backups need no later
	// original download merely to populate a gallery tile.
	if encrypt && thumbnail.IsImage(meta.Name) && len(observer.snapshot) > 0 {
		if err := s.prepareBackupRenditions(ctx, channelID, int64(meta.MsgID), observer.snapshot); err != nil {
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
	o.snapshot = append([]byte(nil), snapshot...)
}
