package file

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
	"TDrive/backend/thumbnail"
)

func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func decodeJPEGPayload(t *testing.T, payload PreviewPayload) image.Image {
	t.Helper()
	if payload.MimeType != "image/jpeg" {
		t.Fatalf("mime = %q, want image/jpeg", payload.MimeType)
	}
	raw, err := base64.StdEncoding.DecodeString(payload.DataBase64)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode thumbnail: %v", err)
	}
	if format != "jpeg" {
		t.Fatalf("format = %q, want jpeg", format)
	}
	return img
}

func TestThumbnailPlainImageGeneratesAndDownscales(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	svc.Thumbs = thumbnail.NewCache(t.TempDir(), 1<<20)

	path := writeTempNamedFile(t, "wide.png", makePNG(t, 1600, 800))
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, false)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	payload, err := svc.Thumbnail(context.Background(), personalChannelID, files[0].MsgID)
	if err != nil {
		t.Fatalf("thumbnail: %v", err)
	}
	img := decodeJPEGPayload(t, payload)
	if b := img.Bounds(); b.Dx() != 512 || b.Dy() != 256 {
		t.Fatalf("thumbnail = %dx%d, want 512x256", b.Dx(), b.Dy())
	}
}

func TestThumbnailServedFromCacheWithoutTelegram(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	svc.Thumbs = thumbnail.NewCache(t.TempDir(), 1<<20)

	path := writeTempNamedFile(t, "pic.png", makePNG(t, 200, 200))
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, false)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	first, err := svc.Thumbnail(context.Background(), personalChannelID, files[0].MsgID)
	if err != nil {
		t.Fatalf("first thumbnail: %v", err)
	}

	// Remove the body from Telegram. A second call must still succeed, proving
	// it was served from the on-disk cache and not re-downloaded.
	if err := fakeTG.DeleteMessages(context.Background(), tgclient.InputPeer{}, []int64{int64(files[0].MsgID)}); err != nil {
		t.Fatalf("delete telegram body: %v", err)
	}

	second, err := svc.Thumbnail(context.Background(), personalChannelID, files[0].MsgID)
	if err != nil {
		t.Fatalf("second thumbnail (cache): %v", err)
	}
	if first.DataBase64 != second.DataBase64 {
		t.Fatalf("cached thumbnail differs from generated one")
	}
}

func TestThumbnailEncryptedStoresCiphertextOnDisk(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	cacheDir := t.TempDir()
	svc.Thumbs = thumbnail.NewCache(cacheDir, 1<<20)
	masterKey := bytes.Repeat([]byte{4}, 32)
	wireEncryption(svc, masterKey)

	path := writeTempNamedFile(t, "secret.png", makePNG(t, 1024, 512))
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, true)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	payload, err := svc.Thumbnail(context.Background(), personalChannelID, files[0].MsgID)
	if err != nil {
		t.Fatalf("thumbnail: %v", err)
	}
	img := decodeJPEGPayload(t, payload)
	if b := img.Bounds(); b.Dx() != 512 || b.Dy() != 256 {
		t.Fatalf("thumbnail = %dx%d, want 512x256", b.Dx(), b.Dy())
	}

	// The cached file must be ciphertext (TDE1 stream), never a raw JPEG.
	cached := readSingleCacheFile(t, cacheDir)
	if bytes.HasPrefix(cached, []byte{0xFF, 0xD8}) {
		t.Fatalf("cache holds a raw JPEG; encrypted thumbnails must be encrypted at rest")
	}
	if !bytes.HasPrefix(cached, []byte("TDE1")) {
		t.Fatalf("cache file is not a TDrive encrypted stream: % x", cached[:min(4, len(cached))])
	}

	// A second call decrypts the cache and returns the same image.
	again, err := svc.Thumbnail(context.Background(), personalChannelID, files[0].MsgID)
	if err != nil {
		t.Fatalf("second thumbnail: %v", err)
	}
	if again.DataBase64 != payload.DataBase64 {
		t.Fatalf("decrypted cache thumbnail differs from the original")
	}
}

func TestThumbnailEncryptedCacheHitClearsOwnedKey(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	svc.Thumbs = thumbnail.NewCache(t.TempDir(), 1<<20)
	masterKey := bytes.Repeat([]byte{0x41}, 32)
	wireEncryption(svc, masterKey)

	path := writeTempNamedFile(t, "cached-secret.png", makePNG(t, 160, 80))
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, true)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if _, err := svc.Thumbnail(context.Background(), personalChannelID, files[0].MsgID); err != nil {
		t.Fatalf("populate thumbnail cache: %v", err)
	}

	var (
		cacheKeyCalls int
		cacheReadKey  []byte
	)
	svc.RequireEncryptionKey = func(encrypted bool) ([]byte, error) {
		if !encrypted {
			t.Fatal("encrypted cache hit requested a plaintext key")
		}
		cacheKeyCalls++
		cacheReadKey = append([]byte(nil), masterKey...)
		return cacheReadKey, nil
	}
	if _, err := svc.Thumbnail(context.Background(), personalChannelID, files[0].MsgID); err != nil {
		t.Fatalf("cached thumbnail: %v", err)
	}
	if cacheKeyCalls != 1 {
		t.Fatalf("key provider calls = %d, want one cache-read key", cacheKeyCalls)
	}
	assertKeyZeroed(t, cacheReadKey)
}

