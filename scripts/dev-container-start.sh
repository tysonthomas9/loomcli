#!/usr/bin/env bash
# dev-container-start.sh — orchestrator for the Loom dev container.
#
# Mirrors the working e2e flow (scripts/start-e2e-server.sh): register
# workspaces via the `loom workspace create` CLI BEFORE starting the server
# so the server reads them from config at boot. Never POST /api/workspaces at
# runtime — that handler can stall on bd-init edge cases and deadlock GETs.
#
# Source repos and workspaces live at distinct paths. Source repos live
# under /root/.loom/src/<name> and workspaces live under
# /root/.loom/workspaces/<name>. This keeps `loom workspace create`'s
# --path and --repos arguments distinct so the git worktree does not get
# nested inside its own source repo (see loomcli-r3ddn.3).
#
# EXTRA_WORKSPACES uses "<name>:<src>:<path>" tuples, whitespace-separated.
set -euo pipefail

API_PORT=${API_PORT:-8080}
UI_PORT=${UI_PORT:-3000}
DEFAULT_BACKEND=${DEFAULT_BACKEND:-claude}

# Primary workspace owns the bd daemon. Extra workspaces get registered via
# `loom workspace create` CLI. Source and workspace paths MUST be distinct
# so `loom workspace create` does not produce a nested worktree gitlink.
PRIMARY_NAME=${PRIMARY_NAME:-alpha}
PRIMARY_SRC=${PRIMARY_SRC:-/root/.loom/src/alpha}
PRIMARY_PATH=${PRIMARY_PATH:-/root/.loom/workspaces/alpha}
EXTRA_WORKSPACES=${EXTRA_WORKSPACES:-bravo:/root/.loom/src/bravo:/root/.loom/workspaces/bravo}

# Scope loom's state to a container-local config dir so we don't need to care
# about ~/.loom vs /root/.loom layout.
export LOOM_CONFIG_DIR=${LOOM_CONFIG_DIR:-/root/.loom-config}
mkdir -p "$LOOM_CONFIG_DIR"

log() { printf '[dev] %s\n' "$*"; }

# ── 1. Seed primary source repo (git only — workspace owns bd init) ──
if [ ! -d "$PRIMARY_SRC/.git" ]; then
    log "seeding primary source repo '$PRIMARY_NAME' at $PRIMARY_SRC"
    mkdir -p "$PRIMARY_SRC"
    git -C "$PRIMARY_SRC" init -q
    git -C "$PRIMARY_SRC" config user.email dev@loom.local
    git -C "$PRIMARY_SRC" config user.name loom-dev
    git -C "$PRIMARY_SRC" commit --allow-empty -qm "init"
fi

# Pre-init bd at the workspace path so the prefix is set before
# `loom workspace create` runs its own (prefix-less) bd init. This is the
# daemon's home; loom serve auto-discovers the socket from cwd=$PRIMARY_PATH.
mkdir -p "$PRIMARY_PATH"
if [ ! -d "$PRIMARY_PATH/.beads" ]; then
    log "initializing bd at workspace path $PRIMARY_PATH"
    (cd "$PRIMARY_PATH" && bd init --prefix "$PRIMARY_NAME" --skip-hooks -q)
fi

