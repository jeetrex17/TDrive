package mountfs

import (
	"context"
	"fmt"
	"time"
)

const (
	// DefaultSnapshotTTL keeps repeated metadata walks inexpensive while
	// allowing projection changes to become visible without event wiring.
	DefaultSnapshotTTL = 1500 * time.Millisecond

	// DefaultMaxCachedDirectories bounds metadata retained by one mount.
	DefaultMaxCachedDirectories = 256

	// DefaultMaxCachedEntries bounds the aggregate number of children retained
	// across cached directory snapshots. Oversized snapshots are still returned
	// to their caller, but are not retained.
	DefaultMaxCachedEntries = 50_000

	// DefaultMaxConcurrentSnapshotLoads bounds distinct projection reads while
	// requests for the same parent continue to coalesce.
	DefaultMaxConcurrentSnapshotLoads = 16
)

// Options controls the bounded directory snapshot cache. Zero values select
// production defaults. DisableSnapshotCache is intended for diagnostics.
type Options struct {
	SnapshotTTL                time.Duration
	MaxCachedDirectories       int
	MaxCachedEntries           int
	MaxConcurrentSnapshotLoads int
	DisableSnapshotCache       bool
}

// FS exposes a stable, protocol-neutral view of one read-only TDrive channel.
type FS struct {
	channelID int64
	source    DirectorySource
	opener    ContentOpener
	cache     *snapshotCache
}

func New(channelID int64, source DirectorySource, opener ContentOpener) (*FS, error) {
	return NewWithOptions(channelID, source, opener, Options{})
}

// NewWithOptions creates a read-only filesystem with explicit cache limits.
func NewWithOptions(channelID int64, source DirectorySource, opener ContentOpener, options Options) (*FS, error) {
	return newFS(channelID, source, opener, options, time.Now)
}

func newFS(
	channelID int64,
	source DirectorySource,
	opener ContentOpener,
	options Options,
	now func() time.Time,
) (*FS, error) {
	if channelID <= 0 || source == nil || opener == nil {
		return nil, ErrInvalidConfiguration
	}
	if options.SnapshotTTL < 0 || options.MaxCachedDirectories < 0 || options.MaxCachedEntries < 0 || options.MaxConcurrentSnapshotLoads < 0 || now == nil {
		return nil, ErrInvalidConfiguration
	}
	if options.SnapshotTTL == 0 {
		options.SnapshotTTL = DefaultSnapshotTTL
	}
	if options.MaxCachedDirectories == 0 {
		options.MaxCachedDirectories = DefaultMaxCachedDirectories
	}
	if options.MaxCachedEntries == 0 {
		options.MaxCachedEntries = DefaultMaxCachedEntries
	}
	if options.MaxConcurrentSnapshotLoads == 0 {
		options.MaxConcurrentSnapshotLoads = DefaultMaxConcurrentSnapshotLoads
	}

	fs := &FS{channelID: channelID, source: source, opener: opener}
	if !options.DisableSnapshotCache {
		fs.cache = newSnapshotCache(
			options.MaxCachedDirectories,
			options.MaxCachedEntries,
			options.MaxConcurrentSnapshotLoads,
			options.SnapshotTTL,
			now,
		)
	}
	return fs, nil
}

func (fs *FS) ReadDir(ctx context.Context, path string) ([]Entry, error) {
	if err := fs.ready(ctx); err != nil {
		return nil, err
	}
	entry, err := fs.resolve(ctx, path)
	if err != nil {
		return nil, err
	}
	if entry.source.Kind != KindDirectory {
		return nil, ErrNotDirectory
	}
	snapshot, err := fs.directorySnapshot(ctx, entry.source.ID)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, len(snapshot.entries))
	for index, item := range snapshot.entries {
		out[index] = item.entry
	}
	return out, nil
}

