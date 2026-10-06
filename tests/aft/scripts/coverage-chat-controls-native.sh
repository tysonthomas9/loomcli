#!/usr/bin/env bash
# Read only the owned Lead's native assistant model; emit no service credentials.
set -euo pipefail
# shellcheck disable=SC1091 # Runtime source resolves beside this script.
source "$(dirname "${BASH_SOURCE[0]}")/agent-flows-ownership.sh"
agent_flows_check_manifest
[[ $# == 1 && "$1" =~ ^(picker|unknown)$ ]] || exit 2
case_name="$1"
agent_id="$(tr -d '\r\n' < "$AFT_WORK_DIR/chat-controls/$case_name.id")"
[[ "$agent_id" =~ ^agt_[A-Za-z0-9]+$ ]] || exit 2
cd "$AFT_SOURCE_ROOT"
compose=(agent_flows_podman compose -p "$AFT_OWNED_PROJECT" -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.agents.yml -f test/local-mode/docker-compose.agents-real.yml -f "$AFT_WORK_DIR/fleet-override.yml")
container="$("${compose[@]}" ps -q loom-local | tr -d '\r')"
agent_flows_check_container "$container"
# shellcheck disable=SC2016 # This single-quoted program is JavaScript in the owned container.
result="$("${compose[@]}" exec -T loom-local node -e '
const fs = require("node:fs");
const path = require("node:path");
const {DatabaseSync} = require("node:sqlite");
const [id, run, caseName, target, repo] = process.argv.slice(1);
const fail = reason => { process.stdout.write(JSON.stringify({status:"unavailable",reason})+"\n"); };
(async () => {
  const db = new DatabaseSync("/root/.loom/agents.db", {readOnly:true});
  const row = db.prepare("SELECT agent_id,name,repo,harness,preset,model,state,harness_session_id AS native_id,harness_session_root AS native_root,worktree_path FROM agents WHERE agent_id=? AND workspace_id=?").get(id,"LOCALMODE");
  if (!row || row.name !== `cov-controls-${caseName}-${run}` || row.repo !== repo ||
      row.harness !== "opencode" || row.preset !== "lead" || row.model !== target ||
      !row.native_id || typeof row.native_root !== "string" || !row.worktree_path)
    return fail("owned-agent-or-native-ref-mismatch");
  if (!db.prepare("SELECT 1 FROM agent_native_sessions WHERE agent_id=? AND harness=? AND native_root=? AND native_id=?").get(id,"opencode",row.native_root,row.native_id))
    return fail("native-ref-not-owned");
  const file = path.join(process.env.LOOM_CONFIG_DIR || "", "agents-opencode/state/opencode/service.json");
  let reg;
  try { reg = JSON.parse(fs.readFileSync(file,"utf8")); } catch { return fail("service-registration-unavailable"); }
  let base;
  try { base = new URL(reg.url); } catch { return fail("service-url-invalid"); }
  if (base.protocol !== "http:" || !["127.0.0.1","localhost"].includes(base.hostname) ||
      base.username || base.password || base.pathname !== "/" || !Number.isInteger(reg.pid) ||
      typeof reg.password !== "string" || !reg.password) return fail("service-identity-invalid");
  const auth = "Basic " + Buffer.from("opencode:" + reg.password).toString("base64");
  const get = async pathname => {
    const response = await fetch(new URL(pathname, base), {headers:{Authorization:auth},signal:AbortSignal.timeout(15000)});
    if (!response.ok) throw Error("native-service-unavailable");
    return response.json();
  };
  let info, session, messages;
  try {
    info = await get("/api/info");
    session = await get("/api/session/" + encodeURIComponent(row.native_id));
    messages = await get("/api/session/" + encodeURIComponent(row.native_id) + "/message?type=assistant&order=desc&limit=200");
  } catch { return fail("native-read-unavailable"); }
  if (info?.pid !== reg.pid || session?.data?.id !== row.native_id ||
      session.data.metadata?.agent_id !== id ||
      session.data.location?.directory !== row.worktree_path ||
      !Array.isArray(messages?.data)) return fail("native-session-identity-mismatch");
  if (caseName === "picker" && session.data.model?.variant !== "high")
    return fail("native-effort-variant-mismatch");
  const completed = messages.data.filter(m => m?.type === "assistant" && m.time?.completed && m.finish && !m.error);
  if (!completed.length) return fail("native-completed-assistant-missing");
  const model = completed[0].model;
  const observed = model?.providerID && model?.id ? model.providerID + "/" + model.id : null;
  if (!observed || observed !== target) return fail("native-completed-model-mismatch");
  process.stdout.write(JSON.stringify({status:"observed",agent_id:id,native_id:row.native_id,
    completed_message_id:completed[0].id, native_model:observed,
    native_effort:session.data.model?.variant || null,
    source:"completed-assistant-message"})+"\n");
})().catch(() => fail("native-probe-error"));
' "$agent_id" "$RUN_ID" "$case_name" "$AFT_REAL_MODEL" "$AFT_AGENT_FLOW_REPO")"
printf '%s\n' "$result" >> "$AFT_WORK_DIR/chat-controls/native-models.jsonl"
jq -e --arg id "$agent_id" --arg model "$AFT_REAL_MODEL" '.status == "observed" and .agent_id == $id and .native_model == $model and .source == "completed-assistant-message"' <<< "$result" >/dev/null || {
  echo "owned native model could not be confirmed; see native-models.jsonl" >&2
  exit 1
}
echo "confirmed native completed assistant model for $case_name"
