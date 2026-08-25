package mountwrite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	tdcrypto "TDrive/backend/crypto"
)

func TestDiskStagingStoreEncryptsWithoutPersistingPlaintext(t *testing.T) {
	t.Parallel()

	store := newEncryptedTestStore(t, 2<<20, 4<<20)
	key := bytes.Repeat([]byte{0x5a}, 32)
	plain := bytes.Repeat([]byte("private mounted content"), 4096)

	staged, err := store.Stage(context.Background(), StageRequest{
		OperationID:       "encrypted-stage",
		PlaintextSize:     int64(len(plain)),
		MaxBytes:          2 << 20,
		EncryptionVersion: EncryptionTDE1,
		MasterKey:         key,
	}, bytes.NewReader(plain))
	if err != nil {
		t.Fatalf("stage encrypted content: %v", err)
	}
	t.Cleanup(func() { _ = store.Remove(context.Background(), staged) })

	stored, err := os.ReadFile(staged.Path)
	if err != nil {
		t.Fatalf("read staged ciphertext: %v", err)
	}
	if bytes.Contains(stored, plain[:64]) {
		t.Fatal("staging file contains plaintext")
	}
	if !bytes.HasPrefix(stored, []byte("TDE1")) {
		t.Fatalf("staging prefix = %q, want TDE1", stored[:4])
	}
	if staged.EncryptionVersion != EncryptionTDE1 || staged.PlaintextSize != int64(len(plain)) {
		t.Fatalf("staged encryption metadata = %#v", staged)
	}
	if staged.StoredSize != tdcrypto.CiphertextSize(int64(len(plain))) || staged.StoredSize != int64(len(stored)) {
		t.Fatalf("stored size = %d (file %d), want %d", staged.StoredSize, len(stored), tdcrypto.CiphertextSize(int64(len(plain))))
	}
	if staged.SHA256 != ([sha256.Size]byte{}) {
		t.Fatalf("encrypted plaintext digest leaked: %x", staged.SHA256)
	}
	if got := sha256.Sum256(stored); staged.StoredSHA256 != got {
		t.Fatalf("stored digest = %x, want %x", staged.StoredSHA256, got)
	}

	var decrypted bytes.Buffer
	if _, err := tdcrypto.DecryptStream(bytes.NewReader(stored), &decrypted, key); err != nil {
		t.Fatalf("decrypt staged content: %v", err)
	}
	if !bytes.Equal(decrypted.Bytes(), plain) {
		t.Fatal("decrypted staged content differs")
	}
	if !bytes.Equal(key, bytes.Repeat([]byte{0x5a}, 32)) {
		t.Fatal("staging mutated caller-owned master key")
	}
}

func TestDiskStagingStoreEncryptsZeroAndExactChunkBoundary(t *testing.T) {
	t.Parallel()

	for _, size := range []int{0, 64 * 1024} {
		size := size
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			store := newEncryptedTestStore(t, 1<<20, 2<<20)
			plain := bytes.Repeat([]byte{0x31}, size)
			staged, err := store.Stage(context.Background(), StageRequest{
				OperationID:       "boundary",
				PlaintextSize:     int64(size),
				EncryptionVersion: EncryptionTDE1,
				MasterKey:         bytes.Repeat([]byte{0x22}, 32),
			}, bytes.NewReader(plain))
			if err != nil {
				t.Fatalf("stage size %d: %v", size, err)
			}
			if staged.StoredSize != tdcrypto.CiphertextSize(int64(size)) {
				t.Fatalf("stored size = %d, want %d", staged.StoredSize, tdcrypto.CiphertextSize(int64(size)))
			}
		})
	}
}

