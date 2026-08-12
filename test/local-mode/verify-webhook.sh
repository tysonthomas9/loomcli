#!/usr/bin/env bash
#
# Real-stack E2E for the trigger-driven GitHub webhook path.
#
# Exercises, against the running local-mode containers (real fleet-db + real
# loom serve), the durable ingestion path from the trigger-workflow proposal:
#
#   signed POST /api/workspaces/{ws}/webhooks/github
#     -> HMAC verified against the binding's webhook_secret
#     -> TriggerEvent persisted (signature_status=verified)
#     -> TriggerDelivery recorded and linked to a queued DriverRun
#     -> redelivering the same X-GitHub-Delivery produces NO duplicate effects
#
# Prereqs: a stack started via `make local-mode-up`. Requires curl, openssl,
# and python3 on the host.
#
# Note: this verifies the durable, non-blocking ingestion contract. Driving the
# pinned `.ts` workflow to completion additionally requires a flue-enabled
# executor in the stack; see docs/design/2026-06-07-trigger-workflow-proposal.md.
set -euo pipefail

LOOM_API="${LOCAL_MODE_API_URL:-http://localhost:${LOCAL_MODE_API_PORT:-8282}}"
FLEETDB_API="${LOCAL_MODE_FLEETDB_URL:-http://localhost:${LOCAL_MODE_FLEETDB_PORT:-8280}}"
WS="${LOOM_WORKSPACE:-LOCALMODE}"
API_KEY="${LOCAL_MODE_FLEETDB_API_KEY:-loom-local-mode-test-only-admin-key-v1}"
SECRET="${WEBHOOK_E2E_SECRET:-e2e-webhook-secret}"
ROUTE="github.pull_request.opened"
DELIVERY="e2e-delivery-$$"

LOOM_API="${LOOM_API%/}"
FLEETDB_API="${FLEETDB_API%/}"

for c in curl openssl python3; do
  command -v "$c" >/dev/null 2>&1 || { echo "[webhook-e2e] FATAL: $c is required" >&2; exit 127; }
done

say() { echo "[webhook-e2e] $*"; }
fail() { echo "[webhook-e2e] FAIL: $*" >&2; exit 1; }

# jfield <json> <key>            -> top-level string field (empty if missing)
jfield() { python3 -c 'import sys,json;print(json.load(sys.stdin).get(sys.argv[1],""))' "$2" <<<"$1"; }
# jlen <json> <array-key>        -> length of the named top-level array
jlen() { python3 -c 'import sys,json;print(len(json.load(sys.stdin).get(sys.argv[1],[])))' "$2" <<<"$1"; }
# github_sigstatus <events-json> -> signature_status of the first github event
github_sigstatus() {
  python3 -c 'import sys,json
o=json.load(sys.stdin)
print(next((e.get("signature_status","") for e in o.get("trigger_events",[]) if e.get("source_kind")=="github"), ""))' <<<"$1"
}

fdb() { # method path [body]
  local method="$1" path="$2" body="${3:-}"
  if [ -n "$body" ]; then
    curl -fsS --max-time 15 -X "$method" "$FLEETDB_API$path" \
      -H 'Content-Type: application/json' -H "X-API-Key: $API_KEY" -d "$body"
  else
    curl -fsS --max-time 15 -X "$method" "$FLEETDB_API$path" -H "X-API-Key: $API_KEY"
  fi
}

count() { # GET a fleet-db list endpoint, print len of the named array
  local path="$1" key="$2"
  jlen "$(fdb GET "$path")" "$key"
}

say "loom=$LOOM_API fleet-db=$FLEETDB_API ws=$WS route=$ROUTE"

# --- Arrange: register a driver + pinned version + signed binding -------------
DRIVER="github-pr-review"
VERSION="${DRIVER}-v1-$$"

say "creating driver $DRIVER"
fdb POST "/api/v1/$WS/drivers" \
  "{\"driver_id\":\"$DRIVER\",\"name\":\"$DRIVER\",\"status\":\"active\"}" >/dev/null || true

say "creating pinned driver version $VERSION"
fdb POST "/api/v1/$WS/drivers/$DRIVER/versions" \
  "{\"version_id\":\"$VERSION\",\"version\":1,\"source_ref\":\"e2e://github-pr-review\",\"source_digest\":\"sha256:e2e-src\",\"bundle_ref\":\".loom/drivers/$DRIVER/$VERSION\",\"bundle_digest\":\"sha256:e2e-bundle\",\"runtime\":\"flue-node\",\"validation_status\":\"passed\"}" >/dev/null

say "creating trigger binding for $ROUTE with webhook_secret"
BINDING_ID="binding-$ROUTE-$$"
BINDING_JSON="$(fdb POST "/api/v1/$WS/trigger-bindings" \
  "{\"binding_id\":\"$BINDING_ID\",\"name\":\"pr-review\",\"source_kind\":\"github\",\"route_key\":\"$ROUTE\",\"driver_id\":\"$DRIVER\",\"driver_version_id\":\"$VERSION\",\"target_entrypoint\":\"run\",\"webhook_secret\":\"$SECRET\",\"enabled\":true}")"
