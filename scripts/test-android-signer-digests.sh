#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script="$root/scripts/android-signer-digests.sh"
first="d52f8be4583ebfc3cfe7ad5456b81bd136e6c07f622752b908355b9a8c7f6958"
second="a52f8be4583ebfc3cfe7ad5456b81bd136e6c07f622752b908355b9a8c7f6958"

expect_digests() {
  local actual
  actual="$(printf '%s\n' "$1" | bash "$script")"
  if [ "$actual" != "$2" ]; then
    printf 'Expected signer digests %s, got %s\n' "$2" "$actual" >&2
    exit 1
  fi
}

expect_digests "Signer #1 certificate SHA-256 digest: $first" "$first"
expect_digests "Signer (minSdkVersion=28, maxSdkVersion=32) certificate SHA-256 digest: $first" "$first"
expect_digests "Signer (minSdkVersion=33 (dev release=true), maxSdkVersion=2147483647) certificate SHA-256 digest: $first" "$first"
expect_digests "Signer #1 certificate SHA-256 digest: $(printf '%s' "$first" | tr '[:lower:]' '[:upper:]')" "$first"
expect_digests "Signer #1 certificate SHA-256 digest: d5:2f:8b:e4:58:3e:bf:c3:cf:e7:ad:54:56:b8:1b:d1:36:e6:c0:7f:62:27:52:b9:08:35:5b:9a:8c:7f:69:58" "$first"
expect_digests "Signer #1 certificate SHA-256 digest: $first"$'\r' "$first"
expect_digests "Signer #1 certificate SHA-256 digest: $first
Signer (minSdkVersion=33, maxSdkVersion=2147483647) certificate SHA-256 digest: $second" "$second
$first"
expect_digests "Source Stamp Signer certificate SHA-256 digest: $second
Signer #1 public key SHA-256 digest: $second
Signer #1 certificate SHA-256 digest: not-a-digest" ""

echo "Android signer-digest tests passed"
