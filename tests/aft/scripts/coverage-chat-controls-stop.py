#!/usr/bin/env python3
"""Saved, nonsecret oracles for a real bare Chat Stop over a native ask."""

import importlib.util
import json
import os
import sys
from pathlib import Path

module_path = Path(__file__).with_name("coverage-chat-controls.py")
spec = importlib.util.spec_from_file_location("coverage_chat_controls", module_path)
controls = importlib.util.module_from_spec(spec)
spec.loader.exec_module(controls)


def create():
    controls.create("stop", "pr-review-interactive", "target")


def pending():
    controls.ask("stop", "pending")
    identity = controls.saved("stop-pending-ask.json")
    rows = controls.events("stop")
    assert not [e for e in rows if e["kind"] in ("ask.resolved", "ask.lost") and
                e["payload"].get("askId") == identity["ask_id"]], "ask was answered before bare Stop"


def stopped():
    identity = controls.saved("stop-pending-ask.json")
    a, rows = controls.agent("stop"), controls.events("stop")
    assert a["running_turn_id"] is None and not a["open_asks"], "Stop left an ask or turn active"
    turn = identity["turn_id"]
    ends = [e for e in rows if e["kind"] == "agent.turn_completed" and e["turn_id"] == turn]
    assert len(ends) == 1 and ends[0]["payload"].get("stopReason") == "cancelled", \
        "bare Stop did not save one cancelled native turn"
    lost = [e for e in rows if e["kind"] == "ask.lost" and
            e["payload"].get("askId") == identity["ask_id"] and e["turn_id"] == turn]
    assert len(lost) == 1 and lost[0]["seq"] > ends[0]["seq"], "bare Stop did not lose the exact ask once"
    assert not [e for e in rows if e["kind"] == "ask.resolved" and
                e["payload"].get("askId") == identity["ask_id"]], "Stop silently answered the ask"
    effect = f"STOP_EFFECT_{controls.RUN}"
    marker = f"/tmp/cov-controls-stop-effect-{controls.RUN}"
    completed_tools = [(e["payload"].get("tool") or {}) for e in rows
                       if e["kind"] == "item.completed" and e["payload"].get("itemKind") == "tool"]
    assert not any(
        (not tool.get("failed") and effect in tool.get("input", "") and marker in tool.get("input", "")) or
        any(line.strip() == effect for line in tool.get("output", "").splitlines())
        for tool in completed_tools), "proposed shell effect executed"
    suffix = f"/v1/agents/{controls.aid('stop')}/asks/{identity['ask_id']}"
    requests = json.loads(controls.browser("eval", "performance.getEntriesByType('resource').filter(e => "
                                          f"e.name.includes({json.dumps(suffix)})).map(e => e.responseStatus)"))
    assert requests == [], "bare Stop used the Respond endpoint"
    controls.ask_history("stop", "stopped", "1,0,1")
    controls.save("stop-proof.json", {"ask_id": identity["ask_id"], "turn_id": turn,
                                      "completion_event_id": ends[0]["event_id"],
                                      "lost_event_id": lost[0]["event_id"],
                                      "stop_reason": "cancelled", "respond_requests": 0,
                                      "proposed_effect": effect, "idle": True})


def reloaded():
    controls.ask_history("stop", "stopped", "1,0,1", True)
    stopped()


def reused():
    controls.turn("stop", f"STOP_RECOVER_{controls.RUN}", "completed")
    controls.ask_history("stop", "stopped", "1,0,1", True)


if __name__ == "__main__":
    {"create": create, "pending": pending, "stopped": stopped,
     "reloaded": reloaded, "reused": reused}[sys.argv[1]]()
