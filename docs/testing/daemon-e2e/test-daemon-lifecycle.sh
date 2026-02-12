#!/usr/bin/env bash
# test-daemon-lifecycle.sh — Test daemon start, status, stop, restart, and signal handling.
#
# Prerequisites: Run setup.sh first, then source test-env.sh.
# Usage: ./test-daemon-lifecycle.sh

set -uo pipefail

TEST_DIR="${TEST_DIR:-/tmp/loom-daemon-e2e}"
cd "$TEST_DIR"

PASS=0
FAIL=0

pass() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL + 1)); }

assert_grep() {
  if echo "$1" | grep -q "$2"; then pass "$3"; else fail "$4"; fi
}

echo "=== Test: Daemon Lifecycle ==="
echo ""

# --- Test 1: Dry-run ---
echo "--- 1. Dry-run ---"
OUTPUT=$(loom daemon --dry-run 2>&1)
assert_grep "$OUTPUT" "DRY RUN" "dry-run shows banner" "dry-run banner missing"
assert_grep "$OUTPUT" "falcon" "dry-run shows falcon" "dry-run missing falcon"
assert_grep "$OUTPUT" "nova" "dry-run shows nova" "dry-run missing nova"
if [ ! -f .loom/daemon.pid ]; then pass "dry-run does not create PID file"; else fail "dry-run created PID file"; fi

# --- Test 2: Start daemon ---
echo "--- 2. Start daemon ---"
rm -f .loom/logs/daemon-stdout.log
loom daemon > .loom/logs/daemon-stdout.log 2>&1 &
sleep 5

if [ -f .loom/daemon.pid ]; then pass "PID file created"; else fail "PID file not created"; fi
PID=$(cat .loom/daemon.pid 2>/dev/null || echo "")
if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then
  pass "daemon process running (PID $PID)"
else
  fail "daemon process not running"
fi

# --- Test 3: Status ---
echo "--- 3. Status ---"
STATUS=$(loom daemon status 2>&1)
assert_grep "$STATUS" "running" "status shows running" "status does not show running"
assert_grep "$STATUS" "falcon" "status shows falcon agent" "status missing falcon agent"
assert_grep "$STATUS" "nova" "status shows nova agent" "status missing nova agent"

# --- Test 4: Concurrent prevention ---
echo "--- 4. Concurrent prevention ---"
SECOND=$(loom daemon 2>&1 || true)
if echo "$SECOND" | grep -q "already running"; then
  pass "second daemon start blocked"
else
  fail "second daemon was not blocked"
fi

# --- Test 5: Stop daemon ---
echo "--- 5. Stop daemon ---"
loom daemon stop 2>&1 || true
sleep 2
if [ ! -f .loom/daemon.pid ]; then pass "PID file removed after stop"; else fail "PID file still exists after stop"; fi
STATUS=$(loom daemon status 2>&1)
assert_grep "$STATUS" "not running" "status shows not running after stop" "status still shows running after stop"

# --- Test 6: Restart ---
echo "--- 6. Restart ---"
rm -f .loom/logs/daemon-stdout.log worktrees/falcon/.agent.lock worktrees/nova/.agent.lock
loom daemon > .loom/logs/daemon-stdout.log 2>&1 &
sleep 5

STATUS=$(loom daemon status 2>&1)
assert_grep "$STATUS" "running" "daemon restarted successfully" "daemon did not restart"

# --- Test 7: SIGTERM clean shutdown ---
echo "--- 7. SIGTERM clean shutdown ---"
PID=$(cat .loom/daemon.pid 2>/dev/null || echo "")
if [ -n "$PID" ]; then
  kill -TERM "$PID"
  EXITED=false
  for i in $(seq 1 20); do
    sleep 0.5
    if ! kill -0 "$PID" 2>/dev/null; then EXITED=true; break; fi
  done
  if $EXITED; then pass "daemon exited on SIGTERM within 10s"; else fail "daemon did not exit on SIGTERM"; fi
  if [ ! -f .loom/daemon.pid ]; then pass "PID file cleaned up after SIGTERM"; else fail "PID file still exists after SIGTERM"; fi
  if grep -q "Daemon stopped" .loom/logs/daemon-stdout.log; then
    pass "clean shutdown message logged"
  else
    fail "no shutdown message in log"
  fi
else
  fail "could not read PID for SIGTERM test"
fi

# --- Summary ---
echo ""
echo "=== Lifecycle Results: $PASS passed, $FAIL failed ==="
if [ "$FAIL" -eq 0 ]; then exit 0; else exit 1; fi
