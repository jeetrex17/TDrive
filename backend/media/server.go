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

const (
	mediaRoutePrefix       = "/media/file/"
	mediaThumbRoutePrefix  = "/media/thumb/"
	mediaThumbSourcePrefix = "/media/thumb-source/"
	mediaSessionIdleTTL    = 2 * time.Hour
	mediaSessionSweepEvery = 5 * time.Minute
	mediaStreamChunkSize   = 256 * 1024
)

var mediaStreamBufferPool = sync.Pool{
	New: func() any {
		buf := make([]byte, mediaStreamChunkSize)
		return &buf
	},
}

type PlaybackUpdate struct {
	Token       string  `json:"token"`
	CurrentTime float64 `json:"current_time"`
	Duration    float64 `json:"duration"`
	// BufferAhead is the number of seconds the player has buffered ahead of the
	// playhead. The thumbnail scheduler uses it to steal slack: build aggressively
	// while the buffer is comfortable and yield to playback as it drains.
	BufferAhead float64 `json:"buffer_ahead"`
}

type Server struct {
	owner *Service

	mu       sync.Mutex
	listener net.Listener
	server   *http.Server
	baseURL  string
	closed   bool
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
	if s.closed {
		s.mu.Unlock()
		return ErrSessionNotFound
	}
	s.sessions[session.Token()] = session
	url := s.baseURL + mediaRoutePrefix + session.Token()
	thumbSourceURL := s.baseURL + mediaThumbSourcePrefix + session.Token()
	thumbURL := s.baseURL + mediaThumbRoutePrefix + session.Token()
	s.mu.Unlock()
	session.setURL(url)
	session.setThumbnailURLs(thumbSourceURL, thumbURL)
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

func (s *Server) UpdatePlayback(update PlaybackUpdate) error {
	session := s.session(update.Token)
	if session == nil {
		return ErrSessionNotFound
	}
	session.touch()
	session.UpdatePlayback(update.CurrentTime, update.Duration, update.BufferAhead)
	return nil
}

func (s *Server) Stats(token string) MediaStats {
	session := s.session(token)
	if session == nil {
		return MediaStats{}
	}
	session.touch()
	stats := session.Stats()
	session.logStats(stats)
	return stats
}

func (s *Server) Close() error {
	s.mu.Lock()
	server := s.server
	listener := s.listener
	s.server = nil
	s.listener = nil
	s.baseURL = ""
	s.closed = true
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
	if s.closed {
		s.mu.Unlock()
		return ErrSessionNotFound
	}
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
	mux.HandleFunc(mediaThumbSourcePrefix, s.handleThumbSource)
	mux.HandleFunc(mediaThumbRoutePrefix, s.handleThumbnail)
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
	go s.sweepIdleSessions(srv)
	return nil
}

func (s *Server) sweepIdleSessions(server *http.Server) {
	ticker := time.NewTicker(mediaSessionSweepEvery)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		if s.server != server || s.closed {
			s.mu.Unlock()
			return
		}
		now := time.Now()
		expired := make([]*Session, 0)
		for token, session := range s.sessions {
			if session == nil || now.Sub(session.LastTouch()) < mediaSessionIdleTTL {
				continue
			}
			delete(s.sessions, token)
			expired = append(expired, session)
		}
		s.mu.Unlock()

		for _, session := range expired {
			session.Close()
		}
	}
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	s.handleSessionBytes(w, r, mediaRoutePrefix, (*Session).ReadAt)
}

func (s *Server) handleThumbSource(w http.ResponseWriter, r *http.Request) {
	s.handleSessionBytes(w, r, mediaThumbSourcePrefix, (*Session).ReadThumbAt)
}

func (s *Server) handleSessionBytes(w http.ResponseWriter, r *http.Request, prefix string, readAt func(*Session, context.Context, []byte, int64) (int, error)) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := strings.TrimPrefix(path.Clean(r.URL.Path), prefix)
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
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Range")
	w.Header().Set("Access-Control-Expose-Headers", "Accept-Ranges, Content-Length, Content-Range")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

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
	if err := streamSessionRange(r.Context(), w, session, start, length, readAt); err != nil {
		return
	}
}

