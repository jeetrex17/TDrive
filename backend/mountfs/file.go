package mountfs

import (
	"context"
	"fmt"
	"sync"
)

// File is an opened immutable content revision. Close is idempotent and waits
// for in-flight reads before closing the underlying content handle.
type File struct {
	mu       sync.RWMutex
	entry    Entry
	content  RandomAccessContent
	closed   bool
	closeErr error
}

// Entry returns a value copy of the file metadata pinned when it was opened.
func (file *File) Entry() Entry {
	if file == nil {
		return Entry{}
	}
	return file.entry
}

// ReadAt reads from an absolute offset in the immutable content revision.
func (file *File) ReadAt(ctx context.Context, buffer []byte, offset int64) (int, error) {
	if file == nil {
		return 0, ErrClosed
	}
	if ctx == nil {
		return 0, fmt.Errorf("%w: context is nil", ErrInvalidConfiguration)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if offset < 0 {
		return 0, ErrInvalidOffset
	}

	file.mu.RLock()
	defer file.mu.RUnlock()
	if file.closed || file.content == nil {
		return 0, ErrClosed
	}
	return file.content.ReadAt(ctx, buffer, offset)
}

// Close releases the underlying immutable content handle exactly once.
func (file *File) Close() error {
	if file == nil {
		return nil
	}
	file.mu.Lock()
	defer file.mu.Unlock()
	if file.closed {
		return file.closeErr
	}
	file.closed = true
	if file.content != nil {
		file.closeErr = file.content.Close()
	}
	return file.closeErr
}
