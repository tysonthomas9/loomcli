#!/usr/bin/env bash
# Checks a Go coverage profile against a minimum threshold.
# Usage: ./scripts/check-coverage.sh [profile_path] [threshold]
# Env vars: COVERAGE_PROFILE, COVERAGE_THRESHOLD

set -euo pipefail

PROFILE="${1:-${COVERAGE_PROFILE:-/tmp/loom.coverage.out}}"
THRESHOLD="${2:-${COVERAGE_THRESHOLD:-70}}"

if [[ ! -f "$PROFILE" ]]; then
    echo "Error: Coverage profile not found: $PROFILE" >&2
    exit 1
fi

total=$(go tool cover -func="$PROFILE" | awk '/^total:/ {gsub(/%/, "", $NF); print $NF}')

if [[ -z "$total" ]]; then
    echo "Error: Could not extract total coverage from profile" >&2
    exit 1
fi

echo "Coverage: ${total}% (threshold: ${THRESHOLD}%)" >&2

if awk "BEGIN {exit !(${total} < ${THRESHOLD})}"; then
    echo "FAIL: Coverage ${total}% is below threshold ${THRESHOLD}%" >&2
    exit 1
fi

echo "PASS: Coverage ${total}% meets threshold ${THRESHOLD}%" >&2
