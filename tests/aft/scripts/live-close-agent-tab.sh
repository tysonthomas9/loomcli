#!/usr/bin/env bash
# Kill an agent's terminal tab(s) and PROVE they are gone — the last step of every
# live-tier test. In the live tier the PTY holds a real, billed model process, and
# `DELETE /terminal/tabs/{session}` is what kills it (terminal/service_tabs.go:145-166):
# deleting the agent or the workspace does not (see zz-lead-agent.graph's
# `verified-16-deleting-a-lead-leaves-its-terminal-pty-running-until-ta` state,
# which pins the orphan). Suite teardown still sweeps as a backstop, but teardown
# failure is report-only in aft, so cleanup that matters has to fail the test itself.
#
# Usage: live-close-agent-tab.sh <agent-name> [workspace-key]
set -euo pipefail

base="${AFT_BASE_URL:?AFT_BASE_URL not set}"
agent="${1:?usage: live-close-agent-tab.sh <agent-name> [workspace-key]}"
ws="${2:-${AFT_WS:-E2E-WS}}"

sessions_for_agent() {
    curl -sf "$base/api/workspaces/$ws/terminal/tabs" | python3 -c '
import json, sys

payload = json.load(sys.stdin)
tabs = payload.get("data") if isinstance(payload, dict) else payload
if not isinstance(tabs, list):
    raise SystemExit("unexpected terminal tabs response shape: expected a list under \"data\"")
want = sys.argv[1]
for tab in tabs:
    if isinstance(tab, dict) and tab.get("agent_id") == want and tab.get("session_name"):
        print(tab["session_name"])
' "$agent"
}

# Reachability first. Without this an unreachable API makes the sweep find zero
# tabs and the verification below read zero remaining — i.e. a confident "all
# clean" for a stack we never actually asked. For a script whose whole job is
# proving a paid process is dead, silence must be failure.
if ! curl -sf "$base/api/workspaces/$ws/terminal/tabs" >/dev/null; then
    echo "LIVE CLEANUP FAILED: cannot read terminal tabs at $base (workspace $ws)" >&2
    echo "a paid backend process may still be running — check 'ps' before rerunning" >&2
    exit 1
fi

failures=0
for session in $(sessions_for_agent); do
    if curl -sf -X DELETE "$base/api/workspaces/$ws/terminal/tabs/$session" >/dev/null; then
        echo "closed live tab $session ($agent)"
    else
        echo "failed to close live tab $session ($agent)" >&2
        failures=$((failures + 1))
    fi
done

remaining_sessions="$(sessions_for_agent)" || {
    echo "LIVE CLEANUP FAILED: could not re-read terminal tabs to verify $agent is detached" >&2
    exit 1
}
remaining="$(printf '%s' "$remaining_sessions" | grep -c . || true)"
if [[ "$remaining" != "0" || "$failures" != "0" ]]; then
    echo "LIVE CLEANUP FAILED: $remaining tab(s) still attached to $agent, $failures delete failure(s)" >&2
    echo "a paid backend process may still be running — check 'ps' before rerunning" >&2
    exit 1
fi
echo "no live tabs remain for $agent"
