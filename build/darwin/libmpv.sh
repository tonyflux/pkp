#!/bin/bash
# Builds libmpv and every library it loads as universal (arm64 + x86_64)
# dylibs for macOS 12+, from the sources pinned in build/libmpv-common.sh,
# into build/darwin/libmpv/{include,lib,bin,licenses}, including ffmpeg/ffprobe.
# `make app-go` copies lib/ to Contents/Frameworks, bin/ to Contents/Helpers,
# and licenses/ to Resources, so the
# app runs without Homebrew. Versions follow the Homebrew formulae PKP was
# developed against. The build is LGPL (see build/libmpv-common.sh).
#
# Needs Xcode, cmake and python3 >= 3.10 (meson). Downloads, build tools and
# per-arch builds live in ~/Library/Caches/pkp/libmpv: outside the
# iCloud-synced checkout, and no path with spaces reaches a configure script.
# Editing this file or build/libmpv-common.sh starts a fresh build (~5 min on
# a 14-core M-series Mac); an interrupted one resumes.
set -euo pipefail

MACOS_MIN=12.0
ARCHS="arm64 x86_64"

. "$(cd "$(dirname "$0")" && pwd)/../libmpv-common.sh"

out=$(cd "$(dirname "$0")" && pwd)/libmpv
work=$cache/build-$key
tools=$work/tools

[ "$(cat "$out/.key" 2>/dev/null)" = "$key" ] && exit 0

check_host
export PATH=$tools/bin:$PATH MACOSX_DEPLOYMENT_TARGET=$MACOS_MIN

# Per-arch state: the prefix every library installs into, and a meson cross
# file whose compilers carry the arch and the deployment target.
setup_arch() {
	prefix=$work/$arch/prefix
	src=$work/$arch/src
	export PKG_CONFIG=$tools/bin/pkgconf PKG_CONFIG_LIBDIR=$prefix/lib/pkgconfig
	local family=$arch cc="'-arch', '$arch', '-mmacosx-version-min=$MACOS_MIN'"
	[ "$arch" = arm64 ] && family=aarch64
	mkdir -p "$prefix" "$src"
	cat >"$work/$arch/cross.ini" <<EOF
[binaries]
c = ['clang', $cc]
cpp = ['clang++', $cc]
objc = ['clang', $cc]
objcpp = ['clang++', $cc]
ar = 'ar'
strip = 'strip'
pkg-config = '$tools/bin/pkgconf'
nasm = '$tools/bin/nasm'

[properties]
pkg_config_libdir = '$prefix/lib/pkgconfig'
needs_exe_wrapper = false

[host_machine]
system = 'darwin'
subsystem = 'macos'
kernel = 'xnu'
cpu_family = '$family'
cpu = '$arch'
endian = 'little'
EOF
}

# meson_build dir option...: --wrap-mode nodownload keeps meson from pulling
# unpinned subprojects when a dependency is missing.
meson_build() {
	local dir=$1
	shift
	meson setup "$dir/build" "$dir" --cross-file "$work/$arch/cross.ini" --prefix "$prefix" \
		--libdir lib --buildtype release --default-library shared --strip --wrap-mode nodownload "$@"
	meson compile -C "$dir/build"
	meson install -C "$dir/build"
}

build_dav1d() {
	unpack dav1d "$src/dav1d"
	meson_build "$src/dav1d" -Denable_tools=false -Denable_examples=false -Denable_tests=false -Denable_docs=false
}

