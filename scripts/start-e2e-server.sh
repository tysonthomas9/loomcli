#!/usr/bin/env bash
# start-e2e-server.sh — Build loom, start daemon, exec loom serve for Playwright e2e tests.
# Designed to be invoked by Playwright's webServer config.
# Playwright kills this process when tests finish; trap cleans up the daemon.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
FRONTEND_DIR="$REPO_ROOT/internal/webui/frontend"
LOOM_BIN="$REPO_ROOT/tmp/loom-e2e"

DAEMON_STARTED=""

cleanup() {
    if [[ -n "$DAEMON_STARTED" ]]; then
        echo "[e2e] Stopping bd daemon..."
        bd daemon stop 2>/dev/null || true
    fi
}
trap cleanup EXIT INT TERM

# --- 1. Build loom binary (skip if fresh) ---
MAIN_PKG="$REPO_ROOT/cmd/loom"
if [[ ! -x "$LOOM_BIN" ]] || [[ "$MAIN_PKG/main.go" -nt "$LOOM_BIN" ]]; then
    echo "[e2e] Building loom binary..."
    mkdir -p "$(dirname "$LOOM_BIN")"
    (cd "$REPO_ROOT" && go build -o "$LOOM_BIN" ./cmd/loom)
else
    echo "[e2e] Using cached loom binary"
fi

# --- 2. Build frontend dist if missing ---
if [[ ! -d "$FRONTEND_DIR/dist" ]]; then
    echo "[e2e] Building frontend..."
    (cd "$FRONTEND_DIR" && npm ci --prefer-offline && npm run build)
fi

# --- 3. Ensure .beads workspace exists (CI needs this) ---
if [[ ! -d "$REPO_ROOT/.beads" ]]; then
    echo "[e2e] Initializing .beads workspace..."
    (cd "$REPO_ROOT" && bd init --local 2>/dev/null || true)
fi

# --- 4. Start bd daemon if not running ---
if ! bd daemon status >/dev/null 2>&1; then
    echo "[e2e] Starting bd daemon..."
    (cd "$REPO_ROOT" && bd daemon start)
    DAEMON_STARTED=1
fi

# --- 5. Exec loom serve from repo root (daemon socket auto-detect needs cwd) ---
# E2E_PORT controls the WebUI port (where tests send requests).
# The Loom API port is offset +1 from the WebUI port.
WEBUI_PORT="${E2E_PORT:-8080}"
API_PORT=$((WEBUI_PORT + 1))
cd "$REPO_ROOT"
echo "[e2e] Starting loom serve --no-auth (webui :${WEBUI_PORT}, api :${API_PORT})..."
exec "$LOOM_BIN" serve --no-auth --webui-port "${WEBUI_PORT}" --port "${API_PORT}"
