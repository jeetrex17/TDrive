package file

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/projection"
	tdsync "TDrive/backend/sync"
	"TDrive/backend/tgclient"
)

type hiddenTestPeerResolver struct {
	peer tgclient.InputPeer
	err  error
}

type hiddenCountingPeerResolver struct {
	peer  tgclient.InputPeer
	calls int
}

func (r *hiddenCountingPeerResolver) ResolvePeer(context.Context, int64) (tgclient.InputPeer, error) {
	r.calls++
	return r.peer, nil
}

func (r hiddenTestPeerResolver) ResolvePeer(context.Context, int64) (tgclient.InputPeer, error) {
	return r.peer, r.err
}

type zeroMessageIDClient struct {
	*tgclient.Fake
}

func (c *zeroMessageIDClient) SendFileWithRandomID(context.Context, tgclient.InputPeer, io.Reader, string, string, int64, func(int64, int64), int64) (tgclient.SendFileResult, error) {
	return tgclient.SendFileResult{}, nil
}

type failNthFileClient struct {
	*tgclient.Fake
	failAt int
	calls  int
	err    error
}

func (c *failNthFileClient) SendFileWithRandomID(ctx context.Context, peer tgclient.InputPeer, source io.Reader, name, caption string, size int64, progress func(int64, int64), randomID int64) (tgclient.SendFileResult, error) {
	c.calls++
	if c.calls == c.failAt {
		return tgclient.SendFileResult{}, c.err
	}
	return c.Fake.SendFileWithRandomID(ctx, peer, source, name, caption, size, progress, randomID)
}

type deleteFailureClient struct {
	*tgclient.Fake
	err error
}

func (c *deleteFailureClient) DeleteMessages(context.Context, tgclient.InputPeer, []int64) error {
	return c.err
}

type failingSeeker struct {
	seekCalls int
	failAt    int
}

func (*failingSeeker) Read([]byte) (int, error) { return 0, io.EOF }

func (s *failingSeeker) Seek(int64, int) (int64, error) {
	s.seekCalls++
	if s.seekCalls == s.failAt {
		return 0, errors.New("injected seek failure")
	}
	return 0, nil
}

func plaintextHiddenRequest(operationID, name string, size int64) HiddenUploadRequest {
	return HiddenUploadRequest{
		OperationID:   operationID,
		Name:          name,
		StoredSize:    size,
		PlaintextSize: size,
	}
}

func encryptedHiddenSource(t *testing.T, plaintext []byte) []byte {
	t.Helper()
	var ciphertext bytes.Buffer
	masterKey := bytes.Repeat([]byte{0x42}, 32)
	if err := tdcrypto.EncryptStream(bytes.NewReader(plaintext), &ciphertext, masterKey, int64(len(plaintext))); err != nil {
		t.Fatalf("EncryptStream: %v", err)
	}
	return ciphertext.Bytes()
}

func TestUploadHiddenSingleIsNotVisibleBeforeCommit(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	body := []byte("hello from the mounted drive")

	remote, err := svc.UploadHidden(
		context.Background(),
		personalChannelID,
		plaintextHiddenRequest("op-single-1", "notes.txt", int64(len(body))),
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("UploadHidden: %v", err)
	}
	if remote.UploadUUID == "" || remote.PartCount != 1 {
		t.Fatalf("remote body = %+v, want one invisible part", remote)
	}
	if remote.StoredSize != int64(len(body)) || remote.PlaintextSize != int64(len(body)) {
		t.Fatalf("remote sizes = stored %d plain %d", remote.StoredSize, remote.PlaintextSize)
	}
	if len(remote.MessageIDs) != 1 || remote.MessageIDs[0] <= 0 {
		t.Fatalf("cleanup ids = %v, want one message", remote.MessageIDs)
	}

	sent := fakeTG.SentFiles()
	if len(sent) != 1 {
		t.Fatalf("sent files = %+v, want one", sent)
	}
	op, err := projection.Parse(sent[0].Caption)
	if err != nil {
		t.Fatalf("hidden body caption: %v", err)
	}
	if op.Type != projection.OpFilePart || op.UploadUUID != remote.UploadUUID || op.PartIndex != 0 {
		t.Fatalf("hidden body op = %+v, want part 0", op)
	}
	history, err := fakeTG.GetHistory(context.Background(), tgclient.InputPeer{ChannelID: personalChannelID}, 0, 0, 10)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	parsed := tdsync.ParseHistoryPageWithOptions(history, tdsync.ParseOptions{AdoptCaptionlessMedia: true})
	if len(parsed) != 1 || parsed[0].AdoptedCaptionless || parsed[0].Op.Type != projection.OpFilePart {
		t.Fatalf("personal sync parsed hidden body as %+v, want an invisible part", parsed)
	}
	parts, err := projection.PartsForUUID(db, personalChannelID, remote.UploadUUID)
	if err != nil || len(parts) != 1 || parts[0].MsgID != remote.MessageIDs[0] {
		t.Fatalf("hidden parts = %+v, err %v", parts, err)
	}
	var visible int
	if err := db.QueryRow(`SELECT COUNT(*) FROM files WHERE channel_id = ? AND tombstoned = 0`, personalChannelID).Scan(&visible); err != nil {
		t.Fatalf("count visible files: %v", err)
	}
	if visible != 0 {
		t.Fatalf("visible files = %d, want 0 before commit", visible)
	}
}

