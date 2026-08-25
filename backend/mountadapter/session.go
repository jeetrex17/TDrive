package mountadapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"TDrive/backend/mountdav"
	"TDrive/backend/mountfs"
	"TDrive/backend/mountwrite"

	"golang.org/x/text/unicode/norm"
)

const (
	defaultMaxObjectBytes int64 = 4 << 30
	defaultTrashRetention       = 30 * 24 * time.Hour
	maxMutationPathBytes        = 4096
)

// Session adapts path-based WebDAV mutations to stable projection identities.
// A Session owns its mutation engine and must be closed during mount teardown.
type Session struct {
	driveID        int64
	resolver       Resolver
	engine         Engine
	maxObjectBytes int64
	recoveryReport mountwrite.RecoveryReport
	encryptWrites  bool
	masterKeys     MasterKeyProvider
}

func (s *Session) Put(ctx context.Context, request mountdav.PutRequest, body io.Reader) (mountdav.MutationResult, error) {
	if err := s.ready(ctx); err != nil || body == nil {
		if err != nil {
			return mountdav.MutationResult{}, err
		}
		return mountdav.MutationResult{}, mountdav.ErrWriteInvalid
	}
	if s.encryptWrites && request.ContentLength < 0 {
		return mountdav.MutationResult{}, mountdav.ErrWriteLengthRequired
	}
	slog.Debug("mountadapter: Session.Put", "path", request.Path, "content_length", request.ContentLength, "encrypt_writes", s.encryptWrites)
	parent, name, target, found, err := s.resolveDestination(ctx, request.Path)
	if err != nil {
		return mountdav.MutationResult{}, err
	}
	if found && target.Kind != mountfs.KindFile {
		slog.Debug("mountadapter: Put conflict, target is not a file", "path", request.Path)
		return mountdav.MutationResult{}, mountdav.ErrWriteConflict
	}
	if found && target.Encrypted && !s.encryptWrites {
		slog.Debug("mountadapter: Put forbidden, target is encrypted but this session cannot write encrypted content", "path", request.Path)
		return mountdav.MutationResult{}, mountdav.ErrWriteForbidden
	}
	resource, err := resourceForNode(ctx, s.driveID, target, found)
	if err != nil {
		return mountdav.MutationResult{}, err
	}
	if err := evaluateMutationConditions(request.Conditions, resource, map[string]conditionResource{request.Path: resource}); err != nil {
		return mountdav.MutationResult{}, err
	}

	domainRequest := mountwrite.PutRequest{
		OperationID:   request.OperationID,
		DriveID:       s.driveID,
		ParentID:      parent.ObjectID,
		Name:          name,
		CreateOnly:    !found,
		ContentLength: request.ContentLength,
		MaxBytes:      s.maxObjectBytes,
	}
	if s.encryptWrites {
		key, keyErr := s.masterKeys.Key()
		if keyErr != nil {
			return mountdav.MutationResult{}, mountdav.ErrWriteUnavailable
		}
		defer clearSensitiveBytes(key)
		domainRequest.EncryptionVersion = mountwrite.EncryptionTDE1
		domainRequest.MasterKey = key
	}
	if found {
		domainRequest.ExistingObjectID = target.ObjectID
		domainRequest.ExpectedRevision = target.Revision
	}
	result, err := s.engine.Put(ctx, domainRequest, body)
	if err != nil {
		mapped := mapWriteError(err)
		slog.Debug("mountadapter: Put returned error", "path", request.Path, "error", mapped)
		return mountdav.MutationResult{}, mapped
	}
	return davResult(ctx, s.driveID, result)
}

