#!/usr/bin/env bash
#
# Doc citation gate: docs/ cites code by stable symbol, never by line number.
# A line number moves with every edit above it, so `file.go:123` citations go
# stale and conflict on every merge. Cite the enclosing symbol instead:
#
#   Go     package.Func, package.Type.Method, or a test name (TestFoo)
#   SQL    path plus the sqlc query name: internal/store/queries.sql: ListSteps
#   Proto  path plus the message, field or RPC: .../step.proto: StepView.state
#   Other  path plus the heading, recipe or function the line sits in
#
# Recorded fixtures (testdata/*.ndjson) are fixed data and may still be cited
# by line.

set -euo pipefail

cd "$(dirname "$0")/.."

pattern='[A-Za-z0-9_./-]\.(go|sql|proto|ts|tsx|kt|nix|sh|mod):[0-9]|(^|[^A-Za-z0-9_.-])justfile:[0-9]'

if hits=$(grep -rnE "$pattern" docs/); then
    echo "check-doc-cites: line-number citations in docs/:" >&2
    printf '%s\n' "$hits" >&2
    count=$(printf '%s\n' "$hits" | wc -l)
    echo "check-doc-cites: $count line(s) cite code by file:line; cite the enclosing symbol instead (see scripts/check-doc-cites.sh)" >&2
    exit 1
fi
echo "check-doc-cites: no line-number citations in docs/"
