#!/bin/sh
# verify_todo.sh — Smoke test for loom CLI + bd integration in the E2E Docker container.
# Verifies: binary existence, stub output, bd task CRUD, loom commands, lock files, signal files.
#
# Usage: verify_todo.sh [-v|--verbose] [-q|--quiet] [-h|--help]
# Exit codes: 0 = all passed, 1 = one or more failed

TEMPLATE_PATH="${LOOM_TEMPLATE_PATH:-/etc/loom/task_template.txt}"
VERBOSE=0
QUIET=0
PASS_COUNT=0
FAIL_COUNT=0
CLEANUP_DIR=""
TASK_NODESIGN=""
TASK_READY=""
TASK_EPIC=""

# ── Argument parsing ──────────────────────────────────────────────────────
while [ $# -gt 0 ]; do
    case "$1" in
        -v|--verbose) VERBOSE=1; shift ;;
        -q|--quiet)   QUIET=1; shift ;;
        -h|--help)
            printf "Usage: verify_todo.sh [OPTIONS]\n\n"
            printf "Options:\n"
            printf "  -v, --verbose    Show detailed output for each test\n"
            printf "  -q, --quiet      Only show summary (pass/fail count)\n"
            printf "  -h, --help       Show usage\n\n"
            printf "Exit codes:\n"
            printf "  0  All tests passed\n"
            printf "  1  One or more tests failed\n"
            exit 0
            ;;
        *) printf "Unknown option: %s\n" "$1" >&2; exit 1 ;;
    esac
done

# ── Output helpers ─────────────────────────────────────────────────────────
pass() {
    PASS_COUNT=$((PASS_COUNT + 1))
    if [ "$QUIET" -eq 0 ]; then
        printf "[PASS] %s\n" "$1"
    fi
}

fail() {
    FAIL_COUNT=$((FAIL_COUNT + 1))
    if [ "$QUIET" -eq 0 ]; then
        printf "[FAIL] %s\n" "$1"
    fi
    if [ "$VERBOSE" -eq 1 ] && [ -n "${2:-}" ]; then
        printf "  %s\n" "$2"
    fi
}

verbose() {
    if [ "$VERBOSE" -eq 1 ]; then
        printf "  > %s\n" "$1"
    fi
}

# ── Cleanup ────────────────────────────────────────────────────────────────
cleanup() {
    if [ -n "$CLEANUP_DIR" ] && [ -d "$CLEANUP_DIR" ]; then
        rm -rf "$CLEANUP_DIR"
    fi
}
trap cleanup EXIT

# ── Begin tests ────────────────────────────────────────────────────────────
if [ "$QUIET" -eq 0 ]; then
    printf "=== verify_todo.sh smoke test ===\n"
fi

# ── Test 1: Binary existence ──────────────────────────────────────────────
for bin in loom bd git tmux jq claude codex opencode; do
    if command -v "$bin" >/dev/null 2>&1; then
        pass "Binary exists: $bin"
    else
        fail "Binary exists: $bin" "$bin not found on PATH"
    fi
done

# ── Test 2: Backend stub output ──────────────────────────────────────────

# Claude stub: stream-json mode
output=$(printf 'test prompt' | claude -p --output-format stream-json 2>&1)
if printf '%s' "$output" | grep -q '"type":"assistant"'; then
    pass "Claude stub stream-json output"
else
    fail "Claude stub stream-json output" "Expected '\"type\":\"assistant\"' in output"
fi

# Codex stub: exec --json mode
output=$(printf 'test prompt' | codex exec --json 2>&1)
if printf '%s' "$output" | grep -q '"status":"completed"'; then
    pass "Codex stub exec --json output"
else
    fail "Codex stub exec --json output" "Expected '\"status\":\"completed\"' in output"
fi

# OpenCode stub: run --format json mode
output=$(printf 'test prompt' | opencode run --format json 2>&1)
if printf '%s' "$output" | grep -q '"status":"completed"'; then
    pass "OpenCode stub run --format json output"
else
    fail "OpenCode stub run --format json output" "Expected '\"status\":\"completed\"' in output"
fi

# Stub exit code overrides
STUB_CLAUDE_EXIT_CODE=1 claude >/dev/null 2>&1; ec=$?
if [ "$ec" -eq 1 ]; then
    pass "Claude stub exit code override"
else
    fail "Claude stub exit code override" "Expected exit code 1, got $ec"
fi

STUB_CODEX_EXIT_CODE=1 codex >/dev/null 2>&1; ec=$?
if [ "$ec" -eq 1 ]; then
    pass "Codex stub exit code override"
else
    fail "Codex stub exit code override" "Expected exit code 1, got $ec"
fi

STUB_OPENCODE_EXIT_CODE=1 opencode >/dev/null 2>&1; ec=$?
if [ "$ec" -eq 1 ]; then
    pass "OpenCode stub exit code override"
