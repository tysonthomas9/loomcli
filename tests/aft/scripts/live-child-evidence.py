#!/usr/bin/env python3
"""Read-only Agent API assertions for the paid child-agent AFT journeys."""

import json
import hashlib
import os
import pathlib
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


def load(label):
    return json.loads((WORK / f"{label}.json").read_text())


def agent(agent_id):
    return get(f"{ROOT}/{urllib.parse.quote(agent_id)}")


def events(agent_id):
    # Each scenario has few turns; fail closed if one page would truncate proof.
    page = get(f"{ROOT}/{urllib.parse.quote(agent_id)}/events?limit=500")
    assert not page["more"], "event page truncated"
    return page["events"]


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


def owned_git(worktree, *args):
    """Read the checked child worktree; Agent API has no diff/commit route."""
    script = r'''
set -euo pipefail
source "$AFT_TESTS_DIR/scripts/agent-flows-ownership.sh"
agent_flows_check_manifest
cd "$AFT_SOURCE_ROOT"
compose=(agent_flows_podman compose -p "$AFT_OWNED_PROJECT"
  -f test/local-mode/docker-compose.yml
  -f test/local-mode/docker-compose.agents.yml
  -f test/local-mode/docker-compose.agents-real.yml
  -f "$AFT_WORK_DIR/fleet-override.yml")
container="$("${compose[@]}" ps -q loom-local | tr -d '\r')"
agent_flows_check_container "$container"
"${compose[@]}" exec -T loom-local git -C "$1" "${@:2}"
'''
    result = subprocess.run(["bash", "-c", script, "owned-git", worktree, *args],
                            capture_output=True, text=True, check=True, timeout=60)
    return result.stdout.strip()


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
        assert pathlib.PurePosixPath(a["worktree_path"]).name == child_id
        head = owned_git(a["worktree_path"], "rev-parse", "HEAD")
        commits = owned_git(a["worktree_path"], "log", "--format=%H",
                            f"{a['base_ref']}..HEAD").splitlines()
        files = owned_git(a["worktree_path"], "diff", "--name-only",
                          f"{a['base_ref']}..HEAD").splitlines()
        expected_path = f"aft-child-fixtures/{os.environ['RUN_ID']}/{name}.txt"
        assert head == rec[0]["payload"]["head"] and head in commits, (head, commits)
        assert files == [expected_path], files
        save(f"{label}-{name}-commits", {"head": head, "commits": commits})
        save(f"{label}-{name}-files", {"base_ref": a["base_ref"], "paths": files})
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
    assert any(all(name in reply for name in names) for reply in replies), replies


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
