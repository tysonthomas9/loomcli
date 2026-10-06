#!/usr/bin/env python3
"""Read-only oracles for run-owned real child activity journeys."""

import json
import os
from pathlib import Path
import re
import subprocess
import sys
from urllib.parse import quote
from urllib.request import urlopen


RUN = os.environ["RUN_ID"]
WS = os.environ["AFT_WS"]
ROOT = f"{os.environ['AFT_API_URL'].rstrip('/')}/api/workspaces/{quote(WS)}/v1/agents"
OUT = Path(os.environ["AFT_WORK_DIR"]) / "coverage-children-activity"
OUT.mkdir(parents=True, exist_ok=True)


def get(url):
    with urlopen(url, timeout=15) as response:
        return json.load(response)


def save(name, data):
    (OUT / f"{name}.json").write_text(json.dumps(data, indent=2) + "\n")


def load(name):
    return json.loads((OUT / f"{name}.json").read_text())


def listed(parent=None):
    query = "?include_archived=true&limit=500"
    if parent:
        query += f"&parent={quote(parent)}"
    page = get(ROOT + query)
    assert not page.get("next"), "agent list truncated"
    return page["agents"]


def agent(agent_id):
    return get(f"{ROOT}/{quote(agent_id)}")


def events(agent_id):
    page = get(f"{ROOT}/{quote(agent_id)}/events?limit=500")
    assert not page.get("more"), "agent event history truncated"
    return page["events"]


def owned(row, name, parent=None):
    assert row["name"] == name and name.endswith(f"-{RUN}") and name.startswith("cov-child-")
    assert row["repo"] == os.environ["AFT_AGENT_FLOW_REPO"]
    assert row["harness"] == os.environ["AFT_REAL_BACKEND"] == "opencode"
    assert row["worktree_path"]
    if parent:
        assert row["preset"] == "task" and row["created_by_kind"] == "agent"
        assert row["parent_agent_id"] == row["root_agent_id"] == parent["agent_id"]
        assert row["created_by_id"] == parent["agent_id"]
        assert row["worktree_path"] != parent["worktree_path"]
        assert row["base_ref"] == parent["branch"]
    else:
        assert row["preset"] == "lead" and not row["parent_agent_id"]
        assert row["model"] == os.environ["AFT_REAL_MODEL"]


def bind(label, name, parent_label=""):
    parent = load(parent_label) if parent_label else None
    rows = listed(parent["agent_id"] if parent else None)
    matches = [row for row in rows if row["name"] == name]
    assert len(matches) == 1, f"expected exactly one {name}"
    row = agent(matches[0]["agent_id"])
    owned(row, name, parent)
    save(label, row)
    (OUT / f"{label}.id").write_text(row["agent_id"] + "\n")
    if parent:
        created = [e for e in events(parent["agent_id"])
                   if e["kind"] == "child.created" and e["payload"].get("child") == row["agent_id"]]
        assert len(created) == 1, "child was not created by the Lead tool"
        tools = [e for e in events(parent["agent_id"]) if calls_operation(e, "agent_create")]
        assert any(name in str(e["payload"]["tool"].get("input", "")) for e in tools)
    print(row["agent_id"])


def tool_name(event):
    p = event["payload"]
    if event["kind"] != "item.completed" or p.get("itemKind") != "tool":
        return None
    name = p.get("tool", {}).get("name", "")
    for tool in ("agent_create", "agent_send", "agent_get", "agent_archive"):
        if name.endswith(tool):
            return tool
        if re.search(r"\btools\.loom\." + tool + r"\s*\(", str(p.get("tool", {}).get("input", ""))):
            return tool
    return name


def calls_operation(event, operation):
    if event["kind"] != "item.completed" or event["payload"].get("itemKind") != "tool":
        return False
    tool = event["payload"].get("tool") or {}
    return tool.get("name", "").endswith(operation) or bool(re.search(
        r"\btools\.loom\." + re.escape(operation) + r"\s*\(", str(tool.get("input", ""))))


def reply_text(event):
    p = event["payload"]
    if event["kind"] == "item.completed" and p.get("itemKind") == "message":
        return p.get("text") or p.get("message") or ""
    return ""


def completions(ev, child_id):
    prefix = f"task_completed:{child_id}:"
    return [e for e in ev if e["kind"] == "task_completed" and e["event_id"].startswith(prefix)]


def deliveries(ev, child_id, attempt):
    key = {"child": child_id, "attempt": attempt}
    return [e for e in ev if e["kind"] == "message.delivered" and
            key in e["payload"].get("completions", [])]


