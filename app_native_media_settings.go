package main

import (
	"fmt"
	"math"
	"strconv"
)

// Presentation settings are deliberately bounded separately from mpv's broader
// property API, which also exposes file access and script configuration.
func validateNativeMediaPresentationSetting(property, value string) error {
	valid := false
	switch property {
	case "video-aspect-override":
		valid = value == "no" || value == "16:9" || value == "4:3"
	case "video-unscaled":
		valid = value == "no" || value == "downscale-big"
	case "panscan":
		valid = nativeMediaSettingInRange(value, 0, 1)
	case "sub-font-size":
		valid = nativeMediaSettingInRange(value, 20, 72)
	case "sub-outline-size":
		valid = nativeMediaSettingInRange(value, 0, 6)
	case "sub-color":
		valid = nativeMediaHexColor(value, 6)
	case "sub-back-color":
		valid = nativeMediaHexColor(value, 8)
	case "sub-border-style":
		valid = value == "background-box" || value == "outline-and-shadow"
	case "sub-ass-override":
		valid = value == "force" || value == "scale"
	default:
		return fmt.Errorf("unsupported native media set target")
	}
	if !valid {
		return fmt.Errorf("invalid native media presentation value")
	}
	return nil
}

func nativeMediaSettingInRange(value string, minimum, maximum float64) bool {
	number, err := strconv.ParseFloat(value, 64)
	return err == nil && !math.IsNaN(number) && !math.IsInf(number, 0) && number >= minimum && number <= maximum
}

func nativeMediaHexColor(value string, digits int) bool {
	if len(value) != digits+1 || value[0] != '#' {
		return false
	}
	for _, char := range value[1:] {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f' || char >= 'A' && char <= 'F') {
			return false
		}
	}
	return true
}
