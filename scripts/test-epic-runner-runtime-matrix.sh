#!/usr/bin/env bash
set -Eeuo pipefail

# E2E matrix for the built-in epic-runner runtime selector.
#
# It registers one epic-runner bundle with three user-authored runners:
# - regular-local-runner: plain node-module, no Flue import
# - flue-local-task-runner: Flue harness using @flue/runtime/node local()
# - flue-daytona-task-runner: Flue harness using an application-owned Daytona sandbox
#
# Daytona is live/external and only runs when RUN_DAYTONA=1, or when
# RUN_DAYTONA=auto and DAYTONA_API_KEY is present.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FLEET_DB_REPO="${FLEET_DB_REPO:-$(cd "$ROOT/../fleet-db" 2>/dev/null && pwd || true)}"
FLUE_REPO="${FLUE_REPO:-$(cd "$ROOT/../flue" 2>/dev/null && pwd || true)}"

WORKSPACE="${LOOM_EPIC_RUNNER_MATRIX_WORKSPACE:-RUNTIMEMATRIX}"
RUN_PREFIX="${LOOM_EPIC_RUNNER_MATRIX_RUN_PREFIX:-run-runtime-matrix}"
REDIS_PORT="${REDIS_PORT:-$(node -e 'const s=require("node:net").createServer();s.listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close();});')}"
FLEET_PORT="${FLEET_PORT:-$(node -e 'const s=require("node:net").createServer();s.listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close();});')}"
LOOM_PORT="${LOOM_PORT:-$(node -e 'const s=require("node:net").createServer();s.listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close();});')}"
FLEET_URL="http://127.0.0.1:${FLEET_PORT}"
LOOM_URL="http://127.0.0.1:${LOOM_PORT}"
# This runtime matrix intentionally runs FleetDB without authorization and
# does not prove the Workflow Catalog lifecycle capability.
export LOOM_WORKFLOW_CATALOG_ENABLED=false

RUN_DAYTONA="${RUN_DAYTONA:-auto}"
DAYTONA_REPO_URL="${DAYTONA_REPO_URL:-https://github.com/tysonthomas9/loom-review-sandbox.git}"
DAYTONA_SDK_IMPORT="${DAYTONA_SDK_IMPORT:-}"

TMP_ROOT="$(mktemp -d -t loom-epic-runner-runtime-matrix.XXXXXX)"
BIN_DIR="$TMP_ROOT/bin"
WORKDIR="$TMP_ROOT/work"
DIST_DIR="$WORKDIR/dist"
LOOM_CONFIG_DIR="$TMP_ROOT/loom-config"
RUNTIME_MATRIX_OUTPUT_DIR="$WORKDIR/.loom/runtime-matrix"
SOURCE_REPO="local/repo"
mkdir -p "$BIN_DIR" "$WORKDIR" "$LOOM_CONFIG_DIR" "$RUNTIME_MATRIX_OUTPUT_DIR"

PIDS=()
SCENARIOS=()

log_step() {
  printf '\n==> %s\n' "$1"
}

log_info() {
  printf '    %s\n' "$1"
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
    echo "epic-runner runtime matrix failed; logs are under $TMP_ROOT" >&2
    for log in "$TMP_ROOT"/*.log; do
      [[ -f "$log" ]] || continue
      echo "--- ${log##*/} ---" >&2
      tail -160 "$log" >&2 || true
    done
  elif [[ "${KEEP_EPIC_RUNNER_MATRIX:-0}" != "1" ]]; then
    rm -rf "$TMP_ROOT"
  else
    echo "kept epic-runner runtime matrix workspace at $TMP_ROOT"
  fi
}
trap cleanup EXIT

curl_json() {
  local method="$1"
  local path="$2"
  local body="${3:-}"

  if [[ -n "$body" ]]; then
    curl -fsS -X "$method" \
      -H 'Content-Type: application/json' \
      -H 'X-Actor: epic-runner-runtime-matrix' \
      --data "$body" \
      "$FLEET_URL$path"
  else
    curl -fsS -X "$method" \
      -H 'X-Actor: epic-runner-runtime-matrix' \
      "$FLEET_URL$path"
  fi
}

