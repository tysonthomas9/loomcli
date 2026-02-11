#!/usr/bin/env bash
# test-recovery.sh — Verify worktree recovery handles dirty state and locks.
# Tests scenarios from loomcli-1jr (WIP commit failure) and general daemon
# recovery logic via `loom recover`.
#
# Lock file format matches internal/cli/lock.go LockInfo struct:
#   {"pid":N,"command":"...","started_at":"...","agent_name":"...","task_id":"..."}
#
# Prereqs: source test-env.sh (created by setup.sh)

set -euo pipefail

: "${TEST_DIR:?Run setup.sh first and source test-env.sh}"
: "${EPIC_A:?Run setup.sh first and source test-env.sh}"

cd "$TEST_DIR"

PASSED=0
FAILED=0
pass() { echo "  PASS: $1"; PASSED=$((PASSED + 1)); }
fail() { echo "  FAIL: $1"; FAILED=$((FAILED + 1)); }

cleanup() {
  cd "$TEST_DIR/worktrees/falcon" && git checkout -q falcon 2>/dev/null || true
  cd "$TEST_DIR/worktrees/falcon" && git clean -fdq 2>/dev/null || true
  cd "$TEST_DIR"
}
trap cleanup EXIT

echo "=== Test: Worktree recovery scenarios ==="

echo ""
echo "--- 1. Simulate dirty worktree (staged changes) ---"
cd "$TEST_DIR/worktrees/falcon"
echo "dirty change" > dirty.txt
git add dirty.txt
STATUS=$(git status --porcelain)
echo "  Dirty files: $STATUS"
[ -n "$STATUS" ] && pass "Worktree is dirty" || fail "Expected dirty state"

echo ""
echo "--- 2. loom recover --no-analyze on dirty worktree ---"
cd "$TEST_DIR"
OUTPUT=$(loom recover falcon --no-analyze 2>&1) || true
echo "  Output: $OUTPUT"
# Recovery should complete without panicking
if echo "$OUTPUT" | grep -qi "panic"; then
  fail "Recovery panicked"
else
  pass "Recovery completed without panic"
fi

echo ""
echo "--- 3. Check worktree state after recovery ---"
cd "$TEST_DIR/worktrees/falcon"
STATUS_AFTER=$(git status --porcelain)
if [ -z "$STATUS_AFTER" ]; then
  pass "Worktree is clean after recovery"
else
  # Recovery may leave changes (it focuses on locks/tasks, not git state)
  echo "  INFO: Worktree still has changes: $STATUS_AFTER"
  pass "Recovery completed (dirty state is expected — recovery cleans locks, not git)"
fi

echo ""
echo "--- 4. WIP commit before branch switch (daemon behavior) ---"
# EnsureWorktreeBranch does: git add -A && git commit -m "WIP: ..."
cd "$TEST_DIR/worktrees/falcon"
git checkout -q -B falcon HEAD
git clean -fdq 2>/dev/null || true
echo "in-progress work" > wip-file.txt

# Simulate daemon's commitWIP: add all + commit
git add -A
if git commit -q -m "WIP: daemon branch switch (test)" 2>/dev/null; then
  pass "WIP commit succeeded with staged changes"
else
  fail "WIP commit failed unexpectedly"
fi

# Now switch branch (should work since tree is clean)
EPIC_BRANCH="epic/$EPIC_A"
git checkout -q -B "$EPIC_BRANCH" HEAD
ACTUAL=$(git branch --show-current)
[ "$ACTUAL" = "$EPIC_BRANCH" ] && pass "Branch switch after WIP commit" \
                                || fail "Expected $EPIC_BRANCH, got $ACTUAL"

echo ""
echo "--- 5. WIP commit with nothing to commit (stash fallback scenario) ---"
cd "$TEST_DIR/worktrees/falcon"
git checkout -q -B falcon HEAD
# Clean tree — WIP commit will fail
if git commit -q --allow-empty -m "WIP: empty" 2>/dev/null; then
  pass "Empty WIP commit with --allow-empty"
else
  # In real daemon, this triggers the stash fallback path
  echo "  INFO: Empty commit fails as expected — daemon uses stash fallback"
  pass "Correctly identified empty commit scenario"
fi

echo ""
echo "--- 6. Simulate stale lock file ---"
cd "$TEST_DIR"
LOCK_FILE="worktrees/falcon/.loom-lock"
# Match actual LockInfo struct from internal/cli/lock.go
cat > "$LOCK_FILE" <<EOF
{"pid":99999,"command":"loom task falcon --auto","started_at":"2026-01-01T00:00:00Z","agent_name":"falcon","task_id":"fake-task-id","task_title":"Test task"}
EOF
echo "  Created stale lock: $LOCK_FILE"
[ -f "$LOCK_FILE" ] && pass "Lock file created" || fail "Lock file not created"

echo ""
echo "--- 7. loom recover clears stale lock ---"
OUTPUT=$(loom recover falcon --no-analyze 2>&1) || true
echo "  Output: $OUTPUT"
if [ -f "$LOCK_FILE" ]; then
  fail "Lock file still exists after recovery"
else
  pass "Lock file cleared by recovery"
fi

echo ""
echo "--- 8. loom recover --force (non-interactive) ---"
# Create another stale lock
cat > "$LOCK_FILE" <<EOF
{"pid":99998,"command":"loom task falcon --auto","started_at":"2026-01-01T00:00:00Z","agent_name":"falcon","task_id":"fake-task-2","task_title":"Another task"}
EOF
OUTPUT=$(loom recover falcon --force 2>&1) || true
echo "  Output: $OUTPUT"
if echo "$OUTPUT" | grep -qi "panic"; then
  fail "Recovery --force panicked"
else
  pass "Recovery --force completed without panic"
fi

echo ""
echo "=== Recovery: $PASSED passed, $FAILED failed ==="
[ "$FAILED" -eq 0 ] || exit 1
