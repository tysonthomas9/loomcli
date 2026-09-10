#!/usr/bin/env bash
# with-timeout.sh - Run a command under a wall-clock cap, killing its whole
# process group when the cap fires.
#
# Usage: ./scripts/with-timeout.sh <seconds> <label> <command> [args...]
#
# Why this exists instead of `timeout(1)`: coreutils `timeout`/`gtimeout` is not
# present on macOS, which is exactly where the unbounded gate hurts (fleet
# agents and the pre-push hook). This is portable bash 3.2 — no `wait -n`, no
# associative arrays, no `setsid`.
#
# Why the process GROUP and not the pid: `go test -p 1` spawns a compile chain
# and one test binary per package. Killing `go test` alone leaves the current
# package's binary running — the orphan shape this script exists to prevent.
# `set -m` makes the child a process-group leader so `kill -TERM -$child`
# reaches the entire tree; SIGKILL follows after a grace period for a `-race`
# binary wedged in the runtime.
#
# Re-entrancy: the wrapper exports LOOM_GATE_TIMEOUT_ACTIVE and becomes a
# transparent `exec` if it is already set. Nesting two armed wrappers would put
# the real work in a THIRD process group that the outer group-kill cannot reach,
# which would reproduce the very orphan being fixed. One gate invocation gets
# exactly one deadline, one process group and one banner.
#
# LOOM_GATE_TIMEOUT_ACTIVE is internal — do not set it by hand. To disable the
# cap use LOOM_GATE_TIMEOUT_SECONDS=0.
#
# Exit status: the command's own status, or 124 (the coreutils `timeout`
# convention) when the cap fired.
set -uo pipefail

if [ "$#" -lt 3 ]; then
    echo "usage: $0 <seconds> <label> <command> [args...]" >&2
    exit 2
fi

seconds="$1"
label="$2"
shift 2

# Re-entrancy check first: before set -m, before any watchdog, before anything
# that could change this process's group.
if [ -n "${LOOM_GATE_TIMEOUT_ACTIVE:-}" ]; then
    echo "[gate] $label: already bounded by an outer wall-time cap; not arming a second one" >&2
    exec "$@"
fi

case "$seconds" in
    '' | *[!0-9]* )
        if [ -n "$seconds" ]; then
            echo "[gate] warning: $label: ignoring non-integer cap '$seconds'; running uncapped" >&2
        fi
        exec "$@"
        ;;
esac
if [ "$seconds" -eq 0 ]; then
    # Disabled: byte-for-byte today's behaviour, no process-group change and no
    # marker, so a nested wrapper is free to arm its own cap.
    exec "$@"
fi

grace="${LOOM_GATE_TIMEOUT_KILL_GRACE_SECONDS:-10}"
case "$grace" in
    '' | *[!0-9]* ) grace=10 ;;
esac

sentinel="$(mktemp "${TMPDIR:-/tmp}/loom.gate.timeout.XXXXXX")"
cleanup() {
    rm -f "$sentinel"
}
trap cleanup EXIT

# Never signal group 0 (every process in our own session), group 1, or our own
# group — the same self-protection the supervisor's orphan sweep applies.
self_pgid="$(ps -o pgid= -p $$ 2>/dev/null | tr -d ' ')"

kill_group() {
    # kill_group <signal> <pgid>
    _sig="$1"
    _pgid="$2"
    [ -n "$_pgid" ] || return 0
    [ "$_pgid" -gt 1 ] 2>/dev/null || return 0
    [ "$_pgid" != "$self_pgid" ] || return 0
    kill -"$_sig" -"$_pgid" 2>/dev/null || true
}

child=0
watchdog=0

on_signal() {
    # on_signal <name> <number> - forward the interrupt to the real tree.
    # Under `set -m` the child sits in a background process group and does NOT
    # receive the terminal's Ctrl-C, so without this a developer interrupting a
    # pre-push gate would leave the whole `go test` tree running.
    _name="$1"
    _num="$2"
    kill_group "$_name" "$child"
    kill_group TERM "$watchdog"
    wait "$child" 2>/dev/null
    exit $((128 + _num))
}
trap 'on_signal INT 2' INT
trap 'on_signal TERM 15' TERM
trap 'on_signal HUP 1' HUP

# Job control: each background job becomes its own process-group leader.
set -m

export LOOM_GATE_TIMEOUT_ACTIVE=1

# </dev/null is deliberate: a background process group that reads the terminal
# gets SIGTTIN and stops forever. Nothing in the gate reads stdin, and feeding
# EOF turns any future such read from a hang into an error.
"$@" </dev/null &
child=$!

(
    sleep "$seconds"
    echo timeout >"$sentinel"
    kill -TERM -"$child" 2>/dev/null || true
    sleep "$grace"
    kill -KILL -"$child" 2>/dev/null || true
) &
watchdog=$!

wait "$child"
rc=$?

# The command is done, so there is nothing left to forward a signal to. Clearing
# the traps before reaping the watchdog also sidesteps a bash 3.2 job-control
# bug that prints `run_pending_traps: bad value in trap_list[15]` when a job
# killed by SIGTERM is reaped by a shell that still has a TERM trap installed.
trap - INT TERM HUP

# Reap the watchdog on every path. A leaked `sleep 1800` is itself an orphan,
# which would be an embarrassing way to fix an orphan bug.
kill_group TERM "$watchdog"
wait "$watchdog" 2>/dev/null || true

# The sentinel, not the exit status, is what distinguishes "we killed it" from
# "it exited 143 on its own".
if [ -s "$sentinel" ]; then
    echo "[gate] TIMEOUT: $label exceeded its wall-time cap of ${seconds}s and was killed (process group -${child})." >&2
    echo "[gate] This is a wall-clock cap, not a test failure. Raise or disable it with" >&2
    echo "[gate]   LOOM_GATE_TIMEOUT_SECONDS=<seconds|0>   (0 = no cap)" >&2
    echo "[gate] Derived from LOOM_RUN_TURN_TIMEOUT_SECONDS=${LOOM_RUN_TURN_TIMEOUT_SECONDS:-unset}." >&2
    exit 124
fi

exit $rc
