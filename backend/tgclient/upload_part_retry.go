package tgclient

import (
	"context"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
)

// uploadPartRunner reacquires the active Telegram API for each attempt. This
// is the key difference from retrying a whole document upload: gotd retains the
// exact 512 KiB buffer, file_id, and part index while a dead liveConn restarts.
type uploadPartRunner func(context.Context, func(uploader.Client) error) error

type retryingUploadClient struct {
	policy FloodWaitRetryPolicy
	run    uploadPartRunner
}

func (c *retryingUploadClient) UploadSaveFilePart(ctx context.Context, request *tg.UploadSaveFilePartRequest) (bool, error) {
	return c.savePart(ctx, func(client uploader.Client) (bool, error) {
		return client.UploadSaveFilePart(ctx, request)
	})
}

func (c *retryingUploadClient) UploadSaveBigFilePart(ctx context.Context, request *tg.UploadSaveBigFilePartRequest) (bool, error) {
	return c.savePart(ctx, func(client uploader.Client) (bool, error) {
		return client.UploadSaveBigFilePart(ctx, request)
	})
}

func (c *retryingUploadClient) savePart(ctx context.Context, action func(uploader.Client) (bool, error)) (bool, error) {
	var accepted bool
	err := c.policy.Do(ctx, func() error {
		return c.run(ctx, func(client uploader.Client) error {
			var err error
			accepted, err = action(client)
			return err
		})
	})
	return accepted, err
}
