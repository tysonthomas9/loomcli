#!/usr/bin/env bash
set -Eeuo pipefail

# §7 step-9 acceptance gate (SP3): an UNTRUSTED user-submitted workflow runs in
# the container sandbox holding ONLY the run-scoped token, drives real work
# through the published SDK against loom serve, and every forbidden path is
# observed as a denial:
#
#   1. env audit inside the sandbox — no LOOM_FLEET_DB_*, no
#      LOOM_DRIVER_API_TOKEN, no lease/fencing identity, only LOOM_RUN_TOKEN;
#   2. claim-ready -> exec-task -> await -> complete via @loom/sdk, token-only;
#   3. a direct fleet-db HTTP write FAILS (network-denied under podman
#      serve-only egress; 401 unauthenticated otherwise — the workflow holds
#      no fleet-db identity material either way);
#   4. a driver-op impersonating another run FAILS 401 identity_mismatch
#      (token bound to run);
#   5. an off-host egress probe FAILS under podman serve-only egress.
#
# Sandbox runtime selection (the real engine is env-gated):
#   LOOM_STEP9_SANDBOX=podman  use real rootless podman (native Linux, e.g.
#                              loom-dev per the deploy runbook). Asserts the
#                              network-isolation legs (3: network, 5).
#   LOOM_STEP9_SANDBOX=fake    use the embedded fake engine: the container
#                              command runs as a host process under env -i
#                              with EXACTLY the launcher-provided env, through
#                              the real container-launcher code path
#                              (env-file, serve-only relay + forwarder).
#                              Isolation legs are reported but not enforced.
#   unset                      podman when `podman info` works on Linux,
#                              fake otherwise (macOS podman-machine cannot
#                              cross the host unix-socket relay boundary).
#
# Requirements: go, jq, curl, node, redis-server, and the real Flue CLI
# (`flue` on PATH or LOOM_REAL_FLUE_CMD / LOOM_REAL_FLUE_CMD_JSON) — the
# workflow bundle is built SERVER-SIDE by the untrusted submission endpoint.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
. "$ROOT/scripts/lib/sandbox.sh"
FLEET_DB_REPO="${FLEET_DB_REPO:-$(cd "$ROOT/../fleet-db" 2>/dev/null && pwd || true)}"
FLUE_REPO="${FLUE_REPO:-$(cd "$ROOT/../flue" 2>/dev/null && pwd || true)}"

WORKSPACE="${LOOM_STEP9_WORKSPACE:-STEP9}"
WORKFLOW_NAME="step9-sandbox"
REDIS_PORT="${REDIS_PORT:-16489}"
FLEET_PORT="${FLEET_PORT:-18197}"
LOOM_PORT="${LOOM_PORT:-18198}"
PROBE_PORT="${PROBE_PORT:-18199}"
FLEET_URL="http://127.0.0.1:${FLEET_PORT}"
LOOM_URL="http://127.0.0.1:${LOOM_PORT}"

loom_mktemp_dir test-step9-sandbox; TMP_ROOT="$LOOM_SANDBOX_DIR"
BIN_DIR="$TMP_ROOT/bin"
WORKDIR="$TMP_ROOT/work"
LOOM_CONFIG_DIR="$TMP_ROOT/loom-config"
TASK_RUNNER_LOG="$TMP_ROOT/task-runner.log"
mkdir -p "$BIN_DIR" "$WORKDIR" "$LOOM_CONFIG_DIR"

PIDS=()

log_step() { printf '\n==> %s\n' "$1"; }

die() {
  echo "ERROR: $*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

cleanup() {
  local status=$?
  for pid in "${PIDS[@]:-}"; do
    if [[ -n "$pid" ]] && kill -0 "$pid" >/dev/null 2>&1; then
      kill "$pid" >/dev/null 2>&1 || true
    fi
  done
  if [[ "$status" -ne 0 ]]; then
    echo
    echo "step-9 sandbox gate FAILED; logs are under $TMP_ROOT" >&2
    for log in "$TMP_ROOT"/*.log; do
      [[ -f "$log" ]] || continue
      echo "--- ${log##*/} ---" >&2
      tail -80 "$log" >&2 || true
    done
  elif [[ "${KEEP_STEP9_E2E:-0}" != "1" ]]; then
    rm -rf "$TMP_ROOT"
  else
    echo "kept step-9 workspace at $TMP_ROOT"
  fi
}
trap cleanup EXIT INT TERM

