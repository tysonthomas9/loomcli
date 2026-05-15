#!/usr/bin/env bash
# start-e2e-server.sh — Build loom and exec loom serve for Playwright e2e tests.
# Designed to be invoked by Playwright's webServer config.
# Playwright kills this process when tests finish; trap cleans up child processes.
#
# Uses an isolated FleetDB workspace (tmp/e2e-workspace/) so tests never
# touch the repository's real local FleetDB database.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
FRONTEND_DIR="$REPO_ROOT/internal/webui/frontend"
LOOM_BIN="$REPO_ROOT/tmp/loom-e2e"
FLEET_DB_REPO="${FLEET_DB_REPO:-$REPO_ROOT/../../fleet-db}"
FLEET_DB_BIN="${FLEET_DB_BIN:-$REPO_ROOT/tmp/fleet-db}"
E2E_WORKSPACE="$REPO_ROOT/tmp/e2e-workspace"

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
    # Clean up second workspace
    rm -rf "$REPO_ROOT/tmp/e2e-workspace-2" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# --- 0. Kill orphaned processes from previous runs ---
# Previous test runs may have left server processes that hold file descriptors.
kill_port_listeners() {
    local port="$1"
    local default_port="$2"
    local pid
    [[ "$port" == "$default_port" ]] && return 0
    if command -v lsof >/dev/null 2>&1; then
        while IFS= read -r pid; do
            [[ -n "$pid" ]] && kill "$pid" 2>/dev/null || true
        done < <(lsof -ti "TCP:$port" -sTCP:LISTEN 2>/dev/null || true)
    elif command -v fuser >/dev/null 2>&1; then
        fuser -k "$port/tcp" >/dev/null 2>&1 || true
    fi
}

# Only do this for non-default ports to avoid killing unrelated services.
kill_port_listeners "${E2E_PORT:-8090}" "8080"
kill_port_listeners "${E2E_FRONTEND_PORT:-3100}" "3000"

# --- 1. Build loom binary (skip if fresh) ---
# Prefer the pre-built binary from `make build` when available and newer.
MAIN_BIN="$REPO_ROOT/loom"

go_sources_newer_than() {
    local target="$1"
    local dirs=()
    [[ ! -e "$target" ]] && return 0
    [[ "$REPO_ROOT/go.mod" -nt "$target" || "$REPO_ROOT/go.sum" -nt "$target" ]] && return 0
    for dir in "$REPO_ROOT/cmd" "$REPO_ROOT/internal" "$REPO_ROOT/pkg"; do
        [[ -d "$dir" ]] && dirs+=("$dir")
    done
    [[ "${#dirs[@]}" -eq 0 ]] && return 1
    [[ -n "$(find "${dirs[@]}" -type f -name '*.go' ! -name '*_test.go' -newer "$target" -print -quit)" ]]
}

if [[ -x "$MAIN_BIN" ]] && ! go_sources_newer_than "$MAIN_BIN" && { [[ ! -x "$LOOM_BIN" ]] || [[ "$MAIN_BIN" -nt "$LOOM_BIN" ]] || go_sources_newer_than "$LOOM_BIN"; }; then
    echo "[e2e] Copying pre-built loom binary..."
    mkdir -p "$(dirname "$LOOM_BIN")"
    cp "$MAIN_BIN" "$LOOM_BIN"
elif [[ ! -x "$LOOM_BIN" ]] || go_sources_newer_than "$LOOM_BIN"; then
    echo "[e2e] Building loom binary..."
    mkdir -p "$(dirname "$LOOM_BIN")"
    (cd "$REPO_ROOT" && go build -o "$LOOM_BIN" ./cmd/loom)
else
    echo "[e2e] Using cached loom binary"
fi

