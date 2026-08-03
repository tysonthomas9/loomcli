#!/usr/bin/env bash
set -euo pipefail

API_URL="${LOCAL_MODE_API_URL:-http://localhost:${LOCAL_MODE_API_PORT:-8282}}"
RUN_MANIFEST_JSON="${LOCAL_MODE_RUN_MANIFEST_JSON:-}"
EXPECTED_BACKEND="${LOCAL_MODE_EXPECTED_BACKEND:-}"
TIMEOUT_SECONDS="${LOCAL_MODE_VERIFY_TIMEOUT:-240}"
POLL_SECONDS="${LOCAL_MODE_VERIFY_POLL_SECONDS:-2}"

API_URL="${API_URL%/}"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "[local-mode-verify] FATAL: $1 is required" >&2
    exit 127
  fi
}

curl_json() {
  curl -fsS --max-time 10 "$1"
}

api_get() {
  curl_json "${API_URL}/$1"
}

manifest_field() {
  field="$1"
  printf '%s' "$RUN_MANIFEST_JSON" | jq -er --arg field "$field" '.[$field] | select(type == "string" and length > 0)'
}

manifest_optional_field() {
  field="$1"
  printf '%s' "$RUN_MANIFEST_JSON" | jq -r --arg field "$field" '.[$field] // "" | tostring'
}

positive_utc_epoch() {
  printf '%s' "$1" | jq -Rer '
    def utc_epoch:
      if type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\\.[0-9]+)?Z$") then
        (sub("\\.[0-9]+Z$"; "Z") | try fromdateiso8601 catch -1)
      else -1 end;
    utc_epoch | select(. > 0)
  '
}

load_run_manifest() {
  if [ -z "$RUN_MANIFEST_JSON" ]; then
    echo "[local-mode-verify] FATAL: LOCAL_MODE_RUN_MANIFEST_JSON is required; use make local-mode-verify to read it from the running stack" >&2
    return 1
  fi
  CHECKOUT_ID="$(manifest_field checkout_id)"
  SOURCE_ROOT="$(manifest_field source_root)"
  COMPOSE_PROJECT="$(manifest_field compose_project)"
  RUN_ID="$(manifest_field run_id)"
  RUN_STARTED_AT="$(manifest_field started_at)"
  BACKEND="$(manifest_field backend)"
  PLANE="$(manifest_optional_field plane)"
  WORKSPACE="$(manifest_field workspace)"
  PLAN_AGENT_RECORD_ID="$(manifest_optional_field plan_agent_record_id)"
  CODE_AGENT_RECORD_ID="$(manifest_optional_field code_agent_record_id)"
  PLAN_AGENT_NAME="$(manifest_optional_field plan_agent_name)"
  CODE_AGENT_NAME="$(manifest_optional_field code_agent_name)"
  PLAN_AGENT_ROLE="$(manifest_optional_field plan_agent_role)"
  CODE_AGENT_ROLE="$(manifest_optional_field code_agent_role)"
  PLAN_BINDING_ID="$(manifest_optional_field plan_binding_id)"
  CODE_BINDING_ID="$(manifest_optional_field code_binding_id)"
  PLAN_TASK_ID="$(manifest_field plan_task_id)"
  CODE_TASK_ID="$(manifest_field code_task_id)"
  PLAN_TASK_TITLE="$(manifest_field plan_task_title)"
  CODE_TASK_TITLE="$(manifest_field code_task_title)"

  if ! RUN_STARTED_EPOCH="$(positive_utc_epoch "$RUN_STARTED_AT")"; then
    echo "[local-mode-verify] FATAL: manifest started_at must be a valid positive UTC timestamp, got ${RUN_STARTED_AT}" >&2
    return 1
  fi

  if [ -n "${LOCAL_MODE_CHECKOUT_ID:-}" ] && [ "$CHECKOUT_ID" != "$LOCAL_MODE_CHECKOUT_ID" ]; then
    echo "[local-mode-verify] FATAL: manifest checkout ${CHECKOUT_ID} does not match requested checkout ${LOCAL_MODE_CHECKOUT_ID}" >&2
    return 1
  fi
  if [ -n "${LOCAL_MODE_SOURCE_ROOT:-}" ] && [ "$SOURCE_ROOT" != "$LOCAL_MODE_SOURCE_ROOT" ]; then
    echo "[local-mode-verify] FATAL: manifest source root ${SOURCE_ROOT} does not match requested root ${LOCAL_MODE_SOURCE_ROOT}" >&2
    return 1
  fi
  if [ -n "${LOCAL_MODE_COMPOSE_PROJECT:-}" ] && [ "$COMPOSE_PROJECT" != "$LOCAL_MODE_COMPOSE_PROJECT" ]; then
    echo "[local-mode-verify] FATAL: manifest project ${COMPOSE_PROJECT} does not match requested project ${LOCAL_MODE_COMPOSE_PROJECT}" >&2
    return 1
  fi
  if [ -n "$EXPECTED_BACKEND" ] && [ "$BACKEND" != "$EXPECTED_BACKEND" ]; then
    echo "[local-mode-verify] FATAL: manifest backend ${BACKEND} does not match expected backend ${EXPECTED_BACKEND}" >&2
    return 1
  fi
}