curl_fleet() {
  local method="$1" path="$2" body="${3:-}"
  if [[ -n "$body" ]]; then
    curl -fsS -X "$method" -H 'Content-Type: application/json' \
      -H 'X-Actor: step9-gate' --data "$body" "$FLEET_URL$path"
  else
    curl -fsS -X "$method" -H 'X-Actor: step9-gate' "$FLEET_URL$path"
  fi
}

wait_http() {
  local url="$1" name="$2"
  for _ in $(seq 1 120); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.25
  done
  die "timed out waiting for $name at $url"
}

resolve_sandbox_mode() {
  SANDBOX_MODE="${LOOM_STEP9_SANDBOX:-}"
  if [[ -z "$SANDBOX_MODE" ]]; then
    if [[ "$(uname -s)" == "Linux" ]] && command -v podman >/dev/null 2>&1 && podman info >/dev/null 2>&1; then
      SANDBOX_MODE="podman"
    else
      SANDBOX_MODE="fake"
    fi
  fi
  case "$SANDBOX_MODE" in
    podman)
      command -v podman >/dev/null 2>&1 || die "LOOM_STEP9_SANDBOX=podman but podman is not installed"
      podman info >/dev/null 2>&1 || die "LOOM_STEP9_SANDBOX=podman but podman is not functional"
      SANDBOX_BINARY="podman"
      ;;
    fake)
      write_fake_engine
      SANDBOX_BINARY="$TMP_ROOT/fake-podman"
      ;;
    *)
      die "LOOM_STEP9_SANDBOX must be 'podman' or 'fake', got '$SANDBOX_MODE'"
      ;;
  esac
  log_step "sandbox runtime: $SANDBOX_MODE ($SANDBOX_BINARY)"
}

# write_fake_engine emits a stand-in container engine: `run` executes the
# launcher-built command as a host process under `env -i` with exactly the
# --env-file env plus name-only --env passthrough keys — the same env surface
# a real container would see — so the gate exercises the REAL container
# launcher (env-file split, serve-only relay socket + loopback forwarder)
# without podman. It cannot enforce --network=none; the network-denial legs
# are asserted only under the real engine.
write_fake_engine() {
  local node_bin
  node_bin="$(command -v node)" || die "node is required for the fake sandbox engine"
  cat >"$TMP_ROOT/fake-podman" <<EOF
#!/usr/bin/env bash
set -euo pipefail
if [[ "\${1:-}" == "rm" ]]; then exit 0; fi
[[ "\${1:-}" == "run" ]] || { echo "fake-podman: unsupported subcommand \${1:-}" >&2; exit 64; }
args=("\$@")
n=\${#args[@]}
i=1
envfile=""
workdir=""
passkeys=()
cmd=()
while (( i < n )); do
  a="\${args[\$i]}"
  case "\$a" in
    --rm|-i|--read-only|--network=*) i=\$((i+1)) ;;
    --name|--memory|--cpus|--pids-limit|--security-opt|--mount|--runtime|--network) i=\$((i+2)) ;;
    --workdir) workdir="\${args[\$((i+1))]}"; i=\$((i+2)) ;;
    --env-file) envfile="\${args[\$((i+1))]}"; i=\$((i+2)) ;;
    --env) passkeys+=("\${args[\$((i+1))]}"); i=\$((i+2)) ;;
    *) break ;;
  esac
done
i=\$((i+1)) # skip the image ref
while (( i < n )); do cmd+=("\${args[\$i]}"); i=\$((i+1)); done
[[ -n "\$envfile" && -f "\$envfile" ]] || { echo "fake-podman: missing --env-file" >&2; exit 65; }
[[ "\${#cmd[@]}" -ge 2 ]] || { echo "fake-podman: missing container command" >&2; exit 66; }
envargs=()
while IFS= read -r line; do
  [[ -n "\$line" ]] && envargs+=("\$line")
