package mountfs

import "errors"

var (
	ErrInvalidConfiguration = errors.New("mountfs: invalid configuration")
	ErrInvalidPath          = errors.New("mountfs: invalid path")
	ErrInvalidName          = errors.New("mountfs: invalid name")
	ErrInvalidEntry         = errors.New("mountfs: invalid source entry")
	ErrNotFound             = errors.New("mountfs: entry not found")
	ErrNotDirectory         = errors.New("mountfs: not a directory")
	ErrIsDirectory          = errors.New("mountfs: is a directory")
	// ErrAccessDenied is a permanent authorization failure. Protocol adapters
	// should distinguish it from ErrContentUnavailable (for example, 403 vs 503).
	ErrAccessDenied = errors.New("mountfs: access denied")
	// ErrContentUnavailable represents content that cannot currently be served.
	ErrContentUnavailable = errors.New("mountfs: content unavailable")
	ErrInvalidOffset      = errors.New("mountfs: invalid read offset")
	ErrClosed             = errors.New("mountfs: file is closed")
)
