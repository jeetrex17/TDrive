package tgclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/gotd/td/pool"
	"github.com/gotd/td/rpc"
)

func TestIsTransientTransport(t *testing.T) {
	floodWaitErr := NewFloodWaitError(5 * time.Second)
	callerCtx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{
			"gotd engine forcibly closed wraps context canceled",
			fmt.Errorf("tgclient: upload: upload part: send upload part 705 RPC: %w",
				fmt.Errorf("rpcDoRequest: retryUntilAck: %w",
					fmt.Errorf("engine forcibly closed: %w", context.Canceled))),
			true,
		},
		{"explicit liveConn close is not retried", errScopeClosed, false},
		{"typed gotd engine closed", fmt.Errorf("rpc: %w", rpc.ErrEngineClosed), true},
		{"typed gotd pooled connection dead", fmt.Errorf("pool: %w", pool.ErrConnDead), true},
		{"typed connection reset", fmt.Errorf("read tcp: %w", syscall.ECONNRESET), true},
		{"typed broken pipe", fmt.Errorf("write tcp: %w", syscall.EPIPE), true},
		{"typed connection aborted", fmt.Errorf("read tcp: %w", syscall.ECONNABORTED), true},
		{"typed unexpected eof", fmt.Errorf("save part: %w", io.ErrUnexpectedEOF), true},
		{"typed net closed", fmt.Errorf("read: %w", net.ErrClosed), true},
		{"connection reset by peer", errors.New("read tcp 1.2.3.4:5->6.7.8.9:443: connection reset by peer"), true},
		{"connection refused", errors.New("dial tcp: connect: connection refused"), true},
		{"broken pipe", errors.New("write tcp 1.2.3.4:5->6.7.8.9:443: broken pipe"), true},
		{"closed network connection", errors.New("use of closed network connection"), true},
		{"unexpected eof", fmt.Errorf("upload.SaveFile: save part: unexpected EOF"), true},
		{"io timeout", errors.New("read tcp 1.2.3.4:5->6.7.8.9:443: i/o timeout"), true},
		{"flood wait is not transient", fmt.Errorf("send media: %w", floodWaitErr), false},
		{"plain caller cancellation is not transient", callerCtx.Err(), false},
		{"plain deadline is not transient", context.DeadlineExceeded, false},
		{"definitive rpc error", errors.New("rpc error code 400: FILE_PARTS_INVALID"), false},
		{"generic send failure", ErrInjectedSend, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTransientTransport(tt.err); got != tt.want {
				t.Fatalf("IsTransientTransport(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
