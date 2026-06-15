#!/usr/bin/env sh
set -eu

DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"

if [ -x "$DIR/tdrive" ]; then
  CLI="$DIR/tdrive"
elif command -v tdrive >/dev/null 2>&1; then
  CLI="$(command -v tdrive)"
else
  CLI=""
fi

if [ -n "$CLI" ]; then
  exec "$CLI" uninstall-cli "$@"
fi

TARGET="${HOME}/.local/bin/tdrive"
rm -f "$TARGET"
echo "removed: ~/.local/bin/tdrive"
