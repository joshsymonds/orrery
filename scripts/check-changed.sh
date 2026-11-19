#!/usr/bin/env bash
# Run only the checks that the changes since BASE can affect. This is the fast gate for a task in progress. Integration,
# review and release keep the full `just check` (and `just android-test`).
#
#   scripts/check-changed.sh [BASE]
#
# BASE defaults to the merge base of HEAD and origin/main. Changes are the files that differ between BASE and the
# working tree (committed and uncommitted, tracked and untracked).
#
# The whole run has a wall-clock budget of $CHECK_CHANGED_BUDGET seconds (default 240), counted from its start. Each
# check runs under timeout with what is left of it; a check still running when it ends, or one that fails past it,
# fails the run with exit 124 and "check-changed: exceeded <budget>s budget (<check> was running)". The Go test
# binaries run through scripts/test-deadline.sh with a -test.timeout that ends 10 s before the budget, so a hung test
# is named.
#
# Selection:
#   - Go: the packages that hold a changed .go file, plus every package that imports one of them, directly or not.
#     Lint and `go test -race` run on those. A change to go.mod, go.sum, internal/store SQL, sqlc.yaml or proto/
#     selects every package. Each tested package's CPU seconds are checked against its baseline
#     (scripts/test-budget.sh), and those budget lines are printed again, with the OK or exceeded line, as the run's last
#     output: a gate keeps only the tail of its output, and the checks after the Go tests can print more than it keeps.
#   - sqlc-diff when internal/store SQL or sqlc.yaml changed.
#   - proto-lint, proto-breaking and gen-diff when proto/ changed.
#   - web-check and web-page when web/ changed (outside web/build).
#   - alert-check when observability/ changed.
#   - android-test when android/ changed. A proto/ change that reaches the Android client regenerates its code under
#     android/ (gen-diff keeps it committed), so it selects android-test too; one that does not reach it selects nothing.
#   - gate-remote-test when scripts/gate-remote.sh or its test changed.
#   - box-cache-test when scripts/box-cache.sh or its test changed.
#   - test-deadline-test when scripts/test-deadline.sh or its test changed.
#   - test-budget-test when scripts/test-budget.sh or its test changed.
#   - pin-check always (it is fast).
set -euo pipefail

budget=${CHECK_CHANGED_BUDGET:-240}
end=$(($(date +%s) + budget))
base=${1:-$(git merge-base HEAD origin/main)}
changed=$( { git diff --name-only "$base" -- . ; git ls-files --others --exclude-standard; } | sort -u )

if [ -z "$changed" ]; then
    echo "check-changed: nothing changed since $base"
    exit 0
fi

echo "check-changed: base $(git rev-parse --short "$base"), $(printf '%s\n' "$changed" | wc -l) changed files"

has() { printf '%s\n' "$changed" | grep -qE "$1"; }

# The test-budget lines of the Go tests, once they ran; budget_lines prints them again.
lines_file=""
budget_lines() {
    if [ -n "$lines_file" ]; then
        cat "$lines_file"
    fi
}

over() {
    budget_lines
    echo "check-changed: exceeded ${budget}s budget ($1 was running)" >&2
    exit 124
}

# within NAME COMMAND...: runs COMMAND under timeout with what is left of the budget. A failure at or past the end of
# the budget is the budget's; any other failure ends the run with COMMAND's status.
within() {
    local name="$1" left rc=0
    shift
    left=$((end - $(date +%s)))
    [ "$left" -ge 1 ] || over "$name"
    timeout "$left" "$@" || rc=$?
    if [ "$rc" -ne 0 ] && [ "$(date +%s)" -ge "$end" ]; then
        over "$name"
    fi
    return "$rc"
}

within pin-check just pin-check

module=$(go list -m)
all_go=no
if has '^(go\.mod|go\.sum|sqlc\.yaml|proto/)' || has '^internal/store/.*\.sql$'; then
    all_go=yes
fi

pkgs=""
if [ "$all_go" = yes ]; then
    pkgs="./..."
