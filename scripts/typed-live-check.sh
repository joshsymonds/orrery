#!/usr/bin/env bash
#
# Live check (epic DL5): types real messages into a real Claude Code session with the
# BUILT `orrery send`, and reports whether the session's transcript records them as typed.
#
# Run by hand only. It spends Haiku turns on the signed-in account, so nothing calls it:
# not `just check`, not the pre-push hook, not CI. The orchestrator runs it under
# `flock /tmp/claude-1000/orrery-heavy.lock` because it is a heavy job.
#
# Isolation: Claude Code runs on a private tmux server (its own -L name, -f /dev/null) in a
# fresh throwaway directory, with hooks disabled through --settings, so nothing reaches
# orreryd, ntfy or the phone. Hooks also set the pane's @orrery_state, and with them off the
# script sets that option by hand before each send.
#
# Usage: scripts/typed-live-check.sh
# Exit status: 0 when every case passes, 1 otherwise.

set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd -P)

# Machine-wide globals. Empty until the step that creates them runs, so the EXIT trap
# only removes what this run made.
server=""
socket=""
build=""
workdir=""
transcript=""
pane=""
failures=0
started=$(date +%s)
scratch_base=$(cd "${TMPDIR:-/tmp}" && pwd -P)

# Bounded wait limits, in seconds. Each wait that exceeds its limit fails the run and
# prints what it waited for, so a stuck session never hangs the script.
trust_limit=60
box_limit=90
submit_limit=60
stream_limit=120
reply_limit=300

# The three cases. Each carries an invented marker word that Claude is asked to echo alone,
# so the reply can be recognised in the transcript.
marker_nine=AMBERKITE
marker_long=BLUEFINCH
marker_mid=COBALTWREN
numbers_request="print the numbers 1 to 2000, one per line, nothing else"

# tm runs tmux on the private server. Every tmux call goes through here so that none can
# reach a server this script did not create.
tm() {
    tmux -L "$server" "$@"
}

# screen prints the pane's visible screen.
screen() {
    tm capture-pane -p -t "$pane"
}

# screen_has TEXT succeeds when the visible screen contains TEXT as a fixed string.
# The screen is captured first: `tmux ... | grep -q` can lose the match to SIGPIPE.
screen_has() {
    local text
    text=$(screen)
    grep -qF -- "$1" <<<"$text"
}

# input_box_ready succeeds when Claude Code's input box is on screen and no folder-trust
# prompt is. The trust prompt also draws a ❯ selector, so the box test alone would pass on it.
input_box_ready() {
    local text
    text=$(screen)
    grep -qF -- "❯" <<<"$text" && ! grep -qi -- "trust" <<<"$text"
}

# trust_or_box succeeds when either the folder-trust prompt or the input box is on screen.
trust_or_box() {
    local text
    text=$(screen)
    grep -qi -- "trust" <<<"$text" || grep -qF -- "❯" <<<"$text"
}

# trust_yes_selected succeeds when the folder-trust prompt's selector is on its "Yes" choice.
trust_yes_selected() {
    local text
    text=$(screen)
    grep -qE -- '❯ +Yes, I trust' <<<"$text"
}

# wait_for DESCRIPTION LIMIT COMMAND [ARG...] polls COMMAND once a second until it succeeds.
# On timeout it prints what it waited for and the last screen, then fails the run.
wait_for() {
    local what=$1 limit=$2 waited=0
    shift 2
    echo "wait: $what (up to ${limit}s)"
    until "$@"; do
        if ((waited >= limit)); then
            echo "FAIL: timed out after ${limit}s waiting for: $what"
            echo "transcript: ${transcript:-not found}"
            echo "pane now:"
            screen | tail -n 15 || true
            return 1
        fi
        sleep 1
        waited=$((waited + 1))
    done
}

# find_transcript prints the session transcript: the .jsonl under ~/.claude/projects, modified
# since this run started, whose lines carry .cwd equal to the throwaway directory.
find_transcript() {
    [ -d "$HOME/.claude/projects" ] || return 0
    find "$HOME/.claude/projects" -name '*.jsonl' -newermt "@$started" 2>/dev/null |
        while IFS= read -r file; do
            if jq -e -n --arg dir "$workdir" 'first(inputs | select(.cwd == $dir))' "$file" >/dev/null 2>&1; then
                printf '%s\n' "$file"
                break
            fi
        done
}

