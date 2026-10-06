#!/usr/bin/env bash
# Read the owned child's current OpenCode session model, never its transcript or credentials.
set -euo pipefail
# shellcheck source=tests/aft/scripts/agent-flows-ownership.sh
source "$(dirname "${BASH_SOURCE[0]}")/agent-flows-ownership.sh"
agent_flows_check_manifest
: "${AFT_AGENT_FLOW_REPO:?owned managed repository required}"
: "${AFT_REAL_MODEL:?runner-validated UI model required}"
: "${AFT_NATIVE_SESSION_PROBE:?runner-owned native identity probe required}"
: "${AFT_TESTS_DIR:?runner-owned test scripts required}"
[[ "$AFT_NATIVE_SESSION_PROBE" == "$AFT_TESTS_DIR/scripts/agent-flows-native-session.sh" ]] \
  || { echo 'native identity probe path mismatch' >&2; exit 2; }
[[ $# -eq 1 && "$1" =~ ^agt_[A-Za-z0-9]+$ ]] || { echo 'expected one exact child Agent ID' >&2; exit 2; }
agent_id="$1"
agent_json="$(curl -fsS --max-time 15 "$AFT_API_URL/api/workspaces/LOCALMODE/v1/agents/$agent_id")" \
  || { echo 'owned child Agent API identity unavailable' >&2; exit 1; }
jq -e --arg id "$agent_id" --arg run "$RUN_ID" --arg repo "$AFT_AGENT_FLOW_REPO" '
  .agent_id == $id and
  (.name | test("^aft-(child-(a|b)|cancel-child|isolation-(own|foreign))-" + $run + "$")) and
  .repo == $repo and
  .harness == "opencode" and .preset == "task" and .created_by_kind == "agent" and
  (.parent_agent_id | type == "string" and startswith("agt_"))' \
  <<< "$agent_json" >/dev/null || { echo 'foreign or non-task child Agent API identity' >&2; exit 1; }
native_ref="$("$AFT_NATIVE_SESSION_PROBE" "$agent_id")" \
  || { echo 'owned child NativeRef unavailable' >&2; exit 1; }
jq -e --arg id "$agent_id" '.agent_id == $id and .harness == "opencode" and
  (.native_id | type == "string" and length > 0) and
  (.native_root | type == "string")' \
  <<< "$native_ref" >/dev/null || { echo 'owned child NativeRef is invalid' >&2; exit 1; }
native_id="$(jq -r '.native_id' <<< "$native_ref" | tr -d '\r')"
native_root="$(jq -r '.native_root' <<< "$native_ref" | tr -d '\r')"

cd "$AFT_SOURCE_ROOT"
compose=(agent_flows_podman compose -p "$AFT_OWNED_PROJECT" -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.agents.yml -f test/local-mode/docker-compose.agents-real.yml -f "$AFT_WORK_DIR/fleet-override.yml")
container="$("${compose[@]}" ps -q loom-local | tr -d '\r')"
agent_flows_check_container "$container"
if ! result="$("${compose[@]}" exec -T loom-local node -e '
const fs = require("node:fs");
const path = require("node:path");
const {DatabaseSync} = require("node:sqlite");
const [id, run, repo, target, expectedNativeID, expectedNativeRoot] = process.argv.slice(1);
const receipt = {agent_id:id, run_id:run, registry_requested_model:null,
  native_reported_model:null, ui_lead_target_model:target,
  completed_answer_evidence:"not assessed by this read-only probe", status:"unavailable"};
const emit = (reason) => {
  if (reason) receipt.reason = reason;
  process.stdout.write(JSON.stringify(receipt) + "\n");
};
(async () => {
  const db = new DatabaseSync("/root/.loom/agents.db", {readOnly:true});
  const row = db.prepare("SELECT agent_id,name,harness,repo,preset,parent_agent_id,model,worktree_path,harness_session_id AS native_id,harness_session_root AS native_root,state,outcome FROM agents WHERE agent_id=? AND workspace_id=?").get(id,"LOCALMODE");
  const ownedName = new RegExp("^aft-(child-(a|b)|cancel-child|isolation-(own|foreign))-" + run + "$");
  if (!row || !ownedName.test(row.name) || row.harness !== "opencode" || row.repo !== repo ||
      row.preset !== "task" || !row.parent_agent_id || !row.native_id ||
      typeof row.native_root !== "string" ||
      !row.worktree_path) return emit("owned child or current NativeRef unavailable");
  if (row.native_id !== expectedNativeID || row.native_root !== expectedNativeRoot)
    return emit("current NativeRef changed after the owned identity probe");
  const parent = db.prepare("SELECT name,harness,repo FROM agents WHERE agent_id=? AND workspace_id=?").get(row.parent_agent_id,"LOCALMODE");
  const ownedParent = new RegExp("^(aft-(child-lead|cancel-lead|isolation-[ab])-" + run + ")$");
  if (!parent || !ownedParent.test(parent.name) || parent.harness !== "opencode" || parent.repo !== repo)
    return emit("parent is not a run-owned Lead");
  const recorded = db.prepare("SELECT 1 FROM agent_native_sessions WHERE agent_id=? AND harness=? AND native_root=? AND native_id=?").get(id,"opencode",row.native_root,row.native_id);
  if (!recorded) return emit("current NativeRef is not recorded as owned");
  receipt.native_id = row.native_id;
  receipt.native_root = row.native_root;
  receipt.registry_requested_model = row.model;
  receipt.agent_state = row.state;
  receipt.agent_outcome = row.outcome;
  const stateRoot = process.env.XDG_STATE_HOME || path.join(process.env.HOME || "/root",".local","state");
  const reg = JSON.parse(fs.readFileSync(path.join(stateRoot,"opencode","service.json"),"utf8"));
  const base = new URL(reg.url);
  if (base.protocol !== "http:" || !["127.0.0.1","localhost"].includes(base.hostname) ||
      base.username || base.password || base.pathname !== "/" || !Number.isInteger(reg.pid) ||
      reg.pid < 1 || typeof reg.password !== "string" || !reg.password)
    return emit("owned OpenCode service registration invalid");
  const auth = "Basic " + Buffer.from("opencode:" + reg.password).toString("base64");
  const get = async (url) => {
    const response = await fetch(url,{headers:{Authorization:auth},signal:AbortSignal.timeout(15000)});
    if (!response.ok) return null;
    return response.json();
  };
  const info = await get(new URL("/api/info",base));
  if (info?.pid !== reg.pid) return emit("OpenCode service process identity mismatch");
  const session = await get(new URL("/api/session/" + encodeURIComponent(row.native_id),base));
  if (session?.data?.metadata?.agent_id !== id ||
      session?.data?.location?.directory !== row.worktree_path)
    return emit("OpenCode session identity or location mismatch");
  const model = session.data.model;
  if (typeof model?.providerID !== "string" || typeof model?.id !== "string" ||
      !model.providerID || !model.id || model.providerID === "aft")
    return emit("native session has no explicit real model");
  receipt.native_reported_model = model.providerID + "/" + model.id;
  receipt.matches_ui_lead_target = receipt.native_reported_model === target;
  receipt.status = "observed";
  emit();
})().catch(() => emit("OpenCode session read unavailable"));
' "$agent_id" "$RUN_ID" "$AFT_AGENT_FLOW_REPO" "$AFT_REAL_MODEL" "$native_id" "$native_root")"; then
  echo 'owned native model probe could not execute' >&2
  exit 1
fi
printf '%s\n' "$result" >> "$AFT_WORK_DIR/native-models.jsonl"
jq -e --arg id "$agent_id" '.agent_id == $id and .status == "observed" and
  (.native_reported_model | type == "string" and length > 2) and
  .completed_answer_evidence == "not assessed by this read-only probe"' \
  <<< "$result" >/dev/null || { echo 'owned child native model unavailable; see native-models.jsonl' >&2; exit 1; }
printf '%s\n' "$result"
