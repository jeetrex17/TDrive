package file

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

type PeerResolver interface {
	ResolvePeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error)
}

type EmitOpFunc func(channelID int64, op projection.Op) (int64, error)
type ActorIDFunc func(ctx context.Context) (int64, error)
type RequireEncryptionKeyFunc func(encrypted bool) ([]byte, error)
type MasterKeyForUploadFunc func(channelID int64, wantEncrypted bool) ([]byte, error)
type WriteCiphertextTempFunc func(plain io.Reader, plaintextSize int64, masterKey []byte) (*os.File, error)
type WarnFunc func(format string, args ...any)

type EventSink interface {
	Emit(name string, args ...any)
}

type Service struct {
	DB                   *sql.DB
	TG                   tgclient.Client
	Peers                PeerResolver
	EmitOp               EmitOpFunc
	ActorID              ActorIDFunc
	RequireEncryptionKey RequireEncryptionKeyFunc
	MasterKeyForUpload   MasterKeyForUploadFunc
	WriteCiphertextTemp  WriteCiphertextTempFunc
	Events               EventSink
	Warnf                WarnFunc
	Now                  func() time.Time
	previewMu            sync.Mutex
}

type Metadata struct {
	Name          string
	Size          int64
	MsgID         int
	ParentID      string
	UploadTime    int64
	Encrypted     bool
	PlaintextSize int64
}

type DownloadResult struct {
	Status    string
	Message   string
	SavedPath string
}

type PreviewPayload struct {
	DataBase64 string `json:"data_base64"`
	MimeType   string `json:"mime_type"`
}

type ChooseSavePathFunc func(defaultName string) (string, error)

const maxPreviewPayloadBytes int64 = 10 * 1024 * 1024

var (
	errPreviewNotFound                   = errors.New("File not found")
	errPreviewNotSupported               = errors.New("Not a supported image")
	errPreviewTooLarge                   = errors.New("File too large")
	errPreviewDownloadFailed             = errors.New("Download failed")
	errPreviewThumbMissing               = errors.New("Preview thumbnail unavailable")
	errPreviewEncryptionPasswordRequired = errors.New("encryption password required")
)

var previewMimeTypes = map[string]string{
	"jpg":  "image/jpeg",
	"jpeg": "image/jpeg",
	"png":  "image/png",
	"gif":  "image/gif",
	"webp": "image/webp",
	"bmp":  "image/bmp",
	"svg":  "image/svg+xml",
}

func (s *Service) Upload(ctx context.Context, channelID int64, filePaths []string, parentIDs []string, encrypt bool) ([]Metadata, error) {
	if len(filePaths) != len(parentIDs) {
		return nil, fmt.Errorf("filepaths and parentIDs length mismatch")
	}
	if err := s.ready(); err != nil {
		return nil, err
	}
	if s.TG == nil {
		return nil, fmt.Errorf("tg client not ready")
	}
	if s.Peers == nil {
		return nil, fmt.Errorf("peer resolver not ready")
	}
	if channelID == 0 {
		return nil, fmt.Errorf("no active channel")
	}
	if s.ActorID == nil {
		return nil, fmt.Errorf("actor resolver not ready")
	}
	actorID, err := s.ActorID(ctx)
	if err != nil {
		return nil, err
	}

	type uploadedResult struct {
		UploadID  int
		Meta      Metadata
		RawHeader string
		Op        projection.Op
	}

	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	var mu sync.Mutex

	uploaded := make([]uploadedResult, 0, len(filePaths))
	failed := 0

	for i := 0; i < len(filePaths); i++ {
		path := filePaths[i]
		pid := parentIDs[i]
		uploadID := i
		wg.Add(1)
		sem <- struct{}{}

		go func(uploadID int, path string, pid string) {
			defer wg.Done()
			defer func() { <-sem }()

			meta, op, header, err := s.uploadSingle(ctx, uploadID, path, pid, channelID, encrypt)
			if err != nil {
				mu.Lock()
				failed++
				mu.Unlock()
				s.emitEvent("upload_error", uploadID, filepath.Base(path), err.Error())
				return
			}

			mu.Lock()
			uploaded = append(uploaded, uploadedResult{
				UploadID:  uploadID,
				Meta:      meta,
				RawHeader: header,
				Op:        op,
			})
			mu.Unlock()
		}(uploadID, path, pid)
	}

	wg.Wait()

	uploadedFiles := make([]Metadata, 0, len(uploaded))
	for _, item := range uploaded {
		uploadedFiles = append(uploadedFiles, item.Meta)
	}

	emitLocalIndexError := func(reason string) {
		for _, item := range uploaded {
			s.emitEvent("upload_error", item.UploadID, item.Meta.Name, reason)
		}
	}

	for _, item := range uploaded {
		if _, err := projection.ProjectFromOp(
			s.DB,
			channelID,
			int64(item.Meta.MsgID),
			item.Op,
			actorID,
			item.RawHeader,
		); err != nil {
			emitLocalIndexError("local index write failed")
			return uploadedFiles, err
		}
	}

	for _, item := range uploaded {
		s.emitEvent("upload_complete", item.UploadID, item.Meta.Name)
	}

	if failed > 0 {
		return uploadedFiles, fmt.Errorf("%d uploads failed", failed)
	}
	return uploadedFiles, nil
}