// TestPartAttachmentName covers partAttachmentName directly, including the
// empty-name fallback: upstream validation should always reject an empty
// name before it reaches this function, so integration tests through
// UploadHidden/Upload can't naturally exercise that branch.
func TestPartAttachmentName(t *testing.T) {
	cases := []struct {
		name      string
		partIndex int
		partCount int
		want      string
	}{
		{name: "photo.png", partIndex: 0, partCount: 1, want: "photo.png"},
		{name: "movie.bin", partIndex: 0, partCount: 4, want: "movie.bin.part0"},
		{name: "movie.bin", partIndex: 3, partCount: 4, want: "movie.bin.part3"},
		{name: "", partIndex: 0, partCount: 1, want: "part-00000"},
		{name: "", partIndex: 7, partCount: 9, want: "part-00007"},
	}
	for _, testCase := range cases {
		got := partAttachmentName(testCase.name, testCase.partIndex, testCase.partCount)
		if got != testCase.want {
			t.Errorf("partAttachmentName(%q, %d, %d) = %q, want %q",
				testCase.name, testCase.partIndex, testCase.partCount, got, testCase.want)
		}
	}
}

// TestUploadHiddenSinglePartUsesOriginalFilename covers a user-requested
// change: the Telegram document attachment for a single-part upload (the
// common case) should show the real original filename when browsed directly
// in the channel, not a generic "part-00000" label. The pix=/UploadUUID
// reconstruction TDrive itself relies on lives entirely in the caption text
// (verified by the existing TestUploadHiddenSingleIsNotVisibleBeforeCommit),
// so this is purely a cosmetic-naming assertion, not a correctness one.
func TestUploadHiddenSinglePartUsesOriginalFilename(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	body := []byte("hello from the mounted drive")

	remote, err := svc.UploadHidden(
		context.Background(),
		personalChannelID,
		plaintextHiddenRequest("op-name-single", "My Report.pdf", int64(len(body))),
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("UploadHidden: %v", err)
	}
	if remote.PartCount != 1 {
		t.Fatalf("PartCount = %d, want 1", remote.PartCount)
	}

	sent := fakeTG.SentFiles()
	if len(sent) != 1 {
		t.Fatalf("sent files = %+v, want one", sent)
	}
	if sent[0].Name != "My Report.pdf" {
		t.Fatalf("attachment name = %q, want the original filename %q", sent[0].Name, "My Report.pdf")
	}
}

// TestUploadHiddenMultipartSuffixesOriginalFilename covers the multi-part
// case: each part keeps the original filename but gains a distinguishing
// suffix, since several messages sharing one identical name would otherwise
// be impossible to tell apart or order when browsing the raw channel.
func TestUploadHiddenMultipartSuffixesOriginalFilename(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 4
	body := []byte("0123456789")

	remote, err := svc.UploadHidden(
		context.Background(),
		personalChannelID,
		plaintextHiddenRequest("op-name-multi", "large.bin", int64(len(body))),
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("UploadHidden: %v", err)
	}
	if remote.PartCount != 3 {
		t.Fatalf("PartCount = %d, want 3", remote.PartCount)
	}

	sent := fakeTG.SentFiles()
	if len(sent) != 3 {
		t.Fatalf("sent files = %+v, want three", sent)
	}
	want := []string{"large.bin.part0", "large.bin.part1", "large.bin.part2"}
	for i, name := range want {
		if sent[i].Name != name {
			t.Fatalf("part %d attachment name = %q, want %q", i, sent[i].Name, name)
		}
	}
}

func TestUploadHiddenMultipartProjectsOnlyInvisibleParts(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 4
	body := []byte("0123456789")

	remote, err := svc.UploadHidden(
		context.Background(),
		personalChannelID,
		plaintextHiddenRequest("op-multipart-1", "large.bin", int64(len(body))),
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("UploadHidden: %v", err)
	}
	if remote.UploadUUID == "" || remote.PartCount != 3 {
		t.Fatalf("remote body = %+v, want three multipart messages", remote)
	}
	if len(remote.MessageIDs) != 3 {
		t.Fatalf("cleanup ids = %v, want 3", remote.MessageIDs)
	}

	parts, err := projection.PartsForUUID(db, personalChannelID, remote.UploadUUID)
	if err != nil {
		t.Fatalf("PartsForUUID: %v", err)
	}
	if len(parts) != 3 || parts[0].Size != 4 || parts[1].Size != 4 || parts[2].Size != 2 {
		t.Fatalf("parts = %+v", parts)
	}
	for i, sent := range fakeTG.SentFiles() {
		op, err := projection.Parse(sent.Caption)
		if err != nil {
			t.Fatalf("part %d caption: %v", i, err)
		}
		if op.Type != projection.OpFilePart || op.UploadUUID != remote.UploadUUID || op.PartIndex != i {
			t.Fatalf("part %d op = %+v", i, op)
		}
	}
	var visible int
	if err := db.QueryRow(`SELECT COUNT(*) FROM files WHERE channel_id = ? AND tombstoned = 0`, personalChannelID).Scan(&visible); err != nil {
		t.Fatalf("count visible files: %v", err)
	}
	if visible != 0 {
		t.Fatalf("visible files = %d, want 0 before commit", visible)
	}
}