def completion(lead_label, child_label, attempt, snapshot):
    lead, child = load(lead_label), load(child_label)
    live = agent(child["agent_id"])
    assert live["state"] == "finished" and live["outcome"] == "completed"
    ev = events(lead["agent_id"])
    rec = [e for e in completions(ev, child["agent_id"])
           if e["event_id"] == f"task_completed:{child['agent_id']}:{attempt}"]
    delivered = deliveries(ev, child["agent_id"], attempt)
    assert len(rec) == len(delivered) == 1, (rec, delivered)
    assert rec[0]["payload"]["outcome"] == "completed"
    assert rec[0]["payload"].get("summary")
    assert any(reply_text(e) for e in events(child["agent_id"])), "child has no saved answer"
    proof = {"child": child["agent_id"], "attempt": attempt,
             "record_id": rec[0]["event_id"], "record_seq": rec[0]["seq"],
             "delivery_id": delivered[0]["event_id"], "delivery_seq": delivered[0]["seq"],
             "branch": rec[0]["payload"].get("branch"), "head": rec[0]["payload"].get("head")}
    save(snapshot, proof)
    return proof, ev


def first(lead_label, child_label):
    proof, ev = completion(lead_label, child_label, 0, "first")
    assert len(completions(ev, proof["child"])) == 1
    assert not [e for e in ev if calls_operation(e, "agent_get")], "Lead refetched child result"
    replies = [e for e in ev if e["seq"] > proof["delivery_seq"] and reply_text(e)]
    assert len(replies) == 1, "expected one brief first acknowledgement"
    save("first-reply", {"event_id": replies[0]["event_id"], "seq": replies[0]["seq"]})


def repeat_ready(lead_label, child_label):
    lead, child = load(lead_label), load(child_label)
    assert agent(lead["agent_id"])["state"] == "idle"
    current = agent(child["agent_id"])
    assert current["state"] == "finished" and current["attempt"] == 0
    assert load("first")["child"] == child["agent_id"]


def activity(child_label):
    child = load(child_label)
    current = agent(child["agent_id"])
    assert current["state"] in ("active", "waiting") and current["running_turn_id"]
    steps = [e for e in events(child["agent_id"]) if
             e["kind"] == "item.completed" and e["payload"].get("itemKind") in ("tool", "reasoning")]
    assert steps, "no saved real tool or reasoning step for the live tray preview"
    last = steps[-1]
    save("activity", {"child": child["agent_id"], "event_id": last["event_id"],
                      "turn_id": last.get("turn_id"), "item_kind": last["payload"]["itemKind"],
                      "tool_name": last["payload"].get("tool", {}).get("name")})


def repeated(lead_label, child_label):
    prior = load("first")
    second, ev = completion(lead_label, child_label, 1, "second")
    child = agent(second["child"])
    assert child["attempt"] == 1
    records = completions(ev, second["child"])
    assert sorted(e["event_id"] for e in records) == sorted([prior["record_id"], second["record_id"]])
    assert len(deliveries(ev, second["child"], 0)) == len(deliveries(ev, second["child"], 1)) == 1
    assert not [e for e in ev if calls_operation(e, "agent_get")], "Lead refetched a completed attempt"
    sends = [e for e in ev if calls_operation(e, "agent_send") and
             second["child"] in str(e["payload"].get("tool", {}).get("input", ""))]
    assert len(sends) == 1 and not sends[0]["payload"]["tool"].get("failed"), "Lead did not reopen its child"
    first_reply = load("first-reply")["seq"]
    assert prior["delivery_seq"] < first_reply < second["delivery_seq"]
    after = [e for e in ev if e["seq"] > second["delivery_seq"] and reply_text(e)]
    assert len(after) == 1 and all(x in reply_text(after[0]) for x in ("first", "second")), "expected one final combined summary"
    ref_helper = Path(os.environ["AFT_TESTS_DIR"]) / "scripts/coverage-children-activity-ref.sh"
    actual = json.loads(subprocess.check_output(["bash", str(ref_helper), second["child"]], text=True))
    branch, head, changed = actual["branch"], actual["head"], actual["changed"]
    assert branch == f"cov-child-switched-{RUN}" and re.fullmatch(r"[0-9a-f]{40}", head)
    assert changed == [f"aft-child-fixtures/{RUN}/second.txt"], changed
    assert second["branch"] and not second["head"], "CL3 must report saved branch without an unowned head"
    save("repeat-proof", {"keys": [prior["record_id"], second["record_id"]],
                          "first_reply": load("first-reply"), "final_reply": after[0]["event_id"],
                          "actual_branch": branch, "actual_head": head, "second_record": second})


def busy(lead_label, first_label, second_label):
    lead = agent(load(lead_label)["agent_id"])
    kids = [agent(load(label)["agent_id"]) for label in (first_label, second_label)]
    assert lead["running_turn_id"] and lead["state"] == "active"
    assert all(k["running_turn_id"] and k["state"] == "active" for k in kids)
    lead_events = events(lead["agent_id"])
    save("busy", {"lead_turn": lead["running_turn_id"], "children": [k["agent_id"] for k in kids],
                  "last_lead_seq": max(e["seq"] for e in lead_events)})


