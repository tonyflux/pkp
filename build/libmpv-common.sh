# Shared by build/darwin/libmpv.sh and build/windows/libmpv.sh, sourced, not
# run: the pinned sources of both, and the steps they build them with. Both
# builds are LGPL: no GPL library goes in, mpv is configured -Dgpl=false and
# FFmpeg without --enable-gpl, so the app can ship libmpv beside a binary of
# its own. build/libmpv-sources.sh packs every pin below (the toolchain
# aside) as the corresponding source for a release.
#
# Downloads live in ~/Library/Caches/pkp/libmpv/src, each build in a
# directory of its own beside it named after the hash of its script and this
# file: editing either starts a fresh build, an interrupted one resumes.

# name version sha256 url. nasm and uchardet come from Debian: their upstream
# hosts refuse plain curl, and Debian's orig tarballs are byte-identical
# (same sha256 as Homebrew's pins). mpv-patch is Homebrew's backport of the
# use-after-free on audio device changes (macOS 26/27), in ao_coreaudio only.
# The SPIR-V stack is the one shaderc v2026.4 pins in its DEPS (glslang and
# SPIRV-Tools there are Vulkan SDK 1.4.363.0, as is SPIRV-Cross here).
PINS="
nasm 3.01 b7324cbe86e767b65f26f467ed8b12ad80e124e3ccb89076855c98e43a9eddd4 https://deb.debian.org/debian/pool/main/n/nasm/nasm_3.01.orig.tar.xz
ninja 1.13.2 974d6b2f4eeefa25625d34da3cb36bdcebe7fbce40f4c16ac0835fd1c0cbae17 https://github.com/ninja-build/ninja/archive/refs/tags/v1.13.2.tar.gz
meson 1.12.1 ab0a6ca09f8ef70c564c8241fb5a23957886a0b53fb58412b5e07eaf07dba743 https://github.com/mesonbuild/meson/releases/download/1.12.1/meson-1.12.1.tar.gz
pkgconf 3.0.8 943f0bc88bcaeceb630b00262e2a8ad5ea4fac3fba72a4267d8b9ec6b35d1194 https://distfiles.ariadne.space/pkgconf/pkgconf-3.0.8.tar.xz
llvm-mingw 20261006 a7a19b11048e89372637d26279cdb7c430ed3cf8c1e224310421312bf986ebf6 https://github.com/mstorsjo/llvm-mingw/releases/download/20261006/llvm-mingw-20261006-ucrt-macos-universal.tar.xz
zlib 1.3.2 d7a0654783a4da529d1bb793b7ad9c3318020af77667bcae35f95d0e42a792f3 https://github.com/madler/zlib/releases/download/v1.3.2/zlib-1.3.2.tar.xz
bzip2 1.0.8 ab5a03176ee106d3f0fa90e381da478ddae405918153cca248e682cd0c4a2269 https://sourceware.org/pub/bzip2/bzip2-1.0.8.tar.gz
libiconv 1.19 88dd96a8c0464eca144fc791ae60cd31cd8ee78321e67397e25fc095c4a19aa6 https://ftp.gnu.org/gnu/libiconv/libiconv-1.19.tar.gz
nv-codec-headers 13.0.19.1 eab9d02d461035a4baded23d4f2e2834e5b17934820b79cea45baaf0c85383eb https://github.com/FFmpeg/nv-codec-headers/releases/download/n13.0.19.1/nv-codec-headers-13.0.19.1.tar.gz
dav1d 1.5.4 2abfb0c89212e6e4733a54e0ae509ec00a5b845a6360946f918806e14aedb011 https://code.videolan.org/videolan/dav1d/-/archive/1.5.4/dav1d-1.5.4.tar.bz2
ffmpeg 9.0.2 8c3850283eb25fa026482078a04051e0be17347b09ef81a0849bec15a96e002e https://ffmpeg.org/releases/ffmpeg-9.0.2.tar.xz
freetype 2.14.3 36bc4f1cc413335368ee656c42afca65c5a3987e8768cc28cf11ba775e785a5f https://downloads.sourceforge.net/project/freetype/freetype2/2.14.3/freetype-2.14.3.tar.xz
fribidi 1.0.17 6949dcde27d41cebad1fd741fcafc36d55a1020d2d872d4a6eb3914caabbada2 https://github.com/fribidi/fribidi/releases/download/v1.0.17/fribidi-1.0.17.tar.xz
harfbuzz 14.6.0 d07a007327277708a2a73ae437887cdbaf282937f6d03ca5467723e9099af586 https://github.com/harfbuzz/harfbuzz/releases/download/14.6.0/harfbuzz-14.6.0.tar.xz
libass 0.17.5 2dca25c0e0c837ddf00b52011b3f82cac1e4ddd3ad018227806b0c2288864acc https://github.com/libass/libass/releases/download/0.17.5/libass-0.17.5.tar.xz
uchardet 0.0.8 e97a60cfc00a1c147a674b097bb1422abd9fa78a2d9ce3f3fdcc2e78a34ac5f0 https://deb.debian.org/debian/pool/main/u/uchardet/uchardet_0.0.8.orig.tar.xz
spirv-headers 04fd3caa 392f4801409aad9c4f1b77745f179952fd6264e4c8bd0fc1bc45dfa6807bf6d0 https://github.com/KhronosGroup/SPIRV-Headers/archive/04fd3caa1e8267e4d95c806cad901181728e1006.tar.gz
spirv-tools 1.4.363.0 e6c83a215538fbfd265ccf398d3877985055df1273dbed73912bd05307664688 https://github.com/KhronosGroup/SPIRV-Tools/archive/refs/tags/vulkan-sdk-1.4.363.0.tar.gz
glslang 1.4.363.0 cda24ec765e6d2845d106b7caa593f34127195a88524cf87821ff0088fb6edec https://github.com/KhronosGroup/glslang/archive/refs/tags/vulkan-sdk-1.4.363.0.tar.gz
shaderc 2026.4 f06ce5bcca94e5df7f34e115743597d0ad2e13c5fe9213c67dc8c76031241947 https://github.com/google/shaderc/archive/refs/tags/v2026.4.tar.gz
spirv-cross 1.4.363.0 f708982e88b763ef5b0394ed468fc2c3628f68cdab89f2e7a946cd628c04e721 https://github.com/KhronosGroup/SPIRV-Cross/archive/refs/tags/vulkan-sdk-1.4.363.0.tar.gz
libplacebo 7.360.1 937aa5eeea596798b3274d362de2e3bd32bc537a66d149dd85043349c74dffb6 https://code.videolan.org/videolan/libplacebo/-/archive/v7.360.1/libplacebo-v7.360.1.tar.bz2
fast_float 8.3.2 71bb9e865b888df01351558db5a39586d13ea97ada162156b3a07784d8c1b26b https://github.com/fastfloat/fast_float/archive/refs/tags/v8.3.2.tar.gz
glad2 2.0.8 b84079b9fa404f37171b961bdd1d8da21370e6c818defb8481c5b3fe3d6436da https://files.pythonhosted.org/packages/6e/5a/d62b24fe1c7c2f34e15c2aa4418a5327a8550fdc272999a59e0dddebc3ee/glad2-2.0.8.tar.gz
jinja2 3.1.6 0137fb05990d35f1275a587e9aee6d56da821fc83491a0fb838183be43f66d6d https://files.pythonhosted.org/packages/df/bf/f7da0350254c0ed7c72f3e33cef02e048281fec7ecec5f032d4aac52226b/jinja2-3.1.6.tar.gz
markupsafe 3.0.3 722695808f4b6457b320fdc131280796bdceb04ab50fe1795cd540799ebe1698 https://files.pythonhosted.org/packages/7e/99/7690b6d4034fffd95959cbe0c02de8deb3098cc577c67bb6a24fe5d7caa7/markupsafe-3.0.3.tar.gz
vulkan-headers 1.4.363.0 4a078be12bef21cfebc09d878b77a63cff9d68f899254a0b00d0e37ef73e7f7e https://github.com/KhronosGroup/Vulkan-Headers/archive/refs/tags/vulkan-sdk-1.4.363.0.tar.gz
mpv 0.41.0 ee21092a5ee427353392360929dc64645c54479aefdb5babc5cfbb5fad626209 https://github.com/mpv-player/mpv/archive/refs/tags/v0.41.0.tar.gz
mpv-patch c5d391ad 769b218df220738cc1cf9f81cf696c16518c5dfe56a5ef028e33b22536e0e924 https://github.com/mpv-player/mpv/commit/c5d391adba7bd024954d0df1e0405f5749f4d4ca.patch?full_index=1
"

