package mountdav

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"

	"TDrive/backend/mountfs"
)

type randomAccessFile struct {
	mu     sync.Mutex
	ctx    context.Context
	file   *mountfs.File
	info   fileInfo
	offset int64
	closed bool
}

func newRandomAccessFile(ctx context.Context, file *mountfs.File, info fileInfo) *randomAccessFile {
	return &randomAccessFile{ctx: ctx, file: file, info: info}
}

func (file *randomAccessFile) Close() error {
	if file == nil {
		return nil
	}
	file.mu.Lock()
	defer file.mu.Unlock()
	if file.closed {
		return nil
	}
	file.closed = true
	return file.file.Close()
}

func (file *randomAccessFile) Read(buffer []byte) (int, error) {
	file.mu.Lock()
	defer file.mu.Unlock()
	if file.closed {
		return 0, os.ErrClosed
	}
	if len(buffer) == 0 {
		return 0, nil
	}
	remaining := file.info.Size() - file.offset
	if remaining <= 0 {
		return 0, io.EOF
	}
	readBuffer := buffer
	if int64(len(readBuffer)) > remaining {
		readBuffer = readBuffer[:remaining]
	}
	n, err := file.file.ReadAt(file.ctx, readBuffer, file.offset)
	if n < 0 || n > len(readBuffer) {
		return 0, errors.New("mountdav: invalid content read count")
	}
	file.offset += int64(n)
	if err == nil && n < len(readBuffer) {
		err = io.ErrNoProgress
	}
	if err == nil && len(readBuffer) < len(buffer) {
		err = io.EOF
	}
	return n, err
}

func (file *randomAccessFile) Seek(offset int64, whence int) (int64, error) {
	file.mu.Lock()
	defer file.mu.Unlock()
	if file.closed {
		return 0, os.ErrClosed
	}
	next := offset
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		next = file.offset + offset
	case io.SeekEnd:
		next = file.info.Size() + offset
	default:
		return 0, os.ErrInvalid
	}
	if next < 0 {
		return 0, os.ErrInvalid
	}
	file.offset = next
	return next, nil
}

func (*randomAccessFile) Readdir(int) ([]os.FileInfo, error) {
	return nil, os.ErrInvalid
}

func (file *randomAccessFile) Stat() (os.FileInfo, error) {
	if file == nil {
		return nil, os.ErrInvalid
	}
	return file.info, nil
}

func (*randomAccessFile) Write([]byte) (int, error) {
	return 0, os.ErrPermission
}

type directoryFile struct {
	mu       sync.Mutex
	info     fileInfo
	children []os.FileInfo
	offset   int
	closed   bool
}

func newDirectoryFile(info fileInfo, children []os.FileInfo) *directoryFile {
	return &directoryFile{info: info, children: children}
}

func (file *directoryFile) Close() error {
	file.mu.Lock()
	defer file.mu.Unlock()
	file.closed = true
	return nil
}

func (*directoryFile) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (file *directoryFile) Seek(offset int64, whence int) (int64, error) {
	file.mu.Lock()
	defer file.mu.Unlock()
	if file.closed {
		return 0, os.ErrClosed
	}
	if whence != io.SeekStart || offset != 0 {
		return 0, os.ErrInvalid
	}
	file.offset = 0
	return 0, nil
}

func (file *directoryFile) Readdir(count int) ([]os.FileInfo, error) {
	file.mu.Lock()
	defer file.mu.Unlock()
	if file.closed {
		return nil, os.ErrClosed
	}
	if count <= 0 {
		result := append([]os.FileInfo(nil), file.children[file.offset:]...)
		file.offset = len(file.children)
		return result, nil
	}
	if file.offset >= len(file.children) {
		return nil, io.EOF
	}
	end := min(file.offset+count, len(file.children))
	result := append([]os.FileInfo(nil), file.children[file.offset:end]...)
	file.offset = end
	return result, nil
}

func (file *directoryFile) Stat() (os.FileInfo, error) {
	if file == nil {
		return nil, os.ErrInvalid
	}
	return file.info, nil
}

func (*directoryFile) Write([]byte) (int, error) {
	return 0, os.ErrPermission
}

type metadataFile struct {
	*metadataReadSeeker
	info fileInfo
}

func newMetadataFile(info fileInfo) *metadataFile {
	return &metadataFile{
		metadataReadSeeker: &metadataReadSeeker{size: info.Size()},
		info:               info,
	}
}

func (*metadataFile) Close() error {
	return nil
}

func (*metadataFile) Readdir(int) ([]os.FileInfo, error) {
	return nil, os.ErrInvalid
}

func (file *metadataFile) Stat() (os.FileInfo, error) {
	return file.info, nil
}

func (*metadataFile) Write([]byte) (int, error) {
	return 0, os.ErrPermission
}
