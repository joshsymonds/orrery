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
#       3. pushes HEAD, origin/main and every tag into the store with --no-verify, so no push hook runs. The box runs the
#          push's receive-pack holding ~/gate/objects.lock (flock -o, so nothing receive-pack leaves running holds it),
#          so pushes from runs that prepare one box at once take turns: each sees the refs the one before it wrote, and a
#          ref already at the pushed commit is no update, where two pushes at once refused each other's new refs. The
#          wait for the lock ends when the push before it ends;
#       4. syncs the work tree into the checkout with rsync --delete, leaving out .git and every git-ignored path, so the
#          box never receives .env files or build output and keeps its own;
#       5. points the checkout's HEAD, origin/main and tags at this work tree's, rebuilds its index from HEAD plus this
#          work tree's staged changes, then runs the command, each argument one word and never evaluated, from the top of
#          the checkout through scripts/in-devenv, under a shell named orrery-gate-run (the box's busyCommand looks for
#          it), once it holds one of the box's slots: `just check`, `just android-test` and `just cover` share about 3
#          full-check slots on 64 cores and every other command shares wider light-check slots; a run that finds every
#          slot held says it is queued and waits for one (box_run below). The run holds the checkout's lock through
#          flock -o, so only flock holds it and nothing the command starts inherits it. The command runs in its own
#          systemd user scope, orrery-gate-<run>.scope, which is stopped when the run ends or is stopped: whatever the
#          command left behind (a timed-out test's daemons, a Gradle daemon) ends with it, since a process stays in its
#          scope's cgroup however it detaches. A box whose user manager does not answer runs nothing.
#          On a box that mounts the fleet S3 cache at ~/.cache/fleet, the command runs under
#          `scripts/box-cache.sh run ~/.cache/fleet/box`, which restores the Go caches an earlier box saved before it and
#          saves them after a command that exited 0 (see that script); $ORRERY_BOX_CACHE_ROOT on the box names another
#          cache root, and empty runs without one.
#          The run lives apart from every connection: one short connection starts it on the box in a session of its
#          own (host_launch), with its stdout and stderr in out and err in the checkout's .meta, beside its exit record,
#          and the output comes back over two more connections, one per log, each following its log until the run ends
#          (host_stream). Each byte shown here is counted, so when a connection drops the script reconnects, with
#          backoff (1, 2, 4, 8, then 16 s), and both logs go on from the byte after the last one shown: nothing is shown
#          twice or left out. It says so on stderr when a connection drops and when it is back. It gives up once
#          $ORRERY_GATE_COLLECT_SECS (default 120) pass without a connection that brings output, stays up 5 s or sees
#          the run end. A signal to this script (TERM, INT, HUP) stops the run on the box too, and a run left going by
#          a script killed outright is stopped by the next run from the work tree before it syncs (host_prepare);
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
#   ORRERY_GATE_FULL_SLOTS   full-check slots on the box (default: its nproc / 20, at least 1)
#   ORRERY_GATE_LIGHT_SLOTS  light-check slots on the box (default: its nproc / 8, at least 1)
#   ORRERY_GATE_COLLECT_SECS seconds a run keeps reconnecting after its connections drop before it gives up (default 120)
#   ORRERY_GATE_REMOTE=0   never use the box
#
# Exit codes: the command's status, wherever it ran, also when its connections dropped while the box stayed up and the
# run was collected after a reconnect. 75 is an environment result, and the line before it says which: the box went
# away after the command started (a spot interruption, for one; a wake starts so the next run finds the replacement
# box); the connections stayed down for $ORRERY_GATE_COLLECT_SECS while the box still answers (the run is stopped
# there); the run ended on the box without recording its exit, as when it was killed there (what is left of it is
# stopped); or the box no longer knows the run. It is never a test failure; run again. The box also ends a run with 75
# when it cannot hold or wait for its slots, saying why. A checkout the box cannot set up in step 5, or a box without a
# user manager for the scope, runs the command here. A signal that ends the script ends it with 128 + the signal. 2
# for bad usage or outside a git work tree.

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

