package mountcontent

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"math"
	"sync/atomic"
	"testing"

	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/media"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

func TestOpenerReadsEncryptedFileAsPlaintext(t *testing.T) {
	db := newTestDB(t)
	masterKey := bytes.Repeat([]byte{0x31}, 32)
	plaintext := encryptedPattern(2*encryptedTestChunkSize + 37)
	ciphertext := encryptMountFixture(t, plaintext, masterKey)
	projectEncryptedSingle(t, db, 10, ciphertext, plaintext)

	providedKey := append([]byte(nil), masterKey...)
	var keyCalls atomic.Int32
	ranges := newRangeFake(map[int64][]byte{10: ciphertext})
	opener, err := New(Config{
		DB:     db,
		Peers:  staticPeerResolver{peer: ranges.peer},
		Ranges: ranges,
		Keys: MasterKeyProviderFunc(func(ctx context.Context, channelID int64) ([]byte, error) {
			keyCalls.Add(1)
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if channelID != testChannelID {
				t.Fatalf("key channel = %d, want %d", channelID, testChannelID)
			}
			return providedKey, nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(opener.Close)

	reader, err := opener.Open(context.Background(), testChannelID, 10)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if keyCalls.Load() != 1 {
		t.Fatalf("key calls = %d, want 1", keyCalls.Load())
	}
	if !zeroed(providedKey) {
		t.Fatal("Open retained the provider-owned master-key bytes")
	}
	if got := reader.Size(); got != int64(len(plaintext)) {
		t.Fatalf("Size = %d, want %d", got, len(plaintext))
	}

	got := make([]byte, encryptedTestChunkSize+29)
	off := int64(encryptedTestChunkSize - 11)
	n, readErr := reader.ReadAt(context.Background(), got, off)
	if readErr != nil || n != len(got) || !bytes.Equal(got, plaintext[off:off+int64(n)]) {
		t.Fatalf("ReadAt = n %d err %v matching=%v", n, readErr, bytes.Equal(got[:n], plaintext[off:off+int64(n)]))
	}

	tail := make([]byte, 100)
	n, readErr = reader.ReadAt(context.Background(), tail, int64(len(plaintext)-7))
	if n != 7 || !errors.Is(readErr, io.EOF) || !bytes.Equal(tail[:n], plaintext[len(plaintext)-7:]) {
		t.Fatalf("tail ReadAt = n %d err %v data=%x", n, readErr, tail[:n])
	}
}

func TestOpenerReadsEncryptedStreamAcrossTelegramSegments(t *testing.T) {
	db := newTestDB(t)
	masterKey := bytes.Repeat([]byte{0x42}, 32)
	plaintext := encryptedPattern(3*encryptedTestChunkSize + 19)
	ciphertext := encryptMountFixture(t, plaintext, masterKey)
	cutPoints := []int{0, 23, encryptedTestChunkSize - 5, 2*encryptedTestChunkSize + 81, len(ciphertext)}
	bodies := make(map[int64][]byte, len(cutPoints)-1)
	msgIDs := make([]int64, 0, len(cutPoints)-1)
	for index := 0; index < len(cutPoints)-1; index++ {
		msgID := int64(100 + index)
		msgIDs = append(msgIDs, msgID)
		bodies[msgID] = append([]byte(nil), ciphertext[cutPoints[index]:cutPoints[index+1]]...)
		projectFile(t, db, msgID, projection.Op{
			Type:       projection.OpFilePart,
			UploadUUID: "encrypted-multipart",
			PartIndex:  index,
			FileSize:   int64(len(bodies[msgID])),
		})
	}
	projectFile(t, db, 200, projection.Op{
		Type:              projection.OpFileManifest,
		UploadUUID:        "encrypted-multipart",
		Name:              "private.bin",
		FileSize:          int64(len(ciphertext)),
		PartCount:         len(msgIDs),
		Encrypted:         true,
		PlaintextSize:     int64(len(plaintext)),
		EncryptionVersion: 1,
	})

	ranges := newRangeFake(bodies)
	opener, err := New(Config{
		DB:     db,
		Peers:  staticPeerResolver{peer: ranges.peer},
		Ranges: ranges,
		Keys: MasterKeyProviderFunc(func(context.Context, int64) ([]byte, error) {
			return append([]byte(nil), masterKey...), nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(opener.Close)
	reader, err := opener.Open(context.Background(), testChannelID, 200)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	got := make([]byte, 2*encryptedTestChunkSize+36)
	off := int64(encryptedTestChunkSize - 17)
	n, readErr := reader.ReadAt(context.Background(), got, off)
	if readErr != nil || n != len(got) || !bytes.Equal(got, plaintext[off:off+int64(n)]) {
		t.Fatalf("multipart encrypted ReadAt = n %d err %v", n, readErr)
	}
}

func TestOpenerRequestsKeyOnlyForEncryptedFilesAfterResolvingBodies(t *testing.T) {
	db := newTestDB(t)
	plainBody := []byte("public")
	projectFile(t, db, 10, projection.Op{Type: projection.OpFileUpload, Name: "public.txt", FileSize: int64(len(plainBody))})

	masterKey := bytes.Repeat([]byte{0x53}, 32)
	privateBody := []byte("private")
	ciphertext := encryptMountFixture(t, privateBody, masterKey)
	projectEncryptedSingle(t, db, 20, ciphertext, privateBody)

	base := newRangeFake(map[int64][]byte{10: plainBody, 20: ciphertext})
	var resolvedPrivate atomic.Bool
	ranges := &resolveControlFake{
		rangeFake: base,
		resolve: func(ctx context.Context, peer tgclient.InputPeer, msgID int64) (tgclient.DocumentRef, error) {
			if msgID == 20 {
				resolvedPrivate.Store(true)
			}
			return base.ResolveDocument(ctx, peer, msgID)
		},
	}
	var keyCalls atomic.Int32
	opener, err := New(Config{
		DB:     db,
		Peers:  staticPeerResolver{peer: base.peer},
		Ranges: ranges,
		Keys: MasterKeyProviderFunc(func(context.Context, int64) ([]byte, error) {
			keyCalls.Add(1)
			if !resolvedPrivate.Load() {
				t.Fatal("key requested before the encrypted Telegram body was resolved")
			}
			return append([]byte(nil), masterKey...), nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(opener.Close)
	if _, err := opener.Open(context.Background(), testChannelID, 10); err != nil {
		t.Fatalf("Open plaintext: %v", err)
	}
	if keyCalls.Load() != 0 {
		t.Fatalf("plaintext Open key calls = %d, want 0", keyCalls.Load())
	}
	if _, err := opener.Open(context.Background(), testChannelID, 20); err != nil {
		t.Fatalf("Open encrypted: %v", err)
	}
	if keyCalls.Load() != 1 {
		t.Fatalf("encrypted Open key calls = %d, want 1", keyCalls.Load())
	}
}

func TestOpenerRejectsUnavailableWrongAndTamperedEncryptedKeys(t *testing.T) {
	db := newTestDB(t)
	masterKey := bytes.Repeat([]byte{0x64}, 32)
	plaintext := encryptedPattern(encryptedTestChunkSize + 9)
	ciphertext := encryptMountFixture(t, plaintext, masterKey)
	projectEncryptedSingle(t, db, 10, ciphertext, plaintext)
	ranges := newRangeFake(map[int64][]byte{10: ciphertext})

	t.Run("missing provider", func(t *testing.T) {
		opener, err := New(Config{DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer opener.Close()
		if reader, openErr := opener.Open(context.Background(), testChannelID, 10); reader != nil || !errors.Is(openErr, ErrKeyUnavailable) {
			t.Fatalf("Open = %v, %v; want nil/ErrKeyUnavailable", reader, openErr)
		}
	})

	t.Run("provider error", func(t *testing.T) {
		sentinel := errors.New("vault locked")
		returnedKey := append([]byte(nil), masterKey...)
		opener, err := New(Config{
			DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges,
			Keys: MasterKeyProviderFunc(func(context.Context, int64) ([]byte, error) { return returnedKey, sentinel }),
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer opener.Close()
		if _, openErr := opener.Open(context.Background(), testChannelID, 10); !errors.Is(openErr, sentinel) || !errors.Is(openErr, ErrKeyUnavailable) {
			t.Fatalf("Open error = %v, want wrapped sentinel and ErrKeyUnavailable", openErr)
		}
		if !zeroed(returnedKey) {
			t.Fatal("Open retained key bytes returned alongside a provider error")
		}
	})

	t.Run("wrong key", func(t *testing.T) {
		opener, err := New(Config{
			DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges,
			Keys: MasterKeyProviderFunc(func(context.Context, int64) ([]byte, error) {
				return bytes.Repeat([]byte{0x65}, 32), nil
			}),
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer opener.Close()
		if _, openErr := opener.Open(context.Background(), testChannelID, 10); !errors.Is(openErr, tdcrypto.ErrAuthFailed) {
			t.Fatalf("Open error = %v, want crypto.ErrAuthFailed", openErr)
		}
	})

	t.Run("tampered read chunk", func(t *testing.T) {
		tampered := append([]byte(nil), ciphertext...)
		tampered[50+3] ^= 0x80
		tamperedRanges := newRangeFake(map[int64][]byte{10: tampered})
		opener, err := New(Config{
			DB: db, Peers: staticPeerResolver{peer: tamperedRanges.peer}, Ranges: tamperedRanges,
			Keys: MasterKeyProviderFunc(func(context.Context, int64) ([]byte, error) {
				return append([]byte(nil), masterKey...), nil
			}),
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer opener.Close()
		reader, openErr := opener.Open(context.Background(), testChannelID, 10)
		if openErr != nil {
			t.Fatalf("Open: %v", openErr)
		}
		if n, readErr := reader.ReadAt(context.Background(), make([]byte, 1), 0); n != 0 || !errors.Is(readErr, tdcrypto.ErrAuthFailed) {
			t.Fatalf("ReadAt tampered chunk = n %d err %v", n, readErr)
		}
	})
}

func TestEncryptedOpenAndReaderLifecycleAreCancellationSafe(t *testing.T) {
	db := newTestDB(t)
	masterKey := bytes.Repeat([]byte{0x75}, 32)
	plaintext := []byte("lifecycle")
	ciphertext := encryptMountFixture(t, plaintext, masterKey)
	projectEncryptedSingle(t, db, 10, ciphertext, plaintext)
	ranges := newRangeFake(map[int64][]byte{10: ciphertext})

	started := make(chan struct{})
	provider := MasterKeyProviderFunc(func(ctx context.Context, _ int64) ([]byte, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	opener, err := New(Config{
		DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges, Keys: provider,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result := make(chan error, 1)
	go func() {
		_, openErr := opener.Open(context.Background(), testChannelID, 10)
		result <- openErr
	}()
	<-started
	opener.Close()
	if openErr := <-result; !errors.Is(openErr, ErrClosed) {
		t.Fatalf("Open overlapping Close error = %v, want ErrClosed", openErr)
	}

	opener, err = New(Config{
		DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges,
		Keys: MasterKeyProviderFunc(func(context.Context, int64) ([]byte, error) {
			return append([]byte(nil), masterKey...), nil
		}),
	})
	if err != nil {
		t.Fatalf("New second: %v", err)
	}
	defer opener.Close()
	reader, err := opener.Open(context.Background(), testChannelID, 10)
	if err != nil {
		t.Fatalf("Open second: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Reader.Close: %v", err)
	}
	if n, readErr := reader.ReadAt(context.Background(), make([]byte, 1), 0); n != 0 || !errors.Is(readErr, ErrReaderClosed) {
		t.Fatalf("closed encrypted Reader.ReadAt = n %d err %v", n, readErr)
	}
}

func TestOpenerCloseClosesPublishedEncryptedReaders(t *testing.T) {
	db := newTestDB(t)
	masterKey := bytes.Repeat([]byte{0x87}, 32)
	plaintext := []byte("close with opener")
	ciphertext := encryptMountFixture(t, plaintext, masterKey)
	projectEncryptedSingle(t, db, 10, ciphertext, plaintext)
	ranges := newRangeFake(map[int64][]byte{10: ciphertext})
	opener, err := New(Config{
		DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges,
		Keys: MasterKeyProviderFunc(func(context.Context, int64) ([]byte, error) {
			return append([]byte(nil), masterKey...), nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	reader, err := opener.Open(context.Background(), testChannelID, 10)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	decryptor := reader.decryptor
	if decryptor == nil {
		t.Fatal("encrypted Reader has no decryptor")
	}

	opener.Close()
	if n, readErr := decryptor.ReadAt(context.Background(), make([]byte, 1), 0); n != 0 || !errors.Is(readErr, tdcrypto.ErrDecryptorClosed) {
		t.Fatalf("decryptor after Opener.Close = n %d err %v, want ErrDecryptorClosed", n, readErr)
	}
	if n, readErr := reader.ReadAt(context.Background(), make([]byte, 1), 0); n != 0 || !errors.Is(readErr, ErrClosed) {
		t.Fatalf("Reader after Opener.Close = n %d err %v, want ErrClosed", n, readErr)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Reader.Close after Opener.Close: %v", err)
	}
}

func TestOpenerRejectsUnsupportedEncryptedVersionBeforeRequestingKey(t *testing.T) {
	db := newTestDB(t)
	masterKey := bytes.Repeat([]byte{0x86}, 32)
	plaintext := []byte("version")
	ciphertext := encryptMountFixture(t, plaintext, masterKey)
	projectFile(t, db, 10, projection.Op{
		Type: projection.OpFileUpload, Name: "future.bin", FileSize: int64(len(ciphertext)),
		Encrypted: true, PlaintextSize: int64(len(plaintext)), EncryptionVersion: 2,
	})
	ranges := newRangeFake(map[int64][]byte{10: ciphertext})
	var keyCalls atomic.Int32
	opener, err := New(Config{
		DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges,
		Keys: MasterKeyProviderFunc(func(context.Context, int64) ([]byte, error) {
			keyCalls.Add(1)
			return append([]byte(nil), masterKey...), nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer opener.Close()
	if _, openErr := opener.Open(context.Background(), testChannelID, 10); !errors.Is(openErr, ErrEncryptedUnsupported) {
		t.Fatalf("Open error = %v, want ErrEncryptedUnsupported", openErr)
	}
	if keyCalls.Load() != 0 {
		t.Fatalf("key calls = %d, want 0", keyCalls.Load())
	}
}

func TestEncryptedMetadataRejectsPlaintextBeyondTDE1Capacity(t *testing.T) {
	err := validateLogicalMetadata(media.LogicalFile{
		StoredSize:        math.MaxInt64,
		PlaintextSize:     math.MaxInt64,
		Encrypted:         true,
		EncryptionVersion: 1,
		Segments:          []media.Segment{{MsgID: 1, Size: math.MaxInt64}},
	})
	if !errors.Is(err, tdcrypto.ErrPlaintextTooLarge) {
		t.Fatalf("validateLogicalMetadata error = %v, want crypto.ErrPlaintextTooLarge", err)
	}
}

const encryptedTestChunkSize = 64 * 1024

func projectEncryptedSingle(t *testing.T, db *sql.DB, msgID int64, ciphertext, plaintext []byte) {
	t.Helper()
	// Kept as a projection operation so the test exercises the same metadata
	// path used by synced and newly committed encrypted files.
	projectFile(t, db, msgID, projection.Op{
		Type:              projection.OpFileUpload,
		Name:              "private.bin",
		FileSize:          int64(len(ciphertext)),
		Encrypted:         true,
		PlaintextSize:     int64(len(plaintext)),
		EncryptionVersion: 1,
	})
}

func encryptMountFixture(t *testing.T, plaintext, key []byte) []byte {
	t.Helper()
	var ciphertext bytes.Buffer
	if err := tdcrypto.EncryptStream(bytes.NewReader(plaintext), &ciphertext, key, int64(len(plaintext))); err != nil {
		t.Fatalf("EncryptStream: %v", err)
	}
	return ciphertext.Bytes()
}

func encryptedPattern(size int) []byte {
	plaintext := make([]byte, size)
	for index := range plaintext {
		plaintext[index] = byte((index*19 + 5) % 251)
	}
	return plaintext
}

func zeroed(value []byte) bool {
	for _, current := range value {
		if current != 0 {
			return false
		}
	}
	return true
}

var _ MasterKeyProvider = MasterKeyProviderFunc(nil)
