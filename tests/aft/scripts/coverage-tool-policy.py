#!/usr/bin/env python3
"""Run-owned, read-only evidence for real Agent API tool policy AFT cases.

Only IDs, counts, digests, and usage amounts are written. Tool inputs and
native messages are inspected in memory and never copied to the report.
"""

import hashlib
import json
import math
import os
from pathlib import Path
import re
import subprocess
import sys
import urllib.parse
import urllib.request


def env(name):
    value = os.environ.get(name, "")
    if not value:
        raise AssertionError(f"missing {name}")
    return value


RUN = env("RUN_ID")
WS = env("AFT_WS")
BASE = env("AFT_API_URL").rstrip("/")
WORK = Path(env("AFT_WORK_DIR")) / "coverage-tool-policy"
API = f"/api/workspaces/{urllib.parse.quote(WS, safe='')}/v1/agents"
SENTINEL = f"ghp_AFTONLY{RUN}Q7mR2pK9xT4vN8cY6bL5fS3dH1jW0"
LEAD = f"aft-lead-{RUN}-cov-policy"
USAGE_LEAD = f"aft-lead-{RUN}-cov-policy-usage"
TRAY_LEAD = f"aft-lead-{RUN}-cov-policy-tray"
TRAY_CHILD = f"aft-child-{RUN}-cov-policy-tray"
REVIEWER = f"aft-review-{RUN}-cov-policy"


def save(name, obj):
    WORK.mkdir(parents=True, exist_ok=True)
    (WORK / name).write_text(json.dumps(obj, sort_keys=True, indent=2) + "\n")


def api(path, method="GET", body=None, key=None):
    headers = {"Accept": "application/json"}
    data = None
    if body is not None:
        headers["Content-Type"] = "application/json"
        data = json.dumps(body).encode()
    if key:
        headers["Idempotency-Key"] = key
    with urllib.request.urlopen(
        urllib.request.Request(BASE + path, data=data, headers=headers, method=method), timeout=20
    ) as response:
        if response.status == 204:
            assert response.read() == b"", "204 response unexpectedly had a body"
            return None
        return json.load(response)


def owned_id(kind):
    value = (WORK / f"{kind}.id").read_text().strip()
    assert re.fullmatch(r"agt_[A-Za-z0-9_-]+", value), "invalid owned Agent ID"
    return value


def agent(kind):
    row = api(f"{API}/{owned_id(kind)}")
    expected = {"lead": LEAD, "usage": USAGE_LEAD, "tray": TRAY_LEAD,
                "child": TRAY_CHILD, "reviewer": REVIEWER}[kind]
    assert row["agent_id"] == owned_id(kind) and row["name"] == expected
    assert row["repo"] == env("AFT_AGENT_FLOW_REPO") and row["harness"] == "opencode"
    if kind == "child":
        assert row["parent_agent_id"] == owned_id("tray") and row["created_by_kind"] == "agent"
    else:
        assert row["parent_agent_id"] is None and row["created_by_kind"] == "user"
    return row


def events(kind):
    result, after = [], 0
    while True:
        page = api(f"{API}/{owned_id(kind)}/events?after={after}&limit=500")
        result.extend(page["events"])
        if not page["more"]:
            break
        next_after = page["next"]
        assert isinstance(next_after, int) and next_after > after, "event cursor stuck"
        after = next_after
    ids = [row["event_id"] for row in result]
    assert len(ids) == len(set(ids)), "duplicate saved events"
    assert [row["seq"] for row in result] == sorted(row["seq"] for row in result)
    return result


def browser(expression):
    raw = subprocess.check_output(
        ["agent-browser", "--session", env("AFT_SESSION"), "eval", expression], text=True
    ).strip()
    return json.loads(raw)


def claim_lead(kind):
    path = browser("location.pathname")
    assert re.fullmatch(rf"/ws/{re.escape(WS)}/chat/agt_[A-Za-z0-9_-]+", path), "wrong Chat route"
    WORK.mkdir(parents=True, exist_ok=True)
    (WORK / f"{kind}.id").write_text(path.rsplit("/", 1)[1] + "\n")
    row = agent(kind)
    assert row["preset"] == "lead"
    save(f"{kind}-identity.json", {"agent_id": row["agent_id"], "name": row["name"],
                                    "preset": row["preset"], "model": row["model"]})


