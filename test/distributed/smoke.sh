#!/bin/sh
set -eu

WORKSPACE="${WORKSPACE:-DIST}"
FLEET_URL="${FLEET_URL:-http://fleet-db:8080}"
LOOM_A_URL="${LOOM_A_URL:-http://loom-a:8080}"
LOOM_B_URL="${LOOM_B_URL:-http://loom-b:8080}"
UI_A_URL="${UI_A_URL:-http://ui-a:8080}"
UI_B_URL="${UI_B_URL:-http://ui-b:8080}"

pass() {
  printf '[distributed-smoke] PASS %s\n' "$1"
}

fail() {
  printf '[distributed-smoke] FAIL %s\n' "$1" >&2
  exit 1
}

wait_url() {
  name="$1"
  url="$2"
  for _ in $(seq 1 90); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      pass "$name reachable"
      return 0
    fi
    sleep 1
  done
  fail "$name not reachable at $url"
}

json_post() {
  actor="$1"
  url="$2"
  body="$3"
  curl -fsS -H "Content-Type: application/json" -H "X-Actor: $actor" \
    -X POST --data "$body" "$url"
}

http_post_status() {
  actor="$1"
  url="$2"
  body="$3"
  curl -sS -o /tmp/http-body.txt -w "%{http_code}" \
    -H "Content-Type: application/json" -H "X-Actor: $actor" \
    -X POST --data "$body" "$url"
}

wait_url "fleet-db" "$FLEET_URL/healthz"
wait_url "loom-a" "$LOOM_A_URL/api/config"
wait_url "loom-b" "$LOOM_B_URL/api/config"
wait_url "ui-a" "$UI_A_URL/api/config"
wait_url "ui-b" "$UI_B_URL/api/config"

create_ws_status="$(http_post_status smoke-admin "$FLEET_URL/api/v1/admin/workspaces" \
  "{\"key\":\"$WORKSPACE\",\"name\":\"Distributed Smoke\",\"repos\":[\"smoke-org/smoke-repo\"]}")"
case "$create_ws_status" in
  201|409) pass "workspace $WORKSPACE ready" ;;
  *) cat /tmp/http-body.txt >&2; fail "workspace create returned HTTP $create_ws_status" ;;
esac

cfg_a="$(curl -fsS "$LOOM_A_URL/api/config")"
cfg_b="$(curl -fsS "$LOOM_B_URL/api/config")"
printf '%s\n' "$cfg_a" | jq -e '.issue_backend == "fleet"' >/dev/null || fail "loom-a is not in fleet backend mode"
printf '%s\n' "$cfg_b" | jq -e '.issue_backend == "fleet"' >/dev/null || fail "loom-b is not in fleet backend mode"
pass "both loom servers are fleet-backed"

stamp="$(date +%s)"
auth_issue="$(json_post alice "$FLEET_URL/api/v1/$WORKSPACE/issues" \
  "{\"title\":\"distributed-smoke-auth-$stamp\",\"type\":\"task\",\"priority\":1,\"repo\":\"smoke-org/smoke-repo\"}")"
auth_issue_id="$(printf '%s\n' "$auth_issue" | jq -r '.id')"
[ -n "$auth_issue_id" ] && [ "$auth_issue_id" != "null" ] || fail "auth issue create did not return an id"

history="$(curl -fsS -H "X-Actor: auditor" "$FLEET_URL/api/v1/$WORKSPACE/issues/$auth_issue_id/history")"
printf '%s\n' "$history" | jq -e '.history[]? | select(.actor == "alice")' >/dev/null ||
  fail "history did not retain creating actor alice: $history"
pass "auth actor is recorded in audit history"

claim_issue="$(json_post claim-seed "$FLEET_URL/api/v1/$WORKSPACE/issues" \
  "{\"title\":\"distributed-smoke-claim-$stamp\",\"type\":\"task\",\"priority\":1,\"repo\":\"smoke-org/smoke-repo\"}")"
claim_issue_id="$(printf '%s\n' "$claim_issue" | jq -r '.id')"
[ -n "$claim_issue_id" ] && [ "$claim_issue_id" != "null" ] || fail "claim issue create did not return an id"

(http_post_status supervisor-a "$FLEET_URL/api/v1/$WORKSPACE/issues/$claim_issue_id/claim" '{"lock_ttl":60}' > /tmp/claim-a.status) &
(http_post_status supervisor-b "$FLEET_URL/api/v1/$WORKSPACE/issues/$claim_issue_id/claim" '{"lock_ttl":60}' > /tmp/claim-b.status) &
wait
claim_a="$(cat /tmp/claim-a.status)"
claim_b="$(cat /tmp/claim-b.status)"
case "$claim_a:$claim_b" in
  200:409|409:200) pass "distributed claim contention produced one winner and one collision" ;;
  *) fail "claim contention returned unexpected statuses a=$claim_a b=$claim_b" ;;
