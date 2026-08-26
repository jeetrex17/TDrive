package nativeplayer

import "strings"

type linuxDisplayMode string

const (
	linuxDisplayUnavailable       linuxDisplayMode = "unavailable"
	linuxDisplayX11Embedded       linuxDisplayMode = "x11-embedded"
	linuxDisplayWaylandStandalone linuxDisplayMode = "wayland-standalone"
)

func selectLinuxDisplayMode(gdkBackend, sessionType, waylandDisplay, display string) linuxDisplayMode {
	backend := strings.ToLower(strings.TrimSpace(gdkBackend))
	hasX11Fallback := false
	if preferred, _, ok := strings.Cut(backend, ","); ok {
		for _, candidate := range strings.Split(backend, ",")[1:] {
			if strings.TrimSpace(candidate) == "x11" {
				hasX11Fallback = true
				break
			}
		}
		backend = strings.TrimSpace(preferred)
	}
	switch backend {
	case "x11":
		if strings.TrimSpace(display) != "" {
			return linuxDisplayX11Embedded
		}
		return linuxDisplayUnavailable
	case "wayland":
		if strings.TrimSpace(waylandDisplay) != "" || strings.EqualFold(strings.TrimSpace(sessionType), "wayland") {
			return linuxDisplayWaylandStandalone
		}
		if hasX11Fallback && strings.TrimSpace(display) != "" {
			return linuxDisplayX11Embedded
		}
		return linuxDisplayUnavailable
	}
	if strings.EqualFold(strings.TrimSpace(sessionType), "wayland") || strings.TrimSpace(waylandDisplay) != "" {
		return linuxDisplayWaylandStandalone
	}
	if strings.TrimSpace(display) != "" {
		return linuxDisplayX11Embedded
	}
	return linuxDisplayUnavailable
}
