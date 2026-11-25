#!/usr/bin/env bash
#
# Tests scripts/gate-remote.sh without AWS or a network: the "box" is a directory standing in for the orrery user's home,
# reached through a fake ssh that runs each remote command there. A fixture repository carries a copy of the script and
# a scripts/in-devenv that runs bash directly. Covers the push, the sync, the run, exit codes, waiting for a box when
# the known one is gone, the box cache wrapping a run, the local fallback when none can come up, a box lost mid-run,
# connections that drop mid-run while the box stays up (the run's status and its whole output, once, after one drop or
# many), a run that cannot be collected or never records its exit, stopping the script and killing it outright, and
# `wake` against a fake ec2, including two runs that share one launch.
#
# Usage: scripts/gate-remote-test.sh   (exits 0 when every case passes)

set -euo pipefail

script="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/gate-remote.sh"
root="$(mktemp -d)"
# Every run a case starts leads a session of its own (spawn below) and is recorded here, so the cleanup can end it with
# everything it started before the fixture goes.
runs=()
# Ends each run's process group (TERM, then KILL once 10 s pass) and waits for the group to empty, ends what the scope
# cases left running on purpose, and only then removes the fixture. A process still naming the fixture after that has
# outlived its run: it is listed and killed, and the test fails.
cleanup() {
    local status=$? p f i leaked
    if [ -n "$guard" ]; then
        pkill -P "$guard" 2>/dev/null || true
        kill "$guard" 2>/dev/null || true
    fi
    for p in "${runs[@]}"; do
        kill -TERM -- "-$p" 2>/dev/null || true
    done
    for p in "${runs[@]}"; do
        wait "$p" 2>/dev/null || true
        for ((i = 0; i < 100; i++)); do
            kill -0 -- "-$p" 2>/dev/null || break
            sleep 0.1
        done
        kill -KILL -- "-$p" 2>/dev/null || true
    done
    for f in "$root"/orphan*; do
        [ ! -s "$f" ] || kill "$(cat "$f")" 2>/dev/null || true
    done
    leaked="$(pgrep -af -- "$root/" || true)"
    if [ -n "$leaked" ]; then
        echo "FAIL: processes outlived the test:" >&2
        while read -r p f; do
            echo "    $p $f" >&2
            kill -KILL "$p" 2>/dev/null || true
        done <<<"$leaked"
        status=1
    fi
    rm -rf -- "$root" || true
    exit "$status"
}
trap cleanup EXIT
trap 'exit 143' TERM
# The hang guard: a case that waits on a run or a state that never comes (no wait in this test has a bound of its own,
# so a loaded host only slows a passing run) fails the whole test after guard_secs, through the TERM trap and the
# cleanup, instead of hanging it. A passing run, even on a host at load 300, ends long before; the bound stays under the
# 1800 s a gate gives `just check`.
guard_secs=1500
guard=""
(sleep "$guard_secs" && echo "FAIL: the test still runs after ${guard_secs}s: the case after the last ok above hangs" >&2 \
    && kill -TERM $$) &
guard=$!
trap 'exit 130' INT
trap 'exit 129' HUP

failures=0
fail() {
    echo "FAIL: $*" >&2
    failures=$((failures + 1))
}
pass() {
    echo "ok: $*"
}

mkdir -p "$root/bin" "$root/boxbin" "$root/box" "$root/run"

# The fake ssh drops options, then runs the command in the box directory with HOME set to it. A connection that streams
# the run's output relays it as ssh does: the box's side runs in a session of its own, and when the connection ends
# (ssh is killed, or a switch below drops it) that side ends with it, while the run itself goes on. Switches:
#   $root/box-down    every connection fails with 255, as ssh does when the box is gone;
#   $root/down-<dest> connections to that destination fail with 255;
#   $root/drop-run    every connection that streams the run's output fails with 255 before it carries anything; with
#                     "box-gone" in the file the box is gone afterwards too, otherwise it still answers;
#   $root/cut-stream  lines of "<out|err> <bytes> [box-gone]": the next connection that streams that log passes on its
#                     first line and then that many bytes, then drops (255), using up the line; with "box-gone" the box
#                     is gone afterwards too;
#   $root/no-push     git's connection fails; with "and-down" in the file its destination is gone afterwards too, and
#                     the switch is used up.
cat >"$root/bin/ssh" <<'EOF'
#!/usr/bin/env bash
while [ "$#" -gt 0 ]; do
    case "$1" in
        -o | -i | -p | -l) shift 2 ;;
        -*) shift ;;
        *) break ;;
    esac
done
host="$1"
shift
cmd="$*"
echo "$host $cmd" >>"$FAKE_ROOT/ssh.log"
[ ! -e "$FAKE_ROOT/box-down" ] || exit 255
[ ! -e "$FAKE_ROOT/down-$host" ] || exit 255
if [ -e "$FAKE_ROOT/no-push" ] && [[ $cmd == *git-receive-pack* ]]; then
    if grep -q and-down "$FAKE_ROOT/no-push"; then
        touch "$FAKE_ROOT/down-$host"
        rm -f "$FAKE_ROOT/no-push"
    fi
    exit 128
fi
if [ -e "$FAKE_ROOT/drop-run" ] && [[ $cmd == *orrery-gate-stream* ]]; then
    if grep -q box-gone "$FAKE_ROOT/drop-run"; then
        touch "$FAKE_ROOT/box-down"
    fi
    exit 255
fi
unset $(git rev-parse --local-env-vars)
# ssh closes every descriptor above stderr when it starts, so the box never inherits one of the caller's.
for fd in /proc/$$/fd/*; do
    fd="${fd##*/}"
    if [ "$fd" -gt 2 ]; then
        eval "exec $fd>&-" 2>/dev/null || true
    fi
done
if [[ $cmd != *orrery-gate-stream* ]]; then
    cd "$FAKE_ROOT/box" && HOME="$FAKE_ROOT/box" PATH="$FAKE_ROOT/boxbin:$PATH" exec bash -c "$cmd"
fi
cut="" gone=""
if [ -s "$FAKE_ROOT/cut-stream" ] && [[ $cmd =~ \ (out|err)\ [0-9]+$ ]]; then
    read -r log bytes then <"$FAKE_ROOT/cut-stream"
    if [ "$log" = "${BASH_REMATCH[1]}" ]; then
        cut="$bytes"
        gone="$then"
        sed -i 1d "$FAKE_ROOT/cut-stream"
    fi
fi
relay="$(mktemp -u "$FAKE_ROOT/relay.XXXXXX")"
mkfifo "$relay"
# The connection ends: the box's side of it ends too. That side is its own session once it has opened the relay, and
# until then a process of this one blocked in the open, with this connection's stdout, so both are ended: left blocked,
# it would hold the caller's end open and the caller would wait on it forever. The trap is set before that side starts,
# so no end of the connection can come between its start and the trap.
far=""
hangup() {
    local side="${far:-$!}"
    if [ -n "$side" ]; then
        kill -TERM -- "-$side" "$side" 2>/dev/null
    fi
    rm -f "$relay"
    [ "$gone" != box-gone ] || touch "$FAKE_ROOT/box-down"
    exit 255
}
trap hangup TERM
(cd "$FAKE_ROOT/box" && HOME="$FAKE_ROOT/box" PATH="$FAKE_ROOT/boxbin:$PATH" exec setsid bash -c "$cmd") >"$relay" &
far=$!
if [ -n "$cut" ]; then
    { IFS= read -r line && printf '%s\n' "$line" && head -c "$cut"; } <"$relay"
    hangup
