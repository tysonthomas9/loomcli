#!/usr/bin/env bash
# Signal only the exact OpenCode service inside this run's checked loom-local.
set -euo pipefail
# shellcheck source=tests/aft/scripts/agent-flows-ownership.sh
source "$(dirname "${BASH_SOURCE[0]}")/agent-flows-ownership.sh"
agent_flows_check_manifest
[[ $# -eq 2 && "$1" =~ ^(probe|restart)$ && "$2" =~ ^agt_[A-Za-z0-9_-]+$ ]] || exit 2
action="$1" agent_id="$2"
cd "$AFT_SOURCE_ROOT"
compose=(agent_flows_podman compose -p "$AFT_OWNED_PROJECT" -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.agents.yml -f test/local-mode/docker-compose.agents-real.yml -f "$AFT_WORK_DIR/fleet-override.yml")
container="$("${compose[@]}" ps -q loom-local | tr -d '\r')"
agent_flows_check_container "$container"
# shellcheck disable=SC2016 # JavaScript template literals are passed verbatim to node.
result="$(agent_flows_podman exec -T "$container" node -e '
const fs=require("node:fs");
const {DatabaseSync}=require("node:sqlite");
const [id,run,action]=process.argv.slice(1);
const fail=(code)=>{process.stderr.write("native restart: "+code+"\n");process.exit(3)};
try {
  const db=new DatabaseSync("/root/.loom/agents.db",{readOnly:true});
  const row=db.prepare("SELECT agent_id,name,harness,harness_session_id AS native_id,harness_session_root AS native_root FROM agents WHERE agent_id=? AND workspace_id=?").get(id,"LOCALMODE");
  if (!row || row.name!==`coverage-rs-native-${run}` || row.harness!=="opencode" || !row.native_id || typeof row.native_root!=="string") fail("unowned-agent");
  const owned=db.prepare("SELECT 1 FROM agent_native_sessions WHERE agent_id=? AND harness=? AND native_root=? AND native_id=?").get(id,row.harness,row.native_root,row.native_id);
  if (!owned) fail("native-ref-unregistered");
  const pids=fs.readdirSync("/proc").filter(x=>/^\d+$/.test(x)).map(x=>{
    try {const args=fs.readFileSync(`/proc/${x}/cmdline`).toString().split("\0").filter(Boolean);
      const at=args.indexOf("serve");
      const binary=at>0 && /(?:^|\/)opencode$/.test(args[at-1]);
      return binary && args[at+1]==="--service" && at+2===args.length ? Number(x):null;
    } catch {return null}
  }).filter(x=>x!==null);
  if (pids.length!==1 || pids[0]<=1) fail("ambiguous-service-pid");
  const value={agent_id:id,harness:row.harness,native_id:row.native_id,native_root:row.native_root,service_pid:pids[0],scope:"owned OpenCode service only"};
  if (action==="restart") process.kill(pids[0],"SIGTERM");
  process.stdout.write(JSON.stringify(value)+"\n");
} catch(e) {fail("probe-failed")}
' "$agent_id" "$RUN_ID" "$action")"
printf '%s\n' "$result" | tee -a "$AFT_WORK_DIR/coverage-receipts-stream-native.jsonl"
