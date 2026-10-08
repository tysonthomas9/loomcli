#!/usr/bin/env python3
"""Read-only oracles for run-owned real child activity journeys."""

import base64
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import time
from urllib.parse import quote, urlsplit
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
        prior = load(parent_label + "-precreate-ref")
        actual = read_ref(row, prior["head"])
        assert_child_ancestry(prior, parent, row, actual)
        save(label + "-create-ref", {"parent_head_before": prior["head"], "parent_branch": prior["branch"],
                                     "child_head_after": actual["head"], "child_branch": actual["branch"],
                                     "merge_base": actual["merge_base"],
                                     "parent_worktree": prior["worktree"], "child_worktree": row["worktree_path"]})
    else:
        actual = read_ref(row)
        save(label + "-precreate-ref", {"head": actual["head"], "branch": actual["branch"],
                                        "worktree": row["worktree_path"]})
    if parent:
        created = [e for e in events(parent["agent_id"])
                   if e["kind"] == "child.created" and e["payload"].get("child") == row["agent_id"]]
        assert len(created) == 1, "child was not created by the Lead tool"
        tools = [e for e in events(parent["agent_id"]) if calls_operation(e, "agent_create")]
        assert any(name in str(e["payload"]["tool"].get("input", "")) for e in tools)
    print(row["agent_id"])


def native_session(lead):
    helper = Path(os.environ["AFT_TESTS_DIR"]) / "scripts/agent-flows-native-session.sh"
    assert os.environ["AFT_NATIVE_SESSION_PROBE"] == str(helper), "native identity probe path mismatch"
    return json.loads(subprocess.check_output(["bash", str(helper), lead["agent_id"]], text=True))


def inventory_result(lead, child, history, native):
    """Require Code Mode's own namespace enumeration, not a model's narration."""
    lead_id = lead["agent_id"]
    owned(lead, lead["name"])
    owned(child, child["name"], lead)
    assert child["parent_agent_id"] == lead_id and child["created_by_id"] == lead_id
    assert native.get("agent_id") == lead_id and native.get("harness") == lead["harness"] == "opencode", \
        "native identity does not match public Lead"
    native_id = native.get("native_id")
    native_root = native.get("native_root")
    assert isinstance(native_id, str) and native_id and isinstance(native_root, str)

    def native_tool(event):
        if event.get("agent_id") != lead_id or event.get("kind") != "item.completed":
            return None
        payload = event.get("payload") or {}
        if payload.get("itemKind") != "tool" or payload.get("session") != native_id:
            return None
        item_id = payload.get("itemId")
        if not isinstance(item_id, str) or not item_id:
            return None
        if event.get("event_id") != f"item.completed:{native_root}:{native_id}:{item_id}":
            return None
        tool = payload.get("tool")
        if not isinstance(tool, dict) or tool.get("failed", False) is not False:
            return None
        return tool

    creates = [event for event in history if native_tool(event) and
               calls_operation(event, "agent_create") and
               child["name"] in str(native_tool(event).get("input", ""))]
    assert len(creates) == 1, "expected one owned completed native child create"
    create = creates[0]
    turn_id = create.get("turn_id")
    assert isinstance(turn_id, str) and turn_id
    starts = [event for event in history if event.get("kind") == "turn.started" and
              event.get("agent_id") == lead_id and event.get("turn_id") == turn_id and
              event.get("payload", {}).get("session") == native_id and
              event.get("event_id") == f"turn.started:{native_root}:{native_id}:{turn_id}"]
    assert len(starts) == 1 and starts[0]["seq"] < create["seq"], "native delegation turn is unbound"
    created = [event for event in history if event.get("kind") == "child.created" and
               event.get("agent_id") == lead_id and
               event.get("payload", {}).get("child") == child["agent_id"] and
               event.get("event_id") == f"child.created:{child['agent_id']}"]
    assert len(created) == 1, "missing exact saved child creation"

    inventory = []
    for event in history:
        tool = native_tool(event)
        if not tool or tool.get("name") != "execute":
            continue
        try:
            raw_input = json.loads(tool.get("input", ""))
        except (TypeError, ValueError):
            continue
        if not isinstance(raw_input, dict) or set(raw_input) != {"code"} or \
                not isinstance(raw_input["code"], str) or \
                raw_input["code"].strip().removesuffix(";").strip() != "return Object.keys(tools.loom)":
            continue
        inventory.append((event, tool, raw_input["code"]))
    assert len(inventory) == 1, "missing or repeated native Loom namespace enumeration"
    event, tool, code = inventory[0]
    assert event["turn_id"] == turn_id and starts[0]["seq"] < event["seq"] < created[0]["seq"] < create["seq"], \
        "inventory did not precede creation in the same owned native turn"
    try:
        names = json.loads(tool.get("output", ""))
    except (TypeError, ValueError) as exc:
        raise AssertionError("native inventory output is not a JSON name array") from exc
    expected = {"agent_create", "agent_list", "agent_get", "agent_send", "agent_archive", "github_read"}
    assert isinstance(names, list) and len(names) == len(expected) and \
        all(isinstance(name, str) for name in names) and set(names) == expected, \
        "installed Loom namespace differs from the Lead bridge contract"
    return {"lead": lead_id, "child": child["agent_id"], "turn_id": turn_id,
            "native_session_id": native_id, "native_root": native_root,
            "event_id": event["event_id"], "item_id": event["payload"]["itemId"],
            "input": code, "installed_names": sorted(names),
            "create_event_id": create["event_id"], "child_created_event_id": created[0]["event_id"]}


def inventory(lead_label, child_label):
    lead = agent(load(lead_label)["agent_id"])
    child = agent(load(child_label)["agent_id"])
    proof = inventory_result(lead, child, events(lead["agent_id"]), native_session(lead))
    save("installed-loom-tools", proof)


def read_ref(row, parent_head=""):
    helper = Path(os.environ["AFT_TESTS_DIR"]) / "scripts/coverage-children-activity-ref.sh"
    args = ["bash", str(helper), row["agent_id"], row["name"]]
    if parent_head:
        args.append(parent_head)
    actual = json.loads(subprocess.check_output(args, text=True))
    assert actual["agent_id"] == row["agent_id"]
    return actual


def assert_child_ancestry(prior, parent, child, actual):
    assert re.fullmatch(r"[a-f0-9]{40}", prior["head"]) and re.fullmatch(r"[a-f0-9]{40}", actual["head"])
    assert prior["worktree"] == parent["worktree_path"]
    assert prior["branch"] == parent["branch"] and actual["branch"] == child["branch"], "actual pre-create branch or child worktree branch mismatch"
    assert actual["merge_base"] == prior["head"], "child base does not descend from pre-create parent HEAD"


def tool_name(event):
    p = event["payload"]
    if event["kind"] != "item.completed" or p.get("itemKind") != "tool":
        return None
    name = p.get("tool", {}).get("name", "")
    for tool in ("agent_create", "agent_send", "agent_get", "agent_archive"):
        if calls_operation(event, tool):
            return tool
    return name


def tool_code(tool):
    raw = tool.get("input")
    if isinstance(raw, str):
        try:
            raw = json.loads(raw)
        except json.JSONDecodeError:
            return raw
    if isinstance(raw, dict) and isinstance(raw.get("code"), str):
        return raw["code"]
    return ""


def calls_operation(event, operation):
    if event["kind"] != "item.completed" or event["payload"].get("itemKind") != "tool":
        return False
    tool = event["payload"].get("tool") or {}
    return tool.get("name", "").endswith(operation) or bool(re.search(
        r"\btools\.loom\." + re.escape(operation) + r"\s*\(", tool_code(tool)))


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


def js_truncate(value, limit):
    """Match the tray's UTF-16 slice and ellipsis limit."""
    units = value.encode("utf-16-le", "surrogatepass")
    return value if len(units) // 2 <= limit else \
        units[:(limit - 1) * 2].decode("utf-16-le", "surrogatepass") + "…"


