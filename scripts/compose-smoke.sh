#!/usr/bin/env bash
# End-to-end smoke test of the shipped compose stacks.
#
#   make compose-smoke        # this script
#   make compose-smoke-down   # tear down a run left up by --keep
#
# Why this exists: docker-compose.dev.yml and deploy/docker-compose.yml rotted
# into an unstartable state precisely because nothing in the Makefile or CI
# ever ran them. Static assertions in makefile_test.go catch a deleted line;
# only actually starting the stack catches a broken image.
#
# What it asserts (each maps to a filed defect):
#   1. the server does not crash-loop            (missing issue backend)
#   2. the API answers from the HOST             (bound to 127.0.0.1 in-netns)
#   3. the container healthcheck reports healthy (wget in a distroless image)
#   4. the terminal WS upgrades and a PTY spawns (no shell in the image)
#   5. tab metadata survives a container RECREATE (state on the container fs)
#   6. the snapshot really is on the named volume
#
# Requires: docker compose >= 2.22, curl, jq, and a sibling fleet-db checkout.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT/docker-compose.dev.yml"
PROD_COMPOSE_FILE="$ROOT/deploy/docker-compose.yml"
FLEETDB_SRC="$ROOT/../fleet-db"

PROJECT="${DEV_COMPOSE_PROJECT:-loomcli-dev}"
API_PORT="${DEV_API_PORT:-8080}"
API="http://localhost:${API_PORT}"
WS="SMOKE"
# lead-shell-<n> maps to a plain login bash. The lead-<backend>-<n> form runs
# `loom lead --backend …`, which exits immediately here because the image
# carries no AI CLI — see deploy/README.md.
SESSION="lead-shell-1"
ACTOR_HEADER=(-H "X-Actor: compose-smoke@local")

KEEP=0
[ "${1:-}" = "--keep" ] && KEEP=1

# Engine selection mirrors the Makefile's compose-runner detection.
if [ -n "${COMPOSE_CMD:-}" ]; then
  # shellcheck disable=SC2206
  COMPOSE_BIN=(${COMPOSE_CMD})
elif command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  COMPOSE_BIN=(docker compose)
elif command -v podman >/dev/null 2>&1 && podman compose version >/dev/null 2>&1; then
  COMPOSE_BIN=(podman compose)
else
  echo "docker compose or podman compose is required" >&2
  exit 127
fi
COMPOSE=("${COMPOSE_BIN[@]}" -f "$COMPOSE_FILE")

FAILURES=0
step()  { printf '\n\033[1m== %s\033[0m\n' "$*"; }
pass()  { printf '  \033[32mPASS\033[0m %s\n' "$*"; }
fail()  { printf '  \033[31mFAIL\033[0m %s\n' "$*"; FAILURES=$((FAILURES + 1)); }

cleanup() {
  local rc=$?
  if [ "$KEEP" = "1" ]; then
    echo
    echo "--keep: stack left running. Tear down with: make compose-smoke-down"
    return $rc
  fi
  step "Tearing down (down -v — the volume must not leak into the next run)"
  "${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
  return $rc
}
trap cleanup EXIT

require_src() {
  if [ ! -f "$FLEETDB_SRC/deploy/docker/Dockerfile" ]; then
    echo "fleet-db checkout not found at $FLEETDB_SRC" >&2
    echo "This stack builds fleet-db from the sibling checkout, like the other" >&2
    echo "stacks in this repo. Clone it next to loomcli:" >&2
    echo "  git clone https://github.com/BrowserOperator/fleet-db $FLEETDB_SRC" >&2
    exit 1
  fi
}

require_tool() {
  command -v "$1" >/dev/null 2>&1 || { echo "$1 is required" >&2; exit 1; }
}

cid() { "${COMPOSE[@]}" ps -q server; }

wait_http() {
  local url="$1" tries="${2:-90}"
  for _ in $(seq 1 "$tries"); do
    curl -fsS "$url" >/dev/null 2>&1 && return 0
    sleep 1
  done
  return 1
}

# ─── Preflight ───────────────────────────────────────────────────────────────
require_src
require_tool curl
require_tool jq

step "Validating both compose files (config -q)"
"${COMPOSE[@]}" config -q && pass "docker-compose.dev.yml parses"
( cd "$ROOT/deploy" && "${COMPOSE_BIN[@]}" -f "$PROD_COMPOSE_FILE" config -q ) \
  && pass "deploy/docker-compose.yml parses"

