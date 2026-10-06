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
    if case == "busy" and stage in {
        "first-waiting", "replaced-waiting", "edited-waiting", "cleared-waiting", "final-waiting"
    }:
        expected = {
            "first-waiting": "BUSY_A: first waiting note",
            "replaced-waiting": "BUSY_B: replacement note",
            "edited-waiting": "BUSY_C: edited note",
            "cleared-waiting": None,
            "final-waiting": "BUSY_D: after your current analysis, run npm test once more and report whether it passed.",
        }[stage]
        waiting = a.get("waiting_messages") or []
        receipt_count = {
            "first-waiting": 2, "replaced-waiting": 3, "edited-waiting": 4,
            "cleared-waiting": 4, "final-waiting": 5,
        }[stage]
        receipts = [e for e in rows if e["kind"] == "message.waiting"]
        assert a["running_turn_id"], f"{stage}: no real running turn"
        assert [w["text"] for w in waiting] == ([] if expected is None else [expected]), (stage, waiting)
        assert len(receipts) == receipt_count, f"{stage}: expected {receipt_count} Send receipts, got {receipts}"
        if stage == "first-waiting":
            tools = [e for e in rows if e["seq"] < receipts[-1]["seq"]
                     and e["kind"] in ("item.started", "item.completed")
                     and e["payload"].get("itemKind") == "tool"]
            assert tools, "no real tool activity before the first waiting follow-up"
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


def readme_contract():
    fixture = Path(required("AFT_TESTS_DIR")).parent / "fixtures/slack-clone/README.md"
    source = fixture.read_text()
    lines = re.findall(r"^- `([^`]+)` runs the tests\.$", source, re.MULTILINE)
    assert len(lines) == 1, f"expected one test command in {fixture}"
    command = lines[0]
    return command, f"- `{command}` runs the tests."


def delivered_event(evs, marker):
    found = [e for e in evs if e["kind"] == "message.delivered" and marker in e["payload"].get("text", "")]
    assert len(found) == 1, f"expected one delivered request containing {marker!r}: {found}"
    return found[0]


def verify_readme_turn(evs, delivered, before_seq=None):
    command, source_line = readme_contract()
    prompt = delivered["payload"].get("text", "")
    assert command not in prompt, f"the expected command was supplied by the user: {prompt}"
    end = before_seq if before_seq is not None else float("inf")
    assert delivered["seq"] < end
    reads = [e for e in evs
             if delivered["seq"] < e["seq"] < end and e["kind"] == "item.completed"
             and e["payload"].get("itemKind") == "tool"
             and "README.md" in (e["payload"].get("tool") or {}).get("input", "")
             and not (e["payload"].get("tool") or {}).get("failed")
             and source_line in (e["payload"].get("tool") or {}).get("output", "")]
    assert reads, f"no successful README tool output contained the checked-in source line: {source_line}"
    answer = "\n".join(e["payload"].get("text", "") for e in evs
                       if reads[0]["seq"] < e["seq"] < end and e["kind"] == "item.completed"
                       and e["payload"].get("itemKind") == "message")
    assert command in answer and "README.md" in answer, f"answer after README read missed its documented command/file: {answer}"
    return answer


def agent_list_payloads(raw):
    decoder = json.JSONDecoder()
    found = []

    def visit(value):
        if isinstance(value, dict):
            if isinstance(value.get("agents"), list):
                found.append(value["agents"])
            for child in value.values():
                visit(child)
        elif isinstance(value, list):
            for child in value:
                visit(child)
        elif isinstance(value, str):
            for index, char in enumerate(value):
                if char not in "{[":
                    continue
                try:
                    parsed, _ = decoder.raw_decode(value[index:])
                except json.JSONDecodeError:
                    continue
                visit(parsed)

    visit(raw)
    assert found, f"agent_list tool output had no parseable agents array: {raw}"
    return found


