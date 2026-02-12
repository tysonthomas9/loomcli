#!/usr/bin/env bash
# test-agent-execution.sh — Verify agents claim tasks, make commits, and close tasks.
#
# This test runs the daemon and waits for agents to complete tasks.
# It uses real Claude agents so it may take several minutes.
#
# Prerequisites: Run setup.sh first, then source test-env.sh.
# Usage: ./test-agent-execution.sh [max_wait_minutes]
#   max_wait_minutes defaults to 30

set -uo pipefail

TEST_DIR="${TEST_DIR:-/tmp/loom-daemon-e2e}"
MAX_WAIT="${1:-30}"
cd "$TEST_DIR"
source test-env.sh

PASS=0
FAIL=0

pass() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL + 1)); }

echo "=== Test: Agent Execution ==="
echo "  Max wait: ${MAX_WAIT} minutes"
echo ""

# Verify tasks are open
OPEN_COUNT=$(bd list --status=open --json 2>/dev/null | jq '[.[] | select(.issue_type != "epic")] | length' || echo "0")
echo "  Open tasks before start: $OPEN_COUNT"
if [ "$OPEN_COUNT" -ge 1 ]; then pass "tasks available to work on"; else fail "no open tasks"; exit 1; fi

# Clean up and start daemon
rm -f .loom/logs/daemon-stdout.log worktrees/falcon/.agent.lock worktrees/nova/.agent.lock
loom daemon > .loom/logs/daemon-stdout.log 2>&1 &
sleep 5

STATUS=$(loom daemon status 2>&1)
if echo "$STATUS" | grep -q "running"; then pass "daemon started"; else fail "daemon did not start"; exit 1; fi

# --- Poll for task completion ---
echo ""
echo "--- Waiting for agents to complete tasks (polling every 60s) ---"

DEADLINE=$((SECONDS + MAX_WAIT * 60))
TASKS_CLOSED=0
LAST_STATUS=""

while [ $SECONDS -lt $DEADLINE ]; do
  sleep 60

  CLOSED=$(bd list --status=closed --json 2>/dev/null | jq '[.[] | select(.issue_type != "epic")] | length' 2>/dev/null || echo "0")
  IN_PROGRESS=$(bd list --status=in_progress 2>/dev/null | grep -c "task" || echo "0")
  REVIEW=$(bd list --status=review 2>/dev/null | grep -c "task" || echo "0")

  CURRENT_STATUS="closed=$CLOSED in_progress=$IN_PROGRESS review=$REVIEW"
  if [ "$CURRENT_STATUS" != "$LAST_STATUS" ]; then
    echo "  [$(date +%H:%M:%S)] $CURRENT_STATUS"
    LAST_STATUS="$CURRENT_STATUS"
  fi

  TASKS_CLOSED=$CLOSED

  if ! loom daemon status 2>&1 | grep -q "running"; then
    echo "  Daemon exited (all work may be done or max retries reached)"
    break
  fi

  if [ "$TASKS_CLOSED" -ge "$OPEN_COUNT" ]; then
    echo "  All tasks closed!"
    break
  fi
done

# Stop daemon
loom daemon stop 2>/dev/null || true
sleep 2

# --- Verify results ---
echo ""
echo "--- Verification ---"

CLOSED_TASKS=$(bd list --status=closed --json 2>/dev/null | jq -r '.[] | select(.issue_type != "epic") | .id' 2>/dev/null || echo "")
CLOSED_COUNT=$(echo "$CLOSED_TASKS" | grep -c . || echo "0")

if [ "$CLOSED_COUNT" -ge 1 ]; then pass "$CLOSED_COUNT task(s) closed by agents"; else fail "no tasks were closed"; fi

# Test: Commits exist in worktrees
echo "--- Commits ---"
for wt in falcon nova; do
  COMMIT_COUNT=$(cd "worktrees/$wt" && git log --oneline --all | grep -cv "initial commit\|WIP:\|Add Python" || echo "0")
  if [ "$COMMIT_COUNT" -gt 0 ]; then
    pass "$wt has $COMMIT_COUNT agent commit(s)"
    cd "worktrees/$wt" && git log --oneline --all | grep -v "initial commit\|WIP:" | head -5 | while read -r line; do
      echo "    $line"
    done
    cd "$TEST_DIR"
  else
    echo "  INFO: $wt has no agent commits (may be expected if it was the plan agent)"
  fi
done

# Test: Lock files were acquired and released
echo "--- Lock cleanup ---"
if [ ! -f worktrees/falcon/.agent.lock ]; then pass "falcon lock released"; else fail "falcon lock still exists"; fi
if [ ! -f worktrees/nova/.agent.lock ]; then pass "nova lock released"; else fail "nova lock still exists"; fi

# Test: Log files exist and have content
echo "--- Logs ---"
for role in plan task; do
  for wt in falcon nova; do
    LOG=".loom/logs/${role}-${wt}.log"
    if [ -f "$LOG" ] && [ -s "$LOG" ]; then
      pass "$LOG has content ($(wc -l < "$LOG") lines)"
    fi
  done
done

echo ""
echo "=== Agent Execution Results: $PASS passed, $FAIL failed ==="
if [ "$FAIL" -eq 0 ]; then exit 0; else exit 1; fi
