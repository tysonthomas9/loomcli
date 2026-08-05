#!/usr/bin/env bash
# test-a1-review-multi-container.sh — run the A1 github-review-agent end-to-end
# on the DISTRIBUTED podman compose stack (deploy/podman-stack/): separate
# redis, fleet-db, loom-serve, worker, and stub-upstream containers, with the
# codex-backed review TaskRun executing in the loom-serve container's EMBEDDED
# driver executor (LOOM_DRIVER_EXECUTOR=1).
#
# WHY codex in loom-serve and not the worker container:
#   The worker container's `loom worker` route runs automode.RunAutoModeLoop —
#   an agent/task-development loop over HTTP-backed lock/event bridges
#   (internal/cli/serve/worker/worker_cmd.go) — NOT a driver TaskWorker that
#   claims driver TaskRuns. Driver TaskRuns (the A1 review) are claimed and
#   executed by serve's embedded task workers, which invoke the runner command
#   in the loom-serve process (internal/driver/task_bridge.go). So the codex
#   CLI + auth + runner command are baked into / mounted onto loom-serve. The
#   stack stays DISTRIBUTED (five separate capped containers); only the executor
#   that runs the review is co-located with serve. This is the SAME proven codex
#   recipe as the single-container loomcli-dev path (Dockerfile.dev +
#   scripts/run-github-review-codex-stack.sh).
#
# Flow (each stage prints PASS + evidence; aborts on first failure, dumps logs):
#   1. resolve secrets (gh auth token / fresh webhook secret / fresh vault key)
#      and the host ~/.codex dir; generate the gitignored stack .env with the
#      codex wiring; build images (build.sh) and `podman compose up` the stack
#      with the codex overlay (compose.codex.yaml);
#   2. wait for fleet-db + loom-serve health; seed the workspace in fleet-db;
#   3. submit the github-review-agent workflow over HTTP (serve builds the
#      bundle in-container via flue) and promote the stamped-untrusted driver to
#      trusted (the process-launcher placement gate refuses untrusted otherwise);
#   4. run deploy/agents/a1-github-review/setup.sh INSIDE the loom-serve
#      container (connector + grants + trigger binding), secrets via env;
#   5. seed a FRESH PR on tysonthomas9/loom-review-sandbox (new branch + a
#      reviewable change) so this distributed review is DISTINCT from PR #1's
#      single-container review;
#   6. baseline the PR's reviews, then replay that PR's pull_request.opened
#      webhook (HMAC-sha256) to serve's webhook ingress;
#   7. poll until a NEW COMMENT review appears, or a hard timeout fires.
#
# Teardown ALWAYS runs `podman compose ... down --volumes` (podman's own volume
# teardown) — NEVER shell `rm` on files. Secrets ride env/stdin, never argv.
#
# Reuse: deploy/agents/a1-github-review/setup.sh (baked into the loom-serve
# image as /usr/local/bin/a1-setup.sh) and the webhook-replay / review-poll
# logic copied from scripts/test-a1-live-review.sh (build_event / sign_body /
# wait_for_review). Task execution goes through the generic
# loom-task-runner-invoker.mjs and the sibling github-review-task-runner
# workflow submitted with github-review-agent.
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STACK_DIR="$ROOT_DIR/deploy/podman-stack"
ENV_FILE="$STACK_DIR/.env"
BUILD_SCRIPT="$STACK_DIR/build.sh"

# Compose contract: base distributed stack + codex overlay. The acceptance
# suite's compose.e2e.yaml is intentionally NOT layered — it routes github
# egress at stub-upstream and wires a deterministic task-runner stub, both of
# which would defeat a LIVE review against real github.com.
COMPOSE=(podman compose -f "$STACK_DIR/compose.yaml" -f "$STACK_DIR/compose.codex.yaml" --env-file "$ENV_FILE")

# ── Tunables / identity ───────────────────────────────────────────────────
LOOM_STACK_PROJECT="${LOOM_STACK_PROJECT:-loom-a1-review-stack}"
LOOM_STACK_SERVE_PORT="${LOOM_STACK_SERVE_PORT:-18282}"
LOOM_STACK_FLEET_DB_PORT="${LOOM_STACK_FLEET_DB_PORT:-18280}"
LOOM_STACK_STUB_PORT="${LOOM_STACK_STUB_PORT:-18299}"
WS="${LOOM_STACK_WORKSPACE:-PODSTACK}"
FLEET_SEED_ACTOR="${FLEET_SEED_ACTOR:-loom-serve@podman-stack.local}"

