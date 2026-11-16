#!/bin/sh
# fakesystemctl stands in for systemctl; FakeScope writes it as systemctl beside its systemd-run. It takes only --user
# stop UNIT. It appends its arguments, on one line, to $FAKE_SYSTEMCTL_ARGS when that is set. UNIT is loaded while a live
# process carries the FAKE_SCOPE_UNIT the systemd-run beside it gives UNIT's holder and everything that holder starts. A
# loaded UNIT is stopped as systemd stops a scope: each process in it gets SIGTERM, one still running once the TERM wait
# is over gets SIGKILL, and the stop returns once none is left. A UNIT that is not loaded fails as systemctl fails it,
# with status 5.

if [ -n "$FAKE_SYSTEMCTL_ARGS" ]; then
	printf '%s\n' "$*" >>"$FAKE_SYSTEMCTL_ARGS"
fi
if [ "$1 $2" != "--user stop" ] || [ $# -ne 3 ]; then
	printf 'fake systemctl: want --user stop UNIT, got %s\n' "$*" >&2
	exit 2
fi
unit=$3
here=$(cd "$(dirname "$0")" && pwd -P)

# members prints the pids of the live processes in the unit. A process that has exited has an empty environment, so a
# zombie is not a member.
members() {
	grep -lzx "FAKE_SCOPE_UNIT=$here/$unit" /proc/[0-9]*/environ 2>/dev/null | sed 's|^/proc/||; s|/environ$||'
}

pids=$(members)
if [ -z "$pids" ]; then
	printf 'Failed to stop %s: Unit %s not loaded.\n' "$unit" "$unit" >&2
	exit 5
fi
# shellcheck disable=SC2086 # each pid is its own argument
kill -TERM $pids 2>/dev/null
# The TERM wait is 100 polls; a process still running after it is killed, as systemd kills one after TimeoutStopSec.
polls=0
while pids=$(members) && [ -n "$pids" ]; do
	polls=$((polls + 1))
	if [ "$polls" -gt 100 ]; then
		# shellcheck disable=SC2086 # each pid is its own argument
		kill -KILL $pids 2>/dev/null
	fi
	sleep 0.01
done
exit 0
