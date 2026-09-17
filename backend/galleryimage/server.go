// Package galleryimage delivers bounded binary images over one scoped loopback
// session. It owns no image cache or per-image session: request cancellation
// flows directly into the shared file-service rendition scheduler.
package galleryimage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	file "TDrive/backend/services/file"
)

const (
	route          = "/gallery/"
	maxSessions    = 8
	sessionIdleTTL = 2 * time.Hour
)

type Loader func(ctx context.Context, channelID, msgID, revision int64, kind string) (file.Rendition, error)
type OpenResult struct {
	BaseURL   string `json:"base_url"`
	Token     string `json:"token"`
	ChannelID int64  `json:"channel_id"`
}
type session struct {
	channelID int64
	ctx       context.Context
	cancel    context.CancelFunc
	load      Loader
	touched   time.Time
}
type Server struct {
	mu       sync.Mutex
	server   *http.Server
	listener net.Listener
	address  string
	sessions map[string]*session
	requests chan struct{}
	closed   bool
}

func NewServer() *Server {
	return &Server{sessions: make(map[string]*session), requests: make(chan struct{}, 16)}
}
func (s *Server) Open(ctx context.Context, channelID int64, load Loader) (OpenResult, error) {
	if ctx == nil || channelID == 0 || load == nil {
		return OpenResult{}, errors.New("invalid gallery session")
	}
	if err := ctx.Err(); err != nil {
		return OpenResult{}, err
	}
	var tokenBytes [32]byte
	if _, err := rand.Read(tokenBytes[:]); err != nil {
		return OpenResult{}, err
	}
	token := hex.EncodeToString(tokenBytes[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return OpenResult{}, errors.New("gallery closed")
	}
	s.expireLocked(time.Now())
	if len(s.sessions) >= maxSessions {
		return OpenResult{}, file.ErrRenditionBusy
	}
	if err := s.startLocked(); err != nil {
		return OpenResult{}, err
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	s.sessions[token] = &session{channelID: channelID, ctx: sessionCtx, cancel: cancel, load: load, touched: time.Now()}
	return OpenResult{BaseURL: "http://" + s.address + route + token, Token: token, ChannelID: channelID}, nil
}
func (s *Server) CloseSession(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session := s.sessions[token]; session != nil {
		delete(s.sessions, token)
		session.cancel()
	}
}

// Revoke closes all current scopes while preserving the cheap listener. Call
// when a vault locks, an account logs out, or the active drive changes.
func (s *Server) Revoke() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for token, session := range s.sessions {
		delete(s.sessions, token)
		session.cancel()
	}
}
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	for token, session := range s.sessions {
		delete(s.sessions, token)
		session.cancel()
	}
	server := s.server
	s.mu.Unlock()
	if server != nil {
		return server.Close()
	}
	return nil
}
func (s *Server) startLocked() error {
	if s.server != nil {
		return nil
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	s.listener = listener
	s.address = listener.Addr().String()
	s.server = &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8 << 10}
	go func() { _ = s.server.Serve(listener) }()
	return nil
}
func (s *Server) expireLocked(now time.Time) {
	for token, session := range s.sessions {
		if session.ctx.Err() != nil || now.Sub(session.touched) > sessionIdleTTL {
			delete(s.sessions, token)
			session.cancel()
		}
	}
}
func (s *Server) lookup(token string) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(time.Now())
	session := s.sessions[token]
	if session != nil {
		session.touched = time.Now()
	}
	return session
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A loopback listener and strict Host check close DNS-rebinding access. A
	// 256-bit capability scopes cross-origin desktop/mobile WebViews to one drive.
	if r.Host != s.address {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	header := w.Header()
	header.Set("Access-Control-Allow-Origin", "*")
	header.Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	header.Set("Access-Control-Expose-Headers", "Content-Length, X-Rendition-Width, X-Rendition-Height, Retry-After")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Cache-Control", "no-store")
	header.Set("Referrer-Policy", "no-referrer")
	if r.Method != "GET" && r.Method != "OPTIONS" {
		http.Error(w, "method not allowed", 405)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, route), "/")
	if !strings.HasPrefix(r.URL.Path, route) || len(parts) != 3 {
		http.NotFound(w, r)
		return
	}
	msgID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || msgID <= 0 {
		http.Error(w, "invalid image", 400)
		return
	}
	kind := parts[2]
	if kind != "thumbnail" && kind != "preview" && kind != "original" {
		http.Error(w, "invalid class", 400)
		return
	}
	revision := int64(0)
	if raw := r.URL.Query().Get("revision"); raw != "" {
		revision, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || revision < 0 {
			http.Error(w, "invalid revision", 400)
			return
		}
	}
	session := s.lookup(parts[0])
	if session == nil {
		http.Error(w, "revoked", 410)
		return
	}
	if r.Method == "OPTIONS" {
		w.WriteHeader(204)
		return
	}
	select {
	case s.requests <- struct{}{}:
		defer func() { <-s.requests }()
	default:
		writeError(w, file.ErrRenditionBusy)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(session.ctx, cancel)
	defer stop()
	result, err := session.load(ctx, session.channelID, msgID, revision, kind)
	if err != nil {
		writeError(w, err)
		return
	}
	if ctx.Err() != nil || session.ctx.Err() != nil {
		http.Error(w, "revoked", 410)
		return
	}
	header.Set("Content-Type", result.MimeType)
	header.Set("Content-Length", strconv.Itoa(len(result.Bytes)))
	header.Set("X-Rendition-Width", strconv.Itoa(result.Width))
	header.Set("X-Rendition-Height", strconv.Itoa(result.Height))
	// Bound slow consumers after retrieval; an absolute server WriteTimeout
	// would incorrectly include legitimate Telegram FloodWait delays.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Second))
	_, _ = w.Write(result.Bytes)
}
func writeError(w http.ResponseWriter, err error) {
	code, status := file.RenditionError(err)
	if wait := file.RenditionRetryAfter(err); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "{\"error\":%q}", code)
}
