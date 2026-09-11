#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FRONTEND_DIR="$ROOT_DIR/internal/webui/frontend"
MANIFEST="$ROOT_DIR/scripts/sse-ui-transition-manifest.tsv"
VERIFY="$ROOT_DIR/scripts/verify-sse-ui-transition-report.mjs"
CHECK_PROJECT="$ROOT_DIR/scripts/check-sse-ui-compose-project.sh"
PINNED_FLEET_REVISION="$(tr -d '[:space:]' <"$ROOT_DIR/scripts/sse-ui-transition-fleet-revision")"

backend="${SSE_UI_BACKEND:-}"
run_id="${SSE_UI_RUN_ID:-$(date -u +%Y%m%d%H%M%S)-$$}"
fleet_repo="${FLEET_DB_REPO:-$ROOT_DIR/../fleet-db}"
fleet_revision="${FLEET_DB_REVISION:-$PINNED_FLEET_REVISION}"
loom_revision="${LOOM_REVISION:-$(git -C "$ROOT_DIR" rev-parse HEAD)}"
artifacts="${SSE_UI_ARTIFACTS_DIR:-}"

usage() {
  cat <<'EOF'
Usage: scripts/test-sse-ui-transitions.sh --backend mocked|redis|postgres [options]

Options:
  --run-id ID             Unique lowercase run identity.
  --fleet-repo PATH       FleetDB checkout used for paired images.
  --fleet-revision SHA    Exact required FleetDB HEAD.
  --loom-revision SHA     Exact required LoomCLI HEAD.
  --artifacts PATH        New directory for reports, traces and service logs.
EOF
}

while (($#)); do
  case "$1" in
    --backend) backend="${2:-}"; shift 2 ;;
    --run-id) run_id="${2:-}"; shift 2 ;;
    --fleet-repo) fleet_repo="${2:-}"; shift 2 ;;
    --fleet-revision) fleet_revision="${2:-}"; shift 2 ;;
    --loom-revision) loom_revision="${2:-}"; shift 2 ;;
    --artifacts) artifacts="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

case "$backend" in mocked|redis|postgres) ;; *) echo "--backend must be mocked, redis or postgres" >&2; exit 2 ;; esac
if [[ ! "$run_id" =~ ^[a-z0-9][a-z0-9-]{0,47}$ ]]; then
  echo "--run-id must match ^[a-z0-9][a-z0-9-]{0,47}$" >&2
  exit 2
fi
if [[ ! "$loom_revision" =~ ^[0-9a-f]{40}$ ]]; then
  echo "--loom-revision must be a full 40-character commit SHA" >&2
  exit 2
fi
artifacts="${artifacts:-$ROOT_DIR/tmp/sse-ui-transitions/$run_id/$backend}"
if [[ "$backend" != mocked && ! "$fleet_revision" =~ ^[0-9a-f]{40}$ ]]; then
  echo "--fleet-revision must be a full 40-character commit SHA" >&2
  exit 2
fi

log() { printf '[sse-ui:%s] %s\n' "$backend" "$*"; }
fatal() { printf '[sse-ui:%s] FATAL: %s\n' "$backend" "$*" >&2; exit 1; }
require_cmd() { command -v "$1" >/dev/null 2>&1 || fatal "$1 is required"; }

sha256_stream() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then shasum -a 256 | awk '{print $1}'
  else fatal "sha256sum or shasum is required"
  fi
}

git_identity() {
  local repo="$1" head dirty
  head="$(git -C "$repo" rev-parse HEAD)"
  if [[ -z "$(git -C "$repo" status --porcelain=v1)" ]]; then
    printf '%s clean\n' "$head"
    return
  fi
  dirty="$({
    git -C "$repo" diff --binary HEAD
    while IFS= read -r -d '' file; do
      printf 'untracked:%s\0' "$file"
      git -C "$repo" hash-object -- "$repo/$file"
    done < <(git -C "$repo" ls-files --others --exclude-standard -z)
  } | sha256_stream)"
  printf '%s dirty-%s\n' "$head" "$dirty"
}

