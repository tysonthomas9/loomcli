#!/usr/bin/env bash
# Real-Flue E2E for the builtin watch-driven epic-runner workflow.
#
# Scenario 1: a 4-task DAG drains through the epic watch stream (no polling
#             cadence; wall time is asserted), the bound lead receives one
#             task-completed inbox message per child FROM THE SERVER OUTBOX
#             (asserted via the outbox dedupe key), and a retry run is a no-op
#             that does not duplicate task runs or lead messages.
# Scenario 2: a deliberately failing branch is retried then parked server-side
#             while sibling branches drain; the run lands in needs_review
#             (epic_tasks_parked) and a rerun stays idempotent.
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FLEET_DB_REPO="${FLEET_DB_REPO:-$(cd "$ROOT/../fleet-db" 2>/dev/null && pwd || true)}"
FLUE_REPO="${FLUE_REPO:-$(cd "$ROOT/../flue" 2>/dev/null && pwd || true)}"

WS="${LOOM_REAL_FLUE_E2E_WORKSPACE:-FLUEE2E}"
RUN_ID="${LOOM_REAL_FLUE_E2E_RUN_ID:-run-real-flue-e2e}"
LEAD_NAME="${LOOM_REAL_FLUE_E2E_LEAD:-e2e-lead}"
REDIS_PORT="${REDIS_PORT:-16379}"
FLEET_PORT="${FLEET_PORT:-18095}"
LOOM_PORT="${LOOM_PORT:-18096}"
FLEET_URL="http://127.0.0.1:${FLEET_PORT}"
LOOM_URL="http://127.0.0.1:${LOOM_PORT}"
# The watch-driven loop reacts to journal events instead of sleeping between
# polls, so the whole DAG must drain well inside this budget (the old
# cadence-based loop burned ~5s per scheduling round).
MAX_DAG_WALL_SECONDS="${LOOM_REAL_FLUE_E2E_MAX_DAG_WALL_SECONDS:-30}"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 2
  fi
}

require_cmd go
require_cmd git
require_cmd jq
require_cmd curl
require_cmd node
require_cmd redis-server

if [[ -z "${LOOM_FLUE_BUILD_CMD:-}" && -z "${LOOM_FLUE_BUILD_CMD_JSON:-}" ]] && ! command -v flue >/dev/null 2>&1; then
  cat >&2 <<'EOF'
missing real Flue CLI.

Install/build Flue so `flue` is on PATH, or set one of:
  LOOM_FLUE_BUILD_CMD=/path/to/flue
  LOOM_FLUE_BUILD_CMD_JSON='["node","/path/to/flue/packages/cli/bin/flue.mjs"]'

This harness intentionally does not fall back to the fake Flue builder.
EOF
  exit 2
fi

if [[ -z "$FLEET_DB_REPO" || ! -d "$FLEET_DB_REPO/cmd/fleet-db" ]]; then
  echo "fleet-db repo not found; set FLEET_DB_REPO=/path/to/fleet-db" >&2
  exit 2
fi
if [[ -z "$FLUE_REPO" || ! -d "$FLUE_REPO/packages/runtime" ]]; then
  echo "flue repo not found; set FLUE_REPO=/path/to/flue" >&2
  exit 2
fi

run_flue() {
  if [[ -n "${LOOM_FLUE_BUILD_CMD_JSON:-}" ]]; then
    local -a cmd
    cmd=()
    while IFS= read -r part; do
      cmd+=("$part")
    done < <(jq -r '.[]' <<<"$LOOM_FLUE_BUILD_CMD_JSON")
    "${cmd[@]}" "$@"
    return
  fi
  if [[ -n "${LOOM_FLUE_BUILD_CMD:-}" ]]; then
    "$LOOM_FLUE_BUILD_CMD" "$@"
    return
  fi
  flue "$@"
}

TMP_ROOT="$(mktemp -d -t loom-real-flue-e2e.XXXXXX)"
BIN_DIR="$TMP_ROOT/bin"
WORKDIR="$TMP_ROOT/work"
LOOM_CONFIG_DIR="$TMP_ROOT/loom-config"
TASK_RUNNER_LOG="$TMP_ROOT/task-runner.log"
mkdir -p "$BIN_DIR" "$WORKDIR" "$LOOM_CONFIG_DIR"

