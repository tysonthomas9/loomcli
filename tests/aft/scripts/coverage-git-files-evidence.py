#!/usr/bin/env python3
"""Read-only, exact-agent receipts for the paid Files and Git journeys."""

import json
import os
import re
import subprocess
import sys
import urllib.parse
import urllib.error
import urllib.request
from pathlib import Path


def env(name):
    value = os.environ.get(name)
    if not value:
        raise RuntimeError(f"{name} is required")
    return value


WORK = Path(env("AFT_WORK_DIR")) / "coverage-git-files"
WS = env("AFT_WS")
RUN = env("RUN_ID")
BASE = env("AFT_API_URL").rstrip("/")
FRONTEND = env("AFT_BASE_URL").rstrip("/")
REPO = env("AFT_AGENT_FLOW_REPO")
ROOT = f"/api/workspaces/{urllib.parse.quote(WS, safe='')}"


def approved_file_url(path, manifest, frontend=FRONTEND, backend=BASE):
    """Use the owned public Caddy /api proxy, whose Host is an allowed frontend authority."""
    parts = urllib.parse.urlsplit(frontend)
    if (parts.scheme != "http" or parts.hostname not in ("127.0.0.1", "localhost", "::1")
            or not parts.port or parts.path or parts.query or parts.fragment or parts.username or parts.password):
        raise ValueError("file readback requires the runner's loopback frontend origin")
    owned = manifest.get("owned", {})
    if owned.get("ui_url") != frontend or owned.get("api_url") != backend:
        raise ValueError("file readback URLs differ from the owned run manifest")
    if not path.startswith(f"{ROOT}/files") or path[len(f"{ROOT}/files"):len(f"{ROOT}/files") + 1] not in ("", "/", "?"):
        raise ValueError("file readback must use a workspace files route")
    return frontend + path


def get(path):
    if path.startswith(f"{ROOT}/files"):
        manifest = json.loads((Path(env("AFT_WORK_DIR")) / "manifest.json").read_text())
        url = approved_file_url(path, manifest)
    else:
        url = BASE + path
    with urllib.request.urlopen(url, timeout=15) as response:
        return json.load(response)


def save(label, value):
    WORK.mkdir(parents=True, exist_ok=True)
    (WORK / f"{label}.json").write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def identity(label):
    agent_id = (WORK / f"{label}.id").read_text().strip()
    assert re.fullmatch(r"agt_[A-Za-z0-9_-]+", agent_id), agent_id
    agent = get(f"{ROOT}/v1/agents/{agent_id}")
    assert agent["agent_id"] == agent_id
    assert agent["workspace_id"] == WS and agent["repo"] == REPO, agent
    assert agent["harness"] == env("AFT_REAL_BACKEND"), agent
    return agent


def scoped(label, path, suffix="files"):
    agent = identity(label)
    query = urllib.parse.urlencode({"scope": "agent", "target": agent["agent_id"], "repo": Path(REPO).name, "path": path})
    return get(f"{ROOT}/{suffix}?{query}")


def claim(label):
    raw = subprocess.check_output(
        ["agent-browser", "--session", env("AFT_SESSION"), "eval", "location.pathname"], text=True
    ).strip()
    path = json.loads(raw) if raw.startswith('"') else raw
    match = re.fullmatch(rf"/ws/{re.escape(WS)}/chat/(agt_[A-Za-z0-9_-]+)", path)
    assert match, f"expected Agent API Chat route, got {path!r}"
    WORK.mkdir(parents=True, exist_ok=True)
    (WORK / f"{label}.id").write_text(match[1] + "\n")
    agent = identity(label)
    assert agent["name"] == f"cov-files-{RUN}-{label}", agent
    assert agent["preset"] == "lead" and agent["created_by_kind"] == "user", agent
    assert not agent.get("parent_agent_id") and agent["worktree_path"], agent
    save(f"{label}-identity", agent)


