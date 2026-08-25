package crypto

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRandomAccessDecryptorReadsChunkBoundaries(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	sizes := []int{0, 1, chunkSizePlain - 1, chunkSizePlain, chunkSizePlain + 1, 3*chunkSizePlain + 17}
	for _, size := range sizes {
		t.Run(testSizeName(size), func(t *testing.T) {
			plain := patternedPlaintext(size)
			ciphertext := encryptRandomAccessFixture(t, plain, key)
			decryptor, err := NewRandomAccessDecryptor(
				context.Background(),
				bytesContextReaderAt(ciphertext),
				int64(len(ciphertext)),
				key,
			)
			if err != nil {
				t.Fatalf("NewRandomAccessDecryptor: %v", err)
			}
			t.Cleanup(func() { _ = decryptor.Close() })
			if got := decryptor.Size(); got != int64(size) {
				t.Fatalf("Size = %d, want %d", got, size)
			}

			reads := []struct {
				off int64
				len int
			}{
				{off: 0, len: 0},
				{off: 0, len: 1},
				{off: maxInt64(0, int64(size)-1), len: 2},
				{off: maxInt64(0, chunkSizePlain-3), len: 9},
				{off: chunkSizePlain, len: chunkSizePlain + 31},
				{off: int64(size), len: 1},
			}
			for _, read := range reads {
				got := make([]byte, read.len)
				n, readErr := decryptor.ReadAt(context.Background(), got, read.off)
				want := make([]byte, read.len)
				wantN, wantErr := bytes.NewReader(plain).ReadAt(want, read.off)
				if n != wantN || !sameReaderAtError(readErr, wantErr) || !bytes.Equal(got[:n], want[:wantN]) {
					t.Fatalf(
						"ReadAt(off=%d,len=%d) = %x n=%d err=%v, want %x n=%d err=%v",
						read.off,
						read.len,
						got[:n],
						n,
						readErr,
						want[:wantN],
						wantN,
						wantErr,
					)
				}
			}
		})
	}
}

func TestRandomAccessDecryptorRejectsInvalidHeaderAndSize(t *testing.T) {
	key := bytes.Repeat([]byte{0x24}, 32)
	plain := patternedPlaintext(chunkSizePlain + 7)
	valid := encryptRandomAccessFixture(t, plain, key)

	tests := []struct {
		name       string
		mutate     func([]byte)
		storedSize int64
		want       error
	}{
		{
			name: "magic",
			mutate: func(ciphertext []byte) {
				ciphertext[0] ^= 0xff
			},
			storedSize: int64(len(valid)),
			want:       ErrBadMagic,
		},
		{
			name: "flags",
			mutate: func(ciphertext []byte) {
				ciphertext[4] = 1
			},
			storedSize: int64(len(valid)),
			want:       ErrUnsupported,
		},
		{
			name: "chunk size",
			mutate: func(ciphertext []byte) {
				ciphertext[5] = chunkSizeLog2 - 1
			},
			storedSize: int64(len(valid)),
			want:       ErrUnsupported,
		},
		{
			name: "declared plaintext",
			mutate: func(ciphertext []byte) {
				binary.LittleEndian.PutUint64(ciphertext[42:50], uint64(len(plain)+1))
			},
			storedSize: int64(len(valid)),
			want:       ErrCiphertextSize,
		},
		{
			name: "declared plaintext exceeds format capacity",
			mutate: func(ciphertext []byte) {
				binary.LittleEndian.PutUint64(ciphertext[42:50], ^uint64(0))
			},
			storedSize: int64(len(valid)),
			want:       ErrUnsupported,
		},
		{
			name:       "stored size too small",
			storedSize: int64(len(valid) - 1),
			want:       ErrCiphertextSize,
		},
		{
			name:       "stored size too large",
			storedSize: int64(len(valid) + 1),
			want:       ErrCiphertextSize,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ciphertext := append([]byte(nil), valid...)
			if tt.mutate != nil {
				tt.mutate(ciphertext)
			}
			decryptor, err := NewRandomAccessDecryptor(
				context.Background(),
				bytesContextReaderAt(ciphertext),
				tt.storedSize,
				key,
			)
			if decryptor != nil || !errors.Is(err, tt.want) {
				t.Fatalf("NewRandomAccessDecryptor = %v, %v; want nil, %v", decryptor, err, tt.want)
			}
		})
	}
}

