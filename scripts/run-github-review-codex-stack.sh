#!/usr/bin/env bash
# run-github-review-codex-stack.sh — bring up the A1 github-review-agent stack
# in one Podman container, end to end, using the SAME proven container
# machinery as scripts/run-slack-codex-epic-runner-stack.sh (Dockerfile.dev
# image, embedded fleet-db sidecar, host ~/.codex mounted read-only with a
# writable CODEX_HOME, LOOM_DRIVER_EXECUTOR, health wait, builtin-workflow
# flue-dist registration).
#
# What differs from the slack stack:
#   * task execution goes through scripts/loom-task-runner-invoker.mjs, which
#     resolves the named github-review-task-runner workflow from the same
#     registered driver bundle;
#   * after health it seals the connector vault key + the gh-token credential
#     (from `gh auth token`) + a freshly generated inbound webhook secret, then
#     runs deploy/agents/a1-github-review/setup.sh to create the connector,
#     grant, and trigger binding;
#   * it registers the github-review-agent BUILTIN workflow (built from
#     internal/workflows/builtin/github-review-agent.ts) — setup.sh's binding
#     references it by name, so it must exist first.
#
# This script provisions and prints how to drive the live flow; it does NOT
# fire a webhook itself. scripts/test-a1-live-review.sh drives the real PR.
#
# Teardown is `podman rm -f <container>` (podman's own container remove) — this
# script never runs shell `rm` on files.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# ── Reused mount sources (verbatim shapes from the slack stack) ────────────
TASK_RUNNER="${TASK_RUNNER:-${ROOT_DIR}/scripts/loom-task-runner-invoker.mjs}"
SETUP_SCRIPT="${SETUP_SCRIPT:-${ROOT_DIR}/deploy/agents/a1-github-review/setup.sh}"
REVIEW_WORKFLOW_SOURCE="${REVIEW_WORKFLOW_SOURCE:-${ROOT_DIR}/internal/workflows/builtin/github-review-agent.ts}"
REVIEW_TASK_RUNNER_SOURCE="${REVIEW_TASK_RUNNER_SOURCE:-${ROOT_DIR}/internal/workflows/builtin/github-review-task-runner.ts}"
LOOM_SDK_DIR="${LOOM_SDK_DIR:-${ROOT_DIR}/sdk}"

CONTAINER="${CONTAINER:-loom-github-review}"
IMAGE="${IMAGE:-loomcli-dev-github-review}"
HOST_PORT="${HOST_PORT:-8093}"
BASE_URL="${BASE_URL:-http://localhost:${HOST_PORT}}"
WORKSPACE_NAME="${WORKSPACE_NAME:-GitHub_Review}"
WORKSPACE_ID="${WORKSPACE_ID:-GITHUB-REVIEW}"
DRIVER_NODE_ID="${DRIVER_NODE_ID:-github-review-codex-driver}"

# Review subject: the open PR the A1 agent will comment on.
A1_GITHUB_REPO="${A1_GITHUB_REPO:-tysonthomas9/loom-review-sandbox}"
A1_CONNECTOR_ID="${A1_CONNECTOR_ID:-github}"
A1_WORKFLOW_NAME="${A1_WORKFLOW_NAME:-github-review-agent}"
A1_BINDING_ID="${A1_BINDING_ID:-a1-github-review}"
A1_WEBHOOK_ENDPOINT_PATH="${A1_WEBHOOK_ENDPOINT_PATH:-/webhooks/github}"

FLEET_DB_REPO="${FLEET_DB_REPO:-${ROOT_DIR}/../fleet-db}"
FLEET_DB_BIN="${FLEET_DB_BIN:-${ROOT_DIR}/tmp/podman/fleet-db-linux}"
BUILD_FLEET_DB="${BUILD_FLEET_DB:-auto}"
BUILD_IMAGE="${BUILD_IMAGE:-auto}"

