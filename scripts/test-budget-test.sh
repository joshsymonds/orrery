#!/usr/bin/env bash
#
# Tests scripts/test-budget.sh with synthetic records and baselines, so no Go runs: which packages are reported and how
# the line reads, the 15% and 5 cpu-s allowances, failing (exit 3) only past twice the baseline, a package with no
# baseline, malformed input, and `update` keeping each package's lowest run.
#
# Usage: scripts/test-budget-test.sh   (exits 0 when every case passes)

set -euo pipefail

budget="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-budget.sh"
root="$(mktemp -d)"
trap 'rm -rf -- "$root"' EXIT

failures=0
check() {
    if [ "$2" = "$3" ]; then
        echo "ok: $1"
    else
        printf 'FAIL: %s:\n got: %s\nwant: %s\n' "$1" "$2" "$3" >&2
        failures=$((failures + 1))
    fi
}

# run NAME RECORD-LINES: checks RECORD-LINES against $root/base.json, setting out (stdout and stderr) and rc.
run() {
    printf '%s' "$2" >"$root/$1.record"
    rc=0
    out=$("$budget" check "$root/$1.record" "$root/base.json" 2>&1) || rc=$?
}

cat >"$root/base.json" <<'EOF'
{
  "internal/store": 160,
  "internal/server": 100,
  "internal/small": 10,
  "internal/tiny": 1,
  "internal/zero": 0,
  "cmd/orreryd": 20.5
}
EOF

run within $'internal/store 170.2\ninternal/server 110\ncmd/orreryd 25.4\n'
check "a run within every budget exits 0" "$rc" 0
check "a run within every budget reports only the count" "$out" "test-budget: 3 packages checked, none over twice its baseline"

run over $'internal/store 212.3\ninternal/server 99\n'
check "a package over budget does not fail the check" "$rc" 0
check "a package over budget gets one line, baseline and growth rounded" "$out" "test-budget: internal/store 212 cpu-s > 160 (+33%)
test-budget: 2 packages checked, none over twice its baseline"

run percent $'internal/server 114.9\ninternal/small 14.9\n'
check "within 15% of a large baseline, or 5 cpu-s of a small one, is within budget" "$out" \
    "test-budget: 2 packages checked, none over twice its baseline"

run edges $'internal/server 115.1\ninternal/small 15.1\n'
check "past 15% of a large baseline and past 5 cpu-s of a small one is over budget" "$out" "test-budget: internal/server 115 cpu-s > 100 (+15%)
test-budget: internal/small 15 cpu-s > 10 (+51%)
test-budget: 2 packages checked, none over twice its baseline"

run tiny $'internal/tiny 5.9\n'
check "over twice a tiny baseline but within its 5 cpu-s passes unreported" "$rc:$out" \
    "0:test-budget: 1 packages checked, none over twice its baseline"

run double $'internal/store 330\ninternal/server 120\ninternal/zero 6\n'
check "a package over twice its baseline exits 3" "$rc" 3
check "each package over twice its baseline is named so" "$out" "test-budget: internal/server 120 cpu-s > 100 (+20%)
test-budget: internal/store 330 cpu-s > 160 (+106%), over twice its baseline
test-budget: internal/zero 6 cpu-s > 0 (from 0), over twice its baseline
test-budget: 2 of 3 packages over twice their baseline"

run exactly $'internal/store 320\n'
check "exactly twice the baseline does not fail" "$rc:$out" "0:test-budget: internal/store 320 cpu-s > 160 (+100%)
test-budget: 1 packages checked, none over twice its baseline"

run new $'internal/new 12.4\ninternal/store 150\n'
check "a package with no baseline is reported and does not fail" "$rc:$out" "0:test-budget: internal/new 12 cpu-s, no baseline
test-budget: 2 packages checked, none over twice its baseline"

run summed $'internal/store 100\ninternal/store 100\n'
check "a package's lines in one run add up" "$out" "test-budget: internal/store 200 cpu-s > 160 (+25%)
test-budget: 1 packages checked, none over twice its baseline"

run malformed $'internal/store 100\ninternal/server\ninternal/x 1e3\n'
check "a malformed record exits 1, naming each bad line" "$rc:$out" "1:test-budget: $root/malformed.record:2: not \"<package> <cpu seconds>\": internal/server
test-budget: $root/malformed.record:3: not \"<package> <cpu seconds>\": internal/x 1e3"

printf 'internal/store 100\n' >"$root/ok.record"
for bad in '[1, 2]' '{"internal/store": "160"}' '{"internal/store": -1}' 'not json'; do
    printf '%s\n' "$bad" >"$root/bad.json"
    rc=0
    out=$("$budget" check "$root/ok.record" "$root/bad.json" 2>&1) || rc=$?
    check "a malformed baseline ($bad) exits 1" "$rc:$(tail -n 1 <<<"$out")" \
        "1:test-budget: $root/bad.json is not an object of package to cpu seconds"
done

rc=0
"$budget" check >/dev/null 2>&1 || rc=$?
check "no record is a usage error" "$rc" 1

printf 'internal/store 150.04\ninternal/server 90\ncmd/orreryd 3\n' >"$root/run1"
printf 'internal/store 140.26\ninternal/server 95\ninternal/server 1\ninternal/new 7.77\n' >"$root/run2"
out=$("$budget" update "$root/updated.json" "$root/run1" "$root/run2")
check "update says what it wrote" "$out" "test-budget: wrote 4 packages to $root/updated.json"
check "update keeps each package's lowest run, to one decimal, sorted" "$(cat "$root/updated.json")" '{
  "cmd/orreryd": 3.0,
  "internal/new": 7.8,
  "internal/server": 90.0,
  "internal/store": 140.3
}'
printf 'internal/store 141\n' >"$root/roundtrip.record"
rc=0
out=$("$budget" check "$root/roundtrip.record" "$root/updated.json" 2>&1) || rc=$?
check "the baseline update writes is one check reads" "$rc:$out" \
    "0:test-budget: 1 packages checked, none over twice its baseline"
printf 'internal/store 1\n' >"$root/run4"
"$budget" update "$root/one.json" "$root/run4" >/dev/null
check "a baseline of one package is valid JSON" "$(jq -c 'map_values(. + 0)' "$root/one.json" 2>&1)" '{"internal/store":1}'

printf 'oops\n' >"$root/run3"
cp "$root/updated.json" "$root/kept.json"
rc=0
"$budget" update "$root/updated.json" "$root/run1" "$root/run3" >/dev/null 2>&1 || rc=$?
check "update from a malformed record exits 1 and leaves the baseline as it was" \
    "$rc:$(cmp -s "$root/kept.json" "$root/updated.json" && echo same)" "1:same"

if [ "$failures" -ne 0 ]; then
    echo "test-budget-test: $failures failed" >&2
    exit 1
fi
echo "test-budget-test: all passed"
