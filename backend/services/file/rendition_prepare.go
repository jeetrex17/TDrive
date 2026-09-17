package file

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"

	"TDrive/backend/projection"
	"TDrive/backend/thumbnail"
)

// PrepareRemoteRenditions is the explicit legacy-preparation boundary. Ordinary
// grid requests never call it. The caller owns the visible job/data budget and
// may cancel between photos. Source bytes returned count actual transfer writes,
// including a partial failed attempt, so an interrupted job can account honestly.
var ErrRenditionPreparationBudget = errors.New("photo changed or exceeds the remaining preparation data budget")

func (s *Service) PrepareRemoteRenditions(ctx context.Context, channelID, msgID int64) (int64, error) {
	return s.PrepareRemoteRenditionsWithinBudget(ctx, channelID, msgID, 30<<20)
}

// PrepareRemoteRenditionsWithinBudget rechecks the authoritative pinned source
// against the bytes admitted by the caller. A replacement between page lookup
// and preparation must never enlarge the user's approved download budget.
func (s *Service) PrepareRemoteRenditionsWithinBudget(ctx context.Context, channelID, msgID, maxBytes int64) (int64, error) {
	if ctx == nil {
		return 0, fmt.Errorf("preparation context required")
	}
	if err := s.ready(); err != nil {
		return 0, err
	}
	if s.TG == nil || s.Peers == nil || s.ActorID == nil {
		return 0, fmt.Errorf("preparation upload dependencies unavailable")
	}
	if err := s.requireOwnerForShared(ctx, channelID, int(msgID), "prepare previews"); err != nil {
		return 0, err
	}
	for _, kind := range []string{projection.RenditionThumbnail, projection.RenditionPreview} {
		_, err := projection.CurrentFileRendition(ctx, s.DB, channelID, msgID, kind)
		if errors.Is(err, sql.ErrNoRows) {
			goto prepare
		}
		if err != nil {
			return 0, err
		}
	}
	return 0, nil
prepare:
	snapshot, found, err := projection.FileDownloadRefContext(ctx, s.DB, channelID, msgID)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, os.ErrNotExist
	}
	const maxPreparationSource = 30 << 20
	if !thumbnail.IsImage(snapshot.Name) {
		return 0, thumbnail.ErrUnsupported
	}
	if snapshot.StoredSize > maxPreparationSource || snapshot.OutputSize > maxPreparationSource {
		return 0, thumbnail.ErrTooLarge
	}
	if maxBytes < 0 || snapshot.StoredSize > maxBytes {
		return 0, ErrRenditionPreparationBudget
	}
	key, err := s.requireEncryptionKey(snapshot.Encrypted)
	clearOwnedKey(key)
	if err != nil {
		return 0, err
	}
	peer, err := s.Peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return 0, err
	}
	stored, err := os.CreateTemp("", "tdrive-rendition-source-*")
	if err != nil {
		return 0, err
	}
	defer func() { stored.Close(); os.Remove(stored.Name()) }()
	// Encrypted originals remain ciphertext on disk. The private temporary file
	// is removed for every success, cancellation and decode-failure path.
	writer := &preparationWriter{dst: stored, remaining: snapshot.StoredSize}
	if len(snapshot.Parts) > 0 {
		for _, part := range snapshot.Parts {
			if err := s.TG.DownloadFile(ctx, peer, part.MsgID, writer, nil); err != nil {
				return writer.written, err
			}
		}
	} else {
		content := snapshot.ContentMsgID
		if content == 0 {
			content = msgID
		}
		if err := s.TG.DownloadFile(ctx, peer, content, writer, nil); err != nil {
			return writer.written, err
		}
	}
	if writer.remaining != 0 {
		return writer.written, io.ErrUnexpectedEOF
	}
	source := projection.File{ChannelID: channelID, MsgID: msgID, Name: snapshot.Name, Size: snapshot.StoredSize, PlaintextSize: snapshot.OutputSize, Encrypted: snapshot.Encrypted, ContentMsgID: snapshot.ContentMsgID, UploadUUID: snapshot.UploadUUID, PartCount: snapshot.PartCount}
	return writer.written, s.PrepareStoredRenditions(ctx, source, stored)
}

type preparationWriter struct {
	dst                io.Writer
	remaining, written int64
}

func (w *preparationWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("preparation source exceeds declared size")
	}
	n, err := w.dst.Write(p)
	w.remaining -= int64(n)
	w.written += int64(n)
	return n, err
}

// ValidateRenditionPreparation performs the no-network start check used by a
// foreground preparation UI. The worker repeats it for each subsequent file;
// losing permission or locking the vault must stop already-running jobs too.
func (s *Service) ValidateRenditionPreparation(ctx context.Context, channelID, msgID int64) error {
	if ctx == nil {
		return fmt.Errorf("preparation context required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.ready(); err != nil {
		return err
	}
	if err := s.requireOwnerForShared(ctx, channelID, int(msgID), "prepare previews"); err != nil {
		return err
	}
	source, found, err := projection.FileByID(s.DB, channelID, msgID)
	if err != nil {
		return err
	}
	if !found {
		return os.ErrNotExist
	}
	key, err := s.requireEncryptionKey(source.Encrypted)
	clearOwnedKey(key)
	return err
}
