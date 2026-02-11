#!/usr/bin/env bash
# test-daemon-e2e.sh — End-to-end daemon integration test.
#
# Verifies the full daemon pipeline:
#   Phase 1: Planning agents pick up all tasks and create designs (status=review)
#   Phase 2: Human review approves all plans (status=open)
#   Phase 3: Task agents implement all approved designs (status=closed)
#
# This test invokes real Claude agents and takes several minutes to complete.
#
# Prereqs: source test-env.sh (created by setup.sh)

set -euo pipefail

: "${TEST_DIR:?Run setup.sh first and source test-env.sh}"
: "${TASK_A1:?Run setup.sh first and source test-env.sh}"
: "${TASK_A2:?Run setup.sh first and source test-env.sh}"
: "${TASK_B1:?Run setup.sh first and source test-env.sh}"
: "${TASK_S:?Run setup.sh first and source test-env.sh}"

cd "$TEST_DIR"

ALL_TASKS=("$TASK_A1" "$TASK_A2" "$TASK_B1" "$TASK_S")
TASK_COUNT=${#ALL_TASKS[@]}

PASSED=0
FAILED=0
pass() { echo "  PASS: $1"; PASSED=$((PASSED + 1)); }
fail() { echo "  FAIL: $1"; FAILED=$((FAILED + 1)); }

# Always stop daemon on exit
cleanup() {
  loom daemon stop 2>/dev/null || true
  if [ -f ".loom/daemon.pid" ]; then
    PID=$(cat .loom/daemon.pid 2>/dev/null || true)
    [ -n "$PID" ] && kill "$PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

echo "=== Test: Daemon E2E (plan → review → implement) ==="
echo "  Tasks: ${ALL_TASKS[*]}"
echo ""

# ─────────────────────────────────────────────────
# Phase 1: Planning
# ─────────────────────────────────────────────────
echo "--- Phase 1: Planning (waiting for all $TASK_COUNT tasks to reach status=review) ---"
cp loom-plan.yaml loom.yaml
loom daemon > /dev/null 2>&1 &
DAEMON_PID=$!

# Wait for daemon PID file
for i in $(seq 1 20); do
  sleep 0.5
  [ -f ".loom/daemon.pid" ] && break
done
if [ -f ".loom/daemon.pid" ]; then
  echo "  Daemon started (PID $(cat .loom/daemon.pid))"
else
  fail "Daemon did not start"
  echo "=== Daemon E2E: $PASSED passed, $FAILED failed ==="
  exit 1
fi

# Poll until all tasks reach status=review (timeout: 5 minutes, overridable)
PLAN_TIMEOUT=${PLAN_TIMEOUT:-300}
PLAN_START=$SECONDS
REVIEW_COUNT=0
while [ $((SECONDS - PLAN_START)) -lt $PLAN_TIMEOUT ]; do
  REVIEW_COUNT=$(bd list --status=review --json 2>/dev/null | jq '[.[] | select(.issue_type != "epic")] | length' 2>/dev/null || echo 0)
  echo "  [$((SECONDS - PLAN_START))s] Tasks in review: $REVIEW_COUNT / $TASK_COUNT"
  if [ "$REVIEW_COUNT" -ge "$TASK_COUNT" ]; then
    break
  fi
  sleep 10
done

loom daemon stop 2>/dev/null || true
wait "$DAEMON_PID" 2>/dev/null || true
sleep 1

if [ "$REVIEW_COUNT" -ge "$TASK_COUNT" ]; then
  pass "All $TASK_COUNT tasks reached status=review ($((SECONDS - PLAN_START))s)"
else
  fail "Only $REVIEW_COUNT / $TASK_COUNT tasks reached review within ${PLAN_TIMEOUT}s"
fi

# Verify each task has a non-empty design
DESIGNS_OK=0
for TASK in "${ALL_TASKS[@]}"; do
  DESIGN=$(bd show "$TASK" --json 2>/dev/null | jq -r '.[0].design // empty' 2>/dev/null || true)
  if [ -n "$DESIGN" ]; then
    DESIGNS_OK=$((DESIGNS_OK + 1))
    echo "  $TASK: design present (${#DESIGN} chars)"
  else
    echo "  $TASK: NO DESIGN"
  fi
done
if [ "$DESIGNS_OK" -eq "$TASK_COUNT" ]; then
  pass "All $TASK_COUNT tasks have non-empty designs"
else
  fail "Only $DESIGNS_OK / $TASK_COUNT tasks have designs"
fi

# Check daemon logs for panics
if grep -rqi "panic" .loom/logs/ 2>/dev/null; then
  fail "Panic found in planning daemon logs"
else
  pass "No panics in planning daemon logs"
fi

echo ""

# ─────────────────────────────────────────────────
# Phase 2: Review (approve all plans)
# ─────────────────────────────────────────────────
echo "--- Phase 2: Review (approving all plans) ---"
APPROVED=0
for TASK in "${ALL_TASKS[@]}"; do
  bd update "$TASK" --status=open -q 2>/dev/null && APPROVED=$((APPROVED + 1))
done
if [ "$APPROVED" -eq "$TASK_COUNT" ]; then
  pass "All $TASK_COUNT plans approved (status=open)"
else
  fail "Only approved $APPROVED / $TASK_COUNT plans"
fi

# Verify tasks are ready for implementation (have designs, status=open)
IMPL_READY=$(bd ready --limit 0 --json 2>/dev/null | jq '[.[] | select(.issue_type != "epic") | select(.design != null and .design != "")] | length' 2>/dev/null || echo 0)
echo "  Tasks ready for implementation: $IMPL_READY"
if [ "$IMPL_READY" -ge "$TASK_COUNT" ]; then
  pass "All $TASK_COUNT tasks ready for implementation"
else
  fail "Only $IMPL_READY / $TASK_COUNT tasks ready for implementation"
fi

echo ""

# ─────────────────────────────────────────────────
# Phase 3: Implementation
# ─────────────────────────────────────────────────
echo "--- Phase 3: Implementation (waiting for all $TASK_COUNT tasks to close) ---"
# Clear logs from planning phase
rm -f .loom/logs/*.log

cp loom-task.yaml loom.yaml
loom daemon > /dev/null 2>&1 &
DAEMON_PID=$!

# Wait for daemon PID file
for i in $(seq 1 20); do
  sleep 0.5
  [ -f ".loom/daemon.pid" ] && break
done
if [ -f ".loom/daemon.pid" ]; then
  echo "  Daemon started (PID $(cat .loom/daemon.pid))"
else
  fail "Daemon did not start for implementation phase"
  echo "=== Daemon E2E: $PASSED passed, $FAILED failed ==="
  exit 1
fi

# Poll until all tasks are closed (timeout: 15 minutes, overridable)
IMPL_TIMEOUT=${IMPL_TIMEOUT:-900}
IMPL_START=$SECONDS
CLOSED_COUNT=0
while [ $((SECONDS - IMPL_START)) -lt $IMPL_TIMEOUT ]; do
  CLOSED_COUNT=0
  for TASK in "${ALL_TASKS[@]}"; do
    STATUS=$(bd show "$TASK" --json 2>/dev/null | jq -r '.[0].status // empty' 2>/dev/null || true)
    if [ "$STATUS" = "closed" ]; then
      CLOSED_COUNT=$((CLOSED_COUNT + 1))
    fi
  done
  echo "  [$((SECONDS - IMPL_START))s] Tasks closed: $CLOSED_COUNT / $TASK_COUNT"
  if [ "$CLOSED_COUNT" -ge "$TASK_COUNT" ]; then
    break
  fi
  sleep 10
done

loom daemon stop 2>/dev/null || true
wait "$DAEMON_PID" 2>/dev/null || true
sleep 1

if [ "$CLOSED_COUNT" -ge "$TASK_COUNT" ]; then
  pass "All $TASK_COUNT tasks closed ($((SECONDS - IMPL_START))s)"
else
  # Report which tasks didn't close
  for TASK in "${ALL_TASKS[@]}"; do
    STATUS=$(bd show "$TASK" --json 2>/dev/null | jq -r '.[0].status // empty' 2>/dev/null || true)
    echo "  $TASK: status=$STATUS"
  done
  fail "Only $CLOSED_COUNT / $TASK_COUNT tasks closed within ${IMPL_TIMEOUT}s"
fi

# Check daemon logs for panics
if grep -rqi "panic" .loom/logs/ 2>/dev/null; then
  fail "Panic found in implementation daemon logs"
else
  pass "No panics in implementation daemon logs"
fi

echo ""
echo "=== Daemon E2E: $PASSED passed, $FAILED failed ==="
[ "$FAILED" -eq 0 ] || exit 1