def reasoning_first_line(text):
    """Mirror AgentChat timelineRows.firstLine for the saved reasoning title."""
    line = re.sub(r"^#{1,6}\s+", "", text.strip().split("\n", 1)[0])
    while True:
        plain = re.sub(r"(\*\*|__|\*|_|`)(.+?)\1", r"\2", line)
        if plain == line:
            break
        line = plain
    return js_truncate(line.strip(), 120)


def step_prefix(event):
    payload = event["payload"]
    if event["kind"] != "item.completed":
        return None
    if payload.get("itemKind") == "reasoning":
        line = reasoning_first_line(payload.get("text") or "")
        if not line:
            return None
        return js_truncate(f"💭 Thinking · {line}", 60)
    if payload.get("itemKind") != "tool":
        return None
    name = (payload.get("tool") or {}).get("name", "").strip()
    short = name.split("/")[-1]
    if re.search(r"(?:^|[^a-z])execute$", name, re.I):
        action = "Ran code"
    elif re.fullmatch(r"read|view|cat|read_?file|notebook_?read|image_?view", short, re.I):
        action = "Read file"
    elif re.fullmatch(r"edit|write|multi_?edit|patch|apply_?patch|str_?replace\w*|notebook_?edit|create_?file", short, re.I):
        action = "Changed file"
    elif re.fullmatch(r"bash|shell|command|exec(_?command)?|terminal|run\w*", short, re.I):
        action = "Ran command"
    elif re.fullmatch(r"grep|glob|find|ls|list|codesearch|search_?code|rg", short, re.I):
        action = "Searched code"
    elif re.fullmatch(r"web_?search|web_?fetch|fetch|browse", short, re.I):
        action = "Searched the web"
    else:
        action = name[:1].upper() + name[1:] if name else "Tool call"
    label = f"▸ {action}"
    return f"{label[:59]}…" if len(label) > 60 else label


def current_preview_event(history, child_id, turn_id, visible):
    assert child_id and turn_id
    steps = [event for event in history if event.get("agent_id") == child_id and
             event.get("turn_id") == turn_id and step_prefix(event)]
    assert steps, "no saved current-turn step matches the exact visible child preview"
    latest = max(steps, key=lambda event: event["seq"])
    assert visible(step_prefix(latest)), "latest saved current-turn step does not match the exact visible child preview"
    return latest


def visible_step(child_id, prefix):
    script = """(() => {
      const id = %s, prefix = %s;
      const row = Array.from(document.querySelectorAll('[data-testid=agent-tray] [data-tray-row]'))
        .find(x => x.dataset.trayRow === id);
      const line = row?.querySelector('[data-status=running] > span:last-child')?.textContent?.trim() || '';
      return !!row?.getClientRects().length && line.startsWith(prefix) &&
        (line.length === prefix.length || line.slice(prefix.length).startsWith(' · '));
    })()""" % (json.dumps(child_id), json.dumps(prefix))
    output = subprocess.check_output(["agent-browser", "--session", os.environ["AFT_SESSION"], "eval", script], text=True)
    return bool(re.search(r"\btrue\s*$", output))


def activity(child_label):
    child = load(child_label)
    current = agent(child["agent_id"])
    assert current["state"] in ("active", "waiting") and current["running_turn_id"]
    last = current_preview_event(events(child["agent_id"]), child["agent_id"], current["running_turn_id"],
                                 lambda prefix: visible_step(child["agent_id"], prefix))
    save("activity", {"child": child["agent_id"], "event_id": last["event_id"],
                      "turn_id": last["turn_id"], "item_kind": last["payload"]["itemKind"],
                      "tool_name": last["payload"].get("tool", {}).get("name"),
                      "visible_prefix": step_prefix(last)})


def browser(*args):
    return subprocess.check_output(["agent-browser", "--session", os.environ["AFT_SESSION"], *args], text=True)


def browser_json(script):
    raw = browser("eval", "-b", base64.b64encode(script.encode()).decode()).strip()
    for _ in range(2):
        value = json.loads(raw)
        if not isinstance(value, str) or not value.startswith(("{", "[", '"')):
            return value
        raw = value
    raise AssertionError("browser did not return JSON geometry")


def native_create_tool_count(history):
    count = 0
    for event in history:
        if event["kind"] != "item.completed" or event["payload"].get("itemKind") != "tool":
            continue
        tool = event["payload"].get("tool") or {}
        raw = tool.get("input")
        input_text = raw if isinstance(raw, str) else json.dumps(raw or {})
        try:
            json_object = isinstance(json.loads(input_text), dict)
        except json.JSONDecodeError:
            json_object = False
        is_execute = bool(re.search(r"(?:^|[^a-z])execute$", tool.get("name", "").strip(), re.I))
        loom_marker = bool(re.search(r"['\"`]loom\b|\bloom\.", input_text))
        named_create = bool(re.search(r"\bagent_create\b", input_text))
        truncated_fallback = not json_object and not re.search(r"tools\.loom\.\w+\s*\(", input_text)
        is_create = calls_operation(event, "agent_create") or (
            is_execute and loom_marker and (named_create or truncated_fallback))
        if is_create and not tool.get("failed"):
            count += 1
    return count


def assert_started_snapshot(snapshot, ids, names, native_count):
    assert not snapshot.get("missing") and snapshot["markerCount"] >= 1
    assert len(ids) == len(names) == len(snapshot["ids"]) == len(snapshot["names"]) == len(snapshot["colors"]), \
        "Started child ID/name/color counts differ"
    assert len(set(ids)) == len(ids) and len(set(snapshot["ids"])) == len(ids), "Started child IDs repeated"
    assert dict(zip(snapshot["ids"], snapshot["names"])) == dict(zip(ids, names)), \
        "Started chips must pair exact saved child IDs and full names"
    assert native_count >= 1, "no saved native create tool entries for the children"
    assert snapshot["toolCount"] == native_count, "Started tool count differs from saved native tool entries"
    assert snapshot["rawCode"] is False, "collapsed Started marker leaked raw bridge input"
    assert all(snapshot["colors"]), "Started child badge lacks its stable color"


def started_ui(lead_label, *child_labels):
    lead = load(lead_label)
    kids = [load(label) for label in child_labels]
    history = events(lead["agent_id"])
    ids = [kid["agent_id"] for kid in kids]
    assert len(ids) == len(set(ids))
    created_ids = [e["payload"].get("child") for e in history if e["kind"] == "child.created"]
    assert sorted(created_ids) == sorted(ids), "saved child.created IDs differ from the Started children"
    shot = browser_json("""JSON.stringify((() => {
      const markers=Array.from(document.querySelectorAll('[data-testid=started-marker]'));
      const links=markers.flatMap(m=>Array.from(m.querySelectorAll('a[href]')));
      const ids=links.map(a=>decodeURIComponent(a.getAttribute('href')?.split('/').pop()||''));
      const names=links.map(a=>a.lastChild?.textContent?.trim()||'');
      const colors=links.map(a=>a.querySelector('[data-agent-color]')?.getAttribute('data-agent-color')||'');
      const toolCount=markers.reduce((n,m)=>n+Number(/^([0-9]+) tool calls?$/.exec(m.querySelector('button[aria-expanded]')?.textContent?.trim().replace(/›/g,'').trim()||'')?.[1]||0),0);
      return {markerCount:markers.length,ids,names,colors,toolCount,
        rawCode:markers.some(m=>/\\{\\s*["'](?:name|brief|agent_id)["']\\s*:|tools\\.loom\\.agent_create\\s*\\(/.test(m.textContent||'')),
        expanded:markers.map(m=>m.querySelector('button[aria-expanded]')?.getAttribute('aria-expanded')||'')};
    })())""")
    tool_count = native_create_tool_count(history)
    assert_started_snapshot(shot, ids, [kid["name"] for kid in kids], tool_count)
    colors_by_id = dict(zip(shot["ids"], shot["colors"]))
    for kid in kids:
        color = colors_by_id[kid["agent_id"]]
        previous = OUT / f"color-{kid['agent_id']}.json"
        if previous.exists():
            assert load(f"color-{kid['agent_id']}")["value"] == color
        else:
            save(f"color-{kid['agent_id']}", {"value": color})
    browser("screenshot", str(OUT / f"started-{lead_label}.png"))
    save(f"started-{lead_label}", {"created_ids": created_ids, "native_tool_entries": tool_count, "ui": shot})


