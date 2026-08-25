package file

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/projection"
	"TDrive/backend/services/servicecontext"
	"TDrive/backend/tgclient"
	"TDrive/backend/thumbnail"

	"golang.org/x/sync/singleflight"
)

type PeerResolver interface {
	ResolvePeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error)
}

type EmitOpFunc func(channelID int64, op projection.Op) (int64, error)
type EmitOpContextFunc func(ctx context.Context, channelID int64, op projection.Op) (int64, error)
type ActorIDFunc func(ctx context.Context) (int64, error)

// RequireEncryptionKeyFunc returns a caller-owned key copy. Service clears a
// non-nil key on every return path, including when err is non-nil.
type RequireEncryptionKeyFunc func(encrypted bool) ([]byte, error)

// MasterKeyForUploadFunc returns a caller-owned key copy. Service clears a
// non-nil key on every return path, including when err is non-nil.
type MasterKeyForUploadFunc func(channelID int64, wantEncrypted bool) ([]byte, error)
type WriteCiphertextTempFunc func(plain io.Reader, plaintextSize int64, masterKey []byte) (*os.File, error)
type encryptStreamFunc func(plain io.Reader, ciphertext io.Writer, masterKey []byte, plaintextSize int64) error
type thumbnailGeneratorFunc func(ctx context.Context, channelID int64, msgID int, cacheKey string, encrypted bool, masterKey []byte) ([]byte, error)
type WarnFunc func(format string, args ...any)

// CreateFolderFunc creates a folder and returns its new ID. It is injected so
// import can build folder trees without the file service depending on the
// folder service directly.
type CreateFolderFunc func(channelID int64, name, parentID string) (folderID string, err error)

type EventSink interface {
	Emit(name string, args ...any)
}

