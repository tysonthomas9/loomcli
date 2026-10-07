#!/usr/bin/env bash
# Optional, read-only, redacted failure evidence for an exact owned task child.
set -euo pipefail
# shellcheck source=tests/aft/scripts/agent-flows-ownership.sh
source "$(dirname "${BASH_SOURCE[0]}")/agent-flows-ownership.sh"
agent_flows_check_manifest
[[ $# -eq 6 && "$1" =~ ^agt_[A-Za-z0-9]+$ && "$2" =~ ^agt_[A-Za-z0-9]+$ ]] || exit 2
agent_id="$1"
parent_id="$2"
event_id="$3"
event_seq="$4"
turn_id="$5"
error_sha="$6"
if [[ -n "$error_sha" ]]; then
  [[ "$event_id" == agent.turn_completed:* && "$event_seq" =~ ^[0-9]+$ &&
     "$turn_id" =~ ^[A-Za-z0-9_-]+$ && "$error_sha" =~ ^[a-f0-9]{64}$ ]] || exit 2
else
  [[ -z "$event_id$event_seq$turn_id" ]] || exit 2
fi
: "${AFT_NATIVE_SESSION_PROBE:?runner-owned native identity probe required}"
[[ "$AFT_NATIVE_SESSION_PROBE" == "$AFT_TESTS_DIR/scripts/agent-flows-native-session.sh" ]] || exit 2
native_ref="$("$AFT_NATIVE_SESSION_PROBE" "$agent_id")" || exit 1
native_id="$(jq -er --arg id "$agent_id" 'select(.agent_id == $id and .harness == "opencode" and
  (.native_id | type == "string" and length > 0) and (.native_root | type == "string")) | .native_id' <<< "$native_ref")" || exit 1
native_root="$(jq -er '.native_root' <<< "$native_ref")" || exit 1
if [[ -n "$error_sha" ]]; then
  # loomagent.nativeRow keys a saved turn end by root, native session, and TurnID.
  [[ "$event_id" == "agent.turn_completed:${native_root}:${native_id}:${turn_id}" ]] || exit 2
fi
cd "$AFT_SOURCE_ROOT"
compose=(agent_flows_podman compose -p "$AFT_OWNED_PROJECT" -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.agents.yml -f test/local-mode/docker-compose.agents-real.yml -f "$AFT_WORK_DIR/fleet-override.yml")
container="$("${compose[@]}" ps -q loom-local | tr -d '\r')"
agent_flows_check_container "$container"
"${compose[@]}" exec -T loom-local node -e "$(cat "$AFT_TESTS_DIR/scripts/agent-flows-native-failure.cjs")" \
  "$agent_id" "$parent_id" "$native_id" "$native_root" "$AFT_AGENT_FLOW_REPO" \
  "$RUN_ID" "$event_id" "$event_seq" "$turn_id" "$error_sha"
