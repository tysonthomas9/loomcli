#!/usr/bin/env bash
# test-epic-assignment.sh — Verify that `bd ready --parent` correctly scopes
# task discovery to a single epic's children, matching the daemon's use of
# --parent-id filtering in automode task discovery.
#
# Prereqs: source test-env.sh (created by setup.sh)

set -euo pipefail

: "${TEST_DIR:?Run setup.sh first and source test-env.sh}"
: "${EPIC_A:?Run setup.sh first and source test-env.sh}"
: "${EPIC_B:?Run setup.sh first and source test-env.sh}"
: "${TASK_A1:?Run setup.sh first and source test-env.sh}"
: "${TASK_B1:?Run setup.sh first and source test-env.sh}"

cd "$TEST_DIR"

PASSED=0
FAILED=0
pass() { echo "  PASS: $1"; PASSED=$((PASSED + 1)); }
fail() { echo "  FAIL: $1"; FAILED=$((FAILED + 1)); }

echo "=== Test: Epic-scoped task discovery ==="

echo ""
echo "--- 1. bd ready --limit 0 (all tasks) ---"
TOTAL=$(bd ready --limit 0 --json 2>/dev/null | jq length)
echo "  Total ready tasks: $TOTAL"
[ "$TOTAL" -ge 3 ] && pass "At least 3 ready tasks (2 epic + 1 standalone)" \
                    || fail "Expected >= 3 ready tasks, got $TOTAL"

echo ""
echo "--- 2. bd ready --parent $EPIC_A (should show only Epic A tasks) ---"
COUNT_A=$(bd ready --parent "$EPIC_A" --limit 0 --json 2>/dev/null | jq length)
echo "  Epic A ready tasks: $COUNT_A"
[ "$COUNT_A" -eq 2 ] && pass "Epic A has exactly 2 tasks" \
                      || fail "Expected 2 tasks for Epic A, got $COUNT_A"

echo ""
echo "--- 3. bd ready --parent $EPIC_B (should show only Epic B tasks) ---"
COUNT_B=$(bd ready --parent "$EPIC_B" --limit 0 --json 2>/dev/null | jq length)
echo "  Epic B ready tasks: $COUNT_B"
[ "$COUNT_B" -eq 1 ] && pass "Epic B has exactly 1 task" \
                      || fail "Expected 1 task for Epic B, got $COUNT_B"

echo ""
echo "--- 4. Verify scoping: Epic A tasks not visible under Epic B ---"
CROSS=$(bd ready --parent "$EPIC_B" --limit 0 --json 2>/dev/null | jq -r ".[].id" | grep -c "$TASK_A1" || true)
[ "$CROSS" -eq 0 ] && pass "Epic A task not in Epic B scope" \
                    || fail "Epic A task leaked into Epic B scope"

echo ""
echo "--- 5. Close one Epic A task, verify count decreases ---"
bd close "$TASK_A1" -r "test: simulating completion" -q
REMAINING=$(bd ready --parent "$EPIC_A" --limit 0 --json 2>/dev/null | jq length)
echo "  After closing $TASK_A1, Epic A ready: $REMAINING"
[ "$REMAINING" -eq 1 ] && pass "Count decreased from 2 to 1" \
                        || fail "Expected 1 remaining, got $REMAINING"

# Reopen for other tests
bd update "$TASK_A1" --status=open -q
echo "  (Reopened $TASK_A1 for subsequent tests)"

echo ""
echo "--- 6. Verify --parent with non-existent epic returns empty ---"
EMPTY=$(bd ready --parent "fake-epic-999" --limit 0 --json 2>/dev/null | jq length)
[ "$EMPTY" -eq 0 ] && pass "Non-existent epic returns 0 tasks" \
                    || fail "Expected 0 for fake epic, got $EMPTY"

echo ""
echo "=== Epic assignment: $PASSED passed, $FAILED failed ==="
[ "$FAILED" -eq 0 ] || exit 1
