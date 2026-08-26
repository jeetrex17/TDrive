package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

// ErrChecksumMismatch means the payload on disk does not match the digest the
// release published for it. The file is discarded; nothing unverified is ever
// left at a final payload path.
var ErrChecksumMismatch = errors.New("payload failed checksum verification")

// errDownloadStalled is raised when no bytes arrive for downloadIdleTimeout.
var errDownloadStalled = errors.New("download stalled")

const (
	downloadIdleTimeout  = 60 * time.Second
	downloadBufferSize   = 256 * 1024
	smallAssetLimitBytes = 256 * 1024
	partSuffix           = ".part"
)

// fetchSmall GETs a small text asset (the checksum manifest) with a hard size cap.
func fetchSmall(ctx context.Context, client *http.Client, userAgent, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, githubTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, smallAssetLimitBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > smallAssetLimitBytes {
		return nil, fmt.Errorf("fetch %s: response larger than %d bytes", url, smallAssetLimitBytes)
	}
	return body, nil
}

// downloadFile streams url into dest. The bytes land in dest+".part" and are
// only renamed into place once both the size and the SHA-256 digest match, so
// a file that exists at dest has always been verified. progress receives
// cumulative byte counts; total is 0 when the server did not announce one.
func downloadFile(ctx context.Context, client *http.Client, userAgent, url, dest string, wantSize int64, wantSHA string, progress func(done, total int64)) (err error) {
	if wantSHA == "" {
		return errors.New("download: no expected checksum")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: status %d", resp.StatusCode)
	}
	total := resp.ContentLength
	if wantSize > 0 && total > 0 && total != wantSize {
		return fmt.Errorf("download: server reports %d bytes, release lists %d", total, wantSize)
	}
	if total <= 0 {
		total = wantSize
	}

	part := dest + partSuffix
	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(part)
		}
	}()

	var stalled atomic.Bool
	body := newIdleReader(resp.Body, downloadIdleTimeout, func() {
		stalled.Store(true)
		cancel()
	})
	defer body.stop()

	hasher := sha256.New()
	writer := io.MultiWriter(f, hasher)
	buf := make([]byte, downloadBufferSize)
	var done int64
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			if _, werr := writer.Write(buf[:n]); werr != nil {
				return werr
			}
			done += int64(n)
			if progress != nil {
				progress(done, total)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			if stalled.Load() {
				return errDownloadStalled
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return readErr
		}
	}

	if wantSize > 0 && done != wantSize {
		return fmt.Errorf("download: got %d bytes, expected %d", done, wantSize)
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if sum := hex.EncodeToString(hasher.Sum(nil)); sum != wantSHA {
		_ = os.Remove(part)
		return ErrChecksumMismatch
	}
	if err := os.Rename(part, dest); err != nil {
		_ = os.Remove(part)
		return err
	}
	return nil
}

// verifyFile re-checks a payload left in the cache by an earlier session.
func verifyFile(path string, wantSize int64, wantSHA string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("verify: %s is not a regular file", path)
	}
	if wantSize > 0 && info.Size() != wantSize {
		return fmt.Errorf("verify: size %d, expected %d", info.Size(), wantSize)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return err
	}
	if hex.EncodeToString(hasher.Sum(nil)) != wantSHA {
		return ErrChecksumMismatch
	}
	return nil
}

// idleReader aborts a transfer that stops delivering bytes. Each successful
// Read pushes the deadline back; a server that hangs mid-body would otherwise
// leave the download "in progress" forever, since http.Client.Timeout is
// unsuitable for multi-minute bodies.
type idleReader struct {
	r     io.Reader
	timer *time.Timer
	idle  time.Duration
}

func newIdleReader(r io.Reader, idle time.Duration, onIdle func()) *idleReader {
	return &idleReader{r: r, idle: idle, timer: time.AfterFunc(idle, onIdle)}
}

func (ir *idleReader) Read(p []byte) (int, error) {
	n, err := ir.r.Read(p)
	if n > 0 {
		ir.timer.Reset(ir.idle)
	}
	return n, err
}

func (ir *idleReader) stop() {
	ir.timer.Stop()
}
