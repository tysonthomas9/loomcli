#!/usr/bin/env python3
"""Saved-receipt oracles for one real two-sender child queue journey."""

import hashlib
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
    "p2": f"QUEUE-P2-{RUN}: inspect README.md, BACKLOG.md, package.json, server.js, app.js, and app.test.js; trace valid POST, invalid POST, and GET /api/channels/general/messages with file citations; run node --test once; do not edit files.",
    "p3": f"QUEUE-P3-{RUN}: after the current task, name the slot ordering rule.",
    "p3b": f"QUEUE-P3B-{RUN}: after the current task, name the slot ordering rule you observed; also cite app.js for how fixture messages are appended.",
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
    ref = native_ref("lead", row["agent_id"])
    demand(ref["branch"] == row["branch"], "owned Lead branch differs from API")
    save("lead-pre-create-ref", {**ref, "repo": row["repo"], "worktree_path": row["worktree_path"]})


def native_ref(kind, agent_id, parent_head=None):
    helper = Path(os.environ["AFT_TESTS_DIR"]) / "scripts/coverage-children-queue-ref.sh"
    args = ["bash", str(helper), kind, agent_id]
    if parent_head:
        args.append(parent_head)
    ref = json.loads(subprocess.check_output(args, text=True))
    demand(ref["agent_id"] == agent_id and ref["kind"] == kind and
           re.fullmatch(r"[a-f0-9]{40}", ref["head"]), "owned native ref proof mismatch")
    return ref


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


def create_call_diagnostic(source, lead, child, created):
    demand(lead["agent_id"] == identity("lead") and child["agent_id"] == identity("child") and
           child["parent_agent_id"] == lead["agent_id"] and
           all(e["agent_id"] == lead["agent_id"] for e in source + created),
           "foreign create-call diagnostic identity")
    completed, pending = [], []
    other_tools = 0
    for event in source:
        if event["kind"] not in ("item.completed", "tool.started", "item.started") or event["payload"].get("itemKind") != "tool":
            continue
        tool = event["payload"].get("tool") or {}
        name = tool.get("name") or ""
        raw = tool.get("input")
        input_text = raw if isinstance(raw, str) else json.dumps(raw or {})
        code = tool_code(tool)
        direct = name.endswith("agent_create")
        operation_match = direct or bool(re.search(r"\btools\.loom\.agent_create\s*\(", code))
        target_match = CHILD_NAME in (input_text if direct else code)
        truncated = isinstance(raw, str) and raw.endswith("…")
        matched = actual_call(event, "agent_create", CHILD_NAME, CHILD_NAME)
        relevant = operation_match or target_match or (truncated and name.endswith("execute") and "loom" in input_text)
        if not relevant:
            other_tools += 1
            continue
        done = event["kind"] == "item.completed"
        (completed if done else pending).append({
            "event_id": event["event_id"], "seq": event["seq"], "turn_id": event.get("turn_id"),
            "kind": event["kind"], "status": "failed" if done and tool.get("failed") else "completed" if done else "pending",
            "failed": bool(tool.get("failed")) if done else "unknown",
            "operation_match": operation_match, "target_match": target_match, "matcher_match": matched,
            "input_truncated": truncated, "input_bytes": len(input_text.encode()),
        })
    save("create-call-diagnostic", {
        "lead_agent_id": lead["agent_id"], "child_agent_id": child["agent_id"],
        "child_created_event_ids": [e["event_id"] for e in created],
        "lead_state": lead["state"], "lead_running_turn_id": lead.get("running_turn_id"),
        "completed_candidates": completed, "pending_candidates": pending,
        "matching_completed_count": sum(e["matcher_match"] for e in completed),
        "other_tool_event_count": other_tools,
        "pending_visibility": "saved events only; live tool starts may be absent",
    })


def one_call(operation, marker, create_context=None):
    source = events("lead")
    rows = [e for e in source if actual_call(e, operation, identity("child") if operation == "agent_send" else CHILD_NAME, marker)]
    if create_context is not None:
        create_call_diagnostic(source, *create_context)
    demand(len(rows) == 1, f"expected one completed native {operation} call for {marker}")
    return rows[0]


def settled_send_turn(rows, lead_id, child_id, marker):
    calls = [e for e in rows if e.get("agent_id") == lead_id and
             actual_call(e, "agent_send", child_id, marker)]
    demand(len(calls) == 1, f"missing or duplicate saved native send for {marker}")
    call = calls[0]
    tool = call["payload"].get("tool") or {}
    demand(not tool.get("failed") and isinstance(tool.get("output"), str) and tool["output"].strip(),
           f"native send failed or lacks a receipt for {marker}")
    turn = call.get("turn_id")
    demand(bool(turn), f"native send lacks a Lead turn for {marker}")
    ends = [e for e in rows if e.get("agent_id") == lead_id and e["kind"] == "agent.turn_completed" and e.get("turn_id") == turn and
            e["seq"] > call["seq"] and e["payload"].get("stopReason") not in ("failed", "cancelled") and
            not e["payload"].get("error")]
    demand(len(ends) == 1, f"saved Lead send turn did not complete for {marker}")
    replies = [e for e in rows if e.get("agent_id") == lead_id and e["kind"] == "item.completed" and e.get("turn_id") == turn and
               e["payload"].get("itemKind") == "message" and isinstance(e["payload"].get("text"), str) and
               e["payload"]["text"].strip() and call["seq"] < e["seq"] < ends[0]["seq"]]
    demand(replies, f"saved Lead send turn lacks a completed reply for {marker}")
    return call, ends[0], replies


