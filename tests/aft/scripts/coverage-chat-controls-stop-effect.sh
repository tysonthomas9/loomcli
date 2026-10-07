#!/usr/bin/env bash
# Refuse an executed proposed shell effect inside this run's owned container.
set -euo pipefail
# shellcheck source=tests/aft/scripts/agent-flows-ownership.sh
source "$(dirname "${BASH_SOURCE[0]}")/agent-flows-ownership.sh"
agent_flows_check_manifest
[[ $# -eq 0 ]] || exit 2
id="$(tr -d '\r\n' < "$AFT_WORK_DIR/chat-controls/stop.id")"
[[ "$id" =~ ^agt_[A-Za-z0-9]+$ ]] || exit 2
agent="$(curl -fsS --max-time 15 "$AFT_API_URL/api/workspaces/LOCALMODE/v1/agents/$id")" \
  || { echo 'owned Stop agent unavailable' >&2; exit 1; }
jq -e --arg id "$id" --arg name "cov-controls-stop-$RUN_ID" --arg repo "$AFT_AGENT_FLOW_REPO" \
  '.agent_id == $id and .name == $name and .repo == $repo and
   .harness == "opencode" and .preset == "pr-review-interactive" and
   .created_by_kind == "user" and .parent_agent_id == null' <<< "$agent" >/dev/null \
  || { echo 'foreign Stop actor' >&2; exit 1; }
cd "$AFT_SOURCE_ROOT"
compose=(agent_flows_podman compose -p "$AFT_OWNED_PROJECT" -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.agents.yml -f test/local-mode/docker-compose.agents-real.yml -f "$AFT_WORK_DIR/fleet-override.yml")
container="$("${compose[@]}" ps -q loom-local | tr -d '\r')"
agent_flows_check_container "$container"
marker="/tmp/cov-controls-stop-effect-$RUN_ID"
"${compose[@]}" exec -T loom-local test ! -e "$marker" \
  || { echo 'proposed native shell marker exists' >&2; exit 1; }
printf 'proposed native shell marker absent for %s\n' "$id"
