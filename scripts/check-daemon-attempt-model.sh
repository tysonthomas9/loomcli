#!/usr/bin/env bash
# Model-check the daemon attempt protocol (test/formal/daemon-attempt).
#
# Runs TLC on every configuration and compares the result with the expected
# outcome: "pass" configs must explore their finite state space with no
# violation; "fail" configs are deliberate mutations that must produce a
# counterexample for the named invariant.
#
# Usage: scripts/check-daemon-attempt-model.sh [config-name ...]
#
# Env:
#   LOOM_TLA_CACHE        jar cache dir (default ~/.cache/loom-tla)
#   LOOM_TLA_SCRATCH      TLC metadata dir (default $TMPDIR/loom-tla-daemon-attempt)
#   LOOM_TLA_MIN_FREE_MB  refuse to start a run below this free space (default 2048)
#   LOOM_TLA_CAP_MB       kill a run whose scratch exceeds this size (default 1024)
#   LOOM_TLA_WORKERS      TLC workers (default auto)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MODEL_DIR="$ROOT/test/formal/daemon-attempt"
TLA_VERSION="v1.7.4"
TLA_SHA256="936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88"
TLA_URL="https://github.com/tlaplus/tlaplus/releases/download/${TLA_VERSION}/tla2tools.jar"
CACHE="${LOOM_TLA_CACHE:-$HOME/.cache/loom-tla}/${TLA_VERSION}"
JAR="$CACHE/tla2tools.jar"
SCRATCH="${LOOM_TLA_SCRATCH:-${TMPDIR:-/tmp}/loom-tla-daemon-attempt}"
MIN_FREE_MB="${LOOM_TLA_MIN_FREE_MB:-2048}"
CAP_MB="${LOOM_TLA_CAP_MB:-1024}"
WORKERS="${LOOM_TLA_WORKERS:-auto}"

# name|expect|invariant (invariant is the one a "fail" run must violate)
CASES=(
  "a_pass|pass|"
  "a_pass_faults|pass|"
  "a_fail_current|fail|NoSupersededWrite"
  "a_fail_token|fail|NoSupersededWrite"
  "a_fail_fence_eq|fail|NoWriteAfterRelease"
  "a_fail_unguarded|fail|SingleLiveProcess"
  "a_fail_drift|fail|SingleLiveProcess"
  "a_fail_pgskew|fail|SingleLiveProcess"
  "a_fail_crash|fail|SingleLiveProcess"
  "b_pass|pass|"
  "b_pass_two_issues|pass|"
  "b_pass_two_agents|pass|"
  "b_fail_current|fail|NoForeignIssueWrite"
  "b_fail_fence_only|fail|NoForeignIssueWrite"
  "b_fail_double_work|fail|NoDoubleWork"
  "b_vacuity|fail|NeverClosed"
  "c_pass|pass|"
  "c_fail_no_guard|fail|TerminalOnce"
  "c_fail_session_unfenced|fail|NoSupersededWrite"
  "c_fail_stale_finalize|fail|NoSupersededFinalize"
  "c_stranded_session|fail|NoStrandedSession"
  "c_vacuity|fail|NeverFinalized"
)

command -v java >/dev/null || { echo "java not found (TLC needs Java 11+)" >&2; exit 2; }

sha256() { shasum -a 256 "$1" 2>/dev/null | awk '{print $1}' || sha256sum "$1" | awk '{print $1}'; }

if [[ ! -f "$JAR" ]]; then
  mkdir -p "$CACHE"
  echo "downloading tla2tools.jar ${TLA_VERSION} -> $JAR"
  curl -fsSL -o "$JAR.partial" "$TLA_URL"
  mv "$JAR.partial" "$JAR"
fi
got="$(sha256 "$JAR")"
if [[ "$got" != "$TLA_SHA256" ]]; then
  echo "tla2tools.jar checksum mismatch: got $got want $TLA_SHA256" >&2
  exit 2
fi

free_mb() { df -Pm "$1" | awk 'NR==2 {print $4}'; }
used_mb() { du -sm "$1" 2>/dev/null | awk '{print $1}'; }

mkdir -p "$SCRATCH"

selected=("$@")
want() {
  [[ ${#selected[@]} -eq 0 ]] && return 0
  local s; for s in "${selected[@]}"; do [[ "$s" == "$1" ]] && return 0; done
  return 1
}

failures=0
printf '%-24s %-6s %-8s %s\n' CONFIG EXPECT RESULT DETAIL
for entry in "${CASES[@]}"; do
  IFS='|' read -r name expect inv <<<"$entry"
  want "$name" || continue
  avail="$(free_mb "$SCRATCH")"
  if (( avail < MIN_FREE_MB )); then
    echo "refusing to run $name: ${avail} MB free under $SCRATCH (< ${MIN_FREE_MB} MB)" >&2
    exit 3
  fi
  meta="$SCRATCH/$name"
  log="$SCRATCH/$name.log"
  # -cleanup makes TLC clear its own states directory; nothing else is removed.
  (cd "$MODEL_DIR" && exec java -XX:+UseParallelGC -cp "$JAR" tlc2.TLC \
      -deadlock -cleanup -checkpoint 0 -workers "$WORKERS" \
      -metadir "$meta" -config "$name.cfg" DaemonAttempt.tla) >"$log" 2>&1 &
  pid=$!
  capped=0
  while kill -0 "$pid" 2>/dev/null; do
    if (( $(used_mb "$meta" || echo 0) > CAP_MB )); then
      capped=1; kill "$pid" 2>/dev/null || true; break
    fi
    sleep 2
  done
  set +e; wait "$pid"; code=$?; set -e
  states="$(grep -Eo '[0-9,]+ distinct states found' "$log" | tail -1 || true)"
  if (( capped )); then
    # A killed TLC skips -cleanup, so remove this run's own metadir, but only
    # after checking it is the per-config directory this script created.
    if [[ "$(basename "$SCRATCH")" == loom-tla-daemon-attempt* \
          && "$meta" == "$SCRATCH/$name" && -d "$meta" && ! -L "$meta" \
          && -O "$meta" ]]; then
      rm -rf -- "$meta"
      detail="scratch exceeded ${CAP_MB} MB; run stopped and its metadir removed; see $log"
    else
      detail="scratch exceeded ${CAP_MB} MB; ownership check failed, left $meta; see $log"
    fi
    result=CAPPED
  elif [[ "$expect" == pass ]] && (( code == 0 )) && grep -q 'No error has been found' "$log"; then
    result=ok; detail="no violation; $states"
  elif [[ "$expect" == fail ]] && (( code == 12 )) && grep -q "Invariant $inv is violated" "$log"; then
    result=ok; detail="counterexample for $inv; $states"
  else
    result=UNEXPECTED; detail="exit $code; see $log"
  fi
  [[ "$result" == ok ]] || failures=$((failures + 1))
  printf '%-24s %-6s %-8s %s\n' "$name" "$expect" "$result" "$detail"
done

echo "TLC ${TLA_VERSION} ($JAR); logs and counterexamples under $SCRATCH"
if (( failures > 0 )); then
  echo "$failures configuration(s) did not match the expected outcome" >&2
  exit 1
fi