done < "\$envfile"
for key in "\${passkeys[@]:-}"; do
  [[ -n "\${key:-}" ]] && envargs+=("\$key=\${!key-}")
done
[[ "\${cmd[0]}" == "node" ]] && cmd[0]="$node_bin"
cd "\$workdir"
exec env -i "\${envargs[@]}" "\${cmd[@]}"
EOF
  chmod +x "$TMP_ROOT/fake-podman"
}

check_prerequisites() {
  require_cmd go
  require_cmd jq
  require_cmd curl
  require_cmd node
  require_cmd redis-server
  if [[ -z "${LOOM_REAL_FLUE_CMD:-}" && -z "${LOOM_REAL_FLUE_CMD_JSON:-}" ]] && ! command -v flue >/dev/null 2>&1; then
    die "real Flue CLI not found; install flue or set LOOM_REAL_FLUE_CMD / LOOM_REAL_FLUE_CMD_JSON"
  fi
  [[ -n "$FLEET_DB_REPO" && -d "$FLEET_DB_REPO/cmd/fleet-db" ]] ||
    die "fleet-db repo not found; set FLEET_DB_REPO=/path/to/fleet-db"
  [[ -n "$FLUE_REPO" && -d "$FLUE_REPO/packages/runtime" ]] ||
    die "Flue repo not found (the server-side build resolves @flue/runtime deps); set FLUE_REPO=/path/to/flue"
}

# prepare_workdir links the Flue runtime deps into serve's working directory:
# the untrusted submission endpoint builds the bundle in a temp project under
# $WORKDIR/.loom/workflow-builds, and node resolution walks up to
# $WORKDIR/node_modules for @flue/runtime's server deps. The BUILT bundle is
# self-contained — nothing here is reachable from inside the sandbox.
prepare_workdir() {
  mkdir -p "$WORKDIR/node_modules/@flue" "$WORKDIR/node_modules/@hono"
  ln -sfn "$FLUE_REPO/packages/runtime" "$WORKDIR/node_modules/@flue/runtime"
  ln -sfn "$FLUE_REPO/packages/runtime/node_modules/@hono/node-server" "$WORKDIR/node_modules/@hono/node-server"
  ln -sfn "$FLUE_REPO/packages/runtime/node_modules/hono" "$WORKDIR/node_modules/hono"
}

build_binaries() {
  log_step "building fleet-db and loom"
  (cd "$FLEET_DB_REPO" && GOCACHE="${GOCACHE:-/tmp/go-build-cache}" go build -o "$BIN_DIR/fleet-db" ./cmd/fleet-db)
  (cd "$ROOT" && GOCACHE="${GOCACHE:-/tmp/go-build-cache}" go build -o "$BIN_DIR/loom" ./cmd/loom)
}

start_services() {
  log_step "starting Redis on ${REDIS_PORT}"
  redis-server --port "$REDIS_PORT" --save "" --appendonly no >"$TMP_ROOT/redis.log" 2>&1 &
  PIDS+=("$!")
  sleep 0.5

  # Auth ENABLED (dev-mode identity = X-Actor): an unauthenticated direct call
  # — which is all a credential-stripped workflow can make — is 401.
  log_step "starting fleet-db on ${FLEET_PORT} (auth enabled, dev-mode identity)"
  "$BIN_DIR/fleet-db" \
    -addr "127.0.0.1:${FLEET_PORT}" \
    -backend redis \
    -redis-addr "127.0.0.1:${REDIS_PORT}" \
    -redis-durability-profile managed \
    -redis-max-retries 0 \
    -redis-cb-fail-threshold 0 \
    -auth-enabled=true \
    -auth-dev-mode \
    -authz-enabled=false \
    -rpc-enabled=false \
    -log-format text \
    >"$TMP_ROOT/fleet-db.log" 2>&1 &
  PIDS+=("$!")
  wait_http "$FLEET_URL/healthz" "fleet-db"

  log_step "starting off-host egress probe listener on ${PROBE_PORT}"
  node -e "require('node:http').createServer((req,res)=>{res.end('reachable')}).listen(${PROBE_PORT},'127.0.0.1')" \
    >"$TMP_ROOT/probe.log" 2>&1 &
  PIDS+=("$!")
}

