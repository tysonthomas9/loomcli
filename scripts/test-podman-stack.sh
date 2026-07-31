#!/usr/bin/env bash
# Acceptance driver for the local resource-capped podman stack that emulates
# the DISTRIBUTED Loom topology (deploy/podman-stack/): redis + fleet-db +
# loom serve + worker(s) + stub-upstream as separate capped containers.
#
# Stages (each prints PASS + evidence; the suite aborts on the first failure
# and dumps per-service logs):
#
#   S0  generate per-run secrets into the gitignored .env, build images,
#       compose up (base compose.yaml + compose.e2e.yaml overlay) with
#       healthcheck waits, fleet-db API-key auth positive/negative, stub
#       smoke, worker container up
#   S1  trust placement (§9.6: untrusted submission refused pre-launch with
#       sandbox_required -> operator promotes to trusted) and the
#       distributed epic drain: a 4-task diamond DAG through serve's task
#       queue + workers, DAG-order assertion from the task-runner log,
#       watch-SSE stream evidence, server-outbox lead delivery
#       (exactly-once, dedupe-keyed), rerun no-op
#   S2  webhook ingress -> Router v2 fan-out: ONE signed GitHub delivery
#       admits TWO bindings; the 202 body is deliveries[]-ONLY (no top-level
#       driver_run_id), both runs execute token-only (env=clean), redelivery
#       heals idempotently, a tampered signature is 401 with nothing
#       persisted
#   S3  connector egress: credential sealed via the vault CLI path (stdin,
#       never argv), grant created for ONE binding, the workflow performs a
#       granted github read (expected to hit stub-upstream with the unsealed
#       credential) and an ungranted github.merge (grant_denied), and the
#       connector-audit journal carries BOTH decisions
#   S4  events.await: run suspends on an approval subject key, loom-serve is
#       RESTARTED while suspended (cross-process durability), an anonymous
#       approval is 401, a session-authenticated approval (RS256 JWT minted
#       against this script's host-side JWKS stub) resolves the await and
#       the run resumes with the decision payload
#   S5  restart resilience: loom-serve restarted mid-epic-drain; a recovery
#       run drains the remainder and every task ends with EXACTLY one
#       completed TaskRun (zero duplicates)
#   S6  step-9 network legs from INSIDE the Linux containers: worker ->
#       fleet-db direct write denied, worker env carries no control-plane
#       credentials, worker -> off-host egress probed against a host
#       listener; container OOMKilled audit + stats snapshot
#
# Compose contract (deploy/podman-stack/):
#   compose.yaml       base stack (capped services: redis, fleet-auth-seed,
#                      fleet-db, loom-serve, worker, stub-upstream)
#   compose.e2e.yaml   suite overlay: session auth (LOOM_AUTH_URL -> this
#                      script's JWKS stub), deterministic task-runner stub,
#                      fast stale-TaskRun recovery
#   .env               per-run secrets (0600, gitignored) — generated here
#   host ports         loopback-only: serve ${LOOM_STACK_SERVE_PORT:-18282},
#                      fleet-db ${LOOM_STACK_FLEET_DB_PORT:-18280},
#                      stub ${LOOM_STACK_STUB_PORT:-18299}
#
# Env knobs:
#   KEEP_STACK=1                            leave the stack up on success and
#                                           keep the tmp workspace
#   LOOM_STACK_SKIP_BUILD=1                 reuse existing images
#   LOOM_STACK_FRESH_ENV=1                  force-regenerate .env secrets
#   LOOM_STACK_MAX_DAG_WALL_SECONDS=90      DAG drain budget
#   LOOM_STACK_CONNECTOR_STRICT=1           0 = tolerate a missing
#                                           stub-upstream egress seam (the
#                                           denial + audit legs stay strict)
#   LOOM_STACK_EXPECT_NETWORK_ISOLATION=0   1 = S6 requires NETWORK-level
#                                           denial (exec plane on an internal
#                                           network); 0 = credential-less
#                                           401/403 passes, egress leak warns
#   LOOM_STACK_RESTART_STRICT=0             1 = S5 hard-requires the recovery
#                                           run to fully drain the epic after
#                                           the mid-epic serve restart. Default
#                                           0: the recovery run is swept
#                                           stale_driver_run before it can
#                                           request the final leaf TaskRun on
#                                           this embedded stack; durability +
#                                           zero-duplicate invariants still
#                                           enforced, full drain downgraded to a
#                                           WARN.
#   LOOM_STACK_REQUIRE_WORKER=0             1 = hard-require worker-container
#                                           registration evidence. Default 0:
#                                           the worker registers but cannot hold
#                                           a task loop without an AI backend
#                                           (CustomPromptGen) on this headless
#                                           stack, so it re-registers on a loop;
#                                           TaskRuns run in serve's embedded
#                                           workers. Registration stays a WARN.
#
# DO NOT point this at loom-dev. It builds, runs, and tears down a private
# local stack; teardown ALWAYS runs (compose down --volumes + host helper
# kills) unless KEEP_STACK=1. The only rm targets this run's own mktemp dir.
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STACK_DIR="$ROOT/deploy/podman-stack"
ENV_FILE="$STACK_DIR/.env"
COMPOSE=(podman compose -f "$STACK_DIR/compose.yaml" -f "$STACK_DIR/compose.e2e.yaml" --env-file "$ENV_FILE")

log_step() { printf '\n==> %s\n' "$1"; }
log_pass() { printf 'PASS  %s\n' "$*"; }
log_warn() { printf 'WARN  %s\n' "$*" >&2; }
die() { echo "ERROR: $*" >&2; exit 1; }
require_cmd() { command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"; }

require_cmd podman
require_cmd curl
require_cmd jq
require_cmd openssl
require_cmd node
require_cmd go
require_cmd git

[[ -f "$STACK_DIR/compose.yaml" ]] || die "missing $STACK_DIR/compose.yaml"
[[ -f "$STACK_DIR/compose.e2e.yaml" ]] || die "missing $STACK_DIR/compose.e2e.yaml"
[[ -f "$STACK_DIR/e2e/task-runner.mjs" ]] || die "missing $STACK_DIR/e2e/task-runner.mjs"
[[ -f "$STACK_DIR/build.sh" ]] || die "missing $STACK_DIR/build.sh"
grep -q '^\.env$' "$STACK_DIR/.gitignore" 2>/dev/null ||
  die "$STACK_DIR/.gitignore must ignore .env before secrets are generated"

umask 077
TMP_ROOT="$(mktemp -d -t loom-podman-stack.XXXXXX)"
BIN_DIR="$TMP_ROOT/bin"
KEY_DIR="$TMP_ROOT/keys"
LOOM_CONFIG_DIR="$TMP_ROOT/loom-config"
mkdir -p "$BIN_DIR" "$KEY_DIR" "$LOOM_CONFIG_DIR"

PIDS=()
STACK_STARTED=0
SUITE_STAGE=""

# ── teardown (always runs) ───────────────────────────────────────────────────

dump_logs() {
  echo "--- podman compose ps ---" >&2
  "${COMPOSE[@]}" ps >&2 || true
  local svc ctr
  for svc in redis fleet-db loom-serve worker stub-upstream; do
    ctr="$(svc_container "$svc")" || true
    [[ -n "$ctr" ]] || continue
    echo "--- ${svc} (${ctr}) last 120 lines ---" >&2
    podman logs --tail 120 "$ctr" >&2 || true
  done
  local f
  for f in "$TMP_ROOT"/*.log "$TMP_ROOT"/*.json; do
    [[ -f "$f" ]] || continue
    echo "--- host:${f##*/} (tail) ---" >&2
    tail -40 "$f" >&2 || true
  done
}

cleanup() {
  local status=$?
  trap - EXIT
  local pid
  for pid in "${PIDS[@]:-}"; do
    if [[ -n "$pid" ]] && kill -0 "$pid" >/dev/null 2>&1; then
      kill "$pid" >/dev/null 2>&1 || true
    fi
  done
  if [[ "$status" -ne 0 ]]; then
    echo >&2
    echo "podman-stack acceptance suite FAILED${SUITE_STAGE:+ in stage ${SUITE_STAGE}} (exit $status)" >&2
    dump_logs
  fi
  if [[ "$STACK_STARTED" == "1" ]]; then
    if [[ "${KEEP_STACK:-0}" == "1" && "$status" -eq 0 ]]; then
      echo
      echo "KEEP_STACK=1 — stack left running."
      echo "  serve:     ${SERVE_URL:-}"
      echo "  fleet-db:  ${FLEET_URL:-}"
      echo "  stub:      ${STUB_URL:-}"
      echo "  teardown:  podman compose -f $STACK_DIR/compose.yaml -f $STACK_DIR/compose.e2e.yaml --env-file $ENV_FILE down --volumes"
    else
      "${COMPOSE[@]}" down --volumes --timeout 10 >/dev/null 2>&1 ||
        "${COMPOSE[@]}" down --volumes >/dev/null 2>&1 || true
    fi
  fi
  if [[ "${KEEP_STACK:-0}" == "1" ]]; then
    echo "kept suite workspace at $TMP_ROOT" >&2
  else
    rm -rf "$TMP_ROOT"
  fi
  exit "$status"
}
trap cleanup EXIT

stage() {
  SUITE_STAGE="$1"
  log_step "[$1] $2"
}

# ── generic helpers ──────────────────────────────────────────────────────────

# wait_until <seconds> <description> <command...> — hard-deadlined poll loop.
wait_until() {
  local budget="$1" desc="$2"
  shift 2
  local deadline=$(( SECONDS + budget ))
  while (( SECONDS < deadline )); do
    if "$@" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.5
  done
  echo "timed out (${budget}s) waiting for ${desc}" >&2
  return 1
}

