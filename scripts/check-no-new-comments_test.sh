#!/usr/bin/env bash
# check-no-new-comments_test.sh - Tests for check-no-new-comments.sh and added-lines.sh
#
# Builds a throwaway git repo, commits a baseline Go file with existing
# comments, then checks which added lines the guard reports.
#
# Usage: ./scripts/check-no-new-comments_test.sh
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT_UNDER_TEST="$SCRIPT_DIR/check-no-new-comments.sh"

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

while IFS= read -r git_env; do
    unset "$git_env"
done < <(git rev-parse --local-env-vars)
unset LOOM_COMMENT_BASE LOOM_COMMENT_BASE_REFS LOOM_COMMENT_PUSH_BEFORE GITHUB_BASE_REF GITHUB_EVENT_NAME

TEST_TMPDIR=$(mktemp -d)
trap 'rm -rf "$TEST_TMPDIR"' EXIT
REPO="$TEST_TMPDIR/repo"

BASELINE='package demo

// Existing comment stays allowed.
func A() int {
	return 1
}
'

git_repo() {
    git -C "$REPO" "$@"
}

reset_repo() {
    rm -rf "$REPO"
    mkdir -p "$REPO/pkg"
    git_repo init -q -b work
    git_repo config user.name test
    git_repo config user.email test@example.com
    git_repo config commit.gpgsign false
    printf '%s' "$BASELINE" >"$REPO/pkg/a.go"
    git_repo add -A
    git_repo commit -q -m base
    git_repo update-ref refs/remotes/origin/main HEAD
}

run_guard() {
    local output exit_code
    output=$(cd "$REPO" && "$SCRIPT_UNDER_TEST" "$@" 2>&1) && exit_code=0 || exit_code=$?
    RUN_OUTPUT="$output"
    RUN_EXIT="$exit_code"
}

expect_pass() {
    local name="$1"
    shift
    run_guard "$@"
    if [[ "$RUN_EXIT" -eq 0 ]]; then
        pass "$name"
    else
        fail "$name (exit $RUN_EXIT): $RUN_OUTPUT"
    fi
}

expect_fail_with() {
    local name="$1" needle="$2"
    shift 2
    run_guard "$@"
    if [[ "$RUN_EXIT" -eq 1 && "$RUN_OUTPUT" == *"$needle"* ]]; then
        pass "$name"
    else
        fail "$name (exit $RUN_EXIT, want 1 with '$needle'): $RUN_OUTPUT"
    fi
}

commit_all() {
    git_repo add -A
    git_repo commit -q -m change
}

test_no_changes_pass() {
    reset_repo
    expect_pass "No changes passes"
}

test_committed_line_comment_fails() {
    reset_repo
    printf '%s\n// new comment\nvar B = 2\n' "$BASELINE" >"$REPO/pkg/a.go"
    commit_all
    expect_fail_with "Committed // comment fails with location" "pkg/a.go:8: // new comment"
}

test_uncommitted_block_comment_fails() {
    reset_repo
    printf '%s\n/*\nblock\n*/\nvar B = 2\n' "$BASELINE" >"$REPO/pkg/a.go"
    expect_fail_with "Unstaged /* */ block comment fails" "pkg/a.go:9: block"
}

test_untracked_file_fails() {
    reset_repo
    printf 'package demo\n\nvar C = 3 // trailing\n' >"$REPO/pkg/new.go"
    expect_fail_with "Untracked file comment fails" "pkg/new.go:3:"
}

test_string_literals_pass() {
    reset_repo
    printf '%s\nvar U = "http://example.com//x"\nvar R = `// raw`\n' "$BASELINE" >"$REPO/pkg/a.go"
    commit_all
    expect_pass "// inside string literals passes"
}

test_go_directive_passes() {
    reset_repo
    printf '//go:build linux\n\n%s' "$BASELINE" >"$REPO/pkg/a.go"
    commit_all
    expect_pass "//go:build directive passes"
}

test_nolint_fails() {
    reset_repo
    printf '%s\nvar N = 1 //nolint:gosec\n' "$BASELINE" >"$REPO/pkg/a.go"
    commit_all
    expect_fail_with "//nolint fails" "//nolint:gosec"
}

test_editing_code_near_existing_comment_passes() {
    reset_repo
    printf '%s' "${BASELINE/return 1/return 42}" >"$REPO/pkg/a.go"
    commit_all
    expect_pass "Editing code next to an existing comment passes"
}

