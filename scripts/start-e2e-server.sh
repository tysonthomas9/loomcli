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
LOOM_PID=""
PREVIEW_PID=""

cleanup() {
    echo "[e2e] Cleaning up..."
    if [[ -n "$PREVIEW_PID" ]]; then
        kill "$PREVIEW_PID" 2>/dev/null || true
        wait "$PREVIEW_PID" 2>/dev/null || true
    fi
    if [[ -n "$LOOM_PID" ]]; then
        kill "$LOOM_PID" 2>/dev/null || true
        wait "$LOOM_PID" 2>/dev/null || true
    fi
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
    # Clean up second workspace
    rm -rf "$REPO_ROOT/tmp/e2e-workspace-2" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# --- 0. Kill orphaned processes from previous runs ---
# Previous test runs may have left daemon/server processes that hold file
# descriptors. Kill any loom-e2e serve and bd daemons rooted under E2E_WORKSPACE.
if [[ -f "$E2E_WORKSPACE/.beads/bd.pid" ]]; then
    old_pid=$(cat "$E2E_WORKSPACE/.beads/bd.pid" 2>/dev/null)
    [[ -n "$old_pid" ]] && kill "$old_pid" 2>/dev/null || true
fi
# Kill any stale loom-e2e serve still listening on the e2e port.
# Only do this for non-default ports to avoid killing unrelated services on 8080.
_e2e_port="${E2E_PORT:-8090}"
if [[ "$_e2e_port" != "8080" ]]; then
    fuser -k "$_e2e_port/tcp" 2>/dev/null || true
fi

# Same cleanup for the Vite preview port (used to back integration tests).
_e2e_frontend_port="${E2E_FRONTEND_PORT:-3100}"
if [[ "$_e2e_frontend_port" != "3000" ]]; then
    fuser -k "$_e2e_frontend_port/tcp" 2>/dev/null || true
fi

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

# --- 3b. Create a second workspace for cross-workspace tests ---
E2E_WORKSPACE_2="$REPO_ROOT/tmp/e2e-workspace-2"
rm -rf "$E2E_WORKSPACE_2"
mkdir -p "$E2E_WORKSPACE_2"
(cd "$E2E_WORKSPACE_2" && git init -q && git commit --allow-empty -m "e2e seed 2" -q)
LOOM_CONFIG_DIR="$E2E_WORKSPACE/.loom-config" "$LOOM_BIN" workspace create e2e-ws-2 \
    --repos "$E2E_WORKSPACE_2" --path "$E2E_WORKSPACE_2" 2>/dev/null || true
echo "[e2e] Created second workspace: $E2E_WORKSPACE_2"

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
FRONTEND_PORT="${E2E_FRONTEND_PORT:-3100}"
echo "[e2e] Starting loom serve (port :${PORT})..."
# Disable h2c (HTTP/2 cleartext) wrapping — Node.js HTTP clients used by
# Playwright helpers hang on PATCH requests when the server uses h2c.
export LOOM_DISABLE_H2C=1
# Run from E2E workspace so the Loom API server also discovers the isolated daemon.
cd "$E2E_WORKSPACE"
"$LOOM_BIN" serve \
    --webui-socket "$E2E_SOCKET" \
    --port "${PORT}" \
    --frontend-url "http://127.0.0.1:${FRONTEND_PORT}" \
    --frontend-url "http://localhost:${FRONTEND_PORT}" &
LOOM_PID=$!

# Wait for loom serve to become ready.
echo "[e2e] Waiting for loom serve on :${PORT}..."
for i in $(seq 1 20); do
    if curl -fsS "http://127.0.0.1:${PORT}/health" >/dev/null 2>&1; then
        break
    fi
    sleep 0.5
done
if ! curl -fsS "http://127.0.0.1:${PORT}/health" >/dev/null 2>&1; then
    echo "[e2e] ERROR: loom serve did not become ready on :${PORT}"
    exit 1
fi

echo "[e2e] Starting vite preview (port :${FRONTEND_PORT})..."
(
    cd "$FRONTEND_DIR"
    E2E_API_URL="http://127.0.0.1:${PORT}" \
        npx vite preview --port "${FRONTEND_PORT}" --strictPort --host 127.0.0.1
) &
PREVIEW_PID=$!

# Wait for the Vite preview server to become ready.
echo "[e2e] Waiting for vite preview on :${FRONTEND_PORT}..."
for i in $(seq 1 20); do
    if curl -fsS "http://127.0.0.1:${FRONTEND_PORT}/" >/dev/null 2>&1; then
        break
    fi
    sleep 0.5
done
if ! curl -fsS "http://127.0.0.1:${FRONTEND_PORT}/" >/dev/null 2>&1; then
    echo "[e2e] ERROR: vite preview did not become ready on :${FRONTEND_PORT}"
    exit 1
fi

echo "[e2e] E2E server stack ready (api :${PORT}, frontend :${FRONTEND_PORT})"

# Keep the script attached to loom serve's lifetime. Playwright sends SIGTERM
# to the script on shutdown, which fires the cleanup trap. `wait` is used
# instead of `wait -n` for bash 3.2 compatibility (macOS system bash).
wait "$LOOM_PID"
# If loom serve exited on its own, make sure preview is also torn down.
if [[ -n "${PREVIEW_PID}" ]]; then
    kill "$PREVIEW_PID" 2>/dev/null || true
    wait "$PREVIEW_PID" 2>/dev/null || true
fi
