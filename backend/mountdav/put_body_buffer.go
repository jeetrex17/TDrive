package mountdav

import (
	"io"
	"net/http"
	"os"
)

// maxBufferedPUTBodyBytes bounds how large an unknown-length PUT body may be
// while its real length is being determined. Matches defaultMaxResumeObjectBytes
// (resume.go) for consistency: both exist to learn an object's true size
// before committing it, so both cap a single object the same way. The
// coordinator/staging layer enforces the real, configured per-drive quota
// independently once the length is known -- this is only a sanity ceiling on
// how much scratch disk one buffering PUT may consume.
const maxBufferedPUTBodyBytes = 8 << 30

// bufferUnknownLengthPUTBody copies an unknown-length (chunked
// Transfer-Encoding) PUT body to a bounded temp file and reports its real
// size, so a caller that needs ContentLength known upfront -- see
// Session.Put's encrypted-write requirement -- can proceed exactly as if the
// client had sent a Content-Length header.
//
// Observed in practice, not hypothetical: macOS Finder's copy engine sends
// the real-content PUT of its two-step create-then-write sequence via
// chunked Transfer-Encoding (no Content-Length at all) for at least some
// writes, which Go's net/http reports as Request.ContentLength == -1.
// TDE1's encrypted stream header embeds the plaintext size and cannot be
// back-filled after streaming starts, so encrypted writes hard-require a
// known length; buffering first is what makes that requirement compatible
// with a real client that does not always provide one.
//
// The caller owns the returned file and must close and remove it exactly
// once (see closeAndRemoveTempFile), regardless of what it does with the
// content.
func bufferUnknownLengthPUTBody(response http.ResponseWriter, body io.ReadCloser) (*os.File, int64, error) {
	file, err := os.CreateTemp("", "tdrive-mountdav-put-*")
	if err != nil {
		return nil, 0, err
	}
	limited := http.MaxBytesReader(response, body, maxBufferedPUTBodyBytes)
	written, copyErr := io.Copy(file, limited)
	if copyErr != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, 0, copyErr
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, 0, err
	}
	return file, written, nil
}

func closeAndRemoveTempFile(file *os.File) {
	if file == nil {
		return
	}
	name := file.Name()
	_ = file.Close()
	_ = os.Remove(name)
}
