#!/usr/bin/env python3
"""Noncredential Agent API evidence for the paid recovery/lifecycle AFT suite.

The native probe is an executable owned by the stack runner. It queries inside
that run's container and prints only agent_id, harness, native_id and native_root.
"""

import json
import os
import pathlib
import subprocess
import sys
from datetime import datetime, timezone
import urllib.parse
import urllib.request


BASE = os.environ["AFT_API_URL"].rstrip("/")
WS = os.environ["AFT_WS"]
OUT = pathlib.Path(os.environ["AFT_WORK_DIR"])


def get(path):
    with urllib.request.urlopen(BASE + path, timeout=20) as response:
        return json.load(response)


def agent_path(agent_id):
    return f"/api/workspaces/{urllib.parse.quote(WS)}/v1/agents/{urllib.parse.quote(agent_id)}"


def verify_repo():
    registry = get(f"/api/workspaces/{urllib.parse.quote(WS)}")
    assert registry.get("success") is True, "workspace registry read failed"
    data = registry["data"]
    assert data["id"] == WS, "workspace registry identity changed"
    repos = data["repos"]
    assert len(repos) == 1 and repos[0]["name"] == "source-repo", "unexpected workspace repos"
    canonical = data["path"].rstrip("/") + "/source-repo"
    assert repos[0]["path"] == canonical, "source repo is not the managed workspace import"
    assert os.environ["AFT_AGENT_FLOW_REPO"] == canonical, "runner repo differs from registry"
    (OUT / "live-recovery-repo.json").write_text(json.dumps({
        "workspace_id": WS, "workspace_path": data["path"],
        "repo_name": repos[0]["name"], "repo_path": canonical,
        "source": "GET /api/workspaces/{ws}",
    }, indent=2) + "\n")


def owned_id(label):
    name = f"live-recovery-{label}-{os.environ['RUN_ID']}"
    query = urllib.parse.urlencode({"name": name, "include_archived": "true"})
    agents = get(f"/api/workspaces/{urllib.parse.quote(WS)}/v1/agents?{query}")["agents"]
    matches = [a for a in agents if a["name"] == name
               and a["harness"] == os.environ["AFT_REAL_BACKEND"]
               and a["repo"] == os.environ["AFT_AGENT_FLOW_REPO"]]
    assert len(matches) == 1, f"expected one owned {name}, found {len(matches)}"
    agent_id = matches[0]["agent_id"]
    (OUT / f"live-recovery-{label}.id").write_text(agent_id)
    print(agent_id)


def events(agent_id):
    found, after = [], 0
    while True:
        page = get(agent_path(agent_id) + f"/events?after={after}&limit=500")
        batch = page["events"]
        found.extend(batch)
        if not page["more"]:
            return found
        assert batch and page["next"] > after, "event page did not advance"
        after = page["next"]


def native(agent_id):
    executable = os.environ.get("AFT_NATIVE_SESSION_PROBE")
    assert executable, "AFT_NATIVE_SESSION_PROBE is required for native continuity proof"
    try:
        result = subprocess.run([executable, agent_id], check=True, capture_output=True, text=True)
    except subprocess.CalledProcessError as error:
        # Do not copy arbitrary container stderr into AFT reports: it may include
        # auth or host paths. Only expose recognized, noncredential diagnostics.
        stderr = (error.stderr or "").lower()
        known = (
            ("cannot connect to podman", "Cannot connect to Podman"),
            ("owned loom-local is absent", "owned loom-local is absent"),
            ("compose project or service ownership mismatch", "compose project or service ownership mismatch"),
            ("owned manifest mismatch", "owned manifest mismatch"),
            ("runner project mismatch", "runner project mismatch"),
        )
        native_categories = {
            f"native probe: {reason}" for reason in (
                "agent-row-missing", "agent-name-not-run-owned", "harness-mismatch",
                "native-id-missing", "native-root-missing", "native-owner-mismatch",
            )
        }
        diagnostic = next((line.strip() for line in stderr.splitlines()
                           if line.strip() in native_categories), None)
        if diagnostic is None:
            diagnostic = next((safe for phrase, safe in known if phrase in stderr),
                              "unrecognized stderr suppressed")
        raise AssertionError(
            f"owned native-session probe exited {error.returncode}; sanitized stderr: {diagnostic}"
        ) from None
    value = json.loads(result.stdout)
    assert value.get("agent_id") == agent_id and value.get("harness") == os.environ["AFT_REAL_BACKEND"]
    assert isinstance(value.get("native_id"), str) and value["native_id"], "native probe returned no qualified ID"
    assert isinstance(value.get("native_root"), str), "native probe returned no qualified root"
    return {"native_id": value["native_id"], "native_root": value["native_root"]}