# ── 2. Seed extra workspaces: git init on the SOURCE path only ──
for spec in $EXTRA_WORKSPACES; do
    ws_name=${spec%%:*}
    rest=${spec#*:}
    ws_src=${rest%%:*}
    ws_path=${rest#*:}
    if [ ! -d "$ws_src/.git" ]; then
        log "seeding extra source repo '$ws_name' at $ws_src"
        mkdir -p "$ws_src"
        git -C "$ws_src" init -q
        git -C "$ws_src" config user.email dev@loom.local
        git -C "$ws_src" config user.name loom-dev
        git -C "$ws_src" commit --allow-empty -qm "init"
    fi
    # Pre-init bd at the workspace path for the prefix, same reason as primary.
    mkdir -p "$ws_path"
    if [ ! -d "$ws_path/.beads" ]; then
        log "initializing bd at workspace path $ws_path"
        (cd "$ws_path" && bd init --prefix "$ws_name" --skip-hooks -q) || true
    fi
done

# ── 3a. Register the primary workspace as default ──
#
# --repos and --path MUST be distinct filesystem locations. Otherwise
# `loom workspace create` rejects up front (loomcli-r3ddn.3): with
# --repos == --path, git worktree add creates a nested gitlink worktree
# (is_linked_worktree=true) inside the source repo, which the frontend
# sidebar then filters out.
#
# PRIMARY_PATH remains the cwd of `loom serve`, which is required so
# DefaultWorkspaceID matches what cwd auto-discovers for bd socket lookup.
log "registering primary workspace '$PRIMARY_NAME' as default via loom CLI"
loom workspace create "$PRIMARY_NAME" --repos "$PRIMARY_SRC" --path "$PRIMARY_PATH" --default \
    2>/dev/null || log "  (already registered or create failed — continuing)"

# ── 3b. Register extra workspaces via the loom CLI (writes to LOOM_CONFIG_DIR) ──
for spec in $EXTRA_WORKSPACES; do
    ws_name=${spec%%:*}
    rest=${spec#*:}
    ws_src=${rest%%:*}
    ws_path=${rest#*:}
    log "registering workspace '$ws_name' via loom CLI"
    loom workspace create "$ws_name" --repos "$ws_src" --path "$ws_path" \
        2>/dev/null || log "  (already registered or create failed — continuing)"
done

# ── 4. Start bd daemon in every workspace ──
#
# loom serve's per-workspace pools try to connect on first use. Without a
# daemon already running, the pool fails once, trips the circuit breaker,
# and the workspace is unreachable for ~30s on first request. Starting all
# daemons up front avoids that cold-start. The daemon lives at the
# workspace path (not the source path) so loom serve's cwd-based
# auto-discovery finds it.
start_bd_daemon() {
    local ws_path="$1"
    local ws_name="$2"
    log "starting bd daemon in $ws_path"
    (cd "$ws_path" && bd daemon start) || true

    local socket="$ws_path/.beads/bd.sock"
    for _ in $(seq 1 10); do
        [ -S "$socket" ] && return 0
        sleep 0.5
    done
    log "  bd daemon socket never appeared for '$ws_name' at $socket"
    return 1
}

start_bd_daemon "$PRIMARY_PATH" "$PRIMARY_NAME" \
    || { log "primary daemon startup failed"; exit 1; }
for spec in $EXTRA_WORKSPACES; do
    ws_name=${spec%%:*}
    rest=${spec#*:}
    ws_path=${rest#*:}
    start_bd_daemon "$ws_path" "$ws_name" || true
done

SOCKET="$PRIMARY_PATH/.beads/bd.sock"

# ── 5. Cleanup hook ──
cleanup() {
    kill "$(jobs -p)" 2>/dev/null || true
    (cd "$PRIMARY_PATH" && bd daemon stop 2>/dev/null) || true
}
trap cleanup EXIT INT TERM

# ── 6. Start loom serve from within the primary workspace ──
#
# No --webui-socket: that flag builds a single "prebuilt" pool from the
# flagged socket that the server hands to whichever workspace registers
# first (SetPrebuiltPool uses initialWorkspaceID). With two+ workspaces
# that causes cross-workspace data leaks — bravo's pool ends up bound to
# alpha's bd daemon. Letting each workspace auto-discover its own
# <wsPath>/.beads/bd.sock keeps the per-workspace multiPool routing honest.
log "starting loom serve on :${API_PORT}"
cd "$PRIMARY_PATH"
loom serve \
    --bind 127.0.0.1 --port "$API_PORT" \
    --frontend-url "${LOOM_FRONTEND_URL:-http://localhost:${UI_PORT}}" \
    --frontend-url "http://localhost:${UI_PORT}" \
    --no-daemon &

# ── 7. Wait for /api/health and /api/workspaces to both work before UI boots ──
for _ in $(seq 1 30); do
    if curl -fs --max-time 2 "http://127.0.0.1:${API_PORT}/api/health" >/dev/null 2>&1 \
       && curl -fs --max-time 2 "http://127.0.0.1:${API_PORT}/api/workspaces" >/dev/null 2>&1
    then break; fi
    sleep 1
done
curl -fs --max-time 2 "http://127.0.0.1:${API_PORT}/api/workspaces" >/dev/null 2>&1 \
    || { log "loom serve /api/workspaces not responding"; exit 1; }
log "loom serve ready"

# ── 8. Set default backend on every registered workspace (non-blocking) ──
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

# ── 9. Vite preview (foreground, keeps container alive) ──
log "starting vite preview on :${UI_PORT}"
cd /src/internal/webui/frontend
exec npx vite preview --host 0.0.0.0 --port "$UI_PORT" --strictPort
