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

# Run tests with race and coverage, and enforce at least 80% total coverage
# excluding cmd/ entry points and the generated code in internal/gen.
# -coverpkg=./... attributes coverage across package boundaries.
[script]
cover:
    dir=$(mktemp -d /tmp/orrery-tmux.XXXXXX)
    trap 'rm -rf "$dir"' EXIT
    rc=0
    env -u TMUX -u TMUX_PANE TMUX_TMPDIR="$dir" timeout "{{TEST_BUDGET}}" go test -count=1 -race -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./... || rc=$?
    if [ "$rc" -eq 124 ]; then
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

# Regenerate the store's typed queries (internal/store/sqlcgen), the typed API (internal/gen), and the app's typed
# client (android/app/src/main/java, orrery.v1 only) from proto/.
generate:
    sqlc generate
    cd proto && buf generate
    rm -rf android/app/src/main/java/io/github/joshsymonds/orrery/gen
    cd proto && buf generate --template buf.gen.android.yaml --path orrery/v1

# Fail when the generated store code drifts from its SQL.
sqlc-diff:
    sqlc diff

# Fail when the generated API code drifts from proto/ (regenerates, then diffs).
gen-diff: generate
    git diff --exit-code -- internal/gen
    git diff --exit-code HEAD -- android/app/src/main/java
    untracked=$(git ls-files --others --exclude-standard -- android/app/src/main/java); [ -z "$untracked" ] || { echo "generated app files not committed:" >&2; echo "$untracked" >&2; exit 1; }

# Regenerate the devenv environment snapshot now, even when it is fresh.
[script("scripts/in-devenv", "--refresh")]
refresh-env:
    echo "regenerated .devenv-shell-env.sh"

# Fail when devenv.lock and flake.lock resolve nixpkgs to different revs.
pin-check:
    scripts/check-nix-pin.sh

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

# Lint, coverage, and generated-code gates. The pin check runs first.
check: pin-check lint proto-lint cover sqlc-diff gen-diff

# Assemble the debug APK, then run the Android lint and JVM unit tests.
android-test:
    nix develop .#android -c gradle -p android assembleDebug lintDebug test

# Build the debug APK and install it on the connected device.
android-install:
    nix develop .#android -c bash -ceu '\
        gradle -p android assembleDebug; \
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