func TestUploadHiddenRetryUsesStableTelegramMessage(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	body := []byte("retry me")
	request := plaintextHiddenRequest("op-retry-1", "retry.txt", int64(len(body)))

	first, err := svc.UploadHidden(context.Background(), personalChannelID, request, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("first UploadHidden: %v", err)
	}
	second, err := svc.UploadHidden(context.Background(), personalChannelID, request, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("retry UploadHidden: %v", err)
	}
	if second.UploadUUID != first.UploadUUID || len(second.MessageIDs) != 1 || second.MessageIDs[0] != first.MessageIDs[0] {
		t.Fatalf("retry body = %+v, want %+v", second, first)
	}
	if sent := fakeTG.SentFiles(); len(sent) != 1 {
		t.Fatalf("sent files = %+v, want one idempotent message", sent)
	}
}

func TestUploadHiddenRetriesBoundedFloodWaitAndRewinds(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	fakeTG.InjectFloodWaits(2)
	var sleeps int
	svc.FloodWaitRetry = tgclient.FloodWaitRetryPolicy{
		MaxRetries:   2,
		MaxWait:      time.Second,
		MaxTotalWait: 2 * time.Second,
		Sleep: func(context.Context, time.Duration) error {
			sleeps++
			return nil
		},
	}
	body := []byte("complete body after retry")

	remote, err := svc.UploadHidden(
		context.Background(),
		personalChannelID,
		plaintextHiddenRequest("op-flood-1", "flood.txt", int64(len(body))),
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("UploadHidden: %v", err)
	}
	if sleeps != 2 {
		t.Fatalf("sleeps = %d, want 2", sleeps)
	}

	var downloaded bytes.Buffer
	peer := tgclient.InputPeer{ChannelID: personalChannelID, AccessHash: 99}
	if err := fakeTG.DownloadFile(context.Background(), peer, remote.MessageIDs[0], &downloaded, nil); err != nil {
		t.Fatalf("download hidden body: %v", err)
	}
	if !bytes.Equal(downloaded.Bytes(), body) {
		t.Fatalf("downloaded = %q, want %q", downloaded.Bytes(), body)
	}
}

func TestUploadHiddenValidatesMetadataAndSourceBeforeRemoteAccess(t *testing.T) {
	tests := []struct {
		name    string
		request HiddenUploadRequest
		source  []byte
	}{
		{
			name: "negative stored size",
			request: HiddenUploadRequest{
				OperationID: "op-negative-stored", Name: "bad.bin", StoredSize: -1, PlaintextSize: 0,
			},
		},
		{
			name: "negative plaintext size",
			request: HiddenUploadRequest{
				OperationID: "op-negative-plain", Name: "bad.bin", StoredSize: 0, PlaintextSize: -1,
			},
		},
		{
			name: "plaintext metadata mismatch",
			request: HiddenUploadRequest{
				OperationID: "op-plain-metadata", Name: "bad.bin", StoredSize: 5, PlaintextSize: 4,
			},
			source: []byte("12345"),
		},
		{
			name: "encrypted metadata mismatch",
			request: HiddenUploadRequest{
				OperationID: "op-encrypted-metadata", Name: "bad.bin", StoredSize: 6, PlaintextSize: 6, Encrypted: true,
			},
			source: []byte("123456"),
		},
		{
			name:    "source shorter than stored size",
			request: plaintextHiddenRequest("op-short-source", "short.bin", 6),
			source:  []byte("short"),
		},
		{
			name:    "source longer than stored size",
			request: plaintextHiddenRequest("op-long-source", "long.bin", 4),
			source:  []byte("longer"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, fakeTG, _ := newTestService(t)
			peers := &hiddenCountingPeerResolver{peer: tgclient.InputPeer{ChannelID: personalChannelID, AccessHash: 99}}
			svc.Peers = peers

			if _, err := svc.UploadHidden(context.Background(), personalChannelID, tc.request, bytes.NewReader(tc.source)); err == nil {
				t.Fatal("UploadHidden accepted invalid staged metadata")
			}
			if peers.calls != 0 {
				t.Fatalf("peer resolver calls = %d, want 0 before validation", peers.calls)
			}
			if sent := fakeTG.SentFiles(); len(sent) != 0 {
				t.Fatalf("sent files = %+v, want none", sent)
			}
		})
	}
}

func TestUploadHiddenRejectsEncryptedPlaintextAboveTDE1Capacity(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	peers := &hiddenCountingPeerResolver{peer: tgclient.InputPeer{ChannelID: personalChannelID, AccessHash: 99}}
	svc.Peers = peers
	request := HiddenUploadRequest{
		OperationID:   "op-encrypted-too-large",
		Name:          "too-large.bin",
		StoredSize:    math.MaxInt64,
		PlaintextSize: math.MaxInt64,
		Encrypted:     true,
	}

	_, err := svc.UploadHidden(context.Background(), personalChannelID, request, bytes.NewReader(nil))
	if !errors.Is(err, tdcrypto.ErrPlaintextTooLarge) {
		t.Fatalf("UploadHidden error = %v, want ErrPlaintextTooLarge", err)
	}
	if peers.calls != 0 {
		t.Fatalf("peer resolver calls = %d, want 0", peers.calls)
	}
	if sent := fakeTG.SentFiles(); len(sent) != 0 {
		t.Fatalf("sent files = %+v, want none", sent)
	}
}