fi
cat <"$relay" &
wait "$!"
status=0
wait "$far" || status=$?
rm -f "$relay"
exit "$status"
EOF
# The box's fake systemd: a scope is a process group. systemd-run --scope records "<unit> <pid>" in $root/scopes and
# runs the command as the leader of a new session; systemctl --user stop [--no-block] <unit>.scope records the unit in
# $root/stops and kills that group. systemctl --user show-environment fails while $root/no-user-manager exists.
cat >"$root/boxbin/systemd-run" <<'EOF'
#!/usr/bin/env bash
unit=""
while [ "$#" -gt 0 ]; do
    case "$1" in
        --unit=*) unit="${1#--unit=}"; shift ;;
        -p) shift 2 ;;
        --) shift; break ;;
        *) shift ;;
    esac
done
echo "$unit $$" >>"$FAKE_ROOT/scopes"
exec setsid "$@"
EOF
cat >"$root/boxbin/systemctl" <<'EOF'
#!/usr/bin/env bash
case "$*" in
    "--user show-environment") [ ! -e "$FAKE_ROOT/no-user-manager" ] ;;
    "--user stop "*.scope | "--user stop --no-block "*.scope)
        unit="${*: -1}"
        unit="${unit%.scope}"
        echo "$unit" >>"$FAKE_ROOT/stops"
        pid="$(awk -v u="$unit" '$1 == u { print $2 }' "$FAKE_ROOT/scopes")"
        [ -z "$pid" ] || kill -TERM -- "-$pid" 2>/dev/null
        exit 0
        ;;
    *) exit 1 ;;
esac
EOF
chmod +x "$root/boxbin/"*

# The fake wake: with a destination in $root/wake-brings it writes it to the host file, as a wake that found or launched
# a box does; otherwise it fails as a refused launch does.
cat >"$root/bin/wake" <<'EOF'
#!/bin/sh
touch "$FAKE_ROOT/woke"
if [ -s "$FAKE_ROOT/wake-brings" ]; then
    cp "$FAKE_ROOT/wake-brings" "$ORRERY_GATE_HOST_FILE"
    echo "2026-01-01T00:00:00+00:00 gate-remote: the gate box is $(cat "$FAKE_ROOT/wake-brings")"
    exit 0
fi
echo "2026-01-01T00:00:00+00:00 gate-remote: orrery-gate launch refused: fake capacity"
exit 1
EOF
cat >"$root/bin/local" <<'EOF'
#!/bin/sh
touch "$FAKE_ROOT/ran-here"
exec "$@"
EOF
# The fake ec2: `ls` prints a header and $root/ec2-ls, `up` records the call and prints $root/ec2-up, exiting with the
# status in $root/ec2-up-status. When $root/ec2-up-fifo exists, `up` prints what is written to it instead, so a test
# holds the launch open until it writes there (for 20 s at most, so a test that never writes fails instead of hanging;
# --foreground keeps timeout in the run's process group, so ending the run ends it).
cat >"$root/bin/ec2" <<'EOF'
#!/bin/sh
case "$1" in
    ls)
        echo "NAME  CLASS  TYPE  SPOT  AGE  ON-DEMAND/HR  SPOT/HR"
        [ ! -e "$FAKE_ROOT/ec2-ls" ] || cat "$FAKE_ROOT/ec2-ls"
        ;;
    up)
        echo "$*" >>"$FAKE_ROOT/ec2-up-calls"
        if [ -p "$FAKE_ROOT/ec2-up-fifo" ]; then
            timeout --foreground 20 cat "$FAKE_ROOT/ec2-up-fifo"
        else
            cat "$FAKE_ROOT/ec2-up"
        fi
        exit "$(cat "$FAKE_ROOT/ec2-up-status" 2>/dev/null || echo 0)"
        ;;
esac
EOF
chmod +x "$root/bin/"*

export FAKE_ROOT="$root"
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=test GIT_AUTHOR_EMAIL=test@example.invalid
export GIT_COMMITTER_NAME=test GIT_COMMITTER_EMAIL=test@example.invalid
export XDG_RUNTIME_DIR="$root/run"
export ORRERY_GATE_SSH="$root/bin/ssh"
export ORRERY_GATE_WAKE="$root/bin/wake"
export ORRERY_GATE_LOCAL="$root/bin/local"
export ORRERY_GATE_EC2="$root/bin/ec2"
export ORRERY_GATE_HOST_FILE="$root/gate-host"
# A run gives up collecting after 2 s of failed reconnects, so the cases that lose the box end soon. The cases whose
# connections drop and are meant to reconnect and collect the run get recover_secs instead, so a loaded host cannot
# turn a slow reconnect into a give-up.
export ORRERY_GATE_COLLECT_SECS=2
recover_secs=30
# A gate run that fell back here exports its Android lock, so a check run under one would hand the tests that path.
unset ORRERY_GATE_DEST ORRERY_GATE_PROBE ORRERY_GATE_USER ORRERY_GATE_REMOTE ORRERY_ANDROID_LOCK

work="$root/work"
mkdir -p "$work/scripts"
cp "$script" "$work/scripts/gate-remote.sh"
printf '#!/bin/sh\nexec bash "$@"\n' >"$work/scripts/in-devenv"
chmod +x "$work/scripts/in-devenv"
printf 'ignored.txt\n/build/\n' >"$work/.gitignore"
echo a1 >"$work/a.txt"
echo b1 >"$work/b.txt"
git -C "$work" init -q
git -C "$work" add -A
git -C "$work" commit -q -m one
git -C "$work" tag v0.0.1
git -C "$work" update-ref refs/remotes/origin/main HEAD
echo c1 >"$work/c.txt"
git -C "$work" add c.txt
git -C "$work" commit -q -m two
head="$(git -C "$work" rev-parse HEAD)"
main="$(git -C "$work" rev-parse refs/remotes/origin/main)"
sub="work-$(printf '%s' "$work" | sha256sum | cut -c1-8)"

# spawn <stdout> <stderr> <dir> <command...>: starts the command from dir in the background as the leader of a session
# of its own, so the cleanup can end it with everything it starts; its pid goes in $spawned and in runs.
spawned=""
spawn() {
    local out="$1" err="$2" dir="$3"
    shift 3
    (cd "$dir" && exec setsid "$@") >"$out" 2>"$err" &
    spawned=$!
    runs+=("$spawned")
}

# gate <stdout> <stderr> <args...>: runs the script in the fixture and waits for it, leaving its status in $rc.
gate() {
    local out="$1" err="$2"
    shift 2
    rm -f "$root/woke" "$root/ran-here"
    rc=0
    spawn "$out" "$err" "$work" scripts/gate-remote.sh "$@"
    wait "$spawned" || rc=$?
}

