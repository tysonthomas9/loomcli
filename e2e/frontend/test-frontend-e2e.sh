#!/usr/bin/env bash
# test-frontend-e2e.sh — End-to-end daemon integration test for frontend.
#
# Verifies the full daemon pipeline:
#   Phase 1: Planning agents pick up all 13 tasks and create designs (status=review)
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
: "${TASK_SCAFFOLD:?Run setup.sh first and source test-env.sh}"
: "${TASK_DATA:?Run setup.sh first and source test-env.sh}"
: "${TASK_HEADER:?Run setup.sh first and source test-env.sh}"
: "${TASK_APP:?Run setup.sh first and source test-env.sh}"
: "${TASK_BOARD:?Run setup.sh first and source test-env.sh}"
: "${TASK_COLUMN:?Run setup.sh first and source test-env.sh}"
: "${TASK_CARD:?Run setup.sh first and source test-env.sh}"
: "${TASK_AGENTLIST:?Run setup.sh first and source test-env.sh}"
: "${TASK_WORKQUEUE:?Run setup.sh first and source test-env.sh}"
: "${TASK_AGENT_DETAIL:?Run setup.sh first and source test-env.sh}"
: "${TASK_TASK_DETAIL:?Run setup.sh first and source test-env.sh}"
: "${TASK_QUALITY:?Run setup.sh first and source test-env.sh}"
: "${TASK_VISUAL:?Run setup.sh first and source test-env.sh}"

cd "$TEST_DIR"

# --- Constants ---
ALL_TASKS=("$TASK_SCAFFOLD" "$TASK_DATA" "$TASK_HEADER" "$TASK_APP" "$TASK_BOARD" "$TASK_COLUMN" "$TASK_CARD" "$TASK_AGENTLIST" "$TASK_WORKQUEUE" "$TASK_AGENT_DETAIL" "$TASK_TASK_DETAIL" "$TASK_QUALITY" "$TASK_VISUAL")
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

# --- Daemon lifecycle helpers ---

# Stop daemon gracefully with timeout, force-kill fallback, and state flush.
# Usage: stop_daemon_gracefully [timeout_seconds]
stop_daemon_gracefully() {
  local timeout=${1:-30}
  local pid=""

  # Read PID before stopping (daemon stop removes the pidfile)
  if [ -f ".loom/daemon.pid" ]; then
    pid=$(cat .loom/daemon.pid 2>/dev/null || true)
  fi

  loom daemon stop 2>/dev/null || true

  # Wait for process to exit
  if [ -n "$pid" ]; then
    local elapsed=0
    while kill -0 "$pid" 2>/dev/null && [ $elapsed -lt $timeout ]; do
      sleep 1
      elapsed=$((elapsed + 1))
    done
    if kill -0 "$pid" 2>/dev/null; then
      echo "  WARN: Daemon PID $pid still alive after ${timeout}s, force-killing"
      kill -9 "$pid" 2>/dev/null || true
      sleep 1
    fi
  fi

  # Flush beads state
  bd sync 2>/dev/null || true

  # Clean stale agent lock files in worktrees
  find worktrees -name ".agent.lock" -delete 2>/dev/null || true
}

# Start daemon and verify it's alive.
# Usage: start_daemon_with_healthcheck log_file [timeout_seconds]
start_daemon_with_healthcheck() {
  local log_file="$1"
  local timeout=${2:-60}

  loom daemon > /dev/null 2>"$log_file" &
  DAEMON_PID=$!

  # Wait for PID file (poll every 0.5s, track real elapsed time)
  local start_time=$SECONDS
  while [ ! -f ".loom/daemon.pid" ] && [ $((SECONDS - start_time)) -lt $timeout ]; do
    sleep 0.5
    # Check if process died before creating pidfile
    if ! kill -0 "$DAEMON_PID" 2>/dev/null; then
      echo "  ERROR: Daemon process died before creating pidfile"
      echo "  Log tail:"
      tail -20 "$log_file" 2>/dev/null | sed 's/^/    /'
      return 1
    fi
  done

  if [ ! -f ".loom/daemon.pid" ]; then
    echo "  ERROR: Daemon pidfile not created within ${timeout}s"
    echo "  Daemon status:"
    loom daemon status 2>&1 | sed 's/^/    /' || true
    return 1
  fi

  # Verify process is actually alive
  local daemon_pid
  daemon_pid=$(cat .loom/daemon.pid 2>/dev/null || true)
  if [ -n "$daemon_pid" ] && kill -0 "$daemon_pid" 2>/dev/null; then
    echo "  Daemon started (PID $daemon_pid)"
    return 0
  else
    echo "  ERROR: Daemon pidfile exists but process $daemon_pid is not alive"
    return 1
  fi
}