PIDS=()
cleanup() {
  local status=$?
  for pid in "${PIDS[@]:-}"; do
    if kill -0 "$pid" >/dev/null 2>&1; then
      kill "$pid" >/dev/null 2>&1 || true
    fi
  done
  if [[ "$status" -ne 0 ]]; then
    echo
    echo "real Flue E2E failed; logs are under $TMP_ROOT" >&2
    for log in "$TMP_ROOT"/*.log; do
      [[ -f "$log" ]] || continue
      echo "--- ${log##*/} ---" >&2
      tail -120 "$log" >&2 || true
    done
  elif [[ "${KEEP_REAL_FLUE_E2E:-0}" != "1" ]]; then
    rm -rf "$TMP_ROOT"
  else
    echo "kept real Flue E2E workspace at $TMP_ROOT"
  fi
}
trap cleanup EXIT

echo "==> building fleet-db and loom"
(
  cd "$FLEET_DB_REPO"
  GOCACHE="${GOCACHE:-/tmp/go-build-cache}" go build -o "$BIN_DIR/fleet-db" ./cmd/fleet-db
)
(
  cd "$ROOT"
  GOCACHE="${GOCACHE:-/tmp/go-build-cache}" go build -o "$BIN_DIR/loom" ./cmd/loom
)

echo "==> creating isolated workflow repo with the builtin epic-runner"
(
  cd "$WORKDIR"
  git init -q
  git config user.email "loom-real-flue-e2e@example.test"
  git config user.name "loom real flue e2e"
  git commit --allow-empty -m "seed" -q
  mkdir -p workflows node_modules/@loom node_modules/@flue
  ln -s "$ROOT/sdk" node_modules/@loom/sdk
  ln -s "$FLUE_REPO/packages/runtime" node_modules/@flue/runtime
  printf '%s\n' '{"type":"module","dependencies":{"@loom/sdk":"file:./node_modules/@loom/sdk","@flue/runtime":"file:./node_modules/@flue/runtime"}}' > package.json
)
cp "$ROOT/internal/workflows/builtin/epic-runner.ts" "$WORKDIR/workflows/epic-runner.ts"

# When this file exists and contains a task id, the task runner fails that
# task on every attempt so the server retry-then-park policy exhausts
# maxAttempts and parks the run (scenario 2 below).
FAIL_TASK_FILE="$TMP_ROOT/fail-task-id"
FAIL_TASK_FILE_JSON="$(jq -nc --arg path "$FAIL_TASK_FILE" '$path')"

TASK_RUNNER_LOG_JSON="$(jq -nc --arg path "$TASK_RUNNER_LOG" '$path')"
cat > "$WORKDIR/task-runner.mjs" <<EOF
#!/usr/bin/env node
import fs from 'node:fs';

const request = JSON.parse(process.env.LOOM_TASK_RUN_REQUEST_JSON || '{}');
const logPath = ${TASK_RUNNER_LOG_JSON};
const failTaskPath = ${FAIL_TASK_FILE_JSON};
if (process.env.LOOM_TASK_RUN_LEASE_TOKEN !== request.lease_token) {
  console.error('task-run lease token did not reach task runner');
  process.exit(3);
}
if (request.provider_profile !== 'flue-local') {
  console.error('unexpected provider profile ' + request.provider_profile);
  process.exit(4);
}

fs.appendFileSync(logPath, request.task_id + '\n');
let failTaskId = '';
try {
  failTaskId = fs.readFileSync(failTaskPath, 'utf8').trim();
} catch {}
if (failTaskId && request.task_id === failTaskId) {
  console.log(JSON.stringify({
    status: 'failed',
    exitCode: 1,
    errorClass: 'injected_task_failure',
    errorMessage: 'deliberate failure injected by real-flue e2e',
    logsRef: 'logs://' + request.task_run_id,
  }));
  process.exit(0);
}
console.log(JSON.stringify({
  status: 'completed',
  exitCode: 0,
  logsRef: 'logs://' + request.task_run_id,
  runtimeMetadata: {
    task_runner: 'real-flue-e2e',
    sandbox_provider: request.sandbox_placement?.provider || '',
  },
}));
EOF
chmod +x "$WORKDIR/task-runner.mjs"

