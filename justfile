#!/usr/bin/env -S just --justfile

set shell := ["bash", "-uc"]

# Wall-clock budget (seconds) for the test suite, a hang guard rather than a
# performance target. Override on the CLI: just TEST_BUDGET=60 check
TEST_BUDGET := "360"

# List available recipes.
default:
    @just --list

# Run golangci-lint.
lint:
    golangci-lint run

# Run tests with race and coverage, and enforce at least 80% total coverage
# excluding cmd/ entry points.
# -coverpkg=./... attributes coverage across package boundaries.
cover:
    #!/usr/bin/env bash
    set -euo pipefail

    rc=0
    timeout "{{TEST_BUDGET}}" go test -race -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./... || rc=$?
    if [ "$rc" -eq 124 ]; then
        echo "tests exceeded {{TEST_BUDGET}}s budget" >&2
        exit 124
    elif [ "$rc" -ne 0 ]; then
        exit "$rc"
    fi

    # cmd/ holds entry-point glue and is not unit-tested code we author.
    grep -vE '/cmd/' coverage.out > coverage.cov.out

    pct=$(go tool cover -func=coverage.cov.out | awk '/^total:/{gsub(/%/, "", $NF); print $NF}')
    ok=$(awk -v p="$pct" 'BEGIN { print (p+0 >= 80.0) ? "yes" : "no" }')
    if [ "$ok" = "no" ]; then
        echo "coverage ${pct}% is below 80% threshold" >&2
        exit 1
    fi
    echo "coverage: ${pct}% (>= 80% OK)"

# Lint and coverage gates.
check: lint cover

# Run the Android lint and JVM unit tests.
android-test:
    nix develop .#android -c gradle -p android lintDebug test

# Build the debug APK and install it on the connected device.
android-install:
    nix develop .#android -c bash -ceu '\
        gradle -p android assembleDebug; \
        adb install -r android/app/build/outputs/apk/debug/app-debug.apk'

# Regenerate the Orrery Mono subsets (Regular, Bold, Italic) of Maple Mono NF
# from the flake's pinned nixpkgs, renamed because the OFL reserves the name.
android-font:
    #!/usr/bin/env bash
    set -euo pipefail
    src=$(nix build --no-link --print-out-paths --inputs-from . nixpkgs#maple-mono.NF-CN-unhinted)
    tmp=$(mktemp -d)
    trap 'rm -rf "$tmp"' EXIT
    nix shell --inputs-from . nixpkgs#python3Packages.fonttools -c bash -euo pipefail -s "$src" "$tmp" "$PWD/android/app/src/main/res" <<'SCRIPT'
    src=$1 tmp=$2 res=$3
    unicodes='U+0020-007E,U+00A0-024F,U+2000-206F,U+2190-21FF,U+2200-22FF,U+2300-23FF,U+2500-259F,U+25A0-25FF,U+2600-27BF,U+2800-28FF,U+2B00-2BFF,U+E000-F8FF,U+F0000-F1FFF'
    mkdir -p "$res/font"
    for style in Regular Bold Italic; do
        lower=$(printf '%s' "$style" | tr '[:upper:]' '[:lower:]')
        pyftsubset "$src/share/fonts/truetype/MapleMono-NF-CN-$style.ttf" \
            --output-file="$tmp/$lower.ttf" --unicodes="$unicodes" --layout-features='*' --name-IDs='*'
        ttx -q -t name -f -o "$tmp/$lower.name.ttx" "$tmp/$lower.ttf"
        sed -i -e 's/Maple Mono NF CN/Orrery Mono/g' -e 's/MapleMono-NF-CN-/OrreryMono-/g' "$tmp/$lower.name.ttx"
        ttx -q --no-recalc-timestamp -m "$tmp/$lower.ttf" -o "$res/font/orrery_mono_$lower.ttf" "$tmp/$lower.name.ttx"
    done
    SCRIPT
