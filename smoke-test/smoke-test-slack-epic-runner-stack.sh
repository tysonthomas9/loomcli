#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FIXTURE_DIR="${FIXTURE_DIR:-${ROOT_DIR}/scripts/fixtures/slack-src}"
TASK_RUNNER="${TASK_RUNNER:-${ROOT_DIR}/scripts/loom-task-runner-invoker.mjs}"
EPIC_RUNNER_SOURCE="${EPIC_RUNNER_SOURCE:-${ROOT_DIR}/internal/workflows/builtin/epic-runner.ts}"
LOCAL_TASK_RUNNER_SOURCE="${LOCAL_TASK_RUNNER_SOURCE:-${ROOT_DIR}/internal/workflows/builtin/local-task-runner.ts}"
DAYTONA_TASK_RUNNER_SOURCE="${DAYTONA_TASK_RUNNER_SOURCE:-${ROOT_DIR}/internal/workflows/builtin/daytona-task-runner.ts}"
OPENSHELL_TASK_RUNNER_SOURCE="${OPENSHELL_TASK_RUNNER_SOURCE:-${ROOT_DIR}/internal/workflows/builtin/openshell-task-runner.ts}"
LOOM_SDK_DIR="${LOOM_SDK_DIR:-${ROOT_DIR}/sdk}"
FLUE_REPO="${FLUE_REPO:-${ROOT_DIR}/../flue}"
FLUE_NODE_MODULES_DIR="${FLUE_NODE_MODULES_DIR:-${FLUE_REPO}/node_modules}"
FLUE_RUNTIME_DIR="${FLUE_RUNTIME_DIR:-${FLUE_REPO}/packages/runtime}"
CONTAINER_FLUE_REPO="${CONTAINER_FLUE_REPO:-/opt/flue-workspace}"

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
# Standalone fleet-db (Phase 2.5): when STANDALONE_FLEET_DB=1, serve runs in CLOUD mode against an
# EXTERNAL fleet-db (started separately by the test harness, e.g. aether-test-framework) instead of
# auto-spawning the embedded one. The harness sets LOOM_FLEET_DB_URL and joins PODMAN_NETWORK so both
# serve AND the in-container `loom` execs (which inherit run-time env) reach the external fleet-db.
# Seeding/registration still flow through serve + the loom CLI, so the rest of this script is unchanged.
STANDALONE_FLEET_DB="${STANDALONE_FLEET_DB:-0}"
PODMAN_NETWORK="${PODMAN_NETWORK:-}"
LOOM_FLEET_DB_URL="${LOOM_FLEET_DB_URL:-}"
LOOM_FLEET_DB_API_KEY="${LOOM_FLEET_DB_API_KEY:-}"
BUILD_IMAGE="${BUILD_IMAGE:-auto}"

CODEX_HOME="${CODEX_HOME:-${HOME}/.codex}"
CONTAINER_CODEX_AUTH_FILE="${CONTAINER_CODEX_AUTH_FILE:-/root/.codex/auth.json}"
CLAUDE_HOME="${CLAUDE_HOME:-${HOME}/.claude}"
# opencode keeps its own auth store (~/.local/share/opencode/auth.json), separate
# from ~/.codex; without it the container CLI falls back to opencode's free hosted
# model. Copy the host auth in and pin the model so local-task-runner opencode runs
# use a real provider (e.g. OpenAI gpt-5.4-mini-fast) instead of the free default.
OPENCODE_AUTH_FILE_HOST="${OPENCODE_AUTH_FILE_HOST:-${HOME}/.local/share/opencode/auth.json}"
CONTAINER_OPENCODE_AUTH_FILE="${CONTAINER_OPENCODE_AUTH_FILE:-/root/.local/share/opencode/auth.json}"
LOOM_OPENCODE_MODEL="${LOOM_OPENCODE_MODEL:-openai/gpt-5.4-mini-fast}"
# cursor-agent: the host binary is platform-specific (a macOS wrapper over native
# components) and its login lives in the OS keychain, so neither is mountable into
# the Linux container. Instead install the Linux cursor-agent CLI on demand and
# authenticate with CURSOR_API_KEY. Set CURSOR_API_KEY (a cursor.com key) to enable
# the cursor backend; leave empty to skip cursor entirely.
CURSOR_API_KEY="${CURSOR_API_KEY:-}"
CURSOR_INSTALL_URL="${CURSOR_INSTALL_URL:-https://cursor.com/install}"
CONTAINER_REPO_PATH="${CONTAINER_REPO_PATH:-/tmp/slack-src}"
CONTAINER_EPIC_RUNNER_DIST="${CONTAINER_EPIC_RUNNER_DIST:-/tmp/epic-runner-dist}"
CONTAINER_DRIVER_WORKDIR="${CONTAINER_DRIVER_WORKDIR:-/root/.loom/workspaces/alpha}"
CONTAINER_CODEX_HOME="${CONTAINER_CODEX_HOME:-/root/.codex-rw}"
CONTAINER_LOOM_URL="${CONTAINER_LOOM_URL:-http://127.0.0.1:8080}"
REGISTER_EPIC_RUNNER="${REGISTER_EPIC_RUNNER:-1}"
LOOM_FLUE_AGENT_MODEL="${LOOM_FLUE_AGENT_MODEL:-openai-codex/gpt-5.3-codex-spark}"
RUN_DAYTONA="${RUN_DAYTONA:-0}"
DAYTONA_REPO_URL="${DAYTONA_REPO_URL:-https://github.com/tysonthomas9/webhook-e2e-sandbox.git}"
DAYTONA_TASK_MODE="${DAYTONA_TASK_MODE:-e2e-smoke}"
# e2e-smoke and slack-pr-chain are gated DEMO_MODES in daytona-task-runner.ts;
# this stack exists to run them, so enable them by default (override with 0).
LOOM_DAYTONA_TASK_RUNNER_ENABLE_DEMO_MODES="${LOOM_DAYTONA_TASK_RUNNER_ENABLE_DEMO_MODES:-1}"
# Real mode (faithful E2E): an empty DAYTONA_TASK_MODE makes the daytona runner use a genuine
# implementation prompt instead of a fabricated DEMO_MODE. The `:-e2e-smoke` default above treats
# an empty string as unset, so the only way to request real mode is this explicit toggle. When set,
# force the task mode empty and keep demo modes disabled (real mode must not ride the demo flag).
DAYTONA_REAL_MODE="${DAYTONA_REAL_MODE:-0}"
case "$DAYTONA_REAL_MODE" in
  1 | true | TRUE | yes | YES | on | ON)
    DAYTONA_TASK_MODE=""
    LOOM_DAYTONA_TASK_RUNNER_ENABLE_DEMO_MODES=0
    ;;
