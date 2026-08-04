#!/bin/sh
# verify_list.sh — Smoke test for loom list / loom ls output formats.
# Verifies: help output, missing/empty worktrees dir, legacy mode, dirty/clean status,
# lock files, alias equivalence, non-git dirs, default branch override, stale locks.
#
# Usage: verify_list.sh [-v|--verbose] [-q|--quiet] [-h|--help]
# Exit codes: 0 = all passed, 1 = one or more failed

VERBOSE=0
QUIET=0
PASS_COUNT=0
FAIL_COUNT=0
CLEANUP_DIRS=""

# ── Argument parsing ──────────────────────────────────────────────────────
while [ $# -gt 0 ]; do
    case "$1" in
        -v|--verbose) VERBOSE=1; shift ;;
        -q|--quiet)   QUIET=1; shift ;;
        -h|--help)
            printf "Usage: verify_list.sh [OPTIONS]\n\n"
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
track_dir() {
    if [ -z "$CLEANUP_DIRS" ]; then
        CLEANUP_DIRS="$1"
    else
        CLEANUP_DIRS="$CLEANUP_DIRS
$1"
    fi
}

cleanup() {
    if [ -n "$CLEANUP_DIRS" ]; then
        printf '%s\n' "$CLEANUP_DIRS" | while IFS= read -r d; do
            if [ -n "$d" ] && [ -d "$d" ]; then
                rm -rf "$d"
            fi
        done
    fi
}
trap cleanup EXIT

# Helper: create a minimal git repo at a given path
init_git_repo() {
    _path="$1"
    mkdir -p "$_path"
    git init -q "$_path"
    git -C "$_path" config user.email "test@e2e"
    git -C "$_path" config user.name "E2E Test"
    git -C "$_path" commit --allow-empty -q -m "initial"
}

# ── Begin tests ────────────────────────────────────────────────────────────
if [ "$QUIET" -eq 0 ]; then
    printf "=== verify_list.sh smoke test ===\n"
fi

# ── Group 1: Basic invocation (help) ─────────────────────────────────────
help_out=$(loom list --help 2>&1); help_ec=$?
if [ "$help_ec" -eq 0 ]; then
    pass "loom list --help exits 0"
else
    fail "loom list --help exits 0" "Exit code: $help_ec"
fi

if printf '%s' "$help_out" | grep -qi "List all agents"; then
    pass "loom list --help contains 'List all agents'"
else
    fail "loom list --help contains 'List all agents'" "Output: $help_out"
fi

ls_help_out=$(loom ls --help 2>&1); ls_help_ec=$?
if [ "$ls_help_ec" -eq 0 ]; then
    pass "loom ls --help exits 0"
else
    fail "loom ls --help exits 0" "Exit code: $ls_help_ec"
fi

# ── Group 2: Missing worktrees directory ──────────────────────────────────
TMPDIR2=$(mktemp -d)
track_dir "$TMPDIR2"
WORK2="$TMPDIR2/project"
mkdir -p "$WORK2"
git init -q "$WORK2"
git -C "$WORK2" config user.email "test@e2e"
git -C "$WORK2" config user.name "E2E Test"
git -C "$WORK2" commit --allow-empty -q -m "initial"
# No worktrees/ subdirectory created
verbose "Testing missing worktrees dir in $WORK2"

missing_out=$(cd "$WORK2" && loom list 2>&1); missing_ec=$?
if [ "$missing_ec" -ne 0 ]; then
    pass "loom list (missing worktrees dir) exits non-zero"
else
    fail "loom list (missing worktrees dir) exits non-zero" "Exit code was 0"
fi

if printf '%s' "$missing_out" | grep -q "worktrees directory not found"; then
    pass "loom list (missing worktrees dir) error message"
else
    fail "loom list (missing worktrees dir) error message" "Expected 'worktrees directory not found' in: $missing_out"
fi

# ── Group 3: Empty worktrees directory ────────────────────────────────────
TMPDIR3=$(mktemp -d)
track_dir "$TMPDIR3"
WORK3="$TMPDIR3/project"
mkdir -p "$WORK3/worktrees"
git init -q "$WORK3"
git -C "$WORK3" config user.email "test@e2e"
git -C "$WORK3" config user.name "E2E Test"
git -C "$WORK3" commit --allow-empty -q -m "initial"
verbose "Testing empty worktrees dir in $WORK3"

empty_out=$(cd "$WORK3" && loom list 2>&1); empty_ec=$?
if [ "$empty_ec" -eq 0 ]; then
    pass "loom list (empty worktrees dir) exits 0"
else
    fail "loom list (empty worktrees dir) exits 0" "Exit code: $empty_ec"
fi

