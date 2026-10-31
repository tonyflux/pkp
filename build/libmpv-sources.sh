#!/bin/bash
# Packs the complete corresponding source of the libraries PKP bundles, as
# the LGPL asks of whoever ships them: every source pinned in
# build/libmpv-common.sh, checksums verified, with the scripts that build
# them, into the .tar.gz named on the command line (`make libmpv-sources`).
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
mkdir -p "$root/src" "$root/build/darwin" "$root/build/windows"
printf '%s\n' "$PINS" | awk 'NF == 4 && $1 != "llvm-mingw"' >"$root/SOURCES.txt"
while read -r name version _; do
	cp "$(fetch "$name")" "$root/src/$name-$version"
done <"$root/SOURCES.txt"
cp "$here/libmpv-common.sh" "$root/build/"
cp "$here/darwin/libmpv.sh" "$root/build/darwin/"
cp "$here/windows/libmpv.sh" "$root/build/windows/"
cat >"$root/README.txt" <<'EOF'
The source of libmpv and the libraries PKP bundles with it.

src/ holds each pinned download as name-version (any tar format); the
list, with sha256 and origin, is SOURCES.txt. build/darwin/libmpv.sh builds
the macOS dylibs, build/windows/libmpv.sh the Windows libmpv-2.dll, both
from the pins in build/libmpv-common.sh; copy src/* to
~/Library/Caches/pkp/libmpv/src and they build offline (bar the Windows
toolchain, llvm-mingw, fetched from its pinned URL).
EOF
tar -czf "$dest.part" -C "$stage" libmpv-sources
mv "$dest.part" "$dest"
echo "libmpv sources: $dest ($(du -h "$dest" | cut -f1))" >&2