func TestThumbnailEncryptedGenerationClearsCacheAndFlightKeys(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	svc.Thumbs = thumbnail.NewCache(t.TempDir(), 1<<20)
	masterKey := bytes.Repeat([]byte{0x42}, 32)
	wireEncryption(svc, masterKey)

	path := writeTempNamedFile(t, "generated-secret.png", makePNG(t, 180, 90))
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, true)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	var (
		keysMu sync.Mutex
		keys   [][]byte
	)
	svc.RequireEncryptionKey = func(encrypted bool) ([]byte, error) {
		if !encrypted {
			t.Fatal("encrypted generation requested a plaintext key")
		}
		key := append([]byte(nil), masterKey...)
		keysMu.Lock()
		keys = append(keys, key)
		keysMu.Unlock()
		return key, nil
	}
	if _, err := svc.Thumbnail(context.Background(), personalChannelID, files[0].MsgID); err != nil {
		t.Fatalf("thumbnail: %v", err)
	}

	keysMu.Lock()
	ownedKeys := append([][]byte(nil), keys...)
	keysMu.Unlock()
	if len(ownedKeys) != 2 {
		t.Fatalf("key provider calls = %d, want cache-read and background-flight keys", len(ownedKeys))
	}
	for i, key := range ownedKeys {
		t.Run(fmt.Sprintf("owned-key-%d", i), func(t *testing.T) {
			assertKeyZeroed(t, key)
		})
	}
}

func TestThumbnailCanceledCallerDoesNotClearBackgroundFlightKey(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	project(t, db, personalChannelID, 93, 7, projection.Op{
		Type:              projection.OpFileUpload,
		Parent:            "",
		Name:              "background.png",
		FileSize:          128,
		FileUploadTime:    1,
		Encrypted:         true,
		PlaintextSize:     64,
		EncryptionVersion: 1,
	})

	providedKeys := make(chan []byte, 4)
	var providerMu sync.Mutex
	providerCall := byte(0)
	svc.RequireEncryptionKey = func(encrypted bool) ([]byte, error) {
		if !encrypted {
			t.Error("encrypted background flight requested a plaintext key")
		}
		providerMu.Lock()
		providerCall++
		value := providerCall
		providerMu.Unlock()
		key := bytes.Repeat([]byte{value}, 32)
		providedKeys <- key
		return key, nil
	}

	flightStarted := make(chan struct{})
	releaseFlight := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFlight) }) }
	t.Cleanup(release)
	var flightKey []byte
	svc.generateThumbnailFn = func(_ context.Context, _ int64, _ int, _ string, _ bool, key []byte) ([]byte, error) {
		flightKey = key
		close(flightStarted)
		<-releaseFlight
		return []byte{0xff, 0xd8, 0xff, 0xd9}, nil
	}

	callerCtx, cancelCaller := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() {
		_, err := svc.Thumbnail(callerCtx, personalChannelID, 93)
		firstDone <- err
	}()
	select {
	case <-flightStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("background thumbnail flight did not start")
	}

	cacheReadKey := <-providedKeys
	backgroundKey := <-providedKeys
	if len(flightKey) == 0 || &flightKey[0] != &backgroundKey[0] {
		t.Fatal("background generator did not receive the flight-owned key")
	}

	waiterDone := make(chan error, 1)
	go func() {
		_, err := svc.Thumbnail(context.Background(), personalChannelID, 93)
		waiterDone <- err
	}()
	var waiterCacheKey []byte
	select {
	case waiterCacheKey = <-providedKeys:
	case <-time.After(2 * time.Second):
		t.Fatal("second thumbnail caller did not complete its cache-key lookup")
	}

	cancelCaller()
	select {
	case err := <-firstDone:
		if err == nil {
			t.Fatal("canceled thumbnail caller unexpectedly succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled thumbnail caller did not return")
	}
	assertKeyZeroed(t, cacheReadKey)
	if !bytes.Equal(backgroundKey, bytes.Repeat([]byte{2}, 32)) {
		t.Fatal("background-flight key was cleared while detached generation was still running")
	}

	release()
	select {
	case err := <-waiterDone:
		if err != nil {
			t.Fatalf("waiting thumbnail caller: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiting thumbnail caller did not receive background result")
	}
	assertKeyZeroed(t, backgroundKey)
	assertKeyZeroed(t, waiterCacheKey)
}

func TestThumbnailEncryptedLockedRequiresPassword(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	svc.Thumbs = thumbnail.NewCache(t.TempDir(), 1<<20)
	masterKey := bytes.Repeat([]byte{5}, 32)
	wireEncryption(svc, masterKey)

	path := writeTempNamedFile(t, "locked.png", makePNG(t, 120, 120))
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, true)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	// Lock the vault.
	failedKey := bytes.Repeat([]byte{0x5c}, 32)
	svc.RequireEncryptionKey = func(encrypted bool) ([]byte, error) {
		if encrypted {
			return failedKey, errNeedPassword
		}
		return nil, nil
	}

	if _, err := svc.Thumbnail(context.Background(), personalChannelID, files[0].MsgID); !errors.Is(err, errPreviewEncryptionPasswordRequired) {
		t.Fatalf("thumbnail err = %v, want password required", err)
	}
	assertKeyZeroed(t, failedKey)
}