# A stale stack from a previous run would make every assertion below lie.
"${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true

step "Building and starting the stack"
"${COMPOSE[@]}" up -d --build

step "Waiting for the stack to answer"
if ! wait_http "$API/health"; then
  echo "server never answered $API/health. Recent logs:" >&2
  "${COMPOSE[@]}" logs --tail 60 server >&2 || true
  exit 1
fi

# ─── 1. no crash loop ────────────────────────────────────────────────────────
step "1. server did not crash-loop"
SERVER_CID="$(cid)"
sleep 20   # long enough for a restart loop to show a nonzero count
restarts="$(docker inspect -f '{{.RestartCount}}' "$SERVER_CID")"
running="$(docker inspect -f '{{.State.Running}}' "$SERVER_CID")"
if [ "$restarts" = "0" ] && [ "$running" = "true" ]; then
  pass "RestartCount=0, Running=true"
else
  fail "RestartCount=$restarts Running=$running"
  "${COMPOSE[@]}" logs --tail 60 server || true
fi

# Ephemeral state would make assertions 5 and 6 meaningless, and it is logged
# rather than fatal — so check for it explicitly.
if "${COMPOSE[@]}" logs server 2>&1 | grep -q "no snapshot path — state is ephemeral"; then
  fail "server reports an ephemeral snapshot path (LOOM_CONFIG_DIR/HOME unset?)"
else
  pass "snapshot path resolved (state is not ephemeral)"
fi

# ─── 2. reachable from the host ──────────────────────────────────────────────
step "2. API reachable from the host"
if curl -fsS "$API/health" >/dev/null; then
  pass "GET $API/health → 200"
else
  fail "GET $API/health failed"
fi

# ─── 3. healthcheck ──────────────────────────────────────────────────────────
step "3. container healthcheck passes"
health=""
for _ in $(seq 1 60); do
  health="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{end}}' "$SERVER_CID" || true)"
  [ "$health" = "healthy" ] && break
  sleep 2
done
if [ "$health" = "healthy" ]; then
  pass "State.Health.Status=healthy"
else
  fail "State.Health.Status=${health:-<none>}"
fi

# ─── 3b. seed ────────────────────────────────────────────────────────────────
step "3b. seeding the local workspace"
if "${COMPOSE[@]}" exec -T server bash -s < "$ROOT/scripts/compose-dev-seed.sh"; then
  pass "seed script ran"
else
  fail "seed script failed"
fi
if curl -fsS "${ACTOR_HEADER[@]}" "$API/api/workspaces/$WS/terminal/tabs" >/dev/null; then
  pass "GET /api/workspaces/$WS/terminal/tabs → 200"
else
  fail "terminal tabs endpoint did not answer for $WS"
fi

# Create the smoke tab deterministically rather than relying on the auto-minted
# default. No launch spec in the body, so the session falls through to the
# legacy lead-shell-* argv (a login bash).
curl -fsS -X PUT "${ACTOR_HEADER[@]}" -H 'Content-Type: application/json' \
  -d '{"label":"smoke"}' \
  "$API/api/workspaces/$WS/terminal/tabs/$SESSION" >/dev/null \
  && pass "created tab $SESSION" || fail "could not create tab $SESSION"

# ─── 4a. WS upgrade ──────────────────────────────────────────────────────────
step "4. terminal WebSocket upgrades and a PTY spawns"
TOKEN="$(curl -fsS "${ACTOR_HEADER[@]}" \
  "$API/api/workspaces/$WS/terminal/token?session=$SESSION" | jq -r .token)"
if [ -z "$TOKEN" ] || [ "$TOKEN" = "null" ]; then
  fail "could not mint a terminal token"
else
  pass "minted a terminal token"
fi

# curl is sufficient: the assertion is on the 101 status line. curl then holds
# the open connection until --max-time fires (exit 28), which is also what
# gives 4b its window — the relay spawns the PTY on upgrade. So: assert on the
# captured line, never on curl's exit status.
WS_OUT="$(mktemp)"
(
  curl -sS -i --http1.1 --max-time 20 "${ACTOR_HEADER[@]}" \
    -H "Connection: Upgrade" -H "Upgrade: websocket" \
    -H "Sec-WebSocket-Version: 13" -H "Sec-WebSocket-Key: AAAAAAAAAAAAAAAAAAAAAA==" \
    "$API/api/workspaces/$WS/terminal/ws?session=$SESSION&token=$TOKEN" \
    > "$WS_OUT" 2>/dev/null || true
) &
WS_PID=$!

