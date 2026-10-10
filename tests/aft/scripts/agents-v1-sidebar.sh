#!/bin/sh
# Shared helpers for the EMU-tier Agent API sidebar and rail suites
# (agents-v1-sidebar and its follow-ups). Each command acts the way an
# operator or the scripted model would; none seeds state by hand.
#
#   kid <child name> <lead follow-up text>
#       Script the fake model: the Lead's agent_create call, the Lead's
#       follow-up reply (its tool result), and a child turn that holds (a tool
#       the emulator does not run waits until interrupted), so the child keeps
#       working until it is stopped.
#   child-id <workspace> <lead id> <child name>   Print the child's agent id.
#   stop <workspace> <lead id> <child name>       Interrupt the working child,
#       which ends its task, and wait until the API no longer has it at work.
#   menu <agent id>          Open the sidebar row's context menu, as a right
#       click on the row does.
#   hover <agent id> <name>  Hover the sidebar row; wait until neither the row
#       nor its name is underlined and its "Archive <name>" action is visible.
#   hover-archive <agent id> <name>   Hover the row and press its Archive action.
#   drag-up <name>           Keyboard-drag the named top-level row up one place:
#       focus its drag handle, lift with Space, ArrowUp, drop with Space,
#       each key once the drag shows the previous one.
#   rail-ids                 Print the collapsed rail's Agent API ids, in order,
#       comma-separated.
set -eu

ab() { agent-browser --session "$AFT_SESSION" "$@"; }
row() { printf "nav[aria-label=Agents] [data-testid=sortable-agent-row] > a[href\$='/%s']" "$1"; }

cmd="${1:?usage: agents-v1-sidebar.sh <command> [args]}"
shift
case "$cmd" in
kid)
    curl -sf -X POST "$AFT_FAKE_MODEL_URL/__reset" >/dev/null
    python3 -c 'import json,sys
code = "return await tools.loom.agent_create(%s)" % json.dumps({"name": sys.argv[1], "brief": "do the task"})
print(json.dumps({"steps": [{"next": "prompt", "tool_calls": [{"name": "execute", "arguments": {"code": code}}]},
  {"text": sys.argv[2], "next": "tool"}, {"tool_calls": [{"name": "df1_hold"}], "next": "prompt"}]}))' "$1" "$2" |
        curl -sf -X POST "$AFT_FAKE_MODEL_URL/__script" -H "Content-Type: application/json" -d @- >/dev/null
    ;;
child-id)
    curl -sf "$AFT_BASE_URL/api/workspaces/$1/v1/agents?parent=$2" |
        python3 -c 'import json,sys
ids = [a["agent_id"] for a in json.load(sys.stdin)["agents"] if a["name"] == sys.argv[1]]
assert len(ids) == 1, "want one child named %s, got %d" % (sys.argv[1], len(ids))
print(ids[0])' "$3"
    ;;
stop)
    id="$("$0" child-id "$1" "$2" "$3")"
    curl -sf -X POST "$AFT_BASE_URL/api/workspaces/$1/v1/agents/$id/messages" -H "Content-Type: application/json" \
        -H "Idempotency-Key: sidebar-stop-$id" -d '{"text":"","delivery":"interrupt"}' >/dev/null
    # The interrupt is accepted before the turn ends: wait until the child is
    # no longer at work.
    i=0
    while :; do
        state="$(curl -sf "$AFT_BASE_URL/api/workspaces/$1/v1/agents/$id" | python3 -c 'import json,sys; print(json.load(sys.stdin)["state"])')"
        case "$state" in creating|active|waiting|stopping) ;; *) break ;; esac
        i=$((i + 1))
        [ "$i" -lt 120 ] || { echo "child $id still $state after its interrupt" >&2; exit 1; }
        sleep 0.5
    done
    ;;
menu)
    ab eval "(() => { const a = document.querySelector(\"$(row "$1")\");
      if (!a) throw Error('sidebar row $1 missing'); const r = a.getBoundingClientRect();
      a.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: r.x + 4, clientY: r.y + 4 }));
      return true; })()" >/dev/null
    ;;
hover)
    ab hover "$(row "$1")"
    ab wait --fn "(() => { const a = document.querySelector(\"$(row "$1")\");
      const b = a?.parentElement?.querySelector(':scope > [data-testid=agent-row-archive]');
      const n = a?.querySelector('[data-testid=agent-list-name]');
      return !!a && a.matches(':hover') && getComputedStyle(a).textDecorationLine === 'none' &&
        !!n && getComputedStyle(n).textDecorationLine === 'none' &&
        !!b && b.getAttribute('aria-label') === 'Archive $2' && Number(getComputedStyle(b).opacity) > 0.9; })()" >/dev/null
    ;;
hover-archive)
    "$0" hover "$1" "$2"
    ab click "[data-testid=sortable-agent-row]:has(> a[href\$='/$1']) > [data-testid=agent-row-archive]"
    ;;
drag-up)
    ab eval "(() => { const h = document.querySelector('nav[aria-label=Agents] [aria-label=\"Drag to reorder $1\"]');
      if (!h) throw Error('drag handle missing'); h.focus(); return document.activeElement === h; })()" | grep -q true
    item="document.querySelector('nav[aria-label=Agents] [aria-label=\"Drag to reorder $1\"]').closest('[data-testid=sortable-agent-item]')"
    ab press Space
    ab wait --fn "!!$item.querySelector(':scope > [data-testid=sortable-agent-row][data-dragging]')" >/dev/null
    ab eval "(() => { const it = $item; window.__sbDragTop = it.getBoundingClientRect().top; return true; })()" >/dev/null
    ab press ArrowUp
    ab wait --fn "$item.getBoundingClientRect().top < window.__sbDragTop - 4" >/dev/null
    ab press Space
    ;;
rail-ids)
    ab eval "Array.from(document.querySelectorAll('[data-testid=collapsed-agent-rail] [data-agent-id]')).map((x) => x.dataset.agentId).join(',')" | tr -d '"'
    ;;
*)
    echo "agents-v1-sidebar.sh: unknown command $cmd" >&2
    exit 2
    ;;
esac
