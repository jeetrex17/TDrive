package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"TDrive/backend"
	"TDrive/backend/media"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
	"TDrive/backend/thumbnail"
)

const previewSourceID int64 = 91
const previewReceiptID int64 = 92

type corePreviewClient struct {
	*tgclient.Fake
	mu        sync.Mutex
	downloads map[int64]int
	download  func(context.Context, int64) error
	document  *tgclient.FileDocument
}

func (c *corePreviewClient) GetFileDocument(ctx context.Context, peer tgclient.InputPeer, msgID int64) (tgclient.FileDocument, error) {
	if c.document != nil {
		return *c.document, nil
	}
	return c.Fake.GetFileDocument(ctx, peer, msgID)
}

func (c *corePreviewClient) DownloadFile(ctx context.Context, peer tgclient.InputPeer, msgID int64, dst io.Writer, progress func(int64, int64)) error {
	c.mu.Lock()
	c.downloads[msgID]++
	c.mu.Unlock()
	if c.download != nil {
		if err := c.download(ctx, msgID); err != nil {
			return err
		}
	}
	return c.Fake.DownloadFile(ctx, peer, msgID, dst, progress)
}

func (c *corePreviewClient) downloadCount(id int64) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.downloads[id]
}

func corePreviewFixture(t *testing.T, withRendition bool) (*Engine, *corePreviewClient, []byte, []byte) {
	t.Helper()
	db := openEngineWriteTestDB(t)
	previousDB := backend.DB
	backend.DB = db
	t.Cleanup(func() { backend.DB = previousDB })
	peer := tgclient.InputPeer{ChannelID: engineWriteTestChannelID, AccessHash: 91}
	if err := projection.UpdateAccessHash(db, peer.ChannelID, peer.AccessHash); err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 8, 4)), nil); err != nil {
		t.Fatal(err)
	}
	preview := bytes.Clone(encoded.Bytes())
	// Only header admission and byte-preserving download use this source. Its
	// synthetic 50MP SOF declaration avoids allocating a camera-sized bitmap;
	// the independently encoded derivative must still decode as a real image.
	original := bytes.Clone(preview)
	sof := bytes.Index(original, []byte{0xff, 0xc0})
	if sof < 0 {
		t.Fatal("encoded JPEG has no baseline frame")
	}
	binary.BigEndian.PutUint16(original[sof+5:sof+7], 8192)
	binary.BigEndian.PutUint16(original[sof+7:sof+9], 6144)
	client := &corePreviewClient{Fake: tgclient.NewFake(7), downloads: make(map[int64]int)}
	client.SeedChannel(peer, "Test drive")
	client.SeedHistory(tgclient.HistoryMessage{MsgID: previewSourceID, HasMedia: true, MediaSize: int64(len(original)), DocumentName: "camera.jpg"})
	client.SeedDocumentBody(previewSourceID, original)
	op := projection.Op{Type: projection.OpFileUpload, Name: "camera.jpg", Parent: projection.RootParent, FileSize: int64(len(original)), FileUploadTime: 1}
	if _, err := projection.ProjectFromOp(db, peer.ChannelID, previewSourceID, op, 7, projection.Format(op)); err != nil {
		t.Fatal(err)
	}
	if withRendition {
		client.SeedHistory(tgclient.HistoryMessage{MsgID: previewReceiptID, HasMedia: true, MediaSize: int64(len(preview)), DocumentName: "preview.jpg"})
		client.SeedDocumentBody(previewReceiptID, preview)
		ref := &projection.FileRendition{ChannelID: peer.ChannelID, FileMsgID: previewSourceID, ContentMsgID: previewSourceID, Kind: projection.RenditionPreview, Version: 1, Width: 8, Height: 4, Size: int64(len(preview)), PlaintextSize: int64(len(preview))}
		op := projection.Op{Type: projection.OpFilePart, UploadUUID: "camera-preview", FileSize: ref.Size, Rendition: ref}
		if _, err := projection.ProjectFromOp(db, peer.ChannelID, previewReceiptID, op, 7, projection.Format(op)); err != nil {
			t.Fatal(err)
		}
	}
	engine := &Engine{ctx: t.Context(), tg: client, thumbs: thumbnail.NewCache(t.TempDir(), 4<<20), warnf: func(string, ...any) {}}
	engine.selfUserID.Store(7)
	return engine, client, original, preview
}

