#!/usr/bin/env bash
# Real Codex epic runner driven by the TypeScript SDK, seeded with the
# Slack-clone app shell fixture (scripts/fixtures/slack-src).
#
# Same orchestration as epic_runner_real_codex_tsfirst.sh (define agent +
# workflow in .loom/, drive with `loom check` / `loom apply` / `loom run`), but
# the workspace repo is the Slack-clone app instead of octocat/Hello-World. The
# gate tasks remain deterministic file-creation steps so the E2E still has a
# clean pass/fail contract while Codex works inside a realistic app codebase.

set -euo pipefail

ROOT="${RESULT_ROOT:-$(mktemp -d /tmp/loom-tsfirst-slack.XXXXXX)}"
ARTIFACTS_OUT="${ARTIFACTS_OUT:-}"
DAEMON_PID=""
SERVE_PID=""
WORKSPACE_CREATED=""
EPIC_RUNNER_TIMEOUT="${EPIC_RUNNER_TIMEOUT:-900s}"
TIMEOUT_SECS="${EPIC_RUNNER_TIMEOUT%s}"
RECONCILE_INTERVAL="${RECONCILE_INTERVAL:-3}"
CODEX_VERSION="${CODEX_VERSION:-0.129.0}"
SLACK_SRC_DIR="${SLACK_SRC_DIR:-/opt/slack-src}"
AGENT_RUNTIME="${AGENT_RUNTIME:-local}"
DAYTONA_REMOTE_REPO_URL="${DAYTONA_REMOTE_REPO_URL:-}"
DAYTONA_FORCE_PUSH_REMOTE="${DAYTONA_FORCE_PUSH_REMOTE:-}"
DAYTONA_SNAPSHOT="${DAYTONA_SNAPSHOT:-}"
DAYTONA_TARGET="${DAYTONA_TARGET:-}"
DAYTONA_GIT_USERNAME="${DAYTONA_GIT_USERNAME:-x-access-token}"
DAYTONA_GIT_TOKEN_ENV="${DAYTONA_GIT_TOKEN_ENV:-}"
WORKSPACE_PATH="$ROOT/workspace"

DAYTONA_PUSH_ASKPASS="$ROOT/daytona-git-askpass.sh"

