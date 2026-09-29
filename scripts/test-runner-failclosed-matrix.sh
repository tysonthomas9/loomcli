#!/usr/bin/env bash
set -Eeuo pipefail

# Consolidated FAIL-CLOSED e2e matrix for the productionized runner result gates.
#
# Proves the core invariant on a LIVE stack (redis + fleet-db + loom serve
# --driver-executor): a malformed / non-terminal / completed-with-nonzero runner
# result is ALWAYS rewritten to failed/invalid_task_result and persists NOTHING,
# while a well-formed completed result still passes (the gate discriminates).
#
# Seam: LOOM_DRIVER_TASK_RUNNER_CMD_JSON points the host-bridge runCommand at a
# tiny stub that ignores the runner and prints ONE chosen bad/good JSON object;
# the Go pre-persist gate (validateBridgeTaskRunnerResult, task_bridge.go) +
# TaskRun-level requireTerminalStatus (task_request.go) are exercised directly.
# No real backend CLI / auth needed — fully deterministic and CI-friendly.
#
# Covers (catalog docs/design/2026-06-16-productionize-runner-e2e-test-catalog.md):
#   VAL-01  empty {}                       -> failed/invalid_task_result
#   VAL-07  completed + exit_code 2        -> failed/invalid_task_result (never completed)
#   VAL-03+VAL-11  missing status + patch+logs+artifacts bait
#                                          -> failed/invalid_task_result + ZERO persisted
#   VAL-12  well-formed completed/0        -> completed (positive control)
#   OS-01   runner=openshell-task-runner   -> resolve-denied (openshell_runner_unimplemented),
#                                             NO completed task-run, driver-run not completed
#
# NOT covered here — NOOP-01 (noop provider fail-closed) needs a child task-run
# request with provider_profile + NO runner, which routes through LocalTaskExecutor.
# That request requires a RUNNING parent driver-run, and the only no-credential
# path (verifyTaskRunRequestParent) is a running run with no lease — which the
# epic-runner pipeline never produces (the executor's claim attaches a lease, and
# DriverRunCreate has no status field). It stays proven by the unit gate
# (internal/driver TestNoopProviderGate, both branches). See catalog §5.
#
# NOTE (catalog §5 correction): stringized/boolean exit_code (VAL-08/09) cannot be
# driven through this Go stub seam (Go decodes exit_code into *int -> decode error,
# not the validate path); those stay on the JS layer (already unit-covered).
#
#   usage: scripts/test-runner-failclosed-matrix.sh
#   env:   KEEP=1  keep the temp workspace + logs on success

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
. "$ROOT/scripts/lib/sandbox.sh"
FLEET_DB_REPO="${FLEET_DB_REPO:-$(cd "$ROOT/../fleet-db" 2>/dev/null && pwd || true)}"

WORKSPACE="${LOOM_FAILCLOSED_WORKSPACE:-FAILCLOSED}"
ACTOR="failclosed-matrix"
SOURCE_REPO="local/repo"
freeport() { node -e 'const s=require("node:net").createServer();s.listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close();});'; }
REDIS_PORT="${REDIS_PORT:-$(freeport)}"
FLEET_PORT="${FLEET_PORT:-$(freeport)}"
LOOM_PORT="${LOOM_PORT:-$(freeport)}"
FLEET_URL="http://127.0.0.1:${FLEET_PORT}"
LOOM_URL="http://127.0.0.1:${LOOM_PORT}"
NODE_ID="failclosed-node"

loom_mktemp_dir test-runner-failclosed-matrix; TMP_ROOT="$LOOM_SANDBOX_DIR"
BIN_DIR="$TMP_ROOT/bin"
REPO="$TMP_ROOT/repo"
STAGE="$TMP_ROOT/dist"
LOOM_CONFIG_DIR="$TMP_ROOT/loom-config"
MODE_MAP="$TMP_ROOT/modes.json"
STUB="$TMP_ROOT/runner-result-stub.cjs"
mkdir -p "$BIN_DIR" "$REPO" "$LOOM_CONFIG_DIR"
echo '{}' > "$MODE_MAP"

PIDS=()
FAILURES=0
RAN=0