# appears <file> [pattern]: true once the file exists (and holds a line matching the pattern); what writes it runs in
# the background, so wait for it, bounded only by the guard.
appears() {
    until [ -e "$1" ] && { [ -z "${2:-}" ] || grep -qE -- "$2" "$1"; }; do
        sleep 0.1
    done
}
# ends <pid>: once the run, a child of this shell, has ended, its status is in $rc; a run that never ends is the guard's.
ends() {
    rc=0
    wait "$1" || rc=$?
}
woke() {
    appears "$root/woke"
}

expect_status() {
    if [ "$rc" -eq "$1" ]; then pass "$2: exit $1"; else fail "$2: exit $rc, want $1"; fi
}
expect() {
    local what="$1"
    shift
    if "$@"; then pass "$what"; else fail "$what"; fi
}
# expect_no_line <file> <pattern> <what>: no line of the file matches the pattern.
expect_no_line() {
    if grep -qE -- "$2" "$1"; then fail "$3: a line matches '$2' in:"; sed 's/^/    /' "$1" >&2; else pass "$3"; fi
}
expect_line() {
    if grep -qE -- "$2" "$1"; then pass "$3"; else fail "$3: no line matching '$2' in:"; sed 's/^/    /' "$1" >&2; fi
}

echo "orrery@fake-box" >"$root/gate-host"

# A remote run sees this work tree as it is: an uncommitted edit, a staged edit, an untracked file, HEAD, origin/main
# and the tags, and no git-ignored file. Arguments arrive as words; the command's status comes back.
echo a2 >"$work/a.txt"
echo b2 >"$work/b.txt"
git -C "$work" add b.txt
echo u1 >"$work/untracked.txt"
echo secret >"$work/ignored.txt"
look=$(cat <<'EOF'
    cat a.txt untracked.txt
    echo "head $(git rev-parse HEAD)"
    echo "main $(git rev-parse origin/main)"
    echo "tags $(git tag --merged HEAD)"
    echo "staged $(git diff --cached --name-only)"
    echo "unstaged $(git diff --name-only)"
    [ -e ignored.txt ] || echo "no ignored"
    echo "args $#:$1"
    exit 3
EOF
)
gate "$root/out" "$root/err" bash -c "$look" _ "two words"
expect_status 3 "remote run returns the command's status"
expect_line "$root/err" '^gate-remote: bash .*exited 3 after [0-9]+s on orrery@fake-box$' "remote run reports its exit and time"
expect_line "$root/err" '^gate-remote: running bash .* on orrery@fake-box \(load [0-9.]+ [0-9.]+ [0-9.]+, nproc [0-9]+\)$' "remote run names the box with its load and nproc"
expect_line "$root/out" '^a2$' "uncommitted edit is synced"
expect_line "$root/out" '^u1$' "untracked file is synced"
expect_line "$root/out" "^head $head\$" "box checkout HEAD is this HEAD"
expect_line "$root/out" "^main $main\$" "box checkout origin/main is this origin/main"
expect_line "$root/out" '^tags v0.0.1$' "tags reach the box checkout"
expect_line "$root/out" '^staged b.txt$' "staged changes reach the box index"
expect_line "$root/out" '^unstaged a.txt$' "unstaged edits stay unstaged on the box"
expect_line "$root/out" '^no ignored$' "git-ignored files never reach the box"
expect_line "$root/out" '^args 1:two words$' "arguments arrive as words"
if [ -d "$root/box/gate/objects.git" ] && git -C "$root/box/gate/objects.git" cat-file -e "$head^{commit}" \
    && grep -qxF "$root/box/gate/objects.git/objects" "$root/box/gate/$sub/.git/objects/info/alternates" \
    && [ ! -e "$root/box/gate/$sub/.git/objects/${head:0:2}/${head:2}" ]; then
    pass "commits land in the one object store, which the checkout borrows"
else
    fail "commits land in the one object store, which the checkout borrows"
fi
expect "remote run runs nothing here" test ! -e "$root/ran-here"

# A second run: a file removed here is removed on the box, while the box's own ignored build output stays.
rm "$work/untracked.txt"
mkdir -p "$root/box/gate/$sub/build"
echo out >"$root/box/gate/$sub/build/out"
gate "$root/out" "$root/err" bash -c '[ ! -e untracked.txt ] && [ -e build/out ]'
expect_status 0 "sync removes deleted files and keeps the box's ignored output"

# A box short of room after the stale-checkout prune trims Go build cache entries unused for an hour and keeps the
# ones a run used since.
mkdir -p "$root/box/.cache/orrery/build/ab"
echo old >"$root/box/.cache/orrery/build/ab/old-a"
echo new >"$root/box/.cache/orrery/build/ab/new-a"
touch -d '3 hours ago' "$root/box/.cache/orrery/build/ab/old-a"
printf '#!/usr/bin/env bash\nprintf "Avail\\n0\\n"\n' >"$root/boxbin/df"
chmod +x "$root/boxbin/df"
gate "$root/out" "$root/err" true
rm -f "$root/boxbin/df"
expect_status 0 "a box short of room still runs the command"
expect "a box short of room trims a Go cache entry unused for an hour" test ! -e "$root/box/.cache/orrery/build/ab/old-a"
expect "a box short of room keeps a Go cache entry used within the hour" test -e "$root/box/.cache/orrery/build/ab/new-a"
expect_line "$root/err" 'trimming the Go build cache on .*under 30 GiB free' "a cache trim says so"

# A box with room leaves the Go build cache alone. Its df reports 1 TiB free, so the case does not read the free space
# of the machine the test runs on.
touch -d '3 hours ago' "$root/box/.cache/orrery/build/ab/new-a"
printf '#!/usr/bin/env bash\nprintf "Avail\\n1073741824\\n"\n' >"$root/boxbin/df"
chmod +x "$root/boxbin/df"
gate "$root/out" "$root/err" true
rm -f "$root/boxbin/df"
expect_status 0 "a box with room runs the command"
expect "a box with room keeps an old Go cache entry" test -e "$root/box/.cache/orrery/build/ab/new-a"
expect_no_line "$root/err" 'trimming the Go build cache' "a box with room trims nothing"

# A remote command that exits 255 itself is the command's result, not a lost box.
gate "$root/out" "$root/err" bash -c 'exit 255'
expect_status 255 "a command's own 255 comes back as 255"

# A box that mounts no fleet cache runs the command as it is; one with a cache root runs it under
# scripts/box-cache.sh run with that root, and the command's output and status come back through it.
cat >"$work/scripts/box-cache.sh" <<'EOF'
#!/usr/bin/env bash
echo "$1 $2" >>"$FAKE_ROOT/box-cache.calls"
shift 2
exec "$@"
EOF
chmod +x "$work/scripts/box-cache.sh"
rm -f "$root/box-cache.calls"
gate "$root/out" "$root/err" true
expect_status 0 "a box with no fleet cache runs the command"
expect "a box with no fleet cache runs no box cache" test ! -e "$root/box-cache.calls"
export ORRERY_BOX_CACHE_ROOT="$root/fleet"
gate "$root/out" "$root/err" bash -c 'echo cached-ran; exit 6'
unset ORRERY_BOX_CACHE_ROOT
expect_status 6 "a cached run returns the command's status"
expect_line "$root/out" '^cached-ran$' "a cached run's output comes back"
expect_line "$root/box-cache.calls" "^run $root/fleet\$" "a box with a cache root runs the command under box-cache.sh run"
rm "$work/scripts/box-cache.sh"

