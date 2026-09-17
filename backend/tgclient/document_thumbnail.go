package tgclient

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	"io"
)

// DocumentThumbnailSender is optional: clients without this capability still
// upload the original and can publish the portable hidden rendition documents.
// Encrypted originals must never use a readable Telegram thumbnail.
type DocumentThumbnailSender interface {
	SendFileWithThumbnail(context.Context, InputPeer, io.Reader, string, string, int64, func(int64, int64), int64, []byte) (SendFileResult, error)
}

func validateDocumentThumbnail(jpeg []byte) error {
	if len(jpeg) == 0 {
		return nil
	}
	if len(jpeg) > 200*1024 {
		return fmt.Errorf("tgclient: thumbnail exceeds 200 KiB")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(jpeg))
	if err != nil || format != "jpeg" || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 320 || cfg.Height > 320 {
		return fmt.Errorf("tgclient: thumbnail must be a JPEG up to 320 pixels")
	}
	return nil
}

func (g *Gotd) SendFileWithThumbnail(ctx context.Context, peer InputPeer, r io.Reader, name, caption string, totalSize int64, progress func(int64, int64), randomID int64, thumb []byte) (SendFileResult, error) {
	if err := validateDocumentThumbnail(thumb); err != nil {
		return SendFileResult{}, err
	}
	return g.sendFileWithThumbnail(ctx, peer, r, name, caption, totalSize, progress, randomID, thumb)
}