log_step() { printf '\n==> %s\n' "$1"; }
log_info() { printf '    %s\n' "$1"; }
die() { echo "ERROR: $*" >&2; exit 1; }
require_cmd() { command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"; }

cleanup() {
  local status=$?
  for pid in "${PIDS[@]:-}"; do kill "$pid" >/dev/null 2>&1 || true; done
  if [[ "$status" -ne 0 || "$FAILURES" -ne 0 ]]; then
    echo
    echo "fail-closed matrix did not pass cleanly; logs under $TMP_ROOT" >&2
    for log in "$TMP_ROOT"/*.log; do
      [[ -f "$log" ]] || continue
      echo "--- ${log##*/} ---" >&2
      tail -80 "$log" >&2 || true
    done
  elif [[ "${KEEP:-0}" != "1" ]]; then
    rm -rf "$TMP_ROOT"
  else
    echo "kept fail-closed matrix workspace at $TMP_ROOT"
  fi
}
trap cleanup EXIT INT TERM

curl_json() {
  local method="$1" path="$2" body="${3:-}"
  if [[ -n "$body" ]]; then
    curl -fsS -X "$method" -H 'Content-Type: application/json' -H "X-Actor: $ACTOR" --data "$body" "$FLEET_URL$path"
  else
    curl -fsS -X "$method" -H "X-Actor: $ACTOR" "$FLEET_URL$path"
  fi
}

wait_http() {
  local url="$1" name="$2"
  for _ in $(seq 1 100); do curl -fsS "$url" >/dev/null 2>&1 && return 0; sleep 0.25; done
  die "timed out waiting for $name at $url"
}

check_prerequisites() {
  require_cmd go; require_cmd git; require_cmd jq; require_cmd curl; require_cmd node; require_cmd redis-server
  [[ -n "$FLEET_DB_REPO" && -d "$FLEET_DB_REPO/cmd/fleet-db" ]] || die "fleet-db repo not found; set FLEET_DB_REPO=/path/to/fleet-db"
  [[ -d "$ROOT/internal/workflows/builtin-dist/epic-runner/dist" ]] || die "builtin epic-runner dist missing; run 'flue build' first"
}

build_binaries() {
  log_step "building loom + fleet-db"
  ( cd "$FLEET_DB_REPO"; GOCACHE="${GOCACHE:-/tmp/go-build-cache}" go build -o "$BIN_DIR/fleet-db" ./cmd/fleet-db )
  ( cd "$ROOT";          GOCACHE="${GOCACHE:-/tmp/go-build-cache}" go build -o "$BIN_DIR/loom" ./cmd/loom )
}

write_stub() {
  # A raw runner-command stub: it IS the host-bridge runCommand. It reads the
  # task_id from LOOM_TASK_RUN_REQUEST_JSON, looks up the per-task mode in
  # $STUB_MODE_MAP, and prints exactly one bridgeTaskRunnerResult JSON object as
  # the LAST stdout line. Always exits 0 (the result's `status` drives outcome).
  cat > "$STUB" <<'EOF'
const fs = require("node:fs");
function out(obj) { process.stdout.write(JSON.stringify(obj) + "\n"); }
let req = {};
try { req = JSON.parse(process.env.LOOM_TASK_RUN_REQUEST_JSON || "{}"); } catch {}
const taskId = req.task_id || req.task_run_id || "";
let mode = "good";
try {
  const map = JSON.parse(fs.readFileSync(process.env.STUB_MODE_MAP, "utf8"));
  if (map[taskId]) mode = map[taskId];
} catch {}
// A syntactically-valid patch that would create smuggled.txt if (wrongly) applied.
const smuggledPatch = [
  "diff --git a/smuggled.txt b/smuggled.txt",
  "new file mode 100644",
  "index 0000000..a000000",
  "--- /dev/null",
  "+++ b/smuggled.txt",
  "@@ -0,0 +1 @@",
  "+smuggled by an invalid runner result",
  "",
].join("\n");
const bait = {
  patch: smuggledPatch,
  base_ref: "HEAD",
  logs: "smuggled logs that must never persist",
  artifacts: [{ id: "smuggled-artifact", type: "patch", mime_type: "text/x-diff", content: smuggledPatch }],
};
switch (mode) {
  case "empty": // VAL-01
    out({});
    break;
  case "completed-nonzero": // VAL-07: completed but nonzero -> must be rejected
    out({ status: "completed", exit_code: 2, ...bait });
    break;
  case "missing-status": // VAL-03 + VAL-11: no status, carrying persist-bait -> reject + persist nothing
    out({ exit_code: 0, ...bait });
    break;
  case "good": // VAL-12 positive control
  default:
    out({ status: "completed", exit_code: 0, logs: "fail-closed stub ok",
          runtime_metadata: { task_runner: "failclosed-stub", runtime_strategy: "failclosed-stub" } });
    break;
}
EOF
}

