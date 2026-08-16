#!/usr/bin/env bash
# check-import-fanout_test.sh - Tests for check-import-fanout.sh
#
# Creates a mock `go` binary that simulates `go list` output, then verifies
# the script's behavior across scenarios.
#
# Usage: ./scripts/check-import-fanout_test.sh
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

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
# Setup: create a temporary fake repo with a mock `go` binary
# ---------------------------------------------------------------------------
TEST_TMPDIR=$(mktemp -d)

cleanup() {
    rm -rf "$TEST_TMPDIR"
}
trap cleanup EXIT

FAKE_REPO="$TEST_TMPDIR/repo"
FAKE_SCRIPTS="$FAKE_REPO/scripts"
FAKE_BIN="$FAKE_REPO/bin"
mkdir -p "$FAKE_SCRIPTS" "$FAKE_BIN"

cp "$SCRIPT_DIR/check-import-fanout.sh" "$FAKE_SCRIPTS/check-import-fanout.sh"
chmod +x "$FAKE_SCRIPTS/check-import-fanout.sh"
# Most cases exercise the default ceiling. A header-only ratchet is valid and
# prevents production exception rows from referring to packages absent from
# the fake go-list fixture.
printf '# package\texact_count\trationale\n' > "$FAKE_SCRIPTS/import-fanout-exceptions.tsv"

SCRIPT_UNDER_TEST="$FAKE_SCRIPTS/check-import-fanout.sh"

# Mock go binary config file — tests write their desired output here.
MOCK_MODULE_FILE="$FAKE_REPO/mock_module.txt"
MOCK_LIST_FILE="$FAKE_REPO/mock_list.txt"
MOCK_LIST_EXIT="$FAKE_REPO/mock_list_exit.txt"

# Create the mock `go` binary.
cat > "$FAKE_BIN/go" << 'MOCKEOF'
#!/usr/bin/env bash
REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"

if [[ "$1" == "list" && "$2" == "-m" ]]; then
    cat "$REPO_DIR/mock_module.txt"
    exit 0
fi

if [[ "$1" == "list" && "$2" == "-f" ]]; then
    exit_code=0
    if [[ -f "$REPO_DIR/mock_list_exit.txt" ]]; then
        exit_code=$(cat "$REPO_DIR/mock_list_exit.txt")
    fi
    if [[ "$exit_code" -ne 0 ]]; then
        exit "$exit_code"
    fi
    cat "$REPO_DIR/mock_list.txt"
    exit 0
fi

echo "mock go: unexpected command: $*" >&2
exit 1
MOCKEOF
chmod +x "$FAKE_BIN/go"

# Helper: configure mock go output and run the script.
run_script() {
    local module="$1"
    local list_output="$2"
    local threshold="${3:-}"
    local list_exit="${4:-0}"

    echo "$module" > "$MOCK_MODULE_FILE"
    echo "$list_output" > "$MOCK_LIST_FILE"
    echo "$list_exit" > "$MOCK_LIST_EXIT"

    local output exit_code
    if [[ -n "$threshold" ]]; then
        output=$(PATH="$FAKE_BIN:$PATH" "$SCRIPT_UNDER_TEST" "$threshold" 2>&1) && exit_code=0 || exit_code=$?
    else
        output=$(PATH="$FAKE_BIN:$PATH" "$SCRIPT_UNDER_TEST" 2>&1) && exit_code=0 || exit_code=$?
    fi

    echo "$output"
    return "$exit_code"
}

# ---------------------------------------------------------------------------
# Test 1: Under threshold → exit 0
# ---------------------------------------------------------------------------
test_under_threshold() {
    local list_output
    list_output="example.com/testmod/internal/alpha example.com/testmod/internal/beta,example.com/testmod/internal/gamma,fmt
example.com/testmod/internal/beta example.com/testmod/internal/gamma,fmt,os
example.com/testmod/internal/gamma fmt"

    local output exit_code
    output=$(run_script "example.com/testmod" "$list_output" 10) && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -eq 0 ]]; then
        pass "Under threshold: exit 0"
    else
        fail "Under threshold should exit 0, got $exit_code. Output: $output"
    fi
}

# ---------------------------------------------------------------------------
# Test 2: Over threshold → exit 1 with correct output
# ---------------------------------------------------------------------------
test_over_threshold() {
    # Package with 15 internal imports, threshold 10.
    local imports=""
    for ((i = 1; i <= 15; i++)); do
        [[ -n "$imports" ]] && imports+=","
        imports+="example.com/testmod/internal/dep$i"
    done
    local list_output="example.com/testmod/internal/big $imports"

    local output exit_code
    output=$(run_script "example.com/testmod" "$list_output" 10) && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -eq 1 ]]; then
        pass "Over threshold: exit 1"
    else
        fail "Over threshold should exit 1, got $exit_code. Output: $output"
        return
    fi

    if echo "$output" | grep -q "internal/big"; then
        pass "Over threshold: output lists offending package"
    else
        fail "Over threshold: output should list internal/big. Got: $output"
    fi

    if echo "$output" | grep -q "15"; then
        pass "Over threshold: output shows count"
    else
        fail "Over threshold: output should show count 15. Got: $output"
    fi
}

