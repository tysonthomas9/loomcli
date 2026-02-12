#!/usr/bin/env bash
# test-exhaustion.sh — Test epic exhaustion and worktree reassignment.
#
# Closes all tasks in one epic, then verifies the daemon reassigns
# the worktree to remaining work or falls back to non-epic mode.
#
# Prerequisites: Run setup.sh first, then source test-env.sh.
# Usage: ./test-exhaustion.sh

set -uo pipefail

TEST_DIR="${TEST_DIR:-/tmp/loom-daemon-e2e}"
cd "$TEST_DIR"
source test-env.sh

PASS=0
FAIL=0

pass() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL + 1)); }

echo "=== Test: Epic Exhaustion ==="
echo ""

# Ensure daemon is stopped
loom daemon stop 2>/dev/null || true
sleep 1

# Close all tasks in Epic A (Calculator) to simulate exhaustion
echo "--- Closing all tasks in Epic A ($EPIC_A) ---"
for TASK in $TASK_A1 $TASK_A2; do
  STATUS=$(bd show "$TASK" --json 2>/dev/null | jq -r '.[0].status // "unknown"' 2>/dev/null || echo "unknown")
  if [ "$STATUS" != "closed" ]; then
    bd close "$TASK" --reason="test: simulating exhaustion" 2>/dev/null || true
    echo "  Closed $TASK"
  else
    echo "  Already closed: $TASK"
  fi
done

# Verify Epic A has no ready tasks
READY_A=$(bd ready --parent "$EPIC_A" --json 2>/dev/null | jq '[.[] | select(.issue_type != "epic")] | length' 2>/dev/null || echo "0")
echo "  Ready tasks in Epic A: $READY_A"
if [ "$READY_A" -eq 0 ]; then pass "Epic A exhausted (no ready tasks)"; else fail "Epic A still has ready tasks"; fi

# Ensure Epic B has ready tasks
READY_B=$(bd ready --parent "$EPIC_B" --json 2>/dev/null | jq '[.[] | select(.issue_type != "epic")] | length' 2>/dev/null || echo "0")
echo "  Ready tasks in Epic B: $READY_B"

# Start daemon
echo ""
echo "--- Starting daemon ---"
rm -f .loom/logs/daemon-stdout.log worktrees/falcon/.agent.lock worktrees/nova/.agent.lock
loom daemon > .loom/logs/daemon-stdout.log 2>&1 &
sleep 8

# Check assignments
STATE=$(cat .loom/daemon-agents.json 2>/dev/null || echo "{}")
FALCON_EPIC=$(echo "$STATE" | jq -r '.agents[] | select(.worktree=="falcon") | .epic_id // empty' 2>/dev/null || echo "")
NOVA_EPIC=$(echo "$STATE" | jq -r '.agents[] | select(.worktree=="nova") | .epic_id // empty' 2>/dev/null || echo "")

echo "  falcon epic: ${FALCON_EPIC:-none}"
echo "  nova epic: ${NOVA_EPIC:-none}"

# Since Epic A is exhausted, neither agent should be assigned to it
if [ "$FALCON_EPIC" != "$EPIC_A" ] && [ "$NOVA_EPIC" != "$EPIC_A" ]; then
  pass "neither agent assigned to exhausted Epic A"
else
  fail "an agent was assigned to exhausted Epic A"
fi

# If Epic B has ready tasks, at least one agent should be on it
if [ "$READY_B" -gt 0 ]; then
  if [ "$FALCON_EPIC" = "$EPIC_B" ] || [ "$NOVA_EPIC" = "$EPIC_B" ]; then
    pass "an agent assigned to Epic B (has ready tasks)"
  else
    fail "no agent assigned to Epic B despite ready tasks"
  fi
fi

# Check daemon log for exhaustion handling
if grep -q "exhausted\|no ready tasks\|non-epic\|no more epics" .loom/logs/daemon-stdout.log 2>/dev/null; then
  pass "daemon log mentions exhaustion/fallback"
fi

# Clean up
loom daemon stop 2>/dev/null || true
sleep 2

echo ""
echo "=== Exhaustion Results: $PASS passed, $FAIL failed ==="
if [ "$FAIL" -eq 0 ]; then exit 0; else exit 1; fi
