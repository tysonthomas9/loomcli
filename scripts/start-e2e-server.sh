#!/usr/bin/env bash
# start-e2e-server.sh — Build loom, start daemon, exec loom serve for Playwright e2e tests.
# Designed to be invoked by Playwright's webServer config.
# Playwright kills this process when tests finish; trap cleans up the daemon.
#
# Uses an isolated beads workspace (tmp/e2e-workspace/) so tests never
# touch the repository's real .beads/ database.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
FRONTEND_DIR="$REPO_ROOT/internal/webui/frontend"
LOOM_BIN="$REPO_ROOT/tmp/loom-e2e"
E2E_WORKSPACE="$REPO_ROOT/tmp/e2e-workspace"

DAEMON_STARTED=""

cleanup() {
    if [[ -n "$DAEMON_STARTED" ]]; then
        echo "[e2e] Stopping bd daemon..."
        (cd "$E2E_WORKSPACE" && bd daemon stop 2>/dev/null) || true
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

# --- 3. Create isolated workspace for E2E tests ---
# Fresh workspace each run so tests start with a clean database.
rm -rf "$E2E_WORKSPACE"
mkdir -p "$E2E_WORKSPACE"
(cd "$E2E_WORKSPACE" && git init -q && bd init --prefix loomcli --skip-hooks --skip-merge-driver -q)
echo "[e2e] Created isolated workspace: $E2E_WORKSPACE"

# --- 4. Start bd daemon in isolated workspace ---
(cd "$E2E_WORKSPACE" && bd daemon start)
DAEMON_STARTED=1

# --- 5. Wait for daemon socket and exec loom serve ---
E2E_SOCKET="$E2E_WORKSPACE/.beads/bd.sock"
for i in $(seq 1 10); do
    [[ -S "$E2E_SOCKET" ]] && break
    sleep 0.5
done
if [[ ! -S "$E2E_SOCKET" ]]; then
    echo "[e2e] ERROR: Daemon socket not found at $E2E_SOCKET"
    exit 1
fi

WEBUI_PORT="${E2E_PORT:-8080}"
API_PORT=$((WEBUI_PORT + 1))
echo "[e2e] Starting loom serve --no-auth (webui :${WEBUI_PORT}, api :${API_PORT})..."
# Run from E2E workspace so the Loom API server also discovers the isolated daemon.
cd "$E2E_WORKSPACE"
exec "$LOOM_BIN" serve --no-auth \
    --webui-socket "$E2E_SOCKET" \
    --webui-port "${WEBUI_PORT}" \
    --port "${API_PORT}"
