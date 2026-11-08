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