# Starts the wake detached and returns at once, so a box is coming up for the next run. The wake never inherits this
# run's work-tree lock (fd 8), so it never holds up the next run from this work tree.
start_wake() {
    local -a command
    read -ra command <<<"${ORRERY_GATE_WAKE:-$self wake}"
    echo "gate-remote: waking an orrery-gate box for the next run (log: $wake_log)" >&2
    setsid "${command[@]}" </dev/null >>"$wake_log" 2>&1 8>&- &
}

# Runs the wake in its own session and waits for it to end, so a run killed while it waits leaves the launch going.
# Its output goes to the wake log as it comes; wake_last is its last line.
wake_last=""
wait_for_wake() {
    local -a command
    local out status
    read -ra command <<<"${ORRERY_GATE_WAKE:-$self wake}"
    out="$(mktemp)"
    { setsid -w "${command[@]}" </dev/null 2>&1 8>&- && echo 0 >"$out.status" || echo "$?" >"$out.status"; } \
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
# exclusively, which a running gate holds. When the box still has less than <free-kib> free after that prune, it
# deletes the Go build cache entries (~/.cache/orrery/build) no run has used in the last hour: Go refreshes an entry's
# time when it uses one, and a missing entry is only rebuilt, so a running gate loses nothing it needs. Its last line
# is "load <1> <5> <15> nproc <n>".
#
# A run of this checkout that is still going was left by a run here that is gone (one killed with SIGKILL, or one that
# gave up collecting it): runs from one work tree take turns, so nothing will collect it. It is stopped before the sync
# changes its files, and the prepare waits up to 30 s for it to end.
host_prepare() {
    cat <<'EOF'
set -eu
sub="$1"
keep="$2"
mkdir -p gate
cd gate
mkdir -p "$sub.meta"
touch "$sub.meta"
if [ -s "$sub.meta/runner" ] && ! flock -n -s "$sub.meta/runner.lock" true 2>/dev/null; then
    read -r old pid _ <"$sub.meta/runner" || true
    if [ -n "${pid:-}" ] && tr '\0' '\n' <"/proc/$pid/cmdline" 2>/dev/null | grep -qx orrery-gate-runner; then
        echo "gate-remote: stopping run $old on $(uname -n), left running by a run that is gone" >&2
        kill -TERM -- "-$pid" 2>/dev/null || true
        flock -w 30 -s "$sub.meta/runner.lock" true || kill -KILL -- "-$pid" 2>/dev/null || true
        systemctl --user stop "orrery-gate-$old.scope" 2>/dev/null || true
    fi
fi
find . -maxdepth 1 -name '*.meta' -mmin +10 -printf '%T@ %f\n' | sort -n | while read -r _ meta; do
    if [ "$(df --output=avail -k . | tail -n 1)" -ge "$keep" ]; then
        break
    fi
    stale="${meta%.meta}"
    flock -n -x "$stale.lock" rm -rf "./$stale" "./$meta" "./$stale.lock" || true
done
gocache="$HOME/.cache/orrery/build"
if [ -d "$gocache" ] && [ "$(df --output=avail -k . | tail -n 1)" -lt "$keep" ]; then
    echo "gate-remote: trimming the Go build cache on $(uname -n): under $((keep / 1024 / 1024)) GiB free" >&2
    find "$gocache" -mindepth 2 -type f -mmin +60 -delete 2>/dev/null || true
fi
[ -d objects.git ] || git init -q --bare objects.git
mkdir -p "$sub"
cd "$sub"
[ -d .git ] || git init -q
printf '%s\n' "$HOME/gate/objects.git/objects" >.git/objects/info/alternates
read -r one five fifteen _ </proc/loadavg
echo "load $one $five $fifteen nproc $(nproc)"
EOF
}

# What the box runs, holding the checkout's lock, as
# `bash -c <this> orrery-gate-run <unit> <full slots> <light slots> <command...>`.
#
# First it takes a slot. `just check`, `just android-test` and `just cover` are full checks and share the full-check
# slots, <full slots> of them (empty: nproc / 20, at least 1, so 3 on 64 cores); every other command shares the
# light-check slots (empty: nproc / 8, at least 1). Slot i is a flock on ~/gate/slots/<full|light>-<i>, held by this
# shell and closed for the command, so nothing the command leaves behind holds it. When every slot is held it says so
# once on stderr and waits: one waiter per slot blocks in flock on it and writes to a pipe once it gets it, so the wait
# ends as soon as a slot is let go or its holder dies, never on a timer. It then tries the slots again, since another
# run may have taken the one that came free. It says which slot it took and after how long. When the slots cannot be
# held or waited for (the slot directory or a slot file cannot be made or opened, or every waiter ends without a slot)
# it says why and exits 75, never going round the wait again.
#
# Then it runs the command in its own user scope, stopped whenever this shell ends, so nothing the command started
# outlives the run. The command runs in the background and this shell waits for it, so a hangup or TERM (a run that is
# stopped) is acted on at once.
box_run() {
    cat <<'EOF'
unit="$1"
full="$2"
light="$3"
shift 3
class=light
label=light-check
if [ "${1:-}" = just ]; then
    for arg in "${@:2}"; do
        case "$arg" in
            check | android-test | cover) class=full label=full-check ;;
        esac
    done
fi
if [ "$class" = full ]; then
    slots="${full:-$(($(nproc) / 20))}"
else
    slots="${light:-$(($(nproc) / 8))}"
fi
[ "$slots" -ge 1 ] || slots=1
dir="$HOME/gate/slots"
# Ends the run when its slots cannot be held or waited for: an environment result, never another round of the wait.
slot_failed() {
    echo "gate-remote: $1 on $(uname -n); the run ends without a slot (exit 75)" >&2
    exit 75
}
mkdir -p "$dir" 2>/dev/null || slot_failed "cannot make the slot directory $dir"
waiters=""
wake_fd=""
end_waiters() {
    [ -z "$waiters" ] || kill -- "-$waiters" 2>/dev/null || true
    waiters=""
    if [ -n "$wake_fd" ]; then
        exec {wake_fd}<&-
        wake_fd=""
    fi
}
trap 'end_waiters; systemctl --user stop "$unit.scope" 2>/dev/null || true' EXIT
trap 'exit 129' HUP
trap 'exit 143' TERM
start=$(date +%s)
queued=""
slot_fd=""
while [ -z "$slot_fd" ]; do
    for ((i = 1; i <= slots; i++)); do
        if ! { exec {fd}>"$dir/$class-$i"; } 2>/dev/null; then
            [ -d "$dir" ] || slot_failed "the slot directory $dir is gone"
            slot_failed "cannot open the slot file $dir/$class-$i"
        fi
        if flock -n "$fd"; then
            slot_fd=$fd
            slot=$i
            break
        fi
        exec {fd}>&-
    done
    [ -z "$slot_fd" ] || break
    if [ -z "$queued" ]; then
        echo "gate-remote: every $label slot on $(uname -n) is held ($slots); queued until one is let go" >&2
        queued=1
    fi
    # One waiter per slot, in a session of their own, blocks in flock on it and prints the slot's number once it gets
    # it. They write to an anonymous pipe, so the read ends with the first number, or at end of file once every waiter
    # has ended without one, as when the slot files are gone.
    exec {wake_fd}< <(exec setsid bash -c 'for ((i = 1; i <= $3; i++)); do { flock "$1/$2-$i" true && echo "$i"; } & done; wait' \
        _ "$dir" "$class" "$slots" 2>/dev/null) || slot_failed "cannot start the slot waiters"
    waiters=$!
    if ! read -r _ <&"$wake_fd"; then
        end_waiters
        # A gone directory also ends the waiters (one that had not yet opened its slot file fails at once), so it is
        # checked first and named as the cause whichever of the two was seen first.
        [ -d "$dir" ] || slot_failed "the slot directory $dir is gone"
        slot_failed "every slot waiter ended without a slot in $dir"
    fi
    end_waiters
done
echo "gate-remote: took $label slot $slot of $slots on $(uname -n) after $(($(date +%s) - start))s" >&2
# The command runs under scripts/box-cache.sh run when the box mounts the fleet S3 cache, so a fresh box starts from the
# caches an earlier box saved. ORRERY_BOX_CACHE_ROOT names the cache root in place of the mount (empty: no cache).
if [ -n "${ORRERY_BOX_CACHE_ROOT+x}" ]; then
    cache_root="$ORRERY_BOX_CACHE_ROOT"
elif mountpoint -q "$HOME/.cache/fleet" 2>/dev/null; then
    cache_root="$HOME/.cache/fleet/box"
else
    cache_root=""
fi
if [ -n "$cache_root" ] && [ -x scripts/box-cache.sh ]; then
    set -- scripts/box-cache.sh run "$cache_root" "$@"
fi
systemd-run --user --scope --quiet --collect -p TimeoutStopSec=15 --unit="$unit" -- \
    scripts/in-devenv -c '"$@"; exit $?' orrery-gate-run "$@" {slot_fd}>&- &
wait "$!"
EOF
}