type Service struct {
	DB                   *sql.DB
	TG                   tgclient.Client
	Peers                PeerResolver
	EmitOp               EmitOpFunc
	EmitOpContext        EmitOpContextFunc
	ActorID              ActorIDFunc
	RequireEncryptionKey RequireEncryptionKeyFunc
	MasterKeyForUpload   MasterKeyForUploadFunc
	WriteCiphertextTemp  WriteCiphertextTempFunc
	encryptStream        encryptStreamFunc
	generateThumbnailFn  thumbnailGeneratorFunc
	CreateFolder         CreateFolderFunc
	Events               EventSink
	Warnf                WarnFunc
	Now                  func() time.Time
	// MaxUploadBytes overrides the per-file upload limit. 0 uses the standard
	// 2 GiB cap; it is raised to the 4 GiB Premium cap once the account is known
	// to be Premium. See maxUploadBytes.
	MaxUploadBytes int64
	// FloodWaitRetry bounds FLOOD_WAIT and transient-transport retries for
	// direct Telegram transfers (uploads, downloads, deletes). The zero value
	// uses tgclient's bounded production defaults.
	FloodWaitRetry tgclient.FloodWaitRetryPolicy
	// MaxConcurrentUploads bounds active uploads across this Service, including
	// GUI/import/daemon calls and hidden mount writes. Set it before the first
	// upload; <= 0 uses defaultUploadConcurrency.
	MaxConcurrentUploads int
	uploadOnce           sync.Once
	uploadSem            chan struct{}
	previewMu            sync.Mutex
	// afterHiddenPartSend is a nil-by-default crash-injection seam used only by
	// package tests. It runs immediately after Telegram returns a positive
	// message ID and before that receipt enters any local collection/projection.
	afterHiddenPartSend func(partIndex int, msgID int64)

	// Thumbs is the on-disk thumbnail cache. Nil disables caching (every
	// Thumbnail call regenerates), which keeps the cache optional in tests.
	Thumbs *thumbnail.Cache
	// ThumbConcurrency bounds how many thumbnails generate at once. <= 0 uses
	// a sensible default. Thumbnail generation runs off previewMu so the grid
	// can fill in parallel without blocking single-file previews/downloads.
	ThumbConcurrency int
	thumbOnce        sync.Once
	thumbSem         chan struct{}
	thumbGroup       singleflight.Group
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
	// ErrHiddenReceiptRecoveryRequired means Telegram returned a positive send
	// receipt but its local ownership projection could not be made durable.
	// Mount cleanup must reconcile it from the unchanged staged source.
	ErrHiddenReceiptRecoveryRequired = errors.New("hidden upload receipt recovery required")
	// ErrHiddenReceiptInvalid marks a cleanup receipt that failed ownership or
	// structural validation. Callers must fail closed instead of retrying it as
	// a transient Telegram outage.
	ErrHiddenReceiptInvalid = errors.New("hidden upload cleanup receipt invalid")
	// errVisibleSendOutcomeUnknownNoRetry stops FloodWaitRetryPolicy from
	// treating an unknown outcome as a retryable transport failure when a
	// legacy client cannot preserve Telegram's random_id across attempts.
	errVisibleSendOutcomeUnknownNoRetry  = errors.New("visible upload outcome unknown without idempotency")
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
	slog.Debug("file: upload batch starting", "channel_id", channelID, "files", len(filePaths), "encrypt", encrypt)
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

	var wg sync.WaitGroup
	var mu sync.Mutex

	uploaded := make([]uploadedResult, 0, len(filePaths))
	failed := 0
	var firstErr error

	for i := 0; i < len(filePaths); i++ {
		path := filePaths[i]
		pid := parentIDs[i]
		uploadID := i
		release, slotErr := s.acquireUploadSlot(ctx)
		if slotErr != nil {
			mu.Lock()
			failed++
			if firstErr == nil {
				firstErr = slotErr
			}
			mu.Unlock()
			s.emitEvent("upload_error", uploadID, filepath.Base(path), slotErr.Error())
			continue
		}
		wg.Add(1)

		go func(uploadID int, path string, pid string, release func()) {
			defer wg.Done()
			defer release()
			defer func() {
				if r := recover(); r != nil {
					mu.Lock()
					failed++
					if firstErr == nil {
						firstErr = fmt.Errorf("upload panic: %v", r)
					}
					mu.Unlock()
					s.emitEvent("upload_error", uploadID, filepath.Base(path), fmt.Sprintf("upload panic: %v", r))
				}
			}()

			meta, op, header, err := s.uploadSingle(ctx, uploadID, path, pid, channelID, encrypt)
			if err != nil {
				if meta.MsgID != 0 {
					mu.Lock()
					uploaded = append(uploaded, uploadedResult{
						UploadID:  uploadID,
						Meta:      meta,
						RawHeader: header,
						Op:        op,
					})
					mu.Unlock()
					s.warnf("warn: upload committed but local projection is pending for %q: %v\n", meta.Name, err)
					return
				}
				mu.Lock()
				failed++
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				s.warnf("warn: upload failed for %q: %v\n", filepath.Base(path), err)
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
		}(uploadID, path, pid, release)
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
		// Multipart uploads project their own parts + manifest inside
		// uploadMultipart and return an empty op, so there's nothing to project
		// here for them.
		if item.Op.Type == "" {
			continue
		}
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
		slog.Warn("file: upload batch completed with failures", "channel_id", channelID, "succeeded", len(uploadedFiles), "failed", failed)
		if firstErr != nil {
			return uploadedFiles, fmt.Errorf("%d uploads failed: %w", failed, firstErr)
		}
		return uploadedFiles, fmt.Errorf("%d uploads failed", failed)
	}
	slog.Debug("file: upload batch completed", "channel_id", channelID, "succeeded", len(uploadedFiles))
	return uploadedFiles, nil
}

func (s *Service) uploadSingle(ctx context.Context, uploadID int, filePath string, parentID string, channelID int64, wantEncrypted bool) (Metadata, projection.Op, string, error) {
	if channelID == 0 {
		return Metadata{}, projection.Op{}, "", fmt.Errorf("drive channel id not found")
	}

	filename := filepath.Base(filePath)
	s.emitEvent("upload_start", uploadID, filename, int64(0), parentID)

	plainFile, err := os.Open(filePath)
	if err != nil {
		slog.Error("file: upload open source failed", "channel_id", channelID, "name", filename, "error", err)
		return Metadata{}, projection.Op{}, "", err
	}
	defer plainFile.Close()

	info, err := plainFile.Stat()
	if err != nil {
		return Metadata{}, projection.Op{}, "", err
	}
	plaintextSize := info.Size()
	slog.Debug("file: uploading", "channel_id", channelID, "name", filename, "size", plaintextSize, "encrypt", wantEncrypted, "parent_id", parentID)
	meta, op, header, err := s.uploadVisibleSource(ctx, uploadID, plainFile, filename, plaintextSize, parentID, channelID, wantEncrypted)
	if err != nil {
		slog.Error("file: upload failed", "channel_id", channelID, "name", filename, "size", plaintextSize, "error", err)
	} else {
		slog.Debug("file: upload succeeded", "channel_id", channelID, "name", filename, "msg_id", meta.MsgID, "stored_size", meta.Size)
	}
	return meta, op, header, err
}

// uploadVisibleSource is the compatibility path used by the existing GUI/CLI
// uploader. Keeping the source boundary seekable lets staged-file callers use
// the same single/multipart planning without coupling the core to local paths.
func (s *Service) uploadVisibleSource(ctx context.Context, uploadID int, source io.ReadSeeker, filename string, plaintextSize int64, parentID string, channelID int64, wantEncrypted bool) (Metadata, projection.Op, string, error) {
	if err := validateSeekableSize(source, plaintextSize); err != nil {
		return Metadata{}, projection.Op{}, "", err
	}
	parent, err := s.validParent(channelID, parentID, "parent")
	if err != nil {
		return Metadata{}, projection.Op{}, "", err
	}

	// Decide single vs multipart, rejecting only files beyond the hard cap.
	// The ciphertext size is used when encrypting, since the overhead can push
	// a file that is just under a part boundary over it.
	storedSize, multipart, err := s.planUpload(filename, plaintextSize, wantEncrypted)
	if err != nil {
		return Metadata{}, projection.Op{}, "", err
	}

	masterKey, err := s.masterKeyForUpload(channelID, wantEncrypted)
	defer clearOwnedKey(masterKey)
	if err != nil {
		return Metadata{}, projection.Op{}, "", err
	}

	peer, err := s.Peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return Metadata{}, projection.Op{}, "", err
	}

	if multipart {
		// uploadMultipart sends the parts, projects them, and emits the manifest
		// (the commit point) itself. Success returns an empty op; if Telegram
		// commits but the local manifest projection fails, it returns that exact
		// op/header so Upload can retry the local-only step.
		return s.uploadMultipart(ctx, uploadID, source, filename, plaintextSize, parent, channelID, wantEncrypted, masterKey, peer, storedSize)
	}

	encrypted := wantEncrypted
	var uploadSource io.Reader = source
	uploadSize := plaintextSize
	if encrypted {
		tempCipher, err := s.writeCiphertextTemp(source, plaintextSize, masterKey)
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

	s.warnf("Starting upload: %s\n", filename)
	s.emitEvent("upload_start", uploadID, filename, uploadSize, parent)

	var (
		lastProgress = time.Now()
		progressMu   sync.Mutex
	)
	onProgress := func(sent, total int64) {
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
	}
	var result tgclient.SendFileResult
	idempotentSend := supportsIdempotentSends(s.TG)
	sendRandomID := int64(0)
	if idempotentSend {
		// Visible uploads have no durable operation journal, but this fresh
		// operation ID remains stable for every automatic retry in this call.
		sendRandomID, err = tgclient.StableRandomID(projection.NewUploadUUID(), "body")
		if err != nil {
			return Metadata{}, projection.Op{}, "", err
		}
	}
	err = s.retryVisibleSend(ctx, idempotentSend, func() error {
		// A retried attempt must resend the whole body from its start.
		if _, ok := rewindSeeker(uploadSource, 0); !ok {
			return fmt.Errorf("staged upload source is not rewindable")
		}
		var serr error
		if idempotentSend {
			result, serr = tgclient.SendFileIdempotent(
				ctx, s.TG, peer, uploadSource, filename, caption, uploadSize, onProgress, sendRandomID,
			)
		} else {
			result, serr = s.TG.SendFile(ctx, peer, uploadSource, filename, caption, uploadSize, onProgress)
		}
		return serr
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

type uploadPartPlan struct {
	partSize  int64
	partCount int
}

func (s *Service) buildUploadPartPlan(storedSize int64) (uploadPartPlan, error) {
	partSize := s.maxPartBytes()
	if storedSize < 0 || partSize <= 0 {
		return uploadPartPlan{}, fmt.Errorf("invalid upload part sizing")
	}

	partCount := storedSize / partSize
	if storedSize%partSize != 0 {
		partCount++
	}
	if partCount == 0 {
		partCount = 1
	}
	if partCount > MaxParts {
		return uploadPartPlan{}, fmt.Errorf(
			"stored upload would split into %d parts (max %d): %w",
			partCount,
			MaxParts,
			ErrFileTooLarge,
		)
	}
	return uploadPartPlan{partSize: partSize, partCount: int(partCount)}, nil
}

func (plan uploadPartPlan) window(storedSize int64, partIndex int) (offset int64, length int64, err error) {
	if storedSize < 0 || plan.partSize <= 0 || plan.partCount <= 0 || partIndex < 0 || partIndex >= plan.partCount {
		return 0, 0, fmt.Errorf("invalid upload part %d", partIndex)
	}
	index := int64(partIndex)
	if index > 0 && plan.partSize > storedSize/index {
		return 0, 0, fmt.Errorf("invalid upload part %d offset", partIndex)
	}
	offset = index * plan.partSize
	return offset, min(plan.partSize, storedSize-offset), nil
}

// uploadMultipart stores a file too big for one Telegram message as N part
// documents plus a manifest. The stored byte stream (ciphertext when encrypting,
// else plaintext) is sliced into <= MaxPartBytes parts. Each part is sent and
// projected before an idempotent manifest commits the logical file. Encrypted
// data is staged one part at a time, which makes retries rewind-safe without
// materializing a complete multi-gigabyte ciphertext file.
func (s *Service) uploadMultipart(ctx context.Context, uploadID int, plainFile io.ReadSeeker, filename string, plaintextSize int64, parent string, channelID int64, encrypt bool, masterKey []byte, peer tgclient.InputPeer, storedSize int64) (Metadata, projection.Op, string, error) {
	if s.ActorID == nil {
		return Metadata{}, projection.Op{}, "", fmt.Errorf("actor resolver not ready")
	}
	actorID, err := s.ActorID(ctx)
	if err != nil {
		return Metadata{}, projection.Op{}, "", err
	}

	plan, err := s.buildUploadPartPlan(storedSize)
	if err != nil {
		return Metadata{}, projection.Op{}, "", fmt.Errorf("%s: %w", filename, err)
	}
	numParts := plan.partCount
	if !supportsIdempotentSends(s.TG) {
		return Metadata{}, projection.Op{}, "", fmt.Errorf("multipart upload requires Telegram idempotent sends")
	}

	uploadUUID := projection.NewUploadUUID()
	s.warnf("Starting multipart upload: %s (%d parts)\n", filename, numParts)
	s.emitEvent("upload_start", uploadID, filename, storedSize, parent)

	var encryptedStream io.Reader
	var finishEncryption func() error
	stagingDir := uploadSourceTempDir(plainFile)
	if encrypt {
		if _, err := plainFile.Seek(0, io.SeekStart); err != nil {
			return Metadata{}, projection.Op{}, "", fmt.Errorf("rewind source for encryption: %w", err)
		}
		reader, writer := io.Pipe()
		done := make(chan error, 1)
		producerKey := append([]byte(nil), masterKey...)
		go func() {
			err := s.encryptStoredStream(plainFile, writer, producerKey, plaintextSize)
			clearOwnedKey(producerKey)
			_ = writer.CloseWithError(err)
			done <- err
			close(done)
		}()
		encryptedStream = reader
		finishEncryption = func() error {
			_ = reader.Close()
			return <-done
		}
		defer func() {
			if finishEncryption != nil {
				_ = finishEncryption()
			}
		}()
	}

	partMsgIDs := make([]int64, 0, numParts)
	abort := func() {
		// Clean-up-and-fail: drop the parts already sent + their rows, using a
		// fresh context since ctx may itself be canceled.
		if len(partMsgIDs) > 0 {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := s.deleteMessagesChunked(cleanupCtx, peer, partMsgIDs); err != nil {
				// Couldn't delete the bodies now; queue them so a later sweep
				// retries rather than leaking them. These parts have no manifest,
				// so the tombstone-scoped sweep wouldn't otherwise see them.
				_ = projection.QueuePartCleanup(s.DB, channelID, partMsgIDs)
			}
		}
		_ = projection.DeleteFileParts(s.DB, channelID, uploadUUID)
	}

	var (
		progressMu   sync.Mutex
		lastProgress = time.Now()
	)
	for i := 0; i < numParts; i++ {
		if err := ctx.Err(); err != nil {
			abort()
			return Metadata{}, projection.Op{}, "", err
		}
		partBase, partLen, err := plan.window(storedSize, i)
		if err != nil {
			abort()
			return Metadata{}, projection.Op{}, "", err
		}
		partOp := projection.Op{
			Type:       projection.OpFilePart,
			UploadUUID: uploadUUID,
			PartIndex:  i,
			FileSize:   partLen,
		}
		partCaption := projection.Format(partOp)
		partReader := plainFile
		partOffset := partBase
		var stagedPart *os.File
		if encrypt {
			stagedPart, err = stageUploadPart(ctx, stagingDir, encryptedStream, partLen)
			if err != nil {
				abort()
				return Metadata{}, projection.Op{}, "", fmt.Errorf("stage encrypted part %d: %w", i, err)
			}
			partReader = stagedPart
			partOffset = 0
		}
		cleanupPart := func() {
			if stagedPart != nil {
				_ = stagedPart.Close()
				_ = os.Remove(stagedPart.Name())
			}
		}
		onProgress := func(sent, total int64) {
			progressMu.Lock()
			defer progressMu.Unlock()
			if time.Since(lastProgress) <= 100*time.Millisecond {
				return
			}
			percent := 0.0
			if storedSize > 0 {
				percent = float64(partBase+sent) / float64(storedSize) * 100
				if percent > 100 {
					percent = 100
				}
			}
			s.emitEvent("upload_progress", uploadID, percent)
			lastProgress = time.Now()
		}
		var result tgclient.SendFileResult
		sendRandomID, err := tgclient.StableRandomID(uploadUUID, fmt.Sprintf("part:%d", i))
		if err != nil {
			cleanupPart()
			abort()
			return Metadata{}, projection.Op{}, "", err
		}
		err = s.retryVisibleSend(ctx, true, func() error {
			if _, serr := partReader.Seek(partOffset, io.SeekStart); serr != nil {
				return fmt.Errorf("rewind staged part %d: %w", i, serr)
			}
			var serr error
			result, serr = tgclient.SendFileIdempotent(
				ctx,
				s.TG,
				peer,
				io.LimitReader(partReader, partLen),
				partAttachmentName(filename, i, numParts),
				partCaption,
				partLen,
				onProgress,
				sendRandomID,
			)
			return serr
		})
		cleanupPart()
		if err != nil {
			abort()
			return Metadata{}, projection.Op{}, "", err
		}
		if result.MsgID == 0 {
			abort()
			return Metadata{}, projection.Op{}, "", fmt.Errorf("upload part %d: no msg id", i)
		}
		// Track the sent part for cleanup before projecting it, so a projection
		// failure here still deletes this part's body in abort().
		partMsgIDs = append(partMsgIDs, result.MsgID)
		if _, err := projection.ProjectFromOp(s.DB, channelID, result.MsgID, partOp, actorID, partCaption); err != nil {
			abort()
			return Metadata{}, projection.Op{}, "", err
		}
	}
	if finishEncryption != nil {
		if err := finishEncryption(); err != nil {
			finishEncryption = nil
			abort()
			return Metadata{}, projection.Op{}, "", fmt.Errorf("encrypt multipart: %w", err)
		}
		finishEncryption = nil
	}

	// Commit: the manifest is a text op whose own msg_id becomes the file id.
	uploadTime := s.now().Unix()
	manifestOp := projection.Op{
		Type:           projection.OpFileManifest,
		UploadUUID:     uploadUUID,
		Parent:         parent,
		Name:           filename,
		FileSize:       storedSize,
		FileUploadTime: uploadTime,
		PartCount:      numParts,
	}
	if encrypt {
		manifestOp.Encrypted = true
		manifestOp.PlaintextSize = plaintextSize
		manifestOp.EncryptionVersion = 1
	}
	committedMeta := func(msgID int64) Metadata {
		return Metadata{
			Name:          filename,
			Size:          storedSize,
			MsgID:         int(msgID),
			ParentID:      parent,
			UploadTime:    uploadTime,
			Encrypted:     encrypt,
			PlaintextSize: plaintextSize,
		}
	}
	if err := ctx.Err(); err != nil {
		abort()
		return Metadata{}, projection.Op{}, "", err
	}
	manifestHeader := projection.Format(manifestOp)
	manifestMsgID, commitAttempted, err := s.commitMultipartManifest(
		ctx,
		channelID,
		manifestOp,
		manifestHeader,
		actorID,
		peer,
		uploadUUID,
	)
	if err != nil {
		if commitAttempted {
			// Once the send starts, Telegram may have accepted the manifest even if
			// cancellation during retry backoff hides the original transport error.
			// Preserve every referenced part. Sync can project an accepted manifest;
			// cleanup would instead corrupt it.
			if manifestMsgID > 0 {
				return committedMeta(manifestMsgID), manifestOp, manifestHeader, err
			}
			return Metadata{}, projection.Op{}, "", err
		}
		abort()
		return Metadata{}, projection.Op{}, "", err
	}

	s.emitEvent("upload_progress", uploadID, 100.0)
	return committedMeta(manifestMsgID), projection.Op{}, "", nil
}

// deleteMessagesChunked deletes Telegram messages in batches of 100 so a large
// part set stays under the deleteMessages API limit. It returns the first error
// encountered (nil if every chunk succeeded) so callers can decide whether to
// drop the local file_parts pointers or keep them for a later retry.
func (s *Service) deleteMessagesChunked(ctx context.Context, peer tgclient.InputPeer, msgIDs []int64) error {
	if s.TG == nil || len(msgIDs) == 0 {
		return nil
	}
	const chunk = 100
	var firstErr error
	policy := s.sendRetryPolicy()
	for start := 0; start < len(msgIDs); start += chunk {
		end := min(start+chunk, len(msgIDs))
		// Deleting already-deleted ids is a no-op, so transport retries are safe.
		if err := policy.Do(ctx, func() error {
			return s.TG.DeleteMessages(ctx, peer, msgIDs[start:end])
		}); err != nil {
			s.warnf("warn: delete %d message bodies failed: %v\n", end-start, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

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

	// A multipart file's identity is its manifest (a text op, not a document),
	// so GetFileDocument would fail. Detect it first and reassemble from parts.
	fileID := int64(lookupID)
	if fileID == 0 {
		fileID = int64(msgID)
	}
	if !projection.FileExists(s.DB, channelID, fileID) {
		return DownloadResult{Status: "error", Message: "Message deleted or not found"}
	}
	if parts, err := projection.MultipartParts(s.DB, channelID, fileID); err != nil {
		return DownloadResult{Status: "error", Message: "System Error: " + err.Error()}
	} else if len(parts) > 0 {
		return s.downloadMultipart(ctx, channelID, fileID, parts, peer, chooseSavePath)
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
	defer clearOwnedKey(masterKey)
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

	finalTmp, err := os.CreateTemp(filepath.Dir(savePath), ".tdrive-download-*")
	if err != nil {
		return DownloadResult{Status: "error", Message: "Disk Error: " + err.Error()}
	}
	finalTmpPath := finalTmp.Name()
	committed := false
	defer func() {
		_ = finalTmp.Close()
		if !committed {
			_ = os.Remove(finalTmpPath)
		}
	}()

	if !encrypted {
		// DownloadFileAt writes through WriterAt, so a retried attempt
		// overwrites the same offsets instead of appending.
		if err := s.sendRetryPolicy().Do(ctx, func() error {
			return s.TG.DownloadFileAt(ctx, peer, int64(msgID), finalTmp, 0, s.downloadProgress(doc.Size))
		}); err != nil {
			return DownloadResult{Status: "error", Message: "Network Error: " + err.Error()}
		}
	} else {
		cipher, err := os.CreateTemp(filepath.Dir(savePath), ".tdrive-download-cipher-*")
		if err != nil {
			return DownloadResult{Status: "error", Message: "Disk Error: " + err.Error()}
		}
		defer func() {
			_ = cipher.Close()
			_ = os.Remove(cipher.Name())
		}()
		if err := s.sendRetryPolicy().Do(ctx, func() error {
			return s.TG.DownloadFileAt(ctx, peer, int64(msgID), cipher, 0, s.downloadProgress(doc.Size))
		}); err != nil {
			return DownloadResult{Status: "error", Message: "Network Error: " + err.Error()}
		}
		if _, err := cipher.Seek(0, io.SeekStart); err != nil {
			return DownloadResult{Status: "error", Message: "Disk Error: " + err.Error()}
		}
		if _, err := tdcrypto.DecryptStream(cipher, finalTmp, masterKey); err != nil {
			return DownloadResult{Status: "error", Message: "Decrypt failed: " + err.Error()}
		}
	}
	if err := finalTmp.Close(); err != nil {
		return DownloadResult{Status: "error", Message: "Disk Error: " + err.Error()}
	}
	if err := replaceDownloadedFile(finalTmpPath, savePath); err != nil {
		return DownloadResult{Status: "error", Message: "Disk Error: " + err.Error()}
	}
	committed = true

	s.emitEvent("download_progress", 100.0)
	return DownloadResult{
		Status:    "success",
		Message:   "Download complete",
		SavedPath: savePath,
	}
}

// downloadMultipart reassembles a multipart file by streaming its parts in
// order. Plain files append each part to the output; encrypted files stream the
// concatenated ciphertext through a pipe into DecryptStream. Retriable encrypted
// downloads stage one part beside the destination, bounding temporary storage
// to one TDrive part instead of the complete ciphertext. Progress is aggregate
// across all parts.
func (s *Service) downloadMultipart(ctx context.Context, channelID int64, fileID int64, parts []projection.FilePart, peer tgclient.InputPeer, chooseSavePath ChooseSavePathFunc) DownloadResult {
	// Refuse to reassemble an incomplete part set: a missing part would otherwise
	// produce a silently-truncated file (plain downloads especially, since
	// there's no AEAD tag to catch it).
	if err := projection.MultipartComplete(s.DB, channelID, fileID, parts); err != nil {
		return DownloadResult{Status: "error", Message: err.Error()}
	}

	originalName := "tdrive_download"
	if name := projection.LookupFileName(s.DB, channelID, fileID); name != "" {
		originalName = name
	}

	encrypted := false
	if enc, _, _, err := projection.FileEncryptionMeta(s.DB, channelID, fileID); err == nil {
		encrypted = enc
	}
	masterKey, err := s.requireEncryptionKey(encrypted)
	defer clearOwnedKey(masterKey)
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

	finalTmp, err := os.CreateTemp(filepath.Dir(savePath), ".tdrive-download-*")
	if err != nil {
		return DownloadResult{Status: "error", Message: "Disk Error: " + err.Error()}
	}
	finalTmpPath := finalTmp.Name()
	committed := false
	defer func() {
		_ = finalTmp.Close()
		if !committed {
			_ = os.Remove(finalTmpPath)
		}
	}()

	var totalStored int64
	for _, p := range parts {
		totalStored += p.Size
	}
	progress := s.downloadProgress(totalStored)

	// Encrypted decryption requires strict ordering. Stage only one ciphertext
	// part at a time so DownloadFileAt can overwrite partial retry attempts
	// without keeping the whole encrypted file on disk.
	downloadPartsOrdered := func(dst io.Writer) error {
		var base int64
		for _, p := range parts {
			partTmp, err := os.CreateTemp(filepath.Dir(savePath), ".tdrive-download-part-*")
			if err != nil {
				return err
			}
			partPath := partTmp.Name()
			cleanup := func() {
				_ = partTmp.Close()
				_ = os.Remove(partPath)
			}
			if err := partTmp.Truncate(p.Size); err != nil {
				cleanup()
				return err
			}

			startBase := base
			var reported int64
			var reportedMu sync.Mutex
			err = s.sendRetryPolicy().Do(ctx, func() error {
				return s.TG.DownloadFileAt(ctx, peer, p.MsgID, partTmp, 0, func(partDone, _ int64) {
					partDone = min(max(partDone, 0), p.Size)
					reportedMu.Lock()
					defer reportedMu.Unlock()
					if partDone > reported {
						reported = partDone
						progress(startBase+partDone, totalStored)
					}
				})
			})
			if err != nil {
				cleanup()
				return err
			}
			if _, err := partTmp.Seek(0, io.SeekStart); err != nil {
				cleanup()
				return err
			}
			if _, err := io.CopyN(dst, partTmp, p.Size); err != nil {
				cleanup()
				return err
			}
			cleanup()
			base += p.Size
		}
		return nil
	}

	downloadPartsAt := func(dst io.WriterAt) error {
		const partConcurrency = 2

		partOffsets := make([]int64, len(parts))
		var off int64
		for i, p := range parts {
			partOffsets[i] = off
			off += p.Size
		}

		dlCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		sem := make(chan struct{}, partConcurrency)
		errCh := make(chan error, len(parts))
		var wg sync.WaitGroup
		var progressMu sync.Mutex
		partDone := make([]int64, len(parts))

		report := func(i int, done int64) {
			if done < 0 {
				done = 0
			}
			if done > parts[i].Size {
				done = parts[i].Size
			}
			progressMu.Lock()
			if done > partDone[i] {
				partDone[i] = done
				var totalDone int64
				for _, n := range partDone {
					totalDone += n
				}
				progress(totalDone, totalStored)
			}
			progressMu.Unlock()
		}

		for i, part := range parts {
			i, part := i, part
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
				case <-dlCtx.Done():
					return
				}
				defer func() { <-sem }()

				// WriterAt at partOffsets[i] makes retries overwrite-safe.
				if err := s.sendRetryPolicy().Do(dlCtx, func() error {
					return s.TG.DownloadFileAt(dlCtx, peer, part.MsgID, dst, partOffsets[i], func(partDone, _ int64) {
						report(i, partDone)
					})
				}); err != nil {
					errCh <- err
					cancel()
					return
				}
				report(i, part.Size)
			}()
		}

		wg.Wait()
		close(errCh)
		if err, ok := <-errCh; ok {
			return err
		}
		progress(totalStored, totalStored)
		return nil
	}

	if !encrypted {
		if err := finalTmp.Truncate(totalStored); err != nil {
			return DownloadResult{Status: "error", Message: "Disk Error: " + err.Error()}
		}
		if err := downloadPartsAt(finalTmp); err != nil {
			return DownloadResult{Status: "error", Message: "Network Error: " + err.Error()}
		}
	} else {
		pr, pw := io.Pipe()
		downloadDone := make(chan error, 1)
		go func() {
			downloadErr := downloadPartsOrdered(pw)
			downloadDone <- downloadErr
			_ = pw.CloseWithError(downloadErr)
		}()
		if _, err := tdcrypto.DecryptStream(pr, finalTmp, masterKey); err != nil {
			select {
			case downloadErr := <-downloadDone:
				if downloadErr != nil {
					return DownloadResult{Status: "error", Message: "Network Error: " + downloadErr.Error()}
				}
			default:
				_ = pr.CloseWithError(err)
				<-downloadDone
			}
			return DownloadResult{Status: "error", Message: "Download/decrypt failed: " + err.Error()}
		}
		if downloadErr := <-downloadDone; downloadErr != nil {
			return DownloadResult{Status: "error", Message: "Network Error: " + downloadErr.Error()}
		}
	}

	if err := finalTmp.Close(); err != nil {
		return DownloadResult{Status: "error", Message: "Disk Error: " + err.Error()}
	}
	if err := replaceDownloadedFile(finalTmpPath, savePath); err != nil {
		return DownloadResult{Status: "error", Message: "Disk Error: " + err.Error()}
	}
	committed = true

	s.emitEvent("download_progress", 100.0)
	return DownloadResult{
		Status:    "success",
		Message:   "Download complete",
		SavedPath: savePath,
	}
}

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

const (
	defaultUploadConcurrency = 3
	maxUploadConcurrency     = 8
)

// sendRetryPolicy returns the bounded retry policy shared by direct Telegram
// sends, deletes, and downloads. The zero-value FloodWaitRetry field selects
// tgclient's production defaults.
func (s *Service) sendRetryPolicy() tgclient.FloodWaitRetryPolicy {
	p := s.FloodWaitRetry
	if p.MaxRetries == 0 && p.MaxWait == 0 && p.MaxTotalWait == 0 && p.Sleep == nil &&
		p.MaxTransientRetries == 0 && p.TransientBackoff == 0 && p.MaxTransientBackoff == 0 && p.TransientJitter == 0 {
		return tgclient.DefaultWriteFloodWaitRetryPolicy()
	}
	return p
}

func supportsIdempotentSends(client tgclient.Client) bool {
	_, ok := client.(tgclient.IdempotentSender)
	return ok
}

// retryVisibleSend uses Telegram random_id idempotency whenever the client
// supports it. A legacy client still retries failures known to precede a send,
// but must surface a lost receipt rather than risk publishing a duplicate.
func (s *Service) retryVisibleSend(ctx context.Context, idempotent bool, action func() error) error {
	var outcomeUnknown error
	err := s.sendRetryPolicy().Do(ctx, func() error {
		err := action()
		if !idempotent && errors.Is(err, tgclient.ErrSendOutcomeUnknown) {
			outcomeUnknown = err
			return errVisibleSendOutcomeUnknownNoRetry
		}
		return err
	})
	if errors.Is(err, errVisibleSendOutcomeUnknownNoRetry) {
		return outcomeUnknown
	}
	return err
}

// uploadConcurrency clamps MaxConcurrentUploads into [1, maxUploadConcurrency].
func (s *Service) uploadConcurrency() int {
	if s.MaxConcurrentUploads <= 0 {
		return defaultUploadConcurrency
	}
	return min(s.MaxConcurrentUploads, maxUploadConcurrency)
}

func (s *Service) acquireUploadSlot(ctx context.Context) (func(), error) {
	limit := s.uploadConcurrency()
	s.uploadOnce.Do(func() {
		s.uploadSem = make(chan struct{}, limit)
	})
	sem := s.uploadSem

	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// rewindSeeker seeks src to offset when it supports seeking. Callers refuse to
// retry a consumed, non-seekable reader rather than silently send a suffix.
func rewindSeeker(src io.Reader, offset int64) (io.Seeker, bool) {
	seeker, ok := src.(io.Seeker)
	if !ok {
		return nil, false
	}
	if _, err := seeker.Seek(offset, io.SeekStart); err != nil {
		return nil, false
	}
	return seeker, true
}

func (s *Service) emit(ctx context.Context, channelID int64, op projection.Op) (int64, error) {
	if err := servicecontext.Check(ctx, "file: emit operation"); err != nil {
		return 0, err
	}
	if s.EmitOpContext != nil {
		return s.EmitOpContext(ctx, channelID, op)
	}
	if s.EmitOp == nil {
		return 0, fmt.Errorf("file emitter not ready")
	}
	return s.EmitOp(channelID, op)
}

func (s *Service) commitMultipartManifest(
	ctx context.Context,
	channelID int64,
	op projection.Op,
	header string,
	actorID int64,
	peer tgclient.InputPeer,
	uploadUUID string,
) (msgID int64, attempted bool, err error) {
	randomID, err := tgclient.StableRandomID(uploadUUID, "manifest")
	if err != nil {
		return 0, false, err
	}
	err = s.retryVisibleSend(ctx, true, func() error {
		attempted = true
		var sendErr error
		msgID, sendErr = tgclient.SendControlIdempotent(ctx, s.TG, peer, header, true, randomID)
		return sendErr
	})
	if err != nil {
		return msgID, attempted, err
	}
	if _, err := projection.ProjectFromOp(s.DB, channelID, msgID, op, actorID, header); err != nil {
		return msgID, true, err
	}
	return msgID, true, nil
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
	if s.WriteCiphertextTemp != nil {
		return s.WriteCiphertextTemp(plain, plaintextSize, masterKey)
	}
	keyCopy := append([]byte(nil), masterKey...)
	defer clearOwnedKey(keyCopy)
	tmp, err := os.CreateTemp("", "tdrive-upload-*")
	if err != nil {
		return nil, err
	}
	if err := s.encryptStoredStream(plain, tmp, keyCopy, plaintextSize); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, err
	}
	return tmp, nil
}

func stageUploadPart(ctx context.Context, dir string, source io.Reader, size int64) (*os.File, error) {
	if ctx == nil || source == nil || size < 0 {
		return nil, fmt.Errorf("invalid upload part staging input")
	}
	tmp, err := createTempWithFallback(dir, ".tdrive-upload-part-*")
	if err != nil {
		return nil, err
	}
	remove := func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}
	written, err := io.CopyN(tmp, &contextReader{ctx: ctx, source: source}, size)
	if err != nil {
		remove()
		return nil, err
	}
	if written != size {
		remove()
		return nil, io.ErrUnexpectedEOF
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		remove()
		return nil, err
	}
	return tmp, nil
}

func createTempWithFallback(dir string, pattern string) (*os.File, error) {
	// Prefer the source filesystem for multi-gigabyte ciphertext so an upload
	// from an external volume does not unexpectedly exhaust the system temp
	// volume. Read-only or otherwise unsuitable source directories fall back to
	// the OS temp directory.
	if dir != "" {
		if tmp, err := os.CreateTemp(dir, pattern); err == nil {
			return tmp, nil
		}
	}
	return os.CreateTemp("", pattern)
}

func uploadSourceTempDir(source io.ReadSeeker) string {
	type named interface {
		Name() string
	}
	n, ok := source.(named)
	if !ok {
		return ""
	}
	name := n.Name()
	if name == "" {
		return ""
	}
	return filepath.Dir(name)
}

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(p)
}

func (s *Service) encryptStoredStream(plain io.Reader, ciphertext io.Writer, masterKey []byte, plaintextSize int64) error {
	if s.encryptStream != nil {
		return s.encryptStream(plain, ciphertext, masterKey, plaintextSize)
	}
	return tdcrypto.EncryptStream(plain, ciphertext, masterKey, plaintextSize)
}

func clearOwnedKey(key []byte) {
	clear(key)
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
