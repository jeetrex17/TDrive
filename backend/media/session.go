package media

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/tgclient"
	"TDrive/backend/thumbnail"
)

type resolvedSegment struct {
	start int64
	size  int64
	ref   tgclient.DocumentRef
}

type Session struct {
	token          string
	url            string
	thumbURL       string
	sourceURL      string
	hlsURL         string
	file           LogicalFile
	segments       []resolvedSegment
	reader         *RangeReader
	thumbReader    *RangeReader
	decryptor      *tdcrypto.RandomAccessDecryptor
	thumbDecryptor *tdcrypto.RandomAccessDecryptor
	thumbs         *videoThumbnailer
	hls            hlsStream

	// ctx bounds work the session starts on its own behalf, such as warming
	// the container index; Close cancels it.
	ctx    context.Context
	cancel context.CancelFunc

	mu        sync.Mutex
	lastTouch time.Time
	closed    bool
	served    atomic.Int64 // plaintext bytes handed to players
}

type MediaStats struct {
	Playback   ThroughputStats `json:"playback"`
	Thumbnails ThroughputStats `json:"thumbnails"`
}

// playbackReadAhead is how many 1 MiB blocks stay in flight beyond the block
// the player is reading. Throughput is blocks in flight divided by Telegram's
// per-request latency, so this and the connection pool are what set the
// ceiling: eight blocks cover a second of a high-bitrate remux on a slow link
// while staying well inside the block cache.
const playbackReadAhead = 8

type SessionOptions struct {
	Context               context.Context
	EnableVideoThumbnails bool
	MasterKey             []byte
}

// warmContainerIndex speculatively pulls the last block of the file.
//
// Players read the container index before the first frame, and in MP4 and MKV
// that index lives at the end. Left alone the player asks for it only after the
// head has arrived, so the two reads run back to back and the viewer waits for
// both. Starting the tail here overlaps it with the head instead. It is a
// background read, so live playback always gets the getFile slots first.
func (s *Session) warmContainerIndex() {
	if s == nil || s.reader == nil || len(s.segments) == 0 {
		return
	}
	// A file that fits in one block has nothing to overlap: the head read the
	// player makes first already covers the index.
	if s.file.StoredSize <= rangeUploadBoundary {
		return
	}
	last := s.segments[len(s.segments)-1]
	if last.ref.Size <= 0 {
		return
	}
	s.reader.prefetchBlock(last.ref, blockStartFor(last.ref.Size-1))
}

func newSession(file LogicalFile, segments []resolvedSegment, ranges tgclient.RangeClient, cache *thumbnail.Cache, generator VideoThumbnailGenerator, opts SessionOptions) (*Session, error) {
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	copied := append([]resolvedSegment(nil), segments...)
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{
		token:     token,
		file:      file,
		segments:  copied,
		ctx:       ctx,
		cancel:    cancel,
		lastTouch: time.Now(),
	}
	// Playback and the thumbnail extractor share one block cache: the
	// extractor reads the same head and index blocks playback already holds,
	// and the blocks it pulls for a preview are where the viewer may seek next.
	blocks := newBlockCache(defaultRangeCacheBytes)
	s.reader = NewRangeReader(RangeReaderConfig{
		Client:    ranges,
		Cache:     blocks,
		ReadAhead: playbackReadAhead,
	})
	s.warmContainerIndex()
	if opts.EnableVideoThumbnails {
		thumbnailCache := cache
		if file.Encrypted {
			// Generated frames are plaintext. Keep encrypted-video thumbnails in
			// the session temp directory only; Close removes that directory and
			// the startup sweep removes leftovers after an unclean exit.
			thumbnailCache = nil
		}
		s.thumbReader = NewRangeReader(RangeReaderConfig{
			Client:     ranges,
			Cache:      blocks,
			Background: true,
			OnFloodWait: func(wait time.Duration) {
				if s.thumbs != nil {
					s.thumbs.NoteFloodWait(wait)
				}
			},
		})
		s.thumbs = newVideoThumbnailer(s, thumbnailCache, generator)
	}
	if file.Encrypted {
		openCtx := opts.Context
		if openCtx == nil {
			openCtx = context.Background()
		}
		decryptor, err := tdcrypto.NewRandomAccessDecryptor(
			openCtx,
			storedContentReader{session: s, reader: s.reader},
			file.StoredSize,
			opts.MasterKey,
		)
		if err != nil {
			s.Close()
			return nil, err
		}
		if decryptor.Size() != file.PlaintextSize {
			_ = decryptor.Close()
			s.Close()
			return nil, fmt.Errorf("media: encrypted plaintext size %d does not match projection %d: %w", decryptor.Size(), file.PlaintextSize, tdcrypto.ErrCiphertextSize)
		}
		s.decryptor = decryptor
		if s.thumbReader != nil {
			thumbDecryptor, err := decryptor.Clone(
				storedContentReader{session: s, reader: s.thumbReader},
			)
			if err != nil {
				s.Close()
				return nil, err
			}
			if thumbDecryptor.Size() != file.PlaintextSize {
				_ = thumbDecryptor.Close()
				s.Close()
				return nil, fmt.Errorf("media: encrypted thumbnail plaintext size %d does not match projection %d: %w", thumbDecryptor.Size(), file.PlaintextSize, tdcrypto.ErrCiphertextSize)
			}
			s.thumbDecryptor = thumbDecryptor
		}
	}
	go s.warmIndex()
	return s, nil
}