func TestRandomAccessDecryptorRejectsInvalidMasterKeyBeforeStorageRead(t *testing.T) {
	t.Parallel()

	source := &countingContextReaderAt{}
	decryptor, err := NewRandomAccessDecryptor(context.Background(), source, streamHeaderLen, bytes.Repeat([]byte{1}, 31))
	if decryptor != nil || !errors.Is(err, ErrInvalidMasterKey) {
		t.Fatalf("NewRandomAccessDecryptor = %#v, %v; want nil, ErrInvalidMasterKey", decryptor, err)
	}
	if calls := source.calls.Load(); calls != 0 {
		t.Fatalf("ciphertext reads = %d, want 0", calls)
	}
}

func TestRandomAccessDecryptorAuthenticatesKeyAndEveryReadChunk(t *testing.T) {
	key := bytes.Repeat([]byte{0x51}, 32)
	wrongKey := bytes.Repeat([]byte{0x52}, 32)
	plain := patternedPlaintext(3*chunkSizePlain + 5)
	valid := encryptRandomAccessFixture(t, plain, key)

	if decryptor, err := NewRandomAccessDecryptor(
		context.Background(),
		bytesContextReaderAt(valid),
		int64(len(valid)),
		wrongKey,
	); decryptor != nil || !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("wrong key = %v, %v; want nil, ErrAuthFailed", decryptor, err)
	}

	tamperedFinal := append([]byte(nil), valid...)
	tamperedFinal[len(tamperedFinal)-1] ^= 0x80
	if decryptor, err := NewRandomAccessDecryptor(
		context.Background(),
		bytesContextReaderAt(tamperedFinal),
		int64(len(tamperedFinal)),
		key,
	); decryptor != nil || !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("tampered final chunk = %v, %v; want nil, ErrAuthFailed", decryptor, err)
	}

	tamperedMiddle := append([]byte(nil), valid...)
	tamperedMiddle[streamHeaderLen+chunkSizePlain+streamTagLen+9] ^= 0x40
	decryptor, err := NewRandomAccessDecryptor(
		context.Background(),
		bytesContextReaderAt(tamperedMiddle),
		int64(len(tamperedMiddle)),
		key,
	)
	if err != nil {
		t.Fatalf("NewRandomAccessDecryptor with non-final tamper: %v", err)
	}
	t.Cleanup(func() { _ = decryptor.Close() })
	buffer := make([]byte, 32)
	if n, readErr := decryptor.ReadAt(context.Background(), buffer, chunkSizePlain+1); n != 0 || !errors.Is(readErr, ErrAuthFailed) {
		t.Fatalf("tampered chunk read = n %d err %v, want 0/ErrAuthFailed", n, readErr)
	}
}

func TestRandomAccessDecryptorRejectsTruncatedFinalChunk(t *testing.T) {
	key := bytes.Repeat([]byte{0x63}, 32)
	plain := patternedPlaintext(2 * chunkSizePlain)
	valid := encryptRandomAccessFixture(t, plain, key)
	truncated := valid[:len(valid)-1]

	decryptor, err := NewRandomAccessDecryptor(
		context.Background(),
		bytesContextReaderAt(truncated),
		int64(len(valid)),
		key,
	)
	if decryptor != nil || !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("truncated final chunk = %v, %v; want nil, ErrAuthFailed", decryptor, err)
	}
}

func TestRandomAccessDecryptorHonorsCancellation(t *testing.T) {
	key := bytes.Repeat([]byte{0x74}, 32)
	plain := patternedPlaintext(chunkSizePlain + 1)
	ciphertext := encryptRandomAccessFixture(t, plain, key)

	ctx, cancel := context.WithCancel(context.Background())
	source := &blockingFinalReader{
		header:           append([]byte(nil), ciphertext[:streamHeaderLen]...),
		finalReadStarted: make(chan struct{}),
	}
	result := make(chan error, 1)
	go func() {
		_, err := NewRandomAccessDecryptor(ctx, source, int64(len(ciphertext)), key)
		result <- err
	}()
	<-source.finalReadStarted
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("constructor cancellation error = %v, want context.Canceled", err)
	}

	decryptor, err := NewRandomAccessDecryptor(
		context.Background(),
		bytesContextReaderAt(ciphertext),
		int64(len(ciphertext)),
		key,
	)
	if err != nil {
		t.Fatalf("NewRandomAccessDecryptor: %v", err)
	}
	t.Cleanup(func() { _ = decryptor.Close() })
	canceled, cancelRead := context.WithCancel(context.Background())
	cancelRead()
	if n, readErr := decryptor.ReadAt(canceled, make([]byte, 1), 0); n != 0 || !errors.Is(readErr, context.Canceled) {
		t.Fatalf("canceled ReadAt = n %d err %v, want 0/context.Canceled", n, readErr)
	}
}

