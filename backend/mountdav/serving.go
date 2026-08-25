package mountdav

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"TDrive/backend/mountfs"

	"golang.org/x/net/webdav"
)

type readApplication struct {
	capabilityPath string
	authority      string
	fs             *FileSystem
	lockSystem     webdav.LockSystem
	writer         WriteCoordinator
}

func (application *readApplication) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet, http.MethodHead:
		application.serveFile(response, request)
	case "PROPFIND":
		application.servePropfind(response, request)
	case http.MethodPut:
		application.serveWritable(response, request, application.servePut)
	case "MKCOL":
		application.serveWritable(response, request, application.serveMkdir)
	case "MOVE":
		application.serveWritable(response, request, application.serveMove)
	case http.MethodDelete:
		application.serveWritable(response, request, application.serveDelete)
	case "LOCK":
		application.serveWritable(response, request, application.serveLock)
	case "UNLOCK":
		application.serveWritable(response, request, application.serveUnlock)
	case "COPY":
		if application.writer == nil {
			writeHTTPError(response, http.StatusMethodNotAllowed)
			return
		}
		// Deliberate first-release policy: never emulate COPY as GET+PUT and
		// never report success for a capability the coordinator cannot commit.
		writeHTTPError(response, http.StatusNotImplemented)
	default:
		writeHTTPError(response, http.StatusMethodNotAllowed)
	}
}

func (application *readApplication) serveWritable(
	response http.ResponseWriter,
	request *http.Request,
	next func(http.ResponseWriter, *http.Request),
) {
	if application.writer == nil || application.lockSystem == nil {
		writeHTTPError(response, http.StatusMethodNotAllowed)
		return
	}
	next(response, request)
}

func (application *readApplication) servePropfind(response http.ResponseWriter, request *http.Request) {
	name := strings.TrimPrefix(request.URL.Path, application.capabilityPath)
	snapshot, err := preflightPropfind(
		request.Context(),
		application.fs,
		name,
		request.Header.Get("Depth") == "1",
	)
	if err != nil {
		serveFileError(response, err)
		return
	}
	lockSystem := application.lockSystem
	if lockSystem == nil {
		lockSystem = webdav.NewMemLS()
	}
	handler := &webdav.Handler{
		Prefix:     application.capabilityPath,
		FileSystem: snapshot,
		LockSystem: lockSystem,
	}
	root, err := snapshot.Stat(request.Context(), name)
	if err != nil {
		serveFileError(response, err)
		return
	}
	if root.IsDir() && !strings.HasSuffix(request.URL.Path, "/") {
		request = cloneRequestWithTrailingPathSlash(request)
	}
	omitRootHref := ""
	if propfindOmitsRoot(request) {
		omitRootHref = propfindResponseHref(application.capabilityPath, name, root.IsDir())
	}
	if application.writer == nil {
		serveReadOnlyPropfind(response, request, handler, omitRootHref)
		return
	}
	serveWritablePropfind(response, request, handler, omitRootHref)
}

func cloneRequestWithTrailingPathSlash(request *http.Request) *http.Request {
	normalized := request.Clone(request.Context())
	normalized.URL.Path += "/"
	if normalized.URL.RawPath != "" && !strings.HasSuffix(normalized.URL.RawPath, "/") {
		normalized.URL.RawPath += "/"
	}
	return normalized
}

func (application *readApplication) serveFile(response http.ResponseWriter, request *http.Request) {
	name := strings.TrimPrefix(request.URL.Path, application.capabilityPath)
	clean, entry, err := application.fs.lookup(request.Context(), "open", name)
	if err != nil {
		serveFileError(response, err)
		return
	}
	info := newFileInfo(entry)
	if info.IsDir() {
		writeHTTPError(response, http.StatusMethodNotAllowed)
		return
	}
	if request.Method == http.MethodHead {
		if err := setFileHeaders(request.Context(), response.Header(), info); err != nil {
			serveFileError(response, err)
			return
		}
		http.ServeContent(response, request, info.Name(), info.ModTime(), &metadataReadSeeker{size: info.Size()})
		return
	}
	file, err := application.fs.openEntry(request.Context(), clean, entry)
	if err != nil {
		serveFileError(response, err)
		return
	}
	defer file.Close()
	if err := setFileHeaders(request.Context(), response.Header(), info); err != nil {
		serveFileError(response, err)
		return
	}
	http.ServeContent(response, request, info.Name(), info.ModTime(), file)
}

func setFileHeaders(ctx context.Context, header http.Header, info fileInfo) error {
	contentType, err := info.ContentType(ctx)
	if err != nil {
		return err
	}
	etag, err := info.ETag(ctx)
	if err != nil {
		return err
	}
	header.Set("Content-Type", contentType)
	header.Set("ETag", etag)
	return nil
}

type metadataReadSeeker struct {
	size   int64
	offset int64
}

func (content *metadataReadSeeker) Read(buffer []byte) (int, error) {
	if content.offset >= content.size {
		return 0, io.EOF
	}
	remaining := content.size - content.offset
	n := min(int64(len(buffer)), remaining)
	clear(buffer[:n])
	content.offset += n
	if content.offset == content.size {
		return int(n), io.EOF
	}
	return int(n), nil
}

func (content *metadataReadSeeker) Seek(offset int64, whence int) (int64, error) {
	next := offset
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		next = content.offset + offset
	case io.SeekEnd:
		next = content.size + offset
	default:
		return 0, os.ErrInvalid
	}
	if next < 0 {
		return 0, os.ErrInvalid
	}
	content.offset = next
	return next, nil
}

func serveFileError(response http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case os.IsNotExist(err):
		status = http.StatusNotFound
	case errors.Is(err, os.ErrPermission), errors.Is(err, mountfs.ErrAccessDenied):
		status = http.StatusForbidden
	case errors.Is(err, mountfs.ErrContentUnavailable):
		response.Header().Set("Retry-After", serverBusyRetrySeconds)
		status = http.StatusServiceUnavailable
	case errors.Is(err, os.ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status = http.StatusRequestTimeout
	}
	writeHTTPError(response, status)
}