func TestEngineLargeImageUsesRenditionAndDownloadRetainsOriginal(t *testing.T) {
	engine, client, original, preview := corePreviewFixture(t, true)
	service := engine.newMediaService()
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	for range 2 {
		opened, err := service.OpenImage(t.Context(), engineWriteTestChannelID, previewSourceID, 1)
		if err != nil {
			t.Fatalf("OpenImage of 50MP photo: %v", err)
		}
		if !opened.ImagePreview || opened.MimeType != "image/jpeg" || opened.Info.PlaintextSize != int64(len(original)) {
			t.Fatalf("preview must retain original metadata: %+v", opened)
		}
		response, err := http.Get(opened.URL)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read preview: %v; close: %v", readErr, closeErr)
		}
		if response.StatusCode != http.StatusOK || !bytes.Equal(body, preview) {
			t.Fatalf("viewing must serve rendition bytes: status %d, body length %d", response.StatusCode, len(body))
		}
		decoded, err := jpeg.Decode(bytes.NewReader(body))
		if err != nil || decoded.Bounds() != image.Rect(0, 0, 8, 4) {
			t.Fatalf("preview must be a decodable small JPEG: %v", err)
		}
		if err := service.CloseSession(opened.Token); err != nil {
			t.Fatal(err)
		}
	}
	if got := client.downloadCount(previewReceiptID); got != 1 {
		t.Fatalf("rendition transfers = %d, want 1; session close must not destroy shared cache", got)
	}
	if got := client.downloadCount(previewSourceID); got != 0 {
		t.Fatalf("viewer downloaded full original %d times", got)
	}
	path := filepath.Join(t.TempDir(), "camera.jpg")
	result := engine.FileService().Download(t.Context(), engineWriteTestChannelID, int(previewSourceID), int(previewSourceID), func(string) (string, error) { return path, nil })
	if result.Status != "success" {
		t.Fatalf("download original: %+v", result)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("download changed original bytes: %v, got %d bytes", err, len(got))
	}
}

func TestEngineImagePreviewPreservesRenditionErrors(t *testing.T) {
	for _, test := range []struct {
		name               string
		withRef            bool
		fileName           string
		revision           int64
		failure            error
		want               error
		renditionTransfers int
	}{
		{name: "missing permits sampled generation", revision: 1, want: media.ErrImagePreviewMissing},
		{name: "unsupported permits sampled generation", fileName: "camera.tiff", revision: 1, want: media.ErrImagePreviewMissing},
		{name: "stale prevents generation", withRef: true, revision: 2, want: media.ErrStaleRevision},
		{name: "transport error prevents generation", withRef: true, revision: 1, failure: errors.New("offline"), renditionTransfers: 1},
		{name: "cancellation prevents generation", withRef: true, revision: 1, failure: context.Canceled, want: context.Canceled, renditionTransfers: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine, client, _, _ := corePreviewFixture(t, test.withRef)
			if test.fileName != "" {
				if _, err := engine.FileService().DB.Exec(`UPDATE files SET name=? WHERE channel_id=? AND msg_id=?`, test.fileName, engineWriteTestChannelID, previewSourceID); err != nil {
					t.Fatal(err)
				}
			}
			client.download = func(context.Context, int64) error { return test.failure }
			_, err := engine.loadImagePreview(t.Context(), media.LogicalFile{ChannelID: engineWriteTestChannelID, FileID: previewSourceID, Revision: test.revision})
			want := test.want
			if want == nil {
				want = test.failure
			}
			if !errors.Is(err, want) {
				t.Fatalf("preview error = %v, want %v", err, want)
			}
			if got := client.downloadCount(previewSourceID); got != 0 {
				t.Fatalf("preview error path downloaded original %d times", got)
			}
			if got := client.downloadCount(previewReceiptID); got != test.renditionTransfers {
				t.Fatalf("rendition transfers = %d, want %d", got, test.renditionTransfers)
			}
		})
	}
}

func TestEngineImagePreviewNormalizesNativePNGThumbnail(t *testing.T) {
	engine, client, _, _ := corePreviewFixture(t, false)
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 12, 6))); err != nil {
		t.Fatal(err)
	}
	client.document = &tgclient.FileDocument{MsgID: previewSourceID, Name: "camera.jpg", Thumbs: []tgclient.FileThumb{{Type: "m", Width: 12, Height: 6, Bytes: raw.Bytes()}}}
	preview, err := engine.loadImagePreview(t.Context(), media.LogicalFile{ChannelID: engineWriteTestChannelID, FileID: previewSourceID, Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(preview))
	if err != nil {
		t.Fatalf("native PNG must become safe viewer JPEG: %v", err)
	}
	if decoded.Bounds() != image.Rect(0, 0, 12, 6) || client.downloadCount(previewSourceID) != 0 {
		t.Fatalf("native thumbnail dimensions or original transfer changed: bounds %v", decoded.Bounds())
	}
}

func TestEngineImagePreviewRejectsReplacementDuringTransfer(t *testing.T) {
	engine, client, _, _ := corePreviewFixture(t, true)
	client.download = func(ctx context.Context, msgID int64) error {
		if msgID != previewReceiptID {
			return nil
		}
		// The service must pin the source's revision across the remote fetch,
		// including namespace changes that arrive while it waits for Telegram.
		op := projection.Op{Type: projection.OpRename, Obj: "f:91", Name: "renamed.jpg"}
		_, err := projection.ProjectFromOp(engine.FileService().DB, engineWriteTestChannelID, 93, op, 7, projection.Format(op))
		return err
	}
	_, err := engine.loadImagePreview(t.Context(), media.LogicalFile{ChannelID: engineWriteTestChannelID, FileID: previewSourceID, Revision: 1})
	if !errors.Is(err, media.ErrStaleRevision) {
		t.Fatalf("replacement during transfer error = %v, want stale revision", err)
	}
}