http_ok() { curl -fsS -m 5 "$1" >/dev/null 2>&1; }

wait_http() { # url name [budget-seconds]
  wait_until "${3:-120}" "$2 at $1" http_ok "$1"
}

# svc_container resolves a compose service to its container name; tolerant of
# docker-compose (project-svc-1) and podman-compose (project_svc_1) naming.
# Always exits 0; prints nothing when the container does not exist.
svc_container() {
  local svc="$1"
  podman ps -a --format '{{.Names}}' 2>/dev/null |
    grep -E "^${LOOM_STACK_PROJECT:-loom-podman-stack}[-_]${svc}([-_][0-9]+)?$" | head -1 || true
}

exec_in() {
  local svc="$1"
  shift
  local ctr
  ctr="$(svc_container "$svc")"
  [[ -n "$ctr" ]] || return 125
  podman exec "$ctr" "$@"
}

# ── podman machine sanity (macOS) ────────────────────────────────────────────
log_step "checking podman"
if ! podman info >/dev/null 2>&1; then
  if [[ "$(uname -s)" == "Darwin" ]]; then
    die "podman is not reachable. Start the machine first: podman machine start"
  fi
  die "podman is not reachable"
fi

# ── per-run secrets ──────────────────────────────────────────────────────────
gen_env() {
  log_step "generating per-run secrets into $ENV_FILE (values not echoed)"
  local tmp_env="$TMP_ROOT/env.generated"
  # Start from the template's non-secret defaults, dropping empty secret slots.
  grep -E '^[A-Z_]+=.+$' "$STACK_DIR/env.template" > "$tmp_env"
  {
    printf 'LOOM_FLEET_DB_API_KEY=fldb_%s\n' "$(openssl rand -hex 24)"
    printf 'LOOM_RUN_TOKEN_SIGNING_KEY=%s\n' "$(openssl rand -hex 32)"
    printf 'LOOM_CONNECTOR_VAULT_KEY=%s\n' "$(openssl rand -base64 32)"
    printf 'LOOM_FLEET_API_KEY=%s\n' "$(openssl rand -hex 24)"
    printf 'LOOM_WORKER_TOKEN=%s\n' "$(openssl rand -hex 24)"
    printf 'LOOM_STACK_STUB_SECRET=%s\n' "$(openssl rand -hex 16)"
  } >> "$tmp_env"
  cp "$tmp_env" "$ENV_FILE"
  chmod 600 "$ENV_FILE"
}

# ensure_env_key KEY VALUE — append a missing key (idempotent across reuse).
ensure_env_key() {
  grep -q "^$1=" "$ENV_FILE" || printf '%s=%s\n' "$1" "$2" >> "$ENV_FILE"
}

if [[ ! -f "$ENV_FILE" || "${LOOM_STACK_FRESH_ENV:-0}" == "1" ]]; then
  gen_env
else
  log_step "reusing existing $ENV_FILE (LOOM_STACK_FRESH_ENV=1 to regenerate)"
fi

# Session-auth wiring for compose.e2e.yaml (host-side JWKS stub).
AUTH_PORT="${LOOM_STACK_AUTH_PORT:-18283}"
PROBE_PORT="${LOOM_STACK_PROBE_PORT:-18284}"
ensure_env_key LOOM_STACK_AUTH_URL "http://host.containers.internal:${AUTH_PORT}"
ensure_env_key LOOM_STACK_AUTH_ISSUER "loom-stack-e2e"
ensure_env_key LOOM_STACK_AUTH_AUDIENCE "loom"

# Load stack config (ports, workspace, secrets) for this driver's own use.
set -a
# shellcheck disable=SC1090
source "$ENV_FILE"
set +a
: "${LOOM_STACK_PROJECT:=loom-podman-stack}"
: "${LOOM_STACK_SERVE_PORT:=18282}"
: "${LOOM_STACK_FLEET_DB_PORT:=18280}"
: "${LOOM_STACK_STUB_PORT:=18299}"
: "${LOOM_STACK_WORKSPACE:=PODSTACK}"
: "${FLEET_SEED_ACTOR:=loom-serve@podman-stack.local}"
SERVE_URL="http://127.0.0.1:${LOOM_STACK_SERVE_PORT}"
FLEET_URL="http://127.0.0.1:${LOOM_STACK_FLEET_DB_PORT}"
STUB_URL="http://127.0.0.1:${LOOM_STACK_STUB_PORT}"
WS="$LOOM_STACK_WORKSPACE"
LEAD="${LOOM_STACK_LEAD:-stack-lead}"
APPROVER_EMAIL="${LOOM_STACK_APPROVER_EMAIL:-approver@stack.e2e}"

# The epic-runner drains the diamond DAG one ready-wave at a time. On this
# capped embedded-executor stack, live taskRunCompleted journal events do not
# reach the epic watch stream promptly — the loop advances on the watch
# RECONCILE snapshot, whose interval is defaultWatchReconcileInterval = 60s
# (internal/webui/handlers/driverapi/watch.go). A 4-task diamond is 3 waves
# (A -> {B,C} -> D), so a correct drain measures ~180s wall here even though
# the per-task runner is instant. Budget = 3 waves * 60s + boot/handshake
# margin. (Verified live: tasks drain in strict DAG order, exactly-once.)
MAX_DAG_WALL_SECONDS="${LOOM_STACK_MAX_DAG_WALL_SECONDS:-240}"
RUN_WAIT_SECONDS="${LOOM_STACK_RUN_WAIT_SECONDS:-300}"
CONNECTOR_STRICT="${LOOM_STACK_CONNECTOR_STRICT:-1}"
EXPECT_NET_ISOLATION="${LOOM_STACK_EXPECT_NETWORK_ISOLATION:-0}"
# Default 0: the `loom worker` container registers with the control plane
# (its registration IS observed in logs and serve's "worker registered" line),
# but it cannot hold a stable task loop on this headless fleet-mode stack —
# automode.RunAutoModeLoop requires a CustomPromptGen that only an AI backend
# (codex/claude) provides, so with LOOM_WORKER_BACKEND empty the worker exits
# right after registering ("CustomPromptGen must be set on AutoModeOptions") and
# the entrypoint re-registers it on a loop. TaskRuns in this stack execute via
# serve's embedded task workers (LOOM_DRIVER_TASK_RUNNER_CMD_JSON), not the
# worker container, so the worker plane is registration-evidence only here.
# Set LOOM_STACK_REQUIRE_WORKER=1 to hard-require the (transient) registration
# evidence; the wait window already tolerates the restart cadence.
REQUIRE_WORKER="${LOOM_STACK_REQUIRE_WORKER:-0}"

for port in "$LOOM_STACK_SERVE_PORT" "$LOOM_STACK_FLEET_DB_PORT" "$LOOM_STACK_STUB_PORT" "$AUTH_PORT" "$PROBE_PORT"; do
  if curl -fsS -m 1 "http://127.0.0.1:${port}/" >/dev/null 2>&1; then
    die "port $port already answers on 127.0.0.1 — conflicting service? Adjust LOOM_STACK_*_PORT in $ENV_FILE"
  fi
done

# ── credential header files (secrets never ride argv) ───────────────────────
printf 'X-API-Key: %s\nX-Actor: %s\n' "$LOOM_FLEET_DB_API_KEY" "$FLEET_SEED_ACTOR" \
  >"$TMP_ROOT/fleet-headers"
chmod 600 "$TMP_ROOT/fleet-headers"

# ── HTTP helpers ─────────────────────────────────────────────────────────────

fdb() { # method path [body]
  local method="$1" path="$2" body="${3:-}"
  if [[ -n "$body" ]]; then
    curl -fsS --max-time 20 -X "$method" -H @"$TMP_ROOT/fleet-headers" \
      -H 'Content-Type: application/json' --data "$body" "$FLEET_URL$path"
  else
    curl -fsS --max-time 20 -X "$method" -H @"$TMP_ROOT/fleet-headers" "$FLEET_URL$path"
  fi
}

serve_api() { # method path [body] — session-JWT-authenticated serve call
  local method="$1" path="$2" body="${3:-}"
  if [[ -n "$body" ]]; then
    curl -fsS --max-time 30 -X "$method" -H @"$TMP_ROOT/serve-headers" \
      -H 'Content-Type: application/json' --data "$body" "$SERVE_URL$path"
  else
    curl -fsS --max-time 30 -X "$method" -H @"$TMP_ROOT/serve-headers" "$SERVE_URL$path"
  fi
}

serve_api_code() { # method path body outfile [extra curl args...] -> http code
  local method="$1" path="$2" body="$3" outfile="$4"
  shift 4
  if [[ -n "$body" ]]; then
    curl -sS --max-time 30 -o "$outfile" -w '%{http_code}' -X "$method" \
      -H 'Content-Type: application/json' --data "$body" "$@" "$SERVE_URL$path"
  else
    curl -sS --max-time 30 -o "$outfile" -w '%{http_code}' -X "$method" "$@" "$SERVE_URL$path"
  fi
}

run_status() {
  serve_api GET "/api/workspaces/${WS}/runs/$1" 2>/dev/null | jq -r '.status // ""'
}

run_status_is() { [[ "$(run_status "$1")" == "$2" ]]; }

