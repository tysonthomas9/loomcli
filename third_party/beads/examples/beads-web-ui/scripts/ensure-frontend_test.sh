#!/usr/bin/env bash
#
# ensure-frontend_test.sh - Tests for ensure-frontend.sh
#
# Creates a temporary directory structure mimicking the frontend layout,
# uses a mock npm command, and verifies each rebuild/skip scenario.
#
set -uo pipefail

SCRIPT_UNDER_TEST="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/ensure-frontend.sh"

PASS=0
FAIL=0
TOTAL=0

# ---------- helpers ----------

setup_temp() {
    TEST_ROOT="$(mktemp -d)"
    # The script resolves PROJECT_DIR as SCRIPT_DIR/.. so we need:
    #   TEST_ROOT/scripts/ensure-frontend.sh  (copy of script)
    #   TEST_ROOT/frontend/                   (the frontend tree)
    mkdir -p "$TEST_ROOT/scripts"
    mkdir -p "$TEST_ROOT/frontend/src"

    # Minimal package.json
    echo '{"name":"test"}' > "$TEST_ROOT/frontend/package.json"

    # Copy the script under test into the temp project
    cp "$SCRIPT_UNDER_TEST" "$TEST_ROOT/scripts/ensure-frontend.sh"
    chmod +x "$TEST_ROOT/scripts/ensure-frontend.sh"

    # Create a mock npm that logs calls to a file and creates dist/index.html
    # when "npm run build" is invoked.
    MOCK_NPM_LOG="$TEST_ROOT/npm_calls.log"
    : > "$MOCK_NPM_LOG"

    MOCK_BIN="$TEST_ROOT/bin"
    mkdir -p "$MOCK_BIN"
    cat > "$MOCK_BIN/npm" <<'MOCKNPM'
#!/usr/bin/env bash
LOG_FILE="__LOG__"
echo "$*" >> "$LOG_FILE"
# Simulate npm run build: create dist/index.html
if [ "${1:-}" = "run" ] && [ "${2:-}" = "build" ]; then
    FRONTEND_DIR="$(pwd)"
    mkdir -p "$FRONTEND_DIR/dist"
    echo "<html></html>" > "$FRONTEND_DIR/dist/index.html"
fi
# Simulate npm install: create node_modules/.package-lock.json
if [ "${1:-}" = "install" ]; then
    FRONTEND_DIR="$(pwd)"
    mkdir -p "$FRONTEND_DIR/node_modules"
    echo "{}" > "$FRONTEND_DIR/node_modules/.package-lock.json"
fi
MOCKNPM
    # Patch the log path into the mock
    sed -i "s|__LOG__|$MOCK_NPM_LOG|g" "$MOCK_BIN/npm"
    chmod +x "$MOCK_BIN/npm"
}

teardown_temp() {
    rm -rf "$TEST_ROOT"
}

run_script() {
    # Run with mock npm on PATH, capture stdout+stderr and exit code.
    PATH="$MOCK_BIN:$PATH" bash "$TEST_ROOT/scripts/ensure-frontend.sh" 2>&1
}

run_script_rc() {
    PATH="$MOCK_BIN:$PATH" bash "$TEST_ROOT/scripts/ensure-frontend.sh" 2>&1
    echo $?
}

assert_contains() {
    local label="$1" haystack="$2" needle="$3"
    TOTAL=$((TOTAL + 1))
    if echo "$haystack" | grep -qF "$needle"; then
        PASS=$((PASS + 1))
        echo "  PASS: $label"
    else
        FAIL=$((FAIL + 1))
        echo "  FAIL: $label"
        echo "        expected output to contain: $needle"
        echo "        got: $haystack"
    fi
}

assert_not_contains() {
    local label="$1" haystack="$2" needle="$3"
    TOTAL=$((TOTAL + 1))
    if echo "$haystack" | grep -qF "$needle"; then
        FAIL=$((FAIL + 1))
        echo "  FAIL: $label"
        echo "        expected output NOT to contain: $needle"
        echo "        got: $haystack"
    else
        PASS=$((PASS + 1))
        echo "  PASS: $label"
    fi
}

assert_file_contains() {
    local label="$1" file="$2" needle="$3"
    TOTAL=$((TOTAL + 1))
    if [ -f "$file" ] && grep -qF "$needle" "$file"; then
        PASS=$((PASS + 1))
        echo "  PASS: $label"
    else
        FAIL=$((FAIL + 1))
        echo "  FAIL: $label"
        echo "        expected $file to contain: $needle"
        if [ -f "$file" ]; then
            echo "        contents: $(cat "$file")"
        else
            echo "        file does not exist"
        fi
    fi
}

