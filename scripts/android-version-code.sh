#!/usr/bin/env bash
set -euo pipefail

tag="${1:-}"
if [[ ! "$tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "Expected a stable release tag such as v2.0.0" >&2
  exit 1
fi

major="${BASH_REMATCH[1]}"
minor="${BASH_REMATCH[2]}"
patch="${BASH_REMATCH[3]}"
if (( ${#major} > 4 || ${#minor} > 3 || ${#patch} > 3 )); then
  echo "Android version components exceed the supported range" >&2
  exit 1
fi

code=$((major * 1000000 + minor * 1000 + patch))
if (( major > 2099 || code == 0 )); then
  echo "Android version code must be between 1 and 2099999999" >&2
  exit 1
fi

printf '%d\n' "$code"
