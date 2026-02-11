#!/usr/bin/env bash
# test-daemon-lifecycle.sh — Verify daemon start/status/stop and config validation.
# Tests the full daemon lifecycle including PID management.
#
# NOTE: This starts a real daemon process. Make sure loom is built and
# on your PATH.
#
# Prereqs: source test-env.sh (created by setup.sh)

set -euo pipefail

: "${TEST_DIR:?Run setup.sh first and source test-env.sh}"

cd "$TEST_DIR"

PASSED=0
FAILED=0
pass() { echo "  PASS: $1"; PASSED=$((PASSED + 1)); }
fail() { echo "  FAIL: $1"; FAILED=$((FAILED + 1)); }

# Always stop daemon on exit
cleanup() {
  loom daemon stop 2>/dev/null || true
  # Kill by PID file as backup
  if [ -f ".loom/daemon.pid" ]; then
    PID=$(cat .loom/daemon.pid 2>/dev/null || true)
    [ -n "$PID" ] && kill "$PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

echo "=== Test: Daemon lifecycle ==="

echo ""
echo "--- 1. Dry-run: validate config ---"
OUTPUT=$(loom daemon --dry-run 2>&1) || true
echo "  Output: $OUTPUT"
if echo "$OUTPUT" | grep -qi "error\|invalid\|panic"; then
  fail "Config validation failed"
else
  pass "Config validation (dry-run)"
fi

echo ""
echo "--- 2. Start daemon ---"
loom daemon > /dev/null 2>&1 &
BASH_PID=$!
echo "  Background bash PID: $BASH_PID"

# Poll for daemon startup (up to 10 seconds)
echo "  Waiting for daemon to start..."
STARTED=false
for i in $(seq 1 20); do
  sleep 0.5
  if [ -f ".loom/daemon.pid" ]; then
    DAEMON_PID=$(cat .loom/daemon.pid 2>/dev/null || true)
    if [ -n "$DAEMON_PID" ]; then
      echo "  Daemon PID (from pidfile): $DAEMON_PID"
      STARTED=true
      break
    fi
  fi
done

if [ "$STARTED" = true ]; then
  pass "Daemon started (PID $DAEMON_PID)"
else
  fail "Daemon did not start within 10 seconds"
  # Try to continue anyway
fi

echo ""
echo "--- 3. Check daemon status ---"
OUTPUT=$(loom daemon status 2>&1) || true
echo "  $OUTPUT"
if echo "$OUTPUT" | grep -qi "running\|pid\|agent\|active"; then
  pass "Daemon status reports running"
else
  fail "Daemon status did not confirm running"
fi

echo ""
echo "--- 4. Wait for agent to claim a task ---"
# The daemon spawns 'loom task <name> --auto --daemon-mode' for each agent.
# Each agent acquires a lock, then Claude picks a task and calls 'loom claim'.
# Poll until at least one agent has a non-empty task_id in its lock file
# (up to 120 seconds — Claude needs time to start and select a task).
echo "  Waiting for an agent to claim a task (up to 120s)..."
CLAIMED=false
CLAIM_AGENT=""
CLAIM_TASK=""
for i in $(seq 1 240); do
  sleep 0.5
  for AGENT in falcon nova; do
    LOCK="worktrees/$AGENT/.agent.lock"
    if [ -f "$LOCK" ]; then
      TASK_ID=$(jq -r '.task_id // empty' "$LOCK" 2>/dev/null || true)
      if [ -n "$TASK_ID" ]; then
        CLAIM_AGENT="$AGENT"
        CLAIM_TASK="$TASK_ID"
        CLAIMED=true
        break 2
      fi
    fi
  done
done
# Report lock file state for both agents
for AGENT in falcon nova; do
  LOCK="worktrees/$AGENT/.agent.lock"
  if [ -f "$LOCK" ]; then
    LOCK_PID=$(jq -r '.pid // empty' "$LOCK" 2>/dev/null || true)
    LOCK_TASK=$(jq -r '.task_id // "none"' "$LOCK" 2>/dev/null || true)
    echo "  $AGENT: PID $LOCK_PID, task=$LOCK_TASK"
  else
    echo "  $AGENT: no lock file"
  fi
done
if [ "$CLAIMED" = true ]; then
  pass "Agent $CLAIM_AGENT claimed task $CLAIM_TASK"
else
  fail "No agent claimed a task within 120 seconds"
fi

echo ""
echo "--- 5. Stop daemon ---"
OUTPUT=$(loom daemon stop 2>&1) || true
echo "  $OUTPUT"

# Wait for process to exit
wait "$BASH_PID" 2>/dev/null || true
sleep 1

if echo "$OUTPUT" | grep -qi "stopped\|signal\|shut"; then
  pass "Daemon stop command succeeded"
else
  # Check if process actually died
  if [ -n "${DAEMON_PID:-}" ] && kill -0 "$DAEMON_PID" 2>/dev/null; then
    fail "Daemon still running after stop"
  else
    pass "Daemon process exited"
  fi
fi

echo ""
echo "--- 6. Verify daemon is stopped ---"
OUTPUT=$(loom daemon status 2>&1) || true
echo "  $OUTPUT"
if echo "$OUTPUT" | grep -qi "not running\|no daemon\|error\|no pid"; then
  pass "Daemon confirmed stopped"
else
  # Process check as backup
  if [ -n "${DAEMON_PID:-}" ] && kill -0 "$DAEMON_PID" 2>/dev/null; then
    fail "Daemon still running"
  else
    pass "Daemon process not running"
  fi
fi

echo ""
echo "=== Daemon lifecycle: $PASSED passed, $FAILED failed ==="
[ "$FAILED" -eq 0 ] || exit 1