CODEX_HOME="${CODEX_HOME:-${HOME}/.codex}"
CONTAINER_CODEX_AUTH_FILE="${CONTAINER_CODEX_AUTH_FILE:-/root/.codex/auth.json}"
CONTAINER_TASK_RUNNER="${CONTAINER_TASK_RUNNER:-/usr/local/bin/loom-task-runner-invoker.mjs}"
CONTAINER_SETUP_SCRIPT="${CONTAINER_SETUP_SCRIPT:-/usr/local/bin/a1-setup.sh}"
CONTAINER_REVIEW_DIST="${CONTAINER_REVIEW_DIST:-/tmp/github-review-agent-dist}"
CONTAINER_DRIVER_WORKDIR="${CONTAINER_DRIVER_WORKDIR:-/root/.loom/workspaces/alpha}"
CONTAINER_CODEX_HOME="${CONTAINER_CODEX_HOME:-/root/.codex-rw}"
LOOM_FLUE_AGENT_MODEL="${LOOM_FLUE_AGENT_MODEL:-openai-codex/gpt-5.3-codex-spark}"

# GH_TOKEN is the outbound credential sealed into the connector (server posts
# the review with it). Default to `gh auth token` so the operator does not have
# to export anything; an explicit GH_TOKEN env overrides.
GH_TOKEN="${GH_TOKEN:-}"
# Inbound webhook HMAC secret. Generated fresh if unset; printed so the live
# test (and GitHub) can sign with the same value.
A1_WEBHOOK_SECRET="${A1_WEBHOOK_SECRET:-}"
# Connector vault key (standard base64, 32 bytes). Shared between serve (which
# unseals at dispatch) and the in-container `loom connector create` (which
# seals). Generated fresh if unset.
LOOM_CONNECTOR_VAULT_KEY="${LOOM_CONNECTOR_VAULT_KEY:-}"

log() {
  printf '[github-review-stack] %s\n' "$*"
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

# json_array — verbatim from the slack stack: encode argv as a JSON string array.
json_array() {
  node -e 'console.log(JSON.stringify(process.argv.slice(1)))' "$@"
}

# workspace_payload — the github-review workspace is a plain empty workspace
# (no repo checkout; the diff arrives via connector reads, never a clone).
workspace_payload() {
  node -e 'console.log(JSON.stringify({name: process.argv[1], type: "empty"}))' "$WORKSPACE_NAME"
}

post_json() {
  local path="$1"
  local payload="$2"
  curl -fsS \
    -X POST "${BASE_URL}${path}" \
    -H "Content-Type: application/json" \
    --data-binary "$payload" >/dev/null
}

# wait_for_health — verbatim from the slack stack.
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

# podman_goarch — verbatim from the slack stack.
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

# ensure_fleet_db — verbatim from the slack stack.
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

# ensure_image — verbatim from the slack stack.
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

# source_digest — same digest scheme the slack stack uses (path NUL source
# NUL), across the github-review-agent and sibling review task runner sources.
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
    "workflows/github-review-agent.ts=${REVIEW_WORKFLOW_SOURCE}" \
    "workflows/github-review-task-runner.ts=${REVIEW_TASK_RUNNER_SOURCE}"
}

