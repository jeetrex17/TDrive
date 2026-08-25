package tgclient

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"syscall"
)

// transientTransportPatterns are lowercase substrings of transport-level
// failures that a reconnect can fix. The most important entry is gotd's
// "engine forcibly closed": when the shared connection scope dies mid-RPC,
// every pending call fails wrapped like
//
//	rpcDoRequest: retryUntilAck: engine forcibly closed: context canceled
//
// even though the *caller's* context is still alive (gotd cancels its own
// internal contexts on engine close). Retry decisions must therefore never
// test errors.Is(err, context.Canceled); they should check the caller's
// ctx.Err() first and then use this classifier.
var transientTransportPatterns = []string{
	"engine forcibly closed",
	"engine was closed",
	"use of closed network connection",
	"connection reset",
	"connection refused",
	"connection dead",
	"broken pipe",
	"unexpected eof",
	"i/o timeout",
	"tls handshake timeout",
	"no such host",
	"network is unreachable",
}

// IsTransientTransport reports whether err looks like a transport failure that
// a fresh connection could resolve: the shared connection scope died, the TCP
// link was reset, or DNS/routing hiccuped. It deliberately ignores context
// cancellation identity (see transientTransportPatterns) and never classifies
// Telegram API rejections (FLOOD_WAIT is handled separately by the retry
// policy; definitive RPC errors must surface to the caller unchanged).
func IsTransientTransport(err error) bool {
	if err == nil {
		return false
	}
	if _, floodWait := FloodWaitDuration(err); floodWait {
		return false
	}
	msg := strings.ToLower(err.Error())
	if errors.Is(err, errScopeClosed) {
		return false
	}
	if (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) &&
		!strings.Contains(msg, "engine forcibly closed") {
		return false
	}
	if isTypedTransientTransport(err) {
		return true
	}
	for _, pattern := range transientTransportPatterns {
		if strings.Contains(msg, pattern) {
			return true
		}
	}
	return false
}

func isTypedTransientTransport(err error) bool {
	if errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ETIMEDOUT) ||
		errors.Is(err, syscall.ENETRESET) ||
		errors.Is(err, syscall.ENETDOWN) ||
		errors.Is(err, syscall.ENETUNREACH) ||
		errors.Is(err, syscall.EHOSTUNREACH) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary())
}
