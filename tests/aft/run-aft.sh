#!/usr/bin/env bash
# run-aft.sh — start the isolated e2e stack (loom serve + fleet-db + vite preview),
# run the aft YAML suites against it, tear down.
#
# Usage: tests/aft/run-aft.sh [aft options...]        # e.g. --no-agent, --strict, --heal, --record
# Env:   E2E_PORT (API, default 8090)   E2E_FRONTEND_PORT (browser target, default 3100)
#        FLEET_DB_REPO (default: sibling ../fleet-db)  AFT_DIR (default: sibling ../testing-app)
#        AFT_SUITES (default: tests/aft/suites — a YAML file or directory)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

: "${E2E_PORT:=8090}"
: "${E2E_FRONTEND_PORT:=3100}"
: "${FLEET_DB_REPO:=$REPO_ROOT/../fleet-db}"
: "${AFT_DIR:=$REPO_ROOT/../testing-app}"
BASE_URL="http://127.0.0.1:${E2E_FRONTEND_PORT}"   # browser entry (vite preview, proxies /api)
API_URL="http://127.0.0.1:${E2E_PORT}"             # loom serve API
REPORT_DIR="$SCRIPT_DIR/reports"
mkdir -p "$REPORT_DIR"

if [[ ! -f "$AFT_DIR/dist/cli.js" ]]; then
    echo "[aft] building aft in $AFT_DIR..."
    (cd "$AFT_DIR" && npm install --silent && npm run build --silent)
fi

SERVER_PID=""
cleanup() {
    if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
        echo "[aft] stopping e2e stack (pid $SERVER_PID)..."
        kill "$SERVER_PID" 2>/dev/null || true   # fires the script's own trap (kills loom + preview)
        wait "$SERVER_PID" 2>/dev/null || true
    fi
    # belt-and-braces: nothing from this run's isolated binaries may outlive it
    pkill -f "$REPO_ROOT/tmp/loom-e2e" 2>/dev/null || true
    pkill -f "$REPO_ROOT/tmp/fleet-db" 2>/dev/null || true
    pkill -f "vite preview --port ${E2E_FRONTEND_PORT}" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

echo "[aft] starting e2e stack (api :${E2E_PORT}, frontend :${E2E_FRONTEND_PORT}; log: $REPORT_DIR/server.log)..."
E2E_PORT="$E2E_PORT" E2E_FRONTEND_PORT="$E2E_FRONTEND_PORT" FLEET_DB_REPO="$FLEET_DB_REPO" \
    bash "$REPO_ROOT/scripts/start-e2e-server.sh" >"$REPORT_DIR/server.log" 2>&1 &
SERVER_PID=$!

READY=""
for _ in $(seq 1 180); do
    if curl -sf "$BASE_URL/api/config" >/dev/null 2>&1; then READY=1; break; fi
    if ! kill -0 "$SERVER_PID" 2>/dev/null; then
        echo "[aft] server exited early — tail of server.log:"
        tail -30 "$REPORT_DIR/server.log"
        exit 1
    fi
    sleep 1
done
if [[ -z "$READY" ]]; then
    echo "[aft] stack not ready after 180s — tail of server.log:"
    tail -30 "$REPORT_DIR/server.log"
    exit 1
fi
# settle: the preview must serve the app shell consistently before browsers connect
for _ in $(seq 1 30); do
    if curl -sf "$BASE_URL/" >/dev/null 2>&1; then break; fi
    sleep 1
done
sleep 1
echo "[aft] stack ready: browser $BASE_URL, api $API_URL"

export AFT_BASE_URL="$BASE_URL"
export AFT_API_URL="$API_URL"
export AFT_WS="E2E-WS"   # primary workspace id seeded by start-e2e-server.sh
export LOOM_BASE_URL="$API_URL"
export RUN_ID="${RUN_ID:-$(date +%s)}"
export AFT_TESTS_DIR="$SCRIPT_DIR"
export AFT_WORK_DIR="$REPORT_DIR/work/$RUN_ID"   # scratch space for run-step state (issue ids etc.)
mkdir -p "$AFT_WORK_DIR"

node "$AFT_DIR/dist/cli.js" run "${AFT_SUITES:-$SCRIPT_DIR/suites}" --report-dir "$REPORT_DIR" "$@"
