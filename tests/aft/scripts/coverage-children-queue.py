#!/usr/bin/env python3
"""Saved-receipt oracles for one real two-sender child queue journey."""

import json
import os
from pathlib import Path
import re
import subprocess
import sys
from urllib.parse import quote, urlencode
from urllib.request import Request, urlopen


RUN = os.environ.get("RUN_ID", "offline")
OUT = Path(os.environ.get("AFT_WORK_DIR", "/tmp")) / "coverage-children-queue"
ROOT = f"{os.environ.get('AFT_API_URL', '').rstrip('/')}/api/workspaces/{quote(os.environ.get('AFT_WS', 'LOCALMODE'))}/v1/agents"
LEAD_NAME = f"cov-child-queue-lead-{RUN}"
CHILD_NAME = f"cov-child-queue-task-{RUN}"
TEXT = {
    "u1": f"QUEUE-U1-{RUN}: after the current task, name its entry point.",
    "p1": f"QUEUE-P1-{RUN}: after the current task, name its test command.",
    "u2": f"QUEUE-U2-{RUN}: after the current task, name its entry point and one handler.",
    "p2": f"QUEUE-P2-{RUN}: inspect the request path in internal/loomagent/send.go, dispatch.go, events.go, get.go, and the Agent v1 handler; report file citations. Do not edit files or run tests.",
    "p3": f"QUEUE-P3-{RUN}: after the current task, name the slot ordering rule.",
    "u3": f"QUEUE-U3-{RUN}: interrupt the current task; first explain how a user message reaches a saved receipt, then name the slot ordering rule.",
}


def demand(ok, reason):
    if not ok:
        raise AssertionError(reason)


def save(name, value):
    OUT.mkdir(parents=True, exist_ok=True)
    (OUT / f"{name}.json").write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def load(name):
    return json.loads((OUT / f"{name}.json").read_text())


def identity(label):
    value = (OUT / f"{label}.id").read_text().strip()
    demand(re.fullmatch(r"agt_[A-Za-z0-9_-]+", value), f"invalid {label} ID")
    return value


def http(url, body=None, request_id=None):
    headers = {"Accept": "application/json"}
    data = None
    if body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
        headers["Idempotency-Key"] = request_id
    with urlopen(Request(url, data=data, headers=headers, method="POST" if data is not None else "GET"), timeout=20) as response:
        if data is not None:
            demand(response.status == 202, "public user Send was not accepted")
        return json.load(response)


def agent(label):
    return http(f"{ROOT}/{quote(identity(label))}")


def events(label):
    aid = identity(label)
    rows, after, snapshot = [], 0, None
    while True:
        query = {"after": after, "limit": 100}
        if snapshot is not None:
            query["snapshot"] = snapshot
        page = http(f"{ROOT}/{quote(aid)}/events?{urlencode(query)}")
        snapshot = page["snapshot_seq"] if snapshot is None else snapshot
        demand(page["snapshot_seq"] == snapshot, "event snapshot changed")
        rows.extend(page["events"])
        if not page["more"]:
            break
        demand(page["next"] > after and page["events"], "event page did not advance")
        after = page["next"]
    demand(all(e["agent_id"] == aid for e in rows), "foreign agent event")
    seqs = [e["seq"] for e in rows]
    demand(seqs == sorted(set(seqs)), "unordered or duplicate event seq")
    demand(len({e["event_id"] for e in rows}) == len(rows), "duplicate event ID")
    return rows


def browser(expr):
    raw = subprocess.check_output(["agent-browser", "--session", os.environ["AFT_SESSION"], "eval", expr], text=True).strip()
    try:
        return json.loads(raw)
    except json.JSONDecodeError:
        return raw.strip('"')


def bind_lead():
    route = browser("location.pathname")
    match = re.fullmatch(rf"/ws/{re.escape(os.environ['AFT_WS'])}/chat/(agt_[A-Za-z0-9_-]+)", route)
    demand(match, "UI did not open its Lead Chat")
    OUT.mkdir(parents=True, exist_ok=True)
    (OUT / "lead.id").write_text(match[1] + "\n")
    row = agent("lead")
    demand(row["agent_id"] == match[1] and row["name"] == LEAD_NAME and row["preset"] == "lead", "wrong UI Lead")
    demand(row["created_by_kind"] == "user" and row["parent_agent_id"] is None, "Lead not created by user")
    demand(row["repo"] == os.environ["AFT_AGENT_FLOW_REPO"] and row["harness"] == "opencode", "wrong Lead backend/repo")
    demand(row["model"] == os.environ["AFT_REAL_MODEL"], "Lead model selection not saved")
    save("lead", row)


