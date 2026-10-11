package tgclient

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
)

type transientUploadPartClient struct {
	calls      int
	firstError error
	failures   []error
	smallParts []*tg.UploadSaveFilePartRequest
	bigParts   []*tg.UploadSaveBigFilePartRequest
}

func (c *transientUploadPartClient) UploadSaveFilePart(_ context.Context, request *tg.UploadSaveFilePartRequest) (bool, error) {
	c.calls++
	c.smallParts = append(c.smallParts, request)
	if c.calls <= len(c.failures) {
		return false, c.failures[c.calls-1]
	}
	if c.calls == 1 {
		return false, c.firstError
	}
	return true, nil
}

func (c *transientUploadPartClient) UploadSaveBigFilePart(_ context.Context, request *tg.UploadSaveBigFilePartRequest) (bool, error) {
	c.calls++
	c.bigParts = append(c.bigParts, request)
	if c.calls <= len(c.failures) {
		return false, c.failures[c.calls-1]
	}
	if c.calls == 1 {
		return false, c.firstError
	}
	return true, nil
}

func TestRetryingUploadClientRetainsPartThroughRepeatedThrottling(t *testing.T) {
	t.Parallel()
	for _, big := range []bool{false, true} {
		t.Run(map[bool]string{false: "small", true: "big"}[big], func(t *testing.T) {
			t.Parallel()
			waits := []time.Duration{16 * time.Second, 15 * time.Second, time.Minute, 16 * time.Second}
			partClient := &transientUploadPartClient{}
			for _, wait := range waits {
				partClient.failures = append(partClient.failures, NewFloodWaitError(wait))
			}
			policy := DefaultUploadFloodWaitRetryPolicy()
			var slept []time.Duration
			policy.Sleep = func(_ context.Context, wait time.Duration) error {
				slept = append(slept, wait)
				return nil
			}
			client := &retryingUploadClient{policy: policy, run: func(ctx context.Context, action func(uploader.Client) error) error {
				return action(partClient)
			}}
			body := []byte("retained upload chunk")
			var accepted bool
			var err error
			if big {
				request := &tg.UploadSaveBigFilePartRequest{FileID: 97, FilePart: 12, FileTotalParts: 20, Bytes: body}
				accepted, err = client.UploadSaveBigFilePart(t.Context(), request)
				for _, attempt := range partClient.bigParts {
					if attempt != request || attempt.FileID != 97 || attempt.FilePart != 12 || attempt.FileTotalParts != 20 || !bytes.Equal(attempt.Bytes, body) {
						t.Fatalf("retried big request = %+v, want original body and identity", attempt)
					}
				}
			} else {
				request := &tg.UploadSaveFilePartRequest{FileID: 97, FilePart: 2, Bytes: body}
				accepted, err = client.UploadSaveFilePart(t.Context(), request)
				for _, attempt := range partClient.smallParts {
					if attempt != request || attempt.FileID != 97 || attempt.FilePart != 2 || !bytes.Equal(attempt.Bytes, body) {
						t.Fatalf("retried small request = %+v, want original body and identity", attempt)
					}
				}
			}
			if err != nil || !accepted || partClient.calls != 5 || !slices.Equal(slept, waits) {
				t.Fatalf("part = %v, %v after %d calls and waits %v; want acceptance after all cooldowns", accepted, err, partClient.calls, slept)
			}
		})
	}
}

func TestRetryingUploadClientReleasesSlotDuringCancelableCooldown(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	partClient := &transientUploadPartClient{firstError: NewFloodWaitError(time.Hour)}
	policy := DefaultUploadFloodWaitRetryPolicy()
	policy.Sleep = func(ctx context.Context, _ time.Duration) error {
		slotCtx, stop := context.WithTimeout(ctx, time.Second)
		defer stop()
		var releases []func()
		defer func() {
			for _, release := range releases {
				release()
			}
		}()
		for range UploadThreads {
			release, err := acquireUploadPartSlot(slotCtx)
			if err != nil {
				t.Fatalf("upload retained a slot during flood wait: %v", err)
			}
			releases = append(releases, release)
		}
		cancel()
		return ctx.Err()
	}
	client := &retryingUploadClient{policy: policy, run: func(ctx context.Context, action func(uploader.Client) error) error {
		return action(partClient)
	}}
	_, err := client.UploadSaveFilePart(ctx, &tg.UploadSaveFilePartRequest{})
	if !errors.Is(err, context.Canceled) || partClient.calls != 1 {
		t.Fatalf("upload part = %v after %d calls, want canceled after one", err, partClient.calls)
	}
}

