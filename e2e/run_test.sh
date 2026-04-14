#!/bin/sh
# run_test.sh — Orchestrate the loomcli E2E test suite inside the Docker container.
# Runs: smoke test, unit tests, and E2E tests across all backends (claude, codex, opencode).
#
# Usage: run_test.sh [OPTIONS] [-- GO_TEST_ARGS...]
# Exit codes: 0 = all passed, 1 = test failure, 2 = usage/environment error

# No set -e: phase functions intentionally handle non-zero exit codes from go test.

# ── Defaults ────────────────────────────────────────────────────────────────
PHASE=""
BACKEND=""
FAIL_FAST=1
TIMEOUT="5m"
VERBOSE=0
QUIET=0

ALL_BACKENDS="claude codex opencode"

# Phase results: "name:status:elapsed"
RESULTS=""
E2E_BACKEND_RESULTS=""
OVERALL_FAIL=0
OVERALL_START=""

# ── Usage ───────────────────────────────────────────────────────────────────
usage() {
    cat <<EOF
Usage: run_test.sh [OPTIONS] [-- GO_TEST_ARGS...]

Options:
  --phase PHASE      Run only this phase (smoke, unit, e2e)
  --backend NAME     Run E2E tests for only this backend (claude, codex, opencode)
  --no-fail-fast     Continue running phases after a failure
  --timeout DURATION Go test timeout (default: 5m)
  -v, --verbose      Verbose test output
  -q, --quiet        Minimal output (phase summaries only)
  -h, --help         Show usage

Anything after -- is appended to go test invocations, e.g.:
  run_test.sh -- -run TestE2E_TmuxSession
EOF
    exit 0
}

# ── Argument parsing ────────────────────────────────────────────────────────
while [ $# -gt 0 ]; do
    case "$1" in
        --phase)
            if [ -z "${2:-}" ]; then
                printf "Error: --phase requires a value\n" >&2
                exit 2
            fi
            case "$2" in
                smoke|unit|e2e) PHASE="$2" ;;
                *) printf "Error: unknown phase '%s' (must be smoke, unit, or e2e)\n" "$2" >&2; exit 2 ;;
            esac
            shift 2 ;;
        --backend)
            if [ -z "${2:-}" ]; then
                printf "Error: --backend requires a value\n" >&2
                exit 2
            fi
            case "$2" in
                claude|codex|opencode) BACKEND="$2" ;;
                *) printf "Error: unknown backend '%s' (must be claude, codex, or opencode)\n" "$2" >&2; exit 2 ;;
            esac
            shift 2 ;;
        --no-fail-fast) FAIL_FAST=0; shift ;;
        --timeout)
            if [ -z "${2:-}" ]; then
                printf "Error: --timeout requires a value\n" >&2
                exit 2
            fi
            TIMEOUT="$2"; shift 2 ;;
        -v|--verbose) VERBOSE=1; shift ;;
        -q|--quiet)   QUIET=1; shift ;;
        -h|--help)    usage ;;
        --)           shift; break ;;
        *)            printf "Error: unknown option '%s'\n" "$1" >&2; exit 2 ;;
    esac
done

# After parsing, "$@" contains extra go test args (preserved with proper quoting).

# ── Output helpers ──────────────────────────────────────────────────────────
elapsed() {
    _start="$1"
    _end=$(date +%s)
    printf '%d' "$(( _end - _start ))"
}

print_banner() {
    if [ "$QUIET" -eq 1 ]; then return; fi
    printf "\n"
    printf "══════════════════════════════════════════════════\n"
    printf "  loomcli E2E Test Suite\n"
    printf "══════════════════════════════════════════════════\n"
    printf "\n"
}

print_phase_header() {
    if [ "$QUIET" -eq 1 ]; then return; fi
    printf "\n━━━ Phase %s/%s: %s ━━━\n" "$1" "$2" "$3"
}

