#!/usr/bin/env -S just --justfile

set shell := ["scripts/in-devenv", "-uc"]
# [script] recipes run through the same script, in bash with the devenv environment loaded.
set script-interpreter := ["scripts/in-devenv", "-euo", "pipefail"]

# Wall-clock budget (seconds) for the test suite, a hang guard rather than a
# performance target. Override on the CLI: just TEST_BUDGET=60 check
TEST_BUDGET := "360"

# Wall-clock budget (seconds) for check-changed's Go tests. Override: just CHECK_CHANGED_BUDGET=600 check-changed
CHECK_CHANGED_BUDGET := "600"

# Wall-clock budget (seconds) for web-check's node run, the whole web suite. Override: just WEB_TEST_BUDGET=300 web-check
WEB_TEST_BUDGET := "120"

# List available recipes.
default:
    @just --list

# Run golangci-lint.
lint:
    golangci-lint run

# Measure, on demand and never in check, how soon a restart over a large store serves sessions.sock, against the
# contract's restart bound (DL11). Run it on an idle machine: the time counts real store writes, which load inflates.
measure-restart-cost:
    env -u TMUX -u TMUX_PANE go test -count=1 -tags measure -run '^TestMeasureRestartCost$' -v ./internal/integration/

# Replay the orrery alert rules against their promtool unit tests.
alert-check:
    promtool test rules observability/orrery-rules.test.yml

# Run tests with race and coverage, and enforce at least 80% total coverage
# excluding cmd/ entry points and the generated code in internal/gen.
# -coverpkg=./... attributes coverage across package boundaries.
[script]
cover:
    dir=$(mktemp -d /tmp/orrery-tmux.XXXXXX)
    trap 'rm -rf "$dir"' EXIT
    rc=0
    # Each test binary runs with -test.timeout set to what is left until 10 s before the budget ends
    # (scripts/test-deadline.sh), so a package still running then panics listing its running tests, which go test
    # prints, instead of ending without a word when timeout kills go test. A failure at or past that deadline is the
    # budget's.
    deadline=$(($(date +%s) + {{TEST_BUDGET}} - 10))
    # Each test's artifact directory, which holds the logs of the processes it starts, is kept under test-artifacts,
    # so a run that fails or times out leaves them on disk.
    artifacts="$PWD/test-artifacts"
    rm -rf "$artifacts" && mkdir "$artifacts"
    # scripts/test-deadline.sh appends each package's CPU seconds to the record, checked against the baseline below.
    record="$artifacts/test-budget.record"
    : >"$record"
    env -u TMUX -u TMUX_PANE TMUX_TMPDIR="$dir" ORRERY_TEST_DEADLINE="$deadline" ORRERY_TEST_CPU_FILE="$record" timeout "{{TEST_BUDGET}}" go test -exec "$PWD/scripts/test-deadline.sh" -count=1 -race -covermode=atomic -coverpkg=./... -coverprofile="$PWD/coverage.out" -artifacts -outputdir "$artifacts" ./... || rc=$?
    if [ "$rc" -eq 124 ] || { [ "$rc" -ne 0 ] && [ "$(date +%s)" -ge "$deadline" ]; }; then
        echo "tests exceeded {{TEST_BUDGET}}s budget" >&2
        exit 124
    elif [ "$rc" -ne 0 ]; then
        exit "$rc"
    fi

    # A test that kills its -L server without tmuxtest.Stop leaves the socket file in this directory. -count=1
    # above keeps a cached pass from skipping a leaking test on a rerun.
    sock_dir="$dir/tmux-$(id -u)"
    if [ ! -d "$sock_dir" ]; then
        echo "tmux socket directory $sock_dir is missing" >&2
        exit 1
    fi
    leftover=$(find "$sock_dir" -mindepth 1 -maxdepth 1 -type s)
    if [ -n "$leftover" ]; then
        echo "tests left tmux sockets behind (stop each -L server with tmuxtest.Stop):" >&2
        echo "$leftover" >&2
        exit 1
    fi
    echo "tmux socket check: no sockets left in $sock_dir"

    # cmd/ holds entry-point glue and internal/gen holds generated code; neither is unit-tested code we author.
    grep -vE '/cmd/|/internal/gen/' coverage.out > coverage.cov.out

    pct=$(go tool cover -func=coverage.cov.out | awk '/^total:/{gsub(/%/, "", $NF); print $NF}')
    ok=$(awk -v p="$pct" 'BEGIN { print (p+0 >= 80.0) ? "yes" : "no" }')
    if [ "$ok" = "no" ]; then
        echo "coverage ${pct}% is below 80% threshold" >&2
        exit 1
    fi
    echo "coverage: ${pct}% (>= 80% OK)"

    # One line per package over its CPU budget; exit 3 when one is over twice its baseline. The lines go to
    # test-artifacts/test-budget.lines too, which check prints again as its last output.
    scripts/test-budget.sh check --lines "$artifacts/test-budget.lines" "$record"

