#!/usr/bin/env bash
# Scenario: startup sweep kills a grandchild orphaned by daemon SIGKILL.
#
# Regression guard for PR #63 (startup orphan sweep half).
#   Fix commit:        2d451815 — fix(daemon): kill backend descendants in
#                                  separate pgroups + startup orphan sweep
#   Pre-fix commit:    5c3385b2 — commit immediately before 2d451815 on this
#                                  branch. Negative control: check out this
#                                  hash and re-run — this scenario must FAIL.
#
# Expected on HEAD: exit 0. Daemon hard-killed leaves grandchild as orphan
# (PPID==1); on restart, sweepOrphanedBackends finds and kills it.
#
# Expected on 5c3385b2: exit 1. Without the sweep, the orphan survives
# indefinitely.
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
SCENARIO_NAME="grandchild"
PLAYGROUND_LOG_SCOPE="orphan"
export PLAYGROUND_LOG_SCOPE

# shellcheck source=../lib/common.sh
. "$HERE/lib/common.sh"
# shellcheck source=../lib/proctree.sh
. "$HERE/lib/proctree.sh"
# shellcheck source=../lib/daemon.sh
. "$HERE/lib/daemon.sh"

WATCHDOG_TIMEOUT="${PLAYGROUND_WATCHDOG_TIMEOUT:-15}"
GRANDCHILD_WAIT_SECS="${PLAYGROUND_GRANDCHILD_WAIT:-25}"
SWEEP_WAIT_SECS="${PLAYGROUND_SWEEP_WAIT:-20}"

# Exact slog message from proctree.go's killOrphanedWorktreeProcesses —
# the specific code path this scenario exists to guard.
readonly LOG_STARTUP_SWEEP='killing orphaned backend from previous daemon run'

RUNTIME="$HERE/.runtime-$SCENARIO_NAME"

cleanup() {
  local rc=$?
  stop_daemon_graceful || true
  "$HERE/teardown.sh" "$SCENARIO_NAME" >/dev/null 2>&1 || true
  if [ $rc -eq 0 ] && [ "$fails" -eq 0 ]; then
    green "startup_sweep_kills_orphan: PASS"
  else
    red "startup_sweep_kills_orphan: FAIL (rc=$rc, fails=$fails)"
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

log "running setup.sh $SCENARIO_NAME"
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
marker_dir="$loom_dir/workspaces/playground-$SCENARIO_NAME/grandchild"
pid_file="$marker_dir/grandchild.pid"
mode_file="$marker_dir/mode"
mkdir -p "$marker_dir"
rm -f "$pid_file"
echo "orphan" > "$mode_file"

log "creating orphan task"
loom data create --title "Orphan scenario" --type task --priority 2 \
  --status open --design "Startup sweep verification (no planner involved)" \
  > "$RUNTIME/create.log"

daemon_log_a="$(daemon_log_path "$RUNTIME" "orphan-a")"
start_daemon_with_short_watchdog "$daemon_log_a" "$WATCHDOG_TIMEOUT"

log "waiting up to ${GRANDCHILD_WAIT_SECS}s for grandchild PID file"
gc_pid="$(read_pid_file "$pid_file" "$GRANDCHILD_WAIT_SECS")" || {
  red "grandchild PID file never appeared at $pid_file"
  log "last 50 lines of daemon log:"
  tail -50 "$daemon_log_a"
  exit "$EXIT_FAIL"
}
log "grandchild PID = $gc_pid"
assert_pid_alive "$gc_pid" "grandchild started"

log "SIGKILLing daemon (PID $DAEMON_PID) — orphaning grandchild"
kill_daemon_hard
sleep 2
assert_pid_alive "$gc_pid" "grandchild survived daemon SIGKILL"

ppid_after="$(ps -o ppid= -p "$gc_pid" 2>/dev/null | tr -d ' ' || true)"
log "grandchild PPID after daemon kill = ${ppid_after:-(none)}"
if [ "$ppid_after" = "1" ]; then
  green "  ✓ grandchild reparented to init (PPID==1)"
else
  red "  ✗ grandchild PPID is ${ppid_after:-unknown}; expected 1"
  fails=$((fails+1))
fi

log "restarting daemon — startup sweep should clean the orphan"
daemon_log_b="$(daemon_log_path "$RUNTIME" "orphan-b")"
start_daemon_with_short_watchdog "$daemon_log_b" "$WATCHDOG_TIMEOUT"

log "waiting up to ${SWEEP_WAIT_SECS}s for startup-sweep log line"
if ! wait_for_daemon_log_line "$daemon_log_b" "$LOG_STARTUP_SWEEP" "$SWEEP_WAIT_SECS"; then
  red "startup sweep log line never appeared within ${SWEEP_WAIT_SECS}s"
  log "last 50 lines of daemon log:"
  tail -50 "$daemon_log_b"
  exit "$EXIT_TIMEOUT"
fi
green "  ✓ daemon log contains '$LOG_STARTUP_SWEEP'"

sleep 2
assert_pid_dead "$gc_pid" "orphan killed by startup sweep"

[ "$fails" -eq 0 ] || exit "$EXIT_FAIL"
exit "$EXIT_PASS"
