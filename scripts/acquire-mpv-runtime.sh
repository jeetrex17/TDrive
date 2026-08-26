#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: acquire-mpv-runtime.sh <linux|darwin> <archive-url> <archive-sha256> <destination-dir>" >&2
  exit 2
}

die() {
  echo "acquire-mpv-runtime: $*" >&2
  exit 1
}

[ "$#" -eq 4 ] || usage

PLATFORM="$1"
RUNTIME_URL="$2"
EXPECTED_SHA256="$3"
RUNTIME_ROOT="$4"

[ -n "$RUNTIME_URL" ] || die "$PLATFORM runtime URL is required"
[ -n "$EXPECTED_SHA256" ] || die "$PLATFORM runtime SHA-256 is required"
[ -n "$RUNTIME_ROOT" ] || die "$PLATFORM destination directory is required"
case "$RUNTIME_URL" in
  https://*|file://*) ;;
  *) die "$PLATFORM runtime URL must use https://, or file:// for local tests" ;;
esac

RUNTIME_PARENT="$(dirname "$RUNTIME_ROOT")"
RUNTIME_BASENAME="$(basename "$RUNTIME_ROOT")"
[ -d "$RUNTIME_PARENT" ] || die "$PLATFORM destination parent does not exist: $RUNTIME_PARENT"
case "$RUNTIME_BASENAME" in
  ''|'.'|'..') die "$PLATFORM destination basename is unsafe: $RUNTIME_ROOT" ;;
esac
RUNTIME_PARENT_REAL="$(cd "$RUNTIME_PARENT" && pwd -P)"
RUNTIME_ROOT="$RUNTIME_PARENT_REAL/$RUNTIME_BASENAME"
[ ! -e "$RUNTIME_ROOT" ] || die "$PLATFORM destination already exists: $RUNTIME_ROOT"

EXPECTED_SHA256="$(printf '%s' "$EXPECTED_SHA256" | tr '[:upper:]' '[:lower:]')"
case "$EXPECTED_SHA256" in
  *[!0-9a-f]*|'') die "$PLATFORM runtime SHA-256 must be a SHA-256 value" ;;
esac
[ "${#EXPECTED_SHA256}" -eq 64 ] || die "$PLATFORM runtime SHA-256 must contain exactly 64 hexadecimal characters"

case "$PLATFORM" in
  linux|darwin) ;;
  *) usage ;;
esac

TMP_PARENT="${RUNNER_TEMP:-/tmp}"
[ -d "$TMP_PARENT" ] || die "temporary directory does not exist: $TMP_PARENT"
ARCHIVE_PATH="$(mktemp "$TMP_PARENT/tdrive-$PLATFORM-mpv-runtime.XXXXXX.tar.gz")"
STAGING_ROOT="$(mktemp -d "$RUNTIME_PARENT_REAL/.tdrive-$PLATFORM-mpv-runtime.XXXXXX")"
cleanup() {
  rm -f "$ARCHIVE_PATH"
  if [ -n "$STAGING_ROOT" ]; then
    rm -rf "$STAGING_ROOT"
  fi
}
trap cleanup EXIT

curl --fail --location --silent --show-error "$RUNTIME_URL" --output "$ARCHIVE_PATH"
if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL_SHA256="$(sha256sum "$ARCHIVE_PATH" | awk '{print $1}')"
else
  ACTUAL_SHA256="$(shasum -a 256 "$ARCHIVE_PATH" | awk '{print $1}')"
fi
if [ "$ACTUAL_SHA256" != "$EXPECTED_SHA256" ]; then
  die "$PLATFORM runtime checksum mismatch: expected $EXPECTED_SHA256, got $ACTUAL_SHA256"
fi

if tar -tzf "$ARCHIVE_PATH" | awk '
  /^\// || /(^|\/)\.\.(\/|$)/ { found = 1 }
  END { exit(found ? 0 : 1) }
'; then
  die "$PLATFORM runtime archive contains an unsafe path"
fi
if tar -tvzf "$ARCHIVE_PATH" | awk '
  substr($1, 1, 1) != "-" && substr($1, 1, 1) != "d" { found = 1 }
  END { exit(found ? 0 : 1) }
'; then
  die "$PLATFORM runtime archive may contain only regular files and directories"
fi

tar -xzf "$ARCHIVE_PATH" -C "$STAGING_ROOT"

for required in SOURCE.txt THIRD_PARTY_NOTICES.txt; do
  [ -s "$STAGING_ROOT/$required" ] || die "$PLATFORM runtime archive is missing non-empty root file: $required"
done

case "$PLATFORM" in
  linux)
    [ -x "$STAGING_ROOT/mpv" ] || die "Linux runtime archive must contain executable root file: mpv"
    ;;
  darwin)
    for required in bin/mpv lib/libmpv.2.dylib lib/pkgconfig/mpv.pc include/mpv/client.h; do
      [ -s "$STAGING_ROOT/$required" ] || die "macOS runtime archive is missing required file: $required"
    done
    if grep -Eq '(^prefix=/|^libdir=/|^includedir=/|/opt/homebrew|/usr/local|/Users/|/private/tmp|/tmp/)' "$STAGING_ROOT/lib/pkgconfig/mpv.pc"; then
      die "macOS runtime lib/pkgconfig/mpv.pc must be relocatable and must not contain build-machine prefixes"
    fi
    if ! grep -Fqx 'prefix=${pcfiledir}/../..' "$STAGING_ROOT/lib/pkgconfig/mpv.pc"; then
      die 'macOS runtime lib/pkgconfig/mpv.pc must use prefix=${pcfiledir}/../..'
    fi
    chmod 755 "$STAGING_ROOT/bin/mpv"
    ;;
esac

mv "$STAGING_ROOT" "$RUNTIME_ROOT"
STAGING_ROOT=""
echo "$EXPECTED_SHA256"
