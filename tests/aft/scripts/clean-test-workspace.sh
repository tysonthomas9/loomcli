#!/usr/bin/env bash
# Best-effort cleanup for an AFT-owned isolated workspace. Terminal tabs must be
# removed before agents/workspace: deleting an agent or workspace alone does not
# guarantee that its PTY process is reclaimed.
set -u

workspace="${1:?usage: clean-test-workspace.sh <workspace-id>}"
: "${AFT_BASE_URL:?AFT_BASE_URL is required}"
: "${AFT_WORK_DIR:?AFT_WORK_DIR is required}"

stem="$(printf '%s' "$workspace" | tr -c 'A-Za-z0-9._-' '_')"
tabs_json="$AFT_WORK_DIR/cleanup-$stem-tabs.json"
tabs_list="$AFT_WORK_DIR/cleanup-$stem-tabs.list"
agents_json="$AFT_WORK_DIR/cleanup-$stem-agents.json"
agents_list="$AFT_WORK_DIR/cleanup-$stem-agents.list"

curl -s "$AFT_BASE_URL/api/workspaces/$workspace/terminal/tabs" > "$tabs_json" || true
python3 - "$tabs_json" <<'PY' > "$tabs_list"
import json, sys
try:
    data = json.load(open(sys.argv[1])).get("data") or []
except Exception:
    data = []
for tab in data if isinstance(data, list) else []:
    if tab.get("session_name"):
        print(tab["session_name"])
PY
while read -r session; do
  [ -n "$session" ] && curl -s -X DELETE "$AFT_BASE_URL/api/workspaces/$workspace/terminal/tabs/$session" >/dev/null || true
done < "$tabs_list"

curl -s "$AFT_BASE_URL/api/workspaces/$workspace/agents" > "$agents_json" || true
python3 - "$agents_json" <<'PY' > "$agents_list"
import json, sys
try:
    data = json.load(open(sys.argv[1])).get("data") or []
except Exception:
    data = []
if isinstance(data, dict):
    data = data.get("agents") or []
for agent in data if isinstance(data, list) else []:
    if agent.get("name"):
        print(agent["name"])
PY
while read -r agent; do
  [ -n "$agent" ] && curl -s -X DELETE "$AFT_BASE_URL/api/workspaces/$workspace/agents/$agent" >/dev/null || true
done < "$agents_list"

curl -s -X DELETE "$AFT_BASE_URL/api/workspaces/$workspace" >/dev/null || true
