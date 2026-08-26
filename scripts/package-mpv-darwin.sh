#!/usr/bin/env bash
set -euo pipefail

readonly EXIT_DEPLOYMENT_TARGET_INCOMPATIBLE=78

if [ "${1:-}" = "--deployment-incompatible-exit-code" ]; then
  printf '%s\n' "$EXIT_DEPLOYMENT_TARGET_INCOMPATIBLE"
  exit 0
fi

APP_PATH="${1:-build/bin/TDrive.app}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VERSION_FILE="$SCRIPT_DIR/package-mpv-version.txt"
. "$SCRIPT_DIR/mpv-metadata.sh"

if [ "$(uname -s)" != "Darwin" ]; then
  echo "package-mpv-darwin: this script only runs on macOS" >&2
  exit 1
fi

if [ ! -d "$APP_PATH" ]; then
  echo "package-mpv-darwin: app bundle not found: $APP_PATH" >&2
  exit 1
fi

BIN_PATH="$APP_PATH/Contents/MacOS/TDrive"
FRAMEWORKS_ROOT="$APP_PATH/Contents/Frameworks"
FRAMEWORKS_DIR="$FRAMEWORKS_ROOT/TDriveMedia"
MEDIA_DIR="$APP_PATH/Contents/Resources/media"
MPV_SIDECAR="$MEDIA_DIR/mpv"
RUNTIME_MANIFEST="$MEDIA_DIR/media-runtime.manifest"
RUNTIME_CHECKSUMS="$MEDIA_DIR/media-runtime.sha256"
RUNTIME_SOURCE="$MEDIA_DIR/SOURCE.txt"
RUNTIME_NOTICE="$MEDIA_DIR/THIRD_PARTY_NOTICES.txt"

if [ ! -x "$BIN_PATH" ]; then
  echo "package-mpv-darwin: app executable not found: $BIN_PATH" >&2
  exit 1
fi

if [ ! -f "$VERSION_FILE" ]; then
  echo "package-mpv-darwin: expected version file not found: $VERSION_FILE" >&2
  exit 1
fi
EXPECTED_MPV_VERSION="$(tr -d '[:space:]' < "$VERSION_FILE")"
if [ -z "$EXPECTED_MPV_VERSION" ]; then
  echo "package-mpv-darwin: expected mpv version is empty" >&2
  exit 1
fi

mkdir -p "$FRAMEWORKS_DIR" "$MEDIA_DIR"

# This script owns the media runtime files it places in the bundle. Removing
# them up front keeps repeated local package runs deterministic.
find "$FRAMEWORKS_DIR" -maxdepth 1 -type f -name '*.dylib' -delete
rm -f "$MPV_SIDECAR" "$RUNTIME_MANIFEST" "$RUNTIME_CHECKSUMS" "$RUNTIME_SOURCE" "$RUNTIME_NOTICE"

log() {
  printf 'package-mpv-darwin: %s\n' "$*"
}

die() {
  echo "package-mpv-darwin: $*" >&2
  exit 1
}

