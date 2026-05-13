#!/usr/bin/env bash
# Shared output helpers and exit codes for playground scenarios.
# Source from scenarios *after* `set -euo pipefail`; sourcing is safe under it.
#
# Exposes: red, green, log, EXIT_* constants, and an initialized `fails` counter
# that proctree.sh's assert_* helpers increment in the caller's scope.

if [ -t 1 ]; then
  PLAYGROUND_COLOR_RED='\033[31m'
  PLAYGROUND_COLOR_GREEN='\033[32m'
  PLAYGROUND_COLOR_RESET='\033[0m'
else
  PLAYGROUND_COLOR_RED=''
  PLAYGROUND_COLOR_GREEN=''
  PLAYGROUND_COLOR_RESET=''
fi

red()   { printf '%b%s%b\n' "$PLAYGROUND_COLOR_RED"   "$*" "$PLAYGROUND_COLOR_RESET"; }
green() { printf '%b%s%b\n' "$PLAYGROUND_COLOR_GREEN" "$*" "$PLAYGROUND_COLOR_RESET"; }

# Scenarios set PLAYGROUND_LOG_SCOPE (e.g. "watchdog") before sourcing this
# file so log lines are attributable when multiple scenarios run in sequence.
log() {
  printf '[%s %s] %s\n' "${PLAYGROUND_LOG_SCOPE:-playground}" "$(date -u +%H:%M:%S)" "$*"
}

# Exit codes — kept stable across scenarios so run_scenario.sh can distinguish
# assertion failures from setup problems and timeouts.
readonly EXIT_PASS=0
readonly EXIT_FAIL=1
readonly EXIT_PREREQ=2
readonly EXIT_TIMEOUT=3

# Shared assertion counter — assert_* helpers in proctree.sh bump this in the
# caller's scope (sourced functions share scope). Scenarios check `fails` at
# the end and exit EXIT_FAIL if non-zero.
fails=${fails:-0}

# wait_for_file <path> [<timeout-sec>] — bounded poll for any file (empty or
# not). Returns 0 once the file exists, 1 on timeout. Use read_pid_file from
# lib/proctree.sh when you need to read non-empty PID content.
wait_for_file() {
  local path="$1" timeout="${2:-25}"
  local deadline=$(( $(date +%s) + timeout ))
  while [ ! -f "$path" ]; do
    if [ "$(date +%s)" -ge "$deadline" ]; then
      return 1
    fi
    sleep 0.5
  done
}
