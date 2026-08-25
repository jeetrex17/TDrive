package mountfs

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	maxPortableNameBytes = 240
	aliasTokenBytes      = 8
)

var portableFold = cases.Fold()

type snapshotEntry struct {
	entry  Entry
	source SourceEntry
}

// directorySnapshot is immutable after construction. Keeping it private lets
// the cache share its slice and lookup map without exposing mutable state.
type directorySnapshot struct {
	entries []snapshotEntry
	byName  map[string]int
}

func buildDirectorySnapshot(channelID int64, parentID string, sourceEntries []SourceEntry) (directorySnapshot, error) {
	entries := make([]snapshotEntry, len(sourceEntries))
	bases := make([]string, len(sourceEntries))
	ids := make(map[string]struct{}, len(sourceEntries))
	baseGroups := make(map[string][]int, len(sourceEntries))

	for index, sourceEntry := range sourceEntries {
		if err := validateSourceEntry(parentID, sourceEntry); err != nil {
			return directorySnapshot{}, err
		}
		if _, exists := ids[sourceEntry.ID]; exists {
			return directorySnapshot{}, fmt.Errorf("%w: duplicate ID %q in parent %q", ErrInvalidEntry, sourceEntry.ID, parentID)
		}
		ids[sourceEntry.ID] = struct{}{}

		base := portableName(sourceEntry.Name)
		bases[index] = base
		baseGroups[nameKey(base)] = append(baseGroups[nameKey(base)], index)
		entries[index] = snapshotEntry{
			source: sourceEntry,
			entry: Entry{
				ChannelID:  channelID,
				ID:         sourceEntry.ID,
				ParentID:   sourceEntry.ParentID,
				SourceName: sourceEntry.Name,
				Kind:       sourceEntry.Kind,
				Size:       sourceEntry.Size,
				ModTime:    sourceEntry.ModTime,
				Encrypted:  sourceEntry.Encrypted,
				ContentRef: sourceEntry.ContentRef,
			},
		}
	}

	if err := assignUniqueNames(entries, bases, baseGroups); err != nil {
		return directorySnapshot{}, err
	}
	sortSnapshotEntries(entries)

	byName := make(map[string]int, len(entries))
	for index, entry := range entries {
		byName[nameKey(entry.entry.Name)] = index
	}
	return directorySnapshot{entries: entries, byName: byName}, nil
}

func validateSourceEntry(parentID string, entry SourceEntry) error {
	if entry.ID == RootID {
		return fmt.Errorf("%w: child has empty ID", ErrInvalidEntry)
	}
	if entry.ParentID != parentID {
		return fmt.Errorf("%w: entry %q belongs to parent %q, not %q", ErrInvalidEntry, entry.ID, entry.ParentID, parentID)
	}
	if entry.Kind != KindFile && entry.Kind != KindDirectory {
		return fmt.Errorf("%w: entry %q has kind %q", ErrInvalidEntry, entry.ID, entry.Kind)
	}
	if entry.Size < 0 {
		return fmt.Errorf("%w: entry %q has negative size", ErrInvalidEntry, entry.ID)
	}
	return nil
}

func assignUniqueNames(entries []snapshotEntry, bases []string, baseGroups map[string][]int) error {
	// Reserve every legacy base before allocating aliases. A unique legacy name
	// always keeps its portable spelling, even when it resembles an alias. With
	// 64-bit per-object tokens, candidate sequences are independent in
	// expectation; every rejected candidate consumes an existing reserved name.
	// Sorting dominates the expected O(k log k) allocation cost.
	reserved := make(map[string]struct{}, len(baseGroups))
	assigned := make(map[string]struct{}, len(entries))
	aliasIndexes := make([]int, 0, len(entries))
	for key, indexes := range baseGroups {
		reserved[key] = struct{}{}
		if len(indexes) == 1 {
			index := indexes[0]
			entries[index].entry.Name = bases[index]
			assigned[key] = struct{}{}
			continue
		}
		aliasIndexes = append(aliasIndexes, indexes...)
	}
	sort.Slice(aliasIndexes, func(left, right int) bool {
		leftIndex := aliasIndexes[left]
		rightIndex := aliasIndexes[right]
		leftKey := nameKey(bases[leftIndex])
		rightKey := nameKey(bases[rightIndex])
		if leftKey != rightKey {
			return leftKey < rightKey
		}
		if entries[leftIndex].source.Kind != entries[rightIndex].source.Kind {
			return entries[leftIndex].source.Kind < entries[rightIndex].source.Kind
		}
		return entries[leftIndex].source.ID < entries[rightIndex].source.ID
	})

	maxRounds := 2*len(entries) + 1
	for _, index := range aliasIndexes {
		allocated := false
		for round := 1; round <= maxRounds; round++ {
			candidate := collisionAlias(bases[index], entries[index].source.Kind, entries[index].source.ID, round)
			key := nameKey(candidate)
			if _, exists := reserved[key]; exists {
				continue
			}
			if _, exists := assigned[key]; exists {
				continue
			}
			entries[index].entry.Name = candidate
			assigned[key] = struct{}{}
			allocated = true
			break
		}
		if !allocated {
			return fmt.Errorf("%w: unable to allocate alias for %q", ErrInvalidEntry, entries[index].source.ID)
		}
	}
	return nil
}