def claim_child():
    children = api(f"{API}?parent={urllib.parse.quote(owned_id('tray'))}&limit=500")["agents"]
    matches = [row for row in children if row["name"] == TRAY_CHILD]
    assert len(matches) == 1, "one exact run-owned child was not created through Loom"
    WORK.mkdir(parents=True, exist_ok=True)
    (WORK / "child.id").write_text(matches[0]["agent_id"] + "\n")
    row = agent("child")
    assert row["preset"] == "task" and row["role_kind"] == "worker"
    save("child-identity.json", {"agent_id": row["agent_id"],
                                  "parent_agent_id": row["parent_agent_id"],
                                  "name": row["name"], "preset": row["preset"]})


def create_reviewer():
    row = api(API, "POST", {"preset": "pr-review-interactive", "name": REVIEWER,
                            "repo": env("AFT_AGENT_FLOW_REPO"), "base_ref": "main",
                            "overrides": {"harness": "opencode", "model": env("AFT_REAL_MODEL")}},
              f"coverage-tool-policy-review-{RUN}")
    assert re.fullmatch(r"agt_[A-Za-z0-9_-]+", row["agent_id"])
    WORK.mkdir(parents=True, exist_ok=True)
    (WORK / "reviewer.id").write_text(row["agent_id"] + "\n")
    current = agent("reviewer")
    assert current["preset"] == "pr-review-interactive"
    save("reviewer-identity.json", {"agent_id": current["agent_id"],
                                    "name": current["name"], "preset": current["preset"]})


def usage_rows(kind):
    rows = [e for e in events(kind) if e["kind"] == "usage"]
    ids = [event_item_id(e) for e in rows]
    assert len(ids) == len(set(ids)), "usage step counted twice"
    return rows


def event_item_id(event):
    event_id = event["event_id"]
    assert event_id.startswith(event["kind"] + ":"), "saved event kind/ID mismatch"
    assert ":seq:" not in event_id, "saved native item ID unavailable"
    item_id = event_id.rsplit(":", 1)[-1]
    assert item_id, "saved native item ID unavailable"
    return item_id


FIELDS = {"inputTokens": "total_input_tokens", "outputTokens": "total_output_tokens",
          "cacheReadTokens": "total_cache_read_tokens", "cacheWriteTokens": "total_cache_write_tokens",
          "costUsd": "total_cost"}


def usage_totals(rows):
    return {target: sum(e["payload"].get(source, 0) for e in rows)
            for source, target in FIELDS.items()}


def assert_native_steps(usage, native_steps):
    native_by_id = {step["itemID"]: step for step in native_steps}
    assert len(native_by_id) == len(native_steps), "duplicate native usage step"
    assert set(native_by_id) == {event_item_id(e) for e in usage}, \
        "native and saved usage step IDs disagree"
    for event in usage:
        step = native_by_id[event_item_id(event)]
        for source in FIELDS:
            assert math.isclose(event["payload"].get(source, 0), step[source],
                                rel_tol=0, abs_tol=1e-8), f"saved {source} disagrees with native step"
    return sorted(native_by_id)


def cli_usage(kind):
    return json.loads(subprocess.check_output(
        [str(Path(env("AFT_TESTS_DIR")) / "scripts/coverage-tool-policy-stack.sh"),
         "usage", {"lead": LEAD, "usage": USAGE_LEAD}[kind]], text=True))