seed_epic() {
  log_step "seeding workspace and single-task epic"
  curl_fleet POST /api/v1/admin/workspaces \
    "$(jq -nc --arg key "$WORKSPACE" '{key:$key,name:"Step9 Sandbox Gate",repos:["local/repo"],state:"ready"}')" >/dev/null

  export LOOM_CONFIG_DIR
  export LOOM_WORKSPACE="$WORKSPACE"
  export LOOM_FLEET_DB_URL="$FLEET_URL"
  export LOOM_FLEET_DB_ACTOR="step9-gate"

  EPIC_ID="$("$BIN_DIR/loom" --workspace "$WORKSPACE" data create -o json --title "Step9 Epic" --type epic --priority 1 | jq -r '.id')"
  TASK_ID="$("$BIN_DIR/loom" --workspace "$WORKSPACE" data create -o json --title "Step9 Task" --type task --parent "$EPIC_ID" --priority 1 | jq -r '.id')"
  echo "    epic=${EPIC_ID} task=${TASK_ID}"

  curl_fleet POST "/api/v1/${WORKSPACE}/nodes" \
    "$(jq -nc '{node_id:"step9-node",owner_actor:"step9-gate",runtime_provider:"local",drain_state:"active",capacity:8,ttl_seconds:300}')" >/dev/null
}

write_task_runner_stub() {
  cat >"$TMP_ROOT/task-runner.mjs" <<EOF
#!/usr/bin/env node
import fs from 'node:fs';
const request = JSON.parse(process.env.LOOM_TASK_RUN_REQUEST_JSON || '{}');
fs.appendFileSync($(jq -nc --arg p "$TASK_RUNNER_LOG" '$p'), request.task_id + '\n');
console.log(JSON.stringify({
  status: 'completed',
  exitCode: 0,
  logsRef: 'logs://' + request.task_run_id,
}));
EOF
}

start_loom_serve() {
  write_task_runner_stub
  local task_runner_cmd_json signing_key
  task_runner_cmd_json="$(jq -nc --arg node "$(command -v node)" --arg runner "$TMP_ROOT/task-runner.mjs" '[$node,$runner]')"
  signing_key="$(node -e "console.log(require('node:crypto').randomBytes(32).toString('hex'))")"

  log_step "starting loom serve on ${LOOM_PORT} (container sandbox, §9.5 lockdown)"
  (
    cd "$WORKDIR"
    LOOM_DISABLE_H2C=1 \
    LOOM_DRIVER_EXECUTOR=1 \
    LOOM_DRIVER_EXECUTOR_NODE_ID=step9-node \
    LOOM_DRIVER_LEGACY_AUTH_ENV=0 \
    LOOM_DRIVER_SANDBOX=container \
    LOOM_DRIVER_SANDBOX_BINARY="$SANDBOX_BINARY" \
    LOOM_DRIVER_API_TOKEN="step9-ops-static-bearer-must-never-reach-workflows" \
    LOOM_RUN_TOKEN_SIGNING_KEY="$signing_key" \
    LOOM_DRIVER_TASK_RUNNER_CMD_JSON="$task_runner_cmd_json" \
    LOOM_SDK_ROOT="$ROOT/sdk" \
    exec "$BIN_DIR/loom" serve --port "$LOOM_PORT" --frontend-url "http://127.0.0.1:9"
  ) >"$TMP_ROOT/loom-serve.log" 2>&1 &
  PIDS+=("$!")
  wait_http "$LOOM_URL/health" "loom serve"
}

