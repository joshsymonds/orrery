#!/bin/sh
# fakesystemdrun stands in for systemd-run in the launch tests. It appends each argument on its own line to the file
# $FAKE_SYSTEMD_RUN_ARGS when that is set. When $FAKE_SYSTEMD_RUN_FAIL is set, it writes that to stderr and exits 1.
# Otherwise it drops the seven scope arguments and execs the rest, so the holder runs in this process's pid, as it
# does under a real scope.

if [ -n "$FAKE_SYSTEMD_RUN_ARGS" ]; then
	for arg in "$@"; do
		printf '%s\n' "$arg" >>"$FAKE_SYSTEMD_RUN_ARGS"
	done
fi
if [ -n "$FAKE_SYSTEMD_RUN_FAIL" ]; then
	printf '%s\n' "$FAKE_SYSTEMD_RUN_FAIL" >&2
	exit 1
fi
shift 7
exec "$@"
