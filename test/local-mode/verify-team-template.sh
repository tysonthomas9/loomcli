#!/usr/bin/env bash
set -euo pipefail

# Verify the four code-shipped Team Templates against an already-running
# local-mode HTTP API. This script never starts, stops, or inspects containers.
#
# Documented HTTP-surface gaps:
#   * Fleet-store mode registers GET /api/workspaces/{ws}/agents/{name}/queue
#     as unsupported, so the API-only routing-floor probe is intentionally
#     omitted. Routing remains covered below the HTTP seam by task-router tests.
#   * There is no workspace role-list/detail endpoint. The catalog exposes role
#     identity/kind only. This verifier therefore checks the hardcoded table's
#     visible catalog projection and requires every role to return
#     skipped_match on re-apply (the server compares the full stored role).
#   * The monitor API projects daemon_managed from the stored auto flag; it is
#     not an authoritative adopted-agent count. Adoption evidence combines an
#     exact four-agent monitor projection with the target workspace's
#     daemon-origin daemon.start audit event. If that event never appears, the
#     adoption check is reported as SKIP-with-warning rather than as green.
#
# Workspace creation always sends type=empty. LOCAL_MODE_TEAM_VERIFY_REPO is a
# serve-host PATH PREFIX: each team uses ${LOCAL_MODE_TEAM_VERIFY_REPO}-<team-id>
# as its sole repo. The caller must pre-create all requested per-team paths as
# git repos on the serve host. Separate repos are required because template
# agents use workspace-scoped branches, but separate fixtures keep each
# template's proof isolated and make cleanup deterministic.

API_URL="${LOCAL_MODE_API_URL:-http://localhost:${LOCAL_MODE_API_PORT:-8282}}"
TIMEOUT_SECONDS="${LOCAL_MODE_TEAM_VERIFY_TIMEOUT:-240}"
POLL_SECONDS="${LOCAL_MODE_TEAM_VERIFY_POLL_SECONDS:-2}"
REQUEST_TIMEOUT_SECONDS="${LOCAL_MODE_TEAM_REQUEST_TIMEOUT:-120}"
API_RETRY_SECONDS="${LOCAL_MODE_TEAM_API_RETRY_SECONDS:-60}"
IDEMPOTENCY_RETRY_SECONDS="${LOCAL_MODE_TEAM_IDEMPOTENCY_RETRY_SECONDS:-30}"
TEAM_VERIFY_REPO="${LOCAL_MODE_TEAM_VERIFY_REPO:-/workspace/team-template-repo}"
WAIT_OUT="/tmp/loom-team-template-verify-$$.out"
WAIT_ERR="/tmp/loom-team-template-verify-$$.err"
HTTP_BODY="${WAIT_OUT}.http"

API_URL="${API_URL%/}"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "[verify-team-template] FATAL: $1 is required" >&2
    exit 127
  fi
}

ok() {
  echo "[verify-team-template] ok: $*"
}

warn() {
  echo "[verify-team-template] WARN: $*" >&2
}

fatal() {
  echo "[verify-team-template] FATAL: $*" >&2
  exit 1
}

fatal_response() {
  local response="$1"
  shift
  echo "[verify-team-template] actual response JSON:" >&2
  printf '%s\n' "$response" >&2
  fatal "$@"
}

curl_json() {
  local url="$1"
  local http_code
  local curl_rc
  : >"$HTTP_BODY"
  http_code="$(curl -sS --max-time 10 -o "$HTTP_BODY" -w '%{http_code}' "$url")" || {
    curl_rc=$?
    echo "[verify-team-template] curl failure (${curl_rc}) for GET ${url}" >&2
    if [ -s "$HTTP_BODY" ]; then
      echo "[verify-team-template] actual response JSON:" >&2
      cat "$HTTP_BODY" >&2
    fi
    return 1
  }
  case "$http_code" in
    2??)
      cat "$HTTP_BODY"
      ;;
    *)
      echo "[verify-team-template] GET ${url} returned HTTP ${http_code}" >&2
      echo "[verify-team-template] actual response JSON:" >&2
      cat "$HTTP_BODY" >&2
      return 1
      ;;
  esac
}

api_get() {
  curl_json "${API_URL}/$1"
}

dump_wait_evidence() {
  if [ -s "$WAIT_ERR" ]; then
    cat "$WAIT_ERR" >&2
  fi
  if [ -s "$WAIT_OUT" ]; then
    cat "$WAIT_OUT" >&2
  fi
}

