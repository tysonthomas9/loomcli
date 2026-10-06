#!/usr/bin/env bash
# Read only an exact queue Lead or child ref inside the runner-owned service.
set -euo pipefail
# shellcheck source=tests/aft/scripts/agent-flows-ownership.sh
source "$(dirname "${BASH_SOURCE[0]}")/agent-flows-ownership.sh"
agent_flows_check_manifest
[[ $# -ge 2 && $# -le 3 && "$1" =~ ^(lead|child)$ && "$2" =~ ^agt_[A-Za-z0-9_-]+$ ]] || exit 2
kind="$1"
agent_id="$2"
if [[ "$kind" == lead ]]; then
  [[ $# -eq 2 ]] || exit 2
else
  [[ $# -eq 3 && "$3" =~ ^[a-f0-9]{40}$ ]] || exit 2
fi
row="$(curl -fsS --max-time 15 "$AFT_API_URL/api/workspaces/LOCALMODE/v1/agents/$agent_id")"
if [[ "$kind" == lead ]]; then
  jq -e --arg id "$agent_id" --arg name "cov-child-queue-lead-$RUN_ID" \
    --arg repo "$AFT_AGENT_FLOW_REPO" \
    '.agent_id == $id and .name == $name and .preset == "lead" and
     .created_by_kind == "user" and .parent_agent_id == null and
     .repo == $repo and .harness == "opencode" and
     (.worktree_path | type == "string" and startswith("/"))' <<< "$row" >/dev/null
else
  parent_id="$(jq -r '.parent_agent_id' <<< "$row")"
  [[ "$parent_id" =~ ^agt_[A-Za-z0-9_-]+$ ]] || exit 2
  parent="$(curl -fsS --max-time 15 "$AFT_API_URL/api/workspaces/LOCALMODE/v1/agents/$parent_id")"
  jq -e --arg id "$agent_id" --arg name "cov-child-queue-task-$RUN_ID" \
    --arg repo "$AFT_AGENT_FLOW_REPO" --arg parent "$parent_id" \
    '.agent_id == $id and .name == $name and .preset == "task" and
     .created_by_kind == "agent" and .created_by_id == $parent and
     .parent_agent_id == $parent and .root_agent_id == $parent and
     .repo == $repo and .harness == "opencode" and
     (.worktree_path | type == "string" and startswith("/"))' <<< "$row" >/dev/null
  jq -e --arg parent "$parent_id" --arg name "cov-child-queue-lead-$RUN_ID" \
    --arg repo "$AFT_AGENT_FLOW_REPO" \
    '.agent_id == $parent and .name == $name and .preset == "lead" and
     .parent_agent_id == null and .repo == $repo and .harness == "opencode"' <<< "$parent" >/dev/null
fi
worktree="$(jq -r '.worktree_path' <<< "$row")"
cd "$AFT_SOURCE_ROOT"
compose=(agent_flows_podman compose -p "$AFT_OWNED_PROJECT" -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.agents.yml -f test/local-mode/docker-compose.agents-real.yml -f "$AFT_WORK_DIR/fleet-override.yml")
container="$("${compose[@]}" ps -q loom-local | tr -d '\r')"
agent_flows_check_container "$container"
"${compose[@]}" exec -T loom-local node -e '
const {spawnSync}=require("node:child_process");
const [path,id,kind,parentHead]=process.argv.slice(1);
const git=(...args)=>{const r=spawnSync("git",["-C",path,...args],{encoding:"utf8",timeout:15000});
  if(r.status!==0) process.exit(3); return r.stdout.trim();};
const branch=git("symbolic-ref","--short","HEAD");
const head=git("rev-parse","HEAD");
if(!/^[a-f0-9]{40}$/.test(head) || !branch) process.exit(3);
const mergeBase=kind==="child"?git("merge-base",parentHead,head):null;
if(kind==="child" && mergeBase!==parentHead) process.exit(3);
process.stdout.write(JSON.stringify({agent_id:id,kind,branch,head,merge_base:mergeBase})+"\n");
' "$worktree" "$agent_id" "$kind" "${3:-}"
