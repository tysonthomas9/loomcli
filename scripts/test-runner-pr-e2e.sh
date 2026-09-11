#!/usr/bin/env bash
set -Eeuo pipefail

# Full epic-runner driver-executor e2e with REAL opt-in PR delivery, per backend.
# Drives the productionized builtin local-task-runner end-to-end: redis + fleet-db
# + `loom serve --driver-executor` from a real clone of a GitHub repo, registers
# the builtin epic-runner bundle, forces the backend, runs 2 child tasks with
# openPullRequest=true, and asserts 2 real draft PRs (distinct loom/<taskId>
# branches + distinct markers).
#
#   usage: scripts/test-runner-pr-e2e.sh <codex|claude|cursor|opencode|gemini>
#   env:
#     GITHUB_TOKEN          PAT with push to the sandbox repo (or ~/.loom-secrets/github_token)
#     PR_E2E_REPO_SLUG      target repo (default: aether-loom/webhook-e2e-sandbox)
#     LOOM_OPENCODE_MODEL   optional: pin opencode's model (e.g. openai/gpt-5.4-fast)
#     KEEP=1                keep the temp workspace + logs
#
# Backend auth uses the host's local tooling (codex/cursor-agent/opencode/claude
# OAuth or API keys), inherited via the trusted-local env widening. Uses `driver
# run` (not `epic run`), so the preflight CURSOR_API_KEY/ANTHROPIC_API_KEY
# false-negatives do not block OAuth-only backends.

BACKEND="${1:?usage: test-runner-pr-e2e.sh <codex|claude|cursor|opencode|gemini>}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
. "$ROOT/scripts/lib/sandbox.sh"
FLEET_DB_REPO="${FLEET_DB_REPO:-$(cd "$ROOT/../fleet-db" 2>/dev/null && pwd || true)}"
REPO_SLUG="${PR_E2E_REPO_SLUG:-aether-loom/webhook-e2e-sandbox}"
TOKEN="${GITHUB_TOKEN:-$(cat "$HOME/.loom-secrets/github_token" 2>/dev/null || true)}"

require_cmd() { command -v "$1" >/dev/null 2>&1 || { echo "ERROR: missing $1" >&2; exit 1; }; }
for c in go git jq curl node redis-server; do require_cmd "$c"; done
[ -n "$FLEET_DB_REPO" ] && [ -d "$FLEET_DB_REPO/cmd/fleet-db" ] || { echo "ERROR: fleet-db repo not found; set FLEET_DB_REPO" >&2; exit 1; }
[ -n "$TOKEN" ] || { echo "ERROR: no GitHub token (set GITHUB_TOKEN or ~/.loom-secrets/github_token)" >&2; exit 1; }
[ -d "$ROOT/internal/workflows/builtin-dist/epic-runner/dist" ] || { echo "ERROR: builtin bundle missing; run scripts/rebuild-builtin-bundle.sh" >&2; exit 1; }

UP="$(printf '%s' "$BACKEND" | tr '[:lower:]' '[:upper:]')"
WS="PRE2E${UP}"; RUN_ID="run-pr-e2e-$BACKEND"; NODE_ID="pr-e2e-node-$BACKEND"; ACTOR="pr-e2e"
CLONE_URL="https://x-access-token:${TOKEN}@github.com/${REPO_SLUG}.git"
freeport() { node -e 's=require("net").createServer();s.listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close()})'; }
REDIS_PORT="$(freeport)"; FLEET_PORT="$(freeport)"; LOOM_PORT="$(freeport)"
FLEET_URL="http://127.0.0.1:$FLEET_PORT"; LOOM_URL="http://127.0.0.1:$LOOM_PORT"

loom_mktemp_dir test-runner-pr-e2e; TMP_ROOT="$LOOM_SANDBOX_DIR"
BIN_DIR="$TMP_ROOT/bin"; REPO="$TMP_ROOT/repo"; STAGE="$TMP_ROOT/dist"; CONFIG="$TMP_ROOT/loom-config"
mkdir -p "$BIN_DIR" "$CONFIG"
PIDS=()
say() { printf '\n==> %s\n' "$*"; }
jqr() { jq -r "$1" 2>/dev/null; }
cleanup() {
  for p in "${PIDS[@]:-}"; do kill "$p" 2>/dev/null || true; done
  [ "${KEEP:-0}" = "1" ] && echo "kept $TMP_ROOT"
}
trap cleanup EXIT INT TERM

say "PR e2e backend=$BACKEND ws=$WS repo=$REPO_SLUG ports redis=$REDIS_PORT fleet=$FLEET_PORT loom=$LOOM_PORT"