# The secret must be redacted on the create/read surface...
[ -z "$(jfield "$BINDING_JSON" webhook_secret)" ] \
  || fail "create response leaked webhook_secret (should be redacted)"
# ...but resolvable via the dedicated privileged endpoint.
SECRET_JSON="$(fdb GET "/api/v1/$WS/trigger-bindings/$BINDING_ID/webhook-secret")"
[ "$(jfield "$SECRET_JSON" webhook_secret)" = "$SECRET" ] \
  || fail "webhook-secret endpoint did not return the stored secret"
say "ok: binding created; secret redacted on read, resolvable via privileged endpoint"

# --- Act: send a signed pull_request.opened webhook --------------------------
PAYLOAD="{\"action\":\"opened\",\"number\":4242,\"pull_request\":{\"number\":4242},\"repository\":{\"full_name\":\"acme/widgets\"},\"sender\":{\"login\":\"octocat\"}}"
SIG="sha256=$(printf '%s' "$PAYLOAD" | openssl dgst -sha256 -hmac "$SECRET" | sed 's/^.* //')"

post_webhook() { # -> prints HTTP status to stdout, body to $1
  local out="$1"
  curl -sS -o "$out" -w '%{http_code}' --max-time 20 \
    -X POST "$LOOM_API/api/workspaces/$WS/webhooks/github" \
    -H 'Content-Type: application/json' \
    -H 'X-GitHub-Event: pull_request' \
    -H "X-GitHub-Delivery: $DELIVERY" \
    -H "X-Hub-Signature-256: $SIG" \
    --data "$PAYLOAD"
}

say "POST signed webhook (delivery=$DELIVERY)"
BODY1=/tmp/webhook-e2e-1.json
CODE1="$(post_webhook "$BODY1")"
[ "$CODE1" = "202" ] || fail "expected 202, got $CODE1 ($(cat "$BODY1"))"
RUN_ID="$(jfield "$(cat "$BODY1")" driver_run_id)"
[ -n "$RUN_ID" ] || fail "no driver_run_id in response"
say "ok: 202 Accepted, driver_run_id=$RUN_ID"

# --- Assert: durable records were created and linked -------------------------
EVENTS_AFTER1="$(count "/api/v1/$WS/trigger-events" trigger_events)"
DELIV_AFTER1="$(count "/api/v1/$WS/trigger-deliveries" trigger_deliveries)"
[ "$EVENTS_AFTER1" -ge 1 ] || fail "no trigger events persisted"
[ "$DELIV_AFTER1" -ge 1 ] || fail "no trigger deliveries persisted"

EVENTS_JSON="$(fdb GET "/api/v1/$WS/trigger-events")"
SIGSTATUS="$(github_sigstatus "$EVENTS_JSON")"
[ "$SIGSTATUS" = "verified" ] || fail "event signature_status=$SIGSTATUS, want verified"

RUN_JSON="$(fdb GET "/api/v1/$WS/driver-runs/$RUN_ID")"
[ "$(jfield "$RUN_JSON" source_kind)" = "github" ] || fail "run source_kind != github"
say "ok: TriggerEvent(verified) + TriggerDelivery + queued DriverRun present and linked"

# run events endpoint is reachable (inspectability)
fdb GET "/api/v1/$WS/driver-runs/$RUN_ID/events" >/dev/null || fail "run events endpoint failed"
say "ok: driver-run events endpoint reachable"

# --- Assert: redelivery is idempotent ----------------------------------------
say "re-POST same delivery id (expect dedup)"
BODY2=/tmp/webhook-e2e-2.json
CODE2="$(post_webhook "$BODY2")"
[ "$CODE2" = "202" ] || fail "redelivery expected 202, got $CODE2 ($(cat "$BODY2"))"
RUN_ID2="$(jfield "$(cat "$BODY2")" driver_run_id)"
[ "$RUN_ID2" = "$RUN_ID" ] || fail "redelivery created a new run ($RUN_ID2 != $RUN_ID)"

EVENTS_AFTER2="$(count "/api/v1/$WS/trigger-events" trigger_events)"
DELIV_AFTER2="$(count "/api/v1/$WS/trigger-deliveries" trigger_deliveries)"
[ "$EVENTS_AFTER2" = "$EVENTS_AFTER1" ] || fail "redelivery created a duplicate trigger event ($EVENTS_AFTER1 -> $EVENTS_AFTER2)"
[ "$DELIV_AFTER2" = "$DELIV_AFTER1" ] || fail "redelivery created a duplicate trigger delivery ($DELIV_AFTER1 -> $DELIV_AFTER2)"
say "ok: redelivery produced no duplicate event/delivery/run"

say "PASS — signed GitHub webhook enqueued a pinned DriverRun durably and idempotently"