def invokes_agent_list(tool):
    if tool.get("name") != "execute":
        return False
    try:
        code = json.loads(tool.get("input", ""))["code"]
    except (json.JSONDecodeError, KeyError, TypeError):
        return False
    return bool(re.search(r"\btools\.loom\.agent_list\s*\(", code))


def assert_chat():
    before, after = rows("chat", "before-reopen"), rows("chat", "after-reopen")
    assert [e["event_id"] for e in before] == [e["event_id"] for e in after[: len(before)]], "reload changed saved history"
    read_request = delivered_event(after, "README.md")
    list_request = delivered_event(after, "agent_list")
    verify_readme_turn(after, read_request, list_request["seq"])
    calls = [e for e in after if e["seq"] > list_request["seq"] and e["kind"] == "item.completed"
             and e["payload"].get("itemKind") == "tool"
             and invokes_agent_list(e["payload"].get("tool") or {})]
    assert len(calls) == 1, f"expected one real Loom agent_list call, got {calls}"
    tool = calls[0]["payload"]["tool"]
    assert not tool.get("failed"), tool
    children = request(f"{PREFIX}?parent={urllib.parse.quote(agent_id('chat'))}&limit=500")["agents"]
    expected_ids = sorted(a["agent_id"] for a in children)
    for roster in agent_list_payloads(tool.get("output", "")):
        actual_ids = sorted(a["agent_id"] for a in roster)
        assert actual_ids == expected_ids, f"agent_list output disagrees with this Lead's child roster: {actual_ids} != {expected_ids}"
    assert any(e["seq"] > calls[0]["seq"] and e["kind"] == "item.completed"
               and e["payload"].get("itemKind") == "message" for e in after), "no reply after agent_list"


def assert_persona():
    before, after = rows("persona", "answer"), rows("persona", "reloaded")
    assert [e["event_id"] for e in before] == [e["event_id"] for e in after], "persona history changed on reload"
    delivered = delivered_event(after, "README.md")
    assert len([e for e in after if e["kind"] == "message.delivered"]) == 1
    answer = verify_readme_turn(after, delivered)
    assert f"PERSONA_{RUN}" in answer and "{{.AgentName}}" in answer, answer


def assert_busy():
    evs = rows("busy", "finished")
    receipts = [e["event_id"] for e in evs if e["kind"] == "message.waiting"]
    assert len(receipts) == 6 and len(set(receipts)) == 6, f"expected one saved Send receipt event per six user sends: {receipts}"
    assert sum(e["kind"] == "message.withdrawn" for e in evs) == 1, "Clear did not save one withdrawal"
    delivered = text_events(evs, "message.delivered")
    assert len(delivered) == 3, f"expected only initial, final waiting and post-Stop deliveries: {delivered}"
    for absent in ("BUSY_A", "BUSY_B", "BUSY_C"):
        assert not any(absent in t for t in delivered), f"withdrawn text reached the model: {absent}"
    assert sum("BUSY_D" in t for t in delivered) == 1, delivered
    assert sum("BUSY_NEXT" in t for t in delivered) == 1, delivered
    stopped = [e for e in evs if e["kind"] == "agent.turn_completed" and e["payload"].get("stopReason") == "cancelled"]
    assert stopped, "Stop did not save a cancelled turn"
    next_request = delivered_event(evs, "BUSY_NEXT")
    verify_readme_turn(evs, next_request)


def preflight():
    assert required("AFT_REAL_BACKEND") == "opencode", "first live tier requires the available OpenCode harness"
    repo_path = required("AFT_AGENT_FLOW_REPO")
    assert Path(repo_path).is_absolute() and Path(repo_path).name == "source-repo", repo_path
    workspace = request(f"/api/workspaces/{urllib.parse.quote(WS, safe='')}")
    repos = (workspace.get("data") or workspace)["repos"]
    source = [r for r in repos if r.get("name") == "source-repo"]
    assert len(source) == 1 and source[0]["path"] == repo_path, (repo_path, source)
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