wait_http() {
  local url="$1"
  local name="$2"

  for _ in $(seq 1 100); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.25
  done

  die "timed out waiting for $name at $url"
}

should_run_daytona() {
  case "$RUN_DAYTONA" in
    1 | true | TRUE | yes | YES | on | ON) return 0 ;;
    0 | false | FALSE | no | NO | off | OFF) return 1 ;;
    auto)
      [[ -n "${DAYTONA_API_KEY:-}" ]]
      return
      ;;
    *) die "RUN_DAYTONA must be 1, 0, or auto; got $RUN_DAYTONA" ;;
  esac
}

resolve_runtime_imports() {
  [[ -n "$FLUE_REPO" && -d "$FLUE_REPO/packages/runtime" ]] ||
    die "Flue repo not found; set FLUE_REPO=/path/to/flue"

  local runtime_index="$FLUE_REPO/packages/runtime/dist/index.mjs"
  local runtime_node="$FLUE_REPO/packages/runtime/dist/node/index.mjs"
  local runtime_internal="$FLUE_REPO/packages/runtime/dist/internal.mjs"
  [[ -f "$runtime_index" && -f "$runtime_node" && -f "$runtime_internal" ]] ||
    die "Flue runtime dist is missing; build the Flue repo first"

  FLUE_RUNTIME_IMPORT="file://$(realpath "$runtime_index")"
  FLUE_RUNTIME_NODE_IMPORT="file://$(realpath "$runtime_node")"
  FLUE_RUNTIME_INTERNAL_IMPORT="file://$(realpath "$runtime_internal")"
  export FLUE_RUNTIME_IMPORT FLUE_RUNTIME_NODE_IMPORT FLUE_RUNTIME_INTERNAL_IMPORT

  if [[ -z "$DAYTONA_SDK_IMPORT" ]]; then
    local daytona_pkg="$FLUE_REPO/node_modules/.pnpm/node_modules/@daytona/sdk"
    if [[ -f "$daytona_pkg/esm/index.js" ]]; then
      DAYTONA_SDK_IMPORT="file://$(realpath "$daytona_pkg")/esm/index.js"
    fi
  fi
  export DAYTONA_SDK_IMPORT
}