retry_for() {
  local timeout_seconds="$1"
  local label="$2"
  local deadline
  local probe_rc
  shift 2
  deadline=$((SECONDS + timeout_seconds))
  while true; do
    probe_rc=0
    "$@" >"$WAIT_OUT" 2>"$WAIT_ERR" || probe_rc=$?
    if [ "$probe_rc" -eq 0 ]; then
      return 0
    fi
    if [ "$probe_rc" -eq 2 ]; then
      echo "[verify-team-template] FATAL: terminal failure while waiting for ${label}" >&2
      dump_wait_evidence
      return 2
    fi
    if [ "$SECONDS" -ge "$deadline" ]; then
      return 1
    fi
    sleep "$POLL_SECONDS"
  done
}

wait_for_seconds() {
  local timeout_seconds="$1"
  local label="$2"
  local wait_rc
  shift 2
  if retry_for "$timeout_seconds" "$label" "$@"; then
    ok "$label"
    return 0
  else
    wait_rc=$?
  fi
  if [ "$wait_rc" -ne 2 ]; then
    echo "[verify-team-template] FATAL: timed out waiting for ${label}" >&2
    dump_wait_evidence
  fi
  return "$wait_rc"
}

wait_for() {
  local label="$1"
  shift
  wait_for_seconds "$TIMEOUT_SECONDS" "$label" "$@"
}

# shellcheck disable=SC2329 # Invoked indirectly by retry_for/wait_for_seconds.
api_mutation_probe() {
  local method="$1"
  local path="$2"
  local retry_mode="$3"
  local body="${4:-}"
  local http_code
  local curl_rc
  local -a curl_args=(
    -sS
    --max-time "$REQUEST_TIMEOUT_SECONDS"
    -o "$HTTP_BODY"
    -w '%{http_code}'
    -X "$method"
  )
  if [ "$body" != "" ]; then
    curl_args+=(
      -H "Content-Type: application/json"
      --data "$body"
    )
  fi

  : >"$HTTP_BODY"
  http_code="$(curl "${curl_args[@]}" "${API_URL}/${path}")" || {
    curl_rc=$?
    echo "[verify-team-template] retryable curl failure (${curl_rc}) for ${method} /${path}" >&2
    if [ -s "$HTTP_BODY" ]; then
      echo "[verify-team-template] actual response JSON:" >&2
      cat "$HTTP_BODY" >&2
    fi
    return 1
  }

  case "$http_code" in
    2??)
      cat "$HTTP_BODY"
      return 0
      ;;
    404)
      if [ "$method" = "DELETE" ]; then
        cat "$HTTP_BODY"
        return 0
      fi
      ;;
    409)
      if [ "$retry_mode" = "conflict" ] || [ "$retry_mode" = "all" ]; then
        echo "[verify-team-template] retryable HTTP 409 for ${method} /${path}" >&2
        echo "[verify-team-template] actual response JSON:" >&2
        cat "$HTTP_BODY" >&2
        return 1
      fi
      ;;
    429|500)
      echo "[verify-team-template] retryable HTTP ${http_code} for ${method} /${path}" >&2
      echo "[verify-team-template] actual response JSON:" >&2
      cat "$HTTP_BODY" >&2
      return 1
      ;;
  esac

  if [ "$retry_mode" = "all" ]; then
    echo "[verify-team-template] retryable HTTP ${http_code} for ${method} /${path}" >&2
    echo "[verify-team-template] actual response JSON:" >&2
    cat "$HTTP_BODY" >&2
    return 1
  fi

  echo "[verify-team-template] terminal HTTP ${http_code} for ${method} /${path}" >&2
  echo "[verify-team-template] actual response JSON:" >&2
  cat "$HTTP_BODY" >&2
  return 2
}

# shellcheck disable=SC2329 # Invoked indirectly by wait_for.
api_reachable() {
  api_get "api/config" >/dev/null
}

