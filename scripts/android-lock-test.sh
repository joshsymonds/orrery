#!/usr/bin/env bash
#
# Tests scripts/android-lock.sh with plain commands, so no Gradle: unset, it runs the command and keeps its status; set
# and free, it runs at once and says nothing; set and held by another process, it says it waits and runs the command
# only once that holder lets go. The waits are on FIFOs; each read's -t is a hang guard that a passing run never reaches.
#
# Usage: scripts/android-lock-test.sh   (exits 0 when every case passes)

set -euo pipefail

lock_script="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/android-lock.sh"
root="$(mktemp -d)"
holder=""
trap '[ -n "$holder" ] && kill "$holder" 2>/dev/null; rm -rf -- "$root"' EXIT

failures=0
check() {
    if [ "$2" = "$3" ]; then
        echo "ok: $1"
    else
        echo "FAIL: $1: got '$2', want '$3'" >&2
        failures=$((failures + 1))
    fi
}

# Unset: the command runs as it is, with its status.
status=0
env -u ORRERY_ANDROID_LOCK "$lock_script" sh -c 'echo ran; exit 3' >"$root/out" 2>"$root/err" || status=$?
check "unset keeps the command's status" "$status" 3
check "unset runs the command" "$(cat "$root/out")" ran
check "unset says nothing" "$(cat "$root/err")" ""

# Set and free: the command runs at once and nothing waits.
lock="$root/android.lock"
status=0
ORRERY_ANDROID_LOCK="$lock" "$lock_script" sh -c 'echo ran; exit 0' >"$root/out" 2>"$root/err" || status=$?
check "a free lock keeps the command's status" "$status" 0
check "a free lock runs the command" "$(cat "$root/out")" ran
check "a free lock says nothing" "$(cat "$root/err")" ""

# Held: another process holds the lock until it reads from release.
mkfifo "$root/held" "$root/release" "$root/err-fifo"
flock "$lock" sh -c 'echo held >"$1"; read -r _ <"$2"' _ "$root/held" "$root/release" &
holder=$!
read -r -t 20 line <"$root/held"
check "the other holder holds the lock" "$line" held

exec 7<>"$root/err-fifo"
ORRERY_ANDROID_LOCK="$lock" "$lock_script" touch "$root/ran" 2>"$root/err-fifo" &
waiter=$!
read -r -t 20 -u 7 line
check "a held lock says it waits" "$line" "android-lock: waiting for another Android build on this machine ($lock)"
check "a held lock runs nothing yet" "$([ -e "$root/ran" ] && echo ran || echo not-run)" not-run

echo go >"$root/release"
wait "$holder"
holder=""
status=0
wait "$waiter" || status=$?
check "the waiter keeps the command's status" "$status" 0
check "the waiter runs once the holder lets go" "$([ -e "$root/ran" ] && echo ran || echo not-run)" ran
exec 7<&-

if [ "$failures" -gt 0 ]; then
    echo "android-lock-test: $failures failed" >&2
    exit 1
fi
echo "android-lock-test: all passed"
