package media

import (
	"bytes"
	"context"
	"errors"
	"image"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/thumbnail"
)

func TestOversizedImageUsesExistingPreviewAndRangeLength(t *testing.T) {
	source := mediaPNGHeader(6144, 8192)
	preview := jpegFixture(t, 120, 160)
	db := newResolverTestDB(t)
	mustApplyOp(t, db, 10, projection.Op{Type: projection.OpFileUpload, Parent: projection.RootParent, Name: "50mp.png", FileSize: int64(len(source))})
	ranges := newMediaRangeFake(map[int64][]byte{10: source})
	var requested LogicalFile
	svc := NewService(Config{
		DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges,
		ImagePreview: func(_ context.Context, file LogicalFile) ([]byte, error) {
			requested = file
			return bytes.Clone(preview), nil
		},
	})
	t.Cleanup(func() { _ = svc.Close() })
	opened, err := svc.OpenImage(t.Context(), testChannelID, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !opened.ImagePreview || opened.MimeType != "image/jpeg" || opened.Info.Name != "50mp.png" || opened.Info.PlaintextSize != int64(len(source)) {
		t.Fatalf("resized image changed original metadata: %+v", opened)
	}
	if requested.ChannelID != testChannelID || requested.FileID != 10 || requested.Revision != 1 {
		t.Fatalf("preview source = %+v", requested)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, opened.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Range", "bytes=2-7")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusPartialContent || !bytes.Equal(body, preview[2:8]) {
		t.Fatalf("preview range = %d %x, %v", response.StatusCode, body, err)
	}
	if response.Header.Get("Content-Range") != "bytes 2-7/"+itoa(int64(len(preview))) || response.ContentLength != 6 || response.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("preview range headers = %v", response.Header)
	}
	assertNoStoreHeaders(t, response.Header)
	snapshot, err := svc.OpenResultForToken(opened.Token)
	if err != nil || !snapshot.ImagePreview {
		t.Fatalf("preview snapshot = %+v, %v", snapshot, err)
	}
	if _, err := svc.OpenStream(t.Context(), testChannelID, 10); !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("original-byte stream bypassed admission: %v", err)
	}
}

func TestImagePreviewRejectsUnsafeDerivative(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  []byte
		err  error
	}{
		{"empty", nil, ErrImageTooLarge},
		{"compressed budget", make([]byte, imagePreviewMaxBytes+1), ErrImageTooLarge},
		{"not image", []byte("invalid"), ErrInvalidImage},
		{"non JPEG", mediaPNGHeader(100, 100), ErrInvalidImage},
		{"oversized dimensions", jpegFixture(t, 1601, 1), ErrImageTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newResolverTestDB(t)
			body := mediaPNGHeader(6144, 8192)
			mustApplyOp(t, db, 10, projection.Op{Type: projection.OpFileUpload, Parent: projection.RootParent, Name: "camera.png", FileSize: int64(len(body))})
			ranges := newMediaRangeFake(map[int64][]byte{10: body})
			owned := bytes.Clone(tc.raw)
			svc := NewService(Config{DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges,
				ImagePreview: func(context.Context, LogicalFile) ([]byte, error) { return owned, nil },
			})
			t.Cleanup(func() { _ = svc.Close() })
			if _, err := svc.OpenImage(t.Context(), testChannelID, 10, 1); !errors.Is(err, tc.err) {
				t.Fatalf("unsafe preview error = %v, want %v", err, tc.err)
			}
			if len(svc.server.sessions) != 0 || !bytes.Equal(owned, make([]byte, len(owned))) {
				t.Fatal("unsafe derivative published a session or retained preview bytes")
			}
		})
	}
}

func TestImagePreviewFallsBackOnlyWhenRenditionIsMissing(t *testing.T) {
	for _, tc := range []struct {
		name       string
		previewErr error
	}{
		{"missing", ErrImagePreviewMissing},
		{"permission", ErrExternalRestricted},
		{"stale", ErrStaleRevision},
		{"canceled", context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newResolverTestDB(t)
			body := jpegFixture(t, 64, 48)
			mustApplyOp(t, db, 10, projection.Op{Type: projection.OpFileUpload, Parent: projection.RootParent, Name: "photo.jpg", FileSize: int64(len(body))})
			ranges := newMediaRangeFake(map[int64][]byte{10: body})
			svc := NewService(Config{DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges,
				ImageAdmission: ImageAdmissionLimits{MaxPixels: 16},
				ImagePreview:   func(context.Context, LogicalFile) ([]byte, error) { return nil, tc.previewErr },
			})
			t.Cleanup(func() { _ = svc.Close() })
			opened, err := svc.OpenImage(t.Context(), testChannelID, 10, 1)
			if tc.previewErr == ErrImagePreviewMissing {
				if err != nil || !opened.ImagePreview {
					t.Fatalf("missing rendition fallback = %+v, %v", opened, err)
				}
			} else if !errors.Is(err, tc.previewErr) || len(svc.server.sessions) != 0 {
				t.Fatalf("authorization/cancellation failure = %+v, %v", opened, err)
			}
		})
	}
}

func TestImagePreviewSourceTransferIsBounded(t *testing.T) {
	svc := NewService(Config{})
	t.Cleanup(func() { _ = svc.Close() })
	session := &Session{file: LogicalFile{PlaintextSize: thumbnail.MaxLocalSourceBytes + 1}}
	if _, err := svc.loadImagePreview(t.Context(), session); !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("oversized transfer = %v", err)
	}
}

