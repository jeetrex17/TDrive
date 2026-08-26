package media

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
	"TDrive/backend/thumbnail"
)

func TestMediaOpenServesEncryptedFileAsPlaintext(t *testing.T) {
	db := newResolverTestDB(t)
	key := bytes.Repeat([]byte{0x31}, 32)
	plaintext := testBytes(2*65536 + 37)
	ciphertext := encryptMediaFixture(t, plaintext, key)
	projectEncryptedMedia(t, db, 10, "secret.mp4", ciphertext, plaintext)
	ranges := newMediaRangeFake(map[int64][]byte{10: ciphertext})
	providedKey := append([]byte(nil), key...)
	svc := NewService(Config{
		DB:     db,
		Peers:  staticPeerResolver{peer: ranges.peer},
		Ranges: ranges,
		Keys: MasterKeyProviderFunc(func(ctx context.Context, channelID int64) ([]byte, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if channelID != testChannelID {
				t.Fatalf("key channel = %d, want %d", channelID, testChannelID)
			}
			return providedKey, nil
		}),
	})
	defer svc.Close()

	opened, err := svc.Open(context.Background(), testChannelID, 10)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !zeroBytes(providedKey) {
		t.Fatal("Open retained provider-owned key bytes")
	}
	if got := opened.Info.PlaintextSize; got != int64(len(plaintext)) {
		t.Fatalf("PlaintextSize = %d, want %d", got, len(plaintext))
	}

	req, _ := http.NewRequest(http.MethodGet, opened.URL, nil)
	req.Header.Set("Range", "bytes=65525-65608")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET encrypted range: %v", err)
	}
	got, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read encrypted response: %v", err)
	}
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", resp.StatusCode)
	}
	if want := fmt.Sprintf("bytes 65525-65608/%d", len(plaintext)); resp.Header.Get("Content-Range") != want {
		t.Fatalf("Content-Range = %q, want %q", resp.Header.Get("Content-Range"), want)
	}
	if !bytes.Equal(got, plaintext[65525:65609]) {
		t.Fatal("encrypted range plaintext mismatch")
	}
}

func TestMediaOpenReadsEncryptedMultipartAcrossStoredSegments(t *testing.T) {
	db := newResolverTestDB(t)
	key := bytes.Repeat([]byte{0x42}, 32)
	plaintext := testBytes(3*65536 + 19)
	ciphertext := encryptMediaFixture(t, plaintext, key)
	cuts := []int{0, 23, 65531, 2*65536 + 81, len(ciphertext)}
	bodies := make(map[int64][]byte, len(cuts)-1)
	parts := make([]partSpec, 0, len(cuts)-1)
	for i := 0; i < len(cuts)-1; i++ {
		msgID := int64(100 + i)
		body := append([]byte(nil), ciphertext[cuts[i]:cuts[i+1]]...)
		bodies[msgID] = body
		parts = append(parts, partSpec{msgID: msgID, size: int64(len(body))})
	}
	applyMultipart(t, db, "encrypted-media", parts, 200, projection.Op{
		Type:              projection.OpFileManifest,
		UploadUUID:        "encrypted-media",
		Parent:            projection.RootParent,
		Name:              "secret.mkv",
		FileSize:          int64(len(ciphertext)),
		PartCount:         len(parts),
		Encrypted:         true,
		PlaintextSize:     int64(len(plaintext)),
		EncryptionVersion: 1,
	})

	ranges := newMediaRangeFake(bodies)
	svc := NewService(Config{
		DB:     db,
		Peers:  staticPeerResolver{peer: ranges.peer},
		Ranges: ranges,
		Keys: MasterKeyProviderFunc(func(context.Context, int64) ([]byte, error) {
			return append([]byte(nil), key...), nil
		}),
	})
	defer svc.Close()

	opened, err := svc.Open(context.Background(), testChannelID, 200)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, opened.URL, nil)
	req.Header.Set("Range", "bytes=65519-131107")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET multipart encrypted range: %v", err)
	}
	got, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read multipart encrypted response: %v", err)
	}
	if !bytes.Equal(got, plaintext[65519:131108]) {
		t.Fatal("multipart encrypted range plaintext mismatch")
	}
}

