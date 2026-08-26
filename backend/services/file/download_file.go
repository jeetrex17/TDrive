package file

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

type downloadProgressFunc func(done, total int64)

type downloadDiskError struct {
	err error
}

func (e downloadDiskError) Error() string { return e.err.Error() }
func (e downloadDiskError) Unwrap() error { return e.err }

// downloadProjectedFileToPath streams one immutable projected file revision to
// destination. It uses retry-safe random-access writes, bounds encrypted
// multipart staging to one part, and only replaces destination after the full
// output has been verified and closed.
func (s *Service) downloadProjectedFileToPath(
	ctx context.Context,
	peer tgclient.InputPeer,
	file projection.DownloadFile,
	destination string,
	masterKey []byte,
	progress downloadProgressFunc,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(destination) == "" {
		return fmt.Errorf("Disk Error: destination path is empty")
	}

	finalTmp, err := os.CreateTemp(filepath.Dir(destination), ".tdrive-download-*")
	if err != nil {
		return fmt.Errorf("Disk Error: %w", err)
	}
	finalTmpPath := finalTmp.Name()
	committed := false
	defer func() {
		_ = finalTmp.Close()
		if !committed {
			_ = os.Remove(finalTmpPath)
		}
	}()

	switch {
	case len(file.Parts) == 0 && !file.Encrypted:
		err = s.downloadSinglePlain(ctx, peer, file, finalTmp, progress)
	case len(file.Parts) == 0 && file.Encrypted:
		err = s.downloadSingleEncrypted(ctx, peer, file, finalTmp, masterKey, progress)
	case len(file.Parts) > 0 && !file.Encrypted:
		err = s.downloadMultipartPlain(ctx, peer, file, finalTmp, progress)
	default:
		err = s.downloadMultipartEncrypted(ctx, peer, file, finalTmp, masterKey, progress)
	}
	if err != nil {
		return err
	}
	if err := verifyOpenFileSize(finalTmp, file.OutputSize); err != nil {
		return fmt.Errorf("Download verification failed: %w", err)
	}
	if err := finalTmp.Close(); err != nil {
		return fmt.Errorf("Disk Error: %w", err)
	}
	if err := replaceDownloadedFile(finalTmpPath, destination); err != nil {
		return fmt.Errorf("Disk Error: %w", err)
	}
	committed = true
	return nil
}

func (s *Service) downloadSinglePlain(
	ctx context.Context,
	peer tgclient.InputPeer,
	file projection.DownloadFile,
	destination *os.File,
	progress downloadProgressFunc,
) error {
	messageID := file.ContentMsgID
	if messageID <= 0 {
		messageID = file.LogicalMsgID
	}
	err := s.sendRetryPolicy().Do(ctx, func() error {
		if err := destination.Truncate(0); err != nil {
			return downloadDiskError{err: fmt.Errorf("truncate download target: %w", err)}
		}
		return s.TG.DownloadFileAt(ctx, peer, messageID, destination, 0, progress)
	})
	return classifyFileDownloadError(err)
}

func (s *Service) downloadSingleEncrypted(
	ctx context.Context,
	peer tgclient.InputPeer,
	file projection.DownloadFile,
	destination *os.File,
	masterKey []byte,
	progress downloadProgressFunc,
) error {
	messageID := file.ContentMsgID
	if messageID <= 0 {
		messageID = file.LogicalMsgID
	}
	cipher, err := os.CreateTemp(filepath.Dir(destination.Name()), ".tdrive-download-cipher-*")
	if err != nil {
		return fmt.Errorf("Disk Error: %w", err)
	}
	defer func() {
		_ = cipher.Close()
		_ = os.Remove(cipher.Name())
	}()

	err = s.sendRetryPolicy().Do(ctx, func() error {
		if err := cipher.Truncate(0); err != nil {
			return downloadDiskError{err: fmt.Errorf("truncate ciphertext target: %w", err)}
		}
		return s.TG.DownloadFileAt(ctx, peer, messageID, cipher, 0, progress)
	})
	if err := classifyFileDownloadError(err); err != nil {
		return err
	}
	if err := verifyOpenFileSize(cipher, file.StoredSize); err != nil {
		return fmt.Errorf("Download verification failed: %w", err)
	}
	if _, err := cipher.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("Disk Error: %w", err)
	}
	if _, err := tdcrypto.DecryptStream(cipher, destination, masterKey); err != nil {
		return fmt.Errorf("Decrypt failed: %w", err)
	}
	return nil
}