# The run on the box, detached from every connection, as
# `bash -c <this> orrery-gate-runner <run> <checkout> <box_run> <full> <light> <origin/main> <HEAD> <command...>` from
# the checkout, with its stdout and stderr in the .meta's out and err and the .meta's runner.lock held on fd 9, which it
# keeps for as long as it lives and closes for everything it starts. It points the checkout at HEAD, origin/main, the
# tags and the staged patch (step 5), checks that the user manager answers, then runs box_run holding the checkout's
# lock. It records "<run> <status>" in the .meta's exit, or "<run> setup" or "<run> noscope" when nothing ran, before
# it ends, so the record is there once the runner.lock is free. A run stopped with TERM records nothing.
host_runner() {
    cat <<'EOF'
id="$1"
sub="$2"
box_run="$3"
full="$4"
light="$5"
origin_main="$6"
head="$7"
shift 7
m="../$sub.meta"
(
    git fetch -q --prune --no-write-fetch-head ../objects.git '+refs/tags/*:refs/tags/*' \
        && { [ -z "$origin_main" ] || git update-ref refs/remotes/origin/main "$origin_main"; } \
        && git update-ref --no-deref HEAD "$head" && git read-tree "$head" \
        && { [ ! -s "$m/staged.patch" ] || git apply --cached "$m/staged.patch"; } \
        && { git update-index -q --refresh || true; }
) 9>&- || {
    echo "$id setup" >"$m/exit"
    exit 1
}
systemctl --user show-environment >/dev/null 2>&1 9>&- || {
    echo "$id noscope" >"$m/exit"
    exit 1
}
flock -o -x "../$sub.lock" bash -c "$box_run" orrery-gate-run "orrery-gate-$id" "$full" "$light" "$@" 9>&-
rc=$?
echo "$id $rc" >"$m/exit"
exit "$rc"
EOF
}

