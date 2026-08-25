package mountdav

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"time"

	"TDrive/backend/mountfs"
)

type fileInfo struct {
	entry mountfs.Entry
}

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
	if err := ctx.Err(); err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = fmt.Fprintf(
		hash,
		"%d\x00%s\x00%s\x00%d\x00%d\x00%t",
		info.entry.ChannelID,
		info.entry.ID,
		info.entry.ContentRef,
		info.Size(),
		info.ModTime().UnixNano(),
		info.entry.Encrypted,
	)
	return `"` + hex.EncodeToString(hash.Sum(nil)) + `"`, nil
}