else
    fail "OpenCode stub exit code override" "Expected exit code 1, got $ec"
fi

# ── Test 3: BD initialization and task creation ──────────────────────────
CLEANUP_DIR=$(mktemp -d)
WORK_DIR="$CLEANUP_DIR/workspace"
mkdir -p "$WORK_DIR"
cd "$WORK_DIR" || { fail "cd to workspace" "Cannot cd to $WORK_DIR"; exit 1; }

# Initialize git repo (required by bd)
git init -q .
git config user.email "test@e2e"
git config user.name "E2E Test"
git commit --allow-empty -q -m "initial"
verbose "Git repo initialized in $WORK_DIR"

# Initialize beads
if bd init >/dev/null 2>&1; then
    pass "BD init"
    verbose "BD initialized in $WORK_DIR"
else
    fail "BD init" "bd init failed in $WORK_DIR"
fi

# Create tasks from template
if [ ! -f "$TEMPLATE_PATH" ]; then
    fail "Task template exists" "$TEMPLATE_PATH not found"
else
    pass "Task template exists"

    # Task needing planning (no design)
    title=$(jq -r '.needs_plan.title' "$TEMPLATE_PATH")
    desc=$(jq -r '.needs_plan.description' "$TEMPLATE_PATH")
    prio=$(jq -r '.needs_plan.priority' "$TEMPLATE_PATH")
    type_val=$(jq -r '.needs_plan.type' "$TEMPLATE_PATH")
    create_out=$(bd create "$title" --type="$type_val" --priority="$prio" --description="$desc" 2>&1)
    TASK_NODESIGN=$(printf '%s\n' "$create_out" | awk '/[Ii]ssue:/{print $NF}')
    if [ -n "$TASK_NODESIGN" ]; then
        pass "Create task (needs plan): $TASK_NODESIGN"
    else
        fail "Create task (needs plan)" "bd create returned no ID: $create_out"
    fi

    # Task ready for implementation (has design)
    title=$(jq -r '.ready_to_implement.title' "$TEMPLATE_PATH")
    desc=$(jq -r '.ready_to_implement.description' "$TEMPLATE_PATH")
    design=$(jq -r '.ready_to_implement.design' "$TEMPLATE_PATH")
    prio=$(jq -r '.ready_to_implement.priority' "$TEMPLATE_PATH")
    type_val=$(jq -r '.ready_to_implement.type' "$TEMPLATE_PATH")
    create_out=$(bd create "$title" --type="$type_val" --priority="$prio" --description="$desc" --design="$design" 2>&1)
    TASK_READY=$(printf '%s\n' "$create_out" | awk '/[Ii]ssue:/{print $NF}')
    if [ -n "$TASK_READY" ]; then
        pass "Create task (ready to implement): $TASK_READY"
    else
        fail "Create task (ready to implement)" "bd create returned no ID: $create_out"
    fi

    # Epic (should be skipped by agents)
    title=$(jq -r '.epic.title' "$TEMPLATE_PATH")
    desc=$(jq -r '.epic.description' "$TEMPLATE_PATH")
    prio=$(jq -r '.epic.priority' "$TEMPLATE_PATH")
    type_val=$(jq -r '.epic.type' "$TEMPLATE_PATH")
    create_out=$(bd create "$title" --type="$type_val" --priority="$prio" --description="$desc" 2>&1)
    TASK_EPIC=$(printf '%s\n' "$create_out" | awk '/[Ii]ssue:/{print $NF}')
    if [ -n "$TASK_EPIC" ]; then
        pass "Create epic: $TASK_EPIC"
    else
        fail "Create epic" "bd create returned no ID: $create_out"
    fi

    # Verify bd ready lists tasks
    ready_json=$(bd ready --json 2>/dev/null || printf '[]')
    ready_count=$(printf '%s' "$ready_json" | jq 'length')
    if [ "$ready_count" -ge 2 ]; then
        pass "BD ready lists tasks ($ready_count found)"
    else
        fail "BD ready lists tasks" "Expected >=2, got $ready_count"
    fi

    # Verify bd list shows all
    list_json=$(bd list --status=open --json 2>/dev/null || printf '[]')
    list_count=$(printf '%s' "$list_json" | jq 'length')
    if [ "$list_count" -ge 3 ]; then
        pass "BD list shows all tasks ($list_count found)"
    else
        fail "BD list shows all tasks" "Expected >=3, got $list_count"
    fi
fi

