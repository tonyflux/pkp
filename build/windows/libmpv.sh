#!/bin/bash
# Builds libmpv-2.dll for Windows x64 and ARM64 from the sources pinned in
# build/libmpv-common.sh, with llvm-mingw, into
# build/windows/libmpv/{amd64,arm64}/libmpv-2.dll and licenses/. `make
# windows-installer` installs the DLL of the machine's arch beside pkp.exe,
# with the licenses. The build is LGPL (see build/libmpv-common.sh).
#
# One self-contained DLL per arch: every library is built static and linked
# into it, the C++ runtime and winpthreads included, so it loads nothing but
# Windows' own DLLs (checked at the end). What PKP uses: mpv's own window
# inside the surface (--wid), drawn by gpu-next / gpu over Direct3D 11
# (shaderc + SPIRV-Cross), with OpenGL over WGL as mpv's fallback; D3D11VA,
# DXVA2, D3D12VA and NVDEC decoding; WASAPI; libass with DirectWrite;
# uchardet + libiconv to recode subtitles; Schannel for TLS. No scripting,
# no CLI, no Vulkan: a Vulkan build needs vulkan-1.dll, which machines
# without a Vulkan driver lack, and then the whole DLL fails to load.
#
# Runs on macOS: the toolchain pin is llvm-mingw's macOS build. Needs cmake
# and python3 >= 3.10. Shares the download cache with the macOS build; its
# own work lives in ~/Library/Caches/pkp/libmpv/win-<key> (~10 min on a
# 14-core M-series Mac); an interrupted build resumes.
set -euo pipefail

ARCHS="x86_64 aarch64"

here=$(cd "$(dirname "$0")" && pwd)
. "$here/../libmpv-common.sh"

out=$here/libmpv
work=$cache/win-$key
tools=$work/tools
toolchain=$work/llvm-mingw

[ "$(cat "$out/.key" 2>/dev/null)" = "$key" ] && exit 0

check_host
export PATH=$tools/bin:$toolchain/bin:$PATH

# The DLL carries LLVM's runtimes (libc++, libunwind, compiler-rt: the
# toolchain's LICENSE.TXT) and mingw-w64's (CRT startup, winpthreads).
build_toolchain() {
	unpack llvm-mingw "$toolchain"
	cp "$toolchain"/aarch64-w64-mingw32/share/mingw32/COPYING.{MinGW-w64-runtime,winpthreads}.txt \
		"$work/licenses/llvm-mingw/"
}

# Per-arch state: the prefix every library installs into, and the meson
# cross file and CMake toolchain file that target it.
setup_arch() {
	triple=$arch-w64-mingw32
	prefix=$work/$arch/prefix
	src=$work/$arch/src
	export PKG_CONFIG=$tools/bin/pkgconf PKG_CONFIG_LIBDIR=$prefix/lib/pkgconfig
	mkdir -p "$prefix/include" "$prefix/lib/pkgconfig" "$src"
	local bin=$toolchain/bin
	cat >"$work/$arch/cross.ini" <<EOF
[binaries]
c = '$bin/$triple-clang'
cpp = '$bin/$triple-clang++'
ar = '$bin/llvm-ar'
strip = '$bin/llvm-strip'
windres = '$bin/$triple-windres'
dlltool = '$bin/llvm-dlltool'
pkg-config = '$tools/bin/pkgconf'
nasm = '$tools/bin/nasm'

[built-in options]
c_args = ['-I$prefix/include']
cpp_args = ['-I$prefix/include']
c_link_args = ['-L$prefix/lib']
cpp_link_args = ['-L$prefix/lib']

[properties]
pkg_config_libdir = '$prefix/lib/pkgconfig'
needs_exe_wrapper = true

[host_machine]
system = 'windows'
cpu_family = '$arch'
cpu = '$arch'
endian = 'little'
EOF
	cat >"$work/$arch/toolchain.cmake" <<EOF
set(CMAKE_SYSTEM_NAME Windows)
set(CMAKE_SYSTEM_PROCESSOR $arch)
set(CMAKE_C_COMPILER $bin/$triple-clang)
set(CMAKE_CXX_COMPILER $bin/$triple-clang++)
set(CMAKE_RC_COMPILER $bin/$triple-windres)
set(CMAKE_AR $bin/llvm-ar)
set(CMAKE_RANLIB $bin/llvm-ranlib)
set(CMAKE_FIND_ROOT_PATH $prefix)
set(CMAKE_FIND_ROOT_PATH_MODE_PROGRAM NEVER)
set(CMAKE_FIND_ROOT_PATH_MODE_LIBRARY ONLY)
set(CMAKE_FIND_ROOT_PATH_MODE_INCLUDE ONLY)
set(CMAKE_FIND_ROOT_PATH_MODE_PACKAGE ONLY)
EOF
}