func TestUploadHiddenAcceptsAlreadyEncryptedStoredRepresentation(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	plaintext := []byte("secret")
	ciphertext := encryptedHiddenSource(t, plaintext)
	request := HiddenUploadRequest{
		OperationID:   "op-encrypted-1",
		Name:          "secret.txt",
		StoredSize:    int64(len(ciphertext)),
		PlaintextSize: int64(len(plaintext)),
		Encrypted:     true,
	}

	remote, err := svc.UploadHidden(context.Background(), personalChannelID, request, bytes.NewReader(ciphertext))
	if err != nil {
		t.Fatalf("UploadHidden: %v", err)
	}
	if remote.StoredSize != request.StoredSize || remote.PlaintextSize != request.PlaintextSize || !remote.Encrypted {
		t.Fatalf("remote metadata = %+v, want encrypted stored representation", remote)
	}
	if len(remote.MessageIDs) != 1 {
		t.Fatalf("remote message ids = %v, want one", remote.MessageIDs)
	}

	var downloaded bytes.Buffer
	peer := tgclient.InputPeer{ChannelID: personalChannelID, AccessHash: 99}
	if err := fakeTG.DownloadFile(context.Background(), peer, remote.MessageIDs[0], &downloaded, nil); err != nil {
		t.Fatalf("download encrypted hidden body: %v", err)
	}
	if !bytes.Equal(downloaded.Bytes(), ciphertext) {
		t.Fatal("Telegram body differs from the staged ciphertext")
	}
	if bytes.Equal(downloaded.Bytes(), plaintext) {
		t.Fatal("hidden upload persisted plaintext for an encrypted request")
	}
}

func TestUploadHiddenHandlesZeroExactPartAndMultipartSizes(t *testing.T) {
	tests := []struct {
		name      string
		body      []byte
		partBytes int64
		wantParts int
	}{
		{name: "zero", body: nil, partBytes: 4, wantParts: 1},
		{name: "exact part", body: []byte("1234"), partBytes: 4, wantParts: 1},
		{name: "multipart", body: []byte("12345"), partBytes: 4, wantParts: 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, fakeTG, _ := newTestService(t)
			svc.MaxUploadBytes = tc.partBytes
			request := plaintextHiddenRequest("op-boundary-"+tc.name, "boundary.bin", int64(len(tc.body)))

			remote, err := svc.UploadHidden(context.Background(), personalChannelID, request, bytes.NewReader(tc.body))
			if err != nil {
				t.Fatalf("UploadHidden: %v", err)
			}
			if remote.PartCount != tc.wantParts || len(remote.MessageIDs) != tc.wantParts {
				t.Fatalf("remote = %+v, want %d parts", remote, tc.wantParts)
			}
			if sent := fakeTG.SentFiles(); len(sent) != tc.wantParts {
				t.Fatalf("sent files = %d, want %d", len(sent), tc.wantParts)
			}
		})
	}
}

func TestUploadHiddenEncryptedZeroExactChunkAndMultipartSizes(t *testing.T) {
	tests := []struct {
		name          string
		plaintext     []byte
		partBytes     func(storedSize int64) int64
		wantPartCount func(storedSize, partSize int64) int
	}{
		{
			name:      "zero plaintext",
			plaintext: nil,
			partBytes: func(storedSize int64) int64 { return storedSize + 1 },
			wantPartCount: func(int64, int64) int {
				return 1
			},
		},
		{
			name:      "exact encryption chunk",
			plaintext: bytes.Repeat([]byte{'x'}, 64*1024),
			partBytes: func(storedSize int64) int64 { return storedSize },
			wantPartCount: func(int64, int64) int {
				return 1
			},
		},
		{
			name:      "multipart ciphertext",
			plaintext: []byte("secret multipart body"),
			partBytes: func(int64) int64 { return 32 },
			wantPartCount: func(storedSize, partSize int64) int {
				return int((storedSize + partSize - 1) / partSize)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, fakeTG, _ := newTestService(t)
			ciphertext := encryptedHiddenSource(t, tc.plaintext)
			storedSize := int64(len(ciphertext))
			if want := tdcrypto.CiphertextSize(int64(len(tc.plaintext))); storedSize != want {
				t.Fatalf("ciphertext size = %d, want %d", storedSize, want)
			}
			svc.MaxUploadBytes = tc.partBytes(storedSize)
			request := HiddenUploadRequest{
				OperationID:   "op-encrypted-boundary-" + tc.name,
				Name:          "secret.bin",
				StoredSize:    storedSize,
				PlaintextSize: int64(len(tc.plaintext)),
				Encrypted:     true,
			}

			remote, err := svc.UploadHidden(context.Background(), personalChannelID, request, bytes.NewReader(ciphertext))
			if err != nil {
				t.Fatalf("UploadHidden: %v", err)
			}
			wantParts := tc.wantPartCount(storedSize, svc.MaxUploadBytes)
			if remote.PartCount != wantParts || len(remote.MessageIDs) != wantParts {
				t.Fatalf("remote = %+v, want %d encrypted parts", remote, wantParts)
			}

			var downloaded bytes.Buffer
			peer := tgclient.InputPeer{ChannelID: personalChannelID, AccessHash: 99}
			for _, messageID := range remote.MessageIDs {
				if err := fakeTG.DownloadFile(context.Background(), peer, messageID, &downloaded, nil); err != nil {
					t.Fatalf("download encrypted part %d: %v", messageID, err)
				}
			}
			if !bytes.Equal(downloaded.Bytes(), ciphertext) {
				t.Fatal("reassembled Telegram parts differ from staged ciphertext")
			}
		})
	}
}

