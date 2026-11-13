#!/bin/sh
# Scripted stand-in for the claude binary. Each run records its invocation under
# calls/<run>/ beside this script, then behaves as the files beside it direct:
#   stdout the run prints this file's content on stdout before it holds or exits
#   stderr the run prints this file's content on stderr before it holds or exits
#   hold   the run sleeps for as long as this file exists
#   kill   the run sends itself SIGKILL once it stops holding
#   exit   the run exits with this file's content as its status and prints no reply
#   reply  the run prints this file's content
D=$(dirname "$0")
mkdir -p "$D/calls"
R=$(mktemp -d "$D/calls/run.XXXXXX")
printf '%s\n' "$@" > "$R/args"
env | grep -E '^(ANTHROPIC_BASE_URL|ORRERY_SUMMARIZER)=' > "$R/env"
pwd > "$R/cwd"
cat > "$R/stdin"
if [ -e "$D/stdout" ]; then
	cat "$D/stdout"
fi
if [ -e "$D/stderr" ]; then
	cat "$D/stderr" >&2
fi
while [ -e "$D/hold" ]; do
	sleep 0.05
done
if [ -e "$D/kill" ]; then
	kill -KILL $$
fi
if [ -e "$D/exit" ]; then
	status=$(cat "$D/exit")
	: > "$R/done"
	exit "$status"
fi
if [ -e "$D/reply" ]; then
	cat "$D/reply"
fi
: > "$R/done"
exit 0
