#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
[[ $(uname -s) == Linux ]] || { echo 'Linux builds require Ubuntu 24.04; use make linux-docker.' >&2; exit 1; }
arch=$(go env GOARCH)
[[ $arch == amd64 || $arch == arm64 ]] || { echo "Unsupported Linux architecture: $arch" >&2; exit 1; }
[[ ${LINUX_ARCH:-$arch} == "$arch" ]] || { echo 'Use a matching native/container architecture, not a cgo cross-build.' >&2; exit 1; }
export GOARCH=$arch CGO_ENABLED=1
export VERSION=${VERSION:-$(go run ./cmd/pkp-release version)}
export SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH:-0}
export TZ=UTC
export LINUX_STAGE="$PWD/bin/linux-$arch"
pkg-config --atleast-version=4.14 gtk4
pkg-config --exists webkitgtk-6.0 mpv libavcodec libavformat libavutil libswresample
[[ $(pkg-config --modversion libavcodec) == 60.* ]] || { echo 'Linux packages require the Ubuntu 24.04 FFmpeg 6 ABI (libavcodec 60).' >&2; exit 1; }
test -s frontend/dist/THIRD_PARTY_LICENSES.txt
mkdir -p "$LINUX_STAGE/usr/lib/pkp" "$LINUX_STAGE/usr/bin" "$LINUX_STAGE/usr/share/licenses/pkp"
go build -tags production -trimpath -buildvcs=false -ldflags="-w -s -X main.version=$VERSION" -o "$LINUX_STAGE/usr/lib/pkp/pkp" .
install -m755 build/linux/pkp "$LINUX_STAGE/usr/bin/pkp"
install -m644 LICENSE frontend/dist/THIRD_PARTY_LICENSES.txt "$LINUX_STAGE/usr/share/licenses/pkp/"
python3 build/go-licenses.py linux "$LINUX_STAGE/usr/share/licenses/pkp/GO_LICENSES.txt"
case ${1:-package} in
  build) exit 0 ;;
  package) ;;
  *) echo 'Usage: build/linux/build.sh [build|package]' >&2; exit 1 ;;
esac
nfpm package -f build/linux/nfpm/nfpm.yaml -p deb -t "bin/PKP-$VERSION-linux-$arch.deb"
bash build/linux/appimage/build.sh
export LINUX_APPDIR="$PWD/bin/appimage-$arch/PKP.AppDir"
nfpm package -f build/linux/nfpm/nfpm.yaml -p rpm -t "bin/PKP-$VERSION-linux-$arch.rpm"