def capture(label, stage, with_native=False):
    agent_id = (OUT / f"live-recovery-{label}.id").read_text().strip()
    a = get(agent_path(agent_id))
    rows = events(agent_id)
    seqs = [e["seq"] for e in rows]
    ids = [e["event_id"] for e in rows]
    assert seqs == sorted(set(seqs)) and len(ids) == len(set(ids)), "duplicate/out-of-order saved events"
    snapshot = {
        "collected_at": datetime.now(timezone.utc).isoformat(),
        "source": "public Agent API Get + ListEvents; native from owned in-container probe when present",
        "agent_id": a["agent_id"], "name": a["name"], "state": a["state"],
        "harness": a["harness"], "last_seq": a["last_seq"],
        "running_turn_id": a["running_turn_id"],
        "waiting_count": len(a["waiting_messages"]),
        "events": [{"seq": e["seq"], "event_id": e["event_id"],
                    "kind": e["kind"], "turn_id": e["turn_id"]} for e in rows],
    }
    if stage == "waiting":
        assert snapshot["state"] == "active" and snapshot["waiting_count"] == 1, snapshot
        assert os.environ["RUN_ID"] in a["waiting_messages"][0]["text"], "wrong waiting message"
    if stage in ("settled", "final", "after"):
        assert snapshot["state"] == "idle" and snapshot["waiting_count"] == 0, snapshot
    if with_native:
        snapshot["native"] = native(agent_id)
    path = OUT / f"live-recovery-{label}-{stage}.json"
    path.write_text(json.dumps(snapshot, indent=2) + "\n")
    print(path)


def saved(label, stage):
    return json.loads((OUT / f"live-recovery-{label}-{stage}.json").read_text())


def compare(label, before, after, require_native=False):
    a, b = saved(label, before), saved(label, after)
    assert a["agent_id"] == b["agent_id"] and a["harness"] == b["harness"]
    if require_native:
        assert a["native"] == b["native"], "native ID/root changed across restart"
    first = a["events"]
    assert b["events"][:len(first)] == first, "saved history changed or lost events"
    assert b["last_seq"] >= a["last_seq"]


def delivery(label, marker):
    agent_id = (OUT / f"live-recovery-{label}.id").read_text().strip()
    rows = events(agent_id)
    before_ids = {e["event_id"] for e in saved(label, "first")["events"]}
    waiting_ids = {e["event_id"] for e in saved(label, "waiting")["events"]}
    new_waiting = [e for e in saved(label, "waiting")["events"]
                   if e["kind"] == "message.waiting" and e["event_id"] in waiting_ids - before_ids]
    assert len(new_waiting) == 2, f"expected one tool-turn send and one queued send, got {new_waiting}"
    matched = {"message.waiting": [new_waiting[-1]["event_id"]], "message.delivered": []}
    for e in rows:
        if e["kind"] == "message.delivered" and marker in json.dumps(e.get("payload")):
            matched["message.delivered"].append(e["event_id"])
    assert len(matched["message.waiting"]) == 1, matched
    assert len(matched["message.delivered"]) == 1, matched
    (OUT / f"live-recovery-{label}-receipt.json").write_text(json.dumps(matched, indent=2) + "\n")


def gone(label):
    agent_id = (OUT / f"live-recovery-{label}.id").read_text().strip()
    a = get(agent_path(agent_id))
    assert a["state"] == "deleted" and a["deleted_at"], "target was not tombstoned"
    listed = get(f"/api/workspaces/{urllib.parse.quote(WS)}/v1/agents?name={urllib.parse.quote(a['name'])}")["agents"]
    assert all(row["agent_id"] != agent_id for row in listed), "deleted target remains in the live roster"


if __name__ == "__main__":
    command, *args = sys.argv[1:]
    {"repo": verify_repo, "id": owned_id, "capture": capture, "compare": compare,
     "delivery": delivery, "gone": gone}[command](*args)
