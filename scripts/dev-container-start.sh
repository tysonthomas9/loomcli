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
REPO_BASE=${REPO_BASE:-/root/.loom/repos}
PRIMARY_REPO_PATH=${PRIMARY_REPO_PATH:-${REPO_BASE}/${PRIMARY_NAME}}
EXTRA_WORKSPACES=${EXTRA_WORKSPACES:-bravo:/root/.loom/workspaces/bravo}

# Scope loom's state to a container-local config dir so we don't need to care
# about ~/.loom vs /root/.loom layout.
export LOOM_CONFIG_DIR=${LOOM_CONFIG_DIR:-/root/.loom-config}
mkdir -p "$LOOM_CONFIG_DIR"

# fleet-db --auth-dev-mode reads the X-Actor header; the loom CLI sends it
# from LOOM_FLEET_DB_ACTOR. Without this every CLI call hits HTTP 401.
export LOOM_FLEET_DB_ACTOR="${LOOM_FLEET_DB_ACTOR:-loom-dev}"
export FLEET_RATE_LIMIT_ENABLED="${FLEET_RATE_LIMIT_ENABLED:-false}"
export FLEET_REDIS_POOL_SIZE="${FLEET_REDIS_POOL_SIZE:-50}"

log() { printf '[dev] %s\n' "$*"; }

workspace_key() {
    printf '%s' "$1" \
        | tr '[:lower:]' '[:upper:]' \
        | sed -E 's/[^A-Z0-9_.-]+//g; s/[_.-]+/-/g; s/^-+//; s/-+$//; s/^([^A-Z])/W-\1/' \
        | cut -c1-32
}

seed_repo() {
    local name=$1 repo_path=$2
    if [ ! -d "$repo_path/.git" ]; then
        log "seeding source repo '$name' at $repo_path"
        mkdir -p "$repo_path"
        git -C "$repo_path" init -q
        git -C "$repo_path" config user.email dev@loom.local
        git -C "$repo_path" config user.name loom-dev
        git -C "$repo_path" commit --allow-empty -qm "init"
    fi
}

register_workspace() {
    local name=$1 workspace_path=$2 repo_path=$3
    log "registering workspace '$name' via loom CLI"
    mkdir -p "$(dirname "$workspace_path")"
    loom workspace create "$name" --repos "$repo_path" --path "$workspace_path" \
        2>/dev/null || log "  (already registered or create failed — continuing)"
}

# ── 0. Mirror read-only auth dirs to writable copies ──
# The host mounts ~/.codex, ~/.claude, ~/.config/opencode read-only, but the
# CLIs (codex especially) write session state under those dirs at runtime and
# crash with "Read-only file system (os error 30)" otherwise. Copy each into
# a writable sibling and point the CLI env vars at the copy.
mirror_rw() {
    local src=$1 dst=$2
    [ -d "$src" ] || return 0
    [ -d "$dst" ] && return 0
    log "mirroring $src → $dst (writable copy for CLI state)"
    mkdir -p "$(dirname "$dst")"
    cp -r "$src" "$dst"
}
mirror_rw /root/.codex          /root/.codex-rw
mirror_rw /root/.claude         /root/.claude-rw
mirror_rw /root/.config/opencode /root/.config/opencode-rw
export CODEX_HOME=/root/.codex-rw
export CLAUDE_CONFIG_DIR=/root/.claude-rw

# ── 1. Seed primary source repo ──
seed_repo "$PRIMARY_NAME" "$PRIMARY_REPO_PATH"

# ── 2. Seed extra source repos ──
for spec in $EXTRA_WORKSPACES; do
    ws_name=${spec%%:*}
    rest=${spec#*:}
    ws_path=${rest%%:*}
    repo_path=${rest#*:}
    [ "$repo_path" != "$rest" ] || repo_path="${REPO_BASE}/${ws_name}"
    seed_repo "$ws_name" "$repo_path"
done

# ── 3. Register workspaces via the loom CLI (writes to LOOM_CONFIG_DIR) ──
register_workspace "$PRIMARY_NAME" "$PRIMARY_PATH" "$PRIMARY_REPO_PATH"
for spec in $EXTRA_WORKSPACES; do
    ws_name=${spec%%:*}
    rest=${spec#*:}
    ws_path=${rest%%:*}
    repo_path=${rest#*:}
    [ "$repo_path" != "$rest" ] || repo_path="${REPO_BASE}/${ws_name}"
    register_workspace "$ws_name" "$ws_path" "$repo_path"
done

# Pre-seeded workspaces are registered but inactive. The loom daemon resolves
# its agent list from the *active* workspace, so set one now; the daemon
# watcher below promotes any newly-created workspace as it appears.
PRIMARY_KEY=$(workspace_key "$PRIMARY_NAME")
loom workspace use "$PRIMARY_KEY" >/dev/null 2>&1 \
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

# ── 8. Agent daemon watcher ──
# loom serve only handles HTTP + terminal. The agent supervisor that actually
# runs codex/claude/opencode lives in a separate `loom daemon` process and
# expects to be pointed at one workspace via LOOM_WORKSPACE. The dev container
# can't know the user's workspace upfront (the onboarding flow creates it via
# the UI), so this watcher polls /api/workspaces and brings up one daemon per
# workspace that has at least one configured agent. It respawns if a daemon
# exits.
(
    declare -A DAEMON_PIDS=()
    start_daemon_for() {
        local ws=$1 ws_path=$2
        log "starting loom daemon for workspace $ws (cwd $ws_path)"
        loom workspace use "$ws" >/dev/null 2>&1 || true
        (
            cd "$ws_path"
            LOOM_WORKSPACE="$ws" loom daemon >> "/tmp/loom-daemon-$ws.log" 2>&1
        ) &
        DAEMON_PIDS[$ws]=$!
    }
    while sleep 5; do
        ws_listing=$(curl -fs --max-time 3 \
            "http://127.0.0.1:${API_PORT}/api/workspaces" 2>/dev/null) || continue
        while IFS=$'\t' read -r ws ws_path; do
            [ -z "$ws" ] && continue
            agent_count=$(curl -fs --max-time 3 \
                "http://127.0.0.1:${API_PORT}/api/workspaces/$ws/agents" 2>/dev/null \
                | jq '.data | length // 0' 2>/dev/null || echo 0)
            [ "$agent_count" -eq 0 ] && continue
            pid=${DAEMON_PIDS[$ws]:-}
            if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
                continue
            fi
            start_daemon_for "$ws" "$ws_path"
        done < <(printf '%s' "$ws_listing" | jq -r '.workspaces[]? | "\(.id)\t\(.path)"')
    done
) &

# ── 9. Vite preview (foreground, keeps container alive) ──
log "starting vite preview on :${UI_PORT}"
cd /src/internal/webui/frontend
exec npx vite preview --host 0.0.0.0 --port "$UI_PORT" --strictPort
