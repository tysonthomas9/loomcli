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

# The dev container already runs the headless platform runtime. Lead agents may
# run `loom workspace ops ensure-runtime`; keep that from starting the packaged
# Desktop runtime alongside it.
export LOOM_LOCAL_RUNTIME="${LOOM_LOCAL_RUNTIME:-disabled}"

# fleet-db --auth-dev-mode reads the X-Actor header; the loom CLI sends it
# from LOOM_FLEET_DB_ACTOR. Without this every CLI call hits HTTP 401.
export LOOM_FLEET_DB_ACTOR="${LOOM_FLEET_DB_ACTOR:-loom-dev}"

log() { printf '[dev] %s\n' "$*"; }

# ── 0. Mirror read-only auth dirs to writable copies ──
# The host mounts ~/.codex, ~/.claude, ~/.config/opencode read-only, but the
# CLIs (codex especially) write session state under those dirs at runtime and
# crash with "Read-only file system (os error 30)" otherwise. Copy each into
# a writable sibling and point the CLI env vars at the copy.
#
# Codex runtime state is intentionally not copied wholesale. Host SQLite/log
# files can be version- or platform-specific, and a stale/corrupt state DB makes
# the containerized TUI print "file is not a database" before every lead prompt.
# Host MCP/plugin config is also omitted because desktop plugin binaries are
# often not executable inside the Linux dev container.
mirror_rw() {
    local src=$1 dst=$2
    [ -d "$src" ] || return 0
    [ -d "$dst" ] && return 0
    log "mirroring $src → $dst (writable copy for CLI state)"
    mkdir -p "$(dirname "$dst")"
    cp -r "$src" "$dst"
}

sanitize_codex_config() {
    local src_file=$1 dst_file=$2
    awk '
        /^notify[[:space:]]*=/ { next }
        /^\[mcp_servers(\.|])/ { skip = 1; next }
        /^\[plugins\./ { skip = 1; next }
        /^\[/ { skip = 0 }
        !skip { print }
    ' "$src_file" > "$dst_file"
    chmod 600 "$dst_file" 2>/dev/null || true
}

mirror_codex_rw() {
    local src=$1 dst=$2
    [ -d "$src" ] || return 0
    [ -d "$dst" ] && return 0
    log "mirroring $src → $dst (sanitized writable copy for Codex auth/config)"
    mkdir -p "$dst"

    local file
    for file in \
        auth.json \
        .credentials.json \
        AGENTS.md \
        installation_id \
        internal_storage.json \
        version.json \
        .codex-global-state.json \
        .personality_migration
    do
        [ -e "$src/$file" ] && cp -p "$src/$file" "$dst/$file"
    done
    [ -e "$src/config.toml" ] && sanitize_codex_config "$src/config.toml" "$dst/config.toml"

    local dir
    for dir in rules skills plugins memories vendor_imports; do
        [ -d "$src/$dir" ] && cp -R "$src/$dir" "$dst/$dir"
    done
}

mirror_codex_rw /root/.codex    /root/.codex-rw
mirror_rw /root/.claude         /root/.claude-rw
mirror_rw /root/.config/opencode /root/.config/opencode-rw
export CODEX_HOME=/root/.codex-rw
export CLAUDE_CONFIG_DIR=/root/.claude-rw

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

# Pre-seeded workspaces are registered but inactive. Select the primary one for
# CLI defaults; the serve runtime itself spans every workspace below.
loom workspace use "$PRIMARY_NAME" >/dev/null 2>&1 \
    || log "  (primary workspace '$PRIMARY_NAME' not yet registered)"

# ── 4. Cleanup hook ──
cleanup() {
    kill "$(jobs -p)" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# ── 5. Start loom serve from within the primary workspace ──
log "starting loom serve on :${API_PORT}"
export LOOM_ISSUE_BACKEND="${LOOM_ISSUE_BACKEND:-fleetdb}"
export LOOM_FLEET_DB_ACTOR="${LOOM_FLEET_DB_ACTOR:-loom-dev}"
export LOOM_DRIVER_EXECUTOR_WORKSPACE="${LOOM_DRIVER_EXECUTOR_WORKSPACE:-*}"
cd "$PRIMARY_PATH"
loom serve \
    --bind 127.0.0.1 --port "$API_PORT" \
    --frontend-url "${LOOM_FRONTEND_URL:-http://localhost:${UI_PORT}}" \
    --frontend-url "http://localhost:${UI_PORT}" &

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
