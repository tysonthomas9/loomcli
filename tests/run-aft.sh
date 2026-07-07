#!/usr/bin/env bash
# run-aft.sh — start the isolated e2e server, run the aft YAML suites against it, tear down.
#
# Usage: tests/aft/run-aft.sh [aft options...]        # e.g. --no-agent, --strict, --heal, --record
# Env:   E2E_PORT (default 8090)   AFT_DIR (default: sibling ../testing-app checkout)
#        AFT_SUITES (default: tests/aft/suites — a YAML file or directory)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

: "${E2E_PORT:=8090}"
: "${AFT_DIR:=$REPO_ROOT/../testing-app}"
BASE_URL="http://localhost:${E2E_PORT}"
REPORT_DIR="$SCRIPT_DIR/reports"
mkdir -p "$REPORT_DIR"

if [[ ! -f "$AFT_DIR/dist/cli.js" ]]; then
    echo "[aft] building aft in $AFT_DIR..."
    (cd "$AFT_DIR" && npm install --silent && npm run build --silent)
fi

SERVER_PID=""
# Snapshot bd daemons that were already running before this harness — cleanup kills
# only daemons the run created (the e2e stack spawns one for the e2e workspace AND,
# when run from a git worktree, one for the parent checkout's .beads workspace).
# NO bd client commands in cleanup: every bd invocation consults the daemon registry
# and can resurrect the daemons it just stopped. Plain kill is the reliable primitive;
# bd auto-respawns daemons on demand, so this loses nothing.
PRE_EXISTING_BD="$(pgrep -f 'bd daemon --start' 2>/dev/null | tr '\n' ' ' || true)"
cleanup() {
    if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
        echo "[aft] stopping e2e server (pid $SERVER_PID)..."
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
    fi
    for p in $(pgrep -f 'bd daemon --start' 2>/dev/null); do
        [[ " $PRE_EXISTING_BD " == *" $p "* ]] && continue
        kill "$p" 2>/dev/null || true
    done
}
trap cleanup EXIT INT TERM

echo "[aft] starting e2e server on :${E2E_PORT} (log: $REPORT_DIR/server.log)..."
E2E_PORT="$E2E_PORT" bash "$REPO_ROOT/scripts/start-e2e-server.sh" >"$REPORT_DIR/server.log" 2>&1 &
SERVER_PID=$!

READY=""
for _ in $(seq 1 120); do
    if curl -sf "$BASE_URL/health" >/dev/null 2>&1; then READY=1; break; fi
    if ! kill -0 "$SERVER_PID" 2>/dev/null; then
        echo "[aft] server exited early — tail of server.log:"
        tail -20 "$REPORT_DIR/server.log"
        exit 1
    fi
    sleep 1
done
if [[ -z "$READY" ]]; then
    echo "[aft] server not healthy after 120s — tail of server.log:"
    tail -20 "$REPORT_DIR/server.log"
    exit 1
fi
echo "[aft] server healthy at $BASE_URL"

export AFT_BASE_URL="$BASE_URL"
export LOOM_BASE_URL="$BASE_URL"
export RUN_ID="${RUN_ID:-$(date +%s)}"
export AFT_TESTS_DIR="$SCRIPT_DIR"
export AFT_WORK_DIR="$REPORT_DIR/work/$RUN_ID"   # scratch space for run-step state (issue ids etc.)
mkdir -p "$AFT_WORK_DIR"

node "$AFT_DIR/dist/cli.js" run "${AFT_SUITES:-$SCRIPT_DIR/suites}" --report-dir "$REPORT_DIR" "$@"
