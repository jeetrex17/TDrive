// Package mountsafe sanitizes mount status text before it crosses a user-facing
// process boundary.
package mountsafe

import (
	"errors"
	"strings"
	"unicode"
)

const (
	fallbackMessage = "Mount operation failed; retry or check the app logs"
	maxMessageRunes = 240
)

// Message normalizes user-facing mount text and replaces capability-bearing
// details with a fixed safe message.
func Message(message string) string {
	message = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return ' '
		}
		return character
	}, message)
	message = strings.Join(strings.Fields(message), " ")
	if ContainsSensitive(message) {
		return fallbackMessage
	}
	runes := []rune(message)
	if len(runes) > maxMessageRunes {
		return string(runes[:maxMessageRunes])
	}
	return message
}

// SanitizeError preserves safe error identity and replaces errors that carry
// private mount endpoint details.
func SanitizeError(err error) error {
	if err == nil || !ContainsSensitive(err.Error()) {
		return err
	}
	return errors.New(fallbackMessage)
}

// ContainsSensitive reports whether text may reveal a private mount endpoint
// or its bearer capability.
func ContainsSensitive(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "tdrive-") ||
		strings.Contains(lower, "http://") ||
		strings.Contains(lower, "https://") ||
		strings.Contains(lower, "dav://") ||
		strings.Contains(lower, "localhost") ||
		strings.Contains(lower, "127.0.0.1")
}