def tool_code(tool):
    raw = tool.get("input")
    if isinstance(raw, str):
        try:
            raw = json.loads(raw)
        except json.JSONDecodeError:
            return raw
    return raw.get("code", "") if isinstance(raw, dict) else ""


def actual_call(event, operation, target, marker):
    if event["kind"] != "item.completed" or event["payload"].get("itemKind") != "tool":
        return False
    tool = event["payload"].get("tool") or {}
    name = tool.get("name", "")
    raw = tool.get("input")
    if name.endswith(operation):
        text = raw if isinstance(raw, str) else json.dumps(raw or {})
    else:
        code = tool_code(tool)
        if not re.search(r"\btools\.loom\." + re.escape(operation) + r"\s*\(", code):
            return False
        text = code
    return target in text and marker in text


def one_call(operation, marker):
    rows = [e for e in events("lead") if actual_call(e, operation, identity("child") if operation == "agent_send" else CHILD_NAME, marker)]
    demand(len(rows) == 1, f"expected one completed native {operation} call for {marker}")
    return rows[0]


def bind_child():
    lead = agent("lead")
    page = http(ROOT + "?" + urlencode({"parent": lead["agent_id"], "include_archived": "true", "limit": 500}))
    demand(not page.get("next"), "child listing truncated")
    found = [r for r in page["agents"] if r["name"] == CHILD_NAME]
    demand(len(found) == 1, "real Lead did not create exactly one named child")
    child = found[0]
    OUT.mkdir(parents=True, exist_ok=True)
    (OUT / "child.id").write_text(child["agent_id"] + "\n")
    child = agent("child")
    demand(child["preset"] == "task" and child["created_by_kind"] == "agent", "child is not a task created by an agent")
    demand(child["parent_agent_id"] == child["root_agent_id"] == child["created_by_id"] == lead["agent_id"], "wrong parent")
    demand(child["repo"] == lead["repo"] == os.environ["AFT_AGENT_FLOW_REPO"], "wrong child repo")
    demand(child["worktree_path"] and child["worktree_path"] != lead["worktree_path"], "child lacks distinct worktree")
    demand(child["base_ref"] == lead["branch"] and child["harness"] == "opencode", "child ref/backend mismatch")
    created = [e for e in events("lead") if e["kind"] == "child.created" and e["payload"].get("child") == child["agent_id"]]
    demand(len(created) == 1, "missing exact saved child.created")
    one_call("agent_create", CHILD_NAME)
    save("child", child)


def capture(label):
    row = agent("child")
    safe_events = []
    for e in events("child"):
        record = {k: e[k] for k in ("agent_id", "seq", "event_id", "kind", "turn_id") if k in e}
        if e["kind"] == "message.delivered":
            record["payload"] = {k: e["payload"][k] for k in ("text", "sender", "inputKey") if k in e["payload"]}
        elif e["kind"] == "message.waiting":
            record["payload"] = {"reason": e["payload"].get("reason")}
        safe_events.append(record)
    save(label, {"agent_id": row["agent_id"], "state": row["state"], "turn": row["running_turn_id"],
                 "attempt": row["attempt"], "waiting": row["waiting_messages"], "events": safe_events})


def active(row):
    demand(row["agent_id"] == identity("child") and row["state"] == "active" and row["running_turn_id"], "exact child turn is not running")


def receipt_send(stage):
    demand(stage in ("u1", "u2", "u3"), "unknown public user send")
    before = agent("child")
    active(before)
    request_id = f"coverage-children-queue-{RUN}-{stage}"
    body = {"text": TEXT[stage]}
    if stage == "u3":
        body["delivery"] = "interrupt"
    result = http(f"{ROOT}/{quote(identity('child'))}/messages", body, request_id)
    expected_states = ("waiting", "handed") if stage == "u3" else ("waiting",)
    demand(result.get("message_id") and result.get("state") in expected_states, f"{stage} was not accepted during a busy turn")
    demand(result.get("replaced") is (stage == "u2"), f"{stage} replacement receipt mismatch")
    if stage == "u3":
        demand(result.get("interrupted") is True, "user interrupt did not stop a running turn")
    else:
        demand("interrupted" not in result, "ordinary queue Send unexpectedly interrupted")
    save(f"receipt-{stage}", {"request_id": request_id, "body": body, "result": result,
                               "before_turn": before["running_turn_id"], "actor": "public user API; server-derived identity"})