print_phase_result() {
    _name="$1"
    _status="$2"
    _secs="$3"
    if [ "$QUIET" -eq 1 ]; then return; fi
    if [ "$_status" = "PASS" ]; then
        printf "✓ %s passed (%ss)\n" "$_name" "$_secs"
    elif [ "$_status" = "SKIP" ]; then
        printf "⊘ %s skipped (%ss)\n" "$_name" "$_secs"
    else
        printf "✗ %s FAILED (%ss)\n" "$_name" "$_secs"
    fi
}

add_result() {
    _entry="$1:$2:$3"
    if [ -z "$RESULTS" ]; then
        RESULTS="$_entry"
    else
        RESULTS="$RESULTS
$_entry"
    fi
}

add_backend_result() {
    _entry="$1:$2"
    if [ -z "$E2E_BACKEND_RESULTS" ]; then
        E2E_BACKEND_RESULTS="$_entry"
    else
        E2E_BACKEND_RESULTS="$E2E_BACKEND_RESULTS
$_entry"
    fi
}

print_summary() {
    _total_elapsed=$(elapsed "$OVERALL_START")

    printf "\n"
    printf "══════════════════════════════════════════════════\n"
    printf "  RESULTS\n"
    printf "══════════════════════════════════════════════════\n"

    echo "$RESULTS" | while IFS=: read -r _name _status _secs; do
        if [ "$_status" = "PASS" ]; then
            printf "  ✓ %-20s PASS (%ss)\n" "$_name" "$_secs"
        elif [ "$_status" = "SKIP" ]; then
            printf "  ⊘ %-20s SKIP (%ss)\n" "$_name" "$_secs"
        else
            printf "  ✗ %-20s FAIL (%ss)\n" "$_name" "$_secs"
        fi
    done

    if [ -n "$E2E_BACKEND_RESULTS" ]; then
        echo "$E2E_BACKEND_RESULTS" | while IFS=: read -r _be _st; do
            if [ "$_st" = "PASS" ]; then
                printf "    - %-18s PASS\n" "$_be"
            elif [ "$_st" = "SKIP" ]; then
                printf "    - %-18s SKIP\n" "$_be"
            else
                printf "    - %-18s FAIL\n" "$_be"
            fi
        done
    fi

    printf "══════════════════════════════════════════════════\n"
    if [ "$OVERALL_FAIL" -eq 0 ]; then
        printf "  All phases passed (%ss total)\n" "$_total_elapsed"
    else
        printf "  Some phases FAILED (%ss total)\n" "$_total_elapsed"
    fi
    printf "══════════════════════════════════════════════════\n"
}

# ── Cleanup (trap EXIT) ────────────────────────────────────────────────────
cleanup() {
    if command -v tmux >/dev/null 2>&1; then
        tmux list-sessions -F '#{session_name}' 2>/dev/null | grep '^loom-e2e-test-' | while read -r sess; do
            tmux kill-session -t "$sess" 2>/dev/null || true
        done
    fi
}
trap cleanup EXIT

