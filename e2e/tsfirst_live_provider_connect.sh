#!/usr/bin/env bash
# Opt-in live-provider TypeScript-first local-connect E2E.
#
# This test requires a real configured provider CLI. It creates a temporary
# .loom project with one typed tool, runs `loom check`, then runs
# `loom connect --json` through the selected backend and verifies the trusted
# typed-tool call/result evidence plus same-turn result follow-up.

set -euo pipefail

BACKEND="${BACKEND:-${LOOM_BACKEND:-codex}}"
MODEL="${MODEL:-${TSFIRST_LIVE_MODEL:-}}"
ROOT="${RESULT_ROOT:-$(mktemp -d /tmp/loom-tsfirst-live-connect.XXXXXX)}"
ARTIFACTS_OUT="${ARTIFACTS_OUT:-}"
CONNECT_TIMEOUT="${CONNECT_TIMEOUT:-180s}"
AGENT_NAME="live-tool-agent"
SESSION_NAME="live-provider-typed-tools"
RESULT_JSON="$ROOT/connect-result.json"
CHECK_LOG="$ROOT/check.log"
CONNECT_LOG="$ROOT/connect.stderr.log"

cleanup() {
    status=$?
    set +e
    echo "RESULT_ROOT=$ROOT"
    if [[ -n "$ARTIFACTS_OUT" ]]; then
        mkdir -p "$ARTIFACTS_OUT"
        cp -a "$ROOT" "$ARTIFACTS_OUT/"
        echo "RESULT_COPY=$ARTIFACTS_OUT/$(basename "$ROOT")"
    fi
    if [[ "$status" -ne 0 ]]; then
        echo "---- tsfirst live provider connect debug ----" >&2
        echo "backend: $BACKEND" >&2
        echo "root: $ROOT" >&2
        [[ -f "$CHECK_LOG" ]] && { echo "---- loom check ----" >&2; cat "$CHECK_LOG" >&2; }
        [[ -f "$CONNECT_LOG" ]] && { echo "---- loom connect stderr ----" >&2; cat "$CONNECT_LOG" >&2; }
        [[ -f "$RESULT_JSON" ]] && { echo "---- connect result ----" >&2; cat "$RESULT_JSON" >&2; }
    fi
    exit "$status"
}
trap cleanup EXIT INT TERM

require() {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "required command not found on PATH: $1" >&2
        exit 1
    fi
}

run_with_timeout() {
    if command -v timeout >/dev/null 2>&1; then
        timeout "$CONNECT_TIMEOUT" "$@"
    else
        "$@"
    fi
}

require loom
require jq
require node

if ! command -v "$BACKEND" >/dev/null 2>&1; then
    echo "backend CLI not found on PATH: $BACKEND" >&2
    exit 1
fi

set +e
health_json="$(loom backend health --json 2>/dev/null)"
health_status=$?
set -e
if [[ -n "$health_json" ]] && ! jq -e --arg backend "$BACKEND" '.[] | select(.name == $backend) | .healthy == true' >/dev/null <<<"$health_json"; then
    echo "backend $BACKEND is not healthy according to loom backend health --json" >&2
    jq -r --arg backend "$BACKEND" '.[] | select(.name == $backend) | "healthy=\(.healthy) installed=\(.installed) api_key_set=\(.api_key_set) message=\(.message)"' <<<"$health_json" >&2 || true
    exit 1
fi
if [[ -z "$health_json" && "$health_status" -ne 0 ]]; then
    echo "loom backend health --json failed before returning health data" >&2
    exit 1
fi

mkdir -p "$ROOT/.loom/agents" "$ROOT/.loom/tools"

MODEL_LINE=""
if [[ -n "$MODEL" ]]; then
    MODEL_LINE="  model: '$MODEL',"
fi

cat > "$ROOT/.loom/tools/create-channel.ts" <<'TS'
import { Type, defineTool } from '@loom/runtime';

export default defineTool({
  name: 'create_channel',
  description: 'Create one approved live-provider E2E channel.',
  parameters: Type.Object({
    name: Type.String({ description: 'Channel name' }),
  }),
  handler: 'workflow',
  timeout: '30s',
  cancellable: true,
  execute: async ({ name }) => `created ${name}`,
});
TS

cat > "$ROOT/.loom/agents/$AGENT_NAME.ts" <<TS
import { createAgent, runtime } from '@loom/runtime';
import createChannel from '../tools/create-channel';

export default createAgent({
  name: '$AGENT_NAME',
  description: 'Live-provider TypeScript-first typed-tool local-connect E2E agent',
  backend: '$BACKEND',
$MODEL_LINE
  runtime: runtime.local({ repos: ['.'] }),
  instructions: 'For this test, follow the reviewed typed tool protocol exactly.',
  tools: [createChannel],
});
TS

if ! loom check --dir "$ROOT" >"$CHECK_LOG" 2>&1; then
    echo "loom check failed for live provider TypeScript-first fixture" >&2
    exit 1
fi

MESSAGE='Use the reviewed typed tool to create channel "triage". First emit exactly one loom.typed_tool.call JSON object and no prose. After Loom gives you the tool result, reply exactly: LIVE_TYPED_TOOL_DONE: <tool result>.'

if ! run_with_timeout loom connect "$AGENT_NAME" local \
    --dir "$ROOT" \
    --session "$SESSION_NAME" \
    --message "$MESSAGE" \
    --json >"$RESULT_JSON" 2>"$CONNECT_LOG"; then
    echo "loom connect failed for live provider TypeScript-first fixture" >&2
    exit 1
fi

jq -e '
  .agent == "live-tool-agent" and
  .tool_runtime.status == "backend_typed_tool_runtime" and
  .tool_runtime.handler_execution == "trusted_executor_configured" and
  .tool_runtime.schema_publication == "prompt_json_contract" and
  .tool_runtime.result_feed == "same_turn_prompt_followup" and
  (.tool_calls | length) >= 1 and
  (.tool_calls[0].name == "create_channel") and
  (.tool_calls[0].status == "completed") and
  (.tool_calls[0].authorization_status == "authorized") and
  (.tool_calls[0].result == "created triage") and
  (.response | contains("LIVE_TYPED_TOOL_DONE")) and
  (.response | contains("created triage")) and
  (.operation.tool_calls | length) >= 1
' "$RESULT_JSON" >/dev/null

echo "PASS tsfirst live-provider local-connect typed-tool E2E"
echo "BACKEND=$BACKEND"
echo "RESULT_JSON=$RESULT_JSON"
