#!/usr/bin/env bash
# Run only the checks that the changes since BASE can affect. This is the fast gate for a task in progress. Integration
# and release run the Done gates a verification choice picks for each candidate, every Done gate when the choice falls
# back, and a gate declared always on every run (see Batch integration in docs/contract.md); an epic whose Done gates
# are the full `just check` and `just android-test` runs them when its choice says so.
#
#   scripts/check-changed.sh [BASE]
#
# BASE defaults to the merge base of HEAD and origin/main. Changes are the files that differ between BASE and the
# working tree (committed and uncommitted, tracked and untracked).
#
# The run has no wall-clock budget of its own: each check runs to its end, and the gate that runs this script is what
# ends a hang. The run's elapsed seconds are on its OK line.
#
# Selection:
#   - Go: the package of each changed file, plus every package that imports one of them, directly or not, through its
#     own code or its tests'. A changed file's package is the nearest directory, its own or one above it, that holds a
#     .go file, so a file a package embeds or its tests read, such as a role text or a testdata fixture, selects the
#     package it sits under. A selected cmd/orrery or cmd/orreryd also selects the tests that build and run those
#     binaries, internal/integration and, for cmd/orrery, internal/tmuxconf. Lint and `go test -race` run on those. A
#     change to go.mod, go.sum, internal/store SQL, sqlc.yaml or proto/ selects every package. Each tested package's
#     CPU seconds are checked against its baseline (scripts/test-budget.sh), advisorily, and those budget lines are
#     printed again, before the OK line, as the run's last output: a gate keeps only the tail of its output, and the
#     checks after the Go tests can print more than it keeps.
#   - The repo-wide scans, when what they read changed and their package is not selected: the Go tests that read the
#     sources of other packages, such as the host seam's scan, or the web and Android trees, the docs, the README, the
#     project file or another package's fixtures, which the import graph does not lead to. The scans table below lists
#     each with the changed paths that run it. Only the scan's own tests run, with -race.
#   - doc-cites when docs/ changed.
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

start=$(date +%s)
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

just pin-check

module=$(go list -m)
all_go=no
if has '^(go\.mod|go\.sum|sqlc\.yaml|proto/)' || has '^internal/store/.*\.sql$'; then
    all_go=yes
fi

pkgs=""
if [ "$all_go" = yes ]; then
    pkgs="./..."