# meson_build dir static|shared option...: --wrap-mode nodownload keeps meson
# from pulling unpinned subprojects when a dependency is missing.
meson_build() {
	local dir=$1 kind=$2
	shift 2
	meson setup "$dir/build" "$dir" --cross-file "$work/$arch/cross.ini" --prefix "$prefix" \
		--libdir lib --buildtype release --default-library "$kind" -Dprefer_static=true --strip \
		--wrap-mode nodownload "$@"
	meson compile -C "$dir/build"
	meson install -C "$dir/build"
}

cmake_build() {
	local dir=$1
	shift
	cmake -S "$dir" -B "$dir/build" -G Ninja -DCMAKE_TOOLCHAIN_FILE="$work/$arch/toolchain.cmake" \
		-DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_PREFIX="$prefix" -DCMAKE_INSTALL_LIBDIR=lib \
		-DBUILD_SHARED_LIBS=OFF -DCMAKE_POLICY_VERSION_MINIMUM=3.5 "$@"
	cmake --build "$dir/build" -j "$jobs"
	cmake --install "$dir/build"
}

# write_pc name version libs: for libraries whose build installs none.
write_pc() {
	cat >"$prefix/lib/pkgconfig/$1.pc" <<EOF
prefix=$prefix
Name: $1
Description: $1
Version: $2
Libs: -L\${prefix}/lib $3
Cflags: -I\${prefix}/include
EOF
}

# zlib's CMake build names its Windows static library libzs.a; its plain
# Makefile builds the libz.a FFmpeg and mpv look for.
build_zlib() {
	unpack zlib "$src/zlib"
	cd "$src/zlib"
	make -f win32/Makefile.gcc -j"$jobs" CC="$triple-clang" AR=llvm-ar libz.a
	cp zlib.h zconf.h "$prefix/include/"
	cp libz.a "$prefix/lib/"
	write_pc zlib 1.3.2 -lz
}

# bzip2's Makefile builds its tools too and runs them; only the library is needed.
build_bzip2() {
	local f
	unpack bzip2 "$src/bzip2"
	cd "$src/bzip2"
	for f in blocksort huffman crctable randtable compress decompress bzlib; do
		"$triple-clang" -O2 -c "$f.c" -o "$f.o"
	done
	llvm-ar rcs "$prefix/lib/libbz2.a" blocksort.o huffman.o crctable.o randtable.o compress.o decompress.o bzlib.o
	cp bzlib.h "$prefix/include/"
}

# Into the toolchain's own sysroot, not the prefix: meson looks for iconv
# with find_library, which in a static build searches only the compiler's
# library directories, not -L.
build_libiconv() {
	unpack libiconv "$src/libiconv"
	cd "$src/libiconv"
	./configure --host="$triple" --prefix="$toolchain/$triple" --enable-static --disable-shared \
		--disable-nls CC="$triple-clang" AR=llvm-ar RANLIB=llvm-ranlib
	make -j"$jobs"
	make install
}

