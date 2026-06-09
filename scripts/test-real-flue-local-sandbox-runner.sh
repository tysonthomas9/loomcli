#!/usr/bin/env bash
set -Eeuo pipefail

# Full-stack native-Flue driver smoke with a Flue local() child task runner.
#
# This is intentionally separate from test-real-flue-epic-runner.sh:
# - test-real-flue-epic-runner.sh keeps the deterministic stub child runner.
# - this script swaps the child runner for @flue/runtime/node local(), so each
#   FleetDB child TaskRun proves it can execute inside the same local sandbox
#   surface used by Flue's local sandbox tests.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FLEET_DB_REPO="${FLEET_DB_REPO:-$(cd "$ROOT/../fleet-db" 2>/dev/null && pwd || true)}"
FLUE_REPO="${FLUE_REPO:-$(cd "$ROOT/../flue" 2>/dev/null && pwd || true)}"
FLUE_LOOM_TS_DIR="$ROOT/scripts/real-flue-local-sandbox"

WORKSPACE="${LOOM_REAL_FLUE_LOCAL_SANDBOX_WORKSPACE:-FLUELOCAL}"
RUN_ID="${LOOM_REAL_FLUE_LOCAL_SANDBOX_RUN_ID:-run-real-flue-local-sandbox}"
REDIS_PORT="${REDIS_PORT:-16479}"
FLEET_PORT="${FLEET_PORT:-18195}"
LOOM_PORT="${LOOM_PORT:-18196}"
FLEET_URL="http://127.0.0.1:${FLEET_PORT}"
LOOM_URL="http://127.0.0.1:${LOOM_PORT}"

TMP_ROOT="$(mktemp -d -t loom-real-flue-local-sandbox.XXXXXX)"
BIN_DIR="$TMP_ROOT/bin"
WORKDIR="$TMP_ROOT/work"
LOOM_CONFIG_DIR="$TMP_ROOT/loom-config"
TASK_RUNNER_LOG="$TMP_ROOT/task-runner.log"
mkdir -p "$BIN_DIR" "$WORKDIR" "$LOOM_CONFIG_DIR"

PIDS=()

log_step() {
  printf '\n==> %s\n' "$1"
}

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
    if kill -0 "$pid" >/dev/null 2>&1; then
      kill "$pid" >/dev/null 2>&1 || true
    fi
  done

  if [[ "$status" -ne 0 ]]; then
    echo
    echo "real Flue local-sandbox E2E failed; logs are under $TMP_ROOT" >&2
    for log in "$TMP_ROOT"/*.log; do
      [[ -f "$log" ]] || continue
      echo "--- ${log##*/} ---" >&2
      tail -120 "$log" >&2 || true
    done
  elif [[ "${KEEP_REAL_FLUE_E2E:-0}" != "1" ]]; then
    rm -rf "$TMP_ROOT"
  else
    echo "kept real Flue local-sandbox E2E workspace at $TMP_ROOT"
  fi
}
trap cleanup EXIT

