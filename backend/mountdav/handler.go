package mountdav

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
)

const (
	allowedMethodsHeader   = "OPTIONS, PROPFIND, HEAD, GET"
	maxRequestBodyBytes    = int64(1 << 20)
	defaultMaxConcurrent   = 32
	serverBusyRetrySeconds = "1"
)

type protectionConfig struct {
	capabilityPath string
	authority      string
	maxConcurrent  int
}

type protectedHandler struct {
	config protectionConfig
	next   http.Handler
	slots  chan struct{}
}

func newProtectedHandler(config protectionConfig, next http.Handler) http.Handler {
	if config.maxConcurrent <= 0 {
		config.maxConcurrent = defaultMaxConcurrent
	}
	return &protectedHandler{
		config: config,
		next:   next,
		slots:  make(chan struct{}, config.maxConcurrent),
	}
}

func (handler *protectedHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	setProtocolHeaders(response.Header())
	if !matchesCapability(request, handler.config.capabilityPath) {
		http.NotFound(response, request)
		return
	}
	if request.Host != handler.config.authority ||
		!isLoopbackPeer(request.RemoteAddr) ||
		hasHeader(request.Header, "Origin") ||
		hasHeader(request.Header, "Sec-Fetch-Site") {
		writeHTTPError(response, http.StatusForbidden)
		return
	}
	if !allowedMethod(request.Method) {
		writeHTTPError(response, http.StatusMethodNotAllowed)
		return
	}
	if request.Method == "PROPFIND" {
		if status := validateDepth(request.Header.Get("Depth")); status != 0 {
			writeHTTPError(response, status)
			return
		}
	}
	select {
	case handler.slots <- struct{}{}:
		defer func() { <-handler.slots }()
	default:
		response.Header().Set("Retry-After", serverBusyRetrySeconds)
		writeHTTPError(response, http.StatusServiceUnavailable)
		return
	}
	body, err := bufferBoundedBody(response, request)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeHTTPError(response, http.StatusRequestEntityTooLarge)
			return
		}
		writeHTTPError(response, http.StatusBadRequest)
		return
	}
	if request.Method == "PROPFIND" && len(body) > 0 && !isWellFormedXMLDocument(body) {
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

func setProtocolHeaders(header http.Header) {
	header.Set("Allow", allowedMethodsHeader)
	header.Set("DAV", "1")
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

func allowedMethod(method string) bool {
	switch method {
	case http.MethodOptions, "PROPFIND", http.MethodHead, http.MethodGet:
		return true
	default:
		return false
	}
}

func validateDepth(depth string) int {
	switch depth {
	case "0", "1":
		return 0
	case "", "infinity":
		return http.StatusForbidden
	default:
		return http.StatusBadRequest
	}
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
