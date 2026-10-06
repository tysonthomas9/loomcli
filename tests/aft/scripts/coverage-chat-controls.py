#!/usr/bin/env python3
"""Owned, nonsecret Agent API readbacks for live chat-control AFT cases."""

import json
import os
import re
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path


def env(name):
    value = os.environ.get(name)
    if not value:
        raise AssertionError(f"{name} is required")
    return value


ROOT = Path(env("AFT_WORK_DIR")) / "chat-controls"
RUN = env("RUN_ID")
WS = env("AFT_WS")
BASE = env("AFT_API_URL").rstrip("/")
PREFIX = f"/api/workspaces/{urllib.parse.quote(WS, safe='')}/v1"
MODEL = env("AFT_REAL_MODEL")
NAME = re.compile(rf"^cov-controls-[a-z-]+-{re.escape(RUN)}$")


def call(path, method="GET", body=None, key=None, status=200):
    headers = {"Accept": "application/json"}
    data = None
    if body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    if key:
        headers["Idempotency-Key"] = key
    req = urllib.request.Request(BASE + path, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=30) as response:
            code, raw = response.status, response.read()
    except urllib.error.HTTPError as exc:
        code, raw = exc.code, exc.read()
    value = json.loads(raw) if raw else None
    assert code == status, f"{method} request: expected HTTP {status}, got {code}; code={value.get('code') if isinstance(value, dict) else 'unknown'}"
    return value


def save(name, data):
    ROOT.mkdir(parents=True, exist_ok=True)
    (ROOT / name).write_text(json.dumps(data, indent=2, sort_keys=True) + "\n")


def saved(name):
    return json.loads((ROOT / name).read_text())


def aid(case):
    value = (ROOT / f"{case}.id").read_text().strip()
    assert re.fullmatch(r"agt_[A-Za-z0-9]+", value), "invalid owned agent id"
    return value


def agent(case):
    result = call(f"{PREFIX}/agents/{aid(case)}")
    assert result["agent_id"] == aid(case)
    assert NAME.fullmatch(result["name"]), "agent name is not run-owned"
    assert result["repo"] == env("AFT_AGENT_FLOW_REPO")
    assert result["harness"] == "opencode"
    return result


def events(case):
    result, after = [], 0
    while True:
        page = call(f"{PREFIX}/agents/{aid(case)}/events?after={after}&limit=500")
        result += page["events"]
        if not page["more"]:
            break
        assert page["next"] > after, "event cursor did not advance"
        after = page["next"]
    ids = [row["event_id"] for row in result]
    assert len(ids) == len(set(ids)), "duplicate saved events"
    return result


def browser(*args):
    return subprocess.check_output(["agent-browser", "--session", env("AFT_SESSION"), *args], text=True).strip()


def claim(case, preset="lead"):
    url = browser("get", "url").split("?", 1)[0]
    match = re.fullmatch(rf"{re.escape(env('AFT_BASE_URL'))}/ws/{re.escape(WS)}/chat/(agt_[A-Za-z0-9]+)", url)
    assert match, f"Chat route did not contain an Agent API ID: {url}"
    ROOT.mkdir(parents=True, exist_ok=True)
    (ROOT / f"{case}.id").write_text(match[1] + "\n")
    a = agent(case)
    assert a["name"] == f"cov-controls-{case}-{RUN}" and a["preset"] == preset, "wrong owned agent"
    save(f"{case}-identity.json", {k: a.get(k) for k in ("agent_id", "name", "repo", "preset", "harness", "model", "created_by_kind")})


def create(case, preset, model=None):
    name = f"cov-controls-{case}-{RUN}"
    body = {"preset": preset, "name": name, "repo": env("AFT_AGENT_FLOW_REPO"),
            "base_ref": "main", "overrides": {"harness": "opencode"}}
    if model == "target":
        model = MODEL
    elif model == "unknown":
        model = f"openai/cov-unknown-{RUN}"
    elif model == "custom":
        model = f"openai/cov-custom-{RUN}"
    if model is not None:
        body["overrides"]["model"] = model
    result = call(f"{PREFIX}/agents", "POST", body, f"{name}-create", 201)
    ROOT.mkdir(parents=True, exist_ok=True)
    (ROOT / f"{case}.id").write_text(result["agent_id"] + "\n")
    a = agent(case)
    assert a["name"] == name and a["preset"] == preset and a["created_by_kind"] == "user"
    if model:
        assert a["model"] == model
    save(f"{case}-create.json", {k: a.get(k) for k in ("agent_id", "name", "preset", "model", "model_unverified")})
    print(f"{case}: created owned Agent API actor {aid(case)}")


