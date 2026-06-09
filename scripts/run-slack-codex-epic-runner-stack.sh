#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FIXTURE_DIR="${FIXTURE_DIR:-${ROOT_DIR}/scripts/fixtures/slack-src}"
TASK_RUNNER="${TASK_RUNNER:-${ROOT_DIR}/scripts/slack-codex-task-runner.mjs}"

CONTAINER="${CONTAINER:-loom-slack-epic}"
IMAGE="${IMAGE:-loomcli-dev-slack-epic}"
HOST_PORT="${HOST_PORT:-8092}"
BASE_URL="${BASE_URL:-http://localhost:${HOST_PORT}}"
WORKSPACE_NAME="${WORKSPACE_NAME:-Slack_UI}"
WORKSPACE_ID="${WORKSPACE_ID:-SLACK-UI}"
DEFAULT_BACKEND="${DEFAULT_BACKEND:-codex}"
DRIVER_NODE_ID="${DRIVER_NODE_ID:-slack-codex-driver}"

FLEET_DB_REPO="${FLEET_DB_REPO:-${ROOT_DIR}/../fleet-db}"
FLEET_DB_BIN="${FLEET_DB_BIN:-${ROOT_DIR}/tmp/podman/fleet-db-linux}"
BUILD_FLEET_DB="${BUILD_FLEET_DB:-auto}"
BUILD_IMAGE="${BUILD_IMAGE:-auto}"

CODEX_HOME="${CODEX_HOME:-${HOME}/.codex}"
CLAUDE_HOME="${CLAUDE_HOME:-${HOME}/.claude}"
CONTAINER_REPO_PATH="${CONTAINER_REPO_PATH:-/tmp/slack-src}"
CONTAINER_CODEX_HOME="${CONTAINER_CODEX_HOME:-/root/.codex-rw}"
CONTAINER_LOOM_URL="${CONTAINER_LOOM_URL:-http://127.0.0.1:8080}"

log() {
  printf '[slack-codex-stack] %s\n' "$*"
}

require_cmd() {
  local name="$1"
  if ! command -v "$name" >/dev/null 2>&1; then
    printf 'missing required command: %s\n' "$name" >&2
    exit 1
  fi
}

require_path() {
  local path="$1"
  if [[ ! -e "$path" ]]; then
    printf 'missing required path: %s\n' "$path" >&2
    exit 1
  fi
}

truthy() {
  case "${1:-}" in
    1 | true | TRUE | yes | YES | on | ON) return 0 ;;
    *) return 1 ;;
  esac
}

json_array() {
  node -e 'console.log(JSON.stringify(process.argv.slice(1)))' "$@"
}

workspace_payload() {
  node -e 'console.log(JSON.stringify({name: process.argv[1], type: "empty", repos: [process.argv[2]]}))' \
    "$WORKSPACE_NAME" "$CONTAINER_REPO_PATH"
}

agent_payload() {
  local name="$1"
  node -e 'console.log(JSON.stringify({workspace_key: process.argv[1], name: process.argv[2], role_name: "lead", backend: process.argv[3], desired_state: "stopped"}))' \
    "$WORKSPACE_ID" "$name" "$DEFAULT_BACKEND"
}

issue_payload() {
  local title="$1"
  local issue_type="$2"
  local priority="$3"
  local parent="$4"
  local design="$5"
  if [[ -n "$parent" ]]; then
    node -e 'console.log(JSON.stringify({title: process.argv[1], issue_type: process.argv[2], priority: Number(process.argv[3]), parent: process.argv[4], design: process.argv[5], source_repo: "slack-src"}))' \
      "$title" "$issue_type" "$priority" "$parent" "$design"
  else
    node -e 'console.log(JSON.stringify({title: process.argv[1], issue_type: process.argv[2], priority: Number(process.argv[3]), description: process.argv[4], source_repo: "slack-src"}))' \
      "$title" "$issue_type" "$priority" "$design"
  fi
}

post_json() {
  local path="$1"
  local payload="$2"
  curl -fsS \
    -X POST "${BASE_URL}${path}" \
    -H "Content-Type: application/json" \
    --data-binary "$payload" >/dev/null
}

