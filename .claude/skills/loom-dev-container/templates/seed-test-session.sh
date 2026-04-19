#!/usr/bin/env bash
# seed-test-session.sh — populate the dev container with a fake bd issue
# and matching session + transcript so the UI's SessionsTab → Transcript
# flow has something to render without running a real agent.
#
# Usage:
#   ./seed-test-session.sh                           # defaults: alpha, canned Claude transcript
#   TITLE="My test" ./seed-test-session.sh
#   WORKSPACE=bravo ./seed-test-session.sh           # bravo has no bd → issue creation skipped
#   TRANSCRIPT=/path/to/agent.jsonl ./seed-test-session.sh
#   CONTAINER=loom-xyz ./seed-test-session.sh        # for parallel containers
#
# Emits two lines on stdout: "TASK <id>" and "SESSION <id>" for scripts.
set -euo pipefail

CONTAINER=${CONTAINER:-loomcli-dev}
WORKSPACE=${WORKSPACE:-alpha}
TITLE=${TITLE:-"E2E transcript test"}

TEMPLATE_DIR="$(cd "$(dirname "$0")" && pwd)"
TRANSCRIPT=${TRANSCRIPT:-"$TEMPLATE_DIR/sample-claude-transcript.jsonl"}

log() { printf '[seed] %s\n' "$*" >&2; }

# ── Sanity ──────────────────────────────────────────────────────────────
if ! podman ps --filter "name=^${CONTAINER}$" --format '{{.Names}}' 2>/dev/null | grep -qx "$CONTAINER"; then
    echo "error: container '$CONTAINER' not running; start with scripts/dev-container-run.sh" >&2
    exit 1
fi
if [ ! -f "$TRANSCRIPT" ]; then
    echo "error: transcript file '$TRANSCRIPT' not found" >&2
    exit 1
fi

WS_PATH=/root/.loom/workspaces/$WORKSPACE
if ! podman exec "$CONTAINER" test -d "$WS_PATH"; then
    echo "error: workspace '$WORKSPACE' not found in container at $WS_PATH" >&2
    exit 1
fi

# ── Create bd issue (skip if workspace has no .beads) ───────────────────
TASK_ID=""
if podman exec "$CONTAINER" test -d "$WS_PATH/.beads"; then
    log "creating bd issue in $WORKSPACE: $TITLE"
    TASK_ID=$(podman exec "$CONTAINER" bash -c "
        cd $WS_PATH && bd create --title '$TITLE' --type task --priority 2 --json
    " | jq -r '.id')
    log "task_id=$TASK_ID"
else
    TASK_ID="bd-synthetic-$(date +%s)"
    log "workspace has no bd — using synthetic task_id=$TASK_ID"
fi

# ── Build session ID (matches GenerateSessionID shape loosely) ──────────
# Use openssl instead of `tr < /dev/urandom | head` — the latter SIGPIPEs
# under `set -o pipefail` because head closes its stdin while tr is still
# writing.
TS=$(date -u +%Y%m%d-%H%M%S)
SUFFIX=$(openssl rand -hex 4)
SID="${TS}-seed-${SUFFIX}"
log "session_id=$SID"

# ── Write metadata.json, agent_transcript.jsonl, index.jsonl ────────────
SESS_DIR="$WS_PATH/sessions/$SID"
podman exec "$CONTAINER" mkdir -p "$SESS_DIR"

METADATA=$(jq -n \
    --arg sid "$SID" --arg task "$TASK_ID" \
    --arg started "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    '{schema_version:1, session_id:$sid, task_id:$task,
      agent_name:"seed-agent", backend:"claude",
      started_at:$started, status:"completed", exit_code:0}')

# Copy files into the container via stdin (no host-mount assumptions)
printf '%s' "$METADATA" | podman exec -i "$CONTAINER" \
    tee "$SESS_DIR/metadata.json" >/dev/null
podman exec -i "$CONTAINER" tee "$SESS_DIR/agent_transcript.jsonl" \
    >/dev/null < "$TRANSCRIPT"
printf '%s\n' "$METADATA" | podman exec -i "$CONTAINER" \
    tee -a "$WS_PATH/sessions/index.jsonl" >/dev/null

log "session seeded at $SESS_DIR"

# ── Report ──────────────────────────────────────────────────────────────
printf 'TASK %s\n' "$TASK_ID"
printf 'SESSION %s\n' "$SID"