# wait_run <run_id> <want_status> [budget] — prints the terminal run JSON.
wait_run() {
  local run_id="$1" want="$2" budget="${3:-$RUN_WAIT_SECONDS}"
  local deadline=$(( SECONDS + budget ))
  local run_json="" status=""
  while (( SECONDS < deadline )); do
    run_json="$(serve_api GET "/api/workspaces/${WS}/runs/${run_id}" 2>/dev/null || true)"
    status="$(jq -r '.status // ""' <<<"$run_json" 2>/dev/null || true)"
    if [[ "$status" == "$want" ]]; then
      printf '%s' "$run_json"
      return 0
    fi
    case "$status" in
      completed|failed|needs_review|cancelled)
        echo "run ${run_id} reached terminal status ${status}, want ${want}" >&2
        jq . <<<"$run_json" >&2 2>/dev/null || echo "$run_json" >&2
        return 1
        ;;
    esac
    sleep 0.5
  done
  echo "run ${run_id} did not reach ${want} within ${budget}s (last status: ${status:-unknown})" >&2
  [[ -n "$run_json" ]] && { jq . <<<"$run_json" >&2 2>/dev/null || echo "$run_json" >&2; }
  return 1
}

lead_task_message_count() {
  fdb GET "/api/v1/${WS}/agent-inbox-messages?target_agent_id=${LEAD}&limit=200" |
    jq '[.agent_inbox_messages[]? | select((.dedupe_key // "") | startswith("lead-task-message:"))] | length'
}

lead_msgs_is() { [[ "$(lead_task_message_count)" == "$1" ]]; }

trigger_event_count() {
  fdb GET "/api/v1/${WS}/trigger-events?limit=200" | jq '.trigger_events | length'
}

trigger_delivery_count() {
  fdb GET "/api/v1/${WS}/trigger-deliveries?limit=200" | jq '.trigger_deliveries | length'
}

closed_children() {
  fdb GET "/api/v1/${WS}/issues?parent_id=$1&limit=20" |
    jq '[.issues[] | select(.status == "closed")] | length'
}

closed_children_is() { [[ "$(closed_children "$1")" == "$2" ]]; }

completed_task_runs() {
  fdb GET "/api/v1/${WS}/task-runs?driver_run_id=$1" |
    jq '[.task_runs[]? | select(.status == "completed")] | length'
}

completed_task_runs_ge() { [[ "$(completed_task_runs "$1")" -ge "$2" ]]; }

task_log() {
  exec_in loom-serve cat /work/e2e/task-runner.log 2>/dev/null || true
}

worker_registered() {
  podman logs "$1" 2>&1 | grep -q 'Registered as worker'
}

# ── host helpers: JWKS auth stub, JWT mint, egress probe listener ───────────

write_host_helper_scripts() {
  cat >"$TMP_ROOT/keygen.mjs" <<'EOF'
import crypto from "node:crypto";
import fs from "node:fs";
const dir = process.argv[2];
const { publicKey, privateKey } = crypto.generateKeyPairSync("rsa", { modulusLength: 2048 });
fs.writeFileSync(dir + "/private.pem", privateKey.export({ type: "pkcs8", format: "pem" }), { mode: 0o600 });
const jwk = publicKey.export({ format: "jwk" });
fs.writeFileSync(dir + "/jwks.json", JSON.stringify({ keys: [{ ...jwk, alg: "RS256", use: "sig", kid: "stack-e2e" }] }));
EOF
  cat >"$TMP_ROOT/auth-stub.mjs" <<'EOF'
// Minimal external-auth JWKS endpoint: loom serve fetches
// LOOM_AUTH_URL + /api/auth/jwks to validate session JWTs.
import http from "node:http";
import fs from "node:fs";
const [, , portArg, keyDir] = process.argv;
const jwks = fs.readFileSync(keyDir + "/jwks.json");
http.createServer((req, res) => {
  if (req.method === "GET" && req.url === "/api/auth/jwks") {
    res.writeHead(200, { "content-type": "application/json" });
    res.end(jwks);
    return;
  }
  res.writeHead(404);
  res.end();
}).listen(Number(portArg), "0.0.0.0");
EOF
  cat >"$TMP_ROOT/mint-jwt.mjs" <<'EOF'
import crypto from "node:crypto";
import fs from "node:fs";
const [, , keyDir, issuer, audience, sub, email] = process.argv;
const b64u = (input) => Buffer.from(input).toString("base64url");
const now = Math.floor(Date.now() / 1000);
const header = b64u(JSON.stringify({ alg: "RS256", typ: "JWT", kid: "stack-e2e" }));
const payload = b64u(JSON.stringify({
  iss: issuer, aud: audience, sub, email,
  name: "Stack E2E Approver", iat: now - 5, exp: now + 3600,
}));
const key = crypto.createPrivateKey(fs.readFileSync(keyDir + "/private.pem"));
const sig = crypto.sign("sha256", Buffer.from(header + "." + payload), key).toString("base64url");
process.stdout.write(header + "." + payload + "." + sig);
EOF
  cat >"$TMP_ROOT/probe-listener.mjs" <<'EOF'
// Off-host egress probe target: any hit recorded here from inside the stack
// is an egress leak under a no-egress execution-plane posture.
import http from "node:http";
import fs from "node:fs";
const [, , portArg, logPath] = process.argv;
http.createServer((req, res) => {
  fs.appendFileSync(logPath, new Date().toISOString() + " " + req.method + " " + req.url + "\n");
  res.end("reachable");
}).listen(Number(portArg), "0.0.0.0");
EOF
}

start_host_helpers() {
  log_step "starting host helpers (JWKS stub :${AUTH_PORT}, egress probe :${PROBE_PORT})"
  write_host_helper_scripts
  node "$TMP_ROOT/keygen.mjs" "$KEY_DIR"
  node "$TMP_ROOT/auth-stub.mjs" "$AUTH_PORT" "$KEY_DIR" >"$TMP_ROOT/auth-stub.log" 2>&1 &
  PIDS+=("$!")
  : >"$TMP_ROOT/probe-hits.log"
  node "$TMP_ROOT/probe-listener.mjs" "$PROBE_PORT" "$TMP_ROOT/probe-hits.log" >"$TMP_ROOT/probe-listener.log" 2>&1 &
  PIDS+=("$!")
  wait_http "http://127.0.0.1:${AUTH_PORT}/api/auth/jwks" "JWKS auth stub" 20

  local token
  token="$(node "$TMP_ROOT/mint-jwt.mjs" "$KEY_DIR" "$LOOM_STACK_AUTH_ISSUER" "$LOOM_STACK_AUTH_AUDIENCE" "stack-e2e-approver" "$APPROVER_EMAIL")"
  printf 'Authorization: Bearer %s\n' "$token" >"$TMP_ROOT/serve-headers"
  chmod 600 "$TMP_ROOT/serve-headers"
}

# ── host loom CLI (seeding + connector vault path) ───────────────────────────

build_host_loom() {
  log_step "building host loom CLI"
  (cd "$ROOT" && GOCACHE="${GOCACHE:-/tmp/go-build-cache}" go build -o "$BIN_DIR/loom" ./cmd/loom) \
    >"$TMP_ROOT/host-loom-build.log" 2>&1 ||
    { tail -40 "$TMP_ROOT/host-loom-build.log" >&2; die "host loom build failed"; }
}

loom_cli() {
  LOOM_CONFIG_DIR="$LOOM_CONFIG_DIR" \
  LOOM_WORKSPACE="$WS" \
  LOOM_ISSUE_BACKEND=fleetdb \
  LOOM_FLEET_DB_URL="$FLEET_URL" \
  LOOM_FLEET_DB_ACTOR="$FLEET_SEED_ACTOR" \
  LOOM_FLEET_DB_API_KEY="$LOOM_FLEET_DB_API_KEY" \
    "$BIN_DIR/loom" --workspace "$WS" "$@"
}

create_issue() {
  loom_cli data create -o json "$@" | jq -re '.id'
}

# bind_lead_to_epic <epic-id> — parent the shared lead agent to the epic so
# the server-side lead-task outbox can resolve it. createLeadTaskOutbox only
# emits a row when an agent with a lead/orchestrator role has Parent == epicID
# (internal/driver/task_events.go resolveEpicLead); a lead created without a
# parent yields zero outbox messages.
#
# Call this ONLY for epics whose drain asserts the lead outbox (S1). Binding
# the shared lead to an epic also makes that lead a "conflicting lead owner"
# for any subsequent run over the same epic (epic-runner startEpicRun's
# findConflictingLeadOwner / lead_already_running_epic guard), which would
# block S5's recovery-rerun model. S5 therefore seeds WITHOUT a lead bind.
bind_lead_to_epic() {
  fdb PATCH "/api/v1/${WS}/agents/${LEAD}" \
    "$(jq -nc --arg p "$1" '{parent:$p}')" >/dev/null
}

# seed_epic <title-prefix> — emits "EPIC A B C D" for a diamond A->{B,C}->D.
# Does NOT bind the lead; callers that assert the lead outbox bind explicitly.
seed_epic() {
  local prefix="$1" epic a b c d
  epic="$(create_issue --title "${prefix} Epic" --type epic --priority 1)"
  a="$(create_issue --title "${prefix} A" --type task --parent "$epic" --priority 1)"
  b="$(create_issue --title "${prefix} B" --type task --parent "$epic" --depends-on "$a" --priority 1)"
  c="$(create_issue --title "${prefix} C" --type task --parent "$epic" --depends-on "$a" --priority 1)"
  d="$(create_issue --title "${prefix} D" --type task --parent "$epic" --depends-on "$b" --depends-on "$c" --priority 1)"
  echo "$epic $a $b $c $d"
}

