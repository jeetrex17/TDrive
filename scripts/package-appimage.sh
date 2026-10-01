#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
die() { printf 'package-appimage: %s\n' "$*" >&2; exit 1; }
[ "$(uname -s)" = Linux ] || die 'Linux host required'
[ "$#" -eq 3 ] || die 'usage: package-appimage.sh <binary> <version> <output-directory>'
binary="$1" version="$2" output_dir="$3"
[[ "$version" =~ ^[a-zA-Z0-9._-]+$ ]] || die 'invalid artifact version'
[ -x "$binary" ] || die "executable not found: $binary"
[ -x "${TDRIVE_LINUXDEPLOY:-}" ] || die 'TDRIVE_LINUXDEPLOY must point to the pinned linuxdeploy'
[ -x "${TDRIVE_APPIMAGETOOL:-}" ] || die 'TDRIVE_APPIMAGETOOL is required'
work="$(mktemp -d /tmp/tdrive-appimage.XXXXXX)"
trap 'rm -rf "$work"' EXIT
appdir="$work/TDrive.AppDir"
mkdir -p "$appdir/usr/bin" "$appdir/usr/share/applications" \
  "$appdir/usr/share/icons/hicolor/256x256/apps" "$appdir/usr/share/doc/TDrive"
cp "$binary" "$appdir/usr/bin/TDrive"
convert "$REPO_DIR/build/appicon.png" -resize 256x256 "$appdir/TDrive.png"
cp "$appdir/TDrive.png" "$appdir/usr/share/icons/hicolor/256x256/apps/TDrive.png"
cp "$REPO_DIR/build/linux/TDrive.desktop" "$appdir/TDrive.desktop"
cp "$appdir/TDrive.desktop" "$appdir/usr/share/applications/TDrive.desktop"
cp "$REPO_DIR/build/linux/AppRun" "$appdir/AppRun"
chmod 755 "$appdir/AppRun"
for tool in bwrap xdg-dbus-proxy; do
  [ -x "/usr/bin/$tool" ] || die "missing WebKit sandbox tool: $tool"
  cp "/usr/bin/$tool" "$appdir/usr/bin/$tool"
done

# These are spawned/dlopen'd by WebKit, so the main binary's DT_NEEDED
# closure alone does not include them. Preserve the distro's multiarch path.
webkit_dir="$(pkg-config --variable=libdir webkit2gtk-4.1)/webkit2gtk-4.1"
[[ "$webkit_dir" = /usr/lib/* ]] || die "unexpected WebKit helper path: $webkit_dir"
for helper in WebKitNetworkProcess WebKitWebProcess injected-bundle/libwebkit2gtkinjectedbundle.so; do
  [ -f "$webkit_dir/$helper" ] || die "missing WebKit helper: $webkit_dir/$helper"
done
mkdir -p "$appdir$(dirname "$webkit_dir")"
cp -a "$webkit_dir" "$appdir$(dirname "$webkit_dir")/"
for resource in /usr/share/webkitgtk-4.1 /usr/share/icu; do
  if [ -d "$resource" ]; then
    mkdir -p "$appdir$(dirname "$resource")"
    cp -a "$resource" "$appdir$(dirname "$resource")/"
  fi
done

export APPIMAGE_EXTRACT_AND_RUN=1 DEPLOY_GTK_VERSION=3 NO_STRIP=1
export PATH="$(dirname "$TDRIVE_LINUXDEPLOY"):$PATH"
"$TDRIVE_LINUXDEPLOY" --appdir "$appdir" --plugin gtk
# Capture redistribution notices before adding the separately qualified mpv
# runtime. Each copied library retains its distro package's copyright file.
while IFS= read -r -d '' library; do
  soname="${library##*/}"
  package="$(dpkg-query -S "*/$soname" 2>/dev/null | sed -n '1s/: \/.*//p' || true)"
  package="${package%%:*}"
  if [ -n "$package" ] && [ -f "/usr/share/doc/$package/copyright" ]; then
    mkdir -p "$appdir/usr/share/doc/$package"
    cp -L "/usr/share/doc/$package/copyright" "$appdir/usr/share/doc/$package/copyright"
  fi
done < <(find "$appdir/usr/lib" -type f -name '*.so*' -print0)
cp "$REPO_DIR/LICENSE" "$appdir/usr/share/doc/TDrive/LICENSE"
if [ -n "${TDRIVE_MPV_TEST_VERSION:-}" ]; then
  TDRIVE_MPV_ALLOW_TEST_VERSION=1 bash "$SCRIPT_DIR/package-mpv-linux.sh" "$appdir" "$TDRIVE_MPV_TEST_VERSION"
else
  bash "$SCRIPT_DIR/package-mpv-linux.sh" "$appdir"
fi
mkdir -p "$output_dir"
ARCH=x86_64 "$TDRIVE_APPIMAGETOOL" "$appdir" "$output_dir/TDrive-$version-x86_64.AppImage"
