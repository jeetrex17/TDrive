#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PACKAGE_SCRIPT="$SCRIPT_DIR/package-mpv-darwin.sh"

fail() {
  printf 'test-package-mpv-darwin: %s\n' "$*" >&2
  exit 1
}

deployment_incompatible_status="$("$PACKAGE_SCRIPT" --deployment-incompatible-exit-code)"
case "$deployment_incompatible_status" in
  ''|*[!0-9]*) fail "deployment incompatibility status is not numeric: $deployment_incompatible_status" ;;
esac
if [ "$deployment_incompatible_status" -le 1 ] || [ "$deployment_incompatible_status" -gt 125 ]; then
  fail "deployment incompatibility status must be a dedicated portable exit code"
fi

TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/tdrive-package-mpv-darwin-test.XXXXXX")"
trap 'rm -rf "$TMP_ROOT"' EXIT

stub_bin="$TMP_ROOT/bin"
app_path="$TMP_ROOT/TDrive.app"
runtime_root="$TMP_ROOT/runtime"
mkdir -p "$stub_bin" "$app_path/Contents/MacOS" "$runtime_root/bin" "$runtime_root/lib"

printf '#!/usr/bin/env bash\nprintf "Darwin\\n"\n' > "$stub_bin/uname"
printf '#!/usr/bin/env bash\n[ "${1:-}" = "-archs" ] || exit 1\nprintf "arm64\\n"\n' > "$stub_bin/lipo"
printf '#!/usr/bin/env bash\ntarget="${!#}"\ncase "$target" in\n  */Contents/MacOS/TDrive) printf "minos %%s\\n" "${FIXTURE_APP_MINOS:-11.0}" ;;\n  *) printf "minos 15.0\\n" ;;\nesac\n' > "$stub_bin/xcrun"
chmod 755 "$stub_bin/uname" "$stub_bin/lipo" "$stub_bin/xcrun"

printf '#!/usr/bin/env bash\nexit 0\n' > "$app_path/Contents/MacOS/TDrive"
printf '#!/usr/bin/env bash\nexit 0\n' > "$runtime_root/bin/mpv"
chmod 755 "$app_path/Contents/MacOS/TDrive" "$runtime_root/bin/mpv"
printf 'fixture\n' > "$runtime_root/lib/libmpv.2.dylib"
printf 'fixture source\n' > "$runtime_root/SOURCE.txt"
printf 'fixture notices\n' > "$runtime_root/THIRD_PARTY_NOTICES.txt"

set +e
PATH="$stub_bin:$PATH" TDRIVE_MPV_RUNTIME_DIR="$runtime_root" \
  "$PACKAGE_SCRIPT" "$app_path" > "$TMP_ROOT/incompatible.out" 2>&1
actual_status=$?
set -e
if [ "$actual_status" -ne "$deployment_incompatible_status" ]; then
  cat "$TMP_ROOT/incompatible.out" >&2
  fail "deployment incompatibility returned $actual_status, expected $deployment_incompatible_status"
fi

set +e
PATH="$stub_bin:$PATH" "$PACKAGE_SCRIPT" "$TMP_ROOT/missing.app" > "$TMP_ROOT/generic.out" 2>&1
generic_status=$?
set -e
if [ "$generic_status" -eq 0 ] || [ "$generic_status" -eq "$deployment_incompatible_status" ]; then
  cat "$TMP_ROOT/generic.out" >&2
  fail "ordinary packaging failure returned reserved status $generic_status"
fi

printf 'package-mpv-darwin exit-code contract tests passed\n'

cat > "$runtime_root/bin/mpv" <<'EOF'
#!/bin/sh
if [ "${2:-}" = --version ]; then
  printf 'mpv v%s\nFFmpeg version: 9.0.2\n' "$FIXTURE_MPV_VERSION"
fi
EOF
cat > "$stub_bin/otool" <<'EOF'
#!/bin/sh
printf '%s:\n' "$2"
EOF
for tool in codesign install_name_tool; do
  printf '#!/bin/sh\nexit 0\n' > "$stub_bin/$tool"
done
chmod 755 "$runtime_root/bin/mpv" "$stub_bin/otool" "$stub_bin/codesign" "$stub_bin/install_name_tool"

export PATH="$stub_bin:$PATH"
export FIXTURE_APP_MINOS=15.0 FIXTURE_MPV_VERSION=99.0.0
export TDRIVE_MPV_RUNTIME_DIR="$runtime_root"
export TDRIVE_MPV_ARCHIVE_SHA256=0000000000000000000000000000000000000000000000000000000000000000
if ! "$PACKAGE_SCRIPT" "$app_path" "$FIXTURE_MPV_VERSION" > "$TMP_ROOT/fixture.out" 2>&1; then
  cat "$TMP_ROOT/fixture.out" >&2
  fail 'the unpinned CI bundle rejected the installed fixture version'
fi
manifest="$app_path/Contents/Resources/media/media-runtime.manifest"
grep -Fxq "mpv_version=$FIXTURE_MPV_VERSION" "$manifest" || fail 'fixture manifest recorded the wrong version'
grep -Fxq 'ci_fixture=true' "$manifest" || fail 'fixture manifest did not identify the CI runtime'
grep -Fxq 'release_runtime=false' "$manifest" || fail 'fixture manifest identified the runtime as a release'

if "$PACKAGE_SCRIPT" "$app_path" > "$TMP_ROOT/default.out" 2>&1 ||
  ! grep -q 'mpv version mismatch' "$TMP_ROOT/default.out"; then
  fail 'default packaging did not enforce the pinned version'
fi

if TDRIVE_MPV_ARCHIVE_SHA256=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef \
  "$PACKAGE_SCRIPT" "$app_path" "$FIXTURE_MPV_VERSION" > "$TMP_ROOT/pinned.out" 2>&1 ||
  ! grep -q 'test mpv version requires an unpinned runtime' "$TMP_ROOT/pinned.out"; then
  fail 'checksum-pinned packaging did not reject the fixture version override'
fi

printf 'package-mpv-darwin version contract tests passed\n'
