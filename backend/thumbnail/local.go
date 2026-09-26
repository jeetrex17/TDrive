package thumbnail

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"io"
	"os"
	"runtime"
)

const localSourceLimit = 30 << 20

// A generation owns its decoder working set until it returns. Network upload
// concurrency must never multiply simultaneous full-resolution decodes.
var localDecodeSlot = make(chan struct{}, 1)

// GenerateLocal creates an upload rendition without altering the source reader's
// position. Native decoders sample large named files directly; the portable
// fallback admits only sources whose full decode fits a conservative budget.
func GenerateLocal(ctx context.Context, source io.ReadSeeker, maxEdge int) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("thumbnail: context required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if source == nil || maxEdge < 1 || maxEdge > 1600 {
		return nil, fmt.Errorf("thumbnail: invalid local rendition size")
	}
	select {
	case localDecodeSlot <- struct{}{}:
		defer func() { <-localDecodeSlot }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if file, ok := source.(*os.File); ok {
		info, err := file.Stat()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() > localSourceLimit {
			return nil, ErrTooLarge
		}
		if result, handled, err := generateNativeLocal(file.Name(), maxEdge); handled {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return result, err
		}
	}
	// Decrypted originals use readers backed by memory. Sample them natively
	// too, without creating a plaintext temporary file.
	position, err := source.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, err
	}
	defer func() { _, _ = source.Seek(position, io.SeekStart) }()
	if _, err = source.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(source, localSourceLimit+1))
	defer clear(raw)
	if err != nil {
		return nil, err
	}
	if len(raw) > localSourceLimit {
		return nil, ErrTooLarge
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if result, handled, err := generateNativeBytes(raw, maxEdge); handled {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return result, err
	}
	return generatePortableLocal(ctx, raw, maxEdge)
}

func generatePortableLocal(ctx context.Context, raw []byte, maxEdge int) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, ErrUnsupported
	}
	// Desktop fallback stays below roughly 96 MB for a four-byte source bitmap.
	// Mobile hosts require sampled native decoding for ordinary camera photos.
	pixelLimit := int64(maxSourcePixels)
	if runtime.GOOS == "android" || runtime.GOOS == "ios" {
		pixelLimit = 2_000_000
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width) > pixelLimit/int64(cfg.Height) {
		return nil, ErrTooLarge
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result, err := Generate(raw, maxEdge)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return result, err
}
