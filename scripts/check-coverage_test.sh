#!/usr/bin/env bash
# check-coverage_test.sh - Tests for check-coverage.sh
#
# Validates the check-coverage.sh script's behavior including missing profile
# handling, pass/fail threshold logic, and env var overrides. Uses a mock for
# `go tool cover -func` to avoid needing real Go packages.
#
# Usage: ./scripts/check-coverage_test.sh
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
# Setup: create a temp directory for mock binaries and fake profiles
# ---------------------------------------------------------------------------
TEST_TMPDIR=$(mktemp -d)

cleanup() {
    rm -rf "$TEST_TMPDIR"
}
trap cleanup EXIT

# Create a mock `go` binary that intercepts `go tool cover -func=...`
# and outputs a fake coverage summary with a configurable total percentage.
#
# The mock reads the total percentage from the coverage profile's second line
# (we embed the desired percentage there as a comment for the mock to extract).
MOCK_BIN_DIR="$TEST_TMPDIR/bin"
mkdir -p "$MOCK_BIN_DIR"

cat > "$MOCK_BIN_DIR/go" <<'MOCKEOF'
#!/usr/bin/env bash
# Mock go binary - only handles: go tool cover -func=<profile>
if [[ "${1:-}" == "tool" && "${2:-}" == "cover" && "${3:-}" == -func=* ]]; then
    profile="${3#-func=}"
    # Read the desired total percentage from the second line of the profile.
    # Our fake profiles embed it as: # MOCK_TOTAL=75.8
    total=$(sed -n '2s/^# MOCK_TOTAL=//p' "$profile")
    if [[ -n "$total" ]]; then
        echo "some/pkg/file.go:10:	SomeFunc	100.0%"
        echo "total:	(statements)	${total}%"
        exit 0
    fi
fi
# For anything else, delegate to the real go
echo "mock go: unhandled command: $*" >&2
exit 1
MOCKEOF
chmod +x "$MOCK_BIN_DIR/go"

# Helper: create a fake coverage profile with a given total percentage.
make_profile() {
    local pct="$1"
    local path="$TEST_TMPDIR/coverage_${pct}.out"
    cat > "$path" <<EOF
mode: atomic
# MOCK_TOTAL=${pct}
some/package/file.go:1.1,2.1 1 1
EOF
    echo "$path"
}

# ---------------------------------------------------------------------------
# Test 1: Missing profile file exits 1 with error message
# ---------------------------------------------------------------------------
test_missing_profile() {
    local output
    output=$("$SCRIPT_DIR/check-coverage.sh" "$TEST_TMPDIR/nonexistent.out" 70 2>&1) || true
    local rc=$?

    # The script uses set -euo pipefail and exits 1 on missing file.
    # Capture exit code by running in a subshell.
    local exit_code
    ("$SCRIPT_DIR/check-coverage.sh" "$TEST_TMPDIR/nonexistent.out" 70 >/dev/null 2>&1) && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -ne 0 ]]; then
        pass "Missing profile exits non-zero (exit $exit_code)"
    else
        fail "Missing profile should exit non-zero but exited 0"
        return
    fi

    if echo "$output" | grep -q "Coverage profile not found"; then
        pass "Missing profile prints 'Coverage profile not found' error"
    else
        fail "Missing profile error message missing expected text. Got: $output"
    fi
}

# ---------------------------------------------------------------------------
# Test 2: Coverage above threshold exits 0 (PASS)
# ---------------------------------------------------------------------------
test_coverage_above_threshold() {
    local profile
    profile=$(make_profile "85.3")

    local output exit_code
    output=$(PATH="$MOCK_BIN_DIR:$PATH" "$SCRIPT_DIR/check-coverage.sh" "$profile" 70 2>&1) && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -eq 0 ]]; then
        pass "Coverage above threshold exits 0"
    else
        fail "Coverage above threshold should exit 0 but exited $exit_code. Output: $output"
        return
    fi

    if echo "$output" | grep -q "PASS"; then
        pass "Coverage above threshold prints PASS message"
    else
        fail "Coverage above threshold output missing PASS. Got: $output"
    fi
}