func (s *Service) downloadMultipartPlain(
	ctx context.Context,
	peer tgclient.InputPeer,
	file projection.DownloadFile,
	destination *os.File,
	progress downloadProgressFunc,
) error {
	const partConcurrency = 2

	partOffsets := make([]int64, len(file.Parts))
	var offset int64
	for i, part := range file.Parts {
		partOffsets[i] = offset
		offset += part.Size
	}

	downloadCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, partConcurrency)
	errCh := make(chan error, len(file.Parts))
	var wait sync.WaitGroup
	var progressMu sync.Mutex
	partDone := make([]int64, len(file.Parts))
	var totalDone int64

	report := func(index int, done int64) {
		done = min(max(done, 0), file.Parts[index].Size)
		progressMu.Lock()
		defer progressMu.Unlock()
		if done <= partDone[index] {
			return
		}
		totalDone += done - partDone[index]
		partDone[index] = done
		if progress != nil {
			progress(totalDone, file.StoredSize)
		}
	}

	for i, part := range file.Parts {
		i, part := i, part
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case sem <- struct{}{}:
			case <-downloadCtx.Done():
				return
			}
			defer func() { <-sem }()

			err := s.sendRetryPolicy().Do(downloadCtx, func() error {
				return s.TG.DownloadFileAt(downloadCtx, peer, part.MsgID, destination, partOffsets[i], func(done, _ int64) {
					report(i, done)
				})
			})
			if err != nil {
				select {
				case errCh <- err:
				default:
				}
				cancel()
				return
			}
			report(i, part.Size)
		}()
	}

	wait.Wait()
	close(errCh)
	if err, ok := <-errCh; ok {
		return classifyFileDownloadError(err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func (s *Service) downloadMultipartEncrypted(
	ctx context.Context,
	peer tgclient.InputPeer,
	file projection.DownloadFile,
	destination *os.File,
	masterKey []byte,
	progress downloadProgressFunc,
) error {
	pipeReader, pipeWriter := io.Pipe()
	downloadDone := make(chan error, 1)
	go func() {
		downloadErr := s.downloadEncryptedPartsOrdered(
			ctx, peer, file, filepath.Dir(destination.Name()), pipeWriter, progress,
		)
		downloadDone <- downloadErr
		_ = pipeWriter.CloseWithError(downloadErr)
	}()

	if _, err := tdcrypto.DecryptStream(pipeReader, destination, masterKey); err != nil {
		select {
		case downloadErr := <-downloadDone:
			if downloadErr != nil {
				return downloadErr
			}
		default:
			_ = pipeReader.CloseWithError(err)
			<-downloadDone
		}
		return fmt.Errorf("Download/decrypt failed: %w", err)
	}
	if downloadErr := <-downloadDone; downloadErr != nil {
		return downloadErr
	}
	return nil
}

func (s *Service) downloadEncryptedPartsOrdered(
	ctx context.Context,
	peer tgclient.InputPeer,
	file projection.DownloadFile,
	tempDir string,
	destination io.Writer,
	progress downloadProgressFunc,
) error {
	var completed int64
	for _, part := range file.Parts {
		partTmp, err := os.CreateTemp(tempDir, ".tdrive-download-part-*")
		if err != nil {
			return fmt.Errorf("Disk Error: %w", err)
		}
		partPath := partTmp.Name()
		cleanup := func() {
			_ = partTmp.Close()
			_ = os.Remove(partPath)
		}

		var reported int64
		var reportedMu sync.Mutex
		err = s.sendRetryPolicy().Do(ctx, func() error {
			if err := partTmp.Truncate(0); err != nil {
				return downloadDiskError{err: fmt.Errorf("truncate part target: %w", err)}
			}
			return s.TG.DownloadFileAt(ctx, peer, part.MsgID, partTmp, 0, func(done, _ int64) {
				done = min(max(done, 0), part.Size)
				reportedMu.Lock()
				defer reportedMu.Unlock()
				if done <= reported {
					return
				}
				reported = done
				if progress != nil {
					progress(completed+done, file.StoredSize)
				}
			})
		})
		if err := classifyFileDownloadError(err); err != nil {
			cleanup()
			return err
		}
		if err := verifyOpenFileSize(partTmp, part.Size); err != nil {
			cleanup()
			return fmt.Errorf("Download verification failed: %w", err)
		}
		if _, err := partTmp.Seek(0, io.SeekStart); err != nil {
			cleanup()
			return fmt.Errorf("Disk Error: %w", err)
		}
		if _, err := io.CopyN(destination, partTmp, part.Size); err != nil {
			cleanup()
			return fmt.Errorf("Disk Error: %w", err)
		}
		cleanup()
		completed += part.Size
		if progress != nil {
			progress(completed, file.StoredSize)
		}
	}
	return nil
}

func verifyOpenFileSize(file *os.File, expected int64) error {
	if expected < 0 {
		return fmt.Errorf("invalid expected size %d", expected)
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() != expected {
		return fmt.Errorf("downloaded %d bytes, expected %d", info.Size(), expected)
	}
	return nil
}

func classifyFileDownloadError(err error) error {
	var diskErr downloadDiskError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &diskErr):
		return fmt.Errorf("Disk Error: %w", diskErr.err)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, tgclient.ErrMessageNotFound):
		return fmt.Errorf("Message deleted or not found: %w", err)
	case errors.Is(err, tgclient.ErrNotFile):
		return fmt.Errorf("This is not a file: %w", err)
	case errors.Is(err, tgclient.ErrEmptyDocument):
		return fmt.Errorf("Empty document: %w", err)
	default:
		return fmt.Errorf("Network Error: %w", err)
	}
}
