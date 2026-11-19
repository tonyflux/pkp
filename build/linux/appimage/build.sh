#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../../.."
: "${VERSION:?Run through build/linux/build.sh}"
: "${LINUX_STAGE:?Run through build/linux/build.sh}"
arch=$(go env GOARCH)
work="$PWD/bin/appimage-$arch"
appdir="$work/PKP.AppDir"
sources="$work/runtime-sources"
tools="$work/tools"
mkdir -p "$tools"
# Keep apt-verified archives outside staging; interrupted builds can reuse them.
mkdir -p "$PWD/bin/appimage-source-cache/distro"
if [ -d "$sources/distro" ]; then
  cp -an "$sources/distro/." "$PWD/bin/appimage-source-cache/distro/"
fi
rm -rf "$appdir" "$sources"
mkdir -p "$appdir" "$sources"
case "$arch" in
  amd64) image_arch=x86_64; tool_id=184558942; tool_sha=46fdd785094c7f6e545b61afcfb0f3d98d8eab243f644b4b17698c01d06083d1 ;;
  arm64) image_arch=aarch64; tool_id=184558934; tool_sha=04f45ea45b5aa07bb2b071aed9dbf7a5185d3953b11b47358c1311f11ea94a96 ;;
  *) exit 1 ;;
esac
fetch() {
  local url=$1 sha=$2 file=$3
  if ! echo "$sha  $file" | sha256sum -c --status 2>/dev/null; then
    curl --fail --location --retry 3 -H 'Accept: application/octet-stream' "$url" -o "$file"
    echo "$sha  $file" | sha256sum -c -
  fi
}
# appimagetool 1.9.0, immutable asset IDs and checked-in SHA-256, no continuous downloads.
fetch "https://api.github.com/repos/AppImage/appimagetool/releases/assets/$tool_id" "$tool_sha" "$tools/appimagetool"
chmod +x "$tools/appimagetool"
runtime_commit=fedfad51acc2860b2916a1eef32ba4a500ff31ef
fetch "https://codeload.github.com/AppImage/type2-runtime/legacy.tar.gz/$runtime_commit" a1601c596b81768626afca9abecf0137092d2d7b281bb6c4b9067d2c79e44a21 "$tools/type2-runtime.tar.gz"
tar -xf "$tools/type2-runtime.tar.gz" -C "$sources"
runtime=$(find "$sources" -maxdepth 1 -type d -name 'AppImage-type2-runtime-*')
patch -p1 -d "$runtime" < build/linux/appimage/runtime-private-extraction.patch
# Rebuild the runtime against known distro static libraries instead of shipping
# an opaque upstream runtime with untracked bundled library source versions.
mkdir -p "$sources/runtime-build"
(cd "$sources/runtime-build" && apt-get source "$(dpkg-query -W -f='${source:Package}=${source:Version}' libsquashfuse-dev)")
squash=$(find "$sources/runtime-build" -maxdepth 1 -type d -name 'squashfuse-*')
mkdir -p "$sources/runtime-include/squashfuse"
cp "$squash/fuseprivate.h" "$sources/runtime-include/squashfuse/"
printf '%s\n' "$runtime_commit" > "$runtime/src/runtime/version"
(cd "$runtime/src/runtime" && cc -std=gnu99 -Os -D_FILE_OFFSET_BITS=64 "-DGIT_COMMIT=\"$runtime_commit\"" \
  -I"$sources/runtime-include" $(pkg-config --cflags fuse3 squashfuse_ll squashfuse) -T data_sections.ld \
  -ffunction-sections -fdata-sections -Wl,--gc-sections -static-pie runtime.c \
  -Wl,--start-group $(pkg-config --static --libs squashfuse_ll squashfuse fuse3) \
  -lz -lzstd -llzma -llz4 -llzo2 -Wl,--end-group -o "$tools/runtime")