# --disable-autodetect makes ffmpeg assume libc has iconv, which macOS's does
# not; mpv recodes subtitles itself (uchardet + iconv), so ffmpeg goes without.
build_ffmpeg() {
	local cc="-arch $arch -mmacosx-version-min=$MACOS_MIN"
	unpack ffmpeg "$src/ffmpeg"
	cd "$src/ffmpeg"
	./configure --prefix="$prefix" --install-name-dir=@rpath \
		--enable-cross-compile --target-os=darwin --arch="$arch" \
		--cc="clang $cc" --cxx="clang++ $cc" --objcc="clang $cc" \
		--x86asmexe="$tools/bin/nasm" --pkg-config="$tools/bin/pkgconf" \
		--enable-shared --disable-static --enable-ffmpeg --enable-ffprobe --disable-ffplay --disable-doc --disable-debug --disable-avdevice \
		--disable-autodetect --disable-iconv --enable-pthreads --enable-zlib --enable-bzlib \
		--disable-gpl --disable-nonfree --enable-videotoolbox --enable-audiotoolbox --enable-libdav1d
	make -j"$jobs"
	make install
}

build_freetype() {
	unpack freetype "$src/freetype"
	meson_build "$src/freetype" -Dbrotli=disabled -Dbzip2=disabled -Dharfbuzz=disabled -Dpng=disabled \
		-Dzlib=disabled -Dtests=disabled
}

build_fribidi() {
	unpack fribidi "$src/fribidi"
	meson_build "$src/fribidi" -Ddocs=false -Dbin=false -Dtests=false
}

build_harfbuzz() {
	unpack harfbuzz "$src/harfbuzz"
	meson_build "$src/harfbuzz" -Dfreetype=enabled -Dglib=disabled -Dgobject=disabled -Dcairo=disabled \
		-Dchafa=disabled -Dpng=disabled -Dzlib=disabled -Dicu=disabled -Draster=disabled -Dvector=disabled \
		-Dgpu=disabled -Dgpu_demo=disabled -Dsubset=disabled -Dtests=disabled -Dintrospection=disabled \
		-Ddocs=disabled -Dutilities=disabled
}

build_libass() {
	unpack libass "$src/libass"
	meson_build "$src/libass" -Dfontconfig=disabled -Dcoretext=enabled -Ddirectwrite=disabled \
		-Dlibunibreak=disabled -Dtest=disabled -Dcompare=disabled -Dprofile=disabled -Dfuzz=disabled \
		-Dcheckasm=disabled
}

build_uchardet() {
	unpack uchardet "$src/uchardet"
	cmake -S "$src/uchardet" -B "$src/uchardet/build" -DCMAKE_BUILD_TYPE=Release \
		-DCMAKE_INSTALL_PREFIX="$prefix" -DCMAKE_INSTALL_LIBDIR=lib -DCMAKE_OSX_ARCHITECTURES="$arch" \
		-DCMAKE_OSX_DEPLOYMENT_TARGET="$MACOS_MIN" -DCMAKE_POLICY_VERSION_MINIMUM=3.5 \
		-DBUILD_BINARY=OFF -DBUILD_STATIC=OFF
	cmake --build "$src/uchardet/build" -j "$jobs"
	cmake --install "$src/uchardet/build" --strip
}

build_libplacebo() {
	local dir=$src/libplacebo
	unpack_libplacebo "$dir"
	meson_build "$dir" -Dvulkan=disabled -Dopengl=enabled -Dd3d11=disabled -Dglslang=disabled \
		-Dshaderc=disabled -Dlcms=disabled -Dlibdovi=disabled -Dunwind=disabled -Dxxhash=disabled \
		-Ddemos=false -Dtests=false -Dbench=false -Dfuzz=false
}