def route(label, tab, child_id=""):
    raw = subprocess.check_output(
        ["agent-browser", "--session", env("AFT_SESSION"), "eval", "location.pathname + location.search"], text=True
    ).strip()
    actual = json.loads(raw) if raw.startswith('"') else raw
    agent_id = child_id or identity(label)["agent_id"]
    expected = f"/ws/{WS}/chat/{agent_id}" + (f"?tab={tab}" if tab != "chat" else "")
    assert actual == expected, f"wrong agent/tab route: {actual!r} != {expected!r}"


def file_state(label, stage, path, expected):
    if expected.startswith("@"):
        expected = Path(expected[1:]).read_text()
    data = scoped(label, path)
    assert data["path"] == path and data["content"] == expected and not data["binary"], data
    assert data["version"], data
    stat = scoped(label, path, "files/stat")
    assert stat["path"] == path and stat["version"] == data["version"], (data, stat)
    status = scoped(label, "", "files/git-status")
    assert path in status["status"], status
    checkouts = get(f"{ROOT}/files/checkouts")["checkouts"]
    matches = [c for c in checkouts if c.get("kind") == "agent" and
               c.get("agent") == identity(label)["agent_id"] and c.get("repo") == Path(REPO).name]
    assert len(matches) == 1 and matches[0]["change_count"] >= 1, matches
    if label == "editor":
        before = json.loads((WORK / "editor-before-edit.json").read_text())["file"]
        assert data["version"] != before["version"], "Save kept the original strong version"
        assert matches[0]["change_count"] == 1 and set(status["status"]) == {"README.md"}, status
        if stage == "reloaded":
            saved = json.loads((WORK / "editor-saved.json").read_text())["file"]
            assert data["version"] == saved["version"], "reload changed the saved version"
    save(f"{label}-{stage}", {"file": data, "stat": stat, "status": status, "checkout": matches[0]})


def baseline(label, stage, path):
    data = scoped(label, path)
    assert data["path"] == path and not data["binary"] and data["version"], data
    save(f"{label}-{stage}", {"file": data})


def prepare_editor(label, path):
    baseline(label, "before-edit", path)
    original = scoped(label, path)["content"]
    assert original and original.endswith("\n"), "expected checked-in README text"
    (WORK / f"{label}-expected.txt").write_text(original + f"COV_FILES_{RUN}_EDITOR")


def editor_visible(label, expected):
    route(label, "files")
    if expected.startswith("@"):
        expected = Path(expected[1:]).read_text()
    script = """(() => { const p=document.querySelector('[data-testid=agent-api-page] [role=tabpanel]:not([data-hidden])');
      const cm=p?.querySelector('.cm-content[contenteditable=true]');
      if (!cm) throw Error('owned CodeMirror editor is not visible');
      return JSON.stringify(Array.from(cm.querySelectorAll('.cm-line')).map(line=>line.textContent).join('\\n')); })()"""
    raw = subprocess.check_output(
        ["agent-browser", "--session", env("AFT_SESSION"), "eval", script], text=True
    ).strip()
    actual = json.loads(raw)
    if isinstance(actual, str) and actual.startswith('"'):
        actual = json.loads(actual)
    save(f"{label}-before-save-visible", {"text": actual, "expected": expected})
    assert actual == expected, f"visible editor differs from expected: {len(actual)} vs {len(expected)} characters"


