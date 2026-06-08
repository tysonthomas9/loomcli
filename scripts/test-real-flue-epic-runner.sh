#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FLEET_DB_REPO="${FLEET_DB_REPO:-$(cd "$ROOT/../fleet-db" 2>/dev/null && pwd || true)}"

WS="${LOOM_REAL_FLUE_E2E_WORKSPACE:-FLUEE2E}"
RUN_ID="${LOOM_REAL_FLUE_E2E_RUN_ID:-run-real-flue-e2e}"
REDIS_PORT="${REDIS_PORT:-16379}"
FLEET_PORT="${FLEET_PORT:-18095}"
LOOM_PORT="${LOOM_PORT:-18096}"
FLEET_URL="http://127.0.0.1:${FLEET_PORT}"
LOOM_URL="http://127.0.0.1:${LOOM_PORT}"

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

echo "==> creating isolated workflow repo"
(
  cd "$WORKDIR"
  git init -q
  git config user.email "loom-real-flue-e2e@example.test"
  git config user.name "loom real flue e2e"
  git commit --allow-empty -m "seed" -q
  mkdir -p workflows node_modules/@loom
  ln -s "$ROOT/sdk" node_modules/@loom/sdk
  printf '%s\n' '{"type":"module","dependencies":{"@loom/sdk":"file:./node_modules/@loom/sdk"}}' > package.json
)

cat > "$WORKDIR/workflows/epic-runner.ts" <<'EOF'
import { createLoomDriverClient } from '@loom/sdk/flue';

export async function run(ctx) {
  const input = ctx.payload || {};
  const loom = createLoomDriverClient({ input });
  console.log("native-flue-driver-start " + input.epicId);
  const completed = [];
  while (true) {
    const task = await loom.tasks.claimReady({ epicId: input.epicId });
    if (!task) {
      return loom.completed({ summary: "Epic drained: " + completed.join(",") });
    }

    const result = await loom.taskRuns.request({
      taskId: task.id,
      providerProfile: "flue-local",
      supportedProviders: ["flue-local"],
      sandboxPlacement: { provider: "flue-local" },
    });

    if (result.status === "completed") {
      await loom.tasks.complete(task.id);
      completed.push(task.id);
    } else {
      await loom.tasks.release(task.id);
      return loom.needsReview({
        summary: "Task failed: " + task.id,
        taskRunId: result.id,
        logsRef: result.logsRef || "",
        artifactsRef: result.artifactsRef || "",
      });
    }
  }
}
EOF

TASK_RUNNER_LOG_JSON="$(jq -nc --arg path "$TASK_RUNNER_LOG" '$path')"
cat > "$WORKDIR/task-runner.mjs" <<EOF
#!/usr/bin/env node
import fs from 'node:fs';

const request = JSON.parse(process.env.LOOM_TASK_RUN_REQUEST_JSON || '{}');
const logPath = ${TASK_RUNNER_LOG_JSON};
if (process.env.LOOM_TASK_RUN_LEASE_TOKEN !== request.lease_token) {
  console.error('task-run lease token did not reach task runner');
  process.exit(3);
}
if (request.provider_profile !== 'flue-local') {
  console.error('unexpected provider profile ' + request.provider_profile);
  process.exit(4);
}

fs.appendFileSync(logPath, request.task_id + '\n');
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

echo "==> seeding workspace and DAG epic"
curl_json POST /api/v1/admin/workspaces "$(jq -nc --arg key "$WS" '{key:$key,name:"Real Flue E2E",repos:["local/repo"],state:"ready"}')" >/dev/null

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

echo "    epic=${EPIC_ID} dag=${TASK_A}->${TASK_B},${TASK_C}->${TASK_D}"

echo "==> registering executor node"
curl_json POST "/api/v1/${WS}/nodes" "$(jq -nc --arg node "real-flue-e2e-node" '{node_id:$node,owner_actor:"real-flue-e2e",runtime_provider:"local",drain_state:"active",capacity:8,ttl_seconds:300}')" >/dev/null

echo "==> building native Flue driver"
(
  cd "$WORKDIR"
  run_flue build --target node --root "$WORKDIR" --output "$WORKDIR/dist"
) >"$TMP_ROOT/flue-build.log" 2>&1

echo "==> registering native Flue driver"
(
  cd "$WORKDIR"
  "$BIN_DIR/loom" --workspace "$WS" driver register --flue-dist dist --name epic-runner --workflow epic-runner --source-ref workflows/epic-runner.ts --activate --json
) >"$TMP_ROOT/register.json"