strip "$tools/runtime"
printf 'AI\002' | dd of="$tools/runtime" bs=1 seek=8 count=3 conv=notrunc status=none
python3 build/linux/appimage/bundle_test.py
python3 build/linux/appimage/bundle.py "$appdir"
cp -a "$LINUX_STAGE/usr/." "$appdir/usr/"
ln -s usr/lib "$appdir/lib"
ln -s usr/bin "$appdir/bin"
ln -s usr/sbin "$appdir/sbin"
mkdir -p "$appdir/usr/lib64"
ln -s usr/lib64 "$appdir/lib64"
mkdir -p "$appdir/host-usr" "$appdir/usr/lib/pkp/Helpers" "$appdir/usr/share/applications" "$appdir/usr/share/icons/hicolor/512x512/apps"
mkdir -p "$appdir/usr/local/share/fonts/host-system" "$appdir/usr/local/share/fonts/host-local"
cp /usr/bin/{ffmpeg,ffprobe} "$appdir/usr/lib/pkp/Helpers/"
cp build/linux/desktop "$appdir/pkp.desktop"
cp build/appicon.png "$appdir/pkp.png"
cp build/linux/desktop "$appdir/usr/share/applications/pkp.desktop"
cp build/appicon.png "$appdir/usr/share/icons/hicolor/512x512/apps/pkp.png"
cp build/linux/appimage/{AppRun,install-apparmor.sh} "$appdir/"
chmod +x "$appdir/AppRun" "$appdir/install-apparmor.sh"
triplet=$(gcc -dumpmachine)
printf '%s\n' "$triplet" > "$appdir/.triplet"
loader=$(readelf -l "$appdir/usr/lib/pkp/pkp" | awk '/interpreter:/ {gsub(/\]/, "", $NF); print $NF}')
printf '%s\n' "$loader" > "$appdir/.loader"
mkdir -p "$appdir$(dirname "$loader")"
cp -L "$loader" "$appdir$loader"
if [ "$(cd "$appdir$(dirname "$loader")" && pwd -P)" = "$(cd "$appdir/usr/lib64" && pwd -P)" ]; then
  mkdir -p "$appdir/usr/lib"
  cp -L "$loader" "$appdir/usr/lib/$(basename "$loader")"
fi
# Portal opener talks to the host session bus; host executables are never run
# against AppImage libraries. A desktop portal is a host service, not bundled.
cc -Os -o "$appdir/usr/bin/xdg-open" build/linux/appimage/portal-open.c $(pkg-config --cflags --libs gio-unix-2.0)
cc -Os -o "$work/portal-open-test" build/linux/appimage/portal-open-test.c $(pkg-config --cflags --libs gio-unix-2.0)
"$work/portal-open-test"
for lang in zh-cn ja ko; do
  fc-list ":lang=$lang" file | grep '/usr/share/fonts/opentype/noto/' > /dev/null && \
    find "$appdir/usr/share/fonts/opentype/noto" -type f -print -quit | grep . > /dev/null || {
    echo "Missing bundled Noto CJK font for $lang" >&2; exit 1;
  }
done
glib-compile-schemas "$appdir/usr/share/glib-2.0/schemas"
cp "$runtime/LICENSE" "$appdir/usr/share/licenses/pkp/APPIMAGE_RUNTIME_LICENSE"
for package in libfuse3-3 libsquashfuse0 zlib1g libzstd1 liblzma5 liblz4-1 liblzo2-2 libc6 "$(dpkg-query -S "$(gcc -print-libgcc-file-name)" | cut -d: -f1)"; do
  cp -a "/usr/share/doc/$package" "$appdir/usr/share/doc/"
done
# The pixbuf cache uses /usr paths generated by bundle.py, never build paths.
git rev-parse HEAD > "$appdir/runtime-source-commit" 2>/dev/null || :
bash build/linux/appimage/sources.sh "$appdir" "$sources"
cat "$appdir/runtime-source-commit" "$appdir/runtime-packages.tsv" "$sources/source-packages.tsv" build/linux/appimage/build.sh build/linux/appimage/sources.sh build/linux/appimage/bundle.py build/linux/appimage/runtime-private-extraction.patch build/docker/Dockerfile.linux | sha256sum | cut -d' ' -f1 > "$appdir/runtime-build-id"
cp "$appdir/runtime-source-commit" "$appdir/runtime-build-id" "$sources/"
cp build/linux/appimage/{portal-open.c,install-apparmor.sh,runtime-private-extraction.patch} "$sources/"
tar --sort=name --mtime="@${SOURCE_DATE_EPOCH:-0}" --owner=0 --group=0 --numeric-owner \
  -C "$sources" -cf - . | gzip -n > "bin/PKP-$VERSION-linux-$arch-runtime-sources.tar.gz"
source_archive="bin/PKP-$VERSION-linux-$arch-runtime-sources.tar.gz"
if [ "$(stat -c %s "$source_archive")" -gt 2147483648 ]; then
  echo "Exact runtime sources exceed the signed update/GitHub 2 GiB attachment limit: $source_archive" >&2
  exit 1
fi
find "$appdir" -exec touch -h -d "@${SOURCE_DATE_EPOCH:-0}" {} +
env -u SOURCE_DATE_EPOCH ARCH=$image_arch "$tools/appimagetool" --appimage-extract-and-run \
  --mksquashfs-opt -mkfs-time --mksquashfs-opt "${SOURCE_DATE_EPOCH:-0}" \
  --runtime-file "$tools/runtime" --no-appstream "$appdir" "bin/PKP-$VERSION-linux-$arch.AppImage"