def expanded_bridge(lead_label):
    shot = browser_json("""JSON.stringify((() => {
      const m=document.querySelector('[data-testid=started-marker]');
      const rows=[];
      for(let li=m?.closest('li')?.nextElementSibling; li?.dataset.kind==='work'; li=li.nextElementSibling) {
        const row=li.querySelector('[data-testid=tool-call][data-in-group=true]');
        if(!row) break;
        rows.push(row);
      }
      return {expanded:m?.querySelector('button[aria-expanded]')?.getAttribute('aria-expanded'),
        rows:rows.map(x=>x.querySelector('[role=button]')?.textContent?.trim()||x.textContent?.trim()||''),
        raw:rows.some(x=>/\\{\\s*["'](?:name|brief|agent_id)["']\\s*:/.test(x.textContent||''))};
    })())""")
    expanded_bridge_ok(shot, load(f"started-{lead_label}")["native_tool_entries"])
    browser("screenshot", str(OUT / f"expanded-{lead_label}.png"))
    save(f"expanded-{lead_label}", shot)


def expanded_bridge_ok(shot, expected_count):
    assert shot["expanded"] == "true" and len(shot["rows"]) == expected_count
    assert any("Started" in row for row in shot["rows"]), "expanded Started calls lack the delegation label"
    assert all(row and not re.search(r"\btools\.loom\.|\bsearch\(", row) for row in shot["rows"])
    assert shot["raw"] is False, "expanded bridge exposed raw tool input"


def working_fallback(child_label, stage):
    child = load(child_label)
    live = agent(child["agent_id"])
    shot = browser_json("""JSON.stringify((() => {
      const id=%s;
      const row=document.querySelector('[data-testid=agent-tray] li[data-tray-row="'+id+'"]');
      return {id:row?.dataset.trayRow||'',running:!!row?.querySelector('[data-status=running]'),
        result:row?.querySelector('[data-status=running] > span:last-child')?.textContent?.trim()||''};
    })())""" % json.dumps(child["agent_id"]))
    observed = (live["state"] == "active" and bool(live["running_turn_id"]) and
                shot["id"] == child["agent_id"] and shot["running"] and shot["result"] == "Working…")
    if observed:
        current_steps = [e for e in events(child["agent_id"]) if e.get("turn_id") == live["running_turn_id"] and step_prefix(e)]
        assert not current_steps, "Working fallback shown despite a saved current-turn step"
        browser("screenshot", str(OUT / f"working-{stage}-{child_label}.png"))
    save(f"working-{stage}-{child_label}", {"observed": observed, "api_state": live["state"],
                                               "turn_id": live["running_turn_id"], "ui": shot})


def card_snapshot_ok(snapshot, children):
    expected = [(kid["agent_id"], kid["name"], attempt) for kid, attempt in children]
    assert len(snapshot["cards"]) == len(expected)
    assert not snapshot["rawBubble"], "raw task_completed surfaced as a user message"
    for kid, attempt in children:
        color = load(f"color-{kid['agent_id']}")["value"]
        matches = [c for c in snapshot["cards"] if c["name"] == kid["name"] and
                   c["id"] == kid["agent_id"] and
                   c["attempt"] == str(attempt) and c["color"] == color and
                   c["outcome"] == "completed" and c["delivery"] == "delivered"]
        assert len(matches) == 1, f"exact completed card missing or duplicated for {kid['agent_id']} attempt {attempt}"


def focus_exact_card(name, attempt):
    browser("wait", "--fn", "Array.from(document.querySelectorAll('[data-testid=completion-record]'))" +
            ".some(c=>c.dataset.attempt===" + json.dumps(str(attempt)) +
            " && c.querySelector(':scope > span:nth-child(2)')?.textContent?.trim()===" +
            json.dumps(name) + ")")
    script = """(() => {
      const name=%s, attempt=%s;
      const cards=Array.from(document.querySelectorAll('[data-testid=completion-record]'))
        .filter(c=>c.dataset.attempt===attempt &&
          c.querySelector(':scope > span:nth-child(2)')?.textContent?.trim()===name);
      if(cards.length!==1) throw Error('exact card name and attempt missing or duplicated');
      cards[0].scrollIntoView({block:'center'});
      cards[0].focus();
      const box=cards[0].getBoundingClientRect();
      return {path:location.pathname,href:location.href,readyState:document.readyState,
        count:cards.length,name:cards[0].querySelector(':scope > span:nth-child(2)')?.textContent?.trim()||'',
        attempt:cards[0].dataset.attempt,color:cards[0].dataset.agentColor,
        outcome:cards[0].dataset.outcome,delivery:cards[0].dataset.delivery,
        role:cards[0].getAttribute('role'),tabIndex:cards[0].tabIndex,
        focused:document.activeElement===cards[0],
        visible:box.width>0 && box.height>0 && box.top>=0 && box.bottom<=innerHeight,
        bounds:{top:box.top,bottom:box.bottom,height:box.height}};
    })()""" % (json.dumps(name), json.dumps(str(attempt)))
    shot = browser_json(f"JSON.stringify({script})")
    assert shot["count"] == 1 and shot["name"] == name and shot["attempt"] == str(attempt)
    assert shot["role"] == "link" and shot["tabIndex"] == 0 and shot["focused"] and shot["visible"]
    return shot


def card_route_ok(shot, agent_id, name):
    route = f"/ws/{quote(WS, safe='')}/chat/{quote(agent_id, safe='')}"
    assert shot["path"] == route and shot["name"] == name and shot["harness"] == "opencode", "card keyboard navigation did not hydrate the exact saved agent Chat"


def card_route():
    return browser_json("""JSON.stringify((() => ({path:location.pathname,href:location.href,
      readyState:document.readyState,
      name:document.querySelector('section[aria-label="Agent chat"] header h2')?.textContent?.trim()||'',
      harness:document.querySelector('section[aria-label="Agent chat"] header [data-testid=harness-label]')?.textContent?.trim()||''}))())""")


def cards_ui(lead_label, *specs):
    lead = load(lead_label)
    children = []
    ev = events(lead["agent_id"])
    for spec in specs:
        label, attempt = spec.split(":")
        kid = load(label)
        attempt = int(attempt)
        assert len([e for e in completions(ev, kid["agent_id"]) if
                    e["event_id"] == f"task_completed:{kid['agent_id']}:{attempt}"]) == 1
        assert len(deliveries(ev, kid["agent_id"], attempt)) == 1
        children.append((kid, attempt))
    shot = browser_json("""JSON.stringify((() => ({
      cards:Array.from(document.querySelectorAll('[data-testid=completion-record]')).map(c=>({
        name:c.querySelector(':scope > span:nth-child(2)')?.textContent?.trim()||'',
        attempt:c.dataset.attempt,color:c.dataset.agentColor,
        outcome:c.dataset.outcome,delivery:c.dataset.delivery})),
      rawBubble:Array.from(document.querySelectorAll('[data-testid=chat-transcript] > li[data-kind=user]'))
        .some(x=>/task_completed:[^\\s]+:[0-9]+/.test(x.textContent||''))
    }))())""")
    save(f"cards-initial-{lead_label}", {"lead": lead["agent_id"],
                                          "expected_children": [{"id": kid["agent_id"], "name": kid["name"], "attempt": attempt}
                                                                for kid, attempt in children], "ui": shot})
    for kid, attempt in children:
        focused = focus_exact_card(kid["name"], attempt)
        tab_before = sidebar_tab()
        save(f"card-focus-{lead_label}-{kid['agent_id']}-{attempt}", {"expected_id": kid["agent_id"],
                                                                      "tab": tab_before, "ui": focused})
        assert focused["path"] == f"/ws/{quote(WS, safe='')}/chat/{quote(lead['agent_id'], safe='')}"
        assert tab_before["url_path"] == focused["path"]
        browser("screenshot", str(OUT / f"card-focus-{lead_label}-{kid['agent_id']}-{attempt}.png"))
        browser("press", "Enter")
        expected_route = f"/ws/{quote(WS, safe='')}/chat/{quote(kid['agent_id'], safe='')}"
        try:
            browser("wait", "--fn", "location.pathname === " + json.dumps(expected_route) +
                    " && document.querySelector('section[aria-label=\"Agent chat\"] header h2')?.textContent?.trim() === " +
                    json.dumps(kid["name"]))
        except subprocess.CalledProcessError:
            save(f"card-navigation-failed-{kid['agent_id']}-{attempt}", card_route())
            raise
        navigated = card_route()
        tab_child = sidebar_tab()
        save(f"card-navigation-{kid['agent_id']}-{attempt}", {"tab": tab_child, "ui": navigated})
        assert tab_child["target_id"] == tab_before["target_id"] and tab_child["url_path"] == expected_route
        card_route_ok(navigated, kid["agent_id"], kid["name"])
        card = next(c for c in shot["cards"] if c["name"] == kid["name"] and c["attempt"] == str(attempt))
        card["id"] = kid["agent_id"]
        browser("open", os.environ["AFT_BASE_URL"].rstrip("/") + f"/ws/{WS}/chat/{lead['agent_id']}")
        lead_route = f"/ws/{quote(WS, safe='')}/chat/{quote(lead['agent_id'], safe='')}"
        browser("wait", "--fn", "location.pathname === " + json.dumps(lead_route) +
                " && document.querySelector('section[aria-label=\"Agent chat\"] header h2')?.textContent?.trim() === " +
                json.dumps(lead["name"]))
        returned = card_route()
        tab_return = sidebar_tab()
        save(f"card-return-{kid['agent_id']}-{attempt}", {"tab": tab_return, "ui": returned})
        assert tab_return["target_id"] == tab_before["target_id"] and tab_return["url_path"] == lead_route
        card_route_ok(returned, lead["agent_id"], lead["name"])
    card_snapshot_ok(shot, children)
    browser("screenshot", str(OUT / f"cards-{lead_label}-{'-'.join(specs)}.png"))
    save(f"cards-{lead_label}-{'-'.join(specs)}", shot)