check_prerequisites() {
  require_cmd go
  require_cmd git
  require_cmd jq
  require_cmd curl
  require_cmd node
  require_cmd redis-server

  [[ -n "$FLEET_DB_REPO" && -d "$FLEET_DB_REPO/cmd/fleet-db" ]] ||
    die "fleet-db repo not found; set FLEET_DB_REPO=/path/to/fleet-db"

  resolve_runtime_imports

  if should_run_daytona; then
    [[ -n "${DAYTONA_API_KEY:-}" ]] || die "DAYTONA_API_KEY is required when RUN_DAYTONA=1"
    [[ -n "$DAYTONA_SDK_IMPORT" ]] || die "DAYTONA_SDK_IMPORT is required; no local @daytona/sdk install was found"
    [[ -n "$DAYTONA_REPO_URL" ]] || die "DAYTONA_REPO_URL is required for the Daytona runner"
  fi
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

create_dummy_repo() {
  log_step "creating dummy repo"
  mkdir -p "$WORKDIR/dummy-repo/src" "$WORKDIR/dummy-repo/test"
  cat > "$WORKDIR/dummy-repo/package.json" <<'EOF'
{
  "type": "module",
  "scripts": {
    "test": "node test/smoke.test.mjs"
  }
}
EOF
  cat > "$WORKDIR/dummy-repo/src/app.js" <<'EOF'
export function title() {
  return "Loom runtime matrix";
}
EOF
  cat > "$WORKDIR/dummy-repo/test/smoke.test.mjs" <<'EOF'
import assert from "node:assert/strict";
import { title } from "../src/app.js";

assert.equal(title(), "Loom runtime matrix");
EOF
  cat > "$WORKDIR/dummy-repo/README.md" <<'EOF'
# Loom Runtime Matrix Dummy Repo

This repo is intentionally tiny. The epic-runner matrix uses it to verify that
plain local, Flue local, and Daytona-backed runners can execute against repo
content without provider-profile routing.
EOF
  (
    cd "$WORKDIR/dummy-repo"
    git init -q -b main
    git add .
    git -c user.name=Loom -c user.email=loom@example.test commit -m "seed runtime matrix dummy repo" -q
  )
  log_info "local dummy repo: $WORKDIR/dummy-repo"
  log_info "Daytona repo URL: $DAYTONA_REPO_URL"
}

copy_loom_sdk() {
  mkdir -p "$DIST_DIR/node_modules/@loom/sdk"
  cp "$ROOT/sdk/package.json" "$DIST_DIR/node_modules/@loom/sdk/package.json"
  cp "$ROOT/sdk/driver.js" "$DIST_DIR/node_modules/@loom/sdk/driver.js"
  cp "$ROOT/sdk/runner.js" "$DIST_DIR/node_modules/@loom/sdk/runner.js"
  cp "$ROOT/sdk/internal.js" "$DIST_DIR/node_modules/@loom/sdk/internal.js"
}

write_epic_workflow() {
  mkdir -p "$DIST_DIR/workflows"
  cp "$ROOT/internal/workflows/builtin/epic-runner.ts" "$DIST_DIR/workflows/epic-runner.mjs"
}

write_regular_local_runner() {
  mkdir -p "$DIST_DIR/runners"
  cat > "$DIST_DIR/runners/regular-local-runner.mjs" <<'EOF'
import fs from "node:fs/promises";
import path from "node:path";

function safeName(value) {
  return String(value || "task").replace(/[^A-Za-z0-9_.-]/g, "_");
}

export async function run(ctx) {
  const request = ctx.request || {};
  const root = process.env.RUNTIME_MATRIX_OUTPUT_DIR || path.join(ctx.worktreePath || process.cwd(), ".loom", "runtime-matrix");
  const dir = path.join(root, "regular-local", safeName(request.task_id));
  await fs.mkdir(dir, { recursive: true });
  await fs.writeFile(path.join(dir, "request.json"), JSON.stringify({
    runner: request.runner,
    runner_kind: request.runner_kind,
    runner_entrypoint: request.runner_entrypoint,
    task_id: request.task_id,
    task_run_id: request.task_run_id,
    lease_token_received_by_host_runner: String(Boolean(request.lease_token)),
  }, null, 2) + "\n");
  await fs.writeFile(path.join(dir, "agent-output.txt"), "REGULAR_LOCAL_RUNTIME_OK\n");
  return {
    status: "completed",
    exitCode: 0,
    logsRef: "logs://" + request.task_run_id,
    runtimeMetadata: {
      task_runner: "regular-local-runner",
      runtime_strategy: "regular-local",
      runner: String(request.runner || "regular-local-runner"),
      task_id: String(request.task_id || ""),
      output_dir: dir,
    },
  };
}
EOF
}

write_flue_local_runner() {
  cat > "$DIST_DIR/workflows/flue-local-task-runner.mjs" <<'EOF'
import fs from "node:fs/promises";
import path from "node:path";

function safeName(value) {
  return String(value || "task").replace(/[^A-Za-z0-9_.-]/g, "_");
}

async function createContext(id, payload) {
  const internal = await import(process.env.FLUE_RUNTIME_INTERNAL_IMPORT);
  const node = await import(process.env.FLUE_RUNTIME_NODE_IMPORT);
  return internal.createFlueContext({
    id,
    payload,
    env: process.env,
    agentConfig: {
      systemPrompt: "",
      skills: {},
      model: undefined,
      resolveModel: () => undefined,
    },
    createDefaultEnv: async () => node.local().createSessionEnv(),
    defaultStore: new internal.InMemorySessionStore(),
  });
}

export async function run(ctx) {
  const request = ctx.payload || {};
  const runtime = await import(process.env.FLUE_RUNTIME_IMPORT);
  const node = await import(process.env.FLUE_RUNTIME_NODE_IMPORT);
  const root = process.env.RUNTIME_MATRIX_OUTPUT_DIR || path.join(process.cwd(), ".loom", "runtime-matrix");
  const dir = path.join(root, "flue-local", safeName(request.task_id));
  await fs.mkdir(dir, { recursive: true });

  const flue = await createContext(request.task_run_id || request.task_id || "flue-local", request);
  const agent = runtime.createAgent(() => ({
    model: false,
    sandbox: node.local({ cwd: dir, env: { PATH: process.env.PATH || "" } }),
  }));
  const harness = await flue.init(agent, { name: "flue-local-runtime-matrix" });
  const pwd = await harness.shell("pwd");
  await harness.shell("printf '%s\\n' FLUE_LOCAL_RUNTIME_OK > agent-output.txt");
  const marker = await harness.shell("cat agent-output.txt");
  const leaseProbe = await harness.shell("printf '%s' \"$LOOM_TASK_RUN_LEASE_TOKEN\"");

  if (pwd.exitCode !== 0 || marker.stdout.trim() !== "FLUE_LOCAL_RUNTIME_OK") {
    throw new Error("Flue local sandbox verification failed");
  }
  if (leaseProbe.stdout.trim() !== "") {
    throw new Error("task-run lease token leaked into the Flue local sandbox env");
  }

  await fs.writeFile(path.join(dir, "request.json"), JSON.stringify({
    runner: request.runner,
    runner_kind: request.runner_kind,
    runner_entrypoint: request.runner_entrypoint,
    task_id: request.task_id,
    task_run_id: request.task_run_id,
    lease_token_received_by_host_runner: String(Boolean(request.lease_token)),
  }, null, 2) + "\n");

  return {
    status: "completed",
    exitCode: 0,
    logsRef: "logs://" + request.task_run_id,
    runtimeMetadata: {
      task_runner: "flue-local-task-runner",
      runtime_strategy: "flue-local",
      runner: String(request.runner || "flue-local-task-runner"),
      task_id: String(request.task_id || ""),
      sandbox_cwd: dir,
      sandbox_pwd: pwd.stdout.trim(),
      lease_token_visible_in_sandbox: "false",
    },
  };
}
EOF
}

write_flue_daytona_runner() {
  cat > "$DIST_DIR/workflows/flue-daytona-task-runner.mjs" <<'EOF'
function shellQuote(value) {
  return "'" + String(value).replace(/'/g, "'\\''") + "'";
}

function stringMetadata(values = {}) {
  const out = {};
  for (const [key, value] of Object.entries(values)) {
    if (value === undefined || value === null) continue;
    out[key] = typeof value === "string" ? value : String(value);
  }
  return out;
}

async function createContext(id, payload) {
  const internal = await import(process.env.FLUE_RUNTIME_INTERNAL_IMPORT);
  return internal.createFlueContext({
    id,
    payload,
    env: process.env,
    agentConfig: {
      systemPrompt: "",
      skills: {},
      model: undefined,
      resolveModel: () => undefined,
    },
    createDefaultEnv: async () => {
      throw new Error("Daytona runner requires explicit sandbox");
    },
    defaultStore: new internal.InMemorySessionStore(),
  });
}

class DaytonaSandboxApi {
  constructor(sandbox) {
    this.sandbox = sandbox;
  }
  async readFile(filePath) {
    const buffer = await this.sandbox.fs.downloadFile(filePath);
    return buffer.toString("utf-8");
  }
  async readFileBuffer(filePath) {
    const buffer = await this.sandbox.fs.downloadFile(filePath);
    return new Uint8Array(buffer);
  }
  async writeFile(filePath, content) {
    const buffer = typeof content === "string" ? Buffer.from(content, "utf-8") : Buffer.from(content);
    await this.sandbox.fs.uploadFile(buffer, filePath);
  }
  async stat(filePath) {
    const info = await this.sandbox.fs.getFileDetails(filePath);
    return {
      isFile: !info.isDir,
      isDirectory: info.isDir ?? false,
      isSymbolicLink: false,
      size: info.size ?? 0,
      mtime: info.modTime ? new Date(info.modTime) : new Date(),
    };
  }
  async readdir(filePath) {
    const entries = await this.sandbox.fs.listFiles(filePath);
    return entries.map((entry) => entry.name).filter(Boolean);
  }
  async exists(filePath) {
    try {
      await this.sandbox.fs.getFileDetails(filePath);
      return true;
    } catch {
      return false;
    }
  }
  async mkdir(filePath, options) {
    if (options?.recursive) {
      await this.exec("mkdir -p " + shellQuote(filePath));
      return;
    }
    await this.sandbox.fs.createFolder(filePath, "755");
  }
  async rm(filePath, options) {
    await this.sandbox.fs.deleteFile(filePath, options?.recursive);
  }
  async exec(command, options) {
    const response = await this.sandbox.process.executeCommand(
      command,
      options?.cwd,
      options?.env,
      options?.timeout,
    );
    return {
      stdout: response.result ?? "",
      stderr: "",
      exitCode: response.exitCode ?? 0,
    };
  }
}

function daytonaSandbox(runtime, sandbox) {
  return {
    async createSessionEnv() {
      const sandboxCwd = (await sandbox.getWorkDir()) || "/home/daytona";
      return runtime.createSandboxSessionEnv(new DaytonaSandboxApi(sandbox), sandboxCwd);
    },
  };
}

export async function run(ctx) {
  const request = ctx.payload || {};
  if (!process.env.DAYTONA_API_KEY) {
    throw new Error("DAYTONA_API_KEY is required");
  }
  if (!process.env.DAYTONA_SDK_IMPORT) {
    throw new Error("DAYTONA_SDK_IMPORT is required");
  }
  const repoUrl = process.env.DAYTONA_REPO_URL || "";
  if (!repoUrl) {
    throw new Error("DAYTONA_REPO_URL is required");
  }

  const runtime = await import(process.env.FLUE_RUNTIME_IMPORT);
  const sdk = await import(process.env.DAYTONA_SDK_IMPORT);
  const config = { apiKey: process.env.DAYTONA_API_KEY };
  if (process.env.DAYTONA_API_URL) config.apiUrl = process.env.DAYTONA_API_URL;
  if (process.env.DAYTONA_TARGET) config.target = process.env.DAYTONA_TARGET;
  const client = new sdk.Daytona(config);
  const sandbox = await client.create({
    labels: {
      loom: "epic-runner-runtime-matrix",
      task_run_id: String(request.task_run_id || ""),
    },
    autoStopInterval: 15,
    autoDeleteInterval: 0,
  });

  let sandboxId = sandbox.id || "";
  let workDir = "";
  try {
    workDir = (await sandbox.getWorkDir()) || "/home/daytona";
    const flue = await createContext(request.task_run_id || request.task_id || "flue-daytona", request);
    const agent = runtime.createAgent(() => ({
      model: false,
      sandbox: daytonaSandbox(runtime, sandbox),
    }));
    const harness = await flue.init(agent, { name: "flue-daytona-runtime-matrix" });
    const uname = await harness.shell("uname -s");
    const marker = await harness.shell("printf '%s\\n' FLUE_DAYTONA_RUNTIME_OK > /tmp/loom-runtime-matrix.txt && cat /tmp/loom-runtime-matrix.txt");
    const clone = await harness.shell("rm -rf /tmp/loom-runtime-matrix-repo && git clone --depth 1 " + shellQuote(repoUrl) + " /tmp/loom-runtime-matrix-repo", { timeout: 120 });
    const head = await harness.shell("git -C /tmp/loom-runtime-matrix-repo rev-parse --short HEAD");
    const leaseProbe = await harness.shell("printf '%s' \"$LOOM_TASK_RUN_LEASE_TOKEN\"");

    if (uname.exitCode !== 0 || !uname.stdout.includes("Linux")) {
      throw new Error("Daytona sandbox uname check failed: " + uname.stdout);
    }
    if (marker.stdout.trim() !== "FLUE_DAYTONA_RUNTIME_OK") {
      throw new Error("Daytona sandbox marker check failed");
    }
    if (clone.exitCode !== 0) {
      throw new Error("Daytona sandbox repo clone failed: " + clone.stdout + clone.stderr);
    }
    if (!head.stdout.trim()) {
      throw new Error("Daytona sandbox repo HEAD check failed");
    }
    if (leaseProbe.stdout.trim() !== "") {
      throw new Error("task-run lease token leaked into Daytona sandbox env");
    }

    return {
      status: "completed",
      exitCode: 0,
      logsRef: "logs://" + request.task_run_id,
      runtimeMetadata: stringMetadata({
        task_runner: "flue-daytona-task-runner",
        runtime_strategy: "flue-daytona",
        runner: request.runner || "flue-daytona-task-runner",
        task_id: request.task_id || "",
        daytona_sandbox_id: sandboxId,
        daytona_workdir: workDir,
        daytona_repo_url: repoUrl,
        daytona_repo_head: head.stdout.trim(),
        lease_token_visible_in_sandbox: "false",
      }),
    };
  } finally {
    try {
      await sandbox.delete(60);
    } catch (error) {
      console.error("warning: failed to delete Daytona sandbox " + sandboxId + ": " + (error?.message || error));
    }
  }
}
EOF
}

write_runtime_matrix_server() {
  cat > "$DIST_DIR/server.mjs" <<'EOF'
const workflowLoaders = {
  "epic-runner": () => import("./workflows/epic-runner.mjs"),
  "flue-local-task-runner": () => import("./workflows/flue-local-task-runner.mjs"),
  "flue-daytona-task-runner": () => import("./workflows/flue-daytona-task-runner.mjs"),
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

write_driver_manifest() {
  node - "$DIST_DIR" <<'EOF'
const fs = require("node:fs");
const dist = process.argv[2];
const runners = [
  { name: "regular-local-runner", kind: "node-module", entrypoint: "dist/runners/regular-local-runner.mjs" },
  { name: "flue-local-task-runner", kind: "flue-workflow", entrypoint: "flue-local-task-runner" },
  { name: "flue-daytona-task-runner", kind: "flue-workflow", entrypoint: "flue-daytona-task-runner" },
];
fs.writeFileSync(dist + "/loom-driver.json", JSON.stringify({ runners: JSON.stringify(runners) }, null, 2) + "\n");
EOF
}

write_driver_bundle() {
  log_step "writing mixed runner bundle"
  mkdir -p "$DIST_DIR"
  copy_loom_sdk
  write_epic_workflow
  write_regular_local_runner
  write_flue_local_runner
  write_flue_daytona_runner
  write_runtime_matrix_server
  write_driver_manifest
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

seed_workspace() {
  log_step "seeding workspace"
  curl_json POST /api/v1/admin/workspaces "$(jq -nc --arg key "$WORKSPACE" --arg repo "$SOURCE_REPO" '{key:$key,name:"Epic Runner Runtime Matrix",repos:[$repo],state:"ready"}')" >/dev/null

  export LOOM_CONFIG_DIR
  export LOOM_WORKSPACE="$WORKSPACE"
  export LOOM_FLEET_DB_URL="$FLEET_URL"
  export LOOM_FLEET_DB_ACTOR="epic-runner-runtime-matrix"
}

register_executor_node() {
  log_step "registering driver executor node"
  curl_json POST "/api/v1/${WORKSPACE}/nodes" "$(jq -nc --arg node "runtime-matrix-node" '{node_id:$node,owner_actor:"epic-runner-runtime-matrix",runtime_provider:"local",drain_state:"active",capacity:8,ttl_seconds:300}')" >/dev/null
}

register_driver() {
  log_step "registering mixed runner epic-runner"
  (
    cd "$WORKDIR"
    "$BIN_DIR/loom" --workspace "$WORKSPACE" driver register \
      --flue-dist dist \
      --name epic-runner \
      --id epic-runner \
      --workflow epic-runner \
      --source-ref "runtime-matrix://epic-runner" \
      --trusted \
      --activate \
      --json
  ) >"$TMP_ROOT/register.json"
}

start_loom_executor() {
  local task_runner_cmd_json
  local -a runner_cmd=(
    env
    "RUNTIME_MATRIX_OUTPUT_DIR=$RUNTIME_MATRIX_OUTPUT_DIR"
    "FLUE_RUNTIME_IMPORT=$FLUE_RUNTIME_IMPORT"
    "FLUE_RUNTIME_NODE_IMPORT=$FLUE_RUNTIME_NODE_IMPORT"
    "FLUE_RUNTIME_INTERNAL_IMPORT=$FLUE_RUNTIME_INTERNAL_IMPORT"
    "DAYTONA_SDK_IMPORT=$DAYTONA_SDK_IMPORT"
    "DAYTONA_API_URL=${DAYTONA_API_URL:-}"
    "DAYTONA_TARGET=${DAYTONA_TARGET:-}"
    "DAYTONA_REPO_URL=$DAYTONA_REPO_URL"
  )
  if [[ -n "${DAYTONA_API_KEY:-}" ]]; then
    runner_cmd+=("DAYTONA_API_KEY=$DAYTONA_API_KEY")
  fi
  runner_cmd+=("$(command -v node)" "$ROOT/scripts/loom-task-runner-invoker.mjs")
  task_runner_cmd_json="$(node -e 'console.log(JSON.stringify(process.argv.slice(1)))' "${runner_cmd[@]}")"

  log_step "starting loom serve driver executor on ${LOOM_PORT}"
  (
    cd "$WORKDIR"
    LOOM_DISABLE_H2C=1 \
    LOOM_DRIVER_EXECUTOR=1 \
    LOOM_DRIVER_EXECUTOR_NODE_ID=runtime-matrix-node \
    LOOM_DRIVER_TASK_RUNNER_CMD_JSON="$task_runner_cmd_json" \
    "$BIN_DIR/loom" serve --port "$LOOM_PORT" --frontend-url "http://127.0.0.1:9"
  ) >"$TMP_ROOT/loom-serve.log" 2>&1 &
  PIDS+=("$!")
  wait_http "$LOOM_URL/health" "loom serve"
}

seed_epic() {
  local label="$1"
  local epic task_a task_b
  epic="$(create_issue --title "${label} runtime epic" --type epic --priority 1)"
  task_a="$(create_issue --title "${label} task A" --type task --parent "$epic" --priority 1 --source-repo "$SOURCE_REPO")"
  task_b="$(create_issue --title "${label} task B" --type task --parent "$epic" --depends-on "$task_a" --priority 1 --source-repo "$SOURCE_REPO")"
  printf '%s,%s,%s\n' "$epic" "$task_a" "$task_b"
}

queue_driver_run() {
  local runner="$1"
  local epic="$2"
  local run_id="$3"
  (
    cd "$WORKDIR"
    "$BIN_DIR/loom" --workspace "$WORKSPACE" driver run epic-runner \
      --epic "$epic" \
      --run-id "$run_id" \
      --input "runner=${runner}" \
      --input "maxConcurrency=1" \
      --json
  ) >"$TMP_ROOT/${run_id}.json"
}

wait_for_completed_run() {
  local run_id="$1"
  local run_json status

  for _ in $(seq 1 240); do
    run_json="$(curl_json GET "/api/v1/${WORKSPACE}/driver-runs/${run_id}")"
    status="$(jq -r '.status' <<<"$run_json")"
    case "$status" in
      completed)
        jq . <<<"$run_json" >"$TMP_ROOT/${run_id}-final.json"
        return 0
        ;;
      failed|needs_review|cancelled)
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

verify_scenario() {
  local runner="$1"
  local expected_kind="$2"
  local expected_strategy="$3"
  local run_id="$4"
  local task_runs_json count bad_count

  task_runs_json="$(curl_json GET "/api/v1/${WORKSPACE}/task-runs?driver_run_id=${run_id}")"
  jq . <<<"$task_runs_json" >"$TMP_ROOT/${run_id}-task-runs.json"

  count="$(jq '[.task_runs[]] | length' <<<"$task_runs_json")"
  [[ "$count" == "2" ]] || die "${runner}: expected 2 child TaskRuns, got $count"

  bad_count="$(
    jq --arg runner "$runner" --arg kind "$expected_kind" --arg strategy "$expected_strategy" '
      [.task_runs[] | select(
        .status != "completed" or
        .runner != $runner or
        .runner_kind != $kind or
        (.provider_profile // "") != "" or
        ((.sandbox_placement.provider // "") != "") or
        (.runtime_metadata.task_runner_invoker != "loom-task-runner-invoker") or
        (.runtime_metadata.runtime_strategy != $strategy)
      )] | length
    ' <<<"$task_runs_json"
  )"
  [[ "$bad_count" == "0" ]] || die "${runner}: child TaskRuns did not match expected runtime metadata"
}

verify_runner_artifacts() {
  local strategy="$1"
  local marker="$2"
  local count
  count="$(
    find "$RUNTIME_MATRIX_OUTPUT_DIR/$strategy" -name agent-output.txt -type f -print 2>/dev/null |
      while IFS= read -r file; do
        grep -qx "$marker" "$file" && echo ok
      done |
      wc -l |
      tr -d ' '
  )"
  [[ "$count" == "2" ]] || die "expected two ${strategy} marker files, got $count"
}

run_scenario() {
  local label="$1"
  local runner="$2"
  local expected_kind="$3"
  local expected_strategy="$4"
  local marker="${5:-}"
  local ids epic run_id

  log_step "running ${label} scenario"
  ids="$(seed_epic "$label")"
  epic="${ids%%,*}"
  run_id="${RUN_PREFIX}-${expected_strategy}"
  queue_driver_run "$runner" "$epic" "$run_id"
  wait_for_completed_run "$run_id"
  verify_scenario "$runner" "$expected_kind" "$expected_strategy" "$run_id"
  if [[ -n "$marker" ]]; then
    verify_runner_artifacts "$expected_strategy" "$marker"
  fi
  SCENARIOS+=("${label}:${run_id}")
}

main() {
  check_prerequisites
  build_binaries
  create_dummy_repo
  write_driver_bundle
  start_services
  seed_workspace
  register_executor_node
  register_driver
  start_loom_executor

  run_scenario "regular local" "regular-local-runner" "node-module" "regular-local" "REGULAR_LOCAL_RUNTIME_OK"
  run_scenario "Flue local" "flue-local-task-runner" "flue-workflow" "flue-local" "FLUE_LOCAL_RUNTIME_OK"

  if should_run_daytona; then
    run_scenario "Flue Daytona" "flue-daytona-task-runner" "flue-workflow" "flue-daytona"
  else
    log_step "skipping Flue Daytona scenario"
    log_info "set RUN_DAYTONA=1 and DAYTONA_API_KEY to run the live Daytona sandbox leg"
  fi

  echo
  echo "epic-runner runtime matrix passed"
  echo "  FleetDB:   ${FLEET_URL}"
  echo "  Loom:      ${LOOM_URL}"
  echo "  Scenarios: ${SCENARIOS[*]}"
}

main "$@"
