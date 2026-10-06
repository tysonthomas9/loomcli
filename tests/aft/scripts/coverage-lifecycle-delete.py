#!/usr/bin/env python3
"""Public API and sanitized read-only receipts for two owned Delete journeys."""
import json
import os
import re
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path
from urllib.error import HTTPError
from urllib.parse import quote, urlencode, urlsplit
from urllib.request import Request, urlopen

RUN = os.environ["RUN_ID"]
WS = os.environ["AFT_WS"]
BASE = os.environ["AFT_API_URL"].rstrip("/")
REPO = os.environ["AFT_AGENT_FLOW_REPO"]
ROOT = f"{BASE}/api/workspaces/{quote(WS)}/v1/agents"
OUT = Path(os.environ["AFT_WORK_DIR"]) / "coverage-lifecycle-delete"
NATIVE = Path(os.environ["AFT_TESTS_DIR"]) / "scripts/coverage-lifecycle-delete-native.sh"
NAMES = {label: f"cov-delete-{label}-{RUN}" for label in ("target", "control", "parent", "child")}


def api(path, method="GET"):
    request = Request(path, method=method)
    try:
        with urlopen(request, timeout=20) as response:
            return response.status, json.load(response) if response.status != 204 else None
    except HTTPError as error:
        return error.code, json.load(error)


def save(name, data):
    OUT.mkdir(parents=True, exist_ok=True)
    (OUT / f"{name}.json").write_text(json.dumps(data, indent=2, sort_keys=True) + "\n")


def load(name):
    return json.loads((OUT / f"{name}.json").read_text())


def url(label):
    return ROOT + "/" + quote(load(label)["agent_id"])


def live(label):
    status, row = api(url(label))
    assert status == 200 and row["agent_id"] == load(label)["agent_id"], (label, status)
    assert row["name"] == NAMES[label] and row["workspace_id"] == WS and row["repo"] == REPO
    assert row["harness"] == "opencode" and row["state"] != "deleted"
    assert row["worktree_path"] == load(label)["worktree"]
    return row


def route_id():
    raw = subprocess.check_output(["agent-browser", "--session", os.environ["AFT_SESSION"],
                                   "eval", "location.pathname"], text=True).strip()
    pathname = json.loads(raw) if raw.startswith('"') else raw
    match = re.fullmatch(rf"/ws/{re.escape(WS)}/chat/(agt_[A-Za-z0-9_-]+)", pathname)
    assert match, "expected the UI-created Agent Chat route"
    return match[1]


def bind(label):
    assert label in ("target", "control", "parent")
    agent_id = route_id()
    status, row = api(ROOT + "/" + quote(agent_id))
    assert status == 200 and row["name"] == NAMES[label] and row["repo"] == REPO
    assert row["harness"] == "opencode" and row["preset"] == "lead"
    assert row["created_by_kind"] == "user" and not row.get("parent_agent_id")
    assert row["worktree_path"] == f"/root/.loom/worktrees/source-repo/{agent_id}"
    save(label, {"label": label, "run": RUN, "agent_id": agent_id, "name": row["name"],
                 "repo": REPO, "worktree": row["worktree_path"], "branch": row["branch"],
                 "parent_agent_id": None})


def events(label):
    status, page = api(url(label) + "/events?limit=500")
    assert status == 200 and not page.get("more"), "event history unavailable or truncated"
    return page["events"]


def child():
    parent = live("parent")
    status, page = api(ROOT + "?" + urlencode({"name": NAMES["child"], "parent": parent["agent_id"],
                                               "include_archived": "true", "limit": 500}))
    assert status == 200 and not page.get("next")
    matches = [r for r in page["agents"] if r["name"] == NAMES["child"]]
    assert len(matches) == 1, "expected exactly one real task child"
    row = matches[0]
    check_child_ownership(row, parent)
    assert row["harness"] == "opencode" and row["worktree_path"] == f"/root/.loom/worktrees/source-repo/{row['agent_id']}"
    parent_events = events("parent")
    created = [e for e in parent_events if e["kind"] == "child.created" and
               e["payload"].get("child") == row["agent_id"]]
    tools = [e for e in parent_events if e["kind"] == "item.completed" and
             e["payload"].get("itemKind") == "tool" and
             "agent_create" in str(e["payload"].get("tool", {}))]
    assert len(created) == 1 and len(tools) >= 1 and any(NAMES["child"] in str(t) for t in tools)
    save("child", {"label": "child", "run": RUN, "agent_id": row["agent_id"],
                   "name": row["name"], "repo": REPO, "worktree": row["worktree_path"],
                   "branch": row["branch"], "parent_agent_id": parent["agent_id"],
                   "created_event_id": created[0]["event_id"],
                   "tool_event_ids": [t["event_id"] for t in tools if NAMES["child"] in str(t)]})