func TestRetryingUploadClientReacquiresAndRetriesSameTelegramPart(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		call func(*retryingUploadClient) (bool, error)
	}{
		{
			name: "small part",
			call: func(client *retryingUploadClient) (bool, error) {
				return client.UploadSaveFilePart(context.Background(), &tg.UploadSaveFilePartRequest{
					FileID: 77, FilePart: 705, Bytes: []byte("same buffered chunk"),
				})
			},
		},
		{
			name: "big part",
			call: func(client *retryingUploadClient) (bool, error) {
				return client.UploadSaveBigFilePart(context.Background(), &tg.UploadSaveBigFilePartRequest{
					FileID: 77, FilePart: 705, FileTotalParts: 3800, Bytes: []byte("same buffered chunk"),
				})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			partClient := &transientUploadPartClient{firstError: errors.New("engine forcibly closed: context canceled")}
			acquires := 0
			client := &retryingUploadClient{
				policy: FloodWaitRetryPolicy{
					MaxTransientRetries: 1,
					TransientBackoff:    time.Millisecond,
					Sleep:               func(context.Context, time.Duration) error { return nil },
				},
				run: func(ctx context.Context, action func(uploader.Client) error) error {
					acquires++
					return action(partClient)
				},
			}
			ok, err := test.call(client)
			if err != nil || !ok {
				t.Fatalf("upload part = (%v, %v), want (true, nil)", ok, err)
			}
			if partClient.calls != 2 || acquires != 2 {
				t.Fatalf("part calls=%d connection acquires=%d, want 2 and 2", partClient.calls, acquires)
			}
			switch test.name {
			case "small part":
				if len(partClient.smallParts) != 2 {
					t.Fatalf("small-part request count = %d, want 2", len(partClient.smallParts))
				}
				if partClient.smallParts[0] != partClient.smallParts[1] {
					t.Fatalf("small-part requests = %p and %p, want the exact retained request", partClient.smallParts[0], partClient.smallParts[1])
				}
			case "big part":
				if len(partClient.bigParts) != 2 {
					t.Fatalf("big-part request count = %d, want 2", len(partClient.bigParts))
				}
				if partClient.bigParts[0] != partClient.bigParts[1] {
					t.Fatalf("big-part requests = %p and %p, want the exact retained request", partClient.bigParts[0], partClient.bigParts[1])
				}
			}
		})
	}
}

func TestRetryingUploadClientDoesNotRetryPermanentRPCError(t *testing.T) {
	t.Parallel()

	want := errors.New("rpc error code 400: FILE_PARTS_INVALID")
	partClient := &transientUploadPartClient{firstError: want}
	client := &retryingUploadClient{
		policy: DefaultUploadFloodWaitRetryPolicy(),
		run: func(ctx context.Context, action func(uploader.Client) error) error {
			return action(partClient)
		},
	}
	_, err := client.UploadSaveBigFilePart(context.Background(), &tg.UploadSaveBigFilePartRequest{})
	if !errors.Is(err, want) || partClient.calls != 1 {
		t.Fatalf("error=%v calls=%d, want permanent error after one call", err, partClient.calls)
	}
}

// Every upload draws parts from one budget, so a file that arrives while the
// budget is spent waits for a slot rather than piling more parts onto the link.
func TestRetryingUploadClientSharesOnePartBudget(t *testing.T) {
	ctx := context.Background()
	var held []func()
	for range UploadThreads {
		release, err := acquireUploadPartSlot(ctx)
		if err != nil {
			t.Fatalf("acquire budget: %v", err)
		}
		held = append(held, release)
	}
	defer func() {
		for _, release := range held {
			release()
		}
	}()

	sent := make(chan struct{}, 1)
	client := &retryingUploadClient{
		policy: DefaultWriteFloodWaitRetryPolicy(),
		run: func(ctx context.Context, action func(uploader.Client) error) error {
			sent <- struct{}{}
			return action(&transientUploadPartClient{calls: 1})
		},
	}
	done := make(chan error, 1)
	go func() {
		_, err := client.UploadSaveBigFilePart(ctx, &tg.UploadSaveBigFilePartRequest{FilePart: 3})
		done <- err
	}()

	select {
	case <-sent:
		t.Fatal("a part was sent while the upload budget was spent")
	case <-time.After(30 * time.Millisecond):
	}

	held[0]()
	held = held[1:]
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("part after a slot freed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("freed slot did not admit the waiting part")
	}
	if len(sent) != 1 {
		t.Fatalf("parts sent = %d, want exactly one", len(sent))
	}
}
