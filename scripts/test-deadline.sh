#!/bin/sh
# go test -exec program: runs a test binary with -test.timeout set to the seconds left until $ORRERY_TEST_DEADLINE
# (Unix seconds), at least 1. A binary still running at the deadline panics with "test timed out", listing the tests
# it was running, which go test prints under the package's FAIL line; killed by an outer timeout it would end without
# a word. The flag goes last, so it wins over the -test.timeout go test passes. Without a deadline the binary runs
# as given.
#
# Usage: go test -exec scripts/test-deadline.sh ...   (with ORRERY_TEST_DEADLINE set)
set -eu
bin="$1"
shift
if [ -n "${ORRERY_TEST_DEADLINE:-}" ]; then
    left=$((ORRERY_TEST_DEADLINE - $(date +%s)))
    [ "$left" -ge 1 ] || left=1
    exec "$bin" "$@" -test.timeout="${left}s"
fi
exec "$bin" "$@"
