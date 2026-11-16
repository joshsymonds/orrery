#!/usr/bin/env bash
#
# Runs a gate command (just check, just android-test, nix flake check, ...) on an orrery-gate fleet box, so heavy gates
# leave this machine's cores alone. It runs the command here only when no box can come up at all.
#
# Usage:
#   scripts/gate-remote.sh <command> [args...]
#       From the top of this git work tree:
#       1. finds the box. The destination is $ORRERY_GATE_DEST, or else the one line of the host file that `wake`
#          writes; the probe is ssh to it running `true` within five seconds. When the host file names no box or its box
#          does not answer, it says so, runs the wake command and waits for it to end (the wake's output goes to
#          ${XDG_RUNTIME_DIR:-/tmp}/orrery-gate-wake.log), then reads the host file again and probes the box it names.
#          The wait ends when the wake ends: with a box, with a launch that failed, or with a launch in progress that
#          failed. Only when no box answers after the wake does the command run here (step 6), with the wake's last line
#          as the reason. A $ORRERY_GATE_DEST that does not answer runs the command here without a wake;
#       2. on the box, under ~/gate: removes the checkouts least recently prepared while 30 GB are not free (only one whose
#          lock is free and whose .meta is over 10 minutes old), makes the object store ~/gate/objects.git and this work
#          tree's checkout ~/gate/<dir>-<hash>, a repository whose objects come from the store, and reports the box's
#          load and CPU count;
#       3. pushes HEAD, origin/main and every tag into the store with --no-verify, so no push hook runs;
#       4. syncs the work tree into the checkout with rsync --delete, leaving out .git and every git-ignored path, so the
#          box never receives .env files or build output and keeps its own;
#       5. points the checkout's HEAD, origin/main and tags at this work tree's, rebuilds its index from HEAD plus this
#          work tree's staged changes, then runs the command, each argument one word and never evaluated, from the top of
#          the checkout through scripts/in-devenv, under a shell named orrery-gate-run (the box's busyCommand looks for
#          it). The command's output streams back as it runs;
#       6. a run here goes through $ORRERY_GATE_LOCAL (default /tmp/claude-1000/heavy when it is executable, else the
#          command runs directly).
#       When steps 2-4 fail because the box stopped answering, it goes back to step 1 once; when they fail on a box that
#       still answers, or fail a second time, the command runs here. Before the command starts it prints one line on
#       stderr, "gate-remote: running <command> on <destination>" or "gate-remote: running <command> here (<why>)", with
#       the load average and nproc of the machine that runs it; when it ends, "gate-remote: <command> exited <status>
#       after <seconds>s on <destination>|here".
#       Runs from one work tree take turns, holding ${XDG_RUNTIME_DIR:-/tmp}/orrery-gate-<dir>-<hash>.lock; runs from
#       different work trees share the box.
#
#   scripts/gate-remote.sh wake
#       Finds or launches the box and writes its destination, <user>@<name>, to the host file. One wake works at a time,
#       holding ${XDG_RUNTIME_DIR:-/tmp}/orrery-gate-wake.lock; a wake that finds the lock held prints "waiting for the
#       orrery-gate launch in progress" and blocks until the holder ends. A wake that waited takes the result the holder
#       recorded in orrery-gate-wake.result beside the lock: a box that answers ends it with that box, and a failed
#       launch ends it with the same failure, so concurrent wakes make one launch between them. A holder that ended
#       without a result leaves the waiter to find a box itself. Finding one, in order:
#       - the box the host file names, when it answers;
#       - a running orrery-gate box from `ec2 ls` that answers;
#       - `ec2 up orrery-gate --spot-only`, once: never on-demand, never retried. ec2's output goes to the wake's output
#         as it runs. A refused launch prints "gate-remote: orrery-gate launch refused: <ec2's last line>" and exits 1.
#
# Environment (each command variable is split into words; the destination is appended as the last word):
#   ORRERY_GATE_DEST       ssh destination of the box, in place of the host file
#   ORRERY_GATE_HOST_FILE  host file (default ${XDG_CONFIG_HOME:-$HOME/.config}/orrery/gate-host)
#   ORRERY_GATE_SSH        ssh command for every connection (default: ssh in batch mode, 5 s connect timeout, accept-new
#                          host keys, keepalives that notice a lost box within a minute)
#   ORRERY_GATE_PROBE      probe command (default: $ORRERY_GATE_SSH <destination> true)
#   ORRERY_GATE_WAKE       wake command, run with no destination (default: this script's `wake`)
#   ORRERY_GATE_LOCAL      prefix for a local run (default above; empty runs the command directly)
#   ORRERY_GATE_EC2        fleet tool `wake` calls (default ec2)
#   ORRERY_GATE_USER       user `wake` puts in the destination (default orrery)
#   ORRERY_GATE_REMOTE=0   never use the box
#
# Exit codes: the command's status, wherever it ran. 75 is an environment result: the box went away or the connection
# to it dropped after the command started (a spot interruption, for one), and the line before it says so. It is never
# a test failure; run again, and the next run waits for the replacement box that the lost one's wake brings up. A
# checkout the box cannot set up in step 5 runs the command here. 2 for bad usage or outside a git work tree.

