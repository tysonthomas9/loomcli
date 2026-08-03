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
#   8. `loom doctor` PASS on the fleet check when fleet-db is reachable
#   9. `loom doctor` FAIL on the fleet check when fleet URL is unreachable
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

# Wait until the workspace is registered and the single runtime reports that
# the workspace capability set is ready.
yellow "==> waiting for HELLO-WORLD workspace runtime"
for _ in $(seq 1 60); do
    if curl -fsS "http://localhost:$HOST_PORT/api/workspaces" 2>/dev/null \
         | grep -q '"id":"HELLO-WORLD"'; then
        if curl -fsS "http://localhost:$HOST_PORT/api/workspaces/HELLO-WORLD/readyz" 2>/dev/null \
             | grep -q '"ready":true'; then
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

# Scenario 11: human-readable doctor text output sanity.
# Asserts the headed format renders (banner + check rows + summary line),
# not just the JSON path.
text_out=$($CTL exec "$LOOM_NAME" sh -c \
    'cd /loom-config/workspaces/Hello-World && LOOM_WORKSPACE=HELLO-WORLD loom doctor 2>/dev/null' \
    2>&1 || true)
expect_contains "doctor text output has banner"      "Loom Doctor"            "$text_out"
expect_contains "doctor text output shows fleet check" "fleet configured"     "$text_out"
expect_contains "doctor text output ends with summary" "checks passed"        "$text_out"

# Scenario 12: human-readable diagnose text output sanity.
text_out=$($CTL exec "$LOOM_NAME" \
    loom workspace ops diagnose HELLO-WORLD 2>/dev/null || true)
expect_contains "diagnose text output names workspace" "Workspace: HELLO-WORLD" "$text_out"
expect_contains "diagnose text output reports runtime" "Runtime:"               "$text_out"
expect_contains "diagnose text output lists repos"    "Repos:"                  "$text_out"
expect_contains "diagnose text output lists agents"   "Agents:"                 "$text_out"
expect_contains "diagnose text marks runtime not applicable in fleet mode" \
    "Runtime:   not applicable" "$text_out"

# Scenario 12b: JSON local_runtime is shaped for fleet mode (applicable=false,
# no misleading runtime / error fields). Confirms the response is
# self-explanatory rather than reporting a false-negative "unhealthy" for a
# concept that does not apply to this deployment.
diag_json=$($CTL exec "$LOOM_NAME" \
    loom workspace ops diagnose HELLO-WORLD --json 2>/dev/null \
    | sed -n '/^{/,$p' | jq -c '.local_runtime' 2>/dev/null || true)
expect_contains "diagnose JSON local_runtime applicable=false in fleet" \
    '"applicable":false' "$diag_json"
expect_contains "diagnose JSON local_runtime has explanatory reason" \
    '"reason":"fleet mode' "$diag_json"
# No "error" field with the runtime.json ENOENT — that was the misleading
# string the lead agent was rendering.
if printf '%s' "$diag_json" | grep -qF '"error":"open '; then
    red "  FAIL  diagnose JSON local_runtime should not surface ENOENT error in fleet"
    red "        got: $diag_json"
    FAIL=$((FAIL+1))
    fail_names+=("diagnose JSON local_runtime should not surface ENOENT error in fleet")
else
    green "  PASS  diagnose JSON local_runtime does not surface ENOENT error in fleet"
    PASS=$((PASS+1))
fi

# Scenario 13: stale lock detection. Plant a lock file at the repo root with
# a definitely-dead PID; doctor.checkStaleLocks should flip to warn. Clean
# up regardless of pass/fail so subsequent test runs aren't poisoned.
lock_path="/loom-config/workspaces/Hello-World/hello-world/.agent.lock"
lock_json='{"pid":99999,"command":"plan","started_at":"2026-01-01T00:00:00Z","agent_name":"planner"}'
$CTL exec "$LOOM_NAME" sh -c "printf '%s' '$lock_json' > $lock_path" 2>/dev/null || true
out=$(doctor_json "cd /loom-config/workspaces/Hello-World && LOOM_WORKSPACE=HELLO-WORLD loom doctor --json")
stale_check=$(printf '%s' "$out" | jq -c '.checks[] | select(.name == "stale_locks")' 2>/dev/null || true)
$CTL exec "$LOOM_NAME" rm -f "$lock_path" >/dev/null 2>&1 || true
expect_contains "doctor stale_locks flags planted lock" '"status":"warn"' "$stale_check"
expect_contains "doctor stale_locks identifies the dead PID" 'PID 99999 dead' "$stale_check"

