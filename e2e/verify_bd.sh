#!/bin/sh
# verify_bd.sh — Smoke test for bd subcommand lifecycle in the E2E Docker container.
# Verifies: create, show, update, status transitions, labels, deps, comments,
# close/reopen, stats, list/ready filtering.
#
# Usage: verify_bd.sh [-v|--verbose] [-q|--quiet] [-h|--help]
# Exit codes: 0 = all passed, 1 = one or more failed

VERBOSE=0
QUIET=0
PASS_COUNT=0
FAIL_COUNT=0
CLEANUP_DIR=""

TASK_A=""
TASK_B=""
EPIC_ID=""

# ── Argument parsing ──────────────────────────────────────────────────────
while [ $# -gt 0 ]; do
    case "$1" in
        -v|--verbose) VERBOSE=1; shift ;;
        -q|--quiet)   QUIET=1; shift ;;
        -h|--help)
            printf "Usage: verify_bd.sh [OPTIONS]\n\n"
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
    printf "=== verify_bd.sh smoke test ===\n"
fi

# Force direct SQLite mode (no daemon)
export BD_NO_DAEMON=1

# ── Group 1: bd init and create ──────────────────────────────────────────
CLEANUP_DIR=$(mktemp -d)
WORK_DIR="$CLEANUP_DIR/workspace"
mkdir -p "$WORK_DIR"
cd "$WORK_DIR" || { fail "cd to workspace" "Cannot cd to $WORK_DIR"; exit 1; }

git init -q .
git config user.email "test@e2e"
git config user.name "E2E Test"
git commit --allow-empty -q -m "initial"
verbose "Git repo initialized in $WORK_DIR"

if bd init >/dev/null 2>&1; then
    pass "bd init"
    verbose "BD initialized in $WORK_DIR"
else
    fail "bd init" "bd init failed in $WORK_DIR"
fi

# Create task A (lifecycle test)
create_out=$(bd create "Lifecycle test task" --type=task --priority=2 --description="Task for lifecycle testing" 2>&1)
TASK_A=$(printf '%s\n' "$create_out" | awk '/[Ii]ssue:/{print $NF}')
if [ -n "$TASK_A" ]; then
    pass "Create task A: $TASK_A"
else
    fail "Create task A" "bd create returned no ID: $create_out"
fi

# Create task B (blocker)
create_out=$(bd create "Blocker task" --type=task --priority=1 --description="Blocks the lifecycle task" 2>&1)
TASK_B=$(printf '%s\n' "$create_out" | awk '/[Ii]ssue:/{print $NF}')
if [ -n "$TASK_B" ]; then
    pass "Create task B (blocker): $TASK_B"
else
    fail "Create task B (blocker)" "bd create returned no ID: $create_out"
fi

# Create epic
create_out=$(bd create "Lifecycle epic" --type=epic --priority=1 --description="Parent epic" 2>&1)
EPIC_ID=$(printf '%s\n' "$create_out" | awk '/[Ii]ssue:/{print $NF}')
if [ -n "$EPIC_ID" ]; then
    pass "Create epic: $EPIC_ID"
else
    fail "Create epic" "bd create returned no ID: $create_out"
fi

# ── Group 2: bd show (human and JSON) ────────────────────────────────────
if [ -n "$TASK_A" ]; then
    show_out=$(bd show "$TASK_A" 2>&1); show_ec=$?
    if [ "$show_ec" -eq 0 ]; then
        pass "bd show exits 0"
    else
        fail "bd show exits 0" "Exit code: $show_ec"
    fi

    if printf '%s' "$show_out" | grep -q "Lifecycle test task"; then
        pass "bd show contains title"
    else
        fail "bd show contains title" "Output: $show_out"
    fi

    show_json=$(bd show "$TASK_A" --json 2>&1); show_json_ec=$?
    if [ "$show_json_ec" -eq 0 ]; then
        pass "bd show --json exits 0"
    else
        fail "bd show --json exits 0" "Exit code: $show_json_ec"
    fi

    json_id=$(printf '%s' "$show_json" | jq -r '.[0].id')
    if [ "$json_id" = "$TASK_A" ]; then
        pass "bd show --json: id matches"
    else
        fail "bd show --json: id matches" "Expected '$TASK_A', got '$json_id'"
    fi

    json_title=$(printf '%s' "$show_json" | jq -r '.[0].title')
    if [ "$json_title" = "Lifecycle test task" ]; then
        pass "bd show --json: title matches"
    else
        fail "bd show --json: title matches" "Expected 'Lifecycle test task', got '$json_title'"
    fi

    json_status=$(printf '%s' "$show_json" | jq -r '.[0].status')
    if [ "$json_status" = "open" ]; then
        pass "bd show --json: status is open"
    else
        fail "bd show --json: status is open" "Expected 'open', got '$json_status'"
    fi

    json_prio=$(printf '%s' "$show_json" | jq -r '.[0].priority')
    if [ "$json_prio" = "2" ]; then
        pass "bd show --json: priority is 2"
    else
        fail "bd show --json: priority is 2" "Expected '2', got '$json_prio'"
    fi

    json_type=$(printf '%s' "$show_json" | jq -r '.[0].issue_type')
    if [ "$json_type" = "task" ]; then
        pass "bd show --json: issue_type is task"
    else
        fail "bd show --json: issue_type is task" "Expected 'task', got '$json_type'"
    fi