# What the box runs to start a run, as `bash -c <this> orrery-gate-launch <run> <checkout> <runner> <runner args...>`.
# It starts the runner in a session of its own with its output in the .meta's out and err, so the run goes on whatever
# happens to the connection, and records "<run> <pid>" in the .meta's runner. A run that is already recorded there is
# never started again, so the launch can be repeated after a dropped connection. It ends at once; nothing it starts
# writes to the connection.
host_launch() {
    cat <<'EOF'
id="$1"
sub="$2"
runner="$3"
shift 3
cd "gate/$sub" || exit 1
m="../$sub.meta"
read -r current _ <"$m/runner" 2>/dev/null || current=""
[ "$current" != "$id" ] || exit 0
exec 9>"$m/runner.lock"
if ! flock -w 30 9; then
    echo "gate-remote: the run before this one still holds $m/runner.lock on $(uname -n)" >&2
    exit 1
fi
rm -f "$m/exit" "$m/out" "$m/err"
: >"$m/out" && : >"$m/err" && echo "$id" >"$m/runner" || exit 1
setsid bash -c "$runner" orrery-gate-runner "$id" "$sub" "$@" </dev/null >>"$m/out" 2>>"$m/err" &
echo "$id $!" >"$m/runner.new" && mv "$m/runner.new" "$m/runner"
EOF
}

