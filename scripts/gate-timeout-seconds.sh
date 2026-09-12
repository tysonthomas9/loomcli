#!/usr/bin/env bash
# gate-timeout-seconds.sh - Resolve the wall-time cap for one gate invocation.
#
# Prints the cap, in seconds, to stdout and NOTHING else — the Makefile parses
# stdout (`cap=$(./scripts/gate-timeout-seconds.sh)`), so every diagnostic goes
# to stderr. A cap of 0 means "disabled": run the gate unwrapped.
#
# Resolution order:
#   1. LOOM_GATE_TIMEOUT_SECONDS, if a non-negative integer, wins verbatim
#      (including 0, the deliberate escape hatch for a long local run).
#   2. else LOOM_RUN_TURN_TIMEOUT_SECONDS (exported by the loom supervisor as
#      the per-turn budget), if a positive integer:
#         cap = min(GATE_DEFAULT, budget - GATE_MARGIN)
#      so the gate always dies far enough inside the turn for the agent to read
#      the failure and check its work in.
#   3. else GATE_DEFAULT.
# Results from rules 2 and 3 are clamped to GATE_FLOOR so a pathological budget
# can never produce a cap that fails instantly and makes the gate unusable.
#
# Garbage in either variable is ignored (with a warning on stderr) and the next
# rule applies — a typo must not silently unbound or instantly red the gate.
#
# Usage: ./scripts/gate-timeout-seconds.sh
set -uo pipefail

# 1800s is ~2.7x a measured green CI `make check-go` (11m09s), which leaves room
# for a loaded shared host while staying well inside a fleet turn budget.
GATE_DEFAULT=1800
GATE_MARGIN=300
GATE_FLOOR=300

is_non_negative_int() {
    case "$1" in
        '' | *[!0-9]* ) return 1 ;;
        * ) return 0 ;;
    esac
}

override="${LOOM_GATE_TIMEOUT_SECONDS:-}"
if [ -n "$override" ]; then
    if is_non_negative_int "$override"; then
        echo "$override"
        exit 0
    fi
    echo "[gate] warning: ignoring LOOM_GATE_TIMEOUT_SECONDS='$override' (not a non-negative integer)" >&2
fi

budget="${LOOM_RUN_TURN_TIMEOUT_SECONDS:-}"
cap=$GATE_DEFAULT
if [ -n "$budget" ]; then
    if is_non_negative_int "$budget" && [ "$budget" -gt 0 ]; then
        derived=$((budget - GATE_MARGIN))
        if [ "$derived" -lt "$cap" ]; then
            cap=$derived
        fi
    else
        echo "[gate] warning: ignoring LOOM_RUN_TURN_TIMEOUT_SECONDS='$budget' (not a positive integer)" >&2
    fi
fi

if [ "$cap" -lt "$GATE_FLOOR" ]; then
    cap=$GATE_FLOOR
fi

echo "$cap"
