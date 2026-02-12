#!/usr/bin/env bash
# test-daemon-e2e-single.sh — Single-worktree E2E test for epic branch switching.
#
# Verifies that with a single agent (falcon), the daemon:
#   1. Completes Epic A tasks (A1, A2) on the epic-alpha branch
#   2. Transitions to Epic B and completes task B1 (epic branch switch)
#   3. Falls back to non-epic mode and completes standalone task S
#
# This exercises handleEpicTransition() in daemon_exhaust.go end-to-end.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TEST_DIR="${1:-${SINGLE_WT_TEST_DIR:-/tmp/loom-mzr-test-single}}"

# Setup with single worktree
bash "$SCRIPT_DIR/setup.sh" --single-worktree "$TEST_DIR"

source "$TEST_DIR/test-env.sh"

# Longer timeouts since all work is sequential through 1 agent
export PLAN_TIMEOUT=600   # 10 min (was 5 min with 2 agents)
export IMPL_TIMEOUT=1500  # 25 min (was 15 min with 2 agents)

# Run the standard E2E test (plan → review → implement)
E2E_EXIT=0
bash "$SCRIPT_DIR/test-daemon-e2e.sh" || E2E_EXIT=$?

# Additional verification: check daemon logs for epic transition messages
echo ""
echo "--- Epic Transition Verification ---"

TRANSITION_PASS=0
TRANSITION_FAIL=0

if grep -q "transitioning from epic" "$TEST_DIR/.loom/logs/"*.log 2>/dev/null; then
  echo "  PASS: Epic-to-epic transition occurred"
  grep "transitioning from epic" "$TEST_DIR/.loom/logs/"*.log | sed 's/^/    /'
  TRANSITION_PASS=$((TRANSITION_PASS + 1))
else
  echo "  FAIL: No epic-to-epic transition found in logs"
  TRANSITION_FAIL=$((TRANSITION_FAIL + 1))
fi

if grep -q "switching to non-epic mode" "$TEST_DIR/.loom/logs/"*.log 2>/dev/null; then
  echo "  PASS: Non-epic fallback occurred"
  grep "switching to non-epic mode" "$TEST_DIR/.loom/logs/"*.log | sed 's/^/    /'
  TRANSITION_PASS=$((TRANSITION_PASS + 1))
else
  echo "  FAIL: No non-epic fallback found in logs"
  TRANSITION_FAIL=$((TRANSITION_FAIL + 1))
fi

echo ""
echo "=== Single-Worktree E2E: transitions $TRANSITION_PASS passed, $TRANSITION_FAIL failed ==="

if [ "$E2E_EXIT" -ne 0 ] || [ "$TRANSITION_FAIL" -gt 0 ]; then
  exit 1
fi