def waiting_event(rows, stage, sender):
    request_id = load(f"receipt-{stage}")["request_id"] if stage.startswith("u") else None
    found = [e for e in rows if e["kind"] == "message.waiting" and
             (e["event_id"] == f"{identity('child')}:send:{request_id}:message.waiting" if request_id else
              e["payload"].get("reason") == sender)]
    if not request_id:
        # Native agent_send owns an opaque RequestID. Bound its waiting event
        # to the completed Lead call and the exact live sender slot below.
        found = [e for e in found if e["payload"].get("reason") == sender]
    demand(len(found) >= 1, f"missing saved waiting event {stage}")
    return found[-1]


def sender_pair(waiting, user_text, parent_text, lead_id):
    demand(len(waiting) == 2, "expected exactly two sender slots")
    user, parent = waiting
    demand(user["sender"].startswith("user:") and user["text"] == user_text and user["since"], "wrong user slot or FIFO position")
    demand(parent["sender"] == f"agent:{lead_id}" and parent["text"] == parent_text and parent["since"], "wrong real parent slot or FIFO position")
    return user["sender"], parent["sender"]


def fifo_before():
    row = agent("child")
    active(row)
    demand(len(row["waiting_messages"]) == 1 and row["waiting_messages"][0]["text"] == TEXT["u1"], "first user slot missing")
    demand(row["waiting_messages"][0]["sender"].startswith("user:"), "wrong first sender")
    capture("fifo-before")


def fifo_parent():
    one_call("agent_send", "QUEUE-P1-")
    row = agent("child")
    active(row)
    user, parent = sender_pair(row["waiting_messages"], TEXT["u1"], TEXT["p1"], identity("lead"))
    demand(row["running_turn_id"] == load("fifo-before")["turn"], "child turn changed before second sender queued")
    capture("fifo-parent")


def fifo_replaced():
    row = agent("child")
    active(row)
    user, parent = sender_pair(row["waiting_messages"], TEXT["u2"], TEXT["p1"], identity("lead"))
    before = load("fifo-parent")
    demand(row["running_turn_id"] == before["turn"], "child turn changed before replacement")
    demand(row["waiting_messages"][0]["since"] == before["waiting"][0]["since"], "replacement lost original FIFO place")
    demand(row["waiting_messages"][1]["since"] == before["waiting"][1]["since"], "parent slot changed")
    demand(load("receipt-u1")["result"]["message_id"] != load("receipt-u2")["result"]["message_id"], "replacement reused the prior message ID")
    ev = events("child")
    u1 = waiting_event(ev, "u1", user)
    u2 = waiting_event(ev, "u2", user)
    p1 = waiting_event(ev, "p1", parent)
    demand(u1["seq"] < p1["seq"] < u2["seq"], "saved enqueue order is not user, parent, replacement")
    demand(u1["payload"].get("reason") == u2["payload"].get("reason") == user, "replacement changed sender")
    save("fifo-proof", {"sender_user": user, "sender_parent": parent, "waiting_ids": [u1["event_id"], p1["event_id"], u2["event_id"]],
                        "seq": [u1["seq"], p1["seq"], u2["seq"]], "turn": row["running_turn_id"]})
    capture("fifo-replaced")


def delivered(rows, text, sender):
    found = [e for e in rows if e["kind"] == "message.delivered" and e["payload"].get("text") == text]
    demand(len(found) == 1, f"expected exactly one saved native delivery for {text}")
    event = found[0]
    demand(event["payload"].get("sender") == sender and event["payload"].get("inputKey"), "delivery sender/input key mismatch")
    return event


def delivery_pair(rows, first_text, first_sender, second_text, second_sender, absent=()):
    seqs = [e["seq"] for e in rows]
    demand(seqs == sorted(set(seqs)), "unordered or duplicate saved event sequence")
    first = delivered(rows, first_text, first_sender)
    second = delivered(rows, second_text, second_sender)
    demand(first["seq"] < second["seq"], "handover order mismatch")
    demand(first["payload"]["inputKey"] != second["payload"]["inputKey"], "duplicate native input key")
    demand(all(not [e for e in rows if e["kind"] == "message.delivered" and e["payload"].get("text") == text] for text in absent), "superseded text was delivered")
    return [{"event_id": e["event_id"], "seq": e["seq"], "input_key": e["payload"]["inputKey"]} for e in (first, second)]