def theme_cards(lead_label, *child_labels):
    original = browser_json("JSON.stringify({theme:document.documentElement.dataset.theme})")["theme"]
    assert original in ("dark", "light")
    kids = [load(label) for label in child_labels]
    for theme in ("dark", "light"):
        current = browser_json("JSON.stringify({theme:document.documentElement.dataset.theme})")["theme"]
        if current != theme:
            browser("click", f'button[aria-label="Switch to {theme} mode"]')
        browser("wait", "--fn", f'document.documentElement.dataset.theme === {json.dumps(theme)}')
        shot = browser_json("""JSON.stringify(Array.from(document.querySelectorAll('[data-testid=completion-record]'))
          .map(c=>({text:c.textContent||'',color:c.dataset.agentColor,attempt:c.dataset.attempt})))""")
        assert len(shot) == len(kids)
        for kid in kids:
            expected = load(f"color-{kid['agent_id']}")["value"]
            assert len([c for c in shot if kid["name"] in c["text"] and c["color"] == expected and c["attempt"] == "0"]) == 1
        browser("screenshot", str(OUT / f"cards-{lead_label}-{theme}.png"))
        save(f"cards-{lead_label}-{theme}", {"theme": theme, "cards": shot})
    current = browser_json("JSON.stringify({theme:document.documentElement.dataset.theme})")["theme"]
    if current != original:
        browser("click", f'button[aria-label="Switch to {original} mode"]')
        browser("wait", "--fn", f'document.documentElement.dataset.theme === {json.dumps(original)}')


def sidebar_child(parent_label, child_label, stage):
    parent, child = load(parent_label), load(child_label)
    live = agent(child["agent_id"])
    assert live["parent_agent_id"] == parent["agent_id"]
    shot = browser_json("""JSON.stringify((() => {
      const parent=%s, id=%s;
      const group=Array.from(document.querySelectorAll('nav[aria-label=Agents] [role=group]'))
        .find(x=>x.getAttribute('aria-label')===parent+' children');
      const matches=Array.from(document.querySelectorAll('nav[aria-label=Agents] a[href]'))
        .filter(x=>decodeURIComponent(x.getAttribute('href')?.split('/').pop()||'')===id);
      const link=matches[0];
      const avatar=link?.querySelector('[data-agent-color][data-dot]');
      return {parentGroup:!!group && !!link && group.contains(link),
        count:Array.from(group?.querySelectorAll('a[href]')||[]).length,
        exactIdCount:matches.length,
        id:link?decodeURIComponent(link.getAttribute('href').split('/').pop()):'',
        name:link?.querySelector('[data-testid=agent-list-name]')?.textContent?.trim()||'',
        logo:!!link?.querySelector('[role=img][aria-label=opencode]'),
        dot:avatar?.dataset.dot||'',color:avatar?.dataset.agentColor||'',
        selected:link?.getAttribute('aria-current')==='page'};
    })())""" % (json.dumps(parent["name"]), json.dumps(child["agent_id"])))
    expected_dot = {"creating": "working", "active": "working", "stopping": "working",
                    "waiting": "waiting", "finished": "done"}.get(live["state"])
    if stage == "reactivated":
        checkpoint = load("reactivation-checkpoint")
        assert checkpoint["stage"] == "running"
        assert checkpoint["event_id"] and checkpoint["turn_id"]
        assert reactivation_checkpoint_ok(checkpoint["ui"], child)["nested"]
        if live["state"] == "finished":
            browser("screenshot", str(OUT / f"sidebar-{stage}-{child_label}.png"))
            save(f"sidebar-{stage}-{child_label}", {"api_state": live["state"], "ui": shot,
                                                    "running_witness_id": checkpoint["event_id"]})
            return
    assert_sidebar_exact_link(shot, child["agent_id"])
    assert shot["name"] == child["name"] and shot["logo"] and shot["dot"] == expected_dot
    assert shot["color"] == load(f"color-{child['agent_id']}")["value"]
    if stage == "finished-open":
        assert live["state"] == "finished" and shot["selected"]
    if stage == "reactivated":
        assert live["state"] in ("active", "waiting")
    browser("screenshot", str(OUT / f"sidebar-{stage}-{child_label}.png"))
    save(f"sidebar-{stage}-{child_label}", {"api_state": live["state"], "ui": shot})


def assert_sidebar_exact_link(shot, child_id):
    assert shot["parentGroup"] and shot["exactIdCount"] == 1 and shot["id"] == child_id, "sidebar child ID link missing or duplicated"


def archive_state(label, expected):
    row = agent(load(label)["agent_id"])
    assert (row["state"] == "archived") == (expected == "archived")
    save(f"archive-{label}-{expected}", {"id": row["agent_id"], "state": row["state"]})


def hover_archive(label):
    row = load(label)
    target = f'nav[aria-label=Agents] a[href$="/{row["agent_id"]}"]'
    browser("hover", target)
    js = """(() => {const id=%s;const link=document.querySelector('nav[aria-label=Agents] a[href$="/'+id+'"]');
      const button=link?.parentElement?.querySelector('[data-testid=agent-row-archive]');
      return !!link && getComputedStyle(link).textDecorationLine==='none' && !!button &&
        button.getAttribute('aria-label')===%s && Number(getComputedStyle(button).opacity)>0.9;
    })()""" % (json.dumps(row["agent_id"]), json.dumps("Archive " + row["name"]))
    browser("wait", "--fn", js)
    browser("screenshot", str(OUT / f"hover-archive-{label}.png"))
    save(f"hover-archive-{label}", {"id": row["agent_id"], "name": row["name"], "no_underline": True, "archive_visible": True})