func (s *Service) uploadSingle(ctx context.Context, uploadID int, filePath string, parentID string, channelID int64, wantEncrypted bool) (Metadata, projection.Op, string, error) {
	if channelID == 0 {
		return Metadata{}, projection.Op{}, "", fmt.Errorf("drive channel id not found")
	}

	plainFile, err := os.Open(filePath)
	if err != nil {
		return Metadata{}, projection.Op{}, "", err
	}
	defer plainFile.Close()

	info, err := plainFile.Stat()
	if err != nil {
		return Metadata{}, projection.Op{}, "", err
	}
	filename := filepath.Base(filePath)
	plaintextSize := info.Size()

	masterKey, err := s.masterKeyForUpload(channelID, wantEncrypted)
	if err != nil {
		return Metadata{}, projection.Op{}, "", err
	}
	encrypted := wantEncrypted

	var uploadSource *os.File = plainFile
	uploadSize := plaintextSize
	if encrypted {
		tempCipher, err := s.writeCiphertextTemp(plainFile, plaintextSize, masterKey)
		if err != nil {
			return Metadata{}, projection.Op{}, "", fmt.Errorf("encrypt: %w", err)
		}
		defer func() {
			_ = tempCipher.Close()
			_ = os.Remove(tempCipher.Name())
		}()
		ciphInfo, err := tempCipher.Stat()
		if err != nil {
			return Metadata{}, projection.Op{}, "", err
		}
		uploadSource = tempCipher
		uploadSize = ciphInfo.Size()
	}

	uploadTime := s.now().Unix()
	parent := normalizeParent(parentID)
	op := projection.Op{
		Type:           projection.OpFileUpload,
		Parent:         parent,
		Name:           filename,
		FileSize:       uploadSize,
		FileUploadTime: uploadTime,
	}
	if encrypted {
		op.Encrypted = true
		op.PlaintextSize = plaintextSize
		op.EncryptionVersion = 1
	}
	header := projection.Format(op)
	caption := header + "\nTDrive: " + filename

	peer, err := s.Peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return Metadata{}, projection.Op{}, "", err
	}

	s.warnf("Starting upload: %s\n", filename)
	s.emitEvent("upload_start", uploadID, filename, uploadSize, parentID)

	var (
		lastProgress = time.Now()
		progressMu   sync.Mutex
	)
	result, err := s.TG.SendFile(ctx, peer, uploadSource, filename, caption, uploadSize, func(sent, total int64) {
		progressMu.Lock()
		defer progressMu.Unlock()
		if time.Since(lastProgress) <= 100*time.Millisecond {
			return
		}
		percent := 0.0
		if total > 0 {
			percent = (float64(sent) / float64(total)) * 100
			if percent > 100 {
				percent = 100
			}
		}
		s.emitEvent("upload_progress", uploadID, percent)
		lastProgress = time.Now()
	})
	if err != nil {
		return Metadata{}, projection.Op{}, "", err
	}
	if result.MsgID == 0 {
		return Metadata{}, projection.Op{}, "", fmt.Errorf("upload success, but could not find msgID")
	}

	s.emitEvent("upload_progress", uploadID, 100.0)
	return Metadata{
		Name:          filename,
		Size:          uploadSize,
		MsgID:         int(result.MsgID),
		ParentID:      parent,
		UploadTime:    uploadTime,
		Encrypted:     encrypted,
		PlaintextSize: plaintextSize,
	}, op, header, nil
}

