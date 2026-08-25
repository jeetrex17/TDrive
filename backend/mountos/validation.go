package mountos

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	capabilityPrefix = "http://127.0.0.1:"
	capabilityMarker = "/tdrive-"
	capabilityBytes  = 64
	maxLabelRunes    = 64
	maxLabelBytes    = 255
)

func validateConfig(config Config) (validatedConfig, error) {
	endpoint, err := validateEndpoint(config.Endpoint)
	if err != nil {
		return validatedConfig{}, err
	}
	label, err := validateLabel(config.Label)
	if err != nil {
		return validatedConfig{}, err
	}
	drive, err := normalizeWindowsDrive(config.WindowsDrive)
	if err != nil {
		return validatedConfig{}, err
	}
	return validatedConfig{endpoint: endpoint, label: label, drive: drive}, nil
}

// validateEndpoint intentionally uses a narrow canonical grammar instead of a
// permissive URL parser. This rejects userinfo, alternate loopback spellings,
// encoded path components, queries, fragments, and scheme confusion in one
// place before any value reaches an OS command.
func validateEndpoint(value string) (string, error) {
	if !strings.HasPrefix(value, capabilityPrefix) {
		return "", ErrInvalidEndpoint
	}
	remainder := strings.TrimPrefix(value, capabilityPrefix)
	markerIndex := strings.Index(remainder, capabilityMarker)
	if markerIndex <= 0 {
		return "", ErrInvalidEndpoint
	}
	portText := remainder[:markerIndex]
	if len(portText) > 5 || portText[0] == '0' {
		return "", ErrInvalidEndpoint
	}
	for _, char := range portText {
		if char < '0' || char > '9' {
			return "", ErrInvalidEndpoint
		}
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", ErrInvalidEndpoint
	}

	capability := remainder[markerIndex+len(capabilityMarker):]
	if len(capability) != capabilityBytes+1 || capability[capabilityBytes] != '/' {
		return "", ErrInvalidEndpoint
	}
	for _, char := range capability[:capabilityBytes] {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return "", ErrInvalidEndpoint
		}
	}
	return value, nil
}

func validateLabel(value string) (string, error) {
	if value == "" || value != strings.TrimSpace(value) || !utf8.ValidString(value) {
		return "", ErrInvalidLabel
	}
	if len(value) > maxLabelBytes || utf8.RuneCountInString(value) > maxLabelRunes {
		return "", ErrInvalidLabel
	}
	for _, char := range value {
		if unicode.Is(unicode.C, char) {
			return "", ErrInvalidLabel
		}
	}
	return value, nil
}

func normalizeWindowsDrive(value string) (string, error) {
	if value == "" {
		return "T:", nil
	}
	if len(value) != 2 || value[1] != ':' {
		return "", ErrInvalidDrive
	}
	letter := value[0]
	if letter >= 'a' && letter <= 'z' {
		letter -= 'a' - 'A'
	}
	if letter < 'A' || letter > 'Z' {
		return "", ErrInvalidDrive
	}
	return string([]byte{letter, ':'}), nil
}
