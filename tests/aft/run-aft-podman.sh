#!/usr/bin/env bash
# Run the real-codex AFT tier against the containerized ModeCloud podman stack.
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
STACK_DIR="$REPO_ROOT/deploy/podman-stack"
FRONTEND_DIR="$REPO_ROOT/internal/webui/frontend"
REPORT_DIR="$SCRIPT_DIR/reports"
BUILD_SCRIPT="$STACK_DIR/build.sh"

: "${AFT_DIR:=$REPO_ROOT/../testing-app}"
: "${AFT_TIMEOUT:=600000}"

log() { printf '[aft-podman] %s\n' "$*"; }
die() { printf '[aft-podman] error: %s\n' "$*" >&2; exit 1; }
require_cmd() { command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"; }
require_file() { [[ -f "$1" ]] || die "missing required file: $1"; }

free_host_port() {
    python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()'
}

next_distinct_port() {
    local port
    while :; do
        port="$(free_host_port)"
        [[ " $* " != *" $port "* ]] && { printf '%s\n' "$port"; return; }
    done
}

frontend_is_stale() {
    local index="$FRONTEND_DIR/dist/index.html"
    [[ -f "$index" ]] || return 0
    [[ "$FRONTEND_DIR/index.html" -nt "$index" ]] && return 0
    [[ "$FRONTEND_DIR/package.json" -nt "$index" ]] && return 0
    [[ "$FRONTEND_DIR/package-lock.json" -nt "$index" ]] && return 0
    [[ "$FRONTEND_DIR/vite.config.ts" -nt "$index" ]] && return 0
    [[ -n "$(find "$FRONTEND_DIR/src" -type f -newer "$index" -print -quit)" ]] && return 0
    [[ -n "$(find "$FRONTEND_DIR/public" -type f -newer "$index" -print -quit)" ]] && return 0
    return 1
}

wait_http() {
    local url="$1" name="$2" budget="${3:-150}"
    local deadline=$((SECONDS + budget))
    while ((SECONDS < deadline)); do
        curl -fsS -m 5 "$url" >/dev/null 2>&1 && return 0
        sleep 1
    done
    die "timed out (${budget}s) waiting for ${name} at ${url}"
}

required_images_present() {
    local image
    for image in fleet-db loom-serve loom-worker stub-upstream; do
        podman image exists "localhost/loom-stack/${image}:latest" || return 1
    done
}

dump_logs() {
    [[ -n "${COMPOSE+x}" ]] || return 0
    "${COMPOSE[@]}" ps >&2 || true
    local service container
    for service in fleet-db loom-serve worker; do
        container="$("${COMPOSE[@]}" ps -q "$service" 2>/dev/null | head -n1)"
        [[ -n "$container" ]] || continue
        printf '%s\n' "--- ${service} (${container}) last 5000 lines ---" >&2
        podman logs --tail 5000 "$container" >&2 || true
    done
}

teardown() {
    local status=$?
    trap - EXIT INT TERM
    if [[ "$status" -ne 0 ]]; then
        log "run failed (exit $status); collecting container logs"
        dump_logs
    fi
    if [[ "${STACK_STARTED:-0}" == "1" ]]; then
        log "tearing down stack and named volumes"
        "${COMPOSE[@]}" down --volumes --timeout 10 >/dev/null 2>&1 ||
            "${COMPOSE[@]}" down --volumes >/dev/null 2>&1 || true
    fi
    if [[ -n "${TMP_ROOT:-}" && -d "$TMP_ROOT" ]]; then
        rm -rf "$TMP_ROOT"
    fi
    exit "$status"
}

require_cmd podman
require_cmd curl
require_cmd openssl
require_cmd python3
require_cmd node
require_cmd npm
require_cmd git
require_file "$STACK_DIR/compose.yaml"
require_file "$STACK_DIR/compose.aft.yaml"
require_file "$BUILD_SCRIPT"

podman info >/dev/null 2>&1 || die "podman is not reachable (start it with: podman machine start)"

CODEX_BIN="$(command -v codex 2>/dev/null || true)"
[[ -n "$CODEX_BIN" ]] || die "real codex tier needs codex on PATH"
case "$CODEX_BIN" in
    "$REPO_ROOT"/e2e/stubs*) die "codex resolved to a test stub ($CODEX_BIN), not the real CLI" ;;