esac
DAYTONA_CREDENTIAL_FILE_HOST="${DAYTONA_CREDENTIAL_FILE_HOST:-}"
CONTAINER_DAYTONA_SECRET_DIR="${CONTAINER_DAYTONA_SECRET_DIR:-/run/loom-secrets}"
CONTAINER_DAYTONA_CREDENTIAL_FILE="${CONTAINER_DAYTONA_CREDENTIAL_FILE:-${CONTAINER_DAYTONA_SECRET_DIR}/daytona_api_key}"
GITHUB_CREDENTIAL_FILE_HOST="${GITHUB_CREDENTIAL_FILE_HOST:-}"
CONTAINER_GITHUB_CREDENTIAL_FILE="${CONTAINER_GITHUB_CREDENTIAL_FILE:-${CONTAINER_DAYTONA_SECRET_DIR}/github_token}"
DAYTONA_BASE_BRANCH="${DAYTONA_BASE_BRANCH:-}"
DAYTONA_PR_BRANCH_PREFIX="${DAYTONA_PR_BRANCH_PREFIX:-}"
DAYTONA_PR_STACKED="${DAYTONA_PR_STACKED:-1}"
DAYTONA_SEED_PR_CHAIN="${DAYTONA_SEED_PR_CHAIN:-${RUN_STACKED_PR:-0}}"
DAYTONA_PR_DRAFT="${DAYTONA_PR_DRAFT:-1}"
DAYTONA_GIT_AUTHOR_NAME="${DAYTONA_GIT_AUTHOR_NAME:-Loom Daytona Runner}"
DAYTONA_GIT_AUTHOR_EMAIL="${DAYTONA_GIT_AUTHOR_EMAIL:-loom-daytona@example.test}"
FLUE_RUNTIME_IMPORT="${FLUE_RUNTIME_IMPORT:-file://${CONTAINER_FLUE_REPO}/packages/runtime/dist/index.mjs}"
FLUE_RUNTIME_INTERNAL_IMPORT="${FLUE_RUNTIME_INTERNAL_IMPORT:-file://${CONTAINER_FLUE_REPO}/packages/runtime/dist/internal.mjs}"
DAYTONA_SDK_IMPORT="${DAYTONA_SDK_IMPORT:-file://${CONTAINER_FLUE_REPO}/node_modules/.pnpm/node_modules/@daytona/sdk/esm/index.js}"
DAYTONA_SECRET_TMP=""
GITHUB_SECRET_TMP=""

cleanup_host_secret() {
  if [[ -n "$DAYTONA_SECRET_TMP" ]]; then
    rm -f "$DAYTONA_SECRET_TMP"
  fi
  if [[ -n "$GITHUB_SECRET_TMP" ]]; then
    rm -f "$GITHUB_SECRET_TMP"
  fi
}
trap cleanup_host_secret EXIT

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

resolve_daytona_secret() {
  if [[ -n "$DAYTONA_CREDENTIAL_FILE_HOST" ]]; then
    require_path "$DAYTONA_CREDENTIAL_FILE_HOST"
    return 0
  fi
  if [[ -n "${DAYTONA_API_KEY:-}" ]]; then
    DAYTONA_SECRET_TMP="$(mktemp "${TMPDIR:-/tmp}/loom-daytona-api-key.XXXXXX")"
    chmod 600 "$DAYTONA_SECRET_TMP"
    printf '%s' "$DAYTONA_API_KEY" >"$DAYTONA_SECRET_TMP"
    DAYTONA_CREDENTIAL_FILE_HOST="$DAYTONA_SECRET_TMP"
    return 0
  fi
  if truthy "$RUN_DAYTONA"; then
    printf 'RUN_DAYTONA=1 requires DAYTONA_API_KEY or DAYTONA_CREDENTIAL_FILE_HOST\n' >&2
    exit 1
  fi
}

requires_github_pr_runtime() {
  [[ "$DAYTONA_TASK_MODE" == "slack-pr-chain" ]] || truthy "${DAYTONA_OPEN_PR:-0}"
}