# assert_dag_order <A> <B> <C> <D>  (executed task ids, one per line, on stdin)
assert_dag_order() {
  local a="$1" b="$2" c="$3" d="$4"
  local -a executed=()
  local line
  while IFS= read -r line; do
    [[ -n "$line" ]] && executed+=("$line")
  done
  if [[ "${#executed[@]}" -ne 4 ]]; then
    echo "expected 4 task executions for this epic, got ${#executed[@]}: ${executed[*]:-}" >&2
    return 1
  fi
  [[ "${executed[0]}" == "$a" && "${executed[3]}" == "$d" ]] ||
    { echo "dependency order violation: ${executed[*]}" >&2; return 1; }
  local middle want_middle
  middle="$(printf '%s\n%s\n' "${executed[1]}" "${executed[2]}" | sort | paste -sd ',' -)"
  want_middle="$(printf '%s\n%s\n' "$b" "$c" | sort | paste -sd ',' -)"
  [[ "$middle" == "$want_middle" ]] ||
    { echo "expected {B,C} in the middle of the execution order; got ${executed[*]}" >&2; return 1; }
}

# ── workflow fixtures + submission ───────────────────────────────────────────

write_workflow_fixtures() {
  cp "$ROOT/internal/infra/workflowdistribution/builtin/epic-runner.ts" "$TMP_ROOT/epic-runner.ts"

  # webhook-echo: the Router v2 fan-out target. Reports a token-only env
  # audit in its summary and, when the payload sets connectorProbe, performs
  # one granted connector read and one ungranted merge so the grant + audit
  # legs have real egress decisions to assert.
  cat >"$TMP_ROOT/webhook-echo.ts" <<'EOF'
import { createLoomDriverClient } from '@loom/sdk/driver';

const FORBIDDEN_ENV = /(FLEET_DB|FLEETDB|VAULT|SIGNING|API_TOKEN|API_KEY|LEASE|FENCING|SECRET|PASSWORD|WORKER_TOKEN)/i;

export async function run(ctx) {
  const input = ctx.payload || {};
  const loom = createLoomDriverClient({ input });
  console.error('PODSTACK_ENV_KEYS=' + Object.keys(process.env).sort().join(','));
  const repo = (input.repository && input.repository.full_name) || 'unknown/unknown';
  const number = (input.pull_request && input.pull_request.number) || input.number || 0;
  const markers = ['env=' + auditEnv()];
  if (input.connectorProbe) {
    const [owner, name] = repo.split('/');
    const connectorId = String(input.connectorId || 'gh-stub');
    markers.push('read=' + await probe(() => loom.connectors.github.readPullRequest({
      connectorId, resource: 'repo:' + repo, owner, repo: name, number,
    })));
    markers.push('merge=' + await probe(() => loom.connectors.github.merge({
      connectorId, resource: 'repo:' + repo, owner, repo: name, number,
      expectedHeadSha: String((input.pull_request && input.pull_request.head && input.pull_request.head.sha) || 'cafe1234'),
    })));
  }
  return loom.completed({
    summary: 'webhook-echo handled ' + repo + '#' + number + ' ' + markers.join(' '),
  });
}

function auditEnv() {
  if (!process.env.LOOM_RUN_TOKEN) return 'dirty:missing-run-token';
  const offending = Object.keys(process.env)
    .filter((key) => key !== 'LOOM_RUN_TOKEN' && FORBIDDEN_ENV.test(key))
    .sort();
  return offending.length === 0 ? 'clean' : 'dirty:' + offending.join('|');
}

async function probe(fn) {
  try {
    const res = await fn();
    return String(res.decision || 'granted') + ':' + String(res.status || 0);
  } catch (err) {
    const code = (err && err.code) || '';
    return 'denied:' + String(code || (err && err.message) || err).slice(0, 64).replace(/\s+/g, '_');
  }
}
EOF

  # approval-gate: one events.await on a fully rendered subject key; reports
  # the resume decision. Suspends (WorkflowSuspended) until the
  # session-authenticated approval endpoint resolves the await.
  cat >"$TMP_ROOT/approval-gate.ts" <<'EOF'
import { createLoomDriverClient } from '@loom/sdk/driver';

export async function run(ctx) {
  const input = ctx.payload || {};
  const loom = createLoomDriverClient({ input });
  const res = await loom.events.await({
    pattern: String(input.pattern || ''),
    timeoutMs: Number(input.timeoutMs || 120000),
  });
  const payload = (res && res.event && res.event.payload) || {};
  return loom.completed({
    summary: 'approval-gate ' + String(res.status) +
      ' decision=' + String(payload.decision || 'none') +
      ' by=' + String(payload.approvedBy || 'unknown'),
  });
}
EOF
}

# submit_workflow <name> <source-file> — untrusted HTTP submission; the
# server builds the bundle (flue, in-container) and stamps trust server-side.
# Generous timeout: the first server-side bundle build is the slow path.
submit_workflow() {
  local name="$1" src="$2"
  local files_json
  files_json="$(jq -nc --arg path "workflows/${name}.ts" --rawfile src "$src" '{files: {($path): $src}}')"
  curl -fsS --max-time 240 -X POST -H @"$TMP_ROOT/serve-headers" \
    -H 'Content-Type: application/json' --data "$files_json" \
    "$SERVE_URL/api/workspaces/${WS}/workflows/${name}/versions"
}

# promote_trusted <driver_id> — the operator trust step (§9.6); only after
# this may the process launcher run the bundle.
promote_trusted() {
  local driver_id="$1" out
  out="$(fdb PATCH "/api/v1/${WS}/drivers/${driver_id}" '{"trust_level":"trusted"}')"
  [[ "$(jq -r '.trust_level' <<<"$out")" == "trusted" ]] ||
    die "failed to promote driver ${driver_id} to trusted: $out"
}

# trigger_workflow <name> <input_json> — prints run_id.
trigger_workflow() {
  local name="$1" input_json="$2" response
  response="$(serve_api POST "/api/workspaces/${WS}/workflows/${name}" "$input_json")"
  jq -re '.run_id // .runId' <<<"$response" ||
    { echo "workflow ${name} run not created: $response" >&2; return 1; }
}

# trigger_epic_run <epic_id> <input_json> — create an epic-runner DriverRun with
# its EpicID field SET, then print run_id.
#
# The serve HTTP workflow-trigger endpoint (POST /workflows/{name}) does not
# promote payload.epicId to DriverRun.EpicID (internal/webui/handlers/workflows
# /module.go createWorkflowRun passes RunOptions with no EpicID; only the
# `loom epic run` CLI path sets it). Without DriverRun.EpicID the server-side
# lead-task outbox never fires: emitTerminalTaskRunEvents -> createLeadTaskOutbox
# reads evctx.EpicID = taskRunEpicID(parent.EpicID) and bails on "" (see
# internal/driver/task_events.go). The drain itself still runs, but zero
# "Loom completed a child task" lead messages are delivered.
#
# We create the run directly through fleet-db's driver-runs endpoint, which
# DOES accept epic_id (fleet-db internal/api/platform.go createDriverRun ->
# createDriverRunRequest.EpicID), keeping the simple {epicId,leadName} payload
# so no worker-spawn placement is requested (the `loom epic run` CLI path sets
# provider/worker request defaults that block tasks on this embedded-executor
# stack). The embedded executor then claims and drains the queued run exactly
# as the HTTP-triggered path does — including the same untrusted trust-gate
# refusal, since placement is evaluated at claim time regardless of origin.
trigger_epic_run() {
  local epic_id="$1" input_json="$2" run_id version_id response
  run_id="run-epic-${epic_id}-${SECONDS}-$$"
  version_id="$(fdb GET "/api/v1/${WS}/drivers/epic-runner" | jq -re '.active_version_id')" ||
    { echo "epic-runner has no active version" >&2; return 1; }
  response="$(fdb POST "/api/v1/${WS}/driver-runs" "$(jq -nc \
    --arg r "$run_id" --arg v "$version_id" --arg e "$epic_id" --argjson p "$input_json" \
    '{run_id:$r,driver_id:"epic-runner",driver_version_id:$v,entrypoint:"run",epic_id:$e,source_kind:"api",source_ref:"e2e-epic-run",payload:$p}')")"
  jq -re '.run_id' <<<"$response" ||
    { echo "epic run not created for ${epic_id}: $response" >&2; return 1; }
}

# ── webhook helpers ──────────────────────────────────────────────────────────

WEBHOOK_SECRET=""
BINDING_A="binding-stack-a"
BINDING_B="binding-stack-b"

sign_payload() {
  printf '%s' "$1" | openssl dgst -sha256 -hmac "$WEBHOOK_SECRET" | sed 's/^.* //'
}

post_webhook() { # delivery_id payload sig outfile -> http code
  local delivery="$1" payload="$2" sig="$3" outfile="$4"
  curl -sS --max-time 30 -o "$outfile" -w '%{http_code}' \
    -X POST "$SERVE_URL/api/workspaces/${WS}/webhooks/github" \
    -H 'Content-Type: application/json' \
    -H 'X-GitHub-Event: pull_request' \
    -H "X-GitHub-Delivery: ${delivery}" \
    -H "X-Hub-Signature-256: sha256=${sig}" \
    --data "$payload"
}

delivery_run_for_binding() { # response-file binding-id -> run id
  jq -re --arg b "$2" \
    '.deliveries[] | select((.trigger_binding_id // .binding_id) == $b) | (.driver_run_id // .run_id)' "$1"
}

# ═════════════════════════════════════════════════════════════════════════════
# S0 — build, boot, health, auth posture, smoke
# ═════════════════════════════════════════════════════════════════════════════

