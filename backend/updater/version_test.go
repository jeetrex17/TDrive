package updater

import "testing"

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"1.7.0", "1.7.0", true},
		{"v1.7.0", "1.7.0", true},
		{" v1.7.0 ", "1.7.0", true},
		{"v1.7.0-rc.1", "1.7.0-rc.1", true},
		{"1.7.0+build.5", "1.7.0", true},
		{"1.7.0-beta+exp", "1.7.0-beta", true},
		{"dev", "", false},
		{"", "", false},
		{"1.7", "", false},
		{"1.07.0", "", false},
		{"1.7.0-", "", false},
		{"1.7.0-rc..1", "", false},
		{"1.7.0-rc.01", "", false},
		{"1.7.0-rc_1", "", false},
		{"master", "", false},
		{"v1.7.0.1", "", false},
	}
	for _, tc := range cases {
		got, err := ParseVersion(tc.in)
		if tc.ok != (err == nil) {
			t.Errorf("ParseVersion(%q) err = %v, want ok=%v", tc.in, err, tc.ok)
			continue
		}
		if tc.ok && got.String() != tc.want {
			t.Errorf("ParseVersion(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCompareFollowsSemverPrecedence(t *testing.T) {
	ordered := []string{
		"0.9.9",
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.11",
		"1.0.0-rc.1",
		"1.0.0",
		"1.0.1",
		"1.1.0",
		"2.0.0",
	}
	for i := range ordered {
		for j := range ordered {
			a := mustVersion(t, ordered[i])
			b := mustVersion(t, ordered[j])
			got := Compare(a, b)
			want := compareInt(i, j)
			if got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", ordered[i], ordered[j], got, want)
			}
			if a.Newer(b) != (want > 0) {
				t.Errorf("%s.Newer(%s) = %v, want %v", ordered[i], ordered[j], a.Newer(b), want > 0)
			}
		}
	}
}

func TestCompareIgnoresBuildMetadata(t *testing.T) {
	a := mustVersion(t, "1.2.3+one")
	b := mustVersion(t, "1.2.3+two")
	if Compare(a, b) != 0 {
		t.Fatalf("build metadata must not affect precedence")
	}
}

func mustVersion(t *testing.T, s string) Version {
	t.Helper()
	v, err := ParseVersion(s)
	if err != nil {
		t.Fatalf("ParseVersion(%q): %v", s, err)
	}
	return v
}
