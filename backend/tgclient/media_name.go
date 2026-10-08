package tgclient

import (
	"fmt"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// mediaExtensions is deterministic across hosts. mime.ExtensionsByType can
// depend on OS MIME registries, causing clients to project different names.
var mediaExtensions = map[string]string{
	"video/mp4": ".mp4", "video/quicktime": ".mov", "video/webm": ".webm",
	"video/x-matroska": ".mkv", "video/x-msvideo": ".avi", "video/mp2t": ".ts",
	"video/x-flv": ".flv", "video/x-ms-wmv": ".wmv", "video/ogg": ".ogv", "video/mpeg": ".mpeg",
	"audio/mpeg": ".mp3", "audio/mp4": ".m4a", "audio/aac": ".aac",
	"audio/ogg": ".ogg", "audio/opus": ".opus", "audio/flac": ".flac", "audio/x-flac": ".flac",
	"audio/wav": ".wav", "audio/x-wav": ".wav",
	"application/pdf": ".pdf", "image/jpeg": ".jpg", "image/png": ".png",
	"image/gif": ".gif", "image/webp": ".webp", "image/bmp": ".bmp", "image/x-ms-bmp": ".bmp",
	"text/plain": ".txt", "text/markdown": ".md", "text/csv": ".csv", "text/tab-separated-values": ".tsv",
	"application/json": ".json", "application/yaml": ".yaml", "text/yaml": ".yaml",
	"application/toml": ".toml", "application/xml": ".xml", "text/xml": ".xml",
	"application/x-subrip": ".srt", "text/vtt": ".vtt",
}

// MediaName supplies one consistent filename for listing, adoption and preview.
// Supplied extensions are preserved, including unsupported documents that can
// still be downloaded. Only a missing extension is inferred from Telegram MIME.
// The inferred extension identifies the container; it does not promise that a
// particular platform can decode its codecs.
func MediaName(message HistoryMessage) string {
	mimeType, _, _ := strings.Cut(message.MimeType, ";")
	ext := mediaExtensions[strings.ToLower(strings.TrimSpace(mimeType))]
	name := path.Base(strings.ReplaceAll(strings.TrimSpace(message.DocumentName), `\`, `/`))
	if name == "" || name == "." || name == ".." || name == "/" || len(name) > 255 ||
		!utf8.ValidString(name) || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return fmt.Sprintf("Telegram file %d%s", message.MsgID, ext)
	}
	if path.Ext(name) == "" && ext != "" {
		if len(name)+len(ext) > 255 {
			return fmt.Sprintf("Telegram file %d%s", message.MsgID, ext)
		}
		return name + ext
	}
	return name
}