func (s *Server) handleThumbnail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := strings.TrimPrefix(path.Clean(r.URL.Path), mediaThumbRoutePrefix)
	if token == "" || strings.Contains(token, "/") {
		http.NotFound(w, r)
		return
	}
	session := s.session(token)
	if session == nil {
		http.NotFound(w, r)
		return
	}
	seconds, err := strconv.ParseFloat(r.URL.Query().Get("t"), 64)
	if err != nil || seconds < 0 {
		http.Error(w, "invalid thumbnail time", http.StatusBadRequest)
		return
	}
	data, err := session.Thumbnail(r.Context(), seconds)
	switch {
	case err == nil:
		w.Header().Set("Content-Type", videoThumbMime)
		w.Header().Set("Cache-Control", "private, max-age=86400")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(data)
		}
	case errors.Is(err, ErrThumbnailPending):
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusAccepted)
	case errors.Is(err, ErrThumbnailUnavailable):
		http.Error(w, "thumbnail unavailable", http.StatusServiceUnavailable)
	default:
		http.Error(w, "thumbnail error", http.StatusInternalServerError)
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

func streamSessionRange(ctx context.Context, w io.Writer, session *Session, start, length int64, readAt func(*Session, context.Context, []byte, int64) (int, error)) error {
	bufPtr := mediaStreamBufferPool.Get().(*[]byte)
	buf := *bufPtr
	defer mediaStreamBufferPool.Put(bufPtr)
	var sent int64
	for sent < length {
		want := length - sent
		if want > int64(len(buf)) {
			want = int64(len(buf))
		}
		n, err := readAt(session, ctx, buf[:want], start+sent)
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
	switch strings.ToLower(ext) {
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".mov", ".qt":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	case ".mkv", ".mk3d":
		return "video/x-matroska"
	case ".avi":
		return "video/x-msvideo"
	case ".ts", ".m2ts", ".mts":
		return "video/mp2t"
	case ".flv":
		return "video/x-flv"
	case ".wmv":
		return "video/x-ms-wmv"
	case ".ogv":
		return "video/ogg"
	case ".mpeg", ".mpg":
		return "video/mpeg"
	case ".mp3":
		return "audio/mpeg"
	case ".m4a":
		return "audio/mp4"
	case ".aac":
		return "audio/aac"
	case ".wav":
		return "audio/wav"
	case ".flac":
		return "audio/flac"
	case ".oga", ".ogg", ".opus":
		return "audio/ogg"
	case ".pdf":
		return "application/pdf"
	case ".txt", ".log", ".md", ".markdown", ".csv", ".tsv", ".json", ".yaml", ".yml", ".toml", ".xml", ".srt", ".vtt":
		return "text/plain; charset=utf-8"
	}
	if typ := mime.TypeByExtension(ext); typ != "" {
		return typ
	}
	return "application/octet-stream"
}

func isSupportedMediaName(name string) bool {
	return streamKindForName(name) == StreamKindVideo
}

func isSupportedStreamName(name string) bool {
	return streamKindForName(name) != StreamKindUnknown
}

func streamKindForName(name string) StreamKind {
	switch strings.ToLower(path.Ext(name)) {
	case ".mp4", ".m4v", ".mov", ".qt", ".webm", ".mkv", ".mk3d", ".avi",
		".ts", ".m2ts", ".mts", ".flv", ".wmv", ".ogv", ".mpeg", ".mpg":
		return StreamKindVideo
	case ".mp3", ".m4a", ".aac", ".wav", ".flac", ".oga", ".ogg", ".opus":
		return StreamKindAudio
	case ".pdf":
		return StreamKindPDF
	case ".txt", ".log", ".md", ".markdown", ".csv", ".tsv", ".json", ".yaml", ".yml", ".toml", ".xml", ".srt", ".vtt":
		return StreamKindText
	default:
		return StreamKindUnknown
	}
}