func (s *Service) Download(ctx context.Context, channelID int64, msgID int, lookupID int, chooseSavePath ChooseSavePathFunc) DownloadResult {
	if err := s.ready(); err != nil {
		return DownloadResult{Status: "error", Message: err.Error()}
	}
	if channelID == 0 {
		return DownloadResult{Status: "error", Message: "Drive ID not found"}
	}
	if s.TG == nil {
		return DownloadResult{Status: "error", Message: "Connection error: tg client not ready"}
	}
	if s.Peers == nil {
		return DownloadResult{Status: "error", Message: "Connection error: peer resolver not ready"}
	}
	if chooseSavePath == nil {
		return DownloadResult{Status: "error", Message: "Failed to choose download location: save dialog not ready"}
	}

	peer, err := s.Peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return DownloadResult{Status: "error", Message: "Error: " + err.Error()}
	}
	doc, err := s.TG.GetFileDocument(ctx, peer, int64(msgID))
	if err != nil {
		return downloadResolveError(err)
	}

	originalName := "tdrive_download"
	if lookupID == 0 {
		lookupID = msgID
	}
	if name := projection.LookupFileName(s.DB, channelID, int64(lookupID)); name != "" {
		originalName = name
	}

	// Check decryption readiness before opening the save dialog. Otherwise a
	// locked vault makes the user choose a path and then repeat it after unlock.
	encrypted := false
	if enc, _, _, err := projection.FileEncryptionMeta(s.DB, channelID, int64(lookupID)); err == nil {
		encrypted = enc
	}
	masterKey, err := s.requireEncryptionKey(encrypted)
	if err != nil {
		return DownloadResult{Status: "error", Message: err.Error()}
	}

	savePath, err := chooseSavePath(originalName)
	if err != nil {
		return DownloadResult{Status: "error", Message: "Failed to choose download location: " + err.Error()}
	}
	if savePath == "" {
		return DownloadResult{Status: "canceled", Message: "Download canceled"}
	}

	f, err := os.Create(savePath)
	if err != nil {
		return DownloadResult{Status: "error", Message: "Disk Error: " + err.Error()}
	}
	defer f.Close()

	if !encrypted {
		if err := s.TG.DownloadFile(ctx, peer, int64(msgID), f, s.downloadProgress(doc.Size)); err != nil {
			return DownloadResult{Status: "error", Message: "Network Error: " + err.Error()}
		}
	} else {
		cipher, err := os.CreateTemp("", "tdrive-dl-*")
		if err != nil {
			return DownloadResult{Status: "error", Message: "Disk Error: " + err.Error()}
		}
		defer func() {
			_ = cipher.Close()
			_ = os.Remove(cipher.Name())
		}()
		if err := s.TG.DownloadFile(ctx, peer, int64(msgID), cipher, s.downloadProgress(doc.Size)); err != nil {
			return DownloadResult{Status: "error", Message: "Network Error: " + err.Error()}
		}
		if _, err := cipher.Seek(0, io.SeekStart); err != nil {
			return DownloadResult{Status: "error", Message: "Disk Error: " + err.Error()}
		}
		if _, err := tdcrypto.DecryptStream(cipher, f, masterKey); err != nil {
			_ = os.Remove(savePath)
			return DownloadResult{Status: "error", Message: "Decrypt failed: " + err.Error()}
		}
	}

	s.emitEvent("download_progress", 100.0)
	return DownloadResult{
		Status:    "success",
		Message:   "Download complete",
		SavedPath: savePath,
	}
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

		var buf bytes.Buffer
		if err := s.TG.DownloadFileThumbnail(ctx, peer, int64(msgID), thumbType, &buf); err != nil {
			return errPreviewDownloadFailed
		}

		payload, err = previewPayloadFromBytes(buf.Bytes(), mimeType)
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
		if err != nil {
			return errPreviewEncryptionPasswordRequired
		}

		var buf bytes.Buffer
		if err := s.TG.DownloadFile(ctx, peer, int64(msgID), &buf, s.previewProgress(msgID, doc.Size)); err != nil {
			return errPreviewDownloadFailed
		}

		if encrypted {
			var plain bytes.Buffer
			if _, err := tdcrypto.DecryptStream(&buf, &plain, masterKey); err != nil {
				return errPreviewDownloadFailed
			}
			s.emitEvent("preview_progress", msgID, 100.0)
			payload, err = previewPayloadFromBytes(plain.Bytes(), mimeType)
			return err
		}

		s.emitEvent("preview_progress", msgID, 100.0)
		payload, err = previewPayloadFromBytes(buf.Bytes(), mimeType)
		return err
	})
	if err != nil {
		return PreviewPayload{}, normalizePreviewError(err)
	}

	return payload, nil
}

