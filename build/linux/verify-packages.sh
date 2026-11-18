#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
version=$(go run ./cmd/pkp-release version)
arch=$(go env GOARCH)
prefix="$PWD/bin/PKP-$version-linux-$arch"
bash build/linux/appimage/verify-extraction.sh "$PWD/bin/appimage-$arch/tools/runtime"
[[ $(dpkg-deb -f "$prefix.deb" Version) == "$version" ]]
[[ $(dpkg-deb -f "$prefix.deb" Architecture) == "$arch" ]]
[[ $(rpm -qp --qf '%{VERSION}' "$prefix.rpm") == "$version" ]]
case $arch in amd64) rpm_arch=x86_64 ;; arm64) rpm_arch=aarch64 ;; esac
[[ $(rpm -qp --qf '%{ARCH}' "$prefix.rpm") == "$rpm_arch" ]]
rpm -qpl "$prefix.rpm" | grep -Fx /opt/pkp/runtime/usr/lib/pkp/pkp
rpm -qpl "$prefix.rpm" | grep -Fx /opt/pkp/runtime/AppRun
dpkg -i "$prefix.deb"
test -x /usr/lib/pkp/pkp
cmp "$PWD/bin/linux-$arch/usr/lib/pkp/pkp" /usr/lib/pkp/pkp
test -x /usr/bin/pkp
test -s /usr/share/licenses/pkp/THIRD_PARTY_LICENSES.txt
desktop-file-validate /usr/share/applications/pkp.desktop
grep -F 'x-scheme-handler/pkp;' /usr/share/applications/pkp.desktop
grep -F '<allow_active>auth_self</allow_active>' /usr/share/polkit-1/actions/com.pkp.desktop.policy
# Malformed Go startup configuration exits before creating a GUI; no unresolved ELF deps.
output=$(GOMEMLIMIT=invalid /usr/bin/pkp 2>&1) && exit 1
[[ $output == *'malformed GOMEMLIMIT'* ]]
if ! id tester >/dev/null 2>&1; then useradd -m tester; fi
su tester -c "xvfb-run -a dbus-run-session -- env GDK_BACKEND=x11 LIBGL_ALWAYS_SOFTWARE=1 bash '$PWD/build/linux/appimage/smoke-launch.sh' /usr/bin/pkp"
# Extract to a path with spaces, unrelated to the build directory. Native libraries
# are hidden behind bundled /usr in AppRun, not found on the host by accident.
tmp=$(mktemp -d)
profiles=()
cleanup() {
  for profile in "${profiles[@]}"; do apparmor_parser -R "$profile"; done
  rm -rf "$tmp"
}
permit_userns() {
  if command -v apparmor_parser >/dev/null && [ -r /sys/kernel/security/apparmor/profiles ]; then
    local profile="$tmp/apparmor-${#profiles[@]}"
    printf 'abi <abi/4.0>,\ninclude <tunables/global>\nprofile pkp-package-test-%s "%s" flags=(unconfined) {\n  userns,\n}\n' "${#profiles[@]}" "$1" > "$profile"
    apparmor_parser -r "$profile"
    profiles+=("$profile")
  fi
}
trap cleanup EXIT
(cd "$tmp" && "$prefix.AppImage" --appimage-extract >/dev/null)
mv "$tmp/squashfs-root" "$tmp/relocated PKP"
image="$tmp/relocated PKP"
cmp "$PWD/bin/linux-$arch/usr/lib/pkp/pkp" "$image/usr/lib/pkp/pkp"
test -x "$image/usr/lib/pkp/Helpers/ffmpeg"
test -x "$image/usr/lib/pkp/Helpers/ffprobe"
find "$image/usr" -name WebKitWebProcess -type f | grep -q .
find "$image/usr" -name WebKitNetworkProcess -type f | grep -q .
# AppRun enters /usr namespace before Go's init, preserving sandbox configuration.
chmod -R a+rX "$tmp"
if ! id tester >/dev/null 2>&1; then useradd -m tester; fi
permit_userns "$image/AppRun"
permit_userns "$prefix.AppImage"
output=$(su tester -c "GOMEMLIMIT=invalid '$image/AppRun'" 2>&1) && exit 1
[[ $output == *'malformed GOMEMLIMIT'* ]]
# Static runtime supports extract-and-run without libfuse2/FUSE mounted.
output=$(su tester -c "GOMEMLIMIT=invalid '$prefix.AppImage' --appimage-extract-and-run" 2>&1) && exit 1
[[ $output == *'malformed GOMEMLIMIT'* ]]
su tester -c "xvfb-run -a dbus-run-session -- env APPIMAGE_EXTRACT_AND_RUN=1 GDK_BACKEND=x11 LIBGL_ALWAYS_SOFTWARE=1 bash '$PWD/build/linux/appimage/smoke-launch.sh' '$prefix.AppImage'"
su tester -c "xvfb-run -a dbus-run-session -- env GDK_BACKEND=x11 LIBGL_ALWAYS_SOFTWARE=1 bash '$PWD/build/linux/appimage/smoke-launch.sh' '$image/AppRun'"
triplet=$(cat "$image/.triplet")
loader=$(cat "$image/.loader")
"$image$loader" --library-path "$image/usr/lib/$triplet" "$image/usr/bin/bwrap" \
  --bind / / --dev-bind /dev /dev --ro-bind "$image/usr" /usr --setenv LD_LIBRARY_PATH "/usr/lib/$triplet" \
  -- /usr/lib/pkp/Helpers/ffmpeg -v error -filter_complex sine=duration=0.01 -f null -
"$image$loader" --library-path "$image/usr/lib/$triplet" "$image/usr/bin/bwrap" \
  --bind / / --dev-bind /dev /dev --ro-bind "$image/usr" /usr --setenv LD_LIBRARY_PATH "/usr/lib/$triplet" \
  -- /usr/lib/pkp/Helpers/ffprobe -version
rpmroot="$tmp/rpm-root"
mkdir -p "$rpmroot"
rpm --root "$rpmroot" -i --nodeps --noscripts "$prefix.rpm"
chmod -R a+rX "$rpmroot"
test -x "$rpmroot/opt/pkp/runtime/AppRun"
cmp "$PWD/bin/linux-$arch/usr/lib/pkp/pkp" "$rpmroot/opt/pkp/runtime/usr/lib/pkp/pkp"
permit_userns "$rpmroot/opt/pkp/runtime/AppRun"
su tester -c "xvfb-run -a dbus-run-session -- env PKP_LINUX_FORMAT=rpm GDK_BACKEND=x11 LIBGL_ALWAYS_SOFTWARE=1 bash '$PWD/build/linux/appimage/smoke-launch.sh' '$rpmroot/opt/pkp/runtime/AppRun'"
tar -tzf "$prefix-runtime-sources.tar.gz" | grep -E '^\./distro/.*\.dsc$'
tar -tzf "$prefix-runtime-sources.tar.gz" | grep -F './apt-metadata/'