func (s *Session) Mkdir(ctx context.Context, request mountdav.MkdirRequest) (mountdav.MutationResult, error) {
	if err := s.ready(ctx); err != nil {
		return mountdav.MutationResult{}, err
	}
	slog.Debug("mountadapter: Session.Mkdir", "path", request.Path)
	parent, name, target, found, err := s.resolveDestination(ctx, request.Path)
	if err != nil {
		return mountdav.MutationResult{}, err
	}
	resource, err := resourceForNode(ctx, s.driveID, target, found)
	if err != nil {
		return mountdav.MutationResult{}, err
	}
	if err := evaluateMutationConditions(request.Conditions, resource, map[string]conditionResource{request.Path: resource}); err != nil {
		return mountdav.MutationResult{}, err
	}
	if found {
		// mountdav mints a fresh OperationID per HTTP request, so the write
		// coordinator's idempotency keying never sees a retried MKCOL as a
		// retry (see mountwrite.Coordinator.createOrLoad). A directory
		// already at this exact path is treated as that retry succeeding
		// again rather than a collision; an explicit If-None-Match/If
		// precondition above already rejects a client that asked for a
		// strict create. A file at this path is a genuine naming conflict.
		if target.Kind != mountfs.KindDirectory {
			return mountdav.MutationResult{}, mountdav.ErrWriteConflict
		}
		return mountdav.MutationResult{}, nil
	}
	result, err := s.engine.Mkdir(ctx, mountwrite.MkdirRequest{
		OperationID: request.OperationID,
		DriveID:     s.driveID,
		ParentID:    parent.ObjectID,
		Name:        name,
	})
	if err != nil {
		return mountdav.MutationResult{}, mapWriteError(err)
	}
	return davResult(ctx, s.driveID, result)
}

func (s *Session) Move(ctx context.Context, request mountdav.MoveRequest) (mountdav.MutationResult, error) {
	if err := s.ready(ctx); err != nil {
		return mountdav.MutationResult{}, err
	}
	slog.Debug("mountadapter: Session.Move", "source", request.SourcePath, "destination", request.DestinationPath, "overwrite", request.Overwrite)
	if err := validateAbsolutePath(request.SourcePath); err != nil {
		return mountdav.MutationResult{}, err
	}
	if request.SourcePath == "/" {
		return mountdav.MutationResult{}, mountdav.ErrWriteForbidden
	}
	source, found, err := s.resolver.Resolve(ctx, normalizedPath(request.SourcePath))
	if err != nil {
		return mountdav.MutationResult{}, mapResolveError(err)
	}
	if !found {
		return mountdav.MutationResult{}, mountdav.ErrWriteNotFound
	}
	if source.Encrypted && !s.encryptWrites {
		return mountdav.MutationResult{}, mountdav.ErrWriteForbidden
	}
	parent, name, destination, destinationFound, err := s.resolveDestination(ctx, request.DestinationPath)
	if err != nil {
		return mountdav.MutationResult{}, err
	}
	if destinationFound && destination.Encrypted && !s.encryptWrites {
		return mountdav.MutationResult{}, mountdav.ErrWriteForbidden
	}
	if destinationFound && destination.ObjectID != source.ObjectID {
		if !request.Overwrite {
			return mountdav.MutationResult{}, mountdav.ErrWritePreconditionFailed
		}
		if source.Kind != mountfs.KindFile || destination.Kind != mountfs.KindFile {
			return mountdav.MutationResult{}, mountdav.ErrWriteConflict
		}
	}

	sourceResource, err := resourceForNode(ctx, s.driveID, source, true)
	if err != nil {
		return mountdav.MutationResult{}, err
	}
	destinationResource, err := resourceForNode(ctx, s.driveID, destination, destinationFound)
	if err != nil {
		return mountdav.MutationResult{}, err
	}
	allowed := map[string]conditionResource{
		request.SourcePath:      sourceResource,
		request.DestinationPath: destinationResource,
	}
	if err := evaluateMutationConditions(request.Conditions, sourceResource, allowed); err != nil {
		return mountdav.MutationResult{}, err
	}
	if destinationFound && destination.ObjectID == source.ObjectID {
		return mountdav.MutationResult{Created: false, ETag: sourceResource.etag}, nil
	}

	domainRequest := mountwrite.MoveRequest{
		OperationID:            request.OperationID,
		DriveID:                s.driveID,
		ObjectID:               source.ObjectID,
		SourceParentID:         source.ParentID,
		DestinationParentID:    parent.ObjectID,
		DestinationName:        name,
		ExpectedSourceRevision: source.Revision,
	}
	if destinationFound {
		domainRequest.OverwriteTargetID = destination.ObjectID
		domainRequest.ExpectedTargetRevision = destination.Revision
	}
	result, err := s.engine.Move(ctx, domainRequest)
	if err != nil {
		return mountdav.MutationResult{}, mapWriteError(err)
	}
	result.Created = !destinationFound
	return davResultWithContentHash(ctx, s.driveID, result, source.ContentHash)
}