def fifo_done():
    proof = load("fifo-proof")
    rows = events("child")
    pair = delivery_pair(rows, TEXT["u2"], proof["sender_user"], TEXT["p1"], proof["sender_parent"], (TEXT["u1"],))
    demand(not agent("child")["waiting_messages"], "FIFO slots remain after handover")
    save("fifo-delivery", pair)
    capture("fifo-done")


def parent_started():
    one_call("agent_send", "QUEUE-P2-")
    row = agent("child")
    active(row)
    demand(row["attempt"] > load("fifo-done")["attempt"], "same task child did not reopen")
    proof = load("fifo-proof")
    delivered(events("child"), TEXT["p2"], proof["sender_parent"])
    demand(not row["waiting_messages"], "new attempt had unexpected queued slots")
    capture("parent-started")


def first_parent():
    one_call("agent_send", "QUEUE-P3-")
    row = agent("child")
    active(row)
    demand(row["running_turn_id"] == load("parent-started")["turn"], "parent slot missed busy child turn")
    demand(len(row["waiting_messages"]) == 1 and row["waiting_messages"][0]["sender"] == f"agent:{identity('lead')}" and row["waiting_messages"][0]["text"] == TEXT["p3"], "older real parent slot missing")
    ev = events("child")
    p3 = waiting_event(ev, "p3", f"agent:{identity('lead')}")
    save("first-parent-proof", {"event_id": p3["event_id"], "seq": p3["seq"], "turn": row["running_turn_id"]})
    capture("first-parent")


def first_done():
    rows = events("child")
    proof = load("first-parent-proof")
    user = load("fifo-proof")["sender_user"]
    u3 = waiting_event(rows, "u3", user)
    demand(proof["seq"] < u3["seq"], "interrupt was not later than the parent slot")
    ids = [load(f"receipt-{stage}")["result"]["message_id"] for stage in ("u1", "u2", "u3")]
    demand(len(set(ids)) == 3, "public user Send receipts reused a message ID")
    pair = delivery_pair(rows, TEXT["u3"], user, TEXT["p3"], f"agent:{identity('lead')}")
    demand(not agent("child")["waiting_messages"], "First phase still has waiting slots")
    save("first-delivery", {"interrupt_receipt": load("receipt-u3"), "older_parent_waiting": proof, "later_user_waiting": u3["event_id"], "delivered": pair})
    capture("first-done")


def shot(name, required):
    demand(re.fullmatch(r"[a-z][a-z0-9-]+", name), "unsafe screenshot name")
    route = browser("location.pathname")
    demand(route == f"/ws/{os.environ['AFT_WS']}/chat/{identity('child')}", "screenshot is not exact child Chat")
    expr = "(() => { const t=document.querySelector('[data-testid=chat-transcript]'); return !!t && " + json.dumps(required) + ".every(x=>t.textContent.includes(x)); })()"
    demand(browser(expr) is True, "child Chat does not visibly contain required message markers")
    subprocess.run(["agent-browser", "--session", os.environ["AFT_SESSION"], "screenshot", str(OUT / f"{name}.png")], check=True)
    save("shot-" + name, {"child": identity("child"), "route": route, "required": required, "file": name + ".png"})


def cleanup():
    # Keep all owned agents and artifacts for the coordinator's paid-run review.
    pass


def main():
    cmd = sys.argv[1]
    if cmd == "bind-lead": bind_lead()
    elif cmd == "bind-child": bind_child()
    elif cmd == "send": receipt_send(sys.argv[2])
    elif cmd == "fifo-before": fifo_before()
    elif cmd == "fifo-parent": fifo_parent()
    elif cmd == "fifo-replaced": fifo_replaced()
    elif cmd == "fifo-done": fifo_done()
    elif cmd == "parent-started": parent_started()
    elif cmd == "first-parent": first_parent()
    elif cmd == "first-done": first_done()
    elif cmd == "shot": shot(sys.argv[2], sys.argv[3:])
    elif cmd == "cleanup": cleanup()
    else: raise SystemExit(f"unknown command: {cmd}")


if __name__ == "__main__":
    main()