def keyboard_card(lead_label, child_label, key):
    assert key in ("Enter", "Space")
    lead, child = load(lead_label), load(child_label)
    focused = focus_exact_card(child["name"], 0)
    tab_before = sidebar_tab()
    save(f"keyboard-focus-{key}-{child_label}", {"expected_id": child["agent_id"],
                                                "tab": tab_before, "ui": focused})
    assert focused["path"] == f"/ws/{quote(WS, safe='')}/chat/{quote(lead['agent_id'], safe='')}"
    assert tab_before["url_path"] == focused["path"]
    browser("screenshot", str(OUT / f"keyboard-focus-{key}-{child_label}.png"))
    browser("press", key)
    child_route = f"/ws/{quote(WS, safe='')}/chat/{quote(child['agent_id'], safe='')}"
    browser("wait", "--fn", "location.pathname === " + json.dumps(child_route) +
            " && document.querySelector('section[aria-label=\"Agent chat\"] header h2')?.textContent?.trim() === " +
            json.dumps(child["name"]))
    navigated = card_route()
    tab_child = sidebar_tab()
    save(f"keyboard-navigation-{key}-{child_label}", {"tab": tab_child, "ui": navigated})
    assert tab_child["target_id"] == tab_before["target_id"] and tab_child["url_path"] == child_route
    card_route_ok(navigated, child["agent_id"], child["name"])
    browser("screenshot", str(OUT / f"keyboard-{key}-{child_label}.png"))
    browser("open", os.environ["AFT_BASE_URL"].rstrip("/") + f"/ws/{WS}/chat/{lead['agent_id']}")
    lead_route = f"/ws/{quote(WS, safe='')}/chat/{quote(lead['agent_id'], safe='')}"
    browser("wait", "--fn", "location.pathname === " + json.dumps(lead_route) +
            " && document.querySelector('section[aria-label=\"Agent chat\"] header h2')?.textContent?.trim() === " +
            json.dumps(lead["name"]))
    returned = card_route()
    tab_return = sidebar_tab()
    save(f"keyboard-return-{key}-{child_label}", {"tab": tab_return, "ui": returned})
    assert tab_return["target_id"] == tab_before["target_id"] and tab_return["url_path"] == lead_route
    card_route_ok(returned, lead["agent_id"], lead["name"])


def mobile_geometry_ok(geometry, child_id, width, theme):
    assert geometry["width"] == width and geometry["height"] == 844
    assert geometry["theme"] == theme and geometry["navPosition"] == "fixed"
    assert geometry["trayOpen"] and geometry["childId"] == child_id
    assert geometry["headerExpanded"] and geometry["rowIds"].count(child_id) == 1
    assert geometry["rowCount"] > 0 and geometry["visibleRowCount"] > 0
    assert geometry["childWhole"], "exact saved child row is not wholly visible"
    assert geometry["partialRows"] == 0, "a visible child row is clipped"
    assert geometry["hiddenRows"] == 0 or geometry["moreCount"] >= geometry["hiddenRows"], "hidden rows lack the More count"
    assert geometry["horizontalOverflow"] <= 1, "mobile tray or document overflows horizontally"
    assert geometry["maxControlBottom"] <= geometry["composerTop"] + 1, "tray control overlaps usable composer"
    assert geometry["composerBottom"] <= geometry["navTop"] + 1, "composer overlaps fixed mobile navigation"
    assert geometry["headerHit"]["hit"], "tray header is hidden or occluded"
    assert geometry["childLinkHit"]["hit"], "exact child Open link is hidden or occluded"


def mobile(child_label, lead_label, width, theme):
    width = int(width)
    assert width in (390, 557) and theme in ("dark", "light")
    if width == 390 and theme == "dark":
        save("mobile-original", browser_json("JSON.stringify({width:innerWidth,height:innerHeight,theme:document.documentElement.dataset.theme})"))
    child, lead = load(child_label), load(lead_label)
    child_id = child["agent_id"]
    live = agent(child_id)
    assert live["name"] == child["name"] and live["parent_agent_id"] == lead["agent_id"]
    assert live["worktree_path"] == child["worktree_path"]
    lead_events = events(lead["agent_id"])
    created = [e for e in lead_events if e["kind"] == "child.created" and e["payload"].get("child") == child_id]
    assert len(created) == 1, "mobile row has no real Lead-created child receipt"
    activity_proof = load("activity")
    assert activity_proof["child"] == child_id
    receipts = [e for e in events(child_id) if e["event_id"] == activity_proof["event_id"] and
                e["turn_id"] == activity_proof["turn_id"]]
    assert len(receipts) == 1, "mobile row has no saved current-turn activity receipt"
    assert live["running_turn_id"] or live["state"] == "finished"
    browser("set", "viewport", str(width), "844")
    current_theme = browser_json("JSON.stringify({value:document.documentElement.dataset.theme})")["value"]
    if current_theme != theme:
        browser("click", f'button[aria-label="Switch to {theme} mode"]')
    browser("wait", "--fn", f'document.documentElement.dataset.theme === {json.dumps(theme)}')
    open_state = browser_json("JSON.stringify({value:document.querySelector('[data-testid=agent-tray]')?.dataset.open || ''})")["value"]
    if open_state != "true":
        browser("click", "[data-testid=agent-tray] button[aria-expanded=false]")
    browser("wait", "--fn", "(() => { const id=" + json.dumps(child_id) + "; return !!document.querySelector('[data-testid=agent-tray][data-open=true] [data-tray-row=\"' + id + '\"]'); })()")
    geometry = browser_json("""JSON.stringify((() => {
      const id = %s;
      const tray = document.querySelector('[data-testid=agent-tray]');
      const header = tray?.querySelector('button[aria-expanded]');
      const list = tray?.querySelector('ul');
      const rows = Array.from(list?.querySelectorAll('li[data-tray-row]') || []);
      const composer = document.querySelector('section[aria-label="Agent chat"] form[data-chat-composer-form]');
      const nav = document.querySelector('nav[aria-label="Primary"]');
      if (!tray || !header || !list || !composer || !nav) return {missing:true};
      const box = el => { const r=el.getBoundingClientRect(); return {left:r.left,right:r.right,top:r.top,bottom:r.bottom,width:r.width,height:r.height}; };
      const clip = box(list), trayBox = box(tray), composerBox = box(composer), navBox = box(nav);
      const rowBoxes = rows.map(row => ({id:row.dataset.trayRow, ...box(row)}));
      const child = rows.find(row => row.dataset.trayRow === id);
      const childBox = child && box(child);
      const childWhole = !!childBox && childBox.left >= clip.left-1 && childBox.right <= clip.right+1 &&
        childBox.top >= clip.top-1 && childBox.bottom <= clip.bottom+1;
      const hit = el => {
        if (!el) return {x:null,y:null,hit:false,target:null};
        const r=el.getBoundingClientRect(), x=(r.left+r.right)/2, y=(r.top+r.bottom)/2;
        const target=document.elementFromPoint(x,y);
        return {x,y,hit:!!target && (target===el || el.contains(target)),target:target?.tagName || null};
      };
      const headerHit = hit(header), childLinkHit = hit(child?.querySelector('a[href]'));
      const visible = rowBoxes.filter(row => row.bottom > clip.top && row.top < clip.bottom);
      const partial = visible.filter(row => row.top < clip.top-1 || row.bottom > clip.bottom+1);
      const hidden = rowBoxes.filter(row => row.bottom <= clip.top || row.top >= clip.bottom);
      const moreText = Array.from(tray.querySelectorAll('div[aria-hidden=true]'))
        .find(x => /↓ [0-9]+ more/.test(x.textContent || ''))?.textContent || '';
      const moreCount = Number(/([0-9]+) more/.exec(moreText)?.[1] || 0);
      return {width:innerWidth,height:innerHeight,theme:document.documentElement.dataset.theme,
        navPosition:getComputedStyle(nav).position,trayOpen:tray.dataset.open==='true',
        childId:id,headerExpanded:header.getAttribute('aria-expanded')==='true',
        headerColors:Array.from(header.querySelectorAll('[data-agent-color]')).map(x=>x.dataset.agentColor),
        rowIds:rowBoxes.map(row=>row.id),rowCount:rows.length,visibleRowCount:visible.length,
        childWhole,headerHit,childLinkHit,
        partialRows:partial.length,hiddenRows:hidden.length,moreCount,
        horizontalOverflow:Math.max(document.documentElement.scrollWidth-innerWidth,
          tray.scrollWidth-tray.clientWidth,list.scrollWidth-list.clientWidth,
          ...rowBoxes.map(row=>Math.max(clip.left-row.left,row.right-clip.right))),
        maxControlBottom:Math.max(header.getBoundingClientRect().bottom,...visible.map(row=>row.bottom)),
        composerTop:composerBox.top,composerBottom:composerBox.bottom,navTop:navBox.top,
        tray:trayBox,clip,composer:composerBox,nav:navBox,rows:rowBoxes};
    })())""" % json.dumps(child_id))
    mobile_geometry_ok(geometry, child_id, width, theme)
    assert load(f"color-{child_id}")["value"] in geometry["headerColors"], "tray avatar changed child color"
    shot = OUT / f"mobile-tray-{width}-{theme}.png"
    browser("screenshot", str(shot))
    save(f"mobile-tray-{width}-{theme}", {"child": child_id, "parent": lead["agent_id"],
                                         "child_state": live["state"], "running_turn_id": live["running_turn_id"],
                                         "created_event_id": created[0]["event_id"],
                                         "child_receipt_ids": [e["event_id"] for e in receipts],
                                         "screenshot": str(shot), "geometry": geometry})


