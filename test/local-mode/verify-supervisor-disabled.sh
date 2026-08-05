#!/usr/bin/env bash
set -euo pipefail

WORKSPACE="${LOOM_WORKSPACE:-LOCALMODE}"
WORKSPACE_ROOT="${LOOM_LOCAL_MODE_WORKSPACE_ROOT:-/root/.loom/workspaces/${WORKSPACE}}"
CONFIG_ROOT="${LOOM_CONFIG_DIR:-/root/.loom}"
LOOM_PORT="${LOOM_LOCAL_MODE_API_PORT:-8080}"
RUN_MANIFEST="${LOCAL_MODE_RUN_MANIFEST:-/tmp/loom-local-mode-run.json}"
API_URL="http://127.0.0.1:${LOOM_PORT}/api/workspaces/${WORKSPACE}"

fail() {
  echo "[supervisor-disabled-verify] FATAL: $*" >&2
  exit 1
}

require_manifest_field() {
  field="$1"
  jq -er --arg field "$field" '.[$field] | select(type == "string" and length > 0)' "$RUN_MANIFEST"
}

[ -s "$RUN_MANIFEST" ] || fail "run manifest is missing"
[ "$(require_manifest_field plane)" = "ts" ] || fail "run manifest does not identify the prompt-agent execution plane"

PLAN_AGENT_ID="$(require_manifest_field plan_agent_record_id)"
CODE_AGENT_ID="$(require_manifest_field code_agent_record_id)"
PLAN_ROLE="$(require_manifest_field plan_agent_role)"
CODE_ROLE="$(require_manifest_field code_agent_role)"
PLAN_BINDING="$(require_manifest_field plan_binding_id)"
CODE_BINDING="$(require_manifest_field code_binding_id)"

agentdefs="$(loom agentdef list --json)" || fail "could not list agent definitions"
if ! printf '%s' "$agentdefs" | jq -e '((.data? // .) | map(select(.auto == true)) | length) == 0' >/dev/null; then
  fail "auto agent definitions remain in the prompt-agent execution plane"
fi
echo "[supervisor-disabled-verify] ok: zero auto agentdefs"

agents="$(curl -fsS --max-time 10 "${API_URL}/agents")" || fail "could not read public agent API"
if ! printf '%s' "$agents" | jq -e \
  --arg plan "$PLAN_AGENT_ID" --arg coder "$CODE_AGENT_ID" \
  --arg plan_role "$PLAN_ROLE" --arg code_role "$CODE_ROLE" \
  --arg plan_binding "$PLAN_BINDING" --arg code_binding "$CODE_BINDING" '
    (.data // []) as $agents |
    ([$agents[] | select(.id == $plan and .kind == "prompt" and .enabled == true and .behavior.role_name == $plan_role and
      any(.bindings[]?; .binding_id == $plan_binding and .source_kind == "internal" and .enabled == true and
        (.event_type_patterns | index("internal.task.ready") != null)))] | length) == 1 and
    ([$agents[] | select(.id == $coder and .kind == "prompt" and .enabled == true and .behavior.role_name == $code_role and
      any(.bindings[]?; .binding_id == $code_binding and .source_kind == "internal" and .enabled == true and
        (.event_type_patterns | index("internal.task.ready") != null)))] | length) == 1
  ' >/dev/null; then
  fail "public planner/coder AgentService records or bindings do not match the manifest"
fi
echo "[supervisor-disabled-verify] ok: public planner/coder prompt agents"

daemon_processes=""
for proc in /proc/[0-9]*; do
  [ -r "${proc}/cmdline" ] || continue
  cmdline="$(tr '\000' ' ' < "${proc}/cmdline" 2>/dev/null || true)"
  case " ${cmdline} " in
    *" loom daemon "*|*" /loom daemon "*|*" /usr/local/bin/loom daemon "*)
      daemon_processes="${daemon_processes}${proc##*/}: ${cmdline}\n"
      ;;
  esac
done
if [ "$daemon_processes" != "" ]; then
  printf '[supervisor-disabled-verify] daemon processes:\n%b' "$daemon_processes" >&2
  fail "workspace daemon process is running"
fi
echo "[supervisor-disabled-verify] ok: zero daemon processes"

control_artifacts="$(find "$CONFIG_ROOT" "$WORKSPACE_ROOT" \
  \( -type s -o -name 'daemon.pid' -o -name 'daemon.sock' -o -name 'agent.sock' \) \
  -print 2>/dev/null | sort -u)"
if [ "$control_artifacts" != "" ]; then
  printf '[supervisor-disabled-verify] daemon control artifacts:\n%s\n' "$control_artifacts" >&2
  fail "daemon control socket/pid artifacts exist"
fi
echo "[supervisor-disabled-verify] ok: zero daemon control sockets"

echo "[supervisor-disabled-verify] supervisor-disabled prompt-agent execution verified"