agents_json() {
  api_get "api/workspaces/${WORKSPACE}/agents"
}

prompt_agents_match_manifest() {
  [ "$PLANE" = "ts" ]
  [ "$PLAN_AGENT_RECORD_ID" != "" ]
  [ "$CODE_AGENT_RECORD_ID" != "" ]
  agents_json | jq -e \
    --arg plan_id "$PLAN_AGENT_RECORD_ID" --arg code_id "$CODE_AGENT_RECORD_ID" \
    --arg plan_name "$PLAN_AGENT_NAME" --arg code_name "$CODE_AGENT_NAME" \
    --arg plan_role "$PLAN_AGENT_ROLE" --arg code_role "$CODE_AGENT_ROLE" \
    --arg plan_binding "$PLAN_BINDING_ID" --arg code_binding "$CODE_BINDING_ID" '
      (.data // []) as $agents |
      ([$agents[] | select(.id == $plan_id and .name == $plan_name and .kind == "prompt" and .enabled == true and
        .behavior.role_name == $plan_role and any(.bindings[]?; .binding_id == $plan_binding and
          .source_kind == "internal" and .enabled == true and
          (.event_type_patterns | index("internal.task.ready") != null)))] | length) == 1 and
      ([$agents[] | select(.id == $code_id and .name == $code_name and .kind == "prompt" and .enabled == true and
        .behavior.role_name == $code_role and any(.bindings[]?; .binding_id == $code_binding and
          .source_kind == "internal" and .enabled == true and
          (.event_type_patterns | index("internal.task.ready") != null)))] | length) == 1
    ' >/dev/null
}

prompt_agents_precede_tasks() {
  agents="$(agents_json)"
  plan_agent_created="$(printf '%s' "$agents" | jq -r --arg id "$PLAN_AGENT_RECORD_ID" '.data[]? | select(.id == $id) | .created_at // empty')"
  code_agent_created="$(printf '%s' "$agents" | jq -r --arg id "$CODE_AGENT_RECORD_ID" '.data[]? | select(.id == $id) | .created_at // empty')"
  plan_task_created="$(issue_json "$PLAN_TASK_ID" | jq -r '.data.created_at // .created_at // empty')"
  code_task_created="$(issue_json "$CODE_TASK_ID" | jq -r '.data.created_at // .created_at // empty')"
  plan_agent_epoch="$(positive_utc_epoch "$plan_agent_created")"
  code_agent_epoch="$(positive_utc_epoch "$code_agent_created")"
  plan_task_epoch="$(positive_utc_epoch "$plan_task_created")"
  code_task_epoch="$(positive_utc_epoch "$code_task_created")"
  [ "$plan_agent_epoch" -le "$plan_task_epoch" ]
  [ "$plan_agent_epoch" -le "$code_task_epoch" ]
  [ "$code_agent_epoch" -le "$plan_task_epoch" ]
  [ "$code_agent_epoch" -le "$code_task_epoch" ]
}

wait_for() {
  label="$1"
  shift
  deadline=$((SECONDS + TIMEOUT_SECONDS))
  while true; do
    if "$@" >/tmp/loom-local-mode-verify.out 2>/tmp/loom-local-mode-verify.err; then
      echo "[local-mode-verify] ok: ${label}"
      return 0
    fi
    if [ "$SECONDS" -ge "$deadline" ]; then
      echo "[local-mode-verify] FATAL: timed out waiting for ${label}" >&2
      if [ -s /tmp/loom-local-mode-verify.err ]; then
        cat /tmp/loom-local-mode-verify.err >&2
      fi
      if [ -s /tmp/loom-local-mode-verify.out ]; then
        cat /tmp/loom-local-mode-verify.out >&2
      fi
      return 1
    fi
    sleep "$POLL_SECONDS"
  done
}

issue_json() {
  task_id="$1"
  api_get "api/workspaces/${WORKSPACE}/issues/${task_id}"
}

manifest_tasks_exist() {
  issue_json "$PLAN_TASK_ID" >/dev/null
  issue_json "$CODE_TASK_ID" >/dev/null
}

issue_matches_run() {
  task_id="$1"
  expected_title="$2"
  issue_json "$task_id" | jq -e --arg title "$expected_title" --argjson started_epoch "$RUN_STARTED_EPOCH" '
    def utc_epoch:
      if type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\\.[0-9]+)?Z$") then
        (sub("\\.[0-9]+Z$"; "Z") | try fromdateiso8601 catch -1)
      else -1 end;
    (.data // .) as $issue |
    $issue.title == $title and
    (($issue.created_at | utc_epoch) >= $started_epoch)
  ' >/dev/null
}

issue_status_is() {
  task_id="$1"
  expected="$2"
  json="$(issue_json "$task_id")"
  printf '%s' "$json" | jq -e --arg expected "$expected" \
    '(.data.status // .status // "") == $expected' >/dev/null
}

issue_status_closed() {
  task_id="$1"
  json="$(issue_json "$task_id")"
  printf '%s' "$json" | jq -e \
    '(.data.status // .status // "") as $status | $status == "closed" or $status == "done"' >/dev/null
}

issue_has_local_branch_review_ref() {
  task_id="$1"
  issue_json "$task_id" | jq -e --arg task_id "$task_id" '
    (.data // .) as $issue |
    $issue.status == "review" and
    (($issue.external_ref // "") | test("^local-branch:loom/" + $task_id + "@[0-9a-f]{40}$"))
  ' >/dev/null
}

issue_is_unassigned() {
  task_id="$1"
  json="$(issue_json "$task_id")"
  printf '%s' "$json" | jq -e \
    '(.data.assignee // .assignee // "") == ""' >/dev/null
}

issue_has_design() {
  task_id="$1"
  json="$(issue_json "$task_id")"
  printf '%s' "$json" | jq -e \
    '((.data.design // .design // "") | length) > 0' >/dev/null
}

sessions_json() {
  task_id="$1"
  api_get "api/workspaces/${WORKSPACE}/tasks/${task_id}/sessions"
}

first_session_id() {
  task_id="$1"
  sessions_json "$task_id" | jq -r --argjson started_epoch "$RUN_STARTED_EPOCH" \
    'def utc_epoch:
      if type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\\.[0-9]+)?Z$") then
        (sub("\\.[0-9]+Z$"; "Z") | try fromdateiso8601 catch -1)
      else -1 end;
    (.data.sessions // .sessions // []) |
    map(select(.status == "completed" and .is_active == false and
      ((.started_at | utc_epoch) >= $started_epoch))) |
    sort_by(.ended_at // .started_at // "") |
    reverse |
    .[0].session_id // empty'
}

code_diff_session_id() {
  sessions_json "$CODE_TASK_ID" | jq -r --argjson started_epoch "$RUN_STARTED_EPOCH" \
    'def utc_epoch:
      if type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\\.[0-9]+)?Z$") then
        (sub("\\.[0-9]+Z$"; "Z") | try fromdateiso8601 catch -1)
      else -1 end;
    (.data.sessions // .sessions // []) |
    map(select(.status == "completed" and (.has_diff == true or ((.files_changed // 0) > 0)) and
      ((.started_at | utc_epoch) >= $started_epoch))) |
    sort_by(.ended_at // .started_at // "") |
    reverse |
    .[0].session_id // empty'
}

task_has_completed_session() {
  task_id="$1"
  sessions_json "$task_id" | jq -e --argjson started_epoch "$RUN_STARTED_EPOCH" '
    def utc_epoch:
      if type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\\.[0-9]+)?Z$") then
        (sub("\\.[0-9]+Z$"; "Z") | try fromdateiso8601 catch -1)
      else -1 end;
    (.data.sessions // .sessions // []) |
    any(.status == "completed" and .is_active == false and
      ((.started_at | utc_epoch) >= $started_epoch))
  ' >/dev/null
}

task_has_expected_backend_exit_zero() {
  task_id="$1"
  sessions_json "$task_id" | jq -e --arg backend "$BACKEND" --argjson started_epoch "$RUN_STARTED_EPOCH" '
    def utc_epoch:
      if type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\\.[0-9]+)?Z$") then
        (sub("\\.[0-9]+Z$"; "Z") | try fromdateiso8601 catch -1)
      else -1 end;
    (.data.sessions // .sessions // []) |
    any(.status == "completed" and .is_active == false and .backend == $backend and .exit_code == 0 and
      ((.started_at | utc_epoch) >= $started_epoch))
  ' >/dev/null
}

task_has_transcript_flag() {
  task_id="$1"
  sessions_json "$task_id" | jq -e --argjson started_epoch "$RUN_STARTED_EPOCH" '
    def utc_epoch:
      if type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\\.[0-9]+)?Z$") then
        (sub("\\.[0-9]+Z$"; "Z") | try fromdateiso8601 catch -1)
      else -1 end;
    (.data.sessions // .sessions // []) |
    any(.has_transcript == true and
      ((.started_at | utc_epoch) >= $started_epoch))
  ' >/dev/null
}

task_has_diff_flag() {
  task_id="$1"
  sessions_json "$task_id" | jq -e --argjson started_epoch "$RUN_STARTED_EPOCH" '
    def utc_epoch:
      if type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\\.[0-9]+)?Z$") then
        (sub("\\.[0-9]+Z$"; "Z") | try fromdateiso8601 catch -1)
      else -1 end;
    (.data.sessions // .sessions // []) |
    any((.has_diff == true or ((.files_changed // 0) > 0)) and
      ((.started_at | utc_epoch) >= $started_epoch))
  ' >/dev/null
}

transcript_has_entries() {
  task_id="$1"
  session_id="$(first_session_id "$task_id")"
  [ "$session_id" != "" ]
  api_get "api/workspaces/${WORKSPACE}/tasks/${task_id}/sessions/${session_id}/transcript" |
    jq -e '((.data.entries // .entries // []) | length) > 0' >/dev/null
}

code_diff_mentions_output_file() {
  session_id="$(code_diff_session_id)"
  [ "$session_id" != "" ]
  curl -fsS --max-time 10 \
    "${API_URL}/api/workspaces/${WORKSPACE}/tasks/${CODE_TASK_ID}/sessions/${session_id}/diff" |
    grep -F "local-mode-agent-output.txt" >/dev/null
}

api_reachable() {
  api_get "api/config" >/dev/null
}

require_cmd curl
require_cmd jq
load_run_manifest

echo "[local-mode-verify] api=${API_URL} workspace=${WORKSPACE} backend=${BACKEND} project=${COMPOSE_PROJECT} root=${SOURCE_ROOT} checkout=${CHECKOUT_ID} run=${RUN_ID} started=${RUN_STARTED_AT}"
echo "[local-mode-verify] planner=${PLAN_TASK_ID} coder=${CODE_TASK_ID}"

wait_for "Loom API is reachable" api_reachable
wait_for "manifest-owned local-mode tasks exist" manifest_tasks_exist
wait_for "planner task belongs to this run" issue_matches_run "$PLAN_TASK_ID" "$PLAN_TASK_TITLE"
wait_for "coder task belongs to this run" issue_matches_run "$CODE_TASK_ID" "$CODE_TASK_TITLE"
if [ "$PLANE" = "ts" ]; then
  wait_for "public planner/coder prompt agents match the manifest" prompt_agents_match_manifest
  wait_for "public planner/coder prompt agents were seeded before tasks" prompt_agents_precede_tasks
fi

wait_for "planner task moved to review" issue_status_is "$PLAN_TASK_ID" review
wait_for "planner task released its assignee" issue_is_unassigned "$PLAN_TASK_ID"
wait_for "planner task has design" issue_has_design "$PLAN_TASK_ID"
wait_for "planner completed session exists" task_has_completed_session "$PLAN_TASK_ID"
wait_for "planner used ${BACKEND} and exited zero" task_has_expected_backend_exit_zero "$PLAN_TASK_ID"
wait_for "planner transcript flag is set" task_has_transcript_flag "$PLAN_TASK_ID"
wait_for "planner transcript has entries" transcript_has_entries "$PLAN_TASK_ID"

if [ "$PLANE" = "ts" ]; then
  wait_for "coder task moved to review" issue_status_is "$CODE_TASK_ID" review
  wait_for "coder task released its assignee" issue_is_unassigned "$CODE_TASK_ID"
  wait_for "coder task has a local-branch review reference" issue_has_local_branch_review_ref "$CODE_TASK_ID"
else
  wait_for "coder task is closed" issue_status_closed "$CODE_TASK_ID"
fi
wait_for "coder completed session exists" task_has_completed_session "$CODE_TASK_ID"
wait_for "coder used ${BACKEND} and exited zero" task_has_expected_backend_exit_zero "$CODE_TASK_ID"
wait_for "coder transcript flag is set" task_has_transcript_flag "$CODE_TASK_ID"
wait_for "coder transcript has entries" transcript_has_entries "$CODE_TASK_ID"
wait_for "coder diff metadata is present" task_has_diff_flag "$CODE_TASK_ID"
wait_for "coder diff contains local-mode artifact" code_diff_mentions_output_file

if [ "$PLANE" = "ts" ]; then
  echo "[local-mode-verify] local-mode TS prompt-agent, transcript, and diff flow verified"
else
  echo "[local-mode-verify] local-mode daemon, agent, transcript, and diff flow verified"
fi
