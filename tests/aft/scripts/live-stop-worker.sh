#!/usr/bin/env bash
# Stop one daemon-supervised live worker through the product lifecycle endpoint,
# then prove the real daemon consumed the command. State/desired_state alone are
# insufficient: the HTTP handler writes them before the daemon poller drains the
# process, so the daemon log is the second, independent completion signal.
#
# Usage: live-stop-worker.sh <agent-name> [workspace-key]
set -euo pipefail

base="${AFT_BASE_URL:?AFT_BASE_URL not set}"
daemon_pid="${AFT_DAEMON_PID:?AFT_DAEMON_PID not set}"
daemon_log="${AFT_DAEMON_LOG:?AFT_DAEMON_LOG not set}"
agent="${1:?usage: live-stop-worker.sh <agent-name> [workspace-key]}"
ws="${2:-${AFT_WS:-E2E-WS}}"

kill -0 "$daemon_pid" 2>/dev/null || {
    echo "LIVE WORKER CLEANUP FAILED: owned daemon $daemon_pid is not running" >&2
    exit 1
}

curl -sf -X POST "$base/api/workspaces/$ws/agents/$agent/stop" >/dev/null

for _ in $(seq 1 100); do
    state_ok=""
    if curl -sf "$base/api/workspaces/$ws/agents" > "${TMPDIR:-/tmp}/aft-live-stop-$$.json"; then
        if python3 - "${TMPDIR:-/tmp}/aft-live-stop-$$.json" "$agent" <<'PY'
import json, sys
payload = json.load(open(sys.argv[1]))
data = payload.get("data") if isinstance(payload, dict) else payload
if isinstance(data, dict):
    data = data.get("agents")
agents = data if isinstance(data, list) else []
matches = [a for a in agents if isinstance(a, dict) and a.get("name") == sys.argv[2]]
assert len(matches) == 1, matches
agent = matches[0]
assert agent.get("desired_state") == "stopped", agent
assert agent.get("state") == "stopped", agent
PY
        then
            state_ok=1
        fi
    fi
    if [[ -n "$state_ok" ]] && grep -F "agent stopped via control socket" "$daemon_log" | grep -F "worktree=$agent" >/dev/null 2>&1; then
        rm -f "${TMPDIR:-/tmp}/aft-live-stop-$$.json"
        kill -0 "$daemon_pid" 2>/dev/null || {
            echo "LIVE WORKER CLEANUP FAILED: daemon exited while stopping $agent" >&2
            exit 1
        }
        echo "stopped live worker $agent through daemon $daemon_pid"
        exit 0
    fi
    sleep 1
done

rm -f "${TMPDIR:-/tmp}/aft-live-stop-$$.json"
echo "LIVE WORKER CLEANUP FAILED: daemon never confirmed stop for $agent" >&2
tail -40 "$daemon_log" >&2 || true
exit 1
