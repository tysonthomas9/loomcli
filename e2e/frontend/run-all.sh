#!/usr/bin/env bash
# run-all.sh — Run the frontend E2E test suite end-to-end.
#
# Usage: ./run-all.sh [test_dir] [--keep]
#   test_dir defaults to /tmp/loom-frontend-e2e
#   --keep   skip teardown after test (for debugging)
#
# This script:
#   1. Runs setup.sh to create the test environment
#   2. Sources the test environment variables
#   3. Runs test-frontend-e2e.sh (3-phase daemon test)
#   4. Runs teardown.sh to clean up (unless --keep)
#   5. Reports pass/fail with elapsed time

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TEST_DIR="/tmp/loom-frontend-e2e"
KEEP=false

# Parse arguments
for arg in "$@"; do
  case "$arg" in
    --keep) KEEP=true ;;
    *)      TEST_DIR="$arg" ;;
  esac
done

# Trap EXIT to ensure teardown runs even on failure
cleanup() {
  local exit_code=$?
  if [ "$KEEP" = true ]; then
    echo ""
    echo "  --keep: skipping teardown, test dir preserved at $TEST_DIR"
  else
    echo ""
    echo "Running teardown..."
    bash "$SCRIPT_DIR/teardown.sh" "$TEST_DIR" 2>/dev/null || true
  fi
  exit $exit_code
}
trap cleanup EXIT

# --- Banner ---
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║  Frontend E2E Test Suite"
echo "║  Test directory: $TEST_DIR"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""

# --- Clean previous run ---
if [ -d "$TEST_DIR" ]; then
  echo "Cleaning up previous test run..."
  bash "$SCRIPT_DIR/teardown.sh" "$TEST_DIR" 2>/dev/null || rm -rf "$TEST_DIR"
fi

# --- Setup ---
echo ""
echo "Running setup..."
if ! bash "$SCRIPT_DIR/setup.sh" "$TEST_DIR"; then
  echo "FATAL: Setup failed"
  exit 1
fi
echo "  [${SECONDS}s] Setup complete"

# --- Source environment ---
export TEST_DIR
source "$TEST_DIR/test-env.sh"

# --- Run test ---
echo ""
echo "Running test-frontend-e2e.sh..."
TEST_EXIT=0
bash "$SCRIPT_DIR/test-frontend-e2e.sh" || TEST_EXIT=$?
echo "  [${SECONDS}s] Test complete"

# --- Report ---
echo ""
echo "╔══════════════════════════════════════════════════════════════╗"
if [ "$TEST_EXIT" -eq 0 ]; then
  echo "║  Result: PASS"
else
  echo "║  Result: FAIL"
fi
echo "║  Elapsed: ${SECONDS}s"
echo "╚══════════════════════════════════════════════════════════════╝"

exit $TEST_EXIT