# gone <pid>: true once the process has ended; the scope's stop signals it, so wait for it, bounded only by the guard.
gone() {
    while kill -0 "$1" 2>/dev/null; do
        sleep 0.1
    done
}

# The command runs in its own scope, which is stopped when the run ends: a child the command left behind ends with it,
# and the checkout's lock is free once the run is over.
rm -f "$root/scopes" "$root/stops"
leave=$(cat <<'EOF'
sleep 300 </dev/null >/dev/null 2>&1 &
echo "$!" >"$FAKE_ROOT/orphan-left"
exit 9
EOF
)
gate "$root/out" "$root/err" bash -c "$leave"
expect_status 9 "a run in a scope returns the command's status"
unit="$(awk 'NR == 1 { print $1 }' "$root/scopes" 2>/dev/null)"
expect "the run gets its own orrery-gate scope" test -n "$unit" -a "${unit#orrery-gate-}" != "$unit"
expect "the run's scope is stopped when it ends" grep -qxF "$unit" "$root/stops"
expect "a child the command left behind ends with the run" gone "$(cat "$root/orphan-left")"
expect "the checkout's lock is free after the run" flock -n "$root/box/gate/$sub.lock" true

# flock -o: only flock holds the lock, so a process that escapes the scope (here, into its own session) does not hold
# it, and the next run in the checkout starts.
escape=$(cat <<'EOF'
setsid sleep 300 </dev/null >/dev/null 2>&1 &
echo "$!" >"$FAKE_ROOT/orphan-escaped"
EOF
)
gate "$root/out" "$root/err" bash -c "$escape"
expect_status 0 "a run that leaves an escaped process"
expect "the escaped process outlives the fake scope" kill -0 "$(cat "$root/orphan-escaped")"
expect "an escaped process does not hold the checkout's lock" flock -n "$root/box/gate/$sub.lock" true
# holds_no <pid> <path>: the process has no descriptor open on the path.
holds_no() {
    local fd
    for fd in /proc/"$1"/fd/*; do
        [ "$(readlink "$fd" 2>/dev/null)" != "$2" ] || return 1
    done
}
expect "the escaped process holds no descriptor of the lock" \
    holds_no "$(cat "$root/orphan-escaped")" "$root/box/gate/$sub.lock"
if flock -n "$root/box/gate/$sub.lock" true; then
    gate "$root/out" "$root/err" true
    expect_status 0 "the next run in the checkout starts"
else
    fail "the next run in the checkout would wait on the lock the escaped process holds"
fi
kill "$(cat "$root/orphan-escaped")"

# A box whose user manager does not answer runs nothing there, since its run could not be scoped.
touch "$root/no-user-manager"
rm -f "$root/scopes"
gate "$root/out" "$root/err" bash -c 'exit 8'
expect_status 8 "a box without a user manager runs the command here"
expect_line "$root/err" 'here \(orrery@fake-box has no user manager for the run.s scope' "a box without a user manager says so"
expect "a box without a user manager starts no scope" test ! -e "$root/scopes"
rm "$root/no-user-manager"

# The box does not answer: the run waits for the wake, which brings another box, and runs there, never here.
touch "$root/down-orrery@fake-box"
echo "orrery@fake-box2" >"$root/wake-brings"
gate "$root/out" "$root/err" bash -c 'echo there-ran; exit 4'
expect_status 4 "a run that waited for a box returns the command's status"
expect_line "$root/err" '^gate-remote: orrery@fake-box did not answer; waiting for an orrery-gate box' "a gone box says the run waits for one"
expect_line "$root/err" '^gate-remote: running bash .* on orrery@fake-box2 \(load' "the run goes to the box the wake brought"
expect_line "$root/out" '^there-ran$' "the command runs on the new box"
expect "a run that waited for a box runs nothing here" test ! -e "$root/ran-here"
expect "a gone box runs the wake" test -e "$root/woke"
expect_line "$root/run/orrery-gate-wake.log" 'the gate box is orrery@fake-box2' "the wake's output goes to the wake log"
echo "orrery@fake-box" >"$root/gate-host"

# No box can come up (the launch is refused): only then the command runs here, with the wake's reason.
: >"$root/wake-brings"
gate "$root/out" "$root/err" bash -c 'echo here-ran; exit 4'
expect_status 4 "fallback returns the local command's status"
expect_line "$root/err" '^gate-remote: running bash .* here \(no orrery-gate box could come up: orrery-gate launch refused: fake capacity; load [0-9.]+ [0-9.]+ [0-9.]+, nproc [0-9]+\)$' "fallback names the wake's reason, with load and nproc"
expect_line "$root/err" '^gate-remote: bash .*exited 4 after [0-9]+s here$' "fallback reports its exit and time"
expect_line "$root/out" '^here-ran$' "fallback runs the command"
expect "fallback goes through ORRERY_GATE_LOCAL" test -e "$root/ran-here"
gate "$root/out" "$root/err" sh -c 'echo "$ORRERY_ANDROID_LOCK"'
expect_line "$root/out" "^$root/run/orrery-android-build.lock\$" "fallback has its Android builds take turns on this machine's lock"

# The wake ends with a box that does not answer either: the command runs here.
echo "orrery@fake-box" >"$root/wake-brings"
gate "$root/out" "$root/err" true
expect_line "$root/err" 'here \(no orrery-gate box could come up: orrery@fake-box did not answer after the wake' "a woken box that does not answer runs here"
rm "$root/down-orrery@fake-box"

# No box known yet: the run waits for the wake and runs on the box it brings.
rm "$root/gate-host"
gate "$root/out" "$root/err" true
expect_status 0 "no host file waits for the wake"
expect_line "$root/err" '^gate-remote: no gate box is known yet; waiting for an orrery-gate box' "no host file says so"
expect_line "$root/err" 'running true on orrery@fake-box \(' "no host file runs on the woken box"
: >"$root/wake-brings"

# An ORRERY_GATE_DEST that does not answer runs here without a wake: the wake could not change it.
touch "$root/down-orrery@fake-down"
ORRERY_GATE_DEST=orrery@fake-down gate "$root/out" "$root/err" true
expect_line "$root/err" 'here \(ORRERY_GATE_DEST orrery@fake-down did not answer' "an unanswering ORRERY_GATE_DEST runs here"
expect "an unanswering ORRERY_GATE_DEST runs no wake" test ! -e "$root/woke"

# ORRERY_GATE_REMOTE=0 skips the box.
ORRERY_GATE_REMOTE=0 gate "$root/out" "$root/err" true
expect_line "$root/err" 'here \(ORRERY_GATE_REMOTE=0' "ORRERY_GATE_REMOTE=0 runs here"

# The box cannot be prepared (here, the push fails): the command runs here.
touch "$root/no-push"
gate "$root/out" "$root/err" bash -c 'exit 5'
expect_status 5 "a box that cannot be prepared runs the command here"
expect_line "$root/err" 'here \(orrery@fake-box could not be prepared' "an unprepared box says so"
expect "a box that answers but cannot be prepared runs no wake" test ! -e "$root/woke"

# The box stops answering while it is prepared: the run finds another box and runs there.
echo and-down >"$root/no-push"
echo "orrery@fake-box2" >"$root/wake-brings"
gate "$root/out" "$root/err" bash -c 'exit 5'
expect_status 5 "a box lost while it is prepared is replaced"
expect_line "$root/err" 'orrery@fake-box stopped answering while it was prepared' "a box lost while it is prepared says so"
expect_line "$root/err" 'running bash .* on orrery@fake-box2 \(' "the run moves to the replacement box"
expect "a box lost while it is prepared runs nothing here" test ! -e "$root/ran-here"
rm "$root/down-orrery@fake-box"
: >"$root/wake-brings"
echo "orrery@fake-box" >"$root/gate-host"

# The box cannot set up its checkout (here, a stale index lock): nothing ran there, so the command runs here.
touch "$root/box/gate/$sub/.git/index.lock"
gate "$root/out" "$root/err" bash -c 'exit 6'
expect_status 6 "a checkout the box cannot set up runs the command here"
expect_line "$root/err" 'here \(orrery@fake-box could not set up its checkout' "a checkout that cannot be set up says so"
rm "$root/box/gate/$sub/.git/index.lock"

# The box goes away mid-run (a spot interruption): an environment result, 75, and a wake for the next run.
echo box-gone >"$root/drop-run"
gate "$root/out" "$root/err" bash -c 'exit 1'
expect_status 75 "a box lost mid-run is an environment result"
expect_line "$root/err" 'went away after the command started.*spot interruption.*environment result, not a test failure \(exit 75\)' "a lost box says so"
expect "a lost box does not rerun here" test ! -e "$root/ran-here"
expect "a lost box starts the wake" woke
rm -f "$root/box-down"

# The connection drops but the box still answers: also 75, without this run's exit record.
echo keep >"$root/drop-run"
gate "$root/out" "$root/err" bash -c 'exit 1'
expect_status 75 "a dropped connection is an environment result"
expect_line "$root/err" 'lost the connection to orrery@fake-box after .*environment result' "a dropped connection says so"
rm "$root/drop-run"

# The run lives apart from its connections. talk writes 40 numbered lines to stdout and to stderr over about 2 s, then
# exits with its argument, so the connections below drop while it runs.
talk=$(cat <<'EOF'
for i in $(seq 1 40); do
    echo "out line $i"
    echo "err line $i" >&2
    sleep 0.05
done
exit "$1"
EOF
)
printf 'out line %d\n' $(seq 1 40) >"$root/want-out"
printf 'err line %d\n' $(seq 1 40) >"$root/want-err"
dropped_line="gate-remote: the connection to orrery@fake-box dropped; reconnecting for up to ${recover_secs}s to collect the run"
reconnected_line="gate-remote: reconnected to orrery@fake-box; the run's output goes on from where it stopped"

# collected_once <what>: the run's stdout is the command's, byte for byte, and its stderr, without the lines saying the
# connection dropped and came back (which a drop in the middle of a line puts inside it), holds the command's lines
# once each and in order.
collected_once() {
    local err
    if cmp -s "$root/want-out" "$root/out"; then
        pass "$1: stdout arrives whole and once"
    else
        fail "$1: stdout differs from the command's:"
        diff "$root/want-out" "$root/out" | sed "s/^/    /" >&2 || true
    fi
    err="$(cat "$root/err")"
    err="${err//"$dropped_line"$'\n'/}"
    err="${err//"$reconnected_line"$'\n'/}"
    if grep '^err line' <<<"$err" | cmp -s "$root/want-err" -; then
        pass "$1: stderr arrives whole and once"
    else
        fail "$1: stderr differs from the command's:"
        sed 's/^/    /' "$root/err" >&2
    fi
}

# box_procs: what still runs on the fake box, a process whose working directory is in it, one "<pid> <command>" a line.
box_procs() {
    local p cwd
    for p in $(pgrep -u "$(id -u)" .); do
        cwd="$(readlink "/proc/$p/cwd" 2>/dev/null)" || continue
        [[ $cwd == "$root/box"* ]] || continue
        echo "$p $(tr '\0' ' ' <"/proc/$p/cmdline" 2>/dev/null | cut -c1-120)"
    done
}
# box_idle: true once nothing runs on the fake box, waiting like appears; what still runs when the guard ends the test
# is listed by the cleanup.
box_idle() {
    until [ -z "$(box_procs)" ]; do
        sleep 0.1
    done
}

# The connection carrying stdout drops in the middle of a line while the box stays up: the run goes on, the script
# reconnects and resumes at the byte after the last one shown, and a passing run passes.
echo "out 37" >"$root/cut-stream"
ORRERY_GATE_COLLECT_SECS="$recover_secs" gate "$root/out" "$root/err" bash -c "$talk" _ 0
expect_status 0 "a passing run whose connection dropped returns its status"
collected_once "a passing run whose connection dropped"
expect "the drop happened" test ! -s "$root/cut-stream"
expect_line "$root/err" "^$dropped_line\$" "a dropped connection says it reconnects"
expect_line "$root/err" "^$reconnected_line\$" "a dropped connection says it reconnected"
# before_line <file> <first> <second>: the first line that is <first> comes before the one that is <second>.
before_line() {
    local a b
    a="$(grep -nxF -- "$2" "$1" | head -n 1 | cut -d: -f1)"
    b="$(grep -nxF -- "$3" "$1" | head -n 1 | cut -d: -f1)"
    [ -n "$a" ] && [ -n "$b" ] && [ "$a" -lt "$b" ]
}
expect "a reconnect is said as the output comes back, before the rest of it" \
    before_line "$root/err" "$reconnected_line" "err line 40"
expect_line "$root/err" '^gate-remote: bash .*exited 0 after [0-9]+s on orrery@fake-box$' "a run collected after a drop reports its exit"
expect_no_line "$root/err" 'environment result' "a run collected after a drop is no environment result"
expect "a run collected after a drop runs nothing here" test ! -e "$root/ran-here"
expect "a run collected after a drop runs no wake" test ! -e "$root/woke"
expect "nothing of a collected run is left on the box" box_idle

# The connection carrying stderr drops in the middle of a line: a failing run fails with its own status.
echo "err 45" >"$root/cut-stream"
ORRERY_GATE_COLLECT_SECS="$recover_secs" gate "$root/out" "$root/err" bash -c "$talk" _ 3
expect_status 3 "a failing run whose connection dropped returns its status"
collected_once "a failing run whose connection dropped"
expect "the stderr drop happened" test ! -s "$root/cut-stream"
expect_no_line "$root/err" 'environment result' "a failing run collected after a drop is no environment result"

# The connections drop again and again, each time somewhere else.
printf 'out 37\nerr 45\nout 100\nerr 1\nout 0\n' >"$root/cut-stream"
ORRERY_GATE_COLLECT_SECS="$recover_secs" gate "$root/out" "$root/err" bash -c "$talk" _ 5
expect_status 5 "a run whose connections dropped again and again returns its status"
collected_once "a run whose connections dropped again and again"
expect "every drop happened" test ! -s "$root/cut-stream"
expect "nothing of a run collected after many drops is left on the box" box_idle

# The box goes away while the run goes on (a spot interruption): 75 once the reconnects give up, and a wake.
echo "out 37 box-gone" >"$root/cut-stream"
gate "$root/out" "$root/err" bash -c "$talk" _ 0
expect_status 75 "a box gone after a drop is an environment result"
expect_line "$root/err" 'went away after the command started.*spot interruption.*environment result, not a test failure \(exit 75\)' "a box gone after a drop says so"
expect "a box gone after a drop starts the wake" woke
expect "a box gone after a drop does not rerun here" test ! -e "$root/ran-here"
rm -f "$root/box-down"
expect "the run on a box that went away ends on its own" box_idle

# long: a command that says it started, records its pid, and runs for 30 s.
long=$(cat <<'EOF'
echo "$$" >"$FAKE_ROOT/long-pid"
exec sleep 30
EOF
)
# long_run: starts long from the work tree in the background and waits until it runs on the box.
long_run() {
    rm -f "$root/long-pid" "$root/woke" "$root/ran-here"
    spawn "$root/out" "$root/err" "$work" scripts/gate-remote.sh bash -c "$long"
    appears "$root/long-pid"
}
# runner_pid: the box-side runner of the work tree's run.
runner_pid() {
    local p
    for p in $(pgrep -f orrery-gate-runner); do
        if [ "$(readlink "/proc/$p/cwd" 2>/dev/null)" = "$root/box/gate/$sub" ] \
            && awk -v RS='\0' 'NR == 4 && $0 == "orrery-gate-runner" { found = 1 } END { exit !found }' "/proc/$p/cmdline"; then
            echo "$p"
        fi
    done
}

# Every connection that streams the run fails while the box answers and the run goes on: 75 once the reconnects give
# up, and the run that could not be collected is stopped.
echo keep >"$root/drop-run"
rm -f "$root/stops"
long_run
rc=""
ends "$spawned"
rm "$root/drop-run"
expect "a run that could not be collected ends with 75" test "${rc:-none}" = 75
expect_line "$root/err" '^gate-remote: lost the connection to orrery@fake-box after the command started and could not collect the run within 2s; environment result, not a test failure \(exit 75\)$' "a run that could not be collected says so"
expect "a run that could not be collected runs no wake" test ! -e "$root/woke"
expect "a run that could not be collected is stopped on the box" gone "$(cat "$root/long-pid")"
expect "the scope of a run that could not be collected is stopped" test -s "$root/stops"
expect "nothing of a run that could not be collected is left on the box" box_idle

# The run's runner is killed on the box, so the run never records an exit: 75, saying so, and what is left of the run
# is stopped.
long_run
runner="$(runner_pid)"
expect "the run's runner is found" test -n "$runner"
[ -z "$runner" ] || kill -KILL "$runner"
rc=""
ends "$spawned"
expect "a run that recorded no exit ends with 75" test "${rc:-none}" = 75
expect_line "$root/err" '^gate-remote: the run on orrery@fake-box ended without recording its exit \(it was killed there\); environment result, not a test failure \(exit 75\)$' "a run that recorded no exit says so"
expect "what is left of a run that recorded no exit is stopped" gone "$(cat "$root/long-pid")"
expect "nothing of a run that recorded no exit is left on the box" box_idle

# The script itself is stopped (TERM to it alone) while the run goes on: the run on the box is stopped too.
long_run
kill -TERM "$spawned"
rc=""
ends "$spawned"
expect "a stopped run ends with 143" test "${rc:-none}" = 143
expect "stopping the script stops the run on the box" gone "$(cat "$root/long-pid")"
expect "nothing of a stopped run is left on the box" box_idle

# The script is killed outright (SIGKILL, no cleanup) while the run goes on: the next run from the work tree stops the
# one left running before it syncs, then runs.
long_run
kill -KILL -- "-$spawned"
wait "$spawned" 2>/dev/null || true
left="$(cat "$root/long-pid")"
expect "a run whose script was killed goes on on the box" kill -0 "$left"
gate "$root/out" "$root/err" bash -c 'exit 4'
expect_status 4 "the run after a killed one returns its status"
expect_line "$root/err" '^gate-remote: stopping run [0-9-]+ on .*, left running by a run that is gone$' "the run after a killed one says it stops the one left running"
expect "the run left running is stopped" gone "$left"
expect "nothing of a run left running is left on the box" box_idle

# wake: launches a box when none is listed and writes its destination.
rm -f "$root/gate-host"
echo "orrery-gate-x1y ready" >"$root/ec2-up"
rc=0
"$script" wake >"$root/out" 2>"$root/err" || rc=$?
expect_status 0 "wake launches a box"
expect "wake writes the launched box" test "$(cat "$root/gate-host" 2>/dev/null)" = "orrery@orrery-gate-x1y"
expect "wake launches spot-only" grep -qx 'up orrery-gate --spot-only' "$root/ec2-up-calls"
expect_line "$root/out" '^orrery-gate-x1y ready$' "wake passes ec2's output on"

# wake: the box the host file names still answers, so nothing is listed or launched.
rm -f "$root/ec2-up-calls"
rc=0
"$script" wake >"$root/out" 2>"$root/err" || rc=$?
expect_status 0 "wake keeps an answering known box"
expect "keeping the known box launches nothing" test ! -e "$root/ec2-up-calls"

# wake: adopts a listed box that answers, without launching.
touch "$root/down-orrery@orrery-gate-x1y"
echo "orrery-gate-abc  orrery-gate  c8a.8xlarge  yes  1h  1.0  0.5" >"$root/ec2-ls"
echo "commonframe-gate-zzz  commonframe-gate  c8a.4xlarge  yes  1h  1.0  0.5" >>"$root/ec2-ls"
rc=0
"$script" wake >"$root/out" 2>"$root/err" || rc=$?
expect_status 0 "wake adopts a running box"
expect "wake writes the adopted box" test "$(cat "$root/gate-host")" = "orrery@orrery-gate-abc"
expect "adopting launches nothing" test ! -e "$root/ec2-up-calls"

# wake: a listed box that does not answer is passed over and a box is launched.
touch "$root/down-orrery@orrery-gate-abc"
echo "orrery-gate-x2z ready" >"$root/ec2-up"
rc=0
"$script" wake >"$root/out" 2>"$root/err" || rc=$?
expect_status 0 "wake past an unanswering listed box"
expect "an unanswering listed box is replaced by a launch" test "$(cat "$root/gate-host")" = "orrery@orrery-gate-x2z"
rm "$root/ec2-ls"

# wake: a refused launch says what ec2 said and leaves the host file alone.
touch "$root/down-orrery@orrery-gate-x2z"
printf 'trying spot us-west-2a\nec2: InsufficientInstanceCapacity in every zone\n' >"$root/ec2-up"
echo 1 >"$root/ec2-up-status"
rc=0
"$script" wake >"$root/out" 2>"$root/err" || rc=$?
expect_status 1 "wake with a refused launch"
expect_line "$root/err" 'orrery-gate launch refused: ec2: InsufficientInstanceCapacity in every zone$' "a refused launch quotes ec2's last line"
expect "a refused launch keeps the host file" test "$(cat "$root/gate-host")" = "orrery@orrery-gate-x2z"
rm "$root/ec2-up-status"

# Two runs from two work trees find the box gone at once, through the real wake: one launch serves both. The second
# run's wake waits for the first's launch instead of launching its own or running here.
git -C "$work" worktree add -q "$root/work2"
rm -f "$root/ec2-up-calls"
mkfifo "$root/ec2-up-fifo"
both() {
    local tree
    : >"$root/run/orrery-gate-wake.log"
    both_pids=()
    for tree in work work2; do
        spawn "$root/$tree.out" "$root/$tree.err" "$root/$tree" env ORRERY_GATE_WAKE='' scripts/gate-remote.sh bash -c 'exit 7'
        both_pids+=("$spawned")
        if [ "$tree" = work ]; then
            appears "$root/ec2-up-calls"
        fi
    done
    appears "$root/run/orrery-gate-wake.log" 'waiting for the orrery-gate launch in progress' \
        || fail "the second run's wake never waited for the launch in progress"
}
both
echo "orrery-gate-new ready" >"$root/ec2-up-fifo"
rc1=0 rc2=0
wait "${both_pids[0]}" || rc1=$?
wait "${both_pids[1]}" || rc2=$?
expect "the run whose wake launched gets the command's status" test "$rc1" -eq 7
expect "the run whose wake waited gets the command's status" test "$rc2" -eq 7
expect_line "$root/work.err" 'running bash .* on orrery@orrery-gate-new \(' "the launching run runs on the new box"
expect_line "$root/work2.err" 'running bash .* on orrery@orrery-gate-new \(' "the waiting run runs on the same new box"
expect "two runs make one launch" test "$(wc -l <"$root/ec2-up-calls")" -eq 1
expect "neither run ran here" test ! -e "$root/ran-here"

# The same, but the launch fails: it ends both waits, the second wake takes its failure without launching again, and
# both runs run here with ec2's reason.
touch "$root/down-orrery@orrery-gate-new"
rm -f "$root/ec2-up-calls"
both
printf 'ec2: InsufficientInstanceCapacity in every zone\n' >"$root/ec2-up-fifo-text"
echo 1 >"$root/ec2-up-status"
cat "$root/ec2-up-fifo-text" >"$root/ec2-up-fifo"
rc1=0 rc2=0
wait "${both_pids[0]}" || rc1=$?
wait "${both_pids[1]}" || rc2=$?
expect "a failed launch ends the launching run here" test "$rc1" -eq 7
expect "a failed launch ends the waiting run here" test "$rc2" -eq 7
expect_line "$root/work.err" 'here \(no orrery-gate box could come up: orrery-gate launch refused: ec2: InsufficientInstanceCapacity' "the launching run names the refusal"
expect_line "$root/work2.err" 'here \(no orrery-gate box could come up: the launch in progress failed: orrery-gate launch refused: ec2: InsufficientInstanceCapacity' "the waiting run names the same refusal"
expect "a failed launch is not retried by the waiting wake" test "$(wc -l <"$root/ec2-up-calls")" -eq 1
rm -f "$root/ec2-up-fifo" "$root/ec2-up-status"

# Box-side slots. The box's fake just runs $root/just-<recipe>; the fake `just check` says it started, then holds its
# run until the test writes to the work tree's release FIFO (30 s at most, so a broken test fails instead of hanging;
# --foreground keeps timeout in the scope's process group, so stopping the scope ends it).
cat >"$root/boxbin/just" <<'EOF'
#!/usr/bin/env bash
recipe="$1"
shift
exec "$FAKE_ROOT/just-$recipe" "$@"
EOF
cat >"$root/just-check" <<'EOF'
#!/usr/bin/env bash
tree="${PWD##*/}"
tree="${tree%-*}"
touch "$FAKE_ROOT/started-$tree"
timeout --foreground 30 cat "$FAKE_ROOT/release-$tree" >/dev/null
EOF
chmod +x "$root/boxbin/just" "$root/just-check"
git -C "$work" worktree add -q "$root/work3"
mkfifo "$root/release-work" "$root/release-work2"
echo "orrery@fake-box" >"$root/gate-host"
export ORRERY_GATE_FULL_SLOTS=1

