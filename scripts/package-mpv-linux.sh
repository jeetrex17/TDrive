#!/usr/bin/env bash
set -euo pipefail

APP_DIR="${1:-}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VERSION_FILE="$SCRIPT_DIR/package-mpv-version.txt"
. "$SCRIPT_DIR/mpv-metadata.sh"

log() {
  printf 'package-mpv-linux: %s\n' "$*"
}

die() {
  printf 'package-mpv-linux: %s\n' "$*" >&2
  exit 1
}

[ "$(uname -s)" = "Linux" ] || die "this script only runs on Linux"
[ -n "$APP_DIR" ] || die "usage: package-mpv-linux.sh <AppDir>"
[ -d "$APP_DIR" ] || die "AppDir not found: $APP_DIR"
TEST_MPV_VERSION="${2:-}"
if [ -n "$TEST_MPV_VERSION" ]; then
  [ "${TDRIVE_MPV_ALLOW_TEST_VERSION:-}" = "1" ] || die "the optional mpv version override is reserved for CI contract testing"
  EXPECTED_MPV_VERSION="$TEST_MPV_VERSION"
else
  [ -f "$VERSION_FILE" ] || die "expected version file not found: $VERSION_FILE"
  EXPECTED_MPV_VERSION="$(tr -d '[:space:]' < "$VERSION_FILE")"
fi
[ -n "$EXPECTED_MPV_VERSION" ] || die "expected mpv version is empty"

RUNTIME_SOURCE_DIR="${TDRIVE_MPV_RUNTIME_DIR:-}"
# The approved runtime directory is the extracted root of a checksum-pinned
# archive. It must contain:
#   mpv                         executable for the target architecture
#   lib/                        private shared libraries, if any
#   SOURCE.txt                  exact source/build provenance
#   THIRD_PARTY_NOTICES.txt     licenses and redistribution notices
# The wrapper below uses only this archive for its private library override and
# deliberately does not inherit a caller-provided LD_LIBRARY_PATH.
[ -n "$RUNTIME_SOURCE_DIR" ] || die "TDRIVE_MPV_RUNTIME_DIR is required; release packaging must use a checksum-pinned runtime archive"
[ -d "$RUNTIME_SOURCE_DIR" ] || die "runtime directory not found: $RUNTIME_SOURCE_DIR"
[ -x "$RUNTIME_SOURCE_DIR/mpv" ] || die "runtime directory must contain an executable mpv at its root"
[ -s "$RUNTIME_SOURCE_DIR/THIRD_PARTY_NOTICES.txt" ] || die "runtime archive must include a non-empty THIRD_PARTY_NOTICES.txt"
[ -s "$RUNTIME_SOURCE_DIR/SOURCE.txt" ] || die "runtime archive must include a non-empty SOURCE.txt with source/build provenance"
FIRST_RUNTIME_SYMLINK="$(find "$RUNTIME_SOURCE_DIR" -type l -print -quit)"
if [ -n "$FIRST_RUNTIME_SYMLINK" ]; then
  die "runtime directory must not contain symbolic links; publish dereferenced files in the checksum-pinned archive"
fi

PACKAGE_SOURCE="${TDRIVE_MPV_PACKAGE_SOURCE:-}"
ARCHIVE_SHA256="${TDRIVE_MPV_ARCHIVE_SHA256:-}"
[ -n "$PACKAGE_SOURCE" ] || die "TDRIVE_MPV_PACKAGE_SOURCE is required"
case "$ARCHIVE_SHA256" in
  *[!0-9A-Fa-f]*|'') die "TDRIVE_MPV_ARCHIVE_SHA256 must be a SHA-256 value" ;;
esac
[ "${#ARCHIVE_SHA256}" -eq 64 ] || die "TDRIVE_MPV_ARCHIVE_SHA256 must contain exactly 64 hexadecimal characters"

APP_BIN="$APP_DIR/usr/bin/TDrive"
[ -x "$APP_BIN" ] || die "AppDir executable not found: $APP_BIN"

MEDIA_DIR="$APP_DIR/usr/bin/media"
RUNTIME_DIR="$MEDIA_DIR/runtime"
MPV_WRAPPER="$MEDIA_DIR/mpv"
RUNTIME_MANIFEST="$MEDIA_DIR/media-runtime.manifest"
RUNTIME_CHECKSUMS="$MEDIA_DIR/media-runtime.sha256"

elf_architecture() {
  local target="$1"
  local machine
  machine="$(readelf -h "$target" 2>/dev/null | awk -F: '/Machine:/{sub(/^[[:space:]]+/, "", $2); print $2; exit}')"
  case "$machine" in
    "Advanced Micro Devices X86-64") printf 'amd64\n' ;;
    AArch64) printf 'arm64\n' ;;
    *) die "unsupported or unreadable ELF architecture for $target: ${machine:-unknown}" ;;
  esac
}