def capture_context_state(label, stage, path):
    """Preserve read-only responses even if the subsequent visible tree assertion fails."""
    agent = identity(label)
    query = urllib.parse.urlencode({"scope": "agent", "target": agent["agent_id"], "repo": Path(REPO).name, "path": path})
    checkout_query = urllib.parse.urlencode({"scope": "agent", "target": agent["agent_id"], "repo": Path(REPO).name})

    def observed(url):
        try:
            return {"status": 200, "body": get(url)}
        except urllib.error.HTTPError as error:
            return {"status": error.code}

    all_checkouts = observed(f"{ROOT}/files/checkouts")
    if all_checkouts["status"] == 200:
        body = all_checkouts["body"]
        all_checkouts["body"] = {"partial": body.get("partial"), "errors": body.get("errors"),
                                  "checkouts": [c for c in body.get("checkouts", []) if
                                                c.get("kind") == "agent" and c.get("agent") == agent["agent_id"]]}
    save(f"{label}-{stage}-context", {
        "agent": {k: agent.get(k) for k in ("agent_id", "state", "worktree_path", "branch", "deleted_at")},
        "checkouts": all_checkouts,
        "tree": observed(f"{ROOT}/files/tree?{checkout_query}"),
        "file": observed(f"{ROOT}/files?{query}"),
        "git_status": observed(f"{ROOT}/files/git-status?{checkout_query}"),
    })


def unchanged(label, path, before_stage):
    before = json.loads((WORK / f"{label}-{before_stage}.json").read_text())["file"]
    now = scoped(label, path)
    assert now["content"] == before["content"] and now["version"] == before["version"], (before, now)


def absent(label, path):
    try:
        scoped(label, path)
    except urllib.error.HTTPError as error:
        assert error.code == 404, f"{path}: unexpected HTTP {error.code}"
    else:
        raise AssertionError(f"{path} still exists")


def child(label, name):
    parent = identity(label)
    page = get(f"{ROOT}/v1/agents?parent={urllib.parse.quote(parent['agent_id'])}&limit=500")
    matches = [a for a in page["agents"] if a["name"] == name]
    assert len(matches) == 1 and not page.get("next"), matches
    value = get(f"{ROOT}/v1/agents/{matches[0]['agent_id']}")
    assert value["parent_agent_id"] == parent["agent_id"] and value["root_agent_id"] == parent["agent_id"], value
    assert value["created_by_kind"] == "agent" and value["created_by_id"] == parent["agent_id"], value
    assert value["repo"] == REPO and value["worktree_path"] != parent["worktree_path"], value
    assert value["branch"] and value["branch"] != parent["branch"], value
    assert value["base_ref"] == parent["branch"], value
    events = get(f"{ROOT}/v1/agents/{parent['agent_id']}/events?limit=500")
    assert not events["more"], "parent event proof truncated"
    created = [e for e in events["events"] if e["kind"] == "child.created" and
               e["payload"].get("child") == value["agent_id"]]
    assert len(created) == 1, "no unique persisted child.created receipt"
    save(f"{label}-child", value)
    (WORK / f"{label}-child.id").write_text(value["agent_id"] + "\n")


def committed(label, path, marker):
    value = json.loads((WORK / f"{label}-child.json").read_text())
    current = get(f"{ROOT}/v1/agents/{value['agent_id']}")
    assert current["agent_id"] == value["agent_id"] and current["branch"] == value["branch"]
    query = urllib.parse.urlencode({"scope": "agent", "target": value["agent_id"], "repo": Path(REPO).name, "path": path})
    file = get(f"{ROOT}/files?{query}")
    diff = get(f"{ROOT}/files/diff?{query}&from=main&to=HEAD")
    git = get(f"{ROOT}/agents/{value['agent_id']}/git/status")
    parent = identity(label)
    events = get(f"{ROOT}/v1/agents/{parent['agent_id']}/events?limit=500")
    completed = [e for e in events["events"] if e["kind"] == "task_completed" and
                 e["payload"].get("child") == value["agent_id"] and
                 e["payload"].get("outcome") == "completed"]
    save(f"{label}-committed-observed", {
        "agent": {k: current.get(k) for k in ("agent_id", "name", "branch", "base_ref", "worktree_path", "state")},
        "file": {k: file.get(k) for k in ("path", "content", "version")},
        "diff": {k: diff.get(k) for k in ("path", "patch")},
        "git": git,
        "completion_events": [{"event_id": e.get("event_id"),
                               "payload": {k: e["payload"].get(k) for k in ("child", "outcome", "branch", "head")}}
                              for e in completed],
        "events_more": events["more"],
    })
    assert marker in file["content"] and file["version"], file
    assert path in diff["patch"] and marker in diff["patch"], diff
    assert not events["more"], "parent completion proof truncated"
    assert git["branch"] == value["branch"] and git["ahead"] >= 1 and git["target_branch"] == value["base_ref"], git
    assert len(completed) == 1 and completed[0]["payload"].get("branch") == value["branch"], completed
    save(f"{label}-committed", {"agent": current, "file": file, "diff": diff, "git": git,
                                 "completion": {"event_id": completed[0]["event_id"], "payload": completed[0]["payload"]}})


