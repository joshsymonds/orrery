#!/usr/bin/env bash
#
# The per-package test CPU budget. scripts/test-deadline.sh, run as go test's -exec program with $ORRERY_TEST_CPU_FILE
# set, appends one line per test binary to that file: the package's directory and the user+sys CPU seconds its binary
# used, "internal/store 158.420". The baseline, testdata/test-budget.json, maps each package to its CPU seconds under
# `go test -count=1 -race ./...`.
#
# Usage:
#   scripts/test-budget.sh check [--lines LINES] RECORD [BASELINE]
#       Compares the packages in RECORD, one go test run, with BASELINE (default testdata/test-budget.json), and prints
#       first one cost line per package in RECORD, sorted by package, with its CPU seconds to one decimal:
#           test-budget: cost internal/store 212.3
#       then one line per package over its budget, where the budget is the baseline plus the larger of 15% and 5 CPU
#       seconds:
#           test-budget: internal/store 212 cpu-s > 160 (+33%)
#       A package over its budget and over twice its baseline gets ", over twice its baseline" on its line. The check is
#       advisory: a package over budget never fails it. A package with no baseline gets "test-budget: internal/new 12
#       cpu-s, no baseline". A baseline package missing from RECORD is not mentioned: a run may test only some packages.
#       The last line counts the packages checked. Exits 0 whatever it prints, and 1 on a malformed RECORD or BASELINE.
#       With --lines, the lines printed are written to LINES too (it is emptied first),
#       so a gate that runs more checks afterwards can print them again as its last output: a gate's output is kept
#       only as a tail, and the budget lines are what the test_parsimony reviewer reads of it.
#   scripts/test-budget.sh update BASELINE RECORD...
#       Writes BASELINE from one or more RECORDs, each one run: each package gets the lowest of its runs' CPU seconds,
#       to one decimal, and a package that no RECORD has is dropped. Exits 1 on a malformed RECORD.
#
# Within one RECORD the lines of one package are added up, so a package whose binary ran twice in a run counts both.

set -euo pipefail
# Numbers are read and written with a decimal point, and packages sorted bytewise, whatever the caller's locale.
export LC_ALL=C

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
    echo "usage: scripts/test-budget.sh check [--lines LINES] RECORD [BASELINE] | update BASELINE RECORD..." >&2
    exit 1
}

# totals RECORD: prints "<package> <cpu seconds>" once per package, adding up a package's lines; a malformed line is
# named on stderr and fails it.
totals() {
    awk '
        NF != 2 || $2 !~ /^[0-9]+(\.[0-9]+)?$/ {
            printf "test-budget: %s:%d: not \"<package> <cpu seconds>\": %s\n", FILENAME, FNR, $0 > "/dev/stderr"
            bad = 1
            next
        }
        { cpu[$1] += $2 }
        END {
            if (bad) exit 1
            for (p in cpu) printf "%s %.3f\n", p, cpu[p]
        }' "$1"
}

# baseline BASELINE: prints "<package> <cpu seconds>" per entry; anything other than an object of non-negative numbers
# fails it.
baseline() {
    jq -r '
        if type != "object" then error("not an object of package to cpu seconds")
        else to_entries[]
            | if (.value | type) != "number" or .value < 0 then error("\(.key): not a number of cpu seconds")
              else "\(.key) \(.value)" end
        end' "$1" || {
        echo "test-budget: $1 is not an object of package to cpu seconds" >&2
        return 1
    }
}

check() {
    local record="$1" base_file="$2" base_lines run_lines
    base_lines=$(baseline "$base_file")
    run_lines=$(totals "$record")
    awk -v base_lines="$base_lines" -v run_lines="$run_lines" '
        BEGIN {
            n = split(base_lines, lines, "\n")
            for (i = 1; i <= n; i++) if (split(lines[i], f, " ") == 2) base[f[1]] = f[2] + 0
            n = split(run_lines, lines, "\n")
            for (i = 1; i <= n; i++) if (split(lines[i], f, " ") == 2) { cpu[f[1]] = f[2] + 0; pkgs[++count] = f[1] }
            # A stable order: the packages sorted by name.
            for (i = 2; i <= count; i++) {
                p = pkgs[i]
                for (j = i - 1; j >= 1 && pkgs[j] > p; j--) pkgs[j + 1] = pkgs[j]
                pkgs[j + 1] = p
            }
            for (i = 1; i <= count; i++) printf "test-budget: cost %s %.1f\n", pkgs[i], cpu[pkgs[i]]
            failed = 0
            for (i = 1; i <= count; i++) {
                p = pkgs[i]
                c = cpu[p]
                if (!(p in base)) {
                    printf "test-budget: %s %d cpu-s, no baseline\n", p, c + 0.5
                    continue
                }
                b = base[p]
                allowance = b * 0.15
                if (allowance < 5) allowance = 5
                if (c <= b + allowance) continue
                growth = b > 0 ? sprintf("+%d%%", (c - b) / b * 100 + 0.5) : "from 0"
                line = sprintf("test-budget: %s %d cpu-s > %d (%s)", p, c + 0.5, b + 0.5, growth)
                if (c > 2 * b) {
                    line = line ", over twice its baseline"
                    failed++
                }
                print line
            }
            if (failed) printf "test-budget: %d of %d packages over twice their baseline\n", failed, count
            else printf "test-budget: %d packages checked, none over twice its baseline\n", count
        }'
}

update() {
    local out="$1" record lines=""
    shift
    for record in "$@"; do
        lines+=$(totals "$record")$'\n'
    done
    # The JSON is written here rather than by jq, whose number formatting differs between versions: one package per
    # line, sorted, each to one decimal.
    printf '%s' "$lines" |
        awk 'NF == 2 { if (!($1 in low) || $2 + 0 < low[$1]) low[$1] = $2 + 0 } END { for (p in low) printf "%s %.1f\n", p, low[p] }' |
        sort |
        awk 'BEGIN { printf "{" } { printf "%s\n  \"%s\": %s", (NR > 1 ? "," : ""), $1, $2 } END { printf "%s}\n", (NR > 0 ? "\n" : "") }' >"$out.tmp"
    mv "$out.tmp" "$out"
    echo "test-budget: wrote $(jq length "$out") packages to $out"
}

case "${1:-}" in
    check)
        shift
        if [ "${1:-}" = --lines ]; then
            [ "$#" -ge 2 ] || usage
            lines="$2"
            shift 2
            [ "$#" -ge 1 ] && [ "$#" -le 2 ] || usage
            check "$1" "${2:-$root/testdata/test-budget.json}" | tee "$lines"
        else
            [ "$#" -ge 1 ] && [ "$#" -le 2 ] || usage
            check "$1" "${2:-$root/testdata/test-budget.json}"
        fi
        ;;
    update)
        [ "$#" -ge 3 ] || usage
        shift
        update "$@"
        ;;
    *) usage ;;
esac
