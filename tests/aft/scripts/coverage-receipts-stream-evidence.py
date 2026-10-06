#!/usr/bin/env python3
"""Public API and read-only browser-network evidence for owned real Agent runs."""

import json
import os
from pathlib import Path
import re
import subprocess
import sys
import urllib.parse
import urllib.error
import urllib.request

BASE = os.environ["AFT_API_URL"].rstrip("/")
WS = os.environ["AFT_WS"]
RUN = os.environ["RUN_ID"]
OUT = Path(os.environ["AFT_WORK_DIR"]) / "receipts-stream"
ROOT = f"/api/workspaces/{urllib.parse.quote(WS, safe='')}/v1"


def http(path, method="GET", body=None, key=None):
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Accept": "application/json"}
    if data is not None:
        headers["Content-Type"] = "application/json"
    if key:
        headers["Idempotency-Key"] = key
    with urllib.request.urlopen(urllib.request.Request(BASE + path, data=data, headers=headers, method=method), timeout=20) as r:
        return json.load(r)


def save(label, stage, value):
    OUT.mkdir(parents=True, exist_ok=True)
    (OUT / f"{label}-{stage}.json").write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def load(label, stage):
    return json.loads((OUT / f"{label}-{stage}.json").read_text())


def agent_id(label):
    value = (OUT / f"{label}.id").read_text().strip()
    assert re.fullmatch(r"agt_[A-Za-z0-9_-]+", value), "invalid owned Agent ID"
    return value


def path(label):
    return f"{ROOT}/agents/{agent_id(label)}"


def agent(label):
    return http(path(label))


def pages(label, kinds=None):
    result, after, boundary = [], 0, None
    while True:
        params = {"after": after, "limit": 2}
        if boundary is not None:
            params["snapshot"] = boundary
        if kinds:
            params["kind"] = ",".join(kinds)
        page = http(path(label) + "/events?" + urllib.parse.urlencode(params))
        boundary = page["snapshot_seq"] if boundary is None else boundary
        assert page["snapshot_seq"] == boundary, "ListEvents snapshot changed during paging"
        result.extend(page["events"])
        if not page["more"]:
            break
        assert page["next"] > after and page["events"], "page cursor did not advance"
        after = page["next"]
    seqs = [e["seq"] for e in result]
    ids = [e["event_id"] for e in result]
    assert seqs == sorted(set(seqs)) and len(ids) == len(set(ids)), "duplicate or unordered saved events"
    assert all(e["agent_id"] == agent_id(label) for e in result), "foreign saved event"
    return result


def browser(expression):
    raw = subprocess.check_output(["agent-browser", "--session", os.environ["AFT_SESSION"], "eval", expression], text=True).strip()
    try:
        return json.loads(raw)
    except json.JSONDecodeError:
        return raw.strip('"')


def preflight():
    assert os.environ["AFT_REAL_BACKEND"] == "opencode"
    assert Path(os.environ["AFT_AGENT_FLOW_REPO"]).name == "source-repo"
    assert Path(os.environ["AFT_SELECT_AGENT_MODEL"]).is_file()
    ws = http(f"/api/workspaces/{urllib.parse.quote(WS, safe='')}")
    data = ws.get("data", ws)
    assert len(data["repos"]) == 1
    assert data["repos"][0]["name"] == "source-repo"
    canonical = data["path"].rstrip("/") + "/source-repo"
    assert data["repos"][0]["path"] == canonical == os.environ["AFT_AGENT_FLOW_REPO"]


def claim(label):
    route = browser("location.pathname")
    match = re.fullmatch(rf"/ws/{re.escape(WS)}/chat/(agt_[A-Za-z0-9_-]+)", route)
    assert match, "UI did not open an Agent API chat"
    OUT.mkdir(parents=True, exist_ok=True)
    (OUT / f"{label}.id").write_text(match[1] + "\n")
    a = agent(label)
    assert a["name"] == f"coverage-rs-{label}-{RUN}" and a["agent_id"] == match[1]
    assert a["created_by_kind"] == "user" and a["parent_agent_id"] is None
    assert a["repo"] == os.environ["AFT_AGENT_FLOW_REPO"] and a["harness"] == "opencode"
    save(label, "identity", {"agent_id": a["agent_id"], "name": a["name"], "repo": a["repo"], "harness": a["harness"], "actor": "production UI user; local stack open-mode user identity"})


OBSERVER = r"""(() => {
  if (window.__coverageRsFetch) return 'already installed';
  const original = window.fetch.bind(window);
  window.__coverageRsCalls = [];
  window.__coverageRsFetch = true;
  window.fetch = async (input, init) => {
    const request = new Request(input, init);
    const url = new URL(request.url);
    const watched = /\/v1\/agents\/[^/]+\/messages(?:\/waiting)?$/.test(url.pathname) && ['POST','DELETE'].includes(request.method);
    const body = watched && request.method === 'POST' ? await request.clone().text() : '';
    const response = await original(input, init);
    if (watched) {
      let result = null;
      try { result = await response.clone().json(); } catch {}
      window.__coverageRsCalls.push({method:request.method, path:url.pathname, key:request.headers.get('Idempotency-Key'), body, status:response.status, result});
    }
    return response;
  };
  return 'installed';
})()"""