func (fs *FS) Stat(ctx context.Context, path string) (Entry, error) {
	if err := fs.ready(ctx); err != nil {
		return Entry{}, err
	}
	entry, err := fs.resolve(ctx, path)
	if err != nil {
		return Entry{}, err
	}
	return entry.entry, nil
}

func (fs *FS) Open(ctx context.Context, path string) (*File, error) {
	if err := fs.ready(ctx); err != nil {
		return nil, err
	}
	entry, err := fs.resolve(ctx, path)
	if err != nil {
		return nil, err
	}
	if entry.source.Kind == KindDirectory {
		return nil, ErrIsDirectory
	}
	content, err := fs.opener.OpenContent(ctx, fs.channelID, entry.source)
	if err != nil {
		return nil, err
	}
	if content == nil {
		return nil, ErrContentUnavailable
	}
	return &File{entry: entry.entry, content: content}, nil
}

// InvalidateDirectories evicts snapshots for mutated parent directories.
// Passing both the source and destination parent after a move makes the
// committed namespace visible immediately without flushing unrelated cache
// entries. Duplicate IDs are harmless.
func (fs *FS) InvalidateDirectories(parentIDs ...string) {
	if fs == nil || fs.cache == nil {
		return
	}
	fs.cache.invalidateDirectories(parentIDs...)
}

// InvalidateSubtree evicts a directory snapshot and all of its descendants
// that are currently discoverable in the bounded cache. Callers should also
// invalidate the directory's parent when its name or membership changed.
func (fs *FS) InvalidateSubtree(rootID string) {
	if fs == nil || fs.cache == nil {
		return
	}
	fs.cache.invalidateSubtree(rootID)
}

func (fs *FS) ready(ctx context.Context) error {
	if fs == nil || fs.source == nil || fs.opener == nil || fs.channelID <= 0 {
		return ErrInvalidConfiguration
	}
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrInvalidConfiguration)
	}
	return ctx.Err()
}

func (fs *FS) resolve(ctx context.Context, value string) (snapshotEntry, error) {
	parts, err := splitAbsolutePath(value)
	if err != nil {
		return snapshotEntry{}, err
	}
	root := snapshotEntry{
		source: SourceEntry{ID: RootID, ParentID: RootID, Kind: KindDirectory},
		entry: Entry{
			ChannelID: fs.channelID,
			ID:        RootID,
			ParentID:  RootID,
			Kind:      KindDirectory,
		},
	}
	if len(parts) == 0 {
		return root, nil
	}

	current := root
	for index, part := range parts {
		if current.source.Kind != KindDirectory {
			return snapshotEntry{}, ErrNotDirectory
		}
		snapshot, err := fs.directorySnapshot(ctx, current.source.ID)
		if err != nil {
			return snapshotEntry{}, err
		}
		childIndex, ok := snapshot.byName[NameKey(part)]
		if !ok {
			return snapshotEntry{}, ErrNotFound
		}
		current = snapshot.entries[childIndex]
		if index < len(parts)-1 && current.source.Kind != KindDirectory {
			return snapshotEntry{}, ErrNotDirectory
		}
	}
	return current, nil
}

func (fs *FS) directorySnapshot(ctx context.Context, parentID string) (directorySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return directorySnapshot{}, err
	}
	if fs.cache != nil {
		return fs.cache.getOrLoad(ctx, parentID, fs.loadDirectorySnapshot)
	}
	return fs.loadDirectorySnapshot(ctx, parentID)
}

func (fs *FS) loadDirectorySnapshot(ctx context.Context, parentID string) (directorySnapshot, error) {
	entries, err := fs.source.ListDirectory(ctx, fs.channelID, parentID)
	if err != nil {
		return directorySnapshot{}, err
	}
	snapshot, err := buildDirectorySnapshot(fs.channelID, parentID, entries)
	if err != nil {
		return directorySnapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return directorySnapshot{}, err
	}
	return snapshot, nil
}

func (fs *FS) cacheLen() int {
	if fs == nil || fs.cache == nil {
		return 0
	}
	return fs.cache.len()
}