resolve_github_secret() {
  if [[ -n "$GITHUB_CREDENTIAL_FILE_HOST" ]]; then
    require_path "$GITHUB_CREDENTIAL_FILE_HOST"
    return 0
  fi

  local token=""
  if [[ -n "${GH_TOKEN:-}" ]]; then
    token="$GH_TOKEN"
  elif [[ -n "${GITHUB_TOKEN:-}" ]]; then
    token="$GITHUB_TOKEN"
  elif command -v gh >/dev/null 2>&1; then
    log "reading GitHub credential from 'gh auth token'"
    token="$(gh auth token 2>/dev/null || true)"
  fi

  if [[ -n "$token" ]]; then
    GITHUB_SECRET_TMP="$(mktemp "${TMPDIR:-/tmp}/loom-github-token.XXXXXX")"
    chmod 600 "$GITHUB_SECRET_TMP"
    printf '%s' "$token" >"$GITHUB_SECRET_TMP"
    GITHUB_CREDENTIAL_FILE_HOST="$GITHUB_SECRET_TMP"
    return 0
  fi

  if requires_github_pr_runtime; then
    printf 'DAYTONA_TASK_MODE=slack-pr-chain requires GH_TOKEN, GITHUB_TOKEN, GITHUB_CREDENTIAL_FILE_HOST, or a working `gh auth token`\n' >&2
    exit 1
  fi
}

install_daytona_secret() {
  if [[ -z "$DAYTONA_CREDENTIAL_FILE_HOST" ]]; then
    return 0
  fi
  log "installing Daytona credential file inside ${CONTAINER} (path only; value is not logged)"
  podman exec "$CONTAINER" mkdir -p "$CONTAINER_DAYTONA_SECRET_DIR"
  podman cp "$DAYTONA_CREDENTIAL_FILE_HOST" "${CONTAINER}:${CONTAINER_DAYTONA_CREDENTIAL_FILE}"
  podman exec "$CONTAINER" chmod 0400 "$CONTAINER_DAYTONA_CREDENTIAL_FILE"
}

install_github_secret() {
  if [[ -z "$GITHUB_CREDENTIAL_FILE_HOST" ]]; then
    return 0
  fi
  log "installing GitHub credential file inside ${CONTAINER} (path only; value is not logged)"
  podman exec "$CONTAINER" mkdir -p "$CONTAINER_DAYTONA_SECRET_DIR"
  podman cp "$GITHUB_CREDENTIAL_FILE_HOST" "${CONTAINER}:${CONTAINER_GITHUB_CREDENTIAL_FILE}"
  podman exec "$CONTAINER" chmod 0400 "$CONTAINER_GITHUB_CREDENTIAL_FILE"
}

install_opencode_auth() {
  if [[ ! -f "$OPENCODE_AUTH_FILE_HOST" ]]; then
    log "no opencode auth at ${OPENCODE_AUTH_FILE_HOST}; container opencode will use its free hosted model"
    return 0
  fi
  log "installing opencode auth into ${CONTAINER} (path only; value is not logged)"
  podman exec "$CONTAINER" mkdir -p "$(dirname "$CONTAINER_OPENCODE_AUTH_FILE")"
  # Copy (not RO-mount) so opencode can refresh/rewrite its own token in-container
  # without touching the host file.
  podman cp "$OPENCODE_AUTH_FILE_HOST" "${CONTAINER}:${CONTAINER_OPENCODE_AUTH_FILE}"
  podman exec "$CONTAINER" chmod 0600 "$CONTAINER_OPENCODE_AUTH_FILE"
}

install_cursor_agent() {
  if [[ -z "$CURSOR_API_KEY" ]]; then
    return 0
  fi
  log "installing cursor-agent (Linux) into ${CONTAINER}"
  podman exec "$CONTAINER" sh -lc '
    set -e
    if ! command -v cursor-agent >/dev/null 2>&1 && [ ! -x /root/.local/bin/cursor-agent ]; then
      curl -fsS '"$CURSOR_INSTALL_URL"' | bash
    fi
    # Symlink onto the default PATH so the task-runner can exec it without a PATH override.
    [ -x /root/.local/bin/cursor-agent ] && ln -sfn /root/.local/bin/cursor-agent /usr/local/bin/cursor-agent
    cursor-agent --version 2>/dev/null | head -1 || true
  '
}

require_daytona_runtime() {
  if ! truthy "$RUN_DAYTONA"; then
    return 0
  fi
  require_path "${CODEX_HOME}/auth.json"
  require_path "${FLUE_REPO}/packages/runtime/dist/index.mjs"
  require_path "${FLUE_REPO}/packages/runtime/dist/internal.mjs"
  require_path "${FLUE_REPO}/node_modules/.pnpm/node_modules/@daytona/sdk/esm/index.js"
  resolve_daytona_secret
  resolve_github_secret
}

