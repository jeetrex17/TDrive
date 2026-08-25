package mountfs

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewRejectsInvalidDependencies(t *testing.T) {
	t.Parallel()

	source := newFakeDirectorySource(nil)
	opener := &fakeContentOpener{}

	tests := []struct {
		name      string
		channelID int64
		source    DirectorySource
		opener    ContentOpener
	}{
		{name: "zero channel", channelID: 0, source: source, opener: opener},
		{name: "negative channel", channelID: -1, source: source, opener: opener},
		{name: "nil directory source", channelID: 42, opener: opener},
		{name: "nil content opener", channelID: 42, source: source},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := New(test.channelID, test.source, test.opener)
			if !errors.Is(err, ErrInvalidConfiguration) {
				t.Fatalf("New() error = %v, want ErrInvalidConfiguration", err)
			}
		})
	}
}

func TestReadDirExportsPortableUniqueNamesDeterministically(t *testing.T) {
	t.Parallel()

	children := []SourceEntry{
		{ID: "f:alpha", ParentID: RootID, Name: "Alpha", Kind: KindFile, Size: 5},
		{ID: "f:upper", ParentID: RootID, Name: "Report.txt", Kind: KindFile, Size: 10},
		{ID: "f:lower", ParentID: RootID, Name: "report.TXT", Kind: KindFile, Size: 11},
		{ID: "d:report", ParentID: RootID, Name: "REPORT.txt", Kind: KindDirectory},
		{ID: "f:invalid", ParentID: RootID, Name: `bad<name>.txt`, Kind: KindFile, Size: 12},
		{ID: "f:reserved", ParentID: RootID, Name: "CON", Kind: KindFile},
		{ID: "f:trailing", ParentID: RootID, Name: "trail. ", Kind: KindFile},
	}
	source := newFakeDirectorySource(map[string][]SourceEntry{RootID: children})
	source.reverseEveryOtherCall = true
	fs := mustNewFSWithOptions(t, 42, source, &fakeContentOpener{}, Options{DisableSnapshotCache: true})

	first, err := fs.ReadDir(context.Background(), "/")
	if err != nil {
		t.Fatalf("ReadDir() first error = %v", err)
	}
	second, err := fs.ReadDir(context.Background(), "/")
	if err != nil {
		t.Fatalf("ReadDir() second error = %v", err)
	}
	if !slices.Equal(first, second) {
		t.Fatalf("ReadDir() order or aliases changed with source order:\nfirst:  %#v\nsecond: %#v", first, second)
	}

	byID := entriesByID(first)
	if got := byID["f:alpha"].Name; got != "Alpha" {
		t.Errorf("portable name = %q, want Alpha", got)
	}
	if got := byID["f:invalid"].Name; got != "bad_name_.txt" {
		t.Errorf("invalid-character export = %q, want bad_name_.txt", got)
	}
	if got := byID["f:reserved"].Name; got != "_CON" {
		t.Errorf("reserved-name export = %q, want _CON", got)
	}
	if got := byID["f:trailing"].Name; got != "trail__" {
		t.Errorf("trailing-character export = %q, want trail__", got)
	}

	seenKeys := make(map[string]string, len(first))
	for _, entry := range first {
		key := NameKey(entry.Name)
		if previous, exists := seenKeys[key]; exists {
			t.Errorf("exported names are not case-insensitively unique: %q and %q", previous, entry.Name)
		}
		seenKeys[key] = entry.Name
	}

	for _, id := range []string{"f:upper", "f:lower", "d:report"} {
		entry := byID[id]
		if !strings.Contains(entry.Name, "[td-") {
			t.Errorf("collision entry %s was not assigned a stable alias: %q", id, entry.Name)
		}
		if entry.SourceName == entry.Name {
			t.Errorf("collision entry %s lost distinction between source and exported name", id)
		}
	}
}