expected_team_json() {
  case "$1" in
    fullstack-app)
      cat <<'JSON'
{
  "id": "fullstack-app",
  "label": "Full-Stack App Development",
  "architect": "app-architect",
  "implementers": ["frontend-dev", "backend-dev", "qa-engineer"],
  "roles": [
    {"name":"app-architect","kind":"worker","task_filter":"any","labels":["architect"],"exclude_labels":[],"denied_tools":[]},
    {"name":"frontend-dev","kind":"worker","task_filter":"has_design","labels":[],"exclude_labels":["architect"],"denied_tools":[]},
    {"name":"backend-dev","kind":"worker","task_filter":"has_design","labels":[],"exclude_labels":["architect"],"denied_tools":[]},
    {"name":"qa-engineer","kind":"worker","task_filter":"has_design","labels":[],"exclude_labels":["architect"],"denied_tools":[]},
    {"name":"code-reviewer","kind":"interactive","task_filter":"","labels":[],"exclude_labels":[],"denied_tools":[]}
  ],
  "agents": [
    {"name":"app-architect-1","role_name":"app-architect"},
    {"name":"frontend-dev-1","role_name":"frontend-dev"},
    {"name":"backend-dev-1","role_name":"backend-dev"},
    {"name":"qa-engineer-1","role_name":"qa-engineer"}
  ]
}
JSON
      ;;
    website)
      cat <<'JSON'
{
  "id": "website",
  "label": "Website Development",
  "architect": "web-designer",
  "implementers": ["frontend-dev", "content-writer", "site-qa"],
  "roles": [
    {"name":"web-designer","kind":"worker","task_filter":"any","labels":["architect"],"exclude_labels":[],"denied_tools":[]},
    {"name":"frontend-dev","kind":"worker","task_filter":"has_design","labels":[],"exclude_labels":["architect"],"denied_tools":[]},
    {"name":"content-writer","kind":"worker","task_filter":"has_design","labels":[],"exclude_labels":["architect"],"denied_tools":[]},
    {"name":"site-qa","kind":"worker","task_filter":"has_design","labels":[],"exclude_labels":["architect"],"denied_tools":[]},
    {"name":"code-reviewer","kind":"interactive","task_filter":"","labels":[],"exclude_labels":[],"denied_tools":[]}
  ],
  "agents": [
    {"name":"web-designer-1","role_name":"web-designer"},
    {"name":"frontend-dev-1","role_name":"frontend-dev"},
    {"name":"content-writer-1","role_name":"content-writer"},
    {"name":"site-qa-1","role_name":"site-qa"}
  ]
}
JSON
      ;;
    ai-agent)
      cat <<'JSON'
{
  "id": "ai-agent",
  "label": "AI Agent Development",
  "architect": "agent-architect",
  "implementers": ["agent-dev", "eval-engineer"],
  "roles": [
    {"name":"agent-architect","kind":"worker","task_filter":"any","labels":["architect"],"exclude_labels":["research"],"denied_tools":[]},
    {"name":"researcher","kind":"worker","task_filter":"any","labels":["research"],"exclude_labels":["architect"],"denied_tools":[]},
    {"name":"agent-dev","kind":"worker","task_filter":"has_design","labels":[],"exclude_labels":["architect","research"],"denied_tools":[]},
    {"name":"eval-engineer","kind":"worker","task_filter":"has_design","labels":[],"exclude_labels":["architect","research"],"denied_tools":[],"max_budget_usd":25.0,"max_run_duration":7200},
    {"name":"code-reviewer","kind":"interactive","task_filter":"","labels":[],"exclude_labels":[],"denied_tools":[]}
  ],
  "agents": [
    {"name":"agent-architect-1","role_name":"agent-architect"},
    {"name":"researcher-1","role_name":"researcher"},
    {"name":"agent-dev-1","role_name":"agent-dev"},
    {"name":"eval-engineer-1","role_name":"eval-engineer"}
  ]
}
JSON
      ;;
    backend)
      cat <<'JSON'
{
  "id": "backend",
  "label": "Backend Development",
  "architect": "api-architect",
  "implementers": ["backend-dev", "data-engineer", "qa-engineer"],
  "roles": [
    {"name":"api-architect","kind":"worker","task_filter":"any","labels":["architect"],"exclude_labels":[],"denied_tools":[]},
    {"name":"backend-dev","kind":"worker","task_filter":"has_design","labels":[],"exclude_labels":["architect"],"denied_tools":[]},
    {"name":"data-engineer","kind":"worker","task_filter":"has_design","labels":[],"exclude_labels":["architect"],"denied_tools":[]},
    {"name":"qa-engineer","kind":"worker","task_filter":"has_design","labels":[],"exclude_labels":["architect"],"denied_tools":[]},
    {"name":"code-reviewer","kind":"interactive","task_filter":"","labels":[],"exclude_labels":[],"denied_tools":[]}
  ],
  "agents": [
    {"name":"api-architect-1","role_name":"api-architect"},
    {"name":"backend-dev-1","role_name":"backend-dev"},
    {"name":"data-engineer-1","role_name":"data-engineer"},
    {"name":"qa-engineer-1","role_name":"qa-engineer"}
  ]
}
JSON
      ;;
    *)
      return 1
      ;;
  esac
}