set -euo pipefail

environment_exit=75
self="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "${BASH_SOURCE[0]}")"

usage() {
    echo "usage: gate-remote.sh <command> [args...]" >&2
    echo "       gate-remote.sh wake" >&2
    exit 2
}

read -ra ssh_cmd <<<"${ORRERY_GATE_SSH:-ssh -o BatchMode=yes -o ConnectTimeout=5 -o StrictHostKeyChecking=accept-new -o ServerAliveInterval=15 -o ServerAliveCountMax=4}"
host_file="${ORRERY_GATE_HOST_FILE:-${XDG_CONFIG_HOME:-$HOME/.config}/orrery/gate-host}"
runtime="${XDG_RUNTIME_DIR:-/tmp}"
wake_log="$runtime/orrery-gate-wake.log"

probe() {
    local -a command
    if [ -n "${ORRERY_GATE_PROBE:-}" ]; then
        read -ra command <<<"$ORRERY_GATE_PROBE"
        "${command[@]}" "$1" </dev/null >/dev/null 2>&1
    else
        "${ssh_cmd[@]}" "$1" true </dev/null >/dev/null 2>&1
    fi
}

# The destination the host file names, or nothing.
host_file_dest() {
    local line=""
    if [ -s "$host_file" ]; then
        read -r line <"$host_file" || true
    fi
    printf '%s' "$line"
}

# Starts the wake detached and returns at once, so a box is coming up for the next run.
start_wake() {
    local -a command
    read -ra command <<<"${ORRERY_GATE_WAKE:-$self wake}"
    echo "gate-remote: waking an orrery-gate box for the next run (log: $wake_log)" >&2
    setsid "${command[@]}" </dev/null >>"$wake_log" 2>&1 &
}

# Runs the wake in its own session and waits for it to end, so a run killed while it waits leaves the launch going.
# Its output goes to the wake log as it comes; wake_last is its last line.
wake_last=""
wait_for_wake() {
    local -a command
    local out status
    read -ra command <<<"${ORRERY_GATE_WAKE:-$self wake}"
    out="$(mktemp)"
    { setsid -w "${command[@]}" </dev/null 2>&1 && echo 0 >"$out.status" || echo "$?" >"$out.status"; } \
        | tee -a "$wake_log" >"$out" &
    wait "$!" || true
    status="$(cat "$out.status" 2>/dev/null || echo 1)"
    wake_last="$(tail -n 1 "$out")"
    wake_last="${wake_last#* gate-remote: }"
    rm -f -- "$out" "$out.status"
    return "$status"
}

