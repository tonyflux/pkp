#!/usr/bin/env bash
set -euo pipefail
binary=$1
log=$(mktemp)
profile=$(mktemp -d)
export PKP_CONFIG_DIR=$profile
export TMPDIR="$profile/tmp"
mkdir -m700 "$TMPDIR"
export PKP_TELEMETRY_API=http://127.0.0.1:1 PKP_CRASH_API= PKP_SUBSRELAY_API=
export PKP_AUTH_API=http://127.0.0.1:1 PKP_DRIVE_API=http://127.0.0.1:1
cleanup() {
  if [[ -n ${pid:-} ]]; then
    kill -- -"$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi
  rm -f "$log"
  rm -rf "$profile"
}
trap cleanup EXIT
setsid "$binary" > "$log" 2>&1 &
pid=$!
renderer() {
  local candidate ancestor parent executable
  for candidate in $(pgrep -f '/usr/.*WebKitWebProcess' || true); do
    IFS= read -r -d '' executable < "/proc/$candidate/cmdline" || continue
    [[ ${executable##*/} == WebKitWebProcess ]] || continue
    ancestor=$candidate
    while [[ $ancestor -gt 1 && -r /proc/$ancestor/status ]]; do
      if [[ $ancestor == "$pid" ]]; then
        # WebKit's renderer must retain its seccomp filter, not run unsandboxed.
        grep -Eq '^Seccomp:[[:space:]]+2$' "/proc/$candidate/status"
        return
      fi
      parent=$(awk '/^PPid:/ {print $2}' "/proc/$ancestor/status")
      ancestor=${parent:-1}
    done
  done
  return 1
}
for ((i=0; i<150; i++)); do
  kill -0 "$pid" || { cat "$log"; exit 1; }
  # Wails has reached a real sandboxed WebKit renderer, not just Go init.
  if renderer; then
    sleep 2
    kill -0 "$pid"
    if grep -E 'Unable to spawn|Failed to.*(sandbox|process)|error while loading shared libraries|symbol lookup error' "$log"; then exit 1; fi
    exit 0
  fi
  sleep 0.1
done
cat "$log"
echo 'PKP did not start a WebKit renderer.' >&2
exit 1