def settled_lead_send(stage, marker):
    call, end, replies = settled_send_turn(events("lead"), identity("lead"), identity("child"), marker)
    save(f"lead-send-{stage}", {"child": identity("child"), "tool_event_id": call["event_id"],
                               "turn_id": call["turn_id"], "turn_end_event_id": end["event_id"],
                               "reply_event_ids": [e["event_id"] for e in replies],
                               "native_output_present": True})
    return call


def native_send_result(event, expected_replaced):
    demand(event["kind"] == "item.completed" and event["payload"].get("itemKind") == "tool", "native send is not a completed tool")
    tool = event["payload"].get("tool") or {}
    demand(not tool.get("failed"), "native send tool failed")
    output = tool.get("output")
    demand(isinstance(output, str) and output.strip(), "native send result missing")
    try:
        result = json.loads(output)
        if isinstance(result, str):
            result = json.loads(result)
    except (ValueError, TypeError) as exc:
        raise AssertionError("native send result is not JSON") from exc
    demand(isinstance(result, dict) and result.get("replaced") is expected_replaced,
           f"native send replaced result is not {expected_replaced}")
    demand(isinstance(result.get("message_id"), str) and result["message_id"], "native send message_id missing")
    demand(result.get("state") == "waiting", "native send did not preserve a waiting slot")
    return result


def replacement_result(first, second):
    demand(first["event_id"] != second["event_id"] and first.get("turn_id") and second.get("turn_id") and
           first["turn_id"] != second["turn_id"],
           "parent replacement must use two distinct native turns")
    initial = native_send_result(first, False)
    replacement = native_send_result(second, True)
    demand(initial["message_id"] != replacement["message_id"], "native replacement reused its message ID")
    return initial, replacement


def native_wait_request(wait, child_id, sender, result):
    demand(wait["kind"] == "message.waiting" and wait["agent_id"] == child_id and
           wait["payload"].get("reason") == sender, "native waiting event has wrong child or sender")
    prefix, suffix = f"{child_id}:send:", ":message.waiting"
    event_id = wait["event_id"]
    demand(event_id.startswith(prefix) and event_id.endswith(suffix), "native waiting event ID has wrong production form")
    request_id = event_id[len(prefix):-len(suffix)]
    demand(bool(re.fullmatch(r"agent_tool-[A-Z2-7]{26,}", request_id)), "native waiting event has invalid tool RequestID")
    expected = "msg_" + hashlib.sha256(f"{child_id}\0{sender}\0{request_id}".encode()).digest()[:13].hex()
    demand(result.get("message_id") == expected, "native send message ID differs from exact saved RequestID")
    return request_id


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
    prior = load("lead-pre-create-ref")
    child_ref = native_ref("child", child["agent_id"], prior["head"])
    demand(prior["branch"] == lead["branch"] and child_ref["branch"] == child["branch"], "actual worktree branch mismatch")
    demand(child_ref["merge_base"] == prior["head"], "owned child does not descend from exact pre-create parent HEAD")
    created = [e for e in events("lead") if e["kind"] == "child.created" and e["payload"].get("child") == child["agent_id"]]
    demand(len(created) == 1, "missing exact saved child.created")
    one_call("agent_create", CHILD_NAME, (lead, child, created))
    save("child", child)
    save("child-create-ref", {"parent_head_before": prior["head"], "parent_branch": prior["branch"],
                              "child_head_after": child_ref["head"], "child_branch": child_ref["branch"],
                              "merge_base": child_ref["merge_base"],
                              "child_base_ref": child["base_ref"], "parent_worktree": prior["worktree_path"],
                              "child_worktree": child["worktree_path"]})


def started_create_role(event):
    if event["kind"] != "item.completed" or event["payload"].get("itemKind") != "tool":
        return None
    tool = event["payload"].get("tool") or {}
    if tool.get("failed"):
        return None
    name = (tool.get("name") or "").strip()
    raw = tool.get("input") or ""
    raw = raw if isinstance(raw, str) else json.dumps(raw)
    try:
        parsed = json.loads(raw)
    except ValueError:
        parsed = None
    parsed = parsed if isinstance(parsed, dict) else None
    code = parsed.get("code", "") if parsed else ""
    code = code if isinstance(code, str) else ""
    if re.search(r"(?:^|[^a-z])agent_create$", name, re.I) or re.search(r"tools\.loom\.agent_create\s*\(", code):
        return "create"
    execute = re.search(r"(?:^|[^a-z])execute$", name, re.I)
    loom_marker = re.search(r"[\"'`]loom\b|\bloom\.", raw)
    if execute and loom_marker and (re.search(r"\bagent_create\b", raw) or
                                    (parsed is None and not re.search(r"tools\.loom\.\w+\s*\(", raw))):
        return "code"
    return None


def saved_chat_item(event):
    kind, payload = event["kind"], event["payload"]
    if kind == "message.delivered":
        sender = payload.get("sender") or ""
        if not sender.startswith("agent:"):
            return True
        message = payload.get("message") if "completions" in payload else payload.get("text")
        return bool(isinstance(message, str) and message.strip())
    if kind == "agent.turn_completed":
        reason = payload.get("stopReason")
        return bool(reason and reason != "completed")
    return kind in ("item.completed", "child.created", "task_completed", "harness.changed")


def started_group(lead_events, child_id, native_create_id):
    markers = [e for e in lead_events if e["kind"] == "child.created"]
    demand(len(markers) == 1 and markers[0]["payload"].get("child") == child_id,
           "Lead did not save exactly one child.created for the owned child")
    visible = [e for e in lead_events if saved_chat_item(e)]
    index = next(i for i, e in enumerate(visible) if e["event_id"] == markers[0]["event_id"])
    before, after = [], []
    for e in reversed(visible[:index]):
        if not started_create_role(e):
            break
        before.insert(0, e)
    for e in visible[index + 1:]:
        if not started_create_role(e):
            break
        after.append(e)
    group = before + after
    demand(group and len([e for e in group if e["event_id"] == native_create_id and
                          started_create_role(e) == "create"]) == 1,
           "exact native agent_create did not belong to the saved Started group")
    return {"marker_event_id": markers[0]["event_id"],
            "entries": [{"event_id": e["event_id"], "role": started_create_role(e)} for e in group]}


