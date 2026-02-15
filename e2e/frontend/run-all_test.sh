#!/usr/bin/env bash
# run-all_test.sh - Tests for run-all.sh
#
# Validates the run-all.sh script's structure, argument parsing,
# and orchestration logic without actually running the full test suite.
#
# Usage: ./e2e/frontend/run-all_test.sh
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUN_ALL_SCRIPT="$SCRIPT_DIR/run-all.sh"

PASS_COUNT=0
FAIL_COUNT=0

pass() {
    echo "PASS: $1"
    PASS_COUNT=$((PASS_COUNT + 1))
}

fail() {
    echo "FAIL: $1"
    FAIL_COUNT=$((FAIL_COUNT + 1))
}

# ---------------------------------------------------------------------------
# Test 1: run-all.sh exists and is executable
# ---------------------------------------------------------------------------
test_script_exists_and_executable() {
    if [[ ! -f "$RUN_ALL_SCRIPT" ]]; then
        fail "run-all.sh does not exist"
        return
    fi
    if [[ ! -x "$RUN_ALL_SCRIPT" ]]; then
        fail "run-all.sh is not executable"
        return
    fi
    pass "run-all.sh exists and is executable"
}

# ---------------------------------------------------------------------------
# Test 2: Script has correct shebang
# ---------------------------------------------------------------------------
test_shebang() {
    local first_line
    first_line=$(head -1 "$RUN_ALL_SCRIPT")
    if [[ "$first_line" == "#!/usr/bin/env bash" ]]; then
        pass "run-all.sh has correct shebang (#!/usr/bin/env bash)"
    else
        fail "run-all.sh shebang is '$first_line', expected '#!/usr/bin/env bash'"
    fi
}

# ---------------------------------------------------------------------------
# Test 3: Script uses strict mode (set -euo pipefail)
# ---------------------------------------------------------------------------
test_strict_mode() {
    if grep -q 'set -euo pipefail' "$RUN_ALL_SCRIPT"; then
        pass "run-all.sh uses strict mode (set -euo pipefail)"
    else
        fail "run-all.sh does not use 'set -euo pipefail'"
    fi
}

# ---------------------------------------------------------------------------
# Test 4: Script has valid bash syntax
# ---------------------------------------------------------------------------
test_syntax_valid() {
    if bash -n "$RUN_ALL_SCRIPT" 2>/dev/null; then
        pass "run-all.sh has valid bash syntax"
    else
        fail "run-all.sh has syntax errors"
    fi
}

# ---------------------------------------------------------------------------
# Test 5: Script defaults to /tmp/loom-frontend-e2e
# ---------------------------------------------------------------------------
test_default_test_dir() {
    if grep -q 'TEST_DIR="/tmp/loom-frontend-e2e"' "$RUN_ALL_SCRIPT"; then
        pass "run-all.sh defaults to /tmp/loom-frontend-e2e"
    else
        fail "run-all.sh does not use expected default TEST_DIR"
    fi
}

# ---------------------------------------------------------------------------
# Test 6: Script supports --keep flag
# ---------------------------------------------------------------------------
test_keep_flag_support() {
    if grep -q '\-\-keep' "$RUN_ALL_SCRIPT" && grep -q 'KEEP=' "$RUN_ALL_SCRIPT"; then
        pass "run-all.sh supports --keep flag"
    else
        fail "run-all.sh does not properly support --keep flag"
    fi
}

# ---------------------------------------------------------------------------
# Test 7: Script accepts custom TEST_DIR as argument
# ---------------------------------------------------------------------------
test_custom_test_dir() {
    if grep -q 'TEST_DIR="$arg"' "$RUN_ALL_SCRIPT"; then
        pass "run-all.sh accepts custom TEST_DIR as argument"
    else
        fail "run-all.sh does not accept custom TEST_DIR"
    fi
}

# ---------------------------------------------------------------------------
# Test 8: Script parses arguments with a loop
# ---------------------------------------------------------------------------
test_argument_parsing_loop() {
    if grep -q 'for arg in "\$@"' "$RUN_ALL_SCRIPT"; then
        pass "run-all.sh parses arguments with a loop"
    else
        fail "run-all.sh does not properly parse arguments"
    fi
}

# ---------------------------------------------------------------------------
# Test 9: Script sets up cleanup trap on EXIT
# ---------------------------------------------------------------------------
test_cleanup_trap() {
    if grep -q 'trap cleanup EXIT' "$RUN_ALL_SCRIPT"; then
        pass "run-all.sh sets cleanup trap on EXIT"
    else
        fail "run-all.sh does not trap cleanup on EXIT"
    fi
}

# ---------------------------------------------------------------------------
# Test 10: Script calls setup.sh
# ---------------------------------------------------------------------------
test_calls_setup() {
    if grep -q 'setup.sh' "$RUN_ALL_SCRIPT"; then
        pass "run-all.sh calls setup.sh"
    else
        fail "run-all.sh does not call setup.sh"
    fi
}

