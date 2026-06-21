package tgclient

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"slices"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

func (g *Gotd) ResolveDocument(ctx context.Context, peer InputPeer, msgID int64) (DocumentRef, error) {
	var ref DocumentRef
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		doc, name, err := getDocumentByMessageID(ctx, api, peer, msgID)
		if err != nil {
			return err
		}
		ref = documentRefFromTG(peer, msgID, doc, name)
		return nil
	})
	return ref, err
}

func (g *Gotd) ReadDocumentRange(ctx context.Context, ref DocumentRef, offset int64, dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	if offset < 0 {
		return 0, fmt.Errorf("tgclient: negative range offset")
	}
	if offset%RangeReadAlignment != 0 {
		return 0, fmt.Errorf("tgclient: range offset %d is not %d-aligned", offset, RangeReadAlignment)
	}
	if len(dst) > RangeReadMaxBytes {
		return 0, fmt.Errorf("tgclient: range length %d exceeds %d", len(dst), RangeReadMaxBytes)
	}
	if crossesRangeBoundary(offset, len(dst)) {
		return 0, fmt.Errorf("tgclient: range crosses %d-byte boundary", RangeReadMaxBytes)
	}

	var n int
	err := g.runClient(ctx, func(ctx context.Context, client *telegram.Client) error {
		current := ref
		for attempt := 0; attempt < 2; attempt++ {
			read, err := g.readDocumentRange(ctx, client, current, offset, dst)
			if err == nil {
				n = read
				return nil
			}
			if attempt == 0 && isFileReferenceError(err) {
				refreshed, refreshErr := resolveDocumentRef(ctx, client.API(), ref.Peer, ref.MsgID)
				if refreshErr != nil {
					return fmt.Errorf("tgclient: refresh file reference after %v: %w", err, refreshErr)
				}
				current = refreshed
				continue
			}
			return err
		}
		return nil
	})
	return n, err
}

func resolveDocumentRef(ctx context.Context, api *tg.Client, peer InputPeer, msgID int64) (DocumentRef, error) {
	doc, name, err := getDocumentByMessageID(ctx, api, peer, msgID)
	if err != nil {
		return DocumentRef{}, err
	}
	return documentRefFromTG(peer, msgID, doc, name), nil
}

func documentRefFromTG(peer InputPeer, msgID int64, doc *tg.Document, name string) DocumentRef {
	return DocumentRef{
		Peer:          peer,
		MsgID:         msgID,
		Size:          doc.Size,
		Name:          name,
		DocumentID:    doc.ID,
		AccessHash:    doc.AccessHash,
		FileReference: append([]byte(nil), doc.FileReference...),
	}
}

func (g *Gotd) readDocumentRange(ctx context.Context, client *telegram.Client, ref DocumentRef, offset int64, dst []byte) (int, error) {
	limit := roundedTelegramLimit(len(dst))
	req := &tg.UploadGetFileRequest{
		Location: &tg.InputDocumentFileLocation{
			ID:            ref.DocumentID,
			AccessHash:    ref.AccessHash,
			FileReference: ref.FileReference,
		},
		Offset: offset,
		Limit:  limit,
	}
	req.SetPrecise(true)
	req.SetCDNSupported(true)

	// client.API() is backed by telegram.Client.invokeDirect, so gotd follows
	// FILE_MIGRATE by invoking upload.getFile on the target data center before
	// returning here.
	result, err := client.API().UploadGetFile(ctx, req)
	if err != nil {
		return 0, err
	}
	switch file := result.(type) {
	case *tg.UploadFile:
		return copyRequestedRange(dst, file.Bytes)
	case *tg.UploadFileCDNRedirect:
		return g.readCDNDocumentRange(ctx, client, file, offset, limit, dst)
	default:
		return 0, fmt.Errorf("tgclient: unexpected upload.getFile result %T", result)
	}
}

func (g *Gotd) readCDNDocumentRange(ctx context.Context, client *telegram.Client, redirect *tg.UploadFileCDNRedirect, offset int64, limit int, dst []byte) (int, error) {
	cdn, err := g.cdnClient(ctx, client, redirect.DCID)
	if err != nil {
		return 0, err
	}

	plain, err := g.readCDNPlain(ctx, client, cdn, redirect, offset, limit)
	if err != nil {
		return 0, err
	}
	if err := g.verifyCDNRange(ctx, client, cdn, redirect, offset, plain); err != nil {
		return 0, err
	}
	return copyRequestedRange(dst, plain)
}

func (g *Gotd) readCDNPlain(ctx context.Context, client *telegram.Client, cdn *tg.Client, redirect *tg.UploadFileCDNRedirect, offset int64, limit int) ([]byte, error) {
	for attempt := 0; attempt < 2; attempt++ {
		result, err := cdn.UploadGetCDNFile(ctx, &tg.UploadGetCDNFileRequest{
			FileToken: redirect.FileToken,
			Offset:    offset,
			Limit:     limit,
		})
		if err != nil {
			return nil, err
		}
		switch file := result.(type) {
		case *tg.UploadCDNFile:
			return decryptCDNBytes(redirect, file.Bytes, offset)
		case *tg.UploadCDNFileReuploadNeeded:
			if attempt > 0 {
				return nil, fmt.Errorf("tgclient: cdn token still requires reupload")
			}
			if _, err := client.API().UploadReuploadCDNFile(ctx, &tg.UploadReuploadCDNFileRequest{
				FileToken:    redirect.FileToken,
				RequestToken: file.RequestToken,
			}); err != nil {
				return nil, fmt.Errorf("tgclient: reupload cdn file: %w", err)
			}
		default:
			return nil, fmt.Errorf("tgclient: unexpected upload.getCdnFile result %T", result)
		}
	}
	return nil, fmt.Errorf("tgclient: cdn range read failed")
}

