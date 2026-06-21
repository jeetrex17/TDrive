#!/usr/bin/env bash
set -euo pipefail

APP_PATH="${1:-build/bin/TDrive.app}"

if [ "$(uname -s)" != "Darwin" ]; then
  echo "package-mpv-darwin: this script only runs on macOS" >&2
  exit 1
fi

if [ ! -d "$APP_PATH" ]; then
  echo "package-mpv-darwin: app bundle not found: $APP_PATH" >&2
  exit 1
fi

BIN_PATH="$APP_PATH/Contents/MacOS/TDrive"
FRAMEWORKS_DIR="$APP_PATH/Contents/Frameworks"
MEDIA_DIR="$APP_PATH/Contents/Resources/media"
MPV_SIDECAR="$MEDIA_DIR/mpv"

if [ ! -x "$BIN_PATH" ]; then
  echo "package-mpv-darwin: app executable not found: $BIN_PATH" >&2
  exit 1
fi

mkdir -p "$FRAMEWORKS_DIR" "$MEDIA_DIR"

# This script owns the media runtime files it places in the bundle. Removing
# them up front keeps repeated local package runs deterministic.
find "$FRAMEWORKS_DIR" -maxdepth 1 -type f -name '*.dylib' -delete
rm -f "$MPV_SIDECAR"

log() {
  printf 'package-mpv-darwin: %s\n' "$*"
}

die() {
  echo "package-mpv-darwin: $*" >&2
  exit 1
}

otool_deps() {
  local file="$1"
  local skip_install_name="${2:-0}"
  local line_no=0

  otool -L "$file" | while IFS= read -r line; do
    line_no=$((line_no + 1))
    if [ "$line_no" -eq 1 ]; then
      continue
    fi
    if [ "$skip_install_name" = "1" ] && [ "$line_no" -eq 2 ]; then
      continue
    fi
    printf '%s\n' "$line" | awk '{print $1}'
  done
}