def catalog():
    c = call(f"{PREFIX}/harnesses/opencode/models")
    models = [m for p in c["providers"] for m in p["models"]]
    assert any(m["id"] == MODEL and m.get("source", "harness") == "harness" for m in models)
    alt = next((m for m in models if m["id"] != MODEL and
                m["id"].split("/", 1)[0] == MODEL.split("/", 1)[0] and
                m.get("source", "harness") == "harness"), None)
    assert alt, "two real connected catalog models are required for this journey"
    target = next(m for m in models if m["id"] == MODEL)
    effort = next((d for d in target.get("option_descriptors", []) if d["id"] == "effort"), None)
    assert effort and effort["type"] == "select" and len(effort["options"]) > 0
    selected = next((o for o in effort["options"] if o["id"] == "high"), None)
    assert selected, "the real target model has no declared high effort option"
    save("catalog.json", {"target": MODEL, "alternate": alt["id"],
                          "target_effort": "high", "declared_efforts": [o["id"] for o in effort["options"]]})
    print(f"catalog has target={MODEL}, alternate={alt['id']}, declared effort=high")


def pick(case, which):
    c = saved("catalog.json")
    wanted = c["target" if which == "target" else "alternate"]
    selector = f'li[role=option][title="{wanted}"]'
    browser("click", '[data-chat-provider-model-picker="true"]')
    browser("wait", '[role=dialog][aria-label="Choose a model"]')
    browser("fill", '[aria-label="Search models"]', wanted)
    browser("wait", selector)
    browser("click", selector)
    js = """(() => {
      const key = '__coverageChatControlsModel';
      const state = window[key] ||= {model: null, ready: false, pending: false};
      if (state.model !== MODEL) {state.model = MODEL; state.ready = false;}
      if (!state.ready && !state.pending) {
        state.pending = true;
        fetch(PATH, {cache: 'no-store'}).then(r => r.ok ? r.json() : null)
          .then(a => {state.ready = a?.agent_id === ID && a?.model === MODEL && a?.model_unverified === false;})
          .catch(() => {state.ready = false;})
          .finally(() => {state.pending = false;});
      }
      return state.ready;
    })()""".replace("MODEL", json.dumps(wanted)).replace("PATH", json.dumps(f"{PREFIX}/agents/{aid(case)}")).replace("ID", json.dumps(aid(case)))
    browser("wait", "--fn", js)
    a = agent(case)
    assert a["model"] == wanted and a["model_unverified"] is False, "UI model selection did not persist"
    save(f"{case}-{which}-selection.json", {"agent_id": aid(case), "model": wanted})
    print(f"UI selected saved catalog model {wanted}")


def check_model(case, which, effort=False):
    c = saved("catalog.json")
    wanted = c["target" if which == "target" else "alternate"]
    a = agent(case)
    assert a["model"] == wanted and a["model_unverified"] is False
    if effort:
        spec = json.loads(a["spec_json"])
        assert {"ID": "effort", "Value": "high"} in spec.get("Options", []), "effort choice was not saved"
    print(f"saved picker choice {wanted}; effort={effort}")


def turn(case, marker, stop="completed"):
    a, rows = agent(case), events(case)
    delivered = [e for e in rows if e["kind"] == "message.delivered" and marker in e["payload"].get("text", "")]
    assert len(delivered) == 1, f"expected one real delivered {marker} request"
    turn_id = delivered[0].get("turn_id")
    started = [e for e in rows if e["kind"] == "turn.started" and e["turn_id"] == turn_id]
    assert turn_id and len(started) == 1, "delivered request has no unique native turn"
    completed = [e for e in rows if e["kind"] == "agent.turn_completed" and
                 e["turn_id"] == turn_id and e["seq"] > delivered[0]["seq"]]
    assert len(completed) == 1 and completed[0]["payload"].get("stopReason") == stop, f"turn did not finish once as {stop}"
    assert a["running_turn_id"] is None, "agent did not become reusable"
    save(f"{case}-{marker}-turn.json", {"request_event_id": delivered[0]["event_id"],
                                          "turn_id": turn_id, "completion_event_id": completed[0]["event_id"],
                                          "stop_reason": completed[0]["payload"].get("stopReason"),
                                          "model_unavailable": "Model unavailable" in completed[0]["payload"].get("error", "")})


