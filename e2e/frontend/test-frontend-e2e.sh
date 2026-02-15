#!/usr/bin/env bash
# test-frontend-e2e.sh — End-to-end daemon integration test for frontend.
#
# Verifies the full daemon pipeline:
#   Phase 1: Planning agents pick up all 8 tasks and create designs (status=review)
#   Phase 2: Human review approves all plans (status=open)
#   Phase 3: Task agents implement all approved designs (status=closed)
#   Verification: Build check, file existence, exports, no panics
#
# This test invokes real Claude agents and takes several minutes to complete.
#
# Prereqs: source test-env.sh (created by setup.sh)

set -euo pipefail

# --- Required env vars (validated) ---
: "${TEST_DIR:?Run setup.sh first and source test-env.sh}"
: "${TASK_DATA:?Run setup.sh first and source test-env.sh}"
: "${TASK_HEADER:?Run setup.sh first and source test-env.sh}"
: "${TASK_APP:?Run setup.sh first and source test-env.sh}"
: "${TASK_BOARD:?Run setup.sh first and source test-env.sh}"
: "${TASK_COLUMN:?Run setup.sh first and source test-env.sh}"
: "${TASK_CARD:?Run setup.sh first and source test-env.sh}"
: "${TASK_AGENTLIST:?Run setup.sh first and source test-env.sh}"
: "${TASK_WORKQUEUE:?Run setup.sh first and source test-env.sh}"

cd "$TEST_DIR"

