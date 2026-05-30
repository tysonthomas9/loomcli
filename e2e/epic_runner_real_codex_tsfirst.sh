#!/usr/bin/env bash
# Real Codex epic runner driven entirely by the TypeScript SDK.
#
# Unlike epic_runner_real_codex_octocat.sh (which orchestrates with the
# imperative `loom epic run`), this defines the agent + an epic-runner workflow
# in TypeScript under .loom/ and drives the epic with the TS-first CLI:
#
#   loom check  --dir <ws>                 # compile + validate the .loom/ project
#   loom apply  nova --dir <ws>            # register the TS-defined agent
#   loom run    epic-runner --input {...}  # one WorkflowContext reconcile pass
#
# Each `loom run` pass reads the epic's ready child work items and dispatches a
# real Codex task worker per ready child (taskRuns.ensure -> dispatchTaskRun ->
# AgentCommands().Create -> daemon spawns the worker). Dependency ordering is
# honored because readyChildren only returns unblocked tasks, and taskRuns.ensure
# is idempotent, so repeating passes is safe. The reconcile loop is external:
# `loom run` performs a single pass, so we poll passes until the tasks close.

set -euo pipefail

ROOT="${RESULT_ROOT:-$(mktemp -d /tmp/loom-tsfirst-epic.XXXXXX)}"
ARTIFACTS_OUT="${ARTIFACTS_OUT:-}"
DAEMON_PID=""
SERVE_PID=""
EPIC_RUNNER_TIMEOUT="${EPIC_RUNNER_TIMEOUT:-900s}"
TIMEOUT_SECS="${EPIC_RUNNER_TIMEOUT%s}"
RECONCILE_INTERVAL="${RECONCILE_INTERVAL:-3}"
CODEX_VERSION="${CODEX_VERSION:-0.129.0}"
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
        echo "---- tsfirst epic runner debug ----" >&2
        echo "root: $ROOT" >&2
        if [[ -f "$ROOT/runner.log" ]]; then
            echo "---- runner.log (tail) ----" >&2
            tail -n 200 "$ROOT/runner.log" >&2
        fi
        if [[ -f "$ROOT/daemon.log" ]]; then
            echo "---- daemon important lines ----" >&2
            grep -E 'agent command|agent-commands|agent started|failed to start|config changed|skipping add|desired_state|worktree|claimed task|spawned subprocess|exited' "$ROOT/daemon.log" >&2 || true
        fi
        if [[ -d "$WORKSPACE_PATH/.loom/logs" ]]; then
            while IFS= read -r log; do
                [[ -f "$log" ]] || continue
                echo "---- ${log#"$WORKSPACE_PATH"/} ----" >&2
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
    if [[ -n "$SERVE_PID" ]] && kill -0 "$SERVE_PID" 2>/dev/null; then
        kill "$SERVE_PID" 2>/dev/null || true
        wait "$SERVE_PID" 2>/dev/null || true
    fi
    exit "$status"
}
trap cleanup EXIT INT TERM

export LOOM_CONFIG_DIR="$ROOT/loom"
export LOOM_WORKSPACE="TSFIRSTREAL"
export LOOM_BACKEND="codex"
export LOOM_ISSUE_BACKEND="fleetdb"
export LOOM_FLEET_DB_ACTOR="tsfirst-epic-runner-e2e"
export GIT_AUTHOR_NAME="Loom TSFirst E2E"
export GIT_AUTHOR_EMAIL="loom-tsfirst-e2e@example.test"
export GIT_COMMITTER_NAME="$GIT_AUTHOR_NAME"
export GIT_COMMITTER_EMAIL="$GIT_AUTHOR_EMAIL"

mkdir -p "$LOOM_CONFIG_DIR"
git config --global user.name "$GIT_AUTHOR_NAME"
git config --global user.email "$GIT_AUTHOR_EMAIL"
git config --global --add safe.directory '*'

rm -f /usr/local/bin/codex
npm install -g "@openai/codex@${CODEX_VERSION}" >/tmp/codex-install.log
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
DEFAULT_BRANCH="loom-tsfirst-target"

cat > "$SEED/Makefile" <<'EOF'
.PHONY: gate
gate:
	test -f README
	test -f epic-runner-tsfirst/task-a.txt
	test -f epic-runner-tsfirst/task-b.txt || true
EOF
git -C "$SEED" add Makefile
git -C "$SEED" commit -m "Add local TSFirst E2E gate" >/dev/null

git init --bare "$REMOTE" >/dev/null
git -C "$SEED" remote set-url origin "$REMOTE"
git -C "$SEED" push -u origin "HEAD:$DEFAULT_BRANCH" >/dev/null

loom workspace create "$LOOM_WORKSPACE" --repos "$SEED" --path "$WORKSPACE_PATH" --branch "$DEFAULT_BRANCH"
cd "$WORKSPACE_PATH"

