package main

import (
	"testing"

	"github.com/gotd/td/tg"
)

func TestPreviewMimeTypeForName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		filename string
		want     string
		ok       bool
	}{
		{name: "jpg", filename: "photo.jpg", want: "image/jpeg", ok: true},
		{name: "uppercase png", filename: "PHOTO.PNG", want: "image/png", ok: true},
		{name: "svg", filename: "vector.svg", want: "image/svg+xml", ok: true},
		{name: "unsupported", filename: "doc.pdf", want: "", ok: false},
		{name: "missing ext", filename: "README", want: "", ok: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := previewMimeTypeForName(tt.filename)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("previewMimeTypeForName(%q) = (%q, %v), want (%q, %v)", tt.filename, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestEstimatedBase64Size(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw  int64
		want int64
	}{
		{raw: 0, want: 0},
		{raw: 1, want: 4},
		{raw: 2, want: 4},
		{raw: 3, want: 4},
		{raw: 4, want: 8},
		{raw: 7_864_320, want: 10_485_760},
	}

	for _, tt := range tests {
		if got := estimatedBase64Size(tt.raw); got != tt.want {
			t.Fatalf("estimatedBase64Size(%d) = %d, want %d", tt.raw, got, tt.want)
		}
	}
}

func TestExceedsPreviewPayloadBudget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  int64
		want bool
	}{
		{name: "negative", raw: -1, want: true},
		{name: "exact limit", raw: 7_864_320, want: false},
		{name: "over limit", raw: 7_864_321, want: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := exceedsPreviewPayloadBudget(tt.raw); got != tt.want {
				t.Fatalf("exceedsPreviewPayloadBudget(%d) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestPreviewThumbTypeForDocument(t *testing.T) {
	t.Parallel()

	doc := &tg.Document{
		Thumbs: []tg.PhotoSizeClass{
			&tg.PhotoSize{Type: "m", W: 320, H: 320, Size: 12_000},
			&tg.PhotoSizeProgressive{Type: "x", W: 800, H: 800, Sizes: []int{9_000, 18_000, 28_000}},
			&tg.PhotoCachedSize{Type: "s", W: 90, H: 90, Bytes: []byte{0xff, 0xd8, 0xff, 0xdb, 0x00}},
		},
	}

	got, ok := previewThumbTypeForDocument(doc)
	if !ok || got != "x" {
		t.Fatalf("previewThumbTypeForDocument() = (%q, %v), want (%q, true)", got, ok, "x")
	}
}

func TestPreviewInlineThumbPayload(t *testing.T) {
	t.Parallel()

	doc := &tg.Document{
		Thumbs: []tg.PhotoSizeClass{
			&tg.PhotoCachedSize{Type: "s", W: 90, H: 90, Bytes: []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43, 0x00}},
		},
	}

	payload, ok, err := previewInlineThumbPayload(doc, "image/jpeg")
	if err != nil {
		t.Fatalf("previewInlineThumbPayload() error = %v", err)
	}
	if !ok {
		t.Fatal("previewInlineThumbPayload() ok = false, want true")
	}
	if payload.MimeType != "image/jpeg" {
		t.Fatalf("previewInlineThumbPayload() mime = %q, want %q", payload.MimeType, "image/jpeg")
	}
	if payload.DataBase64 == "" {
		t.Fatal("previewInlineThumbPayload() returned empty data")
	}
}