# check_in <tree>: starts `just check` from the work tree in the background, its output in $root/<tree>.out and .err.
check_in() {
    rm -f "$root/started-$1"
    spawn "$root/$1.out" "$root/$1.err" "$root/$1" scripts/gate-remote.sh just check
}

# box_run_pid <tree>: the box-side shell that holds the work tree's slot (its argv[3] is orrery-gate-run and its
# argv[4] the run's unit), found by its working directory, the work tree's checkout on the box. A subshell it forked
# carries the same argv until it execs, so only the topmost match counts.
box_run_pid() {
    local p checkout
    local -a found=()
    checkout="$root/box/gate/$1-$(printf '%s' "$root/$1" | sha256sum | cut -c1-8)"
    for p in $(pgrep -f 'orrery-gate-run orrery-gate-'); do
        [ "$(readlink "/proc/$p/cwd" 2>/dev/null)" = "$checkout" ] || continue
        if awk -v RS='\0' 'NR == 4 && $0 == "orrery-gate-run" { a = 1 } NR == 5 && $0 ~ /^orrery-gate-/ { b = 1 } END { exit !(a && b) }' \
            "/proc/$p/cmdline" 2>/dev/null; then
            found+=("$p")
        fi
    done
    for p in "${found[@]}"; do
        if ! printf '%s\n' "${found[@]}" | grep -qx "$(ps -o ppid= -p "$p" | tr -d ' ')"; then
            echo "$p"
        fi
    done
}