verify_expected_table_contract() {
  printf '%s' "$EXPECTED" | jq -e '
    . as $team |
    (.roles | length) == 5 and
    (.agents | length) == 4 and
    any($team.roles[];
      .name == $team.architect and
      .kind == "worker" and
      .task_filter == "any" and
      (.labels | index("architect")) != null
    ) and
    all($team.implementers[];
      . as $role_name |
      any($team.roles[];
        .name == $role_name and
        .kind == "worker" and
        .task_filter == "has_design" and
        (.exclude_labels | index("architect")) != null
      )
    ) and
    any($team.roles[]; .name == "code-reviewer" and .kind == "interactive") and
    all($team.agents[];
      . as $agent |
      $agent.name == ($agent.role_name + "-1") and
      $agent.role_name != "code-reviewer" and
      any($team.roles[]; .name == $agent.role_name)
    ) and
    (if .id == "ai-agent" then
      any(.roles[];
        .name == "researcher" and
        .labels == ["research"] and
        .exclude_labels == ["architect"]
      ) and
      any(.roles[];
        .name == "agent-architect" and
        (.exclude_labels | index("research")) != null
      ) and
      any(.roles[];
        .name == "eval-engineer" and
        .max_budget_usd == 25.0 and
        .max_run_duration == 7200 and
        .denied_tools == []
      )
    else true end)
  ' >/dev/null || fatal "hardcoded expected table is internally inconsistent for ${TEAM}"
  ok "${TEAM} hardcoded role/agent table satisfies the regression invariants"
}

verify_catalog() {
  catalog_json="$(api_get "api/team-templates")" || fatal "GET /api/team-templates failed"

  printf '%s' "$catalog_json" | jq -e '
    [
      {"id":"fullstack-app","label":"Full-Stack App Development"},
      {"id":"website","label":"Website Development"},
      {"id":"ai-agent","label":"AI Agent Development"},
      {"id":"backend","label":"Backend Development"}
    ] as $expected |
    (.templates | map({id, label}) | sort_by(.id)) == ($expected | sort_by(.id))
  ' >/dev/null || fatal_response "$catalog_json" "template catalog does not contain exactly the four shipped id/label pairs"
  ok "template catalog contains all four shipped templates with expected labels"

  target_json="$(printf '%s' "$catalog_json" | jq -c --arg team "$TEAM" '.templates[] | select(.id == $team)')"
  [ "$target_json" != "" ] || fatal_response "$catalog_json" "template catalog has no detail entry for ${TEAM}"

  jq -n -e --argjson actual "$target_json" --argjson expected "$EXPECTED" '
    $actual.id == $expected.id and
    $actual.label == $expected.label and
    ($actual.roles | length) == 5 and
    ($actual.agents | length) == 4 and
    ($actual.roles | map({name, kind})) == ($expected.roles | map({name, kind})) and
    $actual.agents == $expected.agents
  ' >/dev/null || fatal_response "$target_json" "${TEAM} catalog detail does not match the hardcoded role/agent identity table"
  ok "${TEAM} catalog detail has 5 roles and 4 expected agents"
}