SERVE_URL="http://127.0.0.1:${LOOM_STACK_SERVE_PORT}"
FLEET_URL="http://127.0.0.1:${LOOM_STACK_FLEET_DB_PORT}"

# Review subject + binding identity (mirrors the single-container A1 stack).
A1_GITHUB_REPO="${A1_GITHUB_REPO:-tysonthomas9/loom-review-sandbox}"
A1_CONNECTOR_ID="${A1_CONNECTOR_ID:-github}"
A1_WORKFLOW_NAME="${A1_WORKFLOW_NAME:-github-review-agent}"
A1_BINDING_ID="${A1_BINDING_ID:-a1-github-review-multi}"
A1_WEBHOOK_ENDPOINT_PATH="${A1_WEBHOOK_ENDPOINT_PATH:-/webhooks/github}"
WEBHOOK_URL="${SERVE_URL}/api/workspaces/${WS}${A1_WEBHOOK_ENDPOINT_PATH}"
WORKFLOW_SOURCE="${WORKFLOW_SOURCE:-${ROOT_DIR}/internal/infra/workflowdistribution/builtin/github-review-agent.ts}"
REVIEW_TASK_RUNNER_SOURCE="${REVIEW_TASK_RUNNER_SOURCE:-${ROOT_DIR}/internal/infra/workflowdistribution/builtin/github-review-task-runner.ts}"

# Codex auth dir on the host (mounted read-only into loom-serve; the entrypoint
# mirrors it into the writable CODEX_HOME). Never mutated.
CODEX_HOME_HOST="${CODEX_HOME:-${HOME}/.codex}"
LOOM_STACK_FLUE_AGENT_MODEL="${LOOM_STACK_FLUE_AGENT_MODEL:-openai-codex/gpt-5.3-codex-spark}"

# Poll budget: webhook -> dispatch -> driver run -> codex task run -> connector
# post is a long path; allow generous headroom.
REVIEW_TIMEOUT_SECS="${REVIEW_TIMEOUT_SECS:-600}"
REVIEW_POLL_SECS="${REVIEW_POLL_SECS:-10}"

# Secrets (resolved below). Never echoed.
GH_TOKEN="${GH_TOKEN:-}"
A1_WEBHOOK_SECRET="${A1_WEBHOOK_SECRET:-}"
LOOM_CONNECTOR_VAULT_KEY="${LOOM_CONNECTOR_VAULT_KEY:-}"

# The github login the loom connector posts the review as (the gh token owner).
# The poll requires the new review to come from this identity so the repo's
# native ChatGPT Codex bot auto-review can never be mistaken for our pipeline.
A1_REVIEW_AUTHOR="${A1_REVIEW_AUTHOR:-}"

STACK_STARTED=0
TMP_ROOT=""
SUITE_STAGE=""
PR_BRANCH=""
PR_NUMBER=""

log() { printf '[a1-multi] %s\n' "$*"; }
log_pass() { printf 'PASS  %s\n' "$*"; }
die() { printf '[a1-multi] error: %s\n' "$*" >&2; exit 1; }
stage() { SUITE_STAGE="$1"; printf '\n==> [%s] %s\n' "$1" "$2"; }

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}
require_path() {
  [[ -e "$1" ]] || die "missing required path: $1"
}

# svc_container resolves a compose service name to its container name, tolerant
# of docker-compose (project-svc-1) and podman-compose (project_svc_1) naming.
svc_container() {
  local svc="$1"
  podman ps -a --format '{{.Names}}' 2>/dev/null |
    grep -E "^${LOOM_STACK_PROJECT}[-_]${svc}([-_][0-9]+)?$" | head -1 || true
}

dump_logs() {
  echo "--- podman compose ps ---" >&2
  "${COMPOSE[@]}" ps >&2 || true
  local svc ctr
  for svc in fleet-db loom-serve worker; do
    ctr="$(svc_container "$svc")" || true
    [[ -n "$ctr" ]] || continue
    echo "--- ${svc} (${ctr}) last 120 lines ---" >&2
    podman logs --tail 120 "$ctr" >&2 || true
  done
}

