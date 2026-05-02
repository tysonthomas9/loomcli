#!/usr/bin/env bash
# dev-container-start.sh — orchestrator for the Loom dev container.
#
# Mirrors the e2e flow (scripts/start-e2e-server.sh): register workspaces via
# the `loom workspace create` CLI before starting the server so the server reads
# them from config at boot.
set -euo pipefail

API_PORT=${API_PORT:-8080}
UI_PORT=${UI_PORT:-3000}
DEFAULT_BACKEND=${DEFAULT_BACKEND:-claude}

# Extra workspaces get registered via `loom workspace create` CLI.
PRIMARY_NAME=${PRIMARY_NAME:-alpha}
PRIMARY_PATH=${PRIMARY_PATH:-/root/.loom/workspaces/alpha}
EXTRA_WORKSPACES=${EXTRA_WORKSPACES:-bravo:/root/.loom/workspaces/bravo}

# Scope loom's state to a container-local config dir so we don't need to care
# about ~/.loom vs /root/.loom layout.
export LOOM_CONFIG_DIR=${LOOM_CONFIG_DIR:-/root/.loom-config}
mkdir -p "$LOOM_CONFIG_DIR"

log() { printf '[dev] %s\n' "$*"; }

# ── 1. Seed primary workspace: git only ──
if [ ! -d "$PRIMARY_PATH/.git" ]; then
    log "seeding primary workspace '$PRIMARY_NAME' at $PRIMARY_PATH"
    mkdir -p "$PRIMARY_PATH"
    git -C "$PRIMARY_PATH" init -q
    git -C "$PRIMARY_PATH" config user.email dev@loom.local
    git -C "$PRIMARY_PATH" config user.name loom-dev
    git -C "$PRIMARY_PATH" commit --allow-empty -qm "init"
fi

# ── 2. Seed extra workspaces: git only ──
for spec in $EXTRA_WORKSPACES; do
    ws_name=${spec%%:*}
    ws_path=${spec#*:}
    if [ ! -d "$ws_path/.git" ]; then
        log "seeding extra workspace '$ws_name' at $ws_path"
        mkdir -p "$ws_path"
        git -C "$ws_path" init -q
        git -C "$ws_path" config user.email dev@loom.local
        git -C "$ws_path" config user.name loom-dev
        git -C "$ws_path" commit --allow-empty -qm "init"
    fi
done

# ── 3. Register extra workspaces via the loom CLI (writes to LOOM_CONFIG_DIR) ──
for spec in $EXTRA_WORKSPACES; do
    ws_name=${spec%%:*}
    ws_path=${spec#*:}
    log "registering workspace '$ws_name' via loom CLI"
    loom workspace create "$ws_name" --repos "$ws_path" --path "$ws_path" \
        2>/dev/null || log "  (already registered or create failed — continuing)"
done

# ── 4. Cleanup hook ──
cleanup() {
    kill "$(jobs -p)" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# ── 5. Start loom serve from within the primary workspace ──
log "starting loom serve on :${API_PORT}"
export LOOM_ISSUE_BACKEND="${LOOM_ISSUE_BACKEND:-fleetdb}"
export LOOM_FLEET_DB_ACTOR="${LOOM_FLEET_DB_ACTOR:-loom-dev}"
cd "$PRIMARY_PATH"
loom serve \
    --bind 127.0.0.1 --port "$API_PORT" \
    --frontend-url "${LOOM_FRONTEND_URL:-http://localhost:${UI_PORT}}" \
    --frontend-url "http://localhost:${UI_PORT}" \
    --no-daemon &

# ── 6. Wait for /api/health and /api/workspaces to both work before UI boots ──
for _ in $(seq 1 30); do
    if curl -fs --max-time 2 "http://127.0.0.1:${API_PORT}/api/health" >/dev/null 2>&1 \
       && curl -fs --max-time 2 "http://127.0.0.1:${API_PORT}/api/workspaces" >/dev/null 2>&1
    then break; fi
    sleep 1
done
curl -fs --max-time 2 "http://127.0.0.1:${API_PORT}/api/workspaces" >/dev/null 2>&1 \
    || { log "loom serve /api/workspaces not responding"; exit 1; }
log "loom serve ready"

# ── 7. Set default backend on every registered workspace (non-blocking) ──
(
    ws_ids=$(curl -s "http://127.0.0.1:${API_PORT}/api/workspaces" \
        | jq -r '.workspaces[]?.id' 2>/dev/null || true)
    for ws_id in $ws_ids; do
        curl --max-time 3 -sS -X PATCH \
            "http://127.0.0.1:${API_PORT}/api/workspaces/$ws_id/config/backend" \
            -H "Content-Type: application/json" \
            -d "{\"backend\":\"$DEFAULT_BACKEND\"}" >/dev/null 2>&1 || true
    done
    log "default backend '$DEFAULT_BACKEND' applied where possible"
) &

# ── 8. Vite preview (foreground, keeps container alive) ──
log "starting vite preview on :${UI_PORT}"
cd /src/internal/webui/frontend
exec npx vite preview --host 0.0.0.0 --port "$UI_PORT" --strictPort
