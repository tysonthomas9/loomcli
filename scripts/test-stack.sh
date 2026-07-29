#!/usr/bin/env bash
# Recreate a throwaway, production-shaped stack and verify it actually runs agents.
#
# Every invocation of `up` destroys the previous stack first. A fix validated
# against a stack carrying state from the last run has not been validated.
set -euo pipefail

cd "$(dirname "$0")/.."

PROJECT="loomcli-test-mirror"
WORKSPACE="TESTMIRROR"
API="http://127.0.0.1:8382"
UI="http://localhost:8383/ws/${WORKSPACE}/kanban"
COMPOSE=(docker compose -p "$PROJECT"
  -f test/local-mode/docker-compose.yml
  -f test/local-mode/docker-compose.dogfood-mirror.yml)

usage() { sed -n '2,8p' "$0"; exit 1; }

wait_for_api() {
  for _ in $(seq 1 90); do
    if curl -fsS -m 2 "${API}/api/workspaces" >/dev/null 2>&1; then return 0; fi
    sleep 2
  done
  echo "API did not come up at ${API}" >&2
  return 1
}

case "${1:-}" in
  up)
    echo "==> destroying any previous stack"
    "${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
    # The UI sidecar bind-mounts the gitignored frontend bundle. Docker
    # substitutes an empty directory when it is missing, so the UI comes up
    # "running" and serves 404 with nothing in any log. Build it
    # first — going straight to `docker compose` skips this guard.
    echo "==> ensuring the web UI bundle exists"
    make ensure-frontend-dist
    echo "==> building and starting"
    "${COMPOSE[@]}" up -d --build
    echo "==> waiting for the API"
    wait_for_api
    echo "==> verifying"
    "$0" verify
    echo
    echo "Stack ready."
    echo "  UI:  ${UI}"
    echo "  API: ${API}"
    ;;
  verify)
    # A stack that is up but has no supervisor cannot validate anything, and
    # that failure is silent from the outside — check it explicitly.
    agents=$(docker exec "${PROJECT}-loom-local-1" sh -c \
      'LOOM_FLEET_DB_URL=http://fleet-db:8080 LOOM_WORKSPACE='"$WORKSPACE"' LOOM_FLEET_DB_ACTOR=loom loom agentdef list 2>/dev/null' \
      | grep -c 'role=' || true)
    echo "    agents configured: ${agents}"
    [ "${agents:-0}" -ge 3 ] || { echo "expected at least 3 agents (planner, worker, critic)" >&2; exit 1; }

    # A UI that serves 404 is indistinguishable from a healthy one unless asked.
    ui_code=$(curl -s -m 5 -o /dev/null -w '%{http_code}' "http://localhost:8383/" || true)
    echo "    UI returns: ${ui_code}"
    [ "$ui_code" = "200" ] || { echo "UI is not serving the app — is frontend/dist built?" >&2; exit 1; }

    status=$(docker exec "${PROJECT}-loom-local-1" sh -c \
      'LOOM_FLEET_DB_URL=http://fleet-db:8080 LOOM_WORKSPACE='"$WORKSPACE"' LOOM_FLEET_DB_ACTOR=loom loom daemon status 2>/dev/null' \
      | head -1 || true)
    echo "    ${status:-daemon status unavailable}"
    # Match precisely, and put the failure string FIRST. `loom daemon status`
    # prints exactly "Daemon: running (PID n)" or "Daemon: not running"
    # (internal/cli/daemon/daemon_cmd.go:491,495) — a `*running*` glob matches
    # BOTH, so the check passed on a dead supervisor, which is the one silent
    # failure this stack exists to surface. Anything unrecognised fails too:
    # an empty status means `docker exec` itself did not work.
    case "$status" in
      *"Daemon: not running"*)
        echo "daemon is not running — see: $0 logs" >&2; exit 1 ;;
      *"Daemon: running"*) ;;
      *)
        echo "unrecognised daemon status: [${status:-<empty>}] — see: $0 logs" >&2; exit 1 ;;
    esac
    ;;
  env)
    echo "export LOOM_FLEET_DB_URL=http://127.0.0.1:8380"
    echo "export LOOM_WORKSPACE=${WORKSPACE}"
    echo "export LOOM_FLEET_DB_ACTOR=loom"
    echo "unset LOOM_SERVER_URL"
    ;;
  logs)   "${COMPOSE[@]}" logs --tail "${2:-80}" ;;
  status) "${COMPOSE[@]}" ps ;;
  down)   "${COMPOSE[@]}" down -v --remove-orphans ;;
  *) usage ;;
esac
