package mountdav

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	allowedMethodsHeader      = "OPTIONS, PROPFIND, HEAD, GET"
	writableMethodsHeader     = "OPTIONS, PROPFIND, PROPPATCH, HEAD, GET, PUT, MKCOL, MOVE, DELETE, LOCK, UNLOCK"
	windowsNoRootDepth        = "1,noroot"
	maxRequestBodyBytes       = int64(1 << 20)
	defaultMaxConcurrent      = 32
	defaultMaxWriteConcurrent = 4
	defaultPUTBodyIdleTimeout = 30 * time.Second
	defaultControlBodyTimeout = 15 * time.Second
	serverBusyRetrySeconds    = "1"
)

type propfindRequestOptions struct {
	omitRoot bool
}

type propfindRequestOptionsKey struct{}

type protectionConfig struct {
	capabilityPath         string
	authority              string
	maxConcurrent          int
	maxConcurrentWrite     int
	writable               bool
	enforceBodyReadTimeout bool
	putBodyIdleTimeout     time.Duration
	controlBodyReadTimeout time.Duration
	bodyReadNow            func() time.Time
}

type protectedHandler struct {
	config     protectionConfig
	next       http.Handler
	slots      chan struct{}
	writeSlots chan struct{}
}

func newProtectedHandler(config protectionConfig, next http.Handler) http.Handler {
	if config.maxConcurrent <= 0 {
		config.maxConcurrent = defaultMaxConcurrent
	}
	if config.maxConcurrentWrite <= 0 {
		config.maxConcurrentWrite = defaultMaxWriteConcurrent
	}
	if config.putBodyIdleTimeout <= 0 {
		config.putBodyIdleTimeout = defaultPUTBodyIdleTimeout
	}
	if config.controlBodyReadTimeout <= 0 {
		config.controlBodyReadTimeout = defaultControlBodyTimeout
	}
	if config.bodyReadNow == nil {
		config.bodyReadNow = time.Now
	}
	var writeSlots chan struct{}
	if config.writable {
		writeSlots = make(chan struct{}, config.maxConcurrentWrite)
	}
	return &protectedHandler{
		config:     config,
		next:       next,
		slots:      make(chan struct{}, config.maxConcurrent),
		writeSlots: writeSlots,
	}
}