# --- Cleanup trap ---
cleanup() {
  stop_daemon_gracefully 10
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
if ! start_daemon_with_healthcheck "$DAEMON_LOG" 60; then
  fail "Planning daemon did not start"
  echo "=== Frontend Daemon E2E: $PASSED passed, $FAILED failed ==="
  exit 1
fi
PLANNING_PID=$DAEMON_PID

# Poll until all tasks reach status=review (timeout: 30 minutes, overridable)
PLAN_TIMEOUT=${PLAN_TIMEOUT:-1800}
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

echo "  Stopping planning daemon..."
stop_daemon_gracefully 30
wait "$PLANNING_PID" 2>/dev/null || true

if [ "$REVIEW_COUNT" -ge "$TASK_COUNT" ]; then
  pass "All $TASK_COUNT tasks reached status=review ($((SECONDS - PLAN_START))s)"
else
  fail "Only $REVIEW_COUNT / $TASK_COUNT tasks reached review within ${PLAN_TIMEOUT}s"
fi

# Verify each task has a non-empty design
DESIGNS_OK=0
MISSING_DESIGNS=()
for TASK in "${ALL_TASKS[@]}"; do
  DESIGN=$(bd show "$TASK" --json 2>/dev/null | jq -r '.[0].design // empty' 2>/dev/null || true)
  if [ -n "$DESIGN" ]; then
    DESIGNS_OK=$((DESIGNS_OK + 1))
    echo "  $TASK: design present (${#DESIGN} chars)"
  else
    echo "  $TASK: NO DESIGN"
    MISSING_DESIGNS+=("$TASK")
  fi
done
if [ "$DESIGNS_OK" -eq "$TASK_COUNT" ]; then
  pass "All $TASK_COUNT tasks have non-empty designs"
else
  fail "Only $DESIGNS_OK / $TASK_COUNT tasks have designs"
fi

check_panics "planning"

# --- Phase gate: abort if planning incomplete ---
# Implementation agents will spin in retry loops on designless tasks, wasting
# the entire IMPL_TIMEOUT. Fail fast instead.
if [ "$DESIGNS_OK" -lt "$TASK_COUNT" ]; then
  echo ""
  echo "  FATAL: Cannot continue to implementation — ${#MISSING_DESIGNS[@]} task(s) have no designs:"
  for TASK in "${MISSING_DESIGNS[@]}"; do
    TITLE=$(bd show "$TASK" --json 2>/dev/null | jq -r '.[0].title // "unknown"' 2>/dev/null || echo "unknown")
    echo "    $TASK: $TITLE"
  done
  echo ""
  echo "  Implementation requires all designs. Re-run with a longer PLAN_TIMEOUT"
  echo "  (current: ${PLAN_TIMEOUT}s) or check agent logs for errors."
  echo ""
  echo "=== Frontend Daemon E2E: $PASSED passed, $FAILED failed ==="
  exit 1
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

# Add dependencies from deps.conf (not in setup.sh) so planning can parallelize.
# deps.conf format: task-slug depends-on-slug (one per line, # comments)
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DEPS_FILE="$SCRIPT_DIR/deps.conf"
SLUG_MAP="$TEST_DIR/slug-to-id.map"

if [ ! -f "$DEPS_FILE" ]; then
  fail "deps.conf not found at $DEPS_FILE"
  echo "=== Frontend Daemon E2E: $PASSED passed, $FAILED failed ==="
  exit 1
fi
if [ ! -f "$SLUG_MAP" ]; then
  fail "slug-to-id.map not found at $SLUG_MAP"
  echo "=== Frontend Daemon E2E: $PASSED passed, $FAILED failed ==="
  exit 1
fi

echo "  Adding implementation dependencies from deps.conf..."
DEP_COUNT=0
DEP_FAIL=0
DEP_RESOLVE_FAIL=0
while read -r task_slug dep_slug; do
  # Skip comments and blank lines
  case "$task_slug" in "#"*|"") continue ;; esac
  task_id=$(grep "^${task_slug}=" "$SLUG_MAP" | cut -d= -f2)
  dep_id=$(grep "^${dep_slug}=" "$SLUG_MAP" | cut -d= -f2)
  if [ -z "$task_id" ] || [ -z "$dep_id" ]; then
    echo "    WARN: Could not resolve slug: $task_slug → $dep_slug"
    DEP_RESOLVE_FAIL=$((DEP_RESOLVE_FAIL + 1))
    continue
  fi
  DEP_OUTPUT=$(bd dep add "$task_id" "$dep_id" 2>&1) && {
    DEP_COUNT=$((DEP_COUNT + 1))
  } || {
    # bd dep add returns UNIQUE constraint error for existing deps — treat as success
    if echo "$DEP_OUTPUT" | grep -qi "UNIQUE constraint\|already exists"; then
      DEP_COUNT=$((DEP_COUNT + 1))
    else
      echo "    WARN: bd dep add failed for $task_slug ($task_id) → $dep_slug ($dep_id): $DEP_OUTPUT"
      DEP_FAIL=$((DEP_FAIL + 1))
    fi
  }
