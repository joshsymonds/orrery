#!/usr/bin/env bash
# Bundles the web page into the directory named by its one argument, relative to the repo root: index.html, the
# entry script, the stylesheet and the font files, all referenced by same-origin relative paths, and build.txt, the
# page's build. The justfile and the flake package both call this script, so the page is built one way. Only esbuild
# and sha256sum on PATH are needed, and every import resolves from web/node_modules.
#
# The build is the sha256 of the entry script bundled with an empty build, so it changes exactly when the page's code
# does. The script is then bundled again with ORRERY_PAGE_BUILD defined as that build, and serve reports build.txt in
# Host.Status, so a page an earlier serve sent sees that its build differs (web/src/pagebuild.ts).
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
bundle() {
    esbuild web/src/main.tsx web/src/style.css \
        --bundle --minify --format=esm --outbase=web/src --outdir="$out" \
        "--loader:.ttf=file" "--asset-names=[name]" --log-level=warning "--define:ORRERY_PAGE_BUILD=\"$1\""
}
bundle ""
build=$(sha256sum "$out/main.js" | cut -d' ' -f1)
bundle "$build"
printf '%s\n' "$build" >"$out/build.txt"
