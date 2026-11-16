#!/usr/bin/env bash
#
# Tests scripts/test-deadline.sh with a fake test binary that prints its arguments, so no Go builds: the deadline
# becomes a trailing -test.timeout of the seconds left, a passed deadline gives 1s, and no deadline passes the
# arguments through.
#
# Usage: scripts/test-deadline-test.sh   (exits 0 when every case passes)

set -euo pipefail

wrapper="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-deadline.sh"
root="$(mktemp -d)"
trap 'rm -rf -- "$root"' EXIT
printf '#!/bin/sh\nprintf "%%s\\n" "$@"\n' >"$root/fake.test"
chmod +x "$root/fake.test"

failures=0
check() {
    if [ "$2" = "$3" ]; then
        echo "ok: $1"
    else
        echo "FAIL: $1: got '$2', want '$3'" >&2
        failures=$((failures + 1))
    fi
}

now=$(date +%s)
out=$(ORRERY_TEST_DEADLINE=$((now + 300)) "$wrapper" "$root/fake.test" -test.timeout=10m0s "-test.run=^TestA b$")
check "the arguments come through as words" "$(sed -n 2p <<<"$out")" '-test.run=^TestA b$'
last=$(tail -n 1 <<<"$out")
case "$last" in
    -test.timeout=29[89]s | -test.timeout=300s) check "the seconds left come last as -test.timeout" ok ok ;;
    *) check "the seconds left come last as -test.timeout" "$last" "-test.timeout=300s" ;;
esac

out=$(ORRERY_TEST_DEADLINE=$((now - 5)) "$wrapper" "$root/fake.test" -x)
check "a passed deadline gives 1s" "$(tail -n 1 <<<"$out")" "-test.timeout=1s"

out=$(env -u ORRERY_TEST_DEADLINE "$wrapper" "$root/fake.test" -x "y z")
check "no deadline passes the arguments as given" "$(tr '\n' '|' <<<"$out")" "-x|y z|"

if [ "$failures" -ne 0 ]; then
    echo "test-deadline-test: $failures failed" >&2
    exit 1
fi
echo "test-deadline-test: all passed"
