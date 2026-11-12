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
# orreryd, the desk or the phone. Hooks also set the pane's @orrery_state, and with them off the
# script sets that option by hand before each send.
#
# Usage: scripts/typed-live-check.sh [agents|titled|overflow]
# With no argument, runs the three numbered cases. With `agents`, runs only the agent-list case:
# two forked subtasks, then a nine-line message sent while both are listed. With `titled`, renames
# the session and sends a nine-line message while the upper rule shows the name. With `overflow`,
# forks six subtasks and sends a nine-line message while the agent list ends in a more row.
# Exit status: 0 when every case passes, 1 when one fails, 2 for a bad argument.

set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd -P)

# Machine-wide globals. Empty until the step that creates them runs, so the EXIT trap
# only removes what this run made.
server=""
socket=""
build=""
workdir=""
transcript=""
session_id=""
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
fork_limit=60
agents_limit=120
rename_limit=30

# The three numbered cases and the agent case. Each carries an invented marker word that
# Claude is asked to echo alone, so the reply can be recognised in the transcript.
marker_nine=AMBERKITE
marker_long=BLUEFINCH
marker_mid=COBALTWREN
marker_agents=VERMILION
marker_titled=TIDEWRACK
marker_overflow=GRANITEOWL
titled_name=Quillon
numbers_request="print the numbers 1 to 2000, one per line, nothing else"

# The pane's width in columns, which the titled rule fills.
pane_cols=200

# tm runs tmux on the private server. Every tmux call goes through here so that none can
# reach a server this script did not create.
tm() {
    tmux -L "$server" "$@"
}

