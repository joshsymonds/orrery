#!/usr/bin/env bash
#
# Box cache: restores and saves what an orrery-gate box builds and downloads for a gate, keyed by content, so a fresh
# box starts from what an earlier box made from the same inputs. A box lives for minutes after its last run, so without
# it every gate on a new box downloads every Go module, rebuilds the Go build cache, and, for the Android checks,
# fetches Gradle's dependencies and builds the unfree Android SDK the Nix caches do not hold.
#
# Usage:
#   scripts/box-cache.sh run <cache-root> <command> [args...]
#       scripts/gate-remote.sh's box run wraps every gate command in this, from the top of the checkout on the box,
#       inside its devenv shell. Restores the go kinds (gomod, gobuild), runs the command with its arguments, each one
#       word and never evaluated, with ORRERY_BOX_CACHE set to <cache-root>, and after a command that exited 0 saves
#       the go kinds. Exits with the command's status.
#   scripts/box-cache.sh android <cache-root> <command> [args...]
#       The Android recipes wrap their `nix develop .#android` command in this when ORRERY_BOX_CACHE names a cache root,
#       so only a gate that runs the Android checks restores and saves the android kinds (gradle, nix). Otherwise as
#       run.
#       In both, a restore or save that fails prints its failure and a warning on stderr, and the run goes on: the cache
#       never fails a gate. After a command that did not exit 0 nothing is saved, and a line says so.
#   scripts/box-cache.sh restore <cache-root> <state-dir> <kind>...
#   scripts/box-cache.sh save <cache-root> <state-dir> <kind>...
#       The two halves of run and android, with the state directory carrying what the restore found to the save. The
#       kinds, each handled in the order given:
#       gomod: the Go module cache, $GOMODCACHE. Its key is the sha256 over `go env GOVERSION` and the content of go.mod
#       and go.sum. Its entry is <cache-root>/gomod/<key>.tar.
#       gobuild: the Go build cache, $GOCACHE, under the same key, as <cache-root>/gobuild/<key>.tar. The build cache is
#       content-addressed by Go itself, so an entry made from other sources is never wrong, only less useful.
#       gradle: Gradle's caches folder, ${GRADLE_USER_HOME:-$HOME/.gradle}/caches, without its lock files and
#       gc.properties. Its key is the sha256 over the content of flake.lock (which pins Gradle) and every git-visible
#       android/ file named *.gradle.kts, gradle.properties or *.versions.toml. Its entry is <cache-root>/gradle/<key>.tar.
#       nix: the Nix store paths `nix develop .#android` needs: the closure of the shell's environment, which a save
#       gets by running `nix develop <shell> --profile <tmp> --command true` after the command (its inputs are then valid,
#       so only the small environment derivation is realised) and `nix path-info -r` of that profile. Its key is the
#       sha256 over the content of flake.nix and flake.lock, which define and pin the shell. Its entry is
#       <cache-root>/nix/<key>.tar, a tar of a file:// binary cache with a file named paths listing the paths. A restore
#       imports the paths with `nix copy --no-check-sigs` (the orrery user is trusted on the box). The box records the
#       key it holds in $HOME/.local/state/orrery/box-cache-nix-<key>, written by a restore or a save, so a later run on
#       the box keeps what it has and neither imports nor saves again.
#       A folder kind restores only into a folder that is missing or empty. A restore that misses writes
#       <state-dir>/<kind>-fresh, and a save writes an entry only when that marker exists and no entry is there yet, so
#       only what a fresh box built after a miss is saved.
#
# Keys are content hashes, never names or times: a changed lock or build file misses and rebuilds, so a gate never runs
# on stale inputs, and a file that was only touched still hits. Every entry is a plain tar, written to a temporary file
# beside it and linked in with mv -n, so an entry another box wrote first is never replaced. Each kind's restore and save
# hold an exclusive flock on $HOME/.local/state/orrery/box-cache-<kind>.lock, so runs that share a box never extract
# into, or archive, a half-written folder.
#
# Environment:
#   ORRERY_BOX_CACHE_NIX    nix command (default nix), split into words
#   ORRERY_BOX_CACHE_SHELL  the android dev shell's flake attribute (default .#android)
#
# Output: one `box-cache: <kind> <line>` line per kind on stdout: restored, missed, kept, saved, present or unchanged,
# with the key where the line names one.
#
# Exit codes: run and android exit with the command's status, 2 for usage. restore and save exit 0 when every kind
# restored, missed, kept, saved, present or unchanged; 1 when an extraction or a save failed, after removing its
# temporary files and printing `box-cache: <kind> <restore|save> failed: <why>` on stderr, the kinds after it not run;
# 2 for usage or an unknown kind.