func (s *Service) Meta(channelID int64, msgID int, name string, size int64, parentID string) error {
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
	_, err = s.emit(channelID, op)
	return err
}

func (s *Service) Rename(ctx context.Context, channelID int64, msgID int, newName string) error {
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
	if err := s.requireOwnerForShared(ctx, channelID, msgID, "rename"); err != nil {
		return err
	}

	op := projection.Op{
		Type: projection.OpRename,
		Obj:  fmt.Sprintf("%s%d", projection.FileIDPrefix, msgID),
		Name: newName,
	}
	_, err := s.emit(channelID, op)
	return err
}

func (s *Service) Move(channelID int64, msgID int, newParentID string) error {
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
	op := projection.Op{
		Type:   projection.OpMove,
		Obj:    fmt.Sprintf("%s%d", projection.FileIDPrefix, msgID),
		Parent: parent,
	}
	_, err = s.emit(channelID, op)
	return err
}

func (s *Service) Delete(ctx context.Context, channelID int64, msgID int) error {
	if err := s.ready(); err != nil {
		return err
	}
	if channelID == 0 {
		return fmt.Errorf("Drive ID not found")
	}
	if !projection.FileExists(s.DB, channelID, int64(msgID)) {
		return fmt.Errorf("File not found")
	}

	if encrypted, _, _, err := projection.FileEncryptionMeta(s.DB, channelID, int64(msgID)); err == nil {
		if _, err := s.requireEncryptionKey(encrypted); err != nil {
			return err
		}
	}
	if err := s.requireOwnerForShared(ctx, channelID, msgID, "delete"); err != nil {
		return err
	}

	// Tomb first: visibility convergence is the contract; body delete is
	// best-effort. If body cleanup fails, the visible state is still correct.
	tombOp := projection.Op{
		Type: projection.OpTomb,
		Obj:  fmt.Sprintf("%s%d", projection.FileIDPrefix, msgID),
	}
	if _, err := s.emit(channelID, tombOp); err != nil {
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
	if err := s.TG.DeleteMessages(ctx, peer, []int64{int64(msgID)}); err != nil {
		s.warnf("warn: tomb succeeded but body delete failed for msg=%d: %v\n", msgID, err)
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

func (s *Service) emit(channelID int64, op projection.Op) (int64, error) {
	if s.EmitOp == nil {
		return 0, fmt.Errorf("file emitter not ready")
	}
	return s.EmitOp(channelID, op)
}

func (s *Service) requireEncryptionKey(encrypted bool) ([]byte, error) {
	if s.RequireEncryptionKey == nil {
		return nil, nil
	}
	return s.RequireEncryptionKey(encrypted)
}

func (s *Service) masterKeyForUpload(channelID int64, wantEncrypted bool) ([]byte, error) {
	if s.MasterKeyForUpload == nil {
		if wantEncrypted {
			return nil, fmt.Errorf("encryption upload not ready")
		}
		return nil, nil
	}
	return s.MasterKeyForUpload(channelID, wantEncrypted)
}

func (s *Service) writeCiphertextTemp(plain io.Reader, plaintextSize int64, masterKey []byte) (*os.File, error) {
	if s.WriteCiphertextTemp == nil {
		return nil, fmt.Errorf("encryption upload not ready")
	}
	return s.WriteCiphertextTemp(plain, plaintextSize, masterKey)
}

func (s *Service) emitEvent(name string, args ...any) {
	if s.Events != nil {
		s.Events.Emit(name, args...)
	}
}

func (s *Service) downloadProgress(total int64) func(done, total int64) {
	lastProgress := time.Now()
	var mu sync.Mutex
	return func(done, callbackTotal int64) {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(lastProgress) <= 100*time.Millisecond {
			return
		}
		useTotal := callbackTotal
		if useTotal <= 0 {
			useTotal = total
		}
		percent := 100.0
		if useTotal > 0 {
			percent = (float64(done) / float64(useTotal)) * 100
			if percent > 100 {
				percent = 100
			}
		}
		s.emitEvent("download_progress", percent)
		lastProgress = time.Now()
	}
}

func (s *Service) previewProgress(msgID int, total int64) func(done, total int64) {
	lastProgress := time.Now()
	var mu sync.Mutex
	return func(done, callbackTotal int64) {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(lastProgress) <= 100*time.Millisecond {
			return
		}
		useTotal := callbackTotal
		if useTotal <= 0 {
			useTotal = total
		}
		percent := 100.0
		if useTotal > 0 {
			percent = (float64(done) / float64(useTotal)) * 100
			if percent > 100 {
				percent = 100
			}
		}
		s.emitEvent("preview_progress", msgID, percent)
		lastProgress = time.Now()
	}
}

func (s *Service) withPreviewSession(ctx context.Context, channelID int64, fn func(context.Context, tgclient.InputPeer, int64) error) error {
	if ctx == nil {
		return errPreviewDownloadFailed
	}
	if channelID == 0 {
		return errPreviewDownloadFailed
	}
	if s.TG == nil || s.Peers == nil {
		return errPreviewDownloadFailed
	}

	s.previewMu.Lock()
	defer s.previewMu.Unlock()

	peer, err := s.Peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return errPreviewDownloadFailed
	}
	return fn(ctx, peer, channelID)
}

func (s *Service) loadPreviewDocument(ctx context.Context, peer tgclient.InputPeer, channelID int64, msgID int) (tgclient.FileDocument, string, string, error) {
	doc, err := s.TG.GetFileDocument(ctx, peer, int64(msgID))
	if err != nil {
		if errors.Is(err, tgclient.ErrMessageNotFound) || errors.Is(err, tgclient.ErrNotFile) || errors.Is(err, tgclient.ErrEmptyDocument) {
			return tgclient.FileDocument{}, "", "", errPreviewNotFound
		}
		return tgclient.FileDocument{}, "", "", errPreviewDownloadFailed
	}

	filename := s.lookupStoredFilename(channelID, msgID, doc)
	mimeType, ok := previewMimeTypeForName(filename)
	if !ok {
		return tgclient.FileDocument{}, "", "", errPreviewNotSupported
	}

	return doc, filename, mimeType, nil
}

func previewFilenameFromDocument(doc tgclient.FileDocument) string {
	return strings.TrimSpace(doc.Name)
}

func (s *Service) lookupStoredFilename(channelID int64, msgID int, doc tgclient.FileDocument) string {
	if s.DB != nil && channelID != 0 {
		if name := projection.LookupFileName(s.DB, channelID, int64(msgID)); name != "" {
			return name
		}
	}

	return previewFilenameFromDocument(doc)
}

func previewMimeTypeForName(name string) (string, bool) {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(strings.TrimSpace(name))), ".")
	if ext == "" {
		return "", false
	}

	mimeType, ok := previewMimeTypes[ext]
	return mimeType, ok
}