func TestRandomAccessDecryptorSupportsConcurrentReadsWithBoundedRequests(t *testing.T) {
	key := bytes.Repeat([]byte{0x85}, 32)
	plain := patternedPlaintext(8*chunkSizePlain + 101)
	ciphertext := encryptRandomAccessFixture(t, plain, key)
	source := &measuringContextReaderAt{data: ciphertext}
	decryptor, err := NewRandomAccessDecryptor(context.Background(), source, int64(len(ciphertext)), key)
	if err != nil {
		t.Fatalf("NewRandomAccessDecryptor: %v", err)
	}
	t.Cleanup(func() { _ = decryptor.Close() })

	var wait sync.WaitGroup
	errorsFound := make(chan error, 32)
	for worker := range 32 {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			off := int64((worker * 7919) % (len(plain) - 2*chunkSizePlain))
			got := make([]byte, 2*chunkSizePlain+13)
			n, readErr := decryptor.ReadAt(context.Background(), got, off)
			if readErr != nil {
				errorsFound <- readErr
				return
			}
			if !bytes.Equal(got[:n], plain[off:off+int64(n)]) {
				errorsFound <- errors.New("concurrent plaintext mismatch")
			}
		}(worker)
	}
	wait.Wait()
	close(errorsFound)
	for readErr := range errorsFound {
		t.Errorf("concurrent ReadAt: %v", readErr)
	}
	if got := source.maxRequest(); got > chunkSizePlain+streamTagLen {
		t.Fatalf("largest source request = %d, want <= %d", got, chunkSizePlain+streamTagLen)
	}
}

func TestRandomAccessDecryptorCloseClearsKeyAndRejectsReads(t *testing.T) {
	key := bytes.Repeat([]byte{0x96}, 32)
	plain := []byte("secret")
	ciphertext := encryptRandomAccessFixture(t, plain, key)
	decryptor, err := NewRandomAccessDecryptor(
		context.Background(),
		bytesContextReaderAt(ciphertext),
		int64(len(ciphertext)),
		key,
	)
	if err != nil {
		t.Fatalf("NewRandomAccessDecryptor: %v", err)
	}
	retainedKey := decryptor.subkey
	if err := decryptor.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := decryptor.Close(); err != nil {
		t.Fatalf("Close again: %v", err)
	}
	if !allZero(retainedKey) {
		t.Fatal("Close did not clear the owned derived key")
	}
	if n, readErr := decryptor.ReadAt(context.Background(), make([]byte, 1), 0); n != 0 || !errors.Is(readErr, ErrDecryptorClosed) {
		t.Fatalf("ReadAt after Close = n %d err %v, want 0/ErrDecryptorClosed", n, readErr)
	}
}

func TestRandomAccessDecryptorCloseCancelsBlockedReadBeforeClearingKey(t *testing.T) {
	key := bytes.Repeat([]byte{0xa8}, 32)
	plain := patternedPlaintext(2*chunkSizePlain + 1)
	ciphertext := encryptRandomAccessFixture(t, plain, key)
	source := &blockingChunkReader{
		data:    ciphertext,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	decryptor, err := NewRandomAccessDecryptor(context.Background(), source, int64(len(ciphertext)), key)
	if err != nil {
		t.Fatalf("NewRandomAccessDecryptor: %v", err)
	}
	retainedKey := decryptor.subkey
	source.block.Store(true)

	readResult := make(chan error, 1)
	go func() {
		_, readErr := decryptor.ReadAt(context.Background(), make([]byte, 1), 0)
		readResult <- readErr
	}()
	<-source.started
	closeResult := make(chan error, 1)
	go func() { closeResult <- decryptor.Close() }()

	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case closeErr := <-closeResult:
		if closeErr != nil {
			t.Fatalf("Close: %v", closeErr)
		}
	case <-timer.C:
		close(source.release)
		<-readResult
		<-closeResult
		t.Fatal("Close did not cancel the blocked ciphertext read")
	}
	if readErr := <-readResult; !errors.Is(readErr, ErrDecryptorClosed) {
		t.Fatalf("blocked ReadAt error = %v, want ErrDecryptorClosed", readErr)
	}
	if !allZero(retainedKey) {
		t.Fatal("Close returned before clearing the derived key")
	}
}