# Lint the protobuf contract in proto/.
proto-lint:
    cd proto && buf lint

# Fail when proto/ breaks wire compatibility with the newest tag in HEAD's history that does not point at HEAD. CI
# fetches tags, and a checkout with no such tag fails here rather than skipping the check.
[script]
proto-breaking:
    head=$(git rev-parse HEAD)
    previous=""
    for tag in $(git tag --list --merged HEAD --sort=-v:refname); do
        if [ "$(git rev-list -n 1 "$tag")" != "$head" ]; then
            previous="$tag"
            break
        fi
    done
    if [ -z "$previous" ]; then
        echo "proto-breaking: no tag other than HEAD's exists to compare against; fetch tags (CI needs fetch-tags)" >&2
        exit 1
    fi

    # buf reads the previous tag's module from a directory, so the check does not depend on a .git path, which is a
    # file in a linked worktree.
    dir=$(mktemp -d /tmp/orrery-breaking.XXXXXX)
    trap 'rm -rf "$dir"' EXIT
    git archive "$previous" proto | tar -x -C "$dir"
    cd proto && buf breaking --against "$dir/proto"

# Regenerate the store's typed queries (internal/store/sqlcgen), the typed API (internal/gen), and the app's typed
# client (android/app/src/main/java, orrery.v1 only) from proto/.
generate:
    sqlc generate
    cd proto && buf generate
    rm -rf android/app/src/main/java/io/github/joshsymonds/orrery/gen
    cd proto && buf generate --template buf.gen.android.yaml --path orrery/v1
    rm -rf web/src/gen
    cd proto && buf generate --template buf.gen.web.yaml --path orrery/v1

# Fail when the generated store code drifts from its SQL.
sqlc-diff:
    sqlc diff

# Fail when the generated API code drifts from proto/ (regenerates, then diffs).
gen-diff: generate
    git diff --exit-code -- internal/gen
    git diff --exit-code HEAD -- android/app/src/main/java
    untracked=$(git ls-files --others --exclude-standard -- android/app/src/main/java); [ -z "$untracked" ] || { echo "generated app files not committed:" >&2; echo "$untracked" >&2; exit 1; }
    git diff --exit-code HEAD -- web/src/gen
    untracked=$(git ls-files --others --exclude-standard -- web/src/gen); [ -z "$untracked" ] || { echo "generated web files not committed:" >&2; echo "$untracked" >&2; exit 1; }

# Regenerate the devenv environment snapshot now, even when it is fresh.
[script("scripts/in-devenv", "--refresh")]
refresh-env:
    echo "regenerated .devenv-shell-env.sh"

# Fail when devenv.lock and flake.lock resolve nixpkgs to different revs.
pin-check:
    scripts/check-nix-pin.sh

# Fail when docs/ cites code by file:line instead of by symbol.
doc-cites:
    scripts/check-doc-cites.sh

