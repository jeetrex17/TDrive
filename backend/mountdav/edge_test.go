package mountdav

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"TDrive/backend/mountfs"
)

func TestRandomAccessFileEdgeContracts(t *testing.T) {
	fs := testFS(t, &memoryOpener{data: []byte("0123456789")})
	file, err := fs.OpenFile(context.Background(), "/Docs/note.txt", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if info, err := file.Stat(); err != nil || info.Name() != "note.txt" {
		t.Fatalf("Stat = (%v, %v)", info, err)
	}
	if n, err := file.Read(nil); n != 0 || err != nil {
		t.Fatalf("empty Read = (%d, %v)", n, err)
	}
	if offset, err := file.Seek(3, io.SeekStart); err != nil || offset != 3 {
		t.Fatalf("SeekStart = (%d, %v)", offset, err)
	}
	if offset, err := file.Seek(2, io.SeekCurrent); err != nil || offset != 5 {
		t.Fatalf("SeekCurrent = (%d, %v)", offset, err)
	}
	if offset, err := file.Seek(-1, io.SeekEnd); err != nil || offset != 9 {
		t.Fatalf("SeekEnd = (%d, %v)", offset, err)
	}
	buffer := make([]byte, 2)
	if n, err := file.Read(buffer); n != 1 || !errors.Is(err, io.EOF) || string(buffer[:n]) != "9" {
		t.Fatalf("terminal Read = (%d, %v, %q)", n, err, buffer[:n])
	}
	if n, err := file.Read(buffer); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("past-end Read = (%d, %v)", n, err)
	}
	if _, err := file.Seek(-1, io.SeekStart); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("negative Seek error = %v", err)
	}
	if _, err := file.Seek(0, 99); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("invalid-whence Seek error = %v", err)
	}
	if _, err := file.Readdir(1); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("file Readdir error = %v", err)
	}
	if _, err := file.Write([]byte("x")); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("file Write error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := file.Read(buffer); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Read after Close error = %v", err)
	}
	if _, err := file.Seek(0, io.SeekStart); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Seek after Close error = %v", err)
	}

	var nilFile *randomAccessFile
	if err := nilFile.Close(); err != nil {
		t.Fatalf("nil Close: %v", err)
	}
	if _, err := nilFile.Stat(); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("nil Stat error = %v", err)
	}
}

func TestDirectoryFileEdgeContracts(t *testing.T) {
	fs := testFS(t, nil)
	directory, err := fs.OpenFile(context.Background(), "/", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile(root): %v", err)
	}
	if info, err := directory.Stat(); err != nil || !info.IsDir() || info.Mode() != os.ModeDir|0o555 {
		t.Fatalf("directory Stat = (%v, %v)", info, err)
	}
	if n, err := directory.Read(make([]byte, 1)); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("directory Read = (%d, %v)", n, err)
	}
	if _, err := directory.Write([]byte("x")); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("directory Write error = %v", err)
	}
	if _, err := directory.Seek(1, io.SeekStart); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("directory invalid Seek error = %v", err)
	}
	entries, err := directory.Readdir(1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("Readdir(1) = (%v, %v)", entries, err)
	}
	if _, err := directory.Readdir(1); !errors.Is(err, io.EOF) {
		t.Fatalf("exhausted Readdir error = %v", err)
	}
	if _, err := directory.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("directory rewind: %v", err)
	}
	if entries, err := directory.Readdir(0); err != nil || len(entries) != 1 {
		t.Fatalf("Readdir(all) = (%v, %v)", entries, err)
	}
	if err := directory.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := directory.Readdir(1); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Readdir after Close error = %v", err)
	}
	if _, err := directory.Seek(0, io.SeekStart); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Seek after Close error = %v", err)
	}

	var nilDirectory *directoryFile
	if _, err := nilDirectory.Stat(); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("nil directory Stat error = %v", err)
	}
}

func TestFileSystemPathAndErrorMapping(t *testing.T) {
	fs := testFS(t, nil)
	if _, err := NewFileSystem(nil).Stat(context.Background(), "/"); err == nil {
		t.Fatal("nil filesystem Stat unexpectedly succeeded")
	}
	if _, err := NewFileSystem(nil).OpenFile(context.Background(), "/", os.O_RDONLY, 0); err == nil {
		t.Fatal("nil filesystem OpenFile unexpectedly succeeded")
	}
	if _, err := fs.Stat(context.Background(), "/missing"); !os.IsNotExist(err) {
		t.Fatalf("missing Stat error = %v", err)
	}
	for _, name := range []string{"relative", "/../escape", "/Docs/./note.txt", "/Docs//note.txt", `/Docs\note.txt`, "/nul\x00name", string([]byte{'/', 0xff})} {
		if _, err := fs.Stat(context.Background(), name); !errors.Is(err, os.ErrInvalid) {
			t.Errorf("Stat(%q) error = %v, want os.ErrInvalid", name, err)
		}
	}
	if _, err := fs.Stat(context.Background(), "/Docs/"); err != nil {
		t.Fatalf("directory trailing slash: %v", err)
	}
	if clean, err := cleanWebDAVName(""); err != nil || clean != "/" {
		t.Fatalf("clean empty = (%q, %v)", clean, err)
	}

	for _, test := range []struct {
		err  error
		want error
	}{
		{err: mountfs.ErrNotFound, want: os.ErrNotExist},
		{err: mountfs.ErrNotDirectory, want: os.ErrNotExist},
		{err: mountfs.ErrInvalidPath, want: os.ErrInvalid},
		{err: mountfs.ErrIsDirectory, want: os.ErrInvalid},
		{err: mountfs.ErrContentUnavailable, want: mountfs.ErrContentUnavailable},
	} {
		if got := mapMountFSError("test", "/x", test.err); !errors.Is(got, test.want) {
			t.Errorf("mapMountFSError(%v) = %v, want %v", test.err, got, test.want)
		}
	}
	if mapMountFSError("test", "/x", nil) != nil {
		t.Fatal("mapping nil error returned non-nil")
	}
}