say "building loom + fleet-db"
( cd "$FLEET_DB_REPO"; GOCACHE="${GOCACHE:-/tmp/go-build-cache}" go build -o "$BIN_DIR/fleet-db" ./cmd/fleet-db )
( cd "$ROOT";          GOCACHE="${GOCACHE:-/tmp/go-build-cache}" go build -o "$BIN_DIR/loom" ./cmd/loom )

say "cloning $REPO_SLUG (host worktree)"
git clone --quiet "$CLONE_URL" "$REPO" || { echo "clone failed"; exit 1; }
BASE="$(git -C "$REPO" rev-parse --abbrev-ref HEAD)"
echo "default branch=$BASE head=$(git -C "$REPO" rev-parse --short HEAD)"

cp -R "$ROOT/internal/workflows/builtin-dist/epic-runner/dist" "$STAGE"
printf '%s\n' '{"runners":"[{\"name\":\"local-task-runner\",\"kind\":\"flue-workflow\",\"entrypoint\":\"local-task-runner\"}]"}' > "$STAGE/loom-driver.json"

say "starting redis + fleet-db"
redis-server --port "$REDIS_PORT" --save "" --appendonly no >"$TMP_ROOT/redis.log" 2>&1 & PIDS+=($!)
sleep 0.4
"$BIN_DIR/fleet-db" -addr "127.0.0.1:$FLEET_PORT" -backend redis -redis-addr "127.0.0.1:$REDIS_PORT" \
  -redis-durability-profile managed -redis-max-retries 0 -redis-cb-fail-threshold 0 \
  -auth-dev-mode -authz-enabled=false -rpc-enabled=false -log-format text >"$TMP_ROOT/fleet.log" 2>&1 & PIDS+=($!)
for _ in $(seq 1 80); do curl -fsS "$FLEET_URL/healthz" >/dev/null 2>&1 && break; sleep 0.25; done
curl -fsS "$FLEET_URL/healthz" >/dev/null 2>&1 || { echo "fleet-db down"; tail -20 "$TMP_ROOT/fleet.log"; exit 1; }

say "seeding workspace + forcing backend=$BACKEND + node"
curl -fsS -X POST -H 'Content-Type: application/json' -H "X-Actor: $ACTOR" \
  --data "{\"key\":\"$WS\",\"name\":\"PR E2E\",\"repos\":[\"$REPO_SLUG\"],\"state\":\"ready\"}" "$FLEET_URL/api/v1/admin/workspaces" >/dev/null
curl -fsS -X PUT -H 'Content-Type: application/json' -H "X-Actor: $ACTOR" \
  --data "{\"agent_backend\":\"$BACKEND\"}" "$FLEET_URL/api/v1/$WS/daemon" >"$TMP_ROOT/daemon.json"
GET_BACKEND="$(jqr '.agent_backend' < "$TMP_ROOT/daemon.json")"
[ "$GET_BACKEND" = "$BACKEND" ] || echo "!!! WARNING: daemon agent_backend=$GET_BACKEND (wanted $BACKEND)"
curl -fsS -X POST -H 'Content-Type: application/json' -H "X-Actor: $ACTOR" \
  --data "{\"node_id\":\"$NODE_ID\",\"owner_actor\":\"$ACTOR\",\"runtime_provider\":\"local\",\"drain_state\":\"active\",\"capacity\":8,\"ttl_seconds\":600}" \
  "$FLEET_URL/api/v1/$WS/nodes" >/dev/null

export LOOM_CONFIG_DIR="$CONFIG" LOOM_WORKSPACE="$WS" LOOM_FLEET_DB_URL="$FLEET_URL" LOOM_FLEET_DB_ACTOR="$ACTOR"

EPIC=$("$BIN_DIR/loom" --workspace "$WS" data create -o json --title "Epic PR $BACKEND" --type epic --priority 1 | jqr '.id')
mk_task() { "$BIN_DIR/loom" --workspace "$WS" data create -o json --title "$1" --type task --parent "$EPIC" --priority 1 --source-repo "$REPO_SLUG" --description "$2" | jqr '.id'; }
TASK_A=$(mk_task "PR task A ($BACKEND)" "Create a file named pr-marker-$BACKEND-a.md in the repo root with exactly one line: PR task A by $BACKEND via local-task-runner. Make ONLY that change.")
TASK_B=$(mk_task "PR task B ($BACKEND)" "Create a file named pr-marker-$BACKEND-b.md in the repo root with exactly one line: PR task B by $BACKEND via local-task-runner. Make ONLY that change.")
say "epic=$EPIC taskA=$TASK_A taskB=$TASK_B"
[ -n "$EPIC" ] && [ -n "$TASK_A" ] && [ -n "$TASK_B" ] || { echo "issue create failed"; exit 1; }

