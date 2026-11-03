package tgclient

import (
	"context"
	"errors"
	"fmt"

	"github.com/gotd/td/tg"
)

// sendUploadedMedia retains the uploaded handle and random_id through throttling
// and network outages. If recovery stops, preserve uncertain acceptance so the
// owning service cannot mistake it for a safe-to-abort publication.
func (g *Gotd) sendUploadedMedia(
	ctx context.Context,
	request *tg.MessagesSendMediaRequest,
	send func(context.Context, *tg.MessagesSendMediaRequest) (tg.UpdatesClass, error),
) (SendFileResult, error) {
	var result SendFileResult
	var outcomeUnknown error
	policy := DefaultUploadNetworkRetryPolicy()
	err := policy.Do(ctx, func() error {
		return g.writes.Do(ctx, writeClassMessage, func() error {
			updates, err := send(ctx, request)
			if err != nil {
				err = normalizeError(err)
				if _, floodWait := FloodWaitDuration(err); floodWait {
					return err
				}
				unknown := fmt.Errorf("%w: send media: %w", ErrSendOutcomeUnknown, err)
				if outcomeUnknown == nil {
					outcomeUnknown = unknown
				}
				return unknown
			}
			result.MsgID, err = requiredSendMsgID(updates, request.RandomID, "send file")
			return err
		})
	})
	if err != nil && outcomeUnknown != nil {
		err = errors.Join(err, outcomeUnknown)
	}
	return result, err
}
