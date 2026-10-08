package projection

import "testing"

func TestRawAttachmentCaptionAdmitsOnlyPlainLegacyFiles(t *testing.T) {
	tests := []struct {
		name, caption, wantName string
		allowed                 bool
	}{
		{"ordinary attachment", "A forwarded document", "", true},
		{"multiline legacy caption", "TDX1|t=f|p=|n=caption.pdf|sz=12\nTDrive: caption.pdf", "caption.pdf", true},
		{"missing legacy size", "TDX1|t=f|n=caption.pdf", "caption.pdf", true},
		{"mismatching size", "TDX1|t=f|n=caption.pdf|sz=11", "", false},
		{"malformed header", "TDX1|t=f|p=bad|n=caption.pdf", "", false},
		{"unknown control operation", "TDX1|t=unknown", "", false},
		{"internal part", "TDX1|t=part|u=upload|pix=0|sz=12", "", false},
		{"encrypted legacy", "TDX1|t=f|n=caption.pdf|enc=1|psz=12|ev=1", "", false},
		{"version encryption hint", "TDX1|t=f|n=caption.pdf|ev=1", "", false},
		{"plaintext encryption hint", "TDX1|t=f|n=caption.pdf|psz=12", "", false},
		{"multipart upload hint", "TDX1|t=f|n=caption.pdf|u=upload", "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			name, allowed := RawAttachmentCaption(test.caption, 12)
			if name != test.wantName || allowed != test.allowed {
				t.Fatalf("RawAttachmentCaption(%q, 12) = (%q, %v), want (%q, %v)", test.caption, name, allowed, test.wantName, test.allowed)
			}
		})
	}
}
