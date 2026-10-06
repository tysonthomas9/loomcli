#!/usr/bin/env bash
# Read a noncredential native identity from this run's container only.
set -euo pipefail
# shellcheck source=tests/aft/scripts/agent-flows-ownership.sh
source "$(dirname "${BASH_SOURCE[0]}")/agent-flows-ownership.sh"
agent_flows_check_manifest
[[ $# -eq 1 && "$1" =~ ^[A-Za-z0-9_-]+$ ]] || exit 2
agent_id="$1"
cd "$AFT_SOURCE_ROOT"
compose=(podman compose -p "$AFT_OWNED_PROJECT" -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.agents.yml -f test/local-mode/docker-compose.agents-real.yml -f "$AFT_WORK_DIR/fleet-override.yml")
container="$("${compose[@]}" ps -q loom-local | tr -d '\r')"
agent_flows_check_container "$container"
result="$("${compose[@]}" exec -T loom-local node -e '
const {DatabaseSync}=require("node:sqlite");
const db=new DatabaseSync("/root/.loom/agents.db",{readOnly:true});
const row=db.prepare("SELECT agent_id, name, harness, harness_session_id AS native_id, harness_session_root AS native_root FROM agents WHERE agent_id=? AND workspace_id=?").get(process.argv[1],"LOCALMODE");
if (!row || !row.name.includes(process.argv[2]) || !row.native_id || !row.native_root) process.exit(3);
delete row.name;
console.log(JSON.stringify(row));
' "$agent_id" "$RUN_ID")"
printf '%s\n' "$result" | tee -a "$AFT_WORK_DIR/native-sessions.jsonl"
