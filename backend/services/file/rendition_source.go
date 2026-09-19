package file

import (
	"bytes"
	"context"
	"database/sql"
	"errors"

	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

// renditionReference chooses only an immutable reference for the current
// content. Its ID participates in the cache identity, so a newly prepared
// preview immediately supersedes a previously cached Telegram thumbnail.
func (s *Service) renditionReference(ctx context.Context, f projection.File, kind string) (*projection.FileRendition, error) {
	if kind == "original" {
		return nil, nil
	}
	ref, err := projection.CurrentFileRendition(ctx, s.DB, f.ChannelID, f.MsgID, kind)
	if errors.Is(err, sql.ErrNoRows) && kind == "preview" {
		ref, err = projection.CurrentFileRendition(ctx, s.DB, f.ChannelID, f.MsgID, "thumbnail")
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	contentID := f.ContentMsgID
	if f.UploadUUID != "" {
		contentID = 0
	}
	if ref.ContentMsgID != contentID || ref.UploadUUID != f.UploadUUID || ref.Encrypted != f.Encrypted {
		return nil, ErrRenditionStale
	}
	return &ref, nil
}
func (s *Service) fetchStoredRendition(ctx context.Context, peer tgclient.InputPeer, f projection.File, ref projection.FileRendition, key []byte) (Rendition, error) {
	kind := ref.Kind
	limit := renditionPreviewLimit
	if kind == "thumbnail" {
		limit = renditionThumbLimit
	}
	if ref.Size <= 0 || ref.Size > int64(limit) || ref.Encrypted != f.Encrypted {
		return Rendition{}, s.quarantineRendition(ctx, ref)
	}
	var raw []byte
	err := s.sendRetryPolicy().Do(ctx, func() error {
		dst := &renditionWriter{limit: limit}
		if err := s.TG.DownloadFile(ctx, peer, ref.MsgID, dst, nil); err != nil {
			return err
		}
		raw = dst.Bytes()
		return nil
	})
	if err != nil {
		if errors.Is(err, tgclient.ErrMessageNotFound) || errors.Is(err, tgclient.ErrNotFile) || errors.Is(err, tgclient.ErrEmptyDocument) || errors.Is(err, errPreviewTooLarge) {
			return Rendition{}, s.quarantineRendition(ctx, ref)
		}
		return Rendition{}, err
	}
	if int64(len(raw)) != ref.Size {
		return Rendition{}, s.quarantineRendition(ctx, ref)
	}
	raw, err = decryptRendition(raw, key, ref)
	if err != nil {
		return Rendition{}, s.quarantineRendition(ctx, ref)
	}
	return checkedRendition(raw, kind, f.Encrypted)
}

// Definitive remote corruption/absence must not win selection forever.
// Never quarantine transport failures: cancellation and offline state are not
// evidence that a previously valid derivative should be replaced.
func (s *Service) quarantineRendition(ctx context.Context, ref projection.FileRendition) error {
	if err := projection.InvalidateRendition(ctx, s.DB, ref.ChannelID, ref.MsgID); err != nil {
		return err
	}
	return ErrRenditionMissing
}

func (s *Service) fetchOriginalRendition(ctx context.Context, peer tgclient.InputPeer, f projection.File, key []byte) (Rendition, error) {
	if f.ContentMsgID <= 0 || f.PartCount > 1 {
		return Rendition{}, errPreviewNotSupported
	}
	if f.Size <= 0 || f.Size > renditionOriginalLimit {
		return Rendition{}, errPreviewTooLarge
	}
	var raw []byte
	err := s.sendRetryPolicy().Do(ctx, func() error {
		dst := &renditionWriter{limit: renditionOriginalLimit}
		if err := s.TG.DownloadFile(ctx, peer, f.ContentMsgID, dst, nil); err != nil {
			return err
		}
		raw = dst.Bytes()
		return nil
	})
	if err != nil {
		return Rendition{}, err
	}
	if f.Encrypted {
		plain := &renditionWriter{limit: renditionOriginalLimit}
		if _, err := tdcrypto.DecryptStream(bytes.NewReader(raw), plain, key); err != nil {
			return Rendition{}, errPreviewDownloadFailed
		}
		raw = plain.Bytes()
	}
	return checkedRendition(raw, "original", f.Encrypted)
}