func TestDiscardHiddenDeletesBodiesAndPartProjection(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 4
	body := []byte("0123456789")
	remote, err := svc.UploadHidden(
		context.Background(),
		personalChannelID,
		plaintextHiddenRequest("op-discard-1", "discard.bin", int64(len(body))),
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("UploadHidden: %v", err)
	}

	if err := svc.discardHiddenBody(context.Background(), personalChannelID, remote); err != nil {
		t.Fatalf("discardHiddenBody: %v", err)
	}
	if parts, err := projection.PartsForUUID(db, personalChannelID, remote.UploadUUID); err != nil || len(parts) != 0 {
		t.Fatalf("parts after discard = %+v, err %v", parts, err)
	}
	if batches := fakeTG.DeletedBatches(); len(batches) != 1 || len(batches[0]) != 3 {
		t.Fatalf("deleted batches = %+v, want one three-message batch", batches)
	}
}

func TestUploadHiddenRejectsInvalidRequestsBeforeSend(t *testing.T) {
	longName := strings.Repeat("a", 241)
	tests := []struct {
		name      string
		ctx       context.Context
		channelID int64
		request   HiddenUploadRequest
		source    io.ReadSeeker
	}{
		{name: "nil context", channelID: personalChannelID, request: plaintextHiddenRequest("op", "a", 1), source: bytes.NewReader([]byte("a"))},
		{name: "canceled context", ctx: canceledContext(), channelID: personalChannelID, request: plaintextHiddenRequest("op", "a", 1), source: bytes.NewReader([]byte("a"))},
		{name: "missing channel", ctx: context.Background(), request: plaintextHiddenRequest("op", "a", 1), source: bytes.NewReader([]byte("a"))},
		{name: "missing operation", ctx: context.Background(), channelID: personalChannelID, request: plaintextHiddenRequest("", "a", 1), source: bytes.NewReader([]byte("a"))},
		{name: "negative size", ctx: context.Background(), channelID: personalChannelID, request: plaintextHiddenRequest("op", "a", -1), source: bytes.NewReader(nil)},
		{name: "empty name", ctx: context.Background(), channelID: personalChannelID, request: plaintextHiddenRequest("op", "", 1), source: bytes.NewReader([]byte("a"))},
		{name: "trailing whitespace", ctx: context.Background(), channelID: personalChannelID, request: plaintextHiddenRequest("op", "a ", 1), source: bytes.NewReader([]byte("a"))},
		{name: "reserved Windows name", ctx: context.Background(), channelID: personalChannelID, request: plaintextHiddenRequest("op", "CON.txt", 1), source: bytes.NewReader([]byte("a"))},
		{name: "slash", ctx: context.Background(), channelID: personalChannelID, request: plaintextHiddenRequest("op", "a/b", 1), source: bytes.NewReader([]byte("a"))},
		{name: "backslash", ctx: context.Background(), channelID: personalChannelID, request: plaintextHiddenRequest("op", `a\b`, 1), source: bytes.NewReader([]byte("a"))},
		{name: "name too long", ctx: context.Background(), channelID: personalChannelID, request: plaintextHiddenRequest("op", longName, 1), source: bytes.NewReader([]byte("a"))},
		{name: "invalid UTF-8 name", ctx: context.Background(), channelID: personalChannelID, request: plaintextHiddenRequest("op", string([]byte{0xff}), 1), source: bytes.NewReader([]byte("a"))},
		{name: "nil source", ctx: context.Background(), channelID: personalChannelID, request: plaintextHiddenRequest("op", "a", 1)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, fakeTG, _ := newTestService(t)
			_, err := svc.UploadHidden(tc.ctx, tc.channelID, tc.request, tc.source)
			if err == nil {
				t.Fatal("UploadHidden unexpectedly accepted invalid request")
			}
			if tc.name == "nil context" && !errors.Is(err, projection.ErrInvalidContext) {
				t.Fatalf("error = %v, want projection.ErrInvalidContext", err)
			}
			if tc.name == "canceled context" && !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want context.Canceled", err)
			}
			if sent := fakeTG.SentFiles(); len(sent) != 0 {
				t.Fatalf("sent files = %+v, want none", sent)
			}
		})
	}
}

