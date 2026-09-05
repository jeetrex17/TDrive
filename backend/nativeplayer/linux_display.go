package nativeplayer

import "strings"

type linuxDisplayMode string

const (
	linuxDisplayUnavailable       linuxDisplayMode = "unavailable"
	linuxDisplayX11Embedded       linuxDisplayMode = "x11-embedded"
	linuxDisplayWaylandStandalone linuxDisplayMode = "wayland-standalone"
)

// selectLinuxDisplayMode mirrors GTK's backend choice so mpv is embedded only
// when the app window itself is X11. GDK_BACKEND is an ordered preference list;
// without it GTK prefers Wayland when a compositor is reachable.
func selectLinuxDisplayMode(gdkBackend, sessionType, waylandDisplay, display string) linuxDisplayMode {
	hasWayland := strings.TrimSpace(waylandDisplay) != "" || strings.EqualFold(strings.TrimSpace(sessionType), "wayland")
	hasX11 := strings.TrimSpace(display) != ""

	order := []string{"wayland", "x11"}
	if backend := strings.ToLower(strings.TrimSpace(gdkBackend)); backend != "" {
		order = strings.Split(backend, ",")
	}
	for _, candidate := range order {
		switch strings.TrimSpace(candidate) {
		case "x11":
			if hasX11 {
				return linuxDisplayX11Embedded
			}
		case "wayland":
			if hasWayland {
				return linuxDisplayWaylandStandalone
			}
		}
	}
	return linuxDisplayUnavailable
}
