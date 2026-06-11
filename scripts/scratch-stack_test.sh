#!/usr/bin/env bash
# scratch-stack_test.sh - Tests for scratch-stack.sh
#
# Validates PID-recording start/stop/status: pidfile written on start, stop
# kills only recorded PIDs, missing pidfile is a no-op, and stale pidfiles
# with dead or recycled PIDs don't error or kill unrelated processes.
#
# Usage: ./scripts/scratch-stack_test.sh
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUT="$SCRIPT_DIR/scratch-stack.sh"

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

TEST_TMPDIR=$(mktemp -d)
export SCRATCH_STACK_DIR="$TEST_TMPDIR"
cleanup() {
    rm -rf "$TEST_TMPDIR"
    [ -n "${UNRELATED_PID:-}" ] && kill "$UNRELATED_PID" 2>/dev/null
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# Test 1: start records the PID in the pidfile and the process is alive
# ---------------------------------------------------------------------------
"$SUT" start t1 -- sleep 300 >/dev/null
PIDFILE="$TEST_TMPDIR/scratch-stack-t1.pids"
if [ -f "$PIDFILE" ]; then
    pass "start writes pidfile"
else
    fail "start writes pidfile"
fi
PID=$(awk '{print $1}' "$PIDFILE" | head -1)
if kill -0 "$PID" 2>/dev/null; then
    pass "started process is alive"
else
    fail "started process is alive"
fi

# ---------------------------------------------------------------------------
# Test 2: status reports the PID as ALIVE
# ---------------------------------------------------------------------------
if "$SUT" status t1 | grep -q "pid $PID ALIVE"; then
    pass "status reports ALIVE"
else
    fail "status reports ALIVE"
fi

# ---------------------------------------------------------------------------
# Test 3: stop kills the recorded PID and removes the pidfile
# ---------------------------------------------------------------------------
"$SUT" stop t1 >/dev/null
sleep 0.3
if kill -0 "$PID" 2>/dev/null; then
    fail "stop kills recorded pid"
else
    pass "stop kills recorded pid"
fi
if [ ! -f "$PIDFILE" ]; then
    pass "stop removes pidfile"
else
    fail "stop removes pidfile"
fi

# ---------------------------------------------------------------------------
# Test 4: stop of an unknown name exits 0
# ---------------------------------------------------------------------------
if "$SUT" stop nosuchname >/dev/null; then
    pass "stop of unknown name exits 0"
else
    fail "stop of unknown name exits 0"
fi

# ---------------------------------------------------------------------------
# Test 5: multiple starts under one name accumulate, stop kills all
# ---------------------------------------------------------------------------
"$SUT" start t5 -- sleep 300 >/dev/null
"$SUT" start t5 -- sleep 300 >/dev/null
PIDS=$(awk '{print $1}' "$TEST_TMPDIR/scratch-stack-t5.pids")
if [ "$(echo "$PIDS" | wc -l | tr -d ' ')" = "2" ]; then
    pass "starts accumulate in pidfile"
else
    fail "starts accumulate in pidfile"
fi
"$SUT" stop t5 >/dev/null
sleep 0.3
ALIVE=0
for p in $PIDS; do
    kill -0 "$p" 2>/dev/null && ALIVE=1
done
if [ "$ALIVE" = "0" ]; then
    pass "stop kills all accumulated pids"
else
    fail "stop kills all accumulated pids"
fi

# ---------------------------------------------------------------------------
# Test 6: stale pidfile with a dead PID doesn't error
# ---------------------------------------------------------------------------
sleep 1 &
DEAD_PID=$!
wait "$DEAD_PID" 2>/dev/null
echo "$DEAD_PID sleep 1" >"$TEST_TMPDIR/scratch-stack-t6.pids"
if "$SUT" stop t6 >/dev/null 2>&1; then
    pass "stale pidfile with dead pid exits 0"
else
    fail "stale pidfile with dead pid exits 0"
fi

# ---------------------------------------------------------------------------
# Test 7: recycled PID (command mismatch) is skipped, unrelated process lives
# ---------------------------------------------------------------------------
sleep 300 &
UNRELATED_PID=$!
echo "$UNRELATED_PID some-other-binary --flag" >"$TEST_TMPDIR/scratch-stack-t7.pids"
OUT=$("$SUT" stop t7 2>&1)
if echo "$OUT" | grep -q "recycled"; then
    pass "recycled pid is reported"
else
    fail "recycled pid is reported (got: $OUT)"
fi
if kill -0 "$UNRELATED_PID" 2>/dev/null; then
    pass "unrelated recycled-pid process not killed"
else
    fail "unrelated recycled-pid process not killed"
fi
kill "$UNRELATED_PID" 2>/dev/null
UNRELATED_PID=""

# ---------------------------------------------------------------------------
# Test 8: invalid name is rejected
# ---------------------------------------------------------------------------
if "$SUT" start "../evil" -- sleep 1 >/dev/null 2>&1; then
    fail "invalid name rejected"
else
    pass "invalid name rejected"
fi

# ---------------------------------------------------------------------------
# Test 9: TERM-ignoring process gets KILL escalation after the grace period
# ---------------------------------------------------------------------------
SCRATCH_STACK_GRACE=1 "$SUT" start t9 -- bash -c 'trap "" TERM; sleep 300' >/dev/null
T9_PID=$(awk '{print $1}' "$TEST_TMPDIR/scratch-stack-t9.pids" | head -1)
OUT=$(SCRATCH_STACK_GRACE=1 "$SUT" stop t9 2>&1)
sleep 0.3
if echo "$OUT" | grep -q "sending KILL"; then
    pass "KILL escalation reported"
else
    fail "KILL escalation reported (got: $OUT)"
fi
if kill -0 "$T9_PID" 2>/dev/null; then
    fail "TERM-ignoring process killed"
    kill -9 "$T9_PID" 2>/dev/null
else
    pass "TERM-ignoring process killed"
fi

# ---------------------------------------------------------------------------
echo ""
echo "Results: $PASS_COUNT passed, $FAIL_COUNT failed"
[ "$FAIL_COUNT" -eq 0 ]