func TestThumbnailEncryptedFlightProviderErrorClearsOwnedKeys(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	project(t, db, personalChannelID, 94, 7, projection.Op{
		Type:              projection.OpFileUpload,
		Parent:            "",
		Name:              "flight-error.png",
		FileSize:          128,
		FileUploadTime:    1,
		Encrypted:         true,
		PlaintextSize:     64,
		EncryptionVersion: 1,
	})

	cacheReadKey := bytes.Repeat([]byte{0x61}, 32)
	flightKey := bytes.Repeat([]byte{0x62}, 32)
	calls := 0
	svc.RequireEncryptionKey = func(encrypted bool) ([]byte, error) {
		calls++
		if calls == 1 {
			return cacheReadKey, nil
		}
		return flightKey, errNeedPassword
	}

	if _, err := svc.Thumbnail(context.Background(), personalChannelID, 94); !errors.Is(err, errPreviewEncryptionPasswordRequired) {
		t.Fatalf("thumbnail err = %v, want password required", err)
	}
	if calls != 2 {
		t.Fatalf("key provider calls = %d, want cache-read and flight attempts", calls)
	}
	assertKeyZeroed(t, cacheReadKey)
	assertKeyZeroed(t, flightKey)
}

func TestThumbnailRejectsNonImage(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	svc.Thumbs = thumbnail.NewCache(t.TempDir(), 1<<20)

	path := writeTempNamedFile(t, "notes.txt", []byte("just text"))
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, false)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	if _, err := svc.Thumbnail(context.Background(), personalChannelID, files[0].MsgID); !errors.Is(err, errPreviewNotSupported) {
		t.Fatalf("thumbnail err = %v, want not supported", err)
	}
}

func TestThumbnailRejectsNonImageNotInProjection(t *testing.T) {
	// A msgID present on Telegram but not in the projection must still be
	// rejected by name before the body is downloaded.
	svc, _, fakeTG, _ := newTestService(t)
	svc.Thumbs = thumbnail.NewCache(t.TempDir(), 1<<20)

	const msgID = 92
	fakeTG.SeedHistory(tgclient.HistoryMessage{
		MsgID:        msgID,
		HasMedia:     true,
		MediaSize:    4096,
		DocumentName: "report.pdf",
	})

	if _, err := svc.Thumbnail(context.Background(), personalChannelID, msgID); !errors.Is(err, errPreviewNotSupported) {
		t.Fatalf("thumbnail err = %v, want not supported", err)
	}
}

func TestThumbnailTooLargeSource(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.Thumbs = thumbnail.NewCache(t.TempDir(), 1<<20)

	const msgID = 91
	project(t, db, personalChannelID, msgID, 7, projection.Op{
		Type:           projection.OpFileUpload,
		Parent:         "",
		Name:           "big.png",
		FileSize:       maxThumbSourceBytes + 1,
		FileUploadTime: 1,
	})
	fakeTG.SeedHistory(tgclient.HistoryMessage{
		MsgID:        msgID,
		HasMedia:     true,
		MediaSize:    maxThumbSourceBytes + 1,
		DocumentName: "big.png",
	})

	if _, err := svc.Thumbnail(context.Background(), personalChannelID, msgID); !errors.Is(err, errPreviewTooLarge) {
		t.Fatalf("thumbnail err = %v, want too large", err)
	}
}

// --- helpers ---

func readSingleCacheFile(t *testing.T, dir string) []byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read cache dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".bin" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read cache file: %v", err)
		}
		return data
	}
	t.Fatalf("no cache file found in %s", dir)
	return nil
}

// wireEncryption sets up the upload/preview encryption hooks against a fixed
// master key, mirroring the production encryption service closely enough for
// the thumbnail path.
func wireEncryption(svc *Service, masterKey []byte) {
	svc.MasterKeyForUpload = func(channelID int64, wantEncrypted bool) ([]byte, error) {
		if !wantEncrypted {
			return nil, nil
		}
		return append([]byte(nil), masterKey...), nil
	}
	svc.WriteCiphertextTemp = func(plain io.Reader, plaintextSize int64, key []byte) (*os.File, error) {
		tmp, err := os.CreateTemp("", "tdrive-test-cipher-*")
		if err != nil {
			return nil, err
		}
		if err := tdcrypto.EncryptStream(plain, tmp, key, plaintextSize); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
			return nil, err
		}
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
			return nil, err
		}
		return tmp, nil
	}
	svc.RequireEncryptionKey = func(encrypted bool) ([]byte, error) {
		if encrypted {
			return append([]byte(nil), masterKey...), nil
		}
		return nil, nil
	}
}
