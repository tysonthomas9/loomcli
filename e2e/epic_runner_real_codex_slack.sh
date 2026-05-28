#!/usr/bin/env bash
set -euo pipefail

ROOT="${RESULT_ROOT:-$(mktemp -d /tmp/loom-real-slack-epic.XXXXXX)}"
ARTIFACTS_OUT="${ARTIFACTS_OUT:-}"
SERVE_PID=""
DAEMON_PID=""
EPIC_RUNNER_TIMEOUT="${EPIC_RUNNER_TIMEOUT:-1200s}"
WORKSPACE_PATH="$ROOT/workspace"

cleanup() {
    status=$?
    set +e
    echo "RESULT_ROOT=$ROOT"
    if [[ -n "$ARTIFACTS_OUT" ]]; then
        mkdir -p "$ARTIFACTS_OUT"
        cp -a "$ROOT" "$ARTIFACTS_OUT/"
        echo "RESULT_COPY=$ARTIFACTS_OUT/$(basename "$ROOT")"
    fi
    if [[ "$status" -ne 0 ]]; then
        echo "---- real Slack epic runner debug ----" >&2
        echo "root: $ROOT" >&2
        for log in "$ROOT"/runner-*.log "$ROOT/daemon.log" "$ROOT/serve.log"; do
            [[ -f "$log" ]] || continue
            echo "---- ${log#$ROOT/} ----" >&2
            tail -n 260 "$log" >&2
        done
        if [[ -d "$WORKSPACE_PATH/.loom/logs" ]]; then
            while IFS= read -r log; do
                [[ -f "$log" ]] || continue
                echo "---- ${log#$WORKSPACE_PATH/} ----" >&2
                tail -n 260 "$log" >&2
            done < <(find "$WORKSPACE_PATH/.loom/logs" -type f | sort)
        fi
        if command -v loom >/dev/null 2>&1; then
            loom workspace show --json "$LOOM_WORKSPACE" >&2 || true
        fi
    fi
    if [[ -n "$DAEMON_PID" ]] && kill -0 "$DAEMON_PID" 2>/dev/null; then
        kill "$DAEMON_PID" 2>/dev/null || true
        wait "$DAEMON_PID" 2>/dev/null || true
    fi
    if [[ -n "$SERVE_PID" ]] && kill -0 "$SERVE_PID" 2>/dev/null; then
        kill "$SERVE_PID" 2>/dev/null || true
        wait "$SERVE_PID" 2>/dev/null || true
    fi
    exit "$status"
}
trap cleanup EXIT INT TERM

export LOOM_CONFIG_DIR="$ROOT/loom"
export LOOM_WORKSPACE="SLACKREAL"
export LOOM_BACKEND="codex"
export LOOM_ISSUE_BACKEND="fleetdb"
export LOOM_FLEET_DB_ACTOR="real-slack-epic-runner-e2e"
export GIT_AUTHOR_NAME="Loom Real Slack E2E"
export GIT_AUTHOR_EMAIL="loom-real-slack-e2e@example.test"
export GIT_COMMITTER_NAME="$GIT_AUTHOR_NAME"
export GIT_COMMITTER_EMAIL="$GIT_AUTHOR_EMAIL"

mkdir -p "$LOOM_CONFIG_DIR"
git config --global user.name "$GIT_AUTHOR_NAME"
git config --global user.email "$GIT_AUTHOR_EMAIL"
git config --global --add safe.directory '*'

process_running() {
    local pid="$1"
    kill -0 "$pid" 2>/dev/null || return 1
    jobs -pr | grep -qx "$pid"
}

fleetdb_runtime_url() {
    jq -r '.url // empty' "$LOOM_CONFIG_DIR/fleet-db/runtime.json" 2>/dev/null || true
}

wait_for_persistent_fleetdb() {
    local url
    for _ in {1..80}; do
        if ! process_running "$SERVE_PID"; then
            echo "loom serve exited before FleetDB became ready" >&2
            cat "$ROOT/serve.log" >&2 || true
            exit 1
        fi
        url="$(fleetdb_runtime_url)"
        if [[ -n "$url" ]] && curl -fsS "$url/healthz" >/dev/null 2>&1; then
            return 0
        fi
        sleep 0.25
    done
    echo "persistent FleetDB did not become ready" >&2
    cat "$ROOT/serve.log" >&2 || true
    exit 1
}

has_active_daemon_node() {
    local url nodes
    url="$(fleetdb_runtime_url)"
    [[ -n "$url" ]] || return 1
    nodes="$(curl -fsS -H "X-Actor: $LOOM_FLEET_DB_ACTOR" "$url/api/v1/$LOOM_WORKSPACE/nodes" 2>/dev/null)" || return 1
    jq -e '.nodes | map(select((.drain_state // "") == "active")) | length == 1' >/dev/null <<<"$nodes"
}

rm -f /usr/local/bin/codex
npm install -g "@openai/codex@${CODEX_CLI_VERSION:-0.129.0}" >/tmp/codex-install.log
codex --version