func TestUploadHiddenNamePolicyMatchesPortableProjection(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	name := " leading-space.txt"
	if _, err := projection.CanonicalNameKey(name); err != nil {
		t.Fatalf("portable policy rejected test name: %v", err)
	}
	if _, err := svc.UploadHidden(
		context.Background(), personalChannelID,
		plaintextHiddenRequest("op-leading-space", name, 1),
		bytes.NewReader([]byte("x")),
	); err != nil {
		t.Fatalf("UploadHidden rejected portable name: %v", err)
	}
	if len(fakeTG.SentFiles()) != 1 {
		t.Fatal("portable upload did not reach Telegram")
	}
}

func TestDiscardHiddenOperationCleansCrashUploadDeterministically(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 4
	operationID := "op-crash-cleanup"
	remote, err := svc.UploadHidden(
		context.Background(), personalChannelID,
		plaintextHiddenRequest(operationID, "crash.bin", 10),
		bytes.NewReader([]byte("0123456789")),
	)
	if err != nil {
		t.Fatalf("UploadHidden: %v", err)
	}
	if err := svc.DiscardHiddenOperation(context.Background(), personalChannelID, operationID); err != nil {
		t.Fatalf("DiscardHiddenOperation: %v", err)
	}
	if parts, err := projection.PartsForUUIDContext(context.Background(), db, personalChannelID, remote.UploadUUID); err != nil || len(parts) != 0 {
		t.Fatalf("parts after cleanup = %+v, err=%v", parts, err)
	}
	deleted := fakeTG.DeletedBatches()
	if len(deleted) != 1 || len(deleted[0]) != remote.PartCount {
		t.Fatalf("deleted batches = %+v, want %d messages", deleted, remote.PartCount)
	}
	// Recovery may repeat cleanup after an uncertain local transition.
	if err := svc.DiscardHiddenOperation(context.Background(), personalChannelID, operationID); err != nil {
		t.Fatalf("idempotent cleanup: %v", err)
	}
}

func TestDiscardHiddenOperationValidatesBeforeLookup(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	for name, call := range map[string]func() error{
		"nil context":     func() error { return svc.DiscardHiddenOperation(nil, personalChannelID, "op") },
		"zero channel":    func() error { return svc.DiscardHiddenOperation(context.Background(), 0, "op") },
		"empty operation": func() error { return svc.DiscardHiddenOperation(context.Background(), personalChannelID, "") },
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil {
				t.Fatal("invalid cleanup accepted")
			}
		})
	}
	if len(fakeTG.DeletedBatches()) != 0 {
		t.Fatal("invalid cleanup touched Telegram")
	}
}

func TestHiddenCleanupRejectsNilContext(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	tests := map[string]func() error{
		"body": func() error {
			return svc.discardHiddenBody(nil, personalChannelID, HiddenBody{MessageIDs: []int64{1}})
		},
		"receipt": func() error {
			return svc.DiscardHiddenReceipt(nil, personalChannelID, "operation", HiddenBody{})
		},
		"operation": func() error {
			return svc.DiscardHiddenOperation(nil, personalChannelID, "operation")
		},
	}
	for operation, call := range tests {
		t.Run(operation, func(t *testing.T) {
			err := call()
			if !errors.Is(err, projection.ErrInvalidContext) {
				t.Fatalf("error = %v, want projection.ErrInvalidContext", err)
			}
			if !strings.Contains(err.Error(), operation) {
				t.Fatalf("error = %q, want %q operation context", err, operation)
			}
		})
	}
}

func TestUploadHiddenReportsUnavailableDependencies(t *testing.T) {
	body := []byte("a")
	request := plaintextHiddenRequest("op-deps", "a.txt", 1)
	tests := []struct {
		name   string
		mutate func(*Service)
	}{
		{name: "database", mutate: func(s *Service) { s.DB = nil }},
		{name: "telegram", mutate: func(s *Service) { s.TG = nil }},
		{name: "peer resolver", mutate: func(s *Service) { s.Peers = nil }},
		{name: "peer resolution", mutate: func(s *Service) {
			s.Peers = hiddenTestPeerResolver{err: errors.New("resolve failed")}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _, _ := newTestService(t)
			tc.mutate(svc)
			if _, err := svc.UploadHidden(context.Background(), personalChannelID, request, bytes.NewReader(body)); err == nil {
				t.Fatal("UploadHidden unexpectedly succeeded")
			}
		})
	}
}