run_flue() {
  if [[ -n "${LOOM_FLUE_BUILD_CMD_JSON:-}" ]]; then
    local -a cmd=()
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

curl_json() {
  local method="$1"
  local path="$2"
  local body="${3:-}"

  if [[ -n "$body" ]]; then
    curl -fsS -X "$method" \
      -H 'Content-Type: application/json' \
      -H 'X-Actor: real-flue-local-sandbox-e2e' \
      --data "$body" \
      "$FLEET_URL$path"
  else
    curl -fsS -X "$method" \
      -H 'X-Actor: real-flue-local-sandbox-e2e' \
      "$FLEET_URL$path"
  fi
}

wait_http() {
  local url="$1"
  local name="$2"

  for _ in $(seq 1 80); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.25
  done

  die "timed out waiting for $name at $url"
}

check_prerequisites() {
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
EOF
    exit 2
  fi

  [[ -n "$FLEET_DB_REPO" && -d "$FLEET_DB_REPO/cmd/fleet-db" ]] ||
    die "fleet-db repo not found; set FLEET_DB_REPO=/path/to/fleet-db"

  [[ -n "$FLUE_REPO" && -d "$FLUE_REPO/packages/runtime" ]] ||
    die "Flue repo not found; set FLUE_REPO=/path/to/flue"

  [[ -f "$FLUE_REPO/packages/runtime/dist/node/index.mjs" ]] ||
    die "Flue runtime dist is missing; build the Flue repo before running this script"

  [[ -f "$FLUE_LOOM_TS_DIR/epic-runner.ts" && -f "$FLUE_LOOM_TS_DIR/task-runner.ts" ]] ||
    die "Flue/Loom TypeScript fixtures are missing under $FLUE_LOOM_TS_DIR"

  local node_ts_check="$TMP_ROOT/node-ts-check.ts"
  printf '%s\n' 'const value: number = 1; if (value !== 1) process.exit(1);' >"$node_ts_check"
  node "$node_ts_check" >/dev/null 2>&1 ||
    die "node cannot execute TypeScript files directly; use Node 24+ for this script"
}

build_binaries() {
  log_step "building fleet-db and loom"
  (
    cd "$FLEET_DB_REPO"
    GOCACHE="${GOCACHE:-/tmp/go-build-cache}" go build -o "$BIN_DIR/fleet-db" ./cmd/fleet-db
  )
  (
    cd "$ROOT"
    GOCACHE="${GOCACHE:-/tmp/go-build-cache}" go build -o "$BIN_DIR/loom" ./cmd/loom
  )
}

install_flue_loom_typescript() {
  cp "$FLUE_LOOM_TS_DIR/epic-runner.ts" "$WORKDIR/workflows/epic-runner.ts"
  cp "$FLUE_LOOM_TS_DIR/task-runner.ts" "$WORKDIR/task-runner.ts"
}

create_isolated_project() {
  log_step "creating isolated Flue workflow project"
  (
    cd "$WORKDIR"
    git init -q
    git config user.email "loom-real-flue-local-sandbox-e2e@example.test"
    git config user.name "loom real flue local sandbox e2e"
    git commit --allow-empty -m "seed" -q

    mkdir -p workflows node_modules/@loom node_modules/@flue
    ln -s "$ROOT/sdk" node_modules/@loom/sdk
    ln -s "$FLUE_REPO/packages/runtime" node_modules/@flue/runtime
    printf '%s\n' '{"type":"module","dependencies":{"@loom/sdk":"file:./node_modules/@loom/sdk","@flue/runtime":"file:./node_modules/@flue/runtime"}}' > package.json
  )

  install_flue_loom_typescript
}

start_services() {
  log_step "starting Redis on ${REDIS_PORT}"
  redis-server --port "$REDIS_PORT" --save "" --appendonly no >"$TMP_ROOT/redis.log" 2>&1 &
  PIDS+=("$!")
  sleep 0.5

  log_step "starting fleet-db on ${FLEET_PORT}"
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
}

create_issue() {
  "$BIN_DIR/loom" --workspace "$WORKSPACE" data create -o json "$@" | jq -r '.id'
}

seed_epic_dag() {
  log_step "seeding workspace and A -> B,C -> D epic"
  curl_json POST /api/v1/admin/workspaces "$(jq -nc --arg key "$WORKSPACE" '{key:$key,name:"Real Flue Local Sandbox E2E",repos:["local/repo"],state:"ready"}')" >/dev/null

  export LOOM_CONFIG_DIR
  export LOOM_WORKSPACE="$WORKSPACE"
  export LOOM_FLEET_DB_URL="$FLEET_URL"
  export LOOM_FLEET_DB_ACTOR="real-flue-local-sandbox-e2e"

  EPIC_ID="$(create_issue --title "Real Flue Local Sandbox Epic" --type epic --priority 1)"
  TASK_A="$(create_issue --title "A" --type task --parent "$EPIC_ID" --priority 1)"
  TASK_B="$(create_issue --title "B" --type task --parent "$EPIC_ID" --depends-on "$TASK_A" --priority 1)"
  TASK_C="$(create_issue --title "C" --type task --parent "$EPIC_ID" --depends-on "$TASK_A" --priority 1)"
  TASK_D="$(create_issue --title "D" --type task --parent "$EPIC_ID" --depends-on "$TASK_B" --depends-on "$TASK_C" --priority 1)"

  echo "    epic=${EPIC_ID}"
  echo "    dag=${TASK_A}->${TASK_B},${TASK_C}->${TASK_D}"
}

register_executor_node() {
  log_step "registering driver executor node"
  curl_json POST "/api/v1/${WORKSPACE}/nodes" "$(jq -nc --arg node "real-flue-local-sandbox-node" '{node_id:$node,owner_actor:"real-flue-local-sandbox-e2e",runtime_provider:"local",drain_state:"active",capacity:8,ttl_seconds:300}')" >/dev/null
}

build_and_register_driver() {
  log_step "building native Flue driver"
  (
    cd "$WORKDIR"
    run_flue build --target node --root "$WORKDIR" --output "$WORKDIR/dist"
  ) >"$TMP_ROOT/flue-build.log" 2>&1

  log_step "registering native Flue driver"
  (
    cd "$WORKDIR"
    "$BIN_DIR/loom" --workspace "$WORKSPACE" driver register \
      --flue-dist dist \
      --name epic-runner \
      --workflow epic-runner \
      --source-ref workflows/epic-runner.ts \
      --activate \
      --json
  ) >"$TMP_ROOT/register.json"
}

start_loom_executor() {
  local task_runner_cmd_json
  task_runner_cmd_json="$(jq -nc --arg node "$(command -v node)" --arg runner "$WORKDIR/task-runner.ts" --arg log "$TASK_RUNNER_LOG" '[$node,$runner,$log]')"

  log_step "starting loom serve driver executor on ${LOOM_PORT}"
  (
    cd "$WORKDIR"
    LOOM_DISABLE_H2C=1 \
    LOOM_DRIVER_EXECUTOR=1 \
    LOOM_DRIVER_EXECUTOR_NODE_ID=real-flue-local-sandbox-node \
    LOOM_DRIVER_TASK_RUNNER_CMD_JSON="$task_runner_cmd_json" \
    "$BIN_DIR/loom" serve --port "$LOOM_PORT" --frontend-url "http://127.0.0.1:9"
  ) >"$TMP_ROOT/loom-serve.log" 2>&1 &
  PIDS+=("$!")
  wait_http "$LOOM_URL/health" "loom serve"
}

queue_driver_run() {
  log_step "queueing driver run"
  (
    cd "$WORKDIR"
    "$BIN_DIR/loom" --workspace "$WORKSPACE" driver run epic-runner \
      --epic "$EPIC_ID" \
      --run-id "$RUN_ID" \
      --json
  ) | tee "$TMP_ROOT/driver-run.json" >/dev/null
}

wait_for_completed_run() {
  local run_id="$1"
  local run_json status

  log_step "waiting for driver run ${run_id}"
  for _ in $(seq 1 120); do
    run_json="$(curl_json GET "/api/v1/${WORKSPACE}/driver-runs/${run_id}")"
    status="$(jq -r '.status' <<<"$run_json")"
    case "$status" in
      completed)
        return 0
        ;;
      failed|needs_human|cancelled)
        echo "driver run reached terminal status ${status}" >&2
        jq . <<<"$run_json" >&2
        exit 1
        ;;
    esac
    sleep 0.5
  done

  run_json="$(curl_json GET "/api/v1/${WORKSPACE}/driver-runs/${run_id}")"
  echo "driver run did not complete" >&2
  jq . <<<"$run_json" >&2
  exit 1
}

