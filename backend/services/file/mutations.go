package file

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"TDrive/backend/projection"
	"TDrive/backend/services/servicecontext"
)

func (s *Service) Meta(channelID int64, msgID int, name string, size int64, parentID string) error {
	return s.MetaContext(context.Background(), channelID, msgID, name, size, parentID)
}

func (s *Service) MetaContext(ctx context.Context, channelID int64, msgID int, name string, size int64, parentID string) error {
	if err := servicecontext.Check(ctx, "file: metadata"); err != nil {
		return err
	}
	if err := s.ready(); err != nil {
		return err
	}
	if channelID == 0 {
		return fmt.Errorf("No active channel")
	}
	if msgID <= 0 {
		return fmt.Errorf("Invalid msgID")
	}

	if projection.FileExists(s.DB, channelID, int64(msgID)) {
		return nil
	}

	parent, err := s.validParent(channelID, parentID, "parent")
	if err != nil {
		return err
	}
	cleanName := strings.TrimSpace(name)
	if cleanName == "" {
		cleanName = "Untitled"
	}

	op := projection.Op{
		Type:           projection.OpMeta,
		Obj:            fmt.Sprintf("%s%d", projection.FileIDPrefix, msgID),
		Parent:         parent,
		Name:           cleanName,
		FileSize:       size,
		FileUploadTime: s.now().Unix(),
	}
	_, err = s.emit(ctx, channelID, op)
	return err
}

// requireEncryptedFileKey demands the encryption key before a mutation
// (rename/move/delete) on an encrypted file, so only someone who can decrypt it
// can change it. A locked vault returns the "encryption password required" error
// the frontend prompts on. An unknown encryption state does not block.
func (s *Service) requireEncryptedFileKey(channelID int64, msgID int) error {
	encrypted, _, _, err := projection.FileEncryptionMeta(s.DB, channelID, int64(msgID))
	if err != nil {
		return nil
	}
	masterKey, err := s.requireEncryptionKey(encrypted)
	clearOwnedKey(masterKey)
	if err != nil {
		return err
	}
	return nil
}

func (s *Service) Rename(ctx context.Context, channelID int64, msgID int, newName string) (err error) {
	defer func() {
		if err != nil {
			slog.Error("file: rename failed", "channel_id", channelID, "msg_id", msgID, "error", err)
		} else {
			slog.Debug("file: renamed", "channel_id", channelID, "msg_id", msgID, "new_name", newName)
		}
	}()
	if err := servicecontext.Check(ctx, "file: rename"); err != nil {
		return err
	}
	if err := s.ready(); err != nil {
		return err
	}
	if channelID == 0 {
		return fmt.Errorf("No active channel")
	}
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return fmt.Errorf("Invalid name")
	}
	if !projection.FileExists(s.DB, channelID, int64(msgID)) {
		return fmt.Errorf("File not found")
	}
	if err := s.requireEncryptedFileKey(channelID, msgID); err != nil {
		return err
	}
	if err := s.requireOwnerForShared(ctx, channelID, msgID, "rename"); err != nil {
		return err
	}

	op := projection.Op{
		Type: projection.OpRename,
		Obj:  fmt.Sprintf("%s%d", projection.FileIDPrefix, msgID),
		Name: newName,
	}
	_, err = s.emit(ctx, channelID, op)
	return err
}

func (s *Service) Move(ctx context.Context, channelID int64, msgID int, newParentID string) (err error) {
	defer func() {
		if err != nil {
			slog.Error("file: move failed", "channel_id", channelID, "msg_id", msgID, "error", err)
		} else {
			slog.Debug("file: moved", "channel_id", channelID, "msg_id", msgID, "new_parent_id", newParentID)
		}
	}()
	if err := servicecontext.Check(ctx, "file: move"); err != nil {
		return err
	}
	if err := s.ready(); err != nil {
		return err
	}
	if channelID == 0 {
		return fmt.Errorf("No active channel")
	}
	parent, err := s.validParent(channelID, newParentID, "target")
	if err != nil {
		return err
	}
	cur, err := projection.FileParent(s.DB, channelID, int64(msgID))
	if err != nil {
		return fmt.Errorf("File not found")
	}
	if cur == parent {
		return fmt.Errorf("File is already in this folder")
	}
	if err := s.requireEncryptedFileKey(channelID, msgID); err != nil {
		return err
	}
	if err := s.requireOwnerForShared(ctx, channelID, msgID, "move"); err != nil {
		return err
	}
	op := projection.Op{
		Type:   projection.OpMove,
		Obj:    fmt.Sprintf("%s%d", projection.FileIDPrefix, msgID),
		Parent: parent,
	}
	_, err = s.emit(ctx, channelID, op)
	return err
}