cache=$HOME/Library/Caches/pkp/libmpv
common=${BASH_SOURCE[0]}
key=$(cat "$0" "$common" | shasum -a 256 | cut -c1-12)
jobs=$(sysctl -n hw.ncpu)

die() { echo "$(basename "$0"): $*" >&2; exit 1; }

# fetch name: prints the path of the verified download.
fetch() {
	local want=$1
	set -- $(printf '%s\n' "$PINS" | awk -v n="$want" '$1 == n')
	[ $# -eq 4 ] || die "no pin for $want"
	local file=$cache/src/$1-$2
	if [ ! -f "$file" ] || [ "$(shasum -a 256 "$file" | cut -d' ' -f1)" != "$3" ]; then
		mkdir -p "$cache/src"
		curl -fsSL --retry 3 -o "$file.part" "$4"
		[ "$(shasum -a 256 "$file.part" | cut -d' ' -f1)" = "$3" ] || die "checksum mismatch: $4"
		mv "$file.part" "$file"
	fi
	echo "$file"
}

# unpack name dir: a fresh copy of the pinned source, whose license files
# go to $work/licenses/name for the bundle to carry.
unpack() {
	local file f
	file=$(fetch "$1")
	rm -rf "$2" && mkdir -p "$2"
	tar -xf "$file" -C "$2" --strip-components 1
	mkdir -p "$work/licenses/$1"
	for f in "$2"/*; do
		case $(basename "$f") in
		COPYING* | LICENSE* | LICENCE* | COPYRIGHT* | Copyright* | NOTICE*) [ -f "$f" ] && cp "$f" "$work/licenses/$1/" ;;
		esac
	done
	rmdir "$work/licenses/$1" 2>/dev/null || true
}

# run step function: once per arch, logged; a failure shows the log's tail.
step=
trap 'rc=$?; [ $rc -eq 0 ] || [ -z "$step" ] || { tail -n 40 "$work/logs/$step.log" >&2; echo "$(basename "$0"): $step failed, log: $work/logs/$step.log" >&2; }' EXIT
run() {
	[ -f "$work/done/$arch-$1" ] && return
	step=$arch-$1
	echo "libmpv: $step" >&2
	mkdir -p "$work/logs" "$work/done"
	("$2") >"$work/logs/$step.log" 2>&1
	touch "$work/done/$step"
	step=
}

check_host() {
	python=$(command -v python3 || true)
	"${python:-false}" -c 'import sys; sys.exit(sys.version_info < (3, 10))' 2>/dev/null ||
		die "needs python3 >= 3.10 for meson (found ${python:-none})"
	command -v cmake >/dev/null || die "needs cmake"
	# Nothing from the calling shell or Homebrew may leak into the build.
	unset CFLAGS CXXFLAGS OBJCFLAGS CPPFLAGS LDFLAGS CPATH LIBRARY_PATH C_INCLUDE_PATH \
		CPLUS_INCLUDE_PATH OBJC_INCLUDE_PATH PKG_CONFIG_PATH
}

# Host tools, the same versions for every target: built, not taken from
# Homebrew, so a build does not depend on what happens to be installed.
build_tools() {
	local t=$work/tools-src
	unpack nasm "$t/nasm"
	(cd "$t/nasm" && ./configure --prefix="$tools" && make -j"$jobs" && make install)
	unpack ninja "$t/ninja"
	(cd "$t/ninja" && "$python" configure.py --bootstrap && mkdir -p "$tools/bin" && cp ninja "$tools/bin/")
	unpack meson "$tools/meson"
	printf '#!/bin/sh\nexec "%s" "%s" "$@"\n' "$python" "$tools/meson/meson.py" >"$tools/bin/meson"
	chmod +x "$tools/bin/meson"
	# pkgconf's meson.build names test files its release tarball lacks.
	unpack pkgconf "$t/pkgconf"
	(cd "$t/pkgconf" && ./configure --prefix="$tools" --disable-shared && make -j"$jobs" && make install)
	rm -rf "$t" "$work/licenses/nasm" "$work/licenses/ninja" "$work/licenses/meson" "$work/licenses/pkgconf"
}

# libplacebo's release tarball has no submodules; stage them where its meson
# looks. The Vulkan headers are needed even without Vulkan: the stubs
# include them. jinja2 and markupsafe only run at build time.
unpack_libplacebo() {
	unpack libplacebo "$1"
	unpack fast_float "$1/3rdparty/fast_float"
	unpack glad2 "$1/3rdparty/glad"
	unpack jinja2 "$1/3rdparty/jinja"
	unpack markupsafe "$1/3rdparty/markupsafe"
	unpack vulkan-headers "$1/3rdparty/Vulkan-Headers"
	rm -rf "$work/licenses/jinja2" "$work/licenses/markupsafe"
}

# install_licenses dir: what the bundle must carry beside the libraries.
install_licenses() {
	rm -rf "$1"
	cp -R "$work/licenses" "$1"
	cat >"$1/README.txt" <<'EOF'
PKP bundles libmpv and the libraries it is built from. They are free
software under the licenses in the directories beside this file; mpv and
FFmpeg are built LGPL (mpv -Dgpl=false, FFmpeg without --enable-gpl).

The complete corresponding source of the bundled libraries, with the
scripts that build them, is published as libmpv-sources.tar.gz with each
PKP release. You may replace the bundled libraries with your own builds of
them.
EOF
}