func TestUploadHiddenReportsPlanningAndMessageErrors(t *testing.T) {
	t.Run("too large", func(t *testing.T) {
		svc, _, fakeTG, _ := newTestService(t)
		svc.MaxUploadBytes = 1
		body := bytes.Repeat([]byte{'x'}, MaxParts+1)
		_, err := svc.UploadHidden(
			context.Background(), personalChannelID,
			plaintextHiddenRequest("op-too-large", "large.bin", int64(len(body))),
			bytes.NewReader(body),
		)
		if !errors.Is(err, ErrFileTooLarge) {
			t.Fatalf("error = %v, want ErrFileTooLarge", err)
		}
		if sent := fakeTG.SentFiles(); len(sent) != 0 {
			t.Fatalf("sent files = %+v, want none", sent)
		}
	})

	t.Run("single missing message id", func(t *testing.T) {
		svc, _, fakeTG, _ := newTestService(t)
		svc.TG = &zeroMessageIDClient{Fake: fakeTG}
		_, err := svc.UploadHidden(
			context.Background(), personalChannelID,
			plaintextHiddenRequest("op-zero", "zero.txt", 1),
			bytes.NewReader([]byte("x")),
		)
		if err == nil || !strings.Contains(err.Error(), "no message id") {
			t.Fatalf("error = %v, want missing message id", err)
		}
	})

	t.Run("multipart actor unavailable", func(t *testing.T) {
		svc, _, _, _ := newTestService(t)
		svc.MaxUploadBytes = 1
		svc.ActorID = nil
		_, err := svc.UploadHidden(
			context.Background(), personalChannelID,
			plaintextHiddenRequest("op-no-actor", "two.bin", 2),
			bytes.NewReader([]byte("xx")),
		)
		if err == nil || !strings.Contains(err.Error(), "actor resolver") {
			t.Fatalf("error = %v, want actor resolver error", err)
		}
	})
}

func TestUploadPartPlanClassifiesTooManyParts(t *testing.T) {
	svc := &Service{MaxUploadBytes: 1}

	_, err := svc.buildUploadPartPlan(MaxParts + 1)
	if !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("error = %v, want ErrFileTooLarge", err)
	}
}

func TestUploadPartPlanWindows(t *testing.T) {
	svc := &Service{MaxUploadBytes: 4}
	tests := []struct {
		name        string
		storedSize  int64
		wantWindows [][2]int64
	}{
		{name: "empty", storedSize: 0, wantWindows: [][2]int64{{0, 0}}},
		{name: "exact part", storedSize: 4, wantWindows: [][2]int64{{0, 4}}},
		{name: "partial final part", storedSize: 9, wantWindows: [][2]int64{{0, 4}, {4, 4}, {8, 1}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := svc.buildUploadPartPlan(tc.storedSize)
			if err != nil {
				t.Fatalf("buildUploadPartPlan: %v", err)
			}
			if plan.partCount != len(tc.wantWindows) {
				t.Fatalf("partCount = %d, want %d", plan.partCount, len(tc.wantWindows))
			}
			for index, want := range tc.wantWindows {
				offset, length, err := plan.window(tc.storedSize, index)
				if err != nil {
					t.Fatalf("window(%d): %v", index, err)
				}
				if offset != want[0] || length != want[1] {
					t.Fatalf("window(%d) = (%d, %d), want (%d, %d)", index, offset, length, want[0], want[1])
				}
			}
		})
	}
}

func TestUploadHiddenMultipartFailureReturnsPartialReceiptWithoutDeleting(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 4
	sendErr := errors.New("second part failed")
	svc.TG = &failNthFileClient{Fake: fakeTG, failAt: 2, err: sendErr}

	partial, err := svc.UploadHidden(
		context.Background(), personalChannelID,
		plaintextHiddenRequest("op-part-failure", "large.bin", 10),
		bytes.NewReader([]byte("0123456789")),
	)
	if !errors.Is(err, sendErr) {
		t.Fatalf("error = %v, want %v", err, sendErr)
	}
	uploadUUID := hiddenUploadUUID("op-part-failure")
	if len(partial.MessageIDs) != 1 {
		t.Fatalf("partial receipt = %+v, want first part", partial)
	}
	if parts, partErr := projection.PartsForUUID(db, personalChannelID, uploadUUID); partErr != nil || len(parts) != 1 {
		t.Fatalf("durable parts after failure = %+v, err %v", parts, partErr)
	}
	if deleted := fakeTG.DeletedBatches(); len(deleted) != 0 {
		t.Fatalf("UploadHidden deleted before caller persisted receipt: %+v", deleted)
	}
	if err := svc.discardHiddenBody(context.Background(), personalChannelID, partial); err != nil {
		t.Fatalf("discardHiddenBody partial: %v", err)
	}
	if deleted := fakeTG.DeletedBatches(); len(deleted) != 1 || len(deleted[0]) != 1 {
		t.Fatalf("deleted batches = %+v, want explicit cleanup of first part", deleted)
	}
}

func TestUploadHiddenEncryptedMultipartFailureReturnsPartialReceiptWithoutDeleting(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 32
	sendErr := errors.New("encrypted second part failed")
	svc.TG = &failNthFileClient{Fake: fakeTG, failAt: 2, err: sendErr}
	plaintext := []byte("secret multipart body")
	ciphertext := encryptedHiddenSource(t, plaintext)
	request := HiddenUploadRequest{
		OperationID:   "op-encrypted-part-failure",
		Name:          "secret.bin",
		StoredSize:    int64(len(ciphertext)),
		PlaintextSize: int64(len(plaintext)),
		Encrypted:     true,
	}

	partial, err := svc.UploadHidden(context.Background(), personalChannelID, request, bytes.NewReader(ciphertext))
	if !errors.Is(err, sendErr) {
		t.Fatalf("error = %v, want %v", err, sendErr)
	}
	uploadUUID := hiddenUploadUUID(request.OperationID)
	if len(partial.MessageIDs) != 1 || !partial.Encrypted {
		t.Fatalf("encrypted partial receipt = %+v", partial)
	}
	if parts, partErr := projection.PartsForUUID(db, personalChannelID, uploadUUID); partErr != nil || len(parts) != 1 {
		t.Fatalf("durable parts after failure = %+v, err %v", parts, partErr)
	}
	if deleted := fakeTG.DeletedBatches(); len(deleted) != 0 {
		t.Fatalf("UploadHidden deleted encrypted receipt before persistence: %+v", deleted)
	}
	if err := svc.discardHiddenBody(context.Background(), personalChannelID, partial); err != nil {
		t.Fatalf("discardHiddenBody encrypted partial: %v", err)
	}
	if deleted := fakeTG.DeletedBatches(); len(deleted) != 1 || len(deleted[0]) != 1 {
		t.Fatalf("deleted batches = %+v, want explicit encrypted cleanup", deleted)
	}
}

