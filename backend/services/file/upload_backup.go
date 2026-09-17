package file

import (
	"context"
	"fmt"
)

// UploadBackup shares the upload pipeline and its global concurrency budget,
// but cancellation belongs to the durable backup worker. It deliberately does
// not register a manual-transfer row ID: both queues can be active at once.
// A positive receipt is returned even if local projection fails after Telegram
// commits. Callers must retain that receipt instead of uploading a second copy.
func (s *Service) UploadBackup(ctx context.Context, channelID int64, path, parentID string, encrypt bool) (Metadata, error) {
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
	observer := backupUploadObserver{}
	meta, op, header, uploadErr := s.uploadSingleWithObserver(ctx, 0, path, parentID, channelID, encrypt, peer, observer)
	if meta.MsgID == 0 {
		return meta, uploadErr
	}
	if err := s.projectUploaded(channelID, actorID, []uploadedResult{{Meta: meta, Op: op, RawHeader: header}}, observer); err != nil {
		return meta, err
	}
	return meta, nil
}

// Backup progress comes from persisted job transitions, independent of the
// manual upload event stream. Byte-level progress can be added to that stream
// without allocating one frontend transfer row per library item.
type backupUploadObserver struct{}

func (backupUploadObserver) Started(int, string, int64, string) {}
func (backupUploadObserver) Progress(int, float64)              {}
func (backupUploadObserver) Completed(int, string)              {}
func (backupUploadObserver) Failed(int, string, error)          {}