def check_child_ownership(row, parent):
    assert row["preset"] == "task" and row["created_by_kind"] == "agent"
    assert row["parent_agent_id"] == row["root_agent_id"] == parent["agent_id"]
    assert row["created_by_id"] == parent["agent_id"] and row["repo"] == REPO


def checked_file_origin(manifest, ui_url, api_url, source_head):
    owned = manifest["owned"]
    assert manifest["run_id"] == RUN and manifest["backend"] == "opencode"
    assert manifest["source_root"] == os.environ["AFT_SOURCE_ROOT"]
    assert manifest["source_head"] == source_head
    assert owned["compose_project"] == os.environ["AFT_OWNED_PROJECT"] == "loom-aft-agents-" + RUN
    assert owned["evidence_dir"] == os.environ["AFT_WORK_DIR"]
    assert owned["ui_url"] == ui_url and owned["api_url"] == api_url
    for candidate, port in ((ui_url, owned["ports"][2]), (api_url, owned["ports"][1])):
        parsed = urlsplit(candidate)
        assert parsed.scheme == "http" and parsed.hostname == "127.0.0.1"
        assert parsed.port == port and parsed.username is None and parsed.password is None
        assert not parsed.path and not parsed.query and not parsed.fragment
    assert ui_url != api_url
    return ui_url


def file_origin():
    manifest = json.loads((Path(os.environ["AFT_WORK_DIR"]) / "manifest.json").read_text())
    source_head = subprocess.check_output(
        ["git", "-C", os.environ["AFT_SOURCE_ROOT"], "rev-parse", "HEAD"], text=True).strip()
    return checked_file_origin(manifest, os.environ["AFT_BASE_URL"], BASE, source_head)


def file_path(label):
    return f"{file_origin()}/api/workspaces/{quote(WS)}/files?" + urlencode({
        "scope": "agent", "target": load(label)["agent_id"], "repo": Path(REPO).name, "path": "README.md"})


def prepare():
    live("target")
    status, file = api(file_path("target"))
    assert status == 200 and file["path"] == "README.md" and not file["binary"]
    assert file["content"].endswith("\n") and file["version"]
    save("file-original", {"version": file["version"]})
    for stage in ("one", "two"):
        marker = f"COV_DELETE_{RUN}_{stage.upper()}"
        (OUT / f"readme-{stage}.txt").write_text(file["content"] + marker + "\n")


def file_saved(stage):
    assert stage in ("one", "two")
    row = live("target")
    status, file = api(file_path("target"))
    expected = (OUT / f"readme-{stage}.txt").read_text()
    assert status == 200 and file["content"] == expected and file["version"]
    before = load("file-original") if stage == "one" else load("file-one")
    assert file["version"] != before["version"], "Files Save did not change strong version"
    status, git = api(f"{file_origin()}/api/workspaces/{quote(WS)}/files/git-status?" + urlencode({
        "scope": "agent", "target": row["agent_id"], "repo": Path(REPO).name, "path": ""}))
    assert status == 200 and set(git["status"]) == {"README.md"}
    save(f"file-{stage}", {"version": file["version"]})


def editor_bytes_match(actual, expected):
    assert actual == expected, f"CodeMirror buffer differs from intended bytes ({len(actual)} vs {len(expected)})"


def type_editor(stage):
    assert stage in ("one", "two")
    selector = "[role=tabpanel]:not([data-hidden]) .cm-content[contenteditable=true]"
    session = os.environ["AFT_SESSION"]
    expected = (OUT / f"readme-{stage}.txt").read_text()
    for args in (("focus", selector), ("press", "Meta+a"), ("press", "Backspace"),
                 ("keyboard", "type", expected)):
        subprocess.run(["agent-browser", "--session", session, *args], check=True,
                       stdout=subprocess.DEVNULL)