echo "==> starting loom serve driver executor on ${LOOM_PORT}"
(
  cd "$WORKDIR"
  LOOM_DISABLE_H2C=1 \
  LOOM_DRIVER_EXECUTOR=1 \
  LOOM_DRIVER_EXECUTOR_NODE_ID=real-flue-e2e-node \
  LOOM_DRIVER_TASK_RUNNER_CMD_JSON="$TASK_RUNNER_CMD_JSON" \
  "$BIN_DIR/loom" serve --port "$LOOM_PORT" --frontend-url "http://127.0.0.1:9"
) >"$TMP_ROOT/loom-serve.log" 2>&1 &
PIDS+=("$!")
wait_http "$LOOM_URL/health" "loom serve"

echo "==> queueing driver run"
(
  cd "$WORKDIR"
  "$BIN_DIR/loom" --workspace "$WS" driver run epic-runner --epic "$EPIC_ID" --run-id "$RUN_ID" --json
) | tee "$TMP_ROOT/driver-run.json" >/dev/null

echo "==> waiting for run completion"
for _ in $(seq 1 120); do
  run_json="$(curl_json GET "/api/v1/${WS}/driver-runs/${RUN_ID}")"
  status="$(jq -r '.status' <<<"$run_json")"
  if [[ "$status" == "completed" ]]; then
    break
  fi
  if [[ "$status" == "failed" || "$status" == "needs_review" || "$status" == "cancelled" ]]; then
    echo "driver run reached terminal status ${status}" >&2
    jq . <<<"$run_json" >&2
    exit 1
  fi
  sleep 0.5
done

run_json="$(curl_json GET "/api/v1/${WS}/driver-runs/${RUN_ID}")"
if [[ "$(jq -r '.status' <<<"$run_json")" != "completed" ]]; then
  echo "driver run did not complete" >&2
  jq . <<<"$run_json" >&2
  exit 1
fi
if [[ "$(jq -r '.output.logs_ref // ""' <<<"$run_json")" != "driver-run://${RUN_ID}/flue-local" ]]; then
  echo "driver run missing native Flue logs_ref output" >&2
  jq . <<<"$run_json" >&2
  exit 1
fi
if ! jq -e '.output.flue_stderr_tail | contains("native-flue-driver-start")' <<<"$run_json" >/dev/null; then
  echo "driver run output missing captured Flue log marker" >&2
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

echo "==> verifying retry does not duplicate completed child tasks"
RETRY_RUN_ID="${RUN_ID}-retry"
(
  cd "$WORKDIR"
  "$BIN_DIR/loom" --workspace "$WS" driver run epic-runner --epic "$EPIC_ID" --run-id "$RETRY_RUN_ID" --json
) >"$TMP_ROOT/driver-run-retry.json"
for _ in $(seq 1 120); do
  retry_json="$(curl_json GET "/api/v1/${WS}/driver-runs/${RETRY_RUN_ID}")"
  retry_status="$(jq -r '.status' <<<"$retry_json")"
  if [[ "$retry_status" == "completed" ]]; then
    break
  fi
  if [[ "$retry_status" == "failed" || "$retry_status" == "needs_review" || "$retry_status" == "cancelled" ]]; then
    echo "retry driver run reached terminal status ${retry_status}" >&2
    jq . <<<"$retry_json" >&2
    exit 1
  fi
  sleep 0.5
done
retry_json="$(curl_json GET "/api/v1/${WS}/driver-runs/${RETRY_RUN_ID}")"
if [[ "$(jq -r '.status' <<<"$retry_json")" != "completed" ]]; then
  echo "retry driver run did not complete" >&2
  jq . <<<"$retry_json" >&2
  exit 1
fi
retry_task_runs_json="$(curl_json GET "/api/v1/${WS}/task-runs?driver_run_id=${RETRY_RUN_ID}")"
retry_task_runs="$(jq '[.task_runs[]] | length' <<<"$retry_task_runs_json")"
if [[ "$retry_task_runs" != "0" ]]; then
  echo "retry driver run created duplicate child TaskRuns" >&2
  jq . <<<"$retry_task_runs_json" >&2
  exit 1
fi

echo "real Flue epic runner E2E passed"
echo "  FleetDB: ${FLEET_URL}"
echo "  Loom:    ${LOOM_URL}"
echo "  DAG:     ${executed[*]}"