done < "$DEPS_FILE"

if [ $DEP_RESOLVE_FAIL -gt 0 ] || [ $DEP_FAIL -gt 0 ]; then
  fail "Dependency issues: $DEP_RESOLVE_FAIL unresolved slugs, $DEP_FAIL bd dep add failures (${DEP_COUNT} succeeded)"
else
  pass "All $DEP_COUNT dependencies added from deps.conf"
fi

# Show how many tasks are immediately ready
IMPL_READY=$(bd ready --limit 0 --json 2>/dev/null | jq '[.[] | select(.issue_type != "epic") | select(.design != null and .design != "")] | length' 2>/dev/null || echo 0)
echo "  Tasks immediately ready (unblocked): $IMPL_READY / $TASK_COUNT"

echo ""

# ─────────────────────────────────────────────────
# Phase 3: Implementation
# ─────────────────────────────────────────────────
echo "--- Phase 3: Implementation (waiting for all $TASK_COUNT tasks to close) ---"
# Preserve planning logs, clear agent logs only
rm -f .loom/logs/plan-*.log .loom/logs/task-*.log

cp loom-task.yaml loom.yaml
DAEMON_LOG=".loom/logs/daemon-impl.log"
if ! start_daemon_with_healthcheck "$DAEMON_LOG" 60; then
  fail "Implementation daemon did not start"
  echo "=== Frontend Daemon E2E: $PASSED passed, $FAILED failed ==="
  exit 1
fi
IMPL_PID=$DAEMON_PID

# Poll until all tasks are closed (timeout: 60 minutes, overridable)
IMPL_TIMEOUT=${IMPL_TIMEOUT:-3600}
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

echo "  Stopping implementation daemon..."
stop_daemon_gracefully 30
wait "$IMPL_PID" 2>/dev/null || true

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

