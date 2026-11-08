#!/bin/sh
# Scripted stand-in for the zoxide binary. Each run records its argv under calls/<run>/ beside
# this script, then behaves as the files beside it direct:
#   exit   the run exits with this file's content as its status and prints no reply
#   reply  the run prints this file's content
D=$(dirname "$0")
mkdir -p "$D/calls"
R=$(mktemp -d "$D/calls/run.XXXXXX")
printf '%s\n' "$@" > "$R/args"
if [ -e "$D/exit" ]; then
	exit "$(cat "$D/exit")"
fi
if [ -e "$D/reply" ]; then
	cat "$D/reply"
fi
exit 0
