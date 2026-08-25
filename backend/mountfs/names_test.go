package mountfs

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"TDrive/backend/mountpath"
)

func TestPortableNamesNormalizeUnicodeBeforeCollisionDetection(t *testing.T) {
	t.Parallel()

	source := newFakeDirectorySource(map[string][]SourceEntry{
		RootID: {
			{ID: "f:nfc", ParentID: RootID, Name: "Caf\u00e9.txt", Kind: KindFile},
			{ID: "f:nfd", ParentID: RootID, Name: "Cafe\u0301.TXT", Kind: KindFile},
		},
	})
	fs := mustNewFS(t, 42, source, &fakeContentOpener{})

	entries, err := fs.ReadDir(context.Background(), "/")
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("ReadDir() entries = %d, want 2", len(entries))
	}
	if NameKey(entries[0].Name) == NameKey(entries[1].Name) {
		t.Fatalf("normalized Unicode names still collide: %#v", entries)
	}
	for _, entry := range entries {
		if !strings.Contains(entry.Name, "[td-f-") {
			t.Errorf("normalized collision did not receive a file alias: %q", entry.Name)
		}
	}
}

func TestPortableNameBoundsLongNamesAndPreservesShortExtensions(t *testing.T) {
	t.Parallel()

	longName := strings.Repeat("\u754c", 200) + ".txt"
	base := portableName(longName)
	alias := collisionAlias(base, KindFile, "f:long", 1)

	if len(base) > maxPortableNameBytes || !utf8.ValidString(base) {
		t.Fatalf("portableName() produced invalid or oversized UTF-8: bytes=%d", len(base))
	}
	if len(alias) > maxPortableNameBytes || !utf8.ValidString(alias) {
		t.Fatalf("collisionAlias() produced invalid or oversized UTF-8: bytes=%d", len(alias))
	}
	if !strings.HasSuffix(alias, ".txt") {
		t.Fatalf("collisionAlias() = %q, want .txt extension preserved", alias)
	}

	truncationExposesSpaces := strings.Repeat("a", maxPortableNameBytes-2) + "  x"
	if got := portableName(truncationExposesSpaces); strings.HasSuffix(got, " ") || strings.HasSuffix(got, ".") {
		t.Fatalf("portableName() truncation exposed a forbidden suffix: %q", got)
	}
}

func TestPortableNameHandlesWindowsDeviceNamesWithExtensions(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"con.txt":  "_con.txt",
		"PRN.log":  "_PRN.log",
		"aux":      "_aux",
		"NUL.data": "_NUL.data",
		"COM1.txt": "_COM1.txt",
		"lpt9":     "_lpt9",
		"COM¹.txt": "_COM¹.txt",
		"com²":     "_com²",
		"LPT³.log": "_LPT³.log",
		"COM0.txt": "COM0.txt",
	}
	for source, expected := range tests {
		if got := portableName(source); got != expected {
			t.Errorf("portableName(%q) = %q, want %q", source, got, expected)
		}
	}
}

func TestAdversarialAliasLikeNamesKeepTheirUniquePortableNames(t *testing.T) {
	t.Parallel()

	entries := adversarialAliasEntries(258)
	snapshot, err := buildDirectorySnapshot(42, RootID, entries)
	if err != nil {
		t.Fatalf("buildDirectorySnapshot() error = %v", err)
	}
	byID := make(map[string]Entry, len(snapshot.entries))
	for _, entry := range snapshot.entries {
		byID[entry.entry.ID] = entry.entry
	}
	for _, source := range entries[2:] {
		if got := byID[source.ID].Name; got != source.Name {
			t.Fatalf("unique alias-like entry %q renamed to %q", source.Name, got)
		}
	}
}

