#!/usr/bin/env bash
# Daemon lifecycle helpers for playground scenarios.
#
# Manages a single foreground `loom daemon` instance via a global
# DAEMON_PID variable. Scenarios call start_daemon, exercise the failure
# mode, then stop_daemon_graceful or kill_daemon_hard in cleanup.
#
# Depends on lib/common.sh (log).

# Exact slog message strings emitted by the supervisor. Kept here as
# constants so a daemon-side log-string change shows up as one edit, not
# many. Matched substring-style by wait_for_daemon_log_line. Scenarios
# guarding specific bugs add their own constants alongside this one.
readonly LOG_WATCHDOG_HUNG='killing hung process, no activity detected'

DAEMON_PID=""

# daemon_log_path <runtime_dir> <scenario_label> — canonical path scenarios
# use for the daemon log file. Lives under the scenario's runtime dir so
# multiple scenarios don't trample each other's logs.
daemon_log_path() {
  local runtime="$1" label="$2"
  printf '%s/%s.daemon.log\n' "$runtime" "$label"
}

# start_daemon <log_path> — backgrounds `loom daemon`, redirects all output
# to <log_path>, captures the PID into DAEMON_PID, disowns, gives the daemon
# 2s to register agents before returning. The env (LOOM_WORKSPACE etc.) must
# already be sourced.
start_daemon() {
  local logfile="$1"
  : > "$logfile"
  loom daemon >"$logfile" 2>&1 &
  DAEMON_PID=$!
  disown
  log "daemon started (PID $DAEMON_PID), log=$logfile"
  sleep 2
}

# start_daemon_with_short_watchdog <log_path> <output_timeout_sec> — variant
# that exports LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS before launching so the
# watchdog trips quickly. See restart.go GetOutputTimeout for the read site.
start_daemon_with_short_watchdog() {
  local logfile="$1" timeout="$2"
  LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS="$timeout" start_daemon "$logfile"
}

# stop_daemon_graceful — SIGTERM, poll up to ~3s for exit, escalate to
# SIGKILL if still alive. Always wait so the shell reaps the child.
stop_daemon_graceful() {
  if [ -z "$DAEMON_PID" ]; then return 0; fi
  if kill -0 "$DAEMON_PID" 2>/dev/null; then
    kill "$DAEMON_PID" 2>/dev/null || true
    for _ in 1 2 3 4 5 6 7 8 9 10; do
      kill -0 "$DAEMON_PID" 2>/dev/null || break
      sleep 0.3
    done
    kill -9 "$DAEMON_PID" 2>/dev/null || true
  fi
  wait "$DAEMON_PID" 2>/dev/null || true
  DAEMON_PID=""
}

# kill_daemon_hard — SIGKILL with no grace period. Used by scenarios that
# need to inject orphans: the daemon dies without running its cleanup, so
# any setsid descendants survive and reparent to init.
kill_daemon_hard() {
  if [ -z "$DAEMON_PID" ]; then return 0; fi
  if kill -0 "$DAEMON_PID" 2>/dev/null; then
    kill -9 "$DAEMON_PID" 2>/dev/null || true
  fi
  wait "$DAEMON_PID" 2>/dev/null || true
  DAEMON_PID=""
}

# wait_for_daemon_log_line <log_path> <regex> [<timeout-sec>] — tail the log
# (well, grep-poll: tail -F + pattern match would race the timeout cleanly,
# but grep is simpler and the log is local). Returns 0 on match, 1 on
# timeout. Use the LOG_* constants above as the regex argument.
wait_for_daemon_log_line() {
  local logpath="$1" regex="$2" timeout="${3:-90}"
  local deadline=$(( $(date +%s) + timeout ))
  while ! grep -qE "$regex" "$logpath" 2>/dev/null; do
    if [ "$(date +%s)" -ge "$deadline" ]; then
      return 1
    fi
    sleep 1
  done
}