func TestRandomAccessDecryptorValidatesLifecycleInputs(t *testing.T) {
	key := bytes.Repeat([]byte{0xb8}, 32)
	ciphertext := encryptRandomAccessFixture(t, []byte("input checks"), key)

	if decryptor, err := NewRandomAccessDecryptor(nil, bytesContextReaderAt(ciphertext), int64(len(ciphertext)), key); decryptor != nil || !errors.Is(err, ErrNilContext) {
		t.Fatalf("nil context = %v, %v; want nil/ErrNilContext", decryptor, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if decryptor, err := NewRandomAccessDecryptor(canceled, bytesContextReaderAt(ciphertext), int64(len(ciphertext)), key); decryptor != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context = %v, %v; want nil/context.Canceled", decryptor, err)
	}
	if decryptor, err := NewRandomAccessDecryptor(context.Background(), nil, int64(len(ciphertext)), key); decryptor != nil || err == nil {
		t.Fatalf("nil source = %v, %v; want nil/error", decryptor, err)
	}
	if decryptor, err := NewRandomAccessDecryptor(context.Background(), bytesContextReaderAt(ciphertext), streamHeaderLen-1, key); decryptor != nil || !errors.Is(err, ErrShortHeader) {
		t.Fatalf("short stored size = %v, %v; want nil/ErrShortHeader", decryptor, err)
	}
	if decryptor, err := NewRandomAccessDecryptor(context.Background(), bytesContextReaderAt(ciphertext[:10]), int64(len(ciphertext)), key); decryptor != nil || !errors.Is(err, ErrShortHeader) {
		t.Fatalf("short source header = %v, %v; want nil/ErrShortHeader", decryptor, err)
	}

	decryptor, err := NewRandomAccessDecryptor(
		context.Background(),
		bytesContextReaderAt(ciphertext),
		int64(len(ciphertext)),
		key,
	)
	if err != nil {
		t.Fatalf("NewRandomAccessDecryptor: %v", err)
	}
	defer decryptor.Close()
	if n, readErr := decryptor.ReadAt(nil, make([]byte, 1), 0); n != 0 || !errors.Is(readErr, ErrNilContext) {
		t.Fatalf("nil ReadAt context = n %d err %v", n, readErr)
	}
	if n, readErr := decryptor.ReadAt(context.Background(), make([]byte, 1), -1); n != 0 || readErr == nil {
		t.Fatalf("negative ReadAt = n %d err %v", n, readErr)
	}
	if got := (*RandomAccessDecryptor)(nil).Size(); got != 0 {
		t.Fatalf("nil Size = %d, want 0", got)
	}
	if err := (*RandomAccessDecryptor)(nil).Close(); err != nil {
		t.Fatalf("nil Close: %v", err)
	}
	if n, readErr := (*RandomAccessDecryptor)(nil).ReadAt(context.Background(), make([]byte, 1), 0); n != 0 || !errors.Is(readErr, ErrDecryptorClosed) {
		t.Fatalf("nil ReadAt = n %d err %v", n, readErr)
	}
	zeroValue := &RandomAccessDecryptor{}
	if n, readErr := zeroValue.ReadAt(context.Background(), make([]byte, 1), 0); n != 0 || !errors.Is(readErr, ErrDecryptorClosed) {
		t.Fatalf("zero-value ReadAt = n %d err %v", n, readErr)
	}
	if err := zeroValue.Close(); err != nil {
		t.Fatalf("zero-value Close: %v", err)
	}
}

func TestRandomAccessHelpersRejectImpossibleAndStalledReads(t *testing.T) {
	if _, ok := ciphertextChunkOffset(-1); ok {
		t.Fatal("ciphertextChunkOffset accepted a negative index")
	}
	if _, ok := ciphertextChunkOffset(int64(^uint64(0) >> 1)); ok {
		t.Fatal("ciphertextChunkOffset accepted an overflowing index")
	}
	if err := readContextAtFull(context.Background(), noProgressContextReader{}, make([]byte, 1), 0); !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("stalled read error = %v, want io.ErrNoProgress", err)
	}
	if err := readContextAtFull(context.Background(), overReportingContextReader{}, make([]byte, 1), 0); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("over-reporting read error = %v, want io.ErrUnexpectedEOF", err)
	}
}