# ── Test 4: Loom command help ────────────────────────────────────────────
for pair in "plan:planning" "task:implementation" "claim:lock" "complete:completion"; do
    cmd=$(printf '%s' "$pair" | cut -d: -f1)
    keyword=$(printf '%s' "$pair" | cut -d: -f2)
    help_out=$(loom "$cmd" --help 2>&1)
    if printf '%s' "$help_out" | grep -qi "$keyword"; then
        pass "Loom $cmd --help contains '$keyword'"
    else
        fail "Loom $cmd --help contains '$keyword'" "Keyword not found in help output"
    fi
done

# ── Test 5: Loom claim (lock file integration) ──────────────────────────
LOCK_FILE="$WORK_DIR/.agent.lock"

# Create a lock file manually (simulates what loom plan/task does via AcquireLock)
cat > "$LOCK_FILE" <<LOCKEOF
{
  "pid": $$,
  "command": "task",
  "started_at": "2026-01-01T00:00:00Z",
  "agent_name": "e2e-test"
}
LOCKEOF

if [ -n "$TASK_READY" ]; then
    claim_out=$(loom claim "$TASK_READY" 2>&1); claim_ec=$?
    if [ "$claim_ec" -eq 0 ]; then
        pass "Loom claim exits successfully"

        lock_task_id=$(jq -r '.task_id' "$LOCK_FILE" 2>/dev/null || printf '')
        if [ "$lock_task_id" = "$TASK_READY" ]; then
            pass "Lock file updated with task_id"
        else
            fail "Lock file updated with task_id" "Expected '$TASK_READY', got '$lock_task_id'"
        fi

        lock_title=$(jq -r '.task_title' "$LOCK_FILE" 2>/dev/null || printf '')
        if [ -n "$lock_title" ] && [ "$lock_title" != "null" ]; then
            pass "Lock file contains task_title"
        else
            fail "Lock file contains task_title" "task_title is empty or null"
        fi
    else
        fail "Loom claim exits successfully" "Exit code $claim_ec: $claim_out"
        fail "Lock file updated with task_id" "Claim failed, skipping"
        fail "Lock file contains task_title" "Claim failed, skipping"
    fi
else
    fail "Loom claim (skipped)" "No task ID from earlier step"
    fail "Lock file updated with task_id (skipped)" "No task ID"
    fail "Lock file contains task_title (skipped)" "No task ID"
fi

rm -f "$LOCK_FILE"

# ── Test 6: Loom complete (signal file) ──────────────────────────────────
# Create a timestamp marker right before calling complete (used for -newer fallback)
MARKER_FILE="$WORK_DIR/.test_marker"
touch "$MARKER_FILE"
complete_out=$(LOOM_WORKTREE_PATH="$WORK_DIR" loom complete 2>&1); complete_ec=$?
if [ "$complete_ec" -eq 0 ]; then
    pass "Loom complete exits successfully"
else
    fail "Loom complete exits successfully" "Exit code $complete_ec: $complete_out"
fi

# Verify signal file was created
# loom complete writes to /tmp/loom-signals-<uid>/<sha256-hash-prefix>
SIGNAL_DIR="${TMPDIR:-/tmp}/loom-signals-$(id -u)"
RESOLVED_WORK_DIR=$(cd "$WORK_DIR" && pwd -P)
if command -v sha256sum >/dev/null 2>&1; then
    EXPECTED_HASH=$(printf '%s' "$RESOLVED_WORK_DIR" | sha256sum | cut -c1-16)
elif command -v shasum >/dev/null 2>&1; then
    EXPECTED_HASH=$(printf '%s' "$RESOLVED_WORK_DIR" | shasum -a 256 | cut -c1-16)
else
    EXPECTED_HASH=""
fi
SIGNAL_FILE="$SIGNAL_DIR/$EXPECTED_HASH"

if [ -n "$EXPECTED_HASH" ] && [ -f "$SIGNAL_FILE" ]; then
    pass "Signal file created at expected path"
    verbose "Signal file: $SIGNAL_FILE"
    rm -f "$SIGNAL_FILE"
elif [ -d "$SIGNAL_DIR" ]; then
    # Hash mismatch — check if any signal file was created recently
    any_signal=$(find "$SIGNAL_DIR" -type f -newer "$MARKER_FILE" 2>/dev/null | head -1)
    if [ -n "$any_signal" ]; then
        pass "Signal file created (hash differs from expected)"
        verbose "Found: $any_signal (expected: $SIGNAL_FILE)"
        rm -f "$any_signal"
    else
        fail "Signal file created" "No signal file found in $SIGNAL_DIR"
    fi
else
    fail "Signal file created" "Signal directory $SIGNAL_DIR does not exist"
fi

# ── Summary ──────────────────────────────────────────────────────────────
total=$((PASS_COUNT + FAIL_COUNT))
printf "=== Results: %d passed, %d failed (of %d) ===\n" "$PASS_COUNT" "$FAIL_COUNT" "$total"

if [ "$FAIL_COUNT" -gt 0 ]; then
    exit 1
fi
exit 0