wait_for_health() {
  for _ in $(seq 1 90); do
    if curl -fsS "${BASE_URL}/api/health" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  printf 'server did not become healthy at %s\n' "$BASE_URL" >&2
  podman logs "$CONTAINER" >&2 || true
  exit 1
}

podman_goarch() {
  local arch
  arch="$(podman info --format '{{.Host.Arch}}')"
  case "$arch" in
    aarch64 | arm64) printf 'arm64\n' ;;
    x86_64 | amd64) printf 'amd64\n' ;;
    *)
      printf 'unsupported Podman VM architecture: %s\n' "$arch" >&2
      exit 1
      ;;
  esac
}

ensure_fleet_db() {
  local should_build=0
  if truthy "$BUILD_FLEET_DB"; then
    should_build=1
  elif [[ "$BUILD_FLEET_DB" == "auto" && ! -x "$FLEET_DB_BIN" ]]; then
    should_build=1
  fi

  if [[ "$should_build" -eq 0 ]]; then
    require_path "$FLEET_DB_BIN"
    return 0
  fi

  require_path "$FLEET_DB_REPO"
  mkdir -p "$(dirname "$FLEET_DB_BIN")"
  local goarch
  goarch="$(podman_goarch)"
  log "building fleet-db Linux/${goarch} sidecar at ${FLEET_DB_BIN}"
  (
    cd "$FLEET_DB_REPO"
    GOOS=linux GOARCH="$goarch" CGO_ENABLED=0 GOCACHE="${GOCACHE:-/tmp/go-build-cache}" \
      go build -o "$FLEET_DB_BIN" ./cmd/fleet-db
  )
}

ensure_image() {
  if truthy "$BUILD_IMAGE"; then
    log "building ${IMAGE} from Dockerfile.dev"
    podman build -f "${ROOT_DIR}/Dockerfile.dev" -t "$IMAGE" "$ROOT_DIR"
    return 0
  fi

  if podman image exists "$IMAGE"; then
    return 0
  fi

  if [[ "$BUILD_IMAGE" == "auto" ]]; then
    log "building missing ${IMAGE} from Dockerfile.dev"
    podman build -f "${ROOT_DIR}/Dockerfile.dev" -t "$IMAGE" "$ROOT_DIR"
    return 0
  fi

  printf 'missing image %s; set BUILD_IMAGE=auto or BUILD_IMAGE=1 to build it\n' "$IMAGE" >&2
  exit 1
}

seed_repo() {
  log "creating starter app repo in ${CONTAINER_REPO_PATH}"
  podman exec "$CONTAINER" rm -rf "$CONTAINER_REPO_PATH"
  podman exec "$CONTAINER" mkdir -p "$CONTAINER_REPO_PATH"
  podman cp "${FIXTURE_DIR}/." "${CONTAINER}:${CONTAINER_REPO_PATH}/"
  podman exec "$CONTAINER" git -C "$CONTAINER_REPO_PATH" init -b main >/dev/null
  podman exec "$CONTAINER" git -C "$CONTAINER_REPO_PATH" add .
  podman exec "$CONTAINER" git -C "$CONTAINER_REPO_PATH" \
    -c user.name=Loom \
    -c user.email=loom@example.test \
    commit -m "seed slack app shell" >/dev/null
}

