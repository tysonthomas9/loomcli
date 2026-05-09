#!/usr/bin/env bash
# End-to-end epic runner smoke test.
#
# This runs inside the e2e Podman image and exercises:
#   workspace create -> daemon node -> epic runner -> ephemeral task agents
#   -> Codex backend CLI -> commit -> loom push -> close -> dependent unblock.

set -euo pipefail

ROOT="$(mktemp -d /tmp/loom-epic-runner.XXXXXX)"
DAEMON_PID=""
EPIC_RUNNER_TIMEOUT="${EPIC_RUNNER_TIMEOUT:-120s}"
WORKSPACE_PATH=""

cleanup() {
    status=$?
    set +e
    if [[ "$status" -ne 0 ]]; then
        echo "---- epic runner e2e debug ----" >&2
        echo "root: $ROOT" >&2
        runtime_json="$LOOM_CONFIG_DIR/fleet-db/runtime.json"
        if [[ -f "$runtime_json" ]]; then
            echo "---- fleet-db runtime ----" >&2
            cat "$runtime_json" >&2
            runtime_url="$(jq -r '.url // empty' "$runtime_json" 2>/dev/null || true)"
            if [[ -n "$runtime_url" ]]; then
                echo "---- agent commands ----" >&2
                curl -s -H "X-Actor: $LOOM_FLEET_DB_ACTOR" "$runtime_url/api/v1/$LOOM_WORKSPACE/agent-commands" >&2 || true
                echo >&2
                echo "---- agents ----" >&2
                curl -s -H "X-Actor: $LOOM_FLEET_DB_ACTOR" "$runtime_url/api/v1/$LOOM_WORKSPACE/agents" >&2 || true
                echo >&2
            fi
        fi
        if [[ -f "$ROOT/daemon.log" ]]; then
            echo "---- daemon command/start log lines ----" >&2
            grep -E 'agent command|agent-commands|agent started|failed to start|config changed|skipping add|desired_state|worktree' "$ROOT/daemon.log" >&2 || true
        fi
        if [[ -d "$WORKSPACE_PATH/.loom/logs" ]]; then
            while IFS= read -r log; do
                [[ -f "$log" ]] || continue
                echo "---- ${log#$WORKSPACE_PATH/} ----" >&2
                cat "$log" >&2
            done < <(find "$WORKSPACE_PATH/.loom/logs" -type f | sort)
        fi
        if [[ -n "${STUB_CODEX_INVOCATIONS:-}" && -f "$STUB_CODEX_INVOCATIONS" ]]; then
            echo "---- codex invocations ----" >&2
            cat "$STUB_CODEX_INVOCATIONS" >&2
        fi
    fi
    if [[ -n "$DAEMON_PID" ]] && kill -0 "$DAEMON_PID" 2>/dev/null; then
        kill "$DAEMON_PID" 2>/dev/null || true
        wait "$DAEMON_PID" 2>/dev/null || true
    fi
    return "$status"
}
trap cleanup EXIT INT TERM

export LOOM_CONFIG_DIR="$ROOT/loom"
export LOOM_WORKSPACE="EPICRUN"
export LOOM_BACKEND="codex"
export LOOM_ISSUE_BACKEND="fleetdb"
export LOOM_FLEET_DB_ACTOR="epic-runner-e2e"
export STUB_CODEX_EPIC_RUNNER="1"
export STUB_CODEX_INVOCATIONS="$ROOT/codex-invocations.log"
export LEAD_SESSION_ID="lead-session-e2e"
export GIT_AUTHOR_NAME="Loom E2E"
export GIT_AUTHOR_EMAIL="loom-e2e@example.test"
export GIT_COMMITTER_NAME="$GIT_AUTHOR_NAME"
export GIT_COMMITTER_EMAIL="$GIT_AUTHOR_EMAIL"

mkdir -p "$LOOM_CONFIG_DIR"
git config --global user.name "$GIT_AUTHOR_NAME"
git config --global user.email "$GIT_AUTHOR_EMAIL"
git config --global --add safe.directory '*'

REMOTE="$ROOT/app.git"
SEED="$ROOT/app"
WORKSPACE_PATH="$ROOT/workspace"

git init --bare "$REMOTE" >/dev/null
git init "$SEED" >/dev/null
git -C "$SEED" checkout -b seed >/dev/null
git -C "$SEED" remote add origin "$REMOTE"
cat > "$SEED/README.md" <<'EOF'
# Epic Runner E2E
EOF
cat > "$SEED/Makefile" <<'EOF'
.PHONY: gate
gate:
	test -f README.md
EOF
git -C "$SEED" add README.md Makefile
git -C "$SEED" commit -m "Initial app" >/dev/null
git -C "$SEED" push origin seed:main >/dev/null

loom workspace create "$LOOM_WORKSPACE" --repos "$SEED" --path "$WORKSPACE_PATH" --branch main
cd "$WORKSPACE_PATH"