( cd "$REPO"; "$BIN_DIR/loom" --workspace "$WS" driver register --flue-dist "$STAGE" \
    --name epic-runner --id epic-runner --workflow epic-runner --source-ref "builtin://epic-runner" --trusted --activate --json ) \
  >"$TMP_ROOT/register.json" 2>&1 || { echo "register failed"; tail -20 "$TMP_ROOT/register.json"; exit 1; }

# GITHUB_TOKEN reaches the runner via the serve env + the trusted-local widening
# (localTaskRunnerBaseEnv re-admits it) — it must NOT go in argv, where `ps` could
# read it. Only LOOM_OPENCODE_MODEL (not a secret, not in the driver allowlist)
# needs the CMD_JSON env-prefix.
RUNNER_ARGS=()
[ -n "${LOOM_OPENCODE_MODEL:-}" ] && RUNNER_ARGS=(env "LOOM_OPENCODE_MODEL=$LOOM_OPENCODE_MODEL")
RUNNER_ARGS+=("$(command -v node)" "$ROOT/scripts/loom-task-runner-invoker.mjs")
CMD_JSON="$(node -e 'console.log(JSON.stringify(process.argv.slice(1)))' "${RUNNER_ARGS[@]}")"

say "starting loom serve --driver-executor on $LOOM_PORT (cwd=$REPO)"
( cd "$REPO"; LOOM_DISABLE_H2C=1 LOOM_DRIVER_EXECUTOR=1 LOOM_DRIVER_EXECUTOR_NODE_ID="$NODE_ID" \
    LOOM_DRIVER_TASK_RUNNER_CMD_JSON="$CMD_JSON" LOOM_DRIVER_TASK_RUN_MAX_ATTEMPTS=1 GITHUB_TOKEN="$TOKEN" \
    "$BIN_DIR/loom" serve --port "$LOOM_PORT" --frontend-url "http://127.0.0.1:9" ) >"$TMP_ROOT/loom-serve.log" 2>&1 & PIDS+=($!)
for _ in $(seq 1 80); do curl -fsS "$LOOM_URL/health" >/dev/null 2>&1 && break; sleep 0.25; done
curl -fsS "$LOOM_URL/health" >/dev/null 2>&1 || { echo "serve down"; tail -20 "$TMP_ROOT/loom-serve.log"; exit 1; }

say "running epic with openPullRequest=true"
( cd "$REPO"; "$BIN_DIR/loom" --workspace "$WS" driver run epic-runner --epic "$EPIC" --run-id "$RUN_ID" \
    --input "runner=local-task-runner" --input "maxConcurrency=1" --input "openPullRequest=true" --input "baseBranch=$BASE" --json ) \
  >"$TMP_ROOT/driver-run.json" 2>&1 || { echo "run queue failed"; tail -20 "$TMP_ROOT/driver-run.json"; }

STATUS=""
for _ in $(seq 1 300); do
  # || true: under set -e+pipefail a transient curl failure must not abort the retry loop
  STATUS="$(curl -fsS -H "X-Actor: $ACTOR" "$FLEET_URL/api/v1/$WS/driver-runs/$RUN_ID" 2>/dev/null | jqr '.status')" || true
  case "$STATUS" in completed|failed|needs_review|cancelled) break;; esac
  sleep 1
done
say "driver-run status: $STATUS"

TRJ="$(curl -fsS -H "X-Actor: $ACTOR" "$FLEET_URL/api/v1/$WS/task-runs?driver_run_id=$RUN_ID" 2>/dev/null)" || true
echo "$TRJ" > "$TMP_ROOT/task-runs.json"
jq -r '.task_runs[]? | "  status="+.status+" strategy="+(.runtime_metadata.runtime_strategy//"-")+" delivery="+(.runtime_metadata.delivery//"-")+" PR="+(.runtime_metadata.github_pr_url//"-")' <<<"$TRJ"

COMPLETED="$(jq '[.task_runs[]? | select(.status=="completed" and .runtime_metadata.delivery=="pull_request")] | length' <<<"$TRJ")"
PRS="$(jq -r '[.task_runs[]?.runtime_metadata.github_pr_url // empty] | unique | length' <<<"$TRJ")"
echo
echo "PR_URLS:"; jq -r '.task_runs[]?.runtime_metadata.github_pr_url // empty' <<<"$TRJ" | sort -u
if [ "$COMPLETED" = "2" ] && [ "$PRS" = "2" ]; then
  echo "PR e2e PASSED backend=$BACKEND (2 completed PR-delivery task-runs, 2 distinct PRs)"
else
  echo "PR e2e FAILED backend=$BACKEND (completed_pr=$COMPLETED distinct_prs=$PRS driver_run=$STATUS)"; exit 1
fi