# ── Phase: Smoke Test ───────────────────────────────────────────────────────
# Args: none (extra go test args are not relevant to smoke test)
phase_smoke() {
    if ! command -v verify_todo.sh >/dev/null 2>&1; then
        printf "Error: verify_todo.sh not found on PATH\n" >&2
        add_result "Smoke Test" "FAIL" "0"
        print_phase_result "Smoke Test" "FAIL" "0"
        return 1
    fi

    _start=$(date +%s)

    # Run verify_todo.sh
    if [ "$QUIET" -eq 1 ]; then
        verify_todo.sh -q
    elif [ "$VERBOSE" -eq 1 ]; then
        verify_todo.sh -v
    else
        verify_todo.sh
    fi
    _todo_rc=$?

    # Run verify_list.sh (optional — skip if not installed)
    _list_rc=0
    if command -v verify_list.sh >/dev/null 2>&1; then
        if [ "$QUIET" -eq 1 ]; then
            verify_list.sh -q
        elif [ "$VERBOSE" -eq 1 ]; then
            verify_list.sh -v
        else
            verify_list.sh
        fi
        _list_rc=$?
    else
        printf "Warning: verify_list.sh not found on PATH, skipping\n" >&2
    fi

    # Run verify_bd.sh (optional — skip if not installed)
    _bd_rc=0
    if command -v verify_bd.sh >/dev/null 2>&1; then
        if [ "$QUIET" -eq 1 ]; then
            verify_bd.sh -q
        elif [ "$VERBOSE" -eq 1 ]; then
            verify_bd.sh -v
        else
            verify_bd.sh
        fi
        _bd_rc=$?
    else
        printf "Warning: verify_bd.sh not found on PATH, skipping\n" >&2
    fi

    _secs=$(elapsed "$_start")

    # Fail if any script failed
    if [ "$_todo_rc" -ne 0 ] || [ "$_list_rc" -ne 0 ] || [ "$_bd_rc" -ne 0 ]; then
        add_result "Smoke Test" "FAIL" "$_secs"
        print_phase_result "Smoke Test" "FAIL" "$_secs"
        return 1
    else
        add_result "Smoke Test" "PASS" "$_secs"
        print_phase_result "Smoke Test" "PASS" "$_secs"
        return 0
    fi
}

# ── Phase: Unit Tests ──────────────────────────────────────────────────────
# Args: none (extra go test args are not passed to unit tests)
# Note: In container mode (no Go toolchain), unit tests are skipped.
# Run unit tests on the host or in CI with `go test ./...`.
phase_unit() {
    _start=$(date +%s)

    if ! command -v go >/dev/null 2>&1; then
        if [ "$QUIET" -eq 0 ]; then
            printf "Skipping unit tests (no Go toolchain in container; run on host)\n"
        fi
        add_result "Unit Tests" "SKIP" "0"
        print_phase_result "Unit Tests" "SKIP" "0"
        return 0
    fi

    if [ "$QUIET" -eq 0 ]; then
        if [ "$VERBOSE" -eq 1 ]; then
            printf "Running go test -timeout %s -count=1 -v ./...\n" "$TIMEOUT"
        else
            printf "Running go test -timeout %s -count=1 ./...\n" "$TIMEOUT"
        fi
    fi

    if [ "$VERBOSE" -eq 1 ]; then
        if [ "$QUIET" -eq 1 ]; then
            go test -timeout "$TIMEOUT" -count=1 -v ./... >/dev/null 2>&1
        else
            go test -timeout "$TIMEOUT" -count=1 -v ./...
        fi
    else
        if [ "$QUIET" -eq 1 ]; then
            go test -timeout "$TIMEOUT" -count=1 ./... >/dev/null 2>&1
        else
            go test -timeout "$TIMEOUT" -count=1 ./...
        fi
    fi
    _rc=$?
    _secs=$(elapsed "$_start")

    if [ "$_rc" -eq 0 ]; then
        add_result "Unit Tests" "PASS" "$_secs"
        print_phase_result "Unit Tests" "PASS" "$_secs"
        return 0
    else
        add_result "Unit Tests" "FAIL" "$_secs"
        print_phase_result "Unit Tests" "FAIL" "$_secs"
        return 1
    fi
}

