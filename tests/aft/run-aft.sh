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
# fleet-db must include the driver-runs domain (Engine B / epic-runner); prefer a
# sibling fleet-db-main checkout (e.g. a `git worktree` of origin/main) when the
# primary sibling checkout is older
if [[ -z "${FLEET_DB_REPO:-}" && -d "$REPO_ROOT/../fleet-db-main" ]]; then
    FLEET_DB_REPO="$REPO_ROOT/../fleet-db-main"
fi
: "${FLEET_DB_REPO:=$REPO_ROOT/../fleet-db}"
: "${AFT_DIR:=$REPO_ROOT/../testing-app}"
# flue runtime for building the builtin epic-runner workflow bundle (sibling checkout
# at internal/workflows/FLUE_COMMIT, built with: pnpm install && pnpm --filter
# @flue/runtime --filter @flue/cli build). Empty when absent: agent-flow tests fail
# with a clear workflow error, everything else runs.
: "${FLUE_REPO:=$REPO_ROOT/../flue}"
[[ -d "$FLUE_REPO/packages/runtime" ]] || FLUE_REPO=""
BASE_URL="http://127.0.0.1:${E2E_FRONTEND_PORT}"   # browser entry (vite preview, proxies /api)
API_URL="http://127.0.0.1:${E2E_PORT}"             # loom serve API
REPORT_DIR="$SCRIPT_DIR/reports"
mkdir -p "$REPORT_DIR"

if [[ ! -f "$AFT_DIR/dist/cli.js" ]]; then
    echo "[aft] building aft in $AFT_DIR..."
    (cd "$AFT_DIR" && npm install --silent && npm run build --silent)
fi

# The e2e script builds tmp/fleet-db only when MISSING — switching FLEET_DB_REPO (or
# advancing its checkout) would silently reuse a binary with different behavior
# (driver-runs domain, title upsert). Stamp the source repo+SHA and rebuild on change.
FLEET_DB_BIN="$REPO_ROOT/tmp/fleet-db"
FLEET_DB_STAMP="$REPO_ROOT/tmp/fleet-db.source"
FLEET_DB_SRC="$FLEET_DB_REPO@$(git -C "$FLEET_DB_REPO" rev-parse HEAD 2>/dev/null || echo unknown)"
if [[ -x "$FLEET_DB_BIN" && "$(cat "$FLEET_DB_STAMP" 2>/dev/null || true)" != "$FLEET_DB_SRC" ]]; then
    echo "[aft] fleet-db source changed ($FLEET_DB_SRC) — rebuilding binary..."
    (cd "$FLEET_DB_REPO" && CGO_ENABLED=0 go build -o "$FLEET_DB_BIN" ./cmd/fleet-db)
fi
mkdir -p "$REPO_ROOT/tmp" && printf '%s\n' "$FLEET_DB_SRC" > "$FLEET_DB_STAMP"

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
# Stub AI backends — scoped to the SERVER process only, never this script's env:
#  - e2e/stubs first on serve's PATH makes `codex`/`claude` resolve to the harmless
#    stubs, so agent runs (and the terminal view's auto-spawned lead) never invoke a
#    real LLM. The driver env allowlist strips LOOM_CODEX_BIN, so PATH is the lever.
#  - OPENAI_API_KEY satisfies the codex HealthCheck in the workflow-run preflight;
#    the stub never reads it.
# aft itself must NOT see this PATH: the recovery agent's `claude` is the real one.
# The builtin-workflow bundle build also needs the flue CLI itself; point serve at
# the built cli entry (node script) so nothing has to be on PATH.
FLUE_CMD_JSON=""
if [[ -n "$FLUE_REPO" && -f "$FLUE_REPO/packages/cli/bin/flue.mjs" ]]; then
    FLUE_CMD_JSON="[\"node\",\"$FLUE_REPO/packages/cli/bin/flue.mjs\"]"
fi
E2E_PORT="$E2E_PORT" E2E_FRONTEND_PORT="$E2E_FRONTEND_PORT" FLEET_DB_REPO="$FLEET_DB_REPO" \
    PATH="$REPO_ROOT/e2e/stubs:$PATH" OPENAI_API_KEY="stub-e2e" FLUE_REPO="$FLUE_REPO" \
    LOOM_REAL_FLUE_CMD_JSON="$FLUE_CMD_JSON" \
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

# Regenerate the coverage census from the frontend source so it always matches
# this checkout; aft joins run traces against it and reports untouched surface.
CENSUS="$AFT_WORK_DIR/census.json"
python3 "$SCRIPT_DIR/scripts/gen-census.py" --frontend "$REPO_ROOT/internal/webui/frontend/src" --out "$CENSUS" \
    || CENSUS=""   # census is reporting-only; never fail the run over it

# Loom's six-column board is dense — the agent-browser default viewport (1280x577)
# cuts it off; 1920x1080 shows the full board in screenshots and recordings.
# macOS: a sleeping display freezes Chrome's compositor — rendering frames stop, CSS
# @starting-style transitions never complete (elements stuck at opacity:0 read as
# invisible), Playwright actionability waits time out, and screenshots fail. Verified:
# 0 rAF frames/3s with the display asleep vs 182 awake. Keep the display awake for
# the duration of the run; -u wakes it at start. No-op on Linux/CI.
CAFFEINATE=""
command -v caffeinate >/dev/null 2>&1 && CAFFEINATE="caffeinate -dimsu"

# 15s step timeout (aft default is 8s): the Loom SPA leans on SSE + polling stores,
# and under CI/host load its reactions legitimately stretch past 8s — measured as
# whole-suite flake storms on a busy machine.
$CAFFEINATE node "$AFT_DIR/dist/cli.js" run "${AFT_SUITES:-$SCRIPT_DIR/suites}" --report-dir "$REPORT_DIR" \
    --viewport "${AFT_VIEWPORT:-1920x1080}" --timeout "${AFT_TIMEOUT:-15000}" \
    ${CENSUS:+--census "$CENSUS"} \
    ${AFT_MAX_BROWSERS:+--max-browsers "$AFT_MAX_BROWSERS"} "$@"
