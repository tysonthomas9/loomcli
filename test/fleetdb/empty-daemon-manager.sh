#!/bin/sh
set -eu

log() {
  printf '%s\n' "[loom-empty-daemon-manager] $*"
}

daemon_log_dir="${LOOM_DAEMON_LOG_DIR:-/loom-config/daemon-logs}"
scan_interval="${LOOM_DAEMON_SCAN_INTERVAL:-2}"

workspace_daemon_pid() {
  ws_path="$1"
  pid_file="${ws_path}/.loom/daemon.pid"
  if [ ! -f "$pid_file" ]; then
    return 1
  fi

  pid="$(tr -dc '0-9' < "$pid_file" || true)"
  if [ "$pid" = "" ]; then
    return 1
  fi
  if kill -0 "$pid" >/dev/null 2>&1; then
    printf '%s\n' "$pid"
    return 0
  fi
  return 1
}

workspace_has_runnable_agents() {
  ws_key="$1"
  status="$(
    LOOM_WORKSPACE="$ws_key" \
    LOOM_WORKSPACE_ID="$ws_key" \
      loom workspace ops status "$ws_key" --json 2>/tmp/loom-empty-daemon-status.err || true
  )"
  if [ "$status" = "" ]; then
    return 1
  fi
  printf '%s' "$status" | jq -e 'any(.agents[]?; .runnable == true)' >/dev/null 2>&1
}

start_workspace_daemon() {
  ws_key="$1"
  ws_path="$2"
  mkdir -p "${ws_path}/.loom" "$daemon_log_dir"

  log_file="${daemon_log_dir}/${ws_key}.log"
  log "starting daemon for workspace ${ws_key} (${ws_path})"
  (
    cd "$ws_path"
    export LOOM_CONFIG_DIR="${LOOM_CONFIG_DIR:-/loom-config}"
    export LOOM_WORKSPACE="$ws_key"
    export LOOM_WORKSPACE_ID="$ws_key"
    export LOOM_BACKEND="${LOOM_BACKEND:-codex}"
    export LOOM_ISSUE_BACKEND=fleetdb
    export LOOM_FLEET_DB_URL="${LOOM_FLEET_DB_URL:-http://fleet-db:8080}"
    export LOOM_FLEET_DB_ACTOR="${LOOM_FLEET_DB_ACTOR:-local-user}"
    export LOOM_FLEET_REQUIRED="${LOOM_FLEET_REQUIRED:-1}"
    exec loom daemon
  ) >>"$log_file" 2>&1 &
}

reconcile_workspace_daemons() {
  workspace_json="$(loom workspace list --json 2>/tmp/loom-empty-daemon-workspaces.err || true)"
  if [ "$workspace_json" = "" ]; then
    return 0
  fi

  printf '%s' "$workspace_json" |
    jq -r '.[] | select((.path // "") != "") | [(.id // .key // .name), .path] | @tsv' |
    while IFS="$(printf '\t')" read -r ws_key ws_path; do
      if [ "$ws_key" = "" ] || [ "$ws_path" = "" ]; then
        continue
      fi
      if ! workspace_has_runnable_agents "$ws_key"; then
        continue
      fi
      if workspace_daemon_pid "$ws_path" >/dev/null 2>&1; then
        continue
      fi
      start_workspace_daemon "$ws_key" "$ws_path"
    done
}

if [ "${1:-}" = "once" ]; then
  reconcile_workspace_daemons
  exit 0
fi

log "watching fleet-db workspaces for runnable agents every ${scan_interval}s"
while true; do
  reconcile_workspace_daemons || true
  sleep "$scan_interval" &
  wait "$!" || exit 0
done