func TestCollisionAliasDoesNotCollideWithLegacyAliasLikeName(t *testing.T) {
	t.Parallel()

	first := SourceEntry{ID: "f:first", ParentID: RootID, Name: "notes.txt", Kind: KindFile}
	aliasLikeName := collisionAlias(portableName(first.Name), first.Kind, first.ID, 1)
	source := newFakeDirectorySource(map[string][]SourceEntry{
		RootID: {
			first,
			{ID: "f:case", ParentID: RootID, Name: "NOTES.TXT", Kind: KindFile},
			{ID: "f:alias-like", ParentID: RootID, Name: aliasLikeName, Kind: KindFile},
		},
	})
	fs := mustNewFS(t, 42, source, &fakeContentOpener{})

	entries, err := fs.ReadDir(context.Background(), "/")
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		key := NameKey(entry.Name)
		if _, exists := seen[key]; exists {
			t.Fatalf("generated alias collided with an existing source name: %#v", entries)
		}
		seen[key] = struct{}{}
	}
}

func TestStatResolvesExportedPathsCaseInsensitively(t *testing.T) {
	t.Parallel()

	source := newFakeDirectorySource(map[string][]SourceEntry{
		RootID: {
			{ID: "d:docs-one", ParentID: RootID, Name: "Docs", Kind: KindDirectory},
			{ID: "d:docs-two", ParentID: RootID, Name: "DOCS", Kind: KindDirectory},
		},
		"d:docs-one": {
			{
				ID: "f:guide", ParentID: "d:docs-one", Name: "Guide.PDF",
				Kind: KindFile, Size: 1234, Encrypted: true,
				ContentHash: "sha256:guide-v7", Revision: 7, UploadUUID: "upload-guide-v7",
			},
		},
	})
	fs := mustNewFS(t, 99, source, &fakeContentOpener{})

	root, err := fs.Stat(context.Background(), "/")
	if err != nil {
		t.Fatalf("Stat(root) error = %v", err)
	}
	if root.ID != RootID || root.Kind != KindDirectory || root.ChannelID != 99 {
		t.Fatalf("Stat(root) = %#v", root)
	}

	rootEntries, err := fs.ReadDir(context.Background(), "/")
	if err != nil {
		t.Fatalf("ReadDir(root) error = %v", err)
	}
	docsName := entriesByID(rootEntries)["d:docs-one"].Name
	got, err := fs.Stat(context.Background(), "/"+strings.ToUpper(docsName)+"/guide.pdf")
	if err != nil {
		t.Fatalf("Stat(nested) error = %v", err)
	}
	if got.ID != "f:guide" || got.Name != "Guide.PDF" || got.SourceName != "Guide.PDF" {
		t.Fatalf("Stat(nested) = %#v", got)
	}
	if got.Size != 1234 || !got.Encrypted || got.ChannelID != 99 {
		t.Fatalf("Stat(nested) metadata = %#v", got)
	}
	if got.ContentHash != "sha256:guide-v7" || got.Revision != 7 || got.UploadUUID != "upload-guide-v7" {
		t.Fatalf("Stat(nested) immutable identity = %#v", got)
	}
}

func TestStatNormalizesMacOSDecomposedPathBeforeLengthValidation(t *testing.T) {
	t.Parallel()

	nfcName := strings.Repeat("é", 80) + ".txt"
	nfdName := strings.Repeat("é", 80) + ".txt"
	if len(nfdName) <= maxPortableNameBytes {
		t.Fatalf("test precondition: decomposed name is only %d bytes", len(nfdName))
	}
	source := newFakeDirectorySource(map[string][]SourceEntry{
		RootID: {
			{ID: "f:nfd-path", ParentID: RootID, Name: nfcName, Kind: KindFile},
		},
	})
	fs := mustNewFS(t, 42, source, &fakeContentOpener{})

	entry, err := fs.Stat(context.Background(), "/"+nfdName)
	if err != nil {
		t.Fatalf("Stat(NFD path) error = %v", err)
	}
	if entry.ID != "f:nfd-path" || entry.Name != nfcName {
		t.Fatalf("Stat(NFD path) = %#v", entry)
	}
}

