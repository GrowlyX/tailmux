#!/bin/sh
# Renders the Homebrew formula for a source tarball.
# usage: scripts/render-formula.sh <tarball-url> <sha256>
set -eu
here=$(cd "$(dirname "$0")/.." && pwd)
sed -e "s|@URL@|$1|" -e "s|@SHA256@|$2|" "$here/packaging/homebrew/tailmux.rb.tmpl"
