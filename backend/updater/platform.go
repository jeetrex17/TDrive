package updater

import "runtime"

// Platform identifies the build the running process was compiled for.
type Platform struct {
	OS   string
	Arch string
}

// HostPlatform returns the platform of the running binary.
func HostPlatform() Platform {
	return Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
}

// checksumsAssetName is the SHA-256 manifest the release workflow attaches
// next to the binaries (sha256sum format, bare file names).
const checksumsAssetName = "checksums.txt"

// assetSuffix maps a platform to the desktop asset suffix produced by
// .github/workflows/release.yml. Selection is always by the exact asset name
// because the CLI archives share the same prefix
// ("TDrive-v1.6.0-windows-amd64-cli.zip" vs "TDrive-v1.6.0-windows-amd64.zip").
func assetSuffix(p Platform) (string, bool) {
	switch p {
	case Platform{OS: "darwin", Arch: "arm64"}:
		return "macos-arm64.zip", true
	case Platform{OS: "windows", Arch: "amd64"}:
		return "windows-amd64.zip", true
	case Platform{OS: "linux", Arch: "amd64"}:
		return "linux-amd64.AppImage", true
	}
	return "", false
}

// appAssetName returns the desktop asset name for tag on platform p.
func appAssetName(tag string, p Platform) (string, bool) {
	suffix, ok := assetSuffix(p)
	if !ok {
		return "", false
	}
	return "TDrive-" + tag + "-" + suffix, true
}

// displayOS is the user-facing name of a GOOS value.
func displayOS(os string) string {
	switch os {
	case "darwin":
		return "macOS"
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	}
	return os
}