func (handler *protectedHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	setProtocolHeaders(response.Header(), handler.config.writable)
	if request.Host != handler.config.authority ||
		!isLoopbackPeer(request.RemoteAddr) ||
		hasHeader(request.Header, "Origin") ||
		hasHeader(request.Header, "Sec-Fetch-Site") {
		writeHTTPError(response, http.StatusForbidden)
		return
	}
	if trustedRootOptionsProbe(request) {
		response.Header().Set("Content-Length", "0")
		response.WriteHeader(http.StatusOK)
		return
	}
	if !matchesCapability(request, handler.config.capabilityPath) {
		http.NotFound(response, request)
		return
	}
	// Log the path with the capability segment stripped: the full escaped
	// path/URL is a bearer token for this mount and must never be logged.
	slog.Debug("mountdav: request", "method", request.Method,
		"path", strings.TrimPrefix(request.URL.EscapedPath(), handler.config.capabilityPath), "content_length", request.ContentLength)
	if !allowedMethod(request.Method, handler.config.writable) {
		slog.Debug("mountdav: method not allowed", "method", request.Method, "writable", handler.config.writable)
		writeHTTPError(response, http.StatusMethodNotAllowed)
		return
	}
	if request.Method == "PROPFIND" {
		var status int
		request, status = normalizePropfindDepth(request)
		if status != 0 {
			writeHTTPError(response, status)
			return
		}
		request = canonicalizeRootPropfindPath(request, handler.config.capabilityPath)
	}
	select {
	case handler.slots <- struct{}{}:
		defer func() { <-handler.slots }()
	default:
		slog.Warn("mountdav: server busy, concurrent request limit reached", "max_concurrent", cap(handler.slots))
		response.Header().Set("Retry-After", serverBusyRetrySeconds)
		writeHTTPError(response, http.StatusServiceUnavailable)
		return
	}
	if handler.config.writable && isWriteMethod(request.Method) {
		select {
		case handler.writeSlots <- struct{}{}:
			defer func() { <-handler.writeSlots }()
		default:
			slog.Warn("mountdav: server busy, concurrent write limit reached", "max_concurrent_write", cap(handler.writeSlots))
			response.Header().Set("Retry-After", serverBusyRetrySeconds)
			writeHTTPError(response, http.StatusServiceUnavailable)
			return
		}
	}
	var (
		body []byte
		err  error
	)
	if handler.config.writable && request.Method == http.MethodPut {
		if handler.config.enforceBodyReadTimeout {
			request = wrapPUTBodyWithIdleDeadline(
				response,
				request,
				handler.config.putBodyIdleTimeout,
				handler.config.bodyReadNow,
			)
		}
	} else if handler.config.writable && handler.config.enforceBodyReadTimeout {
		body, err = bufferBodyWithAbsoluteDeadline(
			response,
			request,
			handler.config.controlBodyReadTimeout,
			handler.config.bodyReadNow,
		)
	} else {
		body, err = bufferBoundedBody(response, request)
	}
	if err != nil {
		if isBodyReadTimeout(err) {
			writeHTTPError(response, http.StatusRequestTimeout)
			return
		}
		if errors.Is(err, errBodyDeadlineUnavailable) {
			writeHTTPError(response, http.StatusInternalServerError)
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
	if (request.Method == "PROPFIND" || request.Method == "PROPPATCH") &&
		len(body) > 0 && !isWellFormedXMLDocument(body) {
		writeHTTPError(response, http.StatusBadRequest)
		return
	}
	if request.Method == http.MethodOptions {
		response.Header().Set("Content-Length", "0")
		response.WriteHeader(http.StatusOK)
		return
	}
	handler.next.ServeHTTP(response, request)
}

func setProtocolHeaders(header http.Header, writable bool) {
	allow := allowedMethodsHeader
	dav := "1"
	if writable {
		allow = writableMethodsHeader
		dav = "1, 2"
	}
	header.Set("Allow", allow)
	header.Set("DAV", dav)
	header.Set("MS-Author-Via", "DAV")
	header.Set("Accept-Ranges", "bytes")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("Content-Security-Policy", "default-src 'none'; sandbox")
}

func matchesCapability(request *http.Request, capabilityPath string) bool {
	path := request.URL.EscapedPath()
	return path == capabilityPath || strings.HasPrefix(path, capabilityPath+"/")
}

func trustedRootOptionsProbe(request *http.Request) bool {
	if request.Method != http.MethodOptions {
		return false
	}
	path := request.URL.EscapedPath()
	return path == "/" || path == "*"
}

func isLoopbackPeer(remoteAddress string) bool {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func hasHeader(header http.Header, name string) bool {
	for key := range header {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

func allowedMethod(method string, writable bool) bool {
	switch method {
	case http.MethodOptions, "PROPFIND", http.MethodHead, http.MethodGet:
		return true
	case http.MethodPut, "MKCOL", "MOVE", http.MethodDelete, "LOCK", "UNLOCK", "PROPPATCH", "COPY":
		return writable
	default:
		return false
	}
}

func isWriteMethod(method string) bool {
	switch method {
	case http.MethodPut, "MKCOL", "MOVE", http.MethodDelete, "LOCK", "UNLOCK", "PROPPATCH", "COPY":
		return true
	default:
		return false
	}
}

func normalizePropfindDepth(request *http.Request) (*http.Request, int) {
	switch request.Header.Get("Depth") {
	case "0", "1":
		return request, 0
	case windowsNoRootDepth:
		ctx := context.WithValue(
			request.Context(),
			propfindRequestOptionsKey{},
			propfindRequestOptions{omitRoot: true},
		)
		normalized := request.Clone(ctx)
		normalized.Header = request.Header.Clone()
		normalized.Header.Set("Depth", "1")
		return normalized, 0
	case "", "infinity", "infinity,noroot":
		return request, http.StatusForbidden
	default:
		return request, http.StatusBadRequest
	}
}

func propfindOmitsRoot(request *http.Request) bool {
	options, ok := request.Context().Value(propfindRequestOptionsKey{}).(propfindRequestOptions)
	return ok && options.omitRoot
}

func canonicalizeRootPropfindPath(request *http.Request, capabilityPath string) *http.Request {
	if request.URL.EscapedPath() != capabilityPath {
		return request
	}
	normalized := request.Clone(request.Context())
	urlCopy := *request.URL
	urlCopy.Path = capabilityPath + "/"
	urlCopy.RawPath = ""
	normalized.URL = &urlCopy
	return normalized
}

func bufferBoundedBody(response http.ResponseWriter, request *http.Request) ([]byte, error) {
	if request.Body == nil || request.Body == http.NoBody {
		return nil, nil
	}
	limited := http.MaxBytesReader(response, request.Body, maxRequestBodyBytes)
	body, err := io.ReadAll(limited)
	closeErr := limited.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.ContentLength = int64(len(body))
	return body, nil
}

func isWellFormedXMLDocument(body []byte) bool {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	depth := 0
	rootSeen := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return rootSeen && depth == 0
		}
		if err != nil {
			return false
		}
		switch token := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				if rootSeen {
					return false
				}
				rootSeen = true
			}
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && len(bytes.TrimSpace(token)) > 0 {
				return false
			}
		}
	}
}

func writeHTTPError(response http.ResponseWriter, status int) {
	http.Error(response, http.StatusText(status), status)
}
