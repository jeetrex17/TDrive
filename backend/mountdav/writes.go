package mountdav

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
)

const statusInsufficientStorage = 507

func (application *readApplication) servePut(response http.ResponseWriter, request *http.Request) {
	path, status := application.requestResourcePath(request, false)
	if status != 0 {
		writeHTTPError(response, status)
		return
	}
	if isMacOSJunkPath(path) {
		fakeJunkWriteSuccess(response, request, http.StatusCreated)
		return
	}
	contentRangeHeader := request.Header.Get("Content-Range")
	rng, hasRange, rangeErr := parseContentRange(contentRangeHeader)
	if rangeErr != nil {
		writeHTTPError(response, http.StatusBadRequest)
		return
	}
	conditions, status := parseMutationConditions(request, path, application.resolveTaggedResource(request))
	if status != 0 {
		writeHTTPError(response, status)
		return
	}
	release, status := application.confirmMutationLocks([]string{path}, &conditions)
	if status != 0 {
		if status == http.StatusServiceUnavailable {
			response.Header().Set("Retry-After", serverBusyRetrySeconds)
		}
		writeHTTPError(response, status)
		return
	}
	defer release()

	if application.pendingCreates != nil {
		application.pendingCreates.supersede(path)
	}

	slog.Debug("mountdav: serving PUT", "path", path, "content_length", request.ContentLength, "has_range", hasRange)

	if hasRange {
		application.servePutResume(response, request, path, rng, conditions)
		return
	}

	if request.ContentLength == 0 && application.tryDeferEmptyCreate(request.Context(), path) {
		slog.Debug("mountdav: deferred empty create, responding success without committing yet", "path", path)
		response.WriteHeader(http.StatusCreated)
		return
	}

	body := request.Body
	contentLength := request.ContentLength
	if contentLength < 0 {
		// No Content-Length header (chunked Transfer-Encoding) -- observed
		// in practice from macOS Finder's copy engine for the real-content
		// step of its two-step write. Buffer to learn the real size, since
		// an encrypted write requires it known upfront. See
		// bufferUnknownLengthPUTBody's doc comment for the full mechanism.
		slog.Debug("mountdav: PUT has no Content-Length, buffering to determine size", "path", path)
		buffered, size, err := bufferUnknownLengthPUTBody(response, request.Body)
		if err != nil {
			slog.Warn("mountdav: failed to buffer chunked PUT body", "path", path, "error", err)
			if isBodyReadTimeout(err) {
				writeHTTPError(response, http.StatusRequestTimeout)
				return
			}
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeHTTPError(response, http.StatusRequestEntityTooLarge)
				return
			}
			writeHTTPError(response, http.StatusBadRequest)
			return
		}
		defer closeAndRemoveTempFile(buffered)
		body = buffered
		contentLength = size
		slog.Debug("mountdav: buffered chunked PUT body", "path", path, "content_length", contentLength)
	}

	operationID, err := randomOperationID()
	if err != nil {
		slog.Error("mountdav: PUT failed to generate operation id", "path", path, "error", err)
		writeHTTPError(response, http.StatusInternalServerError)
		return
	}
	result, err := application.writer.Put(request.Context(), PutRequest{
		OperationID:   operationID,
		Path:          path,
		ContentLength: contentLength,
		ContentType:   request.Header.Get("Content-Type"),
		Conditions:    conditions,
	}, body)
	if err != nil {
		slog.Warn("mountdav: PUT rejected by coordinator", "path", path, "content_length", contentLength, "error", err)
		serveWriteError(response, err)
		return
	}
	slog.Debug("mountdav: PUT committed", "path", path, "content_length", contentLength, "created", result.Created, "etag_set", result.ETag != "")
	setCommittedETag(response.Header(), result.ETag)
	if result.Created {
		response.WriteHeader(http.StatusCreated)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

// servePutResume applies one Content-Range PUT chunk. macOS's WebDAV client
// resumes an interrupted upload this way: an initial plain PUT commits
// whatever bytes it managed to send, then Content-Range chunk(s) continue
// from there. Non-final chunks are buffered on disk (see resumeStore) and
// never reach the write coordinator; the final chunk assembles the complete
// content and commits it through the exact same application.writer.Put path
// a normal PUT uses, so mountwrite/mountadapter need no awareness this
// chunking happened.
func (application *readApplication) servePutResume(
	response http.ResponseWriter,
	request *http.Request,
	path string,
	rng contentRange,
	conditions MutationConditions,
) {
	if application.resume == nil {
		writeHTTPError(response, http.StatusBadRequest)
		return
	}
	openCurrent := func() (io.ReadCloser, int64, error) {
		file, err := application.fs.OpenFile(request.Context(), path, os.O_RDONLY, 0)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, 0, nil
			}
			return nil, 0, err
		}
		info, statErr := file.Stat()
		if statErr != nil {
			_ = file.Close()
			return nil, 0, statErr
		}
		if info.IsDir() {
			_ = file.Close()
			return nil, 0, os.ErrInvalid
		}
		return file, info.Size(), nil
	}
	slog.Debug("mountdav: PUT resume chunk", "path", path, "start", rng.start, "end", rng.end, "total", rng.total)
	assembled, complete, err := application.resume.appendResumeChunk(path, rng, conditions.LockTokens, openCurrent, request.Body)
	if err != nil {
		slog.Warn("mountdav: PUT resume chunk rejected", "path", path, "start", rng.start, "end", rng.end, "total", rng.total, "error", err)
		writeResumeError(response, err)
		return
	}
	if !complete {
		slog.Debug("mountdav: PUT resume chunk buffered, sequence not yet complete", "path", path, "end", rng.end, "total", rng.total)
		response.WriteHeader(http.StatusNoContent)
		return
	}
	defer closeAndRemoveResumeFile(assembled)

	operationID, err := randomOperationID()
	if err != nil {
		slog.Error("mountdav: PUT resume failed to generate operation id", "path", path, "error", err)
		writeHTTPError(response, http.StatusInternalServerError)
		return
	}
	result, err := application.writer.Put(request.Context(), PutRequest{
		OperationID:   operationID,
		Path:          path,
		ContentLength: rng.total,
		ContentType:   request.Header.Get("Content-Type"),
		Conditions:    conditions,
	}, assembled)
	if err != nil {
		slog.Warn("mountdav: PUT resume assembled commit rejected by coordinator", "path", path, "total", rng.total, "error", err)
		serveWriteError(response, err)
		return
	}
	slog.Debug("mountdav: PUT resume sequence committed", "path", path, "total", rng.total, "created", result.Created)
	setCommittedETag(response.Header(), result.ETag)
	if result.Created {
		response.WriteHeader(http.StatusCreated)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func writeResumeError(response http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case isBodyReadTimeout(err):
		status = http.StatusRequestTimeout
	case errors.Is(err, ErrResumeRangeInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, ErrResumeOffsetMismatch):
		status = http.StatusConflict
	case errors.Is(err, ErrResumeTooLarge):
		status = http.StatusRequestEntityTooLarge
	}
	writeHTTPError(response, status)
}