set -euo pipefail

usage() {
    echo "usage: box-cache.sh run <cache-root> <command> [args...]" >&2
    echo "       box-cache.sh android <cache-root> <command> [args...]" >&2
    echo "       box-cache.sh restore <cache-root> <state-dir> <kind>..." >&2
    echo "       box-cache.sh save <cache-root> <state-dir> <kind>..." >&2
    exit 2
}

[ "$#" -ge 2 ] || usage
mode="$1"
cache="$2"

# wrap <kind>...: the run and android modes: restore the kinds, run the command in "$@" after the kinds' `--`, save the
# kinds after a command that exited 0, and exit with its status.
wrap() {
    local -a kinds=()
    while [ "$1" != -- ]; do
        kinds+=("$1")
        shift
    done
    shift
    local self state rc=0
    self="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "${BASH_SOURCE[0]}")"
    state="$(mktemp -d)"
    trap 'rm -rf -- "$state"' EXIT
    if ! "$self" restore "$cache" "$state" "${kinds[@]}"; then
        echo "box-cache: warning: the restore failed; the command runs on what the box holds" >&2
    fi
    ORRERY_BOX_CACHE="$cache" "$@" || rc=$?
    if [ "$rc" -eq 0 ]; then
        if ! "$self" save "$cache" "$state" "${kinds[@]}"; then
            echo "box-cache: warning: the save failed; the command's status stands" >&2
        fi
    else
        echo "box-cache: the command exited $rc; nothing saved"
    fi
    exit "$rc"
}

case "$mode" in
    run)
        [ "$#" -ge 3 ] || usage
        shift 2
        wrap gomod gobuild -- "$@"
        ;;
    android)
        [ "$#" -ge 3 ] || usage
        shift 2
        wrap gradle nix -- "$@"
        ;;
    restore | save) ;;
    *) usage ;;
esac

[ "$#" -ge 4 ] || usage
state="$3"
shift 3
for kind in "$@"; do
    case "$kind" in
        gomod | gobuild | gradle | nix) ;;
        *) usage ;;
    esac
done

read -ra nix_cmd <<<"${ORRERY_BOX_CACHE_NIX:-nix}"
android_shell="${ORRERY_BOX_CACHE_SHELL:-.#android}"
lock_dir="$HOME/.local/state/orrery"

# fail <kind> <restore|save> <why>: prints the failure on stderr and exits 1. The caller removes its temporary files first.
fail() {
    echo "box-cache: $1 $2 failed: $3" >&2
    exit 1
}

# Reads candidate paths on stdin and prints "<sha256 of content>  <path>" for each one that is a regular file, in C order.
content_lines() {
    local digest
    LC_ALL=C sort | while IFS= read -r path; do
        if [ -f "$path" ]; then
            digest="$(sha256sum <"$path")"
            printf '%s  %s\n' "${digest%% *}" "$path"
        fi
    done
}

go_key() {
    local version
    if ! version="$(go env GOVERSION)"; then
        fail gomod "$mode" "go env GOVERSION exited nonzero"
    fi
    {
        printf '%s\n' "$version"
        printf '%s\n' go.mod go.sum | content_lines
    } | sha256sum | cut -d' ' -f1
}

gradle_key() {
    {
        printf '%s\n' flake.lock
        git ls-files -co --exclude-standard -- android/ | while IFS= read -r path; do
            case "${path##*/}" in
                *.gradle.kts | gradle.properties | *.versions.toml) printf '%s\n' "$path" ;;
            esac
        done
    } | content_lines | sha256sum | cut -d' ' -f1
}

nix_key() {
    printf '%s\n' flake.nix flake.lock | content_lines | sha256sum | cut -d' ' -f1
}

# True when the folder is missing or holds nothing.
is_empty() {
    [ ! -d "$1" ] || [ -z "$(ls -A "$1")" ]
}