func estimatedBase64Size(rawBytes int64) int64 {
	if rawBytes <= 0 {
		return 0
	}

	return ((rawBytes + 2) / 3) * 4
}

func exceedsPreviewPayloadBudget(rawBytes int64) bool {
	if rawBytes < 0 {
		return true
	}

	return estimatedBase64Size(rawBytes) > maxPreviewPayloadBytes
}

func detectedPreviewMimeType(data []byte, fallback string) string {
	detected := strings.TrimSpace(http.DetectContentType(data))
	if strings.HasPrefix(detected, "image/") {
		return detected
	}

	fallback = strings.TrimSpace(fallback)
	if fallback != "" {
		return fallback
	}

	return detected
}

func previewPayloadFromBytes(data []byte, mimeType string) (PreviewPayload, error) {
	if len(data) == 0 {
		return PreviewPayload{}, errPreviewDownloadFailed
	}
	if exceedsPreviewPayloadBudget(int64(len(data))) {
		return PreviewPayload{}, errPreviewTooLarge
	}

	return PreviewPayload{
		DataBase64: base64.StdEncoding.EncodeToString(data),
		MimeType:   detectedPreviewMimeType(data, mimeType),
	}, nil
}

func previewThumbScore(thumb tgclient.FileThumb) int {
	score := thumb.Width * thumb.Height
	if score <= 0 {
		score = thumb.Size
	}
	if score <= 0 {
		score = len(thumb.Bytes)
	}
	return score
}

