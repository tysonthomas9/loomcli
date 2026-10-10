#!/usr/bin/env bash
# Read one exact run-owned Agent API worktree ref inside the owned service.
set -euo pipefail
# shellcheck source=tests/aft/scripts/agent-flows-ownership.sh
source "$(dirname "${BASH_SOURCE[0]}")/agent-flows-ownership.sh"
agent_flows_check_manifest
[[ $# -ge 2 && $# -le 3 && "$1" =~ ^agt_[A-Za-z0-9_-]+$ ]] || exit 2
agent_id="$1"
name="$2"
parent_head="${3:-}"
[[ -z "$parent_head" || "$parent_head" =~ ^[a-f0-9]{40}$ ]] || exit 2
case "$name" in
  "cov-child-repeat-lead-$RUN_ID"|"cov-child-pair-lead-$RUN_ID"|"cov-child-sidebar-a-$RUN_ID"|"cov-child-sidebar-b-$RUN_ID")
    preset=lead; parent_name=; [[ -z "$parent_head" ]] || exit 2 ;;
  "cov-child-repeat-$RUN_ID") preset=task; parent_name="cov-child-repeat-lead-$RUN_ID" ;;
  "cov-child-pair-a-$RUN_ID"|"cov-child-pair-b-$RUN_ID") preset=task; parent_name="cov-child-pair-lead-$RUN_ID" ;;
  "cov-child-sidebar-task-$RUN_ID") preset=task; parent_name="cov-child-sidebar-b-$RUN_ID" ;;
  *) exit 2 ;;
esac
row="$(curl -fsS --max-time 15 "$AFT_API_URL/api/workspaces/LOCALMODE/v1/agents/$agent_id")"
jq -e --arg id "$agent_id" --arg name "$name" --arg preset "$preset" \
  --arg repo "$AFT_AGENT_FLOW_REPO" \
  '.agent_id == $id and .name == $name and .preset == $preset and
   .repo == $repo and .harness == "opencode" and
   (.worktree_path | type == "string" and startswith("/"))' <<< "$row" >/dev/null
if [[ "$preset" == task ]]; then
  parent_id="$(jq -r '.parent_agent_id' <<< "$row")"
  [[ "$parent_id" =~ ^agt_[A-Za-z0-9_-]+$ ]] || exit 2
  jq -e --arg parent "$parent_id" \
    '.created_by_kind == "agent" and .created_by_id == $parent and
     .parent_agent_id == $parent and .root_agent_id == $parent' <<< "$row" >/dev/null
  parent="$(curl -fsS --max-time 15 "$AFT_API_URL/api/workspaces/LOCALMODE/v1/agents/$parent_id")"
  jq -e --arg id "$parent_id" --arg name "$parent_name" --arg repo "$AFT_AGENT_FLOW_REPO" \
    '.agent_id == $id and .name == $name and .preset == "lead" and
     .repo == $repo and .harness == "opencode" and .parent_agent_id == null' <<< "$parent" >/dev/null
else
  jq -e '.parent_agent_id == null' <<< "$row" >/dev/null
fi
worktree="$(jq -r '.worktree_path' <<< "$row")"
cd "$AFT_SOURCE_ROOT"
compose=(agent_flows_podman compose -p "$AFT_OWNED_PROJECT" -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.agents.yml -f test/local-mode/docker-compose.agents-real.yml -f "$AFT_WORK_DIR/fleet-override.yml")
container="$("${compose[@]}" ps -q loom-local | tr -d '\r')"
agent_flows_check_container "$container"
"${compose[@]}" exec -T loom-local node -e '
const {spawnSync}=require("node:child_process");
const [path,id,parentHead]=process.argv.slice(1);
const git=(...args)=>{const r=spawnSync("git",["-C",path,...args],{encoding:"utf8",timeout:15000});
  if(r.status!==0) process.exit(3); return r.stdout.trim();};
const branch=git("symbolic-ref","--short","HEAD");
const head=git("rev-parse","HEAD");
if(!branch || !/^[a-f0-9]{40}$/.test(head)) process.exit(3);
const mergeBase=parentHead?git("merge-base",parentHead,head):null;
if(parentHead && mergeBase!==parentHead) process.exit(3);
const changed=git("show","--format=","--name-only","HEAD").split("\n").filter(Boolean);
process.stdout.write(JSON.stringify({agent_id:id,branch,head,merge_base:mergeBase,changed})+"\n");
' "$worktree" "$agent_id" "$parent_head"