else
    fail "bd show (skipped)" "No task A ID"
fi

# ── Group 3: bd update — status transitions ──────────────────────────────
if [ -n "$TASK_A" ]; then
    update_out=$(bd update "$TASK_A" --status in_progress 2>&1); update_ec=$?
    if [ "$update_ec" -eq 0 ]; then
        pass "bd update --status in_progress"
    else
        fail "bd update --status in_progress" "Exit code: $update_ec, Output: $update_out"
    fi

    json_status=$(bd show "$TASK_A" --json 2>&1 | jq -r '.[0].status')
    if [ "$json_status" = "in_progress" ]; then
        pass "Status verified: in_progress"
    else
        fail "Status verified: in_progress" "Expected 'in_progress', got '$json_status'"
    fi

    bd update "$TASK_A" --status review >/dev/null 2>&1
    json_status=$(bd show "$TASK_A" --json 2>&1 | jq -r '.[0].status')
    if [ "$json_status" = "review" ]; then
        pass "Status verified: review"
    else
        fail "Status verified: review" "Expected 'review', got '$json_status'"
    fi

    bd update "$TASK_A" --status open >/dev/null 2>&1
    json_status=$(bd show "$TASK_A" --json 2>&1 | jq -r '.[0].status')
    if [ "$json_status" = "open" ]; then
        pass "Status verified: reset to open"
    else
        fail "Status verified: reset to open" "Expected 'open', got '$json_status'"
    fi
else
    fail "bd update status transitions (skipped)" "No task A ID"
fi

# ── Group 4: bd update — assignee and design ─────────────────────────────
if [ -n "$TASK_A" ]; then
    bd update "$TASK_A" --assignee="falcon" >/dev/null 2>&1
    json_assignee=$(bd show "$TASK_A" --json 2>&1 | jq -r '.[0].assignee')
    if [ "$json_assignee" = "falcon" ]; then
        pass "Assignee set to falcon"
    else
        fail "Assignee set to falcon" "Expected 'falcon', got '$json_assignee'"
    fi

    bd update "$TASK_A" --design="## Plan
Step 1: Do the thing" >/dev/null 2>&1
    json_design=$(bd show "$TASK_A" --json 2>&1 | jq -r '.[0].design')
    if printf '%s' "$json_design" | grep -q "Plan"; then
        pass "Design field contains Plan"
    else
        fail "Design field contains Plan" "Got: $json_design"
    fi

    bd update "$TASK_A" --assignee="" >/dev/null 2>&1
    json_assignee=$(bd show "$TASK_A" --json 2>&1 | jq -r '.[0].assignee // ""')
    if [ "$json_assignee" = "" ] || [ "$json_assignee" = "null" ]; then
        pass "Assignee cleared"
    else
        fail "Assignee cleared" "Expected empty, got '$json_assignee'"
    fi
else
    fail "bd update assignee/design (skipped)" "No task A ID"
fi

# ── Group 5: bd update — claim (atomic) ──────────────────────────────────
if [ -n "$TASK_A" ]; then
    # Ensure task is open first
    bd update "$TASK_A" --status open >/dev/null 2>&1

    claim_out=$(bd update "$TASK_A" --claim 2>&1); claim_ec=$?
    if [ "$claim_ec" -eq 0 ]; then
        pass "bd update --claim exits 0"
    else
        fail "bd update --claim exits 0" "Exit code: $claim_ec, Output: $claim_out"
    fi

    json_status=$(bd show "$TASK_A" --json 2>&1 | jq -r '.[0].status')
    if [ "$json_status" = "in_progress" ]; then
        pass "Claim sets status to in_progress"
    else
        fail "Claim sets status to in_progress" "Expected 'in_progress', got '$json_status'"
    fi

    json_assignee=$(bd show "$TASK_A" --json 2>&1 | jq -r '.[0].assignee')
    if [ -n "$json_assignee" ] && [ "$json_assignee" != "null" ] && [ "$json_assignee" != "" ]; then
        pass "Claim sets assignee"
    else
        fail "Claim sets assignee" "Assignee is empty after claim"
    fi

    # Reset for later tests
    bd update "$TASK_A" --status open >/dev/null 2>&1