def pair(lead_label, first_label, second_label):
    lead, busy_at = load(lead_label), load("busy")
    ids = [load(first_label)["agent_id"], load(second_label)["agent_id"]]
    assert ids == busy_at["children"]
    ev = events(lead["agent_id"])
    records = [completions(ev, child) for child in ids]
    assert all(len(rows) == 1 and rows[0]["payload"]["outcome"] == "completed" for rows in records)
    busy_end = [e for e in ev if e["kind"] == "agent.turn_completed" and
                e.get("turn_id") == busy_at["lead_turn"]]
    assert len(busy_end) == 1, "saved busy Lead turn did not complete"
    assert all(busy_at["last_lead_seq"] < rows[0]["seq"] < busy_end[0]["seq"]
               for rows in records), "children did not both finish while the Lead was busy"
    delivered = [e for e in ev if e["kind"] == "message.delivered" and e["payload"].get("completions")]
    for index, child in enumerate(ids):
        assert len(deliveries(ev, child, 0)) == 1
        answer = events(child)
        assert any(reply_text(e) for e in answer) and any(e["kind"] == "agent.turn_completed" for e in answer)
        row = agent(child)
        commits_root = f"{os.environ['AFT_API_URL'].rstrip('/')}/api/workspaces/{quote(WS)}/agents/{quote(child)}/diff"
        commits = get(f"{commits_root}/commits?from={quote(row['base_ref'])}")["data"]["commits"]
        files = get(f"{commits_root}/files?from={quote(row['base_ref'])}&to=HEAD")["data"]["files"]
        expected_path = f"aft-child-fixtures/{RUN}/pair-{'a' if index == 0 else 'b'}.txt"
        assert records[index][0]["payload"].get("head") and any(
            c["hash"] == records[index][0]["payload"]["head"] for c in commits)
        assert [f["path"] for f in files] == [expected_path], files
        save(f"pair-commit-{child}", {"commits": commits, "files": files})
    grouped = [e for e in delivered if all({"child": child, "attempt": 0} in e["payload"]["completions"] for child in ids)]
    assert len(grouped) == 1, "two child facts were not delivered in one saved handover"
    assert all(records[i][0]["seq"] < grouped[0]["seq"] for i in (0, 1))
    assert not [e for e in ev if calls_operation(e, "agent_get")]
    replies = [e for e in ev if e["seq"] > grouped[0]["seq"] and reply_text(e)]
    assert len(replies) == 1 and all(load(label)["name"] in reply_text(replies[0]) for label in (first_label, second_label))
    save("pair-proof", {"children": ids, "record_ids": [rows[0]["event_id"] for rows in records],
                        "delivery_id": grouped[0]["event_id"], "final_reply_id": replies[0]["event_id"],
                        "busy_lead_turn": busy_at["lead_turn"]})


def roster(*labels):
    rows = listed()
    ids = [load(label)["agent_id"] for label in labels]
    for agent_id in ids:
        assert sum(row["agent_id"] == agent_id for row in rows) == 1
    save("roster", {"ids": ids, "rows": [{"agent_id": row["agent_id"], "name": row["name"],
                                             "state": row["state"], "parent_agent_id": row.get("parent_agent_id")}
                                            for row in rows]})


def home_expected():
    rows = listed()
    live = {a["agent_id"]: a for a in rows if a["state"] != "archived" and not a.get("deleted_at")}
    working = {"creating", "active", "waiting", "stopping"}
    shown = [a for a in live.values() if not a.get("parent_agent_id") or a["parent_agent_id"] not in live or
             a["state"] in working]
    api_idle = sum(a["state"] not in working for a in shown)
    legacy = get(f"{os.environ['AFT_API_URL'].rstrip('/')}/api/monitor/agents?workspace={quote(WS)}").get("agents") or []
    active_legacy = [a for a in legacy if a.get("status") in ("working", "planning") or
                     a.get("active_task_id") or a.get("current_task_id")]
    assert not active_legacy, "Home count requires a quiescent legacy roster"
    expected = api_idle + len(legacy)
    save("home-count", {"api_idle_ids": [a["agent_id"] for a in shown if a["state"] not in working],
                        "legacy_count": len(legacy), "expected": expected})
    print(expected)


def cleanup():
    for path in OUT.glob("*.id"):
        row = agent(path.read_text().strip())
        assert row["name"].startswith("cov-child-") and row["name"].endswith(f"-{RUN}")
        save(f"final-{row['agent_id']}", {"agent_id": row["agent_id"], "state": row["state"],
                                         "event_ids": [e["event_id"] for e in events(row["agent_id"])]})


if __name__ == "__main__":
    globals()[sys.argv[1]](*sys.argv[2:])