is_system_ref() {
  case "$1" in
    @*|/System/*|/usr/lib/*)
      return 0
      ;;
  esac
  return 1
}

is_bundle_ref() {
  if is_system_ref "$1"; then
    return 1
  fi
  [ -f "$1" ]
}

find_mpv_lib() {
  if [ -n "${TDRIVE_MPV_LIB:-}" ]; then
    [ -f "$TDRIVE_MPV_LIB" ] || die "TDRIVE_MPV_LIB does not exist: $TDRIVE_MPV_LIB"
    printf '%s\n' "$TDRIVE_MPV_LIB"
    return
  fi

  local libdir=""
  libdir="$(pkg-config --variable=libdir mpv 2>/dev/null || true)"
  for candidate in \
    "$libdir/libmpv.2.dylib" \
    "$libdir/libmpv.dylib" \
    "/opt/homebrew/opt/mpv/lib/libmpv.2.dylib" \
    "/usr/local/opt/mpv/lib/libmpv.2.dylib"; do
    if [ -n "$candidate" ] && [ -f "$candidate" ]; then
      printf '%s\n' "$candidate"
      return
    fi
  done

  die "could not find libmpv; install mpv or set TDRIVE_MPV_LIB"
}

find_mpv_bin() {
  if [ -n "${TDRIVE_MPV_BIN:-}" ]; then
    [ -f "$TDRIVE_MPV_BIN" ] || die "TDRIVE_MPV_BIN does not exist: $TDRIVE_MPV_BIN"
    printf '%s\n' "$TDRIVE_MPV_BIN"
    return
  fi

  local found=""
  found="$(command -v mpv || true)"
  [ -n "$found" ] || die "could not find mpv sidecar; install mpv or set TDRIVE_MPV_BIN"
  printf '%s\n' "$found"
}

assert_arch_compatible() {
  local app_arches lib_arches arch
  app_arches="$(lipo -archs "$BIN_PATH" 2>/dev/null || true)"
  lib_arches="$(lipo -archs "$1" 2>/dev/null || true)"
  [ -n "$app_arches" ] || return 0
  [ -n "$lib_arches" ] || return 0

  for arch in $app_arches; do
    case " $lib_arches " in
      *" $arch "*) ;;
      *)
        die "libmpv is missing architecture '$arch' required by the app. App: [$app_arches], libmpv: [$lib_arches]"
        ;;
    esac
  done
}

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

QUEUE="$TMP_DIR/queue"
SEEN="$TMP_DIR/seen"
COPIED="$TMP_DIR/copied"
: > "$QUEUE"
: > "$SEEN"
: > "$COPIED"

seen_path() {
  grep -Fxq "$1" "$SEEN"
}

enqueue() {
  local dep="$1"
  if ! is_bundle_ref "$dep"; then
    return
  fi
  if seen_path "$dep"; then
    return
  fi
  printf '%s\n' "$dep" >> "$SEEN"
  printf '%s\n' "$dep" >> "$QUEUE"
}

enqueue_deps_for() {
  local file="$1"
  local skip_install_name="${2:-0}"
  local dep

  while IFS= read -r dep; do
    [ -n "$dep" ] || continue
    if is_system_ref "$dep"; then
      continue
    fi
    if [ ! -f "$dep" ]; then
      die "non-system dependency does not exist: $dep (from $file)"
    fi
    enqueue "$dep"
  done < <(otool_deps "$file" "$skip_install_name")
}

copy_dylib() {
  local src="$1"
  local base dest
  base="$(basename "$src")"
  dest="$FRAMEWORKS_DIR/$base"

  if [ -e "$dest" ]; then
    if ! cmp -s "$src" "$dest"; then
      die "dylib basename collision for $base ($src conflicts with existing bundled file)"
    fi
  else
    cp -pL "$src" "$dest"
    chmod u+w "$dest"
    log "bundled $base"
  fi
  if ! grep -Fxq "$base" "$COPIED"; then
    printf '%s\n' "$base" >> "$COPIED"
  fi
}

rewrite_executable_refs() {
  local file="$1"
  local prefix="$2"
  local dep base

  while IFS= read -r dep; do
    [ -n "$dep" ] || continue
    if is_bundle_ref "$dep"; then
      base="$(basename "$dep")"
      if [ -f "$FRAMEWORKS_DIR/$base" ]; then
        install_name_tool -change "$dep" "$prefix/$base" "$file"
      fi
    fi
  done < <(otool_deps "$file" 0)
}

rewrite_dylib_refs() {
  local base="$1"
  local dest dep dep_base
  dest="$FRAMEWORKS_DIR/$base"

  install_name_tool -id "@rpath/$base" "$dest"
  while IFS= read -r dep; do
    [ -n "$dep" ] || continue
    if is_bundle_ref "$dep"; then
      dep_base="$(basename "$dep")"
      if [ -f "$FRAMEWORKS_DIR/$dep_base" ]; then
        install_name_tool -change "$dep" "@loader_path/$dep_base" "$dest"
      fi
    fi
  done < <(otool_deps "$dest" 1)
}

verify_no_local_refs() {
  local files bad
  files=("$BIN_PATH" "$MPV_SIDECAR")
  while IFS= read -r base; do
    [ -n "$base" ] || continue
    files+=("$FRAMEWORKS_DIR/$base")
  done < "$COPIED"

  bad="$(
    for file in "${files[@]}"; do
      [ -f "$file" ] || continue
      otool -L "$file"
    done | grep -E '/opt/homebrew|/usr/local' || true
  )"
  if [ -n "$bad" ]; then
    printf '%s\n' "$bad" >&2
    die "packaged app still references local Homebrew paths"
  fi
}

MPV_LIB="$(find_mpv_lib)"
MPV_BIN="$(find_mpv_bin)"
assert_arch_compatible "$MPV_LIB"

log "using libmpv: $MPV_LIB"
log "using mpv sidecar: $MPV_BIN"

cp -pL "$MPV_BIN" "$MPV_SIDECAR"
chmod 755 "$MPV_SIDECAR"

enqueue "$MPV_LIB"
enqueue_deps_for "$MPV_BIN" 0

while [ -s "$QUEUE" ]; do
  src="$(sed -n '1p' "$QUEUE")"
  sed -n '2,$p' "$QUEUE" > "$QUEUE.next" || true
  mv "$QUEUE.next" "$QUEUE"

  copy_dylib "$src"
  enqueue_deps_for "$src" 1
done

while IFS= read -r base; do
  [ -n "$base" ] || continue
  rewrite_dylib_refs "$base"
done < "$COPIED"

rewrite_executable_refs "$BIN_PATH" "@executable_path/../Frameworks"
rewrite_executable_refs "$MPV_SIDECAR" "@executable_path/../../Frameworks"
verify_no_local_refs

if command -v codesign >/dev/null 2>&1; then
  log "ad-hoc signing bundled media runtime"
  while IFS= read -r base; do
    [ -n "$base" ] || continue
    codesign --force --sign - "$FRAMEWORKS_DIR/$base" >/dev/null
  done < "$COPIED"
  codesign --force --sign - "$MPV_SIDECAR" >/dev/null

  log "ad-hoc signing app bundle"
  codesign --force --deep --sign - "$APP_PATH" >/dev/null
fi

"$MPV_SIDECAR" --no-config --version >/dev/null

log "done"