# --- 1b. Build fleet-db binary for embedded FleetDB-backed tests ---
# loom serve starts local FleetDB through this binary when LOOM_ISSUE_BACKEND=fleetdb.
if [[ ! -x "$FLEET_DB_BIN" ]]; then
    if [[ ! -d "$FLEET_DB_REPO/cmd/fleet-db" ]]; then
        echo "[e2e] ERROR: fleet-db repo not found at $FLEET_DB_REPO"
        echo "[e2e] Set FLEET_DB_REPO or FLEET_DB_BIN before running real Playwright tests."
        exit 1
    fi
    echo "[e2e] Building fleet-db binary..."
    mkdir -p "$(dirname "$FLEET_DB_BIN")"
    (cd "$FLEET_DB_REPO" && CGO_ENABLED=0 go build -o "$FLEET_DB_BIN" ./cmd/fleet-db)
else
    echo "[e2e] Using cached fleet-db binary"
fi
export FLEET_DB_BIN
export FLEET_RATE_LIMIT_ENABLED="${FLEET_RATE_LIMIT_ENABLED:-false}"
export FLEET_REDIS_POOL_SIZE="${FLEET_REDIS_POOL_SIZE:-200}"
export FLEET_REDIS_MIN_IDLE_CONNS="${FLEET_REDIS_MIN_IDLE_CONNS:-10}"

# --- 2. Build frontend dist if missing or stale ---
frontend_dist_index="$FRONTEND_DIR/dist/index.html"
frontend_needs_build=0
if [[ ! -f "$frontend_dist_index" ]]; then
    frontend_needs_build=1
elif [[ "$FRONTEND_DIR/index.html" -nt "$frontend_dist_index" ]]; then
    frontend_needs_build=1
elif [[ "$FRONTEND_DIR/package.json" -nt "$frontend_dist_index" ]] || [[ "$FRONTEND_DIR/package-lock.json" -nt "$frontend_dist_index" ]] || [[ "$FRONTEND_DIR/vite.config.ts" -nt "$frontend_dist_index" ]]; then
    frontend_needs_build=1
elif [[ -n "$(find "$FRONTEND_DIR/src" -type f -newer "$frontend_dist_index" -print -quit)" ]]; then
    frontend_needs_build=1
elif [[ -n "$(find "$FRONTEND_DIR/public" -type f -newer "$frontend_dist_index" -print -quit)" ]]; then
    frontend_needs_build=1
fi

if [[ "$frontend_needs_build" -eq 1 ]]; then
    echo "[e2e] Building frontend..."
    (
        cd "$FRONTEND_DIR"
        if [[ ! -d node_modules ]]; then
            npm ci --prefer-offline
        fi
        npm run build
    )
fi

# --- 3. Create isolated workspace for E2E tests ---
# Fresh workspace each run so tests start with a clean database.
rm -rf "$E2E_WORKSPACE"
mkdir -p "$E2E_WORKSPACE"
export LOOM_CONFIG_DIR="$E2E_WORKSPACE/.loom-config"
mkdir -p "$LOOM_CONFIG_DIR"
(
    cd "$E2E_WORKSPACE" &&
        git init -q &&
        git config user.name "Loom E2E" &&
        git config user.email "loom-e2e@example.test" &&
        git commit --allow-empty -m "e2e seed" -q
)
echo "[e2e] Created isolated workspace: $E2E_WORKSPACE"

# --- 3b. Create a second workspace for cross-workspace tests ---
E2E_WORKSPACE_2="$REPO_ROOT/tmp/e2e-workspace-2"
rm -rf "$E2E_WORKSPACE_2"
mkdir -p "$E2E_WORKSPACE_2"
(
    cd "$E2E_WORKSPACE_2" &&
        git init -q &&
        git config user.name "Loom E2E" &&
        git config user.email "loom-e2e@example.test" &&
        git commit --allow-empty -m "e2e seed 2" -q
)
LOOM_CONFIG_DIR="$E2E_WORKSPACE/.loom-config" "$LOOM_BIN" workspace create e2e-ws-2 \
    --repos "$E2E_WORKSPACE_2" --path "$E2E_WORKSPACE_2" 2>/dev/null || true