# With one full-check slot, a second `just check` waits for the first, and its output says it is queued.
check_in work
pid_a=$spawned
appears "$root/started-work"
check_in work2
pid_b=$spawned
expect_line_eventually() {
    if appears "$1" "$2"; then pass "$3"; else fail "$3: no line matching '$2' in:"; sed 's/^/    /' "$1" >&2; fi
}
expect_line_eventually "$root/work2.err" '^gate-remote: every full-check slot on .* is held \(1\); queued until one is let go$' "a full check that finds every slot held says it is queued"
expect "a queued full check has not started" test ! -e "$root/started-work2"

# Light checks share their own slots, so one runs while the full-check slot is held.
rc=0
spawn "$root/work3.out" "$root/work3.err" "$root/work3" scripts/gate-remote.sh bash -c 'exit 0'
wait "$spawned" || rc=$?
expect_status 0 "a light check runs while every full-check slot is held"
expect_line "$root/work3.err" '^gate-remote: took light-check slot 1 of [0-9]+ on .* after [0-9]+s$' "a light check takes a light-check slot"
expect_no_line "$root/work3.err" 'queued until one is let go' "a light check is not queued behind the full-check slot"
expect "the queued full check is still waiting" test ! -e "$root/started-work2"

# The holder's run ends: the queued run takes the slot.
echo released >"$root/release-work"
rc=0
wait "$pid_a" || rc=$?
expect "the first full check ends with its status" test "$rc" -eq 0
expect "the queued full check starts once the slot is let go" appears "$root/started-work2"
expect_line "$root/work2.err" '^gate-remote: took full-check slot 1 of 1 on .* after [0-9]+s$' "a queued full check says when it took its slot"
expect_line "$root/work.err" '^gate-remote: took full-check slot 1 of 1 on .* after [0-9]+s$' "a full check with a free slot takes it"
expect_no_line "$root/work.err" 'queued until one is let go' "a full check with a free slot is not queued"