esac

heartbeat="$(curl -fsS -H "Content-Type: application/json" -H "X-Actor: supervisor-a" -X POST --data '{}' "$FLEET_URL/api/v1/$WORKSPACE/workers/supervisor-a/heartbeat")"
printf '%s\n' "$heartbeat" | jq -e '.success == true or .error == "no_active_session"' >/dev/null ||
  fail "supervisor heartbeat returned unexpected body: $heartbeat"
pass "local supervisor heartbeat endpoint is reachable"

baseline="$(curl -fsS -H "X-Actor: observer" "$FLEET_URL/api/v1/$WORKSPACE/events/mutations?since=0&limit=100")"
cursor="$(printf '%s\n' "$baseline" | jq -r '.cursor')"
[ -n "$cursor" ] && [ "$cursor" != "null" ] || fail "could not read baseline mutation cursor"

gap_issue="$(json_post sse-gap "$FLEET_URL/api/v1/$WORKSPACE/issues" \
  "{\"title\":\"distributed-smoke-sse-gap-$stamp\",\"type\":\"task\",\"priority\":1,\"repo\":\"smoke-org/smoke-repo\"}")"
gap_issue_id="$(printf '%s\n' "$gap_issue" | jq -r '.id')"
[ -n "$gap_issue_id" ] && [ "$gap_issue_id" != "null" ] || fail "gap issue create did not return an id"

sse_url="$LOOM_B_URL/api/workspaces/$WORKSPACE/events?since=$cursor"
sse_body="$(curl -sS -N --max-time 8 "$sse_url" 2>/tmp/sse.err || true)"
printf '%s\n' "$sse_body" | grep -q "$gap_issue_id" ||
  fail "SSE reconnect catch-up from loom-b did not include $gap_issue_id; body: $sse_body; err: $(cat /tmp/sse.err)"
pass "SSE reconnect catch-up works across loom instances"

# Cross-node transcript: seed an agent session + transcript artifact + transcript_ref via the control
# plane (loom seed-transcript -> fleet-db), then assert the NON-owning node (loom-b), which owns no local
# copy of the session, surfaces the transcript end-to-end. This exercises the full daemon-leaf
# transcript_ref path (WS1b): node-b resolves the session + transcript_ref from fleet-db AND reads the
# artifact bytes back via fleet-db's GET /artifacts/{id}/content, then parses the canonical entries.
tx_session="dist-smoke-tx-$stamp"
tx_task="dist-smoke-task-$stamp"
printf '%s\n%s\n%s\n' \
  '{"role":"system","type":"session_meta","text":"distributed-smoke seeded transcript"}' \
  '{"role":"assistant","type":"text","text":"cross-node transcript probe"}' \
  '{"role":"system","type":"result","text":"completed","output":"{\"input_tokens\":1,\"output_tokens\":1}"}' \
  > /tmp/tx.jsonl
loom daemon seed-transcript --workspace "$WORKSPACE" --session "$tx_session" --task "$tx_task" --content /tmp/tx.jsonl \
  || fail "seed-transcript failed"

# (a) Resolution: node-b lists the seeded session (from fleet-db) with has_transcript=true.
sess_body="$(curl -fsS "$LOOM_B_URL/api/workspaces/$WORKSPACE/tasks/$tx_task/sessions" 2>/tmp/tx.err || true)"
printf '%s\n' "$sess_body" | jq -e --arg s "$tx_session" '.data.sessions[]? | select(.session_id == $s) | .has_transcript == true' >/dev/null \
  || fail "loom-b did not resolve the seeded session's transcript_ref cross-node; body: $sess_body; err: $(cat /tmp/tx.err)"
pass "cross-node transcript_ref resolved from the non-owning node (has_transcript=true)"

# (b) Byte read: node-b serves the transcript itself, reading the artifact bytes back from fleet-db and
#     returning the canonical entries (session_meta head + terminal result).
tx_body="$(curl -fsS "$LOOM_B_URL/api/workspaces/$WORKSPACE/tasks/$tx_task/sessions/$tx_session/transcript" 2>/tmp/tx.err || true)"
printf '%s\n' "$tx_body" | jq -e '[.data.entries[].type] | (contains(["session_meta"]) and contains(["result"]))' >/dev/null \
  || fail "loom-b did not surface the transcript bytes cross-node; body: $tx_body; err: $(cat /tmp/tx.err)"
pass "cross-node transcript bytes surfaced from the non-owning node (canonical entries returned)"

curl -fsS "$UI_A_URL/api/health" >/dev/null || fail "ui-a /api/health failed"
curl -fsS "$UI_B_URL/api/health" >/dev/null || fail "ui-b /api/health failed"
pass "WebUI health checks passed on both instances"

printf '[distributed-smoke] SUMMARY auth=pass claims=pass heartbeat=pass sse_reconnect=pass transcript=pass webui=pass\n'