# submit_untrusted_workflow registers the bundle through the EXTERNAL HTTP
# submission endpoint, which stamps trust server-side: untrusted, always.
submit_untrusted_workflow() {
  log_step "submitting workflow through the untrusted HTTP submission path"
  write_step9_workflow
  local files_json response trust
  files_json="$(jq -nc --rawfile src "$TMP_ROOT/step9-workflow.ts" \
    '{files: {("workflows/" + "'"$WORKFLOW_NAME"'" + ".ts"): $src}}')"
  response="$(curl -fsS -X POST -H 'Content-Type: application/json' \
    --data "$files_json" \
    "$LOOM_URL/api/workspaces/${WORKSPACE}/workflows/${WORKFLOW_NAME}/versions")"
  echo "$response" >"$TMP_ROOT/submission.json"
  trust="$(jq -r '.driver.trust_level // ""' <<<"$response")"
  [[ "$trust" == "untrusted" ]] ||
    die "submitted driver trust_level = '$trust', want 'untrusted' (server-side stamp)"
  echo "    driver=$(jq -r '.driver.driver_id' <<<"$response") trust=$trust"
}

write_step9_workflow() {
  cat >"$TMP_ROOT/step9-workflow.ts" <<'EOF'
import { createLoomDriverClient } from '@loom/sdk/driver';

// Step-9 acceptance workflow: prove the positive path works token-only and
// observe every forbidden path as a denial, reporting machine-readable
// markers in the run summary + STEP9_* stderr lines.
const FORBIDDEN_ENV = /(FLEET_DB|FLEETDB|API_TOKEN|API_KEY|LEASE|FENCING|SIGNING|SECRET|PASSWORD|ACCESS_KEY|WORKER_TOKEN)/i;

export async function run(ctx) {
  const input = ctx.payload || {};
  const loom = createLoomDriverClient({ input });
  console.error('STEP9_ENV_KEYS=' + Object.keys(process.env).sort().join(','));

  const checks = {};
  checks.env = auditEnv();
  checks.task = await completeOneTask(loom, input);
  checks.fleetdb = await expectDenied(() =>
    postJSON(String(input.fleetDbUrl || '') + '/api/v1/' + loom.workspace + '/issues',
      { title: 'forged by sandboxed workflow', issue_type: 'task' }));
  checks.foreign = await foreignRunDenied(loom, String(input.foreignRunId || 'run-foreign'));
  checks.egress = await expectDenied(() => postJSON(String(input.egressProbeUrl || ''), {}));

  console.error('STEP9_CHECKS=' + JSON.stringify(checks));
  const summary = 'step9 env=' + checks.env + ' task=' + checks.task +
    ' fleetdb=' + checks.fleetdb + ' foreign=' + checks.foreign + ' egress=' + checks.egress;
  const violations = [];
  if (checks.env !== 'clean') violations.push('env');
  if (!String(checks.task).endsWith(':completed')) violations.push('task');
  if (!String(checks.fleetdb).startsWith('denied')) violations.push('fleetdb');
  if (checks.foreign !== 'denied') violations.push('foreign');
  if (violations.length > 0) {
    return loom.failed({ summary: 'step9 gate violated [' + violations.join(',') + '] ' + summary, errorClass: 'step9_gate_violation' });
  }
  return loom.completed({ summary });
}

function auditEnv() {
  const offending = Object.keys(process.env)
    .filter((key) => key !== 'LOOM_RUN_TOKEN' && FORBIDDEN_ENV.test(key));
  if (!process.env.LOOM_RUN_TOKEN) return 'dirty:missing-run-token';
  if (process.env.LOOM_DRIVER_API_TOKEN) offending.push('LOOM_DRIVER_API_TOKEN');
  return offending.length === 0 ? 'clean' : 'dirty:' + offending.sort().join('|');
}

// completeOneTask is the positive leg: real work through the serve driver-op
// API holding only LOOM_RUN_TOKEN.
async function completeOneTask(loom, input) {
  const task = await loom.tasks.claimReady({ epicId: input.epicId });
  if (!task || !task.id) return 'task-failed:no-ready-task';
  const enqueued = await loom.taskRuns.request({
    taskId: task.id,
    runner: 'local-task-runner',
  });
  const taskRunId = enqueued.taskRunId || enqueued.id;
  if (!taskRunId) return 'task-failed:no-task-run-id';
  const result = await loom.taskRuns.await({ taskRunId, pollMs: 500, timeoutMs: 120000 });
  if (result.status !== 'completed') return 'task-failed:' + (result.errorClass || result.status);
  try {
    await loom.tasks.complete({ taskId: task.id, taskRunId });
  } catch (err) {
    const code = err && err.code;
    if (code !== 'conflict' && code !== 'invalid_transition') {
      return 'task-failed:complete:' + (code || String((err && err.message) || err));
    }
  }
  return task.id + ':completed';
}

async function expectDenied(attempt) {
  try {
    const res = await attempt();
    return res.ok ? 'allowed' : 'denied:http_' + res.status;
  } catch {
    return 'denied:network';
  }
}

function postJSON(url, body) {
  if (!url) throw new Error('no probe url');
  return fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal: AbortSignal.timeout(5000),
  });
}

