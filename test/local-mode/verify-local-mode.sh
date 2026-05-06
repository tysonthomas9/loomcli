#!/usr/bin/env bash
set -euo pipefail

API_URL="${LOCAL_MODE_API_URL:-http://localhost:8282}"
WORKSPACE="${LOOM_WORKSPACE:-LOCALMODE}"
PLAN_TASK_ID="${LOOM_LOCAL_MODE_PLAN_TASK_ID:-LM-PLAN-1}"
CODE_TASK_ID="${LOOM_LOCAL_MODE_CODE_TASK_ID:-LM-CODE-1}"
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
  sessions_json "$task_id" | jq -r \
    '(.data.sessions // .sessions // [])[0].session_id // empty'
}

task_has_completed_session() {
  task_id="$1"
  sessions_json "$task_id" | jq -e '
    (.data.sessions // .sessions // []) |
    any(.status == "completed" and .is_active == false)
  ' >/dev/null
}

task_has_transcript_flag() {
  task_id="$1"
  sessions_json "$task_id" | jq -e '
    (.data.sessions // .sessions // []) |
    any(.has_transcript == true)
  ' >/dev/null
}

task_has_diff_flag() {
  task_id="$1"
  sessions_json "$task_id" | jq -e '
    (.data.sessions // .sessions // []) |
    any(.has_diff == true or ((.files_changed // 0) > 0))
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
  session_id="$(first_session_id "$CODE_TASK_ID")"
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

echo "[local-mode-verify] api=${API_URL} workspace=${WORKSPACE}"
echo "[local-mode-verify] planner=${PLAN_TASK_ID} coder=${CODE_TASK_ID}"

wait_for "Loom API is reachable" api_reachable

wait_for "planner task moved to review" issue_status_is "$PLAN_TASK_ID" review
wait_for "planner task has design" issue_has_design "$PLAN_TASK_ID"
wait_for "planner completed session exists" task_has_completed_session "$PLAN_TASK_ID"
wait_for "planner transcript flag is set" task_has_transcript_flag "$PLAN_TASK_ID"
wait_for "planner transcript has entries" transcript_has_entries "$PLAN_TASK_ID"

wait_for "coder task is closed" issue_status_closed "$CODE_TASK_ID"
wait_for "coder completed session exists" task_has_completed_session "$CODE_TASK_ID"
wait_for "coder transcript flag is set" task_has_transcript_flag "$CODE_TASK_ID"
wait_for "coder transcript has entries" transcript_has_entries "$CODE_TASK_ID"
wait_for "coder diff metadata is present" task_has_diff_flag "$CODE_TASK_ID"
wait_for "coder diff contains local-mode artifact" code_diff_mentions_output_file

echo "[local-mode-verify] local-mode daemon, agent, transcript, and diff flow verified"