func TestDiskStagingStoreEncryptedLimitsAndFailureCleanup(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{0x33}, 32)
	t.Run("ciphertext exceeds object limit", func(t *testing.T) {
		store := newEncryptedTestStore(t, 100, 1000)
		_, err := store.Stage(context.Background(), StageRequest{
			OperationID:       "cipher-limit",
			PlaintextSize:     40,
			EncryptionVersion: EncryptionTDE1,
			MasterKey:         key,
		}, bytes.NewReader(bytes.Repeat([]byte{1}, 40)))
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("stage error = %v, want ErrTooLarge", err)
		}
		assertEmptyStagingRoot(t, store)
	})
	t.Run("ciphertext exceeds aggregate quota", func(t *testing.T) {
		store := newEncryptedTestStore(t, 1000, 100)
		_, err := store.Stage(context.Background(), StageRequest{
			OperationID:       "aggregate-limit",
			PlaintextSize:     40,
			EncryptionVersion: EncryptionTDE1,
			MasterKey:         key,
		}, bytes.NewReader(bytes.Repeat([]byte{1}, 40)))
		if !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("stage error = %v, want ErrQuotaExceeded", err)
		}
		assertEmptyStagingRoot(t, store)
	})
	t.Run("ciphertext size overflow", func(t *testing.T) {
		store := newEncryptedTestStore(t, math.MaxInt64, math.MaxInt64)
		_, err := store.Stage(context.Background(), StageRequest{
			OperationID:       "overflow",
			PlaintextSize:     math.MaxInt64,
			EncryptionVersion: EncryptionTDE1,
			MasterKey:         key,
		}, bytes.NewReader(nil))
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("stage error = %v, want ErrTooLarge", err)
		}
		assertEmptyStagingRoot(t, store)
	})
	t.Run("source error", func(t *testing.T) {
		store := newEncryptedTestStore(t, 1000, 1000)
		_, err := store.Stage(context.Background(), StageRequest{
			OperationID:       "source-error",
			PlaintextSize:     10,
			EncryptionVersion: EncryptionTDE1,
			MasterKey:         key,
		}, &errorReader{})
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("stage error = %v, want ErrUnavailable", err)
		}
		assertEmptyStagingRoot(t, store)
	})
	t.Run("canceled", func(t *testing.T) {
		store := newEncryptedTestStore(t, 1000, 1000)
		ctx, cancel := context.WithCancel(context.Background())
		_, err := store.Stage(ctx, StageRequest{
			OperationID:       "canceled",
			PlaintextSize:     10,
			EncryptionVersion: EncryptionTDE1,
			MasterKey:         key,
		}, &cancelAfterReadReader{
			Reader: bytes.NewReader(bytes.Repeat([]byte{1}, 10)),
			cancel: cancel,
		})
		if !errors.Is(err, ErrCanceled) {
			t.Fatalf("stage error = %v, want ErrCanceled", err)
		}
		assertEmptyStagingRoot(t, store)
	})
}

func TestDiskStagingStoreRejectsSameSizeCiphertextCorruption(t *testing.T) {
	t.Parallel()

	store := newEncryptedTestStore(t, 1000, 1000)
	staged, err := store.Stage(context.Background(), StageRequest{
		OperationID:       "tampered-ciphertext",
		PlaintextSize:     16,
		EncryptionVersion: EncryptionTDE1,
		MasterKey:         bytes.Repeat([]byte{0x65}, 32),
	}, bytes.NewReader(bytes.Repeat([]byte{0x41}, 16)))
	if err != nil {
		t.Fatalf("stage encrypted content: %v", err)
	}
	file, err := os.OpenFile(staged.Path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open staged ciphertext: %v", err)
	}
	if _, err := file.WriteAt([]byte{0xff}, 55); err != nil {
		_ = file.Close()
		t.Fatalf("tamper staged ciphertext: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close staged ciphertext: %v", err)
	}
	if _, err := store.Open(staged); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("open tampered stage error = %v, want ErrInvalidRequest", err)
	}
	if err := store.Remove(context.Background(), staged); err != nil {
		t.Fatalf("remove tampered stage: %v", err)
	}
}

func TestDiskStagingStoreRequiresKnownLengthAndValidPrivateKey(t *testing.T) {
	t.Parallel()

	store := newEncryptedTestStore(t, 1000, 1000)
	invalid := []StageRequest{
		{OperationID: "unknown", PlaintextSize: -1, EncryptionVersion: EncryptionTDE1, MasterKey: bytes.Repeat([]byte{1}, 32)},
		{OperationID: "short-key", PlaintextSize: 1, EncryptionVersion: EncryptionTDE1, MasterKey: bytes.Repeat([]byte{1}, 31)},
		{OperationID: "unknown-version", PlaintextSize: 1, EncryptionVersion: 2, MasterKey: bytes.Repeat([]byte{1}, 32)},
		{OperationID: "plaintext-key", PlaintextSize: 1, MasterKey: bytes.Repeat([]byte{1}, 32)},
	}
	for _, request := range invalid {
		if _, err := store.Stage(context.Background(), request, bytes.NewReader([]byte{1})); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("request %#v error = %v, want ErrInvalidRequest", request, err)
		}
	}
	assertEmptyStagingRoot(t, store)
}

