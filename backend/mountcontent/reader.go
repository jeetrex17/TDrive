// Package mountcontent exposes projected TDrive files as immutable,
// context-aware random-access readers for filesystem adapters.
package mountcontent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"sync"
	"sync/atomic"

	"TDrive/backend/media"
	"TDrive/backend/tgclient"

	"golang.org/x/sync/errgroup"
)

const maxConcurrentDocumentResolutions = 4

var (
	ErrEncryptedUnsupported = errors.New("mount content: encrypted files are not supported yet")
	ErrClosed               = errors.New("mount content: opener is closed")
	ErrReaderClosed         = errors.New("mount content: reader is closed")
	ErrNilContext           = errors.New("mount content: context is required")
)

// PeerResolver resolves the Telegram peer for the drive pinned to a mount.
type PeerResolver interface {
	ResolvePeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error)
}

// Config contains the dependencies shared by all files in one daemon.
type Config struct {
	DB     *sql.DB
	Peers  PeerResolver
	Ranges tgclient.RangeClient
}

// Opener owns the process-wide immutable block cache used by mounted files.
// Individual readers pin their resolved Telegram document references while
// sharing this cache and its singleflight request de-duplication.
type Opener struct {
	resolver *media.Resolver
	peers    PeerResolver
	ranges   tgclient.RangeClient
	reader   *media.RangeReader

	mu             sync.RWMutex
	closed         bool
	lifetime       context.Context
	cancelLifetime context.CancelFunc
	resolveSlots   chan struct{}
}

// New constructs a shared content opener. Dependencies are validated here so
// filesystem callbacks fail during mount startup rather than during a read.
func New(cfg Config) (*Opener, error) {
	switch {
	case cfg.DB == nil:
		return nil, errors.New("mount content: database is required")
	case cfg.Peers == nil:
		return nil, errors.New("mount content: peer resolver is required")
	case cfg.Ranges == nil:
		return nil, errors.New("mount content: range client is required")
	}
	lifetime, cancelLifetime := context.WithCancel(context.Background())

	return &Opener{
		resolver:       media.NewResolver(cfg.DB),
		peers:          cfg.Peers,
		ranges:         cfg.Ranges,
		lifetime:       lifetime,
		cancelLifetime: cancelLifetime,
		resolveSlots:   make(chan struct{}, maxConcurrentDocumentResolutions),
		reader: media.NewRangeReader(media.RangeReaderConfig{
			Client:         cfg.Ranges,
			PrefetchBlocks: 1,
			Background:     true,
		}),
	}, nil
}

// Close releases the shared cache and cancels speculative range fetches.
func (o *Opener) Close() {
	if o == nil {
		return
	}
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return
	}
	o.closed = true
	reader := o.reader
	cancelLifetime := o.cancelLifetime
	o.mu.Unlock()

	if cancelLifetime != nil {
		cancelLifetime()
	}
	if reader != nil {
		reader.Close()
	}
}

