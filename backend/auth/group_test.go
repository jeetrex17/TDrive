package auth

import (
	"strings"
	"testing"
)

func TestParseInviteHashAccepts(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plus form", "t.me/+abc123XYZ", "abc123XYZ"},
		{"https plus", "https://t.me/+abc123XYZ", "abc123XYZ"},
		{"http plus", "http://t.me/+abc123XYZ", "abc123XYZ"},
		{"joinchat", "t.me/joinchat/abc123XYZ", "abc123XYZ"},
		{"https joinchat", "https://t.me/joinchat/abc123XYZ", "abc123XYZ"},
		{"telegram.me plus", "telegram.me/+abc123XYZ", "abc123XYZ"},
		{"trailing slash", "t.me/+abc123XYZ/", "abc123XYZ"},
		{"trailing query", "https://t.me/+abc123XYZ?si=track", "abc123XYZ"},
		{"trailing fragment", "t.me/+abc123XYZ#x", "abc123XYZ"},
		{"underscore + dash", "t.me/+ab-cd_EF", "ab-cd_EF"},
		{"bare hash", "abc123XYZ", "abc123XYZ"},
		{"whitespace padded", "  t.me/+abc123  ", "abc123"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseInviteHash(c.in)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got != c.want {
				t.Fatalf("ParseInviteHash(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestParseInviteHashRejects(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"only spaces", "   "},
		{"only host", "t.me/"},
		{"only plus", "t.me/+"},
		{"only joinchat", "t.me/joinchat/"},
		{"non-alnum char", "t.me/+abc!"},
		{"space in hash", "t.me/+abc def"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseInviteHash(c.in)
			if err == nil {
				t.Fatalf("expected error, got hash %q", got)
			}
			if !strings.Contains(err.Error(), "invite") && !strings.Contains(err.Error(), "hash") {
				t.Fatalf("err = %q, expected invite/hash mention", err.Error())
			}
		})
	}
}

func TestParseInviteHashTakesPrefixBeforeSlash(t *testing.T) {
	// Documented behavior: a stray slash mid-hash truncates at the slash.
	// "t.me/+abc/def" -> "abc". Telegram links don't legitimately have this
	// shape, but if a user pastes a malformed link we'd rather take the
	// prefix than crash. ParseInviteHash trims at /, ?, # uniformly.
	got, err := ParseInviteHash("t.me/+abc/def")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != "abc" {
		t.Fatalf("got %q, want abc", got)
	}
}