# Move the shared nixpkgs pin. Rewrites the rev literal in BOTH devenv.yaml and
# flake.nix, refreshes both locks with their own tools, regenerates the env
# snapshot, then runs the pin check. This is the ONLY sanctioned way to move
# the pin: never hand-edit the locks.
# REF is a 40-char rev used verbatim, or a branch name resolved via git
# ls-remote against github.com/NixOS/nixpkgs.
[script]
bump-nix $ref="nixpkgs-unstable":
    if [[ "$ref" =~ ^[0-9a-f]{40}$ ]]; then
        rev="$ref"
    else
        rev="$(git ls-remote https://github.com/NixOS/nixpkgs "refs/heads/$ref" | cut -f1)"
        if [ -z "$rev" ]; then
            echo "bump-nix: refs/heads/$ref did not resolve on github.com/NixOS/nixpkgs" >&2
            exit 1
        fi
    fi
    echo "Bumping nixpkgs pin to $rev..."

    # Rewrite each pin file's single URL literal. Refuse unless the anchored
    # pattern matches exactly once — zero or several means the file's shape
    # changed and the sed would silently do the wrong thing.
    rewrite() {
        local file="$1" pattern="$2" replacement="$3" count
        count="$(grep -cE "$pattern" "$file" || true)"
        if [ "$count" -ne 1 ]; then
            echo "bump-nix: expected exactly 1 line matching $pattern in $file, found $count" >&2
            exit 1
        fi
        sed -i -E "s|$pattern|$replacement|" "$file"
    }
    rewrite devenv.yaml \
        '^    url: github:NixOS/nixpkgs/[^ ]+$' \
        "    url: github:NixOS/nixpkgs/$rev"
    rewrite flake.nix \
        '^  inputs\.nixpkgs\.url = "github:NixOS/nixpkgs/[^"]+";$' \
        "  inputs.nixpkgs.url = \"github:NixOS/nixpkgs/$rev\";"

    devenv update nixpkgs
    nix flake update nixpkgs
    just refresh-env

    scripts/check-nix-pin.sh

# Typecheck the web client, then compile its tests with esbuild and run them with node --test. Offline: the
# dependencies come from the web/node_modules the devenv shell links from nix.
[script]
web-check:
    scripts/gen-stories.sh
    tsc --noEmit -p web
    rm -rf web/build
    tests=$(find web/src \( -name '*.test.ts' -o -name '*.test.tsx' \) | sort)
    if [ -z "$tests" ]; then
        echo "web-check: no tests under web/src" >&2
        exit 1
    fi
    esbuild $tests --bundle --platform=node --format=esm --outbase=web/src --outdir=web/build --out-extension:.js=.mjs --log-level=warning
    # A test still running after 5 s fails, named, rather than holding its file until the gate's own limit ends it
    # with no test named. Most tests here take milliseconds and the slowest a few hundred, so 5 s bounds a hang, not a
    # slow test. The whole run is bounded too: past WEB_TEST_BUDGET seconds timeout ends it, and the recipe fails
    # saying so, so a file that hangs outside any test cannot hold the gate.
    rc=0
    timeout {{WEB_TEST_BUDGET}} node --test --test-timeout=5000 $(find web/build -name '*.test.mjs' | sort) || rc=$?
    if [ "$rc" -eq 124 ]; then
        echo "web-check: the web suite exceeded {{WEB_TEST_BUDGET}}s budget" >&2
    fi
    exit "$rc"

# Bundle the web page into web/build/page, the directory the flake package embeds. scripts/build-web.sh holds the
# bundle command, which the flake calls too.
web-page:
    scripts/build-web.sh web/build/page

# Build the story catalog into web/build/catalog: the registry of every *.stories.tsx, the index page, and the bundle of
# the catalog's main.tsx with the stylesheet. scripts/gen-stories.sh writes the registry first. The catalog recipes run
# this one first.
[script]
_catalog-build:
    scripts/gen-stories.sh
    rm -rf web/build/catalog
    mkdir -p web/build/catalog
    cp web/src/catalog/index.html web/build/catalog/index.html
    esbuild web/src/catalog/main.tsx web/src/style.css --bundle --format=esm --outdir=web/build/catalog "--entry-names=[name]" "--loader:.ttf=file" "--asset-names=[name]" --log-level=warning