# write_review_dist packages the workflow and its sibling runner into one
# native Flue-style dist. The server dispatches by FLUE_CLI_NAME, so the same
# bundle can run the driver workflow or the named task runner via the generic
# invoker.
write_review_dist() {
  local dist="$1"
  mkdir -p "${dist}/node_modules/@loom/sdk" "${dist}/workflows"
  cp "${LOOM_SDK_DIR}/package.json" "${dist}/node_modules/@loom/sdk/package.json"
  cp "${LOOM_SDK_DIR}/driver.js" "${dist}/node_modules/@loom/sdk/driver.js"
  cp "${LOOM_SDK_DIR}/runner.js" "${dist}/node_modules/@loom/sdk/runner.js"
  cp "${LOOM_SDK_DIR}/internal.js" "${dist}/node_modules/@loom/sdk/internal.js"
  cp "$REVIEW_WORKFLOW_SOURCE" "${dist}/workflows/github-review-agent.mjs"
  cp "$REVIEW_TASK_RUNNER_SOURCE" "${dist}/workflows/github-review-task-runner.mjs"
  node -e '
const fs = require("node:fs");
const dist = process.argv[1];
fs.writeFileSync(dist + "/loom-driver.json", JSON.stringify({
  runners: JSON.stringify([
    { name: "github-review-task-runner", kind: "flue-workflow", entrypoint: "github-review-task-runner" }
  ])
}, null, 2) + "\n");
' "$dist"
  cat > "${dist}/server.mjs" <<'EOF'
const workflowLoaders = {
  "github-review-agent": () => import("./workflows/github-review-agent.mjs"),
  "github-review-task-runner": () => import("./workflows/github-review-task-runner.mjs"),
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
    const workflowName = process.env.FLUE_CLI_NAME || "github-review-agent";
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

# register_review_workflow — clones the slack stack's
# register_epic_runner_workflow: build the dist on the host, copy it into the
# container, and `loom driver register --flue-dist … --activate`. The trigger
# binding setup.sh creates references this workflow by name, so it must exist
# in the workspace first.
register_review_workflow() {
  require_path "$REVIEW_WORKFLOW_SOURCE"
  require_path "${LOOM_SDK_DIR}/package.json"
  require_path "${LOOM_SDK_DIR}/driver.js"

  local build_dir digest
  build_dir="$(mktemp -d "${TMPDIR:-/tmp}/loom-github-review-dist.XXXXXX")"
  write_review_dist "$build_dir"
  digest="$(source_digest)"

  log "registering builtin ${A1_WORKFLOW_NAME} workflow"
  # The container is recreated fresh each run (podman rm -f then run), so the
  # dist dir never pre-exists; mkdir -p is enough and we avoid any `rm`.
  podman exec "$CONTAINER" mkdir -p "$CONTAINER_REVIEW_DIST"
  podman cp "${build_dir}/." "${CONTAINER}:${CONTAINER_REVIEW_DIST}/"
  podman exec \
    -w "$CONTAINER_DRIVER_WORKDIR" \
    -e LOOM_CONFIG_DIR=/root/.loom-config \
    -e LOOM_WORKSPACE="$WORKSPACE_ID" \
    -e LOOM_FLEET_DB_ACTOR=loom-dev \
    "$CONTAINER" \
    loom driver register \
      --flue-dist "$CONTAINER_REVIEW_DIST" \
      --name "$A1_WORKFLOW_NAME" \
      --id "$A1_WORKFLOW_NAME" \
      --workflow "$A1_WORKFLOW_NAME" \
      --source-ref "builtin://workflows/${A1_WORKFLOW_NAME}/versions/${digest}" \
      --source-digest "$digest" \
      --activate \
      --json >/dev/null
  # build_dir is left for the OS temp reaper — this script never runs shell rm.
}

# resolve_secrets fills GH_TOKEN / A1_WEBHOOK_SECRET / LOOM_CONNECTOR_VAULT_KEY
# from `gh auth token` and random generators when the operator did not provide
# them. None of these values are ever echoed.
resolve_secrets() {
  if [[ -z "$GH_TOKEN" ]]; then
    log "reading outbound gh credential from 'gh auth token'"
    GH_TOKEN="$(gh auth token 2>/dev/null || true)"
    if [[ -z "$GH_TOKEN" ]]; then
      # shellcheck disable=SC2016  # backticks here are literal prose, not substitution
      printf 'no GH_TOKEN provided and `gh auth token` returned nothing; run `gh auth login` or export GH_TOKEN\n' >&2
      exit 1
    fi
  fi
  if [[ -z "$A1_WEBHOOK_SECRET" ]]; then
    A1_WEBHOOK_SECRET="$(openssl rand -hex 32)"
    log "generated a fresh inbound webhook secret"
  fi
  if [[ -z "$LOOM_CONNECTOR_VAULT_KEY" ]]; then
    LOOM_CONNECTOR_VAULT_KEY="$(openssl rand -base64 32)"
    log "generated a fresh connector vault key"
  fi
}

# run_setup runs the A1 setup.sh inside the container against the embedded
# fleet-db store, passing the secrets through the container env (env vars, not
# argv) so they never appear in any process listing. The same vault key is set
# so the in-container seal matches serve's unseal key.
run_setup() {
  log "provisioning connector + grant + trigger binding via setup.sh"
  podman exec \
    -e LOOM_WORKSPACE="$WORKSPACE_ID" \
    -e LOOM_FLEET_DB_ACTOR=loom-dev \
    -e LOOM_CONFIG_DIR=/root/.loom-config \
    -e LOOM_CONNECTOR_VAULT_KEY="$LOOM_CONNECTOR_VAULT_KEY" \
    -e GH_TOKEN="$GH_TOKEN" \
    -e A1_WEBHOOK_SECRET="$A1_WEBHOOK_SECRET" \
    -e A1_GITHUB_REPO="$A1_GITHUB_REPO" \
    -e A1_CONNECTOR_ID="$A1_CONNECTOR_ID" \
    -e A1_WORKFLOW_NAME="$A1_WORKFLOW_NAME" \
    -e A1_BINDING_ID="$A1_BINDING_ID" \
    -e A1_WEBHOOK_ENDPOINT_PATH="$A1_WEBHOOK_ENDPOINT_PATH" \
    "$CONTAINER" \
    bash "$CONTAINER_SETUP_SCRIPT"
}

main() {
  require_cmd curl
  require_cmd node
  require_cmd podman
  require_cmd gh
  require_cmd openssl
  require_path "$TASK_RUNNER"
  require_path "$SETUP_SCRIPT"
  require_path "$REVIEW_WORKFLOW_SOURCE"
  require_path "$REVIEW_TASK_RUNNER_SOURCE"
  require_path "$CODEX_HOME"
  require_path "${CODEX_HOME}/auth.json"

  resolve_secrets
  ensure_fleet_db
  ensure_image

  # The task bridge strips CODEX_* from the inherited runner env
  # (internal/driver/env.go), so wrap the generic invoker with `env CODEX_HOME`
  # for the review task runner and the codex child it spawns.
  local runner_cmd_json
  runner_cmd_json="$(json_array env "CODEX_HOME=${CONTAINER_CODEX_HOME}" node "$CONTAINER_TASK_RUNNER")"

  log "recreating container ${CONTAINER} from ${IMAGE}"
  podman rm -f "$CONTAINER" >/dev/null 2>&1 || true

  local podman_args=(
    run -d --init
    --name "$CONTAINER"
    -p "${HOST_PORT}:3000"
    -e "LOOM_FRONTEND_URL=${BASE_URL}"
    -e "LOOM_DRIVER_EXECUTOR=1"
    -e "LOOM_DRIVER_EXECUTOR_NODE_ID=${DRIVER_NODE_ID}"
    -e "LOOM_DRIVER_TASK_RUNNER_CMD_JSON=${runner_cmd_json}"
    -e "LOOM_CODEX_AUTH_FILE=${CONTAINER_CODEX_AUTH_FILE}"
    -e "LOOM_FLUE_AGENT_MODEL=${LOOM_FLUE_AGENT_MODEL}"
    -e "LOOM_CONNECTOR_VAULT_KEY=${LOOM_CONNECTOR_VAULT_KEY}"
    -v "${FLEET_DB_BIN}:/usr/local/bin/fleet-db:ro"
    -v "${TASK_RUNNER}:${CONTAINER_TASK_RUNNER}:ro"
    -v "${SETUP_SCRIPT}:${CONTAINER_SETUP_SCRIPT}:ro"
    -v "${CODEX_HOME}:/root/.codex:ro"
  )

  podman_args+=("$IMAGE")
  podman "${podman_args[@]}" >/dev/null

  wait_for_health
  log "registering workspace ${WORKSPACE_ID}"
  post_json "/api/workspaces" "$(workspace_payload)"

  register_review_workflow
  run_setup

  log "ready"
  printf '%s\n' "Container:  ${CONTAINER}"
  printf '%s\n' "Workspace:  ${WORKSPACE_ID}"
  printf '%s\n' "Webhook:    ${BASE_URL}/api/workspaces/${WORKSPACE_ID}${A1_WEBHOOK_ENDPOINT_PATH}"
  printf '%s\n' "            (POST a signed github pull_request event; HMAC sha256 in X-Hub-Signature-256)"
  printf '%s\n' "Subject:    PR on ${A1_GITHUB_REPO}"
  printf '%s\n' "Logs:       podman logs -f ${CONTAINER}"
  printf '%s\n' "Stop:       podman rm -f ${CONTAINER}"
  # Export the resolved secrets for a parent driver (test-a1-live-review.sh) to
  # reuse the SAME inbound secret when signing the webhook. Written to a
  # caller-supplied file descriptor / path only when A1_SECRETS_OUT is set;
  # never printed.
  if [[ -n "${A1_SECRETS_OUT:-}" ]]; then
    {
      printf 'A1_WEBHOOK_SECRET=%s\n' "$A1_WEBHOOK_SECRET"
      printf 'LOOM_CONNECTOR_VAULT_KEY=%s\n' "$LOOM_CONNECTOR_VAULT_KEY"
    } > "$A1_SECRETS_OUT"
    log "wrote resolved secrets to ${A1_SECRETS_OUT} (consumed by the live test)"
  fi
}

main "$@"
