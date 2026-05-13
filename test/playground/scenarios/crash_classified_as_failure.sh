#!/usr/bin/env bash
# Scenario: backend exits non-zero → daemon classifies as failure and applies
# its backoff policy.
#
# Smoke-level / demonstrative template. Not tied to a specific regression —
# serves as the copy-template for adding new retry/backoff regression
# guards. To convert into a real regression test, replace the "any backoff
# message" grep with the exact failure classification you want to assert.
#
# Expected on HEAD: exit 0. The crash backend exits non-zero a couple
# seconds into invoke; the supervisor's shouldRestart classifies the exit
# and logs a backoff/retry line.
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
SCENARIO_NAME="crash"
PLAYGROUND_LOG_SCOPE="crash"
export PLAYGROUND_LOG_SCOPE

# shellcheck source=../lib/common.sh
. "$HERE/lib/common.sh"
# shellcheck source=../lib/proctree.sh
. "$HERE/lib/proctree.sh"
# shellcheck source=../lib/daemon.sh
. "$HERE/lib/daemon.sh"

STARTED_WAIT_SECS="${PLAYGROUND_CRASH_STARTED_WAIT:-25}"
BACKOFF_WAIT_SECS="${PLAYGROUND_CRASH_BACKOFF_WAIT:-30}"

RUNTIME="$HERE/.runtime-$SCENARIO_NAME"

cleanup() {
  local rc=$?
  stop_daemon_graceful || true
  "$HERE/teardown.sh" "$SCENARIO_NAME" >/dev/null 2>&1 || true
  if [ $rc -eq 0 ] && [ "$fails" -eq 0 ]; then
    green "crash_classified_as_failure: PASS"
  else
    red "crash_classified_as_failure: FAIL (rc=$rc, fails=$fails)"
    [ $rc -eq 0 ] && rc=$EXIT_FAIL
  fi
  exit $rc
}
trap cleanup EXIT

command -v loom >/dev/null || { red "loom not on PATH"; exit "$EXIT_PREREQ"; }
curl -sfm 2 http://localhost:8080/health >/dev/null \
  || { red "loom serve not reachable at http://localhost:8080"; exit "$EXIT_PREREQ"; }
log "prereqs OK"

log "pre-clean stale state"
"$HERE/teardown.sh" "$SCENARIO_NAME" >/dev/null 2>&1 || true

log "running setup.sh $SCENARIO_NAME"
mkdir -p "$RUNTIME"
"$HERE/setup.sh" "$SCENARIO_NAME" >"$RUNTIME/setup.log" 2>&1 || {
  red "setup.sh failed; last 30 lines:"
  tail -30 "$RUNTIME/setup.log"
  exit "$EXIT_PREREQ"
}

# shellcheck disable=SC1091
. "$RUNTIME/env"

loom_dir="${LOOM_CONFIG_DIR:-$HOME/.loom}"
marker_dir="$loom_dir/workspaces/playground-$SCENARIO_NAME/crash"
started_flag="$marker_dir/started.flag"

log "creating crash task"
loom data create --title "Crash scenario" --type task --priority 2 \
  --status open --design "Crash classification verification" \
  > "$RUNTIME/create.log"

daemon_log="$(daemon_log_path "$RUNTIME" "crash")"
start_daemon "$daemon_log"

log "waiting up to ${STARTED_WAIT_SECS}s for crash backend started flag"
if ! wait_for_file "$started_flag" "$STARTED_WAIT_SECS"; then
  red "crash backend never wrote started.flag at $started_flag"
  log "last 50 lines of daemon log:"
  tail -50 "$daemon_log"
  exit "$EXIT_FAIL"
fi
green "  ✓ crash backend ran (started.flag at $started_flag)"

# The daemon's shouldRestart classifies the non-zero exit and the
# restart loop logs a backoff/retry line. The exact format is in
# restart.go:32: "[daemon] Agent X: spawn failed, waiting Xs before retry"
# — but for runtime crashes (not spawn failures) the supervisor logs a
# different classification line. Use a permissive regex so this smoke
# template doesn't break on minor log-format tweaks.
log "waiting up to ${BACKOFF_WAIT_SECS}s for daemon failure classification"
if wait_for_daemon_log_line "$daemon_log" 'waiting.*before retry|spawn failed|max retries|exit code|exited with' "$BACKOFF_WAIT_SECS"; then
  green "  ✓ daemon logged a failure/retry signal"
else
  red "  ✗ daemon never logged a failure/retry signal within ${BACKOFF_WAIT_SECS}s"
  log "last 50 lines of daemon log:"
  tail -50 "$daemon_log"
  fails=$((fails+1))
fi

[ "$fails" -eq 0 ] || exit "$EXIT_FAIL"
exit "$EXIT_PASS"