# What the box runs to stream one of the run's logs, as `bash -c <this> orrery-gate-stream <checkout> <run> out|err
# <bytes already shown>`. Its first line is "orrery-gate-stream <run> <state>": "running", then the log from the given
# byte until the run ends; "ended <record>", then the rest of the log, where the record is what the run recorded in the
# exit file ("<status>", "setup" or "noscope"; empty when it recorded nothing); or "unknown" when the box has no such
# run. Everything after that line is the log's bytes.
host_stream() {
    cat <<'EOF'
id="$2"
log="$3"
from="$(($4 + 1))"
if ! cd "gate/$1.meta" 2>/dev/null || ! read -r current _ <runner 2>/dev/null || [ "$current" != "$id" ]; then
    echo "orrery-gate-stream $id unknown"
    exit 0
fi
if flock -n -s runner.lock true 2>/dev/null; then
    record=""
    if read -r recorded rest <exit 2>/dev/null && [ "$recorded" = "$id" ]; then
        record="$rest"
    fi
    echo "orrery-gate-stream $id ended $record"
    exec tail -c "+$from" "$log"
fi
echo "orrery-gate-stream $id running"
# The watcher ends once the runner has, and tail reads the log once more after it ends. This shell stays tail's parent
# and reaps the watcher (were tail to replace it, as bash does with a last command, the watcher would stay a zombie
# that tail sees alive), so tail is not the last command.
flock -s runner.lock true </dev/null >/dev/null 2>&1 &
tail -q -s 0.2 --pid="$!" -c "+$from" -f "$log"
status=$?
# A watcher that ended while the runner lives (it could not wait on the lock) did not see the run end: a failed stream.
flock -n -s runner.lock true 2>/dev/null || exit 3
exit "$status"
EOF
}

# What the box runs to stop a run, as `bash -c <this> orrery-gate-stop <checkout> <run>`: it ends the runner's session
# (box_run's trap stops the command's scope) and stops the scope too, in case box_run is already gone.
host_stop() {
    cat <<'EOF'
cd "gate/$1.meta" 2>/dev/null || exit 0
read -r current pid _ <runner 2>/dev/null || exit 0
[ "$current" = "$2" ] && [ -n "$pid" ] || exit 0
kill -TERM -- "-$pid" 2>/dev/null
systemctl --user stop --no-block "orrery-gate-$2.scope" 2>/dev/null
exit 0
EOF
}

# Steps 2-4 on dest; prepared holds the box's output.
prepared=""
prepare() {
    local ssh_line
    ssh_line="$(printf '%q ' "${ssh_cmd[@]}")"
    prepared="$("${ssh_cmd[@]}" "$dest" "bash -s -- $(printf '%q' "$sub") $((30 * 1024 * 1024))" <<<"$(host_prepare)")" \
        && GIT_SSH_COMMAND="$ssh_line" git push -q --force --no-verify \
            --receive-pack='flock -o gate/objects.lock git-receive-pack' "$dest:gate/objects.git" "${refs[@]}" \
        && rsync -a -e "$ssh_line" "$meta/" "$dest:$remote.meta/" \
        && rsync -a --delete -e "$ssh_line" --exclude=/.git --exclude=/.worktrees/ --filter=':- .gitignore' \
            ./ "$dest:$remote/"
}

# remote_command <script> <name> [args...]: the command line that runs the script on the box with `bash -c`, its $0
# the name and each argument one word.
remote_command() {
    local text="$1"
    shift
    printf 'bash -c %q' "$text"
    printf ' %q' "$@"
}

# The run's output as shown here: $collect/out and $collect/err hold every byte of the box's out and err logs shown so
# far, so a stream that reconnects starts at the byte after them, and nothing is shown twice or left out.
collect=""
run_id=""
collect_secs="${ORRERY_GATE_COLLECT_SECS:-120}"
stream_pids=()

shown_bytes() {
    echo "$(($(stat -c %s "$collect/out") + $(stat -c %s "$collect/err")))"
}

