#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
model="$root/test/formal/locks"
jar=${TLA2TOOLS_JAR:-"$HOME/.cache/loom-tla/v1.7.4/tla2tools.jar"}
if [[ ! -f "$jar" ]]; then
  echo "TLC jar missing: set TLA2TOOLS_JAR" >&2
  exit 1
fi

scratch=$(mktemp -d "${TMPDIR:-/tmp}/loom-locks-tlc.XXXXXX")
echo "TLC scratch: $scratch (left for inspection)"

check() {
  local case_name=$1 expected=$2 invariant=$3 code=0 states depth
  mkdir -p "$scratch/$case_name"
  (cd "$model" && java -Xmx512m -XX:+UseParallelGC -cp "$jar" tlc2.TLC \
    -deadlock -workers 1 -metadir "$scratch/$case_name" \
    -config "$case_name.cfg" Locks.tla) > "$scratch/$case_name.log" 2>&1 || code=$?
  states=$(sed -nE 's/.* ([0-9]+) distinct states found.*/\1/p' "$scratch/$case_name.log" | tail -1)
  depth=$(sed -nE 's/.*depth of the complete state graph search is ([0-9]+).*/\1/p' "$scratch/$case_name.log" | tail -1)
  if [[ -z "$states" || -z "$depth" || "$states" -lt 2 || "$depth" -lt 2 ]]; then
    echo "$case_name: missing or shallow state exploration" >&2
    exit 1
  fi
  if [[ "$expected" == pass ]]; then
    if [[ "$code" -ne 0 ]]; then cat "$scratch/$case_name.log" >&2; exit 1; fi
  else
    if [[ "$code" -eq 0 ]] || ! rg -q "Invariant $invariant is violated" "$scratch/$case_name.log"; then
      cat "$scratch/$case_name.log" >&2
      exit 1
    fi
  fi
  echo "$case_name: $expected, $states distinct states, depth $depth"
}

check agent-fixed pass -
check agent-legacy fail NoLiveTakeover
check agent-double fail AtMostOneHolder
check daemon-fixed pass -
check daemon-legacy fail NoLiveTakeover
check daemon-double fail AtMostOneHolder
check registry-fixed pass -
check registry-legacy fail NoGhostRegistry
check registry-label-fixed pass -
check registry-label-legacy fail NoUnlabelledDaemon

bytes=$(du -sk "$scratch" | awk '{print $1}')
echo "TLC scratch: ${bytes} KiB"
if (( bytes >= 1048576 )); then
  echo "TLC scratch exceeded 1 GiB" >&2
  exit 1
fi
