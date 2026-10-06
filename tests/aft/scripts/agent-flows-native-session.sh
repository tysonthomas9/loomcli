#!/usr/bin/env bash
# Read a noncredential native identity from this run's container only.
set -euo pipefail
# shellcheck source=tests/aft/scripts/agent-flows-ownership.sh
source "$(dirname "${BASH_SOURCE[0]}")/agent-flows-ownership.sh"
agent_flows_check_manifest
[[ $# -eq 1 && "$1" =~ ^[A-Za-z0-9_-]+$ ]] || exit 2
agent_id="$1"
cd "$AFT_SOURCE_ROOT"
compose=(agent_flows_podman compose -p "$AFT_OWNED_PROJECT" -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.agents.yml -f test/local-mode/docker-compose.agents-real.yml -f "$AFT_WORK_DIR/fleet-override.yml")
container="$("${compose[@]}" ps -q loom-local | tr -d '\r')"
agent_flows_check_container "$container"
result="$("${compose[@]}" exec -T loom-local node -e '
try {
const {DatabaseSync}=require("node:sqlite");
const db=new DatabaseSync("/root/.loom/agents.db",{readOnly:true});
const row=db.prepare("SELECT agent_id, name, harness, harness_session_id AS native_id, harness_session_root AS native_root FROM agents WHERE agent_id=? AND workspace_id=?").get(process.argv[1],"LOCALMODE");
const fail=(category)=>{ process.stderr.write("native probe: "+category+"\n"); process.exit(3); };
if (!row) fail("agent-row-missing");
const run=process.argv[2];
const ownedName=new RegExp("^(aft-(child-(a|b)|cancel-child|isolation-(own|foreign))-"+run+
  "|live-recovery-(restart|archive|sibling|dirty)-"+run+")$");
if (typeof row.name!=="string" || !ownedName.test(row.name)) fail("agent-name-not-run-owned");
if (row.harness!=="opencode") fail("harness-mismatch");
if (typeof row.native_id!=="string" || !row.native_id) fail("native-id-missing");
// OpenCode launch has no Root; the durable, valid NativeRoot is an empty string.
if (typeof row.native_root!=="string") fail("native-root-missing");
const owned=db.prepare("SELECT 1 FROM agent_native_sessions WHERE agent_id=? AND harness=? AND native_root=? AND native_id=?")
  .get(row.agent_id,row.harness,row.native_root,row.native_id);
if (!owned) fail("native-owner-mismatch");
delete row.name;
console.log(JSON.stringify(row));
} catch { process.stderr.write("native probe: storage-unavailable\n"); process.exit(3); }
' "$agent_id" "$RUN_ID")"
printf '%s\n' "$result" | tee -a "$AFT_WORK_DIR/native-sessions.jsonl"