def check_usage(kind, zero):
    row = agent(kind)
    if kind in ("lead", "usage"):
        assert row["model"] == env("AFT_REAL_MODEL"), "Lead did not save the selected real model"
    usage = usage_rows(kind)
    if zero:
        assert not usage, "fresh agent already has usage events"
    else:
        assert usage, "real completed turn saved no usage steps"
    totals = usage_totals(usage)
    if not zero:
        assert totals["total_input_tokens"] > 0 and totals["total_output_tokens"] > 0
    cli = cli_usage(kind)
    assert cli["session_count"] == 1 and len(cli["sessions"]) == 1
    session = cli["sessions"][0]
    assert session["session_id"] == row["agent_id"] and session["agent_name"] == row["name"]
    assert session["backend"] == "opencode"
    for field, expected in totals.items():
        assert math.isclose(cli[field], expected, rel_tol=0, abs_tol=1e-8), f"CLI {field} disagrees with saved steps"
    native_ids = []
    if not zero:
        native = json.loads(subprocess.check_output(
            [str(Path(env("AFT_TESTS_DIR")) / "scripts/coverage-tool-policy-stack.sh"),
             "native-usage", row["agent_id"]], text=True))
        assert native["agent_id"] == row["agent_id"]
        native_ids = assert_native_steps(usage, native["steps"])
    save(f"{kind}-usage-{'zero' if zero else 'turn'}.json", {
        "agent_id": row["agent_id"], "step_event_ids": [e["event_id"] for e in usage],
        "step_item_ids": [event_item_id(e) for e in usage],
        "native_step_item_ids": native_ids, "totals": totals,
        "cli_session_count": cli["session_count"]})


def tool_events():
    rows = events("lead")
    all_tools = [e for e in rows if e["kind"] == "item.completed"
                 and e["payload"].get("itemKind") == "tool"]
    native_ids = native_tool_ids("lead")
    calls = [e for e in all_tools if event_item_id(e) in native_ids]
    assert len(calls) == len(native_ids) == 1, \
        "native sentinel call did not match exactly one saved tool event"
    saved_tool = calls[0]["payload"].get("tool") or {}
    assert "printf" in saved_tool.get("input", "") and "REDACTED" in saved_tool.get("input", ""), \
        "saved native tool input did not retain a safe command and redaction receipt"
    assert SENTINEL not in saved_tool.get("input", "") and SENTINEL not in saved_tool.get("output", ""), \
        "saved native tool event leaked the harmless sentinel"
    assert not saved_tool.get("failed"), "sentinel tool failed"
    return rows, calls


def native_tool_ids(kind):
    result = json.loads(subprocess.check_output(
        [str(Path(env("AFT_TESTS_DIR")) / "scripts/coverage-tool-policy-stack.sh"),
         "native-tool", owned_id(kind)], text=True))
    assert result["agent_id"] == owned_id(kind) and len(result["tool_item_ids"]) == 1
    return result["tool_item_ids"]


SCOPED_DOM = """(() => {
  const selectors = '[data-testid=tool-call],[data-testid=tool-live],[data-testid=tool-group],[data-testid=work-toggle],[data-testid=bridge-call],[data-testid=agent-tray]';
  const nodes = [...document.querySelectorAll(selectors)];
  return nodes.map(n => ({kind:n.getAttribute('data-testid'), text:n.textContent||'',
    aria:[n,...n.querySelectorAll('[aria-label],[title]')]
      .map(e=>(e.getAttribute('aria-label')||'')+' '+(e.getAttribute('title')||'')).join(' '),
    expanded:n.getAttribute('data-testid')==='tool-call'
      ? n.querySelector('[role=button][aria-expanded]')?.getAttribute('aria-expanded') ?? null
      : n.getAttribute('aria-expanded')}));
})()"""


def assert_private_nodes(nodes, sentinel):
    assert nodes, "no scoped tool UI to inspect"
    assert all(sentinel not in n["text"] and sentinel not in n["aria"] for n in nodes), \
        "harmless sentinel leaked from a scoped tool/tray UI surface"


def assert_expanded_state(nodes, stage):
    cards = [n for n in nodes if n["kind"] == "tool-call"]
    if stage == "collapsed":
        assert not any(n["expanded"] == "true" for n in cards), \
            "a native tool card was already expanded at the collapsed checkpoint"
    else:
        assert cards and all(n["expanded"] == "true" for n in cards), \
            "one or more native tool cards were not expanded"