func (s *Service) Delete(ctx context.Context, channelID int64, msgID int) (err error) {
	defer func() {
		if err != nil {
			slog.Error("file: delete failed", "channel_id", channelID, "msg_id", msgID, "error", err)
		} else {
			slog.Debug("file: deleted (tombstoned)", "channel_id", channelID, "msg_id", msgID)
		}
	}()
	if err := servicecontext.Check(ctx, "file: delete"); err != nil {
		return err
	}
	if err := s.ready(); err != nil {
		return err
	}
	if channelID == 0 {
		return fmt.Errorf("Drive ID not found")
	}
	if !projection.FileExists(s.DB, channelID, int64(msgID)) {
		return fmt.Errorf("File not found")
	}

	if err := s.requireEncryptedFileKey(channelID, msgID); err != nil {
		return err
	}
	if err := s.requireOwnerForShared(ctx, channelID, msgID, "delete"); err != nil {
		return err
	}

	// Gather the bodies to drop: the file/manifest message plus any multipart
	// parts. Captured before the tomb while file_parts is intact.
	bodyMsgIDs := []int64{int64(msgID)}
	var partMsgIDs []int64
	if parts, err := projection.MultipartParts(s.DB, channelID, int64(msgID)); err == nil {
		for _, p := range parts {
			bodyMsgIDs = append(bodyMsgIDs, p.MsgID)
			partMsgIDs = append(partMsgIDs, p.MsgID)
		}
	}

	renditionIDs, err := projection.RenditionMessageIDsForFiles(s.DB, channelID, []int64{int64(msgID)})
	if err != nil {
		return err
	}
	bodyMsgIDs = append(bodyMsgIDs, renditionIDs...)
	partMsgIDs = append(partMsgIDs, renditionIDs...)

	// Tomb first: visibility convergence is the contract; body delete is
	// best-effort. If body cleanup fails, the visible state is still correct.
	tombOp := projection.Op{
		Type: projection.OpTomb,
		Obj:  fmt.Sprintf("%s%d", projection.FileIDPrefix, msgID),
	}
	if _, err := s.emit(ctx, channelID, tombOp); err != nil {
		return err
	}

	if s.TG == nil || s.Peers == nil {
		return nil
	}
	peer, err := s.Peers.ResolvePeer(ctx, channelID)
	if err != nil {
		s.warnf("warn: tomb succeeded but peer resolve failed for msg=%d: %v\n", msgID, err)
		return nil
	}
	if err := s.deleteMessagesChunked(ctx, peer, bodyMsgIDs); err != nil {
		// Body cleanup failed; keep the file_parts rows so the orphan sweep can
		// retry the part-body delete later. The file is already tombstoned, so it
		// stays hidden in the meantime.
		return nil
	}
	if len(partMsgIDs) > 0 {
		if err := projection.DeleteFilePartsByMsgIDs(s.DB, channelID, partMsgIDs); err != nil {
			s.warnf("warn: tomb succeeded but dropping file_parts rows failed for msg=%d: %v\n", msgID, err)
		}
	}
	return nil
}

// SweepOrphanParts cleans up two safe sets of part bodies and removes their
// local pointers: parts of a deleted file whose cleanup didn't finish (a
// tombstoned manifest with surviving file_parts rows), and parts queued by a
// failed upload whose inline cleanup failed (pending_part_cleanup). It never
// touches parts that merely lack a manifest row, so it cannot disturb an upload
// still in flight (this client, another instance, or another user on a shared
// drive).
func (s *Service) SweepOrphanParts(ctx context.Context, channelID int64) error {
	if err := s.ready(); err != nil {
		return err
	}
	if channelID == 0 || s.TG == nil || s.Peers == nil {
		return nil
	}
	tombParts, err := projection.OrphanPartMessages(s.DB, channelID)
	if err != nil {
		return err
	}
	pending, err := projection.PendingPartCleanup(s.DB, channelID)
	if err != nil {
		return err
	}
	if len(tombParts) == 0 && len(pending) == 0 {
		return nil
	}
	peer, err := s.Peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return err
	}
	if len(tombParts) > 0 {
		if err := s.deleteMessagesChunked(ctx, peer, tombParts); err != nil {
			return err // keep the rows; retry next sweep
		}
		if err := projection.DeleteFilePartsByMsgIDs(s.DB, channelID, tombParts); err != nil {
			return err
		}
	}
	if len(pending) > 0 {
		if err := s.deleteMessagesChunked(ctx, peer, pending); err != nil {
			return err // keep the queue; retry next sweep
		}
		if err := projection.ClearPartCleanup(s.DB, channelID, pending); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) requireOwnerForShared(ctx context.Context, channelID int64, msgID int, action string) error {
	ch, err := projection.GetChannel(s.DB, channelID)
	if err != nil {
		return err
	}
	if ch.Kind != projection.KindShared {
		return nil
	}
	if s.ActorID == nil {
		return fmt.Errorf("actor resolver not ready")
	}
	actorID, err := s.ActorID(ctx)
	if err != nil {
		return err
	}
	uploader, err := projection.FileUploader(s.DB, channelID, int64(msgID))
	if err != nil {
		return err
	}
	if uploader == 0 || uploader != actorID {
		return fmt.Errorf("Only the uploader can %s this file in a shared drive", action)
	}
	return nil
}

func (s *Service) validParent(channelID int64, parentID string, label string) (string, error) {
	parent := normalizeParent(parentID)
	if parent == projection.RootParent {
		return parent, nil
	}
	if !projection.IsFolderID(parent) {
		if label == "target" {
			return "", fmt.Errorf("Invalid target folder id")
		}
		return "", fmt.Errorf("Invalid parent folder id")
	}
	if !projection.FolderExists(s.DB, channelID, parent) {
		return "", fmt.Errorf("Target folder not found")
	}
	return parent, nil
}
