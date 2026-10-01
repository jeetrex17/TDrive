#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
work="$(mktemp -d /tmp/tdrive-appimage-launcher.XXXXXX)"
trap 'rm -rf "$work"' EXIT
appdir="$work/App With Spaces.AppDir"
mkdir -p "$appdir/usr/bin" "$appdir/apprun-hooks"
cp "$SCRIPT_DIR/../build/linux/AppRun" "$appdir/AppRun"
cat > "$appdir/apprun-hooks/fixture.sh" <<'SH'
export TDRIVE_HOOK_LOADED=yes
SH
cat > "$appdir/usr/bin/TDrive" <<'SH'
#!/usr/bin/env bash
set -eu
[ "$PWD" = "$APPDIR/usr" ]
[ "$TDRIVE_HOOK_LOADED" = yes ]
[ "$LD_LIBRARY_PATH" = "$APPDIR/usr/lib:$APPDIR/usr/lib/x86_64-linux-gnu" ]
[ "$1" = 'argument with spaces' ]
[ "$2" = '--fixture' ]
[ "${WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS:-}" = '' ]
SH
chmod +x "$appdir/usr/bin/TDrive"
LD_LIBRARY_PATH=/bad/host/path bash "$appdir/AppRun" 'argument with spaces' --fixture
echo 'AppImage launcher relocation and argument tests passed'
