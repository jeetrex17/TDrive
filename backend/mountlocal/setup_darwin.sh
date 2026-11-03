set -eu
# Executed as root. All code, paths and data are fixed application literals.
# Serialize cooperating setup attempts; shlock recovers locks from exited PIDs.
/usr/bin/shlock -p "$$" -f /private/var/run/tdrive-hostname.lock || exit 1
trap '/bin/rm -f /private/var/run/tdrive-hostname.lock' EXIT
hosts=/private/etc/hosts
[ ! -L "$hosts" ] && [ -f "$hosts" ] || exit 1
[ "$(/usr/bin/stat -f '%u' "$hosts")" = 0 ] || exit 1
# Reject group/world-writable files without relying on a particular owner mode.
mode=$(/usr/bin/stat -f '%Lp' "$hosts")
[ "$((0$mode & 022))" = 0 ] || exit 1
[ "$(/usr/bin/stat -f '%z' "$hosts")" -le 1048576 ] || exit 1
/usr/bin/awk '
{ sub(/#.*/, ""); for (i=2; i<=NF; i++) {
 name=tolower($i); sub(/\.$/, "", name)
 if (name == "tdrive.localhost") {
  if ($1 != "127.0.0.1") conflict=1
  if (tolower($i) == "tdrive.localhost") found=1
 }
}}
END { if (conflict) exit 2; if (found) exit 0; exit 3 }
' "$hosts" && exit 0
result=$?
[ "$result" = 3 ] || exit 1
# Start with a newline to preserve a final pre-existing line without a newline.
/usr/bin/printf '\n127.0.0.1 TDrive.localhost # TDrive Finder mount\n' >> "$hosts"
/usr/bin/dscacheutil -flushcache
