#!/usr/bin/env bash
# test-epic-assignment.sh — Verify epic-to-worktree assignment, branch names, and no double-assignment.
#
# Prerequisites: Run setup.sh first, then source test-env.sh.
# Usage: ./test-epic-assignment.sh

set -uo pipefail

TEST_DIR="${TEST_DIR:-/tmp/loom-daemon-e2e}"
cd "$TEST_DIR"
source test-env.sh

PASS=0
FAIL=0

pass() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL + 1)); }

echo "=== Test: Epic Assignment ==="
echo ""

# Clean up and start daemon
rm -f .loom/logs/daemon-stdout.log worktrees/falcon/.agent.lock worktrees/nova/.agent.lock
loom daemon > .loom/logs/daemon-stdout.log 2>&1 &
sleep 8

# --- Test 1: Both agents running ---
echo "--- 1. Both agents running ---"
STATUS=$(loom daemon status 2>&1)
if echo "$STATUS" | grep -q "falcon"; then pass "falcon agent present"; else fail "falcon agent missing"; fi
if echo "$STATUS" | grep -q "nova"; then pass "nova agent present"; else fail "nova agent missing"; fi

# --- Test 2: Epic IDs assigned ---
echo "--- 2. Epic assignment ---"
STATE=$(cat .loom/daemon-agents.json 2>/dev/null || echo "{}")

FALCON_EPIC=$(echo "$STATE" | jq -r '.agents[] | select(.worktree=="falcon") | .epic_id // empty')
NOVA_EPIC=$(echo "$STATE" | jq -r '.agents[] | select(.worktree=="nova") | .epic_id // empty')

# At least one agent should have an epic assigned
if [ -n "$FALCON_EPIC" ] || [ -n "$NOVA_EPIC" ]; then
  pass "at least one agent assigned to an epic"
else
  fail "no agents assigned to epics"
fi

# --- Test 3: No double-assignment ---
echo "--- 3. No double-assignment ---"
if [ -n "$FALCON_EPIC" ] && [ -n "$NOVA_EPIC" ]; then
  if [ "$FALCON_EPIC" != "$NOVA_EPIC" ]; then
    pass "different epics assigned (falcon=$FALCON_EPIC, nova=$NOVA_EPIC)"
  else
    fail "same epic assigned to both agents: $FALCON_EPIC"
  fi
else
  pass "only one agent has an epic (other in non-epic mode)"
fi

# --- Test 4: Branch names ---
echo "--- 4. Branch names ---"
FALCON_BRANCH=$(cd worktrees/falcon && git branch --show-current)
NOVA_BRANCH=$(cd worktrees/nova && git branch --show-current)

echo "  falcon branch: $FALCON_BRANCH"
echo "  nova branch: $NOVA_BRANCH"

# If an agent has an epic, its branch should be epic/<id>
if [ -n "$FALCON_EPIC" ]; then
  EXPECTED="epic/$FALCON_EPIC"
  if [ "$FALCON_BRANCH" = "$EXPECTED" ]; then pass "falcon on correct epic branch ($EXPECTED)"; else fail "falcon branch mismatch: got $FALCON_BRANCH, expected $EXPECTED"; fi
fi
if [ -n "$NOVA_EPIC" ]; then
  EXPECTED="epic/$NOVA_EPIC"
  if [ "$NOVA_BRANCH" = "$EXPECTED" ]; then pass "nova on correct epic branch ($EXPECTED)"; else fail "nova branch mismatch: got $NOVA_BRANCH, expected $EXPECTED"; fi
fi

# --- Test 5: Priority ordering ---
echo "--- 5. Priority ordering ---"
# If both have epics, highest priority (P1) should be assigned first
if [ -n "$FALCON_EPIC" ] && [ -n "$NOVA_EPIC" ]; then
  # One of them should have the P1 epic (EPIC_A)
  if [ "$FALCON_EPIC" = "$EPIC_A" ] || [ "$NOVA_EPIC" = "$EPIC_A" ]; then
    pass "P1 epic ($EPIC_A) assigned to an agent"
  else
    fail "P1 epic ($EPIC_A) not assigned to any agent"
  fi
fi

# Clean up
loom daemon stop 2>/dev/null || true
sleep 2

echo ""
echo "=== Epic Assignment Results: $PASS passed, $FAIL failed ==="
if [ "$FAIL" -eq 0 ]; then exit 0; else exit 1; fi
