#!/usr/bin/env bash
# Select a real model in the run-owned Lead's Chat UI before its first Send.
# Agent API calls below only read back identity and the saved model.
set -euo pipefail
# shellcheck source=tests/aft/scripts/agent-flows-ownership.sh
source "$(dirname "${BASH_SOURCE[0]}")/agent-flows-ownership.sh"
agent_flows_check_manifest
: "${AFT_BASE_URL:?runner-owned UI origin required}"
: "${AFT_AGENT_FLOW_REPO:?owned managed repository required}"
: "${AFT_REAL_MODEL:?runner-validated real model required}"
: "${AFT_SESSION:?AFT named browser session required}"
: "${AFT_BROWSER_PROFILES:?runner-owned browser profiles required}"
[[ $# -le 1 ]] || { echo 'expected the current Chat route and at most one matching Lead name' >&2; exit 2; }
[[ "$AFT_SESSION" =~ ^aft-live-(agent-children|lead-chat|recovery-lifecycle)-[0-9]+$ ]] || { echo 'foreign AFT browser session' >&2; exit 2; }
[[ "$AFT_BROWSER_PROFILES" == "${AFT_WORK_DIR%/evidence}/profiles" ]] || { echo 'browser profile ownership mismatch' >&2; exit 2; }
[[ "$AFT_REAL_MODEL" =~ ^[A-Za-z0-9][A-Za-z0-9._/-]*$ && "$AFT_REAL_MODEL" != aft/* ]] || exit 2
[[ -f "$AFT_WORK_DIR/model-selection.json" && ! -L "$AFT_WORK_DIR/model-selection.json" ]] || { echo 'owned model selection is missing' >&2; exit 2; }
jq -e --arg ui "$AFT_BASE_URL" --arg target "$AFT_REAL_MODEL" --arg repo "$AFT_AGENT_FLOW_REPO" \
  '.owned.ui_url == $ui and .required_ui_model == $target and
   .fixture_repo.seed_path == "/workspace/source-repo" and .fixture_repo.managed_path == $repo' \
  "$AFT_WORK_DIR/manifest.json" >/dev/null || { echo 'model selection manifest mismatch' >&2; exit 2; }
jq -e --arg target "$AFT_REAL_MODEL" \
  '.harness == "opencode" and .target == $target and (.displayed_default | type == "string") and (.alternate | type == "string")' \
  "$AFT_WORK_DIR/model-selection.json" >/dev/null || { echo 'model catalog selection mismatch' >&2; exit 2; }

displayed="$(jq -r '.displayed_default' "$AFT_WORK_DIR/model-selection.json" | tr -d '\r')"
alternate="$(jq -r '.alternate' "$AFT_WORK_DIR/model-selection.json" | tr -d '\r')"
[[ -z "$alternate" || "$alternate" =~ ^[A-Za-z0-9][A-Za-z0-9._/-]*$ ]] || exit 2

browser() { agent-browser --session "$AFT_SESSION" "$@"; }
url="$(browser get url | tr -d '\r')"
chat_url="${url%%[?#]*}"
chat_prefix="$AFT_BASE_URL/ws/LOCALMODE/chat/"
[[ "$chat_url" == "$chat_prefix"* ]] || { echo 'browser is not on the owned Lead Chat page' >&2; exit 1; }
agent_id="${chat_url#"$chat_prefix"}"
[[ "$agent_id" =~ ^agt_[a-zA-Z0-9]+$ ]] || { echo 'invalid Chat route Agent ID' >&2; exit 1; }
agent_page="$(curl -fsS --max-time 15 "$AFT_API_URL/api/workspaces/LOCALMODE/v1/agents/$agent_id")" \
  || { echo 'run-owned Lead identity unavailable' >&2; exit 1; }
agent_name="$(jq -r '.name // empty' <<< "$agent_page" | tr -d '\r')"
owned_name="^(aft-child-lead-${RUN_ID}|aft-cancel-lead-${RUN_ID}|aft-isolation-[ab]-${RUN_ID}|aft-lead-${RUN_ID}-(chat|persona|busy)|live-recovery-(restart|archive|sibling|dirty)-${RUN_ID})$"
[[ "$agent_name" =~ $owned_name && ( $# -eq 0 || "$1" == "$agent_name" ) ]] \
  || { echo 'Chat route Lead name is not from this run and suite' >&2; exit 1; }
jq -e --arg id "$agent_id" --arg name "$agent_name" --arg repo "$AFT_AGENT_FLOW_REPO" '
  .agent_id == $id and .name == $name and .harness == "opencode" and
  .repo == $repo and .preset == "lead" and .created_by_kind == "user" and
  .parent_agent_id == null' \
  <<< "$agent_page" >/dev/null || { echo 'Lead is not the owned UI-created OpenCode agent' >&2; exit 1; }
initial="$(jq -r '.model // empty' <<< "$agent_page" | tr -d '\r')"

choose() {
  local id="$1" option
  option="li[role=option][title=\"$id\"]"
  browser wait '[data-chat-provider-model-picker="true"]' >/dev/null
  browser click '[data-chat-provider-model-picker="true"]' >/dev/null
  browser wait '[role=dialog][aria-label="Choose a model"]' >/dev/null
  browser fill '[role=dialog] [role=combobox][aria-label="Search models"]' "$id" >/dev/null
  browser wait "$option" >/dev/null
  browser click "$option" >/dev/null
  # wait --fn needs a synchronous boolean. An unresolved fetch Promise can be
  # truthy before the UI's PATCH is saved, so keep the read-only result on page.
  browser wait --fn "(() => {
    const key = '__aftSavedModel_${agent_id//[^a-zA-Z0-9_]/_}';
    const state = window[key] ||= {model: '$id', ready: false, pending: false};
    if (state.model !== '$id') { state.model = '$id'; state.ready = false; }
    if (!state.ready && !state.pending) {
      state.pending = true;
      fetch('/api/workspaces/LOCALMODE/v1/agents/$agent_id', {cache:'no-store'})
        .then(r => r.ok ? r.json() : null)
        .then(a => { state.ready = a?.agent_id === '$agent_id' &&
          a?.name === '$agent_name' && a?.model === '$id' &&
          a?.model_unverified === false; })
        .catch(() => { state.ready = false; })
        .finally(() => { state.pending = false; });
    }
    return state.ready === true;
  })()" >/dev/null
  local saved
  saved="$(curl -fsS --max-time 15 "$AFT_API_URL/api/workspaces/LOCALMODE/v1/agents/$agent_id")" || return 1
  jq -e --arg id "$agent_id" --arg name "$agent_name" --arg repo "$AFT_AGENT_FLOW_REPO" --arg model "$id" '
    .agent_id == $id and .name == $name and .repo == $repo and .harness == "opencode" and
    .model == $model and .model_unverified == false' \
    <<< "$saved" >/dev/null || { echo 'UI model choice did not persist on the owned Lead' >&2; return 1; }
}

intermediate=""
selection_performed=0
if [[ "$initial" != "$AFT_REAL_MODEL" ]]; then
  if [[ -z "$initial" && "$displayed" == "$AFT_REAL_MODEL" ]]; then
    [[ -n "$alternate" ]] || { echo 'default model needs another catalog choice before it can be saved' >&2; exit 1; }
    choose "$alternate"
    intermediate="$alternate"
    browser reload >/dev/null
    browser wait '[data-chat-provider-model-picker="true"]' >/dev/null
  fi
  choose "$AFT_REAL_MODEL"
  selection_performed=1
fi
final="$(curl -fsS --max-time 15 "$AFT_API_URL/api/workspaces/LOCALMODE/v1/agents/$agent_id")" \
  || { echo 'final saved model readback unavailable' >&2; exit 1; }
jq -e --arg id "$agent_id" --arg name "$agent_name" --arg repo "$AFT_AGENT_FLOW_REPO" --arg model "$AFT_REAL_MODEL" '
  .agent_id == $id and .name == $name and .repo == $repo and .harness == "opencode" and
  .model == $model and .model_unverified == false' \
  <<< "$final" >/dev/null || { echo 'final saved model does not match the UI target' >&2; exit 1; }
observed="$(jq -r '.model' <<< "$final" | tr -d '\r')"
jq -cn --arg time "$(date -u +%FT%TZ)" --arg run "$RUN_ID" --arg session "$AFT_SESSION" \
  --arg agent "$agent_id" --arg name "$agent_name" --arg initial "$initial" \
  --arg intermediate "$intermediate" --arg target "$AFT_REAL_MODEL" --arg observed "$observed" \
  --argjson performed "$selection_performed" \
  '{time:$time,run_id:$run,session:$session,agent_id:$agent,name:$name,
    initial_saved_model:(if $initial == "" then null else $initial end),
    intermediate_ui_model:(if $intermediate == "" then null else $intermediate end),
    target_model:$target,ui_selected_model:(if $performed == 1 then $target else null end),
    observed_saved_model:$observed}' \
  >> "$AFT_WORK_DIR/model-selections.jsonl"
printf 'saved real model %s for %s\n' "$AFT_REAL_MODEL" "$agent_id"
