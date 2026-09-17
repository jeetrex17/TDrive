package file

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

func (s *Service) Download(ctx context.Context, channelID int64, msgID int, lookupID int, chooseSavePath ChooseSavePathFunc) (result DownloadResult) {
	slog.Debug("file: download starting", "channel_id", channelID, "msg_id", msgID, "lookup_id", lookupID)
	defer func() {
		if result.Status == "error" {
			slog.Error("file: download failed", "channel_id", channelID, "msg_id", msgID, "message", result.Message)
		} else {
			slog.Debug("file: download completed", "channel_id", channelID, "msg_id", msgID, "status", result.Status)
		}
	}()
	if err := s.ready(); err != nil {
		return DownloadResult{Status: "error", Message: err.Error(), Err: err}
	}
	if channelID == 0 {
		err := os.ErrNotExist
		return DownloadResult{Status: "error", Message: "Drive ID not found", Err: err}
	}
	if s.TG == nil {
		err := errors.New("tg client not ready")
		return DownloadResult{Status: "error", Message: "Connection error: tg client not ready", Err: err}
	}
	if s.Peers == nil {
		err := errors.New("peer resolver not ready")
		return DownloadResult{Status: "error", Message: "Connection error: peer resolver not ready", Err: err}
	}
	if chooseSavePath == nil {
		err := errors.New("save dialog not ready")
		return DownloadResult{Status: "error", Message: "Failed to choose download location: save dialog not ready", Err: err}
	}

	fileID := int64(lookupID)
	if fileID == 0 {
		fileID = int64(msgID)
	}
	file, found, err := projection.FileDownloadRefContext(ctx, s.DB, channelID, fileID)
	if err != nil {
		return DownloadResult{Status: "error", Message: "System Error: " + err.Error(), Err: err}
	}
	if !found {
		return DownloadResult{Status: "error", Message: "Message deleted or not found", Err: os.ErrNotExist}
	}
	peer, err := s.Peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return DownloadResult{Status: "error", Message: "Error: " + err.Error(), Err: err}
	}

	// Check decryption readiness before opening the save dialog. Otherwise a
	// locked vault makes the user choose a path and then repeat it after unlock.
	masterKey, err := s.requireEncryptionKey(file.Encrypted)
	defer clearOwnedKey(masterKey)
	if err != nil {
		return DownloadResult{Status: "error", Message: err.Error(), Err: err}
	}

	savePath, err := chooseSavePath(file.Name)
	if err != nil {
		return DownloadResult{Status: "error", Message: "Failed to choose download location: " + err.Error(), Err: err}
	}
	if savePath == "" {
		return DownloadResult{Status: "canceled", Message: "Download canceled", Err: context.Canceled}
	}

	if err := s.downloadProjectedFileToPath(ctx, peer, file, savePath, masterKey, s.downloadProgress(ctx, file.StoredSize)); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return DownloadResult{Status: "canceled", Message: "Download canceled", Err: err}
		}
		return DownloadResult{Status: "error", Message: err.Error(), Err: err}
	}

	s.emitEvent("download_progress", 100.0, downloadProgressRequestID(ctx))
	return DownloadResult{
		Status:    "success",
		Message:   "Download complete",
		SavedPath: savePath,
	}
}