func TestCoordinatorEncryptedPutPropagatesSafeDurableMetadata(t *testing.T) {
	t.Parallel()

	remote := &fakeRemote{}
	coordinator, journal, _ := newTestCoordinator(t, remote, &fakeInvalidator{})
	key := bytes.Repeat([]byte("K"), 32)
	plain := []byte("mounted encrypted content that must not leak")
	result, err := coordinator.Put(context.Background(), PutRequest{
		OperationID:       "encrypted-put",
		DriveID:           42,
		Name:              "private.txt",
		ContentLength:     int64(len(plain)),
		EncryptionVersion: EncryptionTDE1,
		MasterKey:         key,
	}, bytes.NewReader(plain))
	if err != nil {
		t.Fatalf("encrypted PUT: %v", err)
	}
	if result.Size != int64(len(plain)) || result.SHA256 != ([sha256.Size]byte{}) {
		t.Fatalf("result leaked encrypted digest or wrong size: %#v", result)
	}

	record := mustJournalRecord(t, journal, "encrypted-put")
	if record.Mutation.EncryptionVersion != EncryptionTDE1 || record.Staged == nil || record.Body == nil {
		t.Fatalf("journal metadata incomplete: %#v", record)
	}
	if record.Staged.SHA256 != ([sha256.Size]byte{}) || record.Body.SHA256 != ([sha256.Size]byte{}) {
		t.Fatal("journal contains encrypted plaintext digest")
	}
	if record.Staged.StoredSize != tdcrypto.CiphertextSize(int64(len(plain))) ||
		record.Body.StoredSize != record.Staged.StoredSize ||
		record.Body.StoredSHA256 != record.Staged.StoredSHA256 ||
		record.Body.EncryptionVersion != EncryptionTDE1 || !record.Body.Encrypted {
		t.Fatalf("journal staging/body metadata differs: staged=%#v body=%#v", record.Staged, record.Body)
	}
	remote.mu.Lock()
	lastUpload := remote.lastUpload
	lastCommit := remote.lastCommit
	remote.mu.Unlock()
	if lastUpload.PlaintextSize != int64(len(plain)) || lastUpload.StoredSize != record.Staged.StoredSize ||
		lastUpload.StoredSHA256 != record.Staged.StoredSHA256 || lastUpload.EncryptionVersion != EncryptionTDE1 || !lastUpload.Encrypted {
		t.Fatalf("hidden upload metadata = %#v", lastUpload)
	}
	if lastCommit.Body == nil || lastCommit.Body.StoredSHA256 != record.Staged.StoredSHA256 {
		t.Fatalf("commit body metadata = %#v", lastCommit.Body)
	}
	journalJSON, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal journal record: %v", err)
	}
	if bytes.Contains(journalJSON, key) || bytes.Contains(journalJSON, plain) {
		t.Fatalf("journal leaked master key or plaintext: %s", journalJSON)
	}
	if !bytes.Equal(key, bytes.Repeat([]byte("K"), 32)) {
		t.Fatal("coordinator mutated caller-owned master key")
	}
}

func TestDiskStagingStoreEncryptedLengthMismatchCleansCiphertext(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		want int64
		body []byte
	}{
		{name: "short", want: 8, body: []byte("short")},
		{name: "long", want: 4, body: []byte("longer")},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			store := newEncryptedTestStore(t, 1024, 2048)
			_, err := store.Stage(context.Background(), StageRequest{
				OperationID:       "length-" + test.name,
				PlaintextSize:     test.want,
				EncryptionVersion: EncryptionTDE1,
				MasterKey:         bytes.Repeat([]byte{0x44}, 32),
			}, bytes.NewReader(test.body))
			if !errors.Is(err, ErrLengthMismatch) {
				t.Fatalf("stage error = %v, want ErrLengthMismatch", err)
			}
			assertEmptyStagingRoot(t, store)
		})
	}
}

func TestEncryptedDurableMetadataRejectsPlaintextBeyondTDE1Capacity(t *testing.T) {
	storedHash := sha256.Sum256([]byte("stored"))
	staged := StagedObject{
		Key: "stage", PlaintextSize: math.MaxInt64, StoredSize: math.MaxInt64,
		StoredSHA256: storedHash, EncryptionVersion: EncryptionTDE1,
	}
	if err := validateStagedObject(staged); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("validateStagedObject() error = %v, want invalid request", err)
	}
	body := RemoteBody{
		PlaintextSize: math.MaxInt64, StoredSize: math.MaxInt64,
		StoredSHA256: storedHash, Encrypted: true, EncryptionVersion: EncryptionTDE1,
	}
	mutation := Mutation{Kind: MutationPut, EncryptionVersion: EncryptionTDE1}
	if err := validateRemoteBody(mutation, body); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("validateRemoteBody() error = %v, want invalid request", err)
	}
}

func newEncryptedTestStore(t *testing.T, maxObject, maxAggregate int64) *DiskStagingStore {
	t.Helper()
	store, err := NewDiskStagingStore(DiskStagingConfig{
		Root:              filepath.Join(t.TempDir(), "staging"),
		MaxObjectBytes:    maxObject,
		MaxAggregateBytes: maxAggregate,
		MaxConcurrent:     1,
	})
	if err != nil {
		t.Fatalf("new staging store: %v", err)
	}
	return store
}

func assertEmptyStagingRoot(t *testing.T, store *DiskStagingStore) {
	t.Helper()
	entries, err := os.ReadDir(store.Root())
	if err != nil || len(entries) != 0 || store.UsedBytes() != 0 {
		t.Fatalf("staging not clean: entries=%v used=%d err=%v", entries, store.UsedBytes(), err)
	}
}

type cancelAfterReadReader struct {
	*bytes.Reader
	cancel context.CancelFunc
}

func (r *cancelAfterReadReader) Read(buffer []byte) (int, error) {
	readCount, err := r.Reader.Read(buffer)
	r.cancel()
	return readCount, err
}
