#!/usr/bin/env bash
set -euo pipefail
appdir=$1
sources=$2
cache="$PWD/bin/appimage-source-cache/distro"
mkdir -p "$cache"
mkdir -p "$sources/distro"
# apt verifies each exact source against the signed Ubuntu source indexes.
# Include development packages too: the AppImage runtime links their static libs.
{ cut -f3,4 "$appdir/runtime-packages.tsv"; dpkg-query -W -f='${source:Package}\t${source:Version}\n' libfuse3-dev libsquashfuse-dev zlib1g-dev libzstd-dev liblzma-dev liblz4-dev liblzo2-dev libc6-dev "$(dpkg-query -S "$(gcc -print-libgcc-file-name)" | cut -d: -f1)"; } | sort -u > "$sources/source-packages.tsv"
while IFS=$'\t' read -r name version; do
  # Query from an empty directory so apt prints every file, including cached ones.
  uris=$(mktemp -d)
  files=$(cd "$uris" && apt-get source --print-uris --download-only "$name=$version" | awk "/^'/ {print \$2, \$4}")
  rmdir "$uris"
  [ -n "$files" ] || { echo "Missing exact source: $name=$version" >&2; exit 1; }
  (cd "$cache" && apt-get source --download-only "$name=$version")
  while read -r file checksum; do
    case $checksum in
      SHA256:*) echo "${checksum#*:}  $cache/$file" | sha256sum -c --status ;;
      SHA512:*) echo "${checksum#*:}  $cache/$file" | sha512sum -c --status ;;
      *) echo "Unsupported source checksum: $checksum" >&2; exit 1 ;;
    esac
    cp "$cache/$file" "$sources/distro/"
  done <<< "$files"
done < "$sources/source-packages.tsv"
cp "$appdir/runtime-packages.tsv" "$sources/"
mkdir -p "$sources/licenses"
cp -a "$appdir/usr/share/doc/." "$sources/licenses/"
mkdir -p "$sources/common-licenses"
cp -a /usr/share/common-licenses/. "$sources/common-licenses/"
cp build/linux/appimage/{build.sh,bundle.py,bundle_test.py,portal-open-test.c,sources.sh,verify-extraction.sh,AppRun} "$sources/"
cp build/docker/Dockerfile.linux "$sources/"
(cd "$sources" && find distro -type f -print0 | sort -z | xargs -0 sha256sum > SHA256SUMS)
mkdir -p "$sources/apt-metadata"
cp /var/lib/apt/lists/*InRelease "$sources/apt-metadata/"
find /var/lib/apt/lists -maxdepth 1 -type f -name '*Sources*' -exec cp '{}' "$sources/apt-metadata/" \;
cp /usr/share/keyrings/ubuntu-archive-keyring.gpg "$sources/apt-metadata/"
mkdir -p "$sources/apt-metadata/sources.list.d"
cp -a /etc/apt/sources.list.d/. "$sources/apt-metadata/sources.list.d/"
if [ -f /etc/apt/apt.conf.d/50snapshot ]; then cp /etc/apt/apt.conf.d/50snapshot "$sources/apt-metadata/"; fi
apt-cache policy > "$sources/apt-metadata/policy.txt"
find "$sources/apt-metadata" -type f -name '*Sources*' -print -quit | grep -q . || { echo 'Missing signed source indexes; enable deb-src and apt-get update.' >&2; exit 1; }