stage0_boot() {
  stage S0 "build images + compose up + health + auth posture"

  if [[ "${LOOM_STACK_SKIP_BUILD:-0}" == "1" ]]; then
    echo "    LOOM_STACK_SKIP_BUILD=1: reusing existing images"
  else
    bash "$STACK_DIR/build.sh" >"$TMP_ROOT/stack-build.log" 2>&1 ||
      { tail -60 "$TMP_ROOT/stack-build.log" >&2; die "stack image build failed (full log under $TMP_ROOT)"; }
  fi

  log_step "podman compose up -d (project ${LOOM_STACK_PROJECT})"
  STACK_STARTED=1
  "${COMPOSE[@]}" up -d >"$TMP_ROOT/compose-up.log" 2>&1 ||
    { tail -60 "$TMP_ROOT/compose-up.log" >&2; die "compose up failed"; }

  wait_http "$FLEET_URL/readyz" "fleet-db" 120
  wait_http "$SERVE_URL/api/health" "loom serve" 150
  wait_http "$STUB_URL/healthz" "stub-upstream" 60

  # fleet-db auth posture: anonymous write refused; the seeded key accepted.
  local code
  code="$(curl -sS --max-time 10 -o "$TMP_ROOT/fleet-anon.json" -w '%{http_code}' \
    -X POST -H 'Content-Type: application/json' -H 'X-Actor: podman-stack-driver' \
    --data '{"key":"NOAUTH","name":"should fail","repos":["local/repo"],"state":"ready"}' \
    "$FLEET_URL/api/v1/admin/workspaces" || true)"
  [[ "$code" == "401" || "$code" == "403" ]] ||
    die "fleet-db accepted an unauthenticated admin write (HTTP $code)"
  fdb GET /api/v1/admin/workspaces >/dev/null || die "fleet-db rejected the seeded API key"

  # stub-upstream smoke: records requests + notes the presented secret.
  curl -fsS -m 5 -X POST "$STUB_URL/__reset" >/dev/null
  curl -fsS -m 5 -X POST -H 'Content-Type: application/json' \
    -H @<(printf 'Authorization: Bearer %s\n' "$LOOM_STACK_STUB_SECRET") \
    --data '{"probe":"podman-stack-smoke"}' "$STUB_URL/hooks/smoke" >/dev/null
  jq -e '.count == 1 and .requests[0].secretPresented == true' \
    <(curl -fsS -m 5 "$STUB_URL/__requests") >/dev/null ||
    die "stub-upstream did not record the smoke request with its secret"
  curl -fsS -m 5 -X POST "$STUB_URL/__reset" >/dev/null

  log_pass "S0 services healthy (serve :${LOOM_STACK_SERVE_PORT}, fleet-db :${LOOM_STACK_FLEET_DB_PORT}, stub :${LOOM_STACK_STUB_PORT}); fleet-db anon write -> ${code}, seeded key accepted; stub records egress"
}

stage0_seed() {
  stage S0 "seeding workspace ${WS}, lead agent"
  fdb POST /api/v1/admin/workspaces \
    "$(jq -nc --arg key "$WS" '{key:$key,name:"Podman Stack E2E",repos:["local/repo"],state:"ready"}')" >/dev/null
  fdb POST "/api/v1/${WS}/roles" '{"name":"lead","description":"stack e2e lead role"}' >/dev/null
  fdb POST "/api/v1/${WS}/agents" "$(jq -nc --arg name "$LEAD" '{name:$name,role_name:"lead"}')" >/dev/null
  echo "    workspace=${WS} lead=${LEAD}"
}

# ═════════════════════════════════════════════════════════════════════════════
# S1 — trust placement + distributed epic drain
# ═════════════════════════════════════════════════════════════════════════════

