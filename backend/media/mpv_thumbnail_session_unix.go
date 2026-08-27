//go:build !windows

package media

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const (
	mpvThumbnailSeekTimeout   = 8 * time.Second
	mpvThumbnailWarmupTimeout = 20 * time.Second
	mpvThumbnailLoadTimeout   = 20 * time.Second
	mpvThumbnailTempPrefix    = "tdrive-media-thumb-mpv-"
)

func (g *MPVThumbnailGenerator) NewVideoThumbnailSession(sourceURL string) (VideoThumbnailSession, error) {
	if !g.Available() {
		return nil, ErrThumbnailUnavailable
	}
	return &mpvThumbnailSession{
		path:      g.path,
		sourceURL: sourceURL,
	}, nil
}

type mpvThumbnailSession struct {
	mu        sync.Mutex
	path      string
	sourceURL string
	dir       string
	socket    string
	cmd       *exec.Cmd
	conn      net.Conn
	reader    *bufio.Reader
	nextID    int64
	events    map[string]int64
	done      chan error
	warm      bool
	closed    bool
}

func (s *mpvThumbnailSession) GenerateVideoThumbnail(ctx context.Context, outPath string, seconds int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrThumbnailUnavailable
	}
	if err := s.ensureStarted(ctx); err != nil {
		return err
	}

	baseline := s.eventCount("playback-restart")
	if err := s.command(ctx, []any{"seek", seconds, "absolute+keyframes"}); err != nil {
		return err
	}
	timeout := mpvThumbnailSeekTimeout
	if !s.warm {
		timeout = mpvThumbnailWarmupTimeout
	}
	if err := s.waitEvent(ctx, "playback-restart", baseline, timeout); err != nil {
		return err
	}
	s.warm = true
	if err := s.command(ctx, []any{"screenshot-to-file", outPath, "video"}); err != nil {
		return err
	}
	return nil
}

func (s *mpvThumbnailSession) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeLocked()
}

func (s *mpvThumbnailSession) ensureStarted(ctx context.Context) error {
	if s.conn != nil {
		return nil
	}
	dir, err := os.MkdirTemp("", mpvThumbnailTempPrefix)
	if err != nil {
		return err
	}
	_ = os.Chmod(dir, videoThumbDirMode)
	socket := filepath.Join(dir, "mpv.sock")

	cmd := exec.Command(s.path,
		"--no-config",
		"--really-quiet",
		"--terminal=no",
		"--force-window=no",
		"--idle=yes",
		"--pause=yes",
		"--ytdl=no",
		"--vo=null",
		"--ao=null",
		"--sid=no",
		"--input-ipc-server="+socket,
		"--demuxer-readahead-secs=0",
		"--demuxer-max-bytes=2097152",
		"--demuxer-max-back-bytes=1048576",
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(dir)
		return err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	conn, err := dialMPVSocket(ctx, socket, done)
	if err != nil {
		terminateMPVProcess(cmd, done)
		_ = os.RemoveAll(dir)
		return err
	}

	s.dir = dir
	s.socket = socket
	s.cmd = cmd
	s.conn = conn
	s.reader = bufio.NewReader(conn)
	s.done = done
	s.events = make(map[string]int64)
	s.warm = false
	if err := s.command(ctx, []any{"loadfile", s.sourceURL, "replace"}); err != nil {
		s.closeProcessLocked()
		return err
	}
	if err := s.waitEvent(ctx, "file-loaded", 0, mpvThumbnailLoadTimeout); err != nil {
		s.closeProcessLocked()
		return err
	}
	return nil
}

func dialMPVSocket(ctx context.Context, socket string, done chan error) (net.Conn, error) {
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			if err == nil {
				err = ErrThumbnailUnavailable
			}
			select {
			case done <- err:
			default:
			}
			return nil, fmt.Errorf("mpv exited before IPC became ready: %w", err)
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		conn, err := net.DialTimeout("unix", socket, 100*time.Millisecond)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		time.Sleep(40 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = ErrThumbnailUnavailable
	}
	return nil, fmt.Errorf("mpv IPC socket was not ready: %w", lastErr)
}

func (s *mpvThumbnailSession) command(ctx context.Context, command []any) error {
	s.nextID++
	id := s.nextID
	payload := map[string]any{
		"command":    command,
		"request_id": id,
	}
	if err := json.NewEncoder(s.conn).Encode(payload); err != nil {
		return classifyMPVSessionError(err)
	}
	for {
		msg, err := s.readMessage(ctx)
		if err != nil {
			return err
		}
		if msg.RequestID != id {
			continue
		}
		if msg.Error != "" && msg.Error != "success" {
			return fmt.Errorf("mpv command %v failed: %s", redactMPVThumbnailCommand(command), redactMPVThumbnailError(msg.Error, command))
		}
		return nil
	}
}

func (s *mpvThumbnailSession) waitEvent(ctx context.Context, event string, baseline int64, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for s.eventCount(event) <= baseline {
		if _, err := s.readMessage(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (s *mpvThumbnailSession) readMessage(ctx context.Context) (mpvIPCMessage, error) {
	for {
		if err := ctx.Err(); err != nil {
			return mpvIPCMessage{}, err
		}
		if deadlineConn, ok := s.conn.(interface{ SetReadDeadline(time.Time) error }); ok {
			_ = deadlineConn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
		}
		line, err := s.reader.ReadBytes('\n')
		if err != nil {
			if isTimeout(err) {
				if err, ok := processDone(s.done); ok {
					return mpvIPCMessage{}, fmt.Errorf("%w: mpv exited: %v", errThumbnailSessionDead, err)
				}
				continue
			}
			return mpvIPCMessage{}, classifyMPVSessionError(err)
		}
		var msg mpvIPCMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if msg.Event != "" {
			s.events[msg.Event]++
		}
		return msg, nil
	}
}

func (s *mpvThumbnailSession) eventCount(event string) int64 {
	if s.events == nil {
		return 0
	}
	return s.events[event]
}

func (s *mpvThumbnailSession) closeLocked() {
	if s.closed {
		return
	}
	s.closed = true
	s.closeProcessLocked()
}

func (s *mpvThumbnailSession) closeProcessLocked() {
	if s.conn != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		_ = s.command(ctx, []any{"quit"})
		cancel()
		_ = s.conn.Close()
		s.conn = nil
		s.reader = nil
	}
	if s.cmd != nil {
		terminateMPVProcess(s.cmd, s.done)
		s.cmd = nil
		s.done = nil
	}
	if s.dir != "" {
		_ = os.RemoveAll(s.dir)
		s.dir = ""
	}
	s.socket = ""
	s.events = nil
	s.warm = false
}

func terminateMPVProcess(cmd *exec.Cmd, done <-chan error) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	select {
	case <-done:
		return
	default:
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(600 * time.Millisecond):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
	}
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func classifyMPVSessionError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, errThumbnailSessionDead) {
		return err
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrClosed) ||
		errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
		return fmt.Errorf("%w: %v", errThumbnailSessionDead, err)
	}
	return err
}

func processDone(done chan error) (error, bool) {
	if done == nil {
		return nil, false
	}
	select {
	case err := <-done:
		if err == nil {
			err = ErrThumbnailUnavailable
		}
		select {
		case done <- err:
		default:
		}
		return err, true
	default:
		return nil, false
	}
}

type mpvIPCMessage struct {
	Event     string `json:"event"`
	RequestID int64  `json:"request_id"`
	Error     string `json:"error"`
}
