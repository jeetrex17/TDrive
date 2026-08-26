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
printf '#!/usr/bin/env bash\ntarget="${!#}"\ncase "$target" in\n  */Contents/MacOS/TDrive) printf "minos 11.0\\n" ;;\n  *) printf "minos 15.0\\n" ;;\nesac\n' > "$stub_bin/xcrun"
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