# transcript_has_text TEXT succeeds once a user line contains TEXT. A typed or pasted
# submission both qualify, so this confirms the message was submitted, not how.
transcript_has_text() {
    if [ -z "$transcript" ]; then
        transcript=$(find_transcript)
    fi
    [ -n "$transcript" ] && jq -e -s --arg t "$1" \
        'any(.[]; .type == "user" and ((.message.content | tostring) | contains($t)))' \
        "$transcript" >/dev/null 2>&1
}

# transcript_has_reply MARKER succeeds once an assistant line contains MARKER.
transcript_has_reply() {
    if [ -z "$transcript" ]; then
        transcript=$(find_transcript)
    fi
    [ -n "$transcript" ] && jq -e -s --arg m "$1" \
        'any(.[]; .type == "assistant" and ((.message.content | tostring) | contains($m)))' \
        "$transcript" >/dev/null 2>&1
}

# transcript_has_any TEXT succeeds once any transcript line contains TEXT, whatever its type:
# a message queued during a reply may be recorded as something other than a user prompt.
transcript_has_any() {
    if [ -z "$transcript" ]; then
        transcript=$(find_transcript)
    fi
    [ -n "$transcript" ] && jq -e -s --arg t "$1" 'any(.[]; tostring | contains($t))' \
        "$transcript" >/dev/null 2>&1
}

# numbers_streaming succeeds once the numbers reply is on screen and not yet finished in the
# transcript. Claude Code draws reply lines with a bullet or an indent before the text, so a
# number line may carry a few leading non-digit characters; five of them show the reply has
# started. The reply's last numbers in an assistant line mean it has finished.
numbers_streaming() {
    local text count
    text=$(screen)
    count=$(grep -cE '^[^0-9]{0,4}[0-9]+[[:space:]]*$' <<<"$text" || true)
    [ "${count:-0}" -ge 5 ] && ! transcript_has_reply "1999"
}

# numbers_finished_at prints the epoch second of the assistant line that holds the end of the
# numbers reply, or nothing while it has not been written.
numbers_finished_at() {
    jq -r -s 'first(.[] | select(.type == "assistant" and (.message.content | tostring | contains("1999"))))
        | .timestamp | sub("\\.[0-9]+Z$"; "Z") | fromdateiso8601' "$transcript" 2>/dev/null || true
}

# settle waits a few seconds after a reply lands, so the turn has ended before the next send.
settle() {
    sleep 5
}