TASK_RUNNER_CMD_JSON="$(jq -nc --arg node "$(command -v node)" --arg runner "$WORKDIR/task-runner.mjs" '[$node,$runner]')"

wait_http() {
  local url="$1"
  local name="$2"
  for _ in $(seq 1 80); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.25
  done
  echo "timed out waiting for $name at $url" >&2
  return 1
}

curl_json() {
  local method="$1"
  local path="$2"
  local body="${3:-}"
  if [[ -n "$body" ]]; then
    curl -fsS -X "$method" \
      -H 'Content-Type: application/json' \
      -H 'X-Actor: real-flue-e2e' \
      --data "$body" \
      "$FLEET_URL$path"
  else
    curl -fsS -X "$method" \
      -H 'X-Actor: real-flue-e2e' \
      "$FLEET_URL$path"
  fi
}

# lead_task_message_count counts the lead's inbox messages created by the
# server outbox dispatcher: only outbox rows carry the lead-task-message
# dedupe key (forwarded into the inbox), so this is the provenance assertion
# AND the exactly-once assertion in one number.
lead_task_message_count() {
  curl_json GET "/api/v1/${WS}/agent-inbox-messages?target_agent_id=${LEAD_NAME}&limit=200" \
    | jq '[.agent_inbox_messages[]? | select((.dedupe_key // "") | startswith("lead-task-message:"))] | length'
}

wait_driver_run_status() {
  local run_id="$1"
  local want="$2"
  local attempts="${3:-240}"
  local run_json status
  for _ in $(seq 1 "$attempts"); do
    run_json="$(curl_json GET "/api/v1/${WS}/driver-runs/${run_id}")"
    status="$(jq -r '.status' <<<"$run_json")"
    if [[ "$status" == "$want" ]]; then
      printf '%s' "$run_json"
      return 0
    fi
    case "$status" in
      completed|failed|needs_review|cancelled)
        echo "driver run ${run_id} reached terminal status ${status}, want ${want}" >&2
        jq . <<<"$run_json" >&2
        return 1
        ;;
    esac
    sleep 0.5
  done
  echo "driver run ${run_id} did not reach ${want}" >&2
  jq . <<<"$run_json" >&2
  return 1
}

echo "==> starting Redis on ${REDIS_PORT}"
redis-server --port "$REDIS_PORT" --save "" --appendonly no >"$TMP_ROOT/redis.log" 2>&1 &
PIDS+=("$!")
sleep 0.5

echo "==> starting fleet-db on ${FLEET_PORT}"
"$BIN_DIR/fleet-db" \
  -addr "127.0.0.1:${FLEET_PORT}" \
  -backend redis \
  -redis-addr "127.0.0.1:${REDIS_PORT}" \
  -redis-durability-profile managed \
  -redis-max-retries 0 \
  -redis-cb-fail-threshold 0 \
  -auth-dev-mode \
  -authz-enabled=false \
  -rpc-enabled=false \
  -log-format text \
  >"$TMP_ROOT/fleet-db.log" 2>&1 &
PIDS+=("$!")
wait_http "$FLEET_URL/healthz" "fleet-db"

echo "==> seeding workspace, lead agent, and DAG epic"
curl_json POST /api/v1/admin/workspaces "$(jq -nc --arg key "$WS" '{key:$key,name:"Real Flue E2E",repos:["local/repo"],state:"ready"}')" >/dev/null
curl_json POST "/api/v1/${WS}/roles" '{"name":"lead","description":"e2e lead role"}' >/dev/null
curl_json POST "/api/v1/${WS}/agents" "$(jq -nc --arg name "$LEAD_NAME" '{name:$name,role_name:"lead"}')" >/dev/null

export LOOM_CONFIG_DIR
export LOOM_WORKSPACE="$WS"
export LOOM_FLEET_DB_URL="$FLEET_URL"
export LOOM_FLEET_DB_ACTOR="real-flue-e2e"