def started_group_ui_ok(value, group, stage):
    count = len(group["entries"])
    demand(count > 0 and value["toolCount"] == count, "Started tool count differs from exact saved group entries")
    if stage == "collapsed":
        demand(value["expanded"] == "false" and not value["expandedRows"], "Started group was not collapsed")
    if stage == "expanded":
        rows = value["expandedRows"]
        demand(value["expanded"] == "true" and len(rows) == count,
               "expanded Started rows differ from exact saved group entries")
        for saved, shown in zip(group["entries"], rows):
            expected = "Started " if saved["role"] == "create" else "Ran code"
            demand(shown["status"] == "completed" and shown["inGroup"] == "true" and
                   shown["label"].startswith(expected) and not re.search(r"tools\.loom|\{|brief:", shown["label"]),
                   f"expanded Started row differs from saved group event {saved['event_id']}")


def lead_ui(stage):
    lead_id, child_id = identity("lead"), identity("child")
    demand(browser("location.pathname") == f"/ws/{os.environ['AFT_WS']}/chat/{lead_id}", "UI is not exact Lead Chat")
    lead_events = events("lead")
    native = [e for e in lead_events if actual_call(e, "agent_create", CHILD_NAME, CHILD_NAME)]
    demand(len(native) == 1, "expected one completed native agent_create for the exact child")
    group = started_group(lead_events, child_id, native[0]["event_id"])
    expected_count = len(group["entries"])
    child_path = f"/ws/{os.environ['AFT_WS']}/chat/{child_id}"
    expr = """(() => {
      const marker=[...document.querySelectorAll('[data-testid=started-marker]')];
      const own=marker.filter(m=>[...m.querySelectorAll('a')].some(a=>a.getAttribute('href')===CHILD));
      const m=own[0], links=m?[...m.querySelectorAll('a')]:[];
      const button=m?.querySelector('button[aria-expanded]');
      const tray=document.querySelector('[data-testid=agent-tray]');
      const badge=m?.querySelector('[data-agent-color]');
      const trayBadge=tray?.querySelector('button[aria-expanded] [data-agent-color]');
      const cards=[...document.querySelectorAll('[data-testid=completion-record]')];
      const card=cards.find(c=>c.dataset.attempt==='0');
      const raw=[...document.querySelectorAll('[data-testid=chat-transcript] li[data-kind=user]')].some(x=>x.textContent.includes('task_completed:'));
      const expandedRows=[];
      if(button?.getAttribute('aria-expanded')==='true') {
        let next=m?.closest('li')?.nextElementSibling;
        for(let i=0;i<=EXPECTED;i++) {
          const entry=next?.dataset.kind==='work' ? next.querySelector('[data-testid=tool-call][data-in-group=true]') : null;
          if(!entry) break;
          const heading=entry.querySelector(':scope > div > span[class*=heading]');
          expandedRows.push({label:heading?.textContent?.trim()||'',
            status:entry.dataset.status,inGroup:entry.dataset.inGroup});
          next=next?.nextElementSibling;
        }
      }
      return {markerCount:marker.length,ownCount:own.length,links:links.map(a=>({href:a.getAttribute('href'),name:a.lastChild?.textContent?.trim()||''})),
        callText:button?.textContent.trim(),expanded:button?.getAttribute('aria-expanded'),
        toolCount:Number(/^([0-9]+) tool calls?/.exec(button?.textContent.trim()||'')?.[1]||0),expandedRows,
        markerText:m?.textContent.trim(),color:badge?.getAttribute('data-agent-color'),
        trayColor:trayBadge?.getAttribute('data-agent-color'),
        cards:cards.map(c=>({color:c.getAttribute('data-agent-color'),badge:c.querySelector('[data-agent-color]')?.getAttribute('data-agent-color'),
          attempt:c.dataset.attempt,outcome:c.dataset.outcome,delivery:c.dataset.delivery,name:c.textContent.trim()})),
        card:card?{color:card.getAttribute('data-agent-color'),badge:card.querySelector('[data-agent-color]')?.getAttribute('data-agent-color'),
          outcome:card.dataset.outcome,delivery:card.dataset.delivery,role:card.getAttribute('role'),tabIndex:card.tabIndex,
          name:card.textContent.trim()}:null,rawCompletionBubble:raw};
    })()""".replace("CHILD", json.dumps(child_path)).replace("EXPECTED", str(expected_count))
    value = browser(expr)
    save("started-group-" + stage, {"child": child_id, "marker_event_id": group["marker_event_id"],
                                   "entry_event_ids": [e["event_id"] for e in group["entries"]],
                                   "entry_roles": [e["role"] for e in group["entries"]],
                                   "ui_marker_count": value["markerCount"], "ui_tool_count": value["toolCount"],
                                   "ui_adjacent_rows": value["expandedRows"]})
    demand(value["markerCount"] == value["ownCount"] == 1 and value["links"] == [{"href": child_path, "name": CHILD_NAME}], "Started child name/count/link mismatch")
    expected_text = f"{expected_count} tool {'call' if expected_count == 1 else 'calls'}"
    demand(value["callText"].startswith(expected_text) and value["color"] is not None, "Started call count/color missing")
    started_group_ui_ok(value, group, stage)
    demand("{" not in value["markerText"] and "tools.loom" not in value["markerText"] and "brief:" not in value["markerText"],
           "Started marker exposed raw bridge code or input")
    demand(not value["rawCompletionBubble"], "raw task_completed leaked as a user bubble")
    if stage == "collapsed":
        demand(value["trayColor"] == value["color"], "Started and live tray child colours differ")
        save("started-color", value["color"])
    if stage in ("card", "card-final"):
        card = value["card"]
        demand(card and card["outcome"] == "completed" and card["delivery"] == "delivered", "first child completion card missing")
        demand(card["role"] == "link" and card["tabIndex"] == 0 and CHILD_NAME in card["name"], "E1 card is not keyboard accessible for exact child")
        demand(card["color"] == card["badge"] == value["color"] == load("started-color"), "Started/card child colour changed")
        lead_events = events("lead")
        child_id = identity("child")
        attempts = (0, 1) if stage == "card-final" else (0,)
        records = [e for e in lead_events if e["kind"] == "task_completed" and
                   e["event_id"].startswith(f"task_completed:{child_id}:")]
        demand(len(records) == len(attempts), "Lead has duplicate or missing keyed child completion")
        for attempt in attempts:
            key = {"child": child_id, "attempt": attempt}
            record = [e for e in records if e["event_id"] == f"task_completed:{child_id}:{attempt}"]
            delivered = [e for e in lead_events if e["kind"] == "message.delivered" and
                         key in e["payload"].get("completions", [])]
            demand(len(record) == len(delivered) == 1 and record[0]["payload"].get("outcome") == "completed",
                   "child completion was not saved and delivered once to its true Lead")
        value["saved_completion_ids"] = [e["event_id"] for e in records]
        if stage == "card-final":
            demand(len(value["cards"]) == 2 and {c["attempt"] for c in value["cards"]} == {"0", "1"}, "same child does not have two keyed result cards")
            demand(all(c["outcome"] == "completed" and c["delivery"] == "delivered" and
                       c["color"] == c["badge"] == load("started-color") and CHILD_NAME in c["name"]
                       for c in value["cards"]), "final result card name, colour or delivery mismatch")
    elif stage not in ("collapsed", "expanded"):
        raise AssertionError("unknown Lead UI stage")
    save("ui-" + stage, value)
    subprocess.run(["agent-browser", "--session", os.environ["AFT_SESSION"], "screenshot", str(OUT / f"ui-{stage}.png")], check=True)