assert_file_not_contains() {
    local label="$1" file="$2" needle="$3"
    TOTAL=$((TOTAL + 1))
    if [ -f "$file" ] && grep -qF "$needle" "$file"; then
        FAIL=$((FAIL + 1))
        echo "  FAIL: $label"
        echo "        expected $file NOT to contain: $needle"
    else
        PASS=$((PASS + 1))
        echo "  PASS: $label"
    fi
}

assert_exit_code() {
    local label="$1" expected="$2" actual="$3"
    TOTAL=$((TOTAL + 1))
    if [ "$actual" -eq "$expected" ]; then
        PASS=$((PASS + 1))
        echo "  PASS: $label"
    else
        FAIL=$((FAIL + 1))
        echo "  FAIL: $label (expected exit $expected, got $actual)"
    fi
}

# ---------- test cases ----------

test_missing_npm() {
    echo "--- Test: missing npm produces error ---"
    setup_temp

    # Build a minimal PATH that has core utilities (bash, grep, find, etc.)
    # but does NOT include our mock npm or real npm.
    local safe_bin
    safe_bin="$(mktemp -d)"
    # Symlink essential commands so the script can run
    for cmd in bash dirname cd pwd cat find; do
        local cmd_path
        cmd_path="$(command -v "$cmd" 2>/dev/null)" || true
        if [ -n "$cmd_path" ] && [ -x "$cmd_path" ]; then
            ln -sf "$cmd_path" "$safe_bin/$cmd"
        fi
    done
    # Also need coreutils that the script depends on
    for cmd in test echo mkdir touch sed grep; do
        local cmd_path
        cmd_path="$(command -v "$cmd" 2>/dev/null)" || true
        if [ -n "$cmd_path" ] && [ -x "$cmd_path" ]; then
            ln -sf "$cmd_path" "$safe_bin/$cmd"
        fi
    done

    local output rc
    output=$(PATH="$safe_bin" bash "$TEST_ROOT/scripts/ensure-frontend.sh" 2>&1; echo "EXIT:$?")
    rc=$(echo "$output" | tail -1 | grep -oP 'EXIT:\K[0-9]+')
    output=$(echo "$output" | sed '/^EXIT:/d')

    assert_contains "prints npm error" "$output" "npm is required"
    assert_exit_code "exits non-zero" 1 "$rc"

    rm -rf "$safe_bin"
    teardown_temp
}

test_missing_node_modules() {
    echo "--- Test: missing node_modules triggers npm install ---"
    setup_temp

    # No node_modules directory exists, dist also missing
    local output
    output=$(run_script)

    assert_file_contains "npm install was called" "$MOCK_NPM_LOG" "install"
    assert_file_contains "npm run build was called" "$MOCK_NPM_LOG" "run build"
    assert_contains "prints install message" "$output" "Installing frontend dependencies"
    assert_contains "prints build message" "$output" "building"

    teardown_temp
}

test_stale_node_modules() {
    echo "--- Test: package.json newer than lock marker triggers npm install ---"
    setup_temp

    # Create node_modules with lock marker, then touch package.json to be newer
    mkdir -p "$TEST_ROOT/frontend/node_modules"
    echo "{}" > "$TEST_ROOT/frontend/node_modules/.package-lock.json"
    sleep 0.1
    touch "$TEST_ROOT/frontend/package.json"

    local output
    output=$(run_script)

    assert_file_contains "npm install was called" "$MOCK_NPM_LOG" "install"
    assert_contains "prints install message" "$output" "Installing frontend dependencies"

    teardown_temp
}

test_missing_dist() {
    echo "--- Test: missing dist/index.html triggers build ---"
    setup_temp

    # Provide node_modules so install is skipped, but no dist/
    mkdir -p "$TEST_ROOT/frontend/node_modules"
    echo "{}" > "$TEST_ROOT/frontend/node_modules/.package-lock.json"
    # Make lock marker newer than package.json
    sleep 0.1
    touch "$TEST_ROOT/frontend/node_modules/.package-lock.json"

    local output
    output=$(run_script)

    assert_file_not_contains "npm install was NOT called" "$MOCK_NPM_LOG" "install"
    assert_file_contains "npm run build was called" "$MOCK_NPM_LOG" "run build"
    assert_contains "prints dist missing message" "$output" "Frontend dist missing"

    teardown_temp
}

