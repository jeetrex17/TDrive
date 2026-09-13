package file

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

func (s *Service) Upload(ctx context.Context, channelID int64, filePaths []string, parentIDs []string, encrypt bool) ([]Metadata, error) {
	return s.upload(ctx, channelID, filePaths, parentIDs, encrypt, uploadOptions{
		observer: detailedUploadObserver{service: s},
	})
}

type uploadOptions struct {
	observer uploadObserver
	peer     *tgclient.InputPeer
	idOffset int
}

type uploadedResult struct {
	UploadID  int
	Meta      Metadata
	RawHeader string
	Op        projection.Op
}

func (s *Service) upload(ctx context.Context, channelID int64, filePaths []string, parentIDs []string, encrypt bool, options uploadOptions) ([]Metadata, error) {
	slog.Debug("file: upload batch starting", "channel_id", channelID, "files", len(filePaths), "encrypt", encrypt)
	if len(filePaths) != len(parentIDs) {
		return nil, fmt.Errorf("filepaths and parentIDs length mismatch")
	}
	if len(filePaths) > maxImportItems {
		return nil, fmt.Errorf("%w: keep the batch under %d files", errImportItemLimit, maxImportItems)
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
	observer := options.observer
	if observer == nil {
		observer = detailedUploadObserver{service: s}
	}
	actorID, err := s.ActorID(ctx)
	if err != nil {
		return nil, err
	}
	peer := options.peer
	if peer == nil {
		resolved, err := s.Peers.ResolvePeer(ctx, channelID)
		if err != nil {
			return nil, err
		}
		peer = &resolved
	}

	var wg sync.WaitGroup
	var mu sync.Mutex

	uploaded := make([]uploadedResult, 0, len(filePaths))
	failed := 0
	var firstErr error

	for i := 0; i < len(filePaths); i++ {
		path := filePaths[i]
		pid := parentIDs[i]
		uploadID := options.idOffset + i
		release, slotErr := s.acquireUploadSlot(ctx)
		if slotErr != nil {
			mu.Lock()
			failed++
			if firstErr == nil {
				firstErr = slotErr
			}
			mu.Unlock()
			observer.Failed(uploadID, filepath.Base(path), slotErr)
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
					observer.Failed(uploadID, filepath.Base(path), fmt.Errorf("upload panic: %v", r))
				}
			}()

			meta, op, header, err := s.uploadSingleWithObserver(ctx, uploadID, path, pid, channelID, encrypt, *peer, observer)
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
				observer.Failed(uploadID, filepath.Base(path), err)
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
	sort.Slice(uploaded, func(i, j int) bool {
		return uploaded[i].Meta.MsgID < uploaded[j].Meta.MsgID
	})

	uploadedFiles := make([]Metadata, 0, len(uploaded))
	for _, item := range uploaded {
		uploadedFiles = append(uploadedFiles, item.Meta)
	}

	if err := s.projectUploaded(channelID, actorID, uploaded, observer); err != nil {
		return uploadedFiles, err
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

const uploadProjectionBatchSize = 128

func (s *Service) projectUploaded(channelID, actorID int64, uploaded []uploadedResult, observer uploadObserver) error {
	pending := make([]uploadedResult, 0, len(uploaded))
	for _, item := range uploaded {
		// Multipart uploads project their parts and manifest before returning.
		if item.Op.Type == "" {
			observer.Completed(item.UploadID, item.Meta.Name)
			continue
		}
		pending = append(pending, item)
	}

	for start := 0; start < len(pending); start += uploadProjectionBatchSize {
		end := min(start+uploadProjectionBatchSize, len(pending))
		// Telegram has already accepted these messages. Finish the local receipt
		// projection even if the caller canceled meanwhile; otherwise the files
		// disappear until the next history sync repairs the cache.
		tx, err := s.DB.Begin()
		if err == nil {
			for _, item := range pending[start:end] {
				_, err = projection.ProjectFromOpTx(tx, channelID, int64(item.Meta.MsgID), item.Op, actorID, item.RawHeader)
				if err != nil {
					break
				}
			}
		}
		if err == nil {
			err = tx.Commit()
		} else if tx != nil {
			_ = tx.Rollback()
		}
		if err != nil {
			wrapped := fmt.Errorf("local index write failed: %w", err)
			for _, item := range pending[start:] {
				observer.Failed(item.UploadID, item.Meta.Name, wrapped)
			}
			return fmt.Errorf("%w: %w", errUploadProjection, err)
		}
		for _, item := range pending[start:end] {
			observer.Completed(item.UploadID, item.Meta.Name)
		}
	}
	return nil
}

func (s *Service) uploadSingle(ctx context.Context, uploadID int, filePath string, parentID string, channelID int64, wantEncrypted bool) (Metadata, projection.Op, string, error) {
	peer, err := s.Peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return Metadata{}, projection.Op{}, "", err
	}
	return s.uploadSingleWithObserver(
		ctx,
		uploadID,
		filePath,
		parentID,
		channelID,
		wantEncrypted,
		peer,
		detailedUploadObserver{service: s},
	)
}

func (s *Service) uploadSingleWithObserver(ctx context.Context, uploadID int, filePath string, parentID string, channelID int64, wantEncrypted bool, peer tgclient.InputPeer, observer uploadObserver) (Metadata, projection.Op, string, error) {
	if channelID == 0 {
		return Metadata{}, projection.Op{}, "", fmt.Errorf("drive channel id not found")
	}

	filename := filepath.Base(filePath)

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
	// Announce the operation once the local source is known, before validating
	// remote metadata. That keeps failed uploads visible to callers while
	// avoiding the duplicate start event that used to be emitted at two layers.
	observer.Started(uploadID, filename, uploadByteSize(plaintextSize, wantEncrypted), parentID)
	slog.Debug("file: uploading", "channel_id", channelID, "name", filename, "size", plaintextSize, "encrypt", wantEncrypted, "parent_id", parentID)
	meta, op, header, err := s.uploadVisibleSource(ctx, uploadID, plainFile, filename, plaintextSize, parentID, channelID, wantEncrypted, peer, observer)
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
func (s *Service) uploadVisibleSource(ctx context.Context, uploadID int, source io.ReadSeeker, filename string, plaintextSize int64, parentID string, channelID int64, wantEncrypted bool, peer tgclient.InputPeer, observer uploadObserver) (Metadata, projection.Op, string, error) {
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

	if multipart {
		// uploadMultipart sends the parts, projects them, and emits the manifest
		// (the commit point) itself. Success returns an empty op; if Telegram
		// commits but the local manifest projection fails, it returns that exact
		// op/header so Upload can retry the local-only step.
		return s.uploadMultipart(ctx, uploadID, source, filename, plaintextSize, parent, channelID, wantEncrypted, masterKey, peer, storedSize, observer)
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
		observer.Progress(uploadID, percent)
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

	observer.Progress(uploadID, 100.0)
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
func (s *Service) uploadMultipart(ctx context.Context, uploadID int, plainFile io.ReadSeeker, filename string, plaintextSize int64, parent string, channelID int64, encrypt bool, masterKey []byte, peer tgclient.InputPeer, storedSize int64, observer uploadObserver) (Metadata, projection.Op, string, error) {
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
			observer.Progress(uploadID, percent)
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

	observer.Progress(uploadID, 100.0)
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
