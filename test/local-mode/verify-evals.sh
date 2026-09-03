#!/usr/bin/env bash
# shellcheck disable=SC2016
set -euo pipefail

MODE="${1:-${LOCAL_MODE_EVALS_MODE:-plain}}"
WORKSPACE="${LOOM_WORKSPACE:-LOCALMODE}"
API_URL="${LOCAL_MODE_API_URL:-http://localhost:${LOCAL_MODE_API_PORT:-8282}}"
FLEETDB_URL="${LOCAL_MODE_FLEETDB_URL:-http://localhost:${LOCAL_MODE_FLEETDB_PORT:-8280}}"
UI_URL="${LOCAL_MODE_UI_URL:-http://localhost:${LOCAL_MODE_UI_PORT:-8283}}"
SCHEDULE="${LOCAL_MODE_EVALS_SCHEDULE:-* * * * *}"
PROMPT_VERSION="${LOCAL_MODE_EVALS_PROMPT_VERSION:-v1}"
EXPECTED_JUDGE_MODEL="${LOOM_EVAL_MODEL:-gpt-5.6-sol}"
TIMEOUT_SECONDS="${LOCAL_MODE_EVALS_VERIFY_TIMEOUT:-360}"
CRON_TIMEOUT_SECONDS="${LOCAL_MODE_EVALS_CRON_TIMEOUT:-150}"
POLL_SECONDS="${LOCAL_MODE_EVALS_POLL_SECONDS:-2}"
COMPOSE_PROJECT="${LOCAL_MODE_COMPOSE_PROJECT:-loomcli-local-mode}"
COMPOSE_FILES="${LOCAL_MODE_COMPOSE_FILES:-}"
LOG_TAIL="${LOCAL_MODE_EVALS_LOG_TAIL:-200}"
# Deliberately NOT named TMPDIR: exporting/overriding TMPDIR poisons child
# processes (podman resolves its API socket under TMPDIR and the long path
# exceeds the unix socket limit).
EVALS_WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/loom-local-mode-evals.XXXXXX")"

API_URL="${API_URL%/}"
FLEETDB_URL="${FLEETDB_URL%/}"
UI_URL="${UI_URL%/}"

COMPOSE_CMD=()
COMPOSE_ARGS=()
COMPOSE_READY=0
BASELINE_RUN_IDS_JSON="[]"
SELECTED_SESSION_ID=""

cleanup() {
  status=$?
  if [ "$status" -ne 0 ]; then
    dump_logs
  fi
  rm -rf "$EVALS_WORKDIR"
}
trap cleanup EXIT

log() {
  printf '[local-mode-evals] %s\n' "$*"
}

pass() {
  printf '[local-mode-evals] PASS: %s\n' "$*"
}

skip() {
  printf '[local-mode-evals] SKIPPED: %s\n' "$*"
}

fatal() {
  printf '[local-mode-evals] FAIL: %s\n' "$*" >&2
  exit 1
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    fatal "$1 is required"
  fi
}

select_compose() {
  if [ "$COMPOSE_READY" -eq 1 ]; then
    return 0
  fi
  if [ -n "${LOCAL_MODE_COMPOSE:-}" ]; then
    # shellcheck disable=SC2206
    COMPOSE_CMD=(${LOCAL_MODE_COMPOSE})
  elif command -v podman >/dev/null 2>&1 && podman compose version >/dev/null 2>&1; then
    COMPOSE_CMD=(podman compose)
  elif command -v podman-compose >/dev/null 2>&1; then
    COMPOSE_CMD=(podman-compose)
  elif command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    COMPOSE_CMD=(docker compose)
  else
    return 1
  fi

  COMPOSE_ARGS=(-p "$COMPOSE_PROJECT" -f test/local-mode/docker-compose.yml)
  if [ "$MODE" = "codex" ]; then
    COMPOSE_ARGS+=(-f test/local-mode/docker-compose.codex.yml)
  fi
  for file in $COMPOSE_FILES; do
    COMPOSE_ARGS+=(-f "$file")
  done
  COMPOSE_READY=1
}

compose_run() {
  select_compose || fatal "podman compose or docker compose is required to exec into the local-mode stack"
  "${COMPOSE_CMD[@]}" "${COMPOSE_ARGS[@]}" "$@"
}

dump_logs() {
  if ! select_compose >/dev/null 2>&1; then
    log "could not select compose command for failure logs"
    return 0
  fi
  {
    echo "[local-mode-evals] --- compose logs tail (${LOG_TAIL}) ---"
    compose_run logs --tail="$LOG_TAIL" loom-local fleet-db ui-local
    echo "[local-mode-evals] --- end compose logs ---"
  } >&2 || true
}