else
    # The package of each changed file, as an import path: the nearest directory that still exists and holds a .go file,
    # the file's own or one above it. A file under no such directory, such as one under web/ or docs/, seeds nothing.
    seeds=""
    while IFS= read -r file; do
        d=$(dirname "$file")
        while [ "$d" != "." ] && ! { [ -d "$d" ] && compgen -G "$d/*.go" > /dev/null; }; do
            d=$(dirname "$d")
        done
        if [ "$d" != "." ]; then
            seeds="$seeds $module/$d"
        fi
    done <<< "$changed"
    seeds=${seeds# }
    if [ -n "$seeds" ]; then
        # Every package whose own path or dependency list holds a seed, its tests' dependencies included. go list -test
        # lists each package's test variants as well, "<path> [<path>.test]", "<path>_test [<path>.test]" and
        # "<path>.test", whose dependencies are the tests'; each is read as the package it tests.
        pkgs=$(go list -test -f '{{.ImportPath}}|{{join .Deps " "}}' ./... | awk -F'|' -v seeds="$seeds" '
            BEGIN { n = split(seeds, s, " "); for (i = 1; i <= n; i++) want[s[i]] = 1 }
            {
                name = $1; sub(/ \[.*$/, "", name); sub(/\.test$/, "", name); sub(/_test$/, "", name)
                if (name in want) { print name; next }
                m = split($2, deps, " "); for (i = 1; i <= m; i++) if (deps[i] in want) { print name; next }
            }')
        pkgs=$(printf '%s\n' "$pkgs" | sed "s#^$module#.#" | sort -u | tr '\n' ' ')
    fi
    # The tests that build cmd/orrery and cmd/orreryd and run them read every package the binaries are built from, which
    # no import names, so a selected binary selects them.
    case " $pkgs " in *" ./cmd/orrery "* | *" ./cmd/orreryd "*) pkgs="$pkgs ./internal/integration " ;; esac
    case " $pkgs " in *" ./cmd/orrery "*) pkgs="$pkgs ./internal/tmuxconf " ;; esac
    # shellcheck disable=SC2086 # word splitting is the package list
    pkgs=$(printf '%s\n' $pkgs | sort -u | tr '\n' ' ')
    pkgs=${pkgs% }
fi

# The repo-wide scans: Go tests that read files the import graph does not lead them to, so the selection above does not
# pick them for a change to what they read. Each line is a package, the -run pattern of its tests that read those
# files, and an extended regex of the changed paths that run them. A scan runs when such a path changed and its package
# is not selected, since a selected package runs every test it has.
scans='
./internal/hostexec ^TestHostSeam$ \.go$
./internal/engine ^TestEngineHoldsNoPrivateDetail$ ^internal/engine/
./internal/guard ^TestKotlinTestsReadAndroidSuite$ ^android/app/src/test/
./internal/guard ^TestNodeTestsReadWebSuite$ ^web/src/
./internal/server ^TestRetireNoGambitRecordsClientsCarryNoRecordFields$ ^(web/src/|android/app/src/(main|test)/kotlin/)
./internal/help ^TestReadmeDailyUseMatchesCard$ ^README\.md$
./internal/store ^TestReaderPoolRestoreOrderIsStated$ ^docs/architecture\.md$
./internal/enginehost ^(TestProjectRelease_RepoStepsAllNameKinds|TestProjectMigrations_RepoDeclaresTheStoreMigrations|TestProjectRunner_RepoProjectFileParses)$ ^\.orrery/project\.toml$
./cmd/orreryd ^TestMetrics ^observability/
./internal/holder . ^internal/enginehost/testdata/
./internal/engine/session . ^internal/engine/capture/testdata/
'
scan_runs=""
if [ "$all_go" = no ]; then
    while read -r scan_pkg scan_pattern scan_paths; do
        if [ -n "$scan_pkg" ] && has "$scan_paths" && [[ " $pkgs " != *" $scan_pkg "* ]]; then
            scan_runs="$scan_runs $scan_pkg|$scan_pattern"
        fi
    done <<< "$scans"
fi

if [ -n "$pkgs" ] || [ -n "$scan_runs" ]; then
    if [ "$pkgs" = "./..." ]; then
        echo "check-changed: Go packages: $pkgs"
        golangci-lint run
    elif [ -n "$pkgs" ]; then
        echo "check-changed: Go packages: $pkgs"
        # shellcheck disable=SC2086 # word splitting is the package list
        golangci-lint run $pkgs
    fi
    dir=$(mktemp -d /tmp/orrery-tmux.XXXXXX)
    trap 'rm -rf "$dir"' EXIT
    # Each test's artifact directory, which holds the logs of the processes it starts, is kept under test-artifacts,
    # so a run that fails or times out leaves them on disk.
    artifacts="$PWD/test-artifacts"
    rm -rf "$artifacts" && mkdir "$artifacts"
    # Each test binary runs through scripts/test-deadline.sh, which appends the package's CPU seconds to the record for
    # the budget check below. No -test.timeout is set here, so go test's own per-binary timeout is what ends a hang.
    record="$artifacts/test-budget.record"
    : >"$record"
    if [ -n "$pkgs" ]; then
        # shellcheck disable=SC2086
        env -u TMUX -u TMUX_PANE TMUX_TMPDIR="$dir" ORRERY_TEST_CPU_FILE="$record" \
            go test -exec "$PWD/scripts/test-deadline.sh" -count=1 -race -artifacts -outputdir "$artifacts" $pkgs
    fi
    for scan in $scan_runs; do
        echo "check-changed: repo-wide scan ${scan#*|} in ${scan%%|*}"
        env -u TMUX -u TMUX_PANE TMUX_TMPDIR="$dir" ORRERY_TEST_CPU_FILE="$record" \
            go test -exec "$PWD/scripts/test-deadline.sh" -count=1 -race -artifacts -outputdir "$artifacts" \
            -run "${scan#*|}" "${scan%%|*}"
    done
    sock_dir="$dir/tmux-$(id -u)"
    if [ -d "$sock_dir" ]; then
        leftover=$(find "$sock_dir" -mindepth 1 -maxdepth 1 -type s)
        if [ -n "$leftover" ]; then
            echo "tests left tmux sockets behind (stop each -L server with tmuxtest.Stop):" >&2
            echo "$leftover" >&2
            exit 1
        fi
    fi
    # One line per package over its CPU budget. The check is advisory and exits 0 whatever the lines say.
    scripts/test-budget.sh check --lines "$artifacts/test-budget.lines" "$record"
    lines_file="$artifacts/test-budget.lines"
else
    echo "check-changed: no Go package affected"
fi

if has '^docs/'; then just doc-cites; fi
if has '^internal/store/.*\.sql$' || has '^sqlc\.yaml$'; then just sqlc-diff; fi
if has '^proto/'; then just proto-lint proto-breaking gen-diff; fi
if printf '%s\n' "$changed" | grep -E '^(web/|android/app/src/main/kotlin/io/github/joshsymonds/orrery/ui/)' | grep -vqE '^web/build/'; then just web-check web-page; fi
if has '^observability/'; then just alert-check; fi
if has '^scripts/gate-remote(-test)?\.sh$'; then just gate-remote-test; fi
if has '^scripts/box-cache(-test)?\.sh$'; then just box-cache-test; fi
if has '^scripts/test-deadline(-test)?\.sh$'; then just test-deadline-test; fi
if has '^scripts/test-budget(-test)?\.sh$'; then just test-budget-test; fi
if has '^android/'; then
    just android-test
else
    echo "check-changed: Android untouched; android-test skipped"
fi

budget_lines
echo "check-changed: OK after $(($(date +%s) - start))s"