# ── Phase: E2E Tests ───────────────────────────────────────────────────────
# Args: "$@" = extra go test args forwarded from the command line
phase_e2e() {
    _start=$(date +%s)

    # Need either pre-compiled test binary or Go toolchain
    if ! command -v cli-e2e.test >/dev/null 2>&1 && ! command -v go >/dev/null 2>&1; then
        if [ "$QUIET" -eq 0 ]; then
            printf "Skipping E2E tests (no test binary or Go toolchain; build with go test -c)\n"
        fi
        add_result "E2E Tests" "SKIP" "0"
        print_phase_result "E2E Tests" "SKIP" "0"
        return 0
    fi

    _backends="$ALL_BACKENDS"
    if [ -n "$BACKEND" ]; then
        _backends="$BACKEND"
    fi

    _e2e_fail=0

    for _be in $_backends; do
        # Verify stub exists
        if ! command -v "$_be" >/dev/null 2>&1; then
            if [ "$QUIET" -eq 0 ]; then
                printf "\n  Backend: %s\n  WARNING: %s not found on PATH, skipping\n" "$_be" "$_be"
            fi
            add_backend_result "$_be" "SKIP"
            continue
        fi

        _be_start=$(date +%s)

        if [ "$QUIET" -eq 0 ]; then
            printf "\n  Backend: %s\n" "$_be"
        fi

        # Use pre-compiled test binary if available, else fall back to go test
        if command -v cli-e2e.test >/dev/null 2>&1; then
            if [ "$QUIET" -eq 1 ]; then
                LOOM_BACKEND="$_be" cli-e2e.test -test.timeout "$TIMEOUT" -test.count=1 -test.v "$@" >/dev/null 2>&1
            else
                LOOM_BACKEND="$_be" cli-e2e.test -test.timeout "$TIMEOUT" -test.count=1 -test.v "$@"
            fi
        else
            if [ "$QUIET" -eq 1 ]; then
                LOOM_BACKEND="$_be" go test -tags e2e -timeout "$TIMEOUT" -count=1 -v ./internal/cli/ "$@" >/dev/null 2>&1
            else
                LOOM_BACKEND="$_be" go test -tags e2e -timeout "$TIMEOUT" -count=1 -v ./internal/cli/ "$@"
            fi
        fi
        _rc=$?
        _be_secs=$(elapsed "$_be_start")

        if [ "$_rc" -eq 0 ]; then
            if [ "$QUIET" -eq 0 ]; then
                printf "  ✓ %s passed (%ss)\n" "$_be" "$_be_secs"
            fi
            add_backend_result "$_be" "PASS"
        else
            _e2e_fail=1
            if [ "$QUIET" -eq 0 ]; then
                printf "  ✗ %s FAILED (%ss)\n" "$_be" "$_be_secs"
            fi
            add_backend_result "$_be" "FAIL"
        fi
    done

    _secs=$(elapsed "$_start")

    if [ "$_e2e_fail" -eq 0 ]; then
        add_result "E2E Tests" "PASS" "$_secs"
        print_phase_result "E2E Tests" "PASS" "$_secs"
        return 0
    else
        add_result "E2E Tests" "FAIL" "$_secs"
        print_phase_result "E2E Tests" "FAIL" "$_secs"
        return 1
    fi
}

# ── Main ────────────────────────────────────────────────────────────────────
OVERALL_START=$(date +%s)
print_banner

# Determine which phases to run
if [ -n "$PHASE" ]; then
    PHASES="$PHASE"
else
    PHASES="smoke unit e2e"
fi

# Count phases for header numbering
_total_phases=0
for _ in $PHASES; do
    _total_phases=$(( _total_phases + 1 ))
done

_phase_num=0
for _p in $PHASES; do
    _phase_num=$(( _phase_num + 1 ))

    case "$_p" in
        smoke)
            print_phase_header "$_phase_num" "$_total_phases" "Smoke Test"
            phase_smoke || {
                OVERALL_FAIL=1
                if [ "$FAIL_FAST" -eq 1 ]; then
                    print_summary
                    exit 1
                fi
            }
            ;;
        unit)
            print_phase_header "$_phase_num" "$_total_phases" "Unit Tests"
            phase_unit || {
                OVERALL_FAIL=1
                if [ "$FAIL_FAST" -eq 1 ]; then
                    print_summary
                    exit 1
                fi
            }
            ;;
        e2e)
            print_phase_header "$_phase_num" "$_total_phases" "E2E Tests"
            phase_e2e "$@" || {
                OVERALL_FAIL=1
                if [ "$FAIL_FAST" -eq 1 ]; then
                    print_summary
                    exit 1
                fi
            }
            ;;
    esac
done

print_summary

if [ "$OVERALL_FAIL" -ne 0 ]; then
    exit 1
fi
exit 0