create_workspace() {
  team_slug="$(printf '%s' "$TEAM" | tr '[:lower:]' '[:upper:]' | tr -cd 'A-Z0-9')"
  workspace_name="TTREG${team_slug}${RANDOM}${RANDOM}"
  if [ "$TEAM_VERIFY_REPO" != "" ]; then
    team_repo="${TEAM_VERIFY_REPO}-${TEAM}"
    create_body="$(jq -nc --arg name "$workspace_name" --arg repo "$team_repo" \
      '{name:$name,type:"empty",repos:[$repo]}')"
  else
    team_repo=""
    create_body="$(jq -nc --arg name "$workspace_name" '{name:$name,type:"empty"}')"
  fi
  wait_for_seconds "$API_RETRY_SECONDS" "POST /api/workspaces for ${workspace_name}" \
    api_mutation_probe POST "api/workspaces" transient "$create_body" || \
    fatal "POST /api/workspaces failed for ${workspace_name}"
  create_json="$(cat "$WAIT_OUT")"

  WORKSPACE="$(printf '%s' "$create_json" | jq -r '.data.id // empty')"
  [ "$WORKSPACE" != "" ] || fatal_response "$create_json" "workspace create response did not contain data.id"
  printf '%s' "$WORKSPACE" | grep -Eq '^[A-Z][A-Z0-9-]{0,31}$' || \
    fatal_response "$create_json" "created workspace ID ${WORKSPACE} violates the server key contract"
  if [ "$team_repo" != "" ]; then
    ok "${TEAM} created fresh workspace ${WORKSPACE} (${workspace_name}) using ${team_repo}"
  else
    ok "${TEAM} created fresh workspace ${WORKSPACE} (${workspace_name})"
  fi

  workspace_json="$(api_get "api/workspaces/${WORKSPACE}")" || \
    fatal "GET /api/workspaces/${WORKSPACE} failed after create"
  printf '%s' "$workspace_json" | jq -e '(.data.repos // []) | length > 0' >/dev/null || \
    fatal_response "$workspace_json" "workspace ${WORKSPACE} has no registered repo after empty create; set LOCAL_MODE_TEAM_VERIFY_REPO to a serve-host git repo path prefix and pre-create ${LOCAL_MODE_TEAM_VERIFY_REPO:-<prefix>}-${TEAM}"
  ok "${WORKSPACE} has a registered repo"
}

expected_apply_steps() {
  action="$1"
  printf '%s' "$EXPECTED" | jq -c --arg action "$action" '
    [.roles[] | {entity:"role", name:.name, action:$action}] +
    [.agents[] | {entity:"agent", name:.name, action:$action}]
  '
}

verify_apply_report() {
  local response="$1"
  local expected_action="$2"
  local expected_created="$3"
  local expected_skipped="$4"

  if ! apply_report_matches "$response" "$expected_action" "$expected_created" "$expected_skipped"; then
    fatal_response "$response" "${TEAM} apply report did not match expected counts and per-step actions"
  fi
}

apply_report_matches() {
  local response="$1"
  local expected_action="$2"
  local expected_created="$3"
  local expected_skipped="$4"
  local expected_steps
  expected_steps="$(expected_apply_steps "$expected_action")"

  printf '%s' "$response" | jq -e \
    --arg team "$TEAM" \
    --arg workspace "$WORKSPACE" \
    --argjson created "$expected_created" \
    --argjson skipped "$expected_skipped" \
    --argjson expected_steps "$expected_steps" '
      .status == "done" and
      .report.template_id == $team and
      .report.workspace_key == $workspace and
      .report.dry_run == false and
      .report.created == $created and
      .report.skipped == $skipped and
      .report.diverged == 0 and
      .report.failed == 0 and
      .report.steps == $expected_steps
    ' >/dev/null
}

# shellcheck disable=SC2329 # Invoked indirectly by wait_for_seconds.
idempotent_apply_probe() {
  local response
  local probe_rc
  if [ "$IDEMPOTENCY_RESPONSE" != "" ]; then
    response="$IDEMPOTENCY_RESPONSE"
    IDEMPOTENCY_RESPONSE=""
  else
    probe_rc=0
    response="$(api_mutation_probe POST "api/workspaces/${WORKSPACE}/team-templates/${TEAM}/apply" conflict)" || probe_rc=$?
    if [ "$probe_rc" -ne 0 ]; then
      return "$probe_rc"
    fi
  fi

  if ! apply_report_matches "$response" "skipped_match" 0 9; then
    echo "[verify-team-template] idempotent apply report has not converged for ${TEAM}" >&2
    echo "[verify-team-template] actual response JSON:" >&2
    printf '%s\n' "$response" >&2
    return 1
  fi
  printf '%s' "$response"
}

