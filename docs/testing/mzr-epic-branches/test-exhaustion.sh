#!/usr/bin/env bash
# test-exhaustion.sh — Verify epic exhaustion detection.
# When all tasks in an epic are closed, bd ready --parent returns empty,
# signaling the daemon to reassign the worktree to another epic or fall
# back to non-epic tasks.
#
# Prereqs: source test-env.sh (created by setup.sh)

set -euo pipefail

: "${TEST_DIR:?Run setup.sh first and source test-env.sh}"
: "${EPIC_A:?Run setup.sh first and source test-env.sh}"
: "${EPIC_B:?Run setup.sh first and source test-env.sh}"
: "${TASK_A1:?Run setup.sh first and source test-env.sh}"
: "${TASK_A2:?Run setup.sh first and source test-env.sh}"
: "${TASK_B1:?Run setup.sh first and source test-env.sh}"
: "${TASK_S:?Run setup.sh first and source test-env.sh}"

cd "$TEST_DIR"

PASSED=0
FAILED=0
pass() { echo "  PASS: $1"; PASSED=$((PASSED + 1)); }
fail() { echo "  FAIL: $1"; FAILED=$((FAILED + 1)); }

# Always reopen tasks on exit so other tests aren't affected
cleanup() {
  bd update "$TASK_A1" --status=open -q 2>/dev/null || true
  bd update "$TASK_A2" --status=open -q 2>/dev/null || true
  bd update "$TASK_B1" --status=open -q 2>/dev/null || true
}
trap cleanup EXIT

echo "=== Test: Epic exhaustion detection ==="

echo ""
echo "--- 1. Baseline: both epics have ready tasks ---"
COUNT_A=$(bd ready --parent "$EPIC_A" --limit 0 --json 2>/dev/null | jq length)
COUNT_B=$(bd ready --parent "$EPIC_B" --limit 0 --json 2>/dev/null | jq length)
echo "  Epic A ready: $COUNT_A"
echo "  Epic B ready: $COUNT_B"
[ "$COUNT_A" -eq 2 ] && pass "Epic A has 2 tasks" || fail "Expected 2 for Epic A, got $COUNT_A"
[ "$COUNT_B" -eq 1 ] && pass "Epic B has 1 task"  || fail "Expected 1 for Epic B, got $COUNT_B"

echo ""
echo "--- 2. Close all Epic B tasks (simulate exhaustion) ---"
bd close "$TASK_B1" -r "test: simulating agent completion" -q
COUNT_B_AFTER=$(bd ready --parent "$EPIC_B" --limit 0 --json 2>/dev/null | jq length)
echo "  Epic B ready after close: $COUNT_B_AFTER"
[ "$COUNT_B_AFTER" -eq 0 ] && pass "Epic B exhausted (0 tasks)" \
                             || fail "Expected 0, got $COUNT_B_AFTER"

echo ""
echo "--- 3. Verify Epic A still has work (reassignment target) ---"
COUNT_A_STILL=$(bd ready --parent "$EPIC_A" --limit 0 --json 2>/dev/null | jq length)
echo "  Epic A ready: $COUNT_A_STILL"
[ "$COUNT_A_STILL" -eq 2 ] && pass "Epic A still has 2 tasks" \
                             || fail "Expected 2, got $COUNT_A_STILL"

echo ""
echo "--- 4. Close all Epic A tasks too ---"
bd close "$TASK_A1" "$TASK_A2" -r "test: simulating completion" -q
COUNT_A_FINAL=$(bd ready --parent "$EPIC_A" --limit 0 --json 2>/dev/null | jq length)
echo "  Epic A ready: $COUNT_A_FINAL"
[ "$COUNT_A_FINAL" -eq 0 ] && pass "Epic A exhausted (0 tasks)" \
                             || fail "Expected 0, got $COUNT_A_FINAL"

echo ""
echo "--- 5. All epics exhausted: standalone tasks still available ---"
# The daemon falls back to non-epic tasks when all epics are exhausted
TOTAL_READY=$(bd ready --limit 0 --json 2>/dev/null | jq length)
echo "  Total ready (should be standalone only): $TOTAL_READY"
[ "$TOTAL_READY" -ge 1 ] && pass "Standalone task $TASK_S available for fallback" \
                           || fail "Expected >= 1 standalone task, got $TOTAL_READY"

echo ""
echo "--- 6. Verify standalone task is the one remaining ---"
REMAINING_ID=$(bd ready --limit 0 --json 2>/dev/null | jq -r '.[0].id')
echo "  Remaining task: $REMAINING_ID"
[ "$REMAINING_ID" = "$TASK_S" ] && pass "Correct standalone task remains" \
                                 || fail "Expected $TASK_S, got $REMAINING_ID"

echo ""
echo "=== Exhaustion detection: $PASSED passed, $FAILED failed ==="
[ "$FAILED" -eq 0 ] || exit 1
