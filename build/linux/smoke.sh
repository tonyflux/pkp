#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
export LIBGL_ALWAYS_SOFTWARE=1
# Explicit opt-in keeps display-dependent tests out of ordinary headless tests.
# shortcut: Xvfb produces intermittent black frames with multiple llvmpipe workers; remove the pin once the runner renders reliably without it.
LP_NUM_THREADS=1 PKP_TEST_VIDEO_SURFACE=1 GDK_BACKEND=x11 xvfb-run -a \
  go test -tags pkp_surface_test ./internal/mpv -run '^TestLinuxSurface$' -count=1 -timeout 90s
runtime=$(mktemp -d)
chmod 700 "$runtime"
export XDG_RUNTIME_DIR=$runtime
weston --backend=headless-backend.so --socket=pkp-wayland --idle-time=0 --width=1280 --height=720 --use-pixman > "$runtime/weston.log" 2>&1 &
weston_pid=$!
trap 'kill "$weston_pid" 2>/dev/null || true; rm -rf "$runtime"' EXIT
for ((i=0; i<100; i++)); do
  [[ -S $runtime/pkp-wayland ]] && break
  kill -0 "$weston_pid" || { cat "$runtime/weston.log"; exit 1; }
  sleep 0.1
done
[[ -S $runtime/pkp-wayland ]] || { cat "$runtime/weston.log"; exit 1; }
PKP_TEST_VIDEO_SURFACE=1 GDK_BACKEND=wayland WAYLAND_DISPLAY=pkp-wayland \
  go test -tags pkp_surface_test ./internal/mpv -run '^TestLinuxSurface$' -count=1 -timeout 90s
