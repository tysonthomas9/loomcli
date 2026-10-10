#!/usr/bin/env bash
# Read one run-owned child's native OpenCode input count; no runtime mutation.
set -euo pipefail
# shellcheck source=tests/aft/scripts/agent-flows-ownership.sh
source "$(dirname "${BASH_SOURCE[0]}")/agent-flows-ownership.sh"
agent_flows_check_manifest
[[ $# -eq 2 && "$1" =~ ^agt_[A-Za-z0-9_-]+$ && "$2" =~ ^msg_[a-f0-9]{26}$ ]] || exit 2
agent_id="$1"
input_key="$2"
row="$(curl -fsS --max-time 15 "$AFT_API_URL/api/workspaces/LOCALMODE/v1/agents/$agent_id")"
jq -e --arg id "$agent_id" --arg name "cov-child-queue-task-$RUN_ID" \
  --arg repo "$AFT_AGENT_FLOW_REPO" \
  '.agent_id == $id and .name == $name and .preset == "task" and
   .created_by_kind == "agent" and .repo == $repo and .harness == "opencode" and
   .parent_agent_id == .root_agent_id and .parent_agent_id == .created_by_id and
   (.parent_agent_id | type == "string" and startswith("agt_"))' <<< "$row" >/dev/null
cd "$AFT_SOURCE_ROOT"
compose=(agent_flows_podman compose -p "$AFT_OWNED_PROJECT" -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.agents.yml -f test/local-mode/docker-compose.agents-real.yml -f "$AFT_WORK_DIR/fleet-override.yml")
container="$("${compose[@]}" ps -q loom-local | tr -d '\r')"
agent_flows_check_container "$container"
agent_flows_podman exec "$container" node -e '
const fs=require("node:fs");
const {DatabaseSync}=require("node:sqlite");
const [id,run,key]=process.argv.slice(1);
const fail=(why)=>{process.stderr.write("queue native input: "+why+"\n");process.exit(3)};
(async()=>{
  const db=new DatabaseSync("/root/.loom/agents.db",{readOnly:true});
  const row=db.prepare("SELECT agent_id,name,repo,harness,preset,parent_agent_id,root_agent_id,created_by_id,worktree_path,harness_session_id AS native_id,harness_session_root AS native_root FROM agents WHERE agent_id=? AND workspace_id=?").get(id,"LOCALMODE");
  const validNativeRow=(row,run)=>!!row && row.name===`cov-child-queue-task-${run}` &&
    row.harness==="opencode" && row.preset==="task" && !!row.parent_agent_id &&
    row.parent_agent_id===row.root_agent_id && row.parent_agent_id===row.created_by_id &&
    !!row.worktree_path && !!row.native_id && typeof row.native_root==="string";
  if(!validNativeRow(row,run)) fail("unowned child or NativeRef");
  const parent=db.prepare("SELECT name,repo,preset FROM agents WHERE agent_id=? AND workspace_id=?").get(row.parent_agent_id,"LOCALMODE");
  if(!parent || parent.name!==`cov-child-queue-lead-${run}` || parent.repo!==row.repo || parent.preset!=="lead") fail("wrong parent");
  const owned=db.prepare("SELECT 1 FROM agent_native_sessions WHERE agent_id=? AND harness=? AND native_root=? AND native_id=?").get(id,"opencode",row.native_root,row.native_id);
  if(!owned) fail("NativeRef unregistered");
  const pids=fs.readdirSync("/proc").filter(x=>/^\d+$/.test(x)).map(x=>{
    try {const args=fs.readFileSync(`/proc/${x}/cmdline`).toString().split("\0").filter(Boolean);
      const at=args.indexOf("serve");
      return at>0 && /(?:^|\/)opencode$/.test(args[at-1]) && args[at+1]==="--service" && at+2===args.length?Number(x):null;
    } catch {return null}
  }).filter(x=>x!==null);
  if(pids.length!==1 || pids[0]<=1) fail("ambiguous owned service");
  const reg=JSON.parse(fs.readFileSync("/root/.loom/agents-opencode/state/opencode/service.json","utf8"));
  const base=new URL(reg.url);
  if(reg.pid!==pids[0] || base.protocol!=="http:" || !["127.0.0.1","localhost"].includes(base.hostname) ||
     !base.port || base.pathname!=="/" || base.search || base.hash || base.username || base.password ||
     typeof reg.password!=="string" || !reg.password) fail("service registration mismatch");
  const auth="Basic "+Buffer.from("opencode:"+reg.password).toString("base64");
  const get=async pathname=>{
    const response=await fetch(new URL(pathname,base),{headers:{Authorization:auth},signal:AbortSignal.timeout(15000)});
    if(!response.ok) fail("native identity unavailable");
    return response.json();
  };
  const info=await get("/api/info");
  const session=await get("/api/session/"+encodeURIComponent(row.native_id));
  if(info?.pid!==reg.pid || session?.data?.id!==row.native_id ||
     session.data.metadata?.agent_id!==id || session.data.location?.directory!==row.worktree_path)
    fail("native service/session owner mismatch");
  let cursor="",count=0;
  const seen=new Set();
  for(let page=0;page<100;page++) {
    const url=new URL("/api/session/"+encodeURIComponent(row.native_id)+"/message",base);
    url.search=new URLSearchParams({limit:"200",...(cursor?{cursor}:{order:"asc"})}).toString();
    const response=await fetch(url,{headers:{Authorization:auth},signal:AbortSignal.timeout(15000)});
    if(!response.ok) fail("native history unavailable");
    const body=await response.json();
    if(!Array.isArray(body.data)) fail("native history invalid");
    count+=body.data.filter(m=>m?.type==="user" && m.id===key).length;
    if(body.data.length<200 || !body.cursor?.next) break;
    if(seen.has(body.cursor.next)) fail("native history cursor repeated");
    seen.add(body.cursor.next);
    cursor=body.cursor.next;
    if(page===99) fail("native history page limit");
  }
  if(count!==1) fail("native input count mismatch");
  process.stdout.write(JSON.stringify({agent_id:id,harness:row.harness,native_id:row.native_id,
    native_root:row.native_root,input_key:key,native_user_message_count:count})+"\n");
})().catch(()=>fail("probe failed"));
' "$agent_id" "$RUN_ID" "$input_key"
