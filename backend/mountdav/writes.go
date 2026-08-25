package mountdav

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

const statusInsufficientStorage = 507

func (application *readApplication) servePut(response http.ResponseWriter, request *http.Request) {
	path, status := application.requestResourcePath(request, false)
	if status != 0 {
		writeHTTPError(response, status)
		return
	}
	if request.Header.Get("Content-Range") != "" {
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

	operationID, err := randomOperationID()
	if err != nil {
		writeHTTPError(response, http.StatusInternalServerError)
		return
	}
	result, err := application.writer.Put(request.Context(), PutRequest{
		OperationID:   operationID,
		Path:          path,
		ContentLength: request.ContentLength,
		ContentType:   request.Header.Get("Content-Type"),
		Conditions:    conditions,
	}, request.Body)
	if err != nil {
		serveWriteError(response, err)
		return
	}
	setCommittedETag(response.Header(), result.ETag)
	if result.Created {
		response.WriteHeader(http.StatusCreated)
		return
	}
	response.WriteHeader(http.StatusNoContent)
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
	operationID, err := randomOperationID()
	if err != nil {
		writeHTTPError(response, http.StatusInternalServerError)
		return
	}
	_, err = application.writer.Mkdir(request.Context(), MkdirRequest{
		OperationID: operationID,
		Path:        path,
		Conditions:  conditions,
	})
	if err != nil {
		serveWriteError(response, err)
		return
	}
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
	operationID, err := randomOperationID()
	if err != nil {
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
		serveWriteError(response, err)
		return
	}
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
	if depth := request.Header.Get("Depth"); depth != "" && depth != "infinity" {
		writeHTTPError(response, http.StatusBadRequest)
		return
	}
	path, status := application.requestResourcePath(request, false)
	if status != 0 {
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
	operationID, err := randomOperationID()
	if err != nil {
		writeHTTPError(response, http.StatusInternalServerError)
		return
	}
	_, err = application.writer.Delete(request.Context(), DeleteRequest{
		OperationID: operationID,
		Path:        path,
		Conditions:  conditions,
	})
	if err != nil {
		serveWriteError(response, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
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