def sidebar(stage):
    lead_id, child_id = identity("lead"), identity("child")
    demand(browser("location.pathname") == f"/ws/{os.environ['AFT_WS']}/chat/{lead_id}", "sidebar check is not on Lead Chat")
    row = agent("child")
    path = f"/ws/{os.environ['AFT_WS']}/chat/{child_id}"
    presence = "false" if stage == "gone" else "true"
    ready = ("(() => { const n=document.querySelector('nav[aria-label=Agents]'); return !!n && "
             "!!n.querySelector('a[href=\"" + path + "\"]') === " + presence + "; })()")
    subprocess.run(["agent-browser", "--session", os.environ["AFT_SESSION"], "wait", "--fn", ready], check=True)
    expr = """(() => {
      const nav=document.querySelector('nav[aria-label=Agents]');
      const links=[...nav.querySelectorAll('a')].filter(a=>a.getAttribute('href')===CHILD);
      const a=links[0], group=a?.closest('[role=group]'), wrapper=a?.closest('[data-testid=sortable-agent-row]');
      const avatar=a?.querySelector('[data-agent-color]');
      return {count:links.length,name:a?.querySelector('[data-testid=agent-list-name]')?.textContent.trim(),
        role:a?.textContent.trim(),group:group?.getAttribute('aria-label'),
        logo:a?.querySelector('[role=img]')?.getAttribute('aria-label'),dot:avatar?.getAttribute('data-dot'),
        color:avatar?.getAttribute('data-agent-color'),archive:wrapper?.querySelector('[data-testid=agent-row-archive]')?.getAttribute('aria-label')};
    })()""".replace("CHILD", json.dumps(path))
    value = browser(expr)
    if stage == "gone":
        demand(row["state"] == "finished" and value["count"] == 0, "finished child still shown under Lead while another chat is open")
    else:
        demand(row["state"] == "active" and row["running_turn_id"], "sidebar check missed active child")
        demand(value["count"] == 1 and value["name"] == CHILD_NAME and value["group"] == f"{LEAD_NAME} children", "child sidebar name/nesting mismatch")
        demand(value["logo"] == "opencode" and value["dot"] == "working", "child sidebar logo/status dot mismatch")
        demand(value["color"] == load("started-color"), "child sidebar colour differs from Started/tray")
        demand(value["archive"] == f"Archive {CHILD_NAME}", "child Archive control missing")
        selector = f'nav[aria-label=Agents] a[href="{path}"]'
        subprocess.run(["agent-browser", "--session", os.environ["AFT_SESSION"], "hover", selector], check=True)
        hover_expr = """(() => {const a=document.querySelector(SELECTOR);
          const button=a?.closest('[data-testid=sortable-agent-row]')?.querySelector('[data-testid=agent-row-archive]');
          return {underline:getComputedStyle(a).textDecorationLine,opacity:Number(getComputedStyle(button).opacity),
            archiveVisible:!!button?.getClientRects().length};})()""".replace("SELECTOR", json.dumps(selector))
        subprocess.run(["agent-browser", "--session", os.environ["AFT_SESSION"], "wait", "--fn",
                        hover_expr + ".opacity > 0.9"], check=True)
        hover = browser(hover_expr)
        demand(hover["underline"] == "none" and hover["opacity"] > 0.9 and hover["archiveVisible"], "hover underline/Archive state mismatch")
        value["hover"] = hover
    save("sidebar-" + stage, {"child": child_id, "saved_state": row["state"], "ui": value})
    subprocess.run(["agent-browser", "--session", os.environ["AFT_SESSION"], "screenshot", str(OUT / f"sidebar-{stage}.png")], check=True)