workspace_payload() {
  # WORKSPACE_BRANCH sets the workspace repo's DefaultBranch. When empty, serve defaults it to the
  # workspace NAME (workspace_store.go:72-74) — which then leaks into the webui daytona payload's
  # baseBranch and makes the remote clone fail ("Remote branch <name> not found"). Set it to the
  # GitHub repo's default branch (e.g. main) for the daytona-via-UI flow.
  node -e 'const o={name: process.argv[1], type: "empty", repos: [process.argv[2]]}; if(process.argv[3]) o.branch=process.argv[3]; console.log(JSON.stringify(o))' \
    "$WORKSPACE_NAME" "$CONTAINER_REPO_PATH" "${WORKSPACE_BRANCH:-}"
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

# The loomcli source rev this build captures, scoped to the image-relevant paths (cmd, internal,
# go.mod, go.sum, Dockerfile.dev) and suffixed -dirty when they carry uncommitted changes. Stamped
# as the image label `loom.source.rev` so callers (e.g. aether's loomImageNeedsRebuild) can detect
# a stale image instead of rebuilding every run or silently reusing an old binary/frontend.
loom_source_rev() {
  local rev
  rev="$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || true)"
  if [[ -z "$rev" ]]; then
    printf 'unknown'
    return 0
  fi
  if [[ -n "$(git -C "$ROOT_DIR" status --porcelain -- cmd internal go.mod go.sum Dockerfile.dev 2>/dev/null)" ]]; then
    printf '%s-dirty' "$rev"
  else
    printf '%s' "$rev"
  fi
}

ensure_image() {
  if truthy "$BUILD_IMAGE"; then
    log "building ${IMAGE} from Dockerfile.dev"
    podman build -f "${ROOT_DIR}/Dockerfile.dev" --label "loom.source.rev=$(loom_source_rev)" -t "$IMAGE" "$ROOT_DIR"
    return 0
  fi

  if podman image exists "$IMAGE"; then
    return 0
  fi

  if [[ "$BUILD_IMAGE" == "auto" ]]; then
    log "building missing ${IMAGE} from Dockerfile.dev"
    podman build -f "${ROOT_DIR}/Dockerfile.dev" --label "loom.source.rev=$(loom_source_rev)" -t "$IMAGE" "$ROOT_DIR"
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
  # The workspace create does `git worktree add -b <WORKSPACE_BRANCH>` from THIS repo, which fails if
  # that branch already exists here. So when WORKSPACE_BRANCH is set (daytona-via-UI, to match the
  # GitHub remote's default branch), seed on a NON-conflicting branch and let the workspace create the
  # target branch fresh. Default unchanged (main) otherwise.
  local seed_branch="main"
  [[ -n "${WORKSPACE_BRANCH:-}" && "${WORKSPACE_BRANCH}" == "main" ]] && seed_branch="loom-seed-base"
  podman exec "$CONTAINER" git -C "$CONTAINER_REPO_PATH" init -b "$seed_branch" >/dev/null
  podman exec "$CONTAINER" git -C "$CONTAINER_REPO_PATH" add .
  podman exec "$CONTAINER" git -C "$CONTAINER_REPO_PATH" \
    -c user.name=Loom \
    -c user.email=loom@example.test \
    commit -m "seed slack app shell" >/dev/null
  # Optionally give the workspace repo a GitHub origin so serve reports a remote_url
  # (gitRemoteURL → workspace_store.go). The webui's daytona epic-run payload derives its
  # repoUrl from the issue's source_repo → this remote; without it, daytona-via-UI cannot
  # resolve a repo and fails closed. Harmless for local backends (they use the worktree).
  if [[ -n "${SEED_REPO_REMOTE_URL:-}" ]]; then
    podman exec "$CONTAINER" git -C "$CONTAINER_REPO_PATH" remote add origin "$SEED_REPO_REMOTE_URL" >/dev/null 2>&1 \
      || podman exec "$CONTAINER" git -C "$CONTAINER_REPO_PATH" remote set-url origin "$SEED_REPO_REMOTE_URL"
    log "workspace repo origin set to ${SEED_REPO_REMOTE_URL}"
  fi
}

seed_loom_default() {
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

dependency_payload() {
  local depends_on="$1"
  node -e 'console.log(JSON.stringify({depends_on_id: process.argv[1], dep_type: "blocks"}))' "$depends_on"
}

add_dependency() {
  local issue="$1"
  local depends_on="$2"
  post_json "/api/workspaces/${WORKSPACE_ID}/issues/${issue}/dependencies" "$(dependency_payload "$depends_on")"
}

seed_loom_pr_chain() {
  log "registering workspace ${WORKSPACE_ID}"
  post_json "/api/workspaces" "$(workspace_payload)"

  log "creating lead agents"
  post_json "/api/workspaces/${WORKSPACE_ID}/agents" "$(agent_payload atlas)"
  post_json "/api/workspaces/${WORKSPACE_ID}/agents" "$(agent_payload nova)"

  log "creating realistic Slack clone epic with a serial dependency chain"
  post_json "/api/workspaces/${WORKSPACE_ID}/issues" "$(issue_payload "Tiny Slack clone PR chain" "epic" 1 "" "Build a tiny Slack-style collaboration app through three dependent task PRs. Each task should produce one visible GitHub pull request.")"
  post_json "/api/workspaces/${WORKSPACE_ID}/issues" "$(issue_payload "Scaffold Slack clone app" "task" 1 "${WORKSPACE_ID}-1" "Create a tiny static Slack-style app under a clear project directory. Include package.json, an HTML entrypoint, CSS, JavaScript state/rendering code, sample channel/message data, and a Node built-in test that validates the rendered data model. Keep dependencies minimal and make npm test pass.")"
  post_json "/api/workspaces/${WORKSPACE_ID}/issues" "$(issue_payload "Add channel navigation and unread state" "task" 1 "${WORKSPACE_ID}-1" "Extend the Slack clone from the prior PR with multiple channels, active-channel switching affordances, unread badges, timestamps, and stronger sidebar styling. Update or add tests so npm test proves the channel data/render helpers still work.")"
  post_json "/api/workspaces/${WORKSPACE_ID}/issues" "$(issue_payload "Add composer search and polish" "task" 1 "${WORKSPACE_ID}-1" "Extend the stacked Slack clone with a message composer, search or command palette behavior, responsive layout polish, and final seed data. Update tests and keep npm test passing.")"

  add_dependency "${WORKSPACE_ID}-3" "${WORKSPACE_ID}-2"
  add_dependency "${WORKSPACE_ID}-4" "${WORKSPACE_ID}-3"
}

seed_loom() {
  if truthy "$DAYTONA_SEED_PR_CHAIN" || [[ "$DAYTONA_TASK_MODE" == "slack-pr-chain" ]]; then
    seed_loom_pr_chain
    return
  fi
  seed_loom_default
}

source_digest() {
  node -e '
const crypto = require("node:crypto");
const fs = require("node:fs");
const hash = crypto.createHash("sha256");
for (const pair of process.argv.slice(1)) {
  const [name, file] = pair.split("=", 2);
  hash.update(name);
  hash.update(Buffer.from([0]));
  hash.update(fs.readFileSync(file));
  hash.update(Buffer.from([0]));
}
console.log("sha256:" + hash.digest("hex"));
' \
    "workflows/epic-runner.ts=${EPIC_RUNNER_SOURCE}" \
    "workflows/local-task-runner.ts=${LOCAL_TASK_RUNNER_SOURCE}" \
    "workflows/daytona-task-runner.ts=${DAYTONA_TASK_RUNNER_SOURCE}" \
    "workflows/openshell-task-runner.ts=${OPENSHELL_TASK_RUNNER_SOURCE}" \
    "sdk/runtime-adapters.js=${LOOM_SDK_DIR}/runtime-adapters.js"
}

write_epic_runner_dist() {
  local dist="$1"
  mkdir -p "${dist}/node_modules/@loom/sdk" "${dist}/workflows"
  cp "${LOOM_SDK_DIR}/package.json" "${dist}/node_modules/@loom/sdk/package.json"
  cp "${LOOM_SDK_DIR}/driver.js" "${dist}/node_modules/@loom/sdk/driver.js"
  cp "${LOOM_SDK_DIR}/runner.js" "${dist}/node_modules/@loom/sdk/runner.js"
  cp "${LOOM_SDK_DIR}/runtime-adapters.js" "${dist}/node_modules/@loom/sdk/runtime-adapters.js"
  cp "${LOOM_SDK_DIR}/internal.js" "${dist}/node_modules/@loom/sdk/internal.js"
  cp "$EPIC_RUNNER_SOURCE" "${dist}/workflows/epic-runner.mjs"
  cp "$LOCAL_TASK_RUNNER_SOURCE" "${dist}/workflows/local-task-runner.mjs"
  cp "$DAYTONA_TASK_RUNNER_SOURCE" "${dist}/workflows/daytona-task-runner.mjs"
  cp "$OPENSHELL_TASK_RUNNER_SOURCE" "${dist}/workflows/openshell-task-runner.mjs"
  node -e '
const fs = require("node:fs");
const dist = process.argv[1];
fs.writeFileSync(dist + "/loom-driver.json", JSON.stringify({
  runners: JSON.stringify([
    { name: "daytona-task-runner", kind: "flue-workflow", entrypoint: "daytona-task-runner" },
    { name: "local-task-runner", kind: "flue-workflow", entrypoint: "local-task-runner" }
  ])
}, null, 2) + "\n");
' "$dist"
  cat > "${dist}/server.mjs" <<'EOF'
const workflowLoaders = {
  "epic-runner": () => import("./workflows/epic-runner.mjs"),
  "local-task-runner": () => import("./workflows/local-task-runner.mjs"),
  "daytona-task-runner": () => import("./workflows/daytona-task-runner.mjs"),
  "openshell-task-runner": () => import("./workflows/openshell-task-runner.mjs"),
};

let completed = false;

function send(message) {
  if (typeof process.send === "function") {
    process.send({ version: 1, ...message });
  } else {
    console.log(JSON.stringify(message.result || message.error || message));
  }
}

function normalizeResult(result) {
  if (!result || typeof result !== "object") {
    return { status: "completed", summary: "completed" };
  }
  return result;
}

async function invoke(message) {
  if (completed) return;
  completed = true;
  try {
    const workflowName = process.env.FLUE_CLI_NAME || "epic-runner";
    const loader = workflowLoaders[workflowName];
    if (!loader) {
      throw new Error("unknown workflow " + JSON.stringify(workflowName));
    }
    const mod = await loader();
    if (!mod || typeof mod.run !== "function") {
      throw new Error("workflow " + JSON.stringify(workflowName) + " does not export run()");
    }
    const result = await mod.run({
      payload: message.payload || {},
      requestId: message.requestId,
      env: process.env,
    });
    send({ type: "result", requestId: message.requestId, result: normalizeResult(result) });
  } catch (error) {
    send({
      type: "error",
      requestId: message.requestId,
      error: {
        type: "driver_runtime",
        message: error && error.message ? error.message : String(error),
        details: error && error.stack ? error.stack : "",
      },
    });
  }
}

process.on("message", (message) => {
  if (!message || message.type !== "invoke") return;
  void invoke(message);
});

send({ type: "ready" });
EOF
}

seed_local_worktree_base() {
  # The local-task-runner derives each backend's isolated worktree from the driver
  # workspace dir (serve's CWD), which loom initializes as an EMPTY git repo. Seed
  # it with the Slack app so local backends (codex/claude/opencode/cursor) have real
  # code to edit; without this they run against an empty tree and produce no diff.
  # (The Daytona runner is unaffected — it clones DAYTONA_REPO_URL into its sandbox.)
  #
  # NOTE: this seeding papers over the per-workspace-worktree-provisioning gap
  # (loomcli FINDINGS #3). Set SEED_LOCAL_WORKTREE_BASE=0 for a deployment-faithful
  # bring-up that leaves the gap visible — aether-test-framework relies on this to
  # observe the gap as an xfail rather than hiding it.
  if ! truthy "${SEED_LOCAL_WORKTREE_BASE:-1}"; then
    log "skipping local-runner worktree seeding (SEED_LOCAL_WORKTREE_BASE=0; faithful bring-up)"
    return 0
  fi
  log "seeding local-task-runner worktree base (${CONTAINER_DRIVER_WORKDIR}) with the Slack app"
  podman exec \
    -e SRC="$CONTAINER_REPO_PATH" \
    -e DST="$CONTAINER_DRIVER_WORKDIR" \
    "$CONTAINER" sh -lc '
      set -e
      cd "$DST"
      for p in README.md index.html package.json scripts src test; do
        [ -e "$SRC/$p" ] && cp -r "$SRC/$p" ./
      done
      printf ".loom/\nsessions/\nnotify.token\n" > .gitignore
      git add -A
      if ! git diff --cached --quiet; then
        git -c user.name=Loom -c user.email=loom@example.test \
          commit -m "seed slack app into local-runner worktree base" >/dev/null
      fi
    '
}

stage_daytona_bundle_deps() {
  # daytona-task-runner.ts has static top-level imports of @daytona/sdk and
  # @flue/runtime (the bundled fallbacks). The canonical esbuild bundle inlines
  # them (see scripts/rebuild-builtin-bundle.sh), but write_epic_runner_dist
  # ships a raw .mjs copy whose node_modules only carries @loom/sdk, so the
  # static imports fail to resolve at module load. Expose the mounted flue repo's
  # packages on the runner's upward node_modules resolution path (/root) so the
  # static imports resolve; the runtime still loads the real code via the
  # DAYTONA_SDK_IMPORT / FLUE_RUNTIME_IMPORT dynamic imports.
  if [[ ! -d "$FLUE_REPO" ]]; then
    return 0
  fi
  log "staging bundle-only deps (@daytona/sdk, @flue/runtime) on the runner resolution path"
  podman exec "$CONTAINER" sh -lc '
    set -e
    mkdir -p /root/node_modules/@daytona /root/node_modules/@flue
    ln -sfn /opt/flue-workspace/node_modules/.pnpm/node_modules/@daytona/sdk /root/node_modules/@daytona/sdk
    ln -sfn /opt/flue-workspace/packages/runtime /root/node_modules/@flue/runtime
  '
}

register_epic_runner_workflow() {
  if ! truthy "$REGISTER_EPIC_RUNNER"; then
    return 0
  fi
  require_path "$EPIC_RUNNER_SOURCE"
  require_path "${LOOM_SDK_DIR}/package.json"
  require_path "${LOOM_SDK_DIR}/driver.js"

  local build_dir digest
  build_dir="$(mktemp -d "${TMPDIR:-/tmp}/loom-epic-runner-dist.XXXXXX")"
  write_epic_runner_dist "$build_dir"
  digest="$(source_digest)"

  log "registering builtin epic-runner workflow"
  podman exec "$CONTAINER" rm -rf "$CONTAINER_EPIC_RUNNER_DIST"
  podman exec "$CONTAINER" mkdir -p "$CONTAINER_EPIC_RUNNER_DIST"
  podman cp "${build_dir}/." "${CONTAINER}:${CONTAINER_EPIC_RUNNER_DIST}/"
  podman exec \
    -w "$CONTAINER_DRIVER_WORKDIR" \
    -e LOOM_CONFIG_DIR=/root/.loom-config \
    -e LOOM_WORKSPACE="$WORKSPACE_ID" \
    -e LOOM_FLEET_DB_ACTOR=loom-dev \
    "$CONTAINER" \
    loom driver register \
      --flue-dist "$CONTAINER_EPIC_RUNNER_DIST" \
      --name epic-runner \
      --id epic-runner \
      --workflow epic-runner \
      --source-ref "builtin://workflows/epic-runner/versions/${digest}" \
      --source-digest "$digest" \
      --trusted \
      --activate \
      --json >/dev/null
  rm -rf "$build_dir"
}

main() {
  require_cmd curl
  require_cmd node
  require_cmd podman
  require_path "$FIXTURE_DIR"
  require_path "$TASK_RUNNER"
  require_path "$EPIC_RUNNER_SOURCE"
  require_path "$LOCAL_TASK_RUNNER_SOURCE"
  require_path "$DAYTONA_TASK_RUNNER_SOURCE"
  require_path "$OPENSHELL_TASK_RUNNER_SOURCE"
  require_daytona_runtime

  ensure_fleet_db
  ensure_image

  local runner_env=(
    env
    "CODEX_HOME=${CONTAINER_CODEX_HOME}"
    "LOOM_CODEX_AUTH_FILE=${CONTAINER_CODEX_HOME}/auth.json"
    "DAYTONA_CREDENTIAL_FILE=${CONTAINER_DAYTONA_CREDENTIAL_FILE}"
    "GITHUB_TOKEN_FILE=${CONTAINER_GITHUB_CREDENTIAL_FILE}"
    "DAYTONA_REPO_URL=${DAYTONA_REPO_URL}"
    "DAYTONA_TASK_MODE=${DAYTONA_TASK_MODE}"
    "LOOM_DAYTONA_TASK_RUNNER_ENABLE_DEMO_MODES=${LOOM_DAYTONA_TASK_RUNNER_ENABLE_DEMO_MODES}"
    "DAYTONA_BASE_BRANCH=${DAYTONA_BASE_BRANCH}"
    "DAYTONA_PR_BRANCH_PREFIX=${DAYTONA_PR_BRANCH_PREFIX}"
    "DAYTONA_PR_STACKED=${DAYTONA_PR_STACKED}"
    "DAYTONA_PR_DRAFT=${DAYTONA_PR_DRAFT}"
    "DAYTONA_GIT_AUTHOR_NAME=${DAYTONA_GIT_AUTHOR_NAME}"
    "DAYTONA_GIT_AUTHOR_EMAIL=${DAYTONA_GIT_AUTHOR_EMAIL}"
    "DAYTONA_API_URL=${DAYTONA_API_URL:-}"
    "DAYTONA_TARGET=${DAYTONA_TARGET:-}"
    "KEEP_DAYTONA_SANDBOX=${KEEP_DAYTONA_SANDBOX:-}"
    "FLUE_RUNTIME_IMPORT=${FLUE_RUNTIME_IMPORT}"
    "FLUE_RUNTIME_INTERNAL_IMPORT=${FLUE_RUNTIME_INTERNAL_IMPORT}"
    "DAYTONA_SDK_IMPORT=${DAYTONA_SDK_IMPORT}"
    # local-task-runner opencode model pin (forwarded to `opencode run --model`).
    "LOOM_OPENCODE_MODEL=${LOOM_OPENCODE_MODEL}"
  )
  # cursor-agent reads CURSOR_API_KEY from its environment (no file/keychain path
  # works in-container). Inject it only when provided. NOTE: this places the key in
  # LOOM_DRIVER_TASK_RUNNER_CMD_JSON (visible via `podman inspect`) — acceptable for
  # a local e2e stack; a production deploy should source it from a mounted secret.
  # Use the `:-` default so an unset key is empty (not an `unbound variable` error
  # under `set -u`) — these credentials are optional and per-backend.
  if [[ -n "${CURSOR_API_KEY:-}" ]]; then
    runner_env+=("CURSOR_API_KEY=${CURSOR_API_KEY}")
  fi
  # claude-code reads CLAUDE_CODE_OAUTH_TOKEN from its environment (a `claude setup-token`
  # long-lived OAuth token). Inject it when provided so claude auth works without a
  # refreshable ~/.claude/.credentials.json login token (mirrors CURSOR_API_KEY above).
  if [[ -n "${CLAUDE_CODE_OAUTH_TOKEN:-}" ]]; then
    runner_env+=("CLAUDE_CODE_OAUTH_TOKEN=${CLAUDE_CODE_OAUTH_TOKEN}")
  fi
  runner_env+=(node /usr/local/bin/loom-task-runner-invoker.mjs)

  local runner_cmd_json
  runner_cmd_json="$(json_array "${runner_env[@]}")"

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
    -e "LOOM_CODEX_AUTH_FILE=${CONTAINER_CODEX_AUTH_FILE}"
    -e "LOOM_FLUE_AGENT_MODEL=${LOOM_FLUE_AGENT_MODEL}"
    -v "${FLEET_DB_BIN}:/usr/local/bin/fleet-db:ro"
    -v "${TASK_RUNNER}:/usr/local/bin/loom-task-runner-invoker.mjs:ro"
  )

  # Lead/terminal capabilities launch `loom --backend <X> lead` INSIDE serve, which reads serve's
  # process env (via cli.FilteredEnv's allowlist) — NOT the task-runner runner_env. Forward the
  # harness backends' creds onto serve's env so a containerized lead can authenticate cursor and pin
  # the opencode model. CURSOR_API_KEY is already lead-allowlisted; LOOM_OPENCODE_MODEL rides the
  # LOOM_ prefix; CLAUDE_CODE_OAUTH_TOKEN is now lead-allowlisted and buildClaudeEnv sets IS_SANDBOX,
  # so a containerized claude lead authenticates from the setup-token.
  [[ -n "${CURSOR_API_KEY:-}" ]] && podman_args+=(-e "CURSOR_API_KEY=${CURSOR_API_KEY}")
  [[ -n "${LOOM_OPENCODE_MODEL:-}" ]] && podman_args+=(-e "LOOM_OPENCODE_MODEL=${LOOM_OPENCODE_MODEL}")
  [[ -n "${CLAUDE_CODE_OAUTH_TOKEN:-}" ]] && podman_args+=(-e "CLAUDE_CODE_OAUTH_TOKEN=${CLAUDE_CODE_OAUTH_TOKEN}")

  # Flag-gated serve behaviors (unified-agents): LOOM_TASK_READY_EVENTS opts the
  # issue-journal bridge into task.ready internal events (serve_loops.go, default off);
  # LOOM_DRIVER_EXECUTOR_WORKSPACE scopes/unscopes the driver automation loops
  # (serve.go). Forwarded only when set so the default stack env is unchanged.
  [[ -n "${LOOM_TASK_READY_EVENTS:-}" ]] && podman_args+=(-e "LOOM_TASK_READY_EVENTS=${LOOM_TASK_READY_EVENTS}")
  [[ -n "${LOOM_DRIVER_EXECUTOR_WORKSPACE:-}" ]] && podman_args+=(-e "LOOM_DRIVER_EXECUTOR_WORKSPACE=${LOOM_DRIVER_EXECUTOR_WORKSPACE}")

  # Server-side workflow-build toolchain (register builtin/custom workflow versions over HTTP):
  # serve resolves @loom/sdk via LOOM_SDK_ROOT, @flue/runtime via FLUE_RUNTIME_ROOT, and the flue
  # CLI via LOOM_REAL_FLUE_CMD_JSON (internal/workflows/workflows.go). The deploy image bakes these
  # (Containerfile.loom-serve /opt/loom-sdk); this dev stack forwards them only when the caller
  # stages the toolchain (e.g. aether's unified-agents suite), leaving the default env unchanged.
  [[ -n "${LOOM_SDK_ROOT:-}" ]] && podman_args+=(-e "LOOM_SDK_ROOT=${LOOM_SDK_ROOT}")
  [[ -n "${FLUE_RUNTIME_ROOT:-}" ]] && podman_args+=(-e "FLUE_RUNTIME_ROOT=${FLUE_RUNTIME_ROOT}")
  [[ -n "${LOOM_REAL_FLUE_CMD_JSON:-}" ]] && podman_args+=(-e "LOOM_REAL_FLUE_CMD_JSON=${LOOM_REAL_FLUE_CMD_JSON}")

  # Standalone fleet-db: put serve (and the inherited `loom` execs) into CLOUD mode against the
  # external fleet-db, and join the harness network so the in-container client can resolve it by name.
  if truthy "$STANDALONE_FLEET_DB"; then
    if [[ -z "$LOOM_FLEET_DB_URL" ]]; then
      printf 'STANDALONE_FLEET_DB=1 requires LOOM_FLEET_DB_URL\n' >&2
      exit 1
    fi
    [[ -n "$PODMAN_NETWORK" ]] && podman_args+=(--network "$PODMAN_NETWORK")
    podman_args+=(-e "LOOM_FLEET_DB_URL=${LOOM_FLEET_DB_URL}")
    podman_args+=(-e "LOOM_FLEET_DB_ACTOR=loom-dev")
    [[ -n "$LOOM_FLEET_DB_API_KEY" ]] && podman_args+=(-e "LOOM_FLEET_DB_API_KEY=${LOOM_FLEET_DB_API_KEY}")
    log "standalone fleet-db: serve in CLOUD mode against ${LOOM_FLEET_DB_URL} (network=${PODMAN_NETWORK:-default})"
  fi

  if [[ -d "$FLUE_REPO" ]]; then
    podman_args+=(-v "${FLUE_REPO}:${CONTAINER_FLUE_REPO}:ro")
  fi

  if [[ -d "$CODEX_HOME" ]]; then
    podman_args+=(-v "${CODEX_HOME}:/root/.codex:ro")
  fi

  if [[ -d "$CLAUDE_HOME" ]]; then
    podman_args+=(-v "${CLAUDE_HOME}:/root/.claude:ro")
  fi

  podman_args+=("$IMAGE")
  podman "${podman_args[@]}" >/dev/null

  wait_for_health
  install_daytona_secret
  install_github_secret
  install_opencode_auth
  install_cursor_agent
  seed_repo
  seed_loom
  stage_daytona_bundle_deps
  seed_local_worktree_base
  register_epic_runner_workflow

  log "ready"
  printf '%s\n' "UI:     ${BASE_URL}/ws/${WORKSPACE_ID}/agents/atlas"
  printf '%s\n' "UI:     ${BASE_URL}/ws/${WORKSPACE_ID}/agents/nova"
  printf '%s\n' "API:    ${BASE_URL}/api/workspaces/${WORKSPACE_ID}/issues"
  if [[ "$DAYTONA_TASK_MODE" == "slack-pr-chain" ]]; then
    printf '%s\n' "Run:    podman exec -w ${CONTAINER_DRIVER_WORKDIR} -e LOOM_CONFIG_DIR=/root/.loom-config -e LOOM_WORKSPACE=${WORKSPACE_ID} -e LOOM_FLEET_DB_ACTOR=loom-e2e ${CONTAINER} loom epic run --parent ${WORKSPACE_ID}-1 --runner daytona-task-runner --max-concurrency 2 --detach"
  fi
  printf '%s\n' "Logs:   podman logs -f ${CONTAINER}"
  printf '%s\n' "Stop:   podman rm -f ${CONTAINER}"
}

main "$@"