func TestMediaOpenRejectsEncryptedFilesWithoutKeyProvider(t *testing.T) {
	db := newResolverTestDB(t)
	key := bytes.Repeat([]byte{0x53}, 32)
	plaintext := testBytes(100)
	ciphertext := encryptMediaFixture(t, plaintext, key)
	projectEncryptedMedia(t, db, 10, "secret.mp4", ciphertext, plaintext)
	ranges := newMediaRangeFake(map[int64][]byte{10: ciphertext})
	svc := NewService(Config{DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges})
	defer svc.Close()

	_, err := svc.Open(context.Background(), testChannelID, 10)
	if !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("err = %v, want ErrKeyUnavailable", err)
	}
}

func TestEncryptedMediaPublicationRejectsStaleGenerationWithoutHoldingGateDuringSetup(t *testing.T) {
	db := newResolverTestDB(t)
	key := bytes.Repeat([]byte{0x54}, 32)
	plaintext := testBytes(100)
	ciphertext := encryptMediaFixture(t, plaintext, key)
	projectEncryptedMedia(t, db, 10, "secret.mp4", ciphertext, plaintext)
	ranges := newMediaRangeFake(map[int64][]byte{10: ciphertext})

	var gate sync.RWMutex
	var generation atomic.Uint64
	svc := NewService(Config{
		DB:                       db,
		Peers:                    staticPeerResolver{peer: ranges.peer},
		Ranges:                   ranges,
		EncryptionOpenGate:       gate.RLocker(),
		EncryptionOpenGeneration: generation.Load,
		Keys: MasterKeyProviderFunc(func(context.Context, int64) ([]byte, error) {
			if !gate.TryLock() {
				t.Fatal("encrypted media setup held the publication gate during key acquisition")
			}
			gate.Unlock()
			generation.Add(2) // Simulate a completed vault-lock transition.
			return append([]byte(nil), key...), nil
		}),
	})
	defer svc.Close()

	if _, err := svc.Open(context.Background(), testChannelID, 10); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("Open after generation change = %v, want ErrKeyUnavailable", err)
	}
	if got := len(svc.server.sessions); got != 0 {
		t.Fatalf("published sessions = %d, want 0", got)
	}
}

func TestMediaOpenRejectsWrongKeyAndTamperedEncryptedTail(t *testing.T) {
	db := newResolverTestDB(t)
	key := bytes.Repeat([]byte{0x64}, 32)
	plaintext := testBytes(65536 + 9)
	ciphertext := encryptMediaFixture(t, plaintext, key)
	projectEncryptedMedia(t, db, 10, "secret.mp4", ciphertext, plaintext)

	t.Run("wrong key", func(t *testing.T) {
		ranges := newMediaRangeFake(map[int64][]byte{10: ciphertext})
		svc := NewService(Config{
			DB:     db,
			Peers:  staticPeerResolver{peer: ranges.peer},
			Ranges: ranges,
			Keys: MasterKeyProviderFunc(func(context.Context, int64) ([]byte, error) {
				return bytes.Repeat([]byte{0x65}, 32), nil
			}),
		})
		defer svc.Close()
		if _, err := svc.Open(context.Background(), testChannelID, 10); !errors.Is(err, tdcrypto.ErrAuthFailed) {
			t.Fatalf("Open error = %v, want crypto.ErrAuthFailed", err)
		}
	})

	t.Run("tail corruption", func(t *testing.T) {
		tampered := append([]byte(nil), ciphertext...)
		tampered[len(tampered)-1] ^= 0x80
		ranges := newMediaRangeFake(map[int64][]byte{10: tampered})
		svc := NewService(Config{
			DB:     db,
			Peers:  staticPeerResolver{peer: ranges.peer},
			Ranges: ranges,
			Keys: MasterKeyProviderFunc(func(context.Context, int64) ([]byte, error) {
				return append([]byte(nil), key...), nil
			}),
		})
		defer svc.Close()
		if _, err := svc.Open(context.Background(), testChannelID, 10); !errors.Is(err, tdcrypto.ErrAuthFailed) {
			t.Fatalf("Open error = %v, want crypto.ErrAuthFailed", err)
		}
	})
}

