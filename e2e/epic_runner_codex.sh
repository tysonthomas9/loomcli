#!/usr/bin/env bash
# End-to-end epic runner smoke test.
#
# This runs inside the e2e Podman image and exercises:
#   workspace create -> daemon node -> epic runner -> task runs
#   -> Codex backend CLI -> patch-back -> close -> dependent unblock.

set -euo pipefail

ROOT="$(mktemp -d /tmp/loom-epic-runner.XXXXXX)"
DAEMON_PID=""
SERVER_PID=""
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
        if [[ -f "$ROOT/serve.log" ]]; then
            echo "---- loom serve log ----" >&2
            cat "$ROOT/serve.log" >&2
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
    if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
    fi
    return "$status"
}
trap cleanup EXIT INT TERM

export LOOM_CONFIG_DIR="$ROOT/loom"
export LOOM_WORKSPACE="EPICRUN"
export LOOM_BACKEND="codex"
export LOOM_ISSUE_BACKEND="fleetdb"
export LOOM_FLEET_DB_ACTOR="epic-runner-e2e"
export LOOM_SDK_ROOT="${LOOM_SDK_ROOT:-/src/sdk}"
export STUB_CODEX_EPIC_RUNNER="1"
export STUB_CODEX_INVOCATIONS="/tmp/loom-epic-runner-codex-invocations.log"
export OPENAI_API_KEY="stub-e2e-not-a-real-key"
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

sleep 0.5
for _ in {1..40}; do
    if loom workspace show --json "$LOOM_WORKSPACE" >/dev/null 2>&1; then
        break
    fi
    sleep 0.25
done
if ! loom workspace show --json "$LOOM_WORKSPACE" >/dev/null 2>&1; then
    echo "workspace did not become readable after creation" >&2
    exit 1
fi
cd "$WORKSPACE_PATH"

if loom role show lead >/dev/null 2>&1; then
    loom role set lead description "Lead orchestration agent" >/dev/null
    loom role set lead backend codex >/dev/null
else
    loom role add lead --description "Lead orchestration agent" --backend codex
fi
loom agentdef add nova --role lead --auto --repos app

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

# v5 workflows use the run-scoped driver HTTP API owned by loom serve. Keep
# the server alive while the detached CLI command queues the epic workflow;
# its driver worker then executes the run through the supported API transport.
loom serve --port 18080 > "$ROOT/serve.log" 2>&1 &
SERVER_PID="$!"
for _ in {1..60}; do
    if curl -fsS http://127.0.0.1:18080/health >/dev/null 2>&1; then
        break
    fi
    sleep 0.5
done
if ! curl -fsS http://127.0.0.1:18080/health >/dev/null 2>&1; then
    echo "loom serve did not become ready" >&2
    cat "$ROOT/serve.log" >&2 || true
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

DESIGN_B=$'STUB_CODEX_WRITE=epic-runner-output/task-b.txt\nRun only after the first task has completed.'
TASK_B="$(create_issue \
    --title "Use first file" \
    --type task \
    --status open \
    --parent "$EPIC_ID" \
    --source-repo app \
    --depends-on "$TASK_A" \
    --design "$DESIGN_B")"

LOOM_ORCHESTRATOR_SESSION_ID="$LEAD_SESSION_ID" loom epic run \
    --parent "$EPIC_ID" \
    --lead nova \
    --max-concurrency 1 \
    --interval-seconds 1 \
    --node-id "$NODE_ID" \
    --detach

export TASK_A TASK_B
timeout "$EPIC_RUNNER_TIMEOUT" bash -c '
    until [[ "$(loom data --output json show "$TASK_A" | jq -r .status)" == "closed" ]] \
       && [[ "$(loom data --output json show "$TASK_B" | jq -r .status)" == "closed" ]]; do
        sleep 1
    done
'

loom data --output json show "$TASK_A" | jq -e '.status == "closed"' >/dev/null
loom data --output json show "$TASK_B" | jq -e '.status == "closed"' >/dev/null

RUNTIME_URL="$(jq -r '.url' "$LOOM_CONFIG_DIR/fleet-db/runtime.json")"
TASK_RUNS="$(curl -fsS -H "X-Actor: $LOOM_FLEET_DB_ACTOR" \
    "$RUNTIME_URL/api/v1/$LOOM_WORKSPACE/task-runs")"
if ! jq -e --arg a "$TASK_A" --arg b "$TASK_B" '
    [.task_runs[] | select(
        (.task_id == $a or .task_id == $b)
        and .status == "completed"
        and .runtime_metadata.delivery == "patch_back"
    )] | length == 2
' <<< "$TASK_RUNS" >/dev/null; then
    echo "expected two completed task runs using patch-back delivery" >&2
    jq '.task_runs | map({task_run_id, task_id, status, runtime_metadata})' <<< "$TASK_RUNS" >&2
    exit 1
fi

TASK_A_RUN="$(jq -r --arg task "$TASK_A" '.task_runs[] | select(.task_id == $task) | .task_run_id' <<< "$TASK_RUNS")"
TASK_B_RUN="$(jq -r --arg task "$TASK_B" '.task_runs[] | select(.task_id == $task) | .task_run_id' <<< "$TASK_RUNS")"
TASK_A_WORKTREE="$WORKSPACE_PATH/.loom/task-worktrees/app/$TASK_A_RUN"
TASK_B_WORKTREE="$WORKSPACE_PATH/.loom/task-worktrees/app/$TASK_B_RUN"
if ! grep -q "$TASK_A" "$TASK_A_WORKTREE/epic-runner-output/task-a.txt" \
    || ! grep -q "$TASK_B" "$TASK_B_WORKTREE/epic-runner-output/task-b.txt"; then
    echo "expected task output in v5 task-run worktrees" >&2
    find "$WORKSPACE_PATH/.loom/task-worktrees" -maxdepth 5 -type f -print >&2 || true
    exit 1
fi

grep -q "$TASK_A" "$STUB_CODEX_INVOCATIONS"
grep -q "$TASK_B" "$STUB_CODEX_INVOCATIONS"

INVOCATION_ORDER="$(sed -n 's/^task=//p' "$STUB_CODEX_INVOCATIONS")"
[[ "$(printf '%s\n' "$INVOCATION_ORDER" | sed -n '1p')" == "$TASK_A" ]]
[[ "$(printf '%s\n' "$INVOCATION_ORDER" | sed -n '2p')" == "$TASK_B" ]]

loom workspace show --json "$LOOM_WORKSPACE" | jq -e \
    --arg epic "$EPIC_ID" \
    --arg session "$LEAD_SESSION_ID" \
    '.agents[] | select(.name == "nova" and .role_name == "lead" and .parent == $epic and .orchestrator_session_id == $session)' >/dev/null

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
