#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/tdrive-acquire-mpv-test.XXXXXX")"
trap 'rm -rf "$TMP_ROOT"' EXIT

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

make_linux_runtime_archive() {
  local archive="$1"
  local root="$TMP_ROOT/linux-runtime"
  mkdir -p "$root"
  printf '#!/usr/bin/env sh\nexit 0\n' > "$root/mpv"
  chmod 755 "$root/mpv"
  printf 'source provenance\n' > "$root/SOURCE.txt"
  printf 'third party notices\n' > "$root/THIRD_PARTY_NOTICES.txt"
  tar -czf "$archive" -C "$root" .
}

pass_count=0

expect_failure() {
  local name="$1"
  shift
  if "$@" >"$TMP_ROOT/$name.stdout" 2>"$TMP_ROOT/$name.stderr"; then
    echo "accepted invalid runtime fixture: $name" >&2
    exit 1
  fi
  pass_count=$((pass_count + 1))
}

archive="$TMP_ROOT/linux-runtime.tar.gz"
make_linux_runtime_archive "$archive"
expected_sha="$(sha256_file "$archive")"
destination="$TMP_ROOT/extracted-linux"
actual_sha="$(bash "$SCRIPT_DIR/acquire-mpv-runtime.sh" linux "file://$archive" "$expected_sha" "$destination")"
[ "$actual_sha" = "$expected_sha" ] || { echo "checksum output mismatch" >&2; exit 1; }
[ -x "$destination/mpv" ] || { echo "extracted mpv is missing or not executable" >&2; exit 1; }
pass_count=$((pass_count + 1))

expect_failure checksum-mismatch \
  bash "$SCRIPT_DIR/acquire-mpv-runtime.sh" linux "file://$archive" "0000000000000000000000000000000000000000000000000000000000000000" "$TMP_ROOT/bad-checksum"
[ ! -e "$TMP_ROOT/bad-checksum" ] || { echo "checksum failure created the destination" >&2; exit 1; }

existing_destination="$TMP_ROOT/existing-runtime"
mkdir -p "$existing_destination"
printf 'preserve me\n' > "$existing_destination/marker"
expect_failure destination-no-clobber \
  bash "$SCRIPT_DIR/acquire-mpv-runtime.sh" linux "file://$archive" "$expected_sha" "$existing_destination"
[ "$(sed -n '1p' "$existing_destination/marker")" = "preserve me" ] || { echo "existing destination was modified" >&2; exit 1; }

if bash "$SCRIPT_DIR/acquire-mpv-runtime.sh" linux "http://example.invalid/runtime.tar.gz" "$expected_sha" "$TMP_ROOT/http-dest" >/dev/null 2>&1; then
  echo "accepted non-HTTPS runtime URL" >&2
  exit 1
fi
pass_count=$((pass_count + 1))

link_root="$TMP_ROOT/link-runtime"
mkdir -p "$link_root"
printf 'source provenance\n' > "$link_root/SOURCE.txt"
printf 'third party notices\n' > "$link_root/THIRD_PARTY_NOTICES.txt"
ln -s /bin/sh "$link_root/mpv"
link_archive="$TMP_ROOT/link-runtime.tar.gz"
tar -czf "$link_archive" -C "$link_root" .
link_sha="$(sha256_file "$link_archive")"
expect_failure symbolic-link \
  bash "$SCRIPT_DIR/acquire-mpv-runtime.sh" linux "file://$link_archive" "$link_sha" "$TMP_ROOT/link-dest"

missing_notice_root="$TMP_ROOT/missing-notice-runtime"
mkdir -p "$missing_notice_root"
cp "$TMP_ROOT/linux-runtime/mpv" "$missing_notice_root/mpv"
cp "$TMP_ROOT/linux-runtime/SOURCE.txt" "$missing_notice_root/SOURCE.txt"
missing_notice_archive="$TMP_ROOT/missing-notice-runtime.tar.gz"
tar -czf "$missing_notice_archive" -C "$missing_notice_root" .
expect_failure missing-notices \
  bash "$SCRIPT_DIR/acquire-mpv-runtime.sh" linux "file://$missing_notice_archive" "$(sha256_file "$missing_notice_archive")" "$TMP_ROOT/missing-notice-dest"

traversal_root="$TMP_ROOT/traversal-source"
mkdir -p "$traversal_root"
printf 'must stay inside staging\n' > "$traversal_root/outside.txt"
traversal_archive="$TMP_ROOT/traversal.tar.gz"
if tar --version 2>/dev/null | grep -q 'GNU tar'; then
  tar -czf "$traversal_archive" --transform='s#^#../#' -C "$traversal_root" outside.txt
else
  tar -czf "$traversal_archive" -s '#^#../#' -C "$traversal_root" outside.txt
fi
expect_failure path-traversal \
  bash "$SCRIPT_DIR/acquire-mpv-runtime.sh" linux "file://$traversal_archive" "$(sha256_file "$traversal_archive")" "$TMP_ROOT/traversal-dest"
[ ! -e "$TMP_ROOT/outside.txt" ] || { echo "archive escaped its staging directory" >&2; exit 1; }

darwin_root="$TMP_ROOT/darwin-runtime"
mkdir -p "$darwin_root/bin" "$darwin_root/lib/pkgconfig" "$darwin_root/include/mpv"
printf '#!/usr/bin/env sh\nexit 0\n' > "$darwin_root/bin/mpv"
chmod 755 "$darwin_root/bin/mpv"
printf 'not a real dylib\n' > "$darwin_root/lib/libmpv.2.dylib"
printf 'prefix=${pcfiledir}/../..\nlibdir=${prefix}/lib\nincludedir=${prefix}/include\n' > "$darwin_root/lib/pkgconfig/mpv.pc"
printf 'header\n' > "$darwin_root/include/mpv/client.h"
printf 'source provenance\n' > "$darwin_root/SOURCE.txt"
printf 'third party notices\n' > "$darwin_root/THIRD_PARTY_NOTICES.txt"
darwin_archive="$TMP_ROOT/darwin-runtime.tar.gz"
tar -czf "$darwin_archive" -C "$darwin_root" .
darwin_sha="$(sha256_file "$darwin_archive")"
bash "$SCRIPT_DIR/acquire-mpv-runtime.sh" darwin "file://$darwin_archive" "$darwin_sha" "$TMP_ROOT/extracted-darwin" >/dev/null
pass_count=$((pass_count + 1))

bad_pc_root="$TMP_ROOT/bad-pc-runtime"
cp -R "$darwin_root" "$bad_pc_root"
printf 'prefix=/opt/build-machine\n' > "$bad_pc_root/lib/pkgconfig/mpv.pc"
bad_pc_archive="$TMP_ROOT/bad-pc-runtime.tar.gz"
tar -czf "$bad_pc_archive" -C "$bad_pc_root" .
expect_failure nonrelocatable-pkgconfig \
  bash "$SCRIPT_DIR/acquire-mpv-runtime.sh" darwin "file://$bad_pc_archive" "$(sha256_file "$bad_pc_archive")" "$TMP_ROOT/bad-pc-dest"

echo "mpv runtime acquisition tests passed: $pass_count"