# 4b, while the connection is still open. POLL rather than sampling once at a
# fixed offset: the spawn is asynchronous (workspace resolve, then fork+exec),
# and a single check a few seconds after the upgrade is a race that reports a
# working PTY as broken. The shell is `$SHELL -l` for a lead-shell-* session.
pty_found=0
for _ in $(seq 1 15); do
  if "${COMPOSE[@]}" exec -T server pgrep -f 'bash -l' >/dev/null 2>&1; then
    pty_found=1
    break
  fi
  sleep 1
done
if [ "$pty_found" = "1" ]; then
  pass "a PTY child (bash -l) is running in the container"
else
  fail "no PTY child found in the container"
  "${COMPOSE[@]}" exec -T server ps -ef || true
fi

wait "$WS_PID" 2>/dev/null || true
if head -1 "$WS_OUT" | grep -q "101 Switching Protocols"; then
  pass "WS handshake → 101 Switching Protocols"
else
  fail "WS handshake did not upgrade: $(head -1 "$WS_OUT" || echo '<no response>')"
fi
rm -f "$WS_OUT"

# ─── 5. survives a recreate ──────────────────────────────────────────────────
step "5. tab metadata survives a container recreate"
# The snapshot is written on a 30s interval and flushed on close by a goroutine
# nobody waits for. Racing that would fail this assertion for reasons unrelated
# to the fix, so wait for the tab to reach disk before removing the container.
snapshotted=0
for _ in $(seq 1 40); do
  if "${COMPOSE[@]}" exec -T server grep -q "$SESSION" \
      /var/lib/loom/terminal-state/snapshot.json 2>/dev/null; then
    snapshotted=1
    break
  fi
  sleep 1
done
if [ "$snapshotted" = "1" ]; then
  pass "$SESSION reached terminal-state/snapshot.json"
else
  fail "$SESSION never reached the snapshot file"
fi

before="$(curl -fsS "${ACTOR_HEADER[@]}" "$API/api/workspaces/$WS/terminal/tabs" \
  | jq -r '.data[].session_name' | sort)"

# rm -sf + up, NOT restart: a restart already preserved tabs while the defect
# stood, so a restart-only test passes on a broken stack. This is the
# load-bearing assertion.
"${COMPOSE[@]}" rm -sf server >/dev/null
"${COMPOSE[@]}" up -d server >/dev/null
SERVER_CID="$(cid)"
if ! wait_http "$API/health"; then
  fail "server did not come back after recreate"
else
  after="$(curl -fsS "${ACTOR_HEADER[@]}" "$API/api/workspaces/$WS/terminal/tabs" \
    | jq -r '.data[].session_name' | sort)"
  if [ "$before" = "$after" ] && printf '%s' "$after" | grep -q "$SESSION"; then
    pass "tab list identical across the recreate"
  else
    fail "tabs changed across recreate: before=[$before] after=[$after]"
  fi
fi

# ─── 6. snapshot lives on the volume ─────────────────────────────────────────
step "6. the snapshot is on the named volume, not the container filesystem"
"${COMPOSE[@]}" rm -sf server >/dev/null
VOLUME="${PROJECT}_loom-data"
if snap="$(docker run --rm -v "${VOLUME}:/v" busybox cat /v/terminal-state/snapshot.json 2>/dev/null)"; then
  if printf '%s' "$snap" | grep -q "$SESSION"; then
    pass "$VOLUME holds terminal-state/snapshot.json containing $SESSION"
  else
    fail "snapshot on $VOLUME does not mention $SESSION"
  fi
else
  fail "could not read terminal-state/snapshot.json from volume $VOLUME"
fi

# ─── Verdict ─────────────────────────────────────────────────────────────────
echo
if [ "$FAILURES" = "0" ]; then
  printf '\033[32mcompose smoke: all assertions passed\033[0m\n'
else
  printf '\033[31mcompose smoke: %d assertion(s) failed\033[0m\n' "$FAILURES"
  exit 1
fi