func (g *Gotd) cdnClient(ctx context.Context, client *telegram.Client, dcID int) (*tg.Client, error) {
	g.cdnMu.Lock()
	if invoker, ok := g.cdn[dcID]; ok {
		g.cdnMu.Unlock()
		return tg.NewClient(invoker), nil
	}
	g.cdnMu.Unlock()

	invoker, err := client.MediaOnly(ctx, dcID, 2)
	if err != nil {
		return nil, fmt.Errorf("tgclient: cdn dc %d: %w", dcID, err)
	}

	g.cdnMu.Lock()
	if existing, ok := g.cdn[dcID]; ok {
		g.cdnMu.Unlock()
		_ = invoker.Close()
		return tg.NewClient(existing), nil
	}
	g.cdn[dcID] = invoker
	g.cdnMu.Unlock()
	return tg.NewClient(invoker), nil
}

func (g *Gotd) verifyCDNRange(ctx context.Context, client *telegram.Client, cdn *tg.Client, redirect *tg.UploadFileCDNRedirect, offset int64, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	hashes, err := cdn.UploadGetCDNFileHashes(ctx, &tg.UploadGetCDNFileHashesRequest{
		FileToken: redirect.FileToken,
		Offset:    offset,
	})
	if err != nil {
		return fmt.Errorf("tgclient: cdn hashes: %w", err)
	}
	fetch := func(h tg.FileHash) ([]byte, error) {
		return g.readCDNPlain(ctx, client, cdn, redirect, h.Offset, h.Limit)
	}
	if err := verifyFileHashes(offset, data, hashes, fetch); err != nil {
		return fmt.Errorf("tgclient: cdn integrity: %w", err)
	}
	return nil
}

func verifyFileHashes(offset int64, data []byte, hashes []tg.FileHash, fetch func(tg.FileHash) ([]byte, error)) error {
	if len(data) == 0 {
		return nil
	}
	sorted := append([]tg.FileHash(nil), hashes...)
	slices.SortFunc(sorted, func(a, b tg.FileHash) int {
		switch {
		case a.Offset < b.Offset:
			return -1
		case a.Offset > b.Offset:
			return 1
		default:
			return 0
		}
	})

	end := offset + int64(len(data))
	pos := offset
	for _, h := range sorted {
		if h.Limit <= 0 {
			continue
		}
		hashStart := h.Offset
		if hashStart >= end {
			break
		}
		chunk, err := hashChunkData(offset, data, h, fetch)
		if err != nil {
			return err
		}
		hashEnd := h.Offset + int64(len(chunk))
		if hashEnd <= pos {
			continue
		}
		if hashStart > pos {
			return fmt.Errorf("hash coverage gap at %d", pos)
		}
		sum := sha256.Sum256(chunk)
		if !slices.Equal(sum[:], h.Hash) {
			return fmt.Errorf("hash mismatch at %d", hashStart)
		}

		verifyEnd := hashEnd
		if verifyEnd > end {
			verifyEnd = end
		}
		pos = verifyEnd
		if pos == end {
			return nil
		}
	}
	return fmt.Errorf("hash coverage ended at %d, want %d", pos, end)
}

func hashChunkData(offset int64, data []byte, h tg.FileHash, fetch func(tg.FileHash) ([]byte, error)) ([]byte, error) {
	relStart := h.Offset - offset
	relEnd := relStart + int64(h.Limit)
	if relStart >= 0 && relEnd <= int64(len(data)) {
		return data[relStart:relEnd], nil
	}
	if fetch == nil {
		return nil, fmt.Errorf("hash range %d-%d is not fully available", h.Offset, h.Offset+int64(h.Limit))
	}
	chunk, err := fetch(h)
	if err != nil {
		return nil, fmt.Errorf("fetch hash range %d-%d: %w", h.Offset, h.Offset+int64(h.Limit), err)
	}
	if len(chunk) == 0 {
		return nil, fmt.Errorf("empty hash range %d-%d", h.Offset, h.Offset+int64(h.Limit))
	}
	return chunk, nil
}

func decryptCDNBytes(redirect *tg.UploadFileCDNRedirect, src []byte, offset int64) ([]byte, error) {
	block, err := aes.NewCipher(redirect.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("tgclient: cdn cipher: %w", err)
	}
	if block.BlockSize() != len(redirect.EncryptionIv) {
		return nil, fmt.Errorf("tgclient: invalid cdn iv length")
	}
	iv := append([]byte(nil), redirect.EncryptionIv...)
	binary.BigEndian.PutUint32(iv[len(iv)-4:], uint32(offset/16))
	dst := make([]byte, len(src))
	cipher.NewCTR(block, iv).XORKeyStream(dst, src)
	return dst, nil
}

func copyRequestedRange(dst, src []byte) (int, error) {
	if len(src) < len(dst) {
		return 0, io.ErrUnexpectedEOF
	}
	copy(dst, src[:len(dst)])
	return len(dst), nil
}

func roundedTelegramLimit(want int) int {
	if want <= 0 {
		return 0
	}
	alignment := int(RangeReadAlignment)
	rounded := ((want + alignment - 1) / alignment) * alignment
	if rounded > RangeReadMaxBytes {
		return RangeReadMaxBytes
	}
	return rounded
}

func crossesRangeBoundary(offset int64, length int) bool {
	if length <= 0 {
		return false
	}
	start := offset / int64(RangeReadMaxBytes)
	end := (offset + int64(length) - 1) / int64(RangeReadMaxBytes)
	return start != end
}

func isFileReferenceError(err error) bool {
	return tg.IsFileReferenceEmpty(err) || tg.IsFileReferenceExpired(err) || tg.IsFileReferenceInvalid(err)
}
