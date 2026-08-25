// Package mountwrite coordinates protocol-neutral mutations for a writable
// TDrive mount.
//
// A content write is staged and hashed locally, uploaded as hidden Telegram
// data, and published by one idempotent remote commit. A successful Commit
// call is the visibility boundary: failures before it must never publish, and
// failures after it are recorded for projection or cleanup recovery. The
// package deliberately contains no WebDAV, projection, or Telegram client
// implementation; those concerns are supplied through narrow interfaces.
package mountwrite