test_stale_src_triggers_rebuild() {
    echo "--- Test: src file newer than dist/index.html triggers rebuild ---"
    setup_temp

    # Set up node_modules (fresh) and dist (existing)
    mkdir -p "$TEST_ROOT/frontend/node_modules"
    echo "{}" > "$TEST_ROOT/frontend/node_modules/.package-lock.json"
    sleep 0.1
    touch "$TEST_ROOT/frontend/node_modules/.package-lock.json"

    mkdir -p "$TEST_ROOT/frontend/dist"
    echo "<html></html>" > "$TEST_ROOT/frontend/dist/index.html"
    # Make sure package.json is older than dist
    touch -t 202001010000 "$TEST_ROOT/frontend/package.json"
    sleep 0.1

    # Now create a src file that is newer than dist/index.html
    sleep 0.1
    echo "console.log('new')" > "$TEST_ROOT/frontend/src/app.tsx"

    local output
    output=$(run_script)

    assert_file_not_contains "npm install was NOT called" "$MOCK_NPM_LOG" "install"
    assert_file_contains "npm run build was called" "$MOCK_NPM_LOG" "run build"
    assert_contains "prints sources changed" "$output" "Frontend sources changed"

    teardown_temp
}

test_stale_package_json_triggers_rebuild() {
    echo "--- Test: package.json newer than dist/index.html triggers rebuild ---"
    setup_temp

    # Make src dir and all contents old
    touch -t 202001010000 "$TEST_ROOT/frontend/src"

    # Set up fresh node_modules
    mkdir -p "$TEST_ROOT/frontend/node_modules"
    echo "{}" > "$TEST_ROOT/frontend/node_modules/.package-lock.json"

    # Create dist with a specific old-ish timestamp
    mkdir -p "$TEST_ROOT/frontend/dist"
    echo "<html></html>" > "$TEST_ROOT/frontend/dist/index.html"
    touch -t 202501010000 "$TEST_ROOT/frontend/dist/index.html"

    # Make package.json newer than dist/index.html
    touch -t 202601010000 "$TEST_ROOT/frontend/package.json"

    # Make lock marker newest so install is skipped
    touch "$TEST_ROOT/frontend/node_modules/.package-lock.json"

    local output
    output=$(run_script)

    assert_file_not_contains "npm install was NOT called" "$MOCK_NPM_LOG" "install"
    assert_file_contains "npm run build was called" "$MOCK_NPM_LOG" "run build"
    assert_contains "prints package.json changed" "$output" "package.json changed"

    teardown_temp
}

test_up_to_date() {
    echo "--- Test: everything up to date prints skip message ---"
    setup_temp

    # Set up fresh node_modules
    mkdir -p "$TEST_ROOT/frontend/node_modules"
    echo "{}" > "$TEST_ROOT/frontend/node_modules/.package-lock.json"

    # Set package.json to be old
    touch -t 202001010000 "$TEST_ROOT/frontend/package.json"
    # Set src files to be old
    touch -t 202001010000 "$TEST_ROOT/frontend/src/placeholder"
    sleep 0.1

    # Make lock marker newer than package.json
    touch "$TEST_ROOT/frontend/node_modules/.package-lock.json"

    # Create dist/index.html newer than everything
    mkdir -p "$TEST_ROOT/frontend/dist"
    sleep 0.1
    touch "$TEST_ROOT/frontend/dist/index.html"

    local output
    output=$(run_script)

    assert_file_not_contains "npm install was NOT called" "$MOCK_NPM_LOG" "install"
    assert_file_not_contains "npm run build was NOT called" "$MOCK_NPM_LOG" "run build"
    assert_contains "prints up to date" "$output" "Frontend is up to date."

    teardown_temp
}

test_install_then_build() {
    echo "--- Test: missing node_modules AND missing dist triggers install then build ---"
    setup_temp

    # No node_modules, no dist -- both should happen
    local output
    output=$(run_script)

    assert_file_contains "npm install was called" "$MOCK_NPM_LOG" "install"
    assert_file_contains "npm run build was called" "$MOCK_NPM_LOG" "run build"

    # Verify install comes before build in the log
    local install_line build_line
    install_line=$(grep -n "install" "$MOCK_NPM_LOG" | head -1 | cut -d: -f1)
    build_line=$(grep -n "run build" "$MOCK_NPM_LOG" | head -1 | cut -d: -f1)
    TOTAL=$((TOTAL + 1))
    if [ "$install_line" -lt "$build_line" ]; then
        PASS=$((PASS + 1))
        echo "  PASS: install runs before build"
    else
        FAIL=$((FAIL + 1))
        echo "  FAIL: install should run before build"
    fi

    teardown_temp
}

# ---------- run all tests ----------

echo "=============================="
echo " ensure-frontend.sh tests"
echo "=============================="
echo

test_missing_npm
echo
test_missing_node_modules
echo
test_stale_node_modules
echo
test_missing_dist
echo
test_stale_src_triggers_rebuild
echo
test_stale_package_json_triggers_rebuild
echo
test_up_to_date
echo
test_install_then_build

echo
echo "=============================="
echo " Results: $PASS passed, $FAIL failed, $TOTAL total"
echo "=============================="

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
exit 0
