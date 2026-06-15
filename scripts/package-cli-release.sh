#!/usr/bin/env bash
set -euo pipefail

version="${1:-dev}"
goos="${CLI_GOOS:-$(go env GOOS)}"
goarch="${CLI_GOARCH:-$(go env GOARCH)}"
name="TDrive-${version}-${goos}-${goarch}-cli"
root="$(git rev-parse --show-toplevel)"
work="${root}/dist/${name}"

rm -rf "$work"
mkdir -p "$work"

GOOS="$goos" GOARCH="$goarch" go build -o "$work/tdrive" ./cmd/tdrive
cp "$root/scripts/install-cli.sh" "$work/install-cli.sh"
cp "$root/scripts/uninstall-cli.sh" "$work/uninstall-cli.sh"
chmod +x "$work/tdrive" "$work/install-cli.sh" "$work/uninstall-cli.sh"

tar -C "$root/dist" -czf "$root/dist/${name}.tar.gz" "$name"
rm -rf "$work"

echo "wrote: dist/${name}.tar.gz"
