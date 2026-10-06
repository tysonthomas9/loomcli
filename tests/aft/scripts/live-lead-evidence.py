#!/usr/bin/env python3
"""Read-only Agent API evidence for the real Lead AFT suite."""

import json
import os
import re
import subprocess
import sys
import urllib.parse
import urllib.request
from pathlib import Path


def required(name):
    value = os.environ.get(name, "")
    if not value:
        raise ValueError(f"{name} is required")
    return value


WORK = Path(required("AFT_WORK_DIR")) / "live-lead"
WS = required("AFT_WS")
RUN = required("RUN_ID")
BASE = required("AFT_API_URL").rstrip("/")
PREFIX = f"/api/workspaces/{urllib.parse.quote(WS, safe='')}/v1/agents"


def request(path, method="GET", body=None, key=None):
    headers = {"Accept": "application/json"}
    data = None
    if body is not None:
        headers["Content-Type"] = "application/json"
        data = json.dumps(body).encode()
    if key:
        headers["Idempotency-Key"] = key
    with urllib.request.urlopen(
        urllib.request.Request(BASE + path, data=data, headers=headers, method=method), timeout=15
    ) as response:
        raw = response.read()
    return json.loads(raw) if raw else None


def write(name, value):
    WORK.mkdir(parents=True, exist_ok=True)
    (WORK / name).write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def agent_id(case):
    value = (WORK / f"{case}.id").read_text().strip()
    if not re.fullmatch(r"agt_[A-Za-z0-9_-]+", value):
        raise AssertionError(f"invalid saved agent ID: {value!r}")
    return value


def agent(case):
    return request(f"{PREFIX}/{agent_id(case)}")


def events(case):
    result, after = [], 0
    while True:
        page = request(f"{PREFIX}/{agent_id(case)}/events?after={after}&limit=500")
        result.extend(page["events"])
        if not page["more"]:
            break
        next_after = page["next"]
        if next_after <= after:
            raise AssertionError("event cursor did not advance")
        after = next_after
    ids = [e["event_id"] for e in result]
    seqs = [e["seq"] for e in result]
    assert len(ids) == len(set(ids)) and len(seqs) == len(set(seqs)), "duplicate saved event"
    assert seqs == sorted(seqs), "saved events are out of order"
    return result


def claim(case):
    session = required("AFT_SESSION")
    value = subprocess.check_output(
        ["agent-browser", "--session", session, "eval", "location.pathname"], text=True
    ).strip()
    try:
        path = json.loads(value)
    except json.JSONDecodeError:
        path = value.strip('"')
    match = re.fullmatch(rf"/ws/{re.escape(WS)}/chat/(agt_[A-Za-z0-9_-]+)", path)
    assert match, f"New Agent did not open an Agent API chat: {path!r}"
    WORK.mkdir(parents=True, exist_ok=True)
    (WORK / f"{case}.id").write_text(match[1] + "\n")
    a = agent(case)
    assert a["agent_id"] == match[1]
    assert a["name"] == f"aft-lead-{RUN}-{case}", a
    assert a["repo"] == required("AFT_AGENT_FLOW_REPO"), a
    assert a["harness"] == required("AFT_REAL_BACKEND"), a
    assert a["preset"] == "lead" and a["created_by_kind"] == "user", a
    assert a["parent_agent_id"] is None, a
    write(f"{case}-identity.json", a)


def snapshot(case, stage):
    a, rows = agent(case), events(case)
    write(f"{case}-{stage}.json", {"agent": a, "events": rows})


def rows(case, stage):
    return json.loads((WORK / f"{case}-{stage}.json").read_text())["events"]


def text_events(rows, kind, item_kind=None):
    return [
        e["payload"].get("text", "")
        for e in rows
        if e["kind"] == kind
        and (item_kind is None or e["payload"].get("itemKind") == item_kind)
    ]