# teardown runs on every exit path. It removes the FRESH review branch via the
# GitHub API (gh), then tears the stack down with `podman compose down
# --volumes` — podman's own volume teardown, NEVER shell rm on files.
# shellcheck disable=SC2329  # invoked indirectly through the EXIT/INT/TERM trap
teardown() {
  local status=$?
  trap - EXIT INT TERM
  if [[ "$status" -ne 0 ]]; then
    echo >&2
    echo "A1 multi-container review FAILED${SUITE_STAGE:+ in stage ${SUITE_STAGE}} (exit $status)" >&2
    dump_logs
  fi
  if [[ -n "$PR_BRANCH" ]]; then
    log "deleting fresh review branch ${PR_BRANCH} on ${A1_GITHUB_REPO}"
    gh api -X DELETE "repos/${A1_GITHUB_REPO}/git/refs/heads/${PR_BRANCH}" >/dev/null 2>&1 || true
  fi
  if [[ "$STACK_STARTED" == "1" ]]; then
    if [[ "${KEEP_STACK:-0}" == "1" && "$status" -eq 0 ]]; then
      echo
      echo "KEEP_STACK=1 — stack left running."
      echo "  serve:    ${SERVE_URL}"
      echo "  fleet-db: ${FLEET_URL}"
      echo "  teardown: ${COMPOSE[*]} down --volumes"
    else
      log "tearing down stack (podman compose down --volumes)"
      "${COMPOSE[@]}" down --volumes --timeout 10 >/dev/null 2>&1 ||
        "${COMPOSE[@]}" down --volumes >/dev/null 2>&1 || true
    fi
  fi
  exit "$status"
}

# ── HTTP helpers ───────────────────────────────────────────────────────────

http_ok() { curl -fsS -m 5 "$1" >/dev/null 2>&1; }

# wait_http url name [budget]
wait_http() {
  local url="$1" name="$2" budget="${3:-150}"
  local deadline=$(( SECONDS + budget ))
  while (( SECONDS < deadline )); do
    http_ok "$url" && return 0
    sleep 1
  done
  die "timed out (${budget}s) waiting for ${name} at ${url}"
}

# fdb method path [body] — fleet-db admin call (X-API-Key + X-Actor from a
# header file so the key never rides argv).
fdb() {
  local method="$1" path="$2" body="${3:-}"
  if [[ -n "$body" ]]; then
    curl -fsS --max-time 20 -X "$method" -H @"$TMP_ROOT/fleet-headers" \
      -H 'Content-Type: application/json' --data "$body" "$FLEET_URL$path"
  else
    curl -fsS --max-time 20 -X "$method" -H @"$TMP_ROOT/fleet-headers" "$FLEET_URL$path"
  fi
}

# ── webhook helpers (copied from scripts/test-a1-live-review.sh) ───────────

# build_event reshapes the live PR JSON (stdin) into a github
# pull_request.opened webhook event body. The adapter
# (internal/webui/handlers/webhooks/github.go) reads action, number,
# pull_request.{number,draft,head.sha,base.ref}, repository.full_name and
# sender.login — so the event carries exactly those, sourced from the real PR.
build_event() {
  node -e '
const fs = require("node:fs");
const pr = JSON.parse(fs.readFileSync(0, "utf8"));
const event = {
  action: "opened",
  number: pr.number,
  pull_request: {
    number: pr.number,
    draft: Boolean(pr.draft),
    state: pr.state,
    title: pr.title,
    head: { sha: pr.head && pr.head.sha, ref: pr.head && pr.head.ref },
    base: { ref: pr.base && pr.base.ref, sha: pr.base && pr.base.sha },
  },
  repository: { full_name: pr.base && pr.base.repo && pr.base.repo.full_name },
  sender: { login: (pr.user && pr.user.login) || "loom-review-bot" },
};
if (!event.repository.full_name) {
  event.repository.full_name = process.env.A1_GITHUB_REPO || "";
}
process.stdout.write(JSON.stringify(event));
'
}