loom daemon > "$ROOT/daemon.log" 2>&1 &
DAEMON_PID="$!"
NODE_ID="loom-supervisor-$(hostname)-$DAEMON_PID"

for _ in {1..60}; do
    if loom daemon status >/dev/null 2>&1; then
        break
    fi
    sleep 0.5
done

if ! loom daemon status >/dev/null 2>&1; then
    echo "daemon did not become ready" >&2
    cat "$ROOT/daemon.log" >&2 || true
    exit 1
fi

create_issue() {
    loom data --output json create "$@" | jq -r '.id'
}

EPIC_ID="$(create_issue \
    --title "Epic runner Codex E2E" \
    --type epic \
    --status open)"

DESIGN_A=$'STUB_CODEX_WRITE=epic-runner-output/task-a.txt\nWrite the first file and integrate it before closing.'
TASK_A="$(create_issue \
    --title "Write first file" \
    --type task \
    --status open \
    --parent "$EPIC_ID" \
    --source-repo app \
    --design "$DESIGN_A")"

DESIGN_B=$'STUB_CODEX_REQUIRE=epic-runner-output/task-a.txt\nSTUB_CODEX_WRITE=epic-runner-output/task-b.txt\nUse the first file from the target branch before writing the second.'
TASK_B="$(create_issue \
    --title "Use first file" \
    --type task \
    --status open \
    --parent "$EPIC_ID" \
    --source-repo app \
    --depends-on "$TASK_A" \
    --design "$DESIGN_B")"

loom role add lead --description "Lead orchestration agent" --backend codex
loom agentdef add nova --role lead --auto --repos app

LOOM_ORCHESTRATOR_SESSION_ID="$LEAD_SESSION_ID" timeout "$EPIC_RUNNER_TIMEOUT" loom epic run \
    --parent "$EPIC_ID" \
    --lead nova \
    --max-concurrency 1 \
    --interval-seconds 1 \
    --node-id "$NODE_ID" \
    --role task

loom data --output json show "$TASK_A" | jq -e '.status == "closed"' >/dev/null
loom data --output json show "$TASK_B" | jq -e '.status == "closed"' >/dev/null

git -C "$WORKSPACE_PATH/app" fetch origin main >/dev/null
git -C "$WORKSPACE_PATH/app" show origin/main:epic-runner-output/task-a.txt | grep -q "$TASK_A"
git -C "$WORKSPACE_PATH/app" show origin/main:epic-runner-output/task-b.txt | grep -q "$TASK_B"

ORDER="$(git -C "$WORKSPACE_PATH/app" show origin/main:epic-runner-output/order.log)"
if [[ "$(printf '%s\n' "$ORDER" | sed -n '1p')" != "$TASK_A" ]]; then
    echo "expected first completed task to be $TASK_A, got:" >&2
    printf '%s\n' "$ORDER" >&2
    exit 1
fi
if [[ "$(printf '%s\n' "$ORDER" | sed -n '2p')" != "$TASK_B" ]]; then
    echo "expected second completed task to be $TASK_B, got:" >&2
    printf '%s\n' "$ORDER" >&2
    exit 1
fi

grep -q "$TASK_A" "$STUB_CODEX_INVOCATIONS"
grep -q "$TASK_B" "$STUB_CODEX_INVOCATIONS"

for _ in {1..30}; do
    if loom workspace show --json "$LOOM_WORKSPACE" | jq -e \
        '[.agents[] | select(.mode == "ephemeral" and .desired_state == "stopped" and .state == "stopped")] | length == 2' >/dev/null; then
        break
    fi
    sleep 0.5
done

loom workspace show --json "$LOOM_WORKSPACE" | jq -e \
    '[.agents[] | select(.mode == "ephemeral" and .desired_state == "stopped" and .state == "stopped")] | length == 2' >/dev/null

loom workspace show --json "$LOOM_WORKSPACE" | jq -e \
    --arg epic "$EPIC_ID" \
    --arg session "$LEAD_SESSION_ID" \
    '.agents[] | select(.name == "nova" and .role_name == "lead" and .parent == $epic and .orchestrator_session_id == $session)' >/dev/null

loom workspace show --json "$LOOM_WORKSPACE" | jq -e \
    --arg epic "$EPIC_ID" \
    --arg session "$LEAD_SESSION_ID" \
    '[.agents[] | select(.mode == "ephemeral" and .parent == $epic and .orchestrator_session_id == $session)] | length == 2' >/dev/null

OTHER_EPIC_ID="$(create_issue \
    --title "Other epic" \
    --type epic \
    --status open)"

if loom epic run --parent "$OTHER_EPIC_ID" --lead nova --dry-run >/tmp/loom-epic-runner-conflict.out 2>&1; then
    echo "expected lead conflict when nova already owns $EPIC_ID" >&2
    cat /tmp/loom-epic-runner-conflict.out >&2
    exit 1
fi
grep -q "already running epic $EPIC_ID" /tmp/loom-epic-runner-conflict.out

echo "PASS epic runner Codex E2E"
