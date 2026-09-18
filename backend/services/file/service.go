// Package file moves bytes. It is the only place in the backend that turns a
// local file into Telegram document messages and back, and the only place that
// decides which messages constitute one logical file.
//
// Everything it publishes leaves as a projection.Op whose formatted TDX1 header
// is the Telegram caption. projection owns the SQLite write, tgclient owns the
// transport and its flood-wait retries, crypto owns the TDE1 stream format, and
// mountwrite owns the durable journal behind mounted writes. This package
// supplies only policy: what to split, when to encrypt, what to retry, and when
// a write becomes visible.
//
// Visibility differs per upload shape and is the easiest thing here to get
// wrong:
//
//  1. A file's identity is the msg_id of its header-carrying message — the
//     document for a single-part upload, the manifest text message for a
//     multipart one. Part messages are OpFilePart and never reach the files
//     table, so they can never be mistaken for orphans.
//  2. A single-part upload commits when Telegram accepts the document. Local
//     projection happens afterwards, so an upload can return both metadata and
//     an error; a non-zero msg id means the file exists and must not be resent.
//  3. A multipart upload commits on the manifest send. Before it, failure
//     aborts and deletes the part bodies; once the manifest send has been
//     attempted, aborting is forbidden, because sync may still project a
//     manifest Telegram accepted.
//  4. Hidden (mount) uploads never commit here at all. UploadHidden returns a
//     body and mountwrite publishes it with OpFileCommit.
//  5. Delete never touches Telegram. It publishes a trash operation and stops;
//     the bytes are destroyed only by an explicit purge or by the retention
//     sweep, both of which delete exactly the messages the projection's
//     immutable hard-delete plan lists.
//
// Retrying is only safe because every send derives a stable Telegram random id
// from the upload UUID plus a step label, and because every body is an
// io.ReadSeeker that is rewound before a resend rather than resumed mid-stream.
// Without an idempotent sender a multipart upload refuses to start and an
// unknown single-part outcome becomes terminal: failing is better than
// publishing a duplicate nobody can tell apart.
//
// The split decision uses the stored (ciphertext) size rather than the
// plaintext size, so encrypting a file near a boundary can make it multipart.
// Concurrency is bounded everywhere on purpose — one upload semaphore covers
// GUI uploads, imports, backups and mount writes alike — so no caller can turn
// a folder import into an unbounded fan-out against Telegram.
//
// Encryption is per-call intent, never per-drive: the caller asks, and the
// injected key providers decide whether that is allowed. Every key this package
// receives is a caller-owned copy that is zeroed on every return path,
// including error paths.
package file

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/datadir"
	"TDrive/backend/projection"
	"TDrive/backend/services/servicecontext"
	"TDrive/backend/tgclient"
	"TDrive/backend/thumbnail"
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

// RequireEncryptionKeyForChannelFunc binds a rendition key request to the
// source channel. Production providers must reject encrypted shared drives.
type RequireEncryptionKeyForChannelFunc func(channelID int64, encrypted bool) ([]byte, error)

// MasterKeyForUploadFunc returns a caller-owned key copy. Service clears a
// non-nil key on every return path, including when err is non-nil.
type MasterKeyForUploadFunc func(channelID int64, wantEncrypted bool) ([]byte, error)
type WriteCiphertextTempFunc func(plain io.Reader, plaintextSize int64, masterKey []byte) (*os.File, error)
type encryptStreamFunc func(plain io.Reader, ciphertext io.Writer, masterKey []byte, plaintextSize int64) error
type WarnFunc func(format string, args ...any)

// CreateFolderFunc creates a folder and returns its new ID. It is injected so
// import can build folder trees without the file service depending on the
// folder service directly.
type CreateFolderFunc func(ctx context.Context, channelID int64, name, parentID string) (folderID string, err error)

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
	// RequireEncryptionKeyForChannel is used by rendition reads before cache or
	// network access. RequireEncryptionKey remains for legacy non-rendition
	// paths until they can adopt the scoped contract.
	RequireEncryptionKeyForChannel RequireEncryptionKeyForChannelFunc
	// PersonalChannelID lets this service fail closed for encrypted shared-drive
	// records even if a test or legacy caller has not provided a scoped key hook.
	PersonalChannelID   func() int64
	MasterKeyForUpload  MasterKeyForUploadFunc
	WriteCiphertextTemp WriteCiphertextTempFunc
	encryptStream       encryptStreamFunc
	CreateFolder        CreateFolderFunc
	Events              EventSink
	Warnf               WarnFunc
	Now                 func() time.Time
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
	// uploadCancels holds a cancel handle per running upload, keyed by the
	// upload ID its progress events carry, so one transfer row can be stopped
	// without taking the rest of the batch with it. Only goroutines that hold
	// an upload slot are registered, so it stays bounded by MaxConcurrentUploads.
	uploadCancelMu sync.Mutex
	uploadCancels  map[int]context.CancelFunc
	previewMu      sync.Mutex
	// afterHiddenPartSend is a nil-by-default crash-injection seam used only by
	// package tests. It runs immediately after Telegram returns a positive
	// message ID and before that receipt enters any local collection/projection.
	afterHiddenPartSend func(partIndex int, msgID int64)

	// Thumbs is the on-disk rendition cache. Nil disables caching; requests
	// then fetch bounded remote derivatives without generating from originals.
	Thumbs *thumbnail.Cache
	// ThumbConcurrency bounds active rendition transfers. Values are clamped
	// to four; the default is three. Grid work does not hold previewMu.
	ThumbConcurrency int
	thumbOnce        sync.Once
	thumbSem         chan struct{}
	renditionFlights renditionFlightGroup
	// CacheNamespace is the authenticated account identity. Configure before use.
	CacheNamespace string
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
	Err       error
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
	errUploadProjection                  = errors.New("visible upload projection failed")
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
	tmp, err := datadir.CreateCacheTemp("tdrive-upload-*")
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
	// volume. Mobile source paths can be shared storage, so all mobile staging
	// stays in the app-private cache regardless of its source location.
	if runtime.GOOS != "android" && runtime.GOOS != "ios" && dir != "" {
		if tmp, err := os.CreateTemp(dir, pattern); err == nil {
			return tmp, nil
		}
	}
	return datadir.CreateCacheTemp(pattern)
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

type downloadProgressRequestIDKey struct{}

// WithDownloadProgressID binds frontend scheduler identity to emitted progress
// events. It is kept on the operation context so other service callers can
// remain compatible while Wails downloads are precisely correlated.
func WithDownloadProgressID(ctx context.Context, requestID string) context.Context {
	if ctx == nil || requestID == "" {
		return ctx
	}
	return context.WithValue(ctx, downloadProgressRequestIDKey{}, requestID)
}

func downloadProgressRequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(downloadProgressRequestIDKey{}).(string)
	return id
}

func (s *Service) downloadProgress(ctx context.Context, total int64) func(done, total int64) {
	requestID := downloadProgressRequestID(ctx)
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
		s.emitEvent("download_progress", percent, requestID)
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
