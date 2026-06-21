package media

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"sync"
	"time"

	"TDrive/backend/tgclient"
)

type resolvedSegment struct {
	start int64
	size  int64
	ref   tgclient.DocumentRef
}

type Session struct {
	token    string
	url      string
	file     LogicalFile
	segments []resolvedSegment
	reader   *RangeReader

	mu        sync.Mutex
	lastTouch time.Time
	closed    bool
}

func newSession(file LogicalFile, segments []resolvedSegment, ranges tgclient.RangeClient) (*Session, error) {
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	copied := append([]resolvedSegment(nil), segments...)
	return &Session{
		token:     token,
		file:      file,
		segments:  copied,
		reader:    NewRangeReader(RangeReaderConfig{Client: ranges}),
		lastTouch: time.Now(),
	}, nil
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

func (s *Session) setURL(url string) {
	s.mu.Lock()
	s.url = url
	s.mu.Unlock()
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

func (s *Session) touch() {
	s.mu.Lock()
	s.lastTouch = time.Now()
	s.mu.Unlock()
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
	if s.reader != nil {
		s.reader.Close()
	}
}

func (s *Session) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if off < 0 {
		return 0, fmt.Errorf("media: negative session offset")
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return 0, ErrSessionNotFound
	}
	if off >= s.Size() {
		return 0, io.EOF
	}

	want := len(p)
	remaining := s.Size() - off
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
		n, err := s.reader.ReadStoredAt(ctx, seg.ref, p[done:done+need], segOff)
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

func (s *Session) segmentFor(off int64) (resolvedSegment, bool) {
	for _, seg := range s.segments {
		if off >= seg.start && off < seg.start+seg.size {
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