def child_selected():
    path = f"/ws/{os.environ['AFT_WS']}/chat/{identity('child')}"
    demand(browser("location.pathname") == path, "E1 keyboard did not open exact child ID")
    ready = "(() => { const a=document.querySelector('nav[aria-label=Agents] a[href=\"" + path + "\"]'); return a?.getAttribute('aria-current') === 'page'; })()"
    subprocess.run(["agent-browser", "--session", os.environ["AFT_SESSION"], "wait", "--fn", ready], check=True)
    expr = """(() => {const a=[...document.querySelectorAll('nav[aria-label=Agents] a')].find(x=>x.getAttribute('href')===CHILD);
      return {selected:a?.getAttribute('aria-current'),name:a?.querySelector('[data-testid=agent-list-name]')?.textContent.trim(),
        logo:a?.querySelector('[role=img]')?.getAttribute('aria-label'),dot:a?.querySelector('[data-dot]')?.getAttribute('data-dot')};})()""".replace("CHILD", json.dumps(path))
    value = browser(expr)
    demand(value == {"selected": "page", "name": CHILD_NAME, "logo": "opencode", "dot": "done"}, "finished-child open exception or E1 selection failed")
    save("ui-keyboard-open", {"child": identity("child"), "route": path, "sidebar": value})
    subprocess.run(["agent-browser", "--session", os.environ["AFT_SESSION"], "screenshot", str(OUT / "ui-keyboard-open.png")], check=True)


def working_fallback(stage):
    demand(stage in ("fresh", "reconnect"), "unknown Working fallback checkpoint")
    child_id = identity("child")
    row = agent("child")
    expr = """(() => {const r=document.querySelector('[data-testid=agent-tray] [data-tray-row="' + CHILD + '"]');
      return {visible:!!r?.getClientRects().length,status:r?.querySelector('[data-status]')?.getAttribute('data-status'),
        result:r?.querySelector('[data-status=running] > span:last-child')?.textContent.trim()};})()""".replace("CHILD", json.dumps(child_id))
    ui = browser(expr)
    observed = row["state"] == "active" and bool(row["running_turn_id"]) and ui["visible"] and ui["status"] == "running" and ui["result"] == "Working…"
    if observed:
        active_items = [e for e in events("child") if e["turn_id"] == row["running_turn_id"] and
                        e["kind"] in ("item.started", "item.completed", "tool.started")]
        demand(not active_items, "Working fallback shown despite a saved current-turn step")
        subprocess.run(["agent-browser", "--session", os.environ["AFT_SESSION"], "screenshot", str(OUT / f"ui-working-{stage}.png")], check=True)
    save("ui-working-" + stage, {"observed": bool(observed), "child": child_id, "state": row["state"],
                                   "turn": row["running_turn_id"], "ui": ui,
                                   "evidence": "genuine live fallback" if observed else "not witnessed; no fallback claim"})


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
    first = settled_lead_send("p1", "QUEUE-P1-")
    child_events = events("child")
    sender = f"agent:{identity('lead')}"
    waits = [e for e in child_events if e["kind"] == "message.waiting" and
             e["payload"].get("reason") == sender]
    row = agent("child")
    precondition = {"child": row["agent_id"], "state": row["state"],
                    "running_turn_id": row.get("running_turn_id"), "attempt": row["attempt"],
                    "expected_turn_id": load("fifo-before")["turn"],
                    "native_tool_event_id": first["event_id"],
                    "saved_waiting_event_ids": [e["event_id"] for e in waits],
                    "live_waiting_senders": [w["sender"] for w in row["waiting_messages"]]}
    save("fifo-parent-precondition", precondition)
    receipt = native_send_result(first, False)
    precondition["native_message_id"] = receipt["message_id"]
    save("fifo-parent-precondition", precondition)
    demand(len(waits) == 1, "exact native P1 send lacks one saved parent waiting event")
    request = native_wait_request(waits[0], row["agent_id"], sender, receipt)
    save("fifo-parent-receipt", {"tool_event_id": first["event_id"], "waiting_event_id": waits[0]["event_id"],
                                 "request_id": request, "message_id": receipt["message_id"]})
    demand(row["state"] == "active" and row.get("running_turn_id"),
           "inconclusive: child finished before the U1/P1 running-turn FIFO witness")
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
    settled_lead_send("p2", "QUEUE-P2-")
    row = agent("child")
    active(row)
    demand(row["attempt"] > load("fifo-done")["attempt"], "same task child did not reopen")
    proof = load("fifo-proof")
    delivered(events("child"), TEXT["p2"], proof["sender_parent"])
    demand(not row["waiting_messages"], "new attempt had unexpected queued slots")
    capture("parent-started")


def first_parent_queued():
    first = settled_lead_send("p3", "QUEUE-P3-")
    initial = native_send_result(first, False)
    row = agent("child")
    active(row)
    demand(row["running_turn_id"] == load("parent-started")["turn"], "parent slot missed busy child turn")
    slot = row["waiting_messages"]
    demand(len(slot) == 1 and slot[0]["sender"] == f"agent:{identity('lead')}" and
           slot[0]["text"] == TEXT["p3"] and slot[0]["since"], "first real parent waiting slot missing")
    ev = events("child")
    p2_delivery = delivered(ev, TEXT["p2"], f"agent:{identity('lead')}")
    waits = [e for e in ev if (e["kind"] == "message.waiting" and e["seq"] > p2_delivery["seq"] and
             e["payload"].get("reason") == f"agent:{identity('lead')}")]
    demand(len(waits) == 1 and not [e for e in ev if e["kind"] == "message.delivered" and
                                    e["payload"].get("text") == TEXT["p3"]], "first parent slot was already delivered")
    request_id = native_wait_request(waits[0], row["agent_id"], slot[0]["sender"], initial)
    save("first-parent-initial", {"tool_event_id": first["event_id"], "tool_turn_id": first["turn_id"],
                                  "native_result": initial, "waiting_event_id": waits[0]["event_id"],
                                  "request_id": request_id,
                                  "waiting_seq": waits[0]["seq"], "slot": slot[0], "turn": row["running_turn_id"]})
    capture("first-parent-initial-state")


