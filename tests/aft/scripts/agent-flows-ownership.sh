#!/usr/bin/env bash
# Sourced by the read-only native probe and the owned-service restart helper.
agent_flows_check_manifest() {
  : "${AFT_OWNED_PROJECT:?runner-owned project required}"
  : "${AFT_SOURCE_ROOT:?runner-owned source required}"
  : "${AFT_WORK_DIR:?runner-owned evidence directory required}"
  : "${AFT_API_URL:?runner-owned API URL required}"
  : "${RUN_ID:?runner-owned run id required}"
  [[ "$RUN_ID" =~ ^af[a-zA-Z0-9]{8}$ ]] || { echo 'invalid runner ID' >&2; return 2; }
  [[ "$AFT_OWNED_PROJECT" == "loom-aft-agents-$RUN_ID" ]] || { echo 'runner project mismatch' >&2; return 2; }
  [[ "$AFT_WORK_DIR" == "/private/tmp/aft-agent-flows.${RUN_ID#af}/evidence" ]] || { echo 'runner evidence path mismatch' >&2; return 2; }
  [[ -f "$AFT_WORK_DIR/manifest.json" && ! -L "$AFT_WORK_DIR/manifest.json" ]] || { echo 'owned manifest missing' >&2; return 2; }
  local head
  head="$(git -C "$AFT_SOURCE_ROOT" rev-parse HEAD)" || return 2
  jq -e --arg run "$RUN_ID" --arg project "$AFT_OWNED_PROJECT" \
    --arg source "$AFT_SOURCE_ROOT" --arg head "$head" --arg api "$AFT_API_URL" \
    '.run_id == $run and .source_root == $source and .source_head == $head and
     .backend == "opencode" and .owned.compose_project == $project and
     .owned.api_url == $api and .owned.api_url == ("http://127.0.0.1:" + (.owned.ports[1] | tostring))' \
    "$AFT_WORK_DIR/manifest.json" >/dev/null || { echo 'owned manifest mismatch' >&2; return 2; }
}

agent_flows_check_container() {
  local container="$1"
  [[ -n "$container" ]] || { echo 'owned loom-local is absent' >&2; return 1; }
  podman inspect "$container" | jq -e --arg project "$AFT_OWNED_PROJECT" '
    .[0].Config.Labels as $labels |
    $labels["com.docker.compose.project"] == $project and
    $labels["com.docker.compose.service"] == "loom-local"' >/dev/null \
    || { echo 'compose project or service ownership mismatch' >&2; return 1; }
}
