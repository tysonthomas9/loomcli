#!/usr/bin/env bash
# dev_test.sh - Tests for dev.sh
#
# Validates the dev.sh script's structure, dependency checking,
# and configuration without actually starting the dev environment.
#
# Usage: ./scripts/dev_test.sh
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

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
# Test 1: dev.sh exists and is executable
# ---------------------------------------------------------------------------
test_script_exists_and_executable() {
    if [[ ! -f "$SCRIPT_DIR/dev.sh" ]]; then
        fail "dev.sh does not exist"
        return
    fi
    if [[ ! -x "$SCRIPT_DIR/dev.sh" ]]; then
        fail "dev.sh is not executable"
        return
    fi
    pass "dev.sh exists and is executable"
}

# ---------------------------------------------------------------------------
# Test 2: Script has correct shebang
# ---------------------------------------------------------------------------
test_shebang() {
    local first_line
    first_line=$(head -1 "$SCRIPT_DIR/dev.sh")
    if [[ "$first_line" == "#!/usr/bin/env bash" ]]; then
        pass "dev.sh has correct shebang (#!/usr/bin/env bash)"
    else
        fail "dev.sh shebang is '$first_line', expected '#!/usr/bin/env bash'"
    fi
}

# ---------------------------------------------------------------------------
# Test 3: Script uses strict mode (set -euo pipefail)
# ---------------------------------------------------------------------------
test_strict_mode() {
    if grep -q 'set -euo pipefail' "$SCRIPT_DIR/dev.sh"; then
        pass "dev.sh uses strict mode (set -euo pipefail)"
    else
        fail "dev.sh does not use 'set -euo pipefail'"
    fi
}

# ---------------------------------------------------------------------------
# Test 4: Script defines check_deps function
# ---------------------------------------------------------------------------
test_check_deps_defined() {
    if grep -q 'check_deps()' "$SCRIPT_DIR/dev.sh"; then
        pass "dev.sh defines check_deps function"
    else
        fail "dev.sh does not define check_deps function"
    fi
}

# ---------------------------------------------------------------------------
# Test 5: check_deps checks for air
# ---------------------------------------------------------------------------
test_check_deps_air() {
    if grep -q 'command -v air' "$SCRIPT_DIR/dev.sh"; then
        pass "check_deps verifies air is installed"
    else
        fail "check_deps does not check for air"
    fi
}

# ---------------------------------------------------------------------------
# Test 6: check_deps checks for node
# ---------------------------------------------------------------------------
test_check_deps_node() {
    if grep -q 'command -v node' "$SCRIPT_DIR/dev.sh"; then
        pass "check_deps verifies node is installed"
    else
        fail "check_deps does not check for node"
    fi
}

# ---------------------------------------------------------------------------
# Test 7: check_deps checks for npm
# ---------------------------------------------------------------------------
test_check_deps_npm() {
    if grep -q 'command -v npm' "$SCRIPT_DIR/dev.sh"; then
        pass "check_deps verifies npm is installed"
    else
        fail "check_deps does not check for npm"
    fi
}

# ---------------------------------------------------------------------------
# Test 8: Script defines cleanup trap
# ---------------------------------------------------------------------------
test_cleanup_trap() {
    if grep -q 'trap cleanup EXIT' "$SCRIPT_DIR/dev.sh"; then
        pass "dev.sh sets cleanup trap on EXIT"
    else
        fail "dev.sh does not trap cleanup on EXIT"
    fi
}

# ---------------------------------------------------------------------------
# Test 9: Script references correct frontend directory
# ---------------------------------------------------------------------------
test_frontend_dir() {
    if grep -q 'internal/webui/frontend' "$SCRIPT_DIR/dev.sh"; then
        pass "dev.sh references correct frontend directory"
    else
        fail "dev.sh does not reference internal/webui/frontend"
    fi
}