def privacy_snapshot(stage):
    rows, calls = tool_events()
    nodes = browser(SCOPED_DOM)
    assert_private_nodes(nodes, SENTINEL)
    assert_expanded_state(nodes, stage)
    digest = hashlib.sha256(json.dumps(nodes, sort_keys=True).encode()).hexdigest()
    save(f"privacy-{stage}.json", {"agent_id": owned_id("lead"),
         "event_ids": [e["event_id"] for e in rows],
         "sentinel_tool_event_ids": [e["event_id"] for e in calls],
         "ui_sha256": digest, "scoped_node_count": len(nodes),
         "expanded_tool_count": sum(n["kind"] == "tool-call" and n["expanded"] == "true" for n in nodes)})
    if stage == "reloaded":
        before = json.loads((WORK / "privacy-expanded.json").read_text())
        assert before["event_ids"] == [e["event_id"] for e in rows], "reload changed saved history"


def current_child_tool_events(rows, agent_id, turn_id, sentinel):
    current = [e for e in rows if e["agent_id"] == agent_id and e["turn_id"] == turn_id]
    matching = [e for e in current if e["kind"] == "item.completed"
                and e["payload"].get("itemKind") == "tool"
                and "printf" in (e["payload"].get("tool") or {}).get("input", "")
                and "SAFE" in (e["payload"].get("tool") or {}).get("input", "")]
    assert len(matching) == 1, "exactly one same-turn saved native child command required"
    saved_input = (matching[0]["payload"].get("tool") or {}).get("input", "")
    assert "REDACTED" in saved_input and sentinel not in saved_input, \
        "child's saved tool input did not redact the harmless sentinel"
    return matching


def tray_capture_script(child_id):
    script = """(() => {
      const id=CHILD_ID;
      const li=[...(document.querySelector('[data-testid=agent-tray]')?.querySelectorAll('li[data-tray-row]')||[])]
        .find(e=>e.getAttribute('data-tray-row')===id);
      const row=li?.querySelector('[data-status=running]');
      const href=li?.querySelector('a[href]')?.getAttribute('href')||'';
      const text=row?.textContent||'';
      if (!row || !href.endsWith('/'+id) || !text.includes('Ran command') ||
          !text.includes('printf') || !text.includes('REDACTED') ||
          text.includes(SENTINEL)) return false;
      window.__aftPolicyTrayCapture={agentId:id,status:'running',href,text,
        aria:[...(row.querySelectorAll('[aria-label]'))].map(e=>e.getAttribute('aria-label')||'').join(' '),
        observedAt:Date.now()};
      return true;
    })()""".replace("CHILD_ID", json.dumps(child_id)).replace("SENTINEL", json.dumps(SENTINEL))
    return script


