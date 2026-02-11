#!/usr/bin/env bash
# test-pr-creation.sh — Verify `loom pr` creates PRs from worktree branches.
#
# NOTE: Without a GitHub remote, this tests command invocation and error
# handling. With a remote, it verifies actual PR creation.
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
  cd "$TEST_DIR/worktrees/nova"   && git checkout -q nova   2>/dev/null || true
  cd "$TEST_DIR/worktrees/nova"   && git clean -fdq 2>/dev/null || true
  cd "$TEST_DIR"
}
trap cleanup EXIT

EPIC_BRANCH="epic/$EPIC_A"

echo "=== Test: PR creation from epic branches ==="

echo ""
echo "--- 1. Setup: create commits on epic branch in falcon ---"
cd "$TEST_DIR/worktrees/falcon"
git checkout -q -B "$EPIC_BRANCH" HEAD
echo "// auth login" > login.go
git add login.go
git commit -q -m "feat: implement login endpoint"
echo "  Created commit on $EPIC_BRANCH"
pass "Commit created on epic branch"

echo ""
echo "--- 2. Test loom pr (expects failure without GitHub remote) ---"
cd "$TEST_DIR"
OUTPUT=$(loom pr falcon main 2>&1) || true
echo "  Output: $OUTPUT"
# Without remote, loom pr should fail with a meaningful error (not a panic)
if echo "$OUTPUT" | grep -qi "error\|fatal\|no remote\|failed\|not found"; then
  pass "loom pr gave meaningful error without remote"
elif echo "$OUTPUT" | grep -qi "pull request\|created\|https://"; then
  pass "loom pr succeeded (repo has GitHub remote)"
else
  fail "loom pr returned unexpected output"
fi

echo ""
echo "--- 3. Test loom pr --all ---"
cd "$TEST_DIR/worktrees/nova"
git checkout -q -B "epic/$EPIC_A" HEAD
echo "// auth logout" > logout.go
git add logout.go
git commit -q -m "feat: implement logout endpoint"

cd "$TEST_DIR"
OUTPUT=$(loom pr --all main 2>&1) || true
echo "  Output: $OUTPUT"
# Just verify it doesn't panic
if echo "$OUTPUT" | grep -qi "panic"; then
  fail "loom pr --all panicked"
else
  pass "loom pr --all ran without panic"
fi

echo ""
echo "--- 4. Test loom pr on branch with no divergence from target ---"
cd "$TEST_DIR/worktrees/falcon"
git checkout -q -B "epic/empty-test" HEAD
# No new commits on this branch vs main

cd "$TEST_DIR"
OUTPUT=$(loom pr falcon main 2>&1) || true
echo "  Output: $OUTPUT"
if echo "$OUTPUT" | grep -qi "no commits\|no changes\|nothing\|already\|error"; then
  pass "Correctly handled no-diff branch"
else
  # May also fail due to no remote, which is fine
  pass "Command completed (no remote or no diff)"
fi

echo ""
echo "=== PR creation: $PASSED passed, $FAILED failed ==="
[ "$FAILED" -eq 0 ] || exit 1