func TestFileInfoStableMetadata(t *testing.T) {
	modTime := time.Date(2026, time.August, 19, 1, 2, 3, 4, time.UTC)
	entry := mountfs.Entry{ChannelID: 7, ID: "f:1", Name: "blob.unknown-extension", Kind: mountfs.KindFile, Size: 8, ModTime: modTime, ContentRef: "telegram:1", Revision: 1, ContentHash: "sha256:one"}
	info := newFileInfo(entry)
	if info.Name() != entry.Name || info.Size() != 8 || info.Mode() != 0o444 || info.IsDir() || info.Sys() != nil || !info.ModTime().Equal(modTime) {
		t.Fatalf("fileInfo = %+v", info)
	}
	if contentType, err := info.ContentType(context.Background()); err != nil || contentType != "application/octet-stream" {
		t.Fatalf("ContentType = (%q, %v)", contentType, err)
	}
	firstETag, err := info.ETag(context.Background())
	if err != nil {
		t.Fatalf("ETag: %v", err)
	}
	entry.ContentRef = "telegram:2"
	secondETag, _ := newFileInfo(entry).ETag(context.Background())
	if firstETag != secondETag {
		t.Fatal("ETag changed after private content reference changed")
	}
	entry.ContentHash = "sha256:two"
	entry.Revision = 2
	thirdETag, _ := newFileInfo(entry).ETag(context.Background())
	if firstETag == thirdETag {
		t.Fatal("ETag did not change with immutable content identity")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := info.ContentType(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ContentType error = %v", err)
	}
	if _, err := info.ETag(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ETag error = %v", err)
	}

	root := newFileInfo(mountfs.Entry{Kind: mountfs.KindDirectory})
	if root.Name() != "." || root.Size() != 0 || root.Mode() != os.ModeDir|0o555 || !root.ModTime().Equal(time.Unix(0, 0).UTC()) {
		t.Fatalf("root info = %+v", root)
	}
	if contentType, err := root.ContentType(context.Background()); err != nil || contentType != "httpd/unix-directory" {
		t.Fatalf("directory ContentType = (%q, %v)", contentType, err)
	}
}

func TestServingErrorMappingAndMetadataSeeker(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
	}{
		{err: os.ErrNotExist, status: http.StatusNotFound},
		{err: os.ErrPermission, status: http.StatusForbidden},
		{err: mountfs.ErrAccessDenied, status: http.StatusForbidden},
		{err: mountfs.ErrContentUnavailable, status: http.StatusServiceUnavailable},
		{err: os.ErrInvalid, status: http.StatusBadRequest},
		{err: context.Canceled, status: http.StatusRequestTimeout},
		{err: errors.New("boom"), status: http.StatusInternalServerError},
	} {
		recorder := httptest.NewRecorder()
		serveFileError(recorder, test.err)
		if recorder.Code != test.status {
			t.Errorf("serveFileError(%v) = %d, want %d", test.err, recorder.Code, test.status)
		}
	}

	content := &metadataReadSeeker{size: 3}
	if offset, err := content.Seek(1, io.SeekStart); err != nil || offset != 1 {
		t.Fatalf("metadata SeekStart = (%d, %v)", offset, err)
	}
	buffer := []byte{1, 1, 1}
	if n, err := content.Read(buffer); n != 2 || !errors.Is(err, io.EOF) || buffer[0] != 0 || buffer[1] != 0 {
		t.Fatalf("metadata Read = (%d, %v, %v)", n, err, buffer)
	}
	if _, err := content.Seek(-1, io.SeekStart); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("metadata negative Seek error = %v", err)
	}
	if _, err := content.Seek(0, 99); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("metadata invalid Seek error = %v", err)
	}
	if offset, err := content.Seek(-1, io.SeekEnd); err != nil || offset != 2 {
		t.Fatalf("metadata SeekEnd = (%d, %v)", offset, err)
	}
	if offset, err := content.Seek(1, io.SeekCurrent); err != nil || offset != 3 {
		t.Fatalf("metadata SeekCurrent = (%d, %v)", offset, err)
	}
}
