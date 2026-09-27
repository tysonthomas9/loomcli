#!/usr/bin/env bash
# Run the bounded SSE handoff model. Deliberate legacy and open failures must
# name the expected property; fixed configs must exhaust the state space cleanly.
# Every invocation creates a fresh scratch tree and leaves it for inspection.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
model_dir="$root/test/formal/sse-live"
jar="${LOOM_TLA_JAR:-$HOME/.cache/loom-tla/v1.7.4/tla2tools.jar}"
scratch_parent="${LOOM_SSE_SCRATCH_PARENT:-${TMPDIR:-/tmp}}"
cap_mb="${LOOM_SSE_CAP_MB:-900}"
min_free_mb="${LOOM_SSE_MIN_FREE_MB:-2048}"
workers="${LOOM_SSE_TLA_WORKERS:-1}"

command -v java >/dev/null || { echo "Java is required for TLC" >&2; exit 2; }
[[ -f "$jar" ]] || { echo "TLC jar missing: $jar" >&2; exit 2; }
expected_sha=936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88
actual_sha="$(shasum -a 256 "$jar" | awk '{print $1}')"
[[ "$actual_sha" == "$expected_sha" ]] || { echo "TLC jar checksum mismatch" >&2; exit 2; }
[[ "$cap_mb" =~ ^[0-9]+$ ]] && (( cap_mb > 0 && cap_mb <= 900 )) || {
  echo "LOOM_SSE_CAP_MB must be 1..900" >&2; exit 2;
}
mkdir -p "$scratch_parent"
scratch="$(mktemp -d "$scratch_parent/loom-tla-sse-live.XXXXXX")"
echo "TLC scratch: $scratch (kept for review)"

# id|property expected from legacy|fixed mechanism to disable (core only)
cases=(
  '626a|NoLostEvent|page' '626b|NoLostEvent|register'
  '626c|NoDoubleApply|dedup' '626d|ConnectedImpliesReplayed|connected'
  '626e|NoDoubleApply'
  '642|CheckpointIsDurable'
  '643|AdmissionOrder|order' '612a|NoLostEvent|overflow'
  '610a|NoDoubleApply'
  '644|CheckpointMonotonic' '640a|ReplayTerminates'
  '670|CursorBoundToSource' '672|ExpiredCursorForcesRecovery'
  'V1|ViewConvergesToDurable' '627b|CheckpointMonotonic'
  '640b|ReplayTerminates' '645|RecoverySuccessIsFresh'
  '655|EventuallyConnected' '656|CursorBoundToSource'
  '657|CheckpointFromCompleteFrame' '669|SnapshotCursorConsistent'
  'FD1|ExpiredCursorForcesRecovery' 'FD2|NoLostEvent'
  'H1|CheckpointIsDurable' 'H2|CheckpointMonotonic'
  'H4|FilterRespected'
)

selected=("$@")
want() {
  (( ${#selected[@]} == 0 )) && return 0
  local choice
  for choice in "${selected[@]}"; do [[ "$choice" == "$1" ]] && return 0; done
  return 1
}

failures=0
printf '%-18s %-8s %-12s %s\n' CONFIG EXPECT RESULT SCRATCH
for entry in "${cases[@]}"; do
  IFS='|' read -r id property disable <<<"$entry"
  for variant in fixed legacy mutation open; do
    [[ "$id" != 626e || "$variant" == open ]] || continue
    [[ "$id" == 626e || "$variant" != open ]] || continue
    [[ "$variant" != mutation || -n "$disable" ]] || continue
    name="$id-$variant"
    if [[ "$variant" == mutation ]]; then name="$id-$disable-disabled"; fi
    want "$name" || continue
    module=SSEScenario.tla
    [[ -z "$disable" ]] || module=SSELive.tla
    [[ "$id" != 626e ]] || module=SSELive.tla
    free_mb="$(df -Pm "$scratch" | awk 'NR==2 {print $4}')"
    if (( free_mb < min_free_mb )); then
      echo "Stopping: only $free_mb MB free (minimum $min_free_mb MB). Scratch: $scratch" >&2
      exit 3
    fi
    meta="$scratch/$name-meta"
    log="$scratch/$name.log"
    (cd "$model_dir" && exec java -XX:ActiveProcessorCount=2 -Xmx512m \
      -cp "$jar" tlc2.TLC -deadlock -cleanup -checkpoint 0 \
      -workers "$workers" -metadir "$meta" -config "$name.cfg" "$module") >"$log" 2>&1 &
    pid=$!
    capped=0
    while kill -0 "$pid" 2>/dev/null; do
      used_mb="$(du -sm "$scratch" 2>/dev/null | awk '{print $1}')"
      if (( used_mb >= cap_mb )); then
        capped=1
        kill "$pid" 2>/dev/null || true
        break
      fi
      sleep 1
    done
    set +e
    wait "$pid"
    code=$?
    set -e
    used_kb="$(du -sk "$scratch" | awk '{print $1}')"
    states="$(grep -Eo '[0-9,]+ distinct states found' "$log" | tail -1 | awk '{gsub(/,/, "", $1); print $1}' || true)"
    result=UNEXPECTED
    if (( capped )); then
      result=CAPPED
    elif [[ "$variant" == fixed ]] && (( code == 0 )) &&
         grep -q 'No error has been found' "$log" &&
         [[ -n "$states" ]] &&
         { { [[ -n "$disable" ]] && (( states > 9 )); } ||
           { [[ -z "$disable" ]] && (( states >= 9 )); }; }; then
      result=ok
    elif [[ "$variant" != fixed ]] &&
         { (( code == 12 )) ||
           { (( code == 13 )) &&
             [[ "$property" == EventuallyConnected || "$property" == ViewConvergesToDurable ]]; }; } &&
         { grep -q "Invariant $property is violated" "$log" ||
           { [[ "$property" == EventuallyConnected || "$property" == ViewConvergesToDurable ]] &&
             grep -q 'Temporal properties were violated' "$log"; }; } &&
         grep -q 'State 2:' "$log"; then
      result=ok
    fi
    [[ "$result" == ok ]] || failures=$((failures + 1))
    printf '%-18s %-8s %-12s %s states, %s KiB\n' "$name" "$variant" "$result" "${states:-?}" "$used_kb"
    if [[ "$result" != ok ]]; then
      echo "  $name: exit $code; inspect $log" >&2
    fi
  done
done
echo "Scratch retained: $scratch ($(du -sh "$scratch" | awk '{print $1}'))"
(( failures == 0 )) || { echo "$failures configuration(s) need attention" >&2; exit 1; }
