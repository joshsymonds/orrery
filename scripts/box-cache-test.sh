#!/usr/bin/env bash
#
# Tests scripts/box-cache.sh without a box, a network or a Nix store: a fixture repository stands in for the checkout, a
# directory for each box's home, another for the S3 cache mount, and stub go and nix programs for the tools, the stub
# store being a directory of marker files. Covers usage, the go kinds' miss, save, restore and keep, a changed
# key, a failing command, a corrupt entry that warns and still runs the command, an entry another box wrote first, the
# command's ORRERY_BOX_CACHE, and the android kinds: Gradle's caches without their lock files, and the android shell's
# closure exported after a run and imported on a fresh box, which records the key it holds.
#
# Usage: scripts/box-cache-test.sh   (exits 0 when every case passes)

set -euo pipefail

script="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/box-cache.sh"
root="$(mktemp -d)"
trap 'chmod -R u+w -- "$root" 2>/dev/null || true; rm -rf -- "$root"' EXIT

failures=0
fail() {
    echo "FAIL: $*" >&2
    failures=$((failures + 1))
}

# The stub tools. go answers `go env GOVERSION` with $STUB_GO_VERSION. The stub store is $STUB_STORE: a store path
# /nix/store/<name> is in it when $STUB_STORE/<name> exists. The android shell's closure is /nix/store/sdk and
# /nix/store/src: `nix develop --profile P` writes them to P, as the profile's closure, and `nix path-info -r P` reads
# them back.
stubs="$root/stubs"
mkdir -p "$stubs"
cat >"$stubs/go" <<'EOF'
#!/usr/bin/env bash
[ "$1 $2" = "env GOVERSION" ] || exit 2
echo "$STUB_GO_VERSION"
EOF
cat >"$stubs/nix" <<'EOF'
#!/usr/bin/env bash
case "$1" in
    develop)
        [ "$3" = --profile ] && [ "$5 $6" = "--command true" ] || exit 2
        printf '%s\n' /nix/store/sdk /nix/store/src >"$4"
        ;;
    path-info)
        [ "$2" = -r ] || exit 2
        cat "$3"
        ;;
    copy)
        shift
        if [ "$1" = --to ]; then
            dir="${2#file://}"
            dir="${dir%%\?*}"
            shift 2
            mkdir -p "$dir"
            for p in "$@"; do cp "$STUB_STORE/${p##*/}" "$dir/${p##*/}" || exit 1; done
        elif [ "$1 $2" = "--no-check-sigs --from" ]; then
            dir="${3#file://}"
            shift 3
            mkdir -p "$STUB_STORE"
            for p in "$@"; do cp "$dir/${p##*/}" "$STUB_STORE/${p##*/}" || exit 1; done
        else
            exit 2
        fi
        ;;
    *) exit 2 ;;
