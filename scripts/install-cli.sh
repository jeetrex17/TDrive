#!/usr/bin/env sh
set -eu

DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"

if [ -x "$DIR/tdrive" ]; then
  CLI="$DIR/tdrive"
elif command -v tdrive >/dev/null 2>&1; then
  CLI="$(command -v tdrive)"
else
  echo "tdrive binary not found next to install-cli.sh" >&2
  exit 1
fi

exec "$CLI" install-cli --update-shell --force "$@"