# Start ONE long-lived process that OWNS the embedded fleet-db. Every other loom
# command in this LOOM_CONFIG_DIR (apply, daemon, run) then reuses this same live
# fleet-db via runtime.json instead of spinning up its own ephemeral instance, so
# the daemon and each `loom run` pass share one datastore.
loom serve --no-daemon > "$ROOT/serve.log" 2>&1 &
SERVE_PID="$!"
for _ in {1..60}; do
    if ! kill -0 "$SERVE_PID" 2>/dev/null; then
        echo "loom serve exited during startup" >&2
        cat "$ROOT/serve.log" >&2 || true
        exit 1
    fi
    [[ -f "$LOOM_CONFIG_DIR/fleet-db/runtime.json" ]] && break
    sleep 0.5
done
if [[ ! -f "$LOOM_CONFIG_DIR/fleet-db/runtime.json" ]]; then
    echo "loom serve did not become the embedded fleet-db owner" >&2
    cat "$ROOT/serve.log" >&2 || true
    exit 1
fi

# NOTE: the daemon is intentionally started later, AFTER `loom apply nova`.
# `loom daemon` exits immediately if the workspace has no agents configured.

REPO_NAME="$(loom workspace show --json "$LOOM_WORKSPACE" | jq -r '.repos[0].name')"

create_issue() {
    loom data --output json create "$@" | jq -r '.id'
}

EPIC_ID="$(create_issue \
    --title "TSFirst Codex epic runner on octocat Hello-World" \
    --type epic \
    --status open)"

DESIGN_A=$'Real Codex E2E task using the octocat/Hello-World repository content mirrored to a local writable remote.\nDo exactly this:\n1. Inspect README and Makefile.\n2. Create directory epic-runner-tsfirst.\n3. Create epic-runner-tsfirst/task-a.txt with one line containing this pre-assigned task ID and the phrase first tsfirst runner task.\n4. Create epic-runner-tsfirst/order.log with this pre-assigned task ID as the first line.\n5. Run make gate. It is acceptable that task-b.txt does not exist yet; the Makefile allows that for the first task.\n6. Commit, run loom push for your agent branch, close the task, and run loom complete as instructed by the workflow.'
TASK_A="$(create_issue \
    --title "Create first tsfirst runner file" \
    --type task \
    --status open \
    --parent "$EPIC_ID" \
    --source-repo "$REPO_NAME" \
    --design "$DESIGN_A")"

DESIGN_B=$'Real Codex E2E dependent task using the octocat/Hello-World repository content mirrored to a local writable remote.\nDo exactly this:\n1. Confirm epic-runner-tsfirst/task-a.txt exists in your checkout before editing.\n2. Create epic-runner-tsfirst/task-b.txt with one line containing this pre-assigned task ID and the phrase second tsfirst runner task.\n3. Append this pre-assigned task ID as the second line of epic-runner-tsfirst/order.log without removing the first line.\n4. Run make gate.\n5. Commit, run loom push for your agent branch, close the task, and run loom complete as instructed by the workflow.'
TASK_B="$(create_issue \
    --title "Create dependent tsfirst runner file" \
    --type task \
    --status open \
    --parent "$EPIC_ID" \
    --source-repo "$REPO_NAME" \
    --depends-on "$TASK_A" \
    --design "$DESIGN_B")"

# --- TypeScript SDK definitions ------------------------------------------------
# Deliberately free of TypeScript type-annotation syntax so the project compiles
# under the e2e image's Node 20 (which lacks module.stripTypeScriptTypes; the
# compiler falls back to regex stripping for plain object/function syntax).
mkdir -p "$WORKSPACE_PATH/.loom/agents" "$WORKSPACE_PATH/.loom/workflows"

# Delimiter is UNquoted so $REPO_NAME expands: the agent MUST declare a repo or
# the daemon cannot derive a runnable worktree target.
cat > "$WORKSPACE_PATH/.loom/agents/nova.ts" <<TS
import { defineAgent, runtime } from '@loom/runtime';

export default defineAgent({
  name: 'nova',
  description: 'Codex agent for the TypeScript-first epic runner E2E',
  backend: 'codex',
  repos: ['$REPO_NAME'],
  runtime: runtime.local({ repos: ['$REPO_NAME'] }),
});
TS

cat > "$WORKSPACE_PATH/.loom/workflows/epic-runner.ts" <<'TS'
import { defineWorkflow } from '@loom/runtime';