func TestEncryptedResizedImageClearsBytesWhenVaultLocks(t *testing.T) {
	db := newResolverTestDB(t)
	body := jpegFixture(t, 64, 48)
	key := bytes.Repeat([]byte{0x6d}, 32)
	ciphertext := encryptMediaFixture(t, body, key)
	mustApplyOp(t, db, 10, projection.Op{Type: projection.OpFileUpload, Parent: projection.RootParent, Name: "private.jpg", FileSize: int64(len(ciphertext)), Encrypted: true, PlaintextSize: int64(len(body)), EncryptionVersion: 1})
	ranges := newMediaRangeFake(map[int64][]byte{10: ciphertext})
	svc := NewService(Config{DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges,
		ImageAdmission: ImageAdmissionLimits{MaxPixels: 16},
		Keys:           MasterKeyProviderFunc(func(context.Context, int64) ([]byte, error) { return bytes.Clone(key), nil }),
	})
	t.Cleanup(func() { _ = svc.Close() })
	opened, err := svc.OpenImage(t.Context(), testChannelID, 10, 1)
	if err != nil || !opened.ImagePreview || !opened.Info.Encrypted {
		t.Fatalf("encrypted preview = %+v, %v", opened, err)
	}
	session := svc.server.session(opened.Token)
	retained := session.previewData
	svc.CloseEncryptedSessions()
	if !bytes.Equal(retained, make([]byte, len(retained))) {
		t.Fatal("vault lock retained preview plaintext")
	}
	if _, err := session.ReadAt(t.Context(), make([]byte, 1), 0); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("locked read = %v", err)
	}
	if !bytes.Equal(ranges.bodies[10], ciphertext) {
		t.Fatal("preview changed encrypted download bytes")
	}
}

func TestImagePreviewRechecksVaultGenerationAndRevision(t *testing.T) {
	for _, changed := range []string{"vault", "deleted", "cancelled"} {
		t.Run(changed, func(t *testing.T) {
			db := newResolverTestDB(t)
			body := jpegFixture(t, 64, 48)
			key := bytes.Repeat([]byte{0x6d}, 32)
			ciphertext := encryptMediaFixture(t, body, key)
			mustApplyOp(t, db, 10, projection.Op{Type: projection.OpFileUpload, Parent: projection.RootParent, Name: "private.jpg", FileSize: int64(len(ciphertext)), Encrypted: true, PlaintextSize: int64(len(body)), EncryptionVersion: 1})
			ranges := newMediaRangeFake(map[int64][]byte{10: ciphertext})
			var generation atomic.Uint64
			var gate sync.RWMutex
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			owned := bytes.Clone(body)
			svc := NewService(Config{DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges,
				ImageAdmission:     ImageAdmissionLimits{MaxPixels: 16},
				Keys:               MasterKeyProviderFunc(func(context.Context, int64) ([]byte, error) { return bytes.Clone(key), nil }),
				EncryptionOpenGate: gate.RLocker(), EncryptionOpenGeneration: generation.Load,
				ImagePreview: func(context.Context, LogicalFile) ([]byte, error) {
					switch changed {
					case "vault":
						generation.Add(2)
					case "deleted":
						mustApplyOp(t, db, 11, projection.Op{Type: projection.OpTomb, Obj: "f:10"})
					case "cancelled":
						cancel()
					}
					return owned, nil
				},
			})
			t.Cleanup(func() { _ = svc.Close() })
			_, err := svc.OpenImage(ctx, testChannelID, 10, 1)
			want := map[string]error{"vault": ErrKeyUnavailable, "deleted": ErrFileNotFound, "cancelled": context.Canceled}[changed]
			if !errors.Is(err, want) || len(svc.server.sessions) != 0 {
				t.Fatalf("racing %s published preview: %v", changed, err)
			}
			if !bytes.Equal(owned, make([]byte, len(owned))) {
				t.Fatal("failed preview retained private bytes")
			}
		})
	}
}

func TestRawDriveLargePhotoUsesResizedPreview(t *testing.T) {
	svc, _, _, _ := rawDriveFixture(t, "forwarded.jpg", "image/jpeg", jpegFixture(t, 64, 48))
	svc.imageLimits.MaxPixels = 16
	opened, err := svc.OpenImage(t.Context(), testChannelID, 7, 0)
	if err != nil || !opened.ImagePreview || opened.Info.SourceKind != "drive" {
		t.Fatalf("raw preview = %+v, %v", opened, err)
	}
	response, err := http.Get(opened.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	decoded, _, err := image.Decode(response.Body)
	if err != nil || decoded.Bounds().Dx() != 64 {
		t.Fatalf("raw preview decode = %v", err)
	}
	var count int
	if err := svc.resolver.db.QueryRow("SELECT COUNT(*) FROM files").Scan(&count); err != nil || count != 0 {
		t.Fatalf("preview adopted raw file: %d, %v", count, err)
	}
}

func TestResizedSessionReadAndCloseAreSynchronized(t *testing.T) {
	preview := jpegFixture(t, 64, 48)
	owned := bytes.Clone(preview)
	session := &Session{previewData: owned, previewSize: int64(len(owned))}
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, len(preview))
		n, err := session.ReadAt(t.Context(), buf, 0)
		if err != nil || n != len(preview) || !bytes.Equal(buf, preview) {
			done <- errors.New("preview read differed before close")
			close(ready)
			return
		}
		close(ready)
		for range 100 {
			n, err := session.ReadAt(t.Context(), buf, 0)
			if errors.Is(err, ErrSessionNotFound) {
				done <- nil
				return
			}
			if err != nil || n != len(preview) || !bytes.Equal(buf, preview) {
				done <- errors.New("concurrent close exposed partial or cleared bytes")
				return
			}
		}
		done <- nil
	}()
	<-ready
	session.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(owned, make([]byte, len(owned))) {
		t.Fatal("close retained resized bytes")
	}
	if _, err := session.ReadAt(t.Context(), make([]byte, 1), 0); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("closed preview read = %v", err)
	}
}