def mobile_restore():
    original = load("mobile-original")
    assert original["width"] > 0 and original["height"] > 0 and original["theme"] in ("dark", "light")
    browser("set", "viewport", str(original["width"]), str(original["height"]))
    current = browser_json("JSON.stringify({value:document.documentElement.dataset.theme})")["value"]
    if current != original["theme"]:
        browser("click", f'button[aria-label="Switch to {original["theme"]} mode"]')
    browser("wait", "--fn", f'document.documentElement.dataset.theme === {json.dumps(original["theme"])}')


def switched_ref(prior, second, original, actual):
    branch, head, changed = actual["branch"], actual["head"], actual["changed"]
    assert branch == f"cov-child-switched-{RUN}" and re.fullmatch(r"[0-9a-f]{40}", head)
    assert changed == [f"aft-child-fixtures/{RUN}/second.txt"], changed
    assert original and prior["branch"] == original, "first completion must report the assigned original branch"
    assert second["branch"] == original and branch != original, "CL3 must report the original branch and an actual distinct switched ref"
    assert not second["head"], "CL3 must not report an unowned head"


def arm_reactivation(lead_label, child_label):
    lead, child = load(lead_label), load(child_label)
    child_id = child["agent_id"]
    live = agent(child_id)
    assert live["parent_agent_id"] == lead["agent_id"] and live["state"] == "finished" and live["attempt"] == 0
    prior = load("first")
    assert prior["child"] == child_id and prior["attempt"] == 0
    last_seq = max(e["seq"] for e in events(child_id))
    route = f"/ws/{quote(WS, safe='')}/chat/{quote(lead['agent_id'], safe='')}"
    child_route = f"/ws/{quote(WS, safe='')}/chat/{quote(child_id, safe='')}"
    script = """JSON.stringify((() => {
      const id=%s, name=%s, parentName=%s, route=%s, childRoute=%s;
      window.__aftChildReactivation?.observer?.disconnect();
      const state={id,capture:null,observer:null};
      const sample=() => {
        if(state.capture || location.pathname!==route) return;
        const rows=Array.from(document.querySelectorAll('[data-testid=agent-tray] li[data-tray-row]'));
        const row=rows.find(r=>r.dataset.trayRow===id);
        const running=row?.querySelector('[data-status=running]');
        const chip=running?.querySelector('[data-chip=attempt]')?.textContent?.trim()||'';
        const result=running?.querySelector(':scope > span:last-child')?.textContent?.trim()||'';
        const elapsed=running?.querySelector(':scope > span:nth-child(3)')?.textContent?.trim()||'';
        const group=Array.from(document.querySelectorAll('nav[aria-label=Agents] [role=group]'))
          .find(x=>x.getAttribute('aria-label')===parentName+' children');
        const links=Array.from(document.querySelectorAll('nav[aria-label=Agents] a[href]'))
          .filter(x=>x.getAttribute('href')===childRoute);
        const link=links[0];
        const avatar=link?.querySelector('[data-agent-color][data-dot]');
        const nested=!!group && !!link && group.contains(link);
        if(rows.filter(r=>r.dataset.trayRow===id).length!==1 || !row?.getClientRects().length ||
           !row.textContent?.includes(name) || chip!=='attempt 2' || !/^(▸|💭)/.test(result) ||
           !/^\\d+m \\d{2}s$/.test(elapsed) || links.length!==1 || !nested ||
           !link.getClientRects().length || link.querySelector('[data-testid=agent-list-name]')?.textContent?.trim()!==name ||
           !link.querySelector('[role=img][aria-label=opencode]') ||
           !avatar?.dataset.agentColor || avatar.dataset.dot!=='working') return;
        state.capture={path:location.pathname,rowId:row.dataset.trayRow,rowVisible:true,running:true,
          attemptChip:chip,result,elapsed,childName:name,linkId:id,nested,
          exactLinkCount:links.length,logo:true,dot:'working',color:avatar.dataset.agentColor,capturedAt:Date.now()};
        state.observer.disconnect();
      };
      state.observer=new MutationObserver(sample);
      window.__aftChildReactivation=state;
      state.observer.observe(document.body,{subtree:true,childList:true,attributes:true,characterData:true});
      sample();
      return {armed:true,id:state.id,path:location.pathname,capturedBeforeSend:!!state.capture};
    })())""" % tuple(map(json.dumps, (child_id, child["name"], lead["name"], route, child_route)))
    armed = browser_json(script)
    assert armed == {"armed": True, "id": child_id, "path": route, "capturedBeforeSend": False}
    save("reactivation-armed", {"child": child_id, "parent": lead["agent_id"], "last_seq": last_seq, "ui": armed})


def open_reactivation_tray(lead_label, child_label):
    lead, child = load(lead_label), load(child_label)
    baseline = load("reactivation-armed")
    assert baseline["child"] == child["agent_id"] and baseline["parent"] == lead["agent_id"]
    live = agent(child["agent_id"])
    started = [e for e in events(child["agent_id"]) if e["kind"] == "turn.started" and
               e["seq"] > baseline["last_seq"] and e.get("turn_id") == live.get("running_turn_id")]
    route = f"/ws/{quote(WS, safe='')}/chat/{quote(lead['agent_id'], safe='')}"
    shot = browser_json("""JSON.stringify((() => {const tray=document.querySelector('[data-testid=agent-tray]');
      const header=tray?.querySelector('button[aria-expanded]');
      return {path:location.pathname,open:tray?.dataset.open||'',expanded:header?.getAttribute('aria-expanded')||'',
        header:header?.getAttribute('aria-label')||'',observerArmed:!!window.__aftChildReactivation?.observer,
        capturedBeforeClick:!!window.__aftChildReactivation?.capture};})())""")
    save("reactivation-tray-before-click", {"child": child["agent_id"], "api_state": live["state"],
                                            "api_attempt": live["attempt"], "running_turn_id": live.get("running_turn_id"),
                                            "started_event_ids": [e["event_id"] for e in started], "ui": shot})
    assert live["parent_agent_id"] == lead["agent_id"] and live["state"] == "active" and live["attempt"] == 1
    assert live.get("running_turn_id") and len(started) == 1, "exact child attempt-two turn is not running"
    assert shot["path"] == route and shot["open"] == shot["expanded"] == "false"
    assert re.search(r"\b1 running\b", shot["header"]) and shot["observerArmed"] and not shot["capturedBeforeClick"]
    browser("click", "[data-testid=agent-tray] button[aria-expanded=false]")
    opened = browser_json("""JSON.stringify((() => {const tray=document.querySelector('[data-testid=agent-tray]');
      return {path:location.pathname,open:tray?.dataset.open||'',
        expanded:tray?.querySelector('button[aria-expanded]')?.getAttribute('aria-expanded')||'',
        captured:!!window.__aftChildReactivation?.capture};})())""")
    save("reactivation-tray-after-click", {"child": child["agent_id"], "ui": opened})
    assert opened["path"] == route and opened["open"] == opened["expanded"] == "true", "real tray click did not open the exact Lead tray"
    browser("screenshot", str(OUT / f"reactivation-tray-open-{child_label}.png"))