is_shell_identifier() {
    [[ "$1" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]
}

daytona_env_value() {
    local name="$1"
    if [[ -n "$name" ]] && is_shell_identifier "$name"; then
        printf '%s' "${!name:-}"
    fi
}

daytona_push_token_available() {
    [[ -n "$(daytona_env_value "$DAYTONA_GIT_TOKEN_ENV")" || -n "${GITHUB_TOKEN:-}" || -n "${GH_TOKEN:-}" ]]
}

validate_daytona_codex_auth_file() {
    local auth_file="${CODEX_AUTH_FILE:-}"
    [[ -n "$auth_file" ]] || return 0
    if [[ "$auth_file" != /* ]]; then
        echo "AGENT_RUNTIME=daytona CODEX_AUTH_FILE must be an absolute Daytona remote path, got: $auth_file" >&2
        exit 1
    fi
    case "$auth_file" in
        /Users/*|/private/*|/Volumes/*|/var/folders/*)
            echo "AGENT_RUNTIME=daytona CODEX_AUTH_FILE must point at a Daytona-provisioned remote auth.json, not host-local path: $auth_file" >&2
            exit 1
            ;;
    esac
    if [[ "${auth_file##*/}" != "auth.json" ]]; then
        echo "AGENT_RUNTIME=daytona CODEX_AUTH_FILE must point to auth.json, got: $auth_file" >&2
        exit 1
    fi
}

validate_daytona_fleetdb_url() {
    local fleet_url="${LOOM_FLEET_DB_URL:-}"
    if [[ -z "$fleet_url" ]]; then
        echo "AGENT_RUNTIME=daytona requires LOOM_FLEET_DB_URL pointing at a URL reachable from Daytona" >&2
        exit 1
    fi
    if [[ "$fleet_url" == /* ]]; then
        echo "AGENT_RUNTIME=daytona cannot use a host-local LOOM_FLEET_DB_URL: $fleet_url" >&2
        exit 1
    fi
    if [[ "$fleet_url" != *"://"* ]]; then
        echo "AGENT_RUNTIME=daytona LOOM_FLEET_DB_URL must use http or https: $fleet_url" >&2
        exit 1
    fi
    local scheme="${fleet_url%%://*}"
    case "$scheme" in
        http|https) ;;
        *)
            echo "AGENT_RUNTIME=daytona LOOM_FLEET_DB_URL must use http or https, got scheme '$scheme': $fleet_url" >&2
            exit 1
            ;;
    esac

    local rest="${fleet_url#*://}"
    local hostport="${rest%%/*}"
    local host="${hostport##*@}"
    if [[ "$host" == \[* ]]; then
        host="${host#\[}"
        host="${host%%\]*}"
    else
        host="${host%%:*}"
    fi
    local lower_host
    lower_host="$(printf '%s' "$host" | tr '[:upper:]' '[:lower:]')"
    if [[ -z "$lower_host" ]]; then
        echo "AGENT_RUNTIME=daytona LOOM_FLEET_DB_URL host is required: $fleet_url" >&2
        exit 1
    fi
    case "$lower_host" in
        localhost|*.localhost|host.docker.internal|127.*|0.0.0.0|10.*|192.168.*|169.254.*|172.1[6-9].*|172.2[0-9].*|172.3[0-1].*|::1|0:0:0:0:0:0:0:1|fe80:*)
            echo "AGENT_RUNTIME=daytona cannot use a host-local LOOM_FLEET_DB_URL: $fleet_url" >&2
            exit 1
            ;;
    esac
}

validate_daytona_remote_repo_url() {
    local repo_url="$DAYTONA_REMOTE_REPO_URL"
    if [[ -z "$repo_url" ]]; then
        echo "AGENT_RUNTIME=daytona requires DAYTONA_REMOTE_REPO_URL for a Git remote reachable from Daytona" >&2
        exit 1
    fi
    if [[ "$repo_url" == *"://"* ]]; then
        local scheme="${repo_url%%://*}"
        case "$scheme" in
            http|https|ssh|git|file) ;;
            *)
                echo "AGENT_RUNTIME=daytona DAYTONA_REMOTE_REPO_URL uses unsupported scheme '$scheme': $repo_url" >&2
                exit 1
                ;;
        esac
    fi
    case "$repo_url" in
        http:*|https:*|ssh:*|git:*|file:*|ftp:*)
            if [[ "$repo_url" != *"://"* ]]; then
                local scheme="${repo_url%%:*}"
                echo "AGENT_RUNTIME=daytona DAYTONA_REMOTE_REPO_URL uses malformed scheme '$scheme'; use $scheme://... or scp-like Git syntax: $repo_url" >&2
                exit 1
            fi
            ;;
    esac
    case "$repo_url" in
        /*|./*|../*|~/*|file://*)
            echo "AGENT_RUNTIME=daytona cannot use a host-local DAYTONA_REMOTE_REPO_URL: $repo_url" >&2
            exit 1
            ;;
        http://localhost*|https://localhost*|ssh://*localhost*|git://localhost*|*localhost:*|*host.docker.internal*)
            echo "AGENT_RUNTIME=daytona cannot use a localhost DAYTONA_REMOTE_REPO_URL: $repo_url" >&2
            exit 1
            ;;
        http://127.*|https://127.*|ssh://*127.*|git://127.*|*@127.*:*|127.*:*|http://0.0.0.0*|https://0.0.0.0*|ssh://*0.0.0.0*|*@0.0.0.0:*)
            echo "AGENT_RUNTIME=daytona cannot use a loopback DAYTONA_REMOTE_REPO_URL: $repo_url" >&2
            exit 1
            ;;
    esac
}

write_daytona_push_askpass() {
cat > "$DAYTONA_PUSH_ASKPASS" <<'EOF'
#!/bin/sh
token=""
if [ -n "${DAYTONA_GIT_TOKEN_ENV:-}" ]; then
    case "$DAYTONA_GIT_TOKEN_ENV" in
        [0-9]*|*[!A-Za-z0-9_]*)
            ;;
        *)
            eval "token=\${$DAYTONA_GIT_TOKEN_ENV:-}"
            ;;
    esac
fi
if [ -z "$token" ]; then
    token="${GITHUB_TOKEN:-}"
fi
if [ -z "$token" ]; then
    token="${GH_TOKEN:-}"
fi
case "$1" in
    *Username*) printf '%s' "${DAYTONA_GIT_USERNAME:-x-access-token}" ;;
    *Password*) printf '%s' "$token" ;;
    *) printf '%s' "" ;;
esac
EOF
    chmod 700 "$DAYTONA_PUSH_ASKPASS"
}

push_seed_remote() {
    if [[ "$AGENT_RUNTIME" == "daytona" ]] && daytona_push_token_available; then
        write_daytona_push_askpass
        GIT_TERMINAL_PROMPT=0 GIT_ASKPASS="$DAYTONA_PUSH_ASKPASS" DAYTONA_GIT_USERNAME="$DAYTONA_GIT_USERNAME" git -C "$SEED" push "$@"
        return
    fi
    git -C "$SEED" push "$@"
}

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
        echo "---- tsfirst slack epic runner debug ----" >&2
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
        if [[ -n "$WORKSPACE_CREATED" ]] && command -v loom >/dev/null 2>&1; then
            loom workspace list --json >&2 || true
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
export LOOM_WORKSPACE="TSFIRSTSLACK"
export LOOM_BACKEND="codex"
export LOOM_ISSUE_BACKEND="fleetdb"
export LOOM_FLEET_DB_ACTOR="tsfirst-slack-epic-runner-e2e"
export GIT_AUTHOR_NAME="Loom TSFirst Slack E2E"
export GIT_AUTHOR_EMAIL="loom-tsfirst-slack-e2e@example.test"
export GIT_COMMITTER_NAME="$GIT_AUTHOR_NAME"
export GIT_COMMITTER_EMAIL="$GIT_AUTHOR_EMAIL"

if [[ "$AGENT_RUNTIME" != "local" && "$AGENT_RUNTIME" != "daytona" ]]; then
    echo "AGENT_RUNTIME must be local or daytona, got: $AGENT_RUNTIME" >&2
    exit 1
fi
if [[ "$AGENT_RUNTIME" == "daytona" ]]; then
    if [[ -z "$DAYTONA_GIT_TOKEN_ENV" ]]; then
        if [[ -n "${GITHUB_TOKEN:-}" ]]; then
            DAYTONA_GIT_TOKEN_ENV="GITHUB_TOKEN"
        elif [[ -n "${GH_TOKEN:-}" ]]; then
            DAYTONA_GIT_TOKEN_ENV="GH_TOKEN"
	    else
	        DAYTONA_GIT_TOKEN_ENV="GITHUB_TOKEN"
	    fi
	fi
	if ! is_shell_identifier "$DAYTONA_GIT_TOKEN_ENV"; then
	    echo "AGENT_RUNTIME=daytona requires DAYTONA_GIT_TOKEN_ENV to be a valid environment variable name, got: $DAYTONA_GIT_TOKEN_ENV" >&2
	    exit 1
	fi
	if [[ -z "${DAYTONA_API_KEY:-}" ]]; then
	    echo "AGENT_RUNTIME=daytona requires DAYTONA_API_KEY" >&2
	    exit 1
    fi
    if [[ -z "${OPENAI_API_KEY:-}" && -z "${CODEX_AUTH_FILE:-}" ]]; then
        echo "AGENT_RUNTIME=daytona requires OPENAI_API_KEY or a Daytona-provisioned CODEX_AUTH_FILE" >&2
        exit 1
    fi
    validate_daytona_codex_auth_file
    validate_daytona_fleetdb_url
	validate_daytona_remote_repo_url
	case "$DAYTONA_REMOTE_REPO_URL" in
	    http://*|https://*)
	        if ! daytona_push_token_available; then
	            if [[ "$DAYTONA_GIT_TOKEN_ENV" == "GITHUB_TOKEN" ]]; then
	                echo "AGENT_RUNTIME=daytona requires GITHUB_TOKEN or GH_TOKEN to seed HTTPS remote $DAYTONA_REMOTE_REPO_URL" >&2
	            else
	                echo "AGENT_RUNTIME=daytona requires $DAYTONA_GIT_TOKEN_ENV, GITHUB_TOKEN, or GH_TOKEN to seed HTTPS remote $DAYTONA_REMOTE_REPO_URL" >&2
	            fi
	            exit 1
	        fi
	        ;;
	esac
fi

mkdir -p "$LOOM_CONFIG_DIR"
git config --global user.name "$GIT_AUTHOR_NAME"
git config --global user.email "$GIT_AUTHOR_EMAIL"
git config --global --add safe.directory '*'

if [[ "$AGENT_RUNTIME" == "local" ]]; then
    rm -f /usr/local/bin/codex
    npm install -g "@openai/codex@${CODEX_VERSION}" >/tmp/codex-install.log
    codex --version
else
    echo "AGENT_RUNTIME=daytona: skipping local Codex install; remote runtime prerequisites will check loom and codex"
fi

if [[ ! -d "$SLACK_SRC_DIR" ]]; then
    echo "slack-src fixture not found at $SLACK_SRC_DIR" >&2
    echo "Mount scripts/fixtures/slack-src into the container and set SLACK_SRC_DIR." >&2
    exit 1
fi

SEED="$ROOT/slack-src"
REMOTE="$ROOT/slack-src.git"
DEFAULT_BRANCH="loom-slack-tsfirst-target"
if [[ "$AGENT_RUNTIME" == "daytona" ]]; then
    REMOTE="$DAYTONA_REMOTE_REPO_URL"
fi

# Seed the workspace repo from the Slack-clone fixture.
mkdir -p "$SEED"
cp -a "$SLACK_SRC_DIR/." "$SEED/"
rm -rf "$SEED/.git"

cat > "$SEED/Makefile" <<'EOF'
.PHONY: gate
gate:
	test -f index.html
	test -f epic-runner-slack/task-a.txt
	test -f epic-runner-slack/task-b.txt || true
EOF

# Keep the seed's local branch distinct from DEFAULT_BRANCH: loom workspace
# create runs `git worktree add -b $DEFAULT_BRANCH`, which fails if a local
# branch of that name already exists. The target branch lives only on the remote.
git -C "$SEED" init -b main >/dev/null
git -C "$SEED" add .
git -C "$SEED" commit -m "Seed Slack-clone app shell for TSFirst E2E" >/dev/null

git -C "$SEED" remote add origin "$REMOTE"
if [[ "$AGENT_RUNTIME" == "daytona" ]]; then
    if [[ "$DAYTONA_FORCE_PUSH_REMOTE" == "1" ]]; then
        push_seed_remote --force -u origin "HEAD:$DEFAULT_BRANCH" >/dev/null
    else
        push_seed_remote -u origin "HEAD:$DEFAULT_BRANCH" >/dev/null
    fi
else
    git init --bare "$REMOTE" >/dev/null
    push_seed_remote -u origin "HEAD:$DEFAULT_BRANCH" >/dev/null
fi

loom workspace create "$LOOM_WORKSPACE" --repos "$SEED" --path "$WORKSPACE_PATH" --branch "$DEFAULT_BRANCH"
WORKSPACE_CREATED=1
cd "$WORKSPACE_PATH"

if [[ "$AGENT_RUNTIME" == "daytona" ]]; then
    echo "AGENT_RUNTIME=daytona: using external FleetDB at $LOOM_FLEET_DB_URL"
else
    # Start ONE long-lived process that OWNS the embedded fleet-db. Every other loom
    # command in this LOOM_CONFIG_DIR (apply, daemon, run) then reuses this same live
    # fleet-db via runtime.json instead of spinning up its own ephemeral instance, so
    # the daemon and each `loom run` pass share one datastore. Without a stable owner,
    # each `loom run` started + tore down its own fleet-db and the daemon never saw
    # the task runs it dispatched.
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
fi

# NOTE: the daemon is intentionally started later, AFTER `loom apply`.
# `loom daemon` exits immediately if the workspace has no agents configured.

REPO_NAME="$(basename "$SEED")"
if [[ ! -e "$WORKSPACE_PATH/$REPO_NAME/.git" ]]; then
    repo_path="$(find "$WORKSPACE_PATH" -mindepth 1 -maxdepth 1 -type d -exec test -e '{}/.git' ';' -print -quit)"
    if [[ -z "$repo_path" ]]; then
        echo "unable to resolve workspace repo name under $WORKSPACE_PATH" >&2
        exit 1
    fi
    REPO_NAME="$(basename "$repo_path")"
fi

create_issue() {
    loom data --output json create "$@" | jq -r '.id'
}

EPIC_ID="$(create_issue \
    --title "TSFirst Codex epic runner on the Slack-clone app" \
    --type epic \
    --status open)"

DESIGN_A=$'Real Codex E2E task in a Slack-clone web app shell (index.html, src/app.js, src/data.js, src/styles.css).\nDo exactly this:\n1. Inspect index.html and src/app.js to understand the existing Slack app shell.\n2. Create directory epic-runner-slack.\n3. Create epic-runner-slack/task-a.txt with one line containing this pre-assigned task ID and the phrase first slack runner task.\n4. Create epic-runner-slack/order.log with this pre-assigned task ID as the first line.\n5. Run make gate. It is acceptable that task-b.txt does not exist yet; the Makefile allows that for the first task.\n6. Commit, run loom push for your agent branch, close the task, and run loom complete as instructed by the workflow.'
TASK_A="$(create_issue \
    --title "Create first slack runner file" \
    --type task \
    --status open \
    --parent "$EPIC_ID" \
    --source-repo "$REPO_NAME" \
    --design "$DESIGN_A")"

DESIGN_B=$'Real Codex E2E dependent task in the Slack-clone web app shell.\nDo exactly this:\n1. Confirm epic-runner-slack/task-a.txt exists in your checkout before editing.\n2. Create epic-runner-slack/task-b.txt with one line containing this pre-assigned task ID and the phrase second slack runner task.\n3. Append this pre-assigned task ID as the second line of epic-runner-slack/order.log without removing the first line.\n4. Run make gate.\n5. Commit, run loom push for your agent branch, close the task, and run loom complete as instructed by the workflow.'
TASK_B="$(create_issue \
    --title "Create dependent slack runner file" \
    --type task \
    --status open \
    --parent "$EPIC_ID" \
    --source-repo "$REPO_NAME" \
    --depends-on "$TASK_A" \
    --design "$DESIGN_B")"

# --- TypeScript SDK definitions ------------------------------------------------
# Deliberately free of TypeScript type-annotation syntax so the project compiles
# under the e2e image's Node 20 (regex-fallback compile path).
mkdir -p "$WORKSPACE_PATH/.loom/agents" "$WORKSPACE_PATH/.loom/workflows"

# Delimiters are UNquoted so repo/runtime values expand: the applied agent MUST
# declare a repo or the daemon rejects it at startup and exits, leaving no
# supervisor to execute dispatched workers.
if [[ "$AGENT_RUNTIME" == "daytona" ]]; then
APPLY_AGENT="task"
DAYTONA_AUTH_ENV_ENTRIES="'OPENAI_API_KEY'"
DAYTONA_CODEX_AUTH_CONFIG=""
if [[ -n "${CODEX_AUTH_FILE:-}" ]]; then
    DAYTONA_AUTH_ENV_ENTRIES="$DAYTONA_AUTH_ENV_ENTRIES, 'CODEX_AUTH_FILE'"
    DAYTONA_CODEX_AUTH_CONFIG="    codexAuthFileEnv: 'CODEX_AUTH_FILE',"
fi
cat > "$WORKSPACE_PATH/.loom/agents/task.ts" <<TS
import { defineAgent, runtime } from '@loom/runtime';

export default defineAgent({
  name: 'task',
  description: 'Daytona-backed Codex task worker for the TypeScript-first Slack epic runner E2E',
  backend: 'codex',
  repos: ['$REPO_NAME'],
  runtime: runtime.daytona({
    name: 'daytona-slack',
    cwd: '/workspace/project',
    repos: ['$REPO_NAME'],
    env: [$DAYTONA_AUTH_ENV_ENTRIES, '$DAYTONA_GIT_TOKEN_ENV', 'GIT_AUTHOR_NAME', 'GIT_AUTHOR_EMAIL', 'GIT_COMMITTER_NAME', 'GIT_COMMITTER_EMAIL'],
    repoUrl: '$REMOTE',
    branch: '$DEFAULT_BRANCH',
    setupCommands: ['npm install'],
    snapshot: '$DAYTONA_SNAPSHOT',
    target: '$DAYTONA_TARGET',
    apiKeyEnv: 'DAYTONA_API_KEY',
    openaiApiKeyEnv: 'OPENAI_API_KEY',
$DAYTONA_CODEX_AUTH_CONFIG
    gitTokenEnv: '$DAYTONA_GIT_TOKEN_ENV',
    gitUsername: '$DAYTONA_GIT_USERNAME',
    autoStopInterval: 15,
  }),
});
TS
else
APPLY_AGENT="nova"
cat > "$WORKSPACE_PATH/.loom/agents/nova.ts" <<TS
import { defineAgent, runtime } from '@loom/runtime';

export default defineAgent({
  name: 'nova',
  description: 'Codex agent for the TypeScript-first Slack epic runner E2E',
  backend: 'codex',
  repos: ['$REPO_NAME'],
  runtime: runtime.local({ repos: ['$REPO_NAME'] }),
});
TS
fi

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
        metadata: { source: 'tsfirst-slack-epic-runner', epic: parentId },
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
# Register the TS-defined agent/role. This MUST succeed before the daemon starts:
# `loom daemon` exits immediately if the workspace has no agents configured.
if ! loom apply "$APPLY_AGENT" --dir "$WORKSPACE_PATH" >> "$ROOT/runner.log" 2>&1; then
    echo "loom apply $APPLY_AGENT failed" >&2
    tail -n 40 "$ROOT/runner.log" >&2 || true
    exit 1
fi

# Start the supervisor daemon now that an agent exists. It stays alive and owns
# the shared embedded fleet-db that subsequent `loom run` invocations reuse, and
# it executes the start commands that taskRuns.ensure dispatches (spawning Codex
# task workers). Without a live daemon the dispatched workers never run.
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

# --- Assertions (same outcome contract as the octocat tsfirst runner) ----------
loom data --output json show "$TASK_A" | jq -e '.status == "closed"' >/dev/null
loom data --output json show "$TASK_B" | jq -e '.status == "closed"' >/dev/null

git -C "$WORKSPACE_PATH/$REPO_NAME" fetch origin "$DEFAULT_BRANCH" >/dev/null
git -C "$WORKSPACE_PATH/$REPO_NAME" show "origin/$DEFAULT_BRANCH:epic-runner-slack/task-a.txt" | grep -q "$TASK_A"
git -C "$WORKSPACE_PATH/$REPO_NAME" show "origin/$DEFAULT_BRANCH:epic-runner-slack/task-b.txt" | grep -q "$TASK_B"

ORDER="$(git -C "$WORKSPACE_PATH/$REPO_NAME" show "origin/$DEFAULT_BRANCH:epic-runner-slack/order.log")"
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

loom workspace list --json > "$ROOT/workspace-final.json"
loom data --output json list --parent "$EPIC_ID" > "$ROOT/issues-final.json"

echo "PASS tsfirst Codex epic runner Slack-clone E2E"
