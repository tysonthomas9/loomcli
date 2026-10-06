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
    "p3b": f"QUEUE-P3B-{RUN}: after the current task, name the slot ordering rule and cite send.go.",
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
    save("lead-pre-create-ref", {"branch": git(row["worktree_path"], "rev-parse", "--abbrev-ref", "HEAD"),
                                 "head": git(row["worktree_path"], "rev-parse", "HEAD"),
                                 "repo": row["repo"], "worktree_path": row["worktree_path"]})


def git(path, *args):
    return subprocess.check_output(["git", "-C", path, *args], text=True).strip()


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


def replacement_result(first, second):
    def values(event):
        output = (event["payload"].get("tool") or {}).get("output") or ""
        return re.findall(r"\breplaced[\"']?\s*[:=]\s*(true|false)\b", output, flags=re.I)
    first_values, second_values = values(first), values(second)
    if first["event_id"] == second["event_id"]:
        demand(len(first_values) >= 2 and first_values[0].lower() == "false" and first_values[-1].lower() == "true",
               "one native execution did not return first=false then replacement=true")
    else:
        demand(first_values and first_values[-1].lower() == "false" and second_values and second_values[-1].lower() == "true",
               "distinct native agent_send executions did not report false then replaced=true")


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
    child_head = git(child["worktree_path"], "rev-parse", "HEAD")
    child_branch = git(child["worktree_path"], "rev-parse", "--abbrev-ref", "HEAD")
    demand(prior["branch"] == lead["branch"] and child_branch == child["branch"], "actual worktree branch mismatch")
    demand(child_head == prior["head"], "read-only child did not start at pre-create parent HEAD")
    subprocess.run(["git", "-C", child["worktree_path"], "merge-base", "--is-ancestor", prior["head"], child_head], check=True)
    created = [e for e in events("lead") if e["kind"] == "child.created" and e["payload"].get("child") == child["agent_id"]]
    demand(len(created) == 1, "missing exact saved child.created")
    one_call("agent_create", CHILD_NAME)
    save("child", child)
    save("child-create-ref", {"parent_head_before": prior["head"], "parent_branch": prior["branch"],
                              "child_head_after": child_head, "child_branch": child_branch,
                              "child_base_ref": child["base_ref"], "parent_worktree": prior["worktree_path"],
                              "child_worktree": child["worktree_path"]})


def lead_ui(stage):
    lead_id, child_id = identity("lead"), identity("child")
    demand(browser("location.pathname") == f"/ws/{os.environ['AFT_WS']}/chat/{lead_id}", "UI is not exact Lead Chat")
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
      return {markerCount:own.length,links:links.map(a=>({href:a.getAttribute('href'),name:a.textContent.trim()})),
        callText:button?.textContent.trim(),expanded:button?.getAttribute('aria-expanded'),
        bridge:[...document.querySelectorAll('[data-testid=bridge-call]')].map(x=>x.textContent.trim()),
        markerText:m?.textContent.trim(),color:badge?.getAttribute('data-agent-color'),
        trayColor:trayBadge?.getAttribute('data-agent-color'),
        cards:cards.map(c=>({color:c.getAttribute('data-agent-color'),badge:c.querySelector('[data-agent-color]')?.getAttribute('data-agent-color'),
          attempt:c.dataset.attempt,outcome:c.dataset.outcome,delivery:c.dataset.delivery,name:c.textContent.trim()})),
        card:card?{color:card.getAttribute('data-agent-color'),badge:card.querySelector('[data-agent-color]')?.getAttribute('data-agent-color'),
          outcome:card.dataset.outcome,delivery:card.dataset.delivery,role:card.getAttribute('role'),tabIndex:card.tabIndex,
          name:card.textContent.trim()}:null,rawCompletionBubble:raw};
    })()""".replace("CHILD", json.dumps(child_path))
    value = browser(expr)
    demand(value["markerCount"] == 1 and value["links"] == [{"href": child_path, "name": CHILD_NAME}], "Started child name/count/link mismatch")
    demand(value["callText"].startswith("1 tool call") and value["color"] is not None, "Started call count/color missing")
    demand("{" not in value["markerText"] and "tools.loom" not in value["markerText"] and "brief:" not in value["markerText"],
           "Started marker exposed raw bridge code or input")
    demand(not value["rawCompletionBubble"], "raw task_completed leaked as a user bubble")
    if stage == "collapsed":
        demand(value["expanded"] == "false" and not [b for b in value["bridge"] if b.startswith("Started ")], "Started bridge was not collapsed")
        demand(value["trayColor"] == value["color"], "Started and live tray child colours differ")
        save("started-color", value["color"])
    elif stage == "expanded":
        started_calls = [b for b in value["bridge"] if b.startswith("Started ")]
        demand(value["expanded"] == "true" and len(started_calls) == 1, "Started did not expand one bridge call")
        demand("tools.loom" not in started_calls[0] and "{" not in started_calls[0], "expanded bridge showed raw code")
    elif stage in ("card", "card-final"):
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
    else:
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
    first = one_call("agent_send", "QUEUE-P3-")
    second = one_call("agent_send", "QUEUE-P3B-")
    replacement_result(first, second)
    row = agent("child")
    active(row)
    demand(row["running_turn_id"] == load("parent-started")["turn"], "parent slot missed busy child turn")
    demand(len(row["waiting_messages"]) == 1 and row["waiting_messages"][0]["sender"] == f"agent:{identity('lead')}" and row["waiting_messages"][0]["text"] == TEXT["p3b"], "replaced real parent slot missing")
    ev = events("child")
    p2_delivery = delivered(ev, TEXT["p2"], f"agent:{identity('lead')}")
    waits = [e for e in ev if (e["kind"] == "message.waiting" and e["seq"] > p2_delivery["seq"] and
             e["payload"].get("reason") == f"agent:{identity('lead')}")]
    demand(len(waits) == 2 and waits[0]["seq"] < waits[1]["seq"], "parent replacement lacks two saved sends")
    save("first-parent-proof", {"event_ids": [e["event_id"] for e in waits], "seq": waits[1]["seq"],
                                "turn": row["running_turn_id"], "replaced": True})
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
    elif cmd == "lead-ui": lead_ui(sys.argv[2])
    elif cmd == "sidebar": sidebar(sys.argv[2])
    elif cmd == "child-selected": child_selected()
    elif cmd == "working": working_fallback(sys.argv[2])
    elif cmd == "shot": shot(sys.argv[2], sys.argv[3:])
    elif cmd == "cleanup": cleanup()
    else: raise SystemExit(f"unknown command: {cmd}")


if __name__ == "__main__":
    main()