verify_run_output() {
  local run_json
  run_json="$(curl_json GET "/api/v1/${WORKSPACE}/driver-runs/${RUN_ID}")"

  [[ "$(jq -r '.output.logs_ref // ""' <<<"$run_json")" == "driver-run://${RUN_ID}/flue-local" ]] ||
    die "driver run missing native Flue logs_ref output"

  jq -e '.output.flue_stderr_tail | contains("native-flue-driver-start")' <<<"$run_json" >/dev/null ||
    die "driver run output missing captured Flue log marker"
}

verify_task_results() {
  log_step "verifying FleetDB tasks, child TaskRuns, and Flue local sandboxes"

  local issues_json task_runs_json closed_count completed_count sandbox_placement_count sandbox_request_count sandbox_file_count
  issues_json="$(curl_json GET "/api/v1/${WORKSPACE}/issues?parent_id=${EPIC_ID}&limit=20")"
  jq . <<<"$issues_json" >"$TMP_ROOT/issues.json"
  closed_count="$(jq '[.issues[] | select(.status == "closed")] | length' <<<"$issues_json")"
  [[ "$closed_count" == "4" ]] || die "expected four closed child tasks"

  task_runs_json="$(curl_json GET "/api/v1/${WORKSPACE}/task-runs?driver_run_id=${RUN_ID}")"
  jq . <<<"$task_runs_json" >"$TMP_ROOT/task-runs.json"
  completed_count="$(jq '[.task_runs[] | select(.status == "completed" and ((.logs_ref // "") != ""))] | length' <<<"$task_runs_json")"
  [[ "$completed_count" == "4" ]] || die "expected four completed child TaskRuns with logs_ref"

  sandbox_placement_count="$(jq '[.task_runs[] | select(.sandbox_placement.provider == "flue-local")] | length' <<<"$task_runs_json")"
  [[ "$sandbox_placement_count" == "4" ]] || die "expected four child TaskRuns to request flue-local sandbox placement"

  [[ -f "$TASK_RUNNER_LOG" ]] || die "task runner did not run"
  EXECUTED_TASKS=()
  while IFS= read -r task_id; do
    [[ -n "$task_id" ]] && EXECUTED_TASKS+=("$task_id")
  done < "$TASK_RUNNER_LOG"
  [[ "${#EXECUTED_TASKS[@]}" == "4" ]] || die "expected four task runner executions, got ${#EXECUTED_TASKS[@]}"
  [[ "${EXECUTED_TASKS[0]}" == "$TASK_A" && "${EXECUTED_TASKS[3]}" == "$TASK_D" ]] ||
    die "dependency order violation: ${EXECUTED_TASKS[*]}"

  local middle want_middle
  middle="$(printf '%s\n%s\n' "${EXECUTED_TASKS[1]}" "${EXECUTED_TASKS[2]}" | sort | paste -sd ',' -)"
  want_middle="$(printf '%s\n%s\n' "$TASK_B" "$TASK_C" | sort | paste -sd ',' -)"
  [[ "$middle" == "$want_middle" ]] ||
    die "expected B/C in middle of execution order; got ${EXECUTED_TASKS[*]}"

  sandbox_request_count="$(
    find "$WORKDIR/.loom" -path '*/task-runner-sandboxes/*/task-request.json' -type f -print |
      while IFS= read -r file; do
        jq -e '
          .provider_profile == "flue-local" and
          .sandbox_placement.provider == "flue-local" and
          .lease_token == "[redacted]" and
          .lease_token_received_by_host_runner == "true"
        ' "$file" >/dev/null && echo ok
      done |
      wc -l |
      tr -d ' '
  )"
  [[ "$sandbox_request_count" == "4" ]] || die "expected four sanitized Flue local sandbox request files"

  sandbox_file_count="$(
    find "$WORKDIR/.loom" -path '*/task-runner-sandboxes/*/agent-output.txt' -type f -print |
      while IFS= read -r file; do
        grep -qx 'LOCAL_SANDBOX_TASK_RUNNER_OK' "$file" && echo ok
      done |
      wc -l |
      tr -d ' '
  )"
  [[ "$sandbox_file_count" == "4" ]] || die "expected four verified Flue local sandbox output files"
}

