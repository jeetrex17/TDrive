package tgclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
)

// downloadBlockBytes is one upload.getFile request, the largest Telegram
// serves. Offsets that are multiples of it satisfy every alignment rule.
const downloadBlockBytes = int64(RangeReadMaxBytes)

// downloadBlockRetry bounds how long one block keeps retrying in place before
// the transfer gives up. gotd needs up to its ping timeout to notice that a
// pooled connection went quiet, so the backoff climbs past that before the
// last attempt; only then does the caller's whole-file retry take over.
func downloadBlockRetry() FloodWaitRetryPolicy {
	return FloodWaitRetryPolicy{
		MaxRetries:          2,
		MaxWait:             30 * time.Second,
		MaxTotalWait:        time.Minute,
		MaxTransientRetries: 6,
		TransientBackoff:    500 * time.Millisecond,
		MaxTransientBackoff: 16 * time.Second,
		TransientJitter:     250 * time.Millisecond,
	}
}

// documentDownload copies one document into a writer block by block. Each
// block is retried in place, so a dropped pooled connection costs one block
// and never the bytes already written; the pool replaces the dead connection
// on the next request. gotd's downloader is not used here because it cancels
// every worker on the first transport error and cannot resume.
type documentDownload struct {
	// read fetches one block with the given reference; resolve fetches a
	// fresh reference once Telegram rejects the current one. Tests substitute
	// both.
	read    func(ctx context.Context, ref DocumentRef, offset int64, dst []byte) (int, error)
	resolve func(ctx context.Context) (DocumentRef, error)
	retry   FloodWaitRetryPolicy

	mu      sync.Mutex
	ref     DocumentRef
	refresh singleflight.Group
	retries atomic.Int64
}

// newDownload prepares a transfer of ref. Every block attempt takes the
// client current at that moment, so a block that failed because the run scope
// restarted lands on the new scope's pool instead of a dead one.
func (g *Gotd) newDownload(ref DocumentRef) *documentDownload {
	return &documentDownload{
		ref:   ref,
		retry: downloadBlockRetry(),
		read: func(ctx context.Context, ref DocumentRef, offset int64, dst []byte) (int, error) {
			client, err := g.acquire(ctx)
			if err != nil {
				return 0, err
			}
			return g.readDocumentRange(ctx, client, ref, offset, dst)
		},
		resolve: func(ctx context.Context) (DocumentRef, error) {
			return g.ResolveDocument(ctx, ref.Peer, ref.MsgID)
		},
	}
}

// parallel fills w with the whole document using threads workers. Blocks are
// handed out front to back, so the file on disk grows from the start and the
// workers never range far from one another.
func (d *documentDownload) parallel(ctx context.Context, w io.WriterAt, threads int) error {
	size := d.current().Size
	blocks := (size + downloadBlockBytes - 1) / downloadBlockBytes
	var next atomic.Int64
	grp, ctx := errgroup.WithContext(ctx)
	for i := int64(0); i < int64(threads) && i < blocks; i++ {
		grp.Go(func() error {
			buf := make([]byte, downloadBlockBytes)
			for {
				block := next.Add(1) - 1
				if block >= blocks {
					return nil
				}
				offset := block * downloadBlockBytes
				want := buf[:min(downloadBlockBytes, size-offset)]
				if err := d.readBlock(ctx, offset, want); err != nil {
					return err
				}
				if _, err := w.WriteAt(want, offset); err != nil {
					return fmt.Errorf("tgclient: write block at %d: %w", offset, err)
				}
			}
		})
	}
	return grp.Wait()
}

// stream writes the document to w in order, one block in flight.
func (d *documentDownload) stream(ctx context.Context, w io.Writer) error {
	size := d.current().Size
	buf := make([]byte, downloadBlockBytes)
	for offset := int64(0); offset < size; offset += downloadBlockBytes {
		want := buf[:min(downloadBlockBytes, size-offset)]
		if err := d.readBlock(ctx, offset, want); err != nil {
			return err
		}
		if _, err := w.Write(want); err != nil {
			return fmt.Errorf("tgclient: write block at %d: %w", offset, err)
		}
	}
	return nil
}

// readBlock fills dst from offset. A stale file reference is refreshed and
// the read repeated within the same attempt; transport failures and
// FLOOD_WAIT back off under d.retry.
func (d *documentDownload) readBlock(ctx context.Context, offset int64, dst []byte) error {
	attempts := 0
	err := d.retry.Do(ctx, func() error {
		attempts++
		ref := d.current()
		_, err := d.read(ctx, ref, offset, dst)
		if IsFileReferenceError(err) {
			if ref, err = d.refreshRef(ctx, ref); err == nil {
				_, err = d.read(ctx, ref, offset, dst)
			}
		}
		if err != nil {
			return fmt.Errorf("tgclient: download block at %d: %w", offset, normalizeError(err))
		}
		return nil
	})
	if attempts > 1 {
		d.retries.Add(int64(attempts - 1))
	}
	return err
}

// refreshRef replaces a reference Telegram rejected. Workers that fail
// together share one lookup, and one that arrives after the refresh reuses
// it instead of asking again.
func (d *documentDownload) refreshRef(ctx context.Context, stale DocumentRef) (DocumentRef, error) {
	if current := d.current(); !bytes.Equal(current.FileReference, stale.FileReference) {
		return current, nil
	}
	fresh, err, _ := d.refresh.Do("ref", func() (any, error) {
		ref, err := d.resolve(ctx)
		if err != nil {
			return nil, err
		}
		d.mu.Lock()
		d.ref = ref
		d.mu.Unlock()
		return ref, nil
	})
	if err != nil {
		return DocumentRef{}, err
	}
	return fresh.(DocumentRef), nil
}

func (d *documentDownload) current() DocumentRef {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.ref
}
