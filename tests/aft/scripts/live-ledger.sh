#!/usr/bin/env bash
# Append one accounting line per live-tier run to reports/live-ledger.log, and
# report whether the run left any terminal tab attached in any workspace.
#
# "How much did that run cost me" has to be answerable after the fact: the aft
# report schema carries no cost fields, codex exposes no price in its stream, and
# interactive lead runtimes do not feed the task-session usage collector. So this
# records what IS knowable — which binary really ran, how many cases, how long,
# and whether cleanup actually left the stack quiet.
#
# Usage: live-ledger.sh <backend> <binary-path> <cases> <exit-code> <wall-seconds>
set -uo pipefail   # deliberately no -e: the ledger must never fail a run

backend="${1:-unknown}"
binary="${2:-unknown}"
cases="${3:-0}"
exit_code="${4:-?}"
wall="${5:-0}"
daemon_cleanup="${AFT_DAEMON_CLEANUP_VERDICT:-not-requested}"
sweep_cleanup="${AFT_LIVE_SWEEP_VERDICT:-unknown}"

base="${AFT_BASE_URL:-}"
report_dir="${AFT_REPORT_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/reports}"
ledger="$report_dir/live-ledger.log"

# Bounded: this runs before the EXIT trap tears the stack down, so a hung probe
# here delays killing everything the run started.
if command -v timeout >/dev/null 2>&1; then
    version="$(timeout 10 "$binary" --version 2>/dev/null | head -1 | tr -d '\n' | cut -c1-60)"
else
    version="$("$binary" --version 2>/dev/null | head -1 | tr -d '\n' | cut -c1-60)"
fi
[[ -n "$version" ]] || version="unknown"

# Cleanup verdict: sum the terminal tabs still registered across every workspace.
# The stack is still up at this point (run-aft.sh's EXIT trap has not fired), so a
# non-zero count here means a live test left a PTY — and therefore possibly a paid
# model process — behind.
tabs="unknown"
if [[ -n "$base" ]]; then
    tabs="$(python3 - "$base" <<'PY' 2>/dev/null || echo unknown
import json, sys, urllib.request

base = sys.argv[1]

def get(path):
    with urllib.request.urlopen(base + path, timeout=5) as fh:
        return json.load(fh)

try:
    payload = get("/api/workspaces")
except Exception:
    raise SystemExit(1)
# /api/workspaces answers {"success":..,"workspaces":[..]}, NOT the {"data":..}
# envelope. Reading "data" here yielded an empty list and printed a confident 0 —
# a leak detector that always says "clean".
items = payload.get("workspaces") if isinstance(payload, dict) else None
if not isinstance(items, list):
    raise SystemExit(1)
keys = []
for ws in items:
    if isinstance(ws, dict):
        key = ws.get("key") or ws.get("id") or ws.get("workspace_key")
        if key:
            keys.append(key)
total = 0
for key in keys:
    # A partial count is worse than no count: it reads as proof of cleanliness.
    tabs = get(f"/api/workspaces/{key}/terminal/tabs")
    entries = tabs.get("data") if isinstance(tabs, dict) else tabs
    if not isinstance(entries, list):
        raise SystemExit(1)
    total += len(entries)
print(total)
PY
)"
fi

mkdir -p "$report_dir"
printf '%s backend=%s binary=%s version=%q cases=%s exit=%s wall=%ss tabs_remaining=%s sweep=%s daemon=%s\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$backend" "$binary" "$version" \
    "$cases" "$exit_code" "$wall" "$tabs" "$sweep_cleanup" "$daemon_cleanup" >> "$ledger"

echo "[aft] live ledger: $(tail -1 "$ledger")"
if [[ "$tabs" != "unknown" && "$tabs" != "0" ]]; then
    echo "[aft] WARNING: $tabs terminal tab(s) still registered after the live run — check 'ps' for a surviving $backend process" >&2
fi
