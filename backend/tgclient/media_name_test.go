package tgclient

import (
	"strings"
	"testing"
)

func TestMediaNamePreservesNamesAndRecognizesUnnamedAttachments(t *testing.T) {
	tests := []struct {
		name, filename, mime, want string
	}{
		{"supplied filename", " Clip.MP4 ", "application/octet-stream", "Clip.MP4"},
		{"unknown document remains downloadable", "archive.bin", "video/mp4", "archive.bin"},
		{"extensionless video", "holiday", "video/mp4", "holiday.mp4"},
		{"unnamed video", "", "video/x-matroska", "Telegram file 42.mkv"},
		{"voice message", "", "audio/ogg", "Telegram file 42.ogg"},
		{"unnamed PDF", "", "application/pdf", "Telegram file 42.pdf"},
		{"unnamed image", "", "image/jpeg", "Telegram file 42.jpg"},
		{"text with charset", "", "Text/Plain; charset=utf-8", "Telegram file 42.txt"},
		{"JSON document", "", "application/json", "Telegram file 42.json"},
		{"unknown MIME", "", "application/octet-stream", "Telegram file 42"},
		{"unix path", "../notes.pdf", "application/pdf", "notes.pdf"},
		{"windows path", `C:\clips\clip.mp4`, "video/mp4", "clip.mp4"},
		{"invalid component", ".", "image/png", "Telegram file 42.png"},
		{"control character", "bad\x00name.pdf", "application/pdf", "Telegram file 42.pdf"},
		{"overlong filename", strings.Repeat("a", 256), "application/pdf", "Telegram file 42.pdf"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := MediaName(HistoryMessage{MsgID: 42, DocumentName: test.filename, MimeType: test.mime})
			if got != test.want {
				t.Fatalf("MediaName(%q, %q) = %q, want %q", test.filename, test.mime, got, test.want)
			}
		})
	}
}