# sign_body computes the X-Hub-Signature-256 value for the body on stdin,
# reading the secret from A1_WEBHOOK_SECRET (env, never argv). Matches
# githubSignature(): "sha256=" + hex(HMAC_SHA256(secret, body)).
sign_body() {
  node -e '
const crypto = require("node:crypto");
const fs = require("node:fs");
const body = fs.readFileSync(0);
const secret = process.env.A1_WEBHOOK_SECRET || "";
const mac = crypto.createHmac("sha256", secret).update(body).digest("hex");
process.stdout.write("sha256=" + mac);
'
}

fetch_review_ids() {
  gh api "repos/${A1_GITHUB_REPO}/pulls/${PR_NUMBER}/reviews" --paginate \
    --jq '.[].id' 2>/dev/null || true
}

# wait_for_review polls the PR reviews until a review id not present in the
# baseline appears with state COMMENTED, or the hard timeout fires.
wait_for_review() {
  local baseline="$1"
  local deadline now reviews_json new_comment serve_ctr
  serve_ctr="$(svc_container loom-serve)"
  deadline=$(( $(date +%s) + REVIEW_TIMEOUT_SECS ))
  while :; do
    reviews_json="$(gh api "repos/${A1_GITHUB_REPO}/pulls/${PR_NUMBER}/reviews" --paginate 2>/dev/null || echo '[]')"
    new_comment="$(BASELINE="$baseline" REVIEW_AUTHOR="$A1_REVIEW_AUTHOR" node -e '
const fs = require("node:fs");
const reviews = JSON.parse(fs.readFileSync(0, "utf8"));
const baseline = new Set(String(process.env.BASELINE || "").split(/\s+/).filter(Boolean));
const wantAuthor = String(process.env.REVIEW_AUTHOR || "").trim().toLowerCase();
// The review must come from OUR loom github connector (it posts with the
// sealed gh credential, so the author is the token owner). GitHub repos with
// the native ChatGPT Codex connector ALSO auto-review opened PRs as
// "chatgpt-codex-connector[bot]"; that is NOT our distributed pipeline and
// must never be accepted as a pass. Require the author to be our connector
// identity and reject any bot account.
const hit = reviews.find((r) => {
  if (!r || r.state !== "COMMENTED" || baseline.has(String(r.id))) return false;
  const login = String((r.user && r.user.login) || "").toLowerCase();
  const isBot = (r.user && r.user.type === "Bot") || /\[bot\]$/.test(login);
  if (isBot) return false;
  if (wantAuthor && login !== wantAuthor) return false;
  return true;
});
if (hit) {
  process.stdout.write(JSON.stringify({ id: hit.id, user: hit.user && hit.user.login, url: hit.html_url }));
}
' <<<"$reviews_json")"
    if [[ -n "$new_comment" ]]; then
      log "agent COMMENT review detected: ${new_comment}"
      return 0
    fi
    now=$(date +%s)
    if (( now >= deadline )); then
      log "timed out after ${REVIEW_TIMEOUT_SECS}s waiting for a new COMMENT review"
      [[ -n "$serve_ctr" ]] && { log "recent loom-serve logs:"; podman logs --tail 100 "$serve_ctr" 2>&1 || true; }
      return 1
    fi
    sleep "$REVIEW_POLL_SECS"
  done
}

# ── stages ─────────────────────────────────────────────────────────────────

