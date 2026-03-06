#!/usr/bin/env bash
# check-loc-stale_test.sh - Tests for check-loc-stale.sh
#
# Validates the check-loc-stale.sh script's behavior including informational
# mode, --check-stale enforcement, missing file detection, STALE_MARGIN env var
# override, and unknown flag rejection. Uses a temporary repo structure with
# fake Go files and a .loc-allowlist.
#
# Usage: ./scripts/check-loc-stale_test.sh
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
# Setup: create a temporary fake repo structure
# ---------------------------------------------------------------------------
TEST_TMPDIR=$(mktemp -d)

cleanup() {
    rm -rf "$TEST_TMPDIR"
}
trap cleanup EXIT

# The script resolves REPO_ROOT from SCRIPT_DIR/.., so we need:
#   $TEST_TMPDIR/repo/scripts/check-loc-stale.sh
#   $TEST_TMPDIR/repo/.loc-allowlist
#   $TEST_TMPDIR/repo/<fake Go files>
FAKE_REPO="$TEST_TMPDIR/repo"
FAKE_SCRIPTS="$FAKE_REPO/scripts"
mkdir -p "$FAKE_SCRIPTS"

# Copy the real script into our fake repo structure.
cp "$SCRIPT_DIR/check-loc-stale.sh" "$FAKE_SCRIPTS/check-loc-stale.sh"
chmod +x "$FAKE_SCRIPTS/check-loc-stale.sh"

SCRIPT_UNDER_TEST="$FAKE_SCRIPTS/check-loc-stale.sh"

# Helper: create a fake Go file with a given number of lines.
make_go_file() {
    local relpath="$1"
    local num_lines="$2"
    local full_path="$FAKE_REPO/$relpath"
    mkdir -p "$(dirname "$full_path")"
    # Generate num_lines lines of content.
    for ((i = 1; i <= num_lines; i++)); do
        echo "// line $i"
    done > "$full_path"
}

# Helper: write a .loc-allowlist file from arguments (each arg is one line).
write_allowlist() {
    printf '%s\n' "$@" > "$FAKE_REPO/.loc-allowlist"
}

# ---------------------------------------------------------------------------
# Test 1: Informational mode exits 0 even with stale entries
# ---------------------------------------------------------------------------
test_informational_mode_exits_0() {
    make_go_file "pkg/small.go" 100
    write_allowlist "600 pkg/small.go"

    local output exit_code
    output=$("$SCRIPT_UNDER_TEST" 2>&1) && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -eq 0 ]]; then
        pass "Informational mode exits 0 with stale entries"
    else
        fail "Informational mode should exit 0 but exited $exit_code. Output: $output"
        return
    fi

    if echo "$output" | grep -q "Stale"; then
        pass "Informational mode prints stale warning"
    else
        fail "Informational mode should print stale warning. Got: $output"
    fi
}

# ---------------------------------------------------------------------------
# Test 2: --check-stale exits 1 when stale entries exist
# ---------------------------------------------------------------------------
test_check_stale_exits_1_when_stale() {
    # 100-line file with default threshold 500 and margin 20%:
    # stale if loc*100 <= 500*(100-20) = 40000; 100*100=10000 <= 40000 -> stale
    make_go_file "pkg/small.go" 100
    write_allowlist "600 pkg/small.go"

    local output exit_code
    output=$("$SCRIPT_UNDER_TEST" --check-stale 2>&1) && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -eq 1 ]]; then
        pass "--check-stale exits 1 when stale entries exist"
    else
        fail "--check-stale should exit 1 with stale entries but exited $exit_code. Output: $output"
        return
    fi

    if echo "$output" | grep -q "can be removed"; then
        pass "--check-stale output includes 'can be removed' hint"
    else
        fail "--check-stale output missing 'can be removed'. Got: $output"
    fi
}

# ---------------------------------------------------------------------------
# Test 3: --check-stale exits 0 when no stale entries exist
# ---------------------------------------------------------------------------
test_check_stale_exits_0_when_clean() {
    # 600-line file with threshold 500 and margin 20%:
    # stale if loc*100 <= 500*80=40000; 600*100=60000 > 40000 -> NOT stale
    make_go_file "pkg/big.go" 600
    write_allowlist "700 pkg/big.go"

    local output exit_code
    output=$("$SCRIPT_UNDER_TEST" --check-stale 2>&1) && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -eq 0 ]]; then
        pass "--check-stale exits 0 when no stale entries"
    else
        fail "--check-stale should exit 0 with no stale entries but exited $exit_code. Output: $output"
    fi
}

