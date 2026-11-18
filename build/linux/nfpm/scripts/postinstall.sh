#!/bin/sh
set -e
if command -v update-desktop-database >/dev/null 2>&1; then
  update-desktop-database -q /usr/share/applications
fi
if command -v gtk-update-icon-cache >/dev/null 2>&1; then
  gtk-update-icon-cache -q -t -f /usr/share/icons/hicolor
fi
if command -v apparmor_parser >/dev/null 2>&1 && [ -r /sys/kernel/security/apparmor/profiles ]; then
  apparmor_parser -r /etc/apparmor.d/pkp
fi
