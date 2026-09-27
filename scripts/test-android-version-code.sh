#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script="$root/scripts/android-version-code.sh"

expect_code() {
  local actual
  actual="$(bash "$script" "$1")"
  if [ "$actual" != "$2" ]; then
    echo "Expected $1 to produce $2, got $actual" >&2
    exit 1
  fi
}

reject_tag() {
  if bash "$script" "$1" >/dev/null 2>&1; then
    echo "Expected $1 to be rejected" >&2
    exit 1
  fi
}

expect_code v1.9.0 1009000
expect_code v2.0.0 2000000
expect_code v2.0.1 2000001
expect_code v2.1.0 2001000
expect_code v0.0.1 1

reject_tag 2.0.0
reject_tag v1.02.0
reject_tag v2.0.0-rc.1
reject_tag 'v2.0.0$(id)'
reject_tag v0.0.0
reject_tag v2.1000.0
reject_tag v2.0.1000
reject_tag v2100.0.0

echo "Android version-code tests passed"
