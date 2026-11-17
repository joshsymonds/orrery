#!/usr/bin/env -S just --justfile

set shell := ["scripts/in-devenv", "-uc"]
# [script] recipes run through the same script, in bash with the devenv environment loaded.
set script-interpreter := ["scripts/in-devenv", "-euo", "pipefail"]

# Wall-clock budget (seconds) for the test suite, a hang guard rather than a
# performance target. Override on the CLI: just TEST_BUDGET=60 check
TEST_BUDGET := "360"

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
    env -u TMUX -u TMUX_PANE TMUX_TMPDIR="$dir" ORRERY_TEST_DEADLINE="$deadline" timeout "{{TEST_BUDGET}}" go test -exec "$PWD/scripts/test-deadline.sh" -count=1 -race -covermode=atomic -coverpkg=./... -coverprofile="$PWD/coverage.out" -artifacts -outputdir "$artifacts" ./... || rc=$?
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
    tsc --noEmit -p web
    rm -rf web/build
    tests=$(find web/src \( -name '*.test.ts' -o -name '*.test.tsx' \) | sort)
    if [ -z "$tests" ]; then
        echo "web-check: no tests under web/src" >&2
        exit 1
    fi
    esbuild $tests --bundle --platform=node --format=esm --outbase=web/src --outdir=web/build --out-extension:.js=.mjs --log-level=warning
    # A test still running after a minute fails, named, rather than holding its file until the gate's own limit ends
    # it with no test named: every test here takes milliseconds, so the minute bounds a hang, not a slow test.
    node --test --test-timeout=60000 $(find web/build -name '*.test.mjs' | sort)

# Bundle the web page into web/build/page, the directory the flake package embeds. scripts/build-web.sh holds the
# bundle command, which the flake calls too.
web-page:
    scripts/build-web.sh web/build/page

# Capture the web client against its accepted wireframe: every scenario of web/visual/map.json at widths 1600 and 1000,
# each side's screenshot and web/build/visual/report.json. The check compares nothing and exits 0 whatever it finds; a
# run that cannot start exits 1. It runs under the visual devenv profile, which carries Chromium and the prose font, so
# the default shell and the gates stay free of them. The driver is web/src/visual/report-main.ts.
[script("devenv", "--profile", "visual", "shell", "--", "bash", "-euo", "pipefail")]
visual-report: _visual-build
    rm -rf web/build/visual
    node web/build/visual-tool/visual/report-main.mjs

# Check the web client against its accepted wireframe: each named page of web/visual/map.json, or every page when none is
# named. It writes the screenshots and web/build/visual/report.json, prints each failure and exits 0 when the pass/fail
# decision passes and 1 when it fails; a run that cannot start exits 1. It runs under the visual devenv profile, as
# visual-report does. The entry point is web/src/visual/check-main.ts.
[positional-arguments]
[script("devenv", "--profile", "visual", "shell", "--", "bash", "-euo", "pipefail")]
visual-check *PAGES: _visual-build
    node web/build/visual-tool/visual/check-main.mjs "$@"

# The build the visual recipes share: the page under test in web/build/page, and the node tools compiled from
# web/src/visual into web/build/visual-tool with playwright-core left external. It runs under the visual devenv profile.
[script("devenv", "--profile", "visual", "shell", "--", "bash", "-euo", "pipefail")]
_visual-build:
    scripts/build-web.sh web/build/page
    rm -rf web/build/visual-tool
    esbuild $(find web/src/visual -name '*.ts' ! -name '*.test.ts' | sort) --bundle --platform=node --format=esm --outbase=web/src --outdir=web/build/visual-tool --out-extension:.js=.mjs --external:playwright-core --log-level=warning

# Lint, coverage, typed-client, and generated-code gates. The pin check runs first.
check: reap-test-tmux pin-check doc-cites lint proto-lint proto-breaking cover sqlc-diff gen-diff web-check web-page alert-check gate-remote-test test-deadline-test

# Stop the tmux servers tests started and outlived: a test binary that is killed, times out or panics runs no cleanup,
# and its -L server runs on as a daemon. Only a server named orrery-<word>(-<word>)*-<pid>-<hex>, as tests name theirs,
# whose test binary <pid> is gone is stopped, with SIGTERM and then SIGKILL (internal/tmuxtest.Reaper). check and
# check-changed run it first.
reap-test-tmux:
    go run ./internal/tmuxtest/cmd/tmuxreap

# The fast gate for a task in progress: run only the checks the changes since BASE can affect (BASE defaults to the
# merge base of HEAD and origin/main). Integration, review and release keep the full `check` and `android-test`.
check-changed BASE="": reap-test-tmux
    scripts/check-changed.sh {{BASE}}

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

# Test scripts/test-deadline.sh, the go test -exec program that gives each test binary what is left of the budget.
test-deadline-test:
    scripts/test-deadline-test.sh

# Assemble the debug APK, then run the Android lint and JVM unit tests.
android-test:
    nix develop .#android -c gradle -p android assembleDebug lintDebug test

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
