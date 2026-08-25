package projection

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const maxPortableNameBytes = 240

var ErrInvalidPortableName = errors.New("projection: invalid portable name")

var windowsReservedBaseNames = map[string]struct{}{
	"con": {}, "prn": {}, "aux": {}, "nul": {}, "clock$": {},
	"com1": {}, "com2": {}, "com3": {}, "com4": {}, "com5": {},
	"com6": {}, "com7": {}, "com8": {}, "com9": {},
	"com¹": {}, "com²": {}, "com³": {},
	"lpt1": {}, "lpt2": {}, "lpt3": {}, "lpt4": {}, "lpt5": {},
	"lpt6": {}, "lpt7": {}, "lpt8": {}, "lpt9": {},
	"lpt¹": {}, "lpt²": {}, "lpt³": {},
}

// CanonicalNameKey validates a cross-platform filename and derives the
// deterministic sibling key used by every projector. The key is NFC-normalized
// and Unicode case-folded so macOS, Windows, and Linux converge.
func CanonicalNameKey(name string) (string, error) {
	if !utf8.ValidString(name) {
		return "", fmt.Errorf("%w: invalid UTF-8", ErrInvalidPortableName)
	}
	normalized := norm.NFC.String(name)
	if normalized == "" || normalized == "." || normalized == ".." {
		return "", fmt.Errorf("%w: empty or reserved component", ErrInvalidPortableName)
	}
	if len([]byte(normalized)) > maxPortableNameBytes {
		return "", fmt.Errorf("%w: component exceeds %d bytes", ErrInvalidPortableName, maxPortableNameBytes)
	}
	if strings.TrimRight(normalized, " .") != normalized {
		return "", fmt.Errorf("%w: trailing space or period", ErrInvalidPortableName)
	}
	for _, r := range normalized {
		if r < 0x20 || unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) ||
			strings.ContainsRune(`<>:"/\|?*`, r) {
			return "", fmt.Errorf("%w: forbidden character", ErrInvalidPortableName)
		}
	}
	base := normalized
	if dot := strings.IndexRune(base, '.'); dot >= 0 {
		base = base[:dot]
	}
	if _, reserved := windowsReservedBaseNames[cases.Fold().String(base)]; reserved {
		return "", fmt.Errorf("%w: reserved Windows device name", ErrInvalidPortableName)
	}
	return cases.Fold().String(normalized), nil
}

func objectKind(objectID string) (string, error) {
	switch {
	case IsFileID(objectID):
		return "file", nil
	case IsFolderID(objectID):
		return "folder", nil
	default:
		return "", fmt.Errorf("%w: object id must be f: or d:", ErrBadOp)
	}
}

func legacyPortableName(name, kind, objectID string) string {
	name = norm.NFC.String(strings.ToValidUTF8(name, "_"))
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) ||
			strings.ContainsRune(`<>:"/\|?*`, r) {
			b.WriteRune('_')
			continue
		}
		b.WriteRune(r)
	}
	candidate := strings.TrimRight(b.String(), " .")
	if candidate == "" || candidate == "." || candidate == ".." {
		candidate = "_"
	}
	if _, err := CanonicalNameKey(candidate); err == nil {
		return candidate
	}
	// A leading underscore also makes Windows device basenames portable.
	candidate = "_" + candidate
	return truncatePortableName(candidate, "")
}

func legacyCollisionAlias(name, kind, objectID string, attempt int) string {
	base := legacyPortableName(name, kind, objectID)
	token := strings.TrimPrefix(objectID, FileIDPrefix)
	token = strings.TrimPrefix(token, FolderIDPrefix)
	if len(token) > 12 {
		token = token[len(token)-12:]
	}
	suffix := fmt.Sprintf(" (%s %s)", kind, token)
	if attempt > 0 {
		suffix = fmt.Sprintf(" (%s %s-%d)", kind, token, attempt)
	}
	return truncatePortableName(base, suffix)
}

func truncatePortableName(name, suffix string) string {
	limit := maxPortableNameBytes - len([]byte(suffix))
	if limit < 1 {
		limit = 1
	}
	for len([]byte(name)) > limit {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	name = strings.TrimRight(name, " .")
	if name == "" {
		name = "_"
	}
	return name + suffix
}
