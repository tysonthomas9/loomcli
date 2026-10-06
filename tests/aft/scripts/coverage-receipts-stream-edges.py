#!/usr/bin/env python3
"""Exact public Agent receipts for the run-owned real receipts edge batch."""

import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request

API = os.environ["AFT_API_URL"].rstrip("/")
UI = os.environ["AFT_BASE_URL"].rstrip("/")
WS = os.environ["AFT_WS"]
RUN = os.environ["RUN_ID"]
REPO = os.environ["AFT_AGENT_FLOW_REPO"]
MODEL = os.environ["AFT_REAL_MODEL"]
OUT = Path(os.environ["AFT_WORK_DIR"]) / "receipts-stream-edges"
ROOT = f"/api/workspaces/{urllib.parse.quote(WS, safe='')}/v1"
UNIT = 'résumé ✓ 日本語 "quoted"\n\t<tag>&\\ 🚀 '
CAP = 1 << 20


def packed(body):
    return json.dumps(body, ensure_ascii=False, separators=(",", ":")).encode("utf-8")


def call(path, method="GET", body=None, key=None, expected=200):
    data = packed(body) if body is not None else None
    headers = {"Accept": "application/json"}
    if data is not None:
        headers["Content-Type"] = "application/json"
    if key:
        headers["Idempotency-Key"] = key
    req = urllib.request.Request(API + path, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=30) as response:
            status, raw = response.status, response.read()
    except urllib.error.HTTPError as error:
        status, raw = error.code, error.read()
    value = json.loads(raw) if raw else None
    assert status == expected, f"{method} {path}: HTTP {status}, expected {expected}; code={value.get('code') if isinstance(value, dict) else None}"
    return value


def save(label, stage, value):
    OUT.mkdir(parents=True, exist_ok=True)
    (OUT / f"{label}-{stage}.json").write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def load(label, stage):
    return json.loads((OUT / f"{label}-{stage}.json").read_text())


def name(label):
    return f"coverage-rs-edges-{label}-{RUN}"


def agent_id(label):
    value = (OUT / f"{label}.id").read_text().strip()
    assert re.fullmatch(r"agt_[A-Za-z0-9_-]+", value)
    return value


def agent_path(label):
    return f"{ROOT}/agents/{agent_id(label)}"


def agent(label):
    value = call(agent_path(label))
    assert value["agent_id"] == agent_id(label) and value["name"] == name(label)
    assert value["repo"] == REPO and value["harness"] == "opencode"
    assert value["created_by_kind"] == "user" and value["parent_agent_id"] is None
    return value


def events(label):
    found, after, snapshot = [], 0, None
    while True:
        params = {"after": after, "limit": 100}
        if snapshot is not None:
            params["snapshot"] = snapshot
        page = call(agent_path(label) + "/events?" + urllib.parse.urlencode(params))
        snapshot = page["snapshot_seq"] if snapshot is None else snapshot
        assert page["snapshot_seq"] == snapshot
        found += page["events"]
        if not page["more"]:
            break
        assert page["events"] and page["next"] > after
        after = page["next"]
    assert [e["seq"] for e in found] == sorted(set(e["seq"] for e in found))
    assert len({e["event_id"] for e in found}) == len(found)
    assert all(e["agent_id"] == agent_id(label) for e in found)
    return found


def event_ids(label):
    return [e["event_id"] for e in events(label)]


def native_key(label, request_id):
    digest = hashlib.sha256((agent_id(label) + "\x00" + request_id).encode()).hexdigest()
    return "msg_" + digest[:26]


def delivered(evs, text):
    rows = [e for e in evs if e["kind"] == "message.delivered" and e["payload"].get("text") == text]
    assert len(rows) == 1, "full persisted delivered text is missing or duplicated"
    assert rows[0]["payload"].get("inputKey"), "native InputKey missing"
    return rows[0]


def completed_turn(evs, delivery):
    turn = delivery["turn_id"]
    started = [e for e in evs if e["kind"] == "turn.started" and e["turn_id"] == turn]
    ended = [e for e in evs if e["kind"] == "agent.turn_completed" and e["turn_id"] == turn and e["seq"] > delivery["seq"]]
    assert turn and len(started) == len(ended) == 1 and ended[0]["payload"].get("stopReason") == "completed", "delivered input did not complete one real native turn"
    return ended[0]


