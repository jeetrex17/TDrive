package media

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"

	"TDrive/backend/media/remux"
)

// HLS serving exists for one reason: Apple ships no Matroska demuxer, so an
// .mkv cannot be opened on an iPhone whatever is inside it. The streams within
// are usually formats iOS decodes perfectly well, so the container is repacked
// into fragmented MP4 on the way out and handed over as HLS. Nothing is
// re-encoded and nothing is written to disk.
//
// The routes sit under one session token, which is the same access boundary the
// byte routes use. Video and each audio track are separate renditions, so the
// viewer can switch language without the stream being rebuilt:
//
//	/media/hls/<token>/index.m3u8       the master, naming what is on offer
//	/media/hls/<token>/v/index.m3u8     the video rendition
//	/media/hls/<token>/a0/index.m3u8    the first audio track
//	/media/hls/<token>/<r>/init.mp4     that rendition's decoder configuration
//	/media/hls/<token>/<r>/<n>.m4s      one segment, muxed on request
const (
	mediaHLSRoutePrefix = "/media/hls/"
	hlsPlaylistName     = "index.m3u8"
)

// hlsStream is a session's remux, prepared once and shared by every request
// after it.
//
// Preparing it reads the file's header and cue index, which is a network round
// trip, and a player opens the playlist and the initialisation segment almost
// simultaneously. Holding the lock across the open makes the second request
// wait for the first rather than probing the same file twice.
type hlsStream struct {
	mu     sync.Mutex
	stream *remux.Stream
}

// get returns the prepared stream, opening it on first use.
//
// A failure is deliberately not remembered. Most failures here are a read that
// did not complete, and those deserve another try; the ones that are permanent
// come back just as fast the second time because the answer comes from bytes
// the block cache is still holding.
func (h *hlsStream) get(ctx context.Context, src remux.Source) (*remux.Stream, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stream != nil {
		return h.stream, nil
	}
	stream, err := remux.Open(ctx, src)
	if err != nil {
		return nil, err
	}
	h.stream = stream
	return stream, nil
}

// HLSURL is where this session's playlist lives, or empty for a file that does
// not need repackaging.
func (s *Session) HLSURL() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hlsURL
}

func (s *Session) setHLSURL(url string) {
	s.mu.Lock()
	s.hlsURL = url
	s.mu.Unlock()
}

// needsRemux reports whether a file's container has to be repacked to be
// playable on Apple platforms.
//
// This is a container question, not a codec one. The codecs inside are judged
// later, by the remuxer, which can say precisely what it cannot carry.
func needsRemux(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".mkv", ".mk3d":
		return true
	default:
		return false
	}
}

func (s *Server) handleHLS(w http.ResponseWriter, r *http.Request) {
	setMediaCORS(w.Header())
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token, rest, ok := strings.Cut(strings.TrimPrefix(path.Clean(r.URL.Path), mediaHLSRoutePrefix), "/")
	if !ok || token == "" {
		http.NotFound(w, r)
		return
	}
	session := s.session(token)
	if session == nil {
		http.NotFound(w, r)
		return
	}
	session.touch()

	stream, err := session.hls.get(r.Context(), session)
	if err != nil {
		writeRemuxError(w, r, session, err)
		return
	}

	// The master sits at the top; everything else lives under the rendition it
	// belongs to.
	if rest == hlsPlaylistName {
		writeHLS(w, r, session, remux.PlaylistContentType, []byte(stream.Master()))
		return
	}
	rendition, name, ok := strings.Cut(rest, "/")
	if !ok || strings.Contains(name, "/") || !stream.HasRendition(rendition) {
		http.NotFound(w, r)
		return
	}

	switch {
	case name == hlsPlaylistName:
		// A playlist changes only if the file does, so it is derived once and
		// costs nothing to serve again.
		media, _ := stream.Media(rendition)
		writeHLS(w, r, session, remux.PlaylistContentType, []byte(media))
	case name == remux.InitName:
		init, _ := stream.Init(rendition)
		writeHLS(w, r, session, remux.InitContentType, init)
	default:
		index, valid := remux.ParseSegmentName(name)
		if !valid || index >= stream.SegmentCount() {
			http.NotFound(w, r)
			return
		}
		segment, err := stream.Segment(r.Context(), rendition, index)
		if err != nil {
			writeRemuxError(w, r, session, err)
			return
		}
		writeHLS(w, r, session, remux.SegmentContentType, segment)
	}
}

// writeHLS sends one whole resource.
//
// Range requests are deliberately not offered. Every one of these is small and
// already addressed by name, so byte addressing within them would buy nothing,
// and not advertising Accept-Ranges is what stops a player asking.
func writeHLS(w http.ResponseWriter, r *http.Request, session *Session, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if session.Encrypted() {
		setMediaNoStore(w.Header())
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// writeRemuxError separates what the viewer can do something about from what
// they cannot.
//
// A file this device will never play is a different answer from a read that
// failed, and the player shows them differently: one is a dead end worth
// explaining, the other is worth retrying.
func writeRemuxError(w http.ResponseWriter, r *http.Request, session *Session, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(r.Context().Err(), context.Canceled) {
		return
	}
	switch {
	case errors.Is(err, remux.ErrNoPlayableTrack):
		http.Error(w, "no audio track this device can decode", http.StatusUnsupportedMediaType)
	case errors.Is(err, remux.ErrUnsupported):
		http.Error(w, "this file cannot be repackaged for playback", http.StatusUnsupportedMediaType)
	default:
		slog.Warn("media: hls request failed", "name", session.Name(), "path", r.URL.Path, "error", err)
		http.Error(w, "remux failed", http.StatusInternalServerError)
	}
}
