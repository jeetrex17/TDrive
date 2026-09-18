package file

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"TDrive/backend/projection"
	"TDrive/backend/services/servicecontext"
)

const (
	// hardDeleteBatchSize is Telegram's per-call messages.deleteMessages limit.
	// The plan is paged at exactly that width so one page is one delete call and
	// one revalidation.
	hardDeleteBatchSize = 100
	// expiredTrashSweepLimit bounds one retention sweep. Whatever is left is
	// picked up by the next one, so a huge expired trash cannot turn a single
	// sweep into an unbounded run of Telegram calls.
	expiredTrashSweepLimit = 100
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

// Delete moves one file into the trash. Nothing leaves Telegram here: the
// bytes are the only copy of the user's data, and they are destroyed only by an
// explicit purge or by the retention sweep once purge_after has passed.
func (s *Service) Delete(ctx context.Context, channelID int64, msgID int) (err error) {
	defer func() {
		if err != nil {
			slog.Error("file: delete failed", "channel_id", channelID, "msg_id", msgID, "error", err)
		} else {
			slog.Debug("file: moved to trash", "channel_id", channelID, "msg_id", msgID)
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
	return s.TrashObject(ctx, channelID, fmt.Sprintf("%s%d", projection.FileIDPrefix, msgID))
}

// TrashObject publishes the trash operation for one live file or folder. It is
// object-kind agnostic and lives here, next to the emitter and the Telegram
// client, so the file and folder services share one definition of what a delete
// is instead of drifting apart.
func (s *Service) TrashObject(ctx context.Context, channelID int64, objectID string) error {
	if err := servicecontext.Check(ctx, "file: trash object"); err != nil {
		return err
	}
	entry, found, err := projection.DirentByID(s.DB, channelID, objectID)
	if err != nil {
		return err
	}
	if !found || entry.Tombstoned {
		return fmt.Errorf("Item not found")
	}
	return s.emitWritable(ctx, channelID, projection.TrashOp(entry, s.now(), projection.DefaultTrashRetention))
}

// RestoreObject takes one trashed object back out of the trash. The destination
// is resolved here, once, and travels on the wire: the original parent may have
// been deleted or its name taken since, and every replica has to land the
// object in the same place. A missing original parent restores to the drive
// root, which is the only destination guaranteed to exist.
func (s *Service) RestoreObject(ctx context.Context, channelID int64, objectID string) error {
	if err := servicecontext.Check(ctx, "file: restore object"); err != nil {
		return err
	}
	if err := s.ready(); err != nil {
		return err
	}
	entry, err := projection.TrashEntryByID(s.DB, channelID, objectID)
	if err != nil {
		return err
	}
	dirent, found, err := projection.DirentByID(s.DB, channelID, objectID)
	if err != nil {
		return err
	}
	if !found || !dirent.Tombstoned {
		return fmt.Errorf("Item is not in the trash")
	}
	parentID := entry.OriginalParentID
	if parentID != projection.RootParent && !projection.FolderExists(s.DB, channelID, parentID) {
		parentID = projection.RootParent
	}
	name, err := projection.NextFreeSiblingName(s.DB, channelID, parentID, entry.OriginalName, entry.ObjectKind)
	if err != nil {
		return err
	}
	if err := s.emitWritable(ctx, channelID, projection.RestoreOp(objectID, dirent.Revision, parentID, name)); err != nil {
		return err
	}
	slog.Info("file: restored from trash", "channel_id", channelID, "object_id", objectID, "parent_id", parentID, "name", name)
	return nil
}

// TrashListing is one restorable object plus the human path it was deleted
// from, which the projection stores only as a parent id.
type TrashListing struct {
	projection.TrashEntry
	ParentPath string
}

// ListTrash returns everything still restorable, most recently deleted first.
// Ancestor chains are resolved once per distinct parent: a bulk delete puts
// many siblings in the trash at the same instant and they all share one.
func (s *Service) ListTrash(channelID int64) ([]TrashListing, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	entries, err := projection.ListTrashEntries(s.DB, channelID)
	if err != nil {
		return nil, err
	}
	listings := make([]TrashListing, 0, len(entries))
	paths := make(map[string]string, len(entries))
	for _, entry := range entries {
		path, cached := paths[entry.OriginalParentID]
		if !cached {
			names, err := projection.FolderPathNames(s.DB, channelID, entry.OriginalParentID)
			if err != nil {
				return nil, err
			}
			path = strings.Join(names, " / ")
			paths[entry.OriginalParentID] = path
		}
		listings = append(listings, TrashListing{TrashEntry: entry, ParentPath: path})
	}
	return listings, nil
}

// PurgeObject destroys one trashed object for good, on the user's explicit
// instruction. Ordering is what makes it safe to interrupt: the intent is
// claimed first, the marker hides the object and captures an immutable body
// plan second, and only messages that plan already listed are ever deleted from
// Telegram.
func (s *Service) PurgeObject(ctx context.Context, channelID int64, objectID string) error {
	return s.purgeObject(ctx, channelID, objectID, func(opID string) (int64, error) {
		return projection.RegisterTrashPurgeIntent(ctx, s.DB, channelID, opID, objectID)
	})
}

// PurgeExpiredTrash destroys every trashed object whose retention window closed
// at or before now (unix seconds). It is best effort per entry: one object that
// cannot be purged -- a flood wait, a lost peer -- must not block the rest, and
// the entry stays in the trash for the next sweep.
func (s *Service) PurgeExpiredTrash(ctx context.Context, channelID int64, now int64) error {
	if err := s.ready(); err != nil {
		return err
	}
	entries, err := projection.ExpiredTrashEntries(s.DB, channelID, now, expiredTrashSweepLimit)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := s.purgeObject(ctx, channelID, entry.ObjectID, func(opID string) (int64, error) {
			return projection.RegisterExpiredTrashPurgeIntent(ctx, s.DB, channelID, opID, entry.ObjectID, now)
		})
		// Purging a folder also destroys anything trashed inside it, so an
		// entry from this page can legitimately be gone already.
		if err != nil && !errors.Is(err, projection.ErrTrashEntryNotFound) {
			s.warnf("warn: expired trash purge failed for %s: %v\n", entry.ObjectID, err)
		}
	}
	return nil
}

// purgeObject runs the shared purge sequence behind a caller-supplied intent
// claim, which is the only thing that differs between an explicit purge and the
// retention sweep. The claim returns the revision the marker must carry, so the
// marker can never target a version of the object other than the trashed one
// the claim inspected.
func (s *Service) purgeObject(ctx context.Context, channelID int64, objectID string, claim func(opID string) (int64, error)) (err error) {
	defer func() {
		if err != nil {
			slog.Error("file: purge failed", "channel_id", channelID, "object_id", objectID, "error", err)
		} else {
			slog.Info("file: purged from trash", "channel_id", channelID, "object_id", objectID)
		}
	}()
	if err := servicecontext.Check(ctx, "file: purge object"); err != nil {
		return err
	}
	if err := s.ready(); err != nil {
		return err
	}
	if s.TG == nil || s.Peers == nil {
		return fmt.Errorf("Telegram client not ready")
	}
	dirent, found, err := projection.DirentByID(s.DB, channelID, objectID)
	if err != nil {
		return err
	}
	if !found || !dirent.Tombstoned {
		return fmt.Errorf("Item is not in the trash")
	}
	// The operation id is derived from the tombstoned revision, which the
	// marker does not change. An interrupted purge therefore resumes on exactly
	// the operation it started, which matters because the marker consumes the
	// trash entry: without this, a crash between marker and body deletion would
	// strand those bodies in Telegram with nothing left to find them by.
	opID := projection.DeterministicOpID("purge", objectID, dirent.Revision)
	if _, _, _, err := projection.HardDeletePlanPage(ctx, s.DB, channelID, opID, 0, 1); err != nil {
		if !errors.Is(err, projection.ErrHardDeletePlanNotFound) {
			return err
		}
		revision, err := claim(opID)
		if err != nil {
			return err
		}
		if err := s.emitWritable(ctx, channelID, projection.HardDeleteOp(opID, objectID, revision)); err != nil {
			if abandonErr := projection.AbandonHardDeleteIntent(ctx, s.DB, channelID, opID); abandonErr != nil {
				return errors.Join(err, abandonErr)
			}
			return err
		}
	}
	if err := s.deletePlannedBodies(ctx, channelID, opID); err != nil {
		return err
	}
	return projection.CompleteHardDeletePlan(ctx, s.DB, channelID, opID)
}

// deletePlannedBodies walks the immutable plan the marker captured and deletes
// exactly those Telegram messages, one Telegram-sized batch at a time. Every
// batch is re-validated against the plan immediately before the delete call, so
// a corrupted or stale page can never widen the blast radius, and no database
// transaction is ever held across a network round trip.
func (s *Service) deletePlannedBodies(ctx context.Context, channelID int64, opID string) error {
	peer, err := s.Peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return err
	}
	for after := int64(0); ; {
		msgIDs, _, done, err := projection.HardDeletePlanPage(ctx, s.DB, channelID, opID, after, hardDeleteBatchSize)
		if err != nil {
			return err
		}
		if len(msgIDs) > 0 {
			if err := projection.ValidateHardDeletePlanItems(ctx, s.DB, channelID, opID, msgIDs); err != nil {
				return err
			}
			if err := s.TG.DeleteMessages(ctx, peer, msgIDs); err != nil {
				return err
			}
			after = msgIDs[len(msgIDs)-1]
		}
		if done {
			return nil
		}
	}
}

// emitWritable publishes a versioned writable operation and reports what the
// projection actually did with it, because a compare-and-swap refusal is
// recorded as a durable outcome rather than returned as a send error.
func (s *Service) emitWritable(ctx context.Context, channelID int64, op projection.Op) error {
	if _, err := s.emit(ctx, channelID, op); err != nil {
		return err
	}
	return projection.ConfirmWritableOperation(s.DB, channelID, op.OpID)
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
