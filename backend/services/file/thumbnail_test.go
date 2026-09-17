package file

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"

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

func TestThumbnailUsesRemoteSmallImageAndCache(t *testing.T) {
	svc, client := renditionFixture(t)
	svc.Thumbs = thumbnail.NewCache(t.TempDir(), 1<<20)
	raw := makePNG(t, 200, 100)
	client.doc.Thumbs = []tgclient.FileThumb{{Bytes: raw, Width: 200, Height: 100}}
	first, err := svc.Thumbnail(context.Background(), personalChannelID, 91)
	if err != nil {
		t.Fatal(err)
	}
	if first.MimeType != "image/png" || first.DataBase64 != base64.StdEncoding.EncodeToString(raw) {
		t.Fatal("wrong thumbnail")
	}
	client.doc.Thumbs = nil
	second, err := svc.Thumbnail(context.Background(), personalChannelID, 91)
	if err != nil || second != first {
		t.Fatalf("cache result %v %v", second, err)
	}
	if client.originals.Load() != 0 {
		t.Fatal("downloaded original")
	}
}
func TestThumbnailEncryptedCacheRequiresKeyAndClearsIt(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	project(t, db, personalChannelID, 91, 7, projection.Op{Type: projection.OpFileUpload, Name: "secret.jpg", FileSize: 128, Encrypted: true, PlaintextSize: 64, EncryptionVersion: 1})
	svc.CacheNamespace = "account"
	dir := t.TempDir()
	svc.Thumbs = thumbnail.NewCache(dir, 1<<20)
	f, _, err := projection.FileByID(db, personalChannelID, 91)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{4}, 32)
	raw := makePNG(t, 200, 100)
	svc.writeThumbCache(renditionCacheKey("account:actor:7", f, "thumbnail", 0), raw, true, key)
	cached := readSingleCacheFile(t, dir)
	if !bytes.HasPrefix(cached, []byte("TDE1")) {
		t.Fatal("plaintext cache")
	}
	owned := append([]byte(nil), key...)
	svc.RequireEncryptionKey = func(bool) ([]byte, error) { return owned, nil }
	got, err := svc.Thumbnail(context.Background(), personalChannelID, 91)
	if err != nil {
		t.Fatal(err)
	}
	if got.DataBase64 != base64.StdEncoding.EncodeToString(raw) {
		t.Fatal("wrong decrypted image")
	}
	assertKeyZeroed(t, owned)
	owned = append([]byte(nil), key...)
	svc.RequireEncryptionKey = func(bool) ([]byte, error) { return owned, errNeedPassword }
	if _, err := svc.Thumbnail(context.Background(), personalChannelID, 91); !errors.Is(err, errPreviewEncryptionPasswordRequired) {
		t.Fatalf("locked err=%v", err)
	}
	assertKeyZeroed(t, owned)
}
func TestThumbnailMissingIsExplicit(t *testing.T) {
	svc, client := renditionFixture(t)
	if _, err := svc.Thumbnail(context.Background(), personalChannelID, 91); !errors.Is(err, ErrRenditionMissing) {
		t.Fatalf("err=%v", err)
	}
	if client.originals.Load() != 0 {
		t.Fatal("downloaded original")
	}
}
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