def check_editor(stage):
    assert stage in ("one", "two")
    expression = """(() => { const p=document.querySelector('[role=tabpanel]:not([data-hidden])');
      const e=p?.querySelector('.cm-content[contenteditable=true]');
      if (!e || !p.closest('[data-testid=agent-api-page]')) throw Error('owned editable Files pane missing');
      return {text:Array.from(e.querySelectorAll('.cm-line')).map(line=>line.textContent).join('\\n')}; })()"""
    raw = subprocess.check_output(["agent-browser", "--session", os.environ["AFT_SESSION"],
                                   "eval", expression], text=True).strip()
    actual = json.loads(raw)["text"]
    editor_bytes_match(actual, (OUT / f"readme-{stage}.txt").read_text())


def fingerprint(stage):
    assert stage in ("one", "stale")
    row = live("target")
    query = "" if stage == "one" else "?" + urlencode({"fingerprint": load("fingerprint-one")["fingerprint"]})
    status, body = api(url("target") + query, "DELETE")
    old = load("fingerprint-one")["fingerprint"] if stage == "stale" else None
    token = check_unsaved(status, body, row["worktree_path"], old)
    save(f"fingerprint-{stage}", {"fingerprint": token, "paths": body["paths"]})
    live("target")


def check_unsaved(status, body, worktree, old=None):
    assert status == 409 and body["code"] == "unsaved_work"
    assert body["paths"] == ["README.md"] and re.fullmatch(r"[a-fA-F0-9]{64}", body["fingerprint"])
    assert worktree in body["error"]
    if old is not None:
        assert body["fingerprint"] != old, "stale token was accepted"
    return body["fingerprint"]


def native(label, stage):
    assert label in NAMES and stage in ("capture", "present", "deleted")
    prior = OUT / f"{label}.json" if stage == "capture" else OUT / f"native-{label}-capture.json"
    result = subprocess.run([str(NATIVE), stage, load(label)["agent_id"], str(prior)],
                            capture_output=True, text=True)
    assert result.returncode == 0, "owned native probe failed"
    receipt = json.loads(result.stdout)
    assert receipt["agent_id"] == load(label)["agent_id"] and receipt["stage"] == stage
    save(f"native-{label}-{stage}", receipt)


def preflight(*labels):
    assert labels in (("target",), ("parent", "child"))
    session = os.environ["AFT_SESSION"]
    assert re.fullmatch(r"aft-coverage-lifecycle-delete-[0-9]+", session)
    probe = os.environ["AFT_NATIVE_SESSION_PROBE"]
    assert Path(probe).is_file()
    pending = []
    journal = Path(os.environ["AFT_WORK_DIR"]) / "lifecycle-delete-preflight.jsonl"
    seen = {json.loads(line)["api"]["agent_id"] for line in journal.read_text().splitlines()} if journal.exists() else set()
    for label in labels:
        row = live(label)
        agent_id = row["agent_id"]
        assert agent_id not in seen
        captured = load(f"native-{label}-capture")
        result = subprocess.run([probe, agent_id], capture_output=True, text=True)
        assert result.returncode == 0, "runner-owned native identity probe failed"
        actual = json.loads(result.stdout)
        check_preflight(label, row, actual, captured)
        pending.append({"run_id": RUN, "session": session, "suite": "coverage-lifecycle-delete",
                        "captured_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
                        "api": row, "native": actual})
    with journal.open("a") as output:
        for receipt in pending:
            output.write(json.dumps(receipt, separators=(",", ":")) + "\n")


def check_preflight(label, row, actual, captured):
    assert actual["agent_id"] == row["agent_id"] and actual["harness"] == "opencode"
    assert actual["native_id"] in {ref["native_id"] for ref in captured["refs"]}
    assert actual["native_root"] == ""
    assert row["deleted_at"] is None and row["model_unverified"] is False
    if label != "child":
        assert row["model"] == os.environ["AFT_REAL_MODEL"]


def child_refusal():
    parent, kid = live("parent"), live("child")
    assert kid["parent_agent_id"] == parent["agent_id"]
    assert kid["state"] not in ("finished", "archived", "deleted"), "child settled before refusal"
    status, body = api(url("parent"), "DELETE")
    assert status == 409 and body["code"] == "children_live"
    assert kid["agent_id"] in body["error"]
    live("parent")
    live("child")
    save("child-refusal", {"parent": parent["agent_id"], "child": kid["agent_id"],
                           "child_state": kid["state"], "code": body["code"]})