# ---------------------------------------------------------------------------
# Test 3: Multiple violations → both listed, sorted descending
# ---------------------------------------------------------------------------
test_multiple_violations() {
    # huge: 20 internal imports, big: 12, small: 3
    local huge_imports="" big_imports=""
    for ((i = 1; i <= 20; i++)); do
        [[ -n "$huge_imports" ]] && huge_imports+=","
        huge_imports+="example.com/testmod/internal/dep$i"
    done
    for ((i = 1; i <= 12; i++)); do
        [[ -n "$big_imports" ]] && big_imports+=","
        big_imports+="example.com/testmod/internal/dep$i"
    done

    local list_output="example.com/testmod/internal/huge $huge_imports
example.com/testmod/internal/big $big_imports
example.com/testmod/internal/small example.com/testmod/internal/dep1,example.com/testmod/internal/dep2,example.com/testmod/internal/dep3"

    local output exit_code
    output=$(run_script "example.com/testmod" "$list_output" 10) && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -eq 1 ]]; then
        pass "Multiple violations: exit 1"
    else
        fail "Multiple violations should exit 1, got $exit_code. Output: $output"
        return
    fi

    if echo "$output" | grep -q "internal/huge" && echo "$output" | grep -q "internal/big"; then
        pass "Multiple violations: both packages listed"
    else
        fail "Multiple violations should list both packages. Got: $output"
        return
    fi

    # Verify sorted descending: huge (20) should appear before big (12).
    local huge_line big_line
    huge_line=$(echo "$output" | grep -n "internal/huge" | head -1 | cut -d: -f1)
    big_line=$(echo "$output" | grep -n "internal/big" | head -1 | cut -d: -f1)
    if [[ "$huge_line" -lt "$big_line" ]]; then
        pass "Multiple violations: sorted descending by count"
    else
        fail "Multiple violations: should be sorted descending. huge line=$huge_line, big line=$big_line"
    fi

    # small (3) should NOT be in violations.
    if ! echo "$output" | grep -q "internal/small"; then
        pass "Multiple violations: under-threshold package not listed"
    else
        fail "Multiple violations: internal/small should not be listed. Got: $output"
    fi
}

# ---------------------------------------------------------------------------
# Test 4: Custom threshold via $1
# ---------------------------------------------------------------------------
test_custom_threshold() {
    local list_output="example.com/testmod/internal/pkg example.com/testmod/internal/a,example.com/testmod/internal/b,example.com/testmod/internal/c,example.com/testmod/internal/d,example.com/testmod/internal/e"

    local output exit_code
    output=$(run_script "example.com/testmod" "$list_output" 3) && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -eq 1 ]]; then
        pass "Custom threshold (3): 5-import package fails"
    else
        fail "Custom threshold (3): 5-import package should fail, got $exit_code. Output: $output"
    fi
}

# ---------------------------------------------------------------------------
# Test 5: Default threshold (no argument) uses 10
# ---------------------------------------------------------------------------
test_default_threshold() {
    # 11 internal imports should exceed default threshold of 10.
    local imports=""
    for ((i = 1; i <= 11; i++)); do
        [[ -n "$imports" ]] && imports+=","
        imports+="example.com/testmod/internal/dep$i"
    done
    local list_output="example.com/testmod/internal/pkg $imports"

    local output exit_code
    echo "example.com/testmod" > "$MOCK_MODULE_FILE"
    echo "$list_output" > "$MOCK_LIST_FILE"
    echo "0" > "$MOCK_LIST_EXIT"
    output=$(PATH="$FAKE_BIN:$PATH" "$SCRIPT_UNDER_TEST" 2>&1) && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -eq 1 ]]; then
        pass "Default threshold: 11-import package fails with no arg"
    else
        fail "Default threshold: 11-import package should fail, got $exit_code. Output: $output"
    fi
}

# ---------------------------------------------------------------------------
# Test 6: Self-import excluded
# ---------------------------------------------------------------------------
test_self_import_excluded() {
    # Package imports itself plus 10 others — self should be excluded, leaving 10 (not over threshold).
    local imports="example.com/testmod/internal/pkg"
    for ((i = 1; i <= 10; i++)); do
        imports+=",example.com/testmod/internal/dep$i"
    done
    local list_output="example.com/testmod/internal/pkg $imports"

    local output exit_code
    output=$(run_script "example.com/testmod" "$list_output" 10) && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -eq 0 ]]; then
        pass "Self-import excluded: 10 internal + self under threshold 10"
    else
        fail "Self-import excluded: should pass (self excluded → 10), got $exit_code. Output: $output"
    fi
}