def reactivation_checkpoint_ok(shot, child):
    captured = shot.get("capture")
    assert captured, "attempt-two running tray and nested sidebar were not witnessed; completed cards alone do not count"
    assert captured["path"] == f"/ws/{quote(WS, safe='')}/chat/{quote(child['parent_agent_id'], safe='')}"
    assert captured["rowId"] == captured["linkId"] == child["agent_id"]
    assert captured["rowVisible"] and captured["running"] and captured["nested"] and captured["exactLinkCount"] == 1
    assert captured["childName"] == child["name"] and captured["attemptChip"] == "attempt 2"
    assert captured["logo"] and captured["dot"] == "working" and captured["color"]
    assert re.match(r"^(▸|💭)", captured["result"]) and re.fullmatch(r"\d+m \d{2}s", captured["elapsed"])
    assert captured["capturedAt"] > 0
    return captured


def reactivation_event(history, child_id, after_seq, result):
    started = [e for e in history if e["agent_id"] == child_id and e["kind"] == "turn.started" and e["seq"] > after_seq]
    assert len(started) == 1 and started[0].get("turn_id"), "exact second-attempt turn did not start after the pre-Send baseline"
    turn = started[0]["turn_id"]
    steps = [e for e in history if e["agent_id"] == child_id and e.get("turn_id") == turn and
             e["seq"] > started[0]["seq"] and step_prefix(e)]
    matches = [e for e in steps if result == step_prefix(e) or result.startswith(step_prefix(e) + " · ")]
    assert matches, "captured attempt-two step lacks an exact saved current-turn tool or reasoning event"
    return matches[0]


def reactivation_event_diagnostic(history, child_id, after_seq, captured, live):
    """Save bounded identities and hashes, never native text or tool arguments."""
    def digest(value):
        return hashlib.sha256(value.encode("utf-8", "surrogatepass")).hexdigest()

    def identity(value):
        return value if isinstance(value, str) and len(value) <= 160 and \
            re.fullmatch(r"[A-Za-z0-9_:/.-]+", value) else "<invalid>"

    candidates = [e for e in history if e.get("agent_id") == child_id and
                  isinstance(e.get("seq"), int) and e["seq"] > after_seq and
                  e.get("kind") in ("turn.started", "tool.started", "item.completed")]
    rows = []
    for event in candidates[-40:]:
        payload = event.get("payload") or {}
        raw = payload.get("text") if payload.get("itemKind") == "reasoning" else None
        raw_line = raw.strip().split("\n", 1)[0] if isinstance(raw, str) else ""
        prefix = step_prefix(event)
        rows.append({"agent_id": identity(event.get("agent_id")), "seq": event["seq"],
                     "event_id": identity(event.get("event_id")),
                     "turn_id": identity(event.get("turn_id")), "kind": event["kind"],
                     "item_id": identity(payload.get("itemId")),
                     "item_kind": payload.get("itemKind") if payload.get("itemKind") in
                     ("reasoning", "tool", "message") else "other",
                     "formatting": [flag for flag, present in (
                         ("heading", bool(re.match(r"^#{1,6}\s+", raw_line))),
                         ("strong", "**" in raw_line or "__" in raw_line),
                         ("emphasis", "*" in raw_line or "_" in raw_line),
                         ("code", "`" in raw_line)) if present],
                     "raw_first_line_sha256": digest(raw_line) if raw_line else None,
                     "normalized_prefix_sha256": digest(prefix) if prefix else None})
    return {"child_id": child_id, "baseline_seq": after_seq,
            "captured_at_ms": captured["capturedAt"], "read_at_ms": int(time.time() * 1000),
            "api_state": live["state"], "api_attempt": live["attempt"],
            "api_running_turn_id": live.get("running_turn_id"),
            "visible_sha256": digest(captured["result"]),
            "candidate_count": len(candidates), "omitted_count": max(0, len(candidates) - len(rows)),
            "events": rows}


def reactivation_checkpoint(lead_label, child_label):
    lead, child = load(lead_label), load(child_label)
    assert child["parent_agent_id"] == lead["agent_id"]
    child_id, name = child["agent_id"], child["name"]
    browser("wait", "--fn", "(() => {const s=window.__aftChildReactivation; const name=" + json.dumps(name) +
            "; const cards=[...document.querySelectorAll('[data-testid=completion-record]')].filter(c=>" +
            "c.querySelector(':scope > span:nth-child(2)')?.textContent?.trim()===name && " +
            "c.dataset.outcome==='completed' && c.dataset.delivery==='delivered');" +
            "return !!s?.capture || JSON.stringify(cards.map(c=>c.dataset.attempt).sort())==='[\"0\",\"1\"]'; })()")
    shot = browser_json("JSON.stringify((() => {const s=window.__aftChildReactivation; s?.observer?.disconnect(); return {capture:s?.capture||null," +
                        "cardAttempts:[...document.querySelectorAll('[data-testid=completion-record]')]" +
                        ".filter(c=>c.querySelector(':scope > span:nth-child(2)')?.textContent?.trim()===" +
                        json.dumps(name) + ").map(c=>c.dataset.attempt)}; })())")
    save("reactivation-observed", shot)
    captured = reactivation_checkpoint_ok(shot, child)
    assert captured["color"] == load(f"color-{child_id}")["value"], "reactivated sidebar badge changed color"
    live = agent(child_id)
    assert live["parent_agent_id"] == lead["agent_id"] and live["state"] in ("active", "waiting", "finished")
    baseline = load("reactivation-armed")
    assert baseline["child"] == child_id and baseline["parent"] == lead["agent_id"]
    history = events(child_id)
    save("reactivation-event-diagnostic",
         reactivation_event_diagnostic(history, child_id, baseline["last_seq"], captured, live))
    step = reactivation_event(history, child_id, baseline["last_seq"], captured["result"])
    browser("screenshot", str(OUT / f"reactivation-checkpoint-{child_label}.png"))
    save("reactivation-checkpoint", {"stage": "running", "api_state": live["state"],
                                     "api_attempt_at_capture_read": live["attempt"], "ui": shot,
                                     "turn_id": step["turn_id"], "event_id": step["event_id"]})


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
    saved_child = load(child_label)
    actual = read_ref(saved_child)
    original = saved_child["branch"]
    switched_ref(prior, second, original, actual)
    save("repeat-proof", {"keys": [prior["record_id"], second["record_id"]],
                          "first_reply": load("first-reply"), "final_reply": after[0]["event_id"],
                          "actual_branch": actual["branch"], "actual_head": actual["head"], "second_record": second})


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
    save("pair-chronology", {"lead": lead["agent_id"], "lead_turn": busy_at["lead_turn"],
                             "busy_start_seq": busy_at["last_lead_seq"],
                             "busy_end": [{"event_id": e["event_id"], "seq": e["seq"]} for e in busy_end],
                             "child_records": [{"child": child, "events": [{"event_id": e["event_id"],
                                 "seq": e["seq"], "turn_id": e.get("turn_id"),
                                 "outcome": e["payload"].get("outcome")} for e in rows]}
                                 for child, rows in zip(ids, records)],
                             "deliveries": [{"event_id": e["event_id"], "seq": e["seq"],
                                 "completions": e["payload"].get("completions")} for e in ev
                                 if e["kind"] == "message.delivered" and e["payload"].get("completions")]})
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
    finished = [e for e in ev if e["kind"] == "agent.turn_completed" and e["seq"] > grouped[0]["seq"]]
    assert len(finished) == 1 and all(reply["seq"] < finished[0]["seq"] for reply in replies), "Lead final reply is not a persisted finished turn"
    assert len(replies) == 1 and all(load(label)["name"] in reply_text(replies[0]) for label in (first_label, second_label))
    save("pair-proof", {"children": ids, "record_ids": [rows[0]["event_id"] for rows in records],
                        "delivery_id": grouped[0]["event_id"], "final_reply_id": replies[0]["event_id"],
                        "final_turn_end_id": finished[0]["event_id"], "busy_lead_turn": busy_at["lead_turn"]})