def native(action, label=None, key=None):
    helper = Path(os.environ["AFT_TESTS_DIR"]) / "scripts/coverage-receipts-stream-edges-native.sh"
    args = [str(helper), action]
    if label is not None:
        args.append(agent_id(label))
    if key is not None:
        args.append(key)
    return json.loads(subprocess.check_output(args, text=True).strip().splitlines()[-1])


def preflight():
    assert WS == "LOCALMODE" and os.environ["AFT_REAL_BACKEND"] == "opencode"
    assert Path(REPO).name == "source-repo" and MODEL and not MODEL.startswith("aft/")
    ws = call(f"/api/workspaces/{urllib.parse.quote(WS, safe='')}")
    ws = ws.get("data", ws)
    assert len(ws["repos"]) == 1 and ws["repos"][0]["name"] == "source-repo"
    assert ws["repos"][0]["path"] == ws["path"].rstrip("/") + "/source-repo" == REPO


def claim_interrupt():
    route = subprocess.check_output(["agent-browser", "--session", os.environ["AFT_SESSION"], "get", "url"], text=True).strip()
    match = re.fullmatch(rf"{re.escape(UI)}/ws/{re.escape(WS)}/chat/(agt_[A-Za-z0-9_-]+)", route.split("?", 1)[0])
    assert match, "Chat route does not own the UI-created interrupt Lead"
    OUT.mkdir(parents=True, exist_ok=True)
    (OUT / "interrupt.id").write_text(match[1] + "\n")
    a = agent("interrupt")
    assert a["preset"] == "lead" and a["model"] == MODEL and not a["model_unverified"]
    save("interrupt", "identity", {"agent_id": a["agent_id"], "name": a["name"], "actor": "production UI user"})


def create_api(label, preset):
    body = {"preset": preset, "name": name(label), "repo": REPO, "base_ref": "main",
            "overrides": {"harness": "opencode", "model": MODEL}}
    a = call(ROOT + "/agents", "POST", body, f"rs-edges-{RUN}-{label}-create", 201)
    assert re.fullmatch(r"agt_[A-Za-z0-9_-]+", a["agent_id"])
    OUT.mkdir(parents=True, exist_ok=True)
    (OUT / f"{label}.id").write_text(a["agent_id"] + "\n")
    assert a["agent_id"] == agent_id(label) and a["name"] == name(label)
    assert a["preset"] == preset and a["model"] == MODEL and not a["model_unverified"]
    assert a["repo"] == REPO and a["worktree_path"] and a["created_by_kind"] == "user"
    proof = native("probe", label)
    assert proof["agent_id"] == a["agent_id"] and proof["worktree_path"] == a["worktree_path"]
    save(label, "identity", {"agent_id": a["agent_id"], "name": a["name"], "preset": preset,
                             "worktree_path": a["worktree_path"], "native_ref": proof["native_id"],
                             "actor": "public user Create API, model supplied by runner-validated override"})


def open_chat(label):
    subprocess.run(["agent-browser", "--session", os.environ["AFT_SESSION"], "open",
                    UI + f"/ws/{WS}/chat/{agent_id(label)}"], check=True, stdout=subprocess.DEVNULL)


def running():
    a = agent("interrupt")
    assert a["state"] == "active" and a["running_turn_id"]
    save("interrupt", "running", {"turn_id": a["running_turn_id"], "event_ids": event_ids("interrupt")})


def send(label, text, key, delivery=None):
    body = {"text": text}
    if delivery:
        body["delivery"] = delivery
    result = call(agent_path(label) + "/messages", "POST", body, key, 202)
    assert result["message_id"] and result["state"] in ("waiting", "handed")
    return body, result


def queue_interrupt():
    text = f"INTERRUPT_OLD-{RUN}: superseded waiting text"
    key = f"rs-edges-{RUN}-interrupt-old"
    body, receipt = send("interrupt", text, key)
    a = agent("interrupt")
    assert a["running_turn_id"] == load("interrupt", "running")["turn_id"]
    assert receipt["state"] == "waiting" and receipt["replaced"] is False
    assert len(a["waiting_messages"]) == 1 and a["waiting_messages"][0]["text"] == text
    save("interrupt", "old", {"body": body, "key": key, "receipt": receipt,
                               "event_ids": event_ids("interrupt")})