require_cmd git
require_cmd node
require_cmd npm
[[ -f "$MANIFEST" ]] || fatal "missing required manifest $MANIFEST"
[[ -x "$VERIFY" || -f "$VERIFY" ]] || fatal "missing report verifier $VERIFY"
[[ -x "$CHECK_PROJECT" || -f "$CHECK_PROJECT" ]] || fatal "missing Compose ownership checker $CHECK_PROJECT"

actual_loom_revision="$(git -C "$ROOT_DIR" rev-parse HEAD)"
[[ "$actual_loom_revision" == "$loom_revision" ]] || fatal "Loom HEAD is $actual_loom_revision, expected $loom_revision"

tier=paired
project_name=integration
if [[ "$backend" == mocked ]]; then
  tier=mocked
  project_name=chromium-ci
  # The reported source identity must belong to the server under test. Never
  # reuse a developer's Vite server or inherit an integration-server mode.
  unset RUN_INTEGRATION_TESTS RUN_LOCAL_INTEGRATION_TESTS LOOM_LOCAL_SERVER
  unset LOOM_BASE_URL LOOM_FRONTEND_BASE_URL
  export E2E_OWNED_MOCKED_SERVER=1
  export E2E_MOCKED_PORT="${SSE_UI_MOCKED_PORT:-3080}"
fi

specs=()
case_ids=()
while IFS=$'\t' read -r entry_tier entry_backend case_id spec _marker; do
  [[ "$entry_tier" == "$tier" && "$entry_backend" == "$backend" ]] || continue
  [[ -f "$FRONTEND_DIR/$spec" ]] || fatal "required fixture unavailable: $spec"
  already_selected=0
  # `${array[@]+...}` is the Bash 3.2-safe empty-array expansion used by the
  # macOS system shell under `set -u`.
  for selected_spec in ${specs[@]+"${specs[@]}"}; do
    [[ "$selected_spec" == "$spec" ]] && already_selected=1
  done
  (( already_selected == 1 )) || specs+=("$spec")
  case_ids+=("$case_id")
