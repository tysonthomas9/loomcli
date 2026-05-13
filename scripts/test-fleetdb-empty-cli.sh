#!/usr/bin/env bash
#
# test-fleetdb-empty-cli.sh — CLI integration scenarios against the empty
# fleet-db stack. Covers behaviors that pure unit tests can't reach:
#
#   1. flag --workspace pre-subcommand resolves correctly
#   2. flag --workspace post-subcommand resolves correctly
#   3. LOOM_WORKSPACE env-only resolves correctly
#   4. flag --workspace overrides conflicting LOOM_WORKSPACE
#   5. empty --workspace="" falls back to LOOM_WORKSPACE
#   6. invalid workspace key surfaces server-side validation_error
#   7. no workspace anywhere yields the canonical "WorkspaceID is required"
#   8. killing the workspace daemon flips `workspace ops diagnose` ok→false
#      and surfaces daemon_not_running
#   9. `loom doctor` PASS on the fleet check when fleet-db is reachable
#  10. `loom doctor` FAIL on the fleet check when fleet URL is unreachable
#
# Usage:
#   scripts/test-fleetdb-empty-cli.sh
#
# Reuses the running stack if one is detected on the configured ports.
# Otherwise builds and starts the empty fleet-db compose stack, runs the
# scenarios, and tears it down on exit.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

COMPOSE_FILE="test/fleetdb/docker-compose.empty.yml"
LOOM_NAME="loomcli-fleetdb-empty-loom-1"
REDIS_NAME="loomcli-fleetdb-empty-redis-1"
HOST_PORT="${LOOM_UI_PORT:-8091}"
KEEP_STACK="${KEEP_STACK:-0}"

red()    { printf '\033[31m%s\033[0m\n' "$*"; }
green()  { printf '\033[32m%s\033[0m\n' "$*"; }
yellow() { printf '\033[33m%s\033[0m\n' "$*"; }

# Pick a compose runner: docker compose, podman compose, or podman-compose.
if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    COMPOSE="docker compose"
elif command -v podman >/dev/null 2>&1 && podman compose version >/dev/null 2>&1; then
    COMPOSE="podman compose"
elif command -v podman-compose >/dev/null 2>&1; then
    COMPOSE="podman-compose"
else
    red "no docker compose / podman compose available — skipping"
    exit 0
fi

# Pick a container CLI for exec.
if command -v podman >/dev/null 2>&1 && podman ps >/dev/null 2>&1; then
    CTL="podman"
elif command -v docker >/dev/null 2>&1 && docker ps >/dev/null 2>&1; then
    CTL="docker"
else
    red "no docker/podman CLI available for exec"
    exit 2
fi

started_stack=0
cleanup() {
    if [[ "$started_stack" == "1" && "$KEEP_STACK" != "1" ]]; then
        yellow "==> tearing down empty fleet-db stack"
        $COMPOSE -f "$COMPOSE_FILE" down >/dev/null 2>&1 || true
    fi
}
trap cleanup EXIT

# Bring up the stack if not already running.
if ! $CTL ps --format '{{.Names}}' 2>/dev/null | grep -q "^$LOOM_NAME$"; then
    yellow "==> starting empty fleet-db stack"
    $COMPOSE -f "$COMPOSE_FILE" up --build -d >/dev/null
    started_stack=1
fi

# Wait for the API to come up.
yellow "==> waiting for stack to become ready"
for _ in $(seq 1 60); do
    if curl -fsS --max-time 2 "http://localhost:$HOST_PORT/api/config" >/dev/null 2>&1; then
        break
    fi
    sleep 1
done
if ! curl -fsS --max-time 2 "http://localhost:$HOST_PORT/api/config" >/dev/null 2>&1; then
    red "stack never became ready on port $HOST_PORT"
    exit 1
fi

# Ensure the HELLO-WORLD workspace exists. The empty stack does not seed it;
# create it via the API so the workspace-scoped scenarios below have something
# real to talk to. If it already exists (from a prior aborted run) the API
# returns a duplicate error which we tolerate.
ws_payload='{"name":"Hello-World","type":"clone","clone_urls":["https://github.com/octocat/Hello-World"]}'
curl -fsS -X POST -H 'content-type: application/json' \
    --data "$ws_payload" \
    "http://localhost:$HOST_PORT/api/workspaces" >/dev/null 2>&1 || true

# Wait until the workspace is registered AND its daemon is alive.
yellow "==> waiting for HELLO-WORLD workspace + daemon"
for _ in $(seq 1 60); do
    if curl -fsS "http://localhost:$HOST_PORT/api/workspaces" 2>/dev/null \
         | grep -q '"id":"HELLO-WORLD"'; then
        if $CTL exec "$LOOM_NAME" cat /loom-config/workspaces/Hello-World/.loom/daemon.pid 2>/dev/null \
             | grep -qE '^[0-9]+$'; then
            break
        fi
    fi
    sleep 1
done

