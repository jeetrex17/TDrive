package mountcontroller

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	sharedLabelPrefix    = "Tdrive — "
	maxDisplayLabelRunes = 64
	maxDisplayLabelBytes = 96
	maxDriveTitleRunes   = 160
	maxDriveTitleBytes   = 256
)

func normalizeDrive(drive Drive) (Drive, error) {
	if drive.ID <= 0 {
		return Drive{}, fmt.Errorf("%w: positive drive id required", ErrInvalidDrive)
	}
	if drive.Kind != DriveKindPersonal && drive.Kind != DriveKindShared {
		return Drive{}, fmt.Errorf("%w: unsupported drive kind", ErrInvalidDrive)
	}
	drive.Title = sanitizeText(drive.Title, maxDriveTitleRunes, maxDriveTitleBytes)
	if drive.Kind == DriveKindShared && drive.Title == "" {
		drive.Title = "Shared drive"
	}
	return drive, nil
}

func displayLabel(drive Drive) (string, error) {
	drive, err := normalizeDrive(drive)
	if err != nil {
		return "", err
	}
	if drive.Kind == DriveKindPersonal {
		return "Tdrive personal", nil
	}

	titleRunes := maxDisplayLabelRunes - utf8.RuneCountInString(sharedLabelPrefix)
	titleBytes := maxDisplayLabelBytes - len(sharedLabelPrefix)
	title := sanitizeText(drive.Title, titleRunes, titleBytes)
	if title == "" {
		title = "Shared drive"
	}
	return sharedLabelPrefix + title, nil
}

func sanitizeText(value string, maxRunes, maxBytes int) string {
	if maxRunes <= 0 || maxBytes <= 0 {
		return ""
	}

	var builder strings.Builder
	builder.Grow(min(len(value), maxBytes))
	runes := 0
	bytes := 0
	spacePending := false
	for _, current := range strings.ToValidUTF8(value, "") {
		unsafe := unicode.IsControl(current) || unicode.In(current, unicode.Cf) || current == '/' || current == '\\' || current == ':'
		if unsafe || unicode.IsSpace(current) {
			spacePending = builder.Len() > 0
			continue
		}
		width := utf8.RuneLen(current)
		separatorBytes := 0
		separatorRunes := 0
		if spacePending {
			separatorBytes = 1
			separatorRunes = 1
		}
		if runes+separatorRunes+1 > maxRunes || bytes+separatorBytes+width > maxBytes {
			break
		}
		if spacePending {
			builder.WriteByte(' ')
			bytes++
			runes++
			spacePending = false
		}
		builder.WriteRune(current)
		bytes += width
		runes++
	}
	return strings.TrimSpace(builder.String())
}