SEED="$ROOT/slack-app"
REMOTE="$ROOT/slack-app.git"
DEFAULT_BRANCH="main"

cp -a /src/scripts/fixtures/slack-src "$SEED"
git init --bare "$REMOTE" >/dev/null
git -C "$SEED" init >/dev/null
git -C "$SEED" checkout -b seed >/dev/null
git -C "$SEED" remote add origin "$REMOTE"
git -C "$SEED" add .
git -C "$SEED" commit -m "Seed Slack app fixture" >/dev/null
git -C "$SEED" push -u origin "HEAD:$DEFAULT_BRANCH" >/dev/null

rm -f "$LOOM_CONFIG_DIR/fleet-db/runtime.json"
loom serve --no-daemon --port 18080 --bind 127.0.0.1 > "$ROOT/serve.log" 2>&1 &
SERVE_PID="$!"
wait_for_persistent_fleetdb

loom workspace create "$LOOM_WORKSPACE" --repos "$SEED" --path "$WORKSPACE_PATH" --branch "$DEFAULT_BRANCH"
cd "$WORKSPACE_PATH"

REPO_NAME="$(loom workspace show --json "$LOOM_WORKSPACE" | jq -r '.repos[0].name')"

if loom role show lead >/dev/null 2>&1; then
    loom role set lead description "Lead orchestration agent" >/dev/null
    loom role set lead backend codex >/dev/null
else
loom role add lead --description "Lead orchestration agent" --backend codex
fi
loom agentdef add nova --role lead --auto --repos "$REPO_NAME"
loom agentdef add atlas --role lead --auto --repos "$REPO_NAME"

loom daemon > "$ROOT/daemon.log" 2>&1 &
DAEMON_PID="$!"

daemon_ready=0
for _ in {1..80}; do
    if ! process_running "$DAEMON_PID"; then
        echo "daemon exited before becoming ready" >&2
        cat "$ROOT/daemon.log" >&2 || true
        exit 1
    fi
    if has_active_daemon_node; then
        daemon_ready=1
        break
    fi
    sleep 0.5
done

if [[ "$daemon_ready" -ne 1 ]]; then
    echo "daemon did not become ready" >&2
    cat "$ROOT/daemon.log" >&2 || true
    exit 1
fi

create_issue() {
    loom data --output json create "$@" | jq -r '.id'
}

set_design() {
    local task_id="$1"
    local design
    design="$(cat)"
    loom data update "$task_id" --design "$design" >/dev/null
}

EPIC_NAV="$(create_issue \
    --title "Slack app channel navigation polish" \
    --type epic \
    --status open)"
EPIC_MSG="$(create_issue \
    --title "Slack app message interaction polish" \
    --type epic \
    --status open)"

TASK_NAV_DATA="$(create_issue \
    --title "Add channel insight seed data" \
    --type task \
    --status open \
    --parent "$EPIC_NAV" \
    --source-repo "$REPO_NAME")"
set_design "$TASK_NAV_DATA" <<EOF
Real Codex Slack E2E task.
Do exactly this in the Slack app repository:
1. Inspect src/data.js, src/app.js, src/styles.css, and test/smoke.test.mjs.
2. In src/data.js, export a channelInsights object keyed by channel id. It must include c1.summary with the word unread and c1.ownerId set to an existing user id.
3. Update test/smoke.test.mjs to import channelInsights and assert channelInsights.c1.summary includes unread.
4. Create epic-runner-slack/channel-insights-$TASK_NAV_DATA.txt containing exactly: $TASK_NAV_DATA channel insights
5. Run npm test and npm run build.
6. Commit only your changes, run loom push for your agent branch, close the task, and run loom complete.
EOF

TASK_NAV_RENDER="$(create_issue \
    --title "Render active channel insight" \
    --type task \
    --status open \
    --parent "$EPIC_NAV" \
    --source-repo "$REPO_NAME" \
    --depends-on "$TASK_NAV_DATA")"
set_design "$TASK_NAV_RENDER" <<EOF
Real Codex Slack E2E dependent task.
Do exactly this in the Slack app repository:
1. Confirm src/data.js exports channelInsights before editing.
2. In src/app.js, import channelInsights and render the active channel summary in the conversation header using class channel-insight.
3. Render the active channel owner using class channel-owner.
4. Add responsive styles for .channel-insight and .channel-owner in src/styles.css.
5. Create epic-runner-slack/channel-render-$TASK_NAV_RENDER.txt containing exactly: $TASK_NAV_RENDER channel render
6. Run npm test and npm run build.
7. Commit only your changes, run loom push for your agent branch, close the task, and run loom complete.
EOF

TASK_REACT_DATA="$(create_issue \
    --title "Add message reaction seed data" \
    --type task \
    --status open \
    --parent "$EPIC_MSG" \
    --source-repo "$REPO_NAME")"
