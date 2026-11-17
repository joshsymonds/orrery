#!/usr/bin/env bash
# go test -exec program: runs a test binary with -test.timeout set to the seconds left until $ORRERY_TEST_DEADLINE
# (Unix seconds), at least 1. A binary still running at the deadline panics with "test timed out", listing the tests
# it was running, which go test prints under the package's FAIL line; killed by an outer timeout it would end without
# a word. The flag goes last, so it wins over the -test.timeout go test passes. Without a deadline the binary runs
# with the arguments as given.
#
# With $ORRERY_TEST_CPU_FILE set, it also appends one line to that file once the binary ends: the package, as the
# binary's directory relative to the top of this checkout (go test runs each binary in its package's directory), and
# the user+sys CPU seconds the binary and the children it waited for used, "internal/store 158.420".
# scripts/test-budget.sh compares those lines with the baseline. The time is this shell's children's time, so nothing
# before the binary starts a process.
#
# Usage: go test -exec "$PWD/scripts/test-deadline.sh" ...   (with ORRERY_TEST_DEADLINE and ORRERY_TEST_CPU_FILE set,
#        or either alone)
set -u
bin="$1"
shift
if [ -n "${ORRERY_TEST_DEADLINE:-}" ]; then
    left=$((ORRERY_TEST_DEADLINE - EPOCHSECONDS))
    [ "$left" -ge 1 ] || left=1
    set -- "$@" -test.timeout="${left}s"
fi
if [ -z "${ORRERY_TEST_CPU_FILE:-}" ]; then
    exec "$bin" "$@"
fi

# The binary runs in the foreground, a child of this shell, so it keeps the signal dispositions go test gave it (a
# background command would ignore SIGINT, and pass that on to the processes its tests start) and gets what is sent to
# go test's process group, an interrupt or timeout's TERM, itself.
rc=0
"$bin" "$@" || rc=$?

# times prints the shell's own user and sys time, then its children's, each as <minutes>m<seconds>.<millis>s; the
# decimal point follows the locale, so only digits are kept.
scratch="$ORRERY_TEST_CPU_FILE.$$"
times >"$scratch"
{
    read -r _
    read -r user sys
} <"$scratch"
rm -f "$scratch"
millis() {
    local minutes="${1%%m*}" rest="${1#*m}"
    rest="${rest%s}"
    echo $((10#$minutes * 60000 + 10#${rest//[!0-9]/}))
}
cpu=$(($(millis "$user") + $(millis "$sys")))
root="${0%/scripts/*}"
pkg="${PWD#"$root"/}"
[ "$PWD" != "$root" ] || pkg="."
printf '%s %d.%03d\n' "$pkg" $((cpu / 1000)) $((cpu % 1000)) >>"$ORRERY_TEST_CPU_FILE"
exit "$rc"