def observe():
    assert browser(OBSERVER) == "installed", "browser request observer was not installed"


def capture(label, stage):
    a, evs = agent(label), pages(label)
    calls = browser("window.__coverageRsCalls || []")
    assert isinstance(calls, list)
    assert all(c.get("key") and c.get("status") in (200, 201) for c in calls), "missing successful UI receipt"
    # Persist only the public fields needed for the oracle. Tool inputs and
    # outputs can contain private data and have no role in these assertions.
    safe_events = [{"agent_id": e["agent_id"], "seq": e["seq"], "event_id": e["event_id"],
                    "kind": e["kind"], "turn_id": e["turn_id"],
                    "payload": {k: e["payload"][k] for k in ("text", "inputKey", "sender") if k in e["payload"]}
                    if e["kind"] == "message.delivered" else {}} for e in evs]
    save(label, stage, {"agent_id": a["agent_id"], "state": a["state"], "turn_id": a["running_turn_id"],
                        "waiting": a["waiting_messages"], "events": safe_events, "ui_calls": calls,
                        "actor": "production UI user; local stack open-mode user identity", "evidence": "real OpenCode plus public Agent API"})


def waiting(label, stage, text):
    value = load(label, stage)
    assert value["state"] == "active" and len(value["waiting"]) == 1, value["waiting"]
    assert value["waiting"][0]["text"] == text, value["waiting"]


def withdrawn(label, stage):
    value = load(label, stage)
    assert value["state"] == "active" and not value["waiting"]
    assert sum(e["kind"] == "message.withdrawn" for e in value["events"]) == 1


def replay(label, calls_stage, state_stage, count):
    """Replay exact UI RequestIDs through the public user route; no new mutation."""
    before = load(label, state_stage)
    # Withdraw is not an idempotent receipt route: replay only Send RequestIDs.
    calls = [c for c in load(label, calls_stage)["ui_calls"] if c["method"] == "POST"][:int(count)]
    assert len(calls) == int(count) and calls, "UI request records absent"
    assert len({c["key"] for c in calls}) == len(calls), "UI reused a RequestID"
    for c in calls:
        assert c["path"] == path(label) + "/messages"
        result = http(c["path"], c["method"], json.loads(c["body"]) if c["body"] else None, c["key"])
        assert result == c["result"], "retry changed the saved public receipt"
    after = agent(label)
    assert after["waiting_messages"] == before["waiting"], "old RequestID changed waiting slots"
    kinds = {"message.waiting", "message.withdrawn"}
    before_ids = [e["event_id"] for e in before["events"] if e["kind"] in kinds]
    after_ids = [e["event_id"] for e in pages(label) if e["kind"] in kinds]
    assert after_ids == before_ids, "retry appended a waiting or withdrawn event"
    save(label, state_stage + "-replay", {"request_ids": [c["key"] for c in calls], "receipts": [c["result"] for c in calls], "slot_event_ids": before_ids})


def delivered(label, stage, text, absent=""):
    value = load(label, stage)
    rows = [e for e in value["events"] if e["kind"] == "message.delivered"]
    found = [e for e in rows if e["payload"].get("text") == text]
    assert len(found) == 1, f"expected one full persisted delivered text {text!r}"
    keys = [e["payload"].get("inputKey") for e in rows]
    assert all(keys) and len(keys) == len(set(keys)), "duplicate native input key in delivered history"
    if absent:
        assert all(e["payload"].get("text") != x for x in absent.split("|") for e in rows), "superseded text delivered"
    assert not value["waiting"], "waiting slot was not consumed"
    save(label, stage + "-delivery", {"event_id": found[0]["event_id"], "seq": found[0]["seq"], "text": text,
                                      "native_input_key": found[0]["payload"]["inputKey"],
                                      "all_delivered_ids": [e["event_id"] for e in rows]})


def prefix(label, first, last):
    before, after = load(label, first), load(label, last)
    assert before["agent_id"] == after["agent_id"]
    ids = [e["event_id"] for e in before["events"]]
    assert [e["event_id"] for e in after["events"][:len(ids)]] == ids, "saved history changed across recovery"


def native(label, stage, action="probe"):
    assert label == "native" and action in ("probe", "restart")
    helper = Path(os.environ["AFT_TESTS_DIR"]) / "scripts/coverage-receipts-stream-native.sh"
    assert os.access(helper, os.X_OK), "checked native-process helper missing"
    value = json.loads(subprocess.check_output([str(helper), action, agent_id(label)], text=True).strip().splitlines()[-1])
    assert value["agent_id"] == agent_id(label) and value["harness"] == "opencode"
    assert value["native_id"] and isinstance(value["native_root"], str)
    assert isinstance(value["service_pid"], int) and value["service_pid"] > 1
    save(label, stage + "-native", value)