// Open resolves and pins the immutable Telegram bodies backing one projected
// file. Network body bytes are fetched lazily by Reader.ReadAt.
func (o *Opener) Open(ctx context.Context, channelID, fileID int64) (*Reader, error) {
	if o == nil {
		return nil, ErrClosed
	}
	if ctx == nil {
		return nil, ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	openCtx, sharedReader, openerDone, cleanup, err := o.beginOpen(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	file, err := o.resolver.Resolve(openCtx, channelID, fileID)
	if err != nil {
		return nil, o.normalizeOpenError(err)
	}
	if err := o.ensureOpen(); err != nil {
		return nil, err
	}
	if file.Encrypted {
		return nil, ErrEncryptedUnsupported
	}
	if err := validateLogicalMetadata(file); err != nil {
		return nil, err
	}

	peer, err := o.peers.ResolvePeer(openCtx, channelID)
	if err != nil {
		return nil, o.normalizeOpenError(fmt.Errorf("mount content: resolve drive: %w", err))
	}

	refs, err := o.resolveDocuments(openCtx, peer, file.Segments)
	if err != nil {
		return nil, o.normalizeOpenError(err)
	}
	segments, err := buildSegments(file.Segments, refs, file.StoredSize)
	if err != nil {
		return nil, err
	}
	if err := o.ensureOpen(); err != nil {
		return nil, err
	}

	return &Reader{
		size:       file.PlaintextSize,
		segments:   segments,
		ranges:     sharedReader,
		openerDone: openerDone,
	}, nil
}

func (o *Opener) beginOpen(
	ctx context.Context,
) (context.Context, *media.RangeReader, <-chan struct{}, func(), error) {
	o.mu.RLock()
	if o.closed {
		o.mu.RUnlock()
		return nil, nil, nil, nil, ErrClosed
	}
	lifetime := o.lifetime
	sharedReader := o.reader
	o.mu.RUnlock()

	openCtx, cancel := context.WithCancel(ctx)
	stopLifetimeLink := context.AfterFunc(lifetime, cancel)
	if lifetime.Err() != nil {
		stopLifetimeLink()
		cancel()
		return nil, nil, nil, nil, ErrClosed
	}
	cleanup := func() {
		stopLifetimeLink()
		cancel()
	}
	return openCtx, sharedReader, lifetime.Done(), cleanup, nil
}

func (o *Opener) ensureOpen() error {
	o.mu.RLock()
	closed := o.closed
	o.mu.RUnlock()
	if closed {
		return ErrClosed
	}
	return nil
}

func (o *Opener) normalizeOpenError(err error) error {
	if o.ensureOpen() != nil {
		return ErrClosed
	}
	return err
}

func (o *Opener) resolveDocuments(
	ctx context.Context,
	peer tgclient.InputPeer,
	projected []media.Segment,
) ([]tgclient.DocumentRef, error) {
	refs := make([]tgclient.DocumentRef, len(projected))
	group, resolveCtx := errgroup.WithContext(ctx)
	group.SetLimit(maxConcurrentDocumentResolutions)

	for index, part := range projected {
		if resolveCtx.Err() != nil {
			break
		}
		index, part := index, part
		group.Go(func() error {
			ref, err := o.resolveDocument(resolveCtx, peer, part)
			if err != nil {
				return fmt.Errorf("mount content: resolve message %d: %w", part.MsgID, err)
			}
			refs[index] = ref
			return nil
		})
	}

	if err := group.Wait(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return refs, nil
}

func (o *Opener) resolveDocument(
	ctx context.Context,
	peer tgclient.InputPeer,
	projected media.Segment,
) (tgclient.DocumentRef, error) {
	select {
	case o.resolveSlots <- struct{}{}:
		defer func() { <-o.resolveSlots }()
	case <-ctx.Done():
		return tgclient.DocumentRef{}, ctx.Err()
	}
	if err := o.ensureOpen(); err != nil {
		return tgclient.DocumentRef{}, err
	}
	if err := ctx.Err(); err != nil {
		return tgclient.DocumentRef{}, err
	}

	ref, err := o.ranges.ResolveDocument(ctx, peer, projected.MsgID)
	if err != nil {
		return tgclient.DocumentRef{}, err
	}
	if err := validateDocumentIdentity(peer, projected.MsgID, ref); err != nil {
		return tgclient.DocumentRef{}, err
	}
	return ref, nil
}

func validateDocumentIdentity(
	expectedPeer tgclient.InputPeer,
	expectedMsgID int64,
	ref tgclient.DocumentRef,
) error {
	if ref.MsgID != expectedMsgID {
		return fmt.Errorf(
			"mount content: Telegram message identity mismatch: requested=%d resolved=%d",
			expectedMsgID,
			ref.MsgID,
		)
	}
	if ref.Peer != expectedPeer {
		return fmt.Errorf("mount content: Telegram peer identity mismatch for message %d", expectedMsgID)
	}
	return nil
}

func validateLogicalMetadata(file media.LogicalFile) error {
	if file.PlaintextSize < 0 {
		return errors.New("mount content: negative plaintext size")
	}
	if file.PlaintextSize != file.StoredSize {
		return fmt.Errorf(
			"mount content: plaintext size %d does not match stored size %d",
			file.PlaintextSize,
			file.StoredSize,
		)
	}
	_, err := validateProjectedSegments(file.Segments, file.StoredSize)
	return err
}

func buildSegments(
	projected []media.Segment,
	refs []tgclient.DocumentRef,
	storedSize int64,
) ([]segment, error) {
	if len(projected) != len(refs) {
		return nil, fmt.Errorf(
			"mount content: segment metadata count mismatch: projection=%d telegram=%d",
			len(projected),
			len(refs),
		)
	}
	if _, err := validateProjectedSegments(projected, storedSize); err != nil {
		return nil, err
	}
	if _, err := validateResolvedSegments(projected, refs, storedSize); err != nil {
		return nil, err
	}

	segments := make([]segment, len(refs))
	var start int64
	for index, ref := range refs {
		segments[index] = segment{start: start, size: ref.Size, ref: ref}
		start += ref.Size
	}
	return segments, nil
}

func validateProjectedSegments(projected []media.Segment, storedSize int64) (int64, error) {
	if storedSize < 0 {
		return 0, errors.New("mount content: negative stored size")
	}
	var total int64
	for _, part := range projected {
		if part.Size < 0 {
			return 0, fmt.Errorf("mount content: message %d has negative projected size", part.MsgID)
		}
		if part.Size > math.MaxInt64-total {
			return 0, errors.New("mount content: projected segment sizes overflow int64")
		}
		total += part.Size
	}
	if total != storedSize {
		return 0, fmt.Errorf("mount content: projected segment size %d does not match file size %d", total, storedSize)
	}
	return total, nil
}

func validateResolvedSegments(
	projected []media.Segment,
	refs []tgclient.DocumentRef,
	storedSize int64,
) (int64, error) {
	var total int64
	for index, ref := range refs {
		part := projected[index]
		if ref.Size < 0 {
			return 0, fmt.Errorf("mount content: message %d has negative Telegram size", part.MsgID)
		}
		if ref.Size > math.MaxInt64-total {
			return 0, errors.New("mount content: Telegram segment sizes overflow int64")
		}
		total += ref.Size
	}
	if total != storedSize {
		return 0, fmt.Errorf("mount content: segment size %d does not match file size %d", total, storedSize)
	}
	for index, ref := range refs {
		part := projected[index]
		if ref.Size != part.Size {
			return 0, fmt.Errorf(
				"mount content: message %d size mismatch: projection=%d telegram=%d",
				part.MsgID,
				part.Size,
				ref.Size,
			)
		}
	}
	return total, nil
}

type segment struct {
	start int64
	size  int64
	ref   tgclient.DocumentRef
}

// Reader pins one immutable logical-file revision. It is safe for concurrent
// ReadAt and Close calls because metadata is immutable, lifecycle state is
// atomic, and RangeReader is concurrency-safe.
type Reader struct {
	size       int64
	segments   []segment
	ranges     *media.RangeReader
	openerDone <-chan struct{}
	closed     atomic.Bool
}

func (r *Reader) Size() int64 {
	if r == nil {
		return 0
	}
	return r.size
}

// Close invalidates only this logical handle. Reader does not own the shared
// range cache, so closing one file never affects other readers or the Opener.
func (r *Reader) Close() error {
	if r != nil {
		r.closed.Store(true)
	}
	return nil
}

// ReadAt follows io.ReaderAt's short-read and EOF contract while translating
// logical offsets across multipart Telegram documents.
func (r *Reader) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	if ctx == nil {
		return 0, ErrNilContext
	}
	if r == nil || r.ranges == nil {
		return 0, ErrReaderClosed
	}
	if r.closed.Load() {
		return 0, ErrReaderClosed
	}
	select {
	case <-r.openerDone:
		return 0, ErrClosed
	default:
	}
	if off < 0 {
		return 0, errors.New("mount content: negative read offset")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if off >= r.size {
		return 0, io.EOF
	}

	want := len(p)
	if remaining := r.size - off; int64(want) > remaining {
		want = int(remaining)
	}

	done := 0
	for done < want {
		absolute := off + int64(done)
		part, ok := r.segmentFor(absolute)
		if !ok {
			return done, io.ErrUnexpectedEOF
		}
		partOffset := absolute - part.start
		need := want - done
		if available := part.size - partOffset; int64(need) > available {
			need = int(available)
		}

		n, err := r.ranges.ReadStoredAt(ctx, part.ref, p[done:done+need], partOffset)
		done += n
		if err != nil {
			return done, err
		}
		if n != need {
			return done, io.ErrUnexpectedEOF
		}
	}

	if done < len(p) {
		return done, io.EOF
	}
	return done, nil
}

func (r *Reader) segmentFor(offset int64) (segment, bool) {
	index := sort.Search(len(r.segments), func(index int) bool {
		part := r.segments[index]
		return part.start+part.size > offset
	})
	if index >= len(r.segments) || offset < r.segments[index].start {
		return segment{}, false
	}
	return r.segments[index], true
}