elif has '\.go$'; then
    # Directories of changed Go files that still exist, as import paths.
    dirs=$(printf '%s\n' "$changed" | grep -E '\.go$' | xargs -r -n1 dirname | sort -u)
    seeds=""
    for d in $dirs; do
        if [ -d "$d" ] && compgen -G "$d/*.go" > /dev/null; then
            seeds="$seeds $module/$d"
        fi
    done
    seeds=${seeds# }
    if [ -n "$seeds" ]; then
        # Every package whose dependency list (or own path) includes a seed.
        pkgs=$(go list -f '{{.ImportPath}} {{join .Deps " "}}' ./... | awk -v seeds="$seeds" '
            BEGIN { n = split(seeds, s, " "); for (i = 1; i <= n; i++) want[s[i]] = 1 }
            { for (i = 1; i <= NF; i++) if ($i in want) { print $1; next } }')
        pkgs=$(printf '%s\n' "$pkgs" | sed "s#^$module#.#" | sort -u | tr '\n' ' ')
    fi
fi

if [ -n "$pkgs" ]; then
    echo "check-changed: Go packages: $pkgs"
    if [ "$pkgs" = "./..." ]; then
        within golangci-lint golangci-lint run
    else
        # shellcheck disable=SC2086 # word splitting is the package list
        within golangci-lint golangci-lint run $pkgs
    fi
    dir=$(mktemp -d /tmp/orrery-tmux.XXXXXX)
    trap 'rm -rf "$dir"' EXIT
    # Each test's artifact directory, which holds the logs of the processes it starts, is kept under test-artifacts,
    # so a run that fails or times out leaves them on disk.
    artifacts="$PWD/test-artifacts"
    rm -rf "$artifacts" && mkdir "$artifacts"
    # Each test binary runs through scripts/test-deadline.sh, as under `just cover`: its -test.timeout ends 10 s before
    # the budget does, so a package still running then panics listing its running tests, and the line it appends to
    # the record gives the package's CPU seconds for the budget check below.
    deadline=$((end - 10))
    record="$artifacts/test-budget.record"
    : >"$record"
    left=$((end - $(date +%s)))
    [ "$left" -ge 1 ] || over "Go tests"
    rc=0
    # shellcheck disable=SC2086
    env -u TMUX -u TMUX_PANE TMUX_TMPDIR="$dir" ORRERY_TEST_DEADLINE="$deadline" ORRERY_TEST_CPU_FILE="$record" \
        timeout "$left" go test -exec "$PWD/scripts/test-deadline.sh" -count=1 -race -artifacts -outputdir "$artifacts" \
        $pkgs || rc=$?
    if [ "$rc" -eq 124 ] || { [ "$rc" -ne 0 ] && [ "$(date +%s)" -ge "$deadline" ]; }; then
        over "Go tests"
    elif [ "$rc" -ne 0 ]; then
        exit "$rc"
    fi
    sock_dir="$dir/tmux-$(id -u)"
    if [ -d "$sock_dir" ]; then
        leftover=$(find "$sock_dir" -mindepth 1 -maxdepth 1 -type s)
        if [ -n "$leftover" ]; then
            echo "tests left tmux sockets behind (stop each -L server with tmuxtest.Stop):" >&2
            echo "$leftover" >&2
            exit 1
        fi
    fi
    # One line per package over its CPU budget; exit 3 when one is over twice its baseline.
    scripts/test-budget.sh check --lines "$artifacts/test-budget.lines" "$record"
    lines_file="$artifacts/test-budget.lines"
else
    echo "check-changed: no Go package affected"
fi

if has '^internal/store/.*\.sql$' || has '^sqlc\.yaml$'; then within sqlc-diff just sqlc-diff; fi
if has '^proto/'; then within "proto checks" just proto-lint proto-breaking gen-diff; fi
if printf '%s\n' "$changed" | grep -E '^web/' | grep -vqE '^web/build/'; then within "web checks" just web-check web-page; fi
if has '^observability/'; then within alert-check just alert-check; fi
if has '^scripts/gate-remote(-test)?\.sh$'; then within gate-remote-test just gate-remote-test; fi
if has '^scripts/box-cache(-test)?\.sh$'; then within box-cache-test just box-cache-test; fi
if has '^scripts/test-deadline(-test)?\.sh$'; then within test-deadline-test just test-deadline-test; fi
if has '^scripts/test-budget(-test)?\.sh$'; then within test-budget-test just test-budget-test; fi
if has '^android/'; then
    within android-test just android-test
else
    echo "check-changed: Android untouched; android-test skipped"
fi

budget_lines
echo "check-changed: OK after $((budget - (end - $(date +%s))))s of its ${budget}s budget"
