package projection

import "strings"

// RawAttachmentCaption admits ordinary Telegram attachments and unencrypted
// legacy uploads before sync has projected them. Internal bodies and encrypted
// metadata require their owning projection; reading them as loose files would
// bypass content relationships or expose ciphertext as previewable plaintext.
// A legacy caption's filename is authoritative over the document attribute.
func RawAttachmentCaption(text string, storedSize int64) (name string, allowed bool) {
	header := ExtractHeaderLine(text)
	if !strings.HasPrefix(strings.TrimSpace(header), "TDX1") {
		return "", true
	}
	op, err := Parse(header)
	if err != nil || op.Type != OpFileUpload || op.Encrypted ||
		op.EncryptionVersion != 0 || op.PlaintextSize != 0 || op.UploadUUID != "" ||
		(op.FileSize != 0 && op.FileSize != storedSize) {
		return "", false
	}
	// The legacy upload parser ignores multipart fields belonging to other
	// operation types. A hinted internal body must still wait for projection.
	for field := range strings.SplitSeq(header, "|") {
		if key, value, ok := strings.Cut(field, "="); ok && key == "u" && value != "" {
			return "", false
		}
	}
	return op.Name, true
}