set_design "$TASK_REACT_DATA" <<EOF
Real Codex Slack E2E task.
Do exactly this in the Slack app repository:
1. Inspect src/data.js, src/app.js, src/styles.css, and test/smoke.test.mjs.
2. In src/data.js, add a reactions array to at least two messages. Use ASCII labels such as thumbs-up and eyes; each reaction must have a numeric count.
3. Update test/smoke.test.mjs to assert at least one message has a non-empty reactions array.
4. Create epic-runner-slack/message-reactions-$TASK_REACT_DATA.txt containing exactly: $TASK_REACT_DATA message reactions
5. Run npm test and npm run build.
6. Commit only your changes, run loom push for your agent branch, close the task, and run loom complete.
EOF

TASK_REACT_RENDER="$(create_issue \
    --title "Render message reactions" \
    --type task \
    --status open \
    --parent "$EPIC_MSG" \
    --source-repo "$REPO_NAME" \
    --depends-on "$TASK_REACT_DATA")"
set_design "$TASK_REACT_RENDER" <<EOF
Real Codex Slack E2E dependent task.
Do exactly this in the Slack app repository:
1. Confirm src/data.js messages include reactions arrays before editing.
2. In src/app.js, render each message's reactions below the message text using a container with class reaction-row and buttons or spans with class reaction-chip.
3. Add styles for .reaction-row and .reaction-chip in src/styles.css.
4. Create epic-runner-slack/reaction-render-$TASK_REACT_RENDER.txt containing exactly: $TASK_REACT_RENDER reaction render
5. Run npm test and npm run build.
6. Commit only your changes, run loom push for your agent branch, close the task, and run loom complete.
EOF

echo "WORKSPACE=$LOOM_WORKSPACE"
echo "WORKSPACE_PATH=$WORKSPACE_PATH"
echo "REMOTE=$REMOTE"
echo "REPO_NAME=$REPO_NAME"
echo "DEFAULT_BRANCH=$DEFAULT_BRANCH"
echo "EPIC_NAV=$EPIC_NAV"
echo "TASK_NAV_DATA=$TASK_NAV_DATA"
echo "TASK_NAV_RENDER=$TASK_NAV_RENDER"
echo "EPIC_MSG=$EPIC_MSG"
echo "TASK_REACT_DATA=$TASK_REACT_DATA"
echo "TASK_REACT_RENDER=$TASK_REACT_RENDER"

run_epic() {
    local epic_id="$1"
    local lead="$2"
    local session="$3"
    local log_name="$4"
    set -o pipefail
    LOOM_ORCHESTRATOR_SESSION_ID="$session" timeout "$EPIC_RUNNER_TIMEOUT" loom epic run \
        --parent "$epic_id" \
        --lead "$lead" \
        --max-concurrency 1 \
        --interval-seconds 2 \
        --role task | tee "$ROOT/$log_name"
}

run_epic "$EPIC_NAV" nova lead-session-slack-nav runner-nav.log
run_epic "$EPIC_MSG" atlas lead-session-slack-message runner-message.log

for task_id in "$TASK_NAV_DATA" "$TASK_NAV_RENDER" "$TASK_REACT_DATA" "$TASK_REACT_RENDER"; do
    loom data --output json show "$task_id" | jq -e '.status == "closed"' >/dev/null
done

git -C "$WORKSPACE_PATH/$REPO_NAME" fetch origin "$DEFAULT_BRANCH" >/dev/null

FINAL="$ROOT/final-slack"
git clone "$REMOTE" "$FINAL" >/dev/null
git -C "$FINAL" checkout "$DEFAULT_BRANCH" >/dev/null

grep -q "$TASK_NAV_DATA channel insights" "$FINAL/epic-runner-slack/channel-insights-$TASK_NAV_DATA.txt"
grep -q "$TASK_NAV_RENDER channel render" "$FINAL/epic-runner-slack/channel-render-$TASK_NAV_RENDER.txt"
grep -q "$TASK_REACT_DATA message reactions" "$FINAL/epic-runner-slack/message-reactions-$TASK_REACT_DATA.txt"
grep -q "$TASK_REACT_RENDER reaction render" "$FINAL/epic-runner-slack/reaction-render-$TASK_REACT_RENDER.txt"
grep -q "channelInsights" "$FINAL/src/data.js"
grep -q "channel-insight" "$FINAL/src/app.js"
grep -q "channel-owner" "$FINAL/src/app.js"
grep -q "reactions" "$FINAL/src/data.js"
grep -q "reaction-row" "$FINAL/src/app.js"
grep -q "reaction-chip" "$FINAL/src/app.js"

(
    cd "$FINAL"
    npm test
    npm run build
)

loom workspace show --json "$LOOM_WORKSPACE" > "$ROOT/workspace-final.json"
loom data --output json list --parent "$EPIC_NAV" > "$ROOT/issues-nav-final.json"
loom data --output json list --parent "$EPIC_MSG" > "$ROOT/issues-message-final.json"

echo "PASS real Codex Slack epic runner E2E"
