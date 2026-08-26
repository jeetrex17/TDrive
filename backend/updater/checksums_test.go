package updater

import (
	"strings"
	"testing"
)

func TestParseChecksums(t *testing.T) {
	const digest = "3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855e"
	input := strings.Join([]string{
		"# generated",
		"",
		digest + "  TDrive-v1.7.0-macos-arm64.zip",
		strings.ToUpper(digest) + " *TDrive-v1.7.0-windows-amd64.zip",
		digest + "  dist/TDrive-v1.7.0-linux-amd64.AppImage",
		digest + "  dist\\TDrive-v1.7.0-source.zip",
		digest + "  with space.zip",
	}, "\n")
	sums, err := parseChecksums(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parseChecksums: %v", err)
	}
	want := map[string]string{
		"TDrive-v1.7.0-macos-arm64.zip":      digest,
		"TDrive-v1.7.0-windows-amd64.zip":    digest,
		"TDrive-v1.7.0-linux-amd64.AppImage": digest,
		"TDrive-v1.7.0-source.zip":           digest,
		"with space.zip":                     digest,
	}
	if len(sums) != len(want) {
		t.Fatalf("got %d entries, want %d: %v", len(sums), len(want), sums)
	}
	for name, sum := range want {
		if sums[name] != sum {
			t.Errorf("sums[%q] = %q, want %q", name, sums[name], sum)
		}
	}
}

func TestParseChecksumsRejectsMalformedLines(t *testing.T) {
	for _, input := range []string{
		"abc  file.zip",
		"zz0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855e  file.zip",
		"3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855e",
	} {
		if _, err := parseChecksums(strings.NewReader(input)); err == nil {
			t.Errorf("parseChecksums(%q) succeeded, want error", input)
		}
	}
}