func TestUploadHiddenStopsAfterFloodWaitRetryBudget(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	fakeTG.InjectFloodWaits(3)
	svc.FloodWaitRetry = tgclient.FloodWaitRetryPolicy{
		MaxRetries: 1, MaxWait: time.Second, MaxTotalWait: time.Second,
		Sleep: func(context.Context, time.Duration) error { return nil },
	}

	_, err := svc.UploadHidden(
		context.Background(), personalChannelID,
		plaintextHiddenRequest("op-flood-stop", "wait.txt", 1),
		bytes.NewReader([]byte("x")),
	)
	if !errors.Is(err, tgclient.ErrFloodWait) {
		t.Fatalf("error = %v, want flood wait", err)
	}
	if sent := fakeTG.SentFiles(); len(sent) != 0 {
		t.Fatalf("sent files = %+v, want none", sent)
	}
}

func TestDiscardHiddenBodyValidationAndCleanupFailure(t *testing.T) {
	t.Run("validation", func(t *testing.T) {
		svc, _, _, _ := newTestService(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		for name, call := range map[string]func() error{
			"nil context": func() error { return svc.discardHiddenBody(nil, personalChannelID, HiddenBody{MessageIDs: []int64{1}}) },
			"canceled":    func() error { return svc.discardHiddenBody(ctx, personalChannelID, HiddenBody{MessageIDs: []int64{1}}) },
			"channel": func() error {
				return svc.discardHiddenBody(context.Background(), 0, HiddenBody{MessageIDs: []int64{1}})
			},
		} {
			t.Run(name, func(t *testing.T) {
				if err := call(); err == nil {
					t.Fatal("DiscardHidden unexpectedly succeeded")
				}
			})
		}
		if err := svc.discardHiddenBody(context.Background(), personalChannelID, HiddenBody{}); err != nil {
			t.Fatalf("empty discardHiddenBody: %v", err)
		}
	})

	t.Run("dependencies", func(t *testing.T) {
		svc, _, _, _ := newTestService(t)
		svc.TG = nil
		if err := svc.discardHiddenBody(context.Background(), personalChannelID, HiddenBody{MessageIDs: []int64{1}}); err == nil {
			t.Fatal("discardHiddenBody succeeded without Telegram")
		}
		svc, _, _, _ = newTestService(t)
		svc.Peers = hiddenTestPeerResolver{err: errors.New("resolve failed")}
		if err := svc.discardHiddenBody(context.Background(), personalChannelID, HiddenBody{MessageIDs: []int64{1}}); err == nil {
			t.Fatal("discardHiddenBody succeeded despite peer failure")
		}
	})

	t.Run("delete failure queues cleanup", func(t *testing.T) {
		svc, db, fakeTG, _ := newTestService(t)
		deleteErr := errors.New("delete failed")
		svc.TG = &deleteFailureClient{Fake: fakeTG, err: deleteErr}
		body := HiddenBody{UploadUUID: "hu-test", PartCount: 1, MessageIDs: []int64{77}}
		if err := svc.discardHiddenBody(context.Background(), personalChannelID, body); !errors.Is(err, deleteErr) {
			t.Fatalf("discardHiddenBody error = %v, want %v", err, deleteErr)
		}
		pending, err := projection.PendingPartCleanup(db, personalChannelID)
		if err != nil {
			t.Fatalf("PendingPartCleanup: %v", err)
		}
		if len(pending) != 1 || pending[0] != 77 {
			t.Fatalf("pending cleanup = %v, want [77]", pending)
		}
	})
}

func TestServiceDoesNotExposeRawHiddenMessageDeletion(t *testing.T) {
	if _, exposed := reflect.TypeOf((*Service)(nil)).MethodByName("DiscardHidden"); exposed {
		t.Fatal("Service exposes raw hidden MessageID deletion without operation ownership")
	}
}

func TestValidateSeekableSizeReportsSeekFailures(t *testing.T) {
	if err := validateSeekableSize(&failingSeeker{failAt: 1}, 0); err == nil || !strings.Contains(err.Error(), "measure") {
		t.Fatalf("first seek error = %v", err)
	}
	if err := validateSeekableSize(&failingSeeker{failAt: 2}, 0); err == nil || !strings.Contains(err.Error(), "rewind") {
		t.Fatalf("second seek error = %v", err)
	}
}

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
