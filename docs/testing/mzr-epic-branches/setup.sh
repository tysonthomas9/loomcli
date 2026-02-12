#!/usr/bin/env bash
# setup.sh — Create a temporary git repo with beads and worktrees for testing
# the Daemon-Managed Epic Branches feature (loomcli-mzr).
#
# Usage: ./setup.sh [--single-worktree] [test_dir]
#   test_dir defaults to /tmp/loom-mzr-test
#   --single-worktree: create only the falcon worktree (for epic transition testing)
#
# Outputs a test-env.sh file that can be sourced by other scripts.

set -euo pipefail

SINGLE_WORKTREE=false
TEST_DIR=""
for arg in "$@"; do
  case "$arg" in
    --single-worktree) SINGLE_WORKTREE=true ;;
    *)                 TEST_DIR="$arg" ;;
  esac
done
TEST_DIR="${TEST_DIR:-/tmp/loom-mzr-test}"

if [ -d "$TEST_DIR" ]; then
  echo "!! $TEST_DIR already exists. Remove it first or choose another path."
  exit 1
fi

echo "==> Creating test repo at $TEST_DIR"
mkdir -p "$TEST_DIR"
cd "$TEST_DIR"

git init -q
git commit --allow-empty -q -m "initial commit"

# Initialise beads
bd init -q

# Create worktrees
mkdir -p worktrees
git worktree add -q ./worktrees/falcon -b falcon
if [ "$SINGLE_WORKTREE" = "false" ]; then
  git worktree add -q ./worktrees/nova -b nova
fi

# Create loom.yaml configs for different test phases
# Agent list depends on single vs multi worktree mode
if [ "$SINGLE_WORKTREE" = "true" ]; then
  AGENTS_TASK='  - worktree: falcon
    role: task
    auto: true'
  AGENTS_PLAN='  - worktree: falcon
    role: plan
    auto: true'
else
  AGENTS_TASK='  - worktree: falcon
    role: task
    auto: true
  - worktree: nova
    role: task
    auto: true'
  AGENTS_PLAN='  - worktree: falcon
    role: plan
    auto: true
  - worktree: nova
    role: plan
    auto: true'
fi

DAEMON_BLOCK='daemon:
  pid_file: .loom/daemon.pid
  log_dir: .loom/logs
  restart_policy:
    max_retries: 3
    backoff_initial: 2
    backoff_max: 60'

# Default config (agents as task) — used by unit-style tests
cat > loom.yaml <<EOF
$DAEMON_BLOCK

agents:
$AGENTS_TASK
EOF

# Planning config — agents create designs
cat > loom-plan.yaml <<EOF
$DAEMON_BLOCK

agents:
$AGENTS_PLAN
EOF

# Implementation config — agents implement approved designs
cat > loom-task.yaml <<EOF
$DAEMON_BLOCK

agents:
$AGENTS_TASK
EOF

mkdir -p .loom/logs

# --- Seed beads with two epics and tasks ---

echo "==> Creating test epics and tasks"

# Use --json | jq for reliable ID extraction
EPIC_A=$(bd create --title="Epic Alpha: Auth system" --type=epic --priority=1 --json 2>/dev/null | jq -r '.id')
[ -n "$EPIC_A" ] || { echo "FAIL: Could not create Epic A"; exit 1; }

TASK_A1=$(bd create --title="Implement login endpoint" --type=task --priority=1 --parent="$EPIC_A" --json 2>/dev/null | jq -r '.id')
TASK_A2=$(bd create --title="Implement logout endpoint" --type=task --priority=2 --parent="$EPIC_A" --json 2>/dev/null | jq -r '.id')
[ -n "$TASK_A1" ] && [ -n "$TASK_A2" ] || { echo "FAIL: Could not create Epic A tasks"; exit 1; }

EPIC_B=$(bd create --title="Epic Beta: Logging" --type=epic --priority=2 --json 2>/dev/null | jq -r '.id')
[ -n "$EPIC_B" ] || { echo "FAIL: Could not create Epic B"; exit 1; }

TASK_B1=$(bd create --title="Add structured logging" --type=task --priority=2 --parent="$EPIC_B" --json 2>/dev/null | jq -r '.id')
[ -n "$TASK_B1" ] || { echo "FAIL: Could not create Epic B task"; exit 1; }

# A standalone non-epic task (for fallback branch testing)
TASK_S=$(bd create --title="Fix typo in README" --type=task --priority=3 --json 2>/dev/null | jq -r '.id')
[ -n "$TASK_S" ] || { echo "FAIL: Could not create standalone task"; exit 1; }

bd sync 2>/dev/null || true

# Write env file for other scripts to source
ENV_FILE="$TEST_DIR/test-env.sh"
cat > "$ENV_FILE" <<EOF
export TEST_DIR="$TEST_DIR"
export EPIC_A="$EPIC_A"
export EPIC_B="$EPIC_B"
export TASK_A1="$TASK_A1"
export TASK_A2="$TASK_A2"
export TASK_B1="$TASK_B1"
export TASK_S="$TASK_S"
EOF

echo ""
echo "==> Test environment ready at $TEST_DIR"
echo ""
echo "  Epics:"
echo "    EPIC_A=$EPIC_A  (2 tasks: $TASK_A1, $TASK_A2)"
echo "    EPIC_B=$EPIC_B  (1 task:  $TASK_B1)"
echo "  Standalone task: $TASK_S"
echo ""
if [ "$SINGLE_WORKTREE" = "true" ]; then
  echo "  Worktrees: falcon (single-worktree mode)"
else
  echo "  Worktrees: falcon, nova"
fi
echo ""
echo "  Source the env file for individual tests:"
echo "    source $ENV_FILE"