# What PKP uses: the OpenGL render API (vo=libmpv into a CAOpenGLLayer),
# VideoToolbox decoding with GL interop, CoreAudio, libass subtitles with
# uchardet charset detection. No scripting, no CLI. GL interop needs cocoa,
# which needs Swift; mpv runs swiftc without a target, so it gets one here.
# -Dgpl=false loses nothing here: what it rules out (X11, DVD, CD, DVB, JACK,
# OSS, caca, the Direct3D 9 output) is off or not on macOS anyway.
build_mpv() {
	local patch
	unpack mpv "$src/mpv"
	patch=$(fetch mpv-patch)
	patch -d "$src/mpv" -p1 <"$patch"
	meson_build "$src/mpv" -Dlibmpv=true -Dcplayer=false -Dgpl=false -Dbuild-date=false -Dmanpage-build=disabled \
		-Dgl=enabled -Dplain-gl=enabled -Dcocoa=enabled -Dgl-cocoa=enabled -Dvideotoolbox-gl=enabled \
		-Dcoreaudio=enabled -Duchardet=enabled -Diconv=enabled -Dzlib=enabled \
		-Dswift-build=enabled "-Dswift-flags=-target $arch-apple-macos$MACOS_MIN" \
		-Dmacos-cocoa-cb=disabled -Dmacos-media-player=disabled -Dmacos-touchbar=disabled \
		-Dvulkan=disabled -Dvideotoolbox-pl=disabled -Dshaderc=disabled -Dspirv-cross=disabled \
		-Dlua=disabled -Djavascript=disabled -Dcplugins=disabled -Dlibavdevice=disabled \
		-Dlibarchive=disabled -Dlibbluray=disabled -Ddvdnav=disabled -Dcdda=disabled -Ddvbin=disabled \
		-Drubberband=disabled -Dvapoursynth=disabled -Dzimg=disabled -Djpeg=disabled -Dlcms2=disabled \
		-Dsixel=disabled -Dcaca=disabled
}

