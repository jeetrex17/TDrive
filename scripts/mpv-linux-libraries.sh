#!/usr/bin/env bash
# Collect or validate the ELF DT_NEEDED closure of a trusted package-manager mpv.
# ldd includes transitive dependencies. Drivers/plugins opened with dlopen remain
# host integrations; glibc and its loader must also come from the target host.
set -euo pipefail
mode="${1:-}"
runtime_root="${2:-}"
die() { printf 'mpv-linux-libraries: %s\n' "$*" >&2; exit 1; }
case "$mode" in collect|validate) ;; *) die 'usage: mpv-linux-libraries.sh <collect|validate> <runtime-root>' ;; esac
[ -f "$runtime_root/mpv" ] || die "mpv not found in $runtime_root"
runtime_root="$(cd "$runtime_root" && pwd -P)"
mkdir -p "$runtime_root/lib"
# Do not let caller overrides contaminate the discovered dependency graph.
ldd_status=0
if [ "$mode" = collect ]; then
  dependencies="$(env -u LD_LIBRARY_PATH -u LD_PRELOAD -u LD_AUDIT ldd "$runtime_root/mpv" 2>&1)" || ldd_status=$?
else
  dependencies="$(env -u LD_PRELOAD -u LD_AUDIT LD_LIBRARY_PATH="$runtime_root/lib" ldd "$runtime_root/mpv" 2>&1)" || ldd_status=$?
fi
if [ "$ldd_status" -ne 0 ] || [[ "$dependencies" =~ ^[[:space:]]*statically[[:space:]]linked[[:space:]]*$ ]]; then
  # ldd rejects static executables. Accept them only when readelf proves there
  # is neither an interpreter nor a DT_NEEDED entry (including static PIE).
  readelf -h "$runtime_root/mpv" >/dev/null 2>&1 || die "ldd failed: $dependencies"
  program_headers="$(readelf -l "$runtime_root/mpv")" || die 'could not read ELF program headers'
  dynamic_entries="$(readelf -d "$runtime_root/mpv")" || die 'could not read ELF dynamic section'
  if ! printf '%s\n' "$program_headers" | grep -q INTERP &&
     ! printf '%s\n' "$dynamic_entries" | grep -q '(NEEDED)'; then
    exit 0
  fi
  die "ldd failed: $dependencies"
fi
[ -n "$dependencies" ] || die 'ldd returned no dependencies'
while IFS= read -r line; do
  # ldd prints whitespace-delimited names, paths and load addresses. Reject
  # anything outside that grammar rather than silently produce a partial bundle.
  read -r name arrow path address extra <<< "$line"
  [ -n "$name" ] || continue
  case "$name" in linux-vdso.so.*|linux-gate.so.*) continue ;; esac
  if [ "$arrow" = '=>' ]; then
    [ "$path" != not ] || die "unresolved dependency: $line"
    [ -z "$extra" ] && [[ "$path" = /* ]] && [[ "$address" = \(0x*\) ]] || die "unrecognized ldd output: $line"
  elif [[ "$name" = /* ]] && [[ "$arrow" = \(0x*\) ]] && [ -z "$path" ]; then
    path="$name"
    name="${path##*/}"
  else
    die "unrecognized ldd output: $line"
  fi
  # Keep the glibc ABI family together with the system loader. All other
  # libraries, including libstdc++, libgcc, FFmpeg and graphics clients, travel
  # in the private runtime. This sets the compatibility floor to the build OS.
  case "$name" in
    libc.so.6|libm.so.6|libpthread.so.0|libdl.so.2|librt.so.1|libresolv.so.2|libutil.so.1|libanl.so.1|libBrokenLocale.so.1|libnss_compat.so.2|libnss_dns.so.2|libnss_files.so.2|ld-linux-x86-64.so.2|ld-linux-aarch64.so.1) continue ;;
  esac
  [ "$name" = "${name##*/}" ] || die "invalid dependency name: $name"
  [ -f "$path" ] || die "dependency file missing: $path"
  if [ "$mode" = validate ]; then
    resolved_dir="$(cd "$(dirname "$path")" && pwd -P)"
    [ "$resolved_dir" = "$runtime_root/lib" ] && [ ! -L "$path" ] || die "dependency resolves outside the runtime: $name => $path"
  elif [ -e "$runtime_root/lib/$name" ]; then
    cmp -s "$path" "$runtime_root/lib/$name" || die "library basename collision: $name ($path)"
  else
    cp -pL "$path" "$runtime_root/lib/$name"
  fi
done <<< "$dependencies"
