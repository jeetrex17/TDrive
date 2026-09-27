#!/usr/bin/env bash
set -euo pipefail

version="${1:-dev}"
if [[ ! "$version" =~ ^[0-9A-Za-z._-]+$ ]]; then
  echo "invalid CLI package version: use letters, digits, dot, underscore, or hyphen" >&2
  exit 1
fi
goos="${CLI_GOOS:-$(go env GOOS)}"
goarch="${CLI_GOARCH:-$(go env GOARCH)}"
name="TDrive-${version}-${goos}-${goarch}-cli"
root="$(git rev-parse --show-toplevel)"
commit="$(git -C "$root" rev-parse --verify HEAD)"
work="${root}/dist/${name}"

rm -rf "$work"
mkdir -p "$work"

GOOS="$goos" GOARCH="$goarch" go build -ldflags "-X main.buildVersion=${version} -X main.buildCommit=${commit}" -o "$work/tdrive" ./cmd/tdrive
cp "$root/scripts/install-cli.sh" "$work/install-cli.sh"
cp "$root/scripts/uninstall-cli.sh" "$work/uninstall-cli.sh"
chmod +x "$work/tdrive" "$work/install-cli.sh" "$work/uninstall-cli.sh"

tar -C "$root/dist" -czf "$root/dist/${name}.tar.gz" "$name"
rm -rf "$work"

echo "wrote: dist/${name}.tar.gz"