# Every library names itself and its siblings @rpath/<the name others link>
# and carries no rpath (mpv's Swift build adds the toolchain's): the app's own
# rpath finds them all. The name others link is the one each build gave
# itself: FFmpeg's real file carries the full version (libavformat.63.1.102)
# while everything, libmpv included, loads libavformat.63.dylib.
build_names() {
	local f dep args
	for f in "$prefix"/lib/*.dylib; do
		[ -L "$f" ] && continue
		args="-id @rpath/$(basename "$(otool -D "$f" | tail -1)")"
		for dep in $(otool -L "$f" | awk 'NR > 2 {print $1}'); do
			case $dep in "$prefix"/*) args="$args -change $dep @rpath/$(basename "$dep")" ;; esac
		done
		for dep in $(otool -l "$f" | awk '/LC_RPATH/ {getline; getline; print $2}'); do
			args="$args -delete_rpath $dep"
		done
		install_name_tool $args "$f"
		# That voided the signature, and arm64 refuses unsigned code. lipo keeps
		# each slice's signature, so the universal file needs no other.
		codesign --force --sign - "$f"
	done
	for f in "$prefix/bin/ffmpeg" "$prefix/bin/ffprobe"; do
		for dep in $(otool -L "$f" | awk 'NR > 1 {print $1}'); do
			case $dep in "$prefix"/*) install_name_tool -change "$dep" "@rpath/$(basename "$dep")" "$f" ;; esac
		done
		for dep in $(otool -l "$f" | awk '/LC_RPATH/ {getline; getline; print $2}'); do
			install_name_tool -delete_rpath "$dep" "$f"
		done
		install_name_tool -add_rpath @executable_path/../Frameworks -add_rpath @executable_path/../lib "$f"
		codesign --force --sign - "$f"
	done
}

arch=host
run tools build_tools
for arch in $ARCHS; do
	setup_arch
	for pkg in dav1d ffmpeg freetype fribidi harfbuzz libass uchardet libplacebo mpv names; do
		run "$pkg" "build_$pkg"
	done
done

# Merge libmpv and everything it loads over @rpath into universal dylibs.
stage=$work/out
rm -rf "$stage"
mkdir -p "$stage/lib" "$stage/include" "$stage/bin"
libs=libmpv.2.dylib
for program in ffmpeg ffprobe; do
	for dep in $(otool -L "$work/arm64/prefix/bin/$program" | awk 'NR > 1 && sub("^@rpath/", "", $1) {print $1}'); do
		case " $libs " in *" $dep "*) ;; *) libs="$libs $dep" ;; esac
	done
	lipo -create "$work/arm64/prefix/bin/$program" "$work/x86_64/prefix/bin/$program" -output "$stage/bin/$program"
	codesign --force --sign - "$stage/bin/$program"
	lipo "$stage/bin/$program" -verify_arch $ARCHS || die "$program lacks an arch"
	codesign --verify "$stage/bin/$program" || die "$program is not validly signed"
	for a in $ARCHS; do
		for dep in $(otool -arch "$a" -L "$stage/bin/$program" | awk 'NR > 1 {print $1}'); do
			case $dep in @rpath/* | /usr/lib/* | /System/Library/*) ;; *) die "$program ($a) loads $dep" ;; esac
		done
	done
done
queue=$libs
while [ -n "$queue" ]; do
	set -- $queue
	lib=$1
	shift
	queue="$*"
	for dep in $(otool -L "$work/arm64/prefix/lib/$lib" | awk 'NR > 2 && sub("^@rpath/", "", $1) {print $1}'); do
		case " $libs " in *" $dep "*) ;; *) libs="$libs $dep" queue="$queue $dep" ;; esac
	done
done
for lib in $libs; do
	lipo -create "$work/arm64/prefix/lib/$lib" "$work/x86_64/prefix/lib/$lib" -output "$stage/lib/$lib"
done
ln -s libmpv.2.dylib "$stage/lib/libmpv.dylib"
cp -R "$work/arm64/prefix/include/mpv" "$stage/include/"
# internal/audiotags reads metadata with FFmpeg itself: its headers (the same
# for both archs) and unversioned names for the linker. The binary records
# the versioned @rpath names, which libmpv already brings into the bundle.
for name in avcodec avformat avutil swresample; do
	cp -R "$work/arm64/prefix/include/lib$name" "$stage/include/"
	ln -s "$(cd "$stage/lib" && ls lib$name.*.dylib)" "$stage/lib/lib$name.dylib"
done

# The bundle contract: both archs, macOS $MACOS_MIN, and nothing loaded from
# outside the bundle or the OS.
for lib in $libs; do
	f=$stage/lib/$lib
	lipo "$f" -verify_arch $ARCHS || die "$lib lacks an arch"
	codesign --verify "$f" || die "$lib is not validly signed"
	for a in $ARCHS; do
		[ "$(otool -arch "$a" -D "$f" | tail -1)" = "@rpath/$lib" ] || die "$lib ($a) does not name itself @rpath/$lib"
		[ "$(otool -arch "$a" -l "$f" | awk '/ minos / {print $2; exit}')" = "$MACOS_MIN" ] || die "$lib ($a) is not built for macOS $MACOS_MIN"
		for dep in $(otool -arch "$a" -L "$f" | awk 'NR > 2 {print $1}'); do
			case $dep in @rpath/* | /usr/lib/* | /System/Library/*) ;; *) die "$lib ($a) loads $dep" ;; esac
		done
		for dep in $(otool -arch "$a" -l "$f" | awk '/LC_RPATH/ {getline; getline; print $2}'); do
			die "$lib ($a) carries rpath $dep"
		done
	done
done
for a in $ARCHS; do
	arch "-$a" "$stage/bin/ffprobe" -version >/dev/null
	encoders=$(arch "-$a" "$stage/bin/ffmpeg" -hide_banner -encoders 2>&1)
	grep -q ' h264_videotoolbox ' <<<"$encoders" || die "missing LGPL H.264 encoder ($a)"
	grep -q ' aac ' <<<"$encoders" || die "missing native AAC encoder ($a)"
done
install_licenses "$stage/licenses"

echo "$key" >"$stage/.key"
rm -rf "$out.tmp"
cp -R "$stage" "$out.tmp"
rm -rf "$out"
mv "$out.tmp" "$out"
find "$cache" -mindepth 1 -maxdepth 1 -name 'build-*' ! -name "build-$key" -exec rm -rf {} +
rm -rf "$work/arm64/src" "$work/x86_64/src"
echo "libmpv: $(echo $libs | wc -w | tr -d ' ') universal dylibs in $out" >&2
