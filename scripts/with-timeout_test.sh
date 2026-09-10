#!/usr/bin/env bash
# with-timeout_test.sh - Tests for with-timeout.sh and gate-timeout-seconds.sh
#
# Covers the wall-clock cap, whole-process-group kill, the re-entrancy marker
# that keeps a nested wrapper from creating an unreachable third process group,
# watchdog leak-freedom, and the cap resolution rules.
#
# Survivor checks match on PID, never on argv: a `pgrep -f 'sleep 3[01]'` also
# matches the harness's own command line (false positive) and a `ps -ax | grep`
# sweep has produced a false absence. Fixtures record their own child PIDs to a
# file and the assertions look those PIDs up individually.
#
# Runs in a few seconds. Like the other scripts/*_test.sh, it is run manually
# and is not wired into make/CI.
#
# Usage: ./scripts/with-timeout_test.sh
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WITH_TIMEOUT="$SCRIPT_DIR/with-timeout.sh"
RESOLVE="$SCRIPT_DIR/gate-timeout-seconds.sh"

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

check_eq() {
    # check_eq <description> <expected> <actual>
    if [ "$2" = "$3" ]; then
        pass "$1"
    else
        fail "$1 (expected '$2', got '$3')"
    fi
}

TEST_TMPDIR=$(mktemp -d)
cleanup() {
    rm -rf "$TEST_TMPDIR"
}
trap cleanup EXIT

# The wrapper must never inherit an active marker from the caller's shell.
unset LOOM_GATE_TIMEOUT_ACTIVE
unset LOOM_GATE_TIMEOUT_SECONDS
unset LOOM_RUN_TURN_TIMEOUT_SECONDS

# A fixture that records the PIDs of the children it backgrounds, so survivors
# can be surveyed by PID after the wrapper returns.
PIDFILE="$TEST_TMPDIR/grandchildren"
FIXTURE="$TEST_TMPDIR/fixture.sh"
cat > "$FIXTURE" <<'FIXEOF'
#!/usr/bin/env bash
pidfile="$1"
sleep 30 &
one=$!
sleep 31 &
two=$!
echo "$one $two" > "$pidfile"
wait
FIXEOF
chmod +x "$FIXTURE"

alive() {
    # alive <pid> - true if the pid still names a live process.
    kill -0 "$1" 2>/dev/null
}

survivors() {
    # survivors <pidfile> - echo the recorded pids that are still alive.
    _out=""
    for _pid in $(cat "$1" 2>/dev/null); do
        if alive "$_pid"; then
            _out="$_out $_pid"
        fi
    done
    echo "$_out"
}

# ---------------------------------------------------------------------------
# 1. Fast command passes through: exit 0, stdout and stderr preserved verbatim.
# ---------------------------------------------------------------------------
out=$("$WITH_TIMEOUT" 30 "fast" bash -c 'echo to-stdout; echo to-stderr >&2' 2>"$TEST_TMPDIR/err1")
rc=$?
check_eq "fast command exits 0" "0" "$rc"
check_eq "fast command stdout preserved" "to-stdout" "$out"
if grep -q '^to-stderr$' "$TEST_TMPDIR/err1"; then
    pass "fast command stderr preserved"
else
    fail "fast command stderr preserved (got: $(cat "$TEST_TMPDIR/err1"))"
fi

# ---------------------------------------------------------------------------
# 2. A non-zero exit is propagated verbatim, not turned into 124.
# ---------------------------------------------------------------------------
"$WITH_TIMEOUT" 30 "rc7" bash -c 'exit 7'
check_eq "non-zero exit propagated" "7" "$?"

# ---------------------------------------------------------------------------
# 3. Cap fires: exit 124 and a banner naming the cap and the knob.
# ---------------------------------------------------------------------------
"$WITH_TIMEOUT" 1 "slow-job" sleep 5 2>"$TEST_TMPDIR/err3"
check_eq "timeout exits 124" "124" "$?"
if grep -q 'cap of 1s' "$TEST_TMPDIR/err3"; then
    pass "timeout banner names the cap"
else
    fail "timeout banner names the cap (got: $(cat "$TEST_TMPDIR/err3"))"
fi
if grep -q 'LOOM_GATE_TIMEOUT_SECONDS' "$TEST_TMPDIR/err3"; then
    pass "timeout banner names LOOM_GATE_TIMEOUT_SECONDS"
