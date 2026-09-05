#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$SCRIPT_DIR/mpv-metadata.sh"

pass_count=0

pass() {
  pass_count=$((pass_count + 1))
}

expect_parse() {
  local name="$1"
  local input="$2"
  local want_mpv="$3"
  local want_ffmpeg="$4"

  MPV_VERSION=""
  FFMPEG_VERSION=""
  if ! tdrive_parse_mpv_metadata "$input"; then
    echo "FAIL $name: parser rejected valid output" >&2
    exit 1
  fi
  if [ "$MPV_VERSION" != "$want_mpv" ]; then
    echo "FAIL $name: mpv=$MPV_VERSION want $want_mpv" >&2
    exit 1
  fi
  if [ "$FFMPEG_VERSION" != "$want_ffmpeg" ]; then
    echo "FAIL $name: ffmpeg=$FFMPEG_VERSION want $want_ffmpeg" >&2
    exit 1
  fi
  pass
}

expect_reject() {
  local name="$1"
  local input="$2"

  MPV_VERSION=""
  FFMPEG_VERSION=""
  if tdrive_parse_mpv_metadata "$input"; then
    echo "FAIL $name: parser accepted invalid output" >&2
    exit 1
  fi
  pass
}

expect_safe_source() {
  local name="$1"
  local input="$2"
  local expected="$3"
  local actual
  actual="$(tdrive_safe_mpv_package_source "$input")"
  if [ "$actual" != "$expected" ]; then
    echo "FAIL $name: safe source=$actual want $expected" >&2
    exit 1
  fi
  pass
}

expect_parse "plain-mpv-version" \
  $'mpv 0.41.0 Copyright\nFFmpeg version: 7.1.1\n' \
  "0.41.0" "7.1.1"

expect_parse "v-prefixed-mpv-version" \
  $'mpv v0.41.0 Copyright\nFFmpeg version: 7.1.1\n' \
  "0.41.0" "7.1.1"

expect_parse "crlf-and-leading-whitespace" \
  $'  mpv v0.41.0 Copyright\r\n  FFmpeg version: 7.1.1\r\n' \
  "0.41.0" "7.1.1"

expect_reject "missing-mpv" \
  $'not mpv\nFFmpeg version: 7.1.1\n'

expect_reject "missing-ffmpeg" \
  $'mpv 0.41.0 Copyright\nlibavcodec 61.19.101\n'

expect_reject "malformed-mpv" \
  $'mpv next Copyright\nFFmpeg version: 7.1.1\n'

expect_reject "conflicting-mpv" \
  $'mpv 0.40.0 Copyright\nmpv v0.41.0 Copyright\nFFmpeg version: 7.1.1\n'

expect_reject "conflicting-ffmpeg" \
  $'mpv 0.41.0 Copyright\nFFmpeg version: 7.0\nFFmpeg version: 7.1.1\n'

expect_safe_source "strips-query" \
  "https://downloads.example.com/mpv/windows.tar.gz?X-Amz-Signature=secret&Expires=1" \
  "https://downloads.example.com/mpv/windows.tar.gz"

expect_safe_source "strips-fragment" \
  "https://downloads.example.com/mpv/darwin.tar.gz#token" \
  "https://downloads.example.com/mpv/darwin.tar.gz"

expect_safe_source "normalizes-newlines-before-stripping" \
  $' https://downloads.example.com/mpv/linux.tar.gz?sig=secret\nignored ' \
  "https://downloads.example.com/mpv/linux.tar.gz"

echo "mpv metadata parser tests passed: $pass_count"