def tray_capture():
    child = agent("child")
    child_id = child["agent_id"]
    turn_id = child.get("running_turn_id")
    if child["state"] != "active" or not turn_id:
        rows = events("child")
        save("tray-failure.json", {"child_agent_id": child_id,
             "current_state": child["state"], "current_turn_id": turn_id,
             "event_count": len(rows), "last_event_kind": rows[-1]["kind"] if rows else None,
             "reason": "exact child has no active turn before tray observation"})
        raise AssertionError("exact child has no active turn before tray observation")
    script = tray_capture_script(child_id)
    wait = subprocess.run(["agent-browser", "--session", env("AFT_SESSION"), "wait", "--fn", script],
                          text=True, capture_output=True, check=False)
    if wait.returncode:
        latest = agent("child")
        rows = events("child")
        save("tray-failure.json", {"child_agent_id": child_id, "expected_turn_id": turn_id,
             "current_state": latest["state"], "current_turn_id": latest.get("running_turn_id"),
             "event_count": len(rows), "last_event_kind": rows[-1]["kind"] if rows else None,
             "reason": "exact running redacted command preview not observed"})
        raise AssertionError("exact running child redacted command preview not observed")
    capture = browser("window.__aftPolicyTrayCapture || null")
    if not isinstance(capture, dict) or capture.get("agentId") != child_id or \
            capture.get("status") != "running":
        save("tray-failure.json", {"child_agent_id": child_id, "expected_turn_id": turn_id,
             "reason": "exact child running tray capture unavailable"})
        raise AssertionError("exact child running tray capture unavailable")
    assert capture["href"].endswith("/" + child_id), "tray capture links to a foreign child"
    assert "Ran command" in capture["text"] and "printf" in capture["text"] and \
        "REDACTED" in capture["text"], "tray capture did not show the redacted native command"
    assert_private_nodes([{"kind": "agent-tray", "text": capture["text"],
                           "aria": capture["aria"]}], SENTINEL)
    rows = events("child")
    try:
        matching = current_child_tool_events(rows, child_id, turn_id, SENTINEL)
        native_ids = native_tool_ids("child")
        assert native_ids == [event_item_id(matching[0])], \
            "running child tray command did not bind to its one native saved tool step"
    except Exception:
        latest = agent("child")
        save("tray-failure.json", {"child_agent_id": child_id, "expected_turn_id": turn_id,
             "current_state": latest["state"], "current_turn_id": latest.get("running_turn_id"),
             "event_count": len(rows), "last_event_kind": rows[-1]["kind"] if rows else None,
             "reason": "captured running row lacks one matching saved native tool step"})
        raise AssertionError("captured running row lacks one matching saved native tool step") from None
    save("tray-capture.json", {"child_agent_id": child_id,
         "parent_agent_id": child["parent_agent_id"], "running_turn_id": turn_id,
         "observed_at": capture["observedAt"],
         "tray_row_sha256": hashlib.sha256(capture["text"].encode()).hexdigest(),
         "tool_event_id": matching[0]["event_id"], "native_item_id": native_ids[0],
         "redacted_command_visible": True})


def tray_snapshot():
    capture = json.loads((WORK / "tray-capture.json").read_text())
    child = agent("child")
    assert capture["child_agent_id"] == child["agent_id"] and \
        capture["parent_agent_id"] == child["parent_agent_id"], \
        "saved running tray capture belongs to a foreign child"
    assert not child.get("running_turn_id") or \
        child["running_turn_id"] == capture["running_turn_id"], \
        "child turn changed after running tray capture"
    matching = current_child_tool_events(events("child"), child["agent_id"],
                                         capture["running_turn_id"], SENTINEL)
    assert matching[0]["event_id"] == capture["tool_event_id"] and \
        event_item_id(matching[0]) == capture["native_item_id"] and \
        native_tool_ids("child") == [capture["native_item_id"]], \
        "native child sentinel tool did not match the captured saved redacted step"
    save("tray-privacy.json", {"child_agent_id": child["agent_id"],
         "parent_agent_id": child["parent_agent_id"],
         "tool_event_ids": [e["event_id"] for e in matching],
         "running_turn_id": capture["running_turn_id"],
         "tray_row_sha256": capture["tray_row_sha256"],
         "redacted_command_visible": capture["redacted_command_visible"]})


def expand_tools():
    browser("""(() => {
      const transcript = document.querySelector('[data-testid=chat-transcript]');
      transcript?.querySelectorAll('[data-testid=work-toggle][aria-expanded=false]')
        .forEach(group=>group.click());
      return true;
    })()""")
    result = browser("""(() => {
      const cards=[...document.querySelectorAll('[data-testid=chat-transcript] [data-testid=tool-call]')];
      cards.forEach(card=>card.querySelector('[role=button][aria-expanded=false]')?.click());
      return {count:cards.length};
    })()""")
    assert result["count"] > 0, "no native tool cards could be expanded"