// foreignRunDenied posts a driver op impersonating another run: the token is
// bound to THIS run, so the server must answer 401 identity_mismatch.
async function foreignRunDenied(loom, foreignRunId) {
  const url = loom.apiUrl + '/api/workspaces/' + encodeURIComponent(loom.workspace) + '/driver/list-agents';
  try {
    const res = await fetch(url, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: 'Bearer ' + loom.runToken,
        'X-Loom-Driver-Run-Id': foreignRunId,
      },
      body: '{}',
      signal: AbortSignal.timeout(10000),
    });
    if (res.status !== 401) return 'allowed:http_' + res.status;
    const body = await res.json().catch(() => ({}));
    const code = (body && body.error && body.error.code) || '';
    return code === 'identity_mismatch' ? 'denied' : 'denied-wrong-code:' + code;
  } catch (err) {
    return 'error:' + String((err && err.message) || err);
  }
}
EOF
}

start_workflow_run() {
  log_step "starting the untrusted workflow run"
  local payload response
  payload="$(jq -nc --arg epic "$EPIC_ID" --arg fleet "$FLEET_URL" --arg probe "http://127.0.0.1:${PROBE_PORT}/probe" \
    '{epicId:$epic, fleetDbUrl:$fleet, egressProbeUrl:$probe, foreignRunId:"run-foreign-step9"}')"
  response="$(curl -fsS -X POST -H 'Content-Type: application/json' --data "$payload" \
    "$LOOM_URL/api/workspaces/${WORKSPACE}/workflows/${WORKFLOW_NAME}")"
  RUN_ID="$(jq -r '.run_id' <<<"$response")"
  [[ -n "$RUN_ID" && "$RUN_ID" != "null" ]] || die "workflow run not created: $response"
  echo "    run=$RUN_ID"
}

wait_for_run() {
  log_step "waiting for run ${RUN_ID}"
  local run_json status
  for _ in $(seq 1 240); do
    run_json="$(curl -fsS "$LOOM_URL/api/workspaces/${WORKSPACE}/runs/${RUN_ID}")"
    status="$(jq -r '.status' <<<"$run_json")"
    case "$status" in
      completed)
        echo "$run_json" >"$TMP_ROOT/run.json"
        return 0
        ;;
      failed|needs_review|cancelled)
        echo "workflow run reached terminal status ${status}" >&2
        jq . <<<"$run_json" >&2
        exit 1
        ;;
    esac
    sleep 0.5
  done
  curl -fsS "$LOOM_URL/api/workspaces/${WORKSPACE}/runs/${RUN_ID}" | jq . >&2 || true
  die "workflow run did not complete in time"
}