def ask(case, stage="first"):
    a, rows = agent(case), events(case)
    assert len(a["open_asks"]) == 1, "expected one real native pending ask"
    pending = a["open_asks"][0]
    opened = [e for e in rows if e["kind"] == "ask.opened" and e["payload"].get("askId") == pending["id"]]
    assert len(opened) == 1, "native ask is not saved exactly once"
    assert opened[0]["turn_id"] == a["running_turn_id"], "ask is bound to another turn"
    if stage == "questions":
        qs = pending.get("questions") or []
        assert pending["type"] == "question" and len(qs) == 2, "wanted one native two-question form"
        assert any(o["label"] == "Blue" for o in qs[0]["options"]), "first question options missing"
        assert any(o["label"] == "Medium" for o in qs[1]["options"]), "second question options missing"
    save(f"{case}-{stage}-ask.json", {"ask_id": pending["id"], "turn_id": opened[0]["turn_id"],
                              "opened_event_id": opened[0]["event_id"], "type": pending["type"],
                              "questions": pending.get("questions")})
    print(f"saved {pending['type']} ask {pending['id']} on turn {opened[0]['turn_id']}")


def resolved(case, stage, decision):
    identity = saved(f"{case}-{stage}-ask.json")
    a, rows = agent(case), events(case)
    assert not a["open_asks"], "ask still open after UI response"
    resolved_rows = [e for e in rows if e["kind"] == "ask.resolved" and
                     e["payload"].get("askId") == identity["ask_id"]]
    assert len(resolved_rows) == 1, "ask resolution not saved exactly once"
    assert resolved_rows[0]["turn_id"] == identity["turn_id"], "resolved another turn's ask"
    assert not [e for e in rows if e["kind"] == "ask.lost" and e["payload"].get("askId") == identity["ask_id"]]
    save(f"{case}-{stage}-resolution.json", {"ask_id": identity["ask_id"], "turn_id": identity["turn_id"],
                                      "decision": decision, "resolved_event_id": resolved_rows[0]["event_id"]})


def respond_receipt(case, stage):
    identity = saved(f"{case}-{stage}-ask.json")
    suffix = f"/v1/agents/{aid(case)}/asks/{identity['ask_id']}"
    script = f"performance.getEntriesByType('resource').filter(e => e.name.includes({json.dumps(suffix)})).map(e => e.responseStatus)"
    raw = browser("eval", script)
    try:
        statuses = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise AssertionError("browser response-status evidence unavailable") from exc
    assert statuses.count(204) == 1, f"expected one UI Respond HTTP 204; got {statuses}"
    save(f"{case}-{stage}-respond-http.json", {"ask_id": identity["ask_id"], "status": 204})


def approval_effect(case, marker, count, asks):
    rows = events(case)
    calls = [e for e in rows if e["kind"] == "item.completed" and
             e["payload"].get("itemKind") == "tool" and
             marker in (e["payload"].get("tool") or {}).get("output", "") and
             not (e["payload"].get("tool") or {}).get("failed")]
    opened = [e for e in rows if e["kind"] == "ask.opened"]
    assert len(calls) == int(count), f"expected {count} executed commands, got {len(calls)}"
    assert len(opened) == int(asks), f"expected {asks} native asks, got {len(opened)}"
    save(f"{case}-approval-effect.json", {"executed_tool_event_ids": [e["event_id"] for e in calls],
                                           "ask_event_ids": [e["event_id"] for e in opened]})


def question_answers(case):
    rows = events(case)
    identity = saved(f"{case}-questions-ask.json")
    assert identity["type"] == "question" and len(identity["questions"]) == 2
    tools = [e for e in rows if e["kind"] == "item.completed" and
             e["payload"].get("itemKind") == "tool" and
             "Blue" in (e["payload"].get("tool") or {}).get("output", "") and
             "Medium, please" in (e["payload"].get("tool") or {}).get("output", "")]
    assert len(tools) == 1, "native question tool did not receive both UI answers"
    save(f"{case}-question-effect.json", {"tool_event_id": tools[0]["event_id"],
                                           "ask_id": identity["ask_id"], "turn_id": identity["turn_id"]})


def lost(case):
    previous = saved(f"{case}-before-restart-ask.json")
    a, rows = agent(case), events(case)
    old = previous["ask_id"]
    lost_rows = [e for e in rows if e["kind"] == "ask.lost" and e["payload"].get("askId") == old]
    assert len(lost_rows) == 1 and lost_rows[0]["turn_id"] == previous["turn_id"], "old ask was not lost exactly once"
    assert len(a["open_asks"]) == 1 and a["open_asks"][0]["id"] != old, "fresh native reask missing"
    fresh = a["open_asks"][0]["id"]
    opened = [e for e in rows if e["kind"] == "ask.opened" and e["payload"].get("askId") == fresh]
    assert len(opened) == 1, "fresh ask.opened was not saved once"
    error = call(f"{PREFIX}/agents/{aid(case)}/asks/{old}", "POST",
                 {"decision": "allow_once"}, f"{case}-{RUN}-stale", 404)
    assert error.get("code") == "ask_not_found", "stale Respond was not refused"
    save(f"{case}-lost-and-reasked.json", {"old_ask_id": old, "lost_event_id": lost_rows[0]["event_id"],
                                          "fresh_ask_id": fresh, "fresh_opened_event_id": opened[0]["event_id"],
                                          "stale_respond_status": 404})


