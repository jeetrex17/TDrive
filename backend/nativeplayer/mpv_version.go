package nativeplayer

import (
	"regexp"
	"strconv"
)

// mpvVersion is the major.minor of an mpv build. The zero value means the
// banner could not be read; callers then pass only the options every release
// understands, because mpv refuses to start on one it does not know.
type mpvVersion struct {
	major, minor int
}

// mpvBannerPattern matches the first line of `mpv --version`, which reads
// "mpv 0.34.1 Copyright ..." for releases and "mpv v0.41.0-12-gabcdef ..."
// for git builds.
var mpvBannerPattern = regexp.MustCompile(`\bmpv v?(\d+)\.(\d+)`)

func parseMPVVersion(banner string) mpvVersion {
	match := mpvBannerPattern.FindStringSubmatch(banner)
	if match == nil {
		return mpvVersion{}
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	return mpvVersion{major: major, minor: minor}
}

func (v mpvVersion) known() bool {
	return v.major > 0 || v.minor > 0
}

func (v mpvVersion) atLeast(major, minor int) bool {
	return v.major > major || (v.major == major && v.minor >= minor)
}

func (v mpvVersion) String() string {
	if !v.known() {
		return "unknown"
	}
	return strconv.Itoa(v.major) + "." + strconv.Itoa(v.minor)
}