def first_parent():
    first = settled_lead_send("p3", "QUEUE-P3-")
    second = settled_lead_send("p3b", "QUEUE-P3B-")
    initial, replacement = replacement_result(first, second)
    receipt = {"stage": "parsed_native; waiting linkage pending",
               "tool_event_ids": [first["event_id"], second["event_id"]],
               "native_results": [initial, replacement]}
    save("first-parent-replacement-receipt", receipt)
    prior = load("first-parent-initial")
    demand(prior["tool_event_id"] == first["event_id"] and prior["native_result"] == initial,
           "initial native result changed before replacement")
    row = agent("child")
    active(row)
    demand(row["running_turn_id"] == prior["turn"] == load("parent-started")["turn"], "parent slot moved to a different child turn")
    slot = row["waiting_messages"]
    demand(len(slot) == 1 and slot[0]["sender"] == prior["slot"]["sender"] == f"agent:{identity('lead')}" and
           slot[0]["text"] == TEXT["p3b"] and slot[0]["since"] == prior["slot"]["since"],
           "native replacement changed sender, FIFO timestamp or exact text")
    ev = events("child")
    p2_delivery = delivered(ev, TEXT["p2"], f"agent:{identity('lead')}")
    waits = [e for e in ev if (e["kind"] == "message.waiting" and e["seq"] > p2_delivery["seq"] and
             e["payload"].get("reason") == f"agent:{identity('lead')}")]
    demand(len(waits) == 2 and waits[0]["event_id"] == prior["waiting_event_id"] and
           waits[0]["seq"] < waits[1]["seq"] and waits[0]["event_id"] != waits[1]["event_id"],
           "parent replacement lacks two saved sends")
    first_request = native_wait_request(waits[0], row["agent_id"], slot[0]["sender"], initial)
    replacement_request = native_wait_request(waits[1], row["agent_id"], slot[0]["sender"], replacement)
    demand(first_request == prior["request_id"] and first_request != replacement_request,
           "native replacement did not use two exact RequestIDs")
    demand(not [e for e in ev if e["kind"] == "message.delivered" and e["payload"].get("text") == TEXT["p3"]],
           "superseded native parent text was delivered")
    save("first-parent-replacement-receipt", {**receipt, "stage": "saved waiting linkage verified",
                                              "waiting_event_ids": [waits[0]["event_id"], waits[1]["event_id"]],
                                              "request_ids": [first_request, replacement_request]})
    save("first-parent-proof", {"event_ids": [e["event_id"] for e in waits], "seq": waits[1]["seq"],
                                "turn": row["running_turn_id"], "native_result": replacement,
                                "request_ids": [first_request, replacement_request],
                                "initial_message_id": initial["message_id"],
                                "sender": slot[0]["sender"], "since": slot[0]["since"]})
    capture("first-parent")


def first_done():
    rows = events("child")
    proof = load("first-parent-proof")
    user = load("fifo-proof")["sender_user"]
    u3 = waiting_event(rows, "u3", user)
    demand(proof["seq"] < u3["seq"], "interrupt was not later than the parent slot")
    ids = [load(f"receipt-{stage}")["result"]["message_id"] for stage in ("u1", "u2", "u3")]
    demand(len(set(ids)) == 3, "public user Send receipts reused a message ID")
    pair = delivery_pair(rows, TEXT["u3"], user, TEXT["p3b"], f"agent:{identity('lead')}", (TEXT["p3"],))
    demand(not agent("child")["waiting_messages"], "First phase still has waiting slots")
    save("first-delivery", {"interrupt_receipt": load("receipt-u3"), "older_parent_waiting": proof, "later_user_waiting": u3["event_id"], "delivered": pair})
    capture("first-done")


