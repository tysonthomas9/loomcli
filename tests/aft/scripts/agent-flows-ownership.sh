#!/usr/bin/env bash
# Sourced by the read-only native probe and the owned-service restart helper.
agent_flows_check_manifest() {
  : "${AFT_OWNED_PROJECT:?runner-owned project required}"
  : "${AFT_SOURCE_ROOT:?runner-owned source required}"
  : "${AFT_WORK_DIR:?runner-owned evidence directory required}"
  : "${AFT_API_URL:?runner-owned API URL required}"
  : "${AFT_PODMAN_HOME:?runner-approved host Podman HOME required}"
  : "${AFT_PODMAN_CONNECTION:?runner-pinned Podman connection required}"
  : "${RUN_ID:?runner-owned run id required}"
  [[ "$AFT_PODMAN_HOME" == /* && -d "$AFT_PODMAN_HOME" && ! -L "$AFT_PODMAN_HOME" &&
     "$AFT_PODMAN_CONNECTION" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] \
    || { echo 'invalid approved host Podman configuration' >&2; return 2; }
  [[ "$RUN_ID" =~ ^af[a-z0-9]{8}$ ]] || { echo 'invalid runner ID' >&2; return 2; }
  [[ "$AFT_OWNED_PROJECT" == "loom-aft-agents-$RUN_ID" ]] || { echo 'runner project mismatch' >&2; return 2; }
  [[ "$AFT_WORK_DIR" =~ ^/private/tmp/aft-agent-flows\.([A-Za-z0-9]{8})/evidence$ ]] || { echo 'runner evidence path mismatch' >&2; return 2; }
  local suffix="${BASH_REMATCH[1]}"
  [[ "$RUN_ID" == "af$(printf '%s' "$suffix" | tr '[:upper:]' '[:lower:]')" ]] || { echo 'runner evidence ID mismatch' >&2; return 2; }
  [[ -f "$AFT_WORK_DIR/manifest.json" && ! -L "$AFT_WORK_DIR/manifest.json" ]] || { echo 'owned manifest missing' >&2; return 2; }
  local head
  head="$(git -C "$AFT_SOURCE_ROOT" rev-parse HEAD | tr -d '\r')" || return 2
  jq -e --arg run "$RUN_ID" --arg project "$AFT_OWNED_PROJECT" \
    --arg source "$AFT_SOURCE_ROOT" --arg head "$head" --arg api "$AFT_API_URL" --arg evidence "$AFT_WORK_DIR" \
    --arg podmanHome "$AFT_PODMAN_HOME" --arg podmanConnection "$AFT_PODMAN_CONNECTION" \
    '.run_id == $run and .source_root == $source and .source_head == $head and
     .backend == "opencode" and .owned.compose_project == $project and .owned.evidence_dir == $evidence and
     .owned.api_url == $api and .owned.api_url == ("http://127.0.0.1:" + (.owned.ports[1] | tostring)) and
     .owned.podman_home == $podmanHome and .owned.podman_connection == $podmanConnection and
     (.owned.podman_connection_fingerprint | test("^[a-f0-9]{64}$"))' \
    "$AFT_WORK_DIR/manifest.json" >/dev/null || { echo 'owned manifest mismatch' >&2; return 2; }
  local connections record fingerprint
  connections="$(HOME="$AFT_PODMAN_HOME" env -u CONTAINER_HOST -u DOCKER_HOST -u CONTAINER_CONNECTION \
    podman system connection list --format json 2>/dev/null)" \
    || { echo 'pinned host Podman connection registry unavailable' >&2; return 2; }
  record="$(jq -cS --arg name "$AFT_PODMAN_CONNECTION" \
    '[.[] | select(.Name == $name) | {Name,URI,Identity}] | if length == 1 then .[0] else error("connection missing") end' \
    <<< "$connections" 2>/dev/null)" \
    || { echo 'pinned host Podman connection missing' >&2; return 2; }
  fingerprint="$(printf '%s' "$record" | shasum -a 256 | awk '{print $1}')" || return 2
  jq -e --arg fingerprint "$fingerprint" '.owned.podman_connection_fingerprint == $fingerprint' \
    "$AFT_WORK_DIR/manifest.json" >/dev/null \
    || { echo 'pinned host Podman connection changed' >&2; return 2; }
}

agent_flows_podman() {
  local error_file status reason
  error_file="$(mktemp "$AFT_WORK_DIR/.podman-helper.XXXXXXXX")" || return 2
  status=0
  HOME="$AFT_PODMAN_HOME" CONTAINER_CONNECTION="$AFT_PODMAN_CONNECTION" \
    env -u CONTAINER_HOST -u DOCKER_HOST podman --connection "$AFT_PODMAN_CONNECTION" "$@" 2>"$error_file" \
    || status=$?
  if ((status)); then
    reason=command-failed
    if rg -qi 'cannot connect to podman|unable to connect|connection refused|no such connection' "$error_file"; then
      reason=connection-unavailable
    fi
    printf 'owned Podman %s failed on pinned connection %s (exit %d, %s)\n' \
      "${1:-command}" "$AFT_PODMAN_CONNECTION" "$status" "$reason" >&2
  fi
  rm -f "$error_file"
  return "$status"
}

agent_flows_check_container() {
  local container="$1"
  [[ -n "$container" ]] || { echo 'owned loom-local is absent' >&2; return 1; }
  agent_flows_podman inspect "$container" | jq -e --arg project "$AFT_OWNED_PROJECT" '
    .[0].Config.Labels as $labels |
    $labels["com.docker.compose.project"] == $project and
    $labels["com.docker.compose.service"] == "loom-local"' >/dev/null \
    || { echo 'compose project or service ownership mismatch' >&2; return 1; }
}