deployment_target_incompatible() {
  echo "package-mpv-darwin: $*" >&2
  exit "$EXIT_DEPLOYMENT_TARGET_INCOMPATIBLE"
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
    /System/*|/usr/lib/*)
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

expand_path_token() {
  local origin="$1"
  local ref="$2"
  local root

  case "$ref" in
    @loader_path/*)
      printf '%s/%s\n' "$(dirname "$origin")" "${ref#@loader_path/}"
      return
      ;;
    @executable_path/*)
      for root in "$(dirname "$origin")" "$(dirname "${MPV_BIN:-$origin}")" "$(dirname "$BIN_PATH")"; do
        printf '%s/%s\n' "$root" "${ref#@executable_path/}"
      done
      return
      ;;
  esac
  printf '%s\n' "$ref"
}

otool_rpaths() {
  local file="$1"
  otool -l "$file" | awk '
    $1 == "cmd" && $2 == "LC_RPATH" { in_rpath = 1; next }
    in_rpath && $1 == "path" { print $2; in_rpath = 0 }
  '
}

resolve_dependency_ref() {
  local origin="$1"
  local dep="$2"
  local rpath expanded_rpath candidate

  case "$dep" in
    @rpath/*)
      while IFS= read -r rpath; do
        [ -n "$rpath" ] || continue
        while IFS= read -r expanded_rpath; do
          [ -n "$expanded_rpath" ] || continue
          candidate="$expanded_rpath/${dep#@rpath/}"
          if [ -f "$candidate" ]; then
            printf '%s\n' "$candidate"
            return
          fi
        done < <(expand_path_token "$origin" "$rpath")
      done < <(otool_rpaths "$origin")
      ;;
    @loader_path/*|@executable_path/*)
      while IFS= read -r candidate; do
        [ -n "$candidate" ] || continue
        if [ -f "$candidate" ]; then
          printf '%s\n' "$candidate"
          return
        fi
      done < <(expand_path_token "$origin" "$dep")
      ;;
    *)
      printf '%s\n' "$dep"
      return
      ;;
  esac

  printf '%s\n' "$dep"
}

find_runtime_root() {
  local root first_symlink
  root="${TDRIVE_MPV_RUNTIME_DIR:-}"
  [ -n "$root" ] || die "TDRIVE_MPV_RUNTIME_DIR is required for macOS media packaging"
  [ -d "$root" ] || die "macOS runtime directory not found: $root"
  root="$(cd "$root" && pwd -P)"

  first_symlink="$(find "$root" -type l -print -quit)"
  [ -z "$first_symlink" ] || die "macOS runtime directory must not contain symbolic links: $first_symlink"
  [ -x "$root/bin/mpv" ] || die "macOS runtime must contain executable bin/mpv"
  [ -f "$root/lib/libmpv.2.dylib" ] || die "macOS runtime must contain lib/libmpv.2.dylib"
  [ -s "$root/SOURCE.txt" ] || die "macOS runtime must contain non-empty SOURCE.txt"
  [ -s "$root/THIRD_PARTY_NOTICES.txt" ] || die "macOS runtime must contain non-empty THIRD_PARTY_NOTICES.txt"
  printf '%s\n' "$root"
}

assert_arch_compatible() {
  local target="$1"
  local label="$2"
  local app_arches target_arches arch
  app_arches="$(lipo -archs "$BIN_PATH" 2>/dev/null || true)"
  target_arches="$(lipo -archs "$target" 2>/dev/null || true)"
  [ -n "$app_arches" ] || die "could not determine app architecture: $BIN_PATH"
  [ -n "$target_arches" ] || die "could not determine $label architecture: $target"

  for arch in $app_arches; do
    case " $target_arches " in
      *" $arch "*) ;;
      *)
        die "$label is missing architecture '$arch' required by the app. App: [$app_arches], $label: [$target_arches]"
        ;;
    esac
  done
}

macos_minimum_for_arch() {
  local target="$1"
  local arch="$2"
  xcrun vtool -arch "$arch" -show-build "$target" 2>/dev/null |
    awk '$1 == "minos" { print $2; exit }'
}

version_at_most() {
  local candidate="$1"
  local limit="$2"
  local candidate_major candidate_minor candidate_patch
  local limit_major limit_minor limit_patch

  IFS=. read -r candidate_major candidate_minor candidate_patch <<< "$candidate"
  IFS=. read -r limit_major limit_minor limit_patch <<< "$limit"
  candidate_minor="${candidate_minor:-0}"
  candidate_patch="${candidate_patch:-0}"
  limit_minor="${limit_minor:-0}"
  limit_patch="${limit_patch:-0}"

  if ((candidate_major != limit_major)); then
    ((candidate_major < limit_major))
    return
  fi
  if ((candidate_minor != limit_minor)); then
    ((candidate_minor < limit_minor))
    return
  fi
  ((candidate_patch <= limit_patch))
}

assert_deployment_compatible() {
  local target="$1"
  local label="$2"
  local arch app_minimum target_minimum

  for arch in $(lipo -archs "$BIN_PATH"); do
    app_minimum="$(macos_minimum_for_arch "$BIN_PATH" "$arch")"
    target_minimum="$(macos_minimum_for_arch "$target" "$arch")"
    [ -n "$app_minimum" ] || die "could not read the app's minimum macOS version for $arch"
    [ -n "$target_minimum" ] || die "could not read $label's minimum macOS version for $arch: $target"
    if ! version_at_most "$target_minimum" "$app_minimum"; then
      deployment_target_incompatible "$label requires macOS $target_minimum for $arch, but the app promises macOS $app_minimum. Supply a compatible pinned runtime or raise the app deployment target explicitly"
    fi
  done
}

read_runtime_metadata() {
  local mpv_bin="$1"
  local output
  output="$("$mpv_bin" --no-config --version)" || die "could not execute mpv runtime: $mpv_bin"
  tdrive_parse_mpv_metadata "$output" || die "could not read mpv/FFmpeg metadata from $mpv_bin"
  if [ "$MPV_VERSION" != "$EXPECTED_MPV_VERSION" ]; then
    die "mpv version mismatch: expected $EXPECTED_MPV_VERSION, got $MPV_VERSION"
  fi
}

qualify_runtime() {
  local mpv_bin="$1"
  "$mpv_bin" \
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

write_runtime_metadata() {
  local app_arches source_reference archive_sha256
  app_arches="$(lipo -archs "$BIN_PATH")"
  source_reference="${TDRIVE_MPV_PACKAGE_SOURCE:-local-unverified}"
  source_reference="$(printf '%s' "$source_reference" | tr '\r\n' '  ')"
  archive_sha256="${TDRIVE_MPV_ARCHIVE_SHA256:-}"
  case "$archive_sha256" in
    *[!0-9A-Fa-f]*|'') die "TDRIVE_MPV_ARCHIVE_SHA256 must be set to the approved macOS runtime archive SHA-256" ;;
  esac
  [ "${#archive_sha256}" -eq 64 ] || die "TDRIVE_MPV_ARCHIVE_SHA256 must contain exactly 64 hexadecimal characters"
  archive_sha256="$(printf '%s' "$archive_sha256" | tr '[:upper:]' '[:lower:]')"

  cp -p "$RUNTIME_SOURCE_DIR/SOURCE.txt" "$RUNTIME_SOURCE"
  cp -p "$RUNTIME_SOURCE_DIR/THIRD_PARTY_NOTICES.txt" "$RUNTIME_NOTICE"

  {
    printf 'schema=1\n'
    printf 'platform=darwin\n'
    printf 'architecture=%s\n' "$app_arches"
    printf 'mpv_version=%s\n' "$MPV_VERSION"
    printf 'ffmpeg_version=%s\n' "$FFMPEG_VERSION"
    printf 'package_source=%s\n' "$source_reference"
    printf 'source_archive_sha256=%s\n' "$archive_sha256"
    printf 'qualification=lavfi-testsrc-64x64-2frames\n'
    printf 'license_metadata=SOURCE.txt,THIRD_PARTY_NOTICES.txt\n'
    printf 'license_review_required=true\n'
  } > "$RUNTIME_MANIFEST"

  (
    cd "$APP_PATH/Contents"
    find Frameworks/TDriveMedia -maxdepth 1 -type f -name '*.dylib' -print
    find Resources/media -maxdepth 1 -type f ! -name 'media-runtime.sha256' -print
  ) | LC_ALL=C sort | while IFS= read -r relative; do
    [ -n "$relative" ] || continue
    checksum="$(shasum -a 256 "$APP_PATH/Contents/$relative" | awk '{print $1}')"
    printf '%s  %s\n' "$checksum" "$relative"
  done > "$RUNTIME_CHECKSUMS"
}

TMP_BASE="${TMPDIR:-/tmp}"
[ -d "$TMP_BASE" ] || die "temporary directory does not exist: $TMP_BASE"
TMP_DIR="$(mktemp -d "$TMP_BASE/tdrive-mpv.XXXXXX")"
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
  local dep resolved

  while IFS= read -r dep; do
    [ -n "$dep" ] || continue
    if is_system_ref "$dep"; then
      continue
    fi
    resolved="$(resolve_dependency_ref "$file" "$dep")"
    if [ ! -f "$resolved" ]; then
      die "non-system dependency does not exist or cannot be resolved: $dep (from $file)"
    fi
    enqueue "$resolved"
  done < <(otool_deps "$file" "$skip_install_name")
}

copy_dylib() {
  local src="$1"
  local base dest
  base="$(basename "$src")"
  dest="$FRAMEWORKS_DIR/$base"

  assert_arch_compatible "$src" "media dependency $base"
  assert_deployment_compatible "$src" "media dependency $base"

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
    if ! is_system_ref "$dep"; then
      base="$(basename "$dep")"
      [ -f "$FRAMEWORKS_DIR/$base" ] || die "packaged dependency is missing: $dep (from $file)"
      install_name_tool -change "$dep" "$prefix/$base" "$file"
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
    if ! is_system_ref "$dep"; then
      dep_base="$(basename "$dep")"
      [ -f "$FRAMEWORKS_DIR/$dep_base" ] || die "packaged dependency is missing: $dep (from $dest)"
      install_name_tool -change "$dep" "@loader_path/$dep_base" "$dest"
    fi
  done < <(otool_deps "$dest" 1)
}

verify_packaged_refs_for() {
  local file="$1"
  local kind="$2"
  local skip_install_name="${3:-0}"
  local dep relative target

  while IFS= read -r dep; do
    [ -n "$dep" ] || continue
    if is_system_ref "$dep"; then
      continue
    fi
    case "$kind:$dep" in
      app:@executable_path/../Frameworks/TDriveMedia/*)
        relative="${dep#@executable_path/}"
        target="$(dirname "$BIN_PATH")/$relative"
        ;;
      sidecar:@executable_path/../../Frameworks/TDriveMedia/*)
        relative="${dep#@executable_path/}"
        target="$(dirname "$MPV_SIDECAR")/$relative"
        ;;
      dylib:@loader_path/*)
        relative="${dep#@loader_path/}"
        target="$(dirname "$file")/$relative"
        ;;
      *)
        die "packaged binary has an unresolved non-system dependency: $dep (from $file)"
        ;;
    esac
    [ -f "$target" ] || die "packaged dependency target does not exist: $dep (from $file)"
  done < <(otool_deps "$file" "$skip_install_name")
}

verify_packaged_refs() {
  local base
  verify_packaged_refs_for "$BIN_PATH" app 0
  verify_packaged_refs_for "$MPV_SIDECAR" sidecar 0
  while IFS= read -r base; do
    [ -n "$base" ] || continue
    verify_packaged_refs_for "$FRAMEWORKS_DIR/$base" dylib 1
  done < "$COPIED"
}

RUNTIME_SOURCE_DIR="$(find_runtime_root)"
MPV_LIB="$RUNTIME_SOURCE_DIR/lib/libmpv.2.dylib"
MPV_BIN="$RUNTIME_SOURCE_DIR/bin/mpv"
assert_arch_compatible "$MPV_LIB" "libmpv"
assert_arch_compatible "$MPV_BIN" "mpv sidecar"
assert_deployment_compatible "$MPV_LIB" "libmpv"
assert_deployment_compatible "$MPV_BIN" "mpv sidecar"
read_runtime_metadata "$MPV_BIN"

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

rewrite_executable_refs "$BIN_PATH" "@executable_path/../Frameworks/TDriveMedia"
rewrite_executable_refs "$MPV_SIDECAR" "@executable_path/../../Frameworks/TDriveMedia"
verify_packaged_refs

if command -v codesign >/dev/null 2>&1; then
  log "ad-hoc signing bundled media runtime"
  while IFS= read -r base; do
    [ -n "$base" ] || continue
    codesign --force --sign - "$FRAMEWORKS_DIR/$base" >/dev/null
  done < "$COPIED"
  codesign --force --sign - "$MPV_SIDECAR" >/dev/null
fi

read_runtime_metadata "$MPV_SIDECAR"
qualify_runtime "$MPV_SIDECAR"
write_runtime_metadata

if command -v codesign >/dev/null 2>&1; then
  log "ad-hoc signing app bundle"
  codesign --force --sign - "$APP_PATH" >/dev/null
  codesign --verify --deep --strict "$APP_PATH" >/dev/null
fi

log "qualified mpv $MPV_VERSION with FFmpeg $FFMPEG_VERSION"
log "done"