func sortSnapshotEntries(entries []snapshotEntry) {
	sort.Slice(entries, func(left, right int) bool {
		leftKey := nameKey(entries[left].entry.Name)
		rightKey := nameKey(entries[right].entry.Name)
		if leftKey != rightKey {
			return leftKey < rightKey
		}
		if entries[left].entry.Name != entries[right].entry.Name {
			return entries[left].entry.Name < entries[right].entry.Name
		}
		if entries[left].entry.Kind != entries[right].entry.Kind {
			return entries[left].entry.Kind == KindDirectory
		}
		return entries[left].entry.ID < entries[right].entry.ID
	})
}

func portableName(sourceName string) string {
	name := norm.NFC.String(strings.ToValidUTF8(sourceName, "�"))
	runes := []rune(name)
	for index, character := range runes {
		if unicode.IsControl(character) || strings.ContainsRune(`<>:"/\\|?*`, character) {
			runes[index] = '_'
		}
	}
	for index := len(runes) - 1; index >= 0 && (runes[index] == '.' || runes[index] == ' '); index-- {
		runes[index] = '_'
	}
	name = string(runes)
	if name == "" {
		name = "_unnamed"
	}
	if isWindowsReservedName(name) {
		name = "_" + name
	}
	if len(name) <= maxPortableNameBytes {
		return name
	}
	stem, extension := splitShortExtension(name, KindFile)
	return truncateUTF8(stem, maxPortableNameBytes-len(extension)) + extension
}

func isWindowsReservedName(name string) bool {
	stem := name
	if index := strings.IndexByte(stem, '.'); index >= 0 {
		stem = stem[:index]
	}
	stem = strings.ToUpper(stem)
	switch stem {
	case "CON", "PRN", "AUX", "NUL", "CLOCK$":
		return true
	}
	characters := []rune(stem)
	if len(characters) == 4 && (string(characters[:3]) == "COM" || string(characters[:3]) == "LPT") {
		switch characters[3] {
		case '1', '2', '3', '4', '5', '6', '7', '8', '9', '\u00b9', '\u00b2', '\u00b3':
			return true
		}
	}
	return false
}

func collisionAlias(base string, kind Kind, id string, round int) string {
	digest := sha256.Sum256([]byte(string(kind) + "\x00" + id))
	kindMarker := "f"
	if kind == KindDirectory {
		kindMarker = "d"
	}
	suffix := fmt.Sprintf(" [td-%s-%x]", kindMarker, digest[:aliasTokenBytes])
	if round > 1 {
		suffix = fmt.Sprintf(" [td-%s-%x-%d]", kindMarker, digest[:aliasTokenBytes], round)
	}

	stem, extension := splitShortExtension(base, kind)
	available := maxPortableNameBytes - len(suffix) - len(extension)
	if available < 1 {
		extension = ""
		available = maxPortableNameBytes - len(suffix)
	}
	return truncateUTF8(stem, available) + suffix + extension
}

func splitShortExtension(name string, kind Kind) (stem string, extension string) {
	if kind != KindFile {
		return name, ""
	}
	index := strings.LastIndexByte(name, '.')
	if index <= 0 || len(name)-index > 32 {
		return name, ""
	}
	return name[:index], name[index:]
}

func nameKey(name string) string {
	return portableFold.String(norm.NFC.String(name))
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	end := 0
	for index, character := range value {
		width := utf8.RuneLen(character)
		if index+width > maxBytes {
			break
		}
		end = index + width
	}
	return value[:end]
}
