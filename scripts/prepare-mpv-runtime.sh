#!/usr/bin/env bash
# Prepares the mpv runtime that a CI or release build packages into the app and
# exports the packaging inputs through GITHUB_ENV.
#
# With TDRIVE_<LINUX|MACOS|WINDOWS>_MPV_RUNTIME_URL and _SHA256 set, the
# checksum-pinned archive is downloaded and verified. Otherwise a runtime is
# assembled from the runner's package manager (apt, Homebrew, Chocolatey); the
# packaging scripts mark that bundle as an unpinned fixture in its manifest.
set -euo pipefail

die() {
  printf 'prepare-mpv-runtime: %s\n' "$*" >&2
  exit 1
}

emit() {
  printf '%s\n' "$@" >> "${GITHUB_ENV:?GITHUB_ENV is required}"
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ZERO_SHA256=0000000000000000000000000000000000000000000000000000000000000000

case "$(uname -s)" in
  Linux) PLATFORM=linux; OS_KEY=LINUX ;;
  Darwin) PLATFORM=darwin; OS_KEY=MACOS ;;
  MINGW*|MSYS*|CYGWIN*) PLATFORM=windows; OS_KEY=WINDOWS ;;
  *) die "unsupported platform: $(uname -s)" ;;
esac

url_var="TDRIVE_${OS_KEY}_MPV_RUNTIME_URL"
sha_var="TDRIVE_${OS_KEY}_MPV_RUNTIME_SHA256"
RUNTIME_URL="${!url_var:-}"
RUNTIME_SHA256="${!sha_var:-}"

runner_temp="${RUNNER_TEMP:-/tmp}"
if command -v cygpath >/dev/null 2>&1; then
  runner_temp="$(cygpath -u "$runner_temp")"
fi
runtime_root="$runner_temp/tdrive-$PLATFORM-mpv-runtime"

if [ "$PLATFORM" = darwin ]; then
  export HOMEBREW_NO_AUTO_UPDATE=1
  brew list pkgconf >/dev/null 2>&1 || brew install pkgconf
fi

if [ -n "$RUNTIME_URL" ] && [ -n "$RUNTIME_SHA256" ]; then
  runtime_sha256="$(bash "$SCRIPT_DIR/acquire-mpv-runtime.sh" "$PLATFORM" "$RUNTIME_URL" "$RUNTIME_SHA256" "$runtime_root")"
  runtime_root_for_env="$runtime_root"
  if command -v cygpath >/dev/null 2>&1; then
    runtime_root_for_env="$(cygpath -w "$runtime_root")"
  fi
  emit "TDRIVE_MPV_RUNTIME_DIR=$runtime_root_for_env" \
    "TDRIVE_MPV_ARCHIVE_SHA256=$runtime_sha256" \
    "TDRIVE_MPV_PACKAGE_SOURCE=${RUNTIME_URL%%[?#]*}" \
    "TDRIVE_MPV_RUNTIME_PINNED=1"
  if [ "$PLATFORM" = darwin ]; then
    emit "PKG_CONFIG_LIBDIR=$runtime_root/lib/pkgconfig" "MACOSX_DEPLOYMENT_TARGET=11.0"
  fi
  exit 0
fi

echo "::warning::$url_var and $sha_var are not set; bundling the runner's package-manager mpv. The bundle manifest marks this runtime as unpinned."
emit "TDRIVE_MPV_RUNTIME_PINNED=0" "TDRIVE_MPV_ARCHIVE_SHA256=$ZERO_SHA256"

write_fixture_notices() {
  local summary="$1"
  shift
  {
    printf '%s\n' "$summary"
    echo "This is not the checksum-pinned release runtime."
    "$@"
  } > "$runtime_root/SOURCE.txt"
  {
    echo "Package-manager media runtime; see the package source in media-runtime.manifest."
    echo "Release archives must provide complete third-party notices."
  } > "$runtime_root/THIRD_PARTY_NOTICES.txt"
}

case "$PLATFORM" in
  linux)
    command -v mpv >/dev/null 2>&1 || die "mpv is not installed; apt-get install mpv first"
    mkdir -p "$runtime_root"
    cp -pL "$(command -v mpv)" "$runtime_root/mpv"
    bash "$SCRIPT_DIR/mpv-linux-libraries.sh" collect "$runtime_root"
    bash "$SCRIPT_DIR/mpv-linux-libraries.sh" validate "$runtime_root"
    chmod 755 "$runtime_root/mpv"
    mpv_version="$("$runtime_root/mpv" --no-config --version | awk '$1 == "mpv" { sub(/^v/, "", $2); print $2; exit }')"
    write_fixture_notices "Linux media runtime assembled from the Ubuntu runner package." apt-cache policy mpv
    emit "TDRIVE_MPV_RUNTIME_DIR=$runtime_root" \
      "TDRIVE_MPV_PACKAGE_SOURCE=ubuntu-ci-system-mpv-not-release-runtime" \
      "TDRIVE_MPV_TEST_VERSION=$mpv_version"
    ;;
  darwin)
    brew list mpv >/dev/null 2>&1 || brew install mpv
    libmpv="$(pkg-config --variable=libdir mpv)/libmpv.2.dylib"
    mkdir -p "$runtime_root/bin" "$runtime_root/lib"
    cp -pL "$(command -v mpv)" "$runtime_root/bin/mpv"
    cp -pL "$libmpv" "$runtime_root/lib/libmpv.2.dylib"
    chmod 755 "$runtime_root/bin/mpv"
    write_fixture_notices "macOS media runtime assembled from Homebrew mpv." brew list --versions mpv
    # Homebrew builds for the runner's own macOS release. The app must promise
    # the same floor, or dyld would refuse the bundled libmpv on older systems.
    arch="$(lipo -archs "$libmpv" | awk '{print $1}')"
    minos="$(xcrun vtool -arch "$arch" -show-build "$libmpv" | awk '$1 == "minos" { print $2; exit }')"
    [ -n "$minos" ] || die "could not read the minimum macOS version of Homebrew libmpv"
    # The plist is a Wails template with {{...}} placeholders, so plutil cannot
    # parse it; patch the value line after the key textually instead.
    plist="$SCRIPT_DIR/../build/darwin/Info.plist"
    sed -e '/<key>LSMinimumSystemVersion<\/key>/{n;s|<string>[^<]*</string>|<string>'"$minos"'</string>|;}' "$plist" > "$plist.tmp"
    mv "$plist.tmp" "$plist"
    grep -A1 '<key>LSMinimumSystemVersion</key>' "$plist" | grep -q "<string>$minos</string>" || die "could not set LSMinimumSystemVersion in $plist"
    emit "TDRIVE_MPV_RUNTIME_DIR=$runtime_root" \
      "TDRIVE_MPV_PACKAGE_SOURCE=homebrew-ci-system-mpv-not-release-runtime" \
      "MACOSX_DEPLOYMENT_TARGET=$minos" \
      "TDRIVE_MPV_RAISE_APP_MINOS=$minos"
    ;;
  windows)
    mpv_version="$(tr -d '[:space:]' < "$SCRIPT_DIR/package-mpv-version.txt")"
    choco install mpvio.install --version "$mpv_version" --no-progress -y
    emit "TDRIVE_MPV_ALLOW_UNPINNED_CI_FIXTURE=1" \
      "TDRIVE_MPV_PACKAGE_SOURCE=chocolatey-ci-system-mpv-not-release-runtime:mpvio.install/$mpv_version"
    ;;
esac
