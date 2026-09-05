#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/system" "$work/runtime" "$work/other"
printf 'executable\n' > "$work/runtime/mpv"
printf 'decoder\n' > "$work/system/libdecoder.so.2.0"
ln -s libdecoder.so.2.0 "$work/system/libdecoder.so.2"
printf 'transitive\n' > "$work/system/libcodec.so.1"
cat > "$work/bin/ldd" <<'STUB'
#!/usr/bin/env bash
cat "$LDD_FIXTURE"
STUB
chmod +x "$work/bin/ldd"
original_path="$PATH"
export PATH="$work/bin:$PATH" LDD_FIXTURE="$work/ldd.txt"
cat > "$LDD_FIXTURE" <<LINES
 linux-vdso.so.1 (0x123)
 libdecoder.so.2 => $work/system/libdecoder.so.2 (0x123)
 libcodec.so.1 => $work/system/libcodec.so.1 (0x123)
 libc.so.6 => /lib/x86_64-linux-gnu/libc.so.6 (0x123)
 /lib64/ld-linux-x86-64.so.2 (0x123)
LINES
bash "$SCRIPT_DIR/mpv-linux-libraries.sh" collect "$work/runtime"
cmp "$work/system/libdecoder.so.2.0" "$work/runtime/lib/libdecoder.so.2"
cmp "$work/system/libcodec.so.1" "$work/runtime/lib/libcodec.so.1"
[ ! -L "$work/runtime/lib/libdecoder.so.2" ]
[ ! -e "$work/runtime/lib/libc.so.6" ]
expect_failure() {
  if bash "$SCRIPT_DIR/mpv-linux-libraries.sh" "$1" "$work/runtime" > "$work/error" 2>&1; then
    echo "expected failure: $2" >&2; exit 1
  fi
  grep -Fq "$2" "$work/error"
}
expect_failure validate 'outside the runtime'
sed "s|$work/system/|$work/runtime/lib/|g" "$LDD_FIXTURE" > "$work/resolved"
mv "$work/resolved" "$LDD_FIXTURE"
bash "$SCRIPT_DIR/mpv-linux-libraries.sh" validate "$work/runtime"
printf ' libmissing.so.1 => not found\n' > "$LDD_FIXTURE"
expect_failure collect 'unresolved dependency'
printf 'different\n' > "$work/other/libcodec.so.1"
printf ' libcodec.so.1 => %s/other/libcodec.so.1 (0x123)\n' "$work" > "$LDD_FIXTURE"
expect_failure collect 'library basename collision'
printf ' unexpected output\n' > "$LDD_FIXTURE"
expect_failure collect 'unrecognized ldd output'
if [ "$(uname -s)" = Linux ]; then
  export PATH="$original_path"
  command -v cc >/dev/null || { echo 'cc required for Linux ELF regression' >&2; exit 1; }
  mkdir -p "$work/elf-source" "$work/elf-runtime"
  printf 'int leaf(void) { return 42; }\n' > "$work/leaf.c"
  printf 'extern int leaf(void); int middle(void) { return leaf(); }\n' > "$work/middle.c"
  printf 'extern int middle(void); int main(void) { return middle() == 42 ? 0 : 1; }\n' > "$work/main.c"
  cc -shared -fPIC "$work/leaf.c" -Wl,-soname,libtdriveleaf.so.1 -o "$work/elf-source/libtdriveleaf.so.1.0"
  ln -s libtdriveleaf.so.1.0 "$work/elf-source/libtdriveleaf.so.1"
  cc -shared -fPIC "$work/middle.c" -L"$work/elf-source" -l:libtdriveleaf.so.1 \
    -Wl,-soname,libtdrivemiddle.so.1 -Wl,-rpath,"$work/elf-source" -o "$work/elf-source/libtdrivemiddle.so.1"
  cc "$work/main.c" -L"$work/elf-source" -l:libtdrivemiddle.so.1 \
    -Wl,-rpath,"$work/elf-source" -o "$work/elf-runtime/mpv"
  if bash "$SCRIPT_DIR/mpv-linux-libraries.sh" validate "$work/elf-runtime" > "$work/error" 2>&1; then
    echo 'expected validation to reject host ELF dependencies' >&2; exit 1
  fi
  grep -Fq 'outside the runtime' "$work/error"
  bash "$SCRIPT_DIR/mpv-linux-libraries.sh" collect "$work/elf-runtime"
  [ -f "$work/elf-runtime/lib/libtdriveleaf.so.1" ]
  [ ! -L "$work/elf-runtime/lib/libtdriveleaf.so.1" ]
  mv "$work/elf-source" "$work/elf-source-unavailable"
  bash "$SCRIPT_DIR/mpv-linux-libraries.sh" validate "$work/elf-runtime"
  env -u LD_PRELOAD -u LD_AUDIT LD_LIBRARY_PATH="$work/elf-runtime/lib" "$work/elf-runtime/mpv"
  mkdir -p "$work/static-runtime"
  printf 'int main(void) { return 0; }\n' > "$work/static.c"
  cc -static "$work/static.c" -o "$work/static-runtime/mpv"
  bash "$SCRIPT_DIR/mpv-linux-libraries.sh" collect "$work/static-runtime"
  bash "$SCRIPT_DIR/mpv-linux-libraries.sh" validate "$work/static-runtime"
  echo 'Linux real ELF transitive-dependency and static-runtime regressions passed'
else
  echo 'Skipping real ELF regression on non-Linux host'
fi
echo 'Linux mpv dependency tests passed'
