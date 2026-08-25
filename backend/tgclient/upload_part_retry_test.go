package tgclient

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
)

type transientUploadPartClient struct {
	calls      int
	firstError error
}

func (c *transientUploadPartClient) UploadSaveFilePart(context.Context, *tg.UploadSaveFilePartRequest) (bool, error) {
	c.calls++
	if c.calls == 1 {
		return false, c.firstError
	}
	return true, nil
}

func (c *transientUploadPartClient) UploadSaveBigFilePart(context.Context, *tg.UploadSaveBigFilePartRequest) (bool, error) {
	c.calls++
	if c.calls == 1 {
		return false, c.firstError
	}
	return true, nil
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
		})
	}
}

func TestRetryingUploadClientDoesNotRetryPermanentRPCError(t *testing.T) {
	t.Parallel()

	want := errors.New("rpc error code 400: FILE_PARTS_INVALID")
	partClient := &transientUploadPartClient{firstError: want}
	client := &retryingUploadClient{
		policy: DefaultWriteFloodWaitRetryPolicy(),
		run: func(ctx context.Context, action func(uploader.Client) error) error {
			return action(partClient)
		},
	}
	_, err := client.UploadSaveBigFilePart(context.Background(), &tg.UploadSaveBigFilePartRequest{})
	if !errors.Is(err, want) || partClient.calls != 1 {
		t.Fatalf("error=%v calls=%d, want permanent error after one call", err, partClient.calls)
	}
}