seed_repo() {
  log_step "seeding host worktree (git repo == serve cwd)"
  ( cd "$REPO"
    git init -q -b main
    printf '# fail-closed sandbox\n' > README.md
    printf '.loom/\nnotify.token\n' > .gitignore
    git add -A
    git -c user.email=fc@x.test -c user.name=fc commit -qm base ) || die "git seed failed"
}

stage_dist() {
  cp -R "$ROOT/internal/workflows/builtin-dist/epic-runner/dist" "$STAGE"
  printf '%s\n' '{"runners":"[{\"name\":\"local-task-runner\",\"kind\":\"flue-workflow\",\"entrypoint\":\"local-task-runner\"}]"}' > "$STAGE/loom-driver.json"
}

start_services() {
  log_step "starting redis on ${REDIS_PORT} + fleet-db on ${FLEET_PORT}"
  redis-server --port "$REDIS_PORT" --save "" --appendonly no >"$TMP_ROOT/redis.log" 2>&1 & PIDS+=("$!")
  sleep 0.4
  "$BIN_DIR/fleet-db" -addr "127.0.0.1:${FLEET_PORT}" -backend redis -redis-addr "127.0.0.1:${REDIS_PORT}" \
    -redis-durability-profile managed -redis-max-retries 0 -redis-cb-fail-threshold 0 \
    -auth-dev-mode -authz-enabled=false -rpc-enabled=false -log-format text >"$TMP_ROOT/fleet-db.log" 2>&1 & PIDS+=("$!")
  wait_http "$FLEET_URL/healthz" "fleet-db"
}

seed_workspace() {
  log_step "seeding workspace + executor node"
  curl_json POST /api/v1/admin/workspaces "$(jq -nc --arg key "$WORKSPACE" --arg repo "$SOURCE_REPO" '{key:$key,name:"Fail-Closed Matrix",repos:[$repo],state:"ready"}')" >/dev/null
  curl_json POST "/api/v1/${WORKSPACE}/nodes" "$(jq -nc --arg node "$NODE_ID" --arg actor "$ACTOR" '{node_id:$node,owner_actor:$actor,runtime_provider:"local",drain_state:"active",capacity:8,ttl_seconds:600}')" >/dev/null
  export LOOM_CONFIG_DIR LOOM_WORKSPACE="$WORKSPACE" LOOM_FLEET_DB_URL="$FLEET_URL" LOOM_FLEET_DB_ACTOR="$ACTOR"
}

register_driver() {
  log_step "registering builtin epic-runner dist"
  ( cd "$REPO"; "$BIN_DIR/loom" --workspace "$WORKSPACE" driver register --flue-dist "$STAGE" \
      --name epic-runner --id epic-runner --workflow epic-runner --source-ref "builtin://epic-runner" --trusted --activate --json ) \
    >"$TMP_ROOT/register.json" 2>&1 || { tail -30 "$TMP_ROOT/register.json"; die "driver register failed"; }
}