create_issue() {
  "$BIN_DIR/loom" --workspace "$WS" data create -o json "$@" | jq -r '.id'
}

EPIC_ID="$(create_issue --title "Real Flue E2E Epic" --type epic --priority 1)"
TASK_A="$(create_issue --title "A" --type task --parent "$EPIC_ID" --priority 1)"
TASK_B="$(create_issue --title "B" --type task --parent "$EPIC_ID" --depends-on "$TASK_A" --priority 1)"
TASK_C="$(create_issue --title "C" --type task --parent "$EPIC_ID" --depends-on "$TASK_A" --priority 1)"
TASK_D="$(create_issue --title "D" --type task --parent "$EPIC_ID" --depends-on "$TASK_B" --depends-on "$TASK_C" --priority 1)"

echo "    epic=${EPIC_ID} dag=${TASK_A}->${TASK_B},${TASK_C}->${TASK_D} lead=${LEAD_NAME}"

echo "==> registering executor node"
curl_json POST "/api/v1/${WS}/nodes" "$(jq -nc --arg node "real-flue-e2e-node" '{node_id:$node,owner_actor:"real-flue-e2e",runtime_provider:"local",drain_state:"active",capacity:8,ttl_seconds:300}')" >/dev/null

echo "==> building native Flue driver"
(
  cd "$WORKDIR"
  run_flue build --target node --root "$WORKDIR" --output "$WORKDIR/dist"
) >"$TMP_ROOT/flue-build.log" 2>&1

echo "==> registering builtin epic-runner driver"
(
  cd "$WORKDIR"
  "$BIN_DIR/loom" --workspace "$WS" driver register --flue-dist dist --name epic-runner --workflow epic-runner --source-ref workflows/epic-runner.ts --activate --json
) >"$TMP_ROOT/register.json"

echo "==> starting loom serve driver executor on ${LOOM_PORT}"
(
  cd "$WORKDIR"
  LOOM_DISABLE_H2C=1 \
  LOOM_DRIVER_EXECUTOR=1 \
  LOOM_DRIVER_LEGACY_AUTH_ENV="${LOOM_DRIVER_LEGACY_AUTH_ENV:-0}" \
  LOOM_DRIVER_EXECUTOR_NODE_ID=real-flue-e2e-node \
  LOOM_DRIVER_TASK_RUNNER_CMD_JSON="$TASK_RUNNER_CMD_JSON" \
  LOOM_DRIVER_TASK_RUN_MAX_ATTEMPTS=2 \
  exec "$BIN_DIR/loom" serve --port "$LOOM_PORT" --frontend-url "http://127.0.0.1:9"
) >"$TMP_ROOT/loom-serve.log" 2>&1 &
PIDS+=("$!")
wait_http "$LOOM_URL/health" "loom serve"

echo "==> scenario 1: watch-driven DAG drain with bound lead"
DAG_STARTED_AT="$SECONDS"
(
  cd "$WORKDIR"
  "$BIN_DIR/loom" --workspace "$WS" driver run epic-runner --epic "$EPIC_ID" --run-id "$RUN_ID" --input "leadName=${LEAD_NAME}" --json
) | tee "$TMP_ROOT/driver-run.json" >/dev/null

echo "==> waiting for run completion"
run_json="$(wait_driver_run_status "$RUN_ID" completed)"
DAG_WALL_SECONDS="$((SECONDS - DAG_STARTED_AT))"
echo "    DAG drained in ${DAG_WALL_SECONDS}s (budget ${MAX_DAG_WALL_SECONDS}s)"
if [[ "$DAG_WALL_SECONDS" -gt "$MAX_DAG_WALL_SECONDS" ]]; then
  echo "watch-driven drain took ${DAG_WALL_SECONDS}s, exceeding the ${MAX_DAG_WALL_SECONDS}s no-polling budget" >&2
  exit 1
fi
if [[ "$(jq -r '.output.logs_ref // ""' <<<"$run_json")" != "driver-run://${RUN_ID}/flue-local" ]]; then
  echo "driver run missing native Flue logs_ref output" >&2
  jq . <<<"$run_json" >&2
  exit 1
