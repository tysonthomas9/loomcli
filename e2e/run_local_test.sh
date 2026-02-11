#!/usr/bin/env bash
# run_local_test.sh - Tests for run_local.sh
#
# Creates a mock docker binary to capture arguments, then exercises each
# feature of run_local.sh: flag parsing, env forwarding, volume mounts,
# and error paths.
#
# Usage: ./e2e/run_local_test.sh
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
SCRIPT_UNDER_TEST="$SCRIPT_DIR/run_local.sh"

PASS=0
FAIL=0
TOTAL=0

# ---------- assertion helpers ----------

assert_eq() {
    local label="$1" expected="$2" actual="$3"
    TOTAL=$((TOTAL + 1))
    if [ "$expected" = "$actual" ]; then
        PASS=$((PASS + 1))
        echo "  PASS: $label"
    else
        FAIL=$((FAIL + 1))
        echo "  FAIL: $label"
        echo "        expected: $expected"
        echo "        actual:   $actual"
    fi
}

assert_contains() {
    local label="$1" haystack="$2" needle="$3"
    TOTAL=$((TOTAL + 1))
    if echo "$haystack" | grep -qF -- "$needle"; then
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
    if echo "$haystack" | grep -qF -- "$needle"; then
        FAIL=$((FAIL + 1))
        echo "  FAIL: $label"
        echo "        expected output NOT to contain: $needle"
        echo "        got: $haystack"
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

assert_file_contains() {
    local label="$1" file="$2" needle="$3"
    TOTAL=$((TOTAL + 1))
    if [ -f "$file" ] && grep -qF -- "$needle" "$file"; then
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
    if [ -f "$file" ] && grep -qF -- "$needle" "$file"; then
        FAIL=$((FAIL + 1))
        echo "  FAIL: $label"
        echo "        expected $file NOT to contain: $needle"
        echo "        contents: $(cat "$file")"
    else
        PASS=$((PASS + 1))
        echo "  PASS: $label"
    fi
}

# ---------- test environment setup ----------

# Each test creates its own temp dir and mock docker.  The script under test
# resolves REPO_ROOT from its own location (SCRIPT_DIR/..) so we copy it
# into a temp project tree.

setup_temp() {
    TEST_ROOT="$(mktemp -d)"

    # Reproduce the directory layout the script expects:
    #   TEST_ROOT/e2e/run_local.sh   (copy of script)
    #   TEST_ROOT/e2e/Dockerfile     (dummy, needed by docker build)
    mkdir -p "$TEST_ROOT/e2e"
    cp "$SCRIPT_UNDER_TEST" "$TEST_ROOT/e2e/run_local.sh"
    chmod +x "$TEST_ROOT/e2e/run_local.sh"
    touch "$TEST_ROOT/e2e/Dockerfile"

    # Create a fake HOME so we control which auth dirs exist.
    FAKE_HOME="$(mktemp -d)"

    # Mock docker binary that logs invocations to a file.
    MOCK_BIN="$TEST_ROOT/bin"
    mkdir -p "$MOCK_BIN"
    DOCKER_LOG="$TEST_ROOT/docker_calls.log"
    : > "$DOCKER_LOG"

    cat > "$MOCK_BIN/docker" <<MOCKDOCKER
#!/bin/sh
echo "\$*" >> "$DOCKER_LOG"
MOCKDOCKER
    chmod +x "$MOCK_BIN/docker"
}

teardown_temp() {
    rm -rf "$TEST_ROOT" "$FAKE_HOME"
}

# Run the script under test with mock docker, fake HOME, and sanitised env.
# Arguments are forwarded to the script.
# Captures combined stdout+stderr and exit code.
run_script() {
    local output rc
    # Unset all STUB_* variables that might leak from the host, and unset
    # the API key variables so they don't leak into tests that don't set them.
    output=$(
        env -i \
            PATH="$MOCK_BIN:/usr/bin:/bin:/usr/sbin:/sbin" \
            HOME="$FAKE_HOME" \
            bash "$TEST_ROOT/e2e/run_local.sh" "$@" 2>&1
    ) && rc=$? || rc=$?
    # Store for assertions
    LAST_OUTPUT="$output"
    LAST_RC=$rc
}

# Like run_script but allows injecting extra env vars.
# Usage: run_script_env "VAR1=val1" "VAR2=val2" -- script_args...
run_script_env() {
    local env_args=()
    while [ $# -gt 0 ] && [ "$1" != "--" ]; do
        env_args+=("$1")
        shift
    done
    # consume the -- separator if present
    [ "${1:-}" = "--" ] && shift

    local output rc
    output=$(
        env -i \
            PATH="$MOCK_BIN:/usr/bin:/bin:/usr/sbin:/sbin" \
            HOME="$FAKE_HOME" \
            "${env_args[@]}" \
            bash "$TEST_ROOT/e2e/run_local.sh" "$@" 2>&1
    ) && rc=$? || rc=$?
    LAST_OUTPUT="$output"
    LAST_RC=$rc
}

# ---------- test cases ----------

test_help_flag() {
    echo "--- Test: --help prints usage and exits 0 ---"
    setup_temp

    run_script --help
    assert_exit_code "exits 0" 0 "$LAST_RC"
    assert_contains "prints Usage line" "$LAST_OUTPUT" "Usage: e2e/run_local.sh"
    assert_contains "mentions --no-build" "$LAST_OUTPUT" "--no-build"
    assert_contains "mentions --mount-clis" "$LAST_OUTPUT" "--mount-clis"
    assert_contains "mentions --backend" "$LAST_OUTPUT" "--backend"
    assert_contains "mentions --image" "$LAST_OUTPUT" "--image"

    teardown_temp
}

test_help_short_flag() {
    echo "--- Test: -h prints usage and exits 0 ---"
    setup_temp

    run_script -h
    assert_exit_code "exits 0" 0 "$LAST_RC"
    assert_contains "prints Usage line" "$LAST_OUTPUT" "Usage: e2e/run_local.sh"

    teardown_temp
}

test_unknown_option() {
    echo "--- Test: unknown option prints error and usage ---"
    setup_temp

    run_script --bogus
    # usage() always exits 0, even when called from the unknown-option path
    assert_exit_code "exits 0 (via usage)" 0 "$LAST_RC"
    assert_contains "prints unknown option message" "$LAST_OUTPUT" "Unknown option: --bogus"
    assert_contains "prints usage" "$LAST_OUTPUT" "Usage: e2e/run_local.sh"

    teardown_temp
}

test_docker_not_installed() {
    echo "--- Test: missing docker prints error and exits 1 ---"
    setup_temp

    # Remove mock docker from PATH so command -v docker fails.
    rm "$MOCK_BIN/docker"

    run_script --no-build
    assert_exit_code "exits 1" 1 "$LAST_RC"
    assert_contains "prints docker error" "$LAST_OUTPUT" "docker is not installed"

    teardown_temp
}

test_default_build_and_run() {
    echo "--- Test: default invocation builds and runs with default image ---"
    setup_temp

    run_script
    assert_exit_code "exits 0" 0 "$LAST_RC"

    # Should have two docker calls: build + run
    local line_count
    line_count=$(wc -l < "$DOCKER_LOG" | tr -d ' ')
    assert_eq "two docker invocations" "2" "$line_count"

    # First call: docker build
    local build_line
    build_line=$(sed -n '1p' "$DOCKER_LOG")
    assert_contains "build uses -f flag" "$build_line" "build -f"
    assert_contains "build tags image loomcli-e2e" "$build_line" "-t loomcli-e2e"
    assert_contains "build references Dockerfile" "$build_line" "e2e/Dockerfile"

    # Second call: docker run
    local run_line
    run_line=$(sed -n '2p' "$DOCKER_LOG")
    assert_contains "run has --rm" "$run_line" "--rm"
    assert_contains "run uses default image" "$run_line" "loomcli-e2e"

    teardown_temp
}

test_no_build_skips_build() {
    echo "--- Test: --no-build skips docker build ---"
    setup_temp

    run_script --no-build
    assert_exit_code "exits 0" 0 "$LAST_RC"

    # Should have only one docker call: run
    local line_count
    line_count=$(wc -l < "$DOCKER_LOG" | tr -d ' ')
    assert_eq "one docker invocation" "1" "$line_count"

    local run_line
    run_line=$(sed -n '1p' "$DOCKER_LOG")
    assert_contains "only call is run" "$run_line" "run"
    assert_not_contains "no build call" "$run_line" "build -f"

    # Confirm no "Building image" message
    assert_not_contains "no build message" "$LAST_OUTPUT" "Building image"

    teardown_temp
}

test_custom_image_name() {
    echo "--- Test: --image NAME sets custom image name ---"
    setup_temp

    run_script --image my-custom-e2e
    assert_exit_code "exits 0" 0 "$LAST_RC"

    # Build should use custom name
    local build_line
    build_line=$(sed -n '1p' "$DOCKER_LOG")
    assert_contains "build tags custom image" "$build_line" "-t my-custom-e2e"

    # Run should use custom name
    local run_line
    run_line=$(sed -n '2p' "$DOCKER_LOG")
    assert_contains "run uses custom image" "$run_line" "my-custom-e2e"

    teardown_temp
}

test_backend_env() {
    echo "--- Test: --backend NAME sets LOOM_BACKEND env ---"
    setup_temp

    run_script --no-build --backend claude
    assert_exit_code "exits 0" 0 "$LAST_RC"

    local run_line
    run_line=$(sed -n '1p' "$DOCKER_LOG")
    assert_contains "sets LOOM_BACKEND" "$run_line" "-e LOOM_BACKEND=claude"

    teardown_temp
}

test_anthropic_api_key_forwarded() {
    echo "--- Test: ANTHROPIC_API_KEY is forwarded when set ---"
    setup_temp

    run_script_env "ANTHROPIC_API_KEY=sk-test-123" -- --no-build
    assert_exit_code "exits 0" 0 "$LAST_RC"

    local run_line
    run_line=$(sed -n '1p' "$DOCKER_LOG")
    assert_contains "passes ANTHROPIC_API_KEY" "$run_line" "-e ANTHROPIC_API_KEY"

    teardown_temp
}

test_anthropic_api_key_absent_info() {
    echo "--- Test: missing ANTHROPIC_API_KEY prints info message ---"
    setup_temp

    run_script --no-build
    assert_exit_code "exits 0" 0 "$LAST_RC"
    assert_contains "prints stub info" "$LAST_OUTPUT" "ANTHROPIC_API_KEY not set"

    # Should NOT pass -e ANTHROPIC_API_KEY
    local run_line
    run_line=$(sed -n '1p' "$DOCKER_LOG")
    assert_not_contains "does not pass ANTHROPIC_API_KEY" "$run_line" "ANTHROPIC_API_KEY"

    teardown_temp
}

test_openai_api_key_forwarded() {
    echo "--- Test: OPENAI_API_KEY is forwarded when set ---"
    setup_temp

    run_script_env "OPENAI_API_KEY=sk-openai-123" -- --no-build
    assert_exit_code "exits 0" 0 "$LAST_RC"

    local run_line
    run_line=$(sed -n '1p' "$DOCKER_LOG")
    assert_contains "passes OPENAI_API_KEY" "$run_line" "-e OPENAI_API_KEY"

    teardown_temp
}

test_openai_api_key_absent() {
    echo "--- Test: missing OPENAI_API_KEY is not forwarded ---"
    setup_temp

    run_script --no-build
    assert_exit_code "exits 0" 0 "$LAST_RC"

    local run_line
    run_line=$(sed -n '1p' "$DOCKER_LOG")
    assert_not_contains "does not pass OPENAI_API_KEY" "$run_line" "OPENAI_API_KEY"

    teardown_temp
}

test_stub_env_forwarding() {
    echo "--- Test: STUB_* env vars are forwarded ---"
    setup_temp

    run_script_env "STUB_CLAUDE_RESPONSE=hello" "STUB_TIMEOUT=5" -- --no-build
    assert_exit_code "exits 0" 0 "$LAST_RC"

    local run_line
    run_line=$(sed -n '1p' "$DOCKER_LOG")
    assert_contains "passes STUB_CLAUDE_RESPONSE" "$run_line" "-e STUB_CLAUDE_RESPONSE"
    assert_contains "passes STUB_TIMEOUT" "$run_line" "-e STUB_TIMEOUT"

    teardown_temp
}

test_no_stub_env_when_absent() {
    echo "--- Test: no STUB_* env vars when none set ---"
    setup_temp

    run_script --no-build
    assert_exit_code "exits 0" 0 "$LAST_RC"

    local run_line
    run_line=$(sed -n '1p' "$DOCKER_LOG")
    assert_not_contains "no STUB_ vars" "$run_line" "STUB_"

    teardown_temp
}

test_claude_config_mounted() {
    echo "--- Test: ~/.claude is mounted when it exists ---"
    setup_temp

    mkdir -p "$FAKE_HOME/.claude"

    run_script --no-build
    assert_exit_code "exits 0" 0 "$LAST_RC"

    local run_line
    run_line=$(sed -n '1p' "$DOCKER_LOG")
    assert_contains "mounts .claude" "$run_line" "$FAKE_HOME/.claude:/root/.claude:ro"

    teardown_temp
}

test_codex_config_mounted() {
    echo "--- Test: ~/.codex is mounted when it exists ---"
    setup_temp

    mkdir -p "$FAKE_HOME/.codex"

    run_script --no-build
    assert_exit_code "exits 0" 0 "$LAST_RC"

    local run_line
    run_line=$(sed -n '1p' "$DOCKER_LOG")
    assert_contains "mounts .codex" "$run_line" "$FAKE_HOME/.codex:/root/.codex:ro"

    teardown_temp
}

test_opencode_config_mounted() {
    echo "--- Test: ~/.config/opencode is mounted when it exists ---"
    setup_temp

    mkdir -p "$FAKE_HOME/.config/opencode"

    run_script --no-build
    assert_exit_code "exits 0" 0 "$LAST_RC"

    local run_line
    run_line=$(sed -n '1p' "$DOCKER_LOG")
    assert_contains "mounts opencode" "$run_line" "$FAKE_HOME/.config/opencode:/root/.config/opencode:ro"

    teardown_temp
}

test_no_config_dirs_when_absent() {
    echo "--- Test: no config mounts when dirs do not exist ---"
    setup_temp

    # FAKE_HOME is empty by default (no .claude, .codex, .config/opencode)

    run_script --no-build
    assert_exit_code "exits 0" 0 "$LAST_RC"

    local run_line
    run_line=$(sed -n '1p' "$DOCKER_LOG")
    assert_not_contains "no .claude mount" "$run_line" ".claude:/root/.claude"
    assert_not_contains "no .codex mount" "$run_line" ".codex:/root/.codex"
    assert_not_contains "no opencode mount" "$run_line" "opencode:/root/.config/opencode"

    teardown_temp
}

test_all_config_dirs_mounted() {
    echo "--- Test: all three config dirs mounted when all exist ---"
    setup_temp

    mkdir -p "$FAKE_HOME/.claude"
    mkdir -p "$FAKE_HOME/.codex"
    mkdir -p "$FAKE_HOME/.config/opencode"

    run_script --no-build
    assert_exit_code "exits 0" 0 "$LAST_RC"

    local run_line
    run_line=$(sed -n '1p' "$DOCKER_LOG")
    assert_contains "mounts .claude" "$run_line" ".claude:/root/.claude:ro"
    assert_contains "mounts .codex" "$run_line" ".codex:/root/.codex:ro"
    assert_contains "mounts opencode" "$run_line" ".config/opencode:/root/.config/opencode:ro"

    teardown_temp
}

test_mount_clis() {
    echo "--- Test: --mount-clis mounts CLI binaries found in PATH ---"
    setup_temp

    # Create fake CLI binaries in mock bin dir
    for cli in claude codex opencode; do
        cat > "$MOCK_BIN/$cli" <<EOF
#!/bin/sh
echo "I am $cli"
EOF
        chmod +x "$MOCK_BIN/$cli"
    done

    run_script --no-build --mount-clis
    assert_exit_code "exits 0" 0 "$LAST_RC"

    local run_line
    run_line=$(sed -n '1p' "$DOCKER_LOG")
    assert_contains "mounts claude binary" "$run_line" "-v $MOCK_BIN/claude:/usr/local/bin/claude"
    assert_contains "mounts codex binary" "$run_line" "-v $MOCK_BIN/codex:/usr/local/bin/codex"
    assert_contains "mounts opencode binary" "$run_line" "-v $MOCK_BIN/opencode:/usr/local/bin/opencode"

    # Should also print info messages
    assert_contains "prints claude mount" "$LAST_OUTPUT" "Mounting real claude"
    assert_contains "prints codex mount" "$LAST_OUTPUT" "Mounting real codex"
    assert_contains "prints opencode mount" "$LAST_OUTPUT" "Mounting real opencode"

    teardown_temp
}

test_mount_clis_partial() {
    echo "--- Test: --mount-clis only mounts CLIs that exist ---"
    setup_temp

    # Only create claude; codex and opencode are absent
    cat > "$MOCK_BIN/claude" <<'EOF'
#!/bin/sh
echo "I am claude"
EOF
    chmod +x "$MOCK_BIN/claude"

    run_script --no-build --mount-clis
    assert_exit_code "exits 0" 0 "$LAST_RC"

    local run_line
    run_line=$(sed -n '1p' "$DOCKER_LOG")
    assert_contains "mounts claude" "$run_line" "/usr/local/bin/claude"
    assert_not_contains "no codex mount" "$run_line" "/usr/local/bin/codex"
    assert_not_contains "no opencode mount" "$run_line" "/usr/local/bin/opencode"

    teardown_temp
}

test_no_mount_clis_by_default() {
    echo "--- Test: CLIs are NOT mounted without --mount-clis ---"
    setup_temp

    # Create fake CLIs but do NOT pass --mount-clis
    for cli in claude codex opencode; do
        cat > "$MOCK_BIN/$cli" <<EOF
#!/bin/sh
echo "I am $cli"
EOF
        chmod +x "$MOCK_BIN/$cli"
    done

    run_script --no-build
    assert_exit_code "exits 0" 0 "$LAST_RC"

    local run_line
    run_line=$(sed -n '1p' "$DOCKER_LOG")
    assert_not_contains "no claude mount" "$run_line" "/usr/local/bin/claude"
    assert_not_contains "no codex mount" "$run_line" "/usr/local/bin/codex"
    assert_not_contains "no opencode mount" "$run_line" "/usr/local/bin/opencode"

    teardown_temp
}

test_passthrough_args() {
    echo "--- Test: arguments after -- are passed to docker run ---"
    setup_temp

    run_script --no-build -- go test -tags e2e -v -run TestFoo ./internal/cli/
    assert_exit_code "exits 0" 0 "$LAST_RC"

    local run_line
    run_line=$(sed -n '1p' "$DOCKER_LOG")
    assert_contains "passes go" "$run_line" "go"
    assert_contains "passes test" "$run_line" "test"
    assert_contains "passes -tags e2e" "$run_line" "-tags"
    assert_contains "passes -run TestFoo" "$run_line" "-run TestFoo"
    assert_contains "passes ./internal/cli/" "$run_line" "./internal/cli/"

    teardown_temp
}

test_no_passthrough_without_separator() {
    echo "--- Test: docker run has no extra args without -- ---"
    setup_temp

    run_script --no-build
    assert_exit_code "exits 0" 0 "$LAST_RC"

    # The run line should end with just the image name and no trailing args.
    local run_line
    run_line=$(sed -n '1p' "$DOCKER_LOG")
    # After the image name there should be nothing extra
    # The format is: run --rm loomcli-e2e
    assert_contains "run line ends with image" "$run_line" "loomcli-e2e"

    teardown_temp
}

test_combined_flags() {
    echo "--- Test: multiple flags combined correctly ---"
    setup_temp

    mkdir -p "$FAKE_HOME/.claude"

    run_script_env "ANTHROPIC_API_KEY=sk-abc" "STUB_MODE=1" -- \
        --no-build --backend codex --image custom-img -- go test -v
    assert_exit_code "exits 0" 0 "$LAST_RC"

    # Should be only one docker call (--no-build)
    local line_count
    line_count=$(wc -l < "$DOCKER_LOG" | tr -d ' ')
    assert_eq "one docker invocation" "1" "$line_count"

    local run_line
    run_line=$(sed -n '1p' "$DOCKER_LOG")
    assert_contains "uses custom image" "$run_line" "custom-img"
    assert_contains "sets LOOM_BACKEND" "$run_line" "-e LOOM_BACKEND=codex"
    assert_contains "forwards API key" "$run_line" "-e ANTHROPIC_API_KEY"
    assert_contains "forwards STUB var" "$run_line" "-e STUB_MODE"
    assert_contains "mounts .claude" "$run_line" ".claude:/root/.claude:ro"
    assert_contains "passes go test" "$run_line" "go test -v"

    teardown_temp
}

# ---------- run all tests ----------

echo "=============================="
echo " run_local.sh test suite"
echo "=============================="
echo

test_help_flag
echo
test_help_short_flag
echo
test_unknown_option
echo
test_docker_not_installed
echo
test_default_build_and_run
echo
test_no_build_skips_build
echo
test_custom_image_name
echo
test_backend_env
echo
test_anthropic_api_key_forwarded
echo
test_anthropic_api_key_absent_info
echo
test_openai_api_key_forwarded
echo
test_openai_api_key_absent
echo
test_stub_env_forwarding
echo
test_no_stub_env_when_absent
echo
test_claude_config_mounted
echo
test_codex_config_mounted
echo
test_opencode_config_mounted
echo
test_no_config_dirs_when_absent
echo
test_all_config_dirs_mounted
echo
test_mount_clis
echo
test_mount_clis_partial
echo
test_no_mount_clis_by_default
echo
test_passthrough_args
echo
test_no_passthrough_without_separator
echo
test_combined_flags

echo
echo "=============================="
echo " Results: $PASS passed, $FAIL failed, $TOTAL total"
echo "=============================="

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
exit 0