func TestEncryptedMediaSessionRejectsTamperedReadChunkAndCloseIsIdempotent(t *testing.T) {
	key := bytes.Repeat([]byte{0x75}, 32)
	plaintext := testBytes(2*65536 + 9)
	ciphertext := encryptMediaFixture(t, plaintext, key)
	tampered := append([]byte(nil), ciphertext...)
	tampered[50+65536+16+7] ^= 0x40
	ranges := newMediaRangeFake(map[int64][]byte{10: tampered})
	ref := tgclient.DocumentRef{
		Peer:  ranges.peer,
		MsgID: 10,
		Size:  int64(len(tampered)),
		Name:  "secret.mp4",
	}
	file := LogicalFile{
		ChannelID:         testChannelID,
		FileID:            10,
		Name:              "secret.mp4",
		StoredSize:        int64(len(tampered)),
		PlaintextSize:     int64(len(plaintext)),
		Encrypted:         true,
		EncryptionVersion: 1,
		Segments:          []Segment{{MsgID: 10, Size: int64(len(tampered))}},
	}
	session, err := newSession(file, []resolvedSegment{{start: 0, size: int64(len(tampered)), ref: ref}}, ranges, nil, nil, SessionOptions{
		EnableVideoThumbnails: true,
		MasterKey:             append([]byte(nil), key...),
	})
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	if n, readErr := session.ReadAt(context.Background(), make([]byte, 1), 65536+1); n != 0 || !errors.Is(readErr, tdcrypto.ErrAuthFailed) {
		t.Fatalf("ReadAt tampered chunk = n %d err %v, want 0/ErrAuthFailed", n, readErr)
	}
	session.Close()
	session.Close()
	if n, readErr := session.ReadAt(context.Background(), make([]byte, 1), 0); n != 0 || !errors.Is(readErr, ErrSessionNotFound) {
		t.Fatalf("ReadAt after Close = n %d err %v, want 0/ErrSessionNotFound", n, readErr)
	}
}

func TestMediaServiceOpenResultForTokenSnapshotsActiveSession(t *testing.T) {
	db := newResolverTestDB(t)
	body := testBytes(512)
	mustApplyOp(t, db, 10, projection.Op{
		Type:     projection.OpFileUpload,
		Parent:   projection.RootParent,
		Name:     "clip.mp4",
		FileSize: int64(len(body)),
	})
	ranges := newMediaRangeFake(map[int64][]byte{10: body})
	svc := NewService(Config{DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges})
	defer svc.Close()
	opened, err := svc.Open(context.Background(), testChannelID, 10)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	got, err := svc.OpenResultForToken(opened.Token)
	if err != nil {
		t.Fatalf("OpenResultForToken: %v", err)
	}
	if got.Token != opened.Token || got.URL != opened.URL || got.Name != opened.Name || got.MimeType != opened.MimeType || got.Kind != opened.Kind {
		t.Fatalf("snapshot = %+v, want fields from %+v", got, opened)
	}
	if got.Info.FileID != opened.Info.FileID || got.Info.PlaintextSize != opened.Info.PlaintextSize {
		t.Fatalf("snapshot info = %+v, want %+v", got.Info, opened.Info)
	}
	session := svc.server.session(opened.Token)
	if session == nil {
		t.Fatal("active session was not registered")
	}
	session.Close()
	if _, err := svc.OpenResultForToken(opened.Token); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("OpenResultForToken for closed registered session = %v, want ErrSessionNotFound", err)
	}
	if err := svc.CloseSession(opened.Token); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	if _, err := svc.OpenResultForToken(opened.Token); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("OpenResultForToken after close = %v, want ErrSessionNotFound", err)
	}
}

