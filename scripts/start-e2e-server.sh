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
    echo "[e2e] Cleaning up..."
    if [[ -n "$DAEMON_STARTED" ]]; then
        echo "[e2e] Stopping bd daemon..."
        (cd "$E2E_WORKSPACE" && bd daemon stop 2>/dev/null) || true
    fi
    # Kill any orphaned bd daemons spawned under the e2e workspace tree.
    # This catches workspace-lifecycle daemons that the server auto-starts.
    if [[ -d "$E2E_WORKSPACE" ]]; then
        for pidfile in "$E2E_WORKSPACE"/.beads/bd.pid "$E2E_WORKSPACE"/.loom-config/workspaces/*/beads/bd.pid; do
            [[ -f "$pidfile" ]] || continue
            pid=$(cat "$pidfile" 2>/dev/null) && kill "$pid" 2>/dev/null || true
        done
    fi
}
trap cleanup EXIT INT TERM

# --- 0. Kill orphaned processes from previous runs ---
# Previous test runs may have left daemon/server processes that hold file
# descriptors. Kill any loom-e2e serve and bd daemons rooted under E2E_WORKSPACE.
if [[ -f "$E2E_WORKSPACE/.beads/bd.pid" ]]; then
    old_pid=$(cat "$E2E_WORKSPACE/.beads/bd.pid" 2>/dev/null)
    [[ -n "$old_pid" ]] && kill "$old_pid" 2>/dev/null || true
fi
# Also kill any loom-e2e serve still running on our port.
fuser -k "${E2E_PORT:-8080}/tcp" 2>/dev/null || true

# --- 1. Build loom binary (skip if fresh) ---
# Prefer the pre-built binary from `make build` when available and newer.
MAIN_BIN="$REPO_ROOT/loom"
if [[ -x "$MAIN_BIN" ]] && { [[ ! -x "$LOOM_BIN" ]] || [[ "$MAIN_BIN" -nt "$LOOM_BIN" ]]; }; then
    echo "[e2e] Copying pre-built loom binary..."
    mkdir -p "$(dirname "$LOOM_BIN")"
    cp "$MAIN_BIN" "$LOOM_BIN"
elif [[ ! -x "$LOOM_BIN" ]]; then
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
export LOOM_CONFIG_DIR="$E2E_WORKSPACE/.loom-config"
mkdir -p "$LOOM_CONFIG_DIR"
(cd "$E2E_WORKSPACE" && git init -q && git commit --allow-empty -m "e2e seed" -q && bd init --prefix loomcli --skip-hooks -q)
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


PORT="${E2E_PORT:-8080}"
echo "[e2e] Starting loom serve (port :${PORT})..."
# Run from E2E workspace so the Loom API server also discovers the isolated daemon.
cd "$E2E_WORKSPACE"
exec "$LOOM_BIN" serve \
    --webui-socket "$E2E_SOCKET" \
    --port "${PORT}"