fi
if ! jq -e --arg epic "$EPIC_ID" '.summary | contains("Epic drained " + $epic)' <<<"$run_json" >/dev/null; then
  echo "driver run summary is not the watch-driven drain summary" >&2
  jq . <<<"$run_json" >&2
  exit 1
fi

echo "==> verifying DAG tasks and child TaskRuns"
issues_json="$(curl_json GET "/api/v1/${WS}/issues?parent_id=${EPIC_ID}&limit=20")"
closed_count="$(jq '[.issues[] | select(.status == "closed")] | length' <<<"$issues_json")"
if [[ "$closed_count" != "4" ]]; then
  echo "expected four closed child tasks" >&2
  jq . <<<"$issues_json" >&2
  exit 1
fi

task_runs_json="$(curl_json GET "/api/v1/${WS}/task-runs?driver_run_id=${RUN_ID}")"
completed_task_runs="$(jq '[.task_runs[] | select(.status == "completed")] | length' <<<"$task_runs_json")"
if [[ "$completed_task_runs" != "4" ]]; then
  echo "expected four completed child TaskRuns" >&2
  jq . <<<"$task_runs_json" >&2
  exit 1
fi
completed_task_runs_with_logs="$(jq '[.task_runs[] | select(.status == "completed" and ((.logs_ref // "") != ""))] | length' <<<"$task_runs_json")"
if [[ "$completed_task_runs_with_logs" != "4" ]]; then
  echo "expected four completed child TaskRuns with logs_ref" >&2
  jq . <<<"$task_runs_json" >&2
  exit 1
fi

if [[ ! -f "$TASK_RUNNER_LOG" ]]; then
  echo "task runner did not run" >&2
  exit 1
fi
executed=()
while IFS= read -r task_id; do
  executed+=("$task_id")
done < "$TASK_RUNNER_LOG"
if [[ "${#executed[@]}" -ne 4 ]]; then
  echo "expected four task runner executions, got ${#executed[@]}: ${executed[*]:-}" >&2
  exit 1
fi
if [[ "${executed[0]}" != "$TASK_A" || "${executed[3]}" != "$TASK_D" ]]; then
  echo "dependency order violation: ${executed[*]}" >&2
  exit 1
fi
middle="$(printf '%s\n%s\n' "${executed[1]}" "${executed[2]}" | sort | paste -sd ',' -)"
want_middle="$(printf '%s\n%s\n' "$TASK_B" "$TASK_C" | sort | paste -sd ',' -)"
if [[ "$middle" != "$want_middle" ]]; then
  echo "expected B/C in middle of execution order; got ${executed[*]}" >&2
  exit 1
fi

echo "==> verifying lead received task-completed messages from the server outbox"
lead_messages=0
for _ in $(seq 1 60); do
  lead_messages="$(lead_task_message_count)"
  if [[ "$lead_messages" == "4" ]]; then
    break
  fi
  sleep 0.5
done
if [[ "$lead_messages" != "4" ]]; then
  echo "expected four outbox-delivered lead task messages, got ${lead_messages}" >&2
  curl_json GET "/api/v1/${WS}/agent-inbox-messages?target_agent_id=${LEAD_NAME}&limit=200" | jq . >&2
  exit 1
fi
lead_inbox_json="$(curl_json GET "/api/v1/${WS}/agent-inbox-messages?target_agent_id=${LEAD_NAME}&limit=200")"
outbox_message_bodies_ok="$(jq '[.agent_inbox_messages[]? | select((.dedupe_key // "") | startswith("lead-task-message:")) | select((.body | contains("Loom completed a child task")) and ((.task_run_id // "") != ""))] | length' <<<"$lead_inbox_json")"
if [[ "$outbox_message_bodies_ok" != "4" ]]; then
  echo "outbox-delivered lead messages are missing the server-side template or task_run_id linkage" >&2
  jq . <<<"$lead_inbox_json" >&2
  exit 1
fi
if ! jq -e '[.agent_inbox_messages[]? | select((.source_kind // "") == "lead_assignment")] | length >= 1' <<<"$lead_inbox_json" >/dev/null; then
  echo "lead assignment inbox message missing (deliver-lead-assignment fire-once + outbox)" >&2
  jq . <<<"$lead_inbox_json" >&2
  exit 1
