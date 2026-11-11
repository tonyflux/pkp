#!/bin/bash
# Packs the complete corresponding source of PKP's bundled libraries and
# PCM helper: every source pinned in build/libmpv-common.sh, checksums
# verified, plus PKP's decoder and the scripts that build them, into the
# .tar.gz named on the command line (`make libmpv-sources`).
# Publish it with each release; the bundled licenses/README.txt says so. The
# prebuilt llvm-mingw toolchain is left out: it is not a bundled library,
# and the scripts fetch it from its pinned URL.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
. "$here/libmpv-common.sh"

[ $# -eq 1 ] || die "usage: $0 out.tar.gz"
dest=$(cd "$(dirname "$1")" && pwd)/$(basename "$1")

stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
root=$stage/libmpv-sources
mkdir -p "$root/src" "$root/build/darwin" "$root/build/windows" "$root/internal/pcm"
printf '%s\n' "$PINS" | awk 'NF == 4 && $1 != "llvm-mingw"' >"$root/SOURCES.txt"
while read -r name version _; do
	cp "$(fetch "$name")" "$root/src/$name-$version"
done <"$root/SOURCES.txt"
cp "$here/libmpv-common.sh" "$root/build/"
cp "$here/darwin/libmpv.sh" "$root/build/darwin/"
cp "$here/windows/libmpv.sh" "$root/build/windows/"
cp "$here/../internal/pcm/decoder.c" "$here/../internal/pcm/decoder.h" "$root/internal/pcm/"
cp "$here/../LICENSE" "$root/LICENSE"
cat >"$root/README.txt" <<'EOF'
The corresponding source of PKP's bundled libraries and native PCM helper.

src/ holds each pinned download as name-version (any tar format); the
list, with sha256 and origin, is SOURCES.txt. build/darwin/libmpv.sh builds
the macOS dylibs and universal ffmpeg/ffprobe tools; build/windows/libmpv.sh
builds the Windows libmpv-2.dll, pkp-pcm.dll and ffmpeg.exe/ffprobe.exe.
Third-party sources come from the pins in build/libmpv-common.sh and retain
their respective licenses. internal/pcm/ holds PKP's GPL-3.0-only decoder
source at the relative path used by the Windows build script; its license
is included in LICENSE. Copy src/* to ~/Library/Caches/pkp/libmpv/src and
the scripts build offline (bar the Windows toolchain, llvm-mingw, fetched
from its pinned URL).
EOF
tar -czf "$dest.part" -C "$stage" libmpv-sources
mv "$dest.part" "$dest"
echo "libmpv sources: $dest ($(du -h "$dest" | cut -f1))" >&2
