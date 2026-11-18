#!/bin/sh
set -eu
[ "$(id -u)" = 0 ] || { echo 'Run this administrator-only helper with sudo.' >&2; exit 1; }
[ "$#" = 1 ] && [ -f "$1" ] || { echo 'Usage: install-apparmor.sh /absolute/stable/PKP.AppImage-or-AppRun' >&2; exit 1; }
path=$(realpath "$1")
case "$path" in *[!a-zA-Z0-9_./\ -]*) echo 'Unsupported AppArmor path characters.' >&2; exit 1 ;; esac
command -v apparmor_parser >/dev/null
# Do not grant a wildcard over every AppImage or every temporary extraction.
profile=/etc/apparmor.d/pkp-appimage
umask 022
printf 'abi <abi/4.0>,\ninclude <tunables/global>\nprofile pkp-appimage "%s" flags=(unconfined) {\n  userns,\n}\n' "$path" > "$profile"
chown root:root "$profile"
chmod 644 "$profile"
apparmor_parser -r "$profile"
echo "Installed $profile for $path. Reinstall it if you move the executable."
