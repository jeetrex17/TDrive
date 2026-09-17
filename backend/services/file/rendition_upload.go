package file

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"

	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
	"TDrive/backend/thumbnail"
)

// PrepareRenditions publishes bounded images from a trusted local original.
// source pins the immutable content before any decoding starts. It must describe
// the bytes in reader, not a fresh lookup after a concurrent replacement.
// Failure never invalidates the successfully stored original. Queued ciphertext
// survives process death and can be resumed without the source or unlocked key.
func (s *Service) PrepareRenditions(ctx context.Context, source projection.File, reader io.ReadSeeker) error {
	if ctx == nil || reader == nil || source.ChannelID == 0 || source.MsgID <= 0 {
		return fmt.Errorf("invalid rendition source")
	}
	if !thumbnail.IsImage(source.Name) {
		return nil
	}
	if err := s.ready(); err != nil {
		return err
	}
	if s.Peers == nil || s.ActorID == nil {
		return fmt.Errorf("rendition upload dependencies unavailable")
	}
	if !supportsIdempotentSends(s.TG) {
		return fmt.Errorf("renditions require idempotent Telegram sends")
	}
	if err := s.requireOwnerForShared(ctx, source.ChannelID, int(source.MsgID), "prepare previews"); err != nil {
		return err
	}
	key, err := s.requireEncryptionKey(source.Encrypted)
	defer clearOwnedKey(key)
	if err != nil {
		return err
	}
	jobIDs := make([]string, 0, 2)
	var preview []byte
	defer func() { clear(preview) }()
	for _, spec := range []struct {
		kind string
		edge int
	}{{projection.RenditionThumbnail, 512}, {projection.RenditionPreview, 1600}} {
		if _, err := projection.CurrentFileRendition(ctx, s.DB, source.ChannelID, source.MsgID, spec.kind); err == nil {
			continue
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		// Decode the original exactly once. Making the grid JPEG from the
		// bounded 1600px preview caps its secondary decode at 2.56 MP.
		if preview == nil {
			preview, err = thumbnail.GenerateLocal(ctx, reader, 1600)
			if err != nil {
				return err
			}
		}
		jpeg := preview
		if spec.kind == projection.RenditionThumbnail {
			jpeg, err = thumbnail.Generate(preview, spec.edge)
			if err != nil {
				return err
			}
		}
		ref, err := renditionDescriptor(source, spec.kind, jpeg)
		if err != nil {
			clear(jpeg)
			return err
		}
		payload := jpeg
		if source.Encrypted {
			payload, err = encryptRendition(jpeg, key, ref)
			clear(jpeg)
			if err != nil {
				return err
			}
		}
		ref.Size = int64(len(payload))
		if err := ref.Validate(); err != nil {
			clear(payload)
			return err
		}
		job, err := newRenditionJob(ref, payload, s.now().Unix())
		if err != nil {
			clear(payload)
			return err
		}
		err = projection.QueueRendition(ctx, s.DB, job)
		clear(payload)
		if err != nil {
			return err
		}
		jobIDs = append(jobIDs, job.JobID)
	}
	// Stage both kinds before sending. A network interruption must not strand
	// the preview when the local original disappears after upload completion.
	for _, jobID := range jobIDs {
		if err := s.sendPendingRendition(ctx, source.ChannelID, jobID); err != nil {
			return err
		}
	}
	return nil
}

func renditionDescriptor(source projection.File, kind string, jpeg []byte) (projection.FileRendition, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(jpeg))
	if err != nil || format != "jpeg" {
		return projection.FileRendition{}, fmt.Errorf("generator returned invalid JPEG")
	}
	content := source.ContentMsgID
	if source.UploadUUID != "" {
		content = 0
	} else if content == 0 {
		content = source.MsgID
	}
	return projection.FileRendition{ChannelID: source.ChannelID, FileMsgID: source.MsgID, ContentMsgID: content, UploadUUID: source.UploadUUID, Kind: kind, Version: 1, Size: int64(len(jpeg)), PlaintextSize: int64(len(jpeg)), Width: cfg.Width, Height: cfg.Height, Encrypted: source.Encrypted}, nil
}
func newRenditionJob(ref projection.FileRendition, payload []byte, now int64) (projection.PendingRendition, error) {
	// Stable source key deduplicates local preparation. Each new intent gets a
	// separate UUID/random_id, so two devices cannot alias different ciphertexts.
	identity := ref
	identity.Size = 0
	identity.PlaintextSize = 0
	identity.Width = 0
	identity.Height = 0
	encoded, _ := json.Marshal(identity)
	digest := sha256.Sum256(encoded)
	uuid := projection.NewUploadUUID()
	randomID, err := tgclient.StableRandomID(uuid, "rendition")
	if err != nil {
		return projection.PendingRendition{}, err
	}
	op := projection.Op{Type: projection.OpFilePart, UploadUUID: uuid, PartIndex: 0, FileSize: ref.Size, Rendition: &ref}
	return projection.PendingRendition{ChannelID: ref.ChannelID, JobID: hex.EncodeToString(digest[:]), RandomID: randomID, Header: projection.Format(op), Payload: payload, CreatedAt: now}, nil
}