# ---------------------------------------------------------------------------
# Test 7: Invalid threshold → exit 2
# ---------------------------------------------------------------------------
test_invalid_threshold() {
    local output exit_code
    output=$(run_script "example.com/testmod" "" "abc") && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -eq 2 ]]; then
        pass "Invalid threshold: exit 2"
    else
        fail "Invalid threshold should exit 2, got $exit_code. Output: $output"
    fi
}

# ---------------------------------------------------------------------------
# Test 8: Zero threshold → exit 2
# ---------------------------------------------------------------------------
test_zero_threshold() {
    local output exit_code
    output=$(run_script "example.com/testmod" "" "0") && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -eq 2 ]]; then
        pass "Zero threshold: exit 2"
    else
        fail "Zero threshold should exit 2, got $exit_code. Output: $output"
    fi
}

# ---------------------------------------------------------------------------
# Test 9: go list failure → exit 2 with error message
# ---------------------------------------------------------------------------
test_go_list_failure() {
    local output exit_code
    output=$(run_script "example.com/testmod" "" 10 1) && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -eq 2 ]]; then
        pass "go list failure: exit 2"
    else
        fail "go list failure should exit 2, got $exit_code. Output: $output"
        return
    fi

    if echo "$output" | grep -qi "go list failed"; then
        pass "go list failure: error message present"
    else
        fail "go list failure: should contain 'go list failed'. Got: $output"
    fi
}

# ---------------------------------------------------------------------------
# Test 10: Relative path output (not full module path)
# ---------------------------------------------------------------------------
test_relative_path_output() {
    local imports=""
    for ((i = 1; i <= 15; i++)); do
        [[ -n "$imports" ]] && imports+=","
        imports+="example.com/testmod/internal/dep$i"
    done
    local list_output="example.com/testmod/internal/foo $imports"

    local output exit_code
    output=$(run_script "example.com/testmod" "$list_output" 10) && exit_code=0 || exit_code=$?

    if echo "$output" | grep -q "internal/foo"; then
        pass "Relative path: output shows internal/foo"
    else
        fail "Relative path: output should show internal/foo. Got: $output"
        return
    fi

    if ! echo "$output" | grep -q "example.com/testmod/internal/foo"; then
        pass "Relative path: output does NOT show full module path"
    else
        fail "Relative path: output should NOT show full module path. Got: $output"
    fi
}

# ---------------------------------------------------------------------------
# Test 11: Exactly at threshold → exit 0 (boundary: > not >=)
# ---------------------------------------------------------------------------
test_exactly_at_threshold() {
    local imports=""
    for ((i = 1; i <= 10; i++)); do
        [[ -n "$imports" ]] && imports+=","
        imports+="example.com/testmod/internal/dep$i"
    done
    local list_output="example.com/testmod/internal/pkg $imports"

    local output exit_code
    output=$(run_script "example.com/testmod" "$list_output" 10) && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -eq 0 ]]; then
        pass "Exactly at threshold (10 imports): exit 0"
    else
        fail "Exactly at threshold should exit 0, got $exit_code. Output: $output"
    fi
}

# ---------------------------------------------------------------------------
# Test 12: External imports not counted
# ---------------------------------------------------------------------------
test_external_imports_not_counted() {
    # Package imports 15 external packages but only 3 internal → should pass.
    local list_output="example.com/testmod/internal/pkg example.com/testmod/internal/a,example.com/testmod/internal/b,example.com/testmod/internal/c,fmt,os,io,net/http,encoding/json,context,sync,errors,strings,bytes,path,time"

    local output exit_code
    output=$(run_script "example.com/testmod" "$list_output" 10) && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -eq 0 ]]; then
        pass "External imports not counted: 3 internal + 12 external under threshold 10"
    else
        fail "External imports not counted: should pass, got $exit_code. Output: $output"
    fi
}

# ---------------------------------------------------------------------------
# Test 13: Package with no imports → exit 0
# ---------------------------------------------------------------------------
test_no_imports() {
    # Package with only its own path (no imports — go list shows just the package path).
    local list_output="example.com/testmod/internal/empty example.com/testmod/internal/empty"

    local output exit_code
    output=$(run_script "example.com/testmod" "$list_output" 10) && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -eq 0 ]]; then
        pass "No imports: exit 0"
    else
        fail "No imports should exit 0, got $exit_code. Output: $output"
    fi
}

# ---------------------------------------------------------------------------
# Run all tests
# ---------------------------------------------------------------------------

echo "=== check-import-fanout.sh test suite ==="
echo ""

test_under_threshold
test_over_threshold
test_multiple_violations
test_custom_threshold
test_default_threshold
test_self_import_excluded
test_invalid_threshold
test_zero_threshold
test_go_list_failure
test_relative_path_output
test_exactly_at_threshold
test_external_imports_not_counted
test_no_imports

echo ""
echo "=== Results: $PASS_COUNT passed, $FAIL_COUNT failed ==="

if [[ "$FAIL_COUNT" -gt 0 ]]; then
    exit 1
fi