def git_visible(label):
    saved = json.loads((WORK / f"{label}-committed.json").read_text())
    git = saved["git"]
    script = """(() => { const p = document.querySelector('[role=tabpanel]:not([data-hidden])');
      return JSON.stringify({ text: p?.textContent || '',
        ahead: p?.querySelector('[data-type=ahead]')?.textContent || '',
        behind: p?.querySelector('[data-type=behind]')?.textContent || '' }); })()"""
    raw = subprocess.check_output(
        ["agent-browser", "--session", env("AFT_SESSION"), "eval", script], text=True
    ).strip()
    visible = json.loads(raw)
    if isinstance(visible, str):
        visible = json.loads(visible)
    assert git["branch"] in visible["text"] and git["target_branch"] in visible["text"], visible
    assert f"cov-files-{RUN}-commit" in visible["text"], visible
    assert visible["ahead"].strip() == (f"+{git['ahead']} ahead" if git["ahead"] else ""), visible
    assert visible["behind"].strip() == (f"-{git['behind']} behind" if git["behind"] else ""), visible


def cleanup():
    if not WORK.exists():
        return
    for path in sorted(WORK.glob("*.id"), key=lambda item: not item.name.endswith("-child.id")):
        agent_id = path.read_text().strip()
        if not re.fullmatch(r"agt_[A-Za-z0-9_-]+", agent_id):
            continue
        current = get(f"{ROOT}/v1/agents/{agent_id}")
        if not current["name"].startswith((f"cov-files-{RUN}-", f"cov-files-child-{RUN}")):
            raise AssertionError(f"refusing to clean up foreign agent {agent_id}")
        req = urllib.request.Request(
            BASE + f"{ROOT}/v1/agents/{agent_id}/archive", data=b'{"cancel":true}',
            headers={"Content-Type": "application/json", "Idempotency-Key": f"cov-files-{RUN}-{agent_id}-cleanup"},
            method="POST")
        try:
            urllib.request.urlopen(req, timeout=15).close()
        except urllib.error.HTTPError as error:
            if error.code != 409:
                raise


