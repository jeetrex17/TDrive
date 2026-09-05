#!/usr/bin/env bash
# Reproduce restored cgo archives retaining an obsolete pkg-config library path.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/tdrive-prepare-mpv-test.XXXXXX")"
trap 'rm -rf "$TMP_ROOT"' EXIT

if [ "$(uname -s)" != Darwin ]; then
  echo "SKIP: macOS runtime preparation regression requires Darwin"
  exit 0
fi

export GOCACHE="$TMP_ROOT/go-cache"
export GOWORK=off
export CGO_ENABLED=1
export RUNNER_TEMP="$TMP_ROOT/runner"
export GITHUB_ENV="$TMP_ROOT/github-env"
export PKG_CONFIG_LIBDIR="$TMP_ROOT/pkgconfig"
unset PKG_CONFIG_PATH TDRIVE_MACOS_MPV_RUNTIME_URL TDRIVE_MACOS_MPV_RUNTIME_SHA256
mkdir -p "$TMP_ROOT/project/native" "$TMP_ROOT/pkgconfig" "$TMP_ROOT/bin" \
  "$TMP_ROOT/repo/scripts" "$TMP_ROOT/repo/build/darwin"
cp "$SCRIPT_DIR/prepare-mpv-runtime.sh" "$TMP_ROOT/repo/scripts/"
cp "$SCRIPT_DIR/../build/darwin/Info.plist" "$TMP_ROOT/repo/build/darwin/"
# Acquisition is independent of cache invalidation and has its own archive tests.
cat > "$TMP_ROOT/repo/scripts/acquire-mpv-runtime.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
mkdir -p "$4/lib/pkgconfig"
cp "$PKG_CONFIG_LIBDIR/mpv.pc" "$4/lib/pkgconfig/mpv.pc"
printf '%s\n' "$3"
EOF

cat > "$TMP_ROOT/project/go.mod" <<'EOF'
module example.com/runtime-cache-test

go 1.22
EOF
cat > "$TMP_ROOT/project/native/native.go" <<'EOF'
package native

// #cgo pkg-config: mpv
// int runtime_fixture(void);
import "C"

func Value() int { return int(C.runtime_fixture()) }
EOF
cat > "$TMP_ROOT/project/main.go" <<'EOF'
package main

import "example.com/runtime-cache-test/native"

func main() { if native.Value() != 42 { panic("wrong native library") } }
EOF
printf 'int runtime_fixture(void) { return 42; }\n' > "$TMP_ROOT/fixture.c"
cc -c "$TMP_ROOT/fixture.c" -o "$TMP_ROOT/fixture.o"

install_fixture() {
  local version="$1"
  mkdir -p "$TMP_ROOT/$version/lib"
  ar rcs "$TMP_ROOT/$version/lib/libtdrivefixture.a" "$TMP_ROOT/fixture.o"
  # Runtime preparation only copies this mocked dylib; the Go fixture links the archive.
  touch "$TMP_ROOT/$version/lib/libmpv.2.dylib"
  cat > "$TMP_ROOT/pkgconfig/mpv.pc" <<EOF
libdir=$TMP_ROOT/$version/lib
Name: mpv
Description: isolated cgo cache regression fixture
Version: 1.0
Libs: -L\${libdir} -ltdrivefixture
EOF
}

cat > "$TMP_ROOT/bin/brew" <<'EOF'
#!/bin/sh
echo 'mpv fixture'
EOF
cat > "$TMP_ROOT/bin/mpv" <<'EOF'
#!/bin/sh
exit 0
EOF
cat > "$TMP_ROOT/bin/lipo" <<'EOF'
#!/bin/sh
echo arm64
EOF
cat > "$TMP_ROOT/bin/xcrun" <<'EOF'
#!/bin/sh
echo 'minos 11.0'
EOF
chmod +x "$TMP_ROOT/bin/"*

cd "$TMP_ROOT/project"
for runtime_mode in homebrew pinned; do
  export PKG_CONFIG_LIBDIR="$TMP_ROOT/pkgconfig"
  unset MACOSX_DEPLOYMENT_TARGET
  if [ "$runtime_mode" = pinned ]; then
    export TDRIVE_MACOS_MPV_RUNTIME_URL=https://example.invalid/mpv.tar.gz
    export TDRIVE_MACOS_MPV_RUNTIME_SHA256=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
  fi
  # Each case uses its own cache so it cannot inherit successful native archives.
  export GOCACHE="$TMP_ROOT/go-cache-$runtime_mode"
  : > "$GITHUB_ENV"
  install_fixture old
  go build -o "$TMP_ROOT/old-app" .
  "$TMP_ROOT/old-app"
  rm -rf "$TMP_ROOT/old"
  install_fixture current
  # Changing only main forces relinking while preserving the cached cgo package.
  printf '\nfunc forceRelink_%s() {}\n' "$runtime_mode" >> main.go
  if go build -o "$TMP_ROOT/stale-app" . >"$TMP_ROOT/stale.log" 2>&1; then
    echo "fixture did not reproduce stale cgo linker flags" >&2
    exit 1
  fi
  if ! grep -q '/old/lib' "$TMP_ROOT/stale.log"; then
    cat "$TMP_ROOT/stale.log" >&2
    echo "fixture failed for a reason other than stale cgo linker flags" >&2
    exit 1
  fi

  PATH="$TMP_ROOT/bin:$PATH" bash "$TMP_ROOT/repo/scripts/prepare-mpv-runtime.sh"
  set -a
  source "$GITHUB_ENV"
  set +a
  if ! go build -o "$TMP_ROOT/fresh-app" . >"$TMP_ROOT/fresh.log" 2>&1; then
    cat "$TMP_ROOT/fresh.log" >&2
    echo "runtime preparation retained stale cgo linker flags" >&2
    exit 1
  fi
  "$TMP_ROOT/fresh-app"
  echo "PASS: $runtime_mode preparation invalidates stale cgo library paths"
done