test_non_go_files_ignored() {
    reset_repo
    printf '// comment\n' >"$REPO/pkg/x.ts"
    printf '# comment\n' >"$REPO/run.sh"
    commit_all
    expect_pass "Non-Go files are ignored"
}

test_added_line_starting_with_plus_plus() {
    reset_repo
    printf 'package demo\n\nvar P = 1 +\n++ 2\n\n// Existing comment stays allowed.\nfunc A() int {\n\treturn 1\n}\n\n// tail\n' >"$REPO/pkg/a.go"
    expect_fail_with "Hunk lines starting with ++ do not confuse file tracking" "pkg/a.go:11: // tail"
}

test_explicit_base_env() {
    reset_repo
    printf '%s\n// first\n' "$BASELINE" >"$REPO/pkg/a.go"
    commit_all
    local mid
    mid=$(git_repo rev-parse HEAD)
    printf '%s\n// first\nvar Z = 0\n' "$BASELINE" >"$REPO/pkg/a.go"
    commit_all
    LOOM_COMMENT_BASE="$mid" expect_pass "LOOM_COMMENT_BASE limits the diff"
    LOOM_COMMENT_BASE=nope run_guard
    if [[ "$RUN_EXIT" -eq 2 ]]; then pass "Bad LOOM_COMMENT_BASE exits 2"; else fail "Bad LOOM_COMMENT_BASE exit $RUN_EXIT"; fi
}

test_nearest_default_ref() {
    reset_repo
    printf '%s\n// on v5\n' "$BASELINE" >"$REPO/pkg/a.go"
    commit_all
    git_repo update-ref refs/remotes/origin/v5 HEAD
    printf '%s\n// on v5\nvar Q = 1\n' "$BASELINE" >"$REPO/pkg/a.go"
    commit_all
    expect_pass "Default base picks the nearest of origin/main and origin/v5"
}

test_missing_default_ref_errors() {
    reset_repo
    git_repo update-ref -d refs/remotes/origin/main
    run_guard
    if [[ "$RUN_EXIT" -eq 2 && "$RUN_OUTPUT" == *"LOOM_COMMENT_BASE"* ]]; then
        pass "Missing default refs exit 2 with hint"
    else
        fail "Missing default refs (exit $RUN_EXIT): $RUN_OUTPUT"
    fi
}

test_ci_pull_request_base() {
    reset_repo
    git_repo update-ref refs/remotes/origin/v5 HEAD
    git_repo update-ref -d refs/remotes/origin/main
    printf '%s\n// pr comment\n' "$BASELINE" >"$REPO/pkg/a.go"
    commit_all
    GITHUB_EVENT_NAME=pull_request GITHUB_BASE_REF=v5 expect_fail_with "CI pull_request diffs against origin/\$GITHUB_BASE_REF" "pr comment"
}

test_ci_push_before() {
    reset_repo
    local before
    before=$(git_repo rev-parse HEAD)
    printf '%s\n// pushed\n' "$BASELINE" >"$REPO/pkg/a.go"
    commit_all
    GITHUB_EVENT_NAME=push LOOM_COMMENT_PUSH_BEFORE="$before" expect_fail_with "CI push diffs against before-SHA" "pushed"
    GITHUB_EVENT_NAME=push LOOM_COMMENT_PUSH_BEFORE=0000000000000000000000000000000000000000 expect_pass "CI push with zero before-SHA skips"
    GITHUB_EVENT_NAME=push LOOM_COMMENT_PUSH_BEFORE=1234567890abcdef1234567890abcdef12345678 expect_pass "CI push with unknown before-SHA skips"
}

test_unknown_language() {
    reset_repo
    run_guard cobol
    if [[ "$RUN_EXIT" -eq 2 ]]; then pass "Unknown language exits 2"; else fail "Unknown language exit $RUN_EXIT"; fi
}

test_no_changes_pass
test_committed_line_comment_fails
test_uncommitted_block_comment_fails
test_untracked_file_fails
test_string_literals_pass
test_go_directive_passes
test_nolint_fails
test_editing_code_near_existing_comment_passes
test_non_go_files_ignored
test_added_line_starting_with_plus_plus
test_explicit_base_env
test_nearest_default_ref
test_missing_default_ref_errors
test_ci_pull_request_base
test_ci_push_before
test_unknown_language

echo ""
echo "Results: $PASS_COUNT passed, $FAIL_COUNT failed"
[[ "$FAIL_COUNT" -eq 0 ]]