# Serve the story catalog at HOST:PORT and rebuild it on each edit. The index lists every story and frames the one the hash
# names at 1600, 1000 and 412 wide. A story's own address, ?story=<name>, draws it alone. Open the address it serves.
catalog HOST="127.0.0.1" PORT="8123": _catalog-build
    esbuild web/src/catalog/main.tsx web/src/style.css --bundle --format=esm --outdir=web/build/catalog "--entry-names=[name]" "--loader:.ttf=file" "--asset-names=[name]" --log-level=warning --watch --serve={{HOST}}:{{PORT}} --servedir=web/build/catalog

# Write a screenshot of each named story of the catalog at each --width, into web/build/shots/<component>/<state>-<width>.png,
# with motion frozen. A NAME is component/state, or a bare component for all its states; without --width the width is
# 1600. An unknown name, or no name, fails with exit 1. It runs under the visual devenv profile, which carries the Chromium
# that playwright-core drives. The driver is web/src/shoot/shoot.ts.
[positional-arguments]
[script("devenv", "--profile", "visual", "shell", "--", "bash", "-euo", "pipefail")]
shoot *ARGS: _catalog-build
    esbuild web/src/shoot/shoot.ts --bundle --platform=node --format=esm --external:playwright-core --outfile=web/build/shoot-tool/shoot.mjs --log-level=warning
    node web/build/shoot-tool/shoot.mjs web/build/catalog "$@"

# Write round 6's names to web/src/catalog/round6.json: each catalogue cell's Component/State, with the states of each
# component, and each scenario's id with the frames that draw it. They are read from the wireframe in
# web/visual/wireframe/round6 in Chromium, under the visual devenv profile, which carries the Chromium that
# playwright-core drives. The driver is web/src/round6names/round6names.ts.
[script("devenv", "--profile", "visual", "shell", "--", "bash", "-euo", "pipefail")]
round6-names:
    esbuild web/src/round6names/round6names.ts --bundle --platform=node --format=esm --external:playwright-core --outfile=web/build/round6names-tool/round6names.mjs --log-level=warning
    node web/build/round6names-tool/round6names.mjs web/visual/wireframe/round6 web/src/catalog/round6.json

# Lint, coverage, typed-client, and generated-code gates. The pin check runs first. cover's test-budget lines are
# printed again last: a gate keeps only the tail of its output, and the checks after cover print far more than it keeps.
check: reap-test-tmux pin-check doc-cites lint proto-lint proto-breaking cover sqlc-diff gen-diff web-check web-page alert-check gate-remote-test box-cache-test test-deadline-test test-budget-test
    cat test-artifacts/test-budget.lines

# Stop the tmux servers tests started and outlived: a test binary that is killed, times out or panics runs no cleanup,
# and its -L server runs on as a daemon. Only a server named orrery-<word>(-<word>)*-<pid>-<hex>, as tests name theirs,
# whose test binary <pid> is gone is stopped, with SIGTERM and then SIGKILL (internal/tmuxtest.Reaper). check and
# check-changed run it first.
reap-test-tmux:
    go run ./internal/tmuxtest/cmd/tmuxreap

# The fast gate for a task in progress: run only the checks the changes since BASE can affect (BASE defaults to the
# merge base of HEAD and origin/main). Integration, review and release keep the full `check` and `android-test`.
check-changed BASE="": reap-test-tmux
    CHECK_CHANGED_BUDGET={{CHECK_CHANGED_BUDGET}} scripts/check-changed.sh {{BASE}}

