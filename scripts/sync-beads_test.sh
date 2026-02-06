#!/usr/bin/env bash
# sync-beads_test.sh - Tests for sync-beads.sh
#
# Verifies the current repo state matches expectations after sync-beads.sh
# has been run. Tests the sync script's behavior including import rewriting,
# file exclusions, file preservation, full package sync, and idempotency.
#
# Usage: ./scripts/sync-beads_test.sh
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

VENDOR_DIR="$REPO_ROOT/third_party/beads/internal"
DEST_DIR="$REPO_ROOT/internal"

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
# Test 1: Import rewriting - no beads import paths in internal/
# ---------------------------------------------------------------------------
test_import_rewriting() {
    local matches
    matches=$(grep -rl "github.com/steveyegge/beads/internal" "$DEST_DIR" --include='*.go' 2>/dev/null || true)
    if [[ -z "$matches" ]]; then
        pass "No files in internal/ contain beads import path"
    else
        fail "Files still contain github.com/steveyegge/beads/internal:"
        echo "  $matches"
    fi
}

# ---------------------------------------------------------------------------
# Test 2: Loom-only files preserved (not deleted by sync)
# ---------------------------------------------------------------------------
test_loom_only_files_preserved() {
    local all_pass=true

    # mutation.go and mutation_test.go are loom-only rpc files
    for f in "rpc/mutation.go" "rpc/mutation_test.go"; do
        if [[ -f "$DEST_DIR/$f" ]]; then
            pass "Loom-only file preserved: $f"
        else
            fail "Loom-only file deleted by sync: $f"
            all_pass=false
        fi
    done
}

# ---------------------------------------------------------------------------
# Test 3: Server file exclusion in rpc/
# ---------------------------------------------------------------------------
test_server_files_excluded() {
    local found_bad=false

    # Check for server*.go files (excluding loom-only files)
    for f in "$DEST_DIR/rpc"/server*.go; do
        [[ ! -f "$f" ]] && continue
        fail "Server file should not exist in internal/rpc/: $(basename "$f")"
        found_bad=true
    done

    # Check for signals_*.go files
    for f in "$DEST_DIR/rpc"/signals_*.go; do
        [[ ! -f "$f" ]] && continue
        fail "Signals file should not exist in internal/rpc/: $(basename "$f")"
        found_bad=true
    done

    # Check for test_helpers*.go files
    for f in "$DEST_DIR/rpc"/test_helpers*.go; do
        [[ ! -f "$f" ]] && continue
        fail "Test helpers file should not exist in internal/rpc/: $(basename "$f")"
        found_bad=true
    done

    if [[ "$found_bad" == "false" ]]; then
        pass "No server*.go, signals_*.go, or test_helpers*.go in internal/rpc/"
    fi
}

# ---------------------------------------------------------------------------
# Test 4: RPC test exclusion - no beads rpc *_test.go files synced
#
# The sync script skips all *_test.go from beads rpc. Loom has its own
# rpc test files (mutation_test.go, client_test.go, etc.) which are
# loom-authored, not synced from beads.
# Verify that beads-only test files were NOT synced.
# ---------------------------------------------------------------------------
test_rpc_beads_tests_excluded() {
    local found_bad=false

    # These test files exist in beads rpc but should NOT be in internal/rpc/
    local beads_only_tests=(
        "rpc_test.go"
        "additional_coverage_test.go"
        "bench_test.go"
        "client_gate_shutdown_test.go"
        "client_selfheal_test.go"
        "comments_test.go"
        "coverage_test.go"
        "isolation_test.go"
        "limits_test.go"
        "list_filters_test.go"
        "server_delete_test.go"
        "server_issues_epics_test.go"
        "server_labels_deps_comments_test.go"
        "server_mutations_test.go"
        "status_test.go"
        "version_test.go"
        "worker_status_test.go"
    )

    for t in "${beads_only_tests[@]}"; do
        if [[ -f "$DEST_DIR/rpc/$t" ]]; then
            fail "Beads rpc test should not be synced: rpc/$t"
            found_bad=true
        fi
    done

    if [[ "$found_bad" == "false" ]]; then
        pass "No beads-only rpc test files found in internal/rpc/"
    fi
}

# ---------------------------------------------------------------------------
# Test 5: Full package sync - types/, debug/, lockfile/ all .go files synced
# ---------------------------------------------------------------------------
test_full_package_sync() {
    local all_pass=true

    for pkg in types debug lockfile; do
        if [[ ! -d "$VENDOR_DIR/$pkg" ]]; then
            fail "Source directory missing: third_party/beads/internal/$pkg"
            all_pass=false
            continue
        fi

        local missing=()
        for src_file in "$VENDOR_DIR/$pkg"/*.go; do
            [[ ! -f "$src_file" ]] && continue
            local basename
            basename="$(basename "$src_file")"
            if [[ ! -f "$DEST_DIR/$pkg/$basename" ]]; then
                missing+=("$basename")
            fi
        done

        if [[ ${#missing[@]} -eq 0 ]]; then
            pass "All beads files synced for $pkg/"
        else
            fail "Missing synced files in $pkg/: ${missing[*]}"
            all_pass=false
        fi
    done
}

# ---------------------------------------------------------------------------
# Test 5b: Full package sync includes test files
# ---------------------------------------------------------------------------
test_full_package_tests_synced() {
    local all_pass=true

    for pkg in types debug lockfile; do
        local has_tests=false
        for src_file in "$VENDOR_DIR/$pkg"/*_test.go; do
            [[ ! -f "$src_file" ]] && continue
            has_tests=true
            local basename
            basename="$(basename "$src_file")"
            if [[ ! -f "$DEST_DIR/$pkg/$basename" ]]; then
                fail "Test file not synced: $pkg/$basename"
                all_pass=false
            fi
        done

        if [[ "$has_tests" == "true" ]] && [[ "$all_pass" == "true" ]]; then
            pass "Test files synced for $pkg/"
        fi
    done
}

# ---------------------------------------------------------------------------
# Test 6: Idempotency - running sync again should produce no diff
# ---------------------------------------------------------------------------
test_idempotency() {
    # Capture the state of synced files before re-running
    local before_checksums
    before_checksums=$(find "$DEST_DIR" -name '*.go' -exec md5sum {} \; | sort)

    # Run the sync script again (suppress output).
    # The script may exit non-zero if go build fails (e.g. missing dependencies),
    # but the file sync itself should still complete. We only care whether the
    # synced file contents are identical.
    "$SCRIPT_DIR/sync-beads.sh" > /dev/null 2>&1 || true

    local after_checksums
    after_checksums=$(find "$DEST_DIR" -name '*.go' -exec md5sum {} \; | sort)

    if [[ "$before_checksums" == "$after_checksums" ]]; then
        pass "Idempotent: second sync produces identical files"
    else
        fail "Idempotency: files differ after second sync run"
        diff <(echo "$before_checksums") <(echo "$after_checksums") || true
    fi
}

# ---------------------------------------------------------------------------
# Run all tests
# ---------------------------------------------------------------------------

echo "=== sync-beads.sh test suite ==="
echo ""

test_import_rewriting
test_loom_only_files_preserved
test_server_files_excluded
test_rpc_beads_tests_excluded
test_full_package_sync
test_full_package_tests_synced
test_idempotency

echo ""
echo "=== Results: $PASS_COUNT passed, $FAIL_COUNT failed ==="

if [[ "$FAIL_COUNT" -gt 0 ]]; then
    exit 1
fi
