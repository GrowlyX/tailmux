#!/usr/bin/env bash
# Puts wintun.dll (the Windows TUN driver, from wintun.net) into a
# directory, checksum-verified.
# usage: scripts/fetch-wintun.sh <dest-dir> [amd64|arm64]
set -euo pipefail
dest=$1
arch=${2:-amd64}
version=0.14.1
sha256=07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51
tmp=$(mktemp -d)
curl -fsSL -o "$tmp/wintun.zip" "https://www.wintun.net/builds/wintun-$version.zip"
got=$(sha256sum "$tmp/wintun.zip" 2>/dev/null || shasum -a 256 "$tmp/wintun.zip")
if [ "${got%% *}" != "$sha256" ]; then
  echo "wintun.zip: checksum mismatch" >&2
  exit 1
fi
unzip -q -o "$tmp/wintun.zip" -d "$tmp"
mkdir -p "$dest"
cp "$tmp/wintun/bin/$arch/wintun.dll" "$dest/"
cp "$tmp/wintun/LICENSE.txt" "$dest/wintun-LICENSE.txt"
echo "wintun $version ($arch) -> $dest/wintun.dll"