func (application *readApplication) serveMkdir(response http.ResponseWriter, request *http.Request) {
	if request.ContentLength != 0 {
		writeHTTPError(response, http.StatusUnsupportedMediaType)
		return
	}
	path, status := application.requestResourcePath(request, false)
	if status != 0 {
		writeHTTPError(response, status)
		return
	}
	if isMacOSJunkPath(path) {
		fakeJunkWriteSuccess(response, request, http.StatusCreated)
		return
	}
	conditions, status := parseMutationConditions(request, path, application.resolveTaggedResource(request))
	if status != 0 {
		writeHTTPError(response, status)
		return
	}
	release, status := application.confirmMutationLocks([]string{path}, &conditions)
	if status != 0 {
		writeHTTPError(response, status)
		return
	}
	defer release()
	if application.pendingCreates != nil {
		application.pendingCreates.supersede(path)
	}
	slog.Debug("mountdav: serving MKCOL", "path", path)
	operationID, err := randomOperationID()
	if err != nil {
		slog.Error("mountdav: MKCOL failed to generate operation id", "path", path, "error", err)
		writeHTTPError(response, http.StatusInternalServerError)
		return
	}
	_, err = application.writer.Mkdir(request.Context(), MkdirRequest{
		OperationID: operationID,
		Path:        path,
		Conditions:  conditions,
	})
	if err != nil {
		slog.Warn("mountdav: MKCOL rejected by coordinator", "path", path, "error", err)
		serveWriteError(response, err)
		return
	}
	slog.Debug("mountdav: MKCOL committed", "path", path)
	response.WriteHeader(http.StatusCreated)
}