read_runtime_metadata() {
  local mpv_bin="$1"
  local output
  output="$(env -u LD_LIBRARY_PATH -u LD_PRELOAD -u LD_AUDIT "$mpv_bin" --no-config --version)" || die "could not execute mpv runtime: $mpv_bin"
  tdrive_parse_mpv_metadata "$output" || die "could not read mpv/FFmpeg metadata from $mpv_bin"
  if [ "$MPV_VERSION" != "$EXPECTED_MPV_VERSION" ]; then
    die "mpv version mismatch: expected $EXPECTED_MPV_VERSION, got $MPV_VERSION"
  fi
}

qualify_runtime() {
  env -u LD_LIBRARY_PATH -u LD_PRELOAD -u LD_AUDIT "$1" \
    --no-config \
    --terminal=no \
    --msg-level=all=warn \
    --vo=null \
    --ao=null \
    --frames=2 \
    -- \
    'av://lavfi:testsrc=size=64x64:rate=1:duration=2' >/dev/null ||
    die "bundled mpv failed deterministic lavfi decode qualification"
}

APP_ARCH="$(elf_architecture "$APP_BIN")"
MPV_ARCH="$(elf_architecture "$RUNTIME_SOURCE_DIR/mpv")"
[ "$APP_ARCH" = "$MPV_ARCH" ] || die "architecture mismatch: app=$APP_ARCH mpv=$MPV_ARCH"

rm -rf "$MEDIA_DIR"
mkdir -p "$RUNTIME_DIR"
cp -R "$RUNTIME_SOURCE_DIR/." "$RUNTIME_DIR/"
chmod 755 "$RUNTIME_DIR/mpv"

{
  printf '%s\n' '#!/usr/bin/env sh'
  printf '%s\n' 'set -eu'
  printf '%s\n' 'media_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)'
  printf '%s\n' 'runtime_dir="$media_dir/runtime"'
  printf '%s\n' 'LD_LIBRARY_PATH="$runtime_dir/lib"'
  printf '%s\n' 'export LD_LIBRARY_PATH'
  printf '%s\n' 'exec "$runtime_dir/mpv" "$@"'
} > "$MPV_WRAPPER"
chmod 755 "$MPV_WRAPPER"

bash "$SCRIPT_DIR/mpv-linux-libraries.sh" validate "$RUNTIME_DIR"
read_runtime_metadata "$MPV_WRAPPER"
qualify_runtime "$MPV_WRAPPER"

PACKAGE_SOURCE="$(tdrive_safe_mpv_package_source "$PACKAGE_SOURCE")"
ARCHIVE_SHA256="$(printf '%s' "$ARCHIVE_SHA256" | tr '[:upper:]' '[:lower:]')"
CI_FIXTURE="false"
RELEASE_RUNTIME="true"
if [ "$ARCHIVE_SHA256" = "0000000000000000000000000000000000000000000000000000000000000000" ] ||
  printf '%s' "$PACKAGE_SOURCE" | grep -Fq 'not-release-runtime'; then
  CI_FIXTURE="true"
  RELEASE_RUNTIME="false"
fi

{
  printf 'schema=1\n'
  printf 'platform=linux\n'
  printf 'architecture=%s\n' "$APP_ARCH"
  printf 'mpv_version=%s\n' "$MPV_VERSION"
  printf 'ffmpeg_version=%s\n' "$FFMPEG_VERSION"
  printf 'package_source=%s\n' "$PACKAGE_SOURCE"
  printf 'source_archive_sha256=%s\n' "$ARCHIVE_SHA256"
  printf 'release_runtime=%s\n' "$RELEASE_RUNTIME"
  printf 'ci_fixture=%s\n' "$CI_FIXTURE"
  printf 'qualification=headless-lavfi-testsrc-64x64-2frames\n'
  printf 'x11_playback=embedded-window\n'
  printf 'wayland_playback=standalone-window\n'
  printf 'license_metadata=runtime/SOURCE.txt,runtime/THIRD_PARTY_NOTICES.txt\n'
  printf 'license_review_required=true\n'
} > "$RUNTIME_MANIFEST"

(
  cd "$MEDIA_DIR"
  find . -type f ! -name 'media-runtime.sha256' -print | LC_ALL=C sort | while IFS= read -r relative; do
    checksum="$(sha256sum "$relative" | awk '{print $1}')"
    printf '%s  %s\n' "$checksum" "${relative#./}"
  done
) > "$RUNTIME_CHECKSUMS"

log "qualified mpv $MPV_VERSION with FFmpeg $FFMPEG_VERSION ($APP_ARCH)"
log "validated headless mpv decode; X11 embedded and Wayland standalone playback still require OS display smoke/manual coverage"
log "bundled checksum-pinned media runtime into $MEDIA_DIR"
