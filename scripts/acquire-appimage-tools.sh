#!/usr/bin/env bash
set -euo pipefail
[ "$#" -eq 1 ] || { echo 'usage: acquire-appimage-tools.sh <tool-directory>' >&2; exit 2; }
tool_dir="$1"
mkdir -p "$tool_dir"
tool_dir="$(cd "$tool_dir" && pwd -P)"
fetch() {
  local url="$1" digest="$2" name="$3"
  curl --fail --location --silent --show-error --retry 3 "$url" --output "$tool_dir/$name"
  printf '%s  %s\n' "$digest" "$tool_dir/$name" | sha256sum --check --status
  chmod 755 "$tool_dir/$name"
}
# Tauri's pinned linuxdeploy and GTK plugin support relocatable WebKitGTK
# helpers, GTK resources, GIO TLS modules and native Wayland sessions.
fetch https://github.com/tauri-apps/binary-releases/releases/download/linuxdeploy-07333c6/linuxdeploy-x86_64.AppImage \
  36a2d7e274d12e1050d0e9ecfe11d339ed54720b2bec464c286d53f8b07f5c62 linuxdeploy-x86_64.AppImage
fetch https://raw.githubusercontent.com/tauri-apps/tauri/30da1fd6e17de6107ecc850c95dfb16b5729f2dd/crates/tauri-bundler/src/bundle/linux/appimage/linuxdeploy-plugin-gtk.sh \
  ef6b9a980417243bc62e0241b51dc49876032afd1bab9b4762389f961b406d9b linuxdeploy-plugin-gtk.sh
appimagetool_url="${APPIMAGETOOL_URL:-https://github.com/AppImage/appimagetool/releases/download/1.9.1/appimagetool-x86_64.AppImage}"
appimagetool_digest="${APPIMAGETOOL_SHA256:-ed4ce84f0d9caff66f50bcca6ff6f35aae54ce8135408b3fa33abfc3cb384eb0}"
if [ -n "${APPIMAGETOOL_URL:-}" ] && [ -z "${APPIMAGETOOL_SHA256:-}" ]; then
  echo 'Custom APPIMAGETOOL_URL requires APPIMAGETOOL_SHA256' >&2
  exit 1
fi
fetch "$appimagetool_url" "$appimagetool_digest" appimagetool-x86_64.AppImage