# The holder dies (SIGKILL, no cleanup): the queued run takes the slot all the same.
check_in work
pid_a=$spawned
expect_line_eventually "$root/work.err" 'queued until one is let go$' "a full check queues behind the holder"
holder="$(box_run_pid work2)"
expect "the holder's box-side shell is found" test -n "$holder"
[ -z "$holder" ] || kill -KILL "$holder"
expect "a dead holder lets the queued full check start" appears "$root/started-work"
echo released >"$root/release-work2"
echo released >"$root/release-work"
wait "$pid_b" || true
rc=0
wait "$pid_a" || rc=$?
expect "the full check that waited on a dead holder ends with its status" test "$rc" -eq 0
expect "no slot waiter outlives the waits" test -z "$(pgrep -f "$root/box/gate/slots/full-" || true)"

# waiter_group: the process group of the queued run's slot waiters, once one of them is in flock on its slot file
# (waiting like appears).
waiter_group() {
    local p=""
    until p="$(pgrep -f "^flock $root/box/gate/slots/full-" | head -n 1 || true)"; [ -n "$p" ]; do
        sleep 0.1
    done
    ps -o pgid= -p "$p" | tr -d ' '
}

# queue_behind_holder: a full check from work holds the one slot and one from work2 queues behind it; their pids go in
# pid_a and pid_b.
queue_behind_holder() {
    check_in work
    pid_a=$spawned
    appears "$root/started-work"
    check_in work2
    pid_b=$spawned
    expect_line_eventually "$root/work2.err" 'queued until one is let go$' "a full check queues behind the holder"
}

