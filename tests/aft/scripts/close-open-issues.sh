#!/usr/bin/env bash
# Close every non-closed issue in the e2e workspace — suite teardown, so each
# suite leaves the board empty for the next one regardless of execution order.
# Fails loudly (non-zero) if the API shape changes, a close fails, or the board
# is not actually empty afterwards — a silent no-op here corrupts later suites.
set -euo pipefail

base="${AFT_BASE_URL:?AFT_BASE_URL not set}"
ws="${AFT_WS:-E2E-WS}"

list_open_ids() {
    # include_blocked=true routes through the kanban list path, which merges the
    # deferred overlay — the plain list hides deferred issues, so without it a
    # deferred issue survives this sweep AND the emptiness check below.
    curl -sf "$base/api/workspaces/$ws/issues?include_blocked=true" | python3 -c '
import json, sys

payload = json.load(sys.stdin)
data = payload.get("data") if isinstance(payload, dict) else None
if not isinstance(data, list):
    raise SystemExit("unexpected issues response shape: expected list under \"data\"")
for issue in data:
    if issue.get("status") != "closed":
        print(issue["id"])
'
}

failures=0
for id in $(list_open_ids); do
    if ! curl -sf -X POST "$base/api/workspaces/$ws/issues/$id/close" \
        -H "Content-Type: application/json" -d '{"reason":"aft teardown"}' >/dev/null; then
        echo "close failed: $id" >&2
        failures=$((failures + 1))
    fi
done

remaining="$(list_open_ids | grep -c . || true)"
if [[ "$remaining" != "0" || "$failures" != "0" ]]; then
    echo "board NOT cleared: $remaining open issue(s) remain, $failures close failure(s)" >&2
    exit 1
fi
echo "board cleared"