# ---------------------------------------------------------------------------
# Test 10: Script starts air for Go hot-reload
# ---------------------------------------------------------------------------
test_starts_air() {
    if grep -q 'air &' "$SCRIPT_DIR/dev.sh"; then
        pass "dev.sh starts air in background"
    else
        fail "dev.sh does not start air in background"
    fi
}

# ---------------------------------------------------------------------------
# Test 11: Script starts Vite dev server
# ---------------------------------------------------------------------------
test_starts_vite() {
    if grep -q 'npm run dev &' "$SCRIPT_DIR/dev.sh"; then
        pass "dev.sh starts Vite dev server in background"
    else
        fail "dev.sh does not start Vite dev server in background"
    fi
}

# ---------------------------------------------------------------------------
# Test 12: check_deps exits on missing air with install instructions
# ---------------------------------------------------------------------------
test_check_deps_air_install_hint() {
    if grep -q 'go install github.com/air-verse/air@latest' "$SCRIPT_DIR/dev.sh"; then
        pass "check_deps provides air install instructions"
    else
        fail "check_deps does not provide air install instructions"
    fi
}

# ---------------------------------------------------------------------------
# Test 13: .air.toml exists and is referenced by the dev workflow
# ---------------------------------------------------------------------------
test_air_config_exists() {
    if [[ -f "$REPO_ROOT/.air.toml" ]]; then
        pass ".air.toml config file exists at repo root"
    else
        fail ".air.toml config file missing from repo root"
    fi
}

# ---------------------------------------------------------------------------
# Test 14: .air.toml uses tmp/ as build directory (matches .gitignore)
# ---------------------------------------------------------------------------
test_air_tmp_dir() {
    if grep -q 'tmp_dir = "tmp"' "$REPO_ROOT/.air.toml"; then
        pass ".air.toml uses tmp/ as tmp_dir"
    else
        fail ".air.toml does not use tmp/ as tmp_dir"
    fi
}

# ---------------------------------------------------------------------------
# Test 15: Cleanup function kills both air and vite PIDs
# ---------------------------------------------------------------------------
test_cleanup_kills_processes() {
    local kills
    kills=$(grep -c 'kill.*PID' "$SCRIPT_DIR/dev.sh" || true)
    if [[ "$kills" -ge 2 ]]; then
        pass "cleanup function kills both air and vite PIDs"
    else
        fail "cleanup function does not kill both processes (found $kills kill statements)"
    fi
}

# ---------------------------------------------------------------------------
# Test 16: Cleanup trap is registered before background launches
# ---------------------------------------------------------------------------
test_cleanup_trap_before_background() {
    local trap_line air_line
    trap_line=$(grep -n 'trap cleanup EXIT' "$SCRIPT_DIR/dev.sh" | head -1 | cut -d: -f1)
    air_line=$(grep -n 'air &' "$SCRIPT_DIR/dev.sh" | head -1 | cut -d: -f1)
    if [[ -n "$trap_line" && -n "$air_line" && "$trap_line" -lt "$air_line" ]]; then
        pass "cleanup trap is registered before background launches"
    else
        fail "cleanup trap must be registered before air & launch (trap=$trap_line, air=$air_line)"
    fi
}

# ---------------------------------------------------------------------------
# Run all tests
# ---------------------------------------------------------------------------

echo "=== dev.sh test suite ==="
echo ""

test_script_exists_and_executable
test_shebang
test_strict_mode
test_check_deps_defined
test_check_deps_air
test_check_deps_node
test_check_deps_npm
test_cleanup_trap
test_frontend_dir
test_starts_air
test_starts_vite
test_check_deps_air_install_hint
test_air_config_exists
test_air_tmp_dir
test_cleanup_kills_processes
test_cleanup_trap_before_background

echo ""
echo "=== Results: $PASS_COUNT passed, $FAIL_COUNT failed ==="

if [[ "$FAIL_COUNT" -gt 0 ]]; then
    exit 1
fi