def roster(stage, expected=None):
    page = call(f"{PREFIX}/agents?limit=500")
    assert page.get("next") in ("", None), "roster was truncated"
    ids = sorted(a["agent_id"] for a in page["agents"] if
                 a["name"].startswith("cov-controls-") and a["name"].endswith("-" + RUN))
    if expected is not None:
        before = saved(f"roster-{expected}.json")["agent_ids"]
        assert ids == before, f"refused create mutated the roster: {before} -> {ids}"
    save(f"roster-{stage}.json", {"agent_ids": ids})


def history(case, stage, compare=None):
    ids = [e["event_id"] for e in events(case)]
    if compare:
        prior = saved(f"{case}-{compare}-history.json")["event_ids"]
        assert ids[:len(prior)] == prior, "later model/effort choice changed saved prior-turn events"
    save(f"{case}-{stage}-history.json", {"event_ids": ids})


def malformed():
    name = f"cov-controls-malformed-{RUN}"
    body = {"preset": "lead", "name": name, "repo": env("AFT_AGENT_FLOW_REPO"),
            "base_ref": "main", "overrides": {"harness": "opencode", "model": "openai/"}}
    error = call(f"{PREFIX}/agents", "POST", body, f"{name}-create", 400)
    assert error.get("code") == "preset_invalid" and "malformed model" in str(error)
    listing = call(f"{PREFIX}/agents?limit=500")["agents"]
    assert not any(a["name"] == name for a in listing), "malformed create left a roster row"
    save("malformed-400.json", {"status": 400, "code": error["code"], "name": name})


def custom(case, present):
    ident = f"openai/cov-custom-{RUN}"
    rows = call(f"{PREFIX}/harnesses/opencode/custom")["models"]
    models = [m for p in call(f"{PREFIX}/harnesses/opencode/models")["providers"] for m in p["models"]]
    matches = [m for m in models if m["id"] == ident and m.get("source") == "custom"]
    assert (ident in rows) == present and bool(matches) == present, "custom UI action disagrees with API/catalog"
    if present:
        a = agent(case)
        assert a["model"] == ident and a["model_unverified"] is False
        assert not any(e["kind"] == "model.unverified" for e in events(case)), "custom model got an unverified warning"
    save(f"{case}-custom-{'listed' if present else 'removed'}.json",
         {"model": ident, "present": present, "source": "custom" if present else None})


def unknown(case):
    a = agent(case)
    assert a["model"] == f"openai/cov-unknown-{RUN}" and a["model_unverified"] is True
    warning = [e for e in events(case) if e["kind"] == "model.unverified"]
    assert len(warning) == 1 and a["model"] in str(warning[0]["payload"]), "missing saved model warning"
    save(f"{case}-warning.json", {"warning_event_id": warning[0]["event_id"], "model": a["model"]})


def recover(case):
    a = agent(case)
    assert a["model"] == MODEL and a["model_unverified"] is False
    rows = events(case)
    failure = [e for e in rows if e["kind"] == "agent.turn_completed" and
               e["payload"].get("stopReason") == "failed" and
               "Model unavailable" in e["payload"].get("error", "")]
    assert len(failure) == 1, "unknown model did not produce one readable failed turn"
    warning = saved(f"{case}-warning.json")
    assert warning["warning_event_id"] in [e["event_id"] for e in rows], "warning disappeared"
    save(f"{case}-recovery.json", {"failed_event_id": failure[0]["event_id"], "saved_valid_model": MODEL})


def main():
    command, *args = sys.argv[1:]
    actions = {"claim": claim, "create": create, "catalog": catalog, "pick": pick,
               "check-model": check_model, "turn": turn, "ask": ask,
               "resolved": resolved, "malformed": malformed, "custom": custom,
               "unknown": unknown, "recover": recover, "approval-effect": approval_effect,
               "question-answers": question_answers, "lost": lost, "roster": roster,
               "history": history, "respond-receipt": respond_receipt}
    if command == "check-model" and len(args) == 3:
        actions[command](args[0], args[1], args[2] == "effort")
    elif command == "custom":
        actions[command](args[0], args[1] == "present")
    else:
        actions[command](*args)


if __name__ == "__main__":
    main()