echo "[e2e] Created second workspace: $E2E_WORKSPACE_2"

# --- 4. Start loom serve with isolated FleetDB local state ---
PORT="${E2E_PORT:-8080}"
FRONTEND_PORT="${E2E_FRONTEND_PORT:-3100}"
echo "[e2e] Starting loom serve (port :${PORT})..."
# Disable h2c (HTTP/2 cleartext) wrapping — Node.js HTTP clients used by
# Playwright helpers hang on PATCH requests when the server uses h2c.
export LOOM_DISABLE_H2C=1
export LOOM_ISSUE_BACKEND=fleetdb
export LOOM_FLEET_DB_ACTOR=loom-e2e
# Run from E2E workspace so the Loom API server discovers the isolated project.
cd "$E2E_WORKSPACE"
"$LOOM_BIN" serve \
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

# Register the primary workspace through the live API. This guarantees the
# FleetDB-backed server, workspace registry, and active workspace state all see
# the same workspace before browser/API tests connect.
PRIMARY_WS_NAME="e2e-ws"
PRIMARY_WS_KEY="E2E-WS"
seed_payload="$(node -e 'const [name, repo] = process.argv.slice(1); process.stdout.write(JSON.stringify({ name, type: "empty", repos: [repo] }));' "$PRIMARY_WS_NAME" "$E2E_WORKSPACE")"
seed_response="$E2E_WORKSPACE/.workspace-create-response.json"
echo "[e2e] Ensuring primary workspace exists: ${PRIMARY_WS_KEY}..."
set +e
seed_status="$(curl -sS -o "$seed_response" -w "%{http_code}" \
    -X POST "http://127.0.0.1:${PORT}/api/workspaces" \
    -H "Content-Type: application/json" \
    --data-binary "$seed_payload")"
seed_curl_status=$?
set -e
if [[ "$seed_curl_status" -ne 0 ]]; then
    echo "[e2e] Workspace create request failed; polling in case the server completed it before closing the connection."
elif [[ "$seed_status" != "201" && "$seed_status" != "200" && "$seed_status" != "409" ]]; then
    echo "[e2e] Workspace create returned HTTP ${seed_status}; polling before treating it as fatal."
    cat "$seed_response" 2>/dev/null || true
fi

workspaces_json=""
for i in $(seq 1 30); do
    workspaces_json="$(curl -fsS "http://127.0.0.1:${PORT}/api/workspaces" 2>/dev/null || true)"
    if [[ "$workspaces_json" == *"\"id\":\"${PRIMARY_WS_KEY}\""* ]]; then
        break
    fi
    sleep 0.5
done
if [[ "$workspaces_json" != *"\"id\":\"${PRIMARY_WS_KEY}\""* ]]; then
    echo "[e2e] ERROR: primary workspace ${PRIMARY_WS_KEY} was not registered"
    echo "[e2e] Last workspace list response: ${workspaces_json}"
    echo "[e2e] Workspace create response:"
    cat "$seed_response" 2>/dev/null || true
    exit 1
fi
echo "[e2e] Primary workspace ready: ${PRIMARY_WS_KEY}"

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

frontend_config_response="$E2E_WORKSPACE/.frontend-config-response.json"
set +e
frontend_config_status="$(curl -sS -o "$frontend_config_response" -w "%{http_code}" \
    "http://127.0.0.1:${FRONTEND_PORT}/api/config")"
frontend_config_curl_status=$?
set -e
if [[ "$frontend_config_curl_status" -ne 0 || "$frontend_config_status" != "200" ]]; then
    echo "[e2e] ERROR: vite preview did not proxy /api/config to loom serve"
    echo "[e2e] /api/config status: ${frontend_config_status:-curl-failed}"
    cat "$frontend_config_response" 2>/dev/null || true
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