if printf '%s' "$empty_out" | grep -q "No agents (worktrees) found"; then
    pass "loom list (empty worktrees dir) shows empty message"
else
    fail "loom list (empty worktrees dir) shows empty message" "Output: $empty_out"
fi

# ── Group 4: Legacy mode (flat list) ─────────────────────────────────────
TMPDIR4=$(mktemp -d)
track_dir "$TMPDIR4"
WORK4="$TMPDIR4/project"
mkdir -p "$WORK4/worktrees"
git init -q "$WORK4"
git -C "$WORK4" config user.email "test@e2e"
git -C "$WORK4" config user.name "E2E Test"
git -C "$WORK4" commit --allow-empty -q -m "initial"

init_git_repo "$WORK4/worktrees/agent1"
init_git_repo "$WORK4/worktrees/agent2"
verbose "Testing legacy list mode in $WORK4"

legacy_out=$(cd "$WORK4" && loom list 2>&1); legacy_ec=$?
if [ "$legacy_ec" -eq 0 ]; then
    pass "loom list (legacy mode) exits 0"
else
    fail "loom list (legacy mode) exits 0" "Exit code: $legacy_ec"
fi

if printf '%s' "$legacy_out" | grep -q "Agents (Worktrees):"; then
    pass "loom list (legacy mode) shows header"
else
    fail "loom list (legacy mode) shows header" "Output: $legacy_out"
fi

if printf '%s' "$legacy_out" | grep -q "agent1"; then
    pass "loom list (legacy mode) shows agent1"
else
    fail "loom list (legacy mode) shows agent1" "Output: $legacy_out"
fi

if printf '%s' "$legacy_out" | grep -q "agent2"; then
    pass "loom list (legacy mode) shows agent2"
else
    fail "loom list (legacy mode) shows agent2" "Output: $legacy_out"
fi

if printf '%s' "$legacy_out" | grep -q "Total: 2 agents"; then
    pass "loom list (legacy mode) shows total count"
else
    fail "loom list (legacy mode) shows total count" "Output: $legacy_out"
fi

if printf '%s' "$legacy_out" | grep -q "Default branch:"; then
    pass "loom list (legacy mode) shows default branch"
else
    fail "loom list (legacy mode) shows default branch" "Output: $legacy_out"
fi

if printf '%s' "$legacy_out" | grep -q "ready"; then
    pass "loom list (legacy mode) shows ready status"
else
    fail "loom list (legacy mode) shows ready status" "Output: $legacy_out"
fi

# ── Group 5: Dirty worktree status ────────────────────────────────────────
TMPDIR5=$(mktemp -d)
track_dir "$TMPDIR5"
WORK5="$TMPDIR5/project"
mkdir -p "$WORK5/worktrees"
git init -q "$WORK5"
git -C "$WORK5" config user.email "test@e2e"
git -C "$WORK5" config user.name "E2E Test"
git -C "$WORK5" commit --allow-empty -q -m "initial"

init_git_repo "$WORK5/worktrees/clean"
init_git_repo "$WORK5/worktrees/dirty"
# Make dirty worktree dirty
printf "untracked content\n" > "$WORK5/worktrees/dirty/untracked_file.txt"
verbose "Testing dirty worktree in $WORK5"

dirty_out=$(cd "$WORK5" && loom list 2>&1)

if printf '%s' "$dirty_out" | grep "dirty" | grep -q "changes"; then
    pass "loom list (dirty worktree) shows changes"
else
    fail "loom list (dirty worktree) shows changes" "Output: $dirty_out"
fi

if printf '%s' "$dirty_out" | grep "clean" | grep -q "ready"; then
    pass "loom list (clean worktree) shows ready"
else
    fail "loom list (clean worktree) shows ready" "Output: $dirty_out"
fi

# ── Group 6: Running agent lock status ────────────────────────────────────
TMPDIR6=$(mktemp -d)
track_dir "$TMPDIR6"
WORK6="$TMPDIR6/project"
mkdir -p "$WORK6/worktrees"
git init -q "$WORK6"
git -C "$WORK6" config user.email "test@e2e"
git -C "$WORK6" config user.name "E2E Test"
git -C "$WORK6" commit --allow-empty -q -m "initial"

init_git_repo "$WORK6/worktrees/locked"

# Gitignore the lock file so it does not count as an untracked change
printf ".agent.lock\n" > "$WORK6/worktrees/locked/.gitignore"
git -C "$WORK6/worktrees/locked" add .gitignore
git -C "$WORK6/worktrees/locked" commit -q -m "add gitignore"

