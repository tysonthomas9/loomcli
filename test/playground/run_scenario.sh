#!/usr/bin/env bash
# Run a daemon-lifecycle scenario.
#
# Usage:
#   run_scenario.sh <scenario-name>     # run scenarios/<scenario-name>.sh
#   run_scenario.sh                     # list available scenarios
#
# Scenarios live in scenarios/*.sh and are self-contained (handle their own
# setup.sh + teardown.sh + cleanup-trap lifecycle). This wrapper just adds a
# uniform entry point and a friendly listing when invoked without args.
#
# Each scenario exits:
#   0  on pass
#   1  on assertion failure
#   2  on prereq missing (loom not on PATH, loom serve unreachable, etc.)
#   3  on timeout waiting for a daemon log line or PID file
#
# Negative-control protocol — see README "Negative-control protocol" section.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
SCENARIO_DIR="$HERE/scenarios"

if [ ! -d "$SCENARIO_DIR" ]; then
  echo "run_scenario.sh: no scenarios/ directory at $SCENARIO_DIR" >&2
  exit 2
fi

list_scenarios() {
  echo "Available scenarios:"
  for s in "$SCENARIO_DIR"/*.sh; do
    [ -e "$s" ] || continue
    name="$(basename "$s" .sh)"
    # Pull the first comment line after the shebang as a short description.
    desc="$(awk '/^# Scenario:/{sub(/^# Scenario: */,""); print; exit}' "$s")"
    if [ -n "$desc" ]; then
      printf '  %-32s  %s\n' "$name" "$desc"
    else
      printf '  %s\n' "$name"
    fi
  done
}

if [ $# -lt 1 ]; then
  list_scenarios
  exit 0
fi

name="$1"
script="$SCENARIO_DIR/$name.sh"
if [ ! -f "$script" ]; then
  echo "run_scenario.sh: no such scenario: $name" >&2
  echo >&2
  list_scenarios >&2
  exit 2
fi

exec bash "$script"