# Scenario 14: doctor stale_signal_files detection + --fix autoremediation.
# Plant a >1h-old file under /tmp/loom-signals-<uid>; doctor without --fix
# should report warn; doctor --fix should remove the file and flip to pass.
signal_dir="/tmp/loom-signals-0"
signal_file="$signal_dir/empty-cli-stale-signal"
$CTL exec "$LOOM_NAME" sh -c "
    mkdir -p $signal_dir
    touch $signal_file
    # busybox touch -d accepts ISO format; date -u -d works on alpine.
    touch -d \"\$(date -u -d '-2 hours' +%Y-%m-%dT%H:%M:%S)\" $signal_file
" 2>/dev/null || true

out=$(doctor_json "cd /loom-config/workspaces/Hello-World && LOOM_WORKSPACE=HELLO-WORLD loom doctor --json")
signal_check=$(printf '%s' "$out" | jq -c '.checks[] | select(.name == "stale_signal_files")' 2>/dev/null || true)
expect_contains "doctor detects stale signal file" '"status":"warn"' "$signal_check"
expect_contains "doctor stale signal detail mentions --fix" 'loom doctor --fix' "$signal_check"

out=$(doctor_json "cd /loom-config/workspaces/Hello-World && LOOM_WORKSPACE=HELLO-WORLD loom doctor --fix --json")
signal_check=$(printf '%s' "$out" | jq -c '.checks[] | select(.name == "stale_signal_files")' 2>/dev/null || true)
expect_contains "doctor --fix clears stale signal" '"status":"pass"' "$signal_check"
expect_contains "doctor --fix reports fixed count" 'fixed 1 stale' "$signal_check"

# Confirm the file was actually removed (the JSON Pass is computed from
# whatever the code chose to print, but disk truth is the real assertion).
remaining=$($CTL exec "$LOOM_NAME" ls "$signal_dir" 2>/dev/null | wc -l | tr -d ' ')
if [[ "$remaining" -eq 0 ]]; then
    green "  PASS  doctor --fix removed the signal file from disk"
    PASS=$((PASS+1))
else
    red "  FAIL  doctor --fix removed the signal file from disk (still $remaining file(s))"
    FAIL=$((FAIL+1))
    fail_names+=("doctor --fix removed the signal file from disk")
fi

# Scenario 15: doctor backend_cli FAIL when the active backend's CLI is
# missing from PATH. Symlink only `loom` itself into a clean tmp PATH so
# codex disappears, then assert the check reports FAIL with a remediation
# detail. Doesn't actually uninstall codex from the container.
out=$($CTL exec "$LOOM_NAME" sh -c '
    tmpbin=$(mktemp -d)
    ln -s /usr/local/bin/loom "$tmpbin/loom"
    PATH="$tmpbin:/usr/bin:/bin" LOOM_BACKEND=codex LOOM_WORKSPACE=HELLO-WORLD \
        "$tmpbin/loom" doctor --json 2>/dev/null
    rm -rf "$tmpbin"
' 2>&1 | sed -n '/^{/,$p' | jq -c . 2>/dev/null || true)
backend_check=$(printf '%s' "$out" | jq -c '.checks[] | select(.name == "backend_cli")' 2>/dev/null || true)
expect_contains "doctor backend_cli FAIL when codex missing" '"status":"fail"' "$backend_check"
expect_contains "doctor backend_cli detail mentions install" 'Install the codex CLI' "$backend_check"

# ensure-runtime convergence is intentionally NOT exercised here. It's a
# Loom.app desktop-mode helper that spawns `loom serve --fleet-mode` as a
# child on a fresh port and writes runtime.json. In this headless container
# the parent serve already owns the only loom port, so the runtime spawn
# health-check never converges. Coverage for the path lives in unit tests
# under internal/cli/local/ and the dedicated test/local-runtime/ harness.

echo
echo "==> Summary: $PASS passed, $FAIL failed"
if [[ "$FAIL" -gt 0 ]]; then
    red "failing scenarios:"
    for n in "${fail_names[@]}"; do red "  - $n"; done
    exit 1
fi
green "all CLI scenarios passed"
