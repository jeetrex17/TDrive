#!/usr/bin/env bash
set -euo pipefail

# Run in the minimal smoke container with namespace permissions sufficient for
# WebKit's sandbox. Never disable WebKit's sandbox to make this test pass.
[ "$#" -eq 1 ] || { echo 'usage: smoke-appimage.sh <AppImage>' >&2; exit 1; }
[ "$(id -u)" -ne 0 ] || { echo 'Smoke test must run as a nonroot user' >&2; exit 1; }
appimage="$(realpath "$1")"
[ -x "$appimage" ] || { echo "AppImage is not executable: $appimage" >&2; exit 1; }
work="$(mktemp -d /tmp/tdrive-smoke.XXXXXX)"
artifacts="${TDRIVE_SMOKE_ARTIFACTS:-$work/artifacts}"
mkdir -p "$artifacts"
app_pid='' xvfb_pid=''
cleanup() {
  local result=$?
  if [ "$result" -ne 0 ]; then
    echo 'AppImage smoke test failed; application log follows:' >&2
    cat "$artifacts/application.log" >&2 2>/dev/null || true
    cat "$artifacts/ldd.log" >&2 2>/dev/null || true
    cat "$artifacts/extract.log" >&2 2>/dev/null || true
    cat "$artifacts/xvfb.log" >&2 2>/dev/null || true
    ps -ef >&2 || true
  fi
  [ -z "$app_pid" ] || kill "$app_pid" 2>/dev/null || true
  [ -z "$xvfb_pid" ] || kill "$xvfb_pid" 2>/dev/null || true
  rm -rf "$work/squashfs-root"
}
trap cleanup EXIT
fail() { echo "$*" >&2; exit 1; }

# Explicit extraction avoids FUSE and makes the actual shipped executables
# available for dependency checks before launching the normal AppRun entrypoint.
cd "$work"
"$appimage" --appimage-extract > "$artifacts/extract.log" 2>&1
appdir="$work/squashfs-root"
[ -x "$appdir/AppRun" ] || fail 'AppImage has no executable AppRun'
library_path="$appdir/usr/lib:$appdir/usr/lib/x86_64-linux-gnu"
while IFS= read -r directory; do
  library_path="$library_path:$directory"
done < <(find "$appdir/usr/lib" -type d | sort)
executables=("$appdir/usr/bin/TDrive")
for helper in WebKitWebProcess WebKitNetworkProcess; do
  helper_path="$(find "$appdir/usr" -type f -name "$helper" -print -quit)"
  [ -n "$helper_path" ] || fail "AppImage is missing $helper"
  executables+=("$helper_path")
done
for executable in "${executables[@]}"; do
  echo "Dependencies for $executable" >> "$artifacts/ldd.log"
  LD_LIBRARY_PATH="$library_path" ldd "$executable" >> "$artifacts/ldd.log" 2>&1 \
    || fail "Cannot inspect dependencies for $executable"
done
if grep -q 'not found' "$artifacts/ldd.log"; then
  fail 'Bundled application or WebKit helper has missing shared libraries'
fi

export DISPLAY=:99 LIBGL_ALWAYS_SOFTWARE=1
export XDG_RUNTIME_DIR="$work/runtime"
mkdir -m 700 "$XDG_RUNTIME_DIR"
Xvfb "$DISPLAY" -screen 0 1280x900x24 -nolisten tcp > "$artifacts/xvfb.log" 2>&1 &
xvfb_pid=$!
for attempt in $(seq 1 50); do
  if xdpyinfo -display "$DISPLAY" >/dev/null 2>&1; then break; fi
  kill -0 "$xvfb_pid" 2>/dev/null || fail 'Xvfb exited before becoming ready'
  sleep 0.1
done
xdpyinfo -display "$DISPLAY" >/dev/null 2>&1 || fail 'Xvfb did not become ready'
dbus-run-session -- "$appdir/AppRun" > "$artifacts/application.log" 2>&1 &
app_pid=$!

has_window() { xdotool search --onlyvisible --name 'TDrive' >/dev/null 2>&1; }
has_webkit() { pgrep -f '[W]ebKitWebProcess' >/dev/null; }
for attempt in $(seq 1 60); do
  kill -0 "$app_pid" 2>/dev/null || fail 'Application exited during startup'
  if has_window && has_webkit; then break; fi
  sleep 1
done
has_window || fail 'No visible TDrive window appeared within 60 seconds'
has_webkit || fail 'No live WebKitWebProcess appeared within 60 seconds'

# Require a stable window and helper before checking the rendered pixels.
for second in $(seq 1 20); do
  kill -0 "$app_pid" 2>/dev/null || fail 'Application exited during the rendering check'
  has_window || fail 'TDrive window disappeared during the rendering check'
  has_webkit || fail 'WebKitWebProcess exited during the rendering check'
  sleep 1
done
if grep -Eiq 'error while loading shared libraries|symbol lookup error|failed to spawn|unable to spawn|failed to launch.*WebKit|bwrap:.*(denied|not permitted)' "$artifacts/application.log"; then
  fail 'Application reported a library, WebKit, or sandbox startup failure'
fi
window_id="$(xdotool search --onlyvisible --name 'TDrive' | head -1)"
xwd -id "$window_id" -silent -out "$artifacts/window.xwd"
convert "$artifacts/window.xwd" "$artifacts/window.png"
colors="$(identify -format '%k' "$artifacts/window.png")"
[ "$colors" -gt 64 ] || fail "Frontend appears blank ($colors colors in the window)"
echo "AppImage smoke test passed: rendered TDrive frontend ($colors colors) and live WebKit process for 20 seconds. Artifacts: $artifacts"