esac

CODEX_HOME_HOST="${CODEX_HOME:-$HOME/.codex}"
require_file "$CODEX_HOME_HOST/auth.json"

if frontend_is_stale; then
    log "building stale or missing frontend dist"
    (
        cd "$FRONTEND_DIR"
        [[ -d node_modules ]] || npm ci --prefer-offline
        npm run build
    )
else
    log "using current frontend dist"
fi

# dist/cli.js alone is not enough: it imports from node_modules at runtime (zod
# etc.), and cleanup tools sweep node_modules while keeping dist.
if [[ ! -f "$AFT_DIR/dist/cli.js" || ! -d "$AFT_DIR/node_modules" ]]; then
    log "installing/building aft in $AFT_DIR"
    (cd "$AFT_DIR" && npm install --silent && npm run build --silent)
fi

# The browser gets swept too: self-heal agent-browser's Chrome before any suite
# needs it (macOS only; no-op elsewhere).
bash "$SCRIPT_DIR/scripts/ensure-agent-browser.sh"

mkdir -p "$REPORT_DIR"
umask 077
TMP_ROOT="$(mktemp -d -t loom-aft-podman.XXXXXX)"
ENV_FILE="$TMP_ROOT/podman.env"
STACK_STARTED=0
trap teardown EXIT INT TERM

RUN_ID="${RUN_ID:-$(date +%s)}"
unset LOOM_DRIVER_TASK_RUNNER_CMD_JSON LOOM_STACK_TASK_RUNNER_CMD_JSON
unset LOOM_STACK_DRIVER_SANDBOX LOOM_STACK_WORKER_BACKEND
LOOM_STACK_PROJECT="loom-aft-podman-$$"
LOOM_STACK_SERVE_PORT="$(free_host_port)"
LOOM_STACK_FLEET_DB_PORT="$(next_distinct_port "$LOOM_STACK_SERVE_PORT")"
LOOM_STACK_STUB_PORT="$(next_distinct_port "$LOOM_STACK_SERVE_PORT" "$LOOM_STACK_FLEET_DB_PORT")"
LOOM_STACK_WORKSPACE="E2E-WS"
LOOM_STACK_CODEX_HOST_DIR="$CODEX_HOME_HOST"
LOOM_AFT_FRONTEND_DIST="$FRONTEND_DIR/dist"
LOOM_FLEET_DB_API_KEY="fldb_$(openssl rand -hex 24)"
LOOM_RUN_TOKEN_SIGNING_KEY="$(openssl rand -hex 32)"
LOOM_CONNECTOR_VAULT_KEY="$(openssl rand -base64 32)"
LOOM_FLEET_API_KEY="$(openssl rand -hex 24)"
LOOM_WORKER_TOKEN="$(openssl rand -hex 24)"
LOOM_STACK_STUB_SECRET="$(openssl rand -hex 16)"
FLEET_SEED_ACTOR="loom-serve@podman-stack.local"
export LOOM_STACK_PROJECT LOOM_STACK_SERVE_PORT LOOM_STACK_FLEET_DB_PORT
export LOOM_STACK_STUB_PORT LOOM_STACK_WORKSPACE LOOM_STACK_CODEX_HOST_DIR
export LOOM_AFT_FRONTEND_DIST LOOM_FLEET_DB_API_KEY LOOM_RUN_TOKEN_SIGNING_KEY
export LOOM_CONNECTOR_VAULT_KEY LOOM_FLEET_API_KEY LOOM_WORKER_TOKEN
export LOOM_STACK_STUB_SECRET FLEET_SEED_ACTOR