resolve_secrets() {
  if [[ -z "$GH_TOKEN" ]]; then
    log "reading outbound gh credential from 'gh auth token'"
    GH_TOKEN="$(gh auth token 2>/dev/null || true)"
    # shellcheck disable=SC2016  # backticks are literal prose, not substitution
    [[ -n "$GH_TOKEN" ]] || die 'no GH_TOKEN and `gh auth token` returned nothing; run `gh auth login` or export GH_TOKEN'
  fi
  if [[ -z "$A1_REVIEW_AUTHOR" ]]; then
    # The loom github connector posts the review with the sealed gh credential,
    # so the review author is that token's owner. Resolve it so the poll can
    # require OUR review (and reject the repo's native Codex bot auto-review).
    A1_REVIEW_AUTHOR="$(gh api user --jq '.login' 2>/dev/null || true)"
    [[ -n "$A1_REVIEW_AUTHOR" ]] ||
      die "could not resolve the gh token owner (gh api user); set A1_REVIEW_AUTHOR"
    log "review author identity (gh token owner): ${A1_REVIEW_AUTHOR}"
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

# gen_env writes the gitignored stack .env with per-run secrets + the codex
# wiring. The connector vault key is PINNED to the value setup.sh seals with so
# serve can unseal at dispatch. Codex host dir + runner command + agent model
# feed compose.codex.yaml. Values are never echoed.
gen_env() {
  log "generating per-run stack secrets into ${ENV_FILE} (values not echoed)"
  umask 077
  {
    printf 'LOOM_STACK_PROJECT=%s\n' "$LOOM_STACK_PROJECT"
    printf 'LOOM_STACK_SERVE_PORT=%s\n' "$LOOM_STACK_SERVE_PORT"
    printf 'LOOM_STACK_FLEET_DB_PORT=%s\n' "$LOOM_STACK_FLEET_DB_PORT"
    printf 'LOOM_STACK_STUB_PORT=%s\n' "$LOOM_STACK_STUB_PORT"
    printf 'LOOM_STACK_WORKSPACE=%s\n' "$WS"
    printf 'FLEET_SEED_ACTOR=%s\n' "$FLEET_SEED_ACTOR"
    printf 'FLEET_SEED_ROLE=admin\n'
    printf 'LOOM_DRIVER_TASK_WORKER_CONCURRENCY=2\n'
    printf 'LOOM_DRIVER_TASK_RUN_MAX_ATTEMPTS=2\n'
    printf 'LOOM_STACK_FRONTEND_URL=http://127.0.0.1:%s\n' "$LOOM_STACK_SERVE_PORT"
    printf 'FLEET_LOG_LEVEL=info\n'
    # secrets
    printf 'LOOM_FLEET_DB_API_KEY=fldb_%s\n' "$(openssl rand -hex 24)"
    printf 'LOOM_RUN_TOKEN_SIGNING_KEY=%s\n' "$(openssl rand -hex 32)"
    printf 'LOOM_FLEET_API_KEY=%s\n' "$(openssl rand -hex 24)"
    printf 'LOOM_WORKER_TOKEN=%s\n' "$(openssl rand -hex 24)"
    printf 'LOOM_STACK_STUB_SECRET=%s\n' "$(openssl rand -hex 16)"
    printf 'LOOM_CONNECTOR_VAULT_KEY=%s\n' "$LOOM_CONNECTOR_VAULT_KEY"
    # codex wiring (compose.codex.yaml)
    printf 'LOOM_STACK_CODEX_HOST_DIR=%s\n' "$CODEX_HOME_HOST"
    printf 'LOOM_STACK_CODEX_RW_DIR=%s\n' "/home/node/.codex-rw"
    printf 'LOOM_STACK_FLUE_AGENT_MODEL=%s\n' "$LOOM_STACK_FLUE_AGENT_MODEL"
    printf 'LOOM_STACK_TASK_RUNNER_CMD_JSON=%s\n' \
      '["env","CODEX_HOME=/home/node/.codex-rw","node","/usr/local/bin/loom-task-runner-invoker.mjs"]'
  } >"$ENV_FILE"
  chmod 600 "$ENV_FILE"
}

stage_up() {
  stage S1 "resolve secrets, build images, compose up the distributed stack + codex overlay"
  resolve_secrets
  gen_env

  # Credential header file for fleet-db admin calls (key never on argv).
  #
  # Read ONLY the two values the header file needs directly out of the env-file
  # — do NOT `source` it into the shell environment. `source` would let bash
  # strip the shell-significant double quotes from JSON values (notably
  # LOOM_STACK_TASK_RUNNER_CMD_JSON: ["env","CODEX_HOME=…",…] -> [env,CODEX_HOME=…,…])
  # and then `set -a` would EXPORT that mangled value. The exported shell var
  # then shadows the env-file during `podman compose up` (compose interpolation
  # prefers the process environment over --env-file), so the loom-serve process
  # would receive invalid JSON for LOOM_DRIVER_TASK_RUNNER_CMD_JSON, json.Unmarshal
  # would fail in HostBridgeTaskExecutor.command(), and every named review
  # TaskRun would fail before the generic invoker reached the bundle runner.
  # Reading the keys without sourcing keeps the
  # quoted JSON intact for compose.
  local fleet_db_api_key fleet_actor
  fleet_db_api_key="$(sed -n 's/^LOOM_FLEET_DB_API_KEY=//p' "$ENV_FILE" | head -1)"
  fleet_actor="$(sed -n 's/^FLEET_SEED_ACTOR=//p' "$ENV_FILE" | head -1)"
  [[ -n "$fleet_db_api_key" ]] || die "could not read LOOM_FLEET_DB_API_KEY from $ENV_FILE"
  [[ -n "$fleet_actor" ]] || fleet_actor="$FLEET_SEED_ACTOR"
  printf 'X-API-Key: %s\nX-Actor: %s\n' "$fleet_db_api_key" "$fleet_actor" \
    >"$TMP_ROOT/fleet-headers"
  chmod 600 "$TMP_ROOT/fleet-headers"

  if [[ "${LOOM_STACK_SKIP_BUILD:-0}" == "1" ]]; then
    log "LOOM_STACK_SKIP_BUILD=1: reusing existing images"
  else
    log "building stack images (build.sh)"
    bash "$BUILD_SCRIPT" >"$TMP_ROOT/build.log" 2>&1 ||
      { tail -60 "$TMP_ROOT/build.log" >&2; die "stack image build failed (full log: $TMP_ROOT/build.log)"; }
  fi

  log "podman compose up -d (project ${LOOM_STACK_PROJECT})"
  STACK_STARTED=1
  "${COMPOSE[@]}" up -d >"$TMP_ROOT/compose-up.log" 2>&1 ||
    { tail -60 "$TMP_ROOT/compose-up.log" >&2; die "compose up failed"; }

  wait_http "$FLEET_URL/readyz" "fleet-db" 120
  wait_http "$SERVE_URL/api/health" "loom serve" 180
  log_pass "S1 stack healthy (serve :${LOOM_STACK_SERVE_PORT}, fleet-db :${LOOM_STACK_FLEET_DB_PORT}); codex review runner wired into loom-serve"
}

stage_seed_workspace() {
  stage S2 "seed workspace ${WS} in fleet-db"
  fdb POST /api/v1/admin/workspaces \
    "$(jq -nc --arg key "$WS" '{key:$key,name:"A1 Review Multi-Container",repos:["local/repo"],state:"ready"}')" \
    >/dev/null 2>&1 || true
  fdb GET "/api/v1/admin/workspaces" >/dev/null || die "fleet-db rejected the seeded API key"
  log_pass "S2 workspace ${WS} present in fleet-db"
}

stage_register_workflow() {
  stage S3 "submit + trust the github-review-agent workflow"
  require_path "$WORKFLOW_SOURCE"
  require_path "$REVIEW_TASK_RUNNER_SOURCE"

  # HTTP submission: serve builds the bundle in-container (flue). The driver is
  # stamped untrusted server-side; the process-launcher placement gate refuses
  # an untrusted driver, so we promote it to trusted via fleet-db (the same
  # path scripts/test-podman-stack.sh uses).
  local files_json submission driver_id trust
  files_json="$(jq -nc \
    --arg workflow_path "workflows/${A1_WORKFLOW_NAME}.ts" \
    --arg runner_path "workflows/github-review-task-runner.ts" \
    --rawfile workflow_src "$WORKFLOW_SOURCE" \
    --rawfile runner_src "$REVIEW_TASK_RUNNER_SOURCE" \
    '{files: {($workflow_path): $workflow_src, ($runner_path): $runner_src}, activate: true}')"
  submission="$(curl -fsS --max-time 240 -X POST \
    -H 'Content-Type: application/json' --data "$files_json" \
    "$SERVE_URL/api/workspaces/${WS}/workflows/${A1_WORKFLOW_NAME}/versions")" ||
    die "workflow submission failed"
  echo "$submission" >"$TMP_ROOT/workflow-submission.json"
  driver_id="$(jq -re '.driver.driver_id // .driver.id' <<<"$submission")" ||
    die "submission response missing driver id: $submission"
  trust="$(jq -r '.driver.trust_level // ""' <<<"$submission")"
  log "registered driver ${driver_id} (trust_level=${trust:-unknown})"

  local promoted
  promoted="$(fdb PATCH "/api/v1/${WS}/drivers/${driver_id}" '{"trust_level":"trusted"}')"
  [[ "$(jq -r '.trust_level' <<<"$promoted")" == "trusted" ]] ||
    die "failed to promote driver ${driver_id} to trusted: $promoted"
  log_pass "S3 ${A1_WORKFLOW_NAME} registered and promoted trusted (driver ${driver_id})"
}

stage_provision() {
  stage S4 "provision connector + grants + binding via setup.sh (in loom-serve)"
  local serve_ctr
  serve_ctr="$(svc_container loom-serve)"
  [[ -n "$serve_ctr" ]] || die "loom-serve container not found in project ${LOOM_STACK_PROJECT}"
  # Run the BAKED-IN setup.sh (reused verbatim). Secrets ride env, never argv.
  # The container already carries LOOM_FLEET_DB_* and LOOM_CONNECTOR_VAULT_KEY
  # (compose.yaml); we re-pass workspace + the A1 knobs + the secrets here.
  podman exec \
    -e LOOM_WORKSPACE="$WS" \
    -e LOOM_CONNECTOR_VAULT_KEY="$LOOM_CONNECTOR_VAULT_KEY" \
    -e GH_TOKEN="$GH_TOKEN" \
    -e A1_WEBHOOK_SECRET="$A1_WEBHOOK_SECRET" \
    -e A1_GITHUB_REPO="$A1_GITHUB_REPO" \
    -e A1_CONNECTOR_ID="$A1_CONNECTOR_ID" \
    -e A1_WORKFLOW_NAME="$A1_WORKFLOW_NAME" \
    -e A1_BINDING_ID="$A1_BINDING_ID" \
    -e A1_WEBHOOK_ENDPOINT_PATH="$A1_WEBHOOK_ENDPOINT_PATH" \
    "$serve_ctr" \
    bash /usr/local/bin/a1-setup.sh ||
    die "setup.sh provisioning failed"
  log_pass "S4 connector ${A1_CONNECTOR_ID} + grants + binding ${A1_BINDING_ID} provisioned"
}

# stage_seed_pr opens a FRESH PR (new branch + a reviewable change) so the
# distributed review is distinct from PR #1's single-container review. The
# branch is deleted on teardown.
stage_seed_pr() {
  stage S5 "seed a fresh reviewable PR on ${A1_GITHUB_REPO}"
  local default_branch base_sha ts content_b64 file_path pr_json

  default_branch="$(gh api "repos/${A1_GITHUB_REPO}" --jq '.default_branch')" ||
    die "could not read default branch for ${A1_GITHUB_REPO}"
  base_sha="$(gh api "repos/${A1_GITHUB_REPO}/git/refs/heads/${default_branch}" --jq '.object.sha')" ||
    die "could not read ${default_branch} head sha"

  ts="$(date +%Y%m%d-%H%M%S)"
  PR_BRANCH="a1-multi-review/${ts}-$$"
  file_path="reviews/distributed-${ts}.js"

  # New branch ref at the base head.
  gh api -X POST "repos/${A1_GITHUB_REPO}/git/refs" \
    -f "ref=refs/heads/${PR_BRANCH}" -f "sha=${base_sha}" >/dev/null ||
    die "failed to create branch ${PR_BRANCH}"

  # A small, deliberately reviewable change (an off-by-one + a missing guard)
  # committed via the contents API so codex has real substance to comment on.
  content_b64="$(node -e '
const ts = process.argv[1];
const src = [
  "// distributed A1 review sample (" + ts + ")",
  "function lastIndex(arr) {",
  "  // BUG: off-by-one — should be arr.length - 1",
  "  return arr[arr.length];",
  "}",
  "",
  "function divide(a, b) {",
  "  // missing guard for b === 0",
  "  return a / b;",
  "}",
  "",
  "module.exports = { lastIndex, divide };",
  "",
].join("\n");
process.stdout.write(Buffer.from(src, "utf8").toString("base64"));
' "$ts")"

  gh api -X PUT "repos/${A1_GITHUB_REPO}/contents/${file_path}" \
    -f "message=Add distributed review sample ${ts}" \
    -f "content=${content_b64}" \
    -f "branch=${PR_BRANCH}" \
    >/dev/null ||
    die "failed to commit the reviewable change on ${PR_BRANCH}"

  pr_json="$(gh api -X POST "repos/${A1_GITHUB_REPO}/pulls" \
    -f "title=Distributed A1 review ${ts}" \
    -f "head=${PR_BRANCH}" \
    -f "base=${default_branch}" \
    -f "body=Automated fixture PR for the distributed (multi-container) A1 github-review-agent run.")" ||
    die "failed to open PR for ${PR_BRANCH}"
  PR_NUMBER="$(jq -re '.number' <<<"$pr_json")" ||
    die "PR response missing number: $pr_json"
  log_pass "S5 opened fresh PR ${A1_GITHUB_REPO}#${PR_NUMBER} (branch ${PR_BRANCH})"
}

stage_fire_and_poll() {
  stage S6 "replay pull_request.opened webhook -> poll for the COMMENT review"
  export A1_WEBHOOK_SECRET A1_GITHUB_REPO

  local baseline_ids
  baseline_ids="$(fetch_review_ids | tr '\n' ' ')"
  log "baseline reviews on ${A1_GITHUB_REPO}#${PR_NUMBER}: [${baseline_ids}]"

  local pr_json event_body signature delivery_id http_status
  pr_json="$(gh api "repos/${A1_GITHUB_REPO}/pulls/${PR_NUMBER}")" ||
    die "failed to fetch PR ${PR_NUMBER}"
  event_body="$(printf '%s' "$pr_json" | build_event)"
  [[ -n "$event_body" ]] || die "failed to build webhook event body from PR JSON"
  signature="$(printf '%s' "$event_body" | sign_body)"
  delivery_id="a1-multi-$(date +%s)-$$"

  log "POSTing signed pull_request.opened to ${WEBHOOK_URL}"
  http_status="$(printf '%s' "$event_body" | curl -sS -o /dev/null -w '%{http_code}' \
    -X POST "$WEBHOOK_URL" \
    -H "Content-Type: application/json" \
    -H "X-GitHub-Event: pull_request" \
    -H "X-GitHub-Delivery: ${delivery_id}" \
    -H "X-Hub-Signature-256: ${signature}" \
    --data-binary @-)"
  log "webhook ingress responded HTTP ${http_status}"
  case "$http_status" in
    200 | 202) : ;;
    *) die "webhook ingress rejected the event (HTTP ${http_status})" ;;
  esac

  log "polling for a NEW COMMENT review (timeout ${REVIEW_TIMEOUT_SECS}s)"
  if wait_for_review "$baseline_ids"; then
    log_pass "S6 A1 github-review-agent posted a COMMENT review on ${A1_GITHUB_REPO}#${PR_NUMBER} (distributed stack)"
    return 0
  fi
  die "no COMMENT review appeared within the timeout"
}

