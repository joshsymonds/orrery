#!/usr/bin/env bash
# Bundles the web page into the directory named by its one argument, relative to the repo root: index.html, the
# entry script, the stylesheet and the font files, all referenced by same-origin relative paths. The justfile and
# the flake package both call this script, so the page is built one way. Only esbuild on PATH is needed, and
# every import resolves from web/node_modules.
set -euo pipefail

if [ "$#" -ne 1 ]; then
    echo "usage: scripts/build-web.sh <output directory>" >&2
    exit 2
fi

cd "$(dirname "$0")/.."
out=$1

rm -rf "$out"
mkdir -p "$out"
cp web/src/index.html "$out/index.html"
esbuild web/src/main.tsx web/src/style.css \
    --bundle --minify --format=esm --outbase=web/src --outdir="$out" \
    "--loader:.ttf=file" "--asset-names=[name]" --log-level=warning
