#!/usr/bin/env bash
#
# Runs one Android build at a time on this machine when a gate falls back to it.
#
# Usage:
#   scripts/android-lock.sh <command> [args...]
#       With ORRERY_ANDROID_LOCK set, holds an exclusive flock on the file it names for the life of the command, waiting
#       for any other holder first and saying so on stderr. scripts/gate-remote.sh sets it when it runs a gate here, so
#       the gates that fall back to this machine take turns at Gradle instead of each taking every core. Unset, it runs
#       the command as it is: a run on a gate box, or one by hand. Exits with the command's status.
set -euo pipefail

if [ "$#" -eq 0 ]; then
    echo "usage: android-lock.sh <command> [args...]" >&2
    exit 2
fi
if [ -z "${ORRERY_ANDROID_LOCK:-}" ]; then
    exec "$@"
fi
exec 9>>"$ORRERY_ANDROID_LOCK"
if ! flock -n 9; then
    echo "android-lock: waiting for another Android build on this machine ($ORRERY_ANDROID_LOCK)" >&2
    flock 9
fi
exec "$@"