# Removes a folder whose files Go made read-only, as it makes the module cache.
remove_tree() {
    chmod -R u+w -- "$1" 2>/dev/null || true
    rm -rf -- "$1"
}

# lock_kind <kind>: holds an exclusive flock on the kind's lock file, on fd 9, until unlock_kind.
lock_kind() {
    local lock="$lock_dir/box-cache-$1.lock"
    if ! mkdir -p "$lock_dir" || ! exec 9>"$lock" || ! flock -x 9; then
        fail "$1" "$mode" "could not lock $lock"
    fi
}

unlock_kind() {
    exec 9>&-
}

# restore_folder <kind> <folder> <key> <entry>: extracts the entry into the folder when the folder is missing or empty
# and the entry exists, and prints the kind's line. A missing entry is a miss, and writes the kind's fresh marker.
restore_folder() {
    local kind="$1" dir="$2" key="$3" entry="$4" staging
    if ! is_empty "$dir"; then
        echo "box-cache: $kind kept"
        return 0
    fi
    if [ ! -f "$entry" ]; then
        : >"$state/$kind-fresh"
        echo "box-cache: $kind missed $key"
        return 0
    fi
    if ! mkdir -p "$(dirname "$dir")" || ! staging="$(mktemp -d "$(dirname "$dir")/.box-cache-XXXXXX")"; then
        fail "$kind" restore "could not make a staging folder beside $dir"
    fi
    if ! tar -xf "$entry" -C "$staging"; then
        remove_tree "$staging"
        fail "$kind" restore "could not extract $entry"
    fi
    if [ -d "$dir" ] && ! rmdir "$dir"; then
        remove_tree "$staging"
        fail "$kind" restore "$dir is not empty"
    fi
    if ! mv "$staging" "$dir"; then
        remove_tree "$staging"
        fail "$kind" restore "could not move the extraction into $dir"
    fi
    echo "box-cache: $kind restored $key"
}

# link_entry <kind> <tmp> <entry> <key>: links the finished temporary archive in as the entry without replacing one
# another box wrote first, and prints the kind's line.
link_entry() {
    local kind="$1" tmp="$2" entry="$3" key="$4"
    # mv -n leaves the temporary file behind when the entry already exists, another box having written it first.
    mv -n "$tmp" "$entry" 2>/dev/null || true
    if [ ! -e "$tmp" ]; then
        echo "box-cache: $kind saved $key"
        return 0
    fi
    rm -f "$tmp"
    if [ -e "$entry" ]; then
        echo "box-cache: $kind present"
        return 0
    fi
    fail "$kind" save "could not link the archive to $entry"
}

# save_folder <kind> <folder> <entry> <key> [tar option...]: archives the folder as the entry when the kind's restore
# missed on this run, the folder holds something and no entry exists, and prints the kind's line. Files a concurrent run
# changes while tar reads them are archived as tar finds them (its exit 1), which each kind's tool tolerates.
save_folder() {
    local kind="$1" dir="$2" entry="$3" key="$4" tmp rc
    shift 4
    if [ ! -e "$state/$kind-fresh" ] || is_empty "$dir"; then
        echo "box-cache: $kind unchanged"
        return 0
    fi
    if [ -e "$entry" ]; then
        echo "box-cache: $kind present"
        return 0
    fi
    if ! mkdir -p "$(dirname "$entry")" || ! tmp="$(mktemp "$(dirname "$entry")/.save-XXXXXX")"; then
        fail "$kind" save "could not make a temporary file beside $entry"
    fi
    rc=0
    tar -cf "$tmp" --warning=no-file-changed --warning=no-file-removed "$@" -C "$dir" . || rc=$?
    if [ "$rc" -gt 1 ]; then
        rm -f "$tmp"
        fail "$kind" save "could not archive $dir"
    fi
    link_entry "$kind" "$tmp" "$entry" "$key"
}

restore_gomod() {
    local key
    key="$(go_key)"
    lock_kind gomod
    restore_folder gomod "${GOMODCACHE:-$HOME/.cache/orrery/mod}" "$key" "$cache/gomod/$key.tar"
    unlock_kind
}

save_gomod() {
    local key
    key="$(go_key)"
    lock_kind gomod
    save_folder gomod "${GOMODCACHE:-$HOME/.cache/orrery/mod}" "$cache/gomod/$key.tar" "$key"
    unlock_kind
}