# ---------------------------------------------------------------------------
# Test 3: Coverage below threshold exits 1 (FAIL)
# ---------------------------------------------------------------------------
test_coverage_below_threshold() {
    local profile
    profile=$(make_profile "42.1")

    local output exit_code
    output=$(PATH="$MOCK_BIN_DIR:$PATH" "$SCRIPT_DIR/check-coverage.sh" "$profile" 70 2>&1) && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -ne 0 ]]; then
        pass "Coverage below threshold exits non-zero (exit $exit_code)"
    else
        fail "Coverage below threshold should exit non-zero but exited 0. Output: $output"
        return
    fi

    if echo "$output" | grep -q "FAIL"; then
        pass "Coverage below threshold prints FAIL message"
    else
        fail "Coverage below threshold output missing FAIL. Got: $output"
    fi
}

# ---------------------------------------------------------------------------
# Test 4: Coverage exactly at threshold exits 0 (PASS)
# ---------------------------------------------------------------------------
test_coverage_at_threshold() {
    local profile
    profile=$(make_profile "70.0")

    local output exit_code
    output=$(PATH="$MOCK_BIN_DIR:$PATH" "$SCRIPT_DIR/check-coverage.sh" "$profile" 70 2>&1) && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -eq 0 ]]; then
        pass "Coverage exactly at threshold exits 0"
    else
        fail "Coverage exactly at threshold should exit 0 but exited $exit_code. Output: $output"
    fi
}

# ---------------------------------------------------------------------------
# Test 5: COVERAGE_PROFILE env var overrides default profile path
# ---------------------------------------------------------------------------
test_env_var_profile() {
    local profile
    profile=$(make_profile "80.0")

    local output exit_code
    output=$(PATH="$MOCK_BIN_DIR:$PATH" COVERAGE_PROFILE="$profile" "$SCRIPT_DIR/check-coverage.sh" 2>&1) && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -eq 0 ]]; then
        pass "COVERAGE_PROFILE env var selects correct profile"
    else
        fail "COVERAGE_PROFILE env var failed. Exit $exit_code. Output: $output"
    fi
}

# ---------------------------------------------------------------------------
# Test 6: COVERAGE_THRESHOLD env var overrides default threshold
# ---------------------------------------------------------------------------
test_env_var_threshold() {
    local profile
    profile=$(make_profile "55.0")

    # With default threshold 70, 55% would fail. Set threshold to 50 via env var.
    local output exit_code
    output=$(PATH="$MOCK_BIN_DIR:$PATH" COVERAGE_THRESHOLD=50 "$SCRIPT_DIR/check-coverage.sh" "$profile" 2>&1) && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -eq 0 ]]; then
        pass "COVERAGE_THRESHOLD env var lowers threshold (55% >= 50%)"
    else
        fail "COVERAGE_THRESHOLD env var did not lower threshold. Exit $exit_code. Output: $output"
        return
    fi

    # Now verify the same coverage fails with a higher env-var threshold.
    output=$(PATH="$MOCK_BIN_DIR:$PATH" COVERAGE_THRESHOLD=60 "$SCRIPT_DIR/check-coverage.sh" "$profile" 2>&1) && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -ne 0 ]]; then
        pass "COVERAGE_THRESHOLD env var raises threshold (55% < 60%)"
    else
        fail "COVERAGE_THRESHOLD=60 should have failed for 55% coverage. Output: $output"
    fi
}

# ---------------------------------------------------------------------------
# Test 7: Positional args take precedence over env vars
# ---------------------------------------------------------------------------
test_args_override_env() {
    local profile_good profile_bad
    profile_good=$(make_profile "90.0")
    profile_bad=$(make_profile "10.0")

    # Env var points to bad profile, positional arg points to good profile.
    # Positional arg should win.
    local output exit_code
    output=$(PATH="$MOCK_BIN_DIR:$PATH" COVERAGE_PROFILE="$profile_bad" "$SCRIPT_DIR/check-coverage.sh" "$profile_good" 70 2>&1) && exit_code=0 || exit_code=$?

    if [[ "$exit_code" -eq 0 ]]; then
        pass "Positional arg overrides COVERAGE_PROFILE env var"
    else
        fail "Positional arg should override env var. Exit $exit_code. Output: $output"
    fi
}

# ---------------------------------------------------------------------------
# Run all tests
# ---------------------------------------------------------------------------

echo "=== check-coverage.sh test suite ==="
echo ""

test_missing_profile
test_coverage_above_threshold
test_coverage_below_threshold
test_coverage_at_threshold
test_env_var_profile
test_env_var_threshold
test_args_override_env

echo ""
echo "=== Results: $PASS_COUNT passed, $FAIL_COUNT failed ==="

if [[ "$FAIL_COUNT" -gt 0 ]]; then
    exit 1
fi
