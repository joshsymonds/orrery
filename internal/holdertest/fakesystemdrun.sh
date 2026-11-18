#!/bin/sh
# fakesystemdrun stands in for systemd-run; FakeScope writes it as systemd-run. It appends each argument on its own line
# to the file $FAKE_SYSTEMD_RUN_ARGS when that is set. When $FAKE_SYSTEMD_RUN_FAIL is set, it writes that to stderr and
# exits 1. Otherwise it drops the scope's arguments, every argument up to the first that does not start with a dash,
# with the value each -p and --unit takes, and execs the rest, so the holder runs in this process's pid, as it does
# under a real scope.
#
# It stands in for the scope as well. It exports FAKE_SCOPE_UNIT, the unit --unit names, prefixed with
# this script's directory, so the scopes of two tests never share a name. Every process the holder starts inherits it,
# as each inherits a real scope's cgroup. A unit is loaded while a live process carries it in its environment. As
# systemd does for a loaded unit, it refuses to start a scope whose name is loaded. The systemctl beside it stops one.

if [ -n "$FAKE_SYSTEMD_RUN_ARGS" ]; then
	for arg in "$@"; do
		printf '%s\n' "$arg" >>"$FAKE_SYSTEMD_RUN_ARGS"
	done
fi
if [ -n "$FAKE_SYSTEMD_RUN_FAIL" ]; then
	printf '%s\n' "$FAKE_SYSTEMD_RUN_FAIL" >&2
	exit 1
fi
unit=
while [ $# -gt 0 ]; do
	case $1 in
	--unit)
		unit=$2.scope
		shift 2
		;;
	-p)
		shift 2
		;;
	-*)
		shift
		;;
	*)
		break
		;;
	esac
done
here=$(cd "$(dirname "$0")" && pwd -P)
# -q exits 0 on a match although other users' environments cannot be read.
if grep -qzx "FAKE_SCOPE_UNIT=$here/$unit" /proc/[0-9]*/environ 2>/dev/null; then
	printf 'Failed to start transient scope unit: Unit %s was already loaded or has a fragment file.\n' "$unit" >&2
	exit 1
fi
FAKE_SCOPE_UNIT=$here/$unit
export FAKE_SCOPE_UNIT
exec "$@"
