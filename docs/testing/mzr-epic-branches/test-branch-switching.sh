#!/usr/bin/env bash
# test-branch-switching.sh — Verify epic branch creation and switching.
# Tests the naming convention (epic/<id>), non-epic fallback (agent name),
# and WIP commit behavior when switching branches with dirty state.
#
# Prereqs: source test-env.sh (created by setup.sh)

set -euo pipefail

: "${TEST_DIR:?Run setup.sh first and source test-env.sh}"
: "${EPIC_A:?Run setup.sh first and source test-env.sh}"
: "${EPIC_B:?Run setup.sh first and source test-env.sh}"

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

EXPECTED_BRANCH_A="epic/$EPIC_A"
EXPECTED_BRANCH_B="epic/$EPIC_B"

echo "=== Test: Epic branch creation and switching ==="

echo ""
echo "--- 1. Create epic branch for falcon (Epic A) ---"
cd "$TEST_DIR/worktrees/falcon"
git checkout -q -B "$EXPECTED_BRANCH_A" HEAD
ACTUAL=$(git branch --show-current)
echo "  Branch: $ACTUAL"
[ "$ACTUAL" = "$EXPECTED_BRANCH_A" ] && pass "Epic A branch created" \
                                      || fail "Expected $EXPECTED_BRANCH_A, got $ACTUAL"

echo ""
echo "--- 2. Create epic branch for nova (Epic B) ---"
cd "$TEST_DIR/worktrees/nova"
git checkout -q -B "$EXPECTED_BRANCH_B" HEAD
ACTUAL=$(git branch --show-current)
echo "  Branch: $ACTUAL"
[ "$ACTUAL" = "$EXPECTED_BRANCH_B" ] && pass "Epic B branch created" \
                                      || fail "Expected $EXPECTED_BRANCH_B, got $ACTUAL"

echo ""
echo "--- 3. Simulate epic exhaustion: switch falcon from Epic A to Epic B ---"
cd "$TEST_DIR/worktrees/falcon"
git checkout -q -B "$EXPECTED_BRANCH_B" HEAD
ACTUAL=$(git branch --show-current)
echo "  Branch after reassignment: $ACTUAL"
[ "$ACTUAL" = "$EXPECTED_BRANCH_B" ] && pass "Reassignment to Epic B" \
                                      || fail "Expected $EXPECTED_BRANCH_B, got $ACTUAL"

echo ""
echo "--- 4. Non-epic fallback: switch to agent-name branch ---"
cd "$TEST_DIR/worktrees/falcon"
git checkout -q -B falcon HEAD
ACTUAL=$(git branch --show-current)
echo "  Fallback branch: $ACTUAL"
[ "$ACTUAL" = "falcon" ] && pass "Non-epic fallback to 'falcon'" \
                          || fail "Expected 'falcon', got $ACTUAL"

echo ""
echo "--- 5. WIP commit before branch switch (dirty worktree) ---"
# The daemon calls commitWIP (git add -A && git commit -m "WIP: ...") before switching
cd "$TEST_DIR/worktrees/falcon"
echo "in-progress work" > wip-file.txt
git add wip-file.txt
echo "  Created staged change on falcon branch"

# Simulate what EnsureWorktreeBranch does: WIP commit then switch
git commit -q -m "WIP: daemon branch switch"
WIP_COMMITTED=$?
git checkout -q -B "$EXPECTED_BRANCH_A" HEAD
ACTUAL=$(git branch --show-current)

if [ $WIP_COMMITTED -eq 0 ] && [ "$ACTUAL" = "$EXPECTED_BRANCH_A" ]; then
  pass "WIP commit + branch switch succeeded"
else
  fail "WIP commit or branch switch failed (commit=$WIP_COMMITTED, branch=$ACTUAL)"
fi

echo ""
echo "--- 6. Branch switch with unstaged changes (stash fallback) ---"
cd "$TEST_DIR/worktrees/falcon"
git checkout -q -B falcon HEAD
echo "unstaged work" > unstaged.txt
# Don't git add — this is unstaged
echo "  Created unstaged change on falcon branch"

# Simulate the stash fallback path when WIP commit fails (nothing staged)
if git add -A && git commit -q -m "WIP: daemon branch switch" 2>/dev/null; then
  git checkout -q -B "$EXPECTED_BRANCH_B" HEAD
  pass "git add -A + WIP commit handled unstaged changes"
elif git stash -q 2>/dev/null; then
  git checkout -q -B "$EXPECTED_BRANCH_B" HEAD
  git stash pop -q 2>/dev/null || true
  pass "Stash fallback handled unstaged changes"
else
  fail "Neither WIP commit nor stash could handle dirty state"
fi

echo ""
echo "=== Branch switching: $PASSED passed, $FAILED failed ==="
[ "$FAILED" -eq 0 ] || exit 1
