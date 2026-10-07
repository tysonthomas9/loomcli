#!/usr/bin/env python3
"""Read-only Agent API assertions for the paid child-agent AFT journeys."""

import json
import hashlib
import base64
from datetime import datetime, timezone
import os
import pathlib
import re
import subprocess
import sys
import urllib.parse
import urllib.request
import urllib.error


WORK = pathlib.Path(os.environ["AFT_WORK_DIR"]) / "live-children"
WORK.mkdir(parents=True, exist_ok=True)
BASE = os.environ["AFT_API_URL"].rstrip("/")
WS = os.environ["AFT_WS"]
ROOT = f"{BASE}/api/workspaces/{urllib.parse.quote(WS)}/v1/agents"


def get(path):
    with urllib.request.urlopen(path, timeout=15) as response:
        return json.load(response)


def save(label, value):
    (WORK / f"{label}.json").write_text(json.dumps(value, indent=2) + "\n")


def utc_now():
    return datetime.now(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def load(label):
    return json.loads((WORK / f"{label}.json").read_text())


def agent(agent_id):
    return get(f"{ROOT}/{urllib.parse.quote(agent_id)}")


def events(agent_id):
    # Each scenario has few turns; fail closed if one page would truncate proof.
    page = get(f"{ROOT}/{urllib.parse.quote(agent_id)}/events?limit=500")
    assert not page["more"], "event page truncated"
    return page["events"]


def safe_turn_error(error):
    assert isinstance(error, str)
    codes = ("harness_down", "auth_failed", "ask_missing", "session_missing",
             "input_id_conflict", "bad_request")
    category = next((f"opencode_{code}" for code in codes
                     if error.startswith(f"opencode: {code} (")), None)
    if category is None and (error == "model not found" or error.startswith("model not found\n")):
        category = "model_not_found"
    return {"category": category or "unknown", "present": bool(error),
            "byte_length": len(error.encode()),
            "sha256": hashlib.sha256(error.encode()).hexdigest() if error else None}


def safe_failure_events(agent_id):
    rows = []
    for event in events(agent_id):
        assert event["agent_id"] == agent_id, "foreign failure event"
        kind = event["kind"]
        if kind not in ("child.created", "task_completed", "agent.turn_completed", "message.delivered"):
            continue
        payload = event["payload"]
        row = {key: event[key] for key in ("event_id", "seq", "kind", "turn_id")}
        if kind == "child.created":
            row["payload"] = {key: payload.get(key) for key in ("child", "name", "preset")}
        elif kind == "task_completed":
            row["payload"] = {key: payload.get(key) for key in ("child", "attempt", "outcome", "head")}
            row["payload"]["summary_present"] = bool(payload.get("summary"))
        elif kind == "agent.turn_completed":
            row["payload"] = {"stopReason": payload.get("stopReason"),
                              "error": safe_turn_error(payload.get("error") or "")}
        else:
            completions = payload.get("completions") or []
            row["payload"] = {"completion_keys": [{"child": c.get("child"), "attempt": c.get("attempt")}
                                                   for c in completions]}
        rows.append(row)
    return rows


def pair_timeout_dom(lead_id, names):
    script = """JSON.stringify((() => {const lead=LEAD,names=NAMES;
      const cards=[...document.querySelectorAll('[data-testid=completion-record]')];
      const tray=document.querySelector('[data-testid=agent-tray]');
      return {lead_route_matches:location.pathname===`/ws/${WS}/chat/${lead}`,
        card_count:cards.length,cards:cards.map(c=>({
          outcome:['completed','failed','cancelled'].includes(c.dataset.outcome)?c.dataset.outcome:'unknown',
          delivery:['delivered','pending'].includes(c.dataset.delivery)?c.dataset.delivery:'unknown',
          child_name_matches:names.map(n=>c.textContent.includes(n))})),
        tray_present:!!tray,tray_row_count:tray?.querySelectorAll('[data-tray-row]').length||0,
        tray_running_count:tray?.querySelectorAll('[data-tray-row] [data-status=running]').length||0,
        lead_running:!!document.querySelector('section[aria-label="Agent chat"] header [data-running=true]')};})())"""
    script = script.replace("LEAD", json.dumps(lead_id)).replace("NAMES", json.dumps(names)).replace("${WS}", WS)
    try:
        raw = subprocess.check_output(["agent-browser", "--session", os.environ["AFT_SESSION"], "eval", "-b",
                                       base64.b64encode(script.encode()).decode()], text=True, timeout=10).strip()
    except subprocess.TimeoutExpired:
        return {"available": False, "failure_category": "timeout"}
    except subprocess.CalledProcessError:
        return {"available": False, "failure_category": "command_failed"}
    except OSError:
        return {"available": False, "failure_category": "command_unavailable"}
    try:
        for _ in range(2):
            value = json.loads(raw)
            if isinstance(value, dict):
                return {"available": True, **value}
            if not isinstance(value, str):
                break
            raw = value
    except (TypeError, ValueError):
        pass
    return {"available": False, "failure_category": "invalid_response"}


def pair_timeout(lead_label, *names):
    assert lead_label == "pair-lead" and len(names) == 2
    wait_keys = ("AFT_PAIR_WAIT_FIRST_STARTED_AT", "AFT_PAIR_WAIT_FIRST_ENDED_AT",
                 "AFT_PAIR_WAIT_SECOND_STARTED_AT", "AFT_PAIR_WAIT_SECOND_ENDED_AT")
    wait_window = {key: os.environ[key] for key in wait_keys}
    assert all(re.fullmatch(r"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ", value)
               for value in wait_window.values()), "invalid pair wait timestamps"
    save("pair-timeout-wait", wait_window)
    capture = {"capture_started_at": utc_now(), "api_started_at": utc_now(),
               "api_status": "pending", "dom_status": "not_attempted"}
    save("pair-timeout-capture", capture)
    try:
        pair_timeout_api(lead_label, names)
    except Exception:
        capture["api_status"] = "failed"
        capture["api_failure_category"] = "snapshot_failed"
        raise
    else:
        capture["api_status"] = "complete"
    finally:
        capture["api_ended_at"] = utc_now()
        capture["capture_ended_at"] = utc_now()
        save("pair-timeout-capture", capture)
    lead_id = (WORK / lead_label).read_text().strip()
    capture["dom_started_at"] = utc_now()
    try:
        dom = pair_timeout_dom(lead_id, names)
        save("pair-timeout-dom", dom)
        capture["dom_status"] = "available" if dom["available"] else "unavailable"
        if not dom["available"]:
            capture["dom_failure_category"] = dom["failure_category"]
    except Exception:
        capture["dom_status"] = "unavailable"
        capture["dom_failure_category"] = "snapshot_failed"
        save("pair-timeout-dom", {"available": False, "failure_category": "snapshot_failed"})
    finally:
        capture["dom_ended_at"] = utc_now()
        capture["capture_ended_at"] = utc_now()
        save("pair-timeout-capture", capture)


def pair_timeout_api(lead_label, names):
    lead_id = (WORK / lead_label).read_text().strip()
    lead = agent(lead_id)
    assert lead["agent_id"] == lead_id and lead["name"] == f"aft-child-lead-{os.environ['RUN_ID']}"
    ids = [(WORK / f"pair-{name}.id").read_text().strip() for name in names]
    children = [agent(child_id) for child_id in ids]
    assert all(c["agent_id"] == child_id and c["name"] == name and
               c["parent_agent_id"] == c["root_agent_id"] == lead_id
               for c, child_id, name in zip(children, ids, names)), "foreign pair child failure snapshot"
    selected = ("agent_id", "name", "state", "running_turn_id", "attempt", "outcome", "finished_at")
    snapshot = {"lead": {key: lead.get(key) for key in selected},
                "children": [{key: c.get(key) for key in selected} for c in children], "events": {}}
    save("pair-timeout-api", snapshot)
    for agent_id in (lead_id, *ids):
        snapshot["events"][agent_id] = safe_failure_events(agent_id)
        save("pair-timeout-api", snapshot)


def evidence(label, agent_id):
    a, ev = agent(agent_id), events(agent_id)
    fields = ("agent_id", "workspace_id", "name", "preset", "harness", "model", "repo", "base_ref",
              "parent_agent_id", "root_agent_id", "created_by_kind", "created_by_id",
              "worktree_path", "branch", "state", "state_reason", "attention_reason",
              "running_turn_id", "outcome", "archive_reason",
              "attempt", "finished_at", "history_purged_at")
    save(label, {key: a.get(key) for key in fields})
    safe_events = []
    for e in ev:
        p = e["payload"]
        row = {key: e[key] for key in ("event_id", "seq", "kind", "turn_id")}
        if e["kind"] == "child.created":
            row["payload"] = {key: p.get(key) for key in ("child", "name", "preset")}
        elif e["kind"] == "task_completed":
            row["payload"] = {key: p.get(key) for key in ("child", "attempt", "outcome", "branch", "head", "summary")}
        elif e["kind"] == "message.delivered" and p.get("completions"):
            row["payload"] = {"sender": p.get("sender"), "completions": p["completions"],
                              "notice_keys": [f"task_completed:{c['child']}:{c['attempt']}" for c in p["completions"]]}
        elif e["kind"] == "item.completed" and p.get("itemKind") == "tool":
            t = p.get("tool") or {}
            row["payload"] = {"tool": {"name": t.get("name"), "failed": t.get("failed"),
                                         "agent_not_found": "agent_not_found" in t.get("output", "")}}
        elif e["kind"] == "agent.turn_completed":
            error = p.get("error") or ""
            row["payload"] = {"stopReason": p.get("stopReason"), "error_present": bool(error),
                              "error_sha256": hashlib.sha256(error.encode()).hexdigest() if error else None}
        else:
            continue
        safe_events.append(row)
    save(f"{label}-events", safe_events)
    return a, ev


def repo():
    response = get(f"{BASE}/api/workspaces/{urllib.parse.quote(WS)}")
    workspace = response["data"]
    assert workspace["id"] == WS, "workspace registry returned another workspace"
    matches = [r for r in workspace["repos"] if r["name"] == "source-repo" and
               not r.get("is_linked_worktree")]
    assert len(matches) == 1, f"expected exactly one registered source-repo: {matches}"
    source = matches[0]
    assert source["path"] == os.environ["AFT_AGENT_FLOW_REPO"], (
        "runner repo is not the registered LOCALMODE source-repo", source["path"],
        os.environ["AFT_AGENT_FLOW_REPO"])
    assert source["path"].startswith("/"), "registered source-repo path is not absolute"
    save("source-repo", {"workspace_id": WS, "name": source["name"], "path": source["path"],
                         "source_repo_id": source.get("source_repo_id")})


def tool_calls(ev):
    return [e["payload"]["tool"] for e in ev
            if e["kind"] == "item.completed" and
            e["payload"].get("itemKind") == "tool" and
            e["payload"].get("tool")]


def called(calls, name, target=None, error=None):
    matched = [c for c in calls if name in (c.get("name", "") + " " + c.get("input", ""))
               and (target is None or target in c.get("input", ""))
               and (error is None or error in c.get("output", ""))]
    assert matched, f"missing real model tool call {name}, target={target}, error={error}"
    return matched


def lead(label, name):
    page = get(f"{ROOT}?include_archived=true&limit=500")
    assert not page.get("next"), "agent list truncated"
    matches = [a for a in page["agents"] if a["name"] == name]
    assert len(matches) == 1, f"expected one UI-created lead {name}: {matches}"
    a, ev = evidence(label, matches[0]["agent_id"])
    assert a["preset"] == "lead" and a["harness"] == os.environ["AFT_REAL_BACKEND"]
    assert a["model"] == os.environ["AFT_REAL_MODEL"], "Lead did not persist the explicit UI model choice"
    assert a["repo"] == os.environ["AFT_AGENT_FLOW_REPO"] and a["worktree_path"]
    (WORK / label).write_text(a["agent_id"] + "\n")
    print(a["agent_id"])


def children(label, lead_label, *names):
    parent = load(lead_label)
    page = get(f"{ROOT}?parent={urllib.parse.quote(parent['agent_id'])}&limit=500")
    assert not page.get("next"), "child list truncated"
    found = [a for a in page["agents"] if a["name"] in names]
    assert sorted(a["name"] for a in found) == sorted(names), (names, found)
    for child in found:
        a, _ = evidence(f"{label}-{child['name']}", child["agent_id"])
        assert a["preset"] == "task" and a["harness"] == parent["harness"]
        assert a["created_by_kind"] == "agent" and a["created_by_id"] == parent["agent_id"]
        assert a["parent_agent_id"] == a["root_agent_id"] == parent["agent_id"]
        assert a["repo"] == parent["repo"] and a["base_ref"] == parent["branch"]
        assert a["worktree_path"] and a["worktree_path"] != parent["worktree_path"]
        assert a["branch"] and a["branch"] != parent["branch"]
        if label == "cancel":
            assert a["state"] == "active" and a["running_turn_id"], "cancellation child is not running"
        (WORK / f"{label}-{a['name']}.id").write_text(a["agent_id"] + "\n")
    _, ev = evidence(lead_label, parent["agent_id"])
    for child in found:
        assert len([e for e in ev if e["kind"] == "child.created" and
                    e["payload"].get("child") == child["agent_id"]]) == 1


def completed(label, lead_label, *names):
    parent = load(lead_label)
    _, ev = evidence(lead_label, parent["agent_id"])
    records = []
    for name in names:
        child_id = (WORK / f"{label}-{name}.id").read_text().strip()
        a, child_events = evidence(f"{label}-{name}", child_id)
        assert a["outcome"] == "completed" and a["finished_at"]
        assert any(e["kind"] == "agent.turn_completed" for e in child_events)
        rec = [e for e in ev if e["kind"] == "task_completed" and
               e["event_id"] == f"task_completed:{child_id}:0"]
        assert len(rec) == 1 and rec[0]["payload"]["outcome"] == "completed", rec
        assert rec[0]["payload"].get("head") and rec[0]["payload"].get("summary")
        diff_root = f"{BASE}/api/workspaces/{urllib.parse.quote(WS)}/agents/{urllib.parse.quote(child_id)}/diff"
        query = urllib.parse.urlencode({"from": a["base_ref"]})
        commits = get(f"{diff_root}/commits?{query}")["data"]["commits"]
        files = get(f"{diff_root}/files?{query}&to=HEAD")["data"]["files"]
        expected_path = f"aft-child-fixtures/{os.environ['RUN_ID']}/{name}.txt"
        assert any(c["hash"] == rec[0]["payload"]["head"] for c in commits), commits
        assert [f["path"] for f in files] == [expected_path], files
        save(f"{label}-{name}-commits", commits)
        save(f"{label}-{name}-files", files)
        delivery = [e for e in ev if e["kind"] == "message.delivered" and
                    {"child": child_id, "attempt": 0} in e["payload"].get("completions", [])]
        assert len(delivery) == 1, delivery
        notice = delivery[0]["payload"].get("text", "")
        assert f"task_completed:{child_id}:0" in notice and name in notice
        assert "outcome=completed" in notice and "branch=" in notice
        records.append(rec[0]["payload"])
    save(f"{label}-completion-proof", records)
    called(tool_calls(ev), "agent_create")
    final_delivery = max(e["seq"] for e in ev if e["kind"] == "message.delivered" and
                         e["payload"].get("completions"))
    replies = [e["payload"].get("text", "") for e in ev if e["kind"] == "item.completed" and
               e["seq"] > final_delivery and e["payload"].get("itemKind") == "message"]
    combined = any(all(name in reply for name in names) for reply in replies)
    save(f"{label}-summary-proof", {"final_delivery_seq": final_delivery,
                                    "post_delivery_message_count": len(replies),
                                    "combined_summary_present": combined})
    assert combined, (f"missing combined Lead reply after delivery seq {final_delivery}; "
                      f"completed post-delivery messages: {len(replies)}")


def cancellation_ready(lead, child, lead_id, child_id, child_name):
    return (lead.get("agent_id") == lead_id and lead.get("preset") == "lead" and
            lead.get("state") == "idle" and not lead.get("running_turn_id") and
            child.get("agent_id") == child_id and child.get("name") == child_name and
            child.get("preset") == "task" and child.get("parent_agent_id") == lead_id and
            child.get("root_agent_id") == lead_id and child.get("state") == "active" and
            bool(child.get("running_turn_id")) and child.get("outcome") is None and
            child.get("finished_at") is None)


def cancel_ready(lead_label, child_name):
    lead_id = (WORK / lead_label).read_text().strip()
    child_id = (WORK / f"cancel-{child_name}.id").read_text().strip()
    lead, child = agent(lead_id), agent(child_id)
    assert cancellation_ready(lead, child, lead_id, child_id, child_name), (
        "cancel Send requires the saved idle Lead and its saved active child turn")
    save("cancel-ready", {"lead_id": lead_id, "lead_state": lead["state"],
                          "child_id": child_id, "child_state": child["state"],
                          "child_running_turn_id": child["running_turn_id"]})


def cancelled(label, lead_label, name):
    parent = load(lead_label)
    child_id = (WORK / f"{label}-{name}.id").read_text().strip()
    a, _ = evidence(f"{label}-{name}", child_id)
    _, ev = evidence(lead_label, parent["agent_id"])
    assert a["state"] == "archived" and a["archive_reason"] == "cancelled"
    assert a["history_purged_at"] is None
    assert events(child_id), "cancelled child lost its history"
    called(tool_calls(ev), "agent_archive", child_id)
    called(tool_calls(ev), "agent_create", name)
    assert any("cancel" in c.get("input", "") and "true" in c.get("input", "")
               for c in called(tool_calls(ev), "agent_archive", child_id))
    rec = [e for e in ev if e["kind"] == "task_completed" and
           e["event_id"] == f"task_completed:{child_id}:0"]
    assert len(rec) == 1 and rec[0]["payload"]["outcome"] == "cancelled", rec
    delivery = [e for e in ev if e["kind"] == "message.delivered" and
                {"child": child_id, "attempt": 0} in e["payload"].get("completions", [])]
    assert len(delivery) == 1, delivery


def rejected(label, owner_label, foreign_name, operation):
    assert operation in ("agent_get", "agent_send")
    owner = load(owner_label)
    foreign_id = (WORK / f"{label}-{foreign_name}.id").read_text().strip()
    _, ev = evidence(owner_label, owner["agent_id"])
    matches = [e for e in ev if e["kind"] == "item.completed" and
               e["payload"].get("itemKind") == "tool" and
               operation in (e["payload"].get("tool", {}).get("name", "") + " " +
                             e["payload"].get("tool", {}).get("input", "")) and
               foreign_id in e["payload"].get("tool", {}).get("input", "") and
               "agent_not_found" in e["payload"].get("tool", {}).get("output", "")]
    assert len(matches) == 1, (operation, foreign_id, matches)
    save(f"{label}-{operation}-rejection", {"operation": operation, "target": foreign_id,
                                          "event_id": matches[0]["event_id"],
                                          "turn_id": matches[0]["turn_id"],
                                          "error_code": "agent_not_found"})


def isolation(label, owner_label, own_name, foreign_label, foreign_name):
    owner = load(owner_label)
    own_id = (WORK / f"{label}-{own_name}.id").read_text().strip()
    foreign_id = (WORK / f"{label}-{foreign_name}.id").read_text().strip()
    _, ev = evidence(owner_label, owner["agent_id"])
    calls = tool_calls(ev)
    called(calls, "agent_get", foreign_id, "agent_not_found")
    called(calls, "agent_send", foreign_id, "agent_not_found")
    own = called(calls, "agent_get", own_id)
    assert any(not c.get("failed") and own_id in c.get("output", "") for c in own), own
    turns = []
    for operation, target in (("agent_get", own_id), ("agent_get", foreign_id),
                              ("agent_send", foreign_id)):
        matches = [e["turn_id"] for e in ev if e["kind"] == "item.completed" and
                   e["payload"].get("itemKind") == "tool" and
                   operation in (e["payload"].get("tool", {}).get("name", "") + " " +
                                 e["payload"].get("tool", {}).get("input", "")) and
                   target in e["payload"].get("tool", {}).get("input", "")]
        assert len(matches) == 1 and matches[0], (operation, target, matches)
        turns.extend(matches)
    assert len(set(turns)) == 3, "own and foreign tool calls were conflated into one turn"
    foreign_owner = load(foreign_label)
    _, foreign_ev = evidence(foreign_label, foreign_owner["agent_id"])
    assert not any("foreign-probe" in json.dumps(e) for e in foreign_ev), "foreign input leaked"


def cleanup():
    ids = []
    leads = []
    for label in ("pair-lead", "cancel-lead", "iso-a", "iso-b"):
        path = WORK / label
        if path.exists():
            leads.append(path.read_text().strip())
    for lead_id in leads:
        page = get(f"{ROOT}?parent={urllib.parse.quote(lead_id)}&include_archived=true&limit=500")
        assert not page.get("next"), "cleanup child list truncated"
        ids.extend(a["agent_id"] for a in page["agents"])
    for path in WORK.glob("*.id"):
        agent_id = path.read_text().strip()
        if agent_id:
            ids.append(agent_id)
    ids.extend(leads)
    for agent_id in dict.fromkeys(ids):
        a = agent(agent_id)
        assert a["name"].startswith("aft-") and a["name"].endswith(os.environ["RUN_ID"])
        evidence(f"final-{agent_id}", agent_id)
        if a["state"] == "archived":
            continue
        body = json.dumps({"reason": "cancelled" if a["state"] != "finished" else "done"}).encode()
        req = urllib.request.Request(f"{ROOT}/{urllib.parse.quote(agent_id)}/archive", data=body,
                                     headers={"Content-Type": "application/json",
                                              "Idempotency-Key": f"aft-{os.environ['RUN_ID']}-{agent_id}-cleanup"},
                                     method="POST")
        try:
            urllib.request.urlopen(req, timeout=15).close()
        except urllib.error.HTTPError as error:
            print(f"cleanup could not archive {agent_id}: HTTP {error.code}", file=sys.stderr)


if __name__ == "__main__":
    command, *args = sys.argv[1:]
    globals()[command](*args)