def assert_chat():
    before, after = rows("chat", "before-reopen"), rows("chat", "after-reopen")
    assert [e["event_id"] for e in before] == [e["event_id"] for e in after[: len(before)]], "reload changed saved history"
    delivered = text_events(after, "message.delivered")
    assert len([t for t in delivered if "README.md" in t and "npm test" in t]) == 1, delivered
    assert len([t for t in delivered if "agent_list" in t]) == 1, delivered
    answers = text_events(after, "item.completed", "message")
    assert any("npm test" in t for t in answers), answers
    calls = [e for e in after if e["kind"] == "item.completed" and e["payload"].get("itemKind") == "tool"
             and "agent_list" in (e["payload"].get("tool") or {}).get("input", "")]
    assert len(calls) == 1, f"expected one real Loom agent_list call, got {calls}"
    tool = calls[0]["payload"]["tool"]
    assert not tool.get("failed"), tool
    assert '"agents":[]' in "".join(tool.get("output", "").split()), tool
    children = request(f"{PREFIX}?parent={urllib.parse.quote(agent_id('chat'))}&limit=500")["agents"]
    assert children == [], f"agent_list output disagrees with this Lead's child roster: {children}"


def assert_persona():
    before, after = rows("persona", "answer"), rows("persona", "reloaded")
    assert [e["event_id"] for e in before] == [e["event_id"] for e in after], "persona history changed on reload"
    answer = "\n".join(text_events(after, "item.completed", "message"))
    assert f"PERSONA_{RUN}" in answer and "{{.AgentName}}" in answer and "npm test" in answer, answer
    delivered = text_events(after, "message.delivered")
    assert len(delivered) == 1 and "README.md" in delivered[0], delivered


def assert_busy():
    evs = rows("busy", "finished")
    delivered = text_events(evs, "message.delivered")
    for absent in ("BUSY_A", "BUSY_B", "BUSY_C"):
        assert not any(absent in t for t in delivered), f"withdrawn text reached the model: {absent}"
    assert sum("BUSY_D" in t for t in delivered) == 1, delivered
    assert sum("BUSY_NEXT" in t for t in delivered) == 1, delivered
    stopped = [e for e in evs if e["kind"] == "agent.turn_completed" and e["payload"].get("stopReason") == "cancelled"]
    assert stopped, "Stop did not save a cancelled turn"
    next_seq = next(e["seq"] for e in evs if e["kind"] == "message.delivered" and "BUSY_NEXT" in e["payload"].get("text", ""))
    answer = "\n".join(e["payload"].get("text", "") for e in evs
                       if e["seq"] > next_seq and e["kind"] == "item.completed"
                       and e["payload"].get("itemKind") == "message")
    assert "npm test" in answer, "the post-Stop turn did not answer the repo question"


def preflight():
    assert required("AFT_REAL_BACKEND") == "opencode", "first live tier requires the available OpenCode harness"
    assert required("AFT_AGENT_FLOW_REPO") == "/workspace/source-repo"
    workspace = request(f"/api/workspaces/{urllib.parse.quote(WS, safe='')}")
    repos = (workspace.get("data") or workspace)["repos"]
    assert any(r["path"] == required("AFT_AGENT_FLOW_REPO") for r in repos), repos
    request(f"/api/workspaces/{urllib.parse.quote(WS, safe='')}/v1/harnesses/opencode")


def cleanup():
    for case in ("chat", "persona", "busy"):
        expected = f"aft-lead-{RUN}-{case}"
        for a in request(f"{PREFIX}?name={urllib.parse.quote(expected)}&include_archived=true&limit=500")["agents"]:
            if a["name"] != expected or a["repo"] != required("AFT_AGENT_FLOW_REPO"):
                continue
            if a["preset"] != "lead" or a["created_by_kind"] != "user" or a["parent_agent_id"] is not None:
                continue
            if not a.get("archived_at"):
                request(f"{PREFIX}/{a['agent_id']}/archive", "POST", {"reason": "cancelled"}, f"aft-lead-{RUN}-{case}-archive")


def main():
    command, *args = sys.argv[1:]
    if command == "preflight":
        preflight()
    elif command == "claim":
        claim(*args)
    elif command == "snapshot":
        snapshot(*args)
    elif command == "assert-chat":
        assert_chat()
    elif command == "assert-persona":
        assert_persona()
    elif command == "assert-busy":
        assert_busy()
    elif command == "cleanup":
        cleanup()
    else:
        raise ValueError(f"unknown command: {command}")


if __name__ == "__main__":
    main()