fi

echo "==> verifying retry does not duplicate completed child tasks or lead messages"
RETRY_RUN_ID="${RUN_ID}-retry"
(
  cd "$WORKDIR"
  "$BIN_DIR/loom" --workspace "$WS" driver run epic-runner --epic "$EPIC_ID" --run-id "$RETRY_RUN_ID" --input "leadName=${LEAD_NAME}" --json
) >"$TMP_ROOT/driver-run-retry.json"
wait_driver_run_status "$RETRY_RUN_ID" completed >/dev/null
retry_task_runs="$(curl_json GET "/api/v1/${WS}/task-runs?driver_run_id=${RETRY_RUN_ID}" | jq '[.task_runs[]] | length')"
if [[ "$retry_task_runs" != "0" ]]; then
  echo "retry driver run created duplicate child TaskRuns" >&2
  curl_json GET "/api/v1/${WS}/task-runs?driver_run_id=${RETRY_RUN_ID}" | jq . >&2
  exit 1
fi
sleep 3
lead_messages_after_retry="$(lead_task_message_count)"
if [[ "$lead_messages_after_retry" != "4" ]]; then
  echo "rerun duplicated outbox lead messages (dedupe broken): ${lead_messages_after_retry}, want 4" >&2
  curl_json GET "/api/v1/${WS}/agent-inbox-messages?target_agent_id=${LEAD_NAME}&limit=200" | jq . >&2
  exit 1
fi

echo "==> scenario 2: builtin epic-runner parks a failed branch and drains siblings"
EPIC2_ID="$(create_issue --title "Real Flue E2E Parked Epic" --type epic --priority 1)"
TASK2_A="$(create_issue --title "A2" --type task --parent "$EPIC2_ID" --priority 1)"
TASK2_B="$(create_issue --title "B2" --type task --parent "$EPIC2_ID" --depends-on "$TASK2_A" --priority 1)"
TASK2_C="$(create_issue --title "C2 deliberately fails" --type task --parent "$EPIC2_ID" --depends-on "$TASK2_A" --priority 1)"
TASK2_D="$(create_issue --title "D2" --type task --parent "$EPIC2_ID" --depends-on "$TASK2_B" --priority 1)"
printf '%s\n' "$TASK2_C" > "$FAIL_TASK_FILE"
echo "    epic=${EPIC2_ID} failing=${TASK2_C} sibling-branch=${TASK2_B}->${TASK2_D}"

PARKED_RUN_ID="${RUN_ID}-parked"
(
  cd "$WORKDIR"
  "$BIN_DIR/loom" --workspace "$WS" driver run epic-runner --epic "$EPIC2_ID" --run-id "$PARKED_RUN_ID" --json
) >"$TMP_ROOT/driver-run-parked.json"

echo "==> waiting for parked-branch run to reach needs_review"
parked_run_json="$(wait_driver_run_status "$PARKED_RUN_ID" needs_review)"
if [[ "$(jq -r '.error_class // ""' <<<"$parked_run_json")" != "epic_tasks_parked" ]]; then
  echo "parked-branch driver run error_class is not epic_tasks_parked" >&2
  jq . <<<"$parked_run_json" >&2
  exit 1
fi
if ! jq -e --arg task "$TASK2_C" '.summary | contains($task)' <<<"$parked_run_json" >/dev/null; then
  echo "parked-branch driver run summary does not list parked task ${TASK2_C}" >&2
  jq . <<<"$parked_run_json" >&2
  exit 1
fi

echo "==> verifying sibling branch drained despite the parked task"
issues2_json="$(curl_json GET "/api/v1/${WS}/issues?parent_id=${EPIC2_ID}&limit=20")"
for sibling in "$TASK2_A" "$TASK2_B" "$TASK2_D"; do
  sibling_status="$(jq -r --arg id "$sibling" '.issues[] | select(.id == $id) | .status' <<<"$issues2_json")"
  if [[ "$sibling_status" != "closed" ]]; then
    echo "expected sibling task ${sibling} closed, got ${sibling_status:-missing}" >&2
    jq . <<<"$issues2_json" >&2
    exit 1
  fi