main() {
  require_cmd podman
  require_cmd curl
  require_cmd jq
  require_cmd node
  require_cmd gh
  require_cmd openssl
  require_cmd go
  require_cmd date

  [[ -f "$STACK_DIR/compose.yaml" ]] || die "missing $STACK_DIR/compose.yaml"
  [[ -f "$STACK_DIR/compose.codex.yaml" ]] || die "missing $STACK_DIR/compose.codex.yaml"
  [[ -f "$BUILD_SCRIPT" ]] || die "missing $BUILD_SCRIPT"
  grep -q '^\.env$' "$STACK_DIR/.gitignore" 2>/dev/null ||
    die "$STACK_DIR/.gitignore must ignore .env before secrets are generated"
  require_path "$WORKFLOW_SOURCE"
  require_path "$CODEX_HOME_HOST"
  require_path "${CODEX_HOME_HOST}/auth.json"

  podman info >/dev/null 2>&1 || die "podman is not reachable (start the machine: podman machine start)"

  TMP_ROOT="$(mktemp -d -t loom-a1-multi.XXXXXX)"
  trap teardown EXIT INT TERM

  stage_up
  stage_seed_workspace
  stage_register_workflow
  stage_provision
  stage_seed_pr
  stage_fire_and_poll

  log_pass "DONE — distributed multi-container A1 review verified end-to-end"
}

main "$@"