done < <(grep -v '^#' "$MANIFEST" | tail -n +2)
(( ${#case_ids[@]} > 0 )) || fatal "no manifest cases for $tier/$backend"

if [[ -e "$artifacts" ]]; then fatal "artifact directory already exists: $artifacts"; fi
mkdir -p "$artifacts/test-results"

loom_identity="$(git_identity "$ROOT_DIR")"
fleet_identity="not-applicable"
if [[ "$backend" != mocked ]]; then
  require_cmd curl
  [[ -d "$fleet_repo/.git" || -f "$fleet_repo/.git" ]] || fatal "FleetDB checkout unavailable: $fleet_repo"
  actual_fleet_revision="$(git -C "$fleet_repo" rev-parse HEAD)"
  [[ "$actual_fleet_revision" == "$fleet_revision" ]] || fatal "FleetDB HEAD is $actual_fleet_revision, expected $fleet_revision"
  fleet_identity="$(git_identity "$fleet_repo")"
fi

playwright_version="$(cd "$FRONTEND_DIR" && npx playwright --version)"
node_version="$(node --version)"
npm_version="$(npm --version)"
{
  printf 'manifest_version=2\n'
  printf 'run_id=%s\n' "$run_id"
  printf 'tier=%s\n' "$tier"
  printf 'backend=%s\n' "$backend"
  printf 'loom=%s\n' "$loom_identity"
  printf 'fleet=%s\n' "$fleet_identity"
  printf 'node=%s\n' "$node_version"
  printf 'npm=%s\n' "$npm_version"
  printf 'playwright=%s\n' "$playwright_version"
  printf 'cases=%s\n' "${case_ids[*]}"
} >"$artifacts/paired-revisions.txt"

log "coordinates: depth=system, realness=$([[ "$backend" == mocked ]] && echo mocked || echo real-local), provisioning=$([[ "$backend" == mocked ]] && echo none || echo compose), polarity=positive+negative, target=$tier/$backend"
log "Loom source identity: $loom_identity"
log "Fleet source identity: $fleet_identity"
log "building the production frontend"
(cd "$FRONTEND_DIR" && npm run build) >"$artifacts/frontend-build.log" 2>&1 || {
  tail -100 "$artifacts/frontend-build.log" >&2
  fatal "production frontend build failed"
}

grep_pattern="@sse-ui-transition @($(IFS='|'; echo "${case_ids[*]}"))(\\s|$)"
playwright=(npx playwright test --project="$project_name" "${specs[@]}" --grep "$grep_pattern" --workers=1 --retries=0 --trace=retain-on-failure --output="$artifacts/test-results")

log "validating exact ${case_ids[*]} selection before execution"
(cd "$FRONTEND_DIR" && PLAYWRIGHT_JSON_OUTPUT_FILE="$artifacts/playwright-list.json" "${playwright[@]}" --list --reporter=json) \
  >"$artifacts/playwright-list.log" 2>&1 || {
    tail -100 "$artifacts/playwright-list.log" >&2
    fatal "Playwright could not list the required manifest"
  }
node "$VERIFY" --mode list --manifest "$MANIFEST" --tier "$tier" --backend "$backend" \
  --report "$artifacts/playwright-list.json" >"$artifacts/selection-summary.json"

compose=()
compose_args=()
container_engine=""
stack_started=0
proxy_container=""

collect_stack_artifacts() {
  (( stack_started == 1 )) || return 0
  "${compose[@]}" "${compose_args[@]}" ps --all >"$artifacts/compose-ps.txt" 2>&1 || true
  "${compose[@]}" "${compose_args[@]}" logs --no-color >"$artifacts/service-logs.txt" 2>&1 || true
  for service in fleet-db loom-local ui-local redis postgres; do
    container_id="$("${compose[@]}" "${compose_args[@]}" ps -q "$service" 2>/dev/null || true)"
    [[ -n "$container_id" ]] || continue
    "$container_engine" inspect --format '{{json .State}}' "$container_id" >"$artifacts/${service}-state.json" 2>/dev/null || true
    "$container_engine" inspect --format '{{.Image}}' "$container_id" >"$artifacts/${service}-image-id.txt" 2>/dev/null || true
  done
}

cleanup() {
  local status=$?
  trap - EXIT INT TERM
  if (( stack_started == 1 )); then
    collect_stack_artifacts
    if [[ -n "$proxy_container" ]]; then
      owner="$($container_engine inspect --format '{{ index .Config.Labels "com.docker.compose.project" }}' "$proxy_container" 2>/dev/null || true)"
      if [[ "$owner" == "$compose_project" ]]; then
        running="$($container_engine inspect --format '{{.State.Running}}' "$proxy_container" 2>/dev/null || true)"
        if [[ "$running" != true ]]; then
          log "restoring owned UI proxy before teardown"
          "$container_engine" start "$proxy_container" >/dev/null 2>&1 || status=1
        fi
      else
        printf '[sse-ui:%s] refusing proxy restore: ownership label is %q, expected %q\n' "$backend" "$owner" "$compose_project" >&2
        status=1
      fi
    fi
    log "removing only Compose project $compose_project"
    "${compose[@]}" "${compose_args[@]}" down -v --remove-orphans >"$artifacts/compose-down.log" 2>&1 || status=1
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

if [[ "$backend" != mocked ]]; then
  case "${LOCAL_MODE_COMPOSE:-}" in
    "")
      if command -v podman >/dev/null 2>&1 && podman compose version >/dev/null 2>&1; then compose=(podman compose); container_engine=podman
      elif command -v podman-compose >/dev/null 2>&1; then compose=(podman-compose); container_engine=podman
      elif command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then compose=(docker compose); container_engine=docker
      else fatal "podman compose or docker compose is required"
      fi
      ;;
    "podman compose") compose=(podman compose); container_engine=podman ;;
    "podman-compose") compose=(podman-compose); container_engine=podman ;;
    "docker compose") compose=(docker compose); container_engine=docker ;;
    *) fatal "LOCAL_MODE_COMPOSE must be podman compose, podman-compose or docker compose" ;;
  esac
  "$container_engine" info >/dev/null 2>&1 || fatal "$container_engine is not reachable"

  compose_project="loomcli-sse-ui-${backend}-${run_id}"
  fleet_port="${SSE_UI_FLEETDB_PORT:-$([[ "$backend" == redis ]] && echo 8880 || echo 8980)}"
  api_port="${SSE_UI_API_PORT:-$([[ "$backend" == redis ]] && echo 8882 || echo 8982)}"
  ui_port="${SSE_UI_UI_PORT:-$([[ "$backend" == redis ]] && echo 8883 || echo 8983)}"
  for port in "$fleet_port" "$api_port" "$ui_port"; do
    if [[ ! "$port" =~ ^[0-9]+$ ]] || (( port <= 0 || port >= 65536 )); then fatal "invalid port $port"; fi
    case "$port" in 8580|8581|8582|8583|8780|8781|8782|8783) fatal "port $port is reserved for an existing stack" ;; esac
    if command -v lsof >/dev/null 2>&1 && lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then fatal "port $port is already in use"; fi
  done
  [[ "$fleet_port" != "$api_port" && "$fleet_port" != "$ui_port" && "$api_port" != "$ui_port" ]] || fatal "stack ports must be distinct"
  # The JavaScript is intentionally single-quoted so the shell cannot expand
  # its template literal while Node probes all candidate ports together.
  # shellcheck disable=SC2016
  node -e '
    const net = require("node:net");
    const ports = process.argv.slice(1).map(Number);
    const servers = [];
    let pending = ports.length;
    const close = (code) => Promise.all(servers.map((server) => new Promise((resolve) => server.close(resolve)))).then(() => process.exit(code));
    for (const port of ports) {
      const server = net.createServer();
      servers.push(server);
      server.once("error", (error) => { console.error(`port ${port} unavailable: ${error.message}`); close(1); });
      server.listen(port, "127.0.0.1", () => { if (--pending === 0) close(0); });
    }
  ' "$fleet_port" "$api_port" "$ui_port" || fatal "one or more stack ports cannot be bound"

  compose_args=(-p "$compose_project" -f "$ROOT_DIR/test/local-mode/docker-compose.yml")
  if [[ "$backend" == postgres ]]; then compose_args+=(-f "$ROOT_DIR/test/local-mode/docker-compose.postgres.yml"); fi
  "$CHECK_PROJECT" "$container_engine" "$compose_project" || fatal "could not prove Compose project is unused"
  printf '%s\n' "$compose_project" >"$artifacts/stack-owned"

  export LOCAL_MODE_COMPOSE_PROJECT="$compose_project"
  export LOCAL_MODE_FLEETDB_PORT="$fleet_port"
  export LOCAL_MODE_API_PORT="$api_port"
  export LOCAL_MODE_UI_PORT="$ui_port"
  export LOCAL_MODE_FLEETDB_BUILD_CONTEXT="$fleet_repo"
  export LOCAL_MODE_FLEETDB_BUILD_REVISION="$fleet_revision"
  export LOCAL_MODE_FLEETDB_IMAGE="${compose_project}-fleet-db:latest"
  export LOCAL_MODE_LOOM_IMAGE="${compose_project}-loom:latest"

  export LOCAL_MODE_STEP_DELAY=8
  log "building and starting owned Compose project $compose_project on $fleet_port/$api_port/$ui_port"
  # Mark ownership before `up`: a partial create/build failure still needs the
  # exact-project down path in the EXIT trap.
  stack_started=1
  "${compose[@]}" "${compose_args[@]}" up --build -d >"$artifacts/compose-up.log" 2>&1 || {
    tail -120 "$artifacts/compose-up.log" >&2
    fatal "Compose startup failed"
  }
  ready=0
  for _ in $(seq 1 120); do
    if curl -fsS --max-time 2 "http://127.0.0.1:${api_port}/health" >/dev/null 2>&1 && \
       curl -fsS --max-time 2 "http://127.0.0.1:${ui_port}/" >/dev/null 2>&1; then ready=1; break; fi
    sleep 1
  done
  (( ready == 1 )) || fatal "stack did not become healthy"

  fleet_container="$("${compose[@]}" "${compose_args[@]}" ps -q fleet-db)"
  loom_container="$("${compose[@]}" "${compose_args[@]}" ps -q loom-local)"
  proxy_container="$("${compose[@]}" "${compose_args[@]}" ps -q ui-local)"
  [[ -n "$fleet_container" && -n "$loom_container" && -n "$proxy_container" ]] || fatal "required Compose services are unavailable"
  for container in "$fleet_container" "$loom_container" "$proxy_container"; do
    owner="$($container_engine inspect --format '{{ index .Config.Labels "com.docker.compose.project" }}' "$container")"
    [[ "$owner" == "$compose_project" ]] || fatal "container $container is not owned by $compose_project"
  done

  "$container_engine" inspect --format '{{json .Args}}' "$fleet_container" >"$artifacts/fleet-command.json" \
    || fatal "could not inspect FleetDB runtime command"
  # shellcheck disable=SC2016
  node -e '
    const args = require(process.argv[1]);
    const expected = process.argv[2];
    let actual = "redis";
    const index = args.findIndex((arg) => arg === "--backend" || arg.startsWith("--backend="));
    if (index >= 0) actual = args[index].includes("=") ? args[index].split("=", 2)[1] : args[index + 1];
    if (actual !== expected) {
      console.error(`Fleet command selects backend=${actual}; expected ${expected}`);
      process.exit(1);
    }
  ' "$artifacts/fleet-command.json" "$backend" || fatal "FleetDB command backend does not match the requested cell"

  fleet_logs="$artifacts/fleet-startup.log"
  "$container_engine" logs "$fleet_container" >"$fleet_logs" 2>&1
  grep -F "starting fleet-db" "$fleet_logs" >/dev/null || fatal "FleetDB startup identity log is missing"
  grep -F "$fleet_revision" "$fleet_logs" >/dev/null || fatal "FleetDB runtime commit does not match $fleet_revision"
  grep -E "backend[= :\"]+${backend}" "$fleet_logs" >/dev/null || fatal "FleetDB runtime did not attest backend=$backend"

  "$container_engine" exec "$loom_container" sh -c 'test -d /workspace/source-repo/.git' || fatal "source repository fixture is unavailable"
  preflight_name="sse-preflight-${run_id}"
  create_body="$(node -e 'process.stdout.write(JSON.stringify({name:process.argv[1],type:"empty",repos:["/workspace/source-repo"]}))' "$preflight_name")"
  create_status="$(curl -sS --max-time 60 -o "$artifacts/preflight-workspace-create.json" -w '%{http_code}' \
    -H 'Content-Type: application/json' --data "$create_body" "http://127.0.0.1:${api_port}/api/workspaces")"
  [[ "$create_status" == 201 ]] || fatal "preflight workspace creation returned HTTP $create_status"
  preflight_id="$(node -e 'const b=require(process.argv[1]); if(!b.success||!b.data?.id) process.exit(1); process.stdout.write(b.data.id)' "$artifacts/preflight-workspace-create.json")" \
    || fatal "preflight workspace response lacks a successful exact ID"
  curl -fsS --max-time 15 "http://127.0.0.1:${api_port}/api/workspaces/${preflight_id}" >"$artifacts/preflight-workspace.json"
  node -e 'const b=require(process.argv[1]); if(!b.success || !Array.isArray(b.data?.agents) || b.data.agents.length!==0 || b.data.repos?.length!==1) process.exit(1)' \
    "$artifacts/preflight-workspace.json" || fatal "preflight workspace is not empty or has autonomous agents"

  {
    printf 'compose_project=%s\n' "$compose_project"
    printf 'compose=%s\n' "${compose[*]}"
    printf 'container_engine=%s\n' "$container_engine"
    printf 'ports=%s/%s/%s\n' "$fleet_port" "$api_port" "$ui_port"
    printf 'fleet_container=%s\n' "$fleet_container"
    printf 'loom_container=%s\n' "$loom_container"
    printf 'proxy_container=%s\n' "$proxy_container"
    printf 'preflight_workspace=%s\n' "$preflight_id"
  } >>"$artifacts/paired-revisions.txt"

  # T12 deliberately exercises the product's deterministic planner/coder in
  # LOCALMODE; the other scenarios keep their isolated no-agent workspaces.
  runtime_backend="$("$container_engine" exec "$loom_container" printenv LOOM_BACKEND)"
  [[ "$runtime_backend" == localdogfood ]] || fatal "workflow proof requires the deterministic localdogfood backend"
  runtime_delay="$("$container_engine" exec "$loom_container" printenv LOOM_LOCAL_MODE_STEP_DELAY)"
  [[ "$runtime_delay" == 8 ]] || fatal "expected deterministic work delay 8, got $runtime_delay"
  printf 'agent_backend=%s\nagent_step_delay=%s\n' "$runtime_backend" "$runtime_delay" >>"$artifacts/paired-revisions.txt"
  export RUN_SSE_WORKFLOW_TRANSITION_TESTS=1
  export LOOM_SSE_TEST_LOOM_CONTAINER="$loom_container"
  export RUN_INTEGRATION_TESTS=1
  export LOOM_LOCAL_SERVER=1
  export LOOM_API_KEY=local-mode-test-unused
  export LOOM_SSE_TEST_SOURCE_REPO=/workspace/source-repo
  export LOOM_SSE_TEST_CONTAINER_ENGINE="$container_engine"
  export LOOM_SSE_TEST_PROXY_CONTAINER="$proxy_container"
  export LOOM_BASE_URL="http://127.0.0.1:${api_port}"
  export LOOM_FRONTEND_BASE_URL="http://127.0.0.1:${ui_port}"
