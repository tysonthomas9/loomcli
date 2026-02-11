#!/usr/bin/env bash
# run-all.sh — Run all mzr epic branch tests in sequence.
#
# Usage:
#   ./run-all.sh                    # Uses /tmp/loom-mzr-test
#   ./run-all.sh /path/to/dir       # Custom test directory
#   ./run-all.sh --skip-daemon      # Skip daemon lifecycle test
#   ./run-all.sh --verbose          # Show full test output inline

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SKIP_DAEMON=false
VERBOSE=false
TEST_DIR=""

for arg in "$@"; do
  case "$arg" in
    --skip-daemon) SKIP_DAEMON=true ;;
    --verbose)     VERBOSE=true ;;
    --*)           echo "Unknown flag: $arg"; exit 1 ;;
    *)             TEST_DIR="$arg" ;;
  esac
done

TEST_DIR="${TEST_DIR:-/tmp/loom-mzr-test}"

echo "========================================="
echo "  loomcli-mzr: Epic Branches Test Suite"
echo "========================================="
echo ""

# Setup
echo ">>> Running setup..."
bash "$SCRIPT_DIR/setup.sh" "$TEST_DIR"
echo ""

# Source the generated env file
ENV_FILE="$TEST_DIR/test-env.sh"
if [ ! -f "$ENV_FILE" ]; then
  echo "FATAL: setup.sh did not create $ENV_FILE"
  exit 1
fi
source "$ENV_FILE"

# Validate all required vars
for var in TEST_DIR EPIC_A EPIC_B TASK_A1 TASK_A2 TASK_B1 TASK_S; do
  if [ -z "${!var:-}" ]; then
    echo "FATAL: $var is empty after sourcing $ENV_FILE"
    exit 1
  fi
done

echo "  EPIC_A=$EPIC_A  EPIC_B=$EPIC_B"
echo "  TASK_A1=$TASK_A1  TASK_A2=$TASK_A2"
echo "  TASK_B1=$TASK_B1  TASK_S=$TASK_S"
echo ""

PASS=0
FAIL=0
SKIP=0
RESULTS=()

run_test() {
  local name="$1"
  local script="$2"
  echo ""
  echo ">>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>"
  echo ">>> $name"
  echo ">>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>"

  local output
  local exit_code=0
  output=$(bash "$script" 2>&1) || exit_code=$?

  if [ "$VERBOSE" = true ] || [ "$exit_code" -ne 0 ]; then
    echo "$output"
  else
    # Show just the summary line
    echo "$output" | tail -1
  fi

  if [ "$exit_code" -eq 0 ]; then
    echo ">>> $name: OK"
    PASS=$((PASS + 1))
    RESULTS+=("  OK   $name")
  else
    echo ">>> $name: FAILED (exit code $exit_code)"
    FAIL=$((FAIL + 1))
    RESULTS+=("  FAIL $name")
    if [ "$VERBOSE" = false ]; then
      echo "  (last 5 lines of output):"
      echo "$output" | tail -5 | sed 's/^/    /'
    fi
  fi
  echo ""
}

run_test "Epic Assignment"      "$SCRIPT_DIR/test-epic-assignment.sh"
run_test "Branch Switching"     "$SCRIPT_DIR/test-branch-switching.sh"
run_test "PR Creation"          "$SCRIPT_DIR/test-pr-creation.sh"
run_test "Exhaustion Detection" "$SCRIPT_DIR/test-exhaustion.sh"
run_test "Recovery"             "$SCRIPT_DIR/test-recovery.sh"

if [ "$SKIP_DAEMON" = false ]; then
  run_test "Daemon Lifecycle"   "$SCRIPT_DIR/test-daemon-lifecycle.sh"
else
  echo ">>> Skipping daemon lifecycle test (--skip-daemon)"
  SKIP=$((SKIP + 1))
fi

echo ""
echo "========================================="
echo "  Results: $PASS passed, $FAIL failed, $SKIP skipped"
echo "========================================="
for r in "${RESULTS[@]}"; do
  echo "$r"
done
echo ""
echo "Test dir: $TEST_DIR"
echo "Env file: $ENV_FILE"
echo "To clean up: rm -rf $TEST_DIR"

exit "$FAIL"