else
    fail "timeout banner names LOOM_GATE_TIMEOUT_SECONDS"
fi
if grep -q 'slow-job' "$TEST_TMPDIR/err3"; then
    pass "timeout banner names the label"
else
    fail "timeout banner names the label"
fi

# ---------------------------------------------------------------------------
# 4. Group kill: no grandchild of the wrapped command survives the cap.
# ---------------------------------------------------------------------------
: > "$PIDFILE"
"$WITH_TIMEOUT" 1 "group-kill" "$FIXTURE" "$PIDFILE" 2>/dev/null
rc=$?
check_eq "group-kill run exits 124" "124" "$rc"
left=$(survivors "$PIDFILE")
if [ -z "$left" ]; then
    pass "group kill reaped every recorded grandchild"
else
    fail "group kill left survivors:$left"
    for pid in $left; do kill -KILL "$pid" 2>/dev/null; done
fi

# ---------------------------------------------------------------------------
# 5. A SIGTERM-ignoring child is killed anyway once the grace elapses.
# ---------------------------------------------------------------------------
start=$(date +%s)
LOOM_GATE_TIMEOUT_KILL_GRACE_SECONDS=1 \
    "$WITH_TIMEOUT" 1 "term-ignorer" bash -c "trap '' TERM; sleep 30" 2>/dev/null
rc=$?
elapsed=$(( $(date +%s) - start ))
check_eq "SIGTERM-ignoring child still reported as timeout" "124" "$rc"
if [ "$elapsed" -lt 10 ]; then
    pass "SIGKILL backstop fired within the grace (${elapsed}s)"
else
    fail "SIGKILL backstop took ${elapsed}s"
fi

# ---------------------------------------------------------------------------
# 6. Cap 0 disables the watchdog entirely.
# ---------------------------------------------------------------------------
start=$(date +%s)
"$WITH_TIMEOUT" 0 "disabled" sleep 2
rc=$?
elapsed=$(( $(date +%s) - start ))
check_eq "cap 0 runs the command to completion" "0" "$rc"
if [ "$elapsed" -ge 2 ]; then
    pass "cap 0 did not cut the command short (${elapsed}s)"
else
    fail "cap 0 cut the command short (${elapsed}s)"
fi

# ---------------------------------------------------------------------------
# 7. No watchdog leak: a fast command leaves no `sleep <cap>` behind.
# ---------------------------------------------------------------------------
"$WITH_TIMEOUT" 987 "leak-check" true
sleep 1
if ps -ax -o command= 2>/dev/null | grep -v grep | grep -q 'sleep 987'; then
    fail "watchdog sleep leaked after a fast command"
    pkill -f 'sleep 987' 2>/dev/null
else
    pass "watchdog reaped after a fast command"
fi

# ---------------------------------------------------------------------------
# 8. A child that exits 143 on its own is not misreported as a timeout.
#    The sentinel file, not the exit status, distinguishes the two.
# ---------------------------------------------------------------------------
"$WITH_TIMEOUT" 30 "self-143" bash -c 'exit 143'
check_eq "self-inflicted 143 is not reported as 124" "143" "$?"

# ---------------------------------------------------------------------------
# 9. Nested wrapper: the inner one must pass through, so the outer cap governs
#    and the outer group-kill reaches the real tree. Without the re-entrancy
#    marker the inner wrapper opens a third process group the outer kill cannot
#    reach, and the grandchildren survive. Test 4's fixture has no `set -m` of
#    its own, so this is the only case that catches that.
# ---------------------------------------------------------------------------
: > "$PIDFILE"
start=$(date +%s)
"$WITH_TIMEOUT" 1 "outer" "$WITH_TIMEOUT" 60 "inner" "$FIXTURE" "$PIDFILE" 2>"$TEST_TMPDIR/err9"
rc=$?
elapsed=$(( $(date +%s) - start ))
check_eq "nested: outer cap fires with 124" "124" "$rc"
if [ "$elapsed" -lt 30 ]; then
    pass "nested: outer deadline governed (${elapsed}s, not the inner 60s)"
else
    fail "nested: outer deadline did not govern (${elapsed}s)"
fi
if grep -q 'already bounded by an outer wall-time cap' "$TEST_TMPDIR/err9"; then
    pass "nested: inner wrapper printed the pass-through note"
