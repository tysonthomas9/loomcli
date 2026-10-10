# shellcheck shell=bash disable=SC2154 # $workspace and $work come from the sourcing driver
# Shared primitives for the loomgit AFT drivers (S14). Source it after `set -e...`;
# the callers define $workspace (and $work for task_id). Only helpers whose
# copies behaved identically live here; each assertion stays in its driver.

# loom <args...>: the e2e loom CLI on this run's config, scoped to $workspace.
loom() { LOOM_CONFIG_DIR="$AFT_LOOM_CONFIG_DIR" "$AFT_LOOM_BIN" "$@" --workspace "$workspace"; }

# json <file> <python> [args...]: run <python> with the parsed file as v (extra args from sys.argv[2]).
json() { python3 -c "import json,sys; v=json.load(open(sys.argv[1])); $2" "$1" "${@:3}"; }

# browser <args...>: this suite's agent-browser session.
browser() { agent-browser --session "${AFT_SESSION:?}" "$@"; }

# task_id <slot>: the task id a driver saved in $work.
task_id() { cat "$work/task-$1.id"; }

# open_issue <issue-id>: open the board, then the task's panel, and wait for its revisions.
open_issue() {
  browser open "$AFT_BASE_URL/ws/$workspace/kanban" > /dev/null
  browser wait '[data-testid="board-toolbar"]' > /dev/null
  browser open "$AFT_BASE_URL/ws/$workspace/issues/$1" > /dev/null
  browser wait '[data-testid="revisions-section"]' > /dev/null
}

# wait_state <name> <status> [reason substring]: poll the caller's approval_state.
wait_state() {
  local got=""
  for _ in $(seq 1 40); do
    got="$(approval_state "$1")"
    if [[ "${got%%|*}" == "$2" && "${got#*|}" == *"${3:-}"* ]]; then return 0; fi
    sleep 2
  done
  echo "task $1: merge approval '$got', want '$2' with '${3:-}'" >&2
  return 1
}
