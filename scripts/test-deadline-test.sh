#!/usr/bin/env bash
#
# Tests scripts/test-deadline.sh with a fake test binary that prints its arguments, so no Go builds: the deadline
# becomes a trailing -test.timeout of the seconds left, a passed deadline gives 1s, and no deadline passes the
# arguments through. With $ORRERY_TEST_CPU_FILE set, a copy of the script at the top of a fake checkout appends one line
# per run, the package directory and CPU seconds, keeps the binary's exit status and output, and still applies the
# deadline.
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

# The fake checkout: the script under scripts/, a package directory to run in, and a binary that burns CPU in a child
# it waits for, prints its arguments and exits with $FAKE_EXIT.
mkdir -p "$root/tree/scripts" "$root/tree/internal/fake"
cp "$wrapper" "$root/tree/scripts/test-deadline.sh"
cat >"$root/burn.test" <<'EOF'
#!/usr/bin/env bash
bash -c 'i=0; while [ "$i" -lt 300000 ]; do i=$((i + 1)); done'
printf '%s\n' "$@"
exit "${FAKE_EXIT:-0}"
EOF
chmod +x "$root/burn.test"
record="$root/cpu.record"

rc=0
out=$(cd "$root/tree/internal/fake" && FAKE_EXIT=0 ORRERY_TEST_CPU_FILE="$record" \
    "$root/tree/scripts/test-deadline.sh" "$root/burn.test" -x "y z") || rc=$?
check "a recorded run passes the arguments as given" "$rc:$(tr '\n' '|' <<<"$out")" "0:-x|y z|"
line=$(cat "$record")
check "a recorded run appends its package and CPU seconds" "${line%% *}:$(grep -cE '^internal/fake [0-9]+\.[0-9]{3}$' "$record")" \
    "internal/fake:1"
cpu=${line#* }
cpu_ms=$((10#${cpu//./}))
if [ "$cpu_ms" -ge 100 ]; then
    check "the CPU seconds count the child the binary waited for" ok ok
else
    check "the CPU seconds count the child the binary waited for" "$cpu" "at least 0.100"
fi
check "a recorded run leaves no scratch file" "$(find "$root" -maxdepth 1 -name 'cpu.record.*' | wc -l)" 0

rc=0
out=$(cd "$root/tree/internal/fake" && FAKE_EXIT=3 ORRERY_TEST_CPU_FILE="$record" ORRERY_TEST_DEADLINE=$((now + 300)) \
    "$root/tree/scripts/test-deadline.sh" "$root/burn.test" -x) || rc=$?
check "a recorded run exits with the binary's status" "$rc" 3
case "$(tail -n 1 <<<"$out")" in
    -test.timeout=29[0-9]s | -test.timeout=300s) check "a recorded run still gets the deadline" ok ok ;;
    *) check "a recorded run still gets the deadline" "$(tail -n 1 <<<"$out")" "-test.timeout=300s" ;;
esac
check "a failed run is recorded too, after the first" "$(cut -d' ' -f1 "$record" | tr '\n' '|')" "internal/fake|internal/fake|"

: >"$record"
(cd "$root/tree" && ORRERY_TEST_CPU_FILE="$record" "$root/tree/scripts/test-deadline.sh" "$root/burn.test" >/dev/null)
check "a binary at the top of the checkout is package ." "$(cut -d' ' -f1 "$record")" "."

if [ "$failures" -ne 0 ]; then
    echo "test-deadline-test: $failures failed" >&2
    exit 1
fi
echo "test-deadline-test: all passed"