def replace_interrupt():
    old = load("interrupt", "old")
    text = f"INTERRUPT_NEW-{RUN}: after stopping that turn, read README.md and name the repository entry point."
    key = f"rs-edges-{RUN}-interrupt-new"
    body, receipt = send("interrupt", text, key, "interrupt")
    assert receipt.get("interrupted") is True and receipt["replaced"] is True
    old_retry = call(agent_path("interrupt") + "/messages", "POST", old["body"], old["key"], 202)
    new_retry = call(agent_path("interrupt") + "/messages", "POST", body, key, 202)
    assert old_retry == old["receipt"] and new_retry == receipt
    save("interrupt", "new", {"body": body, "key": key, "receipt": receipt,
                               "old_receipt": old_retry, "new_receipt": new_retry})


def interrupt_after():
    old, new = load("interrupt", "old"), load("interrupt", "new")
    evs = events("interrupt")
    row = delivered(evs, new["body"]["text"])
    completed = completed_turn(evs, row)
    assert not any(e["kind"] == "message.delivered" and e["payload"].get("text") == old["body"]["text"] for e in evs)
    cancelled = [e for e in evs if e["kind"] == "agent.turn_completed" and
                 e["turn_id"] == load("interrupt", "running")["turn_id"] and
                 e["payload"].get("stopReason") == "cancelled"]
    assert len(cancelled) == 1, "native interrupted turn did not save one cancelled completion"
    cancel = cancelled[0]
    assert cancel["seq"] < row["seq"], "interrupt did not order cancellation before First delivery"
    assert not agent("interrupt")["waiting_messages"]
    before = event_ids("interrupt")
    for entry in (old, new):
        assert call(agent_path("interrupt") + "/messages", "POST", entry["body"], entry["key"], 202) == entry["receipt"]
    assert event_ids("interrupt") == before, "RequestID retry appended an event after delivery"
    native_result = native("count", "interrupt", row["payload"]["inputKey"])
    assert native_result["native_user_message_count"] == 1
    save("interrupt", "delivered", {"event_id": row["event_id"], "input_key": row["payload"]["inputKey"],
                                     "completed_event_id": completed["event_id"],
                                     "cancelled_event_id": cancel["event_id"], "event_ids": before,
                                     "native_user_message_count": 1})


def interrupt_next():
    text = f"INTERRUPT_NEXT-{RUN}: read README.md again and name one documented command with a file citation."
    evs = events("interrupt")
    row = delivered(evs, text)
    completed = completed_turn(evs, row)
    assert not agent("interrupt")["waiting_messages"]
    assert native("count", "interrupt", row["payload"]["inputKey"])["native_user_message_count"] == 1
    before = load("interrupt", "delivered")["event_ids"]
    after = event_ids("interrupt")
    assert after[:len(before)] == before and len(after) > len(before)
    save("interrupt", "next", {"event_id": row["event_id"], "completed_event_id": completed["event_id"], "event_ids": after})


def ask_baseline():
    a = agent("large")
    assert a["preset"] == "pr-review-interactive" and a["state"] == "active"
    assert a["running_turn_id"] and len(a["open_asks"]) == 1 and not a["waiting_messages"]
    evs = events("large")
    ask = a["open_asks"][0]
    assert len([e for e in evs if e["kind"] == "ask.opened" and e["payload"].get("askId") == ask["id"]]) == 1
    save("large", "ask", {"ask_id": ask["id"], "turn_id": a["running_turn_id"],
                           "delivered_ids": [e["event_id"] for e in evs if e["kind"] == "message.delivered"],
                           "actor": "public user API queue while genuine OpenCode approval is pending"})


