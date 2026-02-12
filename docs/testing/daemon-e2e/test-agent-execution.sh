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
# Use setsid to isolate the daemon in its own process group, preventing
# stray signals from the test script's process group from killing it.
rm -f .loom/logs/daemon-stdout.log worktrees/falcon/.agent.lock worktrees/nova/.agent.lock
loom daemon > .loom/logs/daemon-stdout.log 2>&1 &
DAEMON_BG_PID=$!
disown $DAEMON_BG_PID 2>/dev/null || true
sleep 5

STATUS=$(loom daemon status 2>&1 || true)
if echo "$STATUS" | grep -q "running"; then pass "daemon started"; else fail "daemon did not start: $STATUS"; exit 1; fi

# --- Poll for task completion ---
echo ""
echo "--- Waiting for agents to complete tasks (polling every 60s) ---"

DEADLINE=$((SECONDS + MAX_WAIT * 60))
TASKS_CLOSED=0
LAST_STATUS=""

while [ $SECONDS -lt $DEADLINE ]; do
  sleep 60

  CLOSED=$(bd list --status=closed --json 2>/dev/null | jq '[.[] | select(.issue_type != "epic")] | length' 2>/dev/null || echo "0")
  IN_PROGRESS=$(bd list --status=in_progress 2>/dev/null | grep -c "task" || true)
  REVIEW=$(bd list --status=review 2>/dev/null | grep -c "task" || true)

  CURRENT_STATUS="closed=$CLOSED in_progress=$IN_PROGRESS review=$REVIEW"
  if [ "$CURRENT_STATUS" != "$LAST_STATUS" ]; then
    echo "  [$(date +%H:%M:%S)] $CURRENT_STATUS"
    LAST_STATUS="$CURRENT_STATUS"
  fi

  TASKS_CLOSED=$CLOSED

  DAEMON_STATUS=$(loom daemon status 2>&1 || true)
  if ! echo "$DAEMON_STATUS" | grep -q "running"; then
    echo "  Daemon exited (all work may be done or max retries reached)"
    echo "  Status output: $DAEMON_STATUS"
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
CLOSED_COUNT=$(echo "$CLOSED_TASKS" | grep -c . || true)

if [ "$CLOSED_COUNT" -ge 1 ]; then pass "$CLOSED_COUNT task(s) closed by agents"; else fail "no tasks were closed"; fi

# Test: Commits exist in worktrees
echo "--- Commits ---"
for wt in falcon nova; do
  COMMIT_COUNT=$(cd "worktrees/$wt" && git log --oneline --all | grep -cv "initial commit\|WIP:\|Add Python" || true)
  if [ "$COMMIT_COUNT" -gt 0 ]; then
    pass "$wt has $COMMIT_COUNT agent commit(s)"
    (cd "worktrees/$wt" && git log --oneline --all | grep -v "initial commit\|WIP:" | head -5 | while read -r line; do
      echo "    $line"
    done)
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

# --- Functional Verification ---
# Check whether agent output actually satisfies the task descriptions.
# We check all worktrees to find where the code was implemented.
echo ""
echo "--- Functional Verification ---"

# Find a worktree that has the power() function (the implementing agent's work)
CALC_DIR=""
UTILS_DIR=""
for wt in falcon nova; do
  if grep -q "def power" "worktrees/$wt/src/calculator.py" 2>/dev/null; then
    CALC_DIR="worktrees/$wt"
  fi
  if grep -q "def snake_case" "worktrees/$wt/src/utils.py" 2>/dev/null; then
    UTILS_DIR="worktrees/$wt"
  fi
done

# Also check epic branches in worktrees (agent may have switched branches)
if [ -z "$CALC_DIR" ] || [ -z "$UTILS_DIR" ]; then
  for wt in falcon nova; do
    for branch in $(cd "worktrees/$wt" && git branch --all 2>/dev/null | grep "epic/" | sed 's/^[* ]*//' || true); do
      (cd "worktrees/$wt" && git checkout -q "$branch" 2>/dev/null) || continue
      if [ -z "$CALC_DIR" ] && grep -q "def power" "worktrees/$wt/src/calculator.py" 2>/dev/null; then
        CALC_DIR="worktrees/$wt"
      fi
      if [ -z "$UTILS_DIR" ] && grep -q "def snake_case" "worktrees/$wt/src/utils.py" 2>/dev/null; then
        UTILS_DIR="worktrees/$wt"
      fi
    done
  done
fi

echo "  Calculator code found in: ${CALC_DIR:-NONE}"
echo "  Utils code found in: ${UTILS_DIR:-NONE}"