func BenchmarkBuildAdversarialAliasSnapshot(b *testing.B) {
	for _, size := range []int{100, 1_000, 10_000} {
		entries := adversarialAliasEntries(size)
		b.Run(fmt.Sprintf("entries-%d", size), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, err := buildDirectorySnapshot(42, RootID, entries); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func adversarialAliasEntries(count int) []SourceEntry {
	base := SourceEntry{ID: "f:base", ParentID: RootID, Name: "notes.txt", Kind: KindFile}
	entries := []SourceEntry{
		base,
		{ID: "f:case", ParentID: RootID, Name: "NOTES.TXT", Kind: KindFile},
	}
	for round := 1; len(entries) < count; round++ {
		entries = append(entries, SourceEntry{
			ID:       fmt.Sprintf("f:alias-like-%06d", round),
			ParentID: RootID,
			Name:     collisionAlias(portableName(base.Name), base.Kind, base.ID, round),
			Kind:     KindFile,
		})
	}
	return entries
}

func TestPortableNameReplacesAllUnicodeControlCharacters(t *testing.T) {
	t.Parallel()

	if got := portableName("before\u0085after.txt"); got != "before_after.txt" {
		t.Fatalf("portableName() = %q, want C1 control replaced", got)
	}
	if got := portableName("report\u202efdp.exe"); got != "report_fdp.exe" {
		t.Fatalf("portableName() = %q, want bidi control replaced", got)
	}
	for _, name := range []string{
		"family-\U0001f468\u200d\U0001f469\u200d\U0001f467.txt",
		"Persian-می\u200cروم.txt",
	} {
		if got := portableName(name); got != name {
			t.Fatalf("portableName(%q) = %q, want join controls preserved", name, got)
		}
	}
}

func TestBidiReplacementCollisionAliasesAreStable(t *testing.T) {
	t.Parallel()

	forward := []SourceEntry{
		{ID: "f:bidi", ParentID: RootID, Name: "report\u202efdp.exe", Kind: KindFile},
		{ID: "f:literal", ParentID: RootID, Name: "report_fdp.exe", Kind: KindFile},
	}
	reversed := []SourceEntry{forward[1], forward[0]}

	aliasByID := func(entries []SourceEntry) map[string]string {
		t.Helper()
		snapshot, err := buildDirectorySnapshot(42, RootID, entries)
		if err != nil {
			t.Fatalf("buildDirectorySnapshot() error = %v", err)
		}
		aliases := make(map[string]string, len(snapshot.entries))
		for _, entry := range snapshot.entries {
			aliases[entry.entry.ID] = entry.entry.Name
		}
		return aliases
	}

	first := aliasByID(forward)
	second := aliasByID(reversed)
	if first["f:bidi"] != second["f:bidi"] || first["f:literal"] != second["f:literal"] {
		t.Fatalf("aliases depend on input order: forward=%v reversed=%v", first, second)
	}
	if NameKey(first["f:bidi"]) == NameKey(first["f:literal"]) {
		t.Fatalf("replacement collision was not disambiguated: %v", first)
	}
	for id, name := range first {
		if !strings.Contains(name, "[td-f-") {
			t.Errorf("collision alias for %s = %q, want stable token", id, name)
		}
		for _, character := range name {
			if unicode.Is(unicode.Bidi_Control, character) {
				t.Errorf("collision alias for %s retained bidi control U+%04X", id, character)
			}
		}
	}
}

func FuzzPortableNameProperties(f *testing.F) {
	for _, seed := range []string{
		"report.txt",
		"CON",
		"bad<name>.txt",
		"report\u202efdp.exe",
		"trailing. ",
		"Cafe\u0301.txt",
		string([]byte{0xff, 'a'}),
		strings.Repeat("\u754c", 200),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, source string) {
		name := portableName(source)
		if name == "" {
			t.Fatal("portableName() returned an empty name")
		}
		if !utf8.ValidString(name) {
			t.Fatalf("portableName() returned invalid UTF-8: %q", name)
		}
		if len(name) > maxPortableNameBytes {
			t.Fatalf("portableName() returned %d bytes, max is %d", len(name), maxPortableNameBytes)
		}
		if strings.ContainsAny(name, `<>:"/\\|?*`) || strings.ContainsRune(name, '\x00') {
			t.Fatalf("portableName() retained a forbidden character: %q", name)
		}
		for _, character := range name {
			if unicode.IsControl(character) || unicode.Is(unicode.Bidi_Control, character) {
				t.Fatalf("portableName() retained unsafe Unicode control U+%04X: %q", character, name)
			}
		}
		if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
			t.Fatalf("portableName() retained a forbidden trailing character: %q", name)
		}
		if mountpath.IsWindowsReservedComponent(name) {
			t.Fatalf("portableName() retained a Windows device name: %q", name)
		}
	})
}