done
failed_task_status="$(jq -r --arg id "$TASK2_C" '.issues[] | select(.id == $id) | .status' <<<"$issues2_json")"
if [[ "$failed_task_status" != "parked" ]]; then
  echo "parked task ${TASK2_C} should have the explicit parked status, got ${failed_task_status:-missing}" >&2
  jq . <<<"$issues2_json" >&2
  exit 1
fi

parked_task_runs_json="$(curl_json GET "/api/v1/${WS}/task-runs?driver_run_id=${PARKED_RUN_ID}")"
parked_completed="$(jq '[.task_runs[] | select(.status == "completed")] | length' <<<"$parked_task_runs_json")"
if [[ "$parked_completed" != "3" ]]; then
  echo "expected three completed child TaskRuns in the parked-branch run" >&2
  jq . <<<"$parked_task_runs_json" >&2
  exit 1
fi
parked_runs="$(jq '[.task_runs[] | select(.status == "failed" and ((.runtime_metadata.scheduler_state // "") == "parked"))] | length' <<<"$parked_task_runs_json")"
if [[ "$parked_runs" != "1" ]]; then
  echo "expected exactly one parked (failed) child TaskRun" >&2
  jq . <<<"$parked_task_runs_json" >&2
  exit 1
fi
fail_attempts="$(grep -cxF "$TASK2_C" "$TASK_RUNNER_LOG" || true)"
if [[ "$fail_attempts" != "2" ]]; then
  echo "expected the failing task to run exactly twice (retry then park), got ${fail_attempts}" >&2
  exit 1
fi
# Epic 2 has no bound lead, so the parked/completed transitions there must
# not have produced additional outbox lead messages.
lead_messages_after_parked="$(lead_task_message_count)"
if [[ "$lead_messages_after_parked" != "4" ]]; then
  echo "lead-less epic produced outbox lead messages: ${lead_messages_after_parked}, want 4" >&2
  exit 1
fi

echo "==> verifying rerun does not duplicate task runs for the parked epic"
PARKED_RETRY_RUN_ID="${PARKED_RUN_ID}-retry"
(
  cd "$WORKDIR"
  "$BIN_DIR/loom" --workspace "$WS" driver run epic-runner --epic "$EPIC2_ID" --run-id "$PARKED_RETRY_RUN_ID" --json
) >"$TMP_ROOT/driver-run-parked-retry.json"
parked_retry_json="$(wait_driver_run_status "$PARKED_RETRY_RUN_ID" needs_review)"
if [[ "$(jq -r '.error_class // ""' <<<"$parked_retry_json")" != "epic_tasks_parked" ]]; then
  echo "parked-branch rerun error_class is not epic_tasks_parked" >&2
  jq . <<<"$parked_retry_json" >&2
  exit 1
fi
parked_retry_task_runs="$(curl_json GET "/api/v1/${WS}/task-runs?driver_run_id=${PARKED_RETRY_RUN_ID}" | jq '[.task_runs[]] | length')"
if [[ "$parked_retry_task_runs" != "0" ]]; then
  echo "parked-branch rerun created duplicate child TaskRuns" >&2
  curl_json GET "/api/v1/${WS}/task-runs?driver_run_id=${PARKED_RETRY_RUN_ID}" | jq . >&2
  exit 1
fi
fail_attempts_after_rerun="$(grep -cxF "$TASK2_C" "$TASK_RUNNER_LOG" || true)"
if [[ "$fail_attempts_after_rerun" != "2" ]]; then
  echo "rerun re-executed the parked task (attempts ${fail_attempts_after_rerun}, want 2)" >&2
  exit 1
fi
echo "real Flue epic runner E2E passed"
echo "  FleetDB: ${FLEET_URL}"
echo "  Loom:    ${LOOM_URL}"
echo "  DAG:     ${executed[*]} (drained in ${DAG_WALL_SECONDS}s, watch-driven)"
echo "  Lead:    ${LEAD_NAME} received 4 outbox task messages (exactly-once on rerun)"
echo "  Parked:  epic=${EPIC2_ID} task=${TASK2_C} (siblings drained, run needs_review epic_tasks_parked)"