# read_stream <out|err>: passes one stream connection's output on to stdout or stderr. The box's first line,
# "orrery-gate-stream <run> <state>", goes to $collect/<log>.state, and every byte after it is shown and appended to
# $collect/<log>. Output that does not start with that line did not come from the stream and is shown as it is. After a
# drop ($collect/lost), the stderr stream says the run is back as its first line arrives, before its output goes on.
read_stream() {
    local line fd=1
    [ "$1" = out ] || fd=2
    if IFS= read -r line; then
        if [[ $line == "orrery-gate-stream $run_id "* ]]; then
            printf '%s\n' "${line#"orrery-gate-stream $run_id "}" >"$collect/$1.state"
            if [ "$1" = err ] && [ -e "$collect/lost" ]; then
                rm -f -- "$collect/lost"
                echo "gate-remote: reconnected to $dest; the run's output goes on from where it stopped" >&2
            fi
            exec tee -a "$collect/$1" >&"$fd"
        fi
        printf '%s\n' "$line" >&"$fd"
    fi
    exec cat >&"$fd"
}

# One attempt to stream both logs from the bytes already shown. Each log has a connection of its own, so the box's
# stderr never mixes with ssh's own messages. When one connection fails the other is ended too, so both reconnect
# together. streamed is 1 when both connections ended as the box ended them.
streamed=0
stream_logs() {
    local log first other status release failed=0
    local -a readers=()
    local -A ssh_pid=()
    stream_pids=()
    for log in out err; do
        rm -f -- "$collect/$log.fifo" "$collect/$log.state"
        mkfifo "$collect/$log.fifo"
        read_stream "$log" <"$collect/$log.fifo" &
        readers+=("$!")
        stream_pids+=("$!")
        "${ssh_cmd[@]}" "$dest" "$(remote_command "$(host_stream)" orrery-gate-stream "$sub" "$run_id" "$log" \
            "$(stat -c %s "$collect/$log")")" </dev/null >"$collect/$log.fifo" &
        ssh_pid[$log]=$!
        stream_pids+=("$!")
    done
    status=0
    wait -n -p first "${ssh_pid[out]}" "${ssh_pid[err]}" || status=$?
    other="${ssh_pid[out]}"
    [ "$first" != "$other" ] || other="${ssh_pid[err]}"
    if [ "$status" -ne 0 ]; then
        failed=1
        kill -TERM "$other" 2>/dev/null || true
    fi
    status=0
    wait "$other" || status=$?
    [ "$status" -eq 0 ] || failed=1
    # A connection ended before it opened its FIFO leaves the reader waiting for a writer: opening the FIFO for reading
    # and writing never blocks, and closing it again ends that reader's input.
    for log in out err; do
        exec {release}<>"$collect/$log.fifo"
        exec {release}>&-
    done
    wait "${readers[@]}" || true
    stream_pids=()
    streamed=$((1 - failed))
}

# Stops this run on the box, as far as the box answers within 20 s.
stop_remote() {
    timeout --foreground 20 "${ssh_cmd[@]}" "$dest" "$(remote_command "$(host_stop)" orrery-gate-stop "$sub" "$run_id")" \
        </dev/null >/dev/null 2>&1 || true
}

# Collecting is over: a signal no longer stops the run on the box.
stop_collecting() {
    trap - TERM INT HUP
}

