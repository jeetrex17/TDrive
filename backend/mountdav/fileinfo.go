package mountdav

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"mime"
	"os"
	"path/filepath"
	"time"

	"TDrive/backend/mountfs"
)

type fileInfo struct {
	entry mountfs.Entry
}

const resourceETagDomain = "tdrive.mount.resource-etag.v1\x00"

func newFileInfo(entry mountfs.Entry) fileInfo {
	return fileInfo{entry: entry}
}

func (info fileInfo) Name() string {
	if info.entry.ID == mountfs.RootID && info.entry.Name == "" {
		return "."
	}
	return info.entry.Name
}

func (info fileInfo) Size() int64 {
	if info.IsDir() {
		return 0
	}
	return info.entry.Size
}

func (info fileInfo) Mode() os.FileMode {
	if info.IsDir() {
		return os.ModeDir | 0o555
	}
	return 0o444
}

func (info fileInfo) ModTime() time.Time {
	if info.entry.ModTime.IsZero() {
		return time.Unix(0, 0).UTC()
	}
	return info.entry.ModTime
}

func (info fileInfo) IsDir() bool {
	return info.entry.Kind == mountfs.KindDirectory
}

func (info fileInfo) Sys() any {
	return nil
}

func (info fileInfo) ContentType(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if info.IsDir() {
		return "httpd/unix-directory", nil
	}
	if contentType := mime.TypeByExtension(filepath.Ext(info.entry.Name)); contentType != "" {
		return contentType, nil
	}
	return "application/octet-stream", nil
}

func (info fileInfo) ETag(ctx context.Context) (string, error) {
	return EntryETag(ctx, info.entry)
}

// EntryETag returns the strong entity tag used by WebDAV responses for one
// projected mount entry. Writable adapters use the same function when checking
// OS-provided preconditions before building a mutation.
func EntryETag(ctx context.Context, entry mountfs.Entry) (string, error) {
	return ResourceETag(ctx, entry.ChannelID, entry.ID, entry.Revision, entry.ContentHash)
}

// ResourceETag returns a domain-separated strong entity tag from the stable
// projection identity of a mounted resource. It intentionally excludes private
// Telegram references and mutable display metadata; revision is authoritative.
func ResourceETag(ctx context.Context, channelID int64, objectID string, revision int64, contentHash string) (string, error) {
	if ctx == nil {
		return "", errors.New("mountdav: nil context")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	digest := sha256.New()
	_, _ = digest.Write([]byte(resourceETagDomain))
	writeETagInt64(digest, channelID)
	writeETagField(digest, objectID)
	writeETagInt64(digest, revision)
	writeETagField(digest, contentHash)
	return `"tdrive-` + hex.EncodeToString(digest.Sum(nil)) + `"`, nil
}

func writeETagInt64(digest hash.Hash, value int64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value))
	_, _ = digest.Write(encoded[:])
}

func writeETagField(digest hash.Hash, value string) {
	writeETagInt64(digest, int64(len(value)))
	_, _ = digest.Write([]byte(value))
}