# Sets dest to a box that answers, waiting for the wake when the known box does not; returns 1 with the reason in why
# when no box can come up.
dest=""
why=""
find_box() {
    if [ -n "${ORRERY_GATE_DEST:-}" ]; then
        dest="$ORRERY_GATE_DEST"
        probe "$dest" && return 0
        why="ORRERY_GATE_DEST $dest did not answer"
        return 1
    fi
    dest="$(host_file_dest)"
    if [ -n "$dest" ] && probe "$dest"; then
        return 0
    fi
    if [ -n "$dest" ]; then
        echo "gate-remote: $dest did not answer; waiting for an orrery-gate box (log: $wake_log)" >&2
    else
        echo "gate-remote: no gate box is known yet; waiting for an orrery-gate box (log: $wake_log)" >&2
    fi
    if ! wait_for_wake; then
        why="no orrery-gate box could come up: ${wake_last:-the wake failed}"
        return 1
    fi
    dest="$(host_file_dest)"
    if [ -n "$dest" ] && probe "$dest"; then
        return 0
    fi
    why="no orrery-gate box could come up: ${dest:-no box} did not answer after the wake"
    return 1
}

load_line() {
    local one five fifteen _
    read -r one five fifteen _ </proc/loadavg
    printf 'load %s %s %s, nproc %s' "$one" "$five" "$fifteen" "$(nproc)"
}

started=0
finished() {
    echo "gate-remote: $(printf '%q ' "${@:3}")exited $1 after $(($(date +%s) - started))s $2" >&2
}

run_here() {
    local reason="$1"
    shift
    local -a prefix=()
    if [ -n "${ORRERY_GATE_LOCAL+set}" ]; then
        read -ra prefix <<<"$ORRERY_GATE_LOCAL"
    elif [ -x /tmp/claude-1000/heavy ]; then
        prefix=(/tmp/claude-1000/heavy)
    fi
    echo "gate-remote: running $(printf '%q ' "$@")here ($reason; $(load_line))" >&2
    local status=0
    started="$(date +%s)"
    "${prefix[@]}" "$@" || status=$?
    finished "$status" here "$@"
    exit "$status"
}

# The script the box runs as `bash -s -- <checkout> <free-kib>`: step 2. It touches the checkout's .meta before the
# prune, so the prune never takes the checkout being prepared, and removes a checkout only while holding its lock
# exclusively, which a running gate holds. Its last line is "load <1> <5> <15> nproc <n>".
host_prepare() {
    cat <<'EOF'
set -eu
sub="$1"
keep="$2"
mkdir -p gate
cd gate
mkdir -p "$sub.meta"
touch "$sub.meta"
find . -maxdepth 1 -name '*.meta' -mmin +10 -printf '%T@ %f\n' | sort -n | while read -r _ meta; do
    if [ "$(df --output=avail -k . | tail -n 1)" -ge "$keep" ]; then
        break
    fi
    stale="${meta%.meta}"
    flock -n -x "$stale.lock" rm -rf "./$stale" "./$meta" "./$stale.lock" || true
done
[ -d objects.git ] || git init -q --bare objects.git
mkdir -p "$sub"
cd "$sub"
[ -d .git ] || git init -q
printf '%s\n' "$HOME/gate/objects.git/objects" >.git/objects/info/alternates
read -r one five fifteen _ </proc/loadavg
echo "load $one $five $fifteen nproc $(nproc)"
EOF
}

# Steps 2-4 on dest; prepared holds the box's output.
prepared=""
prepare() {
    local ssh_line
    ssh_line="$(printf '%q ' "${ssh_cmd[@]}")"
    prepared="$("${ssh_cmd[@]}" "$dest" "bash -s -- $(printf '%q' "$sub") $((30 * 1024 * 1024))" <<<"$(host_prepare)")" \
        && GIT_SSH_COMMAND="$ssh_line" git push -q --force --no-verify "$dest:gate/objects.git" "${refs[@]}" \
        && rsync -a -e "$ssh_line" "$meta/" "$dest:$remote.meta/" \
        && rsync -a --delete -e "$ssh_line" --exclude=/.git --exclude=/.worktrees/ --filter=':- .gitignore' \
            ./ "$dest:$remote/"
}