log "generating per-run secrets in $ENV_FILE (values not echoed)"
{
    printf 'LOOM_STACK_PROJECT=%s\n' "$LOOM_STACK_PROJECT"
    printf 'LOOM_STACK_SERVE_PORT=%s\n' "$LOOM_STACK_SERVE_PORT"
    printf 'LOOM_STACK_FLEET_DB_PORT=%s\n' "$LOOM_STACK_FLEET_DB_PORT"
    printf 'LOOM_STACK_STUB_PORT=%s\n' "$LOOM_STACK_STUB_PORT"
    printf 'LOOM_STACK_WORKSPACE=%s\n' "$LOOM_STACK_WORKSPACE"
    printf 'FLEET_SEED_ACTOR=%s\n' "$FLEET_SEED_ACTOR"
    printf 'FLEET_SEED_ROLE=admin\n'
    printf 'FLEET_LOG_LEVEL=info\n'
    printf 'LOOM_FLEET_DB_API_KEY=%s\n' "$LOOM_FLEET_DB_API_KEY"
    printf 'LOOM_RUN_TOKEN_SIGNING_KEY=%s\n' "$LOOM_RUN_TOKEN_SIGNING_KEY"
    printf 'LOOM_CONNECTOR_VAULT_KEY=%s\n' "$LOOM_CONNECTOR_VAULT_KEY"
    printf 'LOOM_FLEET_API_KEY=%s\n' "$LOOM_FLEET_API_KEY"
    printf 'LOOM_WORKER_TOKEN=%s\n' "$LOOM_WORKER_TOKEN"
    printf 'LOOM_STACK_STUB_SECRET=%s\n' "$LOOM_STACK_STUB_SECRET"
    printf 'LOOM_STACK_CODEX_HOST_DIR=%s\n' "$LOOM_STACK_CODEX_HOST_DIR"
    printf 'LOOM_AFT_FRONTEND_DIST=%s\n' "$LOOM_AFT_FRONTEND_DIST"
} >"$ENV_FILE"
chmod 600 "$ENV_FILE"

COMPOSE=(podman compose -f "$STACK_DIR/compose.yaml" -f "$STACK_DIR/compose.aft.yaml" --env-file "$ENV_FILE")

if [[ "${LOOM_STACK_SKIP_BUILD:-0}" == "1" ]]; then
    log "LOOM_STACK_SKIP_BUILD=1: reusing existing images"
elif [[ "${AFT_PODMAN_REBUILD:-0}" == "1" ]] || ! required_images_present; then
    log "building missing or explicitly requested podman images"
    # Prefer a sibling fleet-db-main (git worktree of origin/main): the default
    # ../fleet-db can be behind and miss the driver-runs domain + artifact-content
    # route that the epic-runner and the forensics reads require.
    FLEET_DB_REPO="${FLEET_DB_REPO:-$(cd "$STACK_DIR/../../../fleet-db-main" 2>/dev/null && pwd || true)}" \
        bash "$BUILD_SCRIPT"
else
    log "all four stack images already exist"
fi

log "removing any stale stack state for project $LOOM_STACK_PROJECT"
"${COMPOSE[@]}" down --volumes >/dev/null 2>&1 || true
log "starting podman ModeCloud stack (project $LOOM_STACK_PROJECT)"
STACK_STARTED=1
"${COMPOSE[@]}" up -d

SERVE_URL="http://localhost:${LOOM_STACK_SERVE_PORT}"
FLEET_URL="http://127.0.0.1:${LOOM_STACK_FLEET_DB_PORT}"
wait_http "$FLEET_URL/readyz" "fleet-db" 120
wait_http "$SERVE_URL/api/health" "loom serve" 180