# release_holder: the holder's run ends with its status.
release_holder() {
    echo released >"$root/release-work"
    rc=0
    wait "$pid_a" || rc=$?
    expect "the holder ends with its status" test "$rc" -eq 0
}

# The queued run's slot waiters all end without a slot while the slot directory is still there (here they are killed):
# the run ends with 75, naming the waiters, instead of going round the wait again.
queue_behind_holder
group="$(waiter_group)"
[ -z "$group" ] || kill -TERM -- "-$group"
rc=""
ends "$pid_b"
expect "a run whose slot waiters ended ends with 75" test "${rc:-none}" = 75
expect_line "$root/work2.err" '^gate-remote: every slot waiter ended without a slot in .*/gate/slots on .*; the run ends without a slot \(exit 75\)$' "a run whose slot waiters ended names them"
expect "a run whose slot waiters ended never started its command" test ! -e "$root/started-work2"
release_holder

# The slot directory goes while a run is queued, and then its waiters end: the run ends with 75 and names the gone
# directory, which ended the waiters too, as the cause. The waiters are killed after the directory goes, so this takes
# the branch where the waiters' end is seen first.
queue_behind_holder
group="$(waiter_group)"
rm -rf -- "$root/box/gate/slots"
[ -z "$group" ] || kill -TERM -- "-$group"
rc=""
ends "$pid_b"
expect "a run whose slot directory went ends with 75" test "${rc:-none}" = 75
expect_line "$root/work2.err" '^gate-remote: the slot directory .*/gate/slots is gone on .*; the run ends without a slot \(exit 75\)$' "a run whose slot directory went names the cause"
expect "a run whose slot directory went never started its command" test ! -e "$root/started-work2"
release_holder
expect "no slot waiter outlives a gone slot directory" test -z "$(pgrep -f "$root/box/gate/slots/" || true)"
unset ORRERY_GATE_FULL_SLOTS

# Two runs prepare one fresh box at once. The box's object store is made anew with a pre-receive hook that holds the
# first push to reach it after receive-pack has read its ref updates and before it writes them, the point where two
# pushes at once refused each other's new refs ("reference already exists"). Once the other run's push has either
# finished (its refs are in the store) or is waiting for the box's push lock, the held push goes on, and both runs
# must prepare the box and run there.
store="$root/box/gate/objects.git"
rm -rf -- "$store" "$root/push-held" "$root/push-holding"
git init -q --bare "$store"
cat >"$store/hooks/pre-receive" <<'EOF'
#!/usr/bin/env bash
cat >/dev/null
if mkdir "$FAKE_ROOT/push-held" 2>/dev/null; then
    touch "$FAKE_ROOT/push-holding"
    timeout --foreground 30 cat "$FAKE_ROOT/push-release" >/dev/null
fi
EOF
chmod +x "$store/hooks/pre-receive"
mkfifo "$root/push-release"
# other_push_settled: the push that is not held has finished (a tag is in the store) or waits for the push lock (a
# second flock on it runs), waiting like appears.
other_push_settled() {
    until git -C "$store" rev-parse -q --verify refs/tags/v0.0.1 >/dev/null \
        || [ "$(pgrep -fc 'flock -o gate/objects.lock git-receive-pack' || true)" -ge 2 ]; do
        sleep 0.1
    done
}
pair=()
for tree in work work2; do
    spawn "$root/$tree.out" "$root/$tree.err" "$root/$tree" scripts/gate-remote.sh bash -c 'exit 0'
    pair+=("$spawned")
done
expect "one prepare's push is held in the box's store" appears "$root/push-holding"
expect "the other prepare's push finished or waits for the box's push lock" other_push_settled
echo released >"$root/push-release"
for i in 0 1; do
    tree=work
    [ "$i" -eq 0 ] || tree=work2
    rc=0
    wait "${pair[$i]}" || rc=$?
    expect "a run that prepared the fresh box alongside another ends with its status ($tree)" test "$rc" -eq 0
    expect_line "$root/$tree.err" '^gate-remote: running bash .* on orrery@fake-box \(' "a run that prepared the fresh box alongside another runs there ($tree)"
    expect_no_line "$root/$tree.err" 'could not be prepared| here \(' "a run that prepared the fresh box alongside another never runs here ($tree)"
done

if [ "$failures" -ne 0 ]; then
    echo "gate-remote-test: $failures failed" >&2
    exit 1
fi
echo "gate-remote-test: all passed"