# Its MIT notices sit at the top of each header.
build_ffnvcodec() {
	local f
	unpack nv-codec-headers "$src/nv-codec-headers"
	make -C "$src/nv-codec-headers" PREFIX="$prefix" install
	mkdir -p "$work/licenses/nv-codec-headers"
	for f in "$src/nv-codec-headers"/include/ffnvcodec/*.h; do
		printf '%s:\n' "$(basename "$f")"
		sed -n '1,/\*\//p' "$f"
	done >"$work/licenses/nv-codec-headers/LICENSE"
}

build_dav1d() {
	unpack dav1d "$src/dav1d"
	meson_build "$src/dav1d" static -Denable_tools=false -Denable_examples=false -Denable_tests=false \
		-Denable_docs=false
}

# mpv recodes subtitles itself (uchardet + iconv), so ffmpeg goes without
# iconv, as on macOS. gfxcapture (a screen-capture source, ffmpeg's only C++)
# includes <version>, which finds ffmpeg's own VERSION file first on a
# case-insensitive file system.
build_ffmpeg() {
	unpack ffmpeg "$src/ffmpeg"
	cd "$src/ffmpeg"
	./configure --prefix="$prefix" --enable-cross-compile --target-os=mingw32 --arch="$arch" \
		--cc="$triple-clang" --cxx="$triple-clang++" --ar=llvm-ar --ranlib=llvm-ranlib --nm=llvm-nm \
		--strip=llvm-strip --windres="$triple-windres" \
		--x86asmexe="$tools/bin/nasm" --pkg-config="$tools/bin/pkgconf" --pkg-config-flags=--static \
		--extra-cflags="-I$prefix/include" --extra-ldflags="-L$prefix/lib -static" \
		--enable-static --disable-shared --enable-ffmpeg --enable-ffprobe --disable-ffplay --disable-doc --disable-debug --disable-avdevice \
		--disable-autodetect --disable-iconv --enable-w32threads --enable-zlib --enable-bzlib \
		--disable-gpl --disable-nonfree --enable-mediafoundation --enable-d3d11va --enable-dxva2 --enable-d3d12va --enable-ffnvcodec --enable-nvdec \
		--enable-schannel --enable-libdav1d --disable-filter=gfxcapture
	make -j"$jobs"
	make install
}

build_freetype() {
	unpack freetype "$src/freetype"
	meson_build "$src/freetype" static -Dbrotli=disabled -Dbzip2=disabled -Dharfbuzz=disabled \
		-Dpng=disabled -Dzlib=disabled -Dtests=disabled
}

build_fribidi() {
	unpack fribidi "$src/fribidi"
	meson_build "$src/fribidi" static -Ddocs=false -Dbin=false -Dtests=false
}

build_harfbuzz() {
	unpack harfbuzz "$src/harfbuzz"
	meson_build "$src/harfbuzz" static -Dfreetype=enabled -Dglib=disabled -Dgobject=disabled \
		-Dcairo=disabled -Dchafa=disabled -Dpng=disabled -Dzlib=disabled -Dicu=disabled -Draster=disabled \
		-Dvector=disabled -Dgpu=disabled -Dgpu_demo=disabled -Dsubset=disabled -Dtests=disabled \
		-Dintrospection=disabled -Ddocs=disabled -Dutilities=disabled
}

build_libass() {
	unpack libass "$src/libass"
	meson_build "$src/libass" static -Dfontconfig=disabled -Dcoretext=disabled -Ddirectwrite=enabled \
		-Dlibunibreak=disabled -Dtest=disabled -Dcompare=disabled -Dprofile=disabled -Dfuzz=disabled \
		-Dcheckasm=disabled
}

build_uchardet() {
	unpack uchardet "$src/uchardet"
	cmake_build "$src/uchardet" -DBUILD_BINARY=OFF
}

# shaderc builds glslang and SPIRV-Tools from its third_party directory.
# mpv and libplacebo ask for "shaderc", the shared library's name; the
# combined static library stands in for it.
build_shaderc() {
	local dir=$src/shaderc
	unpack shaderc "$dir"
	unpack spirv-headers "$dir/third_party/spirv-headers"
	unpack spirv-tools "$dir/third_party/spirv-tools"
	unpack glslang "$dir/third_party/glslang"
	cmake_build "$dir" -DSHADERC_SKIP_TESTS=ON -DSHADERC_SKIP_EXAMPLES=ON -DSHADERC_SKIP_EXECUTABLES=ON \
		-DSHADERC_SKIP_COPYRIGHT_CHECK=ON -DSHADERC_ENABLE_WERROR_COMPILE=OFF -DSHADERC_ENABLE_HLSL=OFF \
		-DSPIRV_SKIP_EXECUTABLES=ON -DSPIRV_WERROR=OFF -DENABLE_GLSLANG_BINARIES=OFF \
		-DPython3_EXECUTABLE="$python"
	cp "$prefix/lib/pkgconfig/shaderc_combined.pc" "$prefix/lib/pkgconfig/shaderc.pc"
}

# The same stand-in for "spirv-cross-c-shared": the static C API library,
# with the backends it calls.
build_spirv_cross() {
	unpack spirv-cross "$src/spirv-cross"
	cmake_build "$src/spirv-cross" -DSPIRV_CROSS_SHARED=OFF -DSPIRV_CROSS_STATIC=ON -DSPIRV_CROSS_CLI=OFF \
		-DSPIRV_CROSS_ENABLE_TESTS=OFF -DSPIRV_CROSS_ENABLE_MSL=OFF -DSPIRV_CROSS_ENABLE_CPP=OFF
	write_pc spirv-cross-c-shared "$(awk '/^Version:/ {print $2}' "$prefix/lib/pkgconfig/spirv-cross-c.pc")" \
		"-lspirv-cross-c -lspirv-cross-hlsl -lspirv-cross-glsl -lspirv-cross-reflect -lspirv-cross-util -lspirv-cross-core -lc++"
	sed -i '' 's|^Cflags:.*|Cflags: -I${prefix}/include/spirv_cross|' "$prefix/lib/pkgconfig/spirv-cross-c-shared.pc"
}

build_libplacebo() {
	local dir=$src/libplacebo
	unpack_libplacebo "$dir"
	meson_build "$dir" static -Dvulkan=disabled -Dopengl=enabled -Dd3d11=enabled -Dshaderc=enabled \
		-Dglslang=disabled -Dlcms=disabled -Dlibdovi=disabled -Dunwind=disabled -Dxxhash=disabled \
		-Ddemos=false -Dtests=false -Dbench=false -Dfuzz=false
}

# -Dgpl=false rules out, of what Windows has, only the Direct3D 9 video
# output: mpv's last resort after gpu-next and gpu, which Direct3D 11 (with
# its WARP software rasteriser) and OpenGL already cover. -static links the
# C++ runtime, winpthreads and libunwind into the DLL.
build_mpv() {
	unpack mpv "$src/mpv"
	meson_build "$src/mpv" shared -Dlibmpv=true -Dcplayer=false -Dgpl=false -Dbuild-date=false \
		-Dmanpage-build=disabled -Dhtml-build=disabled -Dpdf-build=disabled \
		"-Dc_link_args=['-L$prefix/lib', '-static']" "-Dcpp_link_args=['-L$prefix/lib', '-static']" \
		-Dd3d11=enabled -Dshaderc=enabled -Dspirv-cross=enabled -Dd3d-hwaccel=enabled -Dd3d9-hwaccel=enabled \
		-Dgl=enabled -Dgl-win32=enabled -Dgl-dxinterop=enabled -Degl-angle=disabled -Dcuda-hwaccel=enabled \
		-Dwasapi=enabled -Dwin32-threads=enabled -Duchardet=enabled -Diconv=enabled -Dzlib=enabled \
		-Dvulkan=disabled -Dwin32-smtc=disabled -Dlua=disabled -Djavascript=disabled -Dcplugins=disabled \
		-Dlibavdevice=disabled -Dlibarchive=disabled -Dlibbluray=disabled -Ddvdnav=disabled -Dcdda=disabled \
		-Drubberband=disabled -Dvapoursynth=disabled -Dzimg=disabled -Djpeg=disabled -Dlcms2=disabled \
		-Dsixel=disabled -Dcaca=disabled -Dsdl2-audio=disabled -Dsdl2-video=disabled -Dsdl2-gamepad=disabled \
		-Dopenal=disabled
}

arch=host
run tools build_tools
run toolchain build_toolchain
for arch in $ARCHS; do
	setup_arch
	for pkg in zlib bzip2 libiconv ffnvcodec dav1d ffmpeg freetype fribidi harfbuzz libass uchardet shaderc \
		spirv_cross libplacebo mpv; do
		run "$pkg" "build_$pkg"
	done
done

stage=$work/out
rm -rf "$stage"
for arch in $ARCHS; do
	case $arch in x86_64) dir=amd64 machine=AMD64 ;; aarch64) dir=arm64 machine=ARM64 ;; esac
	f=$work/$arch/prefix/bin/libmpv-2.dll
	# The DLL contract: the right machine, libmpv's API exported (libplacebo
	# exports its own too: it marks its API dllexport even when static), and
	# nothing imported but Windows' own DLLs, the C runtime being UCRT's.
	grep -q "Machine: IMAGE_FILE_MACHINE_$machine" <<<"$(llvm-readobj --file-headers "$f")" || die "$f is not $machine"
	grep -q "Name: mpv_create$" <<<"$(llvm-readobj --coff-exports "$f")" || die "$f does not export mpv_create"
	for dll in $(llvm-readobj --coff-imports "$f" | awk '/^  Name:/ {print tolower($2)}'); do
		case $dll in
		api-ms-win-crt-* | api-ms-win-core-* | kernel32.dll | ntdll.dll | user32.dll | gdi32.dll | \
			advapi32.dll | ole32.dll | shell32.dll | shlwapi.dll | shcore.dll | uxtheme.dll | dwmapi.dll | \
			imm32.dll | version.dll | avrt.dll | opengl32.dll | ws2_32.dll | bcrypt.dll | crypt32.dll | \
			ncrypt.dll | secur32.dll | oleaut32.dll | mfplat.dll | mf.dll | mfuuid.dll | propsys.dll | strmiids.dll) ;;
		*) die "$f imports $dll" ;;
		esac
	done
	mkdir -p "$stage/$dir"
	cp "$f" "$stage/$dir/"
	for program in ffmpeg ffprobe; do
		f=$work/$arch/prefix/bin/$program.exe
		grep -q "Machine: IMAGE_FILE_MACHINE_$machine" <<<"$(llvm-readobj --file-headers "$f")" || die "$f is not $machine"
		for dll in $(llvm-readobj --coff-imports "$f" | awk '/^  Name:/ {print tolower($2)}'); do
			case $dll in
				api-ms-win-crt-* | api-ms-win-core-* | kernel32.dll | ntdll.dll | user32.dll | gdi32.dll | advapi32.dll | ole32.dll | oleaut32.dll | shell32.dll | shlwapi.dll | ws2_32.dll | bcrypt.dll | crypt32.dll | ncrypt.dll | secur32.dll | mfplat.dll | mf.dll | mfuuid.dll | propsys.dll | strmiids.dll | avrt.dll) ;;
				*) die "$f imports non-bundled DLL $dll" ;;
			esac
		done
		cp "$f" "$stage/$dir/"
	done
done
install_licenses "$stage/licenses"

echo "$key" >"$stage/.key"
rm -rf "$out.tmp"
cp -R "$stage" "$out.tmp"
rm -rf "$out"
mv "$out.tmp" "$out"
find "$cache" -mindepth 1 -maxdepth 1 -name 'win-*' ! -name "win-$key" -exec rm -rf {} +
# The toolchain is a gigabyte; a rerun of the same key unpacks it again.
rm -rf "$work/x86_64/src" "$work/aarch64/src" "$toolchain" "$work/done/host-toolchain"
echo "libmpv: libmpv-2.dll for $(ls "$out" | grep -v licenses | tr '\n' ' ')in $out" >&2
