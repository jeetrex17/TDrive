#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
work="$(mktemp -d /tmp/tdrive-appimage-acquisition.XXXXXX)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin"
cat > "$work/bin/curl" <<'SH'
#!/usr/bin/env bash
while [ "$#" -gt 0 ]; do
  if [ "$1" = --output ]; then
    printf 'tampered tool\n' > "$2"
    exit 0
  fi
  shift
done
exit 1
SH
chmod 755 "$work/bin/curl"
if PATH="$work/bin:$PATH" bash "$SCRIPT_DIR/acquire-appimage-tools.sh" "$work/tools" > "$work/result" 2>&1; then
  echo 'Tool acquisition accepted a tampered download' >&2
  exit 1
fi
[ -f "$work/tools/linuxdeploy-x86_64.AppImage" ]
[ ! -x "$work/tools/linuxdeploy-x86_64.AppImage" ]
[ ! -e "$work/tools/linuxdeploy-plugin-gtk.sh" ]
echo 'AppImage tool acquisition rejects tampered downloads before execution'