func TestOperationsRejectUnsafePathsBeforeCallingSource(t *testing.T) {
	source := newFakeDirectorySource(nil)
	fs := mustNewFS(t, 42, source, &fakeContentOpener{})
	unsafePaths := []string{
		"relative/path",
		"/../secret",
		"/safe/../../secret",
		"/safe/./file",
		`/safe\file`,
		"/safe/nu\x00l",
		"//ambiguous",
		"/trailing/",
	}

	for _, unsafePath := range unsafePaths {
		unsafePath := unsafePath
		t.Run(unsafePath, func(t *testing.T) {
			_, err := fs.Stat(context.Background(), unsafePath)
			if !errors.Is(err, ErrInvalidPath) {
				t.Fatalf("Stat(%q) error = %v, want ErrInvalidPath", unsafePath, err)
			}
		})
	}

	if got := source.callCount(); got != 0 {
		t.Fatalf("directory source called %d times for invalid paths", got)
	}
}

func TestOperationsReturnTypedLookupErrors(t *testing.T) {
	t.Parallel()

	source := newFakeDirectorySource(map[string][]SourceEntry{
		RootID: {
			{ID: "f:plain", ParentID: RootID, Name: "plain.txt", Kind: KindFile},
			{ID: "d:empty", ParentID: RootID, Name: "empty", Kind: KindDirectory},
		},
	})
	fs := mustNewFS(t, 42, source, &fakeContentOpener{})

	if _, err := fs.Stat(context.Background(), "/missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Stat(missing) error = %v, want ErrNotFound", err)
	}
	if _, err := fs.Stat(context.Background(), "/plain.txt/child"); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("Stat(through-file) error = %v, want ErrNotDirectory", err)
	}
	if _, err := fs.ReadDir(context.Background(), "/plain.txt"); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("ReadDir(file) error = %v, want ErrNotDirectory", err)
	}
	if _, err := fs.Open(context.Background(), "/empty"); !errors.Is(err, ErrIsDirectory) {
		t.Fatalf("Open(directory) error = %v, want ErrIsDirectory", err)
	}
}

func TestReadDirRejectsMalformedProjectionEntries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		entries []SourceEntry
	}{
		{
			name: "empty id",
			entries: []SourceEntry{
				{ParentID: RootID, Name: "file", Kind: KindFile},
			},
		},
		{
			name: "duplicate id",
			entries: []SourceEntry{
				{ID: "f:1", ParentID: RootID, Name: "one", Kind: KindFile},
				{ID: "f:1", ParentID: RootID, Name: "two", Kind: KindFile},
			},
		},
		{
			name: "wrong parent",
			entries: []SourceEntry{
				{ID: "f:1", ParentID: "d:elsewhere", Name: "one", Kind: KindFile},
			},
		},
		{
			name: "unknown kind",
			entries: []SourceEntry{
				{ID: "f:1", ParentID: RootID, Name: "one", Kind: Kind("socket")},
			},
		},
		{
			name: "negative size",
			entries: []SourceEntry{
				{ID: "f:1", ParentID: RootID, Name: "one", Kind: KindFile, Size: -1},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fs := mustNewFS(t, 42, newFakeDirectorySource(map[string][]SourceEntry{RootID: test.entries}), &fakeContentOpener{})
			_, err := fs.ReadDir(context.Background(), "/")
			if !errors.Is(err, ErrInvalidEntry) {
				t.Fatalf("ReadDir() error = %v, want ErrInvalidEntry", err)
			}
		})
	}
}

func TestOpenProvidesContextAwareRandomAccessAndStableMetadata(t *testing.T) {
	t.Parallel()

	modTime := time.Date(2026, time.August, 19, 10, 30, 0, 0, time.UTC)
	sourceEntry := SourceEntry{
		ID:         "f:manual",
		ParentID:   RootID,
		Name:       "Manual.txt",
		Kind:       KindFile,
		Size:       10,
		ModTime:    modTime,
		Encrypted:  true,
		ContentRef: "telegram:777",
	}
	source := newFakeDirectorySource(map[string][]SourceEntry{RootID: {sourceEntry}})
	content := &memoryContent{data: []byte("0123456789")}
	opener := &fakeContentOpener{content: content}
	fs := mustNewFS(t, 7331, source, opener)

	file, err := fs.Open(context.Background(), "/manual.TXT")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if got := file.Entry(); got.ID != sourceEntry.ID || got.SourceName != sourceEntry.Name || got.ContentRef != sourceEntry.ContentRef || !got.ModTime.Equal(modTime) {
		t.Fatalf("File.Entry() = %#v", got)
	}
	if opener.channelID != 7331 || opener.entry != sourceEntry {
		t.Fatalf("OpenContent() got channel=%d entry=%#v", opener.channelID, opener.entry)
	}

	buffer := make([]byte, 4)
	n, err := file.ReadAt(context.Background(), buffer, 3)
	if err != nil || n != 4 || string(buffer) != "3456" {
		t.Fatalf("ReadAt() = (%d, %v, %q), want (4, nil, 3456)", n, err, buffer)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := file.ReadAt(cancelled, buffer, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadAt(cancelled) error = %v, want context.Canceled", err)
	}
	if _, err := file.ReadAt(context.Background(), buffer, -1); !errors.Is(err, ErrInvalidOffset) {
		t.Fatalf("ReadAt(negative offset) error = %v, want ErrInvalidOffset", err)
	}

	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("second Close() error = %v, want idempotent close", err)
	}
	if got := content.closeCalls; got != 1 {
		t.Fatalf("underlying Close() calls = %d, want 1", got)
	}
	if _, err := file.ReadAt(context.Background(), buffer, 0); !errors.Is(err, ErrClosed) {
		t.Fatalf("ReadAt(after close) error = %v, want ErrClosed", err)
	}
}

