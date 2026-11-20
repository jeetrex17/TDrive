package tgclient

import (
	"context"
	"errors"
	"fmt"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// MessageDocumentResolver reads message policy and the document reference from
// the same message response so an edit cannot separate those two observations.
type MessageDocumentResolver interface {
	ResolveMessageDocument(context.Context, InputPeer, int64) (HistoryMessage, DocumentRef, error)
}

// ExistingMediaSender publishes an existing Telegram document to a
// destination channel without transferring the payload through this device.
// The resolver authorizes the exact source after write admission on every
// attempt. Retries preserve the operation's random ID.
type ExistingMediaSender interface {
	ResendMedia(ctx context.Context, destination InputPeer, caption string, randomID int64, resolve func(context.Context) (DocumentRef, error)) (SendFileResult, error)
}

func (g *Gotd) ResolveMessageDocument(ctx context.Context, peer InputPeer, msgID int64) (HistoryMessage, DocumentRef, error) {
	var message HistoryMessage
	var ref DocumentRef
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		messages, err := channelMessages(ctx, api, peer, []int64{msgID})
		if err != nil {
			return err
		}
		raw := messages[msgID]
		converted, ok := historyMessageFromTG(raw)
		if !ok || converted.Placeholder || converted.MsgID != msgID || !messageBelongsToPeer(raw, peer) {
			return ErrMessageNotFound
		}
		message = converted
		document, name, err := documentOf(raw)
		if err != nil {
			return err
		}
		ref = documentRefFromTG(peer, msgID, document, name)
		return nil
	})
	return message, ref, err
}

func (g *Gotd) ResendMedia(ctx context.Context, destination InputPeer, caption string, randomID int64, resolve func(context.Context) (DocumentRef, error)) (SendFileResult, error) {
	return g.resendMedia(ctx, destination, caption, randomID, resolve,
		func(ctx context.Context, request *tg.MessagesSendMediaRequest) (tg.UpdatesClass, error) {
			var updates tg.UpdatesClass
			err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
				var err error
				updates, err = api.MessagesSendMedia(ctx, request)
				return err
			})
			return updates, err
		})
}

func (g *Gotd) resendMedia(
	ctx context.Context, destination InputPeer, caption string, randomID int64,
	resolve func(context.Context) (DocumentRef, error),
	send func(context.Context, *tg.MessagesSendMediaRequest) (tg.UpdatesClass, error),
) (SendFileResult, error) {
	if destination.PeerKind() != PeerChannel || destination.ChannelID <= 0 || caption == "" || randomID <= 0 || resolve == nil {
		return SendFileResult{}, fmt.Errorf("tgclient: invalid existing media send")
	}
	var result SendFileResult
	var uncertain error
	refreshAvailable := true
	attempt := func() error {
		return g.writes.Do(ctx, writeClassMessage, func() error {
			ref, err := resolve(ctx)
			if err != nil {
				return err
			}
			if ref.DocumentID <= 0 || ref.Size <= 0 || len(ref.FileReference) == 0 || ref.PhotoSizeType != "" {
				return ErrNotFile
			}
			request := &tg.MessagesSendMediaRequest{
				Peer: toPeer(destination), Message: caption, RandomID: randomID, Silent: true,
				Media: &tg.InputMediaDocument{ID: &tg.InputDocument{
					ID: ref.DocumentID, AccessHash: ref.AccessHash, FileReference: ref.FileReference,
				}},
			}
			updates, err := send(ctx, request)
			if err == nil {
				result.MsgID = resentMediaReceipt(updates, request, destination, ref.DocumentID)
				if result.MsgID > 0 {
					return nil
				}
				err = fmt.Errorf("Telegram returned no matching destination message ID")
			} else {
				err = normalizeError(err)
				if definiteMediaRejection(err) {
					return err
				}
			}
			uncertain = fmt.Errorf("%w: resend media: %w", ErrSendOutcomeUnknown, err)
			return uncertain
		})
	}
	err := DefaultWriteFloodWaitRetryPolicy().Do(ctx, func() error {
		err := attempt()
		if refreshAvailable && tgerr.Is(err, "FILE_REFERENCE_EXPIRED", "FILE_REFERENCE_INVALID", "FILEREF_UPGRADE_NEEDED") {
			refreshAvailable = false
			// Reenter the queue so the refreshed reference is obtained after
			// any new write cooldown and under the same scope checks.
			return attempt()
		}
		return err
	})
	if err != nil && uncertain != nil && !errors.Is(err, ErrSendOutcomeUnknown) {
		err = errors.Join(err, uncertain)
	}
	return result, err
}

func definiteMediaRejection(err error) bool {
	if _, floodWait := FloodWaitDuration(err); floodWait {
		return true
	}
	rpcErr, ok := errors.AsType[*tgerr.Error](err)
	return ok && rpcErr.Code >= 400 && rpcErr.Code < 500 && !rpcErr.IsType("RANDOM_ID_DUPLICATE")
}

func resentMediaReceipt(updates tg.UpdatesClass, request *tg.MessagesSendMediaRequest, destination InputPeer, documentID int64) int64 {
	var items []tg.UpdateClass
	switch value := updates.(type) {
	case *tg.UpdateShortSentMessage:
		return int64(value.ID)
	case *tg.Updates:
		items = value.Updates
	case *tg.UpdatesCombined:
		items = value.Updates
	case *tg.UpdateShort:
		items = []tg.UpdateClass{value.Update}
	}
	for _, item := range items {
		if mapping, ok := item.(*tg.UpdateMessageID); ok && mapping.RandomID == request.RandomID && mapping.ID > 0 {
			return int64(mapping.ID)
		}
	}
	// Telegram can send a channel message update without a random ID mapping.
	// The unique operation caption and the exact peer and document still
	// identify the receipt. Unrelated updates are never treated as acceptance.
	for _, item := range items {
		update, ok := item.(*tg.UpdateNewChannelMessage)
		if !ok {
			continue
		}
		message, ok := update.Message.(*tg.Message)
		if !ok || message.ID <= 0 || message.Message != request.Message || !messageBelongsToPeer(message, destination) {
			continue
		}
		document, _, err := documentOf(message)
		if err == nil && document.ID == documentID {
			return int64(message.ID)
		}
	}
	return 0
}

var (
	_ ExistingMediaSender     = (*Gotd)(nil)
	_ MessageDocumentResolver = (*Gotd)(nil)
)