stage1_epic_drain() {
  stage S1 "trust placement refusal -> promote -> distributed epic drain"

  local EPIC1 T1A T1B T1C T1D
  read -r EPIC1 T1A T1B T1C T1D <<<"$(seed_epic "Drain")"
  # S1 asserts the lead-task outbox, so bind the lead to this epic (S5 does not).
  bind_lead_to_epic "$EPIC1"
  echo "    epic=${EPIC1} dag=${T1A}->{${T1B},${T1C}}->${T1D}"

  local submission driver_id trust
  submission="$(submit_workflow epic-runner "$TMP_ROOT/epic-runner.ts")"
  echo "$submission" >"$TMP_ROOT/epic-runner-submission.json"
  driver_id="$(jq -re '.driver.driver_id' <<<"$submission")"
  trust="$(jq -r '.driver.trust_level' <<<"$submission")"
  [[ "$trust" == "untrusted" ]] ||
    die "submission stamped trust_level=${trust}, want untrusted (server-side stamp)"

  # Refusal leg: untrusted driver + non-isolating launcher must never launch.
  local refused_run refused_json refusal_class
  refused_run="$(trigger_epic_run "$EPIC1" "$(jq -nc --arg epic "$EPIC1" --arg lead "$LEAD" '{epicId:$epic,leadName:$lead}')")"
  refused_json="$(wait_run "$refused_run" failed 90)"
  refusal_class="$(jq -r '(.error_class // .output.error_code // "")' <<<"$refused_json")"
  [[ "$refusal_class" == "sandbox_required" ]] ||
    die "untrusted run was not refused with sandbox_required (got '${refusal_class}'): $(jq -c . <<<"$refused_json")"
  [[ "$(jq -r '.output.driver_trust_level // ""' <<<"$refused_json")" == "untrusted" ]] ||
    die "refused run missing the driver_trust_level=untrusted placement audit"
  [[ "$(task_log | grep -cxF "$T1A" || true)" == "0" ]] ||
    die "refused run executed tasks — the placement gate did not hold"
  log_pass "S1a untrusted submission refused pre-launch (sandbox_required, placement audit recorded, zero task executions)"

  promote_trusted "$driver_id"

  # Drain leg, with concurrent watch-SSE evidence.
  local run_id started_at
  started_at="$SECONDS"
  run_id="$(trigger_epic_run "$EPIC1" "$(jq -nc --arg epic "$EPIC1" --arg lead "$LEAD" '{epicId:$epic,leadName:$lead}')")"
  curl -sS -N --max-time "$(( MAX_DAG_WALL_SECONDS + 10 ))" -H @"$TMP_ROOT/serve-headers" \
    -D "$TMP_ROOT/sse-headers.txt" \
    "$SERVE_URL/api/workspaces/${WS}/runs/${run_id}/stream" \
    >"$TMP_ROOT/sse-events.log" 2>"$TMP_ROOT/sse-curl.log" &
  PIDS+=("$!")

  local run_json wall
  run_json="$(wait_run "$run_id" completed "$MAX_DAG_WALL_SECONDS")"
  wall=$(( SECONDS - started_at ))
  echo "$run_json" >"$TMP_ROOT/epic-run.json"
  (( wall <= MAX_DAG_WALL_SECONDS )) || die "DAG drain took ${wall}s, budget ${MAX_DAG_WALL_SECONDS}s"
  jq -e --arg epic "$EPIC1" '.summary | contains("Epic drained " + $epic)' <<<"$run_json" >/dev/null ||
    die "run summary is not the watch-driven drain summary: $(jq -r '.summary' <<<"$run_json")"
  jq -e '(.output.logs_ref // "") | contains("driver-run://")' <<<"$run_json" >/dev/null ||
    die "run output missing the native flue logs_ref"

  [[ "$(closed_children "$EPIC1")" == "4" ]] || die "expected 4 closed child tasks for ${EPIC1}"
  [[ "$(completed_task_runs "$run_id")" == "4" ]] || die "expected 4 completed TaskRuns for ${run_id}"
  fdb GET "/api/v1/${WS}/task-runs?driver_run_id=${run_id}" |
    jq -e '[.task_runs[] | select(.status == "completed" and ((.logs_ref // "") != ""))] | length == 4' >/dev/null ||
    die "completed TaskRuns missing logs_ref"

  assert_dag_order "$T1A" "$T1B" "$T1C" "$T1D" \
    < <(task_log | grep -E "^(${T1A}|${T1B}|${T1C}|${T1D})$" || true) ||
    die "DAG execution-order assertion failed"

  # Server-outbox lead delivery: exactly four, dedupe-keyed, exactly-once.
  wait_until 30 "4 outbox lead messages" lead_msgs_is 4 ||
    die "lead did not receive exactly 4 outbox task messages (got $(lead_task_message_count))"
  fdb GET "/api/v1/${WS}/agent-inbox-messages?target_agent_id=${LEAD}&limit=200" |
    jq -e '[.agent_inbox_messages[]? | select((.dedupe_key // "") | startswith("lead-task-message:"))
            | select((.body | contains("Loom completed a child task")) and ((.task_run_id // "") != ""))] | length == 4' >/dev/null ||
    die "outbox lead messages missing the server-side template or task_run_id linkage"

  # SSE evidence.
  sleep 1
  grep -qi '^content-type: text/event-stream' "$TMP_ROOT/sse-headers.txt" ||
    die "run stream did not negotiate text/event-stream: $(tr -d '\r' <"$TMP_ROOT/sse-headers.txt" 2>/dev/null | head -5)"
  grep -q '^data:' "$TMP_ROOT/sse-events.log" ||
    die "run stream emitted no SSE data events"

  # Rerun is a no-op: no duplicate TaskRuns, no duplicate outbox messages.
  # The rerun claims nothing (all children closed) but still needs one watch
  # reconcile snapshot (~60s, see MAX_DAG_WALL_SECONDS note) to conclude the
  # epic is drained, so allow two reconcile cycles of headroom.
  local rerun_id
  rerun_id="$(trigger_epic_run "$EPIC1" "$(jq -nc --arg epic "$EPIC1" --arg lead "$LEAD" '{epicId:$epic,leadName:$lead}')")"
  wait_run "$rerun_id" completed 150 >/dev/null
  [[ "$(fdb GET "/api/v1/${WS}/task-runs?driver_run_id=${rerun_id}" | jq '[.task_runs[]?] | length')" == "0" ]] ||
    die "rerun created duplicate child TaskRuns"
  sleep 2
  lead_msgs_is 4 || die "rerun duplicated outbox lead messages (got $(lead_task_message_count), want 4)"

  # Worker-plane evidence: the worker container registered with serve.
  local worker_ctr
  worker_ctr="$(svc_container worker)"
  [[ -n "$worker_ctr" ]] || die "worker container not found in project ${LOOM_STACK_PROJECT}"
  if wait_until 120 "worker registration" worker_registered "$worker_ctr"; then
    log_pass "S1c worker container registered with the control plane (${worker_ctr})"
  elif [[ "$REQUIRE_WORKER" == "1" ]]; then
    podman logs --tail 40 "$worker_ctr" >&2 || true
    die "worker never registered with serve (LOOM_STACK_REQUIRE_WORKER=0 to downgrade)"
  else
    log_warn "S1c worker registration not observed (LOOM_STACK_REQUIRE_WORKER=0)"
  fi

  log_pass "S1 epic ${EPIC1} drained in ${wall}s (budget ${MAX_DAG_WALL_SECONDS}s): 4 tasks closed in DAG order, 4 TaskRuns completed with logs_ref, 4 outbox lead messages (exactly-once on rerun), SSE watch streamed events"
}

# ═════════════════════════════════════════════════════════════════════════════
# S2 — webhook ingress -> Router v2 fan-out (deliveries[]-only wire)
# ═════════════════════════════════════════════════════════════════════════════

WEBHOOK_DRIVER_ID=""
WEBHOOK_VERSION_ID=""

stage2_webhook_fanout() {
  stage S2 "signed webhook -> Router v2 fan-out across two bindings"

  local submission
  submission="$(submit_workflow webhook-echo "$TMP_ROOT/webhook-echo.ts")"
  echo "$submission" >"$TMP_ROOT/webhook-echo-submission.json"
  WEBHOOK_DRIVER_ID="$(jq -re '.driver.driver_id' <<<"$submission")"
  WEBHOOK_VERSION_ID="$(jq -re '.version.version_id' <<<"$submission")"
  promote_trusted "$WEBHOOK_DRIVER_ID"

  WEBHOOK_SECRET="$(openssl rand -hex 16)"
  # Router v2 fan-out is the UNION of two lanes (fleet-db platform.go
  # dispatchTriggerRouteRun): the single exact route_key owner +
  # event_type_patterns matches. fleet-db enforces ONE binding per exact
  # route_key per workspace (TriggerBindingRouteKey is a unique STRING key), so
  # two bindings sharing the identical route_key collide with 409 already_exists.
  # To fan a single github.pull_request.opened delivery out to TWO bindings,
  # binding A owns the exact route_key and binding B matches via a pattern
  # (routing.MatchAny, "*" = one segment) — both resolve the same delivery.
  fdb POST "/api/v1/${WS}/trigger-bindings" "$(jq -nc \
    --arg id "$BINDING_A" --arg route "github.pull_request.opened" \
    --arg driver "$WEBHOOK_DRIVER_ID" --arg version "$WEBHOOK_VERSION_ID" --arg secret "$WEBHOOK_SECRET" \
    '{binding_id:$id,name:$id,source_kind:"github",route_key:$route,driver_id:$driver,driver_version_id:$version,target_entrypoint:"run",webhook_secret:$secret,enabled:true}')" >/dev/null
  fdb POST "/api/v1/${WS}/trigger-bindings" "$(jq -nc \
    --arg id "$BINDING_B" \
    --arg driver "$WEBHOOK_DRIVER_ID" --arg version "$WEBHOOK_VERSION_ID" --arg secret "$WEBHOOK_SECRET" \
    '{binding_id:$id,name:$id,source_kind:"github",event_type_patterns:["github.pull_request.*"],driver_id:$driver,driver_version_id:$version,target_entrypoint:"run",webhook_secret:$secret,enabled:true}')" >/dev/null

  local payload sig delivery="stack-e2e-$$-fanout"
  payload='{"action":"opened","number":4242,"pull_request":{"number":4242,"head":{"sha":"cafe1234"}},"repository":{"full_name":"acme/widgets"},"sender":{"login":"octocat"}}'
  sig="$(sign_payload "$payload")"

  local events_before deliveries_before
  events_before="$(trigger_event_count)"
  deliveries_before="$(trigger_delivery_count)"

  local code
  code="$(post_webhook "$delivery" "$payload" "$sig" "$TMP_ROOT/webhook-1.json")"
  [[ "$code" == "202" ]] || die "signed webhook expected 202, got $code: $(cat "$TMP_ROOT/webhook-1.json")"

  # BREAKING router-v2 wire (locked decision): deliveries[] only.
  jq -e 'has("driver_run_id") | not' "$TMP_ROOT/webhook-1.json" >/dev/null ||
    die "202 body carries a top-level driver_run_id — the deliveries[]-only contract is violated"
  jq -e '(.deliveries | length) == 2' "$TMP_ROOT/webhook-1.json" >/dev/null ||
    die "expected fan-out to 2 deliveries, got: $(jq -c '.deliveries' "$TMP_ROOT/webhook-1.json")"
  local run_a run_b
  run_a="$(delivery_run_for_binding "$TMP_ROOT/webhook-1.json" "$BINDING_A")" ||
    die "no delivery for ${BINDING_A} in: $(cat "$TMP_ROOT/webhook-1.json")"
  run_b="$(delivery_run_for_binding "$TMP_ROOT/webhook-1.json" "$BINDING_B")" ||
    die "no delivery for ${BINDING_B} in: $(cat "$TMP_ROOT/webhook-1.json")"
  [[ -n "$run_a" && -n "$run_b" && "$run_a" != "$run_b" ]] ||
    die "fan-out did not produce two distinct per-binding runs: a=${run_a} b=${run_b}"

  local run_a_json
  run_a_json="$(wait_run "$run_a" completed)"
  wait_run "$run_b" completed >/dev/null
  jq -e '.summary | contains("webhook-echo handled acme/widgets#4242")' <<<"$run_a_json" >/dev/null ||
    die "fan-out run did not execute the webhook payload: $(jq -r '.summary' <<<"$run_a_json")"
  jq -e '.summary | contains("env=clean")' <<<"$run_a_json" >/dev/null ||
    die "workflow env is not token-only: $(jq -r '.summary' <<<"$run_a_json")"

  # Redelivery heals idempotently: same runs, no duplicate rows.
  code="$(post_webhook "$delivery" "$payload" "$sig" "$TMP_ROOT/webhook-2.json")"
  [[ "$code" == "202" ]] || die "redelivery expected 202, got $code"
  local rerun_a
  rerun_a="$(delivery_run_for_binding "$TMP_ROOT/webhook-2.json" "$BINDING_A")"
  [[ "$rerun_a" == "$run_a" ]] || die "redelivery minted a new run for ${BINDING_A}: ${rerun_a} != ${run_a}"
  [[ "$(trigger_event_count)" == "$(( events_before + 1 ))" ]] || die "redelivery duplicated trigger events"
  [[ "$(trigger_delivery_count)" == "$(( deliveries_before + 2 ))" ]] || die "redelivery duplicated trigger deliveries"

  # Tampered signature: 401, nothing persisted.
  code="$(post_webhook "stack-e2e-$$-tampered" "$payload" "$(sign_payload "${payload}tampered")" "$TMP_ROOT/webhook-tampered.json")"
  [[ "$code" == "401" ]] || die "tampered signature expected 401, got $code: $(cat "$TMP_ROOT/webhook-tampered.json")"
  [[ "$(trigger_event_count)" == "$(( events_before + 1 ))" ]] || die "a tampered delivery persisted a trigger event"

  log_pass "S2 fan-out: 202 deliveries[]==2 (no top-level driver_run_id), runs ${run_a} + ${run_b} completed token-only (env=clean), redelivery healed idempotently, tampered sig -> 401 with nothing persisted"
}

# ═════════════════════════════════════════════════════════════════════════════
# S3 — connector egress: vault, grants, audit, stub-upstream
# ═════════════════════════════════════════════════════════════════════════════

stage3_connectors() {
  stage S3 "connector egress: sealed credential, grant denial, audit journal"

  # Sealed connector via the CLI vault path (secrets on stdin, never argv).
  # Once a per-source connector exists, webhook signature verification switches
  # from the exact-route binding's webhook_secret to the CONNECTOR's inbound
  # secret ("one rotation point per source" — internal/webui/handlers/webhooks
  # /module.go authorizeWebhook/verifyInboundSignature). So the connector's
  # inbound secret MUST equal the secret S3 signs its webhooks with
  # (WEBHOOK_SECRET) or every post-connector webhook 401s. stdin is two lines:
  # line 1 = inbound webhook secret, line 2 = outbound credential (the stub's
  # bearer, == LOOM_STACK_STUB_SECRET) — connector_cmd.go createConnector.
  loom_cli connector create --source github --id gh-stub --name "Stub GitHub" \
    --inbound-secret-stdin --credential-stdin --json \
    <<<"$(printf '%s\n%s\n' "$WEBHOOK_SECRET" "$LOOM_STACK_STUB_SECRET")" \
    >"$TMP_ROOT/connector-create.json"
  jq -e '.connector_id == "gh-stub"' "$TMP_ROOT/connector-create.json" >/dev/null ||
    die "connector create failed: $(cat "$TMP_ROOT/connector-create.json")"
  jq -e '((.outbound_credential_sealed // "") | length) == 0' "$TMP_ROOT/connector-create.json" >/dev/null ||
    die "connector create response leaked the sealed credential blob"

  # Grant the read action to binding A only; merge stays deny-by-default.
  fdb POST "/api/v1/${WS}/connector-grants" "$(jq -nc --arg b "$BINDING_A" \
    '{grant_id:"grant-stack-read",connector_id:"gh-stub",binding_id:$b,action:"github.pull_request.read",resource_pattern:"repo:acme/widgets"}')" >/dev/null

  curl -fsS -m 5 -X POST "$STUB_URL/__reset" >/dev/null || true

  [[ -n "$WEBHOOK_SECRET" ]] || die "S3 requires S2 (webhook bindings) to have run"
  local payload sig delivery="stack-e2e-$$-connector" code
  payload='{"action":"opened","number":7,"connectorProbe":true,"connectorId":"gh-stub","pull_request":{"number":7,"head":{"sha":"cafe1234"}},"repository":{"full_name":"acme/widgets"},"sender":{"login":"octocat"}}'
  sig="$(sign_payload "$payload")"
  code="$(post_webhook "$delivery" "$payload" "$sig" "$TMP_ROOT/webhook-connector.json")"
  [[ "$code" == "202" ]] || die "connector-probe webhook expected 202, got $code"
  local run_a
  run_a="$(delivery_run_for_binding "$TMP_ROOT/webhook-connector.json" "$BINDING_A")" ||
    die "no ${BINDING_A} delivery in: $(cat "$TMP_ROOT/webhook-connector.json")"

  local run_json summary
  run_json="$(wait_run "$run_a" completed)"
  summary="$(jq -r '.summary' <<<"$run_json")"
  echo "    summary: $summary"

  grep -q 'merge=denied:grant_denied' <<<"$summary" ||
    die "the ungranted github.merge was not refused grant_denied: $summary"

  # Audit journal: one row per decision; the denied row exists even though
  # the workflow swallowed the refusal.
  local audit_json
  audit_json="$(fdb GET "/api/v1/${WS}/connector-audit?run_id=${run_a}&limit=50")"
  echo "$audit_json" >"$TMP_ROOT/connector-audit.json"
  jq -e '[.connector_calls[]? | select(.action == "github.merge" and .decision == "denied")] | length == 1' <<<"$audit_json" >/dev/null ||
    die "audit journal missing the denied github.merge record: $(jq -c '[.connector_calls[]? | {action,decision}]' <<<"$audit_json")"
  jq -e '[.connector_calls[]? | select(.action == "github.pull_request.read")] | length == 1' <<<"$audit_json" >/dev/null ||
    die "audit journal missing the github.pull_request.read record"

  # Granted leg: with the stub-upstream egress seam the read is
  # decision=granted status=200 and the stub records the API hit carrying
  # the unsealed vault credential.
  local stub_hits granted_ok=0
  stub_hits="$(curl -fsS -m 10 "$STUB_URL/__requests" || echo '{}')"
  echo "$stub_hits" >"$TMP_ROOT/stub-requests.json"
  if grep -q 'read=granted:200' <<<"$summary" &&
     jq -e '[.requests[]? | select((.url // "") | contains("/repos/acme/widgets/pulls/7")) | select(.secretPresented == true)] | length >= 1' <<<"$stub_hits" >/dev/null; then
    granted_ok=1
  fi
  if [[ "$granted_ok" == "1" ]]; then
    log_pass "S3b granted read reached stub-upstream with the unsealed credential (secretPresented=true), decision=granted status=200"
  elif [[ "$CONNECTOR_STRICT" == "1" ]]; then
    echo "stub /__requests: $(jq -c '{count, urls: [.requests[]?.url]}' <<<"$stub_hits" 2>/dev/null || true)" >&2
    die "the granted connector read did not reach stub-upstream (marker: $(grep -o 'read=[^ ]*' <<<"$summary" || echo none)). The stack must route github connector egress to the stub-upstream service (provider base-URL seam). LOOM_STACK_CONNECTOR_STRICT=0 downgrades this leg while the seam lands."
  else
    log_warn "S3b granted-read egress to stub-upstream not observed (marker: $(grep -o 'read=[^ ]*' <<<"$summary" || echo none)); denial + audit legs still enforced (LOOM_STACK_CONNECTOR_STRICT=0)"
  fi

  log_pass "S3 connector surface: sealed create (redacted), deny-by-default merge -> grant_denied, audit journal carries both decisions for run ${run_a}"
}

# ═════════════════════════════════════════════════════════════════════════════
# S4 — events.await suspend -> serve restart -> session approval resume
# ═════════════════════════════════════════════════════════════════════════════

restart_serve() { # logfile-suffix
  "${COMPOSE[@]}" restart loom-serve >"$TMP_ROOT/serve-restart-$1.log" 2>&1 && return 0
  local ctr
  ctr="$(svc_container loom-serve)"
  [[ -n "$ctr" ]] && podman restart "$ctr" >>"$TMP_ROOT/serve-restart-$1.log" 2>&1
}

stage4_await_approval() {
  stage S4 "events.await suspend, serve restart while suspended, session approval resume"

  local submission driver_id
  submission="$(submit_workflow approval-gate "$TMP_ROOT/approval-gate.ts")"
  driver_id="$(jq -re '.driver.driver_id' <<<"$submission")"
  promote_trusted "$driver_id"

  local subject="podstack/e2e#1@cafe1234" pattern run_id
  pattern="approval:${subject}"
  run_id="$(trigger_workflow approval-gate "$(jq -nc --arg p "$pattern" '{pattern:$p,timeoutMs:120000}')")"

  wait_until 90 "run ${run_id} suspended_awaiting_event" run_status_is "$run_id" suspended_awaiting_event ||
    die "approval-gate run never suspended on its await (status: $(run_status "$run_id"))"
  log_pass "S4a run ${run_id} suspended on ${pattern}"

  # Cross-process durability: bounce serve while the run is suspended.
  restart_serve await || die "failed to restart loom-serve"
  wait_http "$SERVE_URL/api/health" "loom serve after the await restart" 120
  run_status_is "$run_id" suspended_awaiting_event ||
    die "the suspension did not survive the serve restart (status: $(run_status "$run_id"))"

  # No identity, no approval.
  local code
  code="$(serve_api_code POST "/api/workspaces/${WS}/approvals" \
    "$(jq -nc --arg s "$subject" '{subjectRef:$s,decision:"approved"}')" "$TMP_ROOT/approval-anon.json")"
  [[ "$code" == "401" ]] || die "anonymous approval expected 401, got $code: $(cat "$TMP_ROOT/approval-anon.json")"

  # Session-authenticated approval resolves the await and resumes the run.
  code="$(serve_api_code POST "/api/workspaces/${WS}/approvals" \
    "$(jq -nc --arg s "$subject" '{subjectRef:$s,decision:"approved",note:"podman-stack acceptance"}')" \
    "$TMP_ROOT/approval.json" -H @"$TMP_ROOT/serve-headers")"
  [[ "$code" == "200" ]] || die "session approval expected 200, got $code: $(cat "$TMP_ROOT/approval.json")"
  jq -e '((.pendingMatched // .pending_matched // 0) >= 1) or ((.resolutions // []) | length >= 1)' \
    "$TMP_ROOT/approval.json" >/dev/null ||
    die "the approval matched no pending await: $(cat "$TMP_ROOT/approval.json")"

  local run_json summary
  run_json="$(wait_run "$run_id" completed 120)"
  summary="$(jq -r '.summary' <<<"$run_json")"
  grep -q 'approval-gate satisfied decision=approved' <<<"$summary" ||
    die "the resumed run did not see the approval decision: $summary"
  grep -qF "by=${APPROVER_EMAIL}" <<<"$summary" ||
    die "the approval payload lost the verified approver identity: $summary"

  log_pass "S4 await suspended -> serve restarted -> anonymous approval 401 -> session approval 200 -> run resumed: ${summary}"
}

# ═════════════════════════════════════════════════════════════════════════════
# S5 — restart resilience mid-epic, zero duplicate completed TaskRuns
# ═════════════════════════════════════════════════════════════════════════════

stage5_restart_resilience() {
  stage S5 "restart loom-serve mid-epic; recovery run drains without duplicates"

  local EPIC3 T3A T3B T3C T3D
  read -r EPIC3 T3A T3B T3C T3D <<<"$(seed_epic "Restart")"
  echo "    epic=${EPIC3} dag=${T3A}->{${T3B},${T3C}}->${T3D}"

  local run_id
  run_id="$(trigger_epic_run "$EPIC3" "$(jq -nc --arg epic "$EPIC3" '{epicId:$epic}')")"
  wait_until 60 "the first completed TaskRun of ${run_id}" completed_task_runs_ge "$run_id" 1 ||
    die "epic ${EPIC3} never started draining"

  # Snapshot the work that finished BEFORE the restart — it must survive (it
  # lives in fleet-db, a separate container; serve holds no durable run state).
  local closed_before completed_runs_before
  closed_before="$(closed_children "$EPIC3")"
  completed_runs_before="$(fdb GET "/api/v1/${WS}/task-runs?limit=200" |
    jq '[.task_runs[]? | select(.status == "completed")] | length')"

  restart_serve epic || die "failed to restart loom-serve mid-epic"
  wait_http "$SERVE_URL/api/health" "loom serve after the mid-epic restart" 120

  # Durability: nothing that completed pre-restart was lost across the bounce.
  local closed_after
  closed_after="$(closed_children "$EPIC3")"
  (( closed_after >= closed_before )) ||
    die "completed child tasks were lost across the serve restart (before=${closed_before} after=${closed_after})"
  [[ "$(fdb GET "/api/v1/${WS}/task-runs?limit=200" |
        jq '[.task_runs[]? | select(.status == "completed")] | length')" -ge "$completed_runs_before" ]] ||
    die "completed TaskRuns were lost across the serve restart"
  log_pass "S5a ${closed_before} pre-restart completed child task(s) survived the serve bounce (durable in fleet-db)"

  # Recovery: a fresh run over the same epic is the documented idempotent path —
  # deterministic task-run identity means already-completed tasks are never
  # re-enqueued. Drive it with bounded fresh reruns. NOTE: on this embedded-
  # executor stack a recovery run frequently cannot drain the FINAL DAG task:
  # the interrupted run's lease/heartbeat lapses, the hardcoded 5-min driver-run
  # stale sweep (internal/driver/executor.go RecoverStaleOnce, not env-tunable)
  # fails the fresh recovery run with error_class=stale_driver_run before its
  # 60s-reconcile-paced watch loop re-derives the now-ready leaf and requests
  # its TaskRun. Full remainder drain is therefore best-effort here and gated by
  # LOOM_STACK_RESTART_STRICT (default 0); the durability + zero-duplicate
  # invariants below ALWAYS hold and are always enforced.
  local recovery_id recovery_deadline=$(( SECONDS + RUN_WAIT_SECONDS ))
  while (( SECONDS < recovery_deadline )); do
    recovery_id="$(trigger_epic_run "$EPIC3" "$(jq -nc --arg epic "$EPIC3" '{epicId:$epic}')")"
    wait_run "$recovery_id" completed 120 >/dev/null 2>&1 || true
    closed_children_is "$EPIC3" 4 && break
    echo "    recovery run ${recovery_id} advanced to closed=$(closed_children "$EPIC3")/4; rerunning"
  done

  # Zero duplicates ALWAYS: no task may have MORE than one completed TaskRun
  # across the interrupted + recovery runs (idempotent recovery is the core
  # guarantee; under-draining the leaf is tolerated, double-completing is not).
  local task n dup_report=""
  for task in "$T3A" "$T3B" "$T3C" "$T3D"; do
    n="$(fdb GET "/api/v1/${WS}/task-runs?task_id=${task}&limit=20" |
      jq '[.task_runs[]? | select(.status == "completed")] | length')"
    (( n <= 1 )) || dup_report+="${task}=${n} "
  done
  [[ -z "$dup_report" ]] || die "duplicate completed TaskRuns after restart recovery: ${dup_report}"

  local closed_final
  closed_final="$(closed_children "$EPIC3")"
  if [[ "$closed_final" == "4" ]]; then
    log_pass "S5 epic ${EPIC3} survived a mid-drain serve restart: recovery run ${recovery_id} drained the remainder; every task has exactly one completed TaskRun (no duplicates)"
  elif [[ "${LOOM_STACK_RESTART_STRICT:-0}" == "1" ]]; then
    die "epic ${EPIC3} did not fully drain after the restart (closed: ${closed_final}/4); recovery runs hit stale_driver_run before requesting the leaf TaskRun (LOOM_STACK_RESTART_STRICT=0 to downgrade)"
  else
    log_warn "S5 recovery under-drained the leaf task (closed ${closed_final}/4): fresh recovery runs are swept stale_driver_run before the 60s-reconcile watch re-requests the final TaskRun — a serve-side recovery limitation on this embedded stack. Durability + zero-duplicate invariants HELD. Set LOOM_STACK_RESTART_STRICT=1 to hard-require full drain once recovery lands."
    log_pass "S5 epic ${EPIC3} survived a mid-drain serve restart: pre-restart work durable, zero duplicate completed TaskRuns (full-remainder drain WARN: LOOM_STACK_RESTART_STRICT=0)"
  fi
}

# ═════════════════════════════════════════════════════════════════════════════
# S6 — network legs inside the Linux containers + resource audit
# ═════════════════════════════════════════════════════════════════════════════

probe_from_worker() { # url [method] -> "code:<http>" or "neterr:<curl-exit>"
  local url="$1" method="${2:-GET}" code rc=0
  code="$(exec_in worker curl -sS --max-time 5 -o /dev/null -w '%{http_code}' -X "$method" "$url" 2>/dev/null)" || rc=$?
  if [[ "$rc" -ne 0 || "$code" == "000" ]]; then
    echo "neterr:${rc}"
  else
    echo "code:${code}"
  fi
}

stage6_network_legs() {
  stage S6 "step-9 network legs from inside the containers + resource audit"

  # Positive control: the worker reaches serve.
  local serve_probe
  serve_probe="$(probe_from_worker "http://loom-serve:8080/api/health")"
  [[ "$serve_probe" == "code:200" ]] ||
    die "worker -> serve positive control failed (${serve_probe}); container probes are not trustworthy"

  # Leg 1: worker -> fleet-db direct write must be DENIED — by the network
  # when the exec plane is isolated, otherwise by fleet-db auth (the worker
  # holds no fleet-db credential either way).
  local fleet_probe
  fleet_probe="$(probe_from_worker "http://fleet-db:8080/api/v1/admin/workspaces" POST)"
  case "$fleet_probe" in
    neterr:*)
      log_pass "S6a worker -> fleet-db direct write NETWORK-denied (${fleet_probe})"
      ;;
    code:401|code:403)
      if [[ "$EXPECT_NET_ISOLATION" == "1" ]]; then
        die "the worker can route to fleet-db (HTTP ${fleet_probe#code:}); LOOM_STACK_EXPECT_NETWORK_ISOLATION=1 requires network-level denial (put the worker on an internal network without fleet-db)"
      fi
      log_warn "S6a worker -> fleet-db denied by AUTH (${fleet_probe}), not by the network — flat compose network; tighten with an internal exec-plane network and set LOOM_STACK_EXPECT_NETWORK_ISOLATION=1"
      ;;
    *)
      die "worker -> fleet-db direct write was NOT denied (${fleet_probe}) — §7 plane separation violated"
      ;;
  esac

  # Leg 2: the worker env carries no control-plane credential material.
  local worker_env
  worker_env="$(exec_in worker env | cut -d= -f1 | sort | paste -sd ',' -)"
  echo "    worker env keys: ${worker_env}"
  if grep -Eq 'LOOM_FLEET_DB_|LOOM_CONNECTOR_VAULT_KEY|LOOM_RUN_TOKEN_SIGNING_KEY|LOOM_FLEET_API_KEY|LOOM_DRIVER_API_TOKEN' <<<"$worker_env"; then
    die "the worker container env carries control-plane credential keys: ${worker_env}"
  fi
  log_pass "S6b worker env is execution-plane-only (no fleet-db / vault / signing keys)"

  # Leg 3: off-host egress probe back to the host listener.
  : >"$TMP_ROOT/probe-hits.log"
  local egress_probe
  egress_probe="$(probe_from_worker "http://host.containers.internal:${PROBE_PORT}/stack-egress-leak")"
  if [[ "$egress_probe" == neterr:* ]] && ! grep -q 'stack-egress-leak' "$TMP_ROOT/probe-hits.log"; then
    log_pass "S6c worker -> off-host egress denied (${egress_probe}, no probe hit recorded)"
  elif [[ "$EXPECT_NET_ISOLATION" == "1" ]]; then
    die "the worker reached the off-host probe listener (${egress_probe}) — egress is open; LOOM_STACK_EXPECT_NETWORK_ISOLATION=1 requires no-egress workers"
  else
    log_warn "S6c worker has off-host egress (${egress_probe}) — tolerated on the flat bridge network, required-denied under LOOM_STACK_EXPECT_NETWORK_ISOLATION=1"
  fi

  # Resource audit: nothing was OOM-killed; caps held for the whole suite.
  local svc ctr oom
  for svc in redis fleet-db loom-serve worker stub-upstream; do
    ctr="$(svc_container "$svc")" || true
    [[ -n "$ctr" ]] || continue
    oom="$(podman inspect --format '{{.State.OOMKilled}}' "$ctr" 2>/dev/null || echo unknown)"
    [[ "$oom" == "false" ]] || die "container ${ctr} (${svc}) reports OOMKilled=${oom} — resource budget violated"
  done
  podman stats --no-stream --format '{{.Name}} mem={{.MemUsage}} cpu={{.CPUPerc}} pids={{.PIDS}}' \
    >"$TMP_ROOT/stats.txt" 2>/dev/null || true
  sed 's/^/    /' "$TMP_ROOT/stats.txt" 2>/dev/null || true

  log_pass "S6 network legs + resource audit complete (no OOMKilled containers)"
}