func (application *readApplication) serveMove(response http.ResponseWriter, request *http.Request) {
	if request.ContentLength != 0 {
		writeHTTPError(response, http.StatusUnsupportedMediaType)
		return
	}
	if depth := request.Header.Get("Depth"); depth != "" && depth != "infinity" {
		writeHTTPError(response, http.StatusBadRequest)
		return
	}
	source, status := application.requestResourcePath(request, false)
	if status != 0 {
		writeHTTPError(response, status)
		return
	}
	destination, status := application.parseDestination(request)
	if status != 0 {
		writeHTTPError(response, status)
		return
	}
	overwrite, ok := parseOverwrite(request.Header.Values("Overwrite"))
	if !ok {
		writeHTTPError(response, http.StatusBadRequest)
		return
	}
	conditions, status := parseMutationConditions(request, source, application.resolveTaggedResource(request))
	if status != 0 {
		writeHTTPError(response, status)
		return
	}
	release, status := application.confirmMutationLocks([]string{source, destination}, &conditions)
	if status != 0 {
		writeHTTPError(response, status)
		return
	}
	defer release()
	slog.Debug("mountdav: serving MOVE", "source", source, "destination", destination, "overwrite", overwrite)
	operationID, err := randomOperationID()
	if err != nil {
		slog.Error("mountdav: MOVE failed to generate operation id", "source", source, "error", err)
		writeHTTPError(response, http.StatusInternalServerError)
		return
	}
	result, err := application.writer.Move(request.Context(), MoveRequest{
		OperationID:     operationID,
		SourcePath:      source,
		DestinationPath: destination,
		Overwrite:       overwrite,
		Conditions:      conditions,
	})
	if err != nil {
		slog.Warn("mountdav: MOVE rejected by coordinator", "source", source, "destination", destination, "error", err)
		serveWriteError(response, err)
		return
	}
	slog.Debug("mountdav: MOVE committed", "source", source, "destination", destination, "created", result.Created)
	setCommittedETag(response.Header(), result.ETag)
	if result.Created {
		response.WriteHeader(http.StatusCreated)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (application *readApplication) serveDelete(response http.ResponseWriter, request *http.Request) {
	if request.ContentLength != 0 {
		writeHTTPError(response, http.StatusUnsupportedMediaType)
		return
	}
	path, status := application.requestResourcePath(request, false)
	if status != 0 {
		writeHTTPError(response, status)
		return
	}
	if isMacOSJunkPath(path) {
		fakeJunkWriteSuccess(response, request, http.StatusNoContent)
		return
	}
	if status = application.validateDeleteDepth(request, path); status != 0 {
		if status == http.StatusServiceUnavailable {
			response.Header().Set("Retry-After", serverBusyRetrySeconds)
		}
		writeHTTPError(response, status)
		return
	}
	conditions, status := parseMutationConditions(request, path, application.resolveTaggedResource(request))
	if status != 0 {
		writeHTTPError(response, status)
		return
	}
	release, status := application.confirmMutationLocks([]string{path}, &conditions)
	if status != 0 {
		writeHTTPError(response, status)
		return
	}
	defer release()
	if application.pendingCreates != nil {
		application.pendingCreates.supersede(path)
	}
	slog.Debug("mountdav: serving DELETE", "path", path)
	operationID, err := randomOperationID()
	if err != nil {
		slog.Error("mountdav: DELETE failed to generate operation id", "path", path, "error", err)
		writeHTTPError(response, http.StatusInternalServerError)
		return
	}
	_, err = application.writer.Delete(request.Context(), DeleteRequest{
		OperationID: operationID,
		Path:        path,
		Conditions:  conditions,
	})
	if err != nil {
		slog.Warn("mountdav: DELETE rejected by coordinator", "path", path, "error", err)
		serveWriteError(response, err)
		return
	}
	slog.Debug("mountdav: DELETE committed", "path", path)
	response.WriteHeader(http.StatusNoContent)
}

func (application *readApplication) validateDeleteDepth(request *http.Request, path string) int {
	depth := request.Header.Get("Depth")
	if depth == "" || depth == "infinity" {
		return 0
	}
	switch depth {
	case "0", "1", "1,noroot", "infinity,noroot":
	default:
		return http.StatusBadRequest
	}
	info, err := application.fs.Stat(request.Context(), path)
	if err != nil {
		return fileErrorStatus(err)
	}
	// RFC 4918 requires Depth to be ignored for resources without internal
	// members. MiniRedir may still send a Depth extension when deleting a file.
	if !info.IsDir() {
		return 0
	}
	return http.StatusBadRequest
}

// fakeJunkWriteSuccess answers a mutation targeting macOS's own metadata
// (see isMacOSJunkPath) with success without staging, coordinating, locking,
// or storing anything for it. It drains any request body first (PUT sends
// one; MKCOL/DELETE reject a non-empty one before ever reaching this) so a
// client mid-upload sees the success response it expects rather than a
// connection reset.
func fakeJunkWriteSuccess(response http.ResponseWriter, request *http.Request, status int) {
	slog.Debug("mountdav: faking success for macOS junk path, nothing stored", "path", request.URL.Path, "method", request.Method, "status", status)
	if request.Body != nil {
		_, _ = io.Copy(io.Discard, request.Body)
	}
	response.WriteHeader(status)
}

func (application *readApplication) requestResourcePath(request *http.Request, allowRoot bool) (string, int) {
	if request.URL.RawQuery != "" || hasEncodedSeparator(request.URL.EscapedPath()) {
		return "", http.StatusBadRequest
	}
	path := request.URL.Path
	if path != application.capabilityPath && !strings.HasPrefix(path, application.capabilityPath+"/") {
		return "", http.StatusNotFound
	}
	relative := strings.TrimPrefix(path, application.capabilityPath)
	clean, err := cleanWritablePath(relative)
	if err != nil {
		return "", http.StatusBadRequest
	}
	if !allowRoot && clean == "/" {
		return "", http.StatusForbidden
	}
	return clean, 0
}

func (application *readApplication) parseDestination(request *http.Request) (string, int) {
	values := request.Header.Values("Destination")
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		return "", http.StatusBadRequest
	}
	parsed, err := url.ParseRequestURI(values[0])
	if err != nil || parsed.Fragment != "" || parsed.RawQuery != "" || parsed.User != nil {
		return "", http.StatusBadRequest
	}
	if parsed.IsAbs() {
		if parsed.Scheme != "http" || parsed.Host != application.authority {
			return "", http.StatusBadGateway
		}
	} else if parsed.Host != "" || !strings.HasPrefix(parsed.Path, "/") {
		return "", http.StatusBadRequest
	}
	if hasEncodedSeparator(parsed.EscapedPath()) {
		return "", http.StatusBadRequest
	}
	if parsed.EscapedPath() != application.capabilityPath &&
		!strings.HasPrefix(parsed.EscapedPath(), application.capabilityPath+"/") {
		return "", http.StatusBadGateway
	}
	path := strings.TrimPrefix(parsed.Path, application.capabilityPath)
	clean, err := cleanWritablePath(path)
	if err != nil {
		return "", http.StatusBadRequest
	}
	if clean == "/" {
		return "", http.StatusForbidden
	}
	return clean, 0
}

func (application *readApplication) resolveTaggedResource(request *http.Request) func(string) (string, int) {
	return func(value string) (string, int) {
		parsed, err := url.Parse(value)
		if err != nil || !parsed.IsAbs() || parsed.Scheme != "http" ||
			parsed.Host != application.authority || parsed.Fragment != "" || parsed.RawQuery != "" || parsed.User != nil {
			return "", http.StatusBadRequest
		}
		copy := request.Clone(request.Context())
		copy.URL = parsed
		return application.requestResourcePath(copy, true)
	}
}

func hasEncodedSeparator(escapedPath string) bool {
	lower := strings.ToLower(escapedPath)
	return strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c")
}

func parseOverwrite(values []string) (bool, bool) {
	if len(values) == 0 {
		return true, true
	}
	if len(values) != 1 {
		return false, false
	}
	switch values[0] {
	case "T":
		return true, true
	case "F":
		return false, true
	default:
		return false, false
	}
}

func randomOperationID() (string, error) {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(entropy[:]), nil
}

func setCommittedETag(header http.Header, etag string) {
	if etag != "" && validStrongETag(etag) {
		header.Set("ETag", etag)
	}
}

func serveWriteError(response http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case isBodyReadTimeout(err):
		status = http.StatusRequestTimeout
	case errors.Is(err, ErrWriteInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, ErrWriteForbidden):
		status = http.StatusForbidden
	case errors.Is(err, ErrWriteNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrWriteConflict):
		status = http.StatusConflict
	case errors.Is(err, ErrWritePreconditionFailed):
		status = http.StatusPreconditionFailed
	case errors.Is(err, ErrWriteLengthRequired):
		status = http.StatusLengthRequired
	case errors.Is(err, ErrWriteTooLarge):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, ErrWriteLocked):
		status = statusLocked
	case errors.Is(err, ErrWriteInsufficientStorage):
		status = statusInsufficientStorage
	case errors.Is(err, ErrWriteUnavailable):
		response.Header().Set("Retry-After", serverBusyRetrySeconds)
		status = http.StatusServiceUnavailable
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status = http.StatusRequestTimeout
	}
	writeHTTPError(response, status)
}