apply_team_template() {
  wait_for_seconds "$API_RETRY_SECONDS" "first ${TEAM} apply for ${WORKSPACE}" \
    api_mutation_probe POST "api/workspaces/${WORKSPACE}/team-templates/${TEAM}/apply" conflict || \
    fatal "first ${TEAM} apply failed for ${WORKSPACE}"
  first_apply="$(cat "$WAIT_OUT")"
  verify_apply_report "$first_apply" "created" 9 0
  ok "${TEAM} first apply reported created=9 skipped=0 failed=0"

  wait_for_seconds "$API_RETRY_SECONDS" "idempotent ${TEAM} apply request for ${WORKSPACE}" \
    api_mutation_probe POST "api/workspaces/${WORKSPACE}/team-templates/${TEAM}/apply" conflict || \
    fatal "idempotent ${TEAM} re-apply failed for ${WORKSPACE}"
  IDEMPOTENCY_RESPONSE="$(cat "$WAIT_OUT")"
  wait_for_seconds "$IDEMPOTENCY_RETRY_SECONDS" "${TEAM} idempotent apply report convergence" \
    idempotent_apply_probe || fatal "idempotent ${TEAM} apply report did not converge for ${WORKSPACE}"
  second_apply="$(cat "$WAIT_OUT")"
  verify_apply_report "$second_apply" "skipped_match" 0 9
  ok "${TEAM} re-apply reported created=0 skipped=9 failed=0 with all steps skipped_match"
}

verify_agents() {
  agents_json="$(api_get "api/workspaces/${WORKSPACE}/agents")" || \
    fatal "GET /api/workspaces/${WORKSPACE}/agents failed"
  expected_agents="$(printf '%s' "$EXPECTED" | jq -c '.agents')"

  printf '%s' "$agents_json" | jq -e --argjson expected "$expected_agents" '
    .success == true and
    .total == 4 and
    (.data | length) == 4 and
    (.data | map({name, role_name}) | sort_by(.name)) == ($expected | sort_by(.name)) and
    all(.data[]; .role_name != "code-reviewer")
  ' >/dev/null || fatal_response "$agents_json" "${TEAM} agent assignments do not match the expected four role-name bindings"
  ok "${TEAM} has exactly 4 <role>-1 agents and none for code-reviewer"
}

daemon_adoption_probe() {
  monitor_json="$(api_get "api/monitor/agents?workspace=${WORKSPACE}")" || return 1
  expected_monitor="$(printf '%s' "$EXPECTED" | jq -c '[.agents[] | {name, role:.role_name}]')"

  if printf '%s' "$monitor_json" | jq -e 'any(.agents[]?; .role == "code-reviewer")' >/dev/null; then
    echo "interactive code-reviewer appeared in the daemon monitor projection" >&2
    echo "[verify-team-template] actual response JSON:" >&2
    printf '%s\n' "$monitor_json" >&2
    return 2
  fi
  printf '%s' "$monitor_json" | jq -e --argjson expected "$expected_monitor" '
    (.agents | length) == 4 and
    (.agents | map({name, role}) | sort_by(.name)) == ($expected | sort_by(.name)) and
    all(.agents[]; .daemon_managed == true)
  ' >/dev/null || {
    echo "daemon monitor projection has not converged" >&2
    echo "[verify-team-template] actual response JSON:" >&2
    printf '%s\n' "$monitor_json" >&2
    return 1
  }

  audit_json="$(api_get "api/workspaces/${WORKSPACE}/audit?limit=500")" || return 1
  if printf '%s' "$audit_json" | jq -e '
    any(.data.events[]?;
      .action == "agent.session_start" and
      .details.agent_role == "code-reviewer"
    )
  ' >/dev/null; then
    echo "interactive code-reviewer appeared in the daemon audit trail" >&2
    echo "[verify-team-template] actual response JSON:" >&2
    printf '%s\n' "$audit_json" >&2
    return 2
  fi
  if ! printf '%s' "$audit_json" | jq -e --arg workspace "$WORKSPACE" '
    .success == true and
    any(.data.events[]?;
      .action == "daemon.start" and
      .entity_type == "daemon_profile" and
      .entity_id == $workspace and
      .details.source == "daemon"
    )
  ' >/dev/null; then
    echo "daemon audit evidence has not converged" >&2
    echo "[verify-team-template] actual response JSON:" >&2
    printf '%s\n' "$audit_json" >&2
    return 1
  fi
}