# filler_line PREFIX LENGTH prints PREFIX followed by filler words, cut to LENGTH characters
# with no trailing space, so the text the transcript records matches what was sent exactly.
filler_line() {
    local prefix=$1 length=$2 body=""
    local room=$((length - ${#prefix}))
    while ((${#body} < room)); do
        body+="lorem ipsum dolor sit amet "
    done
    body=${body:0:room}
    body=${body% }
    printf '%s%s' "$prefix" "$body"
}

# nine_line_text prints nine lines of about 1,000 characters in all (the shape measured in DL2:
# over 800 characters and over three lines, which Claude Code would collapse if it were a
# paste), with no trailing newline. The first line carries the marker. The lines are joined
# with LF, which sendTypeable types.
nine_line_text() {
    local lines=() i
    lines+=("$(filler_line "Case 1 of the orrery live check, nine lines. Reply with only ${marker_nine} and nothing else. " 160)")
    for i in 2 3 4 5 6 7 8 9; do
        lines+=("$(filler_line "Line $i of 9: " 105)")
    done
    (IFS=$'\n'; printf '%s' "${lines[*]}")
}

# long_line_text prints one line of 3,000 characters with no trailing newline.
long_line_text() {
    filler_line "Case 2 of the orrery live check, one 3000-character line. Reply with only ${marker_long} and nothing else. " 3000
}

# mid_reply_text prints the message sent while the numbers reply streams.
mid_reply_text() {
    printf '%s' "Case 3 of the orrery live check, sent while a long reply streams. Reply with only ${marker_mid} and nothing else."
}

# set_state STATE sets @orrery_state on the pane by hand, because hooks are off and nothing
# else sets it. `orrery send` types only when this state is working or done.
set_state() {
    tm set-option -p -t "$pane" @orrery_state "$1"
}

# send_message STATE TEXT sets the pane state, then runs the BUILT orrery send against the
# private server. `orrery -L NAME` selects that server, so the send cannot reach another.
send_message() {
    set_state "$1"
    "$build/orrery" -L "$server" send --pane "$pane" --text "$2"
}

# pass and fail print one verdict line. A FAIL line also counts toward the exit status.
pass() {
    printf 'PASS %s\n' "$*"
}

fail() {
    printf 'FAIL %s\n' "$*"
    failures=$((failures + 1))
}

# check_typed NAME MARKER TEXT passes when a user line equals TEXT exactly and its
# promptSource is typed. An exact match rules out a pasted_content wrapper. The evidence is
# every user line that carries the marker.
check_typed() {
    local name=$1 marker=$2 text=$3 evidence
    evidence=$(jq -c -s --arg m "$marker" --arg t "$text" '
        [ .[] | select(.type == "user" and ((.message.content | tostring) | contains($m)))
          | { source: (.promptSource // "none"),
              pasted: ((.message.content | tostring) | contains("<pasted_content")),
              exact: (.message.content == $t) } ]' "$transcript" || echo "[\"transcript unreadable\"]")
    if jq -e 'any(.[]; .exact and .source == "typed" and (.pasted | not))' <<<"$evidence" >/dev/null; then
        pass "$name: typed, text equal to what was sent; user lines with marker: $evidence"
    else
        fail "$name: no user line equal to the sent text with promptSource typed; user lines with marker: $evidence"
    fi
}

# check_whole NAME MARKER TEXT passes when a user line contains all of TEXT, typed or pasted.
# The line that matched is reported so the typed or pasted outcome is recorded.
check_whole() {
    local name=$1 marker=$2 text=$3 evidence
    evidence=$(jq -c -s --arg m "$marker" --arg t "$text" '
        [ .[] | select(.type == "user" and ((.message.content | tostring) | contains($m)))
          | { source: (.promptSource // "none"),
              pasted: ((.message.content | tostring) | contains("<pasted_content")),
              whole: ((.message.content | tostring) | contains($t)) } ]' "$transcript" || echo "[\"transcript unreadable\"]")
    # A message queued during a reply may be recorded as another line type; those count as
    # arriving whole when they hold the full text.
    local queued
    queued=$(jq -c -s --arg t "$text" '
        [ .[] | select(.type != "user" and .type != "assistant" and (tostring | contains($t)))
          | { type, source: (.promptSource // "none") } ]' "$transcript" || echo "[]")
    if jq -e 'any(.[]; .whole)' <<<"$evidence" >/dev/null || jq -e 'length > 0' <<<"$queued" >/dev/null; then
        pass "$name: arrived whole (typed or pasted; recorded here); user lines with marker: $evidence; other lines holding the text: $queued"
    else
        fail "$name: no line holds the whole message; user lines with marker: $evidence; other lines holding the text: $queued"
    fi
}

# cleanup runs on every exit. It stops the private server, removes its socket file and the
# build directory, and removes the throwaway directory only when the guard accepts it.
cleanup() {
    local status=$?
    if [ -n "$server" ]; then
        tmux -L "$server" kill-server 2>/dev/null || true
    fi
    if [ -n "$socket" ]; then
        rm -f -- "$socket"
    fi
    if [ -n "$build" ]; then
        rm -rf -- "$build"
    fi
    remove_workdir
    exit "$status"
}

# remove_workdir removes the throwaway directory only when it is a directory this run made:
# a direct child of the temp base whose name starts with orrery-live-. Otherwise it is left
# in place and printed.
remove_workdir() {
    [ -n "$workdir" ] || return 0
    case "$workdir" in
        "$scratch_base"/orrery-live-*)
            if [ -d "$workdir" ]; then
                rm -rf -- "$workdir"
            fi
            ;;
        *)
            echo "left throwaway directory in place (guard refused): $workdir" >&2
            ;;
    esac
}

# Exit paths: the trap runs on normal exit and on INT or TERM, so the private server is
# stopped even when the run is interrupted.
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# TMUX from an outer session would make tmux attach there instead of the private server.
unset TMUX

# When this runs from inside a Claude Code session, the parent's session, messaging and
# notification variables would reach the live claude through the private server's
# environment. Drop them so the live session is unrelated to the caller and notifies no one.
while IFS= read -r name; do
    unset "$name"
done < <(compgen -e | grep -E '^(CLAUDECODE$|CLAUDE_|STEWARD_|TMUX_PANE$)' || true)

echo "build: go build ./cmd/orrery"
build=$(mktemp -d "$scratch_base/orrery-live-build-XXXXXX")
(cd "$repo" && go build -o "$build/orrery" ./cmd/orrery)

workdir=$(mktemp -d "$scratch_base/orrery-live-XXXXXX")
workdir=$(cd "$workdir" && pwd -P)
server="orrery-live-$$"

# The first call carries -f /dev/null so the private server reads no user config. The pane
# runs claude directly, as argv, so no shell quoting touches the settings JSON.
pane=$(tmux -L "$server" -f /dev/null new-session -d -P -F '#{pane_id}' \
    -s live -x 200 -y 50 -c "$workdir" \
    claude --model haiku --strict-mcp-config --settings '{"disableAllHooks":true}')

# The socket path is read from the running server, so the trap removes exactly that file.
socket=$(tm display-message -p '#{socket_path}')
echo "server: $server socket: $socket pane: $pane dir: $workdir"

wait_for "the folder-trust prompt or the input box" "$trust_limit" trust_or_box
if screen_has "trust"; then
    # The prompt opens on "No, exit", and Enter there quits claude. Move to "Yes, I trust this
    # folder" and confirm it is selected before pressing Enter.
    echo "accepting the folder-trust prompt: Down to its Yes choice, then Enter"
    tm send-keys -t "$pane" Down
    wait_for "the folder-trust prompt's Yes choice selected" 10 trust_yes_selected
    tm send-keys -t "$pane" Enter
fi
wait_for "the input box" "$box_limit" input_box_ready

# Case 1: nine lines of about 1,000 characters in all, typed as one message.
text1=$(nine_line_text)
echo "case 1: sending nine lines, ${#text1} characters"
send_message "done" "$text1"
wait_for "case 1 submitted to the transcript" "$submit_limit" transcript_has_text "$text1"
wait_for "case 1 reply containing $marker_nine" "$reply_limit" transcript_has_reply "$marker_nine"
wait_for "the input box after case 1" "$box_limit" input_box_ready
settle

# Case 2: one line of 3,000 characters.
text2=$(long_line_text)
echo "case 2: sending one line, ${#text2} characters"
send_message "done" "$text2"
wait_for "case 2 submitted to the transcript" "$submit_limit" transcript_has_text "$text2"
wait_for "case 2 reply containing $marker_long" "$reply_limit" transcript_has_reply "$marker_long"
wait_for "the input box after case 2" "$box_limit" input_box_ready
settle

# Case 3: a message sent while a long reply streams. The numbers request is sent first and
# the pane is marked working, so the mid-reply send is eligible as a working-state send.
echo "case 3: starting a long reply"
send_message "done" "$numbers_request"
wait_for "the numbers request submitted to the transcript" "$submit_limit" transcript_has_text "$numbers_request"
set_state working
wait_for "the numbers reply to stream" "$stream_limit" numbers_streaming

text3=$(mid_reply_text)
sent_at=$(date +%s)
echo "case 3: sending mid-reply message while the reply streams"
send_message working "$text3"
wait_for "case 3 submitted or queued to the transcript" "$reply_limit" transcript_has_any "$text3"
wait_for "the numbers reply to finish" "$reply_limit" transcript_has_reply "1999"
wait_for "case 3 reply containing $marker_mid" "$reply_limit" transcript_has_reply "$marker_mid"
wait_for "the input box after case 3" "$box_limit" input_box_ready

# Verdicts, read from the transcript.
echo "transcript: $transcript"
check_typed "case 1 (nine lines, about 1,000 characters in all)" "$marker_nine" "$text1"
check_typed "case 2 (one line, 3,000 characters)" "$marker_long" "$text2"
check_whole "case 3 (mid-reply)" "$marker_mid" "$text3"

# Recorded, not judged: the send went out only after the stream wait saw numbers on screen and
# no finished numbers reply in the transcript. How Claude Code stamps an assistant line (start
# or end of the reply) is unmeasured, so this line is evidence for the reader.
finished_at=$(numbers_finished_at)
echo "case 3 timing: mid-reply message sent at epoch $sent_at; the numbers reply's assistant line is stamped ${finished_at:-unknown}"

if ((failures > 0)); then
    echo "live check: $failures case(s) failed"
    exit 1
fi
echo "live check: all cases passed"
