#!/usr/bin/env bash
# Scenario: slow but legitimate work survives the watchdog.
#
# Regression guard against tightening the output-timeout threshold past
# what real backends need. The watchdog must not fire on a backend that
# writes one log line every (interval) seconds when interval < output_timeout.
#
# Expected on HEAD: exit 0. With output_timeout=15s and the slow backend
# writing every 10s, the watchdog should see continuous activity and never
# fire. After ~45s the backend exits cleanly.
#
# Catches: anyone making the watchdog check more aggressive (shortens the
# default, double-counts ticks, stops resetting on transcript writes). To
# convert into a tighter regression test for a specific watchdog change,
# narrow the assertion to the exact log line the bad code path would emit.
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
SCENARIO_NAME="slow"
PLAYGROUND_LOG_SCOPE="slow"
export PLAYGROUND_LOG_SCOPE

# shellcheck source=../lib/common.sh
. "$HERE/lib/common.sh"
# shellcheck source=../lib/proctree.sh
. "$HERE/lib/proctree.sh"
# shellcheck source=../lib/daemon.sh
. "$HERE/lib/daemon.sh"

# Watchdog threshold must exceed slow-backend interval. Defaults: interval=10s
# from loom-backend-playground-slow, watchdog=15s, total observation=40s.
WATCHDOG_TIMEOUT="${PLAYGROUND_WATCHDOG_TIMEOUT:-15}"
STARTED_WAIT_SECS="${PLAYGROUND_SLOW_STARTED_WAIT:-25}"
OBSERVE_SECS="${PLAYGROUND_SLOW_OBSERVE:-40}"

RUNTIME="$HERE/.runtime-$SCENARIO_NAME"

cleanup() {
  local rc=$?
  stop_daemon_graceful || true
  "$HERE/teardown.sh" "$SCENARIO_NAME" >/dev/null 2>&1 || true
  if [ $rc -eq 0 ] && [ "$fails" -eq 0 ]; then
    green "slow_backend_not_killed: PASS"
  else
    red "slow_backend_not_killed: FAIL (rc=$rc, fails=$fails)"
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

log "running setup.sh $SCENARIO_NAME (watchdog=${WATCHDOG_TIMEOUT}s, observe=${OBSERVE_SECS}s)"
mkdir -p "$RUNTIME"
LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS="$WATCHDOG_TIMEOUT" \
  "$HERE/setup.sh" "$SCENARIO_NAME" >"$RUNTIME/setup.log" 2>&1 || {
    red "setup.sh failed; last 30 lines:"
    tail -30 "$RUNTIME/setup.log"
    exit "$EXIT_PREREQ"
  }

# shellcheck disable=SC1091
. "$RUNTIME/env"

loom_dir="${LOOM_CONFIG_DIR:-$HOME/.loom}"
marker_dir="$loom_dir/workspaces/playground-$SCENARIO_NAME/slow"
started_flag="$marker_dir/started.flag"
heartbeat_file="$marker_dir/heartbeat.count"

log "creating slow task"
loom data create --title "Slow scenario" --type task --priority 2 \
  --status open --design "Slow-work watchdog tolerance verification" \
  > "$RUNTIME/create.log"

daemon_log="$(daemon_log_path "$RUNTIME" "slow")"
start_daemon_with_short_watchdog "$daemon_log" "$WATCHDOG_TIMEOUT"

log "waiting up to ${STARTED_WAIT_SECS}s for slow backend started flag"
if ! wait_for_file "$started_flag" "$STARTED_WAIT_SECS"; then
  red "slow backend never wrote started.flag at $started_flag"
  log "last 50 lines of daemon log:"
  tail -50 "$daemon_log"
  exit "$EXIT_FAIL"
fi
green "  ✓ slow backend started"

log "observing for ${OBSERVE_SECS}s — should accumulate heartbeats without watchdog kill"
sleep "$OBSERVE_SECS"

if grep -qE "$LOG_WATCHDOG_HUNG" "$daemon_log"; then
  red "  ✗ daemon logged watchdog kill — false positive on slow work"
  fails=$((fails+1))
else
  green "  ✓ daemon did NOT log watchdog kill"
fi

# Heartbeat counter is written by the backend after each tick. With a 10s
# interval over ${OBSERVE_SECS}s, expect ≥ 2 ticks.
if [ -f "$heartbeat_file" ]; then
  ticks="$(tr -d '[:space:]' < "$heartbeat_file" || echo 0)"
  if [ "${ticks:-0}" -ge 2 ] 2>/dev/null; then
    green "  ✓ slow backend completed ≥2 heartbeat ticks ($ticks)"
  else
    red "  ✗ slow backend logged only ${ticks:-0} heartbeats (expected ≥2)"
    fails=$((fails+1))
  fi
else
  red "  ✗ heartbeat file missing at $heartbeat_file"
  fails=$((fails+1))
fi

[ "$fails" -eq 0 ] || exit "$EXIT_FAIL"
exit "$EXIT_PASS"
