#!/usr/bin/env bash
# Close every open issue on the e2e server — suite teardown, so each suite
# leaves the board empty for the next one regardless of execution order.
set -euo pipefail

base="${AFT_BASE_URL:?AFT_BASE_URL not set}"

curl -sf "$base/api/issues" | python3 -c '
import json, sys

payload = json.load(sys.stdin)
data = payload.get("data") if isinstance(payload, dict) else payload
if isinstance(data, dict):
    data = data.get("issues") or data.get("items") or []
for issue in data or []:
    if issue.get("status") != "closed":
        print(issue["id"])
' | while read -r id; do
    curl -sf -X POST "$base/api/issues/$id/close" >/dev/null || true
done
echo "board cleared"
