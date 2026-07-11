#!/usr/bin/env bash
# Run `go test` under a process-spawn watchdog.
#
# Catches fork bombs in the test suite before they take down the machine:
# if the number of live *.test processes (or their sleep/agent helpers)
# crosses FORKWATCH_LIMIT, the watchdog kills the run, prints a histogram
# of the offending process names, and exits non-zero. After a normal run
# it also reports leaked test processes that outlived the suite.
#
# Background: a supervisor test once re-exec'd os.Executable() — the test
# binary itself — as the spawned agent, so every spawn re-ran the whole
# suite recursively (>2000 processes in seconds, host-crashing CPU load).
# See internal/cli/daemon/supervisor/main_test.go for the production fix;
# this script exists so the next bomb is a fast red build, not a crash.
#
# Usage:
#   ./scripts/test-forkwatch.sh [packages...]        (default: ./...)
#   FORKWATCH_LIMIT=100 ./scripts/test-forkwatch.sh ./internal/cli/daemon/...
#
# Env:
#   FORKWATCH_LIMIT    max new .test processes before declaring a bomb (default 150)
#   FORKWATCH_TIMEOUT  go test -timeout value (default 10m)

set -uo pipefail

PKGS=("${@:-./...}")
LIMIT="${FORKWATCH_LIMIT:-150}"
TIMEOUT="${FORKWATCH_TIMEOUT:-10m}"

# Count live test binaries. Go names test executables <pkg>.test; the fork
# bomb manifested as hundreds of identically-named copies of one of them.
count_test_procs() {
    ps -axo comm= 2>/dev/null | sed 's|.*/||' | grep -cE '\.test$' || true
}

dump_offenders() {
    echo "--- live .test processes by name ---" >&2
    ps -axo comm= 2>/dev/null | sed 's|.*/||' | grep -E '\.test$' | sort | uniq -c | sort -rn | head -10 >&2
}

baseline=$(count_test_procs)

go test -count=1 -timeout "$TIMEOUT" "${PKGS[@]}" &
test_pid=$!

peak=0
bombed=0
while kill -0 "$test_pid" 2>/dev/null; do
    n=$(count_test_procs)
    live=$((n - baseline))
    if (( live > peak )); then
        peak=$live
    fi
    if (( live > LIMIT )); then
        bombed=1
        echo "" >&2
        echo "FORK BOMB DETECTED: $live live .test processes (limit: $LIMIT)" >&2
        dump_offenders
        # Kill only the bombing binary (top offender), not every .test on
        # the machine — unrelated suites may be running in other repos.
        offender=$(ps -axo comm= 2>/dev/null | sed 's|.*/||' | grep -E '\.test$' | sort | uniq -c | sort -rn | head -1 | awk '{print $2}')
        echo "Killing test run (pid $test_pid) and runaway '$offender' processes..." >&2
        kill -9 "$test_pid" 2>/dev/null
        if [[ -n "$offender" ]]; then
            pkill -9 -x "$offender" 2>/dev/null
        fi
        break
    fi
    sleep 0.5
done

wait "$test_pid" 2>/dev/null
test_rc=$?

if (( bombed )); then
    echo "" >&2
    echo "test-forkwatch: FAILED — fork bomb (peak $peak live .test processes)" >&2
    echo "Likely cause: a test spawns os.Executable()/os.Args[0] (the test binary)" >&2
    echo "as a subprocess, recursively re-running the suite. Find it with:" >&2
    echo "  go test -v <pkg>   # the test running when the count exploded" >&2
    echo "  rg 'os\\.Executable|os\\.Args\\[0\\]' <pkg>" >&2
    exit 1
fi

# Leak check: test processes that outlived the run.
sleep 1
leftover=$(( $(count_test_procs) - baseline ))
if (( leftover < 0 )); then
    # Unrelated .test processes from the baseline died during our run.
    leftover=0
fi
echo ""
echo "test-forkwatch: peak $peak live .test processes; leftover after run: $leftover"
if (( leftover > 0 )); then
    echo "test-forkwatch: FAILED — $leftover leaked .test process(es) survived the suite:" >&2
    dump_offenders
    exit 1
fi

exit "$test_rc"