else
    fail "bd update --claim (skipped)" "No task A ID"
fi

# ── Group 6: bd label add and remove ─────────────────────────────────────
if [ -n "$TASK_A" ]; then
    label_out=$(bd label add "$TASK_A" needs-revision 2>&1); label_ec=$?
    if [ "$label_ec" -eq 0 ]; then
        pass "bd label add exits 0"
    else
        fail "bd label add exits 0" "Exit code: $label_ec, Output: $label_out"
    fi

    if printf '%s' "$label_out" | grep -q "needs-revision"; then
        pass "bd label add output contains label name"
    else
        fail "bd label add output contains label name" "Output: $label_out"
    fi

    json_labels=$(bd show "$TASK_A" --json 2>&1 | jq -r '.[0].labels[]' 2>/dev/null)
    if printf '%s' "$json_labels" | grep -q "needs-revision"; then
        pass "Label appears in show --json"
    else
        fail "Label appears in show --json" "Labels: $json_labels"
    fi

    remove_out=$(bd label remove "$TASK_A" needs-revision 2>&1); remove_ec=$?
    if [ "$remove_ec" -eq 0 ]; then
        pass "bd label remove exits 0"
    else
        fail "bd label remove exits 0" "Exit code: $remove_ec, Output: $remove_out"
    fi

    if printf '%s' "$remove_out" | grep -q "Removed"; then
        pass "bd label remove output contains Removed"
    else
        fail "bd label remove output contains Removed" "Output: $remove_out"
    fi

    json_labels=$(bd show "$TASK_A" --json 2>&1 | jq -r '.[0].labels[]' 2>/dev/null)
    if printf '%s' "$json_labels" | grep -q "needs-revision"; then
        fail "Label removed from show --json" "Label still present: $json_labels"
    else
        pass "Label removed from show --json"
    fi
else
    fail "bd label add/remove (skipped)" "No task A ID"
fi

# ── Group 7: bd dep add and blocked semantics ────────────────────────────
if [ -n "$TASK_A" ] && [ -n "$TASK_B" ]; then
    # Ensure both tasks are open
    bd update "$TASK_A" --status open >/dev/null 2>&1
    bd update "$TASK_B" --status open >/dev/null 2>&1

    dep_out=$(bd dep add "$TASK_A" "$TASK_B" 2>&1); dep_ec=$?
    if [ "$dep_ec" -eq 0 ]; then
        pass "bd dep add exits 0"
    else
        fail "bd dep add exits 0" "Exit code: $dep_ec, Output: $dep_out"
    fi

    if printf '%s' "$dep_out" | grep -q "dependency"; then
        pass "bd dep add output contains dependency"
    else
        fail "bd dep add output contains dependency" "Output: $dep_out"
    fi

    # Verify TASK_A has dependencies
    json_deps=$(bd show "$TASK_A" --json 2>&1 | jq '.[0].dependencies | length')
    if [ "$json_deps" -gt 0 ] 2>/dev/null; then
        pass "TASK_A has dependencies"
    else
        fail "TASK_A has dependencies" "Dependencies length: $json_deps"
    fi

    # Verify TASK_A appears in blocked list
    blocked_json=$(bd blocked --json 2>&1)
    if printf '%s' "$blocked_json" | grep -q "$TASK_A"; then
        pass "TASK_A appears in bd blocked"
    else
        fail "TASK_A appears in bd blocked" "Blocked output: $blocked_json"
    fi

    # Verify TASK_A does NOT appear in ready list
    ready_json=$(bd ready --json 2>&1)
    if printf '%s' "$ready_json" | grep -q "$TASK_A"; then
        fail "TASK_A not in bd ready (blocked)" "TASK_A found in ready: $ready_json"
    else
        pass "TASK_A not in bd ready (blocked)"
    fi

    # Close the blocker
    bd close "$TASK_B" --reason="done" >/dev/null 2>&1

    # Verify TASK_A now appears in ready
    ready_json=$(bd ready --json 2>&1)
    if printf '%s' "$ready_json" | grep -q "$TASK_A"; then
        pass "TASK_A in bd ready (blocker resolved)"
    else
        fail "TASK_A in bd ready (blocker resolved)" "Ready output: $ready_json"
    fi

    # Clean up dependency
    dep_rm_out=$(bd dep remove "$TASK_A" "$TASK_B" 2>&1); dep_rm_ec=$?
    if [ "$dep_rm_ec" -eq 0 ]; then
        pass "bd dep remove exits 0"
    else
        fail "bd dep remove exits 0" "Exit code: $dep_rm_ec, Output: $dep_rm_out"
    fi

    # Reopen TASK_B for later tests
    bd reopen "$TASK_B" >/dev/null 2>&1
