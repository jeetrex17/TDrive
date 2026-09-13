package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	daemonMaxFrameBytes = 1 << 20 // 1 MiB is far above the CLI protocol needs.
	daemonReadTimeout   = 5 * time.Minute
	daemonWriteTimeout  = 30 * time.Second
)

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	slog.Debug("daemon: client connection accepted")
	defer func() {
		slog.Debug("daemon: client connection closed")
		_ = conn.Close()
	}()

	reader := bufio.NewReader(conn)
	enc := json.NewEncoder(conn)
	var encMu sync.Mutex
	writeFrame := func(frame Frame) error {
		encMu.Lock()
		defer encMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(daemonWriteTimeout))
		defer func() { _ = conn.SetWriteDeadline(time.Time{}) }()
		return enc.Encode(frame)
	}

	for {
		var req Request
		if err := readRequestFrame(conn, reader, &req); err != nil {
			return
		}
		if isStreamingCommand(req.Command) {
			reqCtx, cancelReq := context.WithCancel(ctx)
			// Streaming requests do not read from the socket again while work is
			// in flight. Watch for EOF so an abandoned prompt cancels the backend
			// operation and releases streamMu.
			_ = conn.SetReadDeadline(time.Time{})
			go cancelOnConnectionClose(reader, cancelReq)
			if err := s.handleStreamingRequest(reqCtx, req, writeFrame); err != nil {
				cancelReq()
				return
			}
			cancelReq()
			return
		}
		frame := s.handleRequest(ctx, req)
		if err := writeFrame(frame); err != nil {
			return
		}
	}
}

func cancelOnConnectionClose(reader *bufio.Reader, cancel context.CancelFunc) {
	_, _ = reader.Peek(1)
	cancel()
}

func readRequestFrame(conn net.Conn, reader *bufio.Reader, req *Request) error {
	_ = conn.SetReadDeadline(time.Now().Add(daemonReadTimeout))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()

	line, err := readLimitedLine(reader, daemonMaxFrameBytes)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(line))) == 0 {
		return io.EOF
	}
	if err := json.Unmarshal(line, req); err != nil {
		return err
	}
	if len(req.Payload) > daemonMaxFrameBytes {
		return fmt.Errorf("daemon: payload too large")
	}
	return nil
}

func readLimitedLine(reader *bufio.Reader, limit int) ([]byte, error) {
	var out []byte
	for {
		part, isPrefix, err := reader.ReadLine()
		if err != nil {
			return nil, err
		}
		if len(out)+len(part) > limit {
			return nil, fmt.Errorf("daemon: request too large")
		}
		out = append(out, part...)
		if !isPrefix {
			return out, nil
		}
	}
}