run_remote() {
    local top head origin_main run_id script record exit_file round status=0
    if ! top="$(git rev-parse --show-toplevel 2>/dev/null)"; then
        echo "gate-remote: not inside a git work tree" >&2
        exit 2
    fi
    cd "$top"

    if [ "${ORRERY_GATE_REMOTE:-1}" = 0 ]; then
        run_here "ORRERY_GATE_REMOTE=0" "$@"
    fi

    sub="$(basename "$top")-$(printf '%s' "$top" | sha256sum | cut -c1-8)"
    remote="gate/$sub"
    head="$(git rev-parse HEAD)"
    origin_main="$(git rev-parse -q --verify refs/remotes/origin/main || true)"
    refs=("$head:refs/gate/$sub" "+refs/tags/*:refs/tags/*")
    if [ -n "$origin_main" ]; then
        refs+=("$origin_main:refs/heads/origin-main")
    fi

    # The staged changes travel beside the checkout, never inside it, so they are not files of the tree.
    meta="$(mktemp -d)"
    trap 'rm -rf -- "$meta"' EXIT
    git diff --cached --binary >"$meta/staged.patch"

    exec 8>"$runtime/orrery-gate-$sub.lock"
    if ! flock -n 8; then
        echo "gate-remote: another run from this work tree holds the box's checkout; waiting" >&2
        flock 8
    fi

    for round in 1 2; do
        if ! find_box; then
            exec 8>&-
            run_here "$why" "$@"
        fi
        if prepare; then
            break
        fi
        if [ "$round" -eq 2 ] || probe "$dest"; then
            exec 8>&-
            run_here "$dest could not be prepared" "$@"
        fi
        echo "gate-remote: $dest stopped answering while it was prepared" >&2
    done
    local one five fifteen cpus
    read -r _ one five fifteen _ cpus <<<"$(tail -n 1 <<<"$prepared")"

    run_id="$(date +%s)-$$-$RANDOM"
    exit_file="../$(printf '%q' "$sub").meta/exit"
    script="cd $(printf '%q' "$remote") && rm -f $exit_file || exit 1"
    script+="; git fetch -q --prune --no-write-fetch-head ../objects.git '+refs/tags/*:refs/tags/*'"
    if [ -n "$origin_main" ]; then
        script+=" && git update-ref refs/remotes/origin/main $origin_main"
    fi
    script+=" && git update-ref --no-deref HEAD $head && git read-tree $head"
    script+=" && { [ ! -s ../$(printf '%q' "$sub").meta/staged.patch ] || git apply --cached ../$(printf '%q' "$sub").meta/staged.patch; }"
    script+=" && { git update-index -q --refresh || true; }"
    script+=" || { echo \"$run_id setup\" >$exit_file; exit 1; }"
    script+="; flock -x ../$(printf '%q' "$sub").lock scripts/in-devenv -c '\"\$@\"; exit \$?' orrery-gate-run $(printf '%q ' "$@")"
    script+="; rc=\$?; echo \"$run_id \$rc\" >$exit_file; exit \$rc"

    echo "gate-remote: running $(printf '%q ' "$@")on $dest (load $one $five $fifteen, nproc $cpus)" >&2
    started="$(date +%s)"
    "${ssh_cmd[@]}" "$dest" "$script" </dev/null || status=$?
    if [ "$status" -eq 0 ]; then
        finished 0 "on $dest" "$@"
        exit 0
    fi

    # A nonzero status is the command's only when the box recorded this run's exit; anything else lost the box.
    if record="$("${ssh_cmd[@]}" "$dest" "cat $(printf '%q' "$remote.meta/exit")" </dev/null 2>/dev/null)" \
        && [ "${record% *}" = "$run_id" ]; then
        if [ "${record##* }" = setup ]; then
            exec 8>&-
            run_here "$dest could not set up its checkout" "$@"
        fi
        finished "${record##* }" "on $dest" "$@"
        exit "${record##* }"
    fi
    if probe "$dest"; then
        echo "gate-remote: lost the connection to $dest after the command started; environment result, not a test failure (exit $environment_exit)" >&2
    else
        echo "gate-remote: $dest went away after the command started (a spot interruption or the box ended); environment result, not a test failure (exit $environment_exit)" >&2
        start_wake
    fi
    exit "$environment_exit"
}