func TestEncryptedVideoSessionDisablesPersistentThumbnailCache(t *testing.T) {
	key := bytes.Repeat([]byte{0x79}, 32)
	plaintext := testBytes(1024)
	ciphertext := encryptMediaFixture(t, plaintext, key)
	ranges := newMediaRangeFake(map[int64][]byte{10: ciphertext})
	ref := tgclient.DocumentRef{
		Peer: ranges.peer, MsgID: 10, Size: int64(len(ciphertext)), Name: "secret.mkv",
	}
	file := LogicalFile{
		ChannelID: testChannelID, FileID: 10, Revision: 1, Name: "secret.mkv",
		StoredSize: int64(len(ciphertext)), PlaintextSize: int64(len(plaintext)),
		Encrypted: true, EncryptionVersion: 1,
		Segments: []Segment{{MsgID: 10, Size: int64(len(ciphertext))}},
	}
	persistent := thumbnail.NewCache(t.TempDir(), 1<<20)
	session, err := newSession(
		file,
		[]resolvedSegment{{start: 0, size: int64(len(ciphertext)), ref: ref}},
		ranges,
		persistent,
		nil,
		SessionOptions{EnableVideoThumbnails: true, MasterKey: append([]byte(nil), key...)},
	)
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	defer session.Close()
	if session.thumbs == nil {
		t.Fatal("encrypted video thumbnailer was not created")
	}
	if session.thumbs.cache != nil {
		t.Fatal("encrypted video thumbnailer retained the persistent plaintext cache")
	}
}

func TestMediaServiceCloseEncryptedSessionsLeavesPlainSessions(t *testing.T) {
	db := newResolverTestDB(t)
	key := bytes.Repeat([]byte{0x7a}, 32)
	plaintext := testBytes(128)
	ciphertext := encryptMediaFixture(t, plaintext, key)
	projectEncryptedMedia(t, db, 10, "secret.mp4", ciphertext, plaintext)
	publicBody := testBytes(256)
	mustApplyOp(t, db, 11, projection.Op{
		Type:     projection.OpFileUpload,
		Parent:   projection.RootParent,
		Name:     "public.mp4",
		FileSize: int64(len(publicBody)),
	})
	ranges := newMediaRangeFake(map[int64][]byte{10: ciphertext, 11: publicBody})
	svc := NewService(Config{
		DB:     db,
		Peers:  staticPeerResolver{peer: ranges.peer},
		Ranges: ranges,
		Keys: MasterKeyProviderFunc(func(context.Context, int64) ([]byte, error) {
			return append([]byte(nil), key...), nil
		}),
	})
	defer svc.Close()

	encrypted, err := svc.Open(context.Background(), testChannelID, 10)
	if err != nil {
		t.Fatalf("Open encrypted: %v", err)
	}
	plain, err := svc.Open(context.Background(), testChannelID, 11)
	if err != nil {
		t.Fatalf("Open plain: %v", err)
	}

	svc.CloseEncryptedSessions()
	if _, err := svc.OpenResultForToken(encrypted.Token); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("encrypted token after close = %v, want ErrSessionNotFound", err)
	}
	if got, err := svc.OpenResultForToken(plain.Token); err != nil || got.Token != plain.Token {
		t.Fatalf("plain token after encrypted close = %+v, %v", got, err)
	}
}

func projectEncryptedMedia(t *testing.T, db *sql.DB, msgID int64, name string, ciphertext, plaintext []byte) {
	t.Helper()
	mustApplyOp(t, db, msgID, projection.Op{
		Type:              projection.OpFileUpload,
		Parent:            projection.RootParent,
		Name:              name,
		FileSize:          int64(len(ciphertext)),
		Encrypted:         true,
		PlaintextSize:     int64(len(plaintext)),
		EncryptionVersion: 1,
	})
}

func encryptMediaFixture(t *testing.T, plaintext, key []byte) []byte {
	t.Helper()
	var ciphertext bytes.Buffer
	if err := tdcrypto.EncryptStream(bytes.NewReader(plaintext), &ciphertext, key, int64(len(plaintext))); err != nil {
		t.Fatalf("EncryptStream: %v", err)
	}
	return ciphertext.Bytes()
}

func zeroBytes(data []byte) bool {
	for _, b := range data {
		if b != 0 {
			return false
		}
	}
	return true
}