// Drives an epic to completion: each reconcile pass dispatches a Codex task
// worker for every currently-ready child work item. readyChildren excludes
// blocked tasks, so a dependency chain (task-b depends on task-a) is honored
// across passes. taskRuns.ensure is idempotent, so repeating passes is safe.
export default defineWorkflow({
  name: 'epic-runner',
  description: 'Dispatch ready child work items of an epic to Codex task workers',
  singleton: (input) => `epic:${input.parentId}`,
  tools: ['workItems.readyChildren', 'workItems.listChildren', 'taskRuns.ensure'],
  async run(ctx) {
    const parentId = String(ctx.input.parentId || '');
    if (!parentId) {
      throw new Error('epic-runner requires input.parentId');
    }
    const ready = await ctx.workItems.readyChildren(parentId);
    for (const issue of ready) {
      await ctx.taskRuns.ensure({
        workItemId: issue.id,
        role: 'task',
        reason: issue.title,
        metadata: { source: 'tsfirst-epic-runner', epic: parentId },
      });
      ctx.log.info('dispatched task worker', { task: issue.id, title: issue.title });
    }
    const children = await ctx.workItems.listChildren(parentId);
    const open = children.filter((i) => i.status !== 'closed' && i.status !== 'done').length;
    return { ensured: ready.length, openRemaining: open };
  },
});
TS

echo "WORKSPACE=$LOOM_WORKSPACE"
echo "WORKSPACE_PATH=$WORKSPACE_PATH"
echo "REMOTE=$REMOTE"
echo "REPO_NAME=$REPO_NAME"
echo "DEFAULT_BRANCH=$DEFAULT_BRANCH"
echo "EPIC_ID=$EPIC_ID"
echo "TASK_A=$TASK_A"
echo "TASK_B=$TASK_B"

# Compile/validate the TypeScript project (hard gate — fail fast on a bad .loom/).
if ! loom check --dir "$WORKSPACE_PATH" >> "$ROOT/runner.log" 2>&1; then
    echo "loom check failed to compile the .loom/ project" >&2
    exit 1
fi
# Register the TS-defined agent. This MUST succeed before the daemon starts:
# `loom daemon` exits immediately if the workspace has no agents configured.
if ! loom apply nova --dir "$WORKSPACE_PATH" >> "$ROOT/runner.log" 2>&1; then
    echo "loom apply nova failed" >&2
    tail -n 40 "$ROOT/runner.log" >&2 || true
    exit 1
fi

# Start the supervisor daemon now that an agent exists. It stays alive and owns
# the shared embedded fleet-db that subsequent `loom run` invocations reuse, and
# it executes the start commands that taskRuns.ensure dispatches.
loom daemon > "$ROOT/daemon.log" 2>&1 &
DAEMON_PID="$!"

daemon_ready=false
for _ in {1..80}; do
    if ! kill -0 "$DAEMON_PID" 2>/dev/null; then
        echo "daemon process exited during startup" >&2
        cat "$ROOT/daemon.log" >&2 || true
        exit 1
    fi
    if loom daemon status >/dev/null 2>&1; then
        daemon_ready=true
        break
    fi
    sleep 0.5
done
if [[ "$daemon_ready" != true ]]; then
    echo "daemon did not become ready" >&2
    cat "$ROOT/daemon.log" >&2 || true
    exit 1
fi

# External reconcile loop: each `loom run` performs one WorkflowContext pass.
# Repeat until both tasks close (Codex workers complete them) or we time out.
deadline=$(( $(date +%s) + TIMEOUT_SECS ))
pass=0
while :; do
    pass=$((pass + 1))
    echo "==== reconcile pass $pass ====" >> "$ROOT/runner.log"
    if ! loom run epic-runner \
        --input "{\"parentId\":\"$EPIC_ID\"}" \
        --dir "$WORKSPACE_PATH" >> "$ROOT/runner.log" 2>&1; then
        echo "pass $pass: loom run returned non-zero (continuing)" >> "$ROOT/runner.log"
    fi

    status_a="$(loom data --output json show "$TASK_A" | jq -r '.status')"
    status_b="$(loom data --output json show "$TASK_B" | jq -r '.status')"
    echo "pass $pass: task_a=$status_a task_b=$status_b" | tee -a "$ROOT/runner.log"

    if [[ "$status_a" == "closed" && "$status_b" == "closed" ]]; then
        break
    fi
    if (( $(date +%s) >= deadline )); then
        echo "timed out after ${TIMEOUT_SECS}s waiting for tasks to close" >&2
        exit 1
    fi
    sleep "$RECONCILE_INTERVAL"
done

# --- Assertions (identical outcome contract to the octocat runner) -------------
loom data --output json show "$TASK_A" | jq -e '.status == "closed"' >/dev/null
loom data --output json show "$TASK_B" | jq -e '.status == "closed"' >/dev/null

git -C "$WORKSPACE_PATH/$REPO_NAME" fetch origin "$DEFAULT_BRANCH" >/dev/null
git -C "$WORKSPACE_PATH/$REPO_NAME" show "origin/$DEFAULT_BRANCH:epic-runner-tsfirst/task-a.txt" | grep -q "$TASK_A"
git -C "$WORKSPACE_PATH/$REPO_NAME" show "origin/$DEFAULT_BRANCH:epic-runner-tsfirst/task-b.txt" | grep -q "$TASK_B"

ORDER="$(git -C "$WORKSPACE_PATH/$REPO_NAME" show "origin/$DEFAULT_BRANCH:epic-runner-tsfirst/order.log")"
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

echo "PASS tsfirst Codex epic runner octocat E2E"
