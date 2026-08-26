#!/usr/bin/env bash

tdrive_parse_mpv_metadata() {
  local output="$1"
  local versions ffmpeg_versions count

  MPV_VERSION=""
  FFMPEG_VERSION=""

  versions="$(
    printf '%s\n' "$output" | tr -d '\r' | awk '
      /^[[:space:]]*mpv[[:space:]]+v?[0-9][^[:space:]]*/ {
        version = $2
        sub(/^v/, "", version)
        print version
      }
    ' | LC_ALL=C sort -u
  )"
  count="$(printf '%s\n' "$versions" | awk 'NF { count++ } END { print count + 0 }')"
  if [ "$count" -eq 0 ]; then
    return 1
  fi
  if [ "$count" -ne 1 ]; then
    return 2
  fi

  ffmpeg_versions="$(
    printf '%s\n' "$output" | tr -d '\r' | awk '
      /^[[:space:]]*FFmpeg version:[[:space:]]*/ {
        sub(/^[[:space:]]*FFmpeg version:[[:space:]]*/, "")
        if ($0 != "") {
          print
        }
      }
    ' | LC_ALL=C sort -u
  )"
  count="$(printf '%s\n' "$ffmpeg_versions" | awk 'NF { count++ } END { print count + 0 }')"
  if [ "$count" -eq 0 ]; then
    return 3
  fi
  if [ "$count" -ne 1 ]; then
    return 4
  fi

  MPV_VERSION="$(printf '%s\n' "$versions" | sed -n '1p')"
  FFMPEG_VERSION="$(printf '%s\n' "$ffmpeg_versions" | sed -n '1p')"
}
