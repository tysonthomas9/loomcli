#!/usr/bin/env bash
# teardown_test.sh - Tests for teardown.sh
#
# Validates the teardown.sh script's structure, idempotency,
# and behavior without running destructive operations.
#
# Usage: ./e2e/frontend/teardown_test.sh
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEARDOWN_SCRIPT="$SCRIPT_DIR/teardown.sh"

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
# Test 1: teardown.sh exists and is executable
# ---------------------------------------------------------------------------
test_script_exists_and_executable() {
    if [[ ! -f "$TEARDOWN_SCRIPT" ]]; then
        fail "teardown.sh does not exist"
        return
    fi
    if [[ ! -x "$TEARDOWN_SCRIPT" ]]; then
        fail "teardown.sh is not executable"
        return
    fi
    pass "teardown.sh exists and is executable"
}

# ---------------------------------------------------------------------------
# Test 2: Script has correct shebang
# ---------------------------------------------------------------------------
test_shebang() {
    local first_line
    first_line=$(head -1 "$TEARDOWN_SCRIPT")
    if [[ "$first_line" == "#!/usr/bin/env bash" ]]; then
        pass "teardown.sh has correct shebang (#!/usr/bin/env bash)"
    else
        fail "teardown.sh shebang is '$first_line', expected '#!/usr/bin/env bash'"
    fi
}

# ---------------------------------------------------------------------------
# Test 3: Script uses strict mode (set -euo pipefail)
# ---------------------------------------------------------------------------
test_strict_mode() {
    if grep -q 'set -euo pipefail' "$TEARDOWN_SCRIPT"; then
        pass "teardown.sh uses strict mode (set -euo pipefail)"
    else
        fail "teardown.sh does not use 'set -euo pipefail'"
    fi
}

# ---------------------------------------------------------------------------
# Test 4: Script has valid bash syntax
# ---------------------------------------------------------------------------
test_syntax_valid() {
    if bash -n "$TEARDOWN_SCRIPT" 2>/dev/null; then
        pass "teardown.sh has valid bash syntax"
    else
        fail "teardown.sh has syntax errors"
    fi
}

# ---------------------------------------------------------------------------
# Test 5: Script accepts TEST_DIR as first argument
# ---------------------------------------------------------------------------
test_test_dir_argument() {
    if grep -q 'TEST_DIR="\${1:-' "$TEARDOWN_SCRIPT"; then
        pass "teardown.sh accepts TEST_DIR as first argument with default"
    else
        fail "teardown.sh does not properly handle TEST_DIR argument"
    fi
}

# ---------------------------------------------------------------------------
# Test 6: Script defaults to /tmp/loom-frontend-e2e
# ---------------------------------------------------------------------------
test_default_test_dir() {
    if grep -q '/tmp/loom-frontend-e2e' "$TEARDOWN_SCRIPT"; then
        pass "teardown.sh defaults to /tmp/loom-frontend-e2e"
    else
        fail "teardown.sh does not use expected default TEST_DIR"
    fi
}

# ---------------------------------------------------------------------------
# Test 7: Script exits 0 when TEST_DIR doesn't exist (idempotent)
# ---------------------------------------------------------------------------
test_idempotent_nonexistent_dir() {
    # Test with a directory that definitely doesn't exist
    local nonexistent_dir="/tmp/loom-test-dir-does-not-exist-$(date +%s)"
    if bash "$TEARDOWN_SCRIPT" "$nonexistent_dir" >/dev/null 2>&1; then
        pass "teardown.sh exits 0 when TEST_DIR doesn't exist (idempotent)"
    else
        fail "teardown.sh fails when TEST_DIR doesn't exist (should be idempotent)"
    fi
}

# ---------------------------------------------------------------------------
# Test 8: Script attempts to stop daemon if PID file exists
# ---------------------------------------------------------------------------
test_daemon_shutdown() {
    if grep -q 'daemon.pid' "$TEARDOWN_SCRIPT"; then
        pass "teardown.sh checks for daemon.pid"
    else
        fail "teardown.sh does not check for daemon.pid"
    fi
}

# ---------------------------------------------------------------------------
# Test 9: Script attempts to kill orphaned agent processes
# ---------------------------------------------------------------------------
test_orphaned_agents() {
    if grep -q '.agent.lock' "$TEARDOWN_SCRIPT"; then
        pass "teardown.sh checks for orphaned agent processes"
    else
        fail "teardown.sh does not check for .agent.lock files"
    fi
}

# ---------------------------------------------------------------------------
# Test 10: Script removes git worktrees before deleting directory
# ---------------------------------------------------------------------------
test_worktree_removal() {
    if grep -q 'git worktree remove' "$TEARDOWN_SCRIPT"; then
        pass "teardown.sh removes git worktrees before cleanup"
    else
        fail "teardown.sh does not remove git worktrees"
    fi
}

# ---------------------------------------------------------------------------
# Test 11: Script uses force flag for worktree removal
# ---------------------------------------------------------------------------
test_worktree_force_flag() {
    if grep -q 'git worktree remove --force' "$TEARDOWN_SCRIPT"; then
        pass "teardown.sh uses --force flag for worktree removal"
    else
        fail "teardown.sh does not use --force flag for worktree removal"
    fi
}

# ---------------------------------------------------------------------------
# Test 12: Script removes falcon and nova worktrees
# ---------------------------------------------------------------------------
test_specific_worktrees() {
    if grep -q 'falcon' "$TEARDOWN_SCRIPT" && grep -q 'nova' "$TEARDOWN_SCRIPT"; then
        pass "teardown.sh removes falcon and nova worktrees"
    else
        fail "teardown.sh does not remove expected worktrees (falcon, nova)"
    fi
}

# ---------------------------------------------------------------------------
# Test 13: Script uses rm -rf for final cleanup
# ---------------------------------------------------------------------------
test_rm_cleanup() {
    if grep -q 'rm -rf.*TEST_DIR' "$TEARDOWN_SCRIPT"; then
        pass "teardown.sh uses rm -rf for final cleanup"
    else
        fail "teardown.sh does not use rm -rf for cleanup"
    fi
}

# ---------------------------------------------------------------------------
# Test 14: Script gracefully handles daemon stop failures
# ---------------------------------------------------------------------------
test_graceful_daemon_stop() {
    if grep -q '|| true' "$TEARDOWN_SCRIPT"; then
        pass "teardown.sh gracefully handles command failures"
    else
        fail "teardown.sh does not use '|| true' for graceful failures"
    fi
}

# ---------------------------------------------------------------------------
# Run all tests
# ---------------------------------------------------------------------------

echo "=== teardown.sh test suite ==="
echo ""

test_script_exists_and_executable
test_shebang
test_strict_mode
test_syntax_valid
test_test_dir_argument
test_default_test_dir
test_idempotent_nonexistent_dir
test_daemon_shutdown
test_orphaned_agents
test_worktree_removal
test_worktree_force_flag
test_specific_worktrees
test_rm_cleanup
test_graceful_daemon_stop

echo ""
echo "=== Results: $PASS_COUNT passed, $FAIL_COUNT failed ==="

if [[ "$FAIL_COUNT" -gt 0 ]]; then
    exit 1
fi
