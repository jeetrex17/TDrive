package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

const mediaRoutePrefix = "/media/file/"

type Server struct {
	owner *Service

	mu       sync.Mutex
	listener net.Listener
	server   *http.Server
	baseURL  string
	sessions map[string]*Session
}

func NewServer(owner *Service) *Server {
	return &Server{
		owner:    owner,
		sessions: make(map[string]*Session),
	}
}

func (s *Server) Add(session *Session) error {
	if session == nil {
		return fmt.Errorf("media: nil session")
	}
	if err := s.ensureStarted(); err != nil {
		return err
	}

	s.mu.Lock()
	s.sessions[session.Token()] = session
	url := s.baseURL + mediaRoutePrefix + session.Token()
	s.mu.Unlock()
	session.setURL(url)
	return nil
}

func (s *Server) CloseSession(token string) error {
	s.mu.Lock()
	session, ok := s.sessions[token]
	if ok {
		delete(s.sessions, token)
	}
	s.mu.Unlock()
	if !ok {
		return ErrSessionNotFound
	}
	session.Close()
	return nil
}

func (s *Server) Close() error {
	s.mu.Lock()
	server := s.server
	listener := s.listener
	s.server = nil
	s.listener = nil
	s.baseURL = ""
	sessions := s.sessions
	s.sessions = make(map[string]*Session)
	s.mu.Unlock()

	for _, session := range sessions {
		session.Close()
	}
	if server != nil {
		_ = server.Close()
	}
	if listener != nil {
		_ = listener.Close()
	}
	return nil
}

func (s *Server) ensureStarted() error {
	s.mu.Lock()
	if s.server != nil {
		s.mu.Unlock()
		return nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("media: listen loopback: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc(mediaRoutePrefix, s.handleFile)
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	s.listener = ln
	s.server = srv
	s.baseURL = "http://" + ln.Addr().String()
	s.mu.Unlock()

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			// There is intentionally no logging dependency in backend/media.
		}
	}()
	return nil
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := strings.TrimPrefix(path.Clean(r.URL.Path), mediaRoutePrefix)
	if token == "" || strings.Contains(token, "/") {
		http.NotFound(w, r)
		return
	}
	session := s.session(token)
	if session == nil {
		http.NotFound(w, r)
		return
	}

	size := session.Size()
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Type", contentTypeFor(session.Name()))

	start, end, partial, ok := parseRangeHeader(r.Header.Get("Range"), size)
	if !ok {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
		http.Error(w, "invalid range", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	if !partial {
		start, end = 0, size-1
	}

	length := int64(0)
	if size > 0 {
		length = end - start + 1
	}
	if partial {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
		w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		w.WriteHeader(http.StatusOK)
	}
	if r.Method == http.MethodHead || length == 0 {
		return
	}
	if err := streamSessionRange(r.Context(), w, session, start, length); err != nil {
		return
	}
}

func (s *Server) session(token string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[token]
}

func parseRangeHeader(raw string, size int64) (start, end int64, partial bool, ok bool) {
	if raw == "" {
		return 0, 0, false, true
	}
	if size <= 0 || !strings.HasPrefix(raw, "bytes=") || strings.Contains(raw, ",") {
		return 0, 0, true, false
	}
	spec := strings.TrimSpace(strings.TrimPrefix(raw, "bytes="))
	a, b, found := strings.Cut(spec, "-")
	if !found {
		return 0, 0, true, false
	}
	switch {
	case a == "":
		n, err := strconv.ParseInt(b, 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, true, false
		}
		if n > size {
			n = size
		}
		return size - n, size - 1, true, true
	case b == "":
		first, err := strconv.ParseInt(a, 10, 64)
		if err != nil || first < 0 || first >= size {
			return 0, 0, true, false
		}
		return first, size - 1, true, true
	default:
		first, err1 := strconv.ParseInt(a, 10, 64)
		last, err2 := strconv.ParseInt(b, 10, 64)
		if err1 != nil || err2 != nil || first < 0 || last < first || first >= size {
			return 0, 0, true, false
		}
		if last >= size {
			last = size - 1
		}
		return first, last, true, true
	}
}

func streamSessionRange(ctx context.Context, w io.Writer, session *Session, start, length int64) error {
	const chunkSize = 256 * 1024
	buf := make([]byte, chunkSize)
	var sent int64
	for sent < length {
		want := length - sent
		if want > int64(len(buf)) {
			want = int64(len(buf))
		}
		n, err := session.ReadAt(ctx, buf[:want], start+sent)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
			sent += int64(n)
		}
		if err != nil {
			if errors.Is(err, io.EOF) && sent == length {
				return nil
			}
			return err
		}
	}
	return nil
}

func contentTypeFor(name string) string {
	ext := path.Ext(name)
	if ext == "" {
		return "application/octet-stream"
	}
	if typ := mime.TypeByExtension(ext); typ != "" {
		return typ
	}
	return "application/octet-stream"
}
