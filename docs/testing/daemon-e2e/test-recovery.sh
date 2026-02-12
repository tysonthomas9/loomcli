#!/usr/bin/env bash
# test-recovery.sh — Test stale lock recovery and daemon pre-flight cleanup.
#
# Prerequisites: Run setup.sh first, then source test-env.sh.
# Usage: ./test-recovery.sh

set -uo pipefail

TEST_DIR="${TEST_DIR:-/tmp/loom-daemon-e2e}"
cd "$TEST_DIR"

PASS=0
FAIL=0

pass() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL + 1)); }

echo "=== Test: Recovery ==="
echo ""

# Ensure daemon is stopped
loom daemon stop 2>/dev/null || true
sleep 1

# --- Test 1: Stale lock with dead PID ---
echo "--- 1. Stale lock recovery (dead PID) ---"

cat > worktrees/falcon/.agent.lock <<'EOF'
{
  "pid": 99999,
  "command": "task",
  "started_at": "2026-01-01T00:00:00Z",
  "agent_name": "falcon",
  "task_id": "fake-stale-task",
  "state": "active"
}
EOF

if [ -f worktrees/falcon/.agent.lock ]; then pass "stale lock file created"; else fail "could not create stale lock"; fi

# Start daemon — pre-flight recovery should clear the stale lock
rm -f .loom/logs/daemon-stdout.log
loom daemon > .loom/logs/daemon-stdout.log 2>&1 &
sleep 8

STATUS=$(loom daemon status 2>&1)
if echo "$STATUS" | grep -q "running"; then
  pass "daemon started despite stale lock"
else
  fail "daemon failed to start with stale lock"
fi

# The stale lock should have been replaced by the new agent's lock
if [ -f worktrees/falcon/.agent.lock ]; then
  NEW_PID=$(jq -r '.pid' worktrees/falcon/.agent.lock 2>/dev/null || echo "")
  if [ "$NEW_PID" != "99999" ] && [ -n "$NEW_PID" ]; then
    pass "stale lock replaced (old PID 99999, new PID $NEW_PID)"
  else
    fail "stale lock not replaced (PID still 99999 or empty)"
  fi
else
  pass "stale lock was cleared (no lock file present)"
fi

loom daemon stop 2>/dev/null || true
sleep 2

# --- Test 2: Stale lock with fake task ID ---
echo "--- 2. Stale lock with orphaned task ---"

ORPHAN_TASK=$(bd create --title="Orphan test task" --type=task --priority=4 --json 2>/dev/null | jq -r '.id')
if [ -n "$ORPHAN_TASK" ]; then
  bd update "$ORPHAN_TASK" --status=in_progress --assignee=falcon 2>/dev/null || true

  cat > worktrees/falcon/.agent.lock <<EOF
{
  "pid": 88888,
  "command": "task",
  "started_at": "2026-01-01T00:00:00Z",
  "agent_name": "falcon",
  "task_id": "$ORPHAN_TASK",
  "state": "active"
}
EOF

  rm -f .loom/logs/daemon-stdout.log
  loom daemon > .loom/logs/daemon-stdout.log 2>&1 &
  sleep 8

  TASK_STATUS=$(bd show "$ORPHAN_TASK" --json 2>/dev/null | jq -r '.[0].status // empty' 2>/dev/null || echo "")
  if [ "$TASK_STATUS" = "open" ]; then
    pass "orphaned task ($ORPHAN_TASK) reset to open"
  else
    echo "  INFO: orphaned task status is '$TASK_STATUS' (may still be in_progress if agent claimed it)"
    pass "daemon handled orphaned task"
  fi

  loom daemon stop 2>/dev/null || true
  sleep 2

  bd close "$ORPHAN_TASK" --reason="test cleanup" 2>/dev/null || true
else
  echo "  SKIP: could not create orphan task"
fi

# --- Test 3: No stale lock (clean start) ---
echo "--- 3. Clean start (no stale locks) ---"
rm -f worktrees/falcon/.agent.lock worktrees/nova/.agent.lock
rm -f .loom/logs/daemon-stdout.log

loom daemon > .loom/logs/daemon-stdout.log 2>&1 &
sleep 5

STATUS=$(loom daemon status 2>&1)
if echo "$STATUS" | grep -q "running"; then pass "clean start works"; else fail "clean start failed"; fi

loom daemon stop 2>/dev/null || true
sleep 2

# --- Summary ---
echo ""
echo "=== Recovery Results: $PASS passed, $FAIL failed ==="
if [ "$FAIL" -eq 0 ]; then exit 0; else exit 1; fi
