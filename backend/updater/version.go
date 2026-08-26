package updater

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Version is a parsed semantic version. Build metadata ("+abc") is dropped
// because it never participates in precedence.
type Version struct {
	Major, Minor, Patch int
	Pre                 []string
}

var errInvalidVersion = errors.New("invalid version")

// ParseVersion accepts "1.7.0", "v1.7.0" and pre-release forms such as
// "v1.7.0-rc.1". Anything else — including the "dev" placeholder that local
// builds carry — is rejected, which is how the service decides to switch
// itself off.
func ParseVersion(s string) (Version, error) {
	body := strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexByte(body, '+'); i >= 0 {
		body = body[:i]
	}
	var v Version
	core := body
	if i := strings.IndexByte(body, '-'); i >= 0 {
		core = body[:i]
		v.Pre = strings.Split(body[i+1:], ".")
		for _, id := range v.Pre {
			if !validIdentifier(id) {
				return Version{}, fmt.Errorf("%w: %q", errInvalidVersion, s)
			}
		}
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("%w: %q", errInvalidVersion, s)
	}
	var nums [3]int
	for i, part := range parts {
		n, ok := parseNumeric(part)
		if !ok {
			return Version{}, fmt.Errorf("%w: %q", errInvalidVersion, s)
		}
		nums[i] = n
	}
	v.Major, v.Minor, v.Patch = nums[0], nums[1], nums[2]
	return v, nil
}

// String renders the canonical form without a "v" prefix, e.g. "1.7.0-rc.1".
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if len(v.Pre) > 0 {
		s += "-" + strings.Join(v.Pre, ".")
	}
	return s
}

// Compare returns -1, 0 or +1 following semver precedence rules: numeric
// core first, then a release outranks any pre-release of the same core, then
// pre-release identifiers left to right (numeric < alphanumeric, shorter
// prefix < longer).
func Compare(a, b Version) int {
	if c := compareInt(a.Major, b.Major); c != 0 {
		return c
	}
	if c := compareInt(a.Minor, b.Minor); c != 0 {
		return c
	}
	if c := compareInt(a.Patch, b.Patch); c != 0 {
		return c
	}
	switch {
	case len(a.Pre) == 0 && len(b.Pre) == 0:
		return 0
	case len(a.Pre) == 0:
		return 1
	case len(b.Pre) == 0:
		return -1
	}
	for i := 0; i < len(a.Pre) && i < len(b.Pre); i++ {
		if c := compareIdentifier(a.Pre[i], b.Pre[i]); c != 0 {
			return c
		}
	}
	return compareInt(len(a.Pre), len(b.Pre))
}

// Newer reports whether v is strictly newer than other.
func (v Version) Newer(other Version) bool {
	return Compare(v, other) > 0
}

func compareIdentifier(a, b string) int {
	an, aNum := parseNumeric(a)
	bn, bNum := parseNumeric(b)
	switch {
	case aNum && bNum:
		return compareInt(an, bn)
	case aNum:
		return -1
	case bNum:
		return 1
	}
	return strings.Compare(a, b)
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// parseNumeric accepts canonical decimal identifiers: digits only, no leading
// zeros (except "0" itself), within int range.
func parseNumeric(s string) (int, bool) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

func validIdentifier(s string) bool {
	if s == "" {
		return false
	}
	numeric := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '-':
			numeric = false
		default:
			return false
		}
	}
	if numeric {
		_, ok := parseNumeric(s)
		return ok
	}
	return true
}
