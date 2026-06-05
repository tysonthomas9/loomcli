#!/usr/bin/env bash
# run-agent.sh — run a real plan|task agent in a sandbox and report exit + task state.
#
#   run-agent.sh <name> <plan|task> [backend] [extra loom flags...]
#
# backend defaults to claude. Logs to /tmp/loom-e2e/<name>/<mode>-<backend>.log.
# A live run takes ~5–15 min — from an agent harness, launch with run_in_background:true.
#
# Optional env:
#   LOOM_MAX_BUDGET_USD   pass a spend cap (NOTE: no-op under subscription/OAuth auth)
set -euo pipefail

ROOT="${ROOT:-/tmp/loom-e2e}"
name="${1:?usage: run-agent.sh <name> <plan|task> [backend] [extra flags...]}"
mode="${2:?usage: run-agent.sh <name> <plan|task> [backend] [extra flags...]}"
backend="${3:-claude}"
shift $(( $# >= 3 ? 3 : 2 ))   # remaining args ($@) are extra loom flags

dir="$ROOT/$name"
[[ -f "$dir/env.sh" ]] || { echo "no sandbox env at $dir/env.sh — run new-sandbox.sh first" >&2; exit 1; }
[[ "$mode" == "plan" || "$mode" == "task" ]] || { echo "mode must be plan|task" >&2; exit 1; }

# shellcheck disable=SC1090
source "$dir/env.sh"
LOOM="$dir/loom"
log="$dir/${mode}-${backend}.log"

echo "=== loom $mode --backend $backend ${*:-}  (budget=${LOOM_MAX_BUDGET_USD:-default}) ==="
start=$SECONDS
set +e
"$LOOM" "$mode" --backend "$backend" "$@" > "$log" 2>&1
ec=$?
set -e
echo "EXIT_CODE=$ec  DURATION=$((SECONDS-start))s   log: $log"

echo "--- agent final message (non-fleetdb tail) ---"
grep -iv '"service":"fleet-db"' "$log" | tail -15

echo "--- task states ---"
"$LOOM" data list -o json 2>/dev/null | grep -iv '"service"' \
  | python3 -c "import sys,json; [print('   ',i['id'],i['status'],'assignee='+str(i.get('assignee')),'design='+str(len(i.get('design') or ''))) for i in json.load(sys.stdin)]" 2>/dev/null || true

exit "$ec"