# usage prints the usage line to stderr.
usage() {
    echo "usage: scripts/typed-live-check.sh [agents|titled|overflow]" >&2
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

# find_transcript prints the main session's transcript: <session_id>.jsonl directly in a project
# directory named after the throwaway directory's unique name, modified since this run started,
# whose lines carry .cwd equal to the throwaway directory. The session id is the one this run
# passed to claude, so a forked subtask's file under <session_id>/subagents/ is never picked.
# The trailing slash makes find descend when ~/.claude/projects is a symlink.
find_transcript() {
    [ -d "$HOME/.claude/projects" ] || return 0
    find "$HOME/.claude/projects/" -mindepth 2 -maxdepth 2 -name "$session_id.jsonl" \
        -path "*${workdir##*/}/*" -newermt "@$started" 2>/dev/null |
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

# agent_names prints the names of the agent rows under the input box, one per line, leaving out
# the main row. An agent row is two spaces, a bullet (U+25CF or U+25EF), a space and the name.
agent_names() {
    local text
    text=$(screen)
    sed -n -E 's/^  (●|◯) +([^[:space:]]+).*$/\2/p' <<<"$text" | grep -vxF -- main || true
}

# agents_listed succeeds when two or more agent rows besides main are on screen.
agents_listed() {
    [ "$(agent_names | grep -c .)" -ge 2 ]
}

# agents_still_listed NAMES succeeds when NAMES, one name per line, holds at least two names and
# every one of them is still an agent row on screen.
agents_still_listed() {
    local names=$1 name current
    [ "$(grep -c . <<<"$names")" -ge 2 ] || return 1
    current=$(agent_names)
    while IFS= read -r name; do
        grep -qxF -- "$name" <<<"$current" || return 1
    done <<<"$names"
}

# forks_at_least COUNT succeeds when the screen shows at least COUNT "forked" lines, one per
# /subtask that Claude Code has started.
forks_at_least() {
    local text
    text=$(screen)
    [ "$(grep -c -- '⑂ forked' <<<"$text")" -ge "$1" ]
}

# slash_send LINE types LINE at the pane with tmux keys and presses Enter, for a slash command
# such as /subtask or /rename. It is setup for a case, not an orrery send, so it leaves the
# pane's state alone. Enter goes after a pause, as orrery send does, so Claude Code reads it as
# its own key.
slash_send() {
    tm send-keys -t "$pane" -l "$1"
    sleep 0.3
    tm send-keys -t "$pane" Enter
}

# titled_rule_on_screen NAME succeeds when a full-width upper rule ending " NAME ─" is on screen:
# pane_cols columns in all, U+2500 characters, then a space, NAME, a space and one U+2500.
titled_rule_on_screen() {
    local name=$1 fill expected text
    printf -v fill '%*s' $((pane_cols - ${#name} - 3)) ''
    expected="${fill// /─} $name ─"
    text=$(screen)
    grep -qxF -- "$expected" <<<"$text"
}

# more_row_shown succeeds when the agent list's more row is on screen: two spaces, U+2193, a
# space, a count and " more".
more_row_shown() {
    local text
    text=$(screen)
    grep -qE -- '^  ↓ [0-9]+ more$' <<<"$text"
}

# more_ends_list succeeds when the last agent-list row on screen is the more row, with at least
# one agent row above it. Only agent rows and the more row are considered, so the footer is left out.
more_ends_list() {
    local text last
    text=$(screen)
    last=$(grep -E -- '^  (●|◯) |^  ↓ [0-9]+ more$' <<<"$text" | tail -n 1)
    grep -qE -- '^  ↓ [0-9]+ more$' <<<"$last" && grep -qE -- '^  (●|◯) ' <<<"$text"
}

# x_nine_text MARKER CASE prints nine lines of about 1,100 characters in all with no trailing
# newline. Like case 1, it is over 800 characters and three lines, so a paste would be collapsed
# and fail the typed verdict. The first line starts with x, reads CASE and carries MARKER. Every
# line holds an x inside its words.
x_nine_text() {
    local marker=$1 case_note=$2 lines=() i
    lines+=("x ${case_note} Reply with only ${marker} and nothing else.")
    for i in 2 3 4 5 6 7 8 9; do
        lines+=("Line $i of 9: the next box holds extra text, with six more words that mix in a little filler, lorem ipsum dolor sit amet.")
    done
    (IFS=$'\n'; printf '%s' "${lines[*]}")
}

# agents_case forks two subtasks, checks that two agent rows besides main are listed, sends a
# nine-line message while they are, and checks that both are still listed right after the send.
# Each capture is printed. The message verdict comes from the main transcript.
agents_case() {
    local names_before text4
    echo "agents: forking two subtasks with /subtask"
    slash_send "/subtask Run 1: print the numbers 1 to 6000, one per line, no other text, no tools."
    wait_for "the first subtask forked" "$fork_limit" forks_at_least 1 || true
    slash_send "/subtask Run 2: print the numbers 1 to 6000, one per line, no other text, no tools."
    wait_for "the second subtask forked" "$fork_limit" forks_at_least 2 || true
    wait_for "two agent rows besides main" "$agents_limit" agents_listed || true

    echo "screen at the send, before the nine-line message:"
    screen
    names_before=$(agent_names)
    if agents_listed; then
        pass "two agent rows besides main listed at the send: $(tr '\n' ' ' <<<"$names_before")"
    else
        fail "fewer than two agent rows besides main listed at the send; listed: $(tr '\n' ' ' <<<"$names_before")"
    fi

    text4=$(x_nine_text "$marker_agents" "Case 4 of the orrery live check, nine lines while two agents are listed.")
    echo "agents: sending nine lines, ${#text4} characters, while the agents are listed"
    send_message "done" "$text4"
    echo "screen right after the send:"
    screen
    if agents_still_listed "$names_before"; then
        pass "both agents from the send still listed after the send: $(tr '\n' ' ' <<<"$names_before")"
    else
        fail "agents from the send not all still listed after the send; at the send: $(tr '\n' ' ' <<<"$names_before")"
    fi

    wait_for "agent case submitted to the transcript" "$submit_limit" transcript_has_text "$text4"
    wait_for "agent case reply containing $marker_agents" "$reply_limit" transcript_has_reply "$marker_agents"
    echo "transcript: $transcript"
    check_typed "agents case (nine lines containing x, two agents listed)" "$marker_agents" "$text4"
}

# titled_case renames the session with /rename, checks that a full-width upper rule ending in the
# name is on screen at the send and right after it, then sends a nine-line message. Each capture
# is printed. The message verdict comes from the main transcript.
titled_case() {
    local text5
    echo "titled: renaming the session with /rename $titled_name"
    slash_send "/rename $titled_name"
    wait_for "the session renamed" "$rename_limit" screen_has "Session renamed to: $titled_name" || true

    echo "screen at the send, before the nine-line message:"
    screen
    if titled_rule_on_screen "$titled_name"; then
        pass "full-width upper rule ending ' $titled_name ─' on screen at the send"
    else
        fail "no full-width upper rule ending ' $titled_name ─' on screen at the send"
    fi

    text5=$(x_nine_text "$marker_titled" "Case 5 of the orrery live check, nine lines while the session is titled.")
    echo "titled: sending nine lines, ${#text5} characters, under the titled rule"
    send_message "done" "$text5"
    echo "screen right after the send:"
    screen
    if titled_rule_on_screen "$titled_name"; then
        pass "full-width upper rule ending ' $titled_name ─' on screen right after the send"
    else
        fail "no full-width upper rule ending ' $titled_name ─' on screen right after the send"
    fi

    wait_for "titled case submitted to the transcript" "$submit_limit" transcript_has_text "$text5"
    wait_for "titled case reply containing $marker_titled" "$reply_limit" transcript_has_reply "$marker_titled"
    echo "transcript: $transcript"
    check_typed "titled case (nine lines containing x, session titled)" "$marker_titled" "$text5"
}

# overflow_case forks six subtasks, each waited for, then checks that the agent list ends in a
# more row at the send and right after it, and sends a nine-line message between the two checks.
# Each capture is printed. The message verdict comes from the main transcript.
overflow_case() {
    local n text6
    echo "overflow: forking six subtasks with /subtask"
    for n in 1 2 3 4 5 6; do
        slash_send "/subtask Run $n: print the numbers 1 to 20000, one per line, no other text, no tools."
        wait_for "subtask $n forked" "$fork_limit" forks_at_least "$n" || true
    done
    wait_for "the more row under the agent list" "$agents_limit" more_row_shown || true

    echo "screen at the send, before the nine-line message:"
    screen
    if more_ends_list; then
        pass "more row ends the agent list at the send"
    else
        fail "no more row ends the agent list at the send"
    fi

    text6=$(x_nine_text "$marker_overflow" "Case 6 of the orrery live check, nine lines while six subtasks are listed.")
    echo "overflow: sending nine lines, ${#text6} characters, while the agent list overflows"
    send_message "done" "$text6"
    echo "screen right after the send:"
    screen
    if more_ends_list; then
        pass "more row ends the agent list right after the send"
    else
        fail "no more row ends the agent list right after the send"
    fi

    wait_for "overflow case submitted to the transcript" "$submit_limit" transcript_has_text "$text6"
    wait_for "overflow case reply containing $marker_overflow" "$reply_limit" transcript_has_reply "$marker_overflow"
    echo "transcript: $transcript"
    check_typed "overflow case (nine lines containing x, agent list overflowing)" "$marker_overflow" "$text6"
}

# summarize prints the run's result. It returns 1 when a case failed, which ends the run under
# set -e with the same status.
summarize() {
    if ((failures > 0)); then
        echo "live check: $failures case(s) failed"
        return 1
    fi
    echo "live check: all cases passed"
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

# Arguments are checked before anything is built or started. No argument runs the three
# numbered cases; `agents`, `titled` and `overflow` each run only their case.
mode=all
if [ $# -gt 1 ]; then
    usage
    exit 2
elif [ $# -eq 1 ]; then
    case "$1" in
    agents | titled | overflow) mode=$1 ;;
    *)
        usage
        exit 2
        ;;
    esac
fi

echo "build: go build ./cmd/orrery"
build=$(mktemp -d "$scratch_base/orrery-live-build-XXXXXX")
(cd "$repo" && go build -o "$build/orrery" ./cmd/orrery)

workdir=$(mktemp -d "$scratch_base/orrery-live-XXXXXX")
workdir=$(cd "$workdir" && pwd -P)
server="orrery-live-$$"

# The session id is chosen here and passed to claude, so the main transcript is the file named
# by this id. A forked subtask writes under that id's subagents directory instead.
session_id=$(cat /proc/sys/kernel/random/uuid)

# The first call carries -f /dev/null so the private server reads no user config. The pane
# runs claude directly, as argv, so no shell quoting touches the settings JSON.
pane=$(tmux -L "$server" -f /dev/null new-session -d -P -F '#{pane_id}' \
    -s live -x "$pane_cols" -y 50 -c "$workdir" \
    claude --model haiku --strict-mcp-config --session-id "$session_id" \
    --settings '{"disableAllHooks":true}')

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

if [ "$mode" != all ]; then
    case "$mode" in
    agents) agents_case ;;
    titled) titled_case ;;
    overflow) overflow_case ;;
    esac
    summarize
    exit 0
fi

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
text3=$(mid_reply_text)
sent_at=""
if wait_for "the numbers reply to stream" "$stream_limit" numbers_streaming; then
    sent_at=$(date +%s)
    echo "case 3: sending mid-reply message while the reply streams"
    send_message working "$text3"
    # These waits only give the mid-reply message time to land; a timeout here still goes on
    # to the verdicts, which judge from the transcript, so cases 1 and 2 are always reported.
    wait_for "case 3 submitted or queued to the transcript" "$reply_limit" transcript_has_any "$text3" ||
        echo "continuing to the verdicts"
    wait_for "the numbers reply to finish" "$reply_limit" transcript_has_reply "1999" ||
        echo "continuing to the verdicts"
    wait_for "case 3 reply containing $marker_mid" "$reply_limit" transcript_has_reply "$marker_mid" ||
        echo "continuing to the verdicts"
fi

# Verdicts, read from the transcript.
echo "transcript: $transcript"
check_typed "case 1 (nine lines, about 1,000 characters in all)" "$marker_nine" "$text1"
check_typed "case 2 (one line, 3,000 characters)" "$marker_long" "$text2"
if [ -n "$sent_at" ]; then
    check_whole "case 3 (mid-reply)" "$marker_mid" "$text3"
    # Recorded, not judged: the send went out only after the stream wait saw numbers on screen
    # and no finished numbers reply in the transcript. How Claude Code stamps an assistant line
    # (start or end of the reply) is unmeasured, so this line is evidence for the reader.
    finished_at=$(numbers_finished_at)
    echo "case 3 timing: mid-reply message sent at epoch $sent_at; the numbers reply's assistant line is stamped ${finished_at:-unknown}"
else
    fail "case 3 (mid-reply): the numbers reply was never seen streaming, so nothing was sent mid-reply"
fi

summarize
