#!/usr/bin/env bash
# run-all.sh — Run all daemon E2E tests sequentially.
#
# Usage: ./run-all.sh [test_dir]
#   test_dir defaults to /tmp/loom-daemon-e2e
#
# This script:
#   1. Runs setup.sh to create the test environment
#   2. Runs each test script in order
#   3. Runs teardown.sh to clean up
#   4. Reports pass/fail summary

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TEST_DIR="${1:-/tmp/loom-daemon-e2e}"

TOTAL_PASS=0
TOTAL_FAIL=0
RESULTS=()

run_test() {
  local name="$1"
  local script="$2"
  echo ""
  echo "╔══════════════════════════════════════════════════════════════╗"
  echo "║  Running: $name"
  echo "╚══════════════════════════════════════════════════════════════╝"
  echo ""

  if bash "$script"; then
    RESULTS+=("PASS  $name")
    TOTAL_PASS=$((TOTAL_PASS + 1))
  else
    RESULTS+=("FAIL  $name")
    TOTAL_FAIL=$((TOTAL_FAIL + 1))
  fi
}

# --- Setup ---
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║  Daemon E2E Test Suite"
echo "║  Test directory: $TEST_DIR"
echo "╚══════════════════════════════════════════════════════════════╝"

# Clean up any previous run
if [ -d "$TEST_DIR" ]; then
  echo "Cleaning up previous test run..."
  bash "$SCRIPT_DIR/teardown.sh" "$TEST_DIR" 2>/dev/null || rm -rf "$TEST_DIR"
fi

echo ""
echo "Running setup..."
if ! bash "$SCRIPT_DIR/setup.sh" "$TEST_DIR"; then
  echo "FATAL: Setup failed"
  exit 1
fi

# Source environment
export TEST_DIR
source "$TEST_DIR/test-env.sh"

# --- Run tests ---

# Lifecycle tests (fast, no real agents needed)
run_test "Daemon Lifecycle" "$SCRIPT_DIR/test-daemon-lifecycle.sh"

# Epic assignment (starts daemon briefly, checks state)
run_test "Epic Assignment" "$SCRIPT_DIR/test-epic-assignment.sh"

# Recovery (stale locks, orphaned tasks)
run_test "Recovery" "$SCRIPT_DIR/test-recovery.sh"

# Exhaustion (close tasks, verify reassignment)
run_test "Exhaustion" "$SCRIPT_DIR/test-exhaustion.sh"

# Agent execution (uses real Claude — slowest test, runs last)
# Uncomment the next line to include the real agent execution test.
# This test can take 10-30 minutes as it waits for Claude to complete tasks.
# run_test "Agent Execution" "$SCRIPT_DIR/test-agent-execution.sh"

# --- Teardown ---
echo ""
echo "Running teardown..."
bash "$SCRIPT_DIR/teardown.sh" "$TEST_DIR" 2>/dev/null || true

# --- Summary ---
echo ""
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║  Test Results"
echo "╠══════════════════════════════════════════════════════════════╣"
for result in "${RESULTS[@]}"; do
  echo "║  $result"
done
echo "╠══════════════════════════════════════════════════════════════╣"
echo "║  Total: $((TOTAL_PASS + TOTAL_FAIL)) tests, $TOTAL_PASS passed, $TOTAL_FAIL failed"
echo "╚══════════════════════════════════════════════════════════════╝"

if [ "$TOTAL_FAIL" -eq 0 ]; then exit 0; else exit 1; fi