seed_loom() {
  log "registering workspace ${WORKSPACE_ID}"
  post_json "/api/workspaces" "$(workspace_payload)"

  log "creating lead agents"
  post_json "/api/workspaces/${WORKSPACE_ID}/agents" "$(agent_payload atlas)"
  post_json "/api/workspaces/${WORKSPACE_ID}/agents" "$(agent_payload nova)"

  log "creating Slack clone epics and tasks"
  post_json "/api/workspaces/${WORKSPACE_ID}/issues" "$(issue_payload "Slack collaboration shell" "epic" 1 "" "Build channel, conversation, and search workflows on top of the existing Slack app shell.")"
  post_json "/api/workspaces/${WORKSPACE_ID}/issues" "$(issue_payload "Slack workspace foundation" "epic" 1 "" "Build workspace-level foundation features on top of the existing Slack app shell.")"

  post_json "/api/workspaces/${WORKSPACE_ID}/issues" "$(issue_payload "Channel sidebar and unread counts" "task" 2 "SLACK-UI-1" "Extend the existing Slack app shell with richer channel sidebar states, unread badges, and active-channel behavior.")"
  post_json "/api/workspaces/${WORKSPACE_ID}/issues" "$(issue_payload "Message thread and composer" "task" 2 "SLACK-UI-1" "Extend the existing Slack app shell with a stronger message thread, composer states, and message affordances.")"
  post_json "/api/workspaces/${WORKSPACE_ID}/issues" "$(issue_payload "Search and command palette" "task" 2 "SLACK-UI-1" "Extend the existing Slack app shell with search and command palette interactions.")"

  post_json "/api/workspaces/${WORKSPACE_ID}/issues" "$(issue_payload "Responsive layout and empty states" "task" 2 "SLACK-UI-2" "Extend the existing Slack app shell with polished responsive behavior and first-run empty states.")"
  post_json "/api/workspaces/${WORKSPACE_ID}/issues" "$(issue_payload "Seed users channels and messages" "task" 2 "SLACK-UI-2" "Extend the existing Slack app seed data with additional users, channels, teams, and representative messages.")"
  post_json "/api/workspaces/${WORKSPACE_ID}/issues" "$(issue_payload "Team workspace switcher" "task" 2 "SLACK-UI-2" "Extend the existing Slack app shell with a usable team workspace switcher.")"
}

main() {
  require_cmd curl
  require_cmd node
  require_cmd podman
  require_path "$FIXTURE_DIR"
  require_path "$TASK_RUNNER"
  require_path "$CODEX_HOME"

  ensure_fleet_db
  ensure_image

  local runner_cmd_json
  runner_cmd_json="$(json_array node /usr/local/bin/slack-codex-task-runner.mjs "$CONTAINER_REPO_PATH" "$CONTAINER_CODEX_HOME" "$CONTAINER_LOOM_URL")"

  log "recreating container ${CONTAINER} from ${IMAGE}"
  podman rm -f "$CONTAINER" >/dev/null 2>&1 || true

  local podman_args=(
    run -d --init
    --name "$CONTAINER"
    -p "${HOST_PORT}:3000"
    -e "LOOM_FRONTEND_URL=${BASE_URL}"
    -e "DEFAULT_BACKEND=${DEFAULT_BACKEND}"
    -e "LOOM_DRIVER_EXECUTOR=1"
    -e "LOOM_DRIVER_EXECUTOR_NODE_ID=${DRIVER_NODE_ID}"
    -e "LOOM_DRIVER_TASK_RUNNER_CMD_JSON=${runner_cmd_json}"
    -v "${FLEET_DB_BIN}:/usr/local/bin/fleet-db:ro"
    -v "${TASK_RUNNER}:/usr/local/bin/slack-codex-task-runner.mjs:ro"
    -v "${CODEX_HOME}:/root/.codex:ro"
  )

  if [[ -d "$CLAUDE_HOME" ]]; then
    podman_args+=(-v "${CLAUDE_HOME}:/root/.claude:ro")
  fi

  podman_args+=("$IMAGE")
  podman "${podman_args[@]}" >/dev/null

  wait_for_health
  seed_repo
  seed_loom

  log "ready"
  printf '%s\n' "UI:     ${BASE_URL}/ws/${WORKSPACE_ID}/agents/atlas"
  printf '%s\n' "UI:     ${BASE_URL}/ws/${WORKSPACE_ID}/agents/nova"
  printf '%s\n' "API:    ${BASE_URL}/api/workspaces/${WORKSPACE_ID}/issues"
  printf '%s\n' "Logs:   podman logs -f ${CONTAINER}"
  printf '%s\n' "Stop:   podman rm -f ${CONTAINER}"
}

main "$@"