// ResumeRenditionUploads drains a bounded page of previously authorized sends.
// It does not download originals or start preparation for the library. A stale
// intent is reconciled with its original random ID before deleting its receipt;
// simply forgetting it could leak a remotely accepted but locally unknown blob.
func (s *Service) ResumeRenditionUploads(ctx context.Context, channelID int64, limit int) error {
	if ctx == nil {
		return fmt.Errorf("rendition resume context required")
	}
	if err := s.ready(); err != nil {
		return err
	}
	ids, err := projection.PendingRenditionIDs(ctx, s.DB, channelID, limit)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.sendPendingRendition(ctx, channelID, id); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) sendPendingRendition(ctx context.Context, channelID int64, jobID string) error {
	if s.Peers == nil || s.ActorID == nil || !supportsIdempotentSends(s.TG) {
		return fmt.Errorf("rendition upload dependencies unavailable")
	}
	job, err := projection.LoadPendingRendition(ctx, s.DB, channelID, jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	defer clear(job.Payload)
	op, err := projection.Parse(job.Header)
	if err != nil || op.Rendition == nil {
		return fmt.Errorf("invalid persisted rendition")
	}
	if op.Rendition.ChannelID != channelID || op.FileSize != int64(len(job.Payload)) {
		return fmt.Errorf("persisted rendition binding mismatch")
	}
	peer, err := s.Peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return err
	}
	actor, err := s.ActorID(ctx)
	if err != nil {
		return err
	}
	var receipt tgclient.SendFileResult
	err = s.retryVisibleSend(ctx, true, func() error {
		var sendErr error
		receipt, sendErr = tgclient.SendFileIdempotent(ctx, s.TG, peer, bytes.NewReader(job.Payload), "rendition.bin", job.Header, int64(len(job.Payload)), nil, job.RandomID)
		return sendErr
	})
	if err != nil {
		return err
	}
	if receipt.MsgID <= 0 {
		return fmt.Errorf("rendition receipt missing")
	}
	if _, err := projection.ProjectFromOp(s.DB, channelID, receipt.MsgID, op, actor, job.Header); err != nil {
		return err
	}
	current, found, err := projection.FileByID(s.DB, channelID, op.Rendition.FileMsgID)
	if err != nil {
		return err
	}
	if !found || current.Tombstoned || !renditionMatchesFile(*op.Rendition, current) {
		if err := s.deleteMessagesChunked(ctx, peer, []int64{receipt.MsgID}); err != nil {
			return err
		}
	}
	if err := projection.CompletePendingRendition(ctx, s.DB, channelID, jobID); err != nil {
		return err
	}
	if _, err := projection.CurrentFileRendition(ctx, s.DB, channelID, op.Rendition.FileMsgID, op.Rendition.Kind); err == nil {
		s.emitEvent("gallery_rendition_ready", RenditionReadyEvent{ChannelID: channelID, MsgID: op.Rendition.FileMsgID, Kind: op.Rendition.Kind})
	}
	return nil
}