curl_json() {
  curl -fsS --max-time 15 "$1"
}

api_get() {
  curl_json "${API_URL}/$1"
}

fleet_get() {
  # fleet-db's dev-mode auth (--auth-dev-mode) authenticates via X-Actor.
  curl -fsS --max-time 15 -H "X-Actor: ${FLEETDB_ACTOR:-local-mode-evals-verify}" "${FLEETDB_URL}/$1"
}

exec_loom() {
  # Run from the registered workspace runtime dir, not the image WORKDIR
  # (/workspace): workspace-runtime-dir resolution is cwd-sensitive, and
  # bundle materialization must land where serve's executor reads it
  # (/root/.loom/workspaces/<ws>).
  compose_run exec -T --workdir "/root/.loom/workspaces/${WORKSPACE}" loom-local env LOOM_WORKSPACE="$WORKSPACE" "$@"
}

wait_for() {
  label="$1"
  timeout="$2"
  shift 2
  deadline=$((SECONDS + timeout))
  while true; do
    if "$@" >"$EVALS_WORKDIR/wait.out" 2>"$EVALS_WORKDIR/wait.err"; then
      pass "$label"
      return 0
    fi
    if [ "$SECONDS" -ge "$deadline" ]; then
      printf '[local-mode-evals] last stdout for %s:\n' "$label" >&2
      sed -n '1,160p' "$EVALS_WORKDIR/wait.out" >&2 || true
      printf '[local-mode-evals] last stderr for %s:\n' "$label" >&2
      sed -n '1,160p' "$EVALS_WORKDIR/wait.err" >&2 || true
      fatal "timed out waiting for ${label}"
    fi
    sleep "$POLL_SECONDS"
  done
}

assert_jq() {
  label="$1"
  json="$2"
  shift 2
  if printf '%s' "$json" | jq -e "$@" >/dev/null; then
    pass "$label"
    return 0
  fi
  printf '%s\n' "$json" >"$EVALS_WORKDIR/assertion.json"
  jq . "$EVALS_WORKDIR/assertion.json" >&2 || cat "$EVALS_WORKDIR/assertion.json" >&2
  fatal "$label"
}

api_reachable() {
  api_get "api/config" >/dev/null
}

fleet_reachable() {
  fleet_get "api/v1/${WORKSPACE}/agent-sessions?limit=1" >/dev/null
}

capture_baseline_runs() {
  runs_json="$(fleet_get "api/v1/${WORKSPACE}/driver-runs?driver_id=session-eval-agent&limit=200")" ||
    fatal "list driver-runs for run baseline — the stack's fleet-db must serve the loom platform routes (driver-runs); a control-plane-only fleet-db build cannot run the eval loop"
  BASELINE_RUN_IDS_JSON="$(printf '%s' "$runs_json" | jq -c '[.driver_runs[]?.run_id]')"
}

enable_eval_cron() {
  log "provisioning eval cron with schedule ${SCHEDULE}"
  if exec_loom loom --workspace "$WORKSPACE" evals enable --schedule "$SCHEDULE" >"$EVALS_WORKDIR/evals-enable.out" 2>"$EVALS_WORKDIR/evals-enable.err"; then
    sed -n '1,20p' "$EVALS_WORKDIR/evals-enable.out"
    pass "loom evals enable completed"
    return 0
  fi
  sed -n '1,160p' "$EVALS_WORKDIR/evals-enable.err" >&2 || true
  sed -n '1,160p' "$EVALS_WORKDIR/evals-enable.out" >&2 || true
  fatal "loom evals enable failed"
}