# Run a gate command on the orrery-gate EC2 box. When the box is gone it waits for the one its wake brings up, and runs
# here through /tmp/claude-1000/heavy only when no box can come up; see scripts/gate-remote.sh for the steps, the
# environment and the exit codes (75 is an environment result, such as a spot interruption, never a test failure).
# For example: just remote just check, just remote nix flake check.
[positional-arguments]
remote *args:
    scripts/gate-remote.sh "$@"

# `just check` on the orrery-gate box.
remote-check:
    scripts/gate-remote.sh just check

# `just android-test` on the orrery-gate box.
remote-android-test:
    scripts/gate-remote.sh just android-test

# `nix flake check` on the orrery-gate box.
remote-flake-check:
    scripts/gate-remote.sh nix flake check

# Test scripts/gate-remote.sh against a fake box: a directory and a fake ssh, no AWS and no network.
gate-remote-test:
    scripts/gate-remote-test.sh

# Test scripts/box-cache.sh, the gate box's content-keyed caches, against a fixture checkout and stub tools.
box-cache-test:
    scripts/box-cache-test.sh

# With ORRERY_TEST_CPU_FILE set, scripts/test-deadline.sh also records each test binary's CPU seconds.
# Test scripts/test-deadline.sh, the go test -exec program that gives each test binary what is left of the budget.
test-deadline-test:
    scripts/test-deadline-test.sh

# Test scripts/test-budget.sh, the per-package CPU budget check, against synthetic records and baselines.
test-budget-test:
    scripts/test-budget-test.sh

# Two runs of cover's `go test -race -coverpkg=./... ./...`, the gate's full test run, each package keeping the lower of
# its two CPU seconds. A failing or timed-out run writes nothing. Run it on an idle machine, as load inflates CPU.
# Re-measure every package's test CPU seconds and rewrite the baseline, testdata/test-budget.json.
[script]
test-budget-update: reap-test-tmux
    dir=$(mktemp -d /tmp/orrery-budget.XXXXXX)
    trap 'rm -rf "$dir"' EXIT
    for run in 1 2; do
        mkdir "$dir/tmux$run"
        : >"$dir/run$run"
        deadline=$(($(date +%s) + {{TEST_BUDGET}} - 10))
        rc=0
        env -u TMUX -u TMUX_PANE TMUX_TMPDIR="$dir/tmux$run" ORRERY_TEST_DEADLINE="$deadline" ORRERY_TEST_CPU_FILE="$dir/run$run" timeout "{{TEST_BUDGET}}" go test -exec "$PWD/scripts/test-deadline.sh" -count=1 -race -covermode=atomic -coverpkg=./... -coverprofile="$dir/cover$run.out" ./... || rc=$?
        if [ "$rc" -ne 0 ]; then
            echo "test-budget-update: run $run failed (exit $rc); testdata/test-budget.json is unchanged" >&2
            exit "$rc"
        fi
    done
    mkdir -p testdata
    scripts/test-budget.sh update testdata/test-budget.json "$dir/run1" "$dir/run2" >&2
    # The baseline goes to stdout too, which is how remote-test-budget-update brings it back from the box.
    cat testdata/test-budget.json

# A run that fails writes nothing. When no box can come up, test-budget-update runs here instead.
# Run test-budget-update on the orrery-gate box, where the gate's check-changed runs, and write its baseline here.
[script]
remote-test-budget-update:
    out=$(mktemp)
    trap 'rm -f "$out" "$out.json"' EXIT
    scripts/gate-remote.sh just test-budget-update >"$out"
    # The baseline is the last block from a line "{" to a line "}" on the run's stdout.
    awk '/^\{$/ { block = ""; on = 1 } on { block = block $0 "\n" } /^\}$/ { on = 0; last = block } END { printf "%s", last }' "$out" >"$out.json"
    if ! jq -e 'type == "object" and length > 0' "$out.json" >/dev/null 2>&1; then
        echo "remote-test-budget-update: the run printed no baseline; testdata/test-budget.json is unchanged" >&2
        exit 1
    fi
    mkdir -p testdata
    cp "$out.json" testdata/test-budget.json
    echo "remote-test-budget-update: wrote $(jq length testdata/test-budget.json) packages to testdata/test-budget.json"

