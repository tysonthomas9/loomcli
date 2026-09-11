#!/usr/bin/env bash
# Best-effort cleanup for the opt-in real-codex aft tier. This script must
# never fail suite teardown; every operation is scoped to the isolated e2e
# workspace or the known AFT-created agent names.
set +e

base="${AFT_BASE_URL:-http://127.0.0.1:3100}"
ws="${AFT_WS:-E2E-WS}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." 2>/dev/null && pwd)"
e2e_workspace="$repo_root/tmp/e2e-workspace"

short_ws="$ws"
if [[ ${#short_ws} -gt 8 ]]; then
    short_ws="${short_ws:0:8}"
fi
if [[ -z "$short_ws" ]]; then
    short_ws="default"
fi

echo "real-codex teardown: workspace=$ws"

# Close terminal tab metadata through the current API. Older tmux session
# lifecycle routes are gone on this branch, so attempts below are deliberately
# ignored when they 404.
curl -sf "$base/api/workspaces/$ws/terminal/tabs" 2>/dev/null | python3 -c '
import json, sys

try:
    payload = json.load(sys.stdin)
except Exception:
    raise SystemExit(0)
data = payload.get("data") if isinstance(payload, dict) else payload
if not isinstance(data, list):
    raise SystemExit(0)
for tab in data:
    if isinstance(tab, dict) and tab.get("session_name"):
        print(tab["session_name"])
' | while IFS= read -r session; do
    [[ -n "$session" ]] || continue
    curl -sf -X DELETE "$base/api/workspaces/$ws/terminal/tabs/$session" >/dev/null 2>&1 || true
done

curl -sf -X POST "$base/api/workspaces/$ws/terminal/sessions/close-all" >/dev/null 2>&1 || true

# Delete agent definitions this tier may create now or in follow-up terminal
# scenarios. Keep the match narrow so unrelated local agents are not removed.
{
    printf '%s\n' "nova"
    curl -sf "$base/api/workspaces/$ws/agents" 2>/dev/null | python3 -c '
import json, re, sys

try:
    payload = json.load(sys.stdin)
except Exception:
    raise SystemExit(0)
data = payload.get("data") if isinstance(payload, dict) else payload
if not isinstance(data, list):
    raise SystemExit(0)
for agent in data:
    if not isinstance(agent, dict):
        continue
    name = str(agent.get("name") or "")
    if name == "nova" or re.match(r"^real-codex($|-)", name):
        print(name)
'
} | sort -u | while IFS= read -r agent; do
    [[ -n "$agent" ]] || continue
    curl -sf -X DELETE "$base/api/workspaces/$ws/agents/$agent" >/dev/null 2>&1 || true
done

# Stop this tier's detached auto-mode owner FIRST — it is a long-running loop
# that will respawn the tmux session + codex child we reap below, so it must be
# dead before (and not merely alongside) that reap. Match on the isolated loom
# binary + workspace + agent name + --auto. The argv path can be unresolved
# (e.g. tests/aft/../../tmp/loom-e2e), so anchor on the stable `tmp/loom-e2e`
# substring rather than the realpath'd $repo_root, which would never match.
# Escalate TERM -> KILL so a wedged loop cannot survive to respawn.
stop_automode_owner() {
    local sig="$1"
    command -v ps >/dev/null 2>&1 || return 0
    ps -axo pid=,command= 2>/dev/null | while read -r pid command; do
        [[ -n "$pid" && -n "$command" ]] || continue
        case "$command" in
            *tmp/loom-e2e*"--workspace $ws"*"task real-codex-term"*"--auto"*)
                echo "real-codex teardown: signaling $sig auto-mode loom pid $pid"
                kill "$sig" "$pid" >/dev/null 2>&1 || true
                ;;
        esac
    done
}
stop_automode_owner -TERM
sleep 1
stop_automode_owner -KILL

# Kill only codex exec processes that are demonstrably tied to this isolated
# workspace, either by command line or by open cwd/files under tmp/e2e-workspace.
if command -v ps >/dev/null 2>&1; then
    ps -axo pid=,command= 2>/dev/null | while read -r pid command; do
        [[ -n "$pid" && -n "$command" ]] || continue
        case "$command" in
            *"codex exec"*) ;;
            *) continue ;;
        esac
        should_kill=0
        case "$command" in
            *"$e2e_workspace"*) should_kill=1 ;;
        esac
        if [[ "$should_kill" != "1" ]] && command -v lsof >/dev/null 2>&1; then
            lsof -p "$pid" 2>/dev/null | grep -F "$e2e_workspace" >/dev/null 2>&1 && should_kill=1
        fi
        if [[ "$should_kill" == "1" ]]; then
            echo "real-codex teardown: killing codex exec pid $pid"
            kill "$pid" >/dev/null 2>&1 || true
        fi
    done
fi

# Auto-mode tmux sessions are named loom-<short-workspace>-<role>-<agent>-<pid>.
# Limit cleanup to this e2e workspace prefix.
if command -v tmux >/dev/null 2>&1; then
    tmux list-sessions -F '#{session_name}' 2>/dev/null | while IFS= read -r session; do
        case "$session" in
            "loom-$short_ws-"*)
                echo "real-codex teardown: killing tmux session $session"
                tmux kill-session -t "$session" >/dev/null 2>&1 || true
                ;;
        esac
    done
fi

exit 0
