#!/usr/bin/env bash
# Read only this run's Agent/native ownership, native user count and worktrees.
set -euo pipefail
# shellcheck source=tests/aft/scripts/agent-flows-ownership.sh
source "$(dirname "${BASH_SOURCE[0]}")/agent-flows-ownership.sh"
agent_flows_check_manifest
[[ $# -ge 1 && "$1" =~ ^(inventory|probe|count|absent)$ ]] || exit 2
case "$1" in
  inventory) [[ $# -eq 1 ]] || exit 2 ;;
  probe) [[ $# -eq 2 && "$2" =~ ^agt_[A-Za-z0-9_-]+$ ]] || exit 2 ;;
  count|absent) [[ $# -eq 3 && "$2" =~ ^agt_[A-Za-z0-9_-]+$ && "$3" =~ ^msg_[a-f0-9]{26}$ ]] || exit 2 ;;
esac
action="$1" agent_id="${2:-}" input_key="${3:-}"
cd "$AFT_SOURCE_ROOT"
compose=(agent_flows_podman compose -p "$AFT_OWNED_PROJECT" -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.agents.yml -f test/local-mode/docker-compose.agents-real.yml -f "$AFT_WORK_DIR/fleet-override.yml")
container="$("${compose[@]}" ps -q loom-local | tr -d '\r')"
agent_flows_check_container "$container"
# shellcheck disable=SC2016 # The single-quoted program runs in the owned container.
result="$(agent_flows_podman exec "$container" node -e '
const fs=require("node:fs");
const path=require("node:path");
const {DatabaseSync}=require("node:sqlite");
const [action,id,key,run,repo,model]=process.argv.slice(1);
const fail=reason=>{process.stderr.write("edges native: "+reason+"\n");process.exit(3)};
(async()=>{
  if(action==="inventory") {
    const root="/root/.loom/worktrees/source-repo";
    const nonGit=path.dirname(repo);
    if(repo!=="/root/.loom/workspaces/LOCALMODE/source-repo" ||
       !fs.statSync(nonGit).isDirectory() || fs.existsSync(path.join(nonGit,".git")) ||
       !fs.statSync(repo).isDirectory() || !fs.existsSync(path.join(repo,".git")) ||
       fs.existsSync(path.join(nonGit,`no-such-repo-${run}`))) fail("repo-fixture-mismatch");
    const names=fs.existsSync(root)?fs.readdirSync(root).sort():[];
    const db=new DatabaseSync("/root/.loom/agents.db",{readOnly:true});
    const agents=db.prepare("SELECT count(*) AS n FROM agents WHERE workspace_id=?").get("LOCALMODE").n;
    const sessions=db.prepare("SELECT count(*) AS n FROM agent_native_sessions").get().n;
    process.stdout.write(JSON.stringify({worktrees:names,agents,sessions,non_git_dir:nonGit,unknown_repo:path.join(nonGit,`no-such-repo-${run}`)})+"\n");
    return;
  }
  const db=new DatabaseSync("/root/.loom/agents.db",{readOnly:true});
  const row=db.prepare("SELECT agent_id,name,preset,repo,model,harness,worktree_path,harness_session_id AS native_id,harness_session_root AS native_root FROM agents WHERE agent_id=? AND workspace_id=?").get(id,"LOCALMODE");
  const expected={interrupt:[`coverage-rs-edges-interrupt-${run}`,"lead"],large:[`coverage-rs-edges-large-${run}`,"pr-review-interactive"],create:[`coverage-rs-edges-create-${run}`,"lead"]};
  if(!row || !Object.values(expected).some(([name,preset])=>row.name===name && row.preset===preset) ||
     row.repo!==repo || row.model!==model || row.harness!=="opencode" || !row.worktree_path ||
     !row.native_id || typeof row.native_root!=="string") fail("unowned-agent");
  if(!db.prepare("SELECT 1 FROM agent_native_sessions WHERE agent_id=? AND harness=? AND native_root=? AND native_id=?").get(id,"opencode",row.native_root,row.native_id)) fail("unregistered-native-ref");
  const value={agent_id:id,worktree_path:row.worktree_path,native_id:row.native_id,native_root:row.native_root,harness:"opencode"};
  const filename="/root/.loom/agents-opencode/state/opencode/service.json";
  const reg=JSON.parse(fs.readFileSync(filename,"utf8"));
  const base=new URL(reg.url);
  if(!Number.isInteger(reg.pid) || reg.pid<=1 || base.protocol!=="http:" ||
     !["127.0.0.1","localhost"].includes(base.hostname) || !base.port ||
     base.pathname!=="/" || base.search || base.hash || base.username || base.password ||
     typeof reg.password!=="string" || !reg.password) fail("foreign-service-registry");
  const cmd=fs.readFileSync(`/proc/${reg.pid}/cmdline`).toString().split("\0").filter(Boolean);
  if(!cmd.some(x=>/(?:^|\/)opencode$/.test(x)) || !cmd.includes("serve") || !cmd.includes("--service")) fail("foreign-service-pid");
  const auth="Basic "+Buffer.from("opencode:"+reg.password).toString("base64");
  const fetchJSON=async pathname=>{
    const response=await fetch(new URL(pathname,base),{headers:{Authorization:auth},signal:AbortSignal.timeout(15000)});
    if(!response.ok) fail("native-read-failed");
    return response.json();
  };
  const info=await fetchJSON("/api/info");
  const session=await fetchJSON("/api/session/"+encodeURIComponent(row.native_id));
  if(info?.pid!==reg.pid || session?.data?.id!==row.native_id ||
     session.data.metadata?.agent_id!==id || session.data.location?.directory!==row.worktree_path) fail("foreign-native-session");
  if(action==="probe") {value.service_pid=reg.pid;process.stdout.write(JSON.stringify(value)+"\n");return;}
  let cursor="",count=0;
  for(let page=0;page<100;page++) {
    const url=new URL("/api/session/"+encodeURIComponent(row.native_id)+"/message",base);
    url.search=new URLSearchParams({limit:"200",...(cursor?{cursor}:{order:"asc"})}).toString();
    const response=await fetch(url,{headers:{Authorization:auth},signal:AbortSignal.timeout(15000)});
    if(!response.ok) fail("native-history-unavailable");
    const body=await response.json();
    if(!Array.isArray(body.data)) fail("native-history-invalid");
    count+=body.data.filter(m=>m?.type==="user" && m.id===key).length;
    if(body.data.length<200 || !body.cursor?.next) break;
    cursor=body.cursor.next;
    if(page===99) fail("native-history-page-limit");
  }
  if(count!==(action==="count"?1:0)) fail("native-input-count-mismatch");
  value.input_key=key;
  value.native_user_message_count=count;
  process.stdout.write(JSON.stringify(value)+"\n");
})().catch(()=>fail("probe-failed"));
' "$action" "$agent_id" "$input_key" "$RUN_ID" "$AFT_AGENT_FLOW_REPO" "$AFT_REAL_MODEL")"
printf '%s\n' "$result" >> "$AFT_WORK_DIR/coverage-receipts-stream-edges-native.jsonl"
printf '%s\n' "$result"