# Records this wake's result for the wakes waiting on the lock: "<id> ok <destination>" or "<id> failed <reason>".
wake_result() {
    local tmp
    tmp="$(mktemp "$result_file.XXXXXX")"
    printf '%s %s\n' "$wake_id" "$1" >"$tmp"
    mv "$tmp" "$result_file"
}

# Writes the host file and the result, then ends the wake with the box.
wake_found() {
    local tmp
    mkdir -p "$(dirname "$host_file")"
    tmp="$(mktemp "$host_file.XXXXXX")"
    printf '%s\n' "$1" >"$tmp"
    mv "$tmp" "$host_file"
    wake_result "ok $1"
    echo "$(date -Is) gate-remote: the gate box is $1"
    exit 0
}

# Records a failed launch and ends the wake with it.
wake_failed() {
    wake_result "failed $1"
    echo "$(date -Is) gate-remote: $1" >&2
    exit 1
}

wake() {
    local -a ec2
    read -ra ec2 <<<"${ORRERY_GATE_EC2:-ec2}"
    local user="${ORRERY_GATE_USER:-orrery}" listing name known before="" after="" kind rest out status
    result_file="$runtime/orrery-gate-wake.result"
    wake_id="$(date +%s)-$$-$RANDOM"

    export EC2_FLEET_AWS_ENV_FILE="${EC2_FLEET_AWS_ENV_FILE:-/run/user/$UID/agenix/ec2-fleet-aws}"
    export EC2_FLEET_TS_ENV_FILE="${EC2_FLEET_TS_ENV_FILE:-/run/user/$UID/agenix/tailscale-fleet-oauth}"
    export EC2_FLEET_TOFU_PASSPHRASE_FILE="${EC2_FLEET_TOFU_PASSPHRASE_FILE:-/run/user/$UID/agenix/ec2-fleet-tofu-passphrase}"

    exec 9>"$runtime/orrery-gate-wake.lock"
    if ! flock -n 9; then
        before="$(cat "$result_file" 2>/dev/null || true)"
        echo "$(date -Is) gate-remote: waiting for the orrery-gate launch in progress"
        flock 9
        after="$(cat "$result_file" 2>/dev/null || true)"
    fi
    # A wake that waited takes the result of the one it waited for.
    if [ -n "$after" ] && [ "$after" != "$before" ]; then
        read -r _ kind rest <<<"$after"
        if [ "$kind" = failed ]; then
            wake_failed "the launch in progress failed: $rest"
        fi
        if [ "$kind" = ok ] && probe "$rest"; then
            wake_found "$rest"
        fi
    fi

    known="$(host_file_dest)"
    if [ -n "$known" ] && probe "$known"; then
        wake_found "$known"
    fi

    listing="$("${ec2[@]}" ls)" || wake_failed "ec2 ls failed"
    while IFS= read -r name; do
        if [ -n "$name" ] && probe "$user@$name"; then
            echo "$(date -Is) gate-remote: adopting $name"
            wake_found "$user@$name"
        fi
    done < <(awk '$2 == "orrery-gate" { print $1 }' <<<"$listing")

    echo "$(date -Is) gate-remote: launching an orrery-gate box"
    out="$(mktemp)"
    status=0
    "${ec2[@]}" up orrery-gate --spot-only 2>&1 | tee "$out" || status=$?
    name="$(sed -n 's/^\(orrery-gate-[a-z0-9]*\) ready$/\1/p' "$out" | tail -n 1)"
    rest="$(tail -n 1 "$out")"
    rm -f -- "$out"
    if [ "$status" -ne 0 ]; then
        wake_failed "orrery-gate launch refused: $rest"
    fi
    if [ -z "$name" ]; then
        wake_failed "ec2 up printed no ready line: $rest"
    fi
    wake_found "$user@$name"
}

case "${1:-}" in
    "") usage ;;
    wake)
        [ "$#" -eq 1 ] || usage
        wake
        ;;
    *) run_remote "$@" ;;
esac