# --- Task A1: power() function ---
echo "--- A1: power(base, exp) function ---"
if [ -n "$CALC_DIR" ]; then
  grep -q "def power" "$CALC_DIR/src/calculator.py" && pass "power() function exists" || fail "power() function missing"
  grep -A2 "def power" "$CALC_DIR/src/calculator.py" | grep -q '"""' && pass "power() has docstring" || fail "power() missing docstring"

  (cd "$CALC_DIR" && python3 -c "
from src.calculator import power
assert power(2,3) == 8, 'power(2,3) != 8'
assert power(5,0) == 1, 'power(5,0) != 1'
try:
    power(0,-1)
    assert False, 'power(0,-1) should raise ValueError'
except ValueError:
    pass
print('  power() functional checks passed')
" 2>&1) && pass "power() functional tests" || fail "power() functional tests"
else
  fail "power() function not found in any worktree"
fi

# --- Task B1: snake_case() function ---
echo "--- B1: snake_case(text) function ---"
if [ -n "$UTILS_DIR" ]; then
  grep -q "def snake_case" "$UTILS_DIR/src/utils.py" && pass "snake_case() function exists" || fail "snake_case() function missing"
  grep -A2 "def snake_case" "$UTILS_DIR/src/utils.py" | grep -q '"""' && pass "snake_case() has docstring" || fail "snake_case() missing docstring"

  (cd "$UTILS_DIR" && python3 -c "
from src.utils import snake_case
assert snake_case('camelCase') == 'camel_case', f\"camelCase: {snake_case('camelCase')}\"
assert snake_case('PascalCase') == 'pascal_case', f\"PascalCase: {snake_case('PascalCase')}\"
assert snake_case('HTMLParser') == 'html_parser', f\"HTMLParser: {snake_case('HTMLParser')}\"
assert snake_case('hello world') == 'hello_world', f\"hello world: {snake_case('hello world')}\"
assert snake_case('') == '', f\"empty: {snake_case('')}\"
print('  snake_case() functional checks passed')
" 2>&1) && pass "snake_case() functional tests" || fail "snake_case() functional tests"
else
  fail "snake_case() function not found in any worktree"
fi

# --- Task A2: Calculator tests ---
echo "--- A2: Calculator unit tests ---"
# Test file could be in whichever worktree implemented it (may differ from CALC_DIR)
TEST_CALC_DIR=""
for wt in falcon nova; do
  if [ -f "worktrees/$wt/tests/test_calculator.py" ]; then
    TEST_CALC_DIR="worktrees/$wt"
    break
  fi
done
if [ -n "$TEST_CALC_DIR" ]; then
  pass "tests/test_calculator.py exists (in $TEST_CALC_DIR)"
  TEST_COUNT=$(grep -c "def test_" "$TEST_CALC_DIR/tests/test_calculator.py" || echo "0")
  if [ "$TEST_COUNT" -ge 20 ]; then
    pass "test_calculator has $TEST_COUNT tests (>= 20)"
  else
    fail "test_calculator has only $TEST_COUNT tests (need 20)"
  fi
  # Need power() to be in the same worktree for tests to pass
  if grep -q "def power" "$TEST_CALC_DIR/src/calculator.py" 2>/dev/null; then
    (cd "$TEST_CALC_DIR" && python3 -m pytest tests/test_calculator.py -q 2>&1) && pass "test_calculator.py all tests pass" || fail "test_calculator.py has failing tests"
  else
    echo "  INFO: power() not in same worktree as tests — skipping pytest run"
  fi
else
  fail "tests/test_calculator.py missing from all worktrees"
fi

# --- Task B2: Utils tests ---
echo "--- B2: Utils unit tests ---"
TEST_UTILS_DIR=""
for wt in falcon nova; do
  if [ -f "worktrees/$wt/tests/test_utils.py" ]; then
    TEST_UTILS_DIR="worktrees/$wt"
    break
  fi
done
if [ -n "$TEST_UTILS_DIR" ]; then
  pass "tests/test_utils.py exists (in $TEST_UTILS_DIR)"
  TEST_COUNT=$(grep -c "def test_" "$TEST_UTILS_DIR/tests/test_utils.py" || echo "0")
  if [ "$TEST_COUNT" -ge 15 ]; then
    pass "test_utils has $TEST_COUNT tests (>= 15)"
  else
    fail "test_utils has only $TEST_COUNT tests (need 15)"
  fi
  # Need snake_case() to be in the same worktree for tests to pass
  if grep -q "def snake_case" "$TEST_UTILS_DIR/src/utils.py" 2>/dev/null; then
    (cd "$TEST_UTILS_DIR" && python3 -m pytest tests/test_utils.py -q 2>&1) && pass "test_utils.py all tests pass" || fail "test_utils.py has failing tests"
  else
    echo "  INFO: snake_case() not in same worktree as tests — skipping pytest run"
  fi
else
  fail "tests/test_utils.py missing from all worktrees"
fi

echo ""
echo "=== Agent Execution Results: $PASS passed, $FAIL failed ==="
if [ "$FAIL" -eq 0 ]; then exit 0; else exit 1; fi