# Assemble the debug APK, then run the Android lint and JVM unit tests.
# On a gate box (ORRERY_BOX_CACHE set by scripts/box-cache.sh run), it runs under scripts/box-cache.sh android, which
# restores and saves Gradle's caches and the android shell's Nix store paths.
android-test:
    ${ORRERY_BOX_CACHE:+scripts/box-cache.sh android "$ORRERY_BOX_CACHE"} nix develop .#android -c gradle -p android assembleDebug lintDebug test

# Build the debug APK and install it on the connected device. A fresh install
# starts with $ORRERY_DEFAULT_SERVER as its server address (an install that
# saved one in settings keeps it); unset, the app asks for the server.
android-install:
    nix develop .#android -c bash -ceu '\
        gradle -p android assembleDebug -Porrery.defaultServer="${ORRERY_DEFAULT_SERVER:-}"; \
        adb install -r android/app/build/outputs/apk/debug/app-debug.apk'

# Regenerate the Orrery Mono subsets (Regular, Bold, Italic) of Maple Mono NF
# from the flake's pinned nixpkgs, renamed because the OFL reserves the name.
[script]
android-font:
    src=$(nix build --no-link --print-out-paths --inputs-from . nixpkgs#maple-mono.NF-CN-unhinted)
    tmp=$(mktemp -d)
    trap 'rm -rf "$tmp"' EXIT
    nix shell --inputs-from . nixpkgs#python3Packages.fonttools -c bash -euo pipefail -s "$src" "$tmp" "$PWD/android/app/src/main/res" <<'SCRIPT'
    src=$1 tmp=$2 res=$3
    unicodes='U+0020-007E,U+00A0-024F,U+2000-206F,U+2190-21FF,U+2200-22FF,U+2300-23FF,U+2500-259F,U+25A0-25FF,U+2600-27BF,U+2800-28FF,U+2B00-2BFF,U+E000-F8FF,U+F0000-F1FFF'
    mkdir -p "$res/font"
    for style in Regular Bold Italic; do
        lower=$(printf '%s' "$style" | tr '[:upper:]' '[:lower:]')
        pyftsubset "$src/share/fonts/truetype/MapleMono-NF-CN-$style.ttf" \
            --output-file="$tmp/$lower.ttf" --unicodes="$unicodes" --layout-features='*' --name-IDs='*'
        ttx -q -t name -f -o "$tmp/$lower.name.ttx" "$tmp/$lower.ttf"
        sed -i -e 's/Maple Mono NF CN/Orrery Mono/g' -e 's/MapleMono-NF-CN-/OrreryMono-/g' "$tmp/$lower.name.ttx"
        ttx -q --no-recalc-timestamp -m "$tmp/$lower.ttf" -o "$res/font/orrery_mono_$lower.ttf" "$tmp/$lower.name.ttx"
    done
    SCRIPT

# Rerun the Claude Code protocol probes live against the claude on PATH (spends Haiku turns on your sign-in; not part of check).
engine-accept *args:
    go run ./internal/engine/capture/cmd/enginecapture accept {{args}}

# Regenerate docs/img/tree.png: the real `orrery tree`, built from this tree,
# full-frame in VHS's terminal (Catppuccin Mocha, Maple Mono NF) against the
# private tmux server that docs/tui/fixture.sh start builds. Runs under the
# tui-shots devenv profile, so vhs, ttyd and chromium stay out of the default
# shell. docs/img/tree.png is replaced only after the frame's text shows every
# project and pane label of the fixture. Exit stops the fixture server and
# removes the temporary directory; VHS stops its own ttyd and browser when it
# exits normally, but a crashed VHS can leave them running.
[script("devenv", "--profile", "tui-shots", "shell", "--", "bash", "-euo", "pipefail")]
tui-shots:
    tmp="$(mktemp -d)"
    trap 'bash "$PWD/docs/tui/fixture.sh" stop; rm -rf "$tmp"' EXIT

    # Register only Maple Mono NF with fontconfig, in the temporary directory,
    # and refuse to render unless fc-match resolves it.
    cat > "$tmp/fonts.conf" <<EOF
    <?xml version="1.0"?>
    <!DOCTYPE fontconfig SYSTEM "fonts.dtd">
    <fontconfig>
      <dir>$ORRERY_TUI_FONT_DIR</dir>
      <cachedir>$tmp/fontcache</cachedir>
      <selectfont>
        <acceptfont>
          <pattern>
            <patelt name="family"><string>Maple Mono NF</string></patelt>
          </pattern>
        </acceptfont>
      </selectfont>
    </fontconfig>
    EOF
    export FONTCONFIG_FILE="$tmp/fonts.conf"
    family="$(fc-match -f '%{family}' 'Maple Mono NF')"
    case "$family" in
        "Maple Mono NF" | "Maple Mono NF,"*) ;;
        *) echo "tui-shots: fc-match resolves 'Maple Mono NF' to '$family'; ORRERY_TUI_FONT_DIR must hold Maple Mono NF" >&2; exit 1 ;;
    esac

    # ttyd (VHS's terminal) needs its libwebsockets plugin directory on the library path.
    lwsdir="$(dirname "$(ldd "$(command -v ttyd)" | awk '/libwebsockets/{print $3}')")"
    export LD_LIBRARY_PATH="$lwsdir${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"

    # The tape's shell and VHS's browser keep their temporary files in $tmp.
    export TMPDIR="$tmp" COLORTERM=truecolor XDG_STATE_HOME=/tmp/orrery-tui-shots/state
    unset TMUX TMUX_PANE

    mkdir "$tmp/bin"
    go build -o "$tmp/bin/orrery" ./cmd/orrery
    export PATH="$tmp/bin:$PATH"

    bash docs/tui/fixture.sh start

    # Read the expected rows from the private server before VHS runs, so the
    # fixture and this check cannot drift apart.
    tmux -S /tmp/orrery-tui-shots/tmux.sock list-panes -a -F '#{pane_current_path}' | sort -u > "$tmp/projects.txt"
    tmux -S /tmp/orrery-tui-shots/tmux.sock list-panes -a -F '#{@orrery_label}' | sort -u > "$tmp/labels.txt"
    if [ ! -s "$tmp/projects.txt" ] || [ ! -s "$tmp/labels.txt" ]; then
        echo "tui-shots: the fixture server reports no panes; docs/img/tree.png is unchanged" >&2
        exit 1
    fi

    # VHS writes a gif and the frame's text into $tmp; ffmpeg takes the settled
    # last frame as the PNG, also in $tmp.
    tape="$PWD/docs/tui/tree.tape"
    (cd "$tmp" && vhs "$tape")

    # tree.txt holds one block per frame, each closed by a rule of ─. The last
    # block is the settled frame the PNG is taken from, so the tree rendered
    # only if that block holds every expected row.
    awk '/^(─)+$/ { last = block; block = ""; next } { block = block $0 "\n" } END { printf "%s", last }' \
        "$tmp/tree.txt" > "$tmp/frame.txt"
    missing=()
    while IFS= read -r row; do
        grep -qF -- "$row" "$tmp/frame.txt" || missing+=("$row")
    done < <(cat "$tmp/projects.txt" "$tmp/labels.txt")
    if [ "${#missing[@]}" -gt 0 ]; then
        echo "tui-shots: the rendered frame lacks: ${missing[*]}; docs/img/tree.png is unchanged" >&2
        exit 1
    fi

    ffmpeg -y -loglevel error -sseof -0.3 -i "$tmp/tree.gif" -update 1 -frames:v 1 "$tmp/tree.png"
    mkdir -p docs/img
    mv -f "$tmp/tree.png" docs/img/tree.png
    echo "tui-shots: wrote docs/img/tree.png"