else
    fail "bd dep add/blocked (skipped)" "Missing task IDs"
fi

# ── Group 8: bd comments ─────────────────────────────────────────────────
if [ -n "$TASK_A" ]; then
    comment_out=$(bd comments add "$TASK_A" "This is a test comment" 2>&1); comment_ec=$?
    if [ "$comment_ec" -eq 0 ]; then
        pass "bd comments add exits 0"
    else
        fail "bd comments add exits 0" "Exit code: $comment_ec, Output: $comment_out"
    fi

    if printf '%s' "$comment_out" | grep -q "Comment added"; then
        pass "bd comments add output contains 'Comment added'"
    else
        fail "bd comments add output contains 'Comment added'" "Output: $comment_out"
    fi

    comments_out=$(bd comments "$TASK_A" 2>&1); comments_ec=$?
    if [ "$comments_ec" -eq 0 ]; then
        pass "bd comments list exits 0"
    else
        fail "bd comments list exits 0" "Exit code: $comments_ec"
    fi

    if printf '%s' "$comments_out" | grep -q "test comment"; then
        pass "bd comments list contains comment text"
    else
        fail "bd comments list contains comment text" "Output: $comments_out"
    fi

    comments_json=$(bd comments "$TASK_A" --json 2>&1)
    json_count=$(printf '%s' "$comments_json" | jq 'length')
    if [ "$json_count" -ge 1 ] 2>/dev/null; then
        pass "bd comments --json has at least 1 comment"
    else
        fail "bd comments --json has at least 1 comment" "Count: $json_count"
    fi

    json_text=$(printf '%s' "$comments_json" | jq -r '.[0].text')
    if printf '%s' "$json_text" | grep -q "test comment"; then
        pass "bd comments --json text matches"
    else
        fail "bd comments --json text matches" "Got: $json_text"
    fi
else
    fail "bd comments (skipped)" "No task A ID"
fi

# ── Group 9: bd close and reopen ─────────────────────────────────────────
if [ -n "$TASK_A" ]; then
    # Ensure task is open
    bd update "$TASK_A" --status open >/dev/null 2>&1

    close_out=$(bd close "$TASK_A" --reason="completed testing" 2>&1); close_ec=$?
    if [ "$close_ec" -eq 0 ]; then
        pass "bd close exits 0"
    else
        fail "bd close exits 0" "Exit code: $close_ec, Output: $close_out"
    fi

    if printf '%s' "$close_out" | grep -q "Closed"; then
        pass "bd close output contains Closed"
    else
        fail "bd close output contains Closed" "Output: $close_out"
    fi

    json_status=$(bd show "$TASK_A" --json 2>&1 | jq -r '.[0].status')
    if [ "$json_status" = "closed" ]; then
        pass "Status verified: closed"
    else
        fail "Status verified: closed" "Expected 'closed', got '$json_status'"
    fi

    closed_json=$(bd list --status=closed --json 2>&1)
    if printf '%s' "$closed_json" | grep -q "$TASK_A"; then
        pass "TASK_A in bd list --status=closed"
    else
        fail "TASK_A in bd list --status=closed" "Output: $closed_json"
    fi

    ready_json=$(bd ready --json 2>&1)
    if printf '%s' "$ready_json" | grep -q "$TASK_A"; then
        fail "TASK_A not in bd ready (closed)" "TASK_A found in ready"
    else
        pass "TASK_A not in bd ready (closed)"
    fi

    reopen_out=$(bd reopen "$TASK_A" 2>&1); reopen_ec=$?
    if [ "$reopen_ec" -eq 0 ]; then
        pass "bd reopen exits 0"
    else
        fail "bd reopen exits 0" "Exit code: $reopen_ec, Output: $reopen_out"
    fi

    if printf '%s' "$reopen_out" | grep -q "Reopened"; then
        pass "bd reopen output contains Reopened"
    else
        fail "bd reopen output contains Reopened" "Output: $reopen_out"
    fi

    json_status=$(bd show "$TASK_A" --json 2>&1 | jq -r '.[0].status')
    if [ "$json_status" = "open" ]; then
        pass "Status verified: reopened to open"
    else
        fail "Status verified: reopened to open" "Expected 'open', got '$json_status'"
    fi

    ready_json=$(bd ready --json 2>&1)
    if printf '%s' "$ready_json" | grep -q "$TASK_A"; then
        pass "TASK_A in bd ready (reopened)"
    else
        fail "TASK_A in bd ready (reopened)" "Ready output: $ready_json"
    fi