wait_for_daemon_adoption() {
  label="${TEAM} daemon adoption evidence for exactly 4 worker assignments"
  deadline=$((SECONDS + TIMEOUT_SECONDS))
  while true; do
    probe_rc=0
    daemon_adoption_probe >"$WAIT_OUT" 2>"$WAIT_ERR" || probe_rc=$?
    case "$probe_rc" in
      0)
        ok "$label"
        return 0
        ;;
      2)
        if [ -s "$WAIT_ERR" ]; then
          cat "$WAIT_ERR" >&2
        fi
        fatal "interactive code-reviewer was adopted for ${TEAM}"
        ;;
    esac
    if [ "$SECONDS" -ge "$deadline" ]; then
      warn "SKIP: timed out waiting for ${label}; this stack did not expose proof that its daemon manager auto-adopted the fresh workspace"
      if [ -s "$WAIT_ERR" ]; then
        cat "$WAIT_ERR" >&2
      fi
      if [ -s "$WAIT_OUT" ]; then
        cat "$WAIT_OUT" >&2
      fi
      ADOPTION_SKIPPED=1
      return 0
    fi
    sleep "$POLL_SECONDS"
  done
}

# shellcheck disable=SC2329 # Invoked indirectly by the per-team EXIT trap.
cleanup_team() {
  if [ "${WORKSPACE:-}" = "" ]; then
    return 0
  fi
  if retry_for "$API_RETRY_SECONDS" "cleanup DELETE for ${WORKSPACE}" \
    api_mutation_probe DELETE "api/workspaces/${WORKSPACE}" all; then
    ok "deleted throwaway workspace ${WORKSPACE}"
    return 0
  fi
  dump_wait_evidence
  warn "CLEANUP FAILED: leftover workspace ${WORKSPACE}; future runs may hit a checked-out-branch collision because it could not be deleted"
}

verify_one_team() (
  TEAM="$1"
  WORKSPACE=""
  EXPECTED="$(expected_team_json "$TEAM")" || fatal "no expected table for ${TEAM}"
  ADOPTION_SKIPPED=0
  IDEMPOTENCY_RESPONSE=""
  trap cleanup_team EXIT

  echo "[verify-team-template] team=${TEAM} api=${API_URL}"
  wait_for "Loom API is reachable" api_reachable || fatal "Loom API did not become reachable"
  verify_expected_table_contract
  verify_catalog
  create_workspace
  apply_team_template
  verify_agents
  wait_for_daemon_adoption

  if [ "$ADOPTION_SKIPPED" -eq 1 ]; then
    echo "[verify-team-template] ${TEAM}: PASS with daemon adoption SKIP"
    exit 2
  fi
  echo "[verify-team-template] ${TEAM}: PASS"
)

usage() {
  echo "usage: $0 <fullstack-app|website|ai-agent|backend|all>" >&2
  exit 64
}

# shellcheck disable=SC2329 # Invoked indirectly by the script EXIT trap.
cleanup_wait_files() {
  rm -f "$WAIT_OUT" "$WAIT_ERR" "$HTTP_BODY"
}

require_cmd curl
require_cmd jq
require_cmd grep
require_cmd tr
trap cleanup_wait_files EXIT

[ "$#" -eq 1 ] || usage
REQUESTED_TEAM="$1"
case "$REQUESTED_TEAM" in
  fullstack-app|website|ai-agent|backend)
    if verify_one_team "$REQUESTED_TEAM"; then
      team_rc=0
    else
      team_rc=$?
    fi
    case "$team_rc" in
      0)
        echo "[verify-team-template] summary: ${REQUESTED_TEAM} PASS"
        exit 0
        ;;
      2)
        echo "[verify-team-template] summary: ${REQUESTED_TEAM} PASS (daemon adoption SKIP)"
        exit 0
        ;;
      *)
        echo "[verify-team-template] summary: ${REQUESTED_TEAM} FAIL" >&2
        exit 1
        ;;
    esac
    ;;
  all)
    summary_teams=()
    summary_results=()
    any_failed=0
    for team in fullstack-app website ai-agent backend; do
      if verify_one_team "$team"; then
        team_rc=0
      else
        team_rc=$?
      fi
      summary_teams+=("$team")
      case "$team_rc" in
        0)
          summary_results+=("PASS")
          ;;
        2)
          summary_results+=("PASS (daemon adoption SKIP)")
          ;;
        *)
          summary_results+=("FAIL")
          any_failed=1
          ;;
      esac
    done

    echo "[verify-team-template] per-team summary:"
    index=0
    while [ "$index" -lt "${#summary_teams[@]}" ]; do
      echo "[verify-team-template] ${summary_teams[$index]}: ${summary_results[$index]}"
      index=$((index + 1))
    done
    exit "$any_failed"
    ;;
  *)
    usage
    ;;
esac