# Remove worktrees first to avoid "branch is checked out" conflicts
for wt in worktrees/*/; do
  [ -d "$wt" ] && git worktree remove --force "$wt" 2>/dev/null || true
done

git checkout main -q 2>/dev/null || true
EPIC_BRANCHES=$(git branch --list 'epic/*' 2>/dev/null | sed 's/^[+* ]*//' || true)
MERGE_CONFLICTS=0
MERGE_CLEAN=0

merge_branch() {
  local branch="$1"
  # Try clean merge first
  if git merge "$branch" --no-edit -q 2>/dev/null; then
    echo "    Merged $branch (clean)"
    MERGE_CLEAN=$((MERGE_CLEAN + 1))
    return 0
  fi
  git merge --abort 2>/dev/null || true

  # Fallback: prefer incoming changes to maximize code coverage
  if git merge "$branch" --no-edit -X theirs -q 2>/dev/null; then
    echo "    Merged $branch (with -X theirs to resolve conflicts)"
    MERGE_CLEAN=$((MERGE_CLEAN + 1))
    return 0
  fi
  git merge --abort 2>/dev/null || true

  echo "    CONFLICT merging $branch (could not resolve even with -X theirs)"
  MERGE_CONFLICTS=$((MERGE_CONFLICTS + 1))
  return 1
}

for BRANCH in $EPIC_BRANCHES; do
  merge_branch "$BRANCH" || true
done

# Also check worktree branches (falcon, nova) which may have unmerged work
for WT_BRANCH in falcon nova; do
  if git rev-parse --verify "$WT_BRANCH" >/dev/null 2>&1; then
    merge_branch "$WT_BRANCH" || true
  fi
done

if [ $MERGE_CONFLICTS -gt 0 ]; then
  echo "  WARN: $MERGE_CONFLICTS branch(es) had unresolvable merge conflicts"
fi
echo "  Merged $MERGE_CLEAN branch(es) total"

# 1. Build check (authoritative pass/fail)
# Install dependencies first — node_modules aren't committed, so they won't
# exist after merging branches into main.
echo "  Running npm install..."
(cd "$TEST_DIR" && npm install --loglevel=error) > ".loom/logs/npm-install.log" 2>&1 || true
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
  "package.json"
  "tsconfig.json"
  "vite.config.ts"
  "index.html"
  "src/main.tsx"
  "src/index.css"
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
  "src/components/AgentDetailPanel/AgentDetailPanel.tsx"
  "src/components/AgentDetailPanel/AgentDetailPanel.module.css"
  "src/components/TaskDetailPanel/TaskDetailPanel.tsx"
  "src/components/TaskDetailPanel/TaskDetailPanel.module.css"
  "eslint.config.js"
  "scripts/pre-commit.sh"
  "playwright.config.ts"
  "e2e/app.spec.ts"
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

# 4. Lint check (pass/fail)
echo "  Running lint check..."
LINT_LOG=".loom/logs/lint.log"
if (cd "$TEST_DIR" && npm run lint) > "$LINT_LOG" 2>&1; then
  pass "Lint passed (npm run lint)"
else
  fail "Lint failed (npm run lint)"
  echo "  Lint log (last 20 lines):"
  tail -20 "$LINT_LOG"
fi

# 5. Playwright visual tests (pass/fail)
echo "  Installing Playwright chromium..."
(cd "$TEST_DIR" && npx playwright install chromium) > ".loom/logs/playwright-install.log" 2>&1 || true
echo "  Running Playwright visual tests..."
VISUAL_LOG=".loom/logs/visual-test.log"
if (cd "$TEST_DIR" && npm run test:e2e) > "$VISUAL_LOG" 2>&1; then
  pass "Visual tests passed (playwright)"
else
  fail "Visual tests failed (playwright)"
  echo "  Visual test log (last 30 lines):"
  tail -30 "$VISUAL_LOG"
fi

# 6. No panics (final aggregate check — only real Go panics, not git fatals)
check_panics "aggregate"

echo ""

# 7. Summary
echo "=== Frontend Daemon E2E: $PASSED passed, $FAILED failed ==="
[ "$FAILED" -eq 0 ] || exit 1