func FuzzRandomAccessDecryptorChunkBoundaries(f *testing.F) {
	for _, seed := range []struct {
		size, off, read uint32
	}{
		{0, 0, 1},
		{chunkSizePlain - 1, chunkSizePlain - 2, 4},
		{chunkSizePlain, chunkSizePlain - 1, 3},
		{chunkSizePlain + 1, chunkSizePlain - 1, chunkSizePlain + 2},
		{3*chunkSizePlain + 7, 2*chunkSizePlain - 4, chunkSizePlain + 11},
	} {
		f.Add(seed.size, seed.off, seed.read)
	}
	key := bytes.Repeat([]byte{0xa7}, 32)
	f.Fuzz(func(t *testing.T, rawSize, rawOffset, rawRead uint32) {
		size := int(rawSize % (4*chunkSizePlain + 33))
		plain := patternedPlaintext(size)
		ciphertext := encryptRandomAccessFixture(t, plain, key)
		decryptor, err := NewRandomAccessDecryptor(
			context.Background(),
			bytesContextReaderAt(ciphertext),
			int64(len(ciphertext)),
			key,
		)
		if err != nil {
			t.Fatalf("NewRandomAccessDecryptor: %v", err)
		}
		defer decryptor.Close()

		off := int64(rawOffset % uint32(size+2))
		readLen := int(rawRead % (2*chunkSizePlain + 33))
		got := make([]byte, readLen)
		n, readErr := decryptor.ReadAt(context.Background(), got, off)
		want := make([]byte, readLen)
		wantN, wantErr := bytes.NewReader(plain).ReadAt(want, off)
		if n != wantN || !sameReaderAtError(readErr, wantErr) || !bytes.Equal(got[:n], want[:wantN]) {
			t.Fatalf("ReadAt mismatch: n=%d/%d err=%v/%v", n, wantN, readErr, wantErr)
		}
	})
}

type bytesContextReaderAt []byte

func (reader bytesContextReaderAt) ReadAt(ctx context.Context, dst []byte, off int64) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return bytes.NewReader(reader).ReadAt(dst, off)
}

type blockingFinalReader struct {
	header           []byte
	finalReadStarted chan struct{}
	once             sync.Once
}

func (reader *blockingFinalReader) ReadAt(ctx context.Context, dst []byte, off int64) (int, error) {
	if off == 0 {
		return copy(dst, reader.header), nil
	}
	reader.once.Do(func() {
		close(reader.finalReadStarted)
	})
	<-ctx.Done()
	return 0, ctx.Err()
}

type measuringContextReaderAt struct {
	data []byte
	mu   sync.Mutex
	max  int
}

type blockingChunkReader struct {
	data    []byte
	block   atomic.Bool
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (reader *blockingChunkReader) ReadAt(ctx context.Context, dst []byte, off int64) (int, error) {
	if reader.block.Load() && off == streamHeaderLen {
		reader.once.Do(func() { close(reader.started) })
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-reader.release:
		}
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return bytes.NewReader(reader.data).ReadAt(dst, off)
}

type noProgressContextReader struct{}

func (noProgressContextReader) ReadAt(context.Context, []byte, int64) (int, error) {
	return 0, nil
}

type overReportingContextReader struct{}

func (overReportingContextReader) ReadAt(_ context.Context, dst []byte, _ int64) (int, error) {
	return len(dst) + 1, nil
}

type countingContextReaderAt struct {
	calls atomic.Int32
}

func (reader *countingContextReaderAt) ReadAt(context.Context, []byte, int64) (int, error) {
	reader.calls.Add(1)
	return 0, io.EOF
}

func (reader *measuringContextReaderAt) ReadAt(ctx context.Context, dst []byte, off int64) (int, error) {
	reader.mu.Lock()
	if len(dst) > reader.max {
		reader.max = len(dst)
	}
	reader.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return bytes.NewReader(reader.data).ReadAt(dst, off)
}

func (reader *measuringContextReaderAt) maxRequest() int {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	return reader.max
}

func encryptRandomAccessFixture(t *testing.T, plaintext, key []byte) []byte {
	t.Helper()
	var ciphertext bytes.Buffer
	if err := EncryptStream(bytes.NewReader(plaintext), &ciphertext, key, int64(len(plaintext))); err != nil {
		t.Fatalf("EncryptStream: %v", err)
	}
	return ciphertext.Bytes()
}

func patternedPlaintext(size int) []byte {
	plaintext := make([]byte, size)
	for index := range plaintext {
		plaintext[index] = byte((index*31 + 17) % 251)
	}
	return plaintext
}

func testSizeName(size int) string {
	return "bytes-" + strconv.Itoa(size)
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func sameReaderAtError(got, want error) bool {
	return (got == nil && want == nil) || (errors.Is(got, io.EOF) && errors.Is(want, io.EOF))
}

func allZero(value []byte) bool {
	for _, current := range value {
		if current != 0 {
			return false
		}
	}
	return true
}
