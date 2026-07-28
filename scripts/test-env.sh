#!/usr/bin/env bash
# Manage the disposable fleet-db backend used by tests that need a real control
# plane.
#
#   scripts/test-env.sh up       # build, start, wait for /healthz, create LOOMTEST
#   scripts/test-env.sh env      # print the exports; eval this into your shell
#   scripts/test-env.sh status   # container state + health + issue count
#   scripts/test-env.sh logs     # follow fleet-db logs
#   scripts/test-env.sh reset    # wipe and recreate the workspace, stack stays up
#   scripts/test-env.sh down     # stop and remove containers + volumes
#
# Typical use:
#   scripts/test-env.sh up
#   eval "$(scripts/test-env.sh env)"
#   go test ./... -run TestSomethingThatNeedsFleetDB
#   scripts/test-env.sh down
#
# Why this exists: the deployed stack on :3011 holds the live
# workspace. A test that creates probe issues or mutates an agentdef there is
# writing to production data, and cleaning up afterwards is best-effort at best.
# This stack is disposable by construction — `down` takes the volumes with it.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE=(docker compose -f "$ROOT/test/testenv/docker-compose.yml")
URL="http://127.0.0.1:53351"
WORKSPACE="${LOOM_TEST_WORKSPACE:-LOOMTEST}"
ACTOR="loom"
FLEETDB_SRC="$ROOT/../fleet-db"

require_src() {
  if [ ! -f "$FLEETDB_SRC/deploy/docker/Dockerfile" ]; then
    echo "fleet-db checkout not found at $FLEETDB_SRC" >&2
    echo "This stack builds fleet-db from the sibling checkout, like the other" >&2
    echo "stacks in this repo. Clone it next to loomcli:" >&2
    echo "  git clone https://github.com/BrowserOperator/fleet-db $FLEETDB_SRC" >&2
    exit 1
  fi
}

wait_healthy() {
  printf 'Waiting for fleet-db at %s/healthz ' "$URL"
  for _ in $(seq 1 60); do
    if curl -fsS "$URL/healthz" >/dev/null 2>&1; then
      echo "— ready."
      return 0
    fi
    printf '.'
    sleep 1
  done
  echo
  echo "fleet-db did not become healthy in time. Recent logs:" >&2
  "${COMPOSE[@]}" logs --tail 40 fleet-db >&2 || true
  return 1
}

# Idempotent: a workspace that already exists is success, not an error, so `up`
# can be re-run against a live stack without special-casing.
ensure_workspace() {
  local code
  code="$(curl -s -o /dev/null -w '%{http_code}' \
    -H "X-Actor: $ACTOR" "$URL/api/v1/admin/workspaces/$WORKSPACE" || true)"
  if [ "$code" = "200" ]; then
    echo "Workspace $WORKSPACE already present."
    return 0
  fi
  echo "Creating workspace $WORKSPACE …"
  curl -fsS -X POST -H "X-Actor: $ACTOR" -H 'Content-Type: application/json' \
    -d "{\"key\":\"$WORKSPACE\",\"name\":\"$WORKSPACE\"}" \
    "$URL/api/v1/admin/workspaces" >/dev/null
  echo "Created."
}

case "${1:-up}" in
  up)
    require_src
    "${COMPOSE[@]}" up -d --build
    wait_healthy
    ensure_workspace
    echo
    echo "Test environment ready. Point your shell at it with:"
    echo "  eval \"\$($0 env)\""
    ;;
  env)
    # Printed rather than exported so the caller decides the scope. Deliberately
    # includes the workspace: a client with the URL but no LOOM_WORKSPACE falls
    # back to whatever the ambient config says, which is how a test ends up
    # writing to the live workspace.
    echo "export LOOM_FLEET_DB_URL=$URL"
    echo "export LOOM_WORKSPACE=$WORKSPACE"
    echo "export LOOM_FLEET_DB_ACTOR=$ACTOR"
    # LOOM_SERVER_URL would route issue data through a loom server this stack
    # does not run; unset it so a stale value cannot split the backends.
    echo "unset LOOM_SERVER_URL"
    ;;
  status)
    "${COMPOSE[@]}" ps
    if curl -fsS "$URL/healthz" >/dev/null 2>&1; then
      echo "healthz: ok ($URL)"
      local_count="$(curl -fsS -H "X-Actor: $ACTOR" \
        "$URL/api/v1/$WORKSPACE/issues?limit=1000" 2>/dev/null \
        | grep -o '"id"' | wc -l | tr -d ' ' || echo '?')"
      echo "workspace $WORKSPACE: $local_count issue(s)"
    else
      echo "healthz: unreachable ($URL)"
    fi
    ;;
  logs)
    "${COMPOSE[@]}" logs -f fleet-db
    ;;
  reset)
    # Recreating the stack is the only reliable wipe. There is no API path to an
    # empty workspace: fleet-db refuses to delete one that still has issues —
    #   DELETE /api/v1/admin/workspaces/LOOMTEST
    #   409 {"error":{"code":"conflict","message":"workspace contains issues"}}
    # — and issues have no delete endpoint to empty it with first. Dropping the
    # Redis volume is what actually clears state.
    "${COMPOSE[@]}" down -v
    "${COMPOSE[@]}" up -d --build
    wait_healthy
    ensure_workspace
    # Assert rather than assume: a reset that silently left data behind would
    # surface later as a test failure with no obvious cause.
    remaining="$(curl -fsS -H "X-Actor: $ACTOR" \
      "$URL/api/v1/$WORKSPACE/issues?limit=1000" 2>/dev/null \
      | grep -o '"id"' | wc -l | tr -d ' ')"
    if [ "$remaining" != "0" ]; then
      echo "reset failed: $WORKSPACE still holds $remaining issue(s)" >&2
      exit 1
    fi
    echo "Reset: $WORKSPACE is empty."
    ;;
  down)
    "${COMPOSE[@]}" down -v
    ;;
  *)
    echo "usage: $0 {up|env|status|logs|reset|down}" >&2
    exit 2
    ;;
esac
