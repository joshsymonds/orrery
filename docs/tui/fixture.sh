#!/usr/bin/env bash
# fixture.sh builds the made-up tmux world that docs/img/tree.png is captured from.
#
# The tree screenshot is published in a public repository, so the world it shows
# must hold nothing from the machine that took it: no host, user, or real path,
# and no real project. `start` builds that world under two fixed roots and
# `stop` removes both again.
#
#   start  Stops any previous fixture, then creates /tmp/orrery-tui-shots with a
#          private tmux server on /tmp/orrery-tui-shots/tmux.sock holding seven
#          panes, plus the preview file and state directory. It also creates
#          /tmp/demo with four made-up project directories and a marker file,
#          /tmp/demo/.orrery-tui-shots-fixture. The panes show /tmp/demo/<project>.
#   stop   Kills that private server if it is running and removes both roots.
#
# Before start or stop kills, removes or creates anything, it checks /tmp/demo.
# If /tmp/demo exists and is not a real directory owned by you that holds the
# marker, both exit non-zero and touch nothing.
#
# Isolation measures, and why each exists:
#   - The roots are literal paths, not derived from TMPDIR or HOME. A value taken
#     from the environment could point at a real directory, and `stop` deletes
#     both roots.
#   - Every tmux command names the socket with -S. Without it, tmux would
#     contact the default socket, which belongs to the live session that runs
#     this script.
#   - The server starts with -f /dev/null, so no tmux.conf of the machine
#     changes how the fixture behaves.
#   - tmux is run under `env -i` with a minimal environment (see tmx). The live
#     session exports TMUX, which would point panes and commands at the live
#     server. The user's login shell would be the default shell, and its startup
#     files run even for `-c`, so SHELL names sh instead. PATH and PWD would
#     name the user's profile and scratch paths, so both are kept free of them.
#   - Every pane runs a fixed command at a made-up path: `sleep` directly, or a
#     non-interactive `sh -c` that prints the preview file and then sleeps. No
#     pane starts an interactive shell or reads a startup file.
#   - Pane ages are computed from the clock when `start` runs, so the rows read
#     the same only while the frame is taken within 30 seconds of `start`. The
#     90 s lamp-telemetry row shows 1m until 120 s, then 2m. Every other row has
#     at least 60 s of margin.
#   - The root directories are created mode 700 so other local users cannot
#     reach the socket or the projects.

set -euo pipefail

ROOT=/tmp/orrery-tui-shots
SOCK="$ROOT/tmux.sock"
PREVIEW="$ROOT/preview/lighthouse.txt"
DEMO=/tmp/demo
MARKER="$DEMO/.orrery-tui-shots-fixture"
readonly DEMO MARKER

# tmx runs tmux against the private server only. Use it for every tmux command.
# The server's environment is built here, once, when it starts. It starts from /
# so its PWD is not a real path. PATH holds only the directories of sh, cat and
# sleep, so it names no user profile. SHELL is sh, so default-shell is not the
# user's login shell.
tmx() {
  (
    cd / || exit 1
    env -i \
      PATH="$(dirname "$(command -v sh)"):$(dirname "$(readlink -f "$(command -v sleep)")")" \
      SHELL="$(command -v sh)" \
      XDG_STATE_HOME="$ROOT/state" \
      "$(readlink -f "$(command -v tmux)")" -S "$SOCK" "$@"
  )
}

# pane sets the state options on one pane. seen is "unset", or the arithmetic
# expression that gives @orrery_seen from the changed timestamp.
pane() {
  local target=$1 state=$2 label=$3 summary=$4 age=$5 seen=$6
  local changed=$(( now - age ))
  tmx set-option -p -t "$target" @orrery_state "$state"
  tmx set-option -p -t "$target" @orrery_label "$label"
  tmx set-option -p -t "$target" @orrery_summary "$summary"
  tmx set-option -p -t "$target" @orrery_changed "$changed"
  case $seen in
    unset) ;;
    changed) tmx set-option -p -t "$target" @orrery_seen "$changed" ;;
    changed+*) tmx set-option -p -t "$target" @orrery_seen "$(( changed + ${seen#changed+} ))" ;;
    *) echo "fixture.sh: bad seen value $seen" >&2; exit 1 ;;
  esac
}

# demo_guard exits non-zero, touching nothing, unless /tmp/demo is absent or is a
# real directory owned by you that holds the marker.
demo_guard() {
  if [ ! -e "$DEMO" ] && [ ! -L "$DEMO" ]; then
    return 0
  fi
  if [ -L "$DEMO" ] || [ ! -d "$DEMO" ] || [ ! -O "$DEMO" ] || [ ! -f "$MARKER" ]; then
    echo "fixture.sh: $DEMO exists and is not a fixture root of yours; leaving it alone" >&2
    exit 1
  fi
}

stop() {
  demo_guard

  if [ -S "$SOCK" ]; then
    tmx kill-server 2>/dev/null || true
  fi
  rm -rf -- "$ROOT"

  if [ -d "$DEMO" ]; then
    rm -rf -- "$DEMO/lighthouse" "$DEMO/orchard" "$DEMO/quarry" "$DEMO/tidepool"
    rm -f -- "$MARKER"
    rmdir -- "$DEMO"
  fi
}

start() {
  stop

  mkdir -m 700 -- "$ROOT"
  mkdir -m 700 -- "$DEMO"
  : > "$MARKER"
  mkdir -- "$DEMO/lighthouse" "$DEMO/orchard" "$DEMO/quarry" "$DEMO/tidepool"
  mkdir -p "$ROOT/state" "$ROOT/preview"

  cat > "$PREVIEW" <<'PREVIEW_EOF'
● Read(internal/rotation/schedule.go)
  ⎿  Read 142 lines

● The nightly backfill scans every shift because the rotation
  table has no index on keeper_id. Two ways to fix it:

  1. Composite index on (keeper_id, shift_start)
  2. Partial index on open shifts only

  Which one should I add?

❯
PREVIEW_EOF

  now=$(date +%s)

  tmx -f /dev/null new-session -d -s lighthouse -c "$DEMO/lighthouse" -x 100 -y 16 \
    sh -c "cat -- \"\$1\"; exec sleep 86400" sh "$PREVIEW"
  pane lighthouse:0.0 waiting beacon-schedule "asks which index to add" 240 unset

  tmx new-window -t lighthouse -c "$DEMO/lighthouse" sleep 86400
  pane lighthouse:1.0 working lamp-telemetry "adding retry backoff" 90 changed

  tmx new-session -d -s orchard -c "$DEMO/orchard" -x 100 -y 16 sleep 86400
  pane orchard:0.0 "done" graft-parser "parser tests pass" 720 unset

  tmx new-window -t orchard -c "$DEMO/orchard" sleep 86400
  pane orchard:1.0 working harvest-report "rendering CSV export" 180 changed

  tmx new-session -d -s quarry -c "$DEMO/quarry" -x 100 -y 16 sleep 86400
  pane quarry:0.0 "done" ci-cache "cache keys trimmed" 259200 changed+30

  tmx new-session -d -s tidepool -c "$DEMO/tidepool" -x 100 -y 16 sleep 86400
  pane tidepool:0.0 "done" tide-tables "migration merged" 172800 changed+60

  tmx new-window -t tidepool -c "$DEMO/tidepool" sleep 86400
  pane tidepool:1.0 "done" docs-examples "README examples fixed" 18000 changed+60
}

case ${1:-} in
  start) start ;;
  stop) stop ;;
  *)
    echo "usage: fixture.sh {start|stop}" >&2
    exit 2
    ;;
esac