start_loom() {
  local cmd_json
  cmd_json="$(node -e 'console.log(JSON.stringify(process.argv.slice(1)))' \
    env "STUB_MODE_MAP=$MODE_MAP" "$(command -v node)" "$STUB")"
  log_step "starting loom serve --driver-executor on ${LOOM_PORT}"
  ( cd "$REPO"; LOOM_DISABLE_H2C=1 LOOM_DRIVER_EXECUTOR=1 LOOM_DRIVER_EXECUTOR_NODE_ID="$NODE_ID" \
      LOOM_DRIVER_TASK_RUNNER_CMD_JSON="$cmd_json" LOOM_DRIVER_TASK_RUN_MAX_ATTEMPTS=1 \
      "$BIN_DIR/loom" serve --port "$LOOM_PORT" --frontend-url "http://127.0.0.1:9" ) \
    >"$TMP_ROOT/loom-serve.log" 2>&1 & PIDS+=("$!")
  wait_http "$LOOM_URL/health" "loom serve"
}

create_issue() { "$BIN_DIR/loom" --workspace "$WORKSPACE" data create -o json "$@" | jq -r '.id'; }

set_mode() { # task_id mode
  jq --arg id "$1" --arg mode "$2" '.[$id]=$mode' "$MODE_MAP" > "$MODE_MAP.tmp" && mv "$MODE_MAP.tmp" "$MODE_MAP"
}

wait_terminal_driver_run() { # run_id
  local run_id="$1" status=""
  for _ in $(seq 1 180); do
    status="$(curl_json GET "/api/v1/${WORKSPACE}/driver-runs/${run_id}" | jq -r '.status // ""')"
    case "$status" in completed|failed|needs_review|cancelled) echo "$status"; return 0;; esac
    sleep 0.5
  done
  echo "$status"
}

# scenario: id mode expected_status expected_err  [persist_check]
run_scenario() {
  local id="$1" mode="$2" want_status="$3" want_err="$4" persist_check="${5:-}"
  local epic task run_id trj got_status got_err logs_ref drv
  log_step "scenario ${id} (mode=${mode})"
  epic="$(create_issue --title "${id} epic" --type epic --priority 1)"
  task="$(create_issue --title "${id} task" --type task --parent "$epic" --priority 1 --source-repo "$SOURCE_REPO" \
            --description "fail-closed ${id} (${mode})")"
  set_mode "$task" "$mode"
  run_id="run-failclosed-${id}"
  ( cd "$REPO"; "$BIN_DIR/loom" --workspace "$WORKSPACE" driver run epic-runner --epic "$epic" --run-id "$run_id" \
      --input "runner=local-task-runner" --input "maxConcurrency=1" --json ) >"$TMP_ROOT/${run_id}.json" 2>&1 || true
  drv="$(wait_terminal_driver_run "$run_id")"

  trj="$(curl_json GET "/api/v1/${WORKSPACE}/task-runs?driver_run_id=${run_id}")"
  echo "$trj" >"$TMP_ROOT/${run_id}-task-runs.json"
  got_status="$(jq -r '.task_runs[0].status // "<none>"' <<<"$trj")"
  got_err="$(jq -r '.task_runs[0].error_class // ""' <<<"$trj")"
  logs_ref="$(jq -r '.task_runs[0].logs_ref // ""' <<<"$trj")"

  local ok=1 detail="status=${got_status} error_class=${got_err:-<empty>} driver_run=${drv}"
  [[ "$got_status" == "$want_status" ]] || { ok=0; detail+=" [want status=${want_status}]"; }
  if [[ -n "$want_err" ]]; then
    [[ "$got_err" == "$want_err" ]] || { ok=0; detail+=" [want error_class=${want_err}]"; }
  else
    [[ -z "$got_err" || "$got_err" == "null" ]] || { ok=0; detail+=" [want empty error_class]"; }
  fi

  if [[ "$persist_check" == "no-persist" ]]; then
    # VAL-11: invalid result smuggling patch+logs+artifacts must persist NOTHING.
    [[ -z "$logs_ref" || "$logs_ref" == "null" ]] || { ok=0; detail+=" [logs_ref leaked: ${logs_ref}]"; }
    [[ ! -e "$REPO/smuggled.txt" ]] || { ok=0; detail+=" [smuggled.txt was applied!]"; }
    local dirty; dirty="$(cd "$REPO"; git status --porcelain)"
    [[ -z "$dirty" ]] || { ok=0; detail+=" [worktree dirty: ${dirty}]"; }
  fi

  RAN=$((RAN+1))
  if [[ "$ok" == "1" ]]; then printf '    PASS  %s :: %s\n' "$id" "$detail"
  else printf '    FAIL  %s :: %s\n' "$id" "$detail"; FAILURES=$((FAILURES+1)); fi
}

