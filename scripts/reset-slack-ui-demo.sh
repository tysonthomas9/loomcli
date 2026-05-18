#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FIXTURE_DIR="${ROOT_DIR}/scripts/fixtures/slack-src"

CONTAINER="${CONTAINER:-loom-slack-epic}"
IMAGE="${IMAGE:-loomcli-dev-slack-epic}"
HOST_PORT="${HOST_PORT:-8092}"
BASE_URL="${BASE_URL:-http://localhost:${HOST_PORT}}"
WORKSPACE_NAME="${WORKSPACE_NAME:-Slack_UI}"
WORKSPACE_ID="${WORKSPACE_ID:-SLACK-UI}"
FLEET_DB_BIN="${FLEET_DB_BIN:-${ROOT_DIR}/tmp/podman/fleet-db-linux}"
DEFAULT_BACKEND="${DEFAULT_BACKEND:-codex}"
CODEX_HOME="${CODEX_HOME:-${HOME}/.codex}"
CLAUDE_HOME="${CLAUDE_HOME:-${HOME}/.claude}"

require_file() {
  local path="$1"
  if [[ ! -e "$path" ]]; then
    echo "missing required path: $path" >&2
    exit 1
  fi
}

wait_for_health() {
  for _ in $(seq 1 60); do
    if curl -fsS "${BASE_URL}/api/health" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo "server did not become healthy at ${BASE_URL}" >&2
  podman logs "$CONTAINER" >&2 || true
  exit 1
}

post_json() {
  local path="$1"
  local payload="$2"
  curl -fsS \
    -X POST "${BASE_URL}${path}" \
    -H "Content-Type: application/json" \
    --data-binary "$payload" >/dev/null
}

create_issue() {
  local title="$1"
  local issue_type="$2"
  local priority="$3"
  local parent="$4"
  local design="$5"
  local payload

  if [[ -n "$parent" ]]; then
    payload=$(printf '{"title":"%s","issue_type":"%s","priority":%s,"parent":"%s","design":"%s","source_repo":"slack-src"}' "$title" "$issue_type" "$priority" "$parent" "$design")
  else
    payload=$(printf '{"title":"%s","issue_type":"%s","priority":%s,"description":"%s","source_repo":"slack-src"}' "$title" "$issue_type" "$priority" "$design")
  fi
  post_json "/api/workspaces/${WORKSPACE_ID}/issues" "$payload"
}

require_file "$FIXTURE_DIR"
require_file "$FLEET_DB_BIN"
require_file "$CODEX_HOME"

echo "[slack-demo] Recreating container ${CONTAINER} from ${IMAGE}"
if ! podman image exists "$IMAGE"; then
  echo "missing image ${IMAGE}; build it with: podman build -t ${IMAGE} -f Dockerfile.dev ." >&2
  exit 1
fi
podman rm -f "$CONTAINER" >/dev/null 2>&1 || true
podman run -d --init \
  --name "$CONTAINER" \
  -p "${HOST_PORT}:3000" \
  -e "LOOM_FRONTEND_URL=${BASE_URL}" \
  -e "DEFAULT_BACKEND=${DEFAULT_BACKEND}" \
  -v "${FLEET_DB_BIN}:/usr/local/bin/fleet-db:ro" \
  -v "${CODEX_HOME}:/root/.codex:ro" \
  -v "${CLAUDE_HOME}:/root/.claude:ro" \
  "$IMAGE" >/dev/null

wait_for_health

echo "[slack-demo] Creating starter app repo in /tmp/slack-src"
podman exec "$CONTAINER" rm -rf /tmp/slack-src
podman exec "$CONTAINER" mkdir -p /tmp/slack-src
podman cp "${FIXTURE_DIR}/." "${CONTAINER}:/tmp/slack-src/"
podman exec "$CONTAINER" git -C /tmp/slack-src init -b main >/dev/null
podman exec "$CONTAINER" git -C /tmp/slack-src add .
podman exec "$CONTAINER" git -C /tmp/slack-src \
  -c user.name=Loom \
  -c user.email=loom@example.test \
  commit -m "seed slack app shell" >/dev/null

echo "[slack-demo] Registering workspace ${WORKSPACE_ID}"
post_json "/api/workspaces" "$(printf '{"name":"%s","type":"empty","repos":["/tmp/slack-src"]}' "$WORKSPACE_NAME")"

echo "[slack-demo] Creating lead agents"
post_json "/api/workspaces/${WORKSPACE_ID}/agents" "$(printf '{"workspace_key":"%s","name":"atlas","role_name":"lead","backend":"codex","desired_state":"stopped"}' "$WORKSPACE_ID")"
post_json "/api/workspaces/${WORKSPACE_ID}/agents" "$(printf '{"workspace_key":"%s","name":"nova","role_name":"lead","backend":"codex","desired_state":"stopped"}' "$WORKSPACE_ID")"

echo "[slack-demo] Creating epics and tasks sequentially"
create_issue "Slack collaboration shell" "epic" 1 "" "Build channel, conversation, and search workflows on top of the existing Slack app shell."
create_issue "Slack workspace foundation" "epic" 1 "" "Build workspace-level foundation features on top of the existing Slack app shell."

create_issue "Channel sidebar and unread counts" "task" 2 "SLACK-UI-1" "Extend the existing Slack app shell with richer channel sidebar states, unread badges, and active-channel behavior."
create_issue "Message thread and composer" "task" 2 "SLACK-UI-1" "Extend the existing Slack app shell with a stronger message thread, composer states, and message affordances."
create_issue "Search and command palette" "task" 2 "SLACK-UI-1" "Extend the existing Slack app shell with search and command palette interactions."

create_issue "Responsive layout and empty states" "task" 2 "SLACK-UI-2" "Extend the existing Slack app shell with polished responsive behavior and first-run empty states."
create_issue "Seed users channels and messages" "task" 2 "SLACK-UI-2" "Extend the existing Slack app seed data with additional users, channels, teams, and representative messages."
create_issue "Team workspace switcher" "task" 2 "SLACK-UI-2" "Extend the existing Slack app shell with a usable team workspace switcher."

echo "[slack-demo] Ready: ${BASE_URL}/ws/${WORKSPACE_ID}/agents/atlas"
echo "[slack-demo] Ready: ${BASE_URL}/ws/${WORKSPACE_ID}/agents/nova"