verify_retry_is_idempotent() {
  log_step "verifying retry does not duplicate completed child tasks"
  local retry_run_id retry_task_runs_json retry_count
  retry_run_id="${RUN_ID}-retry"

  (
    cd "$WORKDIR"
    "$BIN_DIR/loom" --workspace "$WORKSPACE" driver run epic-runner \
      --epic "$EPIC_ID" \
      --run-id "$retry_run_id" \
      --json
  ) >"$TMP_ROOT/driver-run-retry.json"

  wait_for_completed_run "$retry_run_id"

  retry_task_runs_json="$(curl_json GET "/api/v1/${WORKSPACE}/task-runs?driver_run_id=${retry_run_id}")"
  retry_count="$(jq '[.task_runs[]] | length' <<<"$retry_task_runs_json")"
  [[ "$retry_count" == "0" ]] || die "retry driver run created duplicate child TaskRuns"
}

main() {
  check_prerequisites
  build_binaries
  create_isolated_project
  start_services
  seed_epic_dag
  register_executor_node
  build_and_register_driver
  start_loom_executor
  queue_driver_run
  wait_for_completed_run "$RUN_ID"
  verify_run_output
  verify_task_results
  verify_retry_is_idempotent

  echo
  echo "real Flue local-sandbox runner E2E passed"
  echo "  FleetDB: ${FLEET_URL}"
  echo "  Loom:    ${LOOM_URL}"
  echo "  DAG:     ${EXECUTED_TASKS[*]}"
}

main "$@"
