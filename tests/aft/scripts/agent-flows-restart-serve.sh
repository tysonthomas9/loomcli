#!/usr/bin/env bash
# Recreate only the owned loom-local service; its volume and OpenCode process
# are part of that service, so the evidence describes a serve+OpenCode restart.
set -euo pipefail
# shellcheck source=tests/aft/scripts/agent-flows-ownership.sh
source "$(dirname "${BASH_SOURCE[0]}")/agent-flows-ownership.sh"
agent_flows_check_manifest
cd "$AFT_SOURCE_ROOT"
compose=(podman compose -p "$AFT_OWNED_PROJECT" -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.agents.yml -f test/local-mode/docker-compose.agents-real.yml -f "$AFT_WORK_DIR/fleet-override.yml")
container="$("${compose[@]}" ps -q loom-local)"
agent_flows_check_container "$container"
old_pid="$(podman inspect --format '{{.State.Pid}}' "$container")"
old_started="$(podman inspect --format '{{.State.StartedAt}}' "$container")"
[[ "$old_pid" =~ ^[1-9][0-9]*$ ]] || { echo 'loom-local was not running' >&2; exit 1; }
[[ -n "$old_started" ]] || { echo 'loom-local start identity missing' >&2; exit 1; }
printf 'time=%s project=%s old_container=%s old_init_pid=%s old_started=%s scope=loom-local-plus-OpenCode\n' "$(date -u +%FT%TZ)" "$AFT_OWNED_PROJECT" "$container" "$old_pid" "$old_started" >> "$AFT_WORK_DIR/restarts.log"
podman top "$container" pid comm >> "$AFT_WORK_DIR/restarts.log"
"${compose[@]}" restart loom-local
deadline=$((SECONDS + 180))
until curl -fsS --max-time 3 "$AFT_API_URL/api/config" >/dev/null 2>&1; do
  (( SECONDS < deadline )) || { echo 'loom-local did not become ready' >&2; exit 1; }
  sleep 1
done
new_container="$("${compose[@]}" ps -q loom-local)"
agent_flows_check_container "$new_container"
new_pid="$(podman inspect --format '{{.State.Pid}}' "$new_container")"
new_started="$(podman inspect --format '{{.State.StartedAt}}' "$new_container")"
[[ "$new_pid" =~ ^[1-9][0-9]*$ && "$new_pid" != "$old_pid" && -n "$new_started" && "$new_started" != "$old_started" ]] || { echo 'loom-local process/start identity did not change' >&2; exit 1; }
printf 'ready=%s new_container=%s new_init_pid=%s new_started=%s\n' "$(date -u +%FT%TZ)" "$new_container" "$new_pid" "$new_started" >> "$AFT_WORK_DIR/restarts.log"
podman top "$new_container" pid comm >> "$AFT_WORK_DIR/restarts.log"