# ---------------------------------------------------------------------------
# Test 11: Script calls test-frontend-e2e.sh
# ---------------------------------------------------------------------------
test_calls_test() {
    if grep -q 'test-frontend-e2e.sh' "$RUN_ALL_SCRIPT"; then
        pass "run-all.sh calls test-frontend-e2e.sh"
    else
        fail "run-all.sh does not call test-frontend-e2e.sh"
    fi
}

# ---------------------------------------------------------------------------
# Test 12: Script calls teardown.sh
# ---------------------------------------------------------------------------
test_calls_teardown() {
    if grep -q 'teardown.sh' "$RUN_ALL_SCRIPT"; then
        pass "run-all.sh calls teardown.sh"
    else
        fail "run-all.sh does not call teardown.sh"
    fi
}

# ---------------------------------------------------------------------------
# Test 13: Script sources test-env.sh
# ---------------------------------------------------------------------------
test_sources_env() {
    if grep -q 'source.*test-env.sh' "$RUN_ALL_SCRIPT"; then
        pass "run-all.sh sources test-env.sh"
    else
        fail "run-all.sh does not source test-env.sh"
    fi
}

# ---------------------------------------------------------------------------
# Test 14: Script exports TEST_DIR
# ---------------------------------------------------------------------------
test_exports_test_dir() {
    if grep -q 'export TEST_DIR' "$RUN_ALL_SCRIPT"; then
        pass "run-all.sh exports TEST_DIR"
    else
        fail "run-all.sh does not export TEST_DIR"
    fi
}

# ---------------------------------------------------------------------------
# Test 15: Script cleans up previous test run
# ---------------------------------------------------------------------------
test_cleans_previous_run() {
    if grep -q 'if \[ -d "\$TEST_DIR" \]' "$RUN_ALL_SCRIPT"; then
        pass "run-all.sh cleans up previous test run"
    else
        fail "run-all.sh does not clean up previous test run"
    fi
}

# ---------------------------------------------------------------------------
# Test 16: Script reports PASS/FAIL status
# ---------------------------------------------------------------------------
test_reports_status() {
    if grep -q 'PASS' "$RUN_ALL_SCRIPT" && grep -q 'FAIL' "$RUN_ALL_SCRIPT"; then
        pass "run-all.sh reports PASS/FAIL status"
    else
        fail "run-all.sh does not report test status"
    fi
}

# ---------------------------------------------------------------------------
# Test 17: Script tracks elapsed time
# ---------------------------------------------------------------------------
test_tracks_elapsed_time() {
    if grep -q 'SECONDS' "$RUN_ALL_SCRIPT"; then
        pass "run-all.sh tracks elapsed time"
    else
        fail "run-all.sh does not track elapsed time"
    fi
}

# ---------------------------------------------------------------------------
# Test 18: Script respects --keep flag in cleanup
# ---------------------------------------------------------------------------
test_keep_flag_in_cleanup() {
    if grep -q 'if \[ "\$KEEP" = true \]' "$RUN_ALL_SCRIPT"; then
        pass "run-all.sh respects --keep flag in cleanup"
    else
        fail "run-all.sh does not respect --keep flag in cleanup"
    fi
}

# ---------------------------------------------------------------------------
# Test 19: Script exits with test exit code
# ---------------------------------------------------------------------------
test_exits_with_test_code() {
    if grep -q 'exit \$TEST_EXIT' "$RUN_ALL_SCRIPT"; then
        pass "run-all.sh exits with test exit code"
    else
        fail "run-all.sh does not exit with test exit code"
    fi
}

# ---------------------------------------------------------------------------
# Test 20: Argument parsing handles both --keep and custom dir
# ---------------------------------------------------------------------------
test_argument_parsing_case_statement() {
    if grep -q 'case "\$arg" in' "$RUN_ALL_SCRIPT"; then
        pass "run-all.sh uses case statement for argument parsing"
    else
        fail "run-all.sh does not use case statement for parsing"
    fi
}

# ---------------------------------------------------------------------------
# Test 21: Script fails if setup fails
# ---------------------------------------------------------------------------
test_fails_on_setup_failure() {
    if grep -q 'if ! bash.*setup.sh' "$RUN_ALL_SCRIPT"; then
        pass "run-all.sh fails if setup fails"
    else
        fail "run-all.sh does not properly handle setup failure"
    fi
}

# ---------------------------------------------------------------------------
# Run all tests
# ---------------------------------------------------------------------------

echo "=== run-all.sh test suite ==="
echo ""

test_script_exists_and_executable
test_shebang
test_strict_mode
test_syntax_valid
test_default_test_dir
test_keep_flag_support
test_custom_test_dir
test_argument_parsing_loop
test_cleanup_trap
test_calls_setup
test_calls_test
test_calls_teardown
test_sources_env
test_exports_test_dir
test_cleans_previous_run
test_reports_status
test_tracks_elapsed_time
test_keep_flag_in_cleanup
test_exits_with_test_code
test_argument_parsing_case_statement
test_fails_on_setup_failure

echo ""
echo "=== Results: $PASS_COUNT passed, $FAIL_COUNT failed ==="

if [[ "$FAIL_COUNT" -gt 0 ]]; then
    exit 1
fi