assert_eval_provisioning() {
  workflow_json="$(exec_loom loom --workspace "$WORKSPACE" workflow list --json)"
  assert_jq "session-eval-agent workflow driver exists" "$workflow_json" '
    (.workflows // []) |
    any((.name == "session-eval-agent" or .driver_id == "session-eval-agent") and ((.active_version_id // "") != ""))
  '

  binding_json="$(exec_loom loom --workspace "$WORKSPACE" trigger bindings show binding-cron-session-eval-agent)"
  assert_jq "cron binding exists with Enabled=true" "$binding_json" \
    --arg schedule "$SCHEDULE" '
      .binding_id == "binding-cron-session-eval-agent" and
      .route_key == "cron.session-eval-agent" and
      .driver_id == "session-eval-agent" and
      .enabled == true and
      .schedule == $schedule
    '
}

eval_backend_unavailable_run_exists() {
  fleet_get "api/v1/${WORKSPACE}/driver-runs?driver_id=session-eval-agent&limit=200" |
    jq -e --argjson baseline "$BASELINE_RUN_IDS_JSON" '
      (.driver_runs // []) |
      any(
        (.run_id as $runID | ($baseline | index($runID)) == null) and
        .driver_id == "session-eval-agent" and
        .status == "failed" and
        ((.error_class // "") == "eval_backend_unavailable" or ((.error_class // "") | contains("backend_unavailable")))
      )
    ' >/dev/null
}

assert_no_eval_status_stamps() {
  sessions_json="$(fleet_get "api/v1/${WORKSPACE}/agent-sessions?limit=1000")"
  assert_jq "no session has eval_status metadata after backend-unavailable defer" "$sessions_json" '
    (.agent_sessions // []) |
    all(((.metadata // {}).eval_status // "") == "")
  '
}

assert_session_evals_empty() {
  evals_json="$(fleet_get "api/v1/${WORKSPACE}/session-evals?limit=1000")"
  assert_jq "session-evals list is empty" "$evals_json" '
    ((.session_evals // []) | length) == 0
  '
}

assert_preflight_task_runs_have_no_sessions() {
  task_runs_json="$(fleet_get "api/v1/${WORKSPACE}/task-runs?task_id=session-eval-preflight&limit=1000")"
  sessions_json="$(fleet_get "api/v1/${WORKSPACE}/agent-sessions?limit=1000")"
  assert_jq "preflight TaskRuns produce no sessions" "$sessions_json" \
    --argjson task_runs "$task_runs_json" '
      [($task_runs.task_runs // [])[]?.task_run_id | select(type == "string" and length > 0)] as $preflight_ids |
      ($preflight_ids | length) > 0 and
      all(.agent_sessions[]?; (.task_run_id // "") as $task_run_id | ($preflight_ids | index($task_run_id)) == null)
    '
}

assert_no_legacy_flue_session_ids() {
  sessions_json="$(fleet_get "api/v1/${WORKSPACE}/agent-sessions?limit=1000")"
  assert_jq "no session id has the legacy flue- prefix" "$sessions_json" '
    all(.agent_sessions[]?; ((.session_id // "") | startswith("flue-") | not))
  '
}

judge_session_matches_selected() {
  sid="$1"
  fleet_get "api/v1/${WORKSPACE}/agent-sessions?kind=judge&status=completed&limit=1000" |
    jq -e --arg sid "$sid" --arg prompt "$PROMPT_VERSION" '
      any(
        .agent_sessions[]?;
        .kind == "judge" and
        ((.metadata // {}).judged_session_id // "") == $sid and
        ((.metadata // {}).judge_prompt_version // "") == $prompt
      )
    ' >/dev/null
}

find_completed_task_session_with_ref() {
  fleet_get "api/v1/${WORKSPACE}/agent-sessions?status=completed&limit=1000" |
    jq -r '
      [
        .agent_sessions[]? |
        select(.kind == "task") |
        select(.status == "completed") |
        select(((.task_id // "") | startswith("session-eval-")) | not) |
        select(((.metadata // {}).transcript_ref // "") != "")
      ] |
      sort_by(.finished_at // .updated_at // .created_at // "") |
      reverse |
      .[0].session_id // empty
    ' >"$EVALS_WORKDIR/session-id"
  [ -s "$EVALS_WORKDIR/session-id" ] && [ "$(cat "$EVALS_WORKDIR/session-id")" != "" ]
}

select_completed_task_session_with_ref() {
  SELECTED_SESSION_ID="$(cat "$EVALS_WORKDIR/session-id")"
  log "selected task session ${SELECTED_SESSION_ID}"
}

assert_workspace_sessions_read_path() {
  sid="$1"
  sessions_json="$(api_get "api/workspaces/${WORKSPACE}/sessions?status=completed&kind=task&limit=200")"
  assert_jq "workspace sessions endpoint returns selected session and total" "$sessions_json" \
    --arg sid "$sid" '
      .success == true and
      ((.data.total // 0) >= 1) and
      any(.data.sessions[]?; .session_id == $sid)
    '

  transcript_json="$(api_get "api/workspaces/${WORKSPACE}/sessions/${sid}/transcript")"
  assert_jq "workspace session transcript endpoint serves entries" "$transcript_json" '
    .success == true and
    (((.data.entries // .entries // []) | length) > 0)
  '
}

session_eval_record_valid() {
  sid="$1"
  eval_id="eval-${sid}-${PROMPT_VERSION}"
  fleet_get "api/v1/${WORKSPACE}/session-evals/${eval_id}" |
    jq -e --arg sid "$sid" --arg eval_id "$eval_id" --arg prompt "$PROMPT_VERSION" --arg model "$EXPECTED_JUDGE_MODEL" '
      def score_ok: type == "number" and (floor == .) and . >= 0 and . <= 100;
      def valid_tag:
        . as $tag |
        ([
          "false_success_claim",
          "incomplete_task",
          "instruction_violation",
          "idle_wait",
          "redundant_work",
          "tool_misuse",
          "hallucinated_state",
          "scope_creep",
          "env_or_dependency_failure",
          "killed_or_truncated",
          "unsafe_operation",
          "verification_skipped"
        ] | index($tag)) != null
        or ($tag | test("^other:[a-z][a-z0-9]*(?:_[a-z0-9]+)*$"));
      .eval_id == $eval_id and
      .session_id == $sid and
      .judge_prompt_version == $prompt and
      .judge_model == $model and
      ([
        .scores.outcome_success,
        .scores.instruction_adherence,
        .scores.efficiency,
        .scores.tool_use_quality
      ] | all(score_ok)) and
      ([
        .score_rationales.outcome_success,
        .score_rationales.instruction_adherence,
        .score_rationales.efficiency,
        .score_rationales.tool_use_quality
      ] | all(type == "string" and length > 0)) and
      ((.error_taxonomy_tags // []) | all(valid_tag)) and
      (.eval_cost.input_tokens | type == "number") and
      (.eval_cost.output_tokens | type == "number") and
      (.eval_cost.total_tokens | type == "number")
    ' >/dev/null
}

assert_session_eval_status_done() {
  sid="$1"
  session_json="$(fleet_get "api/v1/${WORKSPACE}/agent-sessions/${sid}")"
  assert_jq "session metadata has eval_status=done" "$session_json" \
    --arg prompt "$PROMPT_VERSION" '
      ((.metadata // {}).eval_status // "") == "done" and
      ((.metadata // {}).eval_prompt_version // "") == $prompt
    '
}

assert_eval_cost_total_tokens() {
  sid="$1"
  eval_id="eval-${sid}-${PROMPT_VERSION}"
  eval_json="$(fleet_get "api/v1/${WORKSPACE}/session-evals/${eval_id}")"
  assert_jq "codex eval_cost.total_tokens is greater than zero" "$eval_json" '
    (.eval_cost.total_tokens | type == "number" and . > 0)
  '
}

assert_eval_rollup_populated() {
  rollup_json="$(api_get "api/workspaces/${WORKSPACE}/eval-rollup")"
  assert_jq "eval-rollup reflects at least one eval" "$rollup_json" '
    .success == true and
    ((.data.eval_count // 0) >= 1)
  '
}

browser_text() {
  agent-browser --profile "$AGENT_BROWSER_PROFILE" get text body
}

# browser_text_until <grep-args...> — poll the page body until the pattern
# appears (cold profiles need several seconds for chunks + data to load).
# Prints the final body; succeeds iff the pattern matched within the budget.
browser_text_until() {
  attempt=0
  body=""
  while [ "$attempt" -lt 6 ]; do
    agent-browser --profile "$AGENT_BROWSER_PROFILE" wait 2500 >/dev/null
    body="$(browser_text)"
    if printf '%s' "$body" | grep "$@" >/dev/null; then
      printf '%s' "$body"
      return 0
    fi
    attempt=$((attempt + 1))
  done
  printf '%s' "$body"
  return 1
}

run_ui_assertions() {
  sid="$1"
  if ! command -v agent-browser >/dev/null 2>&1; then
    skip "UI block: agent-browser CLI is not available"
    return 0
  fi

  AGENT_BROWSER_PROFILE="${LOCAL_MODE_AGENT_BROWSER_PROFILE:-${TMPDIR}/agent-browser-profile}"
  mkdir -p "$AGENT_BROWSER_PROFILE"

  short_sid="${sid:0:8}"
  traces_url="${UI_URL}/ws/${WORKSPACE}/traces?range=30d&status=completed&kind=task"
  log "UI: opening Traces view ${traces_url}"
  agent-browser --profile "$AGENT_BROWSER_PROFILE" open "$traces_url" >/dev/null
  if ! traces_body="$(browser_text_until -F "$short_sid")"; then
    printf '%s\n' "$traces_body" >"$EVALS_WORKDIR/traces-body.txt"
    fatal "UI Traces list did not show selected session ${sid}"
  fi
  pass "UI Traces list shows selected session"

  if agent-browser --profile "$AGENT_BROWSER_PROFILE" find text "$short_sid" click >/dev/null 2>&1; then
    agent-browser --profile "$AGENT_BROWSER_PROFILE" wait 3000 >/dev/null
  fi
  if ! traces_body="$(browser_text_until -E "Transcript|assistant|system|tool")"; then
    printf '%s\n' "$traces_body" >"$EVALS_WORKDIR/traces-detail-body.txt"
    fatal "UI Traces drill-in did not render transcript content"
  fi
  pass "UI Traces drill-in renders transcript"

  obs_url="${UI_URL}/ws/${WORKSPACE}/observability"
  log "UI: opening Observability dashboard ${obs_url}"
  agent-browser --profile "$AGENT_BROWSER_PROFILE" open "$obs_url" >/dev/null
  if ! obs_body="$(browser_text_until -F "Hourly Completions")" ||
    ! printf '%s' "$obs_body" | grep -F "Agent Utilization" >/dev/null; then
    printf '%s\n' "$obs_body" >"$EVALS_WORKDIR/observability-body.txt"
    fatal "UI Observability dashboard did not render populated panels"
  fi
  pass "UI Observability dashboard renders populated panels"

  if printf '%s' "$obs_body" | grep -F "Session Evals" >/dev/null; then
    for label in "Score Trend" "Outcome success" "Instruction adherence" "Tool use quality"; do
      if ! printf '%s' "$obs_body" | grep -F "$label" >/dev/null; then
        printf '%s\n' "$obs_body" >"$EVALS_WORKDIR/observability-evals-body.txt"
        fatal "UI Observability eval panel is missing ${label}"
      fi
    done
    pass "UI Observability dashboard renders eval panels"
  else
    skip "UI eval panel labels: this frontend checkout has no eval-specific Observability panel text; eval-rollup API was asserted"
  fi
}

run_plain() {
  log "mode=plain evidence=real local backend, deterministic; target=eval provisioning, preflight defer, traces read path"
  wait_for "Loom API reachable" "$TIMEOUT_SECONDS" api_reachable
  wait_for "FleetDB API reachable" "$TIMEOUT_SECONDS" fleet_reachable
  capture_baseline_runs
  enable_eval_cron
  assert_eval_provisioning
  wait_for "cron tick produced eval_backend_unavailable driver run" "$CRON_TIMEOUT_SECONDS" eval_backend_unavailable_run_exists
  assert_no_eval_status_stamps
  assert_session_evals_empty
  wait_for "preflight TaskRuns produce no sessions" "$CRON_TIMEOUT_SECONDS" assert_preflight_task_runs_have_no_sessions
  assert_no_legacy_flue_session_ids
  wait_for "deterministic completed task session with transcript_ref exists" "$TIMEOUT_SECONDS" find_completed_task_session_with_ref
  select_completed_task_session_with_ref
  assert_workspace_sessions_read_path "$SELECTED_SESSION_ID"
  log "plain mode complete"
}

run_codex() {
  log "mode=codex evidence=live; target=real Codex task session, judge eval record, rollup, optional UI"
  log "live warning: this mode uses mounted Codex auth and can spend judge tokens"
  wait_for "Loom API reachable" "$TIMEOUT_SECONDS" api_reachable
  wait_for "FleetDB API reachable" "$TIMEOUT_SECONDS" fleet_reachable
  enable_eval_cron
  assert_eval_provisioning
  wait_for "real completed task session with transcript_ref exists" "$TIMEOUT_SECONDS" find_completed_task_session_with_ref
  select_completed_task_session_with_ref
  assert_workspace_sessions_read_path "$SELECTED_SESSION_ID"
  wait_for "session eval record exists and has valid scores, tags, model, rationales, and cost" "$CRON_TIMEOUT_SECONDS" session_eval_record_valid "$SELECTED_SESSION_ID"
  wait_for "preflight TaskRuns produce no sessions" "$CRON_TIMEOUT_SECONDS" assert_preflight_task_runs_have_no_sessions
  wait_for "judge session has kind=judge and judged-session metadata" "$CRON_TIMEOUT_SECONDS" judge_session_matches_selected "$SELECTED_SESSION_ID"
  assert_eval_cost_total_tokens "$SELECTED_SESSION_ID"
  assert_no_legacy_flue_session_ids
  assert_session_eval_status_done "$SELECTED_SESSION_ID"
  assert_eval_rollup_populated
  run_ui_assertions "$SELECTED_SESSION_ID"
  log "codex mode complete"
}

require_cmd curl
require_cmd jq

case "$MODE" in
  plain)
    run_plain
    ;;
  codex)
    run_codex
    ;;
  *)
    fatal "usage: $0 plain|codex"
    ;;
esac