fi

log "executing exact manifest once with retries=0"
set +e
(cd "$FRONTEND_DIR" && \
  PLAYWRIGHT_JSON_OUTPUT_FILE="$artifacts/playwright-report.json" \
  "${playwright[@]}" --reporter=line,json) 2>&1 | tee "$artifacts/playwright.log"
playwright_status=${PIPESTATUS[0]}
set -e

verify_status=0
node "$VERIFY" --mode result --manifest "$MANIFEST" --tier "$tier" --backend "$backend" \
  --report "$artifacts/playwright-report.json" --summary "$artifacts/result-summary.json" || verify_status=$?

final_loom_identity="$(git_identity "$ROOT_DIR")"
final_fleet_identity="not-applicable"
if [[ "$backend" != mocked ]]; then
  final_fleet_identity="$(git_identity "$fleet_repo")"
fi
{
  printf 'loom_after=%s\n' "$final_loom_identity"
  printf 'fleet_after=%s\n' "$final_fleet_identity"
} >>"$artifacts/paired-revisions.txt"

source_status=0
if [[ "$final_loom_identity" != "$loom_identity" ]]; then
  printf '[sse-ui:%s] source changed during run: Loom was %q, now %q\n' \
    "$backend" "$loom_identity" "$final_loom_identity" >&2
  source_status=1
fi
if [[ "$final_fleet_identity" != "$fleet_identity" ]]; then
  printf '[sse-ui:%s] source changed during run: FleetDB was %q, now %q\n' \
    "$backend" "$fleet_identity" "$final_fleet_identity" >&2
  source_status=1
fi

if (( playwright_status != 0 || verify_status != 0 || source_status != 0 )); then
  fatal "required transition manifest failed (playwright=$playwright_status verifier=$verify_status source=$source_status)"
fi

log "PASS: all ${#case_ids[@]} required cases executed once; zero skipped, failed or flaky"
