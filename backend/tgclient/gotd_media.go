package tgclient

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/gotd/td/tg"
)

// mediaRefFromTG shares the existing document transport with Telegram photos,
// while leaving whole-document extraction available to document-only callers.
func mediaRefFromTG(peer InputPeer, msgID int64, msg tg.MessageClass) (DocumentRef, error) {
	full, ok := msg.(*tg.Message)
	if !ok {
		return DocumentRef{}, ErrMessageNotFound
	}
	media, ok := full.Media.(*tg.MessageMediaPhoto)
	if !ok {
		doc, name, err := documentOf(msg)
		if err != nil {
			return DocumentRef{}, err
		}
		return documentRefFromTG(peer, msgID, doc, name), nil
	}
	photo, ok := media.Photo.(*tg.Photo)
	if !ok || photo.ID <= 0 {
		return DocumentRef{}, ErrEmptyDocument
	}
	variant, ok := largestPhotoSize(photo.Sizes)
	if !ok {
		return DocumentRef{}, ErrEmptyDocument
	}
	return DocumentRef{
		Peer: peer, MsgID: msgID, Size: variant.size,
		Name:       fmt.Sprintf("Telegram photo %d.jpg", msgID),
		DocumentID: photo.ID, AccessHash: photo.AccessHash, DCID: photo.DCID,
		FileReference: bytes.Clone(photo.FileReference),
		PhotoSizeType: variant.kind, InlineBytes: bytes.Clone(variant.inline),
	}, nil
}

func getMediaRefByMessageID(ctx context.Context, api *tg.Client, peer InputPeer, msgID int64) (DocumentRef, error) {
	messages, err := channelMessages(ctx, api, peer, []int64{msgID})
	if err != nil {
		return DocumentRef{}, err
	}
	msg, ok := messages[msgID]
	if !ok {
		return DocumentRef{}, ErrMessageNotFound
	}
	return mediaRefFromTG(peer, msgID, msg)
}

func readInlinePhoto(ctx context.Context, ref DocumentRef, offset int64, dst []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if offset >= int64(len(ref.InlineBytes)) {
		return 0, io.EOF
	}
	return copyRequestedRange(dst, ref.InlineBytes[offset:])
}

type photoVariant struct {
	kind   string
	size   int64
	area   int64
	inline []byte
}

// largestPhotoSize selects an actual JPEG, never a stripped/path placeholder.
// Progressive sizes describe successive prefixes; the final prefix is the
// complete file. Cached-only photos remain usable without another RPC.
func largestPhotoSize(sizes []tg.PhotoSizeClass) (photoVariant, bool) {
	var best photoVariant
	for _, size := range sizes {
		var width, height int
		candidate := photoVariant{kind: size.GetType()}
		switch value := size.(type) {
		case *tg.PhotoSize:
			width, height = value.W, value.H
			candidate.size = int64(value.Size)
		case *tg.PhotoSizeProgressive:
			width, height = value.W, value.H
			if len(value.Sizes) > 0 {
				candidate.size = int64(value.Sizes[len(value.Sizes)-1])
			}
		case *tg.PhotoCachedSize:
			width, height = value.W, value.H
			if len(value.Bytes) <= RangeReadMaxBytes {
				candidate.size = int64(len(value.Bytes))
				candidate.inline = value.Bytes
			}
		default:
			continue
		}
		// Telegram dimensions are int32. This bound also makes area arithmetic
		// safe for unexpected values supplied by transport implementations.
		if candidate.kind == "" || candidate.size <= 0 || width <= 0 || height <= 0 || width > 1<<31-1 || height > 1<<31-1 {
			continue
		}
		candidate.area = int64(width) * int64(height)
		if candidate.area > best.area || candidate.area == best.area && candidate.size > best.size {
			best = candidate
		}
	}
	return best, best.size > 0
}
