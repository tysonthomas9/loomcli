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
        const file="/root/.loom/agents-opencode/state/opencode/service.json";
        const registration=JSON.parse(fs.readFileSync(file,"utf8"));
        const base=new URL(registration.url);
        if (base.protocol!=="http:" || !["127.0.0.1","localhost"].includes(base.hostname) ||
            base.pathname!=="/" || !registration.password || !Number.isInteger(registration.pid))
          throw Error("native service registration invalid");
        const authorization="Basic "+Buffer.from("opencode:"+registration.password).toString("base64");
        const get=async path=>{
          const res=await fetch(new URL(path,base),{headers:{Authorization:authorization},signal:AbortSignal.timeout(15000)});
          if (!res.ok) throw Error("native service read failed");
          return (await res.json()).data;
        };
        const info=await get("/api/info");
        if (info?.pid!==registration.pid) throw Error("native service identity changed");
        const session=await get("/api/session/"+encodeURIComponent(row.native_id));
        if (session?.id!==row.native_id || session.metadata?.agent_id!==id ||
            session.location?.directory!==row.worktree_path) throw Error("native session mismatch");
        const messages=await get("/api/session/"+encodeURIComponent(row.native_id)+"/message?type=assistant&order=desc&limit=200");
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
  git-state)
    [[ "$2" == /workspace/* && "$2" != *$'\n'* ]] || { echo 'foreign checkout path' >&2; exit 2; }
    # shellcheck disable=SC2016 # The single-quoted program runs inside Node.
    "${compose[@]}" exec -T loom-local node -e '
      const fs=require("node:fs");
      const cp=require("node:child_process");
      const [dir,run]=process.argv.slice(1);
      const root=fs.realpathSync(dir);
      if (!root.startsWith("/workspace/") || root==="/workspace/source-repo") throw Error("foreign checkout");
      if (!/^[A-Za-z0-9_-]+$/.test(run)) throw Error("invalid run ID");
      const git=(...args)=>cp.execFileSync("git",["-C",root,...args],{encoding:"utf8"}).trim();
      const remotes=git("remote").split("\n").filter(Boolean).sort();
      const remoteRefs={};
      for (const remote of remotes) {
        const url=git("remote","get-url",remote);
        if (!url.startsWith("/workspace/") && !url.startsWith("file:///workspace/"))
          throw Error("nonlocal git remote is outside owned proof");
        remoteRefs[remote]=git("ls-remote",remote);
      }
      process.stdout.write(JSON.stringify({head:git("rev-parse","HEAD"),
        status:git("status","--porcelain","--untracked-files=all"),
        refs:git("show-ref"),remoteRefs,
        deniedTargetExists:fs.existsSync(require("node:path").join(root,`SECURITY_DENIED_${run}.txt`))})+"\n");
    ' "$2" "$RUN_ID"
    ;;
  *) echo 'unknown scoped readback' >&2; exit 2 ;;
esac
