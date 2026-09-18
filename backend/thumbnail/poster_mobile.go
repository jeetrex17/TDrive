//go:build (android || ios) && cgo

package thumbnail

import (
	"context"
	"errors"
	"fmt"
)

// nativePosterSlot keeps abandoned decodes from multiplying.
//
// Neither platform decoder can be interrupted once it is inside the call:
// MediaMetadataRetriever and AVAssetImageGenerator both return when they are
// ready and not before. So a timed-out extraction is abandoned, not stopped,
// and the goroutine holding it keeps this slot until the framework lets go.
// The next poster then queues behind it instead of opening a second decoder on
// a phone that has just demonstrated it cannot afford the first.
var nativePosterSlot = make(chan struct{}, 1)

// posterDecode runs one uninterruptible native extraction under ctx.
//
// The work happens on its own goroutine purely so the caller can stop waiting:
// the result is handed back over a buffered channel, so an abandoned decode
// finishes into a send that never blocks rather than parking forever.
func posterDecode(ctx context.Context, decode func() ([]byte, error)) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, posterTimeout)
	defer cancel()

	type outcome struct {
		poster []byte
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		select {
		case nativePosterSlot <- struct{}{}:
			defer func() { <-nativePosterSlot }()
		case <-ctx.Done():
			// The caller has already given up, or a previous decode is still
			// wedged. Either way there is nothing left to draw for.
			done <- outcome{nil, ctx.Err()}
			return
		}
		poster, err := decode()
		done <- outcome{poster, err}
	}()

	select {
	case result := <-done:
		if result.err != nil {
			return nil, result.err
		}
		return result.poster, nil
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: decode timed out", ErrPosterUnsupported)
		}
		return nil, ctx.Err()
	}
}

// posterFromFrame bounds and re-encodes a native frame through the one image
// pipeline every other rendition goes through, so a poster is sized, oriented
// and compressed by exactly the same code.
//
// A frame the resizer will not take is still "no picture": the poster contract
// is that only an invalid request is an error the caller should notice.
func posterFromFrame(frame []byte, maxEdge int) ([]byte, error) {
	if len(frame) == 0 {
		return nil, fmt.Errorf("%w: no frame decoded", ErrPosterUnsupported)
	}
	poster, err := Generate(frame, maxEdge)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPosterUnsupported, err)
	}
	return poster, nil
}
