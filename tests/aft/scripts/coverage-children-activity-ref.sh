#!/usr/bin/env bash
# Read only the ref of one exact run-owned task child in the runner-owned container.
set -euo pipefail
# shellcheck source=tests/aft/scripts/agent-flows-ownership.sh
source "$(dirname "${BASH_SOURCE[0]}")/agent-flows-ownership.sh"
agent_flows_check_manifest
[[ $# -eq 1 && "$1" =~ ^agt_[A-Za-z0-9]+$ ]] || exit 2
child_id="$1"
row="$(curl -fsS --max-time 15 "$AFT_API_URL/api/workspaces/LOCALMODE/v1/agents/$child_id")"
jq -e --arg id "$child_id" --arg name "cov-child-repeat-$RUN_ID" \
  --arg repo "$AFT_AGENT_FLOW_REPO" \
  '.agent_id == $id and .name == $name and .preset == "task" and
   .created_by_kind == "agent" and .repo == $repo and .harness == "opencode" and
   .root_agent_id == .parent_agent_id and .created_by_id == .parent_agent_id and
   (.parent_agent_id | type == "string" and startswith("agt_")) and
   (.worktree_path | type == "string" and startswith("/"))' <<< "$row" >/dev/null
parent_id="$(jq -r '.parent_agent_id' <<< "$row")"
parent="$(curl -fsS --max-time 15 "$AFT_API_URL/api/workspaces/LOCALMODE/v1/agents/$parent_id")"
jq -e --arg id "$parent_id" --arg name "cov-child-repeat-lead-$RUN_ID" \
  --arg repo "$AFT_AGENT_FLOW_REPO" \
  '.agent_id == $id and .name == $name and .preset == "lead" and
   .repo == $repo and .harness == "opencode" and .parent_agent_id == null' <<< "$parent" >/dev/null
worktree="$(jq -r '.worktree_path' <<< "$row")"
cd "$AFT_SOURCE_ROOT"
compose=(agent_flows_podman compose -p "$AFT_OWNED_PROJECT" -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.agents.yml -f test/local-mode/docker-compose.agents-real.yml -f "$AFT_WORK_DIR/fleet-override.yml")
container="$("${compose[@]}" ps -q loom-local | tr -d '\r')"
agent_flows_check_container "$container"
"${compose[@]}" exec -T loom-local node -e '
const {spawnSync}=require("node:child_process");
const [path,id,run]=process.argv.slice(1);
const exec=(...args)=>{const r=spawnSync("git",["-C",path,...args],{encoding:"utf8",timeout:15000});
  if(r.status!==0) process.exit(3); return r.stdout.trim();};
const branch=exec("symbolic-ref","--short","HEAD");
const head=exec("rev-parse","HEAD");
const changed=exec("show","--format=","--name-only","HEAD").split("\n").filter(Boolean);
if(branch!=="cov-child-switched-"+run || !/^[a-f0-9]{40}$/.test(head)) process.exit(3);
process.stdout.write(JSON.stringify({agent_id:id,branch,head,changed})+"\n");
' "$worktree" "$child_id" "$RUN_ID"
