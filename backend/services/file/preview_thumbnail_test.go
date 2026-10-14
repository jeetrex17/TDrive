package file

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"testing"

	"TDrive/backend/tgclient"
)

func TestPreviewThumbnailUsesBestInlineImageWithoutRemoteReads(t *testing.T) {
	larger := append(bytes.Clone(tinyPNG), 0x01)
	for _, tc := range []struct {
		name   string
		thumbs []tgclient.FileThumb
	}{
		{"dimensions", []tgclient.FileThumb{{Bytes: tinyPNG, Width: 16, Height: 16}, {Bytes: larger, Width: 64, Height: 64}}},
		{"declared size", []tgclient.FileThumb{{Bytes: tinyPNG, Size: 10}, {Bytes: larger, Size: 100}}},
		{"actual bytes", []tgclient.FileThumb{{Bytes: tinyPNG}, {Bytes: larger}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, client := renditionFixture(t)
			client.doc.Thumbs = append([]tgclient.FileThumb{{Type: "remote", Width: 4096, Height: 4096}}, tc.thumbs...)
			payload, err := svc.PreviewThumbnail(t.Context(), personalChannelID, 91)
			if err != nil {
				t.Fatal(err)
			}
			got, err := base64.StdEncoding.DecodeString(payload.DataBase64)
			if err != nil || !bytes.Equal(got, larger) || payload.MimeType != "image/png" {
				t.Fatalf("payload bytes=%x mime=%q error=%v, want selected PNG", got, payload.MimeType, err)
			}
			if client.thumbs.Load() != 0 || client.originals.Load() != 0 {
				t.Fatal("inline preview performed a remote body read")
			}
		})
	}
}

type selectedPreviewThumbnailClient struct {
	*renditionClient
	types []string
}

func (c *selectedPreviewThumbnailClient) DownloadFileThumbnail(ctx context.Context, peer tgclient.InputPeer, id int64, kind string, w io.Writer) error {
	c.types = append(c.types, kind)
	return c.renditionClient.DownloadFileThumbnail(ctx, peer, id, kind, w)
}

func TestPreviewThumbnailRetryDiscardsPartialBytes(t *testing.T) {
	svc, client := renditionFixture(t)
	svc.FloodWaitRetry = instantRetryPolicy()
	client.doc.Thumbs = []tgclient.FileThumb{
		{Type: " ", Width: 4096, Height: 4096},
		{Type: "m", Width: 32, Height: 32},
		{Type: " x ", Width: 256, Height: 256},
	}
	client.thumbnail = func(_ context.Context, w io.Writer) error {
		if client.thumbs.Load() == 1 {
			if _, err := w.Write([]byte("partial attempt")); err != nil {
				return err
			}
			return tgclient.ErrInjectedTransport
		}
		_, err := w.Write(tinyPNG)
		return err
	}
	selected := &selectedPreviewThumbnailClient{renditionClient: client}
	svc.TG = selected
	payload, err := svc.PreviewThumbnail(t.Context(), personalChannelID, 91)
	if err != nil {
		t.Fatal(err)
	}
	got, err := base64.StdEncoding.DecodeString(payload.DataBase64)
	if err != nil || !bytes.Equal(got, tinyPNG) {
		t.Fatalf("retried payload=%x error=%v, want PNG without failed-attempt prefix", got, err)
	}
	if len(selected.types) != 2 || selected.types[0] != "x" || selected.types[1] != "x" || client.originals.Load() != 0 {
		t.Fatalf("thumbnail requests=%v original reads=%d, want two reads of x only", selected.types, client.originals.Load())
	}
}

func TestPreviewThumbnailRejectsUnusablePayloads(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      []byte
		remoteErr error
		want      error
	}{
		{name: "empty response", want: errPreviewDownloadFailed},
		{name: "permanent remote error", remoteErr: errors.New("FILE_REFERENCE_EXPIRED"), want: errPreviewDownloadFailed},
		{name: "encoded payload exceeds budget", body: make([]byte, 8<<20), want: errPreviewTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, client := renditionFixture(t)
			client.doc.Thumbs = []tgclient.FileThumb{{Type: "m", Width: 128, Height: 128}}
			client.thumbnail = func(_ context.Context, w io.Writer) error {
				if tc.remoteErr != nil {
					return tc.remoteErr
				}
				_, err := w.Write(tc.body)
				return err
			}
			payload, err := svc.PreviewThumbnail(t.Context(), personalChannelID, 91)
			if !errors.Is(err, tc.want) || payload.DataBase64 != "" {
				t.Fatalf("payload=%+v error=%v, want empty result and %v", payload, err, tc.want)
			}
			if client.thumbs.Load() != 1 || client.originals.Load() != 0 {
				t.Fatalf("thumbnail reads=%d original reads=%d, want one failed thumbnail read", client.thumbs.Load(), client.originals.Load())
			}
		})
	}
}

func TestPreviewThumbnailRejectsMissingAndUnsupportedFiles(t *testing.T) {
	svc, _, fake, _ := newTestService(t)
	fake.SeedHistory(tgclient.HistoryMessage{MsgID: 92, HasMedia: true, DocumentName: "document.pdf"})
	for _, tc := range []struct {
		id   int
		want error
	}{
		{0, errPreviewNotFound},
		{91, errPreviewNotFound},
		{92, errPreviewNotSupported},
	} {
		payload, err := svc.PreviewThumbnail(t.Context(), personalChannelID, tc.id)
		if !errors.Is(err, tc.want) || payload.DataBase64 != "" {
			t.Errorf("file %d: payload=%+v error=%v, want %v", tc.id, payload, err, tc.want)
		}
	}
}

func TestPreviewThumbnailDoesNotReplaceMissingOrOversizedInlineImageWithOriginal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		thumbs []tgclient.FileThumb
		want   error
	}{
		{name: "missing thumbnail", thumbs: []tgclient.FileThumb{{Type: " "}}, want: errPreviewDownloadFailed},
		{name: "oversized inline image", thumbs: []tgclient.FileThumb{{Bytes: make([]byte, 8<<20)}}, want: errPreviewTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, client := renditionFixture(t)
			client.doc.Thumbs = tc.thumbs
			payload, err := svc.PreviewThumbnail(t.Context(), personalChannelID, 91)
			if !errors.Is(err, tc.want) || payload.DataBase64 != "" {
				t.Fatalf("payload=%+v error=%v, want no payload and %v", payload, err, tc.want)
			}
			if client.thumbs.Load() != 0 || client.originals.Load() != 0 {
				t.Fatal("unavailable inline preview initiated a remote transfer")
			}
		})
	}
}