# ---------------------------------------------------------------------------
# Test 4: Missing file detection
# ---------------------------------------------------------------------------
test_missing_file_detection() {
    # Remove the file but keep it in the allowlist.
    rm -f "$FAKE_REPO/pkg/gone.go"
    write_allowlist "500 pkg/gone.go"

    local output exit_code
    output=$("$SCRIPT_UNDER_TEST" --check-stale 2>&1) && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -eq 1 ]]; then
        pass "Missing file causes --check-stale to exit 1"
    else
        fail "Missing file should cause exit 1 but exited $exit_code. Output: $output"
        return
    fi

    if echo "$output" | grep -q "MISSING"; then
        pass "Missing file output includes MISSING marker"
    else
        fail "Missing file output should include MISSING. Got: $output"
    fi

    if echo "$output" | grep -q "file no longer exists"; then
        pass "Missing file output includes explanation"
    else
        fail "Missing file output should include 'file no longer exists'. Got: $output"
    fi
}

# ---------------------------------------------------------------------------
# Test 5: STALE_MARGIN env var changes the stale threshold
# ---------------------------------------------------------------------------
test_stale_margin_env_var() {
    # 450-line file with threshold 500.
    # Default margin 20%: stale if loc*100 <= 500*80=40000; 450*100=45000 > 40000 -> NOT stale
    # With margin 15%:    stale if loc*100 <= 500*85=42500; 45000 > 42500 -> NOT stale
    # With margin 50%:    stale if loc*100 <= 500*50=25000; 45000 > 25000 -> NOT stale
    # Need a file small enough to be stale only with a wider margin.
    #
    # 390-line file with threshold 500:
    # Default margin 20%: stale if 39000 <= 40000 -> stale
    # With margin 5%:     stale if 39000 <= 500*95=47500 -> stale (even more so)
    # With margin 25%:    stale if 39000 <= 500*75=37500 -> NOT stale
    make_go_file "pkg/medium.go" 390
    write_allowlist "500 pkg/medium.go"

    # With default margin (20%), 390 lines should be stale.
    local output exit_code
    output=$("$SCRIPT_UNDER_TEST" --check-stale 2>&1) && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -eq 1 ]]; then
        pass "Default margin (20%): 390-line file is stale at threshold 500"
    else
        fail "Default margin should make 390-line file stale. Exit $exit_code. Output: $output"
        return
    fi

    # With margin 25%, 390 lines should NOT be stale (390*100=39000 > 500*75=37500).
    output=$(STALE_MARGIN=25 "$SCRIPT_UNDER_TEST" --check-stale 2>&1) && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -eq 0 ]]; then
        pass "STALE_MARGIN=25: 390-line file is NOT stale at threshold 500"
    else
        fail "STALE_MARGIN=25 should make 390-line file non-stale. Exit $exit_code. Output: $output"
    fi
}

# ---------------------------------------------------------------------------
# Test 6: Unknown flag exits 2
# ---------------------------------------------------------------------------
test_unknown_flag_exits_2() {
    write_allowlist "500 pkg/big.go"

    local output exit_code
    output=$("$SCRIPT_UNDER_TEST" --bogus-flag 2>&1) && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -eq 2 ]]; then
        pass "Unknown flag exits 2"
    else
        fail "Unknown flag should exit 2 but exited $exit_code. Output: $output"
        return
    fi

    if echo "$output" | grep -q "Unknown flag"; then
        pass "Unknown flag prints error message"
    else
        fail "Unknown flag output missing 'Unknown flag'. Got: $output"
    fi
}

# ---------------------------------------------------------------------------
# Test 7: Comments and blank lines in allowlist are ignored
# ---------------------------------------------------------------------------
test_allowlist_comments_and_blanks() {
    make_go_file "pkg/big.go" 600
    write_allowlist "# This is a comment" "" "700 pkg/big.go" "  # Another comment"

    local output exit_code
    output=$("$SCRIPT_UNDER_TEST" --check-stale 2>&1) && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -eq 0 ]]; then
        pass "Comments and blank lines in allowlist are ignored"
    else
        fail "Comments/blanks should be ignored. Exit $exit_code. Output: $output"
    fi
}

# ---------------------------------------------------------------------------
# Run all tests
# ---------------------------------------------------------------------------

echo "=== check-loc-stale.sh test suite ==="
echo ""

test_informational_mode_exits_0
test_check_stale_exits_1_when_stale
test_check_stale_exits_0_when_clean
test_missing_file_detection
test_stale_margin_env_var
test_unknown_flag_exits_2
test_allowlist_comments_and_blanks

echo ""
echo "=== Results: $PASS_COUNT passed, $FAIL_COUNT failed ==="

if [[ "$FAIL_COUNT" -gt 0 ]]; then
    exit 1
fi
