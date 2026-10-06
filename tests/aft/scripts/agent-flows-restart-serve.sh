#!/usr/bin/env bash
# Recreate only the owned loom-local service; its volume and OpenCode process
# are part of that service, so the evidence describes a serve+OpenCode restart.
set -euo pipefail
: "${AFT_OWNED_PROJECT:?runner-owned project required}"
: "${AFT_SOURCE_ROOT:?runner-owned source required}"
: "${AFT_WORK_DIR:?runner-owned evidence directory required}"
: "${AFT_API_URL:?runner-owned API URL required}"
[[ "$AFT_OWNED_PROJECT" == loom-aft-agents-* ]] || exit 2
cd "$AFT_SOURCE_ROOT"
compose=(podman compose -p "$AFT_OWNED_PROJECT" -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.agents.yml -f test/local-mode/docker-compose.agents-real.yml -f "$AFT_WORK_DIR/fleet-override.yml")
container="$("${compose[@]}" ps -q loom-local)"
[[ -n "$container" ]] || { echo 'owned loom-local is absent' >&2; exit 1; }
project="$(podman inspect --format '{{ index .Config.Labels "com.docker.compose.project" }}' "$container")"
[[ "$project" == "$AFT_OWNED_PROJECT" ]] || { echo 'compose ownership mismatch' >&2; exit 1; }
old_pid="$(podman inspect --format '{{.State.Pid}}' "$container")"
[[ "$old_pid" =~ ^[1-9][0-9]*$ ]] || { echo 'loom-local was not running' >&2; exit 1; }
printf 'time=%s project=%s old_container=%s old_init_pid=%s scope=loom-local-plus-OpenCode\n' "$(date -u +%FT%TZ)" "$AFT_OWNED_PROJECT" "$container" "$old_pid" >> "$AFT_WORK_DIR/restarts.log"
podman top "$container" pid comm >> "$AFT_WORK_DIR/restarts.log"
"${compose[@]}" restart loom-local
deadline=$((SECONDS + 180))
until curl -fsS --max-time 3 "$AFT_API_URL/api/config" >/dev/null 2>&1; do
  (( SECONDS < deadline )) || { echo 'loom-local did not become ready' >&2; exit 1; }
  sleep 1
done
new_container="$("${compose[@]}" ps -q loom-local)"
new_pid="$(podman inspect --format '{{.State.Pid}}' "$new_container")"
[[ "$new_pid" =~ ^[1-9][0-9]*$ && "$new_pid" != "$old_pid" ]] || { echo 'loom-local process identity did not change' >&2; exit 1; }
printf 'ready=%s new_container=%s new_init_pid=%s\n' "$(date -u +%FT%TZ)" "$new_container" "$new_pid" >> "$AFT_WORK_DIR/restarts.log"
podman top "$new_container" pid comm >> "$AFT_WORK_DIR/restarts.log"