func (s *Session) Token() string {
	if s == nil {
		return ""
	}
	return s.token
}

func (s *Session) URL() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.url
}

func (s *Session) ThumbnailURL() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.thumbs == nil {
		return ""
	}
	return s.thumbURL
}

func (s *Session) setURL(url string) {
	s.mu.Lock()
	s.url = url
	s.mu.Unlock()
}

func (s *Session) setThumbnailURLs(sourceURL, thumbURL string) {
	s.mu.Lock()
	s.sourceURL = sourceURL
	s.thumbURL = thumbURL
	s.mu.Unlock()
}

func (s *Session) thumbnailSourceURL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sourceURL
}

func (s *Session) Size() int64 {
	if s == nil {
		return 0
	}
	return s.file.PlaintextSize
}

func (s *Session) Name() string {
	if s == nil {
		return ""
	}
	return s.file.Name
}

func (s *Session) openSnapshot() (token, url, thumbnailURL, hlsURL string, file LogicalFile, ok bool) {
	if s == nil {
		return "", "", "", "", LogicalFile{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", "", "", "", LogicalFile{}, false
	}
	s.lastTouch = time.Now()
	file = s.file
	file.Segments = append([]Segment(nil), s.file.Segments...)
	return s.token, s.url, s.thumbURL, s.hlsURL, file, true
}

func (s *Session) Encrypted() bool {
	return s != nil && s.file.Encrypted
}

func (s *Session) touch() {
	s.mu.Lock()
	s.lastTouch = time.Now()
	s.mu.Unlock()
}

func (s *Session) LastTouch() time.Time {
	if s == nil {
		return time.Time{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastTouch
}

func (s *Session) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	s.logSummary()
	if s.decryptor != nil {
		_ = s.decryptor.Close()
	}
	if s.thumbDecryptor != nil {
		_ = s.thumbDecryptor.Close()
	}
	if s.reader != nil {
		s.reader.Close()
	}
	if s.thumbReader != nil {
		s.thumbReader.Close()
	}
	if s.thumbs != nil {
		s.thumbs.Close()
	}
}

func (s *Session) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	return s.readPlainAt(ctx, s.reader, s.decryptor, p, off)
}

func (s *Session) ReadThumbAt(ctx context.Context, p []byte, off int64) (int, error) {
	if s == nil || s.thumbReader == nil {
		return 0, ErrThumbnailUnavailable
	}
	return s.readPlainAt(ctx, s.thumbReader, s.thumbDecryptor, p, off)
}

func (s *Session) Thumbnail(ctx context.Context, seconds float64) ([]byte, error) {
	if s == nil || s.thumbs == nil {
		return nil, ErrThumbnailUnavailable
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, ErrSessionNotFound
	}
	s.touch()
	return s.thumbs.Get(ctx, seconds)
}

func (s *Session) UpdatePlayback(currentTime, duration, bufferAhead float64) {
	if s == nil || s.thumbs == nil {
		return
	}
	s.thumbs.UpdatePlayback(currentTime, duration, bufferAhead)
}

func (s *Session) Stats() MediaStats {
	if s == nil {
		return MediaStats{}
	}
	var thumbStats ThroughputStats
	if s.thumbReader != nil {
		thumbStats = s.thumbReader.Throughput()
	}
	return MediaStats{
		Playback:   s.reader.Throughput(),
		Thumbnails: thumbStats,
	}
}

func (s *Session) logStats(stats MediaStats) {
	if s == nil || s.thumbs == nil {
		return
	}
	s.thumbs.LogStats(stats)
}

func (s *Session) readPlainAt(ctx context.Context, reader *RangeReader, decryptor *tdcrypto.RandomAccessDecryptor, p []byte, off int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if decryptor != nil {
		if err := s.validateRead(reader, off); err != nil {
			return 0, err
		}
		n, err := decryptor.ReadAt(ctx, p, off)
		if errors.Is(err, tdcrypto.ErrDecryptorClosed) {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return n, ErrSessionNotFound
			}
		}
		if n > 0 || err == nil {
			s.touch()
		}
		if reader == s.reader {
			s.served.Add(int64(n))
		}
		return n, err
	}
	n, err := s.readStoredAt(ctx, reader, p, off, s.Size())
	if reader == s.reader {
		s.served.Add(int64(n))
	}
	return n, err
}

