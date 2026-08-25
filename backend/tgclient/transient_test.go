package tgclient

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
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
		{"liveConn scope closed", errScopeClosed, true},
		{"connection reset by peer", errors.New("read tcp 1.2.3.4:5->6.7.8.9:443: connection reset by peer"), true},
		{"connection refused", errors.New("dial tcp: connect: connection refused"), true},
		{"broken pipe", errors.New("write tcp 1.2.3.4:5->6.7.8.9:443: broken pipe"), true},
		{"closed network connection", errors.New("use of closed network connection"), true},
		{"unexpected eof", fmt.Errorf("upload.SaveFile: save part: unexpected EOF"), true},
		{"io timeout", errors.New("read tcp 1.2.3.4:5->6.7.8.9:443: i/o timeout"), true},
		{"flood wait is not transient", fmt.Errorf("send media: %w", floodWaitErr), false},
		{"plain caller cancellation is not transient", callerCtx.Err(), false},
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