esac
EOF
chmod +x "$stubs"/*

# The fixture checkout. fixture_reset writes its files as they were committed, undoing a case's edits.
repo="$root/repo"
mkdir -p "$repo/android"
fixture_reset() {
    printf 'module example\n' >"$repo/go.mod"
    printf 'example.com/a v1.0.0 h1:x\n' >"$repo/go.sum"
    printf '{ }\n' >"$repo/flake.nix"
    printf '{"nodes":{}}\n' >"$repo/flake.lock"
    printf 'plugins {}\n' >"$repo/android/build.gradle.kts"
    printf 'org.gradle.jvmargs=-Xmx1g\n' >"$repo/android/gradle.properties"
}
fixture_reset
(
    cd "$repo"
    git init -q
    git add -A
    git -c user.email=t@example.invalid -c user.name=t commit -qm fixture
)
cache="$root/cache"

# box <name>: points HOME, the Go caches and the stub store at box <name>, a fresh box the first time it is named.
box() {
    export HOME="$root/box-$1"
    export GOMODCACHE="$HOME/.cache/orrery/mod" GOCACHE="$HOME/.cache/orrery/build"
    export STUB_STORE="$HOME/store"
    mkdir -p "$GOMODCACHE" "$GOCACHE"
}

export PATH="$stubs:$PATH" STUB_GO_VERSION=go1.26.0
export ORRERY_BOX_CACHE_NIX="$stubs/nix"
unset GRADLE_USER_HOME

# cache_run <out file> <mode> <command...>: runs box-cache.sh from the fixture checkout, stdout and stderr in <out file>,
# and prints its exit status.
cache_run() {
    local out="$1" rc=0
    shift
    (cd "$repo" && "$script" "$1" "$cache" "${@:2}") >"$out" 2>&1 || rc=$?
    echo "$rc"
}

expect_line() {
    grep -qF -- "$2" "$1" || fail "$3: no line '$2' in: $(tr '\n' '|' <"$1")"
}

out="$root/out"

# --- usage ---
rc=0
"$script" >"$out" 2>&1 || rc=$?
[ "$rc" -eq 2 ] || fail "no arguments exited $rc, want 2"
rc=0
(cd "$repo" && "$script" restore "$cache" "$root/state" gomod pip) >"$out" 2>&1 || rc=$?
[ "$rc" -eq 2 ] || fail "an unknown kind exited $rc, want 2"
rc=0
"$script" run "$cache" >"$out" 2>&1 || rc=$?
[ "$rc" -eq 2 ] || fail "run with no command exited $rc, want 2"

# --- a fresh box with an empty cache misses, builds, and saves the go kinds ---
box a
rc="$(cache_run "$out" run bash -c 'echo mod >"$GOMODCACHE/m"; echo build >"$GOCACHE/b"; echo "$ORRERY_BOX_CACHE" >"$HOME/seen"')"
[ "$rc" -eq 0 ] || fail "first run exited $rc: $(cat "$out")"
expect_line "$out" "box-cache: gomod missed" "first run"
expect_line "$out" "box-cache: gobuild missed" "first run"
expect_line "$out" "box-cache: gomod saved" "first run"
expect_line "$out" "box-cache: gobuild saved" "first run"
[ "$(cat "$HOME/seen")" = "$cache" ] || fail "the command saw ORRERY_BOX_CACHE='$(cat "$HOME/seen")', want $cache"
[ "$(find "$cache/gomod" -name '*.tar' | wc -l)" -eq 1 ] || fail "want one gomod entry after the first run"
[ -z "$(find "$cache" -name '.save-*')" ] || fail "a temporary save file was left behind"

# --- a second fresh box restores what the first saved, and saves nothing ---
box b
rc="$(cache_run "$out" run bash -c 'cat "$GOMODCACHE/m" "$GOCACHE/b" >"$HOME/got"')"
[ "$rc" -eq 0 ] || fail "second box exited $rc: $(cat "$out")"
expect_line "$out" "box-cache: gomod restored" "second box"
expect_line "$out" "box-cache: gobuild restored" "second box"
expect_line "$out" "box-cache: gomod unchanged" "second box"
[ "$(cat "$HOME/got")" = "$(printf 'mod\nbuild')" ] || fail "second box read '$(cat "$HOME/got")', want the first box's caches"

# --- a box that already holds its caches keeps them ---
rc="$(cache_run "$out" run true)"
[ "$rc" -eq 0 ] || fail "warm run exited $rc"
expect_line "$out" "box-cache: gomod kept" "warm run"
expect_line "$out" "box-cache: gobuild kept" "warm run"

# --- a Go module cache made read-only, as Go makes it, restores and leaves no staging behind ---
box ro
mkdir -p "$GOMODCACHE/example.com/a@v1.0.0"
echo src >"$GOMODCACHE/example.com/a@v1.0.0/a.go"
chmod -R a-w "$GOMODCACHE/example.com"
(cd "$repo" && printf 'example.com/a v1.0.1 h1:y\n' >go.sum)
rm -rf "$root/state-ro" && mkdir "$root/state-ro"
: >"$root/state-ro/gomod-fresh"
rc=0
(cd "$repo" && "$script" save "$cache" "$root/state-ro" gomod) >"$out" 2>&1 || rc=$?
[ "$rc" -eq 0 ] || fail "saving a read-only module cache exited $rc: $(cat "$out")"
box ro2
rc=0
(cd "$repo" && "$script" restore "$cache" "$root/state-ro" gomod) >"$out" 2>&1 || rc=$?
[ "$rc" -eq 0 ] || fail "restoring a read-only module cache exited $rc: $(cat "$out")"
[ -f "$GOMODCACHE/example.com/a@v1.0.0/a.go" ] || fail "the read-only module cache did not restore"
[ -z "$(find "$HOME/.cache/orrery" -maxdepth 1 -name '.box-cache-*')" ] || fail "a staging folder was left behind"
fixture_reset

# --- a changed key misses ---
box c
(cd "$repo" && printf 'example.com/a v1.0.2 h1:z\n' >go.sum)
rc="$(cache_run "$out" run true)"
[ "$rc" -eq 0 ] || fail "changed-key run exited $rc"
expect_line "$out" "box-cache: gomod missed" "changed go.sum"
STUB_GO_VERSION=go1.27.0
box c2
rc="$(cache_run "$out" run true)"
expect_line "$out" "box-cache: gomod missed" "changed Go version"
STUB_GO_VERSION=go1.26.0
fixture_reset

# --- a failing command keeps its status and saves nothing ---
box d
before="$(find "$cache" -name '*.tar' | wc -l)"
(cd "$repo" && printf 'example.com/a v9.0.0 h1:q\n' >go.sum)
rc="$(cache_run "$out" run bash -c 'echo x >"$GOMODCACHE/m"; exit 3')"
[ "$rc" -eq 3 ] || fail "a failing command's run exited $rc, want 3"
expect_line "$out" "box-cache: the command exited 3; nothing saved" "failing command"
[ "$(find "$cache" -name '*.tar' | wc -l)" -eq "$before" ] || fail "a failing command saved an entry"
fixture_reset

# --- a corrupt entry warns, and the command still runs ---
box e
for f in "$cache"/gomod/*.tar; do printf 'not a tar' >"$f"; done
rc="$(cache_run "$out" run bash -c 'echo ran >"$HOME/ran"')"
[ "$rc" -eq 0 ] || fail "a corrupt entry's run exited $rc, want the command's 0"
[ -f "$HOME/ran" ] || fail "a corrupt entry stopped the command"
expect_line "$out" "box-cache: gomod restore failed" "corrupt entry"
expect_line "$out" "box-cache: warning: the restore failed" "corrupt entry"
rm -f "$cache"/gomod/*.tar

# --- an entry another box wrote first is left alone ---
box f
rc="$(cache_run "$out" run bash -c 'echo mine >"$GOMODCACHE/m"; for d in "$ORRERY_BOX_CACHE"/gomod; do mkdir -p "$d"; done; key="$(ls "$ORRERY_BOX_CACHE/gobuild" | head -1)"; printf theirs >"$ORRERY_BOX_CACHE/gomod/$key"')"
[ "$rc" -eq 0 ] || fail "racing save exited $rc: $(cat "$out")"
expect_line "$out" "box-cache: gomod present" "entry written first"
[ "$(cat "$cache"/gomod/*.tar)" = theirs ] || fail "the entry another box wrote first was replaced"

# --- the android kinds: Gradle's caches and the android shell's store paths ---
box g
rc="$(cache_run "$out" android bash -c 'mkdir -p "$HOME/.gradle/caches/modules-2" "$STUB_STORE"; echo dep >"$HOME/.gradle/caches/modules-2/dep.jar"; : >"$HOME/.gradle/caches/modules-2/modules-2.lock"; echo sdk >"$STUB_STORE/sdk"; echo src >"$STUB_STORE/src"')"
[ "$rc" -eq 0 ] || fail "first android run exited $rc: $(cat "$out")"
expect_line "$out" "box-cache: gradle missed" "first android run"
expect_line "$out" "box-cache: nix missed" "first android run"
expect_line "$out" "box-cache: gradle saved" "first android run"
expect_line "$out" "box-cache: nix saved" "first android run"
tar -tf "$cache"/gradle/*.tar | grep -q 'dep.jar' || fail "the gradle entry lacks the dependency"
! tar -tf "$cache"/gradle/*.tar | grep -q '\.lock$' || fail "the gradle entry holds a lock file"
tar -xOf "$cache"/nix/*.tar paths | grep -qx /nix/store/sdk || fail "the nix entry does not list the SDK path"

box h
rc="$(cache_run "$out" android bash -c 'cat "$HOME/.gradle/caches/modules-2/dep.jar" "$STUB_STORE/sdk" >"$HOME/got"')"
[ "$rc" -eq 0 ] || fail "second android box exited $rc: $(cat "$out")"
expect_line "$out" "box-cache: gradle restored" "second android box"
expect_line "$out" "box-cache: nix restored" "second android box"
[ "$(cat "$HOME/got")" = "$(printf 'dep\nsdk')" ] || fail "second android box read '$(cat "$HOME/got")'"
rc="$(cache_run "$out" android true)"
expect_line "$out" "box-cache: nix kept" "warm android box"

# --- a fresh box that imports the shell's paths records it, and a later run keeps them ---
box j
rc="$(cache_run "$out" android true)"
expect_line "$out" "box-cache: nix restored" "fresh android box"
[ -n "$(find "$HOME/.local/state/orrery" -name 'box-cache-nix-*')" ] || fail "an import recorded no key on the box"
rc="$(cache_run "$out" android true)"
expect_line "$out" "box-cache: nix kept" "a box that imported the key"

# --- a changed flake misses the nix kind and saves a new entry ---
box k
printf '{ inputs = { }; }\n' >"$repo/flake.nix"
rc="$(cache_run "$out" android bash -c 'mkdir -p "$STUB_STORE"; echo sdk2 >"$STUB_STORE/sdk"; echo src >"$STUB_STORE/src"')"
[ "$rc" -eq 0 ] || fail "changed-flake android run exited $rc: $(cat "$out")"
expect_line "$out" "box-cache: nix missed" "changed flake"
expect_line "$out" "box-cache: nix saved" "changed flake"
[ "$(find "$cache/nix" -name '*.tar' | wc -l)" -eq 2 ] || fail "want a second nix entry for the changed flake"
fixture_reset

if [ "$failures" -gt 0 ]; then
    echo "box-cache-test: $failures failed" >&2
    exit 1
fi
echo "box-cache-test: every case passed"