def replay_u3():
    receipt = load("receipt-u3")
    demand(receipt["body"] == {"text": TEXT["u3"], "delivery": "interrupt"} and
           receipt["result"].get("interrupted") is True, "original U3 interrupt receipt invalid")
    before_events = events("child")
    before_agent = agent("child")
    demand(before_agent["state"] == "finished" and not before_agent["waiting_messages"],
           "U3 replay must happen after natural delivery")
    replayed = http(f"{ROOT}/{quote(identity('child'))}/messages", receipt["body"], receipt["request_id"])
    after_events = events("child")
    after_agent = agent("child")
    fields = ("message_id", "state", "replaced", "interrupted", "turn_id")
    def accepted_result(original, retry, handed_proof):
        allowed = set(fields)
        if not isinstance(original, dict) or not isinstance(retry, dict) or set(original) != set(retry) or not set(original) <= allowed:
            return False
        if not {"message_id", "state", "replaced", "interrupted"} <= set(original):
            return False
        if (not isinstance(original["message_id"], str) or not original["message_id"] or
                original["state"] not in ("waiting", "handed") or type(retry["state"]) is not str or
                any(type(value[key]) is not bool for value in (original, retry) for key in ("replaced", "interrupted")) or
                any(type(value["turn_id"]) is not str for value in (original, retry) if "turn_id" in value)):
            return False
        if any(original[key] != retry[key] for key in set(original) - {"state"}):
            return False
        return original["state"] == retry["state"] or (original["state"] == "waiting" and
               retry["state"] == "handed" and handed_proof)

    def handed_proof():
        child = identity("child")
        key = "msg_" + hashlib.sha256((child + "\x00" + receipt["request_id"]).encode()).hexdigest()[:26]
        pair = load("first-delivery")["delivered"]
        native = load("first-native-inputs")
        rows = [e for e in before_events if e.get("agent_id") == child and e["kind"] == "message.delivered" and
                e["payload"].get("text") == TEXT["u3"] and e["payload"].get("inputKey") == key]
        return (len(rows) == 1 and len(pair) == len(native) == 2 and
                pair[0]["event_id"] == rows[0]["event_id"] and pair[0]["input_key"] == key and
                native[0]["agent_id"] == child and native[0]["input_key"] == key and
                native[0]["native_user_message_count"] == 1)

    progressed = isinstance(replayed, dict) and receipt["result"]["state"] == "waiting" and replayed.get("state") == "handed"
    accepted = accepted_result(receipt["result"], replayed, handed_proof() if progressed else False)
    def safe_result(value):
        if not isinstance(value, dict):
            return {}
        result = {}
        for key in fields:
            if key in value:
                expected_type = bool if key in ("replaced", "interrupted") else str
                result[key] = value[key] if isinstance(value[key], expected_type) else "<invalid type>"
        return result
    before_ids = [(e["seq"], e["event_id"]) for e in before_events]
    after_ids = [(e["seq"], e["event_id"]) for e in after_events]
    save("u3-replay-diagnostic", {"original_request_id": receipt["request_id"],
                                  "retry_request_id": receipt["request_id"],
                                  "original": safe_result(receipt["result"]), "replay": safe_result(replayed),
                                  "original_fields_present": [key for key in fields if key in receipt["result"]],
                                  "replay_fields_present": [key for key in fields if isinstance(replayed, dict) and key in replayed],
                                  "receipt_equal": replayed == receipt["result"],
                                  "receipt_accepted": accepted,
                                  "events_before": before_ids, "events_after": after_ids,
                                  "events_equal": after_ids == before_ids,
                                  "agent_before": {"state": before_agent["state"], "attempt": before_agent["attempt"],
                                                   "waiting_count": len(before_agent["waiting_messages"])},
                                  "agent_after": {"state": after_agent["state"], "attempt": after_agent["attempt"],
                                                  "waiting_count": len(after_agent["waiting_messages"])},
                                  "slots_equal": after_agent["waiting_messages"] == before_agent["waiting_messages"]})
    demand(accepted, "same U3 RequestID changed stable receipt fields or lacks saved handover proof")
    demand(after_ids == before_ids, "U3 replay added a saved event")
    demand(after_agent["state"] == before_agent["state"] and
           after_agent["attempt"] == before_agent["attempt"] and
           after_agent["waiting_messages"] == before_agent["waiting_messages"],
           "U3 replay mutated the finished child")
    pair = load("first-delivery")["delivered"]
    demand(len(pair) == 2 and all(x["input_key"] for x in pair), "First delivery input keys missing")
    prior_native = load("first-native-inputs")
    native_inputs()
    after_native = load("first-native-inputs")
    demand([(p["agent_id"], p["input_key"], p["native_id"], p["native_root"], p["native_user_message_count"])
            for p in after_native] ==
           [(p["agent_id"], p["input_key"], p["native_id"], p["native_root"], 1) for p in prior_native],
           "U3 replay changed the owned native input count")
    save("u3-replay", {"request_id": receipt["request_id"], "result": replayed,
                       "event_ids_before_after": [e["event_id"] for e in after_events],
                       "input_keys": [x["input_key"] for x in pair]})


def native_inputs():
    pair = load("first-delivery")["delivered"]
    demand(len(pair) == 2 and pair[0]["input_key"] != pair[1]["input_key"], "First input keys not distinct")
    helper = Path(os.environ["AFT_TESTS_DIR"]) / "scripts/coverage-children-queue-native.sh"
    proofs = []
    for entry in pair:
        raw = subprocess.check_output(["bash", str(helper), identity("child"), entry["input_key"]], text=True)
        value = json.loads(raw.strip().splitlines()[-1])
        demand(value["agent_id"] == identity("child") and value["input_key"] == entry["input_key"] and
               value["native_user_message_count"] == 1 and value["harness"] == "opencode",
               "owned native history does not contain exact input once")
        proofs.append(value)
    demand(proofs[0]["native_id"] == proofs[1]["native_id"] and
           proofs[0]["native_root"] == proofs[1]["native_root"], "First inputs resolved to different child NativeRefs")
    save("first-native-inputs", proofs)


SHOT_KEYS = {
    "fifo-user-waiting": ("u1",),
    "fifo-two-senders": ("u1", "p1"),
    "fifo-replaced": ("u2", "p1"),
    "fifo-delivered": ("u2", "p1"),
    "fifo-reloaded": ("u2", "p1"),
    "first-parent-initial": ("p3",),
    "first-parent-waiting": ("p3b",),
    "first-user-interrupt": ("u3", "p3b"),
    "first-delivered": ("u3", "p3b"),
    "first-reloaded": ("u3", "p3b"),
}


