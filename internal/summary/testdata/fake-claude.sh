#!/bin/sh
# Scripted stand-in for the claude binary. Each run records its invocation under
# calls/<run>/ beside this script, then behaves as the files beside it direct:
#   hold   the run sleeps for as long as this file exists
#   exit   the run exits with this file's content as its status and prints no reply
#   reply  the run prints this file's content
D=$(dirname "$0")
mkdir -p "$D/calls"
R=$(mktemp -d "$D/calls/run.XXXXXX")
printf '%s\n' "$@" > "$R/args"
env | grep -E '^(ANTHROPIC_BASE_URL|ORRERY_SUMMARIZER)=' > "$R/env"
pwd > "$R/cwd"
cat > "$R/stdin"
while [ -e "$D/hold" ]; do
	sleep 0.05
done
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
