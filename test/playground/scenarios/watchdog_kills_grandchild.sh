#!/usr/bin/env bash
# Scenario: watchdog kills a setsid grandchild.
#
# Regression guard for PR #63 (kill backend descendants in separate pgroups).
#   Fix commit:        2d451815 — fix(daemon): kill backend descendants in
#                                  separate pgroups + startup orphan sweep
#   Pre-fix commit:    5c3385b2 — commit immediately before 2d451815 on this
#                                  branch. Negative control: check out this
#                                  hash and re-run — this scenario must FAIL.
#
# Expected on HEAD: exit 0. Watchdog fires, StopAgent snapshots descendant
# pgroups before SIGTERM, grandchild dies along with parent.
#
# Expected on 5c3385b2: exit 1. Without #63's fix, the grandchild in its own
# pgroup is invisible to syscall.Kill(-pid) and survives.
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
SCENARIO_NAME="grandchild"
PLAYGROUND_LOG_SCOPE="watchdog"
export PLAYGROUND_LOG_SCOPE

# shellcheck source=../lib/common.sh
. "$HERE/lib/common.sh"
# shellcheck source=../lib/proctree.sh
. "$HERE/lib/proctree.sh"
# shellcheck source=../lib/daemon.sh
. "$HERE/lib/daemon.sh"

WATCHDOG_TIMEOUT="${PLAYGROUND_WATCHDOG_TIMEOUT:-15}"
WATCHDOG_WAIT_SECS="${PLAYGROUND_WATCHDOG_WAIT:-90}"
GRANDCHILD_WAIT_SECS="${PLAYGROUND_GRANDCHILD_WAIT:-25}"

# Exact slog message from proctree.go's signalDescendantPGroups — the
# specific code path this scenario exists to guard.
readonly LOG_DESCENDANT_PGROUP_SIGNAL='sent signal to descendant process group'

RUNTIME="$HERE/.runtime-$SCENARIO_NAME"

cleanup() {
  local rc=$?
  stop_daemon_graceful || true
  "$HERE/teardown.sh" "$SCENARIO_NAME" >/dev/null 2>&1 || true
  if [ $rc -eq 0 ] && [ "$fails" -eq 0 ]; then
    green "watchdog_kills_grandchild: PASS"
  else
    red "watchdog_kills_grandchild: FAIL (rc=$rc, fails=$fails)"
    [ $rc -eq 0 ] && rc=$EXIT_FAIL
  fi
  exit $rc
}
trap cleanup EXIT

command -v loom >/dev/null || { red "loom not on PATH"; exit "$EXIT_PREREQ"; }
command -v ps   >/dev/null || { red "ps not on PATH"; exit "$EXIT_PREREQ"; }
curl -sfm 2 http://localhost:8080/health >/dev/null \
  || { red "loom serve not reachable at http://localhost:8080"; exit "$EXIT_PREREQ"; }
log "prereqs OK"

log "pre-clean stale state"
"$HERE/teardown.sh" "$SCENARIO_NAME" >/dev/null 2>&1 || true

log "running setup.sh $SCENARIO_NAME (watchdog=${WATCHDOG_TIMEOUT}s)"
mkdir -p "$RUNTIME"
LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS="$WATCHDOG_TIMEOUT" \
  "$HERE/setup.sh" "$SCENARIO_NAME" >"$RUNTIME/setup.log" 2>&1 || {
    red "setup.sh failed; last 30 lines:"
    tail -30 "$RUNTIME/setup.log"
    exit "$EXIT_PREREQ"
  }

# shellcheck disable=SC1091
. "$RUNTIME/env"

# Backend marker convention: $LOOM_WORKSPACE_RUNTIME_DIR/grandchild/.
# Resolved via loom_dir/workspaces/<workspace>/<scenario>/.
loom_dir="${LOOM_CONFIG_DIR:-$HOME/.loom}"
marker_dir="$loom_dir/workspaces/playground-$SCENARIO_NAME/grandchild"
pid_file="$marker_dir/grandchild.pid"
mode_file="$marker_dir/mode"
mkdir -p "$marker_dir"
echo "hang" > "$mode_file"

log "creating hang task"
loom data create --title "Watchdog scenario" --type task --priority 2 \
  --status open --design "Watchdog kill verification (no planner involved)" \
  > "$RUNTIME/create.log"

daemon_log="$(daemon_log_path "$RUNTIME" "watchdog")"
start_daemon_with_short_watchdog "$daemon_log" "$WATCHDOG_TIMEOUT"

log "waiting up to ${GRANDCHILD_WAIT_SECS}s for grandchild PID file"
gc_pid="$(read_pid_file "$pid_file" "$GRANDCHILD_WAIT_SECS")" || {
  red "grandchild PID file never appeared at $pid_file"
  log "last 50 lines of daemon log:"
  tail -50 "$daemon_log"
  exit "$EXIT_FAIL"
}
log "grandchild PID = $gc_pid"
assert_pid_alive "$gc_pid" "grandchild started"

log "waiting up to ${WATCHDOG_WAIT_SECS}s for watchdog kill"
if ! wait_for_daemon_log_line "$daemon_log" "$LOG_WATCHDOG_HUNG" "$WATCHDOG_WAIT_SECS"; then
  red "watchdog did not fire within ${WATCHDOG_WAIT_SECS}s"
  log "last 50 lines of daemon log:"
  tail -50 "$daemon_log"
  exit "$EXIT_TIMEOUT"
fi
log "watchdog fired; checking #63 code path log line"

if grep -qE "$LOG_DESCENDANT_PGROUP_SIGNAL" "$daemon_log"; then
  green "  ✓ daemon log contains '$LOG_DESCENDANT_PGROUP_SIGNAL'"
else
  red "  ✗ daemon log missing '$LOG_DESCENDANT_PGROUP_SIGNAL' — was #63 fix applied?"
  fails=$((fails+1))
fi

# Give SIGKILL phase a moment to land.
sleep 3
assert_pid_dead "$gc_pid" "grandchild killed by descendant-pgroup snapshot"

[ "$fails" -eq 0 ] || exit "$EXIT_FAIL"
exit "$EXIT_PASS"
