#!/usr/bin/env bash
#
# Tests scripts/gate-remote.sh without AWS or a network: the "box" is a directory standing in for the orrery user's home,
# reached through a fake ssh that runs each remote command there. A fixture repository carries a copy of the script and
# a scripts/in-devenv that runs bash directly. Covers the push, the sync, the run, exit codes, waiting for a box when
# the known one is gone, the local fallback when none can come up, a box lost mid-run, and `wake` against a fake ec2,
# including two runs that share one launch.
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

# The fake ssh drops options, then runs the command in the box directory with HOME set to it. Switches:
#   $root/box-down    every connection fails with 255, as ssh does when the box is gone;
#   $root/down-<dest> connections to that destination fail with 255;
#   $root/drop-run    the run's connection prints a line and fails with 255; with "box-gone" in the file the box is gone
#                     afterwards too, otherwise it still answers;
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
if [ -e "$FAKE_ROOT/no-push" ] && [[ $cmd == git-receive-pack* ]]; then
    if grep -q and-down "$FAKE_ROOT/no-push"; then
        touch "$FAKE_ROOT/down-$host"
        rm -f "$FAKE_ROOT/no-push"
    fi
    exit 128
fi
if [ -e "$FAKE_ROOT/drop-run" ] && [[ $cmd == *orrery-gate-run* ]]; then
    echo "partial output"
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
cd "$FAKE_ROOT/box" && HOME="$FAKE_ROOT/box" PATH="$FAKE_ROOT/boxbin:$PATH" exec bash -c "$cmd"
EOF
# The box's fake systemd: a scope is a process group. systemd-run --scope records "<unit> <pid>" in $root/scopes and
# runs the command as the leader of a new session; systemctl --user stop <unit>.scope records the unit in $root/stops
# and kills that group. systemctl --user show-environment fails while $root/no-user-manager exists.
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
    "--user stop "*.scope)
        unit="${3%.scope}"
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
unset ORRERY_GATE_DEST ORRERY_GATE_PROBE ORRERY_GATE_USER ORRERY_GATE_REMOTE

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
# the background, so wait for it a little.
appears() {
    local i
    for ((i = 0; i < 100; i++)); do
        if [ -e "$1" ] && { [ -z "${2:-}" ] || grep -qE -- "$2" "$1"; }; then
            return 0
        fi
        sleep 0.1
    done
    return 1
}
# ends <pid>: true once the run has ended, waiting a little like appears, with its status in $rc; a run that never ends
# fails the case instead of hanging the test, and the cleanup ends it.
ends() {
    local i state
    for ((i = 0; i < 100; i++)); do
        state="$(awk '{ print $3 }' "/proc/$1/stat" 2>/dev/null || true)"
        if [ -z "$state" ] || [ "$state" = Z ]; then
            rc=0
            wait "$1" || rc=$?
            return 0
        fi
        sleep 0.1
    done
    return 1
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

# A remote command that exits 255 itself is the command's result, not a lost box.
gate "$root/out" "$root/err" bash -c 'exit 255'
expect_status 255 "a command's own 255 comes back as 255"

# gone <pid>: true once the process has ended; the scope's stop signals it, so wait for it a little.
gone() {
    local i
    for ((i = 0; i < 100; i++)); do
        kill -0 "$1" 2>/dev/null || return 0
        sleep 0.1
    done
    return 1
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
            appears "$root/ec2-up-calls" || fail "the first run's wake never reached ec2 up"
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
appears "$root/started-work" || fail "the first full check never started"
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

# The slot directory goes while a run is queued: once the holder lets its slot go, the queued run cannot take or wait
# for a slot, so it ends with 75, naming the cause, instead of going round the wait again.
check_in work
pid_a=$spawned
appears "$root/started-work" || fail "the holder of the slot never started"
check_in work2
pid_b=$spawned
expect_line_eventually "$root/work2.err" 'queued until one is let go$' "a full check queues before its slot directory goes"
rm -rf -- "$root/box/gate/slots"
echo released >"$root/release-work"
rc=0
wait "$pid_a" || rc=$?
expect "the holder ends with its status" test "$rc" -eq 0
rc=""
ends "$pid_b" || fail "a run whose slot directory went never ended"
expect "a run whose slot directory went ends with 75" test "${rc:-none}" = 75
expect_line "$root/work2.err" '^gate-remote: the slot directory .*/gate/slots is gone on .*; the run ends without a slot \(exit 75\)$' "a run whose slot directory went names the cause"
expect "a run whose slot directory went never started its command" test ! -e "$root/started-work2"
expect "no slot waiter outlives a gone slot directory" test -z "$(pgrep -f "$root/box/gate/slots/" || true)"
unset ORRERY_GATE_FULL_SLOTS

if [ "$failures" -ne 0 ]; then
    echo "gate-remote-test: $failures failed" >&2
    exit 1
fi
echo "gate-remote-test: all passed"