func (s *Session) Delete(ctx context.Context, request mountdav.DeleteRequest) (mountdav.MutationResult, error) {
	if err := s.ready(ctx); err != nil {
		return mountdav.MutationResult{}, err
	}
	slog.Debug("mountadapter: Session.Delete", "path", request.Path)
	if err := validateAbsolutePath(request.Path); err != nil {
		return mountdav.MutationResult{}, err
	}
	path := normalizedPath(request.Path)
	if path == "/" {
		return mountdav.MutationResult{}, mountdav.ErrWriteForbidden
	}
	target, found, err := s.resolver.Resolve(ctx, path)
	if err != nil {
		return mountdav.MutationResult{}, mapResolveError(err)
	}
	if !found {
		return mountdav.MutationResult{}, mountdav.ErrWriteNotFound
	}
	if target.Encrypted && !s.encryptWrites {
		return mountdav.MutationResult{}, mountdav.ErrWriteForbidden
	}
	resource, err := resourceForNode(ctx, s.driveID, target, true)
	if err != nil {
		return mountdav.MutationResult{}, err
	}
	if err := evaluateMutationConditions(request.Conditions, resource, map[string]conditionResource{request.Path: resource}); err != nil {
		return mountdav.MutationResult{}, err
	}
	result, err := s.engine.Delete(ctx, mountwrite.DeleteRequest{
		OperationID:      request.OperationID,
		DriveID:          s.driveID,
		ObjectID:         target.ObjectID,
		ParentID:         target.ParentID,
		ExpectedRevision: target.Revision,
		Recursive:        target.Kind == mountfs.KindDirectory,
		TrashRetention:   defaultTrashRetention,
	})
	if err != nil {
		return mountdav.MutationResult{}, mapWriteError(err)
	}
	return davResult(ctx, s.driveID, result)
}

func (s *Session) Status() mountwrite.Status {
	if s == nil || s.engine == nil {
		return mountwrite.Status{}
	}
	return s.engine.Status()
}

func (s *Session) RecoveryReport() mountwrite.RecoveryReport {
	if s == nil {
		return mountwrite.RecoveryReport{}
	}
	return s.recoveryReport
}

func (s *Session) Drain(ctx context.Context) error {
	if s == nil || s.engine == nil || ctx == nil {
		return mountwrite.ErrInvalidRequest
	}
	return s.engine.Drain(ctx)
}

func (s *Session) Close(ctx context.Context) error {
	if s == nil || s.engine == nil || ctx == nil {
		return mountwrite.ErrInvalidRequest
	}
	return s.engine.Close(ctx)
}

func (s *Session) ready(ctx context.Context) error {
	if s == nil || s.driveID == 0 || s.resolver == nil || s.engine == nil || s.maxObjectBytes <= 0 || ctx == nil {
		return mountdav.ErrWriteUnavailable
	}
	if s.encryptWrites && s.masterKeys == nil {
		return mountdav.ErrWriteUnavailable
	}
	if err := ctx.Err(); err != nil {
		return mapWriteError(err)
	}
	return nil
}

func (s *Session) resolveDestination(ctx context.Context, value string) (Node, string, Node, bool, error) {
	parentPath, rawName, err := splitParentPath(value)
	if err != nil {
		return Node{}, "", Node{}, false, err
	}
	name, err := mountfs.NormalizeWritableName(rawName)
	if err != nil {
		return Node{}, "", Node{}, false, mountdav.ErrWriteForbidden
	}
	parent, found, err := s.resolver.Resolve(ctx, parentPath)
	if err != nil {
		return Node{}, "", Node{}, false, mapResolveError(err)
	}
	if !found || parent.Kind != mountfs.KindDirectory {
		return Node{}, "", Node{}, false, mountdav.ErrWriteConflict
	}
	targetPath := joinPath(parentPath, name)
	target, targetFound, err := s.resolver.Resolve(ctx, targetPath)
	if err != nil {
		return Node{}, "", Node{}, false, mapResolveError(err)
	}
	return parent, name, target, targetFound, nil
}