def assert_reviewer_binding(row, state, repo):
    expected = f"/root/.loom/worktrees/source-repo/{row['agent_id']}"
    assert row["worktree_path"] == expected and row["repo"] == repo, \
        "reviewer API row has a foreign checkout or source repo"
    assert row["preset"] == "pr-review-interactive" and row["harness"] == "opencode"
    assert row["created_by_kind"] == "user" and row["parent_agent_id"] is None
    assert state["agent_id"] == row["agent_id"] and state["checkout"] == expected, \
        "Git readback was not bound to the exact saved reviewer Agent ID"
    assert state["repo"] == repo and state["preset"] == row["preset"] \
        and state["harness"] == row["harness"], "Git readback changed reviewer identity"


def policy_snapshot(stage):
    row = agent("reviewer")
    raw = subprocess.check_output(
        [str(Path(env("AFT_TESTS_DIR")) / "scripts/coverage-tool-policy-stack.sh"),
         "git-state", row["agent_id"]], text=True)
    state = json.loads(raw)
    assert_reviewer_binding(row, state, env("AFT_AGENT_FLOW_REPO"))
    assert state["deniedTargetExists"] is False, "denied target exists in owned checkout"
    save(f"policy-{stage}.json", state)
    if stage == "after":
        before = json.loads((WORK / "policy-before.json").read_text())
        assert before == state, "denied tool changed owned checkout, refs or remotes"
        denied = [e for e in events("reviewer") if e["kind"] == "item.completed"
                  and e["payload"].get("itemKind") == "tool"
                  and (e["payload"].get("tool") or {}).get("failed")
                  and re.search(r"edit|patch|write", (e["payload"].get("tool") or {}).get("name", ""), re.I)
                  and f"SECURITY_DENIED_{RUN}.txt" in (e["payload"].get("tool") or {}).get("input", "")]
        assert denied, "no actual denied edit of the owned target in saved native tool events"
        assert any(re.search(r"denied|permission|not allowed", (e["payload"].get("tool") or {}).get("output", ""), re.I)
                   for e in denied), "failed tool did not report a policy denial"
        save("policy-denial.json", {"agent_id": owned_id("reviewer"),
             "denied_event_ids": [e["event_id"] for e in denied]})


def cleanup():
    for name, preset in ((TRAY_CHILD, "task"), (LEAD, "lead"),
                         (USAGE_LEAD, "lead"), (TRAY_LEAD, "lead"),
                         (REVIEWER, "pr-review-interactive")):
        listing = api(f"{API}?name={urllib.parse.quote(name)}&include_archived=true&limit=500")
        for row in listing["agents"]:
            expected_creator = "agent" if name == TRAY_CHILD else "user"
            if row["name"] != name or row["repo"] != env("AFT_AGENT_FLOW_REPO") \
                    or row["preset"] != preset or row["created_by_kind"] != expected_creator:
                continue
            if not row.get("archived_at"):
                api(f"{API}/{row['agent_id']}/archive", "POST", {"reason": "cancelled"},
                    f"coverage-tool-policy-{RUN}-{preset}-archive")


def main():
    op = sys.argv[1]
    if op == "claim-lead":
        claim_lead("lead")
    elif op == "claim-usage":
        claim_lead("usage")
    elif op == "claim-tray":
        claim_lead("tray")
    elif op == "claim-child":
        claim_child()
    elif op == "create-reviewer":
        create_reviewer()
    elif op == "reviewer-id":
        print(owned_id("reviewer"))
    elif op == "usage-zero":
        check_usage("usage", True)
    elif op == "usage-turn":
        check_usage("usage", False)
    elif op == "usage-reloaded":
        before = json.loads((WORK / "usage-usage-turn.json").read_text())
        check_usage("usage", False)
        after = json.loads((WORK / "usage-usage-turn.json").read_text())
        assert before == after, "reload changed native, saved, or CLI usage"
        save("usage-reloaded.json", after)
    elif op == "privacy":
        privacy_snapshot(sys.argv[2])
    elif op == "tray":
        tray_snapshot()
    elif op == "tray-capture":
        tray_capture()
    elif op == "expand-tools":
        expand_tools()
    elif op == "policy":
        policy_snapshot(sys.argv[2])
    elif op == "cleanup":
        cleanup()
    else:
        raise ValueError("unknown evidence action")


if __name__ == "__main__":
    main()