func TestOperationsPropagateContextCancellation(t *testing.T) {
	t.Parallel()

	source := newFakeDirectorySource(nil)
	fs := mustNewFS(t, 42, source, &fakeContentOpener{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := fs.ReadDir(ctx, "/"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadDir(cancelled) error = %v, want context.Canceled", err)
	}
}

func TestReturnedEntriesAreIndependentCopies(t *testing.T) {
	t.Parallel()

	source := newFakeDirectorySource(map[string][]SourceEntry{
		RootID: {
			{ID: "f:immutable", ParentID: RootID, Name: "Immutable.txt", Kind: KindFile, Size: 9},
		},
	})
	fs := mustNewFS(t, 42, source, &fakeContentOpener{})

	entries, err := fs.ReadDir(context.Background(), "/")
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	entries[0].Name = "mutated"
	entries[0].Size = 999

	entry, err := fs.Stat(context.Background(), "/immutable.txt")
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if entry.Name != "Immutable.txt" || entry.Size != 9 {
		t.Fatalf("Stat() observed caller mutation: %#v", entry)
	}
}

func entriesByID(entries []Entry) map[string]Entry {
	result := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		result[entry.ID] = entry
	}
	return result
}

func mustNewFS(t *testing.T, channelID int64, source DirectorySource, opener ContentOpener) *FS {
	t.Helper()

	fs, err := New(channelID, source, opener)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return fs
}

type fakeDirectorySource struct {
	mu                    sync.Mutex
	entries               map[string][]SourceEntry
	calls                 int
	reverseEveryOtherCall bool
}

func newFakeDirectorySource(entries map[string][]SourceEntry) *fakeDirectorySource {
	return &fakeDirectorySource{entries: entries}
}

func (s *fakeDirectorySource) ListDirectory(ctx context.Context, channelID int64, parentID string) ([]SourceEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	entries := slices.Clone(s.entries[parentID])
	if s.reverseEveryOtherCall && s.calls%2 == 0 {
		slices.Reverse(entries)
	}
	return entries, nil
}

func (s *fakeDirectorySource) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type fakeContentOpener struct {
	mu        sync.Mutex
	channelID int64
	entry     SourceEntry
	content   RandomAccessContent
	err       error
}

func (o *fakeContentOpener) OpenContent(ctx context.Context, channelID int64, entry SourceEntry) (RandomAccessContent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	o.channelID = channelID
	o.entry = entry
	if o.err != nil {
		return nil, o.err
	}
	if o.content == nil {
		return &memoryContent{}, nil
	}
	return o.content, nil
}

type memoryContent struct {
	mu         sync.Mutex
	data       []byte
	closed     bool
	closeCalls int
}

func (c *memoryContent) ReadAt(ctx context.Context, buffer []byte, offset int64) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, ErrClosed
	}
	if offset >= int64(len(c.data)) {
		return 0, io.EOF
	}
	n := copy(buffer, c.data[offset:])
	if n < len(buffer) {
		return n, io.EOF
	}
	return n, nil
}

func (c *memoryContent) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeCalls++
	c.closed = true
	return nil
}