// logSummary records what playback cost, so a slow stream can be attributed
// from the log: bytes served against blocks fetched says how well the cache
// and read-ahead did, cancellations count seeks that outran the window, and
// the block percentiles are Telegram's own latency on this link.
func (s *Session) logSummary() {
	if s.reader == nil {
		return
	}
	stats := s.reader.Stats()
	slog.Info("media: session closed",
		"name", s.file.Name,
		"served_mib", s.served.Load()>>20,
		"fetched_blocks", stats.Fetched,
		"cancelled_blocks", stats.Cancelled,
		"cache_hits", stats.CacheHits,
		"peak_in_flight", stats.PeakInFlight,
		"block_p50", stats.BlockP50.Round(time.Millisecond),
		"block_p95", stats.BlockP95.Round(time.Millisecond))
}

func (s *Session) validateRead(reader *RangeReader, off int64) error {
	if off < 0 {
		return fmt.Errorf("media: negative session offset")
	}
	if reader == nil {
		return ErrRangeClientNotReady
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return ErrSessionNotFound
	}
	if off >= s.Size() {
		return io.EOF
	}
	return nil
}

func (s *Session) readStoredAt(ctx context.Context, reader *RangeReader, p []byte, off, size int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if off < 0 {
		return 0, fmt.Errorf("media: negative session offset")
	}
	if reader == nil {
		return 0, ErrRangeClientNotReady
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return 0, ErrSessionNotFound
	}
	if off >= size {
		return 0, io.EOF
	}

	want := len(p)
	remaining := size - off
	if int64(want) > remaining {
		want = int(remaining)
	}

	done := 0
	for done < want {
		absolute := off + int64(done)
		seg, ok := s.segmentFor(absolute)
		if !ok {
			if done > 0 {
				return done, io.EOF
			}
			return 0, io.EOF
		}
		segOff := absolute - seg.start
		available := seg.size - segOff
		need := want - done
		if int64(need) > available {
			need = int(available)
		}
		n, err := reader.ReadStoredAt(ctx, seg.ref, p[done:done+need], segOff)
		done += n
		if err != nil {
			if done > 0 {
				return done, err
			}
			return 0, err
		}
	}
	s.touch()
	if done < len(p) {
		return done, io.EOF
	}
	return done, nil
}

type storedContentReader struct {
	session *Session
	reader  *RangeReader
}

func (r storedContentReader) ReadAt(ctx context.Context, dst []byte, offset int64) (int, error) {
	if r.session == nil {
		return 0, ErrSessionNotFound
	}
	return r.session.readStoredAt(ctx, r.reader, dst, offset, r.session.file.StoredSize)
}

func (s *Session) segmentFor(off int64) (resolvedSegment, bool) {
	i := sort.Search(len(s.segments), func(i int) bool {
		return s.segments[i].start+s.segments[i].size > off
	})
	if i < len(s.segments) {
		seg := s.segments[i]
		if off >= seg.start {
			return seg, true
		}
	}
	return resolvedSegment{}, false
}

func randomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("media: token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
