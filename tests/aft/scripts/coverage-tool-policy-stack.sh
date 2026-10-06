#!/usr/bin/env bash
# Scoped CLI readbacks from the runner-owned Loom container; no host auth read.
set -euo pipefail
# shellcheck source=tests/aft/scripts/agent-flows-ownership.sh
source "$(dirname "${BASH_SOURCE[0]}")/agent-flows-ownership.sh"
agent_flows_check_manifest
[[ $# -eq 2 ]] || { echo 'usage or git-state plus one argument required' >&2; exit 2; }
cd "$AFT_SOURCE_ROOT"
compose=(agent_flows_podman compose -p "$AFT_OWNED_PROJECT" -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.agents.yml -f test/local-mode/docker-compose.agents-real.yml -f "$AFT_WORK_DIR/fleet-override.yml")
container="$("${compose[@]}" ps -q loom-local | tr -d '\r')"
agent_flows_check_container "$container"

case "$1" in
  usage)
    [[ "$2" == "aft-lead-${RUN_ID}-cov-policy-usage" ]] || { echo 'foreign usage agent' >&2; exit 2; }
    "${compose[@]}" exec -T loom-local loom usage --format json --agent "$2"
    ;;
  native-usage)
    [[ "$2" =~ ^agt_[A-Za-z0-9]+$ ]] || { echo 'invalid native probe Agent ID' >&2; exit 2; }
    # shellcheck disable=SC2016 # JavaScript template literals belong to Node.
    "${compose[@]}" exec -T loom-local node -e '
      const fs=require("node:fs");
      const path=require("node:path");
      const {DatabaseSync}=require("node:sqlite");
      const [id,run,repo]=process.argv.slice(1);
      const db=new DatabaseSync("/root/.loom/agents.db",{readOnly:true});
      const row=db.prepare("SELECT agent_id,name,repo,harness,harness_session_id AS native_id,harness_session_root AS native_root,worktree_path FROM agents WHERE workspace_id=? AND agent_id=?").get("LOCALMODE",id);
      if (!row || row.name!==`aft-lead-${run}-cov-policy-usage` || row.repo!==repo ||
          row.harness!=="opencode" || !row.native_id || typeof row.native_root!=="string")
        throw Error("native probe identity unavailable");
      const owner=db.prepare("SELECT 1 FROM agent_native_sessions WHERE agent_id=? AND harness=? AND native_root=? AND native_id=?")
        .get(id,"opencode",row.native_root,row.native_id);
      if (!owner) throw Error("native probe ownership missing");
      (async()=>{
        if (process.env.LOOM_CONFIG_DIR!=="/root/.loom") throw Error("owned service root mismatch");
        const file=path.join(process.env.LOOM_CONFIG_DIR,"agents-opencode","state","opencode","service.json");
        const registration=JSON.parse(fs.readFileSync(file,"utf8"));
        const base=new URL(registration.url);
        if (base.protocol!=="http:" || !["127.0.0.1","localhost"].includes(base.hostname) ||
            base.username || base.password || base.search || base.hash || base.pathname!=="/" ||
            typeof registration.password!=="string" || !registration.password ||
            !Number.isInteger(registration.pid) || registration.pid<1)
          throw Error("native service registration invalid");
        const authorization="Basic "+Buffer.from("opencode:"+registration.password).toString("base64");
        const get=async path=>{
          const res=await fetch(new URL(path,base),{headers:{Authorization:authorization},signal:AbortSignal.timeout(15000)});
          if (!res.ok) throw Error("native service read failed");
          return await res.json();
        };
        const info=await get("/api/info");
        if (info?.pid!==registration.pid) throw Error("native service identity changed");
        const session=(await get("/api/session/"+encodeURIComponent(row.native_id)))?.data;
        if (session?.id!==row.native_id || session.metadata?.agent_id!==id ||
            session.location?.directory!==row.worktree_path) throw Error("native session mismatch");
        const messages=(await get("/api/session/"+encodeURIComponent(row.native_id)+"/message?type=assistant&order=desc&limit=200"))?.data;
        if (!Array.isArray(messages)) throw Error("native usage messages unavailable");
        const steps=messages.filter(m=>m?.type==="assistant" && m.time?.completed!=null && m.tokens).map(m=>({
          itemID:m.id,inputTokens:m.tokens.input||0,
          outputTokens:(m.tokens.output||0)+(m.tokens.reasoning||0),
          cacheReadTokens:m.tokens.cache?.read||0,cacheWriteTokens:m.tokens.cache?.write||0,
          costUsd:m.cost||0
        }));
        if (!steps.length) throw Error("native usage steps missing");
        process.stdout.write(JSON.stringify({agent_id:id,native_id:row.native_id,steps})+"\n");
      })().catch(()=>{console.error("native usage probe failed");process.exitCode=1});
    ' "$2" "$RUN_ID" "$AFT_AGENT_FLOW_REPO"
    ;;
  native-tool)
    [[ "$2" =~ ^agt_[A-Za-z0-9_-]+$ ]] || { echo 'invalid native tool Agent ID' >&2; exit 2; }
    # shellcheck disable=SC2016 # The single-quoted program runs inside Node.
    "${compose[@]}" exec -T loom-local node -e '
      const fs=require("node:fs");
      const path=require("node:path");
      const {DatabaseSync}=require("node:sqlite");
      const [id,run,repo]=process.argv.slice(1);
      const db=new DatabaseSync("/root/.loom/agents.db",{readOnly:true});
      const row=db.prepare("SELECT agent_id,name,preset,repo,harness,created_by_kind,parent_agent_id,harness_session_id AS native_id,harness_session_root AS native_root,worktree_path FROM agents WHERE workspace_id=? AND agent_id=?")
        .get("LOCALMODE",id);
      const lead=`aft-lead-${run}-cov-policy`, child=`aft-child-${run}-cov-policy-tray`;
      const parent=db.prepare("SELECT agent_id,preset,repo,harness,created_by_kind FROM agents WHERE workspace_id=? AND name=?")
        .get("LOCALMODE",`aft-lead-${run}-cov-policy-tray`);
      const ownedLead=row?.name===lead && row.preset==="lead" && row.created_by_kind==="user" && row.parent_agent_id===null;
      const ownedChild=row?.name===child && row.preset==="task" && row.created_by_kind==="agent" &&
        row.parent_agent_id===parent?.agent_id && parent?.preset==="lead" && parent?.repo===repo &&
        parent?.harness==="opencode" && parent?.created_by_kind==="user";
      if (!row || row.agent_id!==id || (!ownedLead && !ownedChild) || row.repo!==repo ||
          row.harness!=="opencode" || !row.native_id || typeof row.native_root!=="string" ||
          row.worktree_path!==`/root/.loom/worktrees/source-repo/${id}`)
        throw Error("native tool probe identity unavailable");
      const owner=db.prepare("SELECT 1 FROM agent_native_sessions WHERE agent_id=? AND harness=? AND native_root=? AND native_id=?")
        .get(id,"opencode",row.native_root,row.native_id);
      if (!owner) throw Error("native tool probe ownership missing");
      (async()=>{
        if (process.env.LOOM_CONFIG_DIR!=="/root/.loom") throw Error("owned service root mismatch");
        const registration=JSON.parse(fs.readFileSync(path.join(process.env.LOOM_CONFIG_DIR,
          "agents-opencode","state","opencode","service.json"),"utf8"));
        const base=new URL(registration.url);
        if (base.protocol!=="http:" || !["127.0.0.1","localhost"].includes(base.hostname) ||
            base.username || base.password || base.search || base.hash || base.pathname!=="/" ||
            typeof registration.password!=="string" || !registration.password ||
            !Number.isInteger(registration.pid) || registration.pid<1)
          throw Error("native service registration invalid");
        const authorization="Basic "+Buffer.from("opencode:"+registration.password).toString("base64");
        const get=async path=>{
          const res=await fetch(new URL(path,base),{headers:{Authorization:authorization},signal:AbortSignal.timeout(15000)});
          if (!res.ok) throw Error("native service read failed");
          return await res.json();
        };
        const info=await get("/api/info");
        if (info?.pid!==registration.pid) throw Error("native service identity changed");
        const session=(await get("/api/session/"+encodeURIComponent(row.native_id)))?.data;
        if (session?.id!==row.native_id || session.metadata?.agent_id!==id ||
            session.location?.directory!==row.worktree_path) throw Error("native session mismatch");
        const messages=(await get("/api/session/"+encodeURIComponent(row.native_id)+"/message?type=assistant&order=desc&limit=200"))?.data;
        if (!Array.isArray(messages)) throw Error("native tool messages unavailable");
        const sentinel=`ghp_AFTONLY${run}Q7mR2pK9xT4vN8cY6bL5fS3dH1jW0`;
        const calls=messages.flatMap(m=>m?.type==="assistant" && Array.isArray(m.content) ?
          m.content.filter(c=>c?.type==="tool" && c.state?.status==="completed" &&
            JSON.stringify(c.state.input||{}).includes(sentinel))
            .map(c=>({itemID:m.id+"/tool/"+c.id,name:c.name,
              safeOutput:!JSON.stringify(c.state.content||{}).includes(sentinel)})) : []);
        if (calls.length!==1 || !calls[0].safeOutput || !calls[0].itemID ||
            !/^(bash|shell|command|exec)$/i.test(calls[0].name||""))
          throw Error("native sentinel tool call missing, duplicated or unsafe");
        process.stdout.write(JSON.stringify({agent_id:id,tool_item_ids:[calls[0].itemID]})+"\n");
      })().catch(()=>{console.error("native tool probe failed");process.exitCode=1});
    ' "$2" "$RUN_ID" "$AFT_AGENT_FLOW_REPO"
    ;;
  git-state)
    [[ "$2" =~ ^agt_[A-Za-z0-9_-]+$ ]] || { echo 'invalid reviewer Agent ID' >&2; exit 2; }
    jq -e --arg repo "$AFT_AGENT_FLOW_REPO" \
      '.fixture_repo.seed_path == "/workspace/source-repo" and .fixture_repo.managed_path == $repo' \
      "$AFT_WORK_DIR/manifest.json" >/dev/null || { echo 'owned fixture repo mismatch' >&2; exit 2; }
    # shellcheck disable=SC2016 # The single-quoted program runs inside Node.
    "${compose[@]}" exec -T loom-local node -e '
      const fs=require("node:fs");
      const cp=require("node:child_process");
      const path=require("node:path");
      const {fileURLToPath}=require("node:url");
      const {DatabaseSync}=require("node:sqlite");
      const [id,run,sourcePath,seedPath]=process.argv.slice(1);
      if (!/^[A-Za-z0-9_-]+$/.test(run)) throw Error("invalid run ID");
      const source=fs.realpathSync(sourcePath);
      const seed=fs.realpathSync(seedPath);
      if (source!==sourcePath || seed!==seedPath || source!=="/root/.loom/workspaces/LOCALMODE/source-repo" ||
          seed!=="/workspace/source-repo") throw Error("foreign source repo");
      const db=new DatabaseSync("/root/.loom/agents.db",{readOnly:true});
      const row=db.prepare("SELECT agent_id,name,preset,repo,harness,created_by_kind,parent_agent_id,worktree_path,branch FROM agents WHERE workspace_id=? AND agent_id=?")
        .get("LOCALMODE",id);
      const expected=path.join("/root/.loom/worktrees/source-repo",id);
      if (!row || row.agent_id!==id || row.name!==`aft-review-${run}-cov-policy` ||
          row.preset!=="pr-review-interactive" || row.repo!==source || row.harness!=="opencode" ||
          row.created_by_kind!=="user" || row.parent_agent_id!==null || row.branch!==null ||
          row.worktree_path!==expected) throw Error("foreign reviewer Agent binding");
      const root=fs.realpathSync(row.worktree_path);
      if (root!==expected || root===source || root===seed) throw Error("foreign checkout");
      const git=(...args)=>cp.execFileSync("git",["-C",root,...args],{encoding:"utf8"}).trim();
      const sourceGit=(...args)=>cp.execFileSync("git",["-C",source,...args],{encoding:"utf8"}).trim();
      if (git("rev-parse","--show-toplevel")!==root ||
          fs.realpathSync(git("rev-parse","--path-format=absolute","--git-common-dir"))!==
            fs.realpathSync(sourceGit("rev-parse","--path-format=absolute","--git-common-dir")))
        throw Error("checkout is not a worktree of owned source");
      const remotes=git("remote").split("\n").filter(Boolean).sort();
      const remoteRefs={};
      for (const remote of remotes) {
        const url=git("remote","get-url",remote);
        const remotePath=url.startsWith("file://") ? fileURLToPath(url) :
          path.isAbsolute(url) ? url : path.resolve(root,url);
        const resolved=fs.realpathSync(remotePath);
        if (resolved!==source && resolved!==seed) throw Error("nonlocal or foreign git remote");
        remoteRefs[remote]=git("ls-remote",remote);
      }
      process.stdout.write(JSON.stringify({agent_id:id,checkout:root,repo:source,
        preset:row.preset,harness:row.harness,head:git("rev-parse","HEAD"),
        status:git("status","--porcelain","--untracked-files=all"),
        refs:git("show-ref"),remoteRefs,
        deniedTargetExists:fs.existsSync(path.join(root,`SECURITY_DENIED_${run}.txt`))})+"\n");
    ' "$2" "$RUN_ID" "$AFT_AGENT_FLOW_REPO" /workspace/source-repo
    ;;
  *) echo 'unknown scoped readback' >&2; exit 2 ;;
esac