def native_compare(label, first, last):
    a, b = load(label, first + "-native"), load(label, last + "-native")
    assert (a["agent_id"], a["harness"], a["native_id"], a["native_root"]) == (
        b["agent_id"], b["harness"], b["native_id"], b["native_root"]), "NativeRef ownership changed"
    assert a["service_pid"] != b["service_pid"], "OpenCode service process did not restart"


def native_count(label, stage):
    assert label in ("waiting", "native")
    key = load(label, stage + "-delivery")["native_input_key"]
    helper = Path(os.environ["AFT_TESTS_DIR"]) / "scripts/coverage-receipts-stream-native.sh"
    value = json.loads(subprocess.check_output([str(helper), "count", agent_id(label), key], text=True).strip().splitlines()[-1])
    assert value["agent_id"] == agent_id(label) and value["input_key"] == key
    assert value["native_user_message_count"] == 1, "native history contains missing or duplicate user input"
    save(label, stage + "-native-input", value)


def sse(label, stage, types, after_stage="zero", other_label="none"):
    """Bounded cursor replay: compare exact filtered rows to paged ListEvents."""
    allowed = types.split(",")
    after = 0 if after_stage == "zero" else load(label, after_stage)["events"][-1]["seq"]
    expected = [e for e in pages(label, allowed) if e["seq"] > after]
    assert expected, "filtered saved-event oracle is empty"
    other_ids = set()
    if other_label != "none":
        other_ids = {e["event_id"] for e in pages(other_label)}
        assert other_ids, "foreign-agent filter oracle is empty"
    try:
        token_result = http(f"/api/workspaces/{urllib.parse.quote(WS, safe='')}/events/token")
    except urllib.error.HTTPError as error:
        if error.code != 404:
            raise
        token_result = {"disabled": True}
    assert token_result.get("token") or token_result.get("disabled") is True, "SSE token exchange failed"
    params = {"agents": agent_id(label), "after": f"{agent_id(label)}:{after}", "types": types}
    if token_result.get("token"):
        params["token"] = token_result["token"]
    query = urllib.parse.urlencode(params)
    request = urllib.request.Request(BASE + ROOT + "/events?" + query, headers={"Accept": "text/event-stream"})
    got, frame = [], {}
    with urllib.request.urlopen(request, timeout=25) as response:
        assert response.headers.get_content_type() == "text/event-stream"
        while len(got) < len(expected):
            line = response.readline().decode("utf-8")
            assert line, "SSE ended before saved replay completed"
            line = line.rstrip("\r\n")
            if line.startswith("id:"):
                frame["id"] = line[3:].strip()
            elif line.startswith("data:"):
                frame["data"] = json.loads(line[5:].strip())
            elif not line and frame:
                if frame.get("data", {}).get("seq", 0) > 0:
                    event = frame["data"]
                    assert frame.get("id") == f"{event['agent_id']}:{event['seq']}"
                    got.append(event)
                frame = {}
    assert [e["event_id"] for e in got] == [e["event_id"] for e in expected], "SSE replay differs from ListEvents"
    assert got == expected, "SSE payload differs from persisted events"
    assert not ({e["event_id"] for e in got} & other_ids), "agent filter leaked a foreign event"
    assert all(e["kind"] in allowed and e["agent_id"] == agent_id(label) for e in got)
    assert all(e["seq"] > 0 for e in got), "unexpected live-only notice in saved replay"
    save(label, stage + "-sse", {"types": allowed, "event_ids": [e["event_id"] for e in got],
                                  "texts": [e["payload"].get("text") for e in got if e["kind"] == "message.delivered"],
                                  "cursor_from": f"{agent_id(label)}:{after}", "cursor_to": f"{agent_id(label)}:{got[-1]['seq']}",
                                  "classification": "ordinary saved-event replay; no feed.gap asserted"})


def cleanup():
    for file in OUT.glob("*.id"):
        label = file.stem
        expected = f"coverage-rs-{label}-{RUN}"
        a = agent(label)
        if a["name"] == expected and a["repo"] == os.environ["AFT_AGENT_FLOW_REPO"] and not a.get("archived_at"):
            http(path(label) + "/archive", "POST", {"reason": "cancelled"}, f"coverage-rs-{RUN}-{label}-archive")


if __name__ == "__main__":
    commands = {"preflight": preflight, "claim": claim, "observe": observe, "capture": capture,
                "waiting": waiting, "withdrawn": withdrawn, "replay": replay, "delivered": delivered,
                "prefix": prefix, "sse": sse, "native": native, "native-compare": native_compare,
                "native-count": native_count,
                "cleanup": cleanup}
    commands[sys.argv[1]](*sys.argv[2:])
