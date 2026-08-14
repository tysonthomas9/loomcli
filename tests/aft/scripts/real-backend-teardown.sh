#!/usr/bin/env bash
# Best-effort cleanup for the opt-in real-<backend> aft tiers, generalized from
# real-codex-teardown.sh (which the codex tier still uses). This script must
# never fail suite teardown; every operation is scoped to the isolated e2e
# workspace or the known AFT-created agent names.
#
# Usage: real-backend-teardown.sh <backend>   # claude|opencode|cursor|codex
set +e

backend="${1:-}"
if [[ -z "$backend" ]]; then
    echo "real-backend teardown: no backend given; nothing to do"
    exit 0
fi

# Process signature of a non-interactive run of this backend's CLI (mirrors
# internal/workflows/builtin/local-task-runner.ts backendArgs()).
case "$backend" in
    codex)    cli_sig="codex exec" ;;
    claude)   cli_sig="claude -p" ;;
    opencode) cli_sig="opencode run" ;;
    cursor)   cli_sig="cursor-agent -p" ;;
    *)        cli_sig="" ;;
esac

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

echo "real-$backend teardown: workspace=$ws"

# Close terminal tab metadata through the current API; ignore 404s.
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

# Delete agent definitions this tier may create. Keep the match narrow so
# unrelated local agents are not removed.
curl -sf "$base/api/workspaces/$ws/agents" 2>/dev/null | AFT_TEARDOWN_BACKEND="$backend" python3 -c '
import json, os, re, sys

try:
    payload = json.load(sys.stdin)
except Exception:
    raise SystemExit(0)
data = payload.get("data") if isinstance(payload, dict) else payload
if not isinstance(data, list):
    raise SystemExit(0)
backend = os.environ.get("AFT_TEARDOWN_BACKEND", "")
for agent in data:
    if not isinstance(agent, dict):
        continue
    name = str(agent.get("name") or "")
    if backend and re.match(r"^real-" + re.escape(backend) + r"($|-)", name):
        print(name)
' | sort -u | while IFS= read -r agent; do
    [[ -n "$agent" ]] || continue
    curl -sf -X DELETE "$base/api/workspaces/$ws/agents/$agent" >/dev/null 2>&1 || true
done

# Stop any detached auto-mode owner for this tier FIRST (it respawns the tmux
# session + CLI child we reap below). Anchor on the stable tmp/loom-e2e
# substring; escalate TERM -> KILL.
stop_automode_owner() {
    local sig="$1"
    command -v ps >/dev/null 2>&1 || return 0
    ps -axo pid=,command= 2>/dev/null | while read -r pid command; do
        [[ -n "$pid" && -n "$command" ]] || continue
        case "$command" in
            *tmp/loom-e2e*"--workspace $ws"*"task real-$backend-term"*"--auto"*)
                echo "real-$backend teardown: signaling $sig auto-mode loom pid $pid"
                kill "$sig" "$pid" >/dev/null 2>&1 || true
                ;;
        esac
    done
}
stop_automode_owner -TERM
sleep 1
stop_automode_owner -KILL

# Kill only backend CLI processes that are demonstrably tied to this isolated
# workspace, either by command line or by open cwd/files under tmp/e2e-workspace.
if [[ -n "$cli_sig" ]] && command -v ps >/dev/null 2>&1; then
    ps -axo pid=,command= 2>/dev/null | while read -r pid command; do
        [[ -n "$pid" && -n "$command" ]] || continue
        case "$command" in
            *"$cli_sig"*) ;;
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
            echo "real-$backend teardown: killing $cli_sig pid $pid"
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
                echo "real-$backend teardown: killing tmux session $session"
                tmux kill-session -t "$session" >/dev/null 2>&1 || true
                ;;
        esac
    done
fi

exit 0