else
    fail "bd close/reopen (skipped)" "No task A ID"
fi

# ── Group 10: bd stats ───────────────────────────────────────────────────
stats_json=$(bd stats --json 2>&1); stats_ec=$?
if [ "$stats_ec" -eq 0 ]; then
    pass "bd stats --json exits 0"
else
    fail "bd stats --json exits 0" "Exit code: $stats_ec"
fi

total_issues=$(printf '%s' "$stats_json" | jq '.summary.total_issues')
if [ "$total_issues" -ge 3 ] 2>/dev/null; then
    pass "bd stats: total_issues >= 3 (got $total_issues)"
else
    fail "bd stats: total_issues >= 3" "Got: $total_issues"
fi

open_issues=$(printf '%s' "$stats_json" | jq '.summary.open_issues')
if [ "$open_issues" -ge 1 ] 2>/dev/null; then
    pass "bd stats: open_issues >= 1 (got $open_issues)"
else
    fail "bd stats: open_issues >= 1" "Got: $open_issues"
fi

stats_out=$(bd stats 2>&1); stats_out_ec=$?
if [ "$stats_out_ec" -eq 0 ]; then
    pass "bd stats (human) exits 0"
else
    fail "bd stats (human) exits 0" "Exit code: $stats_out_ec"
fi

# ── Group 11: bd list with filters ───────────────────────────────────────
list_json=$(bd list --json 2>&1)
list_count=$(printf '%s' "$list_json" | jq 'length')
if [ "$list_count" -ge 3 ] 2>/dev/null; then
    pass "bd list --json: total >= 3 (got $list_count)"
else
    fail "bd list --json: total >= 3" "Got: $list_count"
fi

open_json=$(bd list --status=open --json 2>&1)
open_count=$(printf '%s' "$open_json" | jq 'length')
if [ "$open_count" -ge 1 ] 2>/dev/null; then
    pass "bd list --status=open: found open tasks ($open_count)"
else
    fail "bd list --status=open: found open tasks" "Got: $open_count"
fi

epic_json=$(bd list --type=epic --json 2>&1)
if printf '%s' "$epic_json" | grep -q "$EPIC_ID"; then
    pass "bd list --type=epic: contains epic"
else
    fail "bd list --type=epic: contains epic" "Output: $epic_json"
fi

epic_types=$(printf '%s' "$epic_json" | jq -r '.[].issue_type' | sort -u)
if [ "$epic_types" = "epic" ]; then
    pass "bd list --type=epic: all results are epics"
else
    fail "bd list --type=epic: all results are epics" "Types found: $epic_types"
fi

# ── Group 12: bd ready with filters ──────────────────────────────────────
ready_json=$(bd ready --json 2>&1); ready_ec=$?
if [ "$ready_ec" -eq 0 ]; then
    pass "bd ready --json exits 0"
else
    fail "bd ready --json exits 0" "Exit code: $ready_ec"
fi

if printf '%s' "$ready_json" | grep -q "$TASK_A"; then
    pass "bd ready: TASK_A appears"
else
    fail "bd ready: TASK_A appears" "Ready output: $ready_json"
fi

limit_json=$(bd ready --limit 1 --json 2>&1)
limit_count=$(printf '%s' "$limit_json" | jq 'length')
if [ "$limit_count" -eq 1 ]; then
    pass "bd ready --limit 1: exactly 1 result"
else
    fail "bd ready --limit 1: exactly 1 result" "Got: $limit_count"
fi

task_ready_json=$(bd ready --type=task --json 2>&1)
task_types=$(printf '%s' "$task_ready_json" | jq -r '.[].issue_type' | sort -u)
if [ -z "$task_types" ] || [ "$task_types" = "task" ]; then
    pass "bd ready --type=task: all results are tasks"
else
    fail "bd ready --type=task: all results are tasks" "Types found: $task_types"
fi

# ── Summary ──────────────────────────────────────────────────────────────
total=$((PASS_COUNT + FAIL_COUNT))
printf "=== Results: %d passed, %d failed (of %d) ===\n" "$PASS_COUNT" "$FAIL_COUNT" "$total"

if [ "$FAIL_COUNT" -gt 0 ]; then
    exit 1
fi
exit 0
