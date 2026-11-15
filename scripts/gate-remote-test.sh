#!/usr/bin/env bash
#
# Tests scripts/gate-remote.sh without AWS or a network: the "box" is a directory standing in for the orrery user's home,
# reached through a fake ssh that runs each remote command there. A fixture repository carries a copy of the script and
# a scripts/in-devenv that runs bash directly. Covers the push, the sync, the run, exit codes, the local fallback, a box
# lost mid-run, and `wake` against a fake ec2.
#
# Usage: scripts/gate-remote-test.sh   (exits 0 when every case passes)

set -euo pipefail

script="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/gate-remote.sh"
root="$(mktemp -d)"
trap 'rm -rf -- "$root"' EXIT

failures=0
fail() {
    echo "FAIL: $*" >&2
    failures=$((failures + 1))
}
pass() {
    echo "ok: $*"
}

mkdir -p "$root/bin" "$root/box" "$root/run"

# The fake ssh drops options, then runs the command in the box directory with HOME set to it. Switches:
#   $root/box-down    every connection fails with 255, as ssh does when the box is gone;
#   $root/drop-run    the run's connection prints a line and fails with 255; with "box-gone" in the file the box is gone
#                     afterwards too, otherwise it still answers;
#   $root/no-push     git's connection fails.
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
if [ -e "$FAKE_ROOT/no-push" ] && [[ $cmd == git-receive-pack* ]]; then
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
cd "$FAKE_ROOT/box" && HOME="$FAKE_ROOT/box" exec bash -c "$cmd"
EOF
cat >"$root/bin/wake" <<'EOF'
#!/bin/sh
touch "$FAKE_ROOT/woke"
EOF
cat >"$root/bin/local" <<'EOF'
#!/bin/sh
touch "$FAKE_ROOT/ran-here"
exec "$@"
EOF
# The fake ec2: `ls` prints a header and $root/ec2-ls, `up` records the call and prints $root/ec2-up, exiting with the
# status in $root/ec2-up-status.
cat >"$root/bin/ec2" <<'EOF'
#!/bin/sh
case "$1" in
    ls)
        echo "NAME  CLASS  TYPE  SPOT  AGE  ON-DEMAND/HR  SPOT/HR"
        [ ! -e "$FAKE_ROOT/ec2-ls" ] || cat "$FAKE_ROOT/ec2-ls"
        ;;
    up)
        echo "$*" >>"$FAKE_ROOT/ec2-up-calls"
        cat "$FAKE_ROOT/ec2-up"
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

# gate <stdout> <stderr> <args...>: runs the script in the fixture, leaving its status in $rc.
gate() {
    local out="$1" err="$2"
    shift 2
    rm -f "$root/woke" "$root/ran-here"
    rc=0
    (cd "$work" && scripts/gate-remote.sh "$@") >"$out" 2>"$err" || rc=$?
}

# woke: true once the detached wake has run; it is started in the background, so wait for it a little.
woke() {
    local i
    for ((i = 0; i < 50; i++)); do
        [ -e "$root/woke" ] && return 0
        sleep 0.1
    done
    return 1
}

expect_status() {
    if [ "$rc" -eq "$1" ]; then pass "$2: exit $1"; else fail "$2: exit $rc, want $1"; fi
}
expect() {
    local what="$1"
    shift
    if "$@"; then pass "$what"; else fail "$what"; fi
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

# The box does not answer: wake starts and the command runs here through the local runner.
touch "$root/box-down"
gate "$root/out" "$root/err" bash -c 'echo here-ran; exit 4'
expect_status 4 "fallback returns the local command's status"
expect_line "$root/err" '^gate-remote: running bash .* here \(orrery@fake-box did not answer; load [0-9.]+ [0-9.]+ [0-9.]+, nproc [0-9]+\)$' "fallback says it ran here, with load and nproc"
expect_line "$root/out" '^here-ran$' "fallback runs the command"
expect "fallback goes through ORRERY_GATE_LOCAL" test -e "$root/ran-here"
expect "an unanswering box starts the wake" woke
rm "$root/box-down"

# No box known yet: wake and run here.
rm "$root/gate-host"
gate "$root/out" "$root/err" true
expect_status 0 "no host file runs here"
expect_line "$root/err" 'here \(no gate box is known yet' "no host file says so"
expect "no host file starts the wake" woke
echo "orrery@fake-box" >"$root/gate-host"

# ORRERY_GATE_REMOTE=0 skips the box.
ORRERY_GATE_REMOTE=0 gate "$root/out" "$root/err" true
expect_line "$root/err" 'here \(ORRERY_GATE_REMOTE=0' "ORRERY_GATE_REMOTE=0 runs here"

# The box cannot be prepared (here, the push fails): the command runs here.
touch "$root/no-push"
gate "$root/out" "$root/err" bash -c 'exit 5'
expect_status 5 "a box that cannot be prepared runs the command here"
expect_line "$root/err" 'here \(orrery@fake-box could not be prepared' "an unprepared box says so"
rm "$root/no-push"

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

# wake: adopts a listed box that answers, without launching.
rm -f "$root/ec2-up-calls"
echo "orrery-gate-abc  orrery-gate  c8a.8xlarge  yes  1h  1.0  0.5" >"$root/ec2-ls"
echo "commonframe-gate-zzz  commonframe-gate  c8a.4xlarge  yes  1h  1.0  0.5" >>"$root/ec2-ls"
rc=0
"$script" wake >"$root/out" 2>"$root/err" || rc=$?
expect_status 0 "wake adopts a running box"
expect "wake writes the adopted box" test "$(cat "$root/gate-host")" = "orrery@orrery-gate-abc"
expect "adopting launches nothing" test ! -e "$root/ec2-up-calls"

# wake: a listed box that does not answer stops a launch.
touch "$root/box-down"
rc=0
"$script" wake >"$root/out" 2>"$root/err" || rc=$?
expect_status 1 "wake with only an unanswering box listed"
expect "an unanswering listed box launches nothing" test ! -e "$root/ec2-up-calls"
rm "$root/box-down" "$root/ec2-ls"

# wake: a refused launch says what ec2 said and leaves the host file alone.
printf 'trying spot us-west-2a\nec2: InsufficientInstanceCapacity in every zone\n' >"$root/ec2-up"
echo 1 >"$root/ec2-up-status"
rc=0
"$script" wake >"$root/out" 2>"$root/err" || rc=$?
expect_status 1 "wake with a refused launch"
expect_line "$root/err" 'orrery-gate launch refused: ec2: InsufficientInstanceCapacity in every zone$' "a refused launch quotes ec2's last line"
expect "a refused launch keeps the host file" test "$(cat "$root/gate-host")" = "orrery@orrery-gate-abc"

if [ "$failures" -ne 0 ]; then
    echo "gate-remote-test: $failures failed" >&2
    exit 1
fi
echo "gate-remote-test: all passed"
