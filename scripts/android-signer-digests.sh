#!/usr/bin/env bash
set -euo pipefail

sed -nE 's/^Signer (#[0-9]+|\(.*\)) certificate SHA-256 digest: ([[:xdigit:]:[:space:]]+)$/\2/p' |
  while IFS= read -r digest; do
    normalized="$(printf '%s' "$digest" | tr -d '[:space:]:' | tr '[:upper:]' '[:lower:]')"
    if [[ "$normalized" =~ ^[0-9a-f]{64}$ ]]; then
      printf '%s\n' "$normalized"
    fi
  done |
  sort -u
