#!/usr/bin/env bash
set -euo pipefail

ROOT="${RESULT_ROOT:-$(mktemp -d /tmp/loom-real-epic.XXXXXX)}"
ARTIFACTS_OUT="${ARTIFACTS_OUT:-}"
DAEMON_PID=""
EPIC_RUNNER_TIMEOUT="${EPIC_RUNNER_TIMEOUT:-900s}"
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
        echo "---- real epic runner debug ----" >&2
        echo "root: $ROOT" >&2
        if [[ -f "$ROOT/runner.log" ]]; then
            echo "---- runner.log ----" >&2
            cat "$ROOT/runner.log" >&2
        fi
        if [[ -f "$ROOT/daemon.log" ]]; then
            echo "---- daemon important lines ----" >&2
            grep -E 'agent command|agent-commands|agent started|failed to start|config changed|skipping add|desired_state|worktree|claimed task|spawned subprocess|exited' "$ROOT/daemon.log" >&2 || true
        fi
        if [[ -d "$WORKSPACE_PATH/.loom/logs" ]]; then
            while IFS= read -r log; do
                [[ -f "$log" ]] || continue
                echo "---- ${log#$WORKSPACE_PATH/} ----" >&2
                tail -n 240 "$log" >&2
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
    exit "$status"
}
trap cleanup EXIT INT TERM

export LOOM_CONFIG_DIR="$ROOT/loom"
export LOOM_WORKSPACE="OCTOREAL"
export LOOM_BACKEND="codex"
export LOOM_ISSUE_BACKEND="fleetdb"
export LOOM_FLEET_DB_ACTOR="real-epic-runner-e2e"
export LEAD_SESSION_ID="lead-session-real-octocat"
export GIT_AUTHOR_NAME="Loom Real E2E"
export GIT_AUTHOR_EMAIL="loom-real-e2e@example.test"
export GIT_COMMITTER_NAME="$GIT_AUTHOR_NAME"
export GIT_COMMITTER_EMAIL="$GIT_AUTHOR_EMAIL"

mkdir -p "$LOOM_CONFIG_DIR"
git config --global user.name "$GIT_AUTHOR_NAME"
git config --global user.email "$GIT_AUTHOR_EMAIL"
git config --global --add safe.directory '*'

rm -f /usr/local/bin/codex
npm install -g @openai/codex@0.129.0 >/tmp/codex-install.log
codex --version

SEED="$ROOT/Hello-World"
REMOTE="$ROOT/hello-world.git"

git clone https://github.com/octocat/Hello-World.git "$SEED"
SOURCE_BRANCH="$(git -C "$SEED" symbolic-ref --short refs/remotes/origin/HEAD 2>/dev/null | sed 's#^origin/##' || true)"
if [[ -z "$SOURCE_BRANCH" ]]; then
    SOURCE_BRANCH="$(git -C "$SEED" branch --show-current)"
fi
if [[ -z "$SOURCE_BRANCH" ]]; then
    SOURCE_BRANCH="master"
fi
DEFAULT_BRANCH="loom-e2e-target"

cat > "$SEED/Makefile" <<'EOF'
.PHONY: gate
gate:
	test -f README
	test -f epic-runner-real/task-a.txt
	test -f epic-runner-real/task-b.txt || true
EOF
git -C "$SEED" add Makefile
git -C "$SEED" commit -m "Add local E2E gate" >/dev/null

git init --bare "$REMOTE" >/dev/null
git -C "$SEED" remote set-url origin "$REMOTE"
git -C "$SEED" push -u origin "HEAD:$DEFAULT_BRANCH" >/dev/null

loom workspace create "$LOOM_WORKSPACE" --repos "$SEED" --path "$WORKSPACE_PATH" --branch "$DEFAULT_BRANCH"
cd "$WORKSPACE_PATH"

loom daemon > "$ROOT/daemon.log" 2>&1 &
DAEMON_PID="$!"
NODE_ID="loom-supervisor-$(hostname)-$DAEMON_PID"

for _ in {1..80}; do
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