AFT_SERVE_CONTAINER="$("${COMPOSE[@]}" ps -q loom-serve | head -n1)"
[[ -n "$AFT_SERVE_CONTAINER" ]] || die "could not resolve the loom-serve container"
SERVE_LOGS="$(podman logs "$AFT_SERVE_CONTAINER" 2>&1 || true)"
grep -q 'opened cloud fleet-db client' <<<"$SERVE_LOGS" ||
    die "loom-serve logs do not prove ModeCloud (missing 'opened cloud fleet-db client')"
if grep -q 'embedded fleet-db started' <<<"$SERVE_LOGS"; then
    die "loom-serve silently fell back to embedded fleet-db"
fi

# Provision through loom serve, matching the host AFT tier. The fleet-db admin
# workspace endpoint accepts a repos string list as metadata, but it does not
# create the store Repo rows or local worktree mapping required by the task-run
# resolver. Build a real source repo in the named /work volume, then let the
# product workspace API attach it as a worktree and register the complete
# workspace topology in both fleet-db and serve's local state cache.
podman exec "$AFT_SERVE_CONTAINER" sh -c '
    set -eu
    repo=/work/source-repos/aft-repo
    workspace=/work/workspaces/E2E-WS
    mkdir -p "$repo"
    git -C "$repo" init -q
    git -C "$repo" config user.email aft@example.test
    git -C "$repo" config user.name aft
    git -C "$repo" commit --allow-empty -m seed -q
    rmdir "$workspace/worktrees"
'
seed_body='{"name":"e2e-ws","type":"empty","path":"/work/workspaces/E2E-WS","repos":["/work/source-repos/aft-repo"]}'
seed_code="$(curl -sS -o "$TMP_ROOT/seed-resp.json" -w '%{http_code}' --max-time 30 \
    -X POST -H 'Content-Type: application/json' --data "$seed_body" \
    "$SERVE_URL/api/workspaces" 2>&1 || true)"
log "workspace provision POST -> HTTP ${seed_code}: $(head -c 400 "$TMP_ROOT/seed-resp.json" 2>/dev/null)"
[[ "$seed_code" == "201" ]] || die "failed to provision E2E-WS through loom serve (HTTP ${seed_code})"
repos_json="$(curl -fsS --max-time 20 "$SERVE_URL/api/workspaces/E2E-WS/repos")"
grep -q '"name":"aft-repo"' <<<"$repos_json" ||
    die "E2E-WS has no registered aft-repo after provisioning: $repos_json"

PUBLISHED_SERVE="$("${COMPOSE[@]}" port loom-serve 8080 | head -n1)"
if [[ "$PUBLISHED_SERVE" =~ :([0-9]+)$ ]]; then
    LOOM_STACK_SERVE_PORT="${BASH_REMATCH[1]}"
fi

export AFT_BASE_URL="http://localhost:${LOOM_STACK_SERVE_PORT}"
export AFT_API_URL="$AFT_BASE_URL"
export AFT_WS="E2E-WS"
export LOOM_BASE_URL="$AFT_API_URL"
export RUN_ID
export AFT_TESTS_DIR="$SCRIPT_DIR"
export AFT_WORK_DIR="$REPORT_DIR/work/$RUN_ID"
export AFT_SERVE_CONTAINER
mkdir -p "$AFT_WORK_DIR"

log "stack ready: $AFT_BASE_URL (serve container $AFT_SERVE_CONTAINER)"
if command -v caffeinate >/dev/null 2>&1; then
    caffeinate -dimsu node "$AFT_DIR/dist/cli.js" run "$SCRIPT_DIR/real-suites-podman" \
        --report-dir "$REPORT_DIR" --viewport "${AFT_VIEWPORT:-1920x1080}" \
        --timeout "$AFT_TIMEOUT" "$@"
else
    node "$AFT_DIR/dist/cli.js" run "$SCRIPT_DIR/real-suites-podman" \
        --report-dir "$REPORT_DIR" --viewport "${AFT_VIEWPORT:-1920x1080}" \
        --timeout "$AFT_TIMEOUT" "$@"
fi
