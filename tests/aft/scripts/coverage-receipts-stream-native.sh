#!/usr/bin/env bash
# Signal only the exact OpenCode service inside this run's checked loom-local.
set -euo pipefail
# shellcheck source=tests/aft/scripts/agent-flows-ownership.sh
source "$(dirname "${BASH_SOURCE[0]}")/agent-flows-ownership.sh"
agent_flows_check_manifest
[[ $# -ge 2 && $# -le 3 && "$1" =~ ^(probe|restart|count)$ && "$2" =~ ^agt_[A-Za-z0-9_-]+$ ]] || exit 2
if [[ "$1" == count ]]; then
  [[ $# -eq 3 && "$3" =~ ^msg_[a-f0-9]{26}$ ]] || exit 2
else
  [[ $# -eq 2 ]] || exit 2
fi
action="$1" agent_id="$2"
input_key="${3:-}"
cd "$AFT_SOURCE_ROOT"
compose=(agent_flows_podman compose -p "$AFT_OWNED_PROJECT" -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.agents.yml -f test/local-mode/docker-compose.agents-real.yml -f "$AFT_WORK_DIR/fleet-override.yml")
container="$("${compose[@]}" ps -q loom-local | tr -d '\r')"
agent_flows_check_container "$container"
# shellcheck disable=SC2016 # JavaScript template literals are passed verbatim to node.
result="$(agent_flows_podman exec -T "$container" node -e '
const fs=require("node:fs");
const path=require("node:path");
const {DatabaseSync}=require("node:sqlite");
const [id,run,action,key]=process.argv.slice(1);
const fail=(code)=>{process.stderr.write("native restart: "+code+"\n");process.exit(3)};
(async()=>{
  const db=new DatabaseSync("/root/.loom/agents.db",{readOnly:true});
  const row=db.prepare("SELECT agent_id,name,harness,worktree_path,harness_session_id AS native_id,harness_session_root AS native_root FROM agents WHERE agent_id=? AND workspace_id=?").get(id,"LOCALMODE");
  if (!row || ![`coverage-rs-native-${run}`,`coverage-rs-waiting-${run}`,`coverage-rs-create-${run}`].includes(row.name) || row.harness!=="opencode" || !row.native_id || typeof row.native_root!=="string" || !row.worktree_path) fail("unowned-agent");
  if (action==="restart" && row.name!==`coverage-rs-native-${run}`) fail("restart-scope");
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
  const value={agent_id:id,harness:row.harness,worktree_path:row.worktree_path,native_id:row.native_id,native_root:row.native_root,service_pid:pids[0],scope:"owned OpenCode service only"};
  if (action==="count") {
    if (process.env.LOOM_CONFIG_DIR!=="/root/.loom") fail("service-root-mismatch");
    const filename=path.join(process.env.LOOM_CONFIG_DIR,"agents-opencode","state","opencode","service.json");
    const reg=JSON.parse(fs.readFileSync(filename,"utf8"));
    const base=new URL(reg.url);
    if (reg.pid!==pids[0] || base.protocol!=="http:" || !["127.0.0.1","localhost"].includes(base.hostname) ||
        base.pathname!=="/" || base.username || base.password || typeof reg.password!=="string" || !reg.password) fail("service-registration-mismatch");
    const auth="Basic "+Buffer.from("opencode:"+reg.password).toString("base64");
    let cursor="",count=0;
    for (let page=0;page<100;page++) {
      const url=new URL("/api/session/"+encodeURIComponent(row.native_id)+"/message",base);
      url.search=new URLSearchParams({order:"asc",limit:"200",...(cursor?{cursor}:{})}).toString();
      const response=await fetch(url,{headers:{Authorization:auth},signal:AbortSignal.timeout(15000)});
      if (!response.ok) fail("native-history-unavailable");
      const body=await response.json();
      if (!Array.isArray(body.data)) fail("native-history-invalid");
      count+=body.data.filter(m=>m?.type==="user" && m.id===key).length;
      if (body.data.length<200 || !body.cursor?.next) break;
      cursor=body.cursor.next;
      if (page===99) fail("native-history-page-limit");
    }
    value.input_key=key;
    value.native_user_message_count=count;
  }
  if (action==="restart") process.kill(pids[0],"SIGTERM");
  process.stdout.write(JSON.stringify(value)+"\n");
})().catch(()=>fail("probe-failed"));
' "$agent_id" "$RUN_ID" "$action" "$input_key")"
printf '%s\n' "$result" | tee -a "$AFT_WORK_DIR/coverage-receipts-stream-native.jsonl"