def roster(*labels):
    rows = listed()
    ids = [load(label)["agent_id"] for label in labels]
    for agent_id in ids:
        assert sum(row["agent_id"] == agent_id for row in rows) == 1
    save("roster", {"ids": ids, "rows": [{"agent_id": row["agent_id"], "name": row["name"],
                                             "state": row["state"], "parent_agent_id": row.get("parent_agent_id")}
                                            for row in rows]})


def sidebar_order_ok(shot, a, b, stage):
    assert shot["path"] == f"/ws/{quote(WS, safe='')}/chat/{quote(b, safe='')}", "sidebar drag changed the Chat route"
    assert shot["nav"], "Agents sidebar disappeared during drag"
    assert shot["ids"].count(a) == shot["ids"].count(b) == 1, "saved Lead rows missing or duplicated"
    if stage == "before":
        assert shot["ids"].index(a) < shot["ids"].index(b), "initial Lead order differs"
    if stage in ("after", "reload"):
        assert shot["ids"].index(b) < shot["ids"].index(a), "keyboard drag did not reorder saved Leads"


def sidebar_tab_result(value):
    data = value.get("data", value) if isinstance(value, dict) else value
    tabs = data.get("tabs", []) if isinstance(data, dict) else data
    active_id = data.get("activeTabId") if isinstance(data, dict) else None
    assert isinstance(tabs, list), "browser tab list has no tab rows"
    active = [tab for tab in tabs if isinstance(tab, dict) and
              (tab.get("active") or tab.get("isActive") or tab.get("selected") or tab.get("current") or
              (active_id and active_id in (tab.get("id"), tab.get("targetId"))))]
    assert len(active) == 1 and active[0].get("targetId"), "one owned active browser tab is required"
    tab = active[0]
    parsed = urlsplit(tab.get("url") or "")
    return {"session": os.environ["AFT_SESSION"], "target_id": tab["targetId"],
            "tab_id": tab.get("id", ""), "tab_count": len(tabs),
            "url_path": parsed.path if parsed.scheme in ("http", "https") else tab.get("url", "")}


def sidebar_tab():
    raw = subprocess.check_output(["agent-browser", "--session", os.environ["AFT_SESSION"],
                                   "--json", "tab", "list"], text=True)
    return sidebar_tab_result(json.loads(raw))


def sidebar_drag_state(label):
    saved = load(label)
    route = f"/ws/{quote(WS, safe='')}/chat/{quote(saved['agent_id'], safe='')}"
    script = """JSON.stringify((() => {const id=%s,name=%s,route=%s;
      const nav=document.querySelector('nav[aria-label=Agents]');
      const links=Array.from(nav?.querySelectorAll('[data-testid=sortable-agent-row] > a[href]')||[]);
      const match=links.filter(a=>a.getAttribute('href')===route);
      const handle=match[0]?.closest('[data-testid=sortable-agent-row]')?.querySelector('[aria-label="Drag to reorder '+name+'"]');
      return {path:location.pathname,href:location.href,readyState:document.readyState,nav:!!nav,
        ids:links.map(a=>decodeURIComponent(a.getAttribute('href')?.split('/').pop()||'')),
        linkCount:match.length,handleCount:handle?1:0,focused:document.activeElement===handle};})())""" % (
        json.dumps(saved["agent_id"]), json.dumps(saved["name"]), json.dumps(route))
    return browser_json(script)


def sidebar_key(label, key, phase):
    assert (key, phase) in (("Space", "lifted"), ("ArrowUp", "moved"), ("Space", "attempted"))
    saved = load(label)
    route = f"/ws/{quote(WS, safe='')}/chat/{quote(saved['agent_id'], safe='')}"
    tab_before, ui_before = sidebar_tab(), sidebar_drag_state(label)
    baseline = load("sidebar-drag-tab")
    save(f"sidebar-key-{phase}-before", {"key": key, "tab": tab_before, "ui": ui_before})
    assert tab_before["target_id"] == baseline["target_id"] and tab_before["url_path"] == route
    assert ui_before["path"] == route and ui_before["nav"] and ui_before["linkCount"] == 1 and ui_before["handleCount"] == 1 and ui_before["focused"]
    command = subprocess.run(["agent-browser", "--session", os.environ["AFT_SESSION"], "press", key],
                             capture_output=True, text=True)
    diagnostic = {"key": key, "command_status": command.returncode}
    try:
        diagnostic["tab"] = sidebar_tab()
        diagnostic["ui"] = sidebar_drag_state(label)
    except (subprocess.CalledProcessError, ValueError, AssertionError) as error:
        diagnostic["diagnostic_error"] = type(error).__name__
    try:
        browser("screenshot", str(OUT / f"sidebar-key-{phase}.png"))
        diagnostic["screenshot"] = f"sidebar-key-{phase}.png"
    except subprocess.CalledProcessError as error:
        diagnostic["screenshot_error_exit"] = error.returncode
    save(f"sidebar-key-{phase}-after", diagnostic)
    assert command.returncode == 0, f"browser keyboard {key} failed"
    assert diagnostic.get("tab", {}).get("target_id") == baseline["target_id"], "sidebar drag left its owned browser tab"
    assert diagnostic.get("tab", {}).get("url_path") == route and diagnostic.get("ui", {}).get("path") == route, "sidebar drag changed the exact Chat route"


def sidebar_focus(label):
    saved = load(label)
    live = agent(saved["agent_id"])
    assert live["agent_id"] == saved["agent_id"] and live["name"] == saved["name"]
    route = f"/ws/{quote(WS, safe='')}/chat/{quote(saved['agent_id'], safe='')}"
    shot = browser_json("""JSON.stringify((() => {
      const id=%s, name=%s, route=%s;
      const nav=document.querySelector('nav[aria-label=Agents]');
      const links=Array.from(nav?.querySelectorAll('[data-testid=sortable-agent-row] > a[href]') || []);
      const matches=links.filter(a=>a.getAttribute('href')===route);
      const row=matches[0]?.closest('[data-testid=sortable-agent-row]');
      const handles=Array.from(row?.querySelectorAll('[aria-label]') || [])
        .filter(h=>h.getAttribute('aria-label')==='Drag to reorder '+name);
      const handle=handles[0];
      handle?.focus();
      return {path:location.pathname,id,name,linkCount:matches.length,handleCount:handles.length,
        focused:document.activeElement===handle};
    })())""" % (json.dumps(saved["agent_id"]), json.dumps(saved["name"]), json.dumps(route)))
    save("sidebar-keyboard-focus", shot)
    assert shot == {"path": route, "id": saved["agent_id"], "name": saved["name"],
                    "linkCount": 1, "handleCount": 1, "focused": True}, "saved Lead drag handle missing or unfocused"
    tab = sidebar_tab()
    save("sidebar-drag-tab", tab)
    assert tab["url_path"] == route, "drag focus is not on the exact saved Lead tab"


def sidebar_order(first_label, second_label, stage):
    a, b = load(first_label), load(second_label)
    rows = listed()
    assert all(sum(row["agent_id"] == lead["agent_id"] for row in rows) == 1 for lead in (a, b))
    script = """JSON.stringify((() => {
      const nav=document.querySelector('nav[aria-label=Agents]');
      const links=Array.from(nav?.querySelectorAll('[data-testid=sortable-agent-row] > a[href]') || []);
      return {path:location.pathname,href:location.href,readyState:document.readyState,nav:!!nav,
        ids:links.map(a=>decodeURIComponent(a.getAttribute('href')?.split('/').pop()||''))};
    })())"""
    try:
        tab = sidebar_tab()
        shot = browser_json(script)
    except subprocess.CalledProcessError as error:
        save(f"sidebar-order-{stage}", {"browser_error_exit": error.returncode})
        raise
    save(f"sidebar-order-{stage}", {"saved_ids": [a["agent_id"], b["agent_id"]], "tab": tab, "ui": shot})
    if stage in ("lifted", "moved", "attempted"):
        browser("screenshot", str(OUT / f"sidebar-order-{stage}.png"))
    if stage != "before":
        assert tab["target_id"] == load("sidebar-drag-tab")["target_id"], "sidebar order used a different browser tab"
    sidebar_order_ok(shot, a["agent_id"], b["agent_id"], stage)


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
