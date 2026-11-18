#!/usr/bin/env bash
set -euo pipefail
# Execute a tiny real image with the same static runtime as the shipped image.
runtime=$1
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
chmod 755 "$tmp"
mkdir "$tmp/app" "$tmp/shared"
chmod 1777 "$tmp/shared"
cat > "$tmp/app/AppRun" <<'APP'
#!/bin/sh
set -eu
[ "$(stat -c %a "$APPDIR")" = 700 ]
[ "$(stat -c %u "$APPDIR")" = "$(id -u)" ]
printf '%s\n' "$APPDIR" > "$TMPDIR/result-$(id -u)"
APP
chmod 755 "$tmp/app/AppRun"
mksquashfs "$tmp/app" "$tmp/root.squashfs" -noappend -all-root -quiet >/dev/null
cat "$runtime" "$tmp/root.squashfs" > "$tmp/test.AppImage"
chmod 755 "$tmp/test.AppImage"
# A shared TMPDIR must not share an extraction path between users/invocations.
TMPDIR="$tmp/shared" "$tmp/test.AppImage" --appimage-extract-and-run
if ! id tester >/dev/null 2>&1; then useradd -m tester; fi
su tester -c "TMPDIR='$tmp/shared' '$tmp/test.AppImage' --appimage-extract-and-run"
first=$(cat "$tmp/shared/result-$(id -u)")
second=$(cat "$tmp/shared/result-$(id -u tester)")
[[ $first != "$second" ]]
[[ ! -e $first && ! -e $second ]]
echo 'Private 0700 per-user extraction and cleanup passed.'
