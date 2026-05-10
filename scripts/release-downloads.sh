#!/usr/bin/env bash
set -euo pipefail

repo="${GITHUB_REPOSITORY:-jeetrex17/TDrive}"
tag="${1:-}"

if ! command -v gh >/dev/null 2>&1; then
  echo "GitHub CLI (gh) is required." >&2
  exit 1
fi

if [[ -n "$tag" ]]; then
  rows="$(gh api "repos/${repo}/releases/tags/${tag}" \
    --jq '.tag_name as $tag | .assets[]? | [$tag, .name, (.download_count | tostring)] | @tsv')"
else
  rows="$(gh api --paginate "repos/${repo}/releases" \
    --jq '.[] | .tag_name as $tag | .assets[]? | [$tag, .name, (.download_count | tostring)] | @tsv')"
fi

if [[ -z "$rows" ]]; then
  echo "No release assets found."
  exit 0
fi

printf "%-10s  %-55s  %10s\n" "Release" "Asset" "Downloads"
printf "%-10s  %-55s  %10s\n" "-------" "-----" "---------"

total=0
while IFS=$'\t' read -r release asset downloads; do
  printf "%-10s  %-55s  %10d\n" "$release" "$asset" "$downloads"
  total=$((total + downloads))
done <<< "$rows"

printf "%-10s  %-55s  %10s\n" "-------" "-----" "---------"
printf "%-10s  %-55s  %10d\n" "Total" "" "$total"