REPO_NAME="$(loom workspace show --json "$LOOM_WORKSPACE" | jq -r '.repos[0].name')"

create_issue() {
    loom data --output json create "$@" | jq -r '.id'
}

EPIC_ID="$(create_issue \
    --title "Real Codex epic runner on octocat Hello-World" \
    --type epic \
    --status open)"

DESIGN_A=$'Real Codex E2E task using the octocat/Hello-World repository content mirrored to a local writable remote.\nDo exactly this:\n1. Inspect README and Makefile.\n2. Create directory epic-runner-real.\n3. Create epic-runner-real/task-a.txt with one line containing this pre-assigned task ID and the phrase first real runner task.\n4. Create epic-runner-real/order.log with this pre-assigned task ID as the first line.\n5. Run make gate. It is acceptable that task-b.txt does not exist yet; the Makefile allows that for the first task.\n6. Commit, run loom push for your agent branch, close the task, and run loom complete as instructed by the workflow.'
TASK_A="$(create_issue \
    --title "Create first real runner file" \
    --type task \
    --status open \
    --parent "$EPIC_ID" \
    --source-repo "$REPO_NAME" \
    --design "$DESIGN_A")"

DESIGN_B=$'Real Codex E2E dependent task using the octocat/Hello-World repository content mirrored to a local writable remote.\nDo exactly this:\n1. Confirm epic-runner-real/task-a.txt exists in your checkout before editing.\n2. Create epic-runner-real/task-b.txt with one line containing this pre-assigned task ID and the phrase second real runner task.\n3. Append this pre-assigned task ID as the second line of epic-runner-real/order.log without removing the first line.\n4. Run make gate.\n5. Commit, run loom push for your agent branch, close the task, and run loom complete as instructed by the workflow.'
TASK_B="$(create_issue \
    --title "Create dependent real runner file" \
    --type task \
    --status open \
    --parent "$EPIC_ID" \
    --source-repo "$REPO_NAME" \
    --depends-on "$TASK_A" \
    --design "$DESIGN_B")"

loom role add lead --description "Lead orchestration agent" --backend codex
loom agentdef add nova --role lead --auto --repos "$REPO_NAME"

echo "WORKSPACE=$LOOM_WORKSPACE"
echo "WORKSPACE_PATH=$WORKSPACE_PATH"
echo "REMOTE=$REMOTE"
echo "REPO_NAME=$REPO_NAME"
echo "DEFAULT_BRANCH=$DEFAULT_BRANCH"
echo "EPIC_ID=$EPIC_ID"
echo "TASK_A=$TASK_A"
echo "TASK_B=$TASK_B"

set -o pipefail
LOOM_ORCHESTRATOR_SESSION_ID="$LEAD_SESSION_ID" timeout "$EPIC_RUNNER_TIMEOUT" loom epic run \
    --parent "$EPIC_ID" \
    --lead nova \
    --max-concurrency 1 \
    --interval-seconds 2 \
    --node-id "$NODE_ID" \
    --role task | tee "$ROOT/runner.log"

loom data --output json show "$TASK_A" | jq -e '.status == "closed"' >/dev/null
loom data --output json show "$TASK_B" | jq -e '.status == "closed"' >/dev/null

git -C "$WORKSPACE_PATH/$REPO_NAME" fetch origin "$DEFAULT_BRANCH" >/dev/null
git -C "$WORKSPACE_PATH/$REPO_NAME" show "origin/$DEFAULT_BRANCH:epic-runner-real/task-a.txt" | grep -q "$TASK_A"
git -C "$WORKSPACE_PATH/$REPO_NAME" show "origin/$DEFAULT_BRANCH:epic-runner-real/task-b.txt" | grep -q "$TASK_B"

ORDER="$(git -C "$WORKSPACE_PATH/$REPO_NAME" show "origin/$DEFAULT_BRANCH:epic-runner-real/order.log")"
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

loom workspace show --json "$LOOM_WORKSPACE" > "$ROOT/workspace-final.json"
loom data --output json list --parent "$EPIC_ID" > "$ROOT/issues-final.json"

echo "PASS real Codex epic runner octocat E2E"