def text_for(stage):
    if stage == "hundred-k":
        text = UNIT * (100_001 // len(UNIT.encode("utf-8")) + 1)
        assert len(text.encode("utf-8")) > 100_000
        return text
    assert stage == "near-cap"
    base = {"text": ""}
    unit_bytes = len(packed({"text": UNIT})) - len(packed(base))
    repeats = (CAP - 1 - len(packed(base))) // unit_bytes
    text = UNIT * repeats
    text += "x" * (CAP - 1 - len(packed({"text": text})))
    assert len(packed({"text": text})) == CAP - 1 and len(text.encode("utf-8")) > 100_000
    return text


def large_send(stage):
    assert stage in ("hundred-k", "near-cap")
    text = text_for(stage)
    key = f"rs-edges-{RUN}-{stage}"
    body, receipt = send("large", text, key)
    a = agent("large")
    assert a["state"] == "active" and a["running_turn_id"] == load("large", "ask")["turn_id"]
    assert len(a["open_asks"]) == 1 and a["open_asks"][0]["id"] == load("large", "ask")["ask_id"]
    assert len(a["waiting_messages"]) == 1 and a["waiting_messages"][0]["text"].encode("utf-8") == text.encode("utf-8")
    assert receipt["state"] == "waiting" and receipt["replaced"] is (stage == "near-cap")
    save("large", stage, {"key": key, "receipt": receipt, "text_bytes": len(text.encode("utf-8")),
                           "json_body_bytes": len(packed(body)), "sha256": hashlib.sha256(text.encode()).hexdigest(),
                           "event_ids": event_ids("large")})


def large_over():
    text = text_for("near-cap")
    body = {"text": text + "xx"}
    assert len(packed(body)) > CAP
    before = agent("large")
    before_ids = event_ids("large")
    key = f"rs-edges-{RUN}-over-cap"
    error = call(agent_path("large") + "/messages", "POST", body, key, 413)
    assert isinstance(error, dict) and "too large" in error.get("error", "").lower()
    after = agent("large")
    assert after["waiting_messages"] == before["waiting_messages"]
    assert len(after["waiting_messages"]) == 1 and after["waiting_messages"][0]["text"].encode() == text.encode()
    assert event_ids("large") == before_ids, "over-cap Send changed saved events"
    save("large", "over-cap", {"key": key, "status": 413, "json_body_bytes": len(packed(body)),
                               "waiting_sha256": hashlib.sha256(text.encode()).hexdigest(), "event_ids": before_ids})


def withdraw_large():
    result = call(agent_path("large") + "/messages/waiting", "DELETE", key=f"rs-edges-{RUN}-withdraw-near")
    assert result == {"result": "withdrawn"} and not agent("large")["waiting_messages"]
    evs = events("large")
    assert sum(e["kind"] == "message.withdrawn" for e in evs) == 1
    save("large", "withdrawn", {"result": result, "event_ids": [e["event_id"] for e in evs]})


def retry_over_key():
    key = load("large", "over-cap")["key"]
    text = f"OVER_CAP_RETRY-{RUN}: valid body after rejected 413"
    _, receipt = send("large", text, key)
    assert receipt["state"] == "waiting" and receipt["replaced"] is False
    a = agent("large")
    assert len(a["waiting_messages"]) == 1 and a["waiting_messages"][0]["text"] == text
    result = call(agent_path("large") + "/messages/waiting", "DELETE", key=f"rs-edges-{RUN}-withdraw-retry")
    assert result == {"result": "withdrawn"} and not agent("large")["waiting_messages"]
    save("large", "over-key-retry", {"request_id": key, "accepted_receipt": receipt,
                                     "withdraw_result": result, "event_ids": event_ids("large")})


def large_after():
    a = agent("large")
    assert a["state"] == "idle" and not a["open_asks"] and not a["waiting_messages"]
    evs = events("large")
    baseline = load("large", "ask")["delivered_ids"]
    assert [e["event_id"] for e in evs if e["kind"] == "message.delivered"] == baseline
    assert sum(e["kind"] == "message.withdrawn" for e in evs) == 2
    assert any(e["kind"] == "ask.resolved" and e["payload"].get("askId") == load("large", "ask")["ask_id"] for e in evs)
    ended = [e for e in evs if e["kind"] == "agent.turn_completed" and e["turn_id"] == load("large", "ask")["turn_id"]]
    assert len(ended) == 1 and ended[0]["payload"].get("stopReason") == "declined"
    absent = {}
    for stage in ("hundred-k", "near-cap", "over-cap"):
        key = native_key("large", load("large", stage)["key"])
        result = native("absent", "large", key)
        assert result["native_user_message_count"] == 0
        absent[stage] = key
    save("large", "final", {"delivered_ids": baseline, "withdrawn": 2,
                             "event_ids": [e["event_id"] for e in evs], "absent_native_input_keys": absent,
                             "evidence": "queued byte round-trip only; no large native delivery"})


def create_baseline():
    assert not listed_create()
    save("create", "baseline", {"inventory": native("inventory"), "name": name("create")})


def listed_create():
    query = urllib.parse.urlencode({"name": name("create"), "include_archived": "true"})
    rows = call(ROOT + "/agents?" + query)["agents"]
    return [a for a in rows if a["name"] == name("create")]


def create_refuse(stage):
    assert stage in ("missing", "unknown")
    body = {"preset": "lead", "name": name("create"), "repo": REPO,
            "overrides": {"harness": "opencode", "model": MODEL}}
    if stage == "unknown":
        body["base_ref"] = f"no-such-ref-{RUN}"
    error = call(ROOT + "/agents", "POST", body, f"rs-edges-{RUN}-create-{stage}", 400)
    assert error["code"] == "preset_invalid" and "base_ref" in error["error"]
    if stage == "unknown":
        assert body["base_ref"] in error["error"] and REPO in error["error"]
    assert not listed_create(), "refused Create left an Agent row"
    assert native("inventory") == load("create", "baseline")["inventory"], "refused Create left an Agent, worktree or native registration"
    save("create", stage, {"status": 400, "code": error["code"], "request_id": f"rs-edges-{RUN}-create-{stage}",
                           "agent_rows": 0, "inventory_unchanged": True})


def create_harness_capability():
    preset = call(ROOT + "/presets/lead")
    assert preset["name"] == "lead" and "opencode" in preset["harnesses"]
    candidates = [h for h in preset["harnesses"] if h != "opencode"]
    assert not candidates, "unexpected additional advertised harness; inspect before trying a paid Create"
    save("create", "harness-capability", {"advertised": preset["harnesses"],
                                          "disposition": "capability-blocked: no advertised unavailable harness in owned OpenCode stack",
                                          "source": "public GET /v1/presets/lead; presetOut intersects with wired harnesses"})


def create_success():
    create_api("create", "lead")
    assert [a["agent_id"] for a in listed_create()] == [agent_id("create")]
    before = load("create", "baseline")["inventory"]
    after = native("inventory")
    new_worktrees = set(after["worktrees"]) - set(before["worktrees"])
    assert len(new_worktrees) == 1 and after["agents"] == before["agents"] + 1 and after["sessions"] == before["sessions"] + 1, "Create did not own exactly one Agent, worktree and native registration"
    save("create", "success", {"agent_id": agent_id("create"), "new_worktree": next(iter(new_worktrees)),
                               "native_ref": load("create", "identity")["native_ref"]})


def create_answer():
    text = f"CREATE_RETRY-{RUN}: read README.md with a tool and name its documented test command with a file citation."
    evs = events("create")
    row = delivered(evs, text)
    completed = completed_turn(evs, row)
    assert agent("create")["state"] == "idle" and not agent("create")["waiting_messages"]
    assert native("count", "create", row["payload"]["inputKey"])["native_user_message_count"] == 1
    save("create", "answer", {"delivered_event_id": row["event_id"], "completed_event_id": completed["event_id"], "input_key": row["payload"]["inputKey"],
                              "event_ids": event_ids("create")})


def create_reload():
    earlier = load("create", "answer")["event_ids"]
    later = event_ids("create")
    assert earlier == later and len(listed_create()) == 1


def cleanup():
    for label in ("interrupt", "large", "create"):
        file = OUT / f"{label}.id"
        if not file.exists():
            continue
        a = agent(label)
        if not a.get("archived_at"):
            assert call(agent_path(label) + "/archive", "POST", {"reason": "cancelled"},
                        f"rs-edges-{RUN}-{label}-archive", 204) is None


if __name__ == "__main__":
    commands = {"preflight": preflight, "claim-interrupt": claim_interrupt,
                "create-api": create_api, "open-chat": open_chat, "running": running,
                "queue-interrupt": queue_interrupt, "replace-interrupt": replace_interrupt,
                "interrupt-after": interrupt_after, "interrupt-next": interrupt_next,
                "ask-baseline": ask_baseline, "large-send": large_send, "large-over": large_over,
                "withdraw-large": withdraw_large, "retry-over-key": retry_over_key,
                "large-after": large_after, "create-baseline": create_baseline,
                "create-refuse": create_refuse, "create-harness-capability": create_harness_capability,
                "create-success": create_success, "create-answer": create_answer,
                "create-reload": create_reload, "cleanup": cleanup}
    commands[sys.argv[1]](*sys.argv[2:])