def assert_shot_rows(name, ui, api_waiting, lead_id):
    expected = SHOT_KEYS[name]
    delivered_phase = name.endswith(("delivered", "reloaded"))
    if delivered_phase:
        demand(not ui["waiting"] and not api_waiting, "delivered screenshot still has a waiting row")
        demand(all(any(TEXT[key] in text for text in ui["history"]) for key in expected),
               "delivered screenshot lacks exact saved message text")
    elif name == "first-user-interrupt":
        keys = ("u3", "p3b") if len(api_waiting) == 2 else ("p3b",)
        demand(keys in (("u3", "p3b"), ("p3b",)), "invalid First waiting shape")
        demand(len(ui["waiting"]) == len(api_waiting) == len(keys), "First screenshot waiting count mismatch")
        for index, key in enumerate(keys):
            demand(api_waiting[index]["text"] == TEXT[key] and TEXT[key] in ui["waiting"][index],
                   "First screenshot waiting order/text mismatch")
            demand(api_waiting[index]["sender"].startswith("user:") if key == "u3" else
                   api_waiting[index]["sender"] == f"agent:{lead_id}", "First screenshot sender mismatch")
        if keys == ("p3b",):
            demand(any(TEXT["u3"] in text for text in ui["user"]), "interrupt is not visible as the exact user message")
    else:
        demand(len(ui["waiting"]) == len(api_waiting) == len(expected), "waiting screenshot count mismatch")
        for index, key in enumerate(expected):
            demand(api_waiting[index]["text"] == TEXT[key] and TEXT[key] in ui["waiting"][index],
                   "waiting screenshot sender order/text mismatch")
            demand(api_waiting[index]["sender"].startswith("user:") if key.startswith("u") else
                   api_waiting[index]["sender"] == f"agent:{lead_id}", "waiting screenshot sender mismatch")
    absent = ("u1",) if name in ("fifo-replaced", "fifo-delivered", "fifo-reloaded") else \
             ("p3",) if name.startswith("first-") and name != "first-parent-initial" else ()
    demand(not any(TEXT[key] in text for key in absent for text in ui["waiting"] + ui["history"]),
           "superseded text is still visible")


def assert_shot_route(route, child_id, workspace):
    demand(route == f"/ws/{workspace}/chat/{child_id}", "screenshot is not exact child Chat")


def shot(name, required):
    demand(re.fullmatch(r"[a-z][a-z0-9-]+", name), "unsafe screenshot name")
    demand(name in SHOT_KEYS and required == [f"QUEUE-{key.upper()}-{RUN}" for key in SHOT_KEYS[name]],
           "screenshot requested arbitrary or wrong-stage markers")
    route = browser("location.pathname")
    assert_shot_route(route, identity("child"), os.environ["AFT_WS"])
    row = agent("child")
    demand(row["agent_id"] == identity("child"), "screenshot API agent mismatch")
    ui = browser("""(() => { const t=document.querySelector('[data-testid=chat-transcript]');
      if(!t) return null;
      const all=[...t.querySelectorAll(':scope > li')];
      return {waiting:all.filter(x=>x.dataset.kind==='waiting').map(x=>x.textContent.trim()),
        user:all.filter(x=>x.dataset.kind==='user').map(x=>x.textContent.trim()),
        history:all.filter(x=>x.dataset.kind!=='waiting').map(x=>x.textContent.trim()),
        running:!!document.querySelector('section[aria-label="Agent chat"] header [data-running=true]')};
    })()""")
    demand(isinstance(ui, dict), "exact child transcript absent")
    assert_shot_rows(name, ui, row["waiting_messages"], identity("lead"))
    if name.endswith(("delivered", "reloaded")):
        demand(row["state"] == "finished" and not ui["running"], "delivered screenshot is not finished")
        if name.startswith("fifo-"):
            proof = load("fifo-proof")
            delivery_pair(events("child"), TEXT["u2"], proof["sender_user"], TEXT["p1"], proof["sender_parent"], (TEXT["u1"],))
        else:
            proof = load("first-parent-proof")
            delivery_pair(events("child"), TEXT["u3"], load("fifo-proof")["sender_user"], TEXT["p3b"], proof["sender"], (TEXT["p3"],))
    else:
        active(row)
        demand(ui["running"], "waiting screenshot lacks the actual running child")
        if name.startswith("fifo-"):
            demand(row["running_turn_id"] == load("fifo-before")["turn"], "FIFO screenshot is from another child turn")
        else:
            if name == "first-parent-initial":
                demand(row["running_turn_id"] == load("first-parent-initial")["turn"], "first parent screenshot is from another child turn")
            elif name == "first-parent-waiting":
                demand(row["running_turn_id"] == load("first-parent-proof")["turn"], "parent waiting screenshot is from another child turn")
            else:
                receipt = load("receipt-u3")["result"]
                demand(receipt.get("interrupted") is True, "First screenshot lacks a real interrupt receipt")
                u3 = waiting_event(events("child"), "u3", load("fifo-proof")["sender_user"])
                demand(load("first-parent-proof")["seq"] < u3["seq"], "First screenshot lacks later user order")
    subprocess.run(["agent-browser", "--session", os.environ["AFT_SESSION"], "screenshot", str(OUT / f"{name}.png")], check=True)
    save("shot-" + name, {"child": identity("child"), "route": route, "required": required,
                           "api_state": row["state"], "waiting": row["waiting_messages"],
                           "ui": ui, "first_receipt": load("receipt-u3")["result"] if name.startswith("first-user") else None,
                           "file": name + ".png"})


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
    elif cmd == "first-parent-queued": first_parent_queued()
    elif cmd == "first-parent": first_parent()
    elif cmd == "first-done": first_done()
    elif cmd == "replay-u3": replay_u3()
    elif cmd == "native-inputs": native_inputs()
    elif cmd == "lead-ui": lead_ui(sys.argv[2])
    elif cmd == "sidebar": sidebar(sys.argv[2])
    elif cmd == "child-selected": child_selected()
    elif cmd == "working": working_fallback(sys.argv[2])
    elif cmd == "shot": shot(sys.argv[2], sys.argv[3:])
    elif cmd == "cleanup": cleanup()
    else: raise SystemExit(f"unknown command: {cmd}")


if __name__ == "__main__":
    main()
