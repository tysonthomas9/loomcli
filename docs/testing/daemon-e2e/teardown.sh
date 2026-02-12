#!/usr/bin/env bash
# teardown.sh — Stop daemon and clean up test environment.
#
# Usage: ./teardown.sh [test_dir]
#   test_dir defaults to /tmp/loom-daemon-e2e

set -euo pipefail

TEST_DIR="${1:-/tmp/loom-daemon-e2e}"

if [ ! -d "$TEST_DIR" ]; then
  echo "Test directory does not exist: $TEST_DIR"
  exit 0
fi

cd "$TEST_DIR"

# Stop daemon if running
if [ -f .loom/daemon.pid ]; then
  PID=$(cat .loom/daemon.pid 2>/dev/null || echo "")
  if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then
    echo "==> Stopping daemon (PID $PID)..."
    loom daemon stop 2>/dev/null || kill -TERM "$PID" 2>/dev/null || true
    # Wait up to 10s
    for i in $(seq 1 20); do
      sleep 0.5
      if ! kill -0 "$PID" 2>/dev/null; then break; fi
    done
    # Force kill if still alive
    if kill -0 "$PID" 2>/dev/null; then
      echo "Force killing daemon..."
      kill -9 "$PID" 2>/dev/null || true
    fi
  fi
fi

# Kill any orphaned agent processes in worktrees
for wt in falcon nova; do
  LOCK="$TEST_DIR/worktrees/$wt/.agent.lock"
  if [ -f "$LOCK" ]; then
    AGENT_PID=$(jq -r '.pid' "$LOCK" 2>/dev/null || echo "")
    if [ -n "$AGENT_PID" ] && kill -0 "$AGENT_PID" 2>/dev/null; then
      echo "Killing orphaned agent in $wt (PID $AGENT_PID)..."
      kill -TERM "$AGENT_PID" 2>/dev/null || true
      sleep 1
      kill -9 "$AGENT_PID" 2>/dev/null || true
    fi
  fi
done

# Remove worktrees before deleting directory (git requires this)
cd "$TEST_DIR"
git worktree remove --force ./worktrees/falcon 2>/dev/null || true
git worktree remove --force ./worktrees/nova   2>/dev/null || true

echo "==> Removing test directory: $TEST_DIR"
rm -rf "$TEST_DIR"
echo "==> Teardown complete"