cat > "$WORK6/worktrees/locked/.agent.lock" <<LOCKEOF
{
  "pid": $$,
  "command": "task",
  "started_at": "2026-01-01T00:00:00Z",
  "agent_name": "e2e-test"
}
LOCKEOF
verbose "Testing running lock in $WORK6 (PID $$)"

lock_out=$(cd "$WORK6" && loom list 2>&1)

if printf '%s' "$lock_out" | grep "locked" | grep -qv "ready"; then
    pass "loom list (running lock) does not show ready"
else
    fail "loom list (running lock) does not show ready" "Output: $lock_out"
fi

rm -f "$WORK6/worktrees/locked/.agent.lock"

# ── Group 7: Alias equivalence ────────────────────────────────────────────
# Reuse WORK4 which has two clean worktrees
alias_list=$(cd "$WORK4" && loom list 2>&1)
alias_ls=$(cd "$WORK4" && loom ls 2>&1)

if [ "$alias_list" = "$alias_ls" ]; then
    pass "loom ls produces identical output to loom list"
else
    fail "loom ls produces identical output to loom list" "Outputs differ"
    if [ "$VERBOSE" -eq 1 ]; then
        printf "  list: %s\n" "$alias_list"
        printf "  ls:   %s\n" "$alias_ls"
    fi
fi

# ── Group 8: Non-git directories are skipped ──────────────────────────────
TMPDIR8=$(mktemp -d)
track_dir "$TMPDIR8"
WORK8="$TMPDIR8/project"
mkdir -p "$WORK8/worktrees"
git init -q "$WORK8"
git -C "$WORK8" config user.email "test@e2e"
git -C "$WORK8" config user.name "E2E Test"
git -C "$WORK8" commit --allow-empty -q -m "initial"

init_git_repo "$WORK8/worktrees/valid"
mkdir -p "$WORK8/worktrees/invalid"  # plain dir, no .git
verbose "Testing non-git dir skip in $WORK8"

skip_out=$(cd "$WORK8" && loom list 2>&1)

if printf '%s' "$skip_out" | grep -q "valid"; then
    pass "loom list shows valid git worktree"
else
    fail "loom list shows valid git worktree" "Output: $skip_out"
fi

if printf '%s' "$skip_out" | grep -q "Total: 1 agents"; then
    pass "loom list skips non-git directory (count=1)"
else
    fail "loom list skips non-git directory (count=1)" "Output: $skip_out"
fi

# ── Group 9: Default branch override ─────────────────────────────────────
# Reuse WORK4 which has valid worktrees
branch_out=$(cd "$WORK4" && LOOM_DEFAULT_BRANCH=develop loom list 2>&1)

if printf '%s' "$branch_out" | grep -q "Default branch: develop"; then
    pass "LOOM_DEFAULT_BRANCH=develop reflected in output"
else
    fail "LOOM_DEFAULT_BRANCH=develop reflected in output" "Output: $branch_out"
fi

# ── Group 10: Stale lock file (dead PID) ──────────────────────────────────
TMPDIR10=$(mktemp -d)
track_dir "$TMPDIR10"
WORK10="$TMPDIR10/project"
mkdir -p "$WORK10/worktrees"
git init -q "$WORK10"
git -C "$WORK10" config user.email "test@e2e"
git -C "$WORK10" config user.name "E2E Test"
git -C "$WORK10" commit --allow-empty -q -m "initial"

init_git_repo "$WORK10/worktrees/stale"

# Gitignore the lock file so it does not count as an untracked change
printf ".agent.lock\n" > "$WORK10/worktrees/stale/.gitignore"
git -C "$WORK10/worktrees/stale" add .gitignore
git -C "$WORK10/worktrees/stale" commit -q -m "add gitignore"

cat > "$WORK10/worktrees/stale/.agent.lock" <<LOCKEOF
{
  "pid": 99999,
  "command": "task",
  "started_at": "2026-01-01T00:00:00Z",
  "agent_name": "e2e-stale"
}
LOCKEOF
verbose "Testing stale lock in $WORK10 (PID 99999)"

stale_out=$(cd "$WORK10" && loom list 2>&1)

if printf '%s' "$stale_out" | grep "stale" | grep -q "ready"; then
    pass "loom list (stale lock) shows ready"
else
    fail "loom list (stale lock) shows ready" "Output: $stale_out"
fi

rm -f "$WORK10/worktrees/stale/.agent.lock"

# ── Summary ──────────────────────────────────────────────────────────────
total=$((PASS_COUNT + FAIL_COUNT))
printf "=== Results: %d passed, %d failed (of %d) ===\n" "$PASS_COUNT" "$FAIL_COUNT" "$total"

if [ "$FAIL_COUNT" -gt 0 ]; then
    exit 1
fi
exit 0
