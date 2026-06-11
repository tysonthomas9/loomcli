#!/usr/bin/env bash
# scratch-stack.sh — PID-recording start/stop/status helper for ad-hoc scratch stacks.
#
# Agents spinning up temporary serve/daemon/vite instances MUST tear down only
# what they started. This helper records every spawned PID in a per-name
# pidfile on start, and stop kills exactly those PIDs — never by process-name
# pattern (an unscoped `pkill -f "loom serve"` kills the shared production
# stack).
#
# Multiple `start` calls with the same <name> accumulate PIDs (serve + daemon
# + vite under one name). `stop` no-ops safely on a missing pidfile and skips
# recycled PIDs (live command's basename must match the recorded one).
#
# Limitation: only the direct child PID is recorded. A command that forks
# further children (e.g. `sh -c 'a & b'`) leaves grandchildren behind on
# stop — start each process with its own `start` call instead.
set -euo pipefail

PIDDIR="${SCRATCH_STACK_DIR:-/tmp/scratch-stack-$(id -u)}"
# TERM grace period before KILL escalation, in seconds (shared across all
# PIDs of a stop, so total stop latency stays bounded).
GRACE="${SCRATCH_STACK_GRACE:-10}"
case "$GRACE" in
    '' | *[!0-9]*)
        echo "invalid SCRATCH_STACK_GRACE: $GRACE (must be a number of seconds)" >&2
        exit 1
        ;;
esac

usage() {
    cat <<'EOF'
Usage:
  scratch-stack.sh start <name> -- <command...>   # spawn detached, record PID
  scratch-stack.sh stop <name>                    # kill recorded PIDs only
  scratch-stack.sh status <name>                  # list recorded PIDs + liveness

<name> must match [A-Za-z0-9._-]+. Pidfiles live in $SCRATCH_STACK_DIR
(default /tmp/scratch-stack-<uid>).
EOF
    exit 1
}

pidfile() {
    echo "$PIDDIR/scratch-stack-$1.pids"
}

valid_name() {
    case "$1" in
        '' | *[!A-Za-z0-9._-]*) return 1 ;;
        *) return 0 ;;
    esac
}

valid_pid() {
    case "$1" in
        '' | *[!0-9]*) return 1 ;;
        *) return 0 ;;
    esac
}

cmd_start() {
    local name="$1"
    shift
    [ "${1:-}" = "--" ] || usage
    shift
    [ $# -ge 1 ] || usage
    mkdir -p -m 700 "$PIDDIR"
    # Guard against a pre-created/symlinked dir owned by another user.
    if [ -L "$PIDDIR" ] || [ ! -O "$PIDDIR" ]; then
        echo "refusing to use $PIDDIR: not a directory owned by you" >&2
        exit 1
    fi
    chmod 700 "$PIDDIR"
    local pf
    pf="$(pidfile "$name")"
    nohup "$@" >/dev/null 2>&1 &
    local pid=$!
    # Record pid + command so stop can detect PID recycling.
    echo "$pid $*" >>"$pf"
    echo "started [$name] pid $pid: $*"
}

# alive_matches <pid> <recorded command line> — pid is alive AND the live
# command's basename matches the recorded command's basename (recycling
# guard; basenames so `./loom serve` matches ps's `/abs/path/loom serve`).
# Returns 1 if dead, 2 if alive but recycled by an unrelated process.
alive_matches() {
    local pid="$1" recorded="$2" current
    kill -0 "$pid" 2>/dev/null || return 1
    current="$(ps -p "$pid" -o command= 2>/dev/null || true)"
    [ "$(basename "${current%% *}")" = "$(basename "${recorded%% *}")" ] || return 2
}

cmd_stop() {
    local name="$1" pf
    pf="$(pidfile "$name")"
    if [ ! -f "$pf" ]; then
        echo "no pidfile for [$name] ($pf) — nothing to stop"
        return 0
    fi
    local pids_to_wait=()
    while read -r pid cmd; do
        valid_pid "$pid" || continue
        rc=0
        alive_matches "$pid" "$cmd" || rc=$?
        case $rc in
            1) echo "pid $pid already exited" ;;
            2) echo "pid $pid was recycled by an unrelated process — skipping" ;;
            0)
                echo "killing pid $pid ($cmd)"
                kill "$pid" 2>/dev/null || true
                pids_to_wait+=("$pid $cmd")
                ;;
        esac
    done <"$pf"
    # Wait up to $GRACE seconds for TERM'd processes, then KILL stragglers.
    local deadline=$((SECONDS + GRACE)) line pid cmd
    for line in "${pids_to_wait[@]+"${pids_to_wait[@]}"}"; do
        pid="${line%% *}"
        cmd="${line#* }"
        while kill -0 "$pid" 2>/dev/null && [ "$SECONDS" -lt "$deadline" ]; do
            sleep 0.2
        done
        rc=0
        alive_matches "$pid" "$cmd" || rc=$?
        if [ $rc -eq 0 ]; then
            echo "pid $pid did not exit after TERM — sending KILL"
            kill -9 "$pid" 2>/dev/null || true
        fi
    done
    rm -f "$pf"
    echo "stopped [$name]"
}

cmd_status() {
    local name="$1" pf
    pf="$(pidfile "$name")"
    if [ ! -f "$pf" ]; then
        echo "no pidfile for [$name] ($pf)"
        return 0
    fi
    while read -r pid cmd; do
        valid_pid "$pid" || continue
        rc=0
        alive_matches "$pid" "$cmd" || rc=$?
        case $rc in
            0) echo "pid $pid ALIVE: $cmd" ;;
            1) echo "pid $pid DEAD: $cmd" ;;
            2) echo "pid $pid RECYCLED (now an unrelated process): $cmd" ;;
        esac
    done <"$pf"
}

[ $# -ge 2 ] || usage
sub="$1"
name="$2"
shift 2
valid_name "$name" || {
    echo "invalid name: $name (must match [A-Za-z0-9._-]+)" >&2
    exit 1
}
case "$sub" in
    start) cmd_start "$name" "$@" ;;
    stop) cmd_stop "$name" ;;
    status) cmd_status "$name" ;;
    *) usage ;;
esac