# --- Constants ---
ALL_TASKS=("$TASK_DATA" "$TASK_HEADER" "$TASK_APP" "$TASK_BOARD" "$TASK_COLUMN" "$TASK_CARD" "$TASK_AGENTLIST" "$TASK_WORKQUEUE")
TASK_COUNT=${#ALL_TASKS[@]}

# --- Counters and helpers ---
PASSED=0
FAILED=0
pass() { echo "  PASS: $1"; PASSED=$((PASSED + 1)); }
fail() { echo "  FAIL: $1"; FAILED=$((FAILED + 1)); }

# Check for Go panics in log files (excludes git "fatal:" messages which are expected
# in test repos without remotes, and excludes "no-panic" or similar benign strings)
check_panics() {
  local label="$1"
  if grep -ri "panic:" .loom/logs/ 2>/dev/null | grep -v "no-panic" | grep -qi "panic:" 2>/dev/null; then
    fail "Go panic found in $label daemon logs"
    grep -ri "panic:" .loom/logs/ 2>/dev/null | grep -v "no-panic" | head -5
  else
    pass "No panics in $label daemon logs"
  fi
}

# --- Cleanup trap ---
cleanup() {
  loom daemon stop 2>/dev/null || true
  if [ -f ".loom/daemon.pid" ]; then
    PID=$(cat .loom/daemon.pid 2>/dev/null || true)
    [ -n "$PID" ] && kill "$PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

echo "=== Test: Frontend Daemon E2E (plan → review → implement) ==="
echo "  Tasks: ${ALL_TASKS[*]}"
echo ""

# ─────────────────────────────────────────────────
# Phase 1: Planning
# ─────────────────────────────────────────────────
echo "--- Phase 1: Planning (waiting for all $TASK_COUNT tasks to reach status=review) ---"
cp loom-plan.yaml loom.yaml
DAEMON_LOG=".loom/logs/daemon-planning.log"
loom daemon > /dev/null 2>"$DAEMON_LOG" &
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
  echo "=== Frontend Daemon E2E: $PASSED passed, $FAILED failed ==="
  exit 1
fi

# Poll until all tasks reach status=review (timeout: 8 minutes, overridable)
PLAN_TIMEOUT=${PLAN_TIMEOUT:-900}
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

check_panics "planning"

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

# Add dependencies now (not in setup.sh) so planning phase can parallelize all tasks.
# TASK_APP imports all other components, so it must be implemented last.
echo "  Adding TASK_APP dependencies for implementation ordering..."
bd dep add "$TASK_APP" "$TASK_DATA" 2>/dev/null || true
bd dep add "$TASK_APP" "$TASK_HEADER" 2>/dev/null || true
bd dep add "$TASK_APP" "$TASK_BOARD" 2>/dev/null || true
bd dep add "$TASK_APP" "$TASK_COLUMN" 2>/dev/null || true
bd dep add "$TASK_APP" "$TASK_CARD" 2>/dev/null || true
bd dep add "$TASK_APP" "$TASK_AGENTLIST" 2>/dev/null || true
bd dep add "$TASK_APP" "$TASK_WORKQUEUE" 2>/dev/null || true

# Show how many tasks are immediately ready
IMPL_READY=$(bd ready --limit 0 --json 2>/dev/null | jq '[.[] | select(.issue_type != "epic") | select(.design != null and .design != "")] | length' 2>/dev/null || echo 0)
echo "  Tasks immediately ready (unblocked): $IMPL_READY / $TASK_COUNT"
echo "  (TASK_APP will unblock after all others close)"

echo ""

# ─────────────────────────────────────────────────
# Phase 3: Implementation
# ─────────────────────────────────────────────────
echo "--- Phase 3: Implementation (waiting for all $TASK_COUNT tasks to close) ---"
# Preserve planning logs, clear agent logs only
rm -f .loom/logs/plan-*.log .loom/logs/task-*.log

cp loom-task.yaml loom.yaml
DAEMON_LOG=".loom/logs/daemon-impl.log"
loom daemon > /dev/null 2>"$DAEMON_LOG" &
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
  echo "=== Frontend Daemon E2E: $PASSED passed, $FAILED failed ==="
  exit 1
fi

# Poll until all tasks are closed (timeout: 20 minutes, overridable)
IMPL_TIMEOUT=${IMPL_TIMEOUT:-1800}
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

check_panics "implementation"

echo ""

# ─────────────────────────────────────────────────
# Verification
# ─────────────────────────────────────────────────
echo "--- Verification ---"

# Agents work in worktrees on epic branches. Merge all epic branches into main
# so we can verify the combined result from a single tree.
echo "  Merging epic branches into main..."
CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)
EPIC_BRANCHES=$(git branch --list 'epic/*' 2>/dev/null | sed 's/^[+* ]*//' || true)
MERGE_OK=true
for BRANCH in $EPIC_BRANCHES; do
  if git merge "$BRANCH" --no-edit -q 2>/dev/null; then
    echo "    Merged $BRANCH"
  else
    echo "    CONFLICT merging $BRANCH (skipping)"
    git merge --abort 2>/dev/null || true
    MERGE_OK=false
  fi
done

# Also check worktree branches (falcon, nova) which may have unmerged work
for WT_BRANCH in falcon nova; do
  if git rev-parse --verify "$WT_BRANCH" >/dev/null 2>&1; then
    if git merge "$WT_BRANCH" --no-edit -q 2>/dev/null; then
      echo "    Merged $WT_BRANCH"
    else
      echo "    CONFLICT merging $WT_BRANCH (skipping)"
      git merge --abort 2>/dev/null || true
    fi
  fi
done

# 1. Build check (authoritative pass/fail)
echo "  Running npm run build..."
BUILD_LOG=".loom/logs/build.log"
if (cd "$TEST_DIR" && npm run build) > "$BUILD_LOG" 2>&1; then
  pass "Build succeeded (npm run build)"
else
  fail "Build failed (npm run build)"
  echo "  Build log (last 50 lines):"
  tail -50 "$BUILD_LOG"
fi

# 2. File existence (informational — not counted in pass/fail)
echo "  Checking expected files..."
EXPECTED_FILES=(
  "src/App.tsx"
  "src/App.module.css"
  "src/data/mockData.ts"
  "src/components/Header/Header.tsx"
  "src/components/Header/Header.module.css"
  "src/components/KanbanBoard/KanbanBoard.tsx"
  "src/components/KanbanBoard/KanbanBoard.module.css"
  "src/components/KanbanColumn/KanbanColumn.tsx"
  "src/components/KanbanColumn/KanbanColumn.module.css"
  "src/components/IssueCard/IssueCard.tsx"
  "src/components/IssueCard/IssueCard.module.css"
  "src/components/AgentList/AgentList.tsx"
  "src/components/AgentList/AgentList.module.css"
  "src/components/AgentCard/AgentCard.tsx"
  "src/components/AgentCard/AgentCard.module.css"
  "src/components/WorkQueue/WorkQueue.tsx"
  "src/components/WorkQueue/WorkQueue.module.css"
)
FILES_FOUND=0
FILES_MISSING=0
for F in "${EXPECTED_FILES[@]}"; do
  if [ -f "$F" ]; then
    FILES_FOUND=$((FILES_FOUND + 1))
  else
    echo "    MISSING: $F"
    FILES_MISSING=$((FILES_MISSING + 1))
  fi
done
echo "  Files: $FILES_FOUND / ${#EXPECTED_FILES[@]} found ($FILES_MISSING missing)"

# Also show what files actually exist in src/ for debugging
if [ "$FILES_MISSING" -gt 0 ]; then
  echo "  Actual files in src/:"
  find src -type f \( -name "*.tsx" -o -name "*.ts" -o -name "*.css" \) | sort | sed 's/^/    /'
fi

# 3. Export check (pass/fail)
COMPONENT_FILES=(
  "src/App.tsx"
  "src/data/mockData.ts"
  "src/components/Header/Header.tsx"
  "src/components/KanbanBoard/KanbanBoard.tsx"
  "src/components/KanbanColumn/KanbanColumn.tsx"
  "src/components/IssueCard/IssueCard.tsx"
  "src/components/AgentList/AgentList.tsx"
  "src/components/AgentCard/AgentCard.tsx"
  "src/components/WorkQueue/WorkQueue.tsx"
)
EXPORTS_OK=0
for F in "${COMPONENT_FILES[@]}"; do
  if [ -f "$F" ] && grep -qE "export (default|const)" "$F" 2>/dev/null; then
    EXPORTS_OK=$((EXPORTS_OK + 1))
  fi
done
if [ "$EXPORTS_OK" -eq "${#COMPONENT_FILES[@]}" ]; then
  pass "All ${#COMPONENT_FILES[@]} component files have exports"
else
  fail "Only $EXPORTS_OK / ${#COMPONENT_FILES[@]} component files have exports"
fi

# 4. No panics (final aggregate check — only real Go panics, not git fatals)
check_panics "aggregate"

echo ""

# 5. Summary
echo "=== Frontend Daemon E2E: $PASSED passed, $FAILED failed ==="
[ "$FAILED" -eq 0 ] || exit 1