def cascade():
    parent, kid = live("parent"), live("child")
    assert kid["parent_agent_id"] == parent["agent_id"]
    status, body = api(url("parent") + "?cascade=true", "DELETE")
    assert status == 204 and body is None, "public cascade Delete failed"


def deleted(label):
    status, row = api(url(label))
    assert status == 200 and row["agent_id"] == load(label)["agent_id"]
    assert row["state"] == "deleted" and row["deleted_at"] and row["history_purged_at"]
    status, body = api(url(label) + "/events?limit=10")
    assert status == 410 and body["code"] == "history_expired"
    native(label, "deleted")


def control():
    row = live("control")
    assert row["state"] in ("idle", "waiting"), row["state"]
    native("control", "present")
    assert events("control"), "control history vanished"


def self_test():
    f1, f2 = "a" * 64, "b" * 64
    body = {"code": "unsaved_work", "paths": ["README.md"],
            "fingerprint": f2, "error": "uncommitted changes in /owned"}
    assert check_unsaved(409, body, "/owned", f1) == f2
    parent = {"agent_id": "agt_parent"}
    child_row = {"preset": "task", "created_by_kind": "agent", "parent_agent_id": "agt_parent",
                 "root_agent_id": "agt_parent", "created_by_id": "agt_parent", "repo": REPO}
    check_child_ownership(child_row, parent)
    model = "openai/example"
    os.environ["AFT_REAL_MODEL"] = model
    row = {"agent_id": "agt_owned", "deleted_at": None, "model_unverified": False, "model": model}
    native_row = {"agent_id": "agt_owned", "harness": "opencode", "native_id": "ses_owned", "native_root": ""}
    captured = {"refs": [{"native_id": "ses_owned"}]}
    check_preflight("target", row, native_row, captured)
    ui_url, api_url, head = "http://127.0.0.1:8283", "http://127.0.0.1:8282", "a" * 40
    os.environ["AFT_SOURCE_ROOT"] = "/owned/source"
    os.environ["AFT_OWNED_PROJECT"] = "loom-aft-agents-" + RUN
    manifest = {"run_id": RUN, "backend": "opencode", "source_root": "/owned/source",
                "source_head": head,
                "owned": {"compose_project": os.environ["AFT_OWNED_PROJECT"],
                          "evidence_dir": os.environ["AFT_WORK_DIR"],
                          "ui_url": ui_url, "api_url": api_url, "ports": [8281, 8282, 8283]}}
    assert checked_file_origin(manifest, ui_url, api_url, head) == ui_url
    editor_bytes_match("original\nmarker\n", "original\nmarker\n")
    for action in (
        lambda: check_unsaved(204, body, "/owned", f1),
        lambda: check_unsaved(409, {**body, "fingerprint": f1}, "/owned", f1),
        lambda: check_unsaved(409, {**body, "code": "conflict"}, "/owned", f1),
        lambda: check_unsaved(409, body, "/foreign", f1),
        lambda: check_child_ownership({**child_row, "parent_agent_id": "agt_foreign"}, parent),
        lambda: check_child_ownership({**child_row, "repo": "/foreign"}, parent),
        lambda: check_preflight("target", {**row, "model": "other"}, native_row, captured),
        lambda: check_preflight("target", {**row, "deleted_at": "past"}, native_row, captured),
        lambda: check_preflight("target", row, {**native_row, "agent_id": "agt_foreign"}, captured),
        lambda: check_preflight("target", row, {**native_row, "native_id": "ses_foreign"}, captured),
        lambda: check_preflight("target", row, {**native_row, "native_root": "/foreign"}, captured),
        lambda: checked_file_origin(manifest, "http://127.0.0.1:9999", api_url, head),
        lambda: checked_file_origin(manifest, "http://example.org:8283", api_url, head),
        lambda: checked_file_origin({**manifest, "source_head": "b" * 40}, ui_url, api_url, head),
        lambda: checked_file_origin({**manifest, "owned": {**manifest["owned"],
                                    "evidence_dir": "/foreign"}}, ui_url, api_url, head),
        lambda: editor_bytes_match("original\nmarker\noriginal", "original\nmarker\n"),
        lambda: editor_bytes_match("original\nmarker", "original\nmarker\n"),
    ):
        try:
            action()
        except AssertionError:
            continue
        raise AssertionError("lifecycle oracle accepted a negative case")
    print("lifecycle stale and ownership negative checks passed")


if __name__ == "__main__":
    globals()[sys.argv[1]](*sys.argv[2:])