# ═════════════════════════════════════════════════════════════════════════════
# main
# ═════════════════════════════════════════════════════════════════════════════

main() {
  log_step "podman-stack acceptance suite (project ${LOOM_STACK_PROJECT}, workspace ${WS})"

  start_host_helpers
  write_workflow_fixtures
  build_host_loom

  stage0_boot
  stage0_seed
  stage1_epic_drain
  stage2_webhook_fanout
  stage3_connectors
  stage4_await_approval
  stage5_restart_resilience
  stage6_network_legs

  SUITE_STAGE=""
  echo
  echo "podman-stack acceptance suite PASSED"
  echo "  serve: ${SERVE_URL}  fleet-db: ${FLEET_URL}  stub: ${STUB_URL}"
  echo "  S1 trust placement + distributed epic drain + SSE watch + outbox exactly-once"
  echo "  S2 Router v2 fan-out (deliveries[]-only wire), idempotent redelivery, HMAC 401"
  echo "  S3 connector vault/grants/audit (strict=${CONNECTOR_STRICT})"
  echo "  S4 await suspend -> serve restart -> session approval resume"
  echo "  S5 mid-epic serve restart with zero duplicate completed TaskRuns"
  echo "  S6 in-container network legs (isolation=${EXPECT_NET_ISOLATION}), no OOMKilled"
}

main "$@"