func resourceForNode(ctx context.Context, driveID int64, item Node, exists bool) (conditionResource, error) {
	if !exists {
		return conditionResource{}, nil
	}
	if item.Revision == 0 || item.Revision > math.MaxInt64 {
		return conditionResource{}, mountdav.ErrWriteUnavailable
	}
	etag, err := mountdav.ResourceETag(ctx, driveID, item.ObjectID, int64(item.Revision), item.ContentHash)
	if err != nil {
		return conditionResource{}, mountdav.ErrWriteUnavailable
	}
	return conditionResource{exists: true, etag: etag}, nil
}

func davResult(ctx context.Context, driveID int64, result mountwrite.MutationResult) (mountdav.MutationResult, error) {
	return davResultWithContentHash(ctx, driveID, result, "")
}

func davResultWithContentHash(
	ctx context.Context,
	driveID int64,
	result mountwrite.MutationResult,
	fallbackContentHash string,
) (mountdav.MutationResult, error) {
	output := mountdav.MutationResult{Created: result.Created}
	if result.ObjectID == "" || result.Revision == 0 || result.Revision > math.MaxInt64 {
		return output, nil
	}
	contentHash := fallbackContentHash
	if result.SHA256 != ([32]byte{}) {
		contentHash = fmt.Sprintf("%x", result.SHA256)
	}
	etag, err := mountdav.ResourceETag(ctx, driveID, result.ObjectID, int64(result.Revision), contentHash)
	if err != nil {
		return mountdav.MutationResult{}, mountdav.ErrWriteUnavailable
	}
	output.ETag = etag
	return output, nil
}

func mapResolveError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return mountdav.ErrWriteUnavailable
	}
	return mountdav.ErrWriteUnavailable
}

func mapWriteError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, mountwrite.ErrInvalidRequest), errors.Is(err, mountwrite.ErrLengthMismatch):
		return mountdav.ErrWriteInvalid
	case errors.Is(err, mountwrite.ErrForbidden):
		return mountdav.ErrWriteForbidden
	case errors.Is(err, mountwrite.ErrNotFound):
		return mountdav.ErrWriteNotFound
	case errors.Is(err, mountwrite.ErrConflict), errors.Is(err, mountwrite.ErrOperationExists), errors.Is(err, mountwrite.ErrOperationInProgress):
		return mountdav.ErrWriteConflict
	case errors.Is(err, mountwrite.ErrPreconditionFailed):
		return mountdav.ErrWritePreconditionFailed
	case errors.Is(err, mountwrite.ErrTooLarge):
		return mountdav.ErrWriteTooLarge
	case errors.Is(err, mountwrite.ErrLocked):
		return mountdav.ErrWriteLocked
	case errors.Is(err, mountwrite.ErrQuotaExceeded):
		return mountdav.ErrWriteInsufficientStorage
	default:
		return mountdav.ErrWriteUnavailable
	}
}

func splitParentPath(value string) (string, string, error) {
	value = normalizedPath(value)
	if err := validateAbsolutePath(value); err != nil || value == "/" {
		return "", "", mountdav.ErrWriteInvalid
	}
	components := strings.Split(value[1:], "/")
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			return "", "", mountdav.ErrWriteInvalid
		}
	}
	name := components[len(components)-1]
	if len(components) == 1 {
		return "/", name, nil
	}
	return "/" + strings.Join(components[:len(components)-1], "/"), name, nil
}

func validateAbsolutePath(value string) error {
	value = normalizedPath(value)
	if value == "" || value[0] != '/' || len(value) > maxMutationPathBytes || !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') || strings.ContainsRune(value, '\\') {
		return mountdav.ErrWriteInvalid
	}
	if value == "/" {
		return nil
	}
	for _, component := range strings.Split(value[1:], "/") {
		if component == "" || component == "." || component == ".." {
			return mountdav.ErrWriteInvalid
		}
	}
	return nil
}

func normalizedPath(value string) string {
	if !utf8.ValidString(value) {
		return value
	}
	return norm.NFC.String(value)
}

func clearSensitiveBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func joinPath(parent, name string) string {
	if parent == "/" {
		return "/" + name
	}
	return parent + "/" + name
}

var _ mountdav.WriteCoordinator = (*Session)(nil)
