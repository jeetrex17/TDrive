package tgclient

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"
)

// sendUploadedMedia retries server-directed throttling with the uploaded file
// handle and random_id intact. A transport failure still returns to the owning
// service's bounded retry/reconciliation path because acceptance is uncertain.
func (g *Gotd) sendUploadedMedia(
	ctx context.Context,
	request *tg.MessagesSendMediaRequest,
	send func(context.Context, *tg.MessagesSendMediaRequest) (tg.UpdatesClass, error),
) (SendFileResult, error) {
	var result SendFileResult
	policy := FloodWaitRetryPolicy{WaitUntilCanceled: true}
	err := policy.Do(ctx, func() error {
		return g.writes.Do(ctx, writeClassMessage, func() error {
			updates, err := send(ctx, request)
			if err != nil {
				err = normalizeError(err)
				if _, floodWait := FloodWaitDuration(err); floodWait {
					return err
				}
				return fmt.Errorf("%w: send media: %w", ErrSendOutcomeUnknown, err)
			}
			result.MsgID, err = requiredSendMsgID(updates, request.RandomID, "send file")
			return err
		})
	})
	return result, err
}
