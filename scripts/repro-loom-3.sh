#!/usr/bin/env bash
# Deterministic repro for LOOM-3:
#   `loom workspace ops diagnose --json` falsely reports daemon_not_running
#   when the supervisor daemon is launched from a cwd outside the desktop
#   runtime's data dir.
#
# On a pre-fix tree the script exits 1 (bug reproduced).
# After the fix it exits 0 (the new Node-registry detection sees the
# daemon regardless of cwd).
#
# Run from the loomcli repo root.

set -euo pipefail

LOOM="${LOOM_BIN:-loom}"
WORKSPACE="LOOM3_REPRO"
TMPROOT="$(mktemp -d)"
DAEMON_CWD="$TMPROOT/repro-cwd"
mkdir -p "$DAEMON_CWD"
DAEMON_PID=""

cleanup() {
    if [[ -n "$DAEMON_PID" ]] && kill -0 "$DAEMON_PID" 2>/dev/null; then
        kill "$DAEMON_PID" 2>/dev/null || true
        wait "$DAEMON_PID" 2>/dev/null || true
    fi
    rm -rf "$TMPROOT"
}
trap cleanup EXIT

# Make sure the workspace exists (idempotent — ignore if already added).
"$LOOM" workspace add "$WORKSPACE" >/dev/null 2>&1 || true

# Start a daemon from a non-default cwd. The bug is that diagnose can't
# see this daemon because it only inspects .loom/ files relative to its
# own cwd / the desktop runtime data dir.
(
    cd "$DAEMON_CWD"
    LOOM_WORKSPACE="$WORKSPACE" exec "$LOOM" daemon
) >"$TMPROOT/daemon.log" 2>&1 &
DAEMON_PID=$!

# Wait up to 15s for the daemon to register itself in the Node registry.
DEADLINE=$(( $(date +%s) + 15 ))
while [[ "$(date +%s)" -lt "$DEADLINE" ]]; do
    if ! kill -0 "$DAEMON_PID" 2>/dev/null; then
        echo "ERROR: daemon exited prematurely; log follows:" >&2
        cat "$TMPROOT/daemon.log" >&2
        exit 2
    fi
    if LOOM_WORKSPACE="$WORKSPACE" "$LOOM" workspace ops status --json 2>/dev/null \
        | grep -q '"registered"' ; then
        # Registered field present — supervisor has published itself.
        # Even on the pre-fix tree we keep going because the bug
        # symptom is in diagnose, not status.
        break
    fi
    sleep 0.5
done

# Run diagnose from a *different* cwd. This is the exact failure mode in
# the bug report.
DIAGNOSE_CWD="$TMPROOT/diagnose-cwd"
mkdir -p "$DIAGNOSE_CWD"
DIAGNOSE_OUT="$TMPROOT/diagnose.json"
(
    cd "$DIAGNOSE_CWD"
    LOOM_WORKSPACE="$WORKSPACE" "$LOOM" workspace ops diagnose --json
) >"$DIAGNOSE_OUT" || true

if grep -q '"daemon_not_running"' "$DIAGNOSE_OUT"; then
    echo "REPRO: diagnose reports daemon_not_running for live daemon (PID $DAEMON_PID, cwd $DAEMON_CWD)" >&2
    echo "--- diagnose output ---" >&2
    cat "$DIAGNOSE_OUT" >&2
    exit 1
fi

echo "OK: diagnose recognizes the daemon (PID $DAEMON_PID) launched from $DAEMON_CWD"
exit 0