restore_gobuild() {
    local key
    key="$(go_key)"
    lock_kind gobuild
    restore_folder gobuild "${GOCACHE:-$HOME/.cache/orrery/build}" "$key" "$cache/gobuild/$key.tar"
    unlock_kind
}

save_gobuild() {
    local key
    key="$(go_key)"
    lock_kind gobuild
    save_folder gobuild "${GOCACHE:-$HOME/.cache/orrery/build}" "$cache/gobuild/$key.tar" "$key"
    unlock_kind
}

restore_gradle() {
    local key
    key="$(gradle_key)"
    lock_kind gradle
    restore_folder gradle "${GRADLE_USER_HOME:-$HOME/.gradle}/caches" "$key" "$cache/gradle/$key.tar"
    unlock_kind
}

save_gradle() {
    local key
    key="$(gradle_key)"
    lock_kind gradle
    save_folder gradle "${GRADLE_USER_HOME:-$HOME/.gradle}/caches" "$cache/gradle/$key.tar" "$key" \
        --exclude='*.lock' --exclude=gc.properties
    unlock_kind
}

restore_nix() {
    local key entry stamp staging
    key="$(nix_key)"
    entry="$cache/nix/$key.tar"
    stamp="$lock_dir/box-cache-nix-$key"
    lock_kind nix
    if [ -e "$stamp" ]; then
        echo "box-cache: nix kept"
        unlock_kind
        return 0
    fi
    if [ ! -f "$entry" ]; then
        : >"$state/nix-fresh"
        echo "box-cache: nix missed $key"
        unlock_kind
        return 0
    fi
    if ! staging="$(mktemp -d)"; then
        fail nix restore "could not make a staging folder"
    fi
    if ! tar -xf "$entry" -C "$staging"; then
        rm -rf -- "$staging"
        fail nix restore "could not extract $entry"
    fi
    local -a stored=()
    mapfile -t stored <"$staging/paths"
    if [ "${#stored[@]}" -eq 0 ] \
        || ! "${nix_cmd[@]}" copy --no-check-sigs --from "file://$staging/store" "${stored[@]}"; then
        rm -rf -- "$staging"
        fail nix restore "could not import the paths of $entry"
    fi
    rm -rf -- "$staging"
    : >"$stamp"
    echo "box-cache: nix restored $key"
    unlock_kind
}

save_nix() {
    local key entry stamp staging tmp paths
    key="$(nix_key)"
    entry="$cache/nix/$key.tar"
    stamp="$lock_dir/box-cache-nix-$key"
    lock_kind nix
    if [ ! -e "$state/nix-fresh" ]; then
        echo "box-cache: nix unchanged"
        unlock_kind
        return 0
    fi
    if [ -e "$entry" ]; then
        : >"$stamp"
        echo "box-cache: nix present"
        unlock_kind
        return 0
    fi
    if ! staging="$(mktemp -d)"; then
        fail nix save "could not make a staging folder"
    fi
    if ! "${nix_cmd[@]}" develop "$android_shell" --profile "$staging/profile" --command true >/dev/null \
        || ! paths="$("${nix_cmd[@]}" path-info -r "$staging/profile")"; then
        rm -rf -- "$staging"
        fail nix save "could not read the closure of $android_shell"
    fi
    local -a wanted=()
    mapfile -t wanted <<<"$paths"
    if [ -z "$paths" ] || ! "${nix_cmd[@]}" copy --to "file://$staging/store?compression=zstd" "${wanted[@]}"; then
        rm -rf -- "$staging"
        fail nix save "could not export the closure of $android_shell"
    fi
    printf '%s\n' "${wanted[@]}" >"$staging/paths"
    if ! mkdir -p "$(dirname "$entry")" || ! tmp="$(mktemp "$(dirname "$entry")/.save-XXXXXX")"; then
        rm -rf -- "$staging"
        fail nix save "could not make a temporary file beside $entry"
    fi
    if ! tar -cf "$tmp" -C "$staging" paths store; then
        rm -f "$tmp"
        rm -rf -- "$staging"
        fail nix save "could not archive the export"
    fi
    rm -rf -- "$staging"
    link_entry nix "$tmp" "$entry" "$key"
    : >"$stamp"
    unlock_kind
}

for kind in "$@"; do
    "${mode}_$kind"
done