# verify_run audits the terminal run row: the workflow's own check markers,
# the §9.6 placement audit (untrusted + container + serve-only egress), and
# the env-key dump captured from the sandbox stderr.
verify_run() {
  log_step "verifying run output and placement audit"
  local run_json summary
  run_json="$(cat "$TMP_ROOT/run.json")"
  summary="$(jq -r '.summary' <<<"$run_json")"
  echo "    summary: $summary"

  grep -q 'env=clean' <<<"$summary" || die "workflow env audit failed: $summary"
  grep -Eq "task=${TASK_ID}:completed" <<<"$summary" || die "workflow did not complete the task via the serve API: $summary"
  grep -Eq 'fleetdb=denied' <<<"$summary" || die "direct fleet-db call was not denied: $summary"
  grep -q 'foreign=denied' <<<"$summary" || die "foreign-run driver op was not denied: $summary"
  if [[ "$SANDBOX_MODE" == "podman" ]]; then
    grep -Eq 'egress=denied' <<<"$summary" || die "off-host egress was not denied under podman serve-only: $summary"
    grep -Eq 'fleetdb=denied:network' <<<"$summary" || die "direct fleet-db call should be network-denied under podman: $summary"
  else
    echo "    (fake runtime: egress leg reported '$(grep -o 'egress=[^ ]*' <<<"$summary")', enforced only under podman)"
  fi

  [[ "$(jq -r '.output.driver_trust_level' <<<"$run_json")" == "untrusted" ]] ||
    die "run output missing driver_trust_level=untrusted audit"
  [[ "$(jq -r '.output.sandbox_launcher' <<<"$run_json")" == "container" ]] ||
    die "run output missing sandbox_launcher=container audit"
  local placement
  placement="$(jq -r '.output.sandbox_placement // ""' <<<"$run_json")"
  [[ -n "$placement" ]] || die "run output missing sandbox_placement"
  [[ "$(jq -r '.provider' <<<"$placement")" == "container" ]] || die "sandbox_placement.provider != container: $placement"
  [[ "$(jq -r '.egress_mode' <<<"$placement")" == "serve-only" ]] || die "sandbox_placement.egress_mode != serve-only: $placement"

  local env_keys
  env_keys="$(jq -r '.output.flue_stderr_tail // ""' <<<"$run_json" | grep -o 'STEP9_ENV_KEYS=[^[:space:]]*' | head -1)"
  [[ -n "$env_keys" ]] || die "sandbox env-key dump not captured in flue_stderr_tail"
  echo "    $env_keys"
  if grep -Eq 'FLEET_DB|FLEETDB|API_TOKEN|LEASE_ID|FENCING|SIGNING_KEY' <<<"$env_keys"; then
    die "forbidden credential key visible inside the sandbox: $env_keys"
  fi
  grep -q 'LOOM_RUN_TOKEN' <<<"$env_keys" || die "LOOM_RUN_TOKEN missing inside the sandbox"
}

verify_fleet_db_state() {
  log_step "verifying fleet-db task state"
  local task_json task_runs_json
  task_json="$(curl_fleet GET "/api/v1/${WORKSPACE}/issues/${TASK_ID}")"
  [[ "$(jq -r '.status' <<<"$task_json")" == "closed" ]] ||
    die "task ${TASK_ID} not closed after workflow completion: $(jq -c '{status}' <<<"$task_json")"
  task_runs_json="$(curl_fleet GET "/api/v1/${WORKSPACE}/task-runs?driver_run_id=${RUN_ID}")"
  [[ "$(jq '[.task_runs[] | select(.status == "completed")] | length' <<<"$task_runs_json")" == "1" ]] ||
    die "expected exactly one completed child TaskRun for ${RUN_ID}"
  [[ -f "$TASK_RUNNER_LOG" ]] && grep -q "$TASK_ID" "$TASK_RUNNER_LOG" ||
    die "stub task runner never executed ${TASK_ID}"
  [[ "$(jq -r '[.issues[]? | select(.title == "forged by sandboxed workflow")] | length' <<<"$(curl_fleet GET "/api/v1/${WORKSPACE}/issues?limit=100")")" == "0" ]] ||
    die "forged issue reached fleet-db — direct-write denial did not hold"
}

main() {
  check_prerequisites
  resolve_sandbox_mode
  prepare_workdir
  build_binaries
  start_services
  seed_epic
  start_loom_serve
  submit_untrusted_workflow
  start_workflow_run
  wait_for_run
  verify_run
  verify_fleet_db_state

  echo
  echo "step-9 sandbox acceptance gate PASSED (runtime: $SANDBOX_MODE)"
  echo "  untrusted submission -> container sandbox -> token-only SDK work"
  echo "  denials observed: fleet-db direct write, foreign-run op$(
    [[ "$SANDBOX_MODE" == "podman" ]] && echo ', off-host egress')"
}

main "$@"