run_remote() {
    local top head origin_main launch round
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
    collect="$meta/collect"
    mkdir "$collect"
    : >"$collect/out"
    : >"$collect/err"
    launch="$(remote_command "$(host_launch)" orrery-gate-launch "$run_id" "$sub" "$(host_runner)" "$(box_run)" \
        "${ORRERY_GATE_FULL_SLOTS:-}" "${ORRERY_GATE_LIGHT_SLOTS:-}" "$origin_main" "$head" "$@")"

    echo "gate-remote: running $(printf '%q ' "$@")on $dest (load $one $five $fifteen, nproc $cpus)" >&2
    started="$(date +%s)"
    # A signal while the run is collected ends its connections and stops the run on the box, as the connection's end
    # stopped it when the run lived and died with its connection.
    trap 'stop_collecting; kill -TERM "${stream_pids[@]}" 2>/dev/null || true; stop_remote; exit 143' TERM
    trap 'stop_collecting; kill -TERM "${stream_pids[@]}" 2>/dev/null || true; stop_remote; exit 130' INT
    trap 'stop_collecting; kill -TERM "${stream_pids[@]}" 2>/dev/null || true; stop_remote; exit 129' HUP
    local launched=0 deadline=0 delay=1 now begun shown connected out_state err_state record="" state
    while :; do
        begun="$(date +%s)"
        shown="$(shown_bytes)"
        if [ "$launched" -eq 0 ] && "${ssh_cmd[@]}" "$dest" "$launch" </dev/null; then
            launched=1
        fi
        connected=0
        streamed=0
        out_state=""
        err_state=""
        if [ "$launched" -eq 1 ]; then
            stream_logs
            out_state="$(cat "$collect/out.state" 2>/dev/null || true)"
            err_state="$(cat "$collect/err.state" 2>/dev/null || true)"
            if [ -n "$out_state" ] && [ -n "$err_state" ]; then
                connected=1
            fi
        fi
        now="$(date +%s)"
        # The run is being collected again once a connection brings output, stays up for 5 s or ends as the box ends
        # it; a connection that fails at once each time counts towards the bound.
        if [ "$connected" -eq 1 ] && { [ "$streamed" -eq 1 ] || [ "$(shown_bytes)" -gt "$shown" ] || [ $((now - begun)) -ge 5 ]; }; then
            deadline=0
            delay=1
        fi
        if [ "$streamed" -eq 1 ] && [ "$connected" -eq 1 ]; then
            if [ "$out_state" = unknown ] || [ "$err_state" = unknown ]; then
                stop_collecting
                echo "gate-remote: $dest no longer knows the run (its checkout was removed or replaced); environment result, not a test failure (exit $environment_exit)" >&2
                exit "$environment_exit"
            fi
            # Both logs were followed until the run ended; the next connection reads its record.
            for state in "$out_state" "$err_state"; do
                if [ "${state%% *}" = ended ]; then
                    read -r _ record _ <<<"$state"
                    break 2
                fi
            done
            continue
        fi
        # Each drop after the run was reached again is said once, and the stderr stream says when it is back.
        if [ ! -e "$collect/lost" ]; then
            : >"$collect/lost"
            echo "gate-remote: the connection to $dest dropped; reconnecting for up to ${collect_secs}s to collect the run" >&2
        fi
        if [ "$deadline" -eq 0 ]; then
            deadline=$((now + collect_secs))
        fi
        if [ "$now" -ge "$deadline" ]; then
            stop_collecting
            if probe "$dest"; then
                stop_remote
                if [ "$launched" -eq 1 ]; then
                    echo "gate-remote: lost the connection to $dest after the command started and could not collect the run within ${collect_secs}s; environment result, not a test failure (exit $environment_exit)" >&2
                else
                    echo "gate-remote: lost the connection to $dest as the command started and could not start or collect the run within ${collect_secs}s; environment result, not a test failure (exit $environment_exit)" >&2
                fi
            else
                echo "gate-remote: $dest went away after the command started (a spot interruption or the box ended); environment result, not a test failure (exit $environment_exit)" >&2
                start_wake
            fi
            exit "$environment_exit"
        fi
        sleep "$((delay < deadline - now ? delay : deadline - now))"
        delay=$((delay * 2 > 16 ? 16 : delay * 2))
    done
    stop_collecting

    # The box recorded this run's exit; a run that ended without one was killed there.
    case "$record" in
        "")
            stop_remote
            echo "gate-remote: the run on $dest ended without recording its exit (it was killed there); environment result, not a test failure (exit $environment_exit)" >&2
            exit "$environment_exit"
            ;;
        setup)
            exec 8>&-
            run_here "$dest could not set up its checkout" "$@"
            ;;
        noscope)
            exec 8>&-
            run_here "$dest has no user manager for the run's scope" "$@"
            ;;
    esac
    finished "$record" "on $dest" "$@"
    exit "$record"
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