else
    fail "nested: inner wrapper did not print the pass-through note"
fi
left=$(survivors "$PIDFILE")
if [ -z "$left" ]; then
    pass "nested: outer group kill reaped the whole tree"
else
    fail "nested: outer group kill left survivors:$left"
    for pid in $left; do kill -KILL "$pid" 2>/dev/null; done
fi

# ---------------------------------------------------------------------------
# 10. The marker does not leak upward into the calling shell — otherwise a
#     second gate in the same shell would silently run uncapped.
# ---------------------------------------------------------------------------
"$WITH_TIMEOUT" 30 "marker-scope" true
if [ -z "${LOOM_GATE_TIMEOUT_ACTIVE:-}" ]; then
    pass "marker does not leak into the calling shell"
else
    fail "marker leaked into the calling shell (LOOM_GATE_TIMEOUT_ACTIVE=$LOOM_GATE_TIMEOUT_ACTIVE)"
fi

# ---------------------------------------------------------------------------
# Cap resolution - gate-timeout-seconds.sh
# ---------------------------------------------------------------------------
check_eq "resolve: no env -> default 1800" "1800" "$(env -u LOOM_GATE_TIMEOUT_SECONDS -u LOOM_RUN_TURN_TIMEOUT_SECONDS "$RESOLVE")"
check_eq "resolve: explicit override wins" "42" "$(LOOM_GATE_TIMEOUT_SECONDS=42 LOOM_RUN_TURN_TIMEOUT_SECONDS=2580 "$RESOLVE")"
check_eq "resolve: explicit 0 (disabled) wins" "0" "$(LOOM_GATE_TIMEOUT_SECONDS=0 LOOM_RUN_TURN_TIMEOUT_SECONDS=2580 "$RESOLVE")"
check_eq "resolve: budget 2580 -> min(1800, 2280)" "1800" "$(env -u LOOM_GATE_TIMEOUT_SECONDS LOOM_RUN_TURN_TIMEOUT_SECONDS=2580 "$RESOLVE")"
check_eq "resolve: budget 900 -> 600" "600" "$(env -u LOOM_GATE_TIMEOUT_SECONDS LOOM_RUN_TURN_TIMEOUT_SECONDS=900 "$RESOLVE")"
check_eq "resolve: budget 400 clamped to the 300 floor" "300" "$(env -u LOOM_GATE_TIMEOUT_SECONDS LOOM_RUN_TURN_TIMEOUT_SECONDS=400 "$RESOLVE")"

# Garbage falls through to the next rule, warns on stderr, and keeps stdout clean.
resolved=$(LOOM_GATE_TIMEOUT_SECONDS=abc LOOM_RUN_TURN_TIMEOUT_SECONDS=900 "$RESOLVE" 2>"$TEST_TMPDIR/errR")
check_eq "resolve: garbage override falls through" "600" "$resolved"
if grep -q 'LOOM_GATE_TIMEOUT_SECONDS' "$TEST_TMPDIR/errR"; then
    pass "resolve: garbage override warns on stderr"
else
    fail "resolve: garbage override warns on stderr"
fi

resolved=$(env -u LOOM_GATE_TIMEOUT_SECONDS LOOM_RUN_TURN_TIMEOUT_SECONDS=-5 "$RESOLVE" 2>"$TEST_TMPDIR/errR2")
check_eq "resolve: negative budget falls through to the default" "1800" "$resolved"
if grep -q 'LOOM_RUN_TURN_TIMEOUT_SECONDS' "$TEST_TMPDIR/errR2"; then
    pass "resolve: negative budget warns on stderr"
else
    fail "resolve: negative budget warns on stderr"
fi

resolved=$(env -u LOOM_GATE_TIMEOUT_SECONDS LOOM_RUN_TURN_TIMEOUT_SECONDS=1.5 "$RESOLVE" 2>/dev/null)
check_eq "resolve: fractional budget falls through to the default" "1800" "$resolved"

# ---------------------------------------------------------------------------
echo ""
echo "======================================"
echo "Passed: $PASS_COUNT"
echo "Failed: $FAIL_COUNT"
echo "======================================"

if [ "$FAIL_COUNT" -gt 0 ]; then
    exit 1
fi
exit 0
