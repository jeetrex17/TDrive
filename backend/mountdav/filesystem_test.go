package mountdav

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"TDrive/backend/mountfs"

	"golang.org/x/net/webdav"
)

type memorySource map[string][]mountfs.SourceEntry

func (s memorySource) ListDirectory(ctx context.Context, channelID int64, parentID string) ([]mountfs.SourceEntry, error) {
	entries := s[parentID]
	out := make([]mountfs.SourceEntry, len(entries))
	copy(out, entries)
	return out, nil
}

type memoryOpener struct {
	data  []byte
	calls int
}

func (o *memoryOpener) OpenContent(ctx context.Context, channelID int64, entry mountfs.SourceEntry) (mountfs.RandomAccessContent, error) {
	o.calls++
	return &memoryContent{data: o.data}, nil
}

type memoryContent struct {
	data []byte
}

func (c *memoryContent) ReadAt(ctx context.Context, buffer []byte, offset int64) (int, error) {
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
	return nil
}

func testFS(t *testing.T, opener *memoryOpener) *FileSystem {
	t.Helper()
	if opener == nil {
		opener = &memoryOpener{data: []byte("hello")}
	}
	modTime := time.Unix(1700000000, 0)
	mfs, err := mountfs.New(42, memorySource{
		mountfs.RootID: {
			{ID: "d:docs", ParentID: mountfs.RootID, Name: "Docs", Kind: mountfs.KindDirectory, ModTime: modTime},
		},
		"d:docs": {
			{ID: "f:42", ParentID: "d:docs", Name: "note.txt", Kind: mountfs.KindFile, Size: int64(len(opener.data)), ModTime: modTime, ContentRef: "42"},
		},
	}, opener)
	if err != nil {
		t.Fatalf("mountfs.New: %v", err)
	}
	return NewFileSystem(mfs)
}

func TestFileSystemRejectsMutations(t *testing.T) {
	fs := testFS(t, nil)
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/New", 0o755); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("Mkdir error = %v, want permission", err)
	}
	if err := fs.RemoveAll(ctx, "/Docs/note.txt"); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("RemoveAll error = %v, want permission", err)
	}
	if err := fs.Rename(ctx, "/Docs/note.txt", "/Docs/renamed.txt"); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("Rename error = %v, want permission", err)
	}
	if _, err := fs.OpenFile(ctx, "/Docs/new.txt", os.O_CREATE|os.O_WRONLY, 0o644); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("OpenFile create error = %v, want permission", err)
	}
}

func TestFileSystemListsDirectories(t *testing.T) {
	fs := testFS(t, nil)
	f, err := fs.OpenFile(context.Background(), "/", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile root: %v", err)
	}
	defer f.Close()

	entries, err := f.Readdir(-1)
	if err != nil {
		t.Fatalf("Readdir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "Docs" || !entries[0].IsDir() {
		t.Fatalf("entries = %+v, want Docs directory", entries)
	}
}

func TestFileSystemStreamsRandomAccessReads(t *testing.T) {
	opener := &memoryOpener{data: []byte("streamed bytes")}
	fs := testFS(t, opener)

	file, err := fs.OpenFile(context.Background(), "/Docs/note.txt", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer file.Close()

	if _, err := file.Seek(9, io.SeekStart); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	got := make([]byte, 5)
	n, err := file.Read(got)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if n != 5 || string(got) != "bytes" {
		t.Fatalf("Read = %d/%q, want bytes", n, got)
	}
	if opener.calls != 1 {
		t.Fatalf("open calls = %d, want 1", opener.calls)
	}
}

func TestWebDAVHandlerIsReadOnly(t *testing.T) {
	fs := testFS(t, &memoryOpener{data: []byte("hello webdav")})
	handler := &webdav.Handler{
		Prefix:     "/dav",
		FileSystem: fs,
		LockSystem: webdav.NewMemLS(),
	}

	getReq := httptest.NewRequest(http.MethodGet, "/dav/Docs/note.txt", nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body=%q", getRec.Code, getRec.Body.String())
	}
	if getRec.Body.String() != "hello webdav" {
		t.Fatalf("GET body = %q", getRec.Body.String())
	}

	putReq := httptest.NewRequest(http.MethodPut, "/dav/Docs/new.txt", bytes.NewBufferString("write"))
	putRec := httptest.NewRecorder()
	handler.ServeHTTP(putRec, putReq)
	if putRec.Code < 400 {
		t.Fatalf("PUT status = %d, want read-only failure", putRec.Code)
	}
}