# OS-01: an explicit unsupported runner is denied at resolve time, BEFORE any
# TaskRun is created. Forwarded through the epic-runner via --input runner=...,
# the child exec-task request hits resolveDriverRunner -> ErrOpenShellRunnerUnimplemented.
run_openshell_scenario() {
  local id="OS-01" epic task run_id drv trj
  log_step "scenario ${id} (runner=openshell-task-runner)"
  epic="$(create_issue --title "${id} epic" --type epic --priority 1)"
  task="$(create_issue --title "${id} task" --type task --parent "$epic" --priority 1 --source-repo "$SOURCE_REPO" \
            --description "fail-closed ${id} openshell")"
  run_id="run-failclosed-${id}"
  ( cd "$REPO"; "$BIN_DIR/loom" --workspace "$WORKSPACE" driver run epic-runner --epic "$epic" --run-id "$run_id" \
      --input "runner=openshell-task-runner" --input "maxConcurrency=1" --json ) >"$TMP_ROOT/${run_id}.json" 2>&1 || true
  drv="$(wait_terminal_driver_run "$run_id")"
  trj="$(curl_json GET "/api/v1/${WORKSPACE}/task-runs?driver_run_id=${run_id}")"
  echo "$trj" >"$TMP_ROOT/${run_id}-task-runs.json"
  # The resolve guard (ErrOpenShellRunnerUnimplemented) denies the child exec-task
  # request BEFORE createQueuedTaskRun, so ZERO TaskRun rows exist and the
  # host-bridge exec-task op returns HTTP 400. (The openshell_runner_unimplemented
  # error_class itself is in the 400 response body, not the serve log; it is
  # unit-proven by TestResolveDriverRunnerGuardsOpenShell.)
  local total_count exec_400
  total_count="$(jq '.count // (.task_runs | length)' <<<"$trj")"
  exec_400="$(grep -c 'exec-task status=400' "$TMP_ROOT/loom-serve.log" 2>/dev/null || true)"

  local ok=1 detail="driver_run=${drv} task_runs=${total_count} exec_task_400=${exec_400}"
  [[ "$drv" != "completed" ]]   || { ok=0; detail+=" [driver_run must not be completed]"; }
  [[ "$total_count" == "0" ]]   || { ok=0; detail+=" [resolve-deny must create ZERO task-run rows; got ${total_count}]"; }
  [[ "${exec_400:-0}" -ge 1 ]]  || { ok=0; detail+=" [exec-task resolve-deny (HTTP 400) not observed in serve log]"; }

  RAN=$((RAN+1))
  if [[ "$ok" == "1" ]]; then printf '    PASS  %s :: %s\n' "$id" "$detail"
  else printf '    FAIL  %s :: %s\n' "$id" "$detail"; FAILURES=$((FAILURES+1)); fi
}

main() {
  check_prerequisites
  build_binaries
  write_stub
  seed_repo
  stage_dist
  start_services
  seed_workspace
  register_driver
  start_loom

  # VAL-12 positive control FIRST: proves the gate passes a well-formed result
  # (fail-closed != fail-always) before we assert the rejections.
  run_scenario "VAL-12" "good"             "completed" ""                    ""
  run_scenario "VAL-01" "empty"            "failed"    "invalid_task_result" ""
  run_scenario "VAL-07" "completed-nonzero" "failed"   "invalid_task_result" ""
  run_scenario "VAL-11" "missing-status"   "failed"    "invalid_task_result" "no-persist"

  # OS-01: resolve-time deny of an unsupported runner (Req 3).
  run_openshell_scenario

  echo
  if [[ "$FAILURES" -eq 0 ]]; then
    echo "fail-closed matrix PASSED (${RAN}/${RAN} scenarios)"
    echo "  FleetDB: ${FLEET_URL}   Loom: ${LOOM_URL}"
  else
    echo "fail-closed matrix FAILED (${FAILURES}/${RAN} scenario(s))"
    exit 1
  fi
}

main "$@"