// replaceDownloadedFile preserves the existing single-file download contract:
// a completed temporary file replaces an existing destination with rollback if
// the final rename fails.
func replaceDownloadedFile(tmpPath string, savePath string) error {
	if err := os.Rename(tmpPath, savePath); err == nil {
		return nil
	}

	dir := filepath.Dir(savePath)
	backup, err := os.CreateTemp(dir, ".tdrive-backup-*")
	if err != nil {
		return err
	}
	backupPath := backup.Name()
	if err := backup.Close(); err != nil {
		_ = os.Remove(backupPath)
		return err
	}
	if err := os.Remove(backupPath); err != nil {
		return err
	}

	hadExisting := false
	if err := os.Rename(savePath, backupPath); err == nil {
		hadExisting = true
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.Rename(tmpPath, savePath); err != nil {
		if hadExisting {
			_ = os.Rename(backupPath, savePath)
		}
		return err
	}

	if hadExisting {
		_ = os.Remove(backupPath)
	}
	return nil
}

func (s *Service) PreviewThumbnail(ctx context.Context, channelID int64, msgID int) (PreviewPayload, error) {
	if msgID <= 0 {
		return PreviewPayload{}, errPreviewNotFound
	}

	var payload PreviewPayload
	err := s.withPreviewSession(ctx, channelID, func(ctx context.Context, peer tgclient.InputPeer, channelID int64) error {
		doc, _, mimeType, err := s.loadPreviewDocument(ctx, peer, channelID, msgID)
		if err != nil {
			return err
		}

		if inlinePayload, ok, err := previewInlineThumbPayload(doc, mimeType); ok || err != nil {
			if err != nil {
				return err
			}
			payload = inlinePayload
			return nil
		}

		thumbType, ok := previewThumbTypeForDocument(doc)
		if !ok {
			return errPreviewThumbMissing
		}

		var payloadBytes []byte
		if err := s.sendRetryPolicy().Do(ctx, func() error {
			// A fresh buffer each attempt so a retry never appends to a
			// partially downloaded thumbnail.
			var buf bytes.Buffer
			if err := s.TG.DownloadFileThumbnail(ctx, peer, int64(msgID), thumbType, &buf); err != nil {
				return err
			}
			payloadBytes = buf.Bytes()
			return nil
		}); err != nil {
			return errPreviewDownloadFailed
		}

		payload, err = previewPayloadFromBytes(payloadBytes, mimeType)
		return err
	})
	if err != nil {
		return PreviewPayload{}, normalizePreviewError(err)
	}

	return payload, nil
}

func (s *Service) PreviewFile(ctx context.Context, channelID int64, msgID int) (PreviewPayload, error) {
	if msgID <= 0 {
		return PreviewPayload{}, errPreviewNotFound
	}

	var payload PreviewPayload
	err := s.withPreviewSession(ctx, channelID, func(ctx context.Context, peer tgclient.InputPeer, channelID int64) error {
		doc, _, mimeType, err := s.loadPreviewDocument(ctx, peer, channelID, msgID)
		if err != nil {
			return err
		}

		// For encrypted files, gate the preview budget on the plaintext size.
		// Telegram's document size is ciphertext size and can differ.
		encrypted := false
		plaintextSize := doc.Size
		if enc, psz, _, lookupErr := projection.FileEncryptionMeta(s.DB, channelID, int64(msgID)); lookupErr == nil && enc {
			encrypted = true
			if psz > 0 {
				plaintextSize = psz
			}
		}
		if exceedsPreviewPayloadBudget(plaintextSize) {
			return errPreviewTooLarge
		}
		masterKey, err := s.requireEncryptionKey(encrypted)
		defer clearOwnedKey(masterKey)
		if err != nil {
			return errPreviewEncryptionPasswordRequired
		}

		var downloaded []byte
		if err := s.sendRetryPolicy().Do(ctx, func() error {
			var attempt bytes.Buffer
			if err := s.TG.DownloadFile(ctx, peer, int64(msgID), &attempt, s.previewProgress(msgID, doc.Size)); err != nil {
				return err
			}
			downloaded = append(downloaded[:0], attempt.Bytes()...)
			return nil
		}); err != nil {
			return errPreviewDownloadFailed
		}

		if encrypted {
			var plain bytes.Buffer
			if _, err := tdcrypto.DecryptStream(bytes.NewReader(downloaded), &plain, masterKey); err != nil {
				return errPreviewDownloadFailed
			}
			s.emitEvent("preview_progress", msgID, 100.0)
			payload, err = previewPayloadFromBytes(plain.Bytes(), mimeType)
			return err
		}

		s.emitEvent("preview_progress", msgID, 100.0)
		payload, err = previewPayloadFromBytes(downloaded, mimeType)
		return err
	})
	if err != nil {
		return PreviewPayload{}, normalizePreviewError(err)
	}

	return payload, nil
}