def selftest():
    """Check the readback oracle rejects wrong bytes, versions and checkout IDs."""
    original_route, original_check_output = globals()["route"], subprocess.check_output
    original_session = os.environ.get("AFT_SESSION")
    os.environ["AFT_SESSION"] = "aft-selftest"
    globals()["route"] = lambda _label, _tab: None
    try:
        subprocess.check_output = lambda *_args, **_kwargs: json.dumps(json.dumps("expectedexpected"))
        try:
            editor_visible("editor", "expected")
        except AssertionError:
            pass
        else:
            raise AssertionError("visible editor oracle accepted appended original text")
        subprocess.check_output = lambda *_args, **_kwargs: json.dumps(json.dumps("expected"))
        editor_visible("editor", "expected")
    finally:
        globals()["route"], subprocess.check_output = original_route, original_check_output
        if original_session is None:
            os.environ.pop("AFT_SESSION", None)
        else:
            os.environ["AFT_SESSION"] = original_session

    source_root = Path(__file__).resolve().parents[3]
    page = (source_root / "internal/webui/frontend/src/views/AgentChatPage.tsx").read_text()
    tabs = re.search(r"export const AGENT_API_TABS[^=]*=\s*\[([^]]+)\]", page)
    assert tabs and re.findall(r'"([a-z]+)"', tabs[1]) == ["chat", "info", "git", "diff", "files"]
    suite = (source_root / "tests/aft/live-agent-coverage-suites/git-files.test.yaml").read_text()
    assert not re.search(r"click: \{ role: button, name: (?:Chat|Info|Git|Diff|Files)\b", suite), "global nav label can steal an Agent tab click"
    tab_clicks = re.findall(r'- click: \{ selector: "([^"]*agent-editor-groups[^"]*)" \}', suite)
    assert len(tab_clicks) == 7 and all('[data-testid=agent-api-page] ' in selector for selector in tab_clicks)
    assert [int(re.search(r'nth-of-type\((\d+)\)', selector)[1]) for selector in tab_clicks] == [5, 3, 2, 1, 4, 5, 4]

    manifest = {"owned": {"ui_url": "http://127.0.0.1:8283", "api_url": "http://127.0.0.1:8282"}}
    file_path = f"{ROOT}/files?scope=agent&target=agt_owned"
    assert approved_file_url(file_path, manifest, manifest["owned"]["ui_url"], manifest["owned"]["api_url"]) == "http://127.0.0.1:8283" + file_path
    for frontend, path, owned in (
        ("http://127.0.0.1:8282", file_path, manifest),  # bare API authority is denied
        ("http://attacker.example:8283", file_path, manifest),
        ("http://127.0.0.1:8283", f"{ROOT}/v1/agents/agt_owned", manifest),
        ("http://127.0.0.1:8283", file_path, {"owned": {"ui_url": "http://127.0.0.1:9999", "api_url": "http://127.0.0.1:8282"}}),
    ):
        try:
            approved_file_url(path, owned, frontend, "http://127.0.0.1:8282")
        except ValueError:
            pass
        else:
            raise AssertionError("file readback accepted a wrong authority, route or manifest")
    original_get, original_identity = globals()["get"], globals()["identity"]
    current = {"path": "a.txt", "content": "expected", "binary": False, "version": "v2"}
    stat = {"path": "a.txt", "version": "v2"}
    status = {"status": {"a.txt": " M"}}
    checkouts = {"checkouts": [{"kind": "agent", "agent": "agt_owned", "repo": Path(REPO).name,
                                 "change_count": 1}]}

    def fake_get(path):
        if "/files/stat?" in path:
            return stat
        if "/files/git-status?" in path:
            return status
        if "/files/checkouts" in path:
            return checkouts
        return current

    globals()["get"] = fake_get
    globals()["identity"] = lambda _label: {"agent_id": "agt_owned"}
    try:
        file_state("actions", "selftest-good", "a.txt", "expected")
        for mutate in (
            lambda: current.update(content="wrong"),
            lambda: stat.update(version="stale"),
            lambda: checkouts["checkouts"][0].update(agent="agt_foreign"),
        ):
            mutate()
            try:
                file_state("actions", "selftest-bad", "a.txt", "expected")
            except AssertionError:
                pass
            else:
                raise AssertionError("readback oracle accepted corrupted evidence")
            current["content"] = "expected"
            stat["version"] = "v2"
            checkouts["checkouts"][0]["agent"] = "agt_owned"
        def missing_tree(path):
            if "/files/tree?" in path:
                raise urllib.error.HTTPError("http://owned-ui/api/files/tree", 404, "not found", {}, None)
            if "/files/checkouts" in path:
                return {"checkouts": [], "partial": False, "errors": []}
            return fake_get(path)
        globals()["get"] = missing_tree
        capture_context_state("actions", "selftest-missing", "a.txt")
        captured = json.loads((WORK / "actions-selftest-missing-context.json").read_text())
        assert captured["tree"]["status"] == 404 and captured["file"]["status"] == 200
        assert captured["checkouts"]["body"]["checkouts"] == []
    finally:
        globals()["get"], globals()["identity"] = original_get, original_identity


if __name__ == "__main__":
    globals()[sys.argv[1]](*sys.argv[2:])