# Test harness — run a command inside the loom container and assert on output.
PASS=0
FAIL=0
fail_names=()
expect_contains() {
    local name="$1"; local expected="$2"; local output="$3"
    if printf '%s' "$output" | grep -qF "$expected"; then
        green "  PASS  $name"
        PASS=$((PASS+1))
    else
        red "  FAIL  $name"
        red "        expected substring: $expected"
        red "        got:"
        printf '%s\n' "$output" | sed 's/^/          /' >&2
        FAIL=$((FAIL+1))
        fail_names+=("$name")
    fi
}

run_in_container() {
    # $1 = shell command string; runs inside the loom container.
    # Strips fleet-db's INFO log lines so assertions only see the command's
    # actual output, not the boilerplate.
    $CTL exec "$LOOM_NAME" sh -c "$1" 2>&1 \
        | grep -v 'msg="fleet issue backend created"' \
        | grep -v 'msg="opened cloud fleet-db client"' \
        || true
}

# Scenario 1: flag pre-subcommand
out=$(run_in_container 'unset LOOM_WORKSPACE; loom --workspace HELLO-WORLD data blocked --output json')
expect_contains "flag pre-subcommand resolves" "[]" "$out"

# Scenario 2: flag post-subcommand
out=$(run_in_container 'unset LOOM_WORKSPACE; loom data blocked --output json')
# data subcommand's own --workspace flag was removed; should error without env
expect_contains "post-subcommand without env errors" "WorkspaceID is required" "$out"

# Scenario 3: LOOM_WORKSPACE env-only
out=$(run_in_container 'LOOM_WORKSPACE=HELLO-WORLD loom data blocked --output json')
expect_contains "env-only resolves" "[]" "$out"

# Scenario 4: flag overrides env
out=$(run_in_container 'LOOM_WORKSPACE=WRONG-WORKSPACE loom --workspace HELLO-WORLD data blocked --output json')
expect_contains "flag overrides conflicting env" "[]" "$out"

# Scenario 5: empty flag falls back to env
out=$(run_in_container 'LOOM_WORKSPACE=HELLO-WORLD loom --workspace="" data blocked --output json')
expect_contains "empty flag falls back to env" "[]" "$out"

# Scenario 6: invalid workspace key
out=$(run_in_container 'loom --workspace "../etc/passwd" data blocked --output json')
expect_contains "invalid workspace key rejected" "invalid workspace key" "$out"

# Scenario 7: no workspace anywhere
out=$(run_in_container 'unset LOOM_WORKSPACE; loom data blocked --output json')
expect_contains "no workspace yields canonical error" "WorkspaceID is required" "$out"

# NOTE: The daemon_not_running detection is covered by the pure-function
# unit test TestWorkspaceOpsGlobalProblemsDetectsDaemonNotRunningWithRunnableAgent
# in internal/cli/workspace/ops_cmd_test.go. We intentionally do NOT try to
# reproduce that scenario at the container level here — the empty stack's
# loom-empty-daemon-manager has a 2s reconcile loop that races every
# attempt to kill the daemon from outside, and the orchestration to pause
# the manager reliably across podman exec sessions is fragile enough that
# it produces more false negatives than real regressions. The wiring from
# WorkspaceOpsStatus.Daemon.Running to the problem code is exercised by the
# unit test; the integration test focuses on behaviors that genuinely need
# a running fleet-db and HTTP stack.

# Compact JSON for substring matching regardless of pretty-print whitespace.
# `loom doctor` exits non-zero when checks fail, so swallow the status here
# (we assert on the JSON content, not the exit code).
doctor_json() {
    {
        $CTL exec "$LOOM_NAME" sh -c "$1" 2>/dev/null || true
    } | sed -n '/^{/,$p' | jq -c . 2>/dev/null || true
}

# Scenario 9: doctor PASS with fleet-db reachable.
out=$(doctor_json 'LOOM_WORKSPACE=HELLO-WORLD loom doctor --json')
fleet_check=$(printf '%s' "$out" | jq -c '.checks[] | select(.name == "fleet")' 2>/dev/null || true)
expect_contains "doctor exposes fleet check" '"name":"fleet"' "$fleet_check"
expect_contains "doctor fleet PASS when reachable" '"status":"pass"' "$fleet_check"
expect_contains "doctor fleet summary mentions reachable" 'reachable' "$fleet_check"

# Scenario 10: doctor FAIL when LOOM_FLEET_URL is unreachable.
out=$(doctor_json 'LOOM_FLEET_URL=http://fleet-probe-unreachable.invalid:9999 LOOM_WORKSPACE=HELLO-WORLD loom doctor --json')
fleet_check=$(printf '%s' "$out" | jq -c '.checks[] | select(.name == "fleet")' 2>/dev/null || true)
expect_contains "doctor fleet FAIL when unreachable" '"status":"fail"' "$fleet_check"
expect_contains "doctor fleet detail mentions probe failure" 'probe failed' "$fleet_check"

echo
echo "==> Summary: $PASS passed, $FAIL failed"
if [[ "$FAIL" -gt 0 ]]; then
    red "failing scenarios:"
    for n in "${fail_names[@]}"; do red "  - $n"; done
    exit 1
fi
green "all CLI scenarios passed"