func previewInlineThumbPayload(doc tgclient.FileDocument, fallbackMimeType string) (PreviewPayload, bool, error) {
	var best *tgclient.FileThumb
	bestScore := 0

	for i := range doc.Thumbs {
		thumb := &doc.Thumbs[i]
		if len(thumb.Bytes) == 0 {
			continue
		}

		score := previewThumbScore(*thumb)
		if best == nil || score > bestScore {
			best = thumb
			bestScore = score
		}
	}

	if best == nil {
		return PreviewPayload{}, false, nil
	}

	payload, err := previewPayloadFromBytes(best.Bytes, fallbackMimeType)
	if err != nil {
		return PreviewPayload{}, true, err
	}
	return payload, true, nil
}

func previewThumbTypeForDocument(doc tgclient.FileDocument) (string, bool) {
	bestType := ""
	bestScore := 0

	for _, thumb := range doc.Thumbs {
		if len(thumb.Bytes) > 0 {
			continue
		}
		thumbType := strings.TrimSpace(thumb.Type)
		if thumbType == "" {
			continue
		}

		score := previewThumbScore(thumb)
		if bestType == "" || score > bestScore {
			bestType = thumbType
			bestScore = score
		}
	}

	return bestType, bestType != ""
}

func normalizePreviewError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errPreviewNotFound):
		return errPreviewNotFound
	case errors.Is(err, errPreviewNotSupported):
		return errPreviewNotSupported
	case errors.Is(err, errPreviewTooLarge):
		return errPreviewTooLarge
	case errors.Is(err, errPreviewEncryptionPasswordRequired):
		return errPreviewEncryptionPasswordRequired
	default:
		return errPreviewDownloadFailed
	}
}

func downloadResolveError(err error) DownloadResult {
	switch {
	case errors.Is(err, tgclient.ErrMessageNotFound):
		return DownloadResult{Status: "error", Message: "Message deleted or not found"}
	case errors.Is(err, tgclient.ErrNotFile):
		return DownloadResult{Status: "error", Message: "This is not a file"}
	case errors.Is(err, tgclient.ErrEmptyDocument):
		return DownloadResult{Status: "error", Message: "Empty document"}
	default:
		return DownloadResult{Status: "error", Message: "System Error: " + err.Error()}
	}
}

func (s *Service) ready() error {
	if s.DB == nil {
		return fmt.Errorf("DB not ready")
	}
	return nil
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) warnf(format string, args ...any) {
	if s.Warnf != nil {
		s.Warnf(format, args...)
	}
}

func normalizeParent(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return projection.RootParent
	}
	return p
}
