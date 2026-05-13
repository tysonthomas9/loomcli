#!/bin/sh
# Playground harness setup.
#
# Usage: setup.sh [<scenario>]
#
# No arg                — original happy-path workspace: planner+coder agents
#                         plus 3 seed tasks. Used by smoke_test.sh and
#                         playground_test.go.
# <scenario>            — isolated workspace `playground-<scenario>` wired to
#                         loom-backend-playground-<scenario>, single agent
#                         filtering on has_design, no seed tasks. Used by
#                         run_scenario.sh + scenarios/ for daemon-lifecycle
#                         regression tests.
#
# Optional env knobs (scenarios use these; the no-arg path ignores them):
#   LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS — written into .runtime-<scenario>/env
#     so the scenario daemon's watchdog trips quickly. Read by
#     Supervisor.GetOutputTimeout (internal/cli/daemon/supervisor/restart.go).
set -eu

HERE="$(cd "$(dirname "$0")" && pwd)"
SCENARIO="${1:-}"

if [ -n "$SCENARIO" ]; then
  SUFFIX="-$SCENARIO"
  WORKSPACE_NAME="playground-$SCENARIO"
  WORKSPACE_KEY="$(printf 'PLAYGROUND-%s' "$SCENARIO" | tr '[:lower:]' '[:upper:]')"
else
  SUFFIX=""
  WORKSPACE_NAME="playground"
  WORKSPACE_KEY="PLAYGROUND"
fi

RUNTIME="$HERE/.runtime$SUFFIX"
REPO="$RUNTIME/repo"
BIN="$RUNTIME/bin"

# Materialize the runtime: link backends, seed repo, write env file.
materialize_runtime() {
  mkdir -p "$BIN"
  ln -sf "$HERE/loom-backend-playground" "$BIN/loom-backend-playground"
  if [ -n "$SCENARIO" ] && [ -x "$HERE/loom-backend-playground-$SCENARIO" ]; then
    ln -sf "$HERE/loom-backend-playground-$SCENARIO" "$BIN/loom-backend-playground-$SCENARIO"
  fi

  if [ ! -d "$REPO/.git" ]; then
    rm -rf "$REPO"
    mkdir -p "$REPO"
    cp -R "$HERE/repo-template/." "$REPO/"
    git -C "$REPO" init -q -b main
    git -C "$REPO" add .
    git -C "$REPO" -c user.email=playground@loom.local -c user.name=Playground \
      commit -q -m "Initial $WORKSPACE_NAME commit"
  fi

  {
    printf 'export PATH="%s:$PATH"\n' "$BIN"
    printf 'export LOOM_WORKSPACE=%s\n' "$WORKSPACE_KEY"
    if [ -n "${LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS:-}" ]; then
      printf 'export LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS=%s\n' "$LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS"
    fi
  } > "$RUNTIME/env"
}
materialize_runtime
export PATH="$BIN:$PATH"

# Try to create the workspace. If a previous run left orphan fleet-db keys
# (e.g. interrupted setup, kill -9'd daemon), `loom workspace create` fails
# with HTTP 409 on role seeding. Recover by running teardown.sh (same
# scenario arg) and retrying once.
if ! loom workspace create "$WORKSPACE_NAME" --repos "$REPO" 2>"$RUNTIME/create.err"; then
  if grep -q "already exists" "$RUNTIME/create.err"; then
    echo "[setup$SUFFIX] orphan fleet-db state detected; running teardown then retrying..." >&2
    "$HERE/teardown.sh" "$SCENARIO" >/dev/null 2>&1 || true
    # teardown nukes .runtime$SUFFIX/, so re-materialize before the retry.
    materialize_runtime
    loom workspace create "$WORKSPACE_NAME" --repos "$REPO"
  else
    cat "$RUNTIME/create.err" >&2
    exit 1
  fi
fi
rm -f "$RUNTIME/create.err"
export LOOM_WORKSPACE="$WORKSPACE_KEY"
loom workspace use "$WORKSPACE_KEY"

if [ -z "$SCENARIO" ]; then
  loom agentdef add playground-planner --role plan --backend playground --repos "$(basename "$REPO")" --auto --task-filter needs_plan
  loom agentdef add playground-coder   --role task --backend playground --repos "$(basename "$REPO")" --auto --task-filter has_design
  loom data create --title "Seed task 1 (playground)" --type task --priority 2
  loom data create --title "Seed task 2 (playground)" --type task --priority 2
  loom data create --title "Seed task 3 (playground)" --type task --priority 3
else
  # Agent name must differ from the workspace name. loom seeds the primary
  # repo checkout on a branch named after the workspace, and each agent
  # gets a worktree on a branch named after the agent. Using the same name
  # for both collides with "already checked out".
  loom agentdef add "${SCENARIO}-worker" --role task --backend "playground-$SCENARIO" \
    --repos "$(basename "$REPO")" --auto --task-filter has_design
fi

if [ -z "$SCENARIO" ]; then
  cat <<EOF

Playground ready. Next steps:

  # 1. In a new terminal, source the env so loom finds the workspace + harness
  source $RUNTIME/env

  # 2. Start the agent supervisor (foreground; Ctrl+C to stop)
  loom daemon

  # 3. In a third terminal (after sourcing env too), watch progress
  loom monitor              # or:  loom data list

After ~30s the coder closes the seeded tasks. Inspect:
  git -C $REPO log --oneline
  cat $REPO/playground.txt

To tear down: $HERE/teardown.sh
EOF
else
  cat <<EOF

playground-$SCENARIO workspace ready.

Drive a scenario by hand:
  source $RUNTIME/env
  loom data create --title "Probe" --type task --priority 2 \\
    --status open --design "Scenario probe"
  loom daemon               # foreground; Ctrl+C to stop

Or use the wrapper: $HERE/run_scenario.sh <scenario-name>
Teardown: $HERE/teardown.sh $SCENARIO
EOF
fi