// RenditionReadyEvent rearms only affected visible tiles. Derivative uploads do
// not invalidate paginated file metadata or rebuild the whole gallery layout.
type RenditionReadyEvent struct {
	ChannelID int64  `json:"channel_id"`
	MsgID     int64  `json:"msg_id"`
	Kind      string `json:"kind"`
}

func renditionMatchesFile(ref projection.FileRendition, source projection.File) bool {
	content := source.ContentMsgID
	if source.UploadUUID != "" {
		content = 0
	} else if content == 0 {
		content = source.MsgID
	}
	return source.ChannelID == ref.ChannelID && source.MsgID == ref.FileMsgID && content == ref.ContentMsgID && source.UploadUUID == ref.UploadUUID && source.Encrypted == ref.Encrypted
}

// PrepareStoredRenditions reuses a mounted write's staged body. Encrypted input
// is authenticated in bounded memory and never materialized as plaintext disk.
func (s *Service) PrepareStoredRenditions(ctx context.Context, source projection.File, reader io.ReadSeeker) error {
	if !thumbnail.IsImage(source.Name) {
		return nil
	}
	if !source.Encrypted {
		return s.PrepareRenditions(ctx, source, reader)
	}
	const maxCompressedSource = 32 << 20
	if ctx == nil || reader == nil {
		return fmt.Errorf("invalid stored rendition source")
	}
	if source.PlaintextSize > maxCompressedSource {
		return thumbnail.ErrTooLarge
	}
	// Encrypted mounted sources need a compressed plaintext buffer before native
	// sampled decoding. Bound that working set across simultaneous mount writes.
	select {
	case encryptedRenditionSourceSlot <- struct{}{}:
		defer func() { <-encryptedRenditionSourceSlot }()
	case <-ctx.Done():
		return ctx.Err()
	}
	key, err := s.requireEncryptionKey(true)
	defer clearOwnedKey(key)
	if err != nil {
		return err
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return err
	}
	var plain bytes.Buffer
	defer func() { clear(plain.Bytes()) }()
	bounded := &renditionBoundedWriter{buffer: &plain, remaining: maxCompressedSource}
	if _, err := tdcrypto.DecryptStream(io.LimitReader(reader, maxCompressedSource+65536), bounded, key); err != nil {
		return err
	}
	return s.PrepareRenditions(ctx, source, bytes.NewReader(plain.Bytes()))
}

var encryptedRenditionSourceSlot = make(chan struct{}, 1)

type renditionBoundedWriter struct {
	buffer    *bytes.Buffer
	remaining int
}

func (w *renditionBoundedWriter) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		return 0, thumbnail.ErrTooLarge
	}
	n, err := w.buffer.Write(p)
	w.remaining -= n
	return n, err
}

func (s *Service) prepareUploadedRenditions(ctx context.Context, channelID int64, meta Metadata, op projection.Op, header string, reader io.ReadSeeker) {
	if !thumbnail.IsImage(meta.Name) {
		return
	}
	// Visible uploads are normally batch-projected by their caller. Publish this
	// one source before derivatives so every remote descriptor refers backwards
	// to a real file and a restarted preparation worker can resolve it locally.
	if op.Type != "" {
		actor, err := s.ActorID(ctx)
		if err != nil {
			s.warnf("photo preparation deferred: %v\n", err)
			return
		}
		if _, err := projection.ProjectFromOp(s.DB, channelID, int64(meta.MsgID), op, actor, header); err != nil {
			s.warnf("photo preparation projection deferred: %v\n", err)
			return
		}
	}
	source, found, err := projection.FileByID(s.DB, channelID, int64(meta.MsgID))
	if err != nil || !found {
		return
	}
	// A sync replacement between upload and this lookup must not label the old
	// local bytes as the new revision's preview.
	if source.Revision > 1 || (source.ContentMsgID != 0 && source.ContentMsgID != int64(meta.MsgID)) {
		return
	}
	if err := s.PrepareRenditions(ctx, source, reader); err != nil {
		s.warnf("photo preparation deferred: %v\n", err)
	}
}
