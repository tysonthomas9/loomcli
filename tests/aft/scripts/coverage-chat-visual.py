#!/usr/bin/env python3
"""Read-only saved-event and browser oracles for the real chat visual journeys."""

import base64
import hashlib
import json
import math
import os
import re
import secrets
import selectors
import shutil
import subprocess
import sys
import time
import urllib.parse
import urllib.request
from pathlib import Path


def required(name):
    value = os.environ.get(name, "")
    if not value:
        raise ValueError(f"{name} is required")
    return value


WORK = Path(required("AFT_WORK_DIR")) / "chat-visual"
WS = required("AFT_WS")
RUN = required("RUN_ID")
API = required("AFT_API_URL").rstrip("/")
PREFIX = f"/api/workspaces/{urllib.parse.quote(WS, safe='')}/v1/agents"
NAMES = {case: f"aft-{RUN}-cov-visual-{case}" for case in ("render", "input")}
MOBILE_WS_NAMES = [f"aft-{RUN}-visual-ws-{index}" for index in range(1, 4)]


def request(path, method="GET", body=None, key=None, expected_status=None):
    headers = {"Accept": "application/json"}
    data = None
    if body is not None:
        headers["Content-Type"] = "application/json"
        data = json.dumps(body).encode()
    if key:
        headers["Idempotency-Key"] = key
    with urllib.request.urlopen(
        urllib.request.Request(API + path, data=data, headers=headers, method=method), timeout=15
    ) as response:
        if expected_status is not None:
            assert response.status == expected_status, (method, path, response.status)
        raw = response.read()
        if response.status == 204:
            assert not raw, "204 response unexpectedly carried a body"
            return None
        return json.loads(raw)


def self_test_request_204():
    from io import BytesIO
    from unittest.mock import patch

    class Empty204(BytesIO):
        status = 204

    with patch("urllib.request.urlopen", return_value=Empty204(b"")):
        assert request(f"{PREFIX}/agt_self_test/archive", "POST", {"reason": "cancelled"}) is None


def browser(*args, timeout=None):
    return subprocess.check_output(
        ["agent-browser", "--session", required("AFT_SESSION"), *args], text=True, timeout=timeout
    ).strip()


def evaluate(js, timeout=None):
    raw = browser("eval", "-b", base64.b64encode(js.encode()).decode(), timeout=timeout)
    return json.loads(raw)


def write(name, value):
    WORK.mkdir(parents=True, exist_ok=True)
    (WORK / name).write_text(json.dumps(value, indent=2, ensure_ascii=False, sort_keys=True) + "\n")


def agent_id(case):
    value = (WORK / f"{case}.id").read_text().strip()
    assert re.fullmatch(r"agt_[A-Za-z0-9_-]+", value), value
    return value


def events(case):
    result, after = [], 0
    while True:
        page = request(f"{PREFIX}/{agent_id(case)}/events?after={after}&limit=500")
        result.extend(page["events"])
        if not page["more"]:
            break
        new = page["next"]
        assert new > after, "saved event cursor did not advance"
        after = new
    ids = [e["event_id"] for e in result]
    seqs = [e["seq"] for e in result]
    assert len(ids) == len(set(ids)) and len(seqs) == len(set(seqs)), "duplicate saved event"
    assert seqs == sorted(seqs), "saved event order changed"
    return result


def current(case):
    path = evaluate("location.pathname")
    match = re.fullmatch(rf"/ws/{re.escape(WS)}/chat/(agt_[A-Za-z0-9_-]+)", path)
    assert match, f"current page is not this workspace's Chat: {path!r}"
    assert match[1] == agent_id(case), f"Chat changed agents: {match[1]} != {agent_id(case)}"


def preflight():
    assert required("AFT_REAL_BACKEND") == "opencode"
    repo = required("AFT_AGENT_FLOW_REPO")
    assert Path(repo).is_absolute() and Path(repo).name == "source-repo", repo
    workspace = request(f"/api/workspaces/{urllib.parse.quote(WS, safe='')}")
    source = [r for r in (workspace.get("data") or workspace)["repos"] if r.get("name") == "source-repo"]
    assert len(source) == 1 and source[0]["path"] == repo, source
    request(f"/api/workspaces/{urllib.parse.quote(WS, safe='')}/v1/harnesses/opencode")
    fixture = Path(required("AFT_TESTS_DIR")).parent / "fixtures/slack-clone/README.md"
    assert "- `npm test` runs the tests." in fixture.read_text(), fixture


def claim(case):
    assert case in NAMES, case
    path = evaluate("location.pathname")
    match = re.fullmatch(rf"/ws/{re.escape(WS)}/chat/(agt_[A-Za-z0-9_-]+)", path)
    assert match, f"New Agent did not open Chat: {path!r}"
    WORK.mkdir(parents=True, exist_ok=True)
    (WORK / f"{case}.id").write_text(match[1] + "\n")
    a = request(f"{PREFIX}/{match[1]}")
    assert a["name"] == NAMES[case] and a["agent_id"] == match[1], a
    assert a["repo"] == required("AFT_AGENT_FLOW_REPO") and a["harness"] == "opencode", a
    assert a["preset"] == "lead" and a["created_by_kind"] == "user", a
    assert a["parent_agent_id"] is None, a
    write(f"{case}-identity.json", a)


def snapshot(case, stage):
    current(case)
    write(f"{case}-{stage}.json", {"agent": request(f"{PREFIX}/{agent_id(case)}"), "events": events(case)})


def shot(case, stage):
    current(case)
    WORK.mkdir(parents=True, exist_ok=True)
    browser("screenshot", str(WORK / f"chat-visual-{case}-{stage}.png"))


def long_text():
    prefix = '<img src="x" onerror="alert(1)"> <script>alert(2)</script> &lt;b&gt;literal&lt;/b&gt;'
    return f"VISUAL_{RUN} " + prefix + "\n" + " ".join(
        ["The narrow chat bubble keeps this exact harmless sentence."] * 160)


INPUT_STATE_JS = """(() => { const t=document.querySelector('textarea[aria-label=Message]');
  const style=t && getComputedStyle(t);
  return {route:location.pathname, focused:document.activeElement===t,
    length:t?.value.length??null, selectionStart:t?.selectionStart??null,
    selectionEnd:t?.selectionEnd??null, value:t?.value??null,
    textareaCount:document.querySelectorAll('textarea[aria-label=Message]').length,
    scrollTop:t?.scrollTop??null, scrollHeight:t?.scrollHeight??null,
    clientHeight:t?.clientHeight??null, textColor:style?.color??null,
    visibility:style?.visibility??null}; })()"""


def input_value_shape(value):
    if not isinstance(value, str):
        return {"value_type": type(value).__name__}
    return {"value_sha256": hashlib.sha256(value.encode()).hexdigest(),
            "whitespace_count": sum(c.isspace() for c in value),
            "newline_count": value.count("\n"),
            "alphanumeric_count": sum(c.isalnum() for c in value),
            "printable_count": sum(c.isprintable() for c in value)}


def input_progress(stage, status, state=None, expected_length=None):
    state = state or {}
    entry = {"stage": stage, "status": status,
          "route": state.get("route"), "focused": state.get("focused"),
          "current_length": state.get("length"), "expected_length": expected_length,
          "selection_start": state.get("selectionStart"), "selection_end": state.get("selectionEnd"),
          "textarea_count": state.get("textareaCount"), "scroll_top": state.get("scrollTop"),
          "scroll_height": state.get("scrollHeight"), "client_height": state.get("clientHeight"),
          "text_color": state.get("textColor"), "visibility": state.get("visibility"),
          **input_value_shape(state.get("value"))}
    write("input-long-progress.json", entry)
    history_path = WORK / "input-long-history.json"
    history = json.loads(history_path.read_text()) if history_path.exists() else {
        "total": 0, "first": [], "recent": [], "failures": []}
    assert isinstance(history, dict) and isinstance(history.get("recent"), list), "invalid long-input diagnostic history"
    history["total"] += 1
    if len(history["first"]) < 4:
        history["first"].append(entry)
    history["recent"] = [*history["recent"], entry][-32:]
    if status in ("CalledProcessError", "TimeoutExpired", "total-budget-exhausted", "oracle-failed"):
        history["failures"] = [*history["failures"], entry][-8:]
    write("input-long-history.json", history)


def input_timeout(stage, deadline, state, expected_length):
    remaining = deadline - time.monotonic()
    if remaining <= 0:
        input_progress(stage, "total-budget-exhausted", state, expected_length)
        raise AssertionError(f"long input exceeded its 140s command budget at {stage}")
    return min(20, remaining)


def input_action(stage, state, expected_length, deadline, *args):
    input_progress(stage, "action-started", state, expected_length)
    try:
        browser(*args, timeout=input_timeout(stage, deadline, state, expected_length))
    except (subprocess.TimeoutExpired, subprocess.CalledProcessError) as exc:
        input_progress(stage, type(exc).__name__, state, expected_length)
        raise AssertionError(f"real composer action stalled or failed at {stage}") from None
    input_progress(stage, "action-returned", state, expected_length)


def input_readback(stage, previous, expected_length, deadline):
    input_progress(stage, "readback-started", previous, expected_length)
    try:
        state = evaluate(INPUT_STATE_JS, timeout=input_timeout(stage, deadline, previous, expected_length))
    except (subprocess.TimeoutExpired, subprocess.CalledProcessError) as exc:
        input_progress(stage, type(exc).__name__, previous, expected_length)
        raise AssertionError(f"real composer readback stalled or failed at {stage}") from None
    input_progress(stage, "readback-returned", state, expected_length)
    assert state["route"] == f"/ws/{WS}/chat/{agent_id('input')}", "long input changed Chat route"
    assert isinstance(state["value"], str) and state["length"] == len(state["value"]), "real composer textarea missing"
    return state


MAX_CLEAR_DRAFT_CHARS = 1024
MAX_CLEAR_NAV_KEYS = 128
MAX_CLEAR_DELETE_KEYS = 1024


def require_clear_state(stage, state, expected, selection=None):
    valid = state.get("focused") is True and state.get("value") == expected
    start, end = state.get("selectionStart"), state.get("selectionEnd")
    valid = valid and type(start) is int and type(end) is int and 0 <= start == end <= len(expected)
    if selection is not None:
        valid = valid and start == selection
    if not valid:
        input_progress(stage, "oracle-failed", state, len(expected))
        raise AssertionError(f"real composer keyboard clear changed focus, selection or draft at {stage}")


def clear_draft(initial, deadline):
    original = initial["value"]
    if len(original) > MAX_CLEAR_DRAFT_CHARS or len(original) > MAX_CLEAR_DELETE_KEYS:
        input_progress("clear-limit", "oracle-failed", initial, 0)
        raise AssertionError(f"real composer draft exceeds bounded keyboard-clear length ({len(original)})")
    selector = "textarea[aria-label=Message]"
    input_action("clear-focus", initial, len(original), deadline, "focus", selector)
    state = input_readback("after-clear-focus", initial, len(original), deadline)
    require_clear_state("after-clear-focus", state, original)
    for index in range(MAX_CLEAR_NAV_KEYS):
        if state["selectionStart"] == 0:
            break
        previous_position = state["selectionStart"]
        input_action(f"clear-home-{index + 1}", state, len(original), deadline, "press", "Home")
        state = input_readback(f"after-clear-home-{index + 1}", state, len(original), deadline)
        require_clear_state(f"after-clear-home-{index + 1}", state, original)
        if state["selectionStart"] == 0:
            break
        input_action(f"clear-up-{index + 1}", state, len(original), deadline, "press", "ArrowUp")
        state = input_readback(f"after-clear-up-{index + 1}", state, len(original), deadline)
        require_clear_state(f"after-clear-up-{index + 1}", state, original)
        if state["selectionStart"] >= previous_position:
            input_progress(f"after-clear-up-{index + 1}", "oracle-failed", state, 0)
            raise AssertionError("real composer caret did not move toward the start")
    require_clear_state("before-clear-delete", state, original, selection=0)
    for index in range(len(original)):
        input_action(f"clear-delete-{index + 1}", state, len(original) - index - 1,
                     deadline, "press", "Delete")
        state = input_readback(f"after-clear-delete-{index + 1}", state,
                               len(original) - index - 1, deadline)
        require_clear_state(f"after-clear-delete-{index + 1}", state,
                            original[index + 1:], selection=0)
    return state


def fill_long():
    source = long_text()
    assert len(source) > 5000
    deadline = time.monotonic() + 140
    state = input_readback("initial", None, None, deadline)
    state = clear_draft(state, deadline)
    state = input_readback("after-clear", state, 0, deadline)
    assert state["focused"] and state["value"] == "", \
        f"multiline draft was not cleared through the real composer (remaining length: {state['length']})"
    previous = ""
    for index, prefix in enumerate(long_text_prefixes(source)):
        input_action(f"insert-{index + 1}", state, len(prefix), deadline,
                     "keyboard", "inserttext", prefix[len(previous):])
        state = input_readback(f"after-insert-{index + 1}", state, len(prefix), deadline)
        assert state["value"] == prefix, \
            f"real keyboard insertion changed/truncated long input at chunk {index + 1} (length {state['length']} != {len(prefix)})"
        previous = prefix
    write("input-source.json", {"text": source, "length": len(source)})


def long_text_prefixes(source, size=1024):
    assert source and size > 0
    return [source[:end] for end in range(size, len(source), size)] + [source]


def self_test_long_text_prefixes():
    import tempfile
    from unittest.mock import patch

    source = long_text()
    prefixes = long_text_prefixes(source)
    assert len(source) > 5000 and all(0 < len(p) <= len(source) for p in prefixes)
    assert prefixes[-1] == source and all(len(b) - len(a) <= 1024 for a, b in zip(["", *prefixes[:-1]], prefixes))
    assert "".join(b[len(a):] for a, b in zip(["", *prefixes[:-1]], prefixes)) == source
    assert '<img src="x" onerror="alert(1)">' in source and "\n" in source
    with tempfile.TemporaryDirectory() as directory, patch.dict(globals(), {"WORK": Path(directory)}):
        input_progress("initial", "readback-returned", {"value": " \nX", "length": 3}, 3)
        input_progress("after-clear", "readback-returned", {"value": "", "length": 0}, 0)
        history = json.loads((WORK / "input-long-history.json").read_text())
        assert history["total"] == 2
        assert [(item["stage"], item["current_length"]) for item in history["recent"]] == [
            ("initial", 3), ("after-clear", 0)]
        assert history["recent"][0]["whitespace_count"] == 2 and history["recent"][0]["newline_count"] == 1
        assert history["recent"][0]["alphanumeric_count"] == 1 and history["recent"][1]["whitespace_count"] == 0
        assert all("value" not in item for item in history["recent"]), "long-input diagnostic stored literal text"


def self_test_clear_draft():
    import tempfile
    from unittest.mock import patch

    class Composer:
        def __init__(self, value, fault=None):
            self.value = self.react_value = value
            self.position = len(value)
            self.focused = False
            self.fault = fault
            self.deleted = 0
            self.commands = []

        def browser(self, *args, timeout=None):
            self.commands.append(args)
            if args[0] == "focus":
                self.focused = True
            elif args == ("press", "Home"):
                if self.fault != "stuck-navigation":
                    self.position = self.value.rfind("\n", 0, self.position) + 1
            elif args == ("press", "ArrowUp"):
                if self.fault != "stuck-navigation":
                    start = self.value.rfind("\n", 0, self.position) + 1
                    self.position = self.value.rfind("\n", 0, max(0, start - 1)) + 1
            elif args == ("press", "Delete"):
                if self.fault == "failed-delete":
                    raise subprocess.CalledProcessError(2, ["agent-browser", "press", "Delete"])
                if self.fault != "no-op-delete" and self.position < len(self.value):
                    self.value = self.value[:self.position] + self.value[self.position + 1:]
                    self.react_value = self.value
                self.deleted += 1
                if self.fault == "lost-focus":
                    self.focused = False
            else:
                raise AssertionError(f"unexpected browser action in clear test: {args}")
            return "done"

        def evaluate(self, _script, timeout=None):
            return {"route": f"/ws/{WS}/chat/agt_selftest", "focused": self.focused,
                    "length": len(self.value), "selectionStart": self.position,
                    "selectionEnd": self.position, "value": self.value}

    def exercise(value, fault=None):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "input.id").write_text("agt_selftest\n")
            composer = Composer(value, fault)
            with patch.dict(globals(), {"WORK": root, "browser": composer.browser,
                                        "evaluate": composer.evaluate}):
                initial = input_readback("initial", None, len(value), time.monotonic() + 10)
                if fault or len(value) > MAX_CLEAR_DRAFT_CHARS:
                    try:
                        clear_draft(initial, time.monotonic() + 10)
                    except AssertionError:
                        pass
                    else:
                        raise AssertionError(f"keyboard clear accepted {fault or 'oversized draft'}")
                else:
                    final = clear_draft(initial, time.monotonic() + 10)
                    assert final["value"] == composer.react_value == "" and final["focused"]
                    assert composer.deleted == len(value)
                    assert all(args[0] in ("focus", "press") for args in composer.commands)
                history = json.loads((root / "input-long-history.json").read_text())
                assert len(history["recent"]) <= 32 and len(history["first"]) <= 4
                assert all("value" not in row for row in history["recent"])
                if fault in ("failed-delete", "no-op-delete", "lost-focus", "stuck-navigation"):
                    assert history["failures"], f"failure diagnostic missing for {fault}"
                if fault == "failed-delete":
                    assert history["failures"][-1]["status"] == "CalledProcessError"
                    assert history["failures"][-1]["stage"] == "clear-delete-1"
                return composer, history

    composer, history = exercise("first line\nsecond line\nthird line")
    assert history["total"] > 100 and composer.commands.count(("press", "Delete")) == 33
    assert history["first"][1]["stage"] == "initial"
    exercise("one line")
    exercise("")
    for fault in ("lost-focus", "failed-delete", "no-op-delete", "stuck-navigation"):
        exercise("first line\nsecond line", fault)
    exercise("x" * (MAX_CLEAR_DRAFT_CHARS + 1))


def draft(stage):
    state = evaluate("""(() => { const t=document.querySelector('textarea[aria-label=Message]');
      const forms=document.querySelectorAll('form[data-chat-composer-form=true]');
      const sends=forms[0]?.querySelectorAll('button[type=submit]')||[];
      const send=sends[0];
      return {value:t?.value??null, height:t?.getBoundingClientRect().height??null,
        overflow:t?getComputedStyle(t).overflowY:null, focused:document.activeElement===t,
        formCount:forms.length, sendCount:sends.length, sendDisabled:send?.disabled??null,
        sendTitle:send?.title??null, rows:document.querySelectorAll('[data-testid=chat-transcript] li').length}; })()""")
    if stage == "empty":
        assert_empty_draft(state)
    elif stage == "focused":
        assert state["focused"] and state["height"] >= 70, state
    elif stage == "newline":
        assert state["value"] == "first line\n" and state["rows"] == 0, state
        assert state["height"] >= 70, state
    elif stage == "grown":
        assert "\n" in state["value"] and 70 < state["height"] <= 200, state
    else:
        raise ValueError(stage)
    write(f"draft-{stage}.json", state)


def assert_empty_draft(state):
    assert state.get("value") == "" and state.get("rows") == 0, state
    assert state.get("formCount") == 1 and state.get("sendCount") == 1, \
        f"expected one real Chat composer and submit button: {state}"
    assert state.get("sendDisabled") is True and state.get("sendTitle") == "Type a message to send", \
        f"empty Chat submit button was not disabled for an empty message: {state}"


def self_test_empty_draft():
    good = {"value": "", "rows": 0, "formCount": 1, "sendCount": 1,
            "sendDisabled": True, "sendTitle": "Type a message to send"}
    assert_empty_draft(good)
    for wrong in ({"sendCount": 0}, {"sendDisabled": None}, {"sendDisabled": False},
                  {"sendTitle": "Send message"}, {"rows": 1}, {"value": "x"}):
        try:
            assert_empty_draft(good | wrong)
        except AssertionError:
            pass
        else:
            raise AssertionError(f"empty composer selector negative passed: {wrong}")


def rename(case):
    current(case)
    expected = NAMES[case] + "-renamed"
    a = request(f"{PREFIX}/{agent_id(case)}")
    assert a["name"] == expected, a
    shown = evaluate("document.querySelector('section[aria-label=" + json.dumps("Agent chat") + "] header h2')?.textContent")
    assert shown == expected, (shown, expected)
    write(f"{case}-renamed.json", a)


def rename_reloaded(case):
    current(case)
    original = json.loads((WORK / f"{case}-renamed.json").read_text())
    saved = request(f"{PREFIX}/{agent_id(case)}")
    shown = evaluate("document.querySelector('section[aria-label=\"Agent chat\"] header h2')?.textContent")
    assert saved["name"] == original["name"] == shown == NAMES[case] + "-renamed", (saved, shown)
    write(f"{case}-rename-reloaded.json", {"agent": saved, "header": shown})


def rename_restored(case):
    current(case)
    saved = request(f"{PREFIX}/{agent_id(case)}")
    shown = evaluate("document.querySelector('section[aria-label=\"Agent chat\"] header h2')?.textContent")
    assert saved["name"] == shown == NAMES[case], (saved, shown)
    write(f"{case}-rename-restored.json", {"agent": saved, "header": shown})


def rename_restored_reloaded(case):
    current(case)
    before = json.loads((WORK / f"{case}-rename-restored.json").read_text())
    saved = request(f"{PREFIX}/{agent_id(case)}")
    shown = evaluate("document.querySelector('section[aria-label=\"Agent chat\"] header h2')?.textContent")
    assert saved["agent_id"] == before["agent"]["agent_id"] == agent_id(case), (saved, before)
    assert saved["name"] == before["agent"]["name"] == before["header"] == shown == NAMES[case], \
        (saved, before, shown)
    write(f"{case}-rename-restored-reloaded.json", {"agent": saved, "header": shown})


def assert_input_journey_order(steps):
    def run_index(command):
        matches = [i for i, step in enumerate(steps)
                   if f'coverage-chat-visual.py" {command}' in step.get("run", "")]
        assert len(matches) == 1, f"expected one {command} step, got {matches}"
        return matches[0]

    declared = "aft-${RUN_ID}-cov-visual-input"
    def name_fill(value):
        matches = [i for i, step in enumerate(steps)
                   if step.get("fill") == {"label": "Agent name", "value": value}]
        assert len(matches) == 1, f"expected one header name fill for {value}: {matches}"
        return matches[0]

    grown = run_index("draft grown")
    long = run_index("fill-long")
    collapsed = run_index("input-check collapsed")
    full = run_index("input-check all")
    reloaded = run_index("input-reload-check")
    mobile = run_index("mobile-switcher 360")
    renamed = run_index("rename input")
    renamed_reloaded = run_index("rename-reloaded input")
    restored = run_index("rename-restored input")
    restored_reloaded = run_index("rename-restored-reloaded input")
    suffix_fill = name_fill(declared + "-renamed")
    original_fill = name_fill(declared)
    assert grown < long < collapsed < full < reloaded < mobile < suffix_fill < renamed < renamed_reloaded < original_fill < restored < restored_reloaded, \
        "input, saved text and mobile checks must precede both header commits"
    assert steps[long + 1].get("press") == "Enter", "long literal text must be sent with composer Enter"
    assert all(step.get("press") != "Enter" and
               not (isinstance(step.get("fill"), dict) and step["fill"].get("label") == "Agent name")
               for step in steps[:long]), "header Enter/rename occurred before long input"
    assert steps[suffix_fill - 1].get("click") == {"role": "button", "name": "Rename agent", "exact": True}
    assert steps[suffix_fill + 1].get("click") == {"testid": "harness-label"}, \
        "first header rename must commit through a real blur click"
    assert steps[suffix_fill + 2].get("wait", {}).get("fn") and renamed == suffix_fill + 3
    assert steps[renamed + 1].get("reload") is True and steps[renamed + 2].get("wait", {}).get("fn")
    assert renamed_reloaded == renamed + 3, "blur rename must retain saved API and reload readbacks"
    assert steps[original_fill - 1].get("click") == {"role": "button", "name": "Rename agent", "exact": True}
    assert steps[original_fill + 1].get("press") == "Enter", "original name must be committed by header Enter"
    assert steps[original_fill + 2].get("wait", {}).get("fn") and restored == original_fill + 3
    assert steps[restored + 1].get("reload") is True and steps[restored + 2].get("wait", {}).get("fn")
    assert restored_reloaded == restored + 3, "Enter restore must retain saved API and reload readbacks"


def self_test_input_journey_order():
    from copy import deepcopy
    import yaml

    suite = yaml.safe_load((Path(__file__).parents[1] / "live-agent-coverage-suites/chat-visual.test.yaml").read_text())
    assert suite["suite"] == "live-chat-visual" and len(suite["tests"]) == 2
    steps = next(test["steps"] for test in suite["tests"] if test["name"].startswith("chat composer and long"))
    assert_input_journey_order(steps)

    def rejects(change):
        candidate = deepcopy(steps)
        change(candidate)
        try:
            assert_input_journey_order(candidate)
        except AssertionError:
            return
        raise AssertionError("input journey order accepted a missing UI0 or saved-text proof")

    def index(command):
        return next(i for i, step in enumerate(steps)
                    if f'coverage-chat-visual.py" {command}' in step.get("run", ""))

    rejects(lambda c: c.insert(index("fill-long"), c.pop(index("rename input") - 3)))
    rejects(lambda c: c[index("rename input") - 2].update(click={"role": "button", "name": "Rename agent"}))
    rejects(lambda c: c.pop(index("rename input") + 1))
    rejects(lambda c: c[index("rename-restored input") - 2].update(press="Shift+Enter"))
    rejects(lambda c: c.pop(index("input-reload-check")))
    rejects(lambda c: c.pop(index("rename-restored-reloaded input")))


def self_test_restored_reload():
    from tempfile import TemporaryDirectory
    from unittest.mock import patch

    with TemporaryDirectory() as folder, patch.dict(globals(), WORK=Path(folder)):
        original = {"agent_id": "agt_input", "name": NAMES["input"]}
        write("input-rename-restored.json", {"agent": original, "header": original["name"]})

        def check(saved, header, should_pass):
            with patch.dict(globals(), current=lambda case: None, agent_id=lambda case: "agt_input",
                            request=lambda path: saved, evaluate=lambda script: header):
                try:
                    rename_restored_reloaded("input")
                except AssertionError:
                    assert not should_pass
                else:
                    assert should_pass, "reload accepted changed saved identity or title"

        check(original, original["name"], True)
        assert json.loads((WORK / "input-rename-restored-reloaded.json").read_text())["agent"] == original
        for saved, header in (({**original, "agent_id": "agt_other"}, original["name"]),
                              ({**original, "name": "changed"}, original["name"]),
                              (original, "changed")):
            check(saved, header, False)
        (WORK / "input-rename-restored.json").unlink()
        with patch.dict(globals(), current=lambda case: None, agent_id=lambda case: "agt_input",
                        request=lambda path: original, evaluate=lambda script: original["name"]):
            try:
                rename_restored_reloaded("input")
            except FileNotFoundError:
                pass
            else:
                raise AssertionError("reload accepted missing pre-reload rename receipt")


def skip_link(stage):
    current("input")
    result = evaluate("""(() => {const a=document.querySelector('a[href="#main-content"]');
      const m=document.querySelector('main#main-content'), r=a?.getBoundingClientRect();
      return {text:a?.textContent?.trim(), focused:document.activeElement===a,
        focusVisible:a?.matches(':focus-visible')===true,
        inViewport:!!r&&r.bottom>0&&r.top<innerHeight, hash:location.hash,
        focusInMain:!!m&&m.contains(document.activeElement)&&m!==document.activeElement};})()""")
    assert result["text"] == "Skip to main content", result
    if stage == "hidden":
        assert not result["inViewport"] and not result["focused"], result
    elif stage == "tab":
        assert result["focused"] and result["inViewport"], result
    elif stage == "entered":
        assert result["hash"] == "#main-content" and result["focusInMain"], result
    elif stage == "mouse_focus":
        assert result["focused"] and result["inViewport"] is result["focusVisible"], result
    else:
        raise ValueError(stage)
    write(f"input-skip-link-{stage}.json", result)
    shot("input", f"skip-link-{stage}")


SKIP_PROBE_JS = r"""(async () => {
  const action=__ACTION__, token=__TOKEN__, route=__ROUTE__;
  const name='__aftVisualSkipProbe', cleanupName='__aftVisualSkipCleanup';
  const owned=()=>location.pathname===route;
  const safeTag=e=>['A','BODY','BUTTON','DIV','MAIN','TEXTAREA','INPUT','SPAN'].includes(e?.tagName)?e.tagName:'OTHER';
  const safeId=e=>e?.id==='main-content'?'main-content':(e?.id?'other':'');
  const rect=e=>{if(!e)return null;const r=e.getBoundingClientRect();
    return {left:r.left,right:r.right,top:r.top,bottom:r.bottom,width:r.width,height:r.height};};
  if(action==='install'){
    if(!owned() || Object.prototype.hasOwnProperty.call(window,name) ||
       Object.prototype.hasOwnProperty.call(window,cleanupName))throw Error('foreign skip probe or route');
    const a=document.querySelector('a[href="#main-content"]'), m=document.querySelector('main#main-content');
    if(!a||!m||a.textContent.trim()!=='Skip to main content')throw Error('skip probe target absent');
    const controller=new AbortController(), events=[];
    let dropped=0;
    const handler=e=>{if(events.length>=80){dropped++;return;}
      events.push({type:e.type, trusted:e.isTrusted===true, tag:safeTag(e.target), id:safeId(e.target),
        key:e instanceof KeyboardEvent?(['Tab','Enter'].includes(e.key)?e.key:'other'):null,
        button:e instanceof MouseEvent?e.button:null,
        transform:e.type==='transitionend'?e.propertyName==='transform':null});};
    for(const type of ['keydown','keyup','mousedown','mouseup','click','focusin','focusout','transitionend'])
      document.addEventListener(type,handler,{capture:true,signal:controller.signal});
    const snapshot=()=>{if(!owned())throw Error('foreign skip route');
      const link=document.querySelector('a[href="#main-content"]'), main=document.querySelector('main#main-content');
      if(link!==a||main!==m)throw Error('skip probe target changed');
      const lr=rect(link), mr=rect(main), style=getComputedStyle(link);
      const hit=mr?document.elementFromPoint(mr.left+mr.width/2,mr.top+mr.height/2):null;
      return {routeOwned:true, active:{tag:safeTag(document.activeElement),id:safeId(document.activeElement),
        focusVisible:document.activeElement?.matches(':focus-visible')===true},
        link:{focused:document.activeElement===link,focusVisible:link.matches(':focus-visible'),
          inViewport:!!lr&&lr.bottom>0&&lr.top<innerHeight,rect:lr,
          transform:style.transform,transitionDuration:style.transitionDuration},
        main:{rect:mr,centerHit:{tag:safeTag(hit),id:safeId(hit),inside:!!hit&&main.contains(hit)}},
        hash:location.hash==='#main-content'?'#main-content':(location.hash===''?'':'other'),
        reducedMotion:matchMedia('(prefers-reduced-motion: reduce)').matches,
        events:events.slice(),dropped};};
    const probe={token,snapshot,link:a,controller};
    const cleanup=Object.freeze({token,probe});
    probe.cleanup=cleanup;
    Object.freeze(probe);
    try{
      Object.defineProperty(window,name,{value:probe,writable:false,configurable:true});
      Object.defineProperty(window,cleanupName,{value:cleanup,writable:false,configurable:true});
    }catch(e){controller.abort();delete window[name];throw e;}
    return {installed:true};
  }
  if(action==='cleanup'){
    const c=window[cleanupName], p=window[name];
    if(!p&&!c)return {removed:false,aborted:false,status:'absent'};
    const original=(c?.token===token&&c.probe?.cleanup===c&&c.probe.token===token)?c.probe:
      (p?.token===token&&p.cleanup?.probe===p&&p.cleanup.token===token)?p:null;
    if(!original)throw Error('foreign skip probe cleanup owner');
    original.controller.abort();
    const aborted=original.controller.signal.aborted===true;
    const consistent=p===original&&c===original.cleanup&&c.probe===original;
    if(p===original)delete window[name];
    if(c===original.cleanup)delete window[cleanupName];
    if(!aborted||!consistent||(p===original&&Object.prototype.hasOwnProperty.call(window,name))||
       (c===original.cleanup&&Object.prototype.hasOwnProperty.call(window,cleanupName)))
      throw Error('skip probe replaced or cleanup failed');
    return {removed:true,aborted:true};
  }
  if(!owned())throw Error('foreign skip route');
  const p=window[name], c=window[cleanupName];
  const descriptor=Object.getOwnPropertyDescriptor(window,name);
  if(!p||!c||p.token!==token||c.token!==token||c.probe!==p||p.cleanup!==c||descriptor?.value!==p||
     descriptor.writable!==false||p.controller.signal.aborted)
    throw Error('skip probe missing or replaced');
  if(action==='settled'){
    let guard;
    try{
      const deadline=new Promise((_,reject)=>{guard=setTimeout(()=>reject(Error('skip animation timed out')),1500);});
      await Promise.race([(async()=>{
        await new Promise(resolve=>requestAnimationFrame(resolve));
        await Promise.all(p.link.getAnimations().map(a=>a.finished));
      })(),deadline]);
    }finally{clearTimeout(guard);}
  }else if(!['before_click','after_click','before_focus','after_focus'].includes(action))
    throw Error('unknown skip probe action');
  return p.snapshot();
})()"""


def skip_probe(action, token):
    assert re.fullmatch(r"[0-9a-f]{24}", token), "invalid skip probe token"
    script = SKIP_PROBE_JS.replace("__ACTION__", json.dumps(action)) \
        .replace("__TOKEN__", json.dumps(token)) \
        .replace("__ROUTE__", json.dumps(f"/ws/{WS}/chat/{agent_id('input')}"))
    return evaluate(script, timeout=5)


def mouse_focus_skip_link():
    token = secrets.token_hex(12)
    receipt = {"run": RUN, "case": "input", "agent_id": agent_id("input"),
               "stages": {}, "cleanup": None, "failure_type": None}
    install_attempted = False
    original_failure = None
    try:
        current("input")
        install_attempted = True
        result = skip_probe("install", token)
        assert result == {"installed": True}, "skip-link event probe failed to install"
        for stage in ("before_click",):
            current("input")
            receipt["stages"][stage] = skip_probe(stage, token)
        browser("click", "main#main-content")
        for stage in ("after_click", "before_focus"):
            current("input")
            receipt["stages"][stage] = skip_probe(stage, token)
        assert evaluate("(() => { const a=document.querySelector('a[href=\"#main-content\"]'); a.focus(); return document.activeElement===a; })()")
        current("input")
        receipt["stages"]["after_focus"] = skip_probe("after_focus", token)
        current("input")
        receipt["stages"]["settled"] = skip_probe("settled", token)
    except Exception as exc:
        original_failure = exc
        receipt["failure_type"] = type(exc).__name__
        raise
    finally:
        if install_attempted:
            try:
                receipt["cleanup"] = skip_probe("cleanup", token)
                if receipt["cleanup"] != {"removed": True, "aborted": True} and original_failure is None:
                    raise AssertionError("skip-link event probe did not close")
            except Exception as exc:
                receipt["cleanup"] = {**receipt["cleanup"], "failure_type": type(exc).__name__} \
                    if isinstance(receipt["cleanup"], dict) else \
                    {"removed": False, "failure_type": type(exc).__name__}
                if original_failure is None:
                    raise
            finally:
                write("input-skip-mouse-diagnostic.json", receipt)
        else:
            write("input-skip-mouse-diagnostic.json", receipt)
    skip_link("mouse_focus")


def self_test_skip_link_diagnostic():
    from tempfile import TemporaryDirectory
    from unittest.mock import patch

    for focus_visible, in_viewport, accepted in ((False, False, True), (True, True, True),
                                                 (True, False, False), (False, True, False)):
        observed = {"text": "Skip to main content", "focused": True,
                    "focusVisible": focus_visible, "inViewport": in_viewport,
                    "hash": "#main-content", "focusInMain": False}
        with patch.dict(globals(), current=lambda _: None, evaluate=lambda _: observed,
                        write=lambda *_: None, shot=lambda *_: None):
            try:
                skip_link("mouse_focus")
            except AssertionError:
                assert not accepted, "valid focus-visible state was rejected"
            else:
                assert accepted, "mismatched focus-visible state was accepted"

    def exercise(*, focus_visible=False, in_viewport=False, drift=False, replaced=False, cleanup_failure=False,
                 install_timeout=False, install_absent=False, foreign_cleanup=False):
        calls, receipts = [], {}
        state = {"current": 0, "listeners_attached": False}

        def owned(_case):
            state["current"] += 1
            if drift and state["current"] == 3:
                raise AssertionError("Chat changed agents")

        def probe(action, _token):
            calls.append(action)
            if replaced and action == "after_click":
                raise RuntimeError("skip probe missing or replaced")
            if cleanup_failure and action == "cleanup":
                raise RuntimeError("skip probe cleanup failed")
            if foreign_cleanup and action == "cleanup":
                raise PermissionError("foreign skip probe cleanup owner")
            if action == "install":
                state["listeners_attached"] = not install_absent
                if install_timeout:
                    raise subprocess.TimeoutExpired(["agent-browser", "eval"], 5)
                return {"installed": True}
            if action == "cleanup":
                if install_absent:
                    return {"removed": False, "aborted": False, "status": "absent"}
                state["listeners_attached"] = False
                return {"removed": True, "aborted": True}
            return {"link": {"focused": True, "focusVisible": focus_visible,
                             "inViewport": in_viewport}}

        def strict(stage):
            calls.append("strict:" + stage)
            assert "input-skip-mouse-diagnostic.json" in receipts, "diagnostic was not saved before strict assertion"
            observed = receipts["input-skip-mouse-diagnostic.json"]["stages"]["settled"]["link"]
            assert observed["focused"] and observed["inViewport"] is observed["focusVisible"], observed

        with TemporaryDirectory(prefix="aft-visual-skip-offline-") as temp, \
             patch.dict(globals(), WORK=Path(temp), current=owned, skip_probe=probe,
                        browser=lambda *args: calls.append("browser:" + args[0]),
                        evaluate=lambda _: True, agent_id=lambda _: "agt_owned",
                        skip_link=strict,
                        write=lambda name, data: receipts.update({name: json.loads(json.dumps(data))})):
            try:
                mouse_focus_skip_link()
            except (AssertionError, RuntimeError, subprocess.TimeoutExpired) as exc:
                error = str(exc)
                error_type = type(exc).__name__
            else:
                error = None
                error_type = None
        return calls, receipts["input-skip-mouse-diagnostic.json"], error, error_type, state

    calls, receipt, error, _, _ = exercise()
    assert error is None and calls[-1] == "strict:mouse_focus" and receipt["cleanup"]["aborted"]
    assert list(receipt["stages"]) == ["before_click", "after_click", "before_focus", "after_focus", "settled"]
    calls, receipt, error, _, _ = exercise(focus_visible=True, in_viewport=True)
    assert error is None and calls[-1] == "strict:mouse_focus"
    assert receipt["stages"]["settled"]["link"]["focusVisible"] and receipt["cleanup"]["removed"]
    calls, receipt, error, _, _ = exercise(focus_visible=True, in_viewport=False)
    assert error and "inViewport" in error and calls[-1] == "strict:mouse_focus"
    assert receipt["stages"]["settled"]["link"]["focusVisible"] and receipt["cleanup"]["removed"]
    calls, receipt, error, _, _ = exercise(focus_visible=False, in_viewport=True)
    assert error and "inViewport" in error and calls[-1] == "strict:mouse_focus"
    calls, receipt, error, _, _ = exercise(drift=True)
    assert error == "Chat changed agents" and calls[-1] == "cleanup" and receipt["failure_type"] == "AssertionError"
    calls, receipt, error, _, _ = exercise(replaced=True)
    assert error == "skip probe missing or replaced" and calls[-1] == "cleanup"
    assert receipt["failure_type"] == "RuntimeError"
    calls, receipt, error, _, _ = exercise(cleanup_failure=True)
    assert error == "skip probe cleanup failed" and receipt["cleanup"]["removed"] is False
    assert "strict:mouse_focus" not in calls
    calls, receipt, error, error_type, state = exercise(install_timeout=True)
    assert error_type == "TimeoutExpired" and calls == ["install", "cleanup"], error
    assert receipt["failure_type"] == "TimeoutExpired" and receipt["cleanup"]["aborted"]
    assert not state["listeners_attached"], "timed-out installation retained listeners"
    calls, receipt, _, error_type, state = exercise(install_timeout=True, install_absent=True)
    assert error_type == "TimeoutExpired" and calls == ["install", "cleanup"]
    assert receipt["cleanup"]["status"] == "absent" and not state["listeners_attached"]
    calls, receipt, _, error_type, _ = exercise(install_timeout=True, foreign_cleanup=True)
    assert error_type == "TimeoutExpired" and receipt["cleanup"]["failure_type"] == "PermissionError"


def self_test_skip_probe_javascript():
    script = r"""
const vm=require('vm'), fs=require('fs'), assert=require('assert');
const template=fs.readFileSync(0,'utf8'), route='/ws/OFFLINE/chat/agt_owned';
function page(timeoutNow=false){
  const listeners=new Map(), window={}, location={pathname:route,hash:''};
  let active, pending=false, guardCleared=false;
  const link={tagName:'A',id:'',textContent:'Skip to main content',
    matches:s=>s===':focus-visible'&&active===link,
    getBoundingClientRect:()=>({left:16,right:180,top:8,bottom:42,width:164,height:34}),
    getAnimations:()=>pending?[{finished:new Promise(()=>{})}]:[]};
  const main={tagName:'MAIN',id:'main-content',matches:()=>false,contains:e=>e===main,
    getBoundingClientRect:()=>({left:0,right:800,top:50,bottom:650,width:800,height:600})};
  const document={activeElement:main,querySelector:s=>s.startsWith('a[')?link:main,
    elementFromPoint:()=>main,addEventListener(type,handler,opts){
      if(!listeners.has(type))listeners.set(type,new Set());
      listeners.get(type).add(handler);
      opts.signal.addEventListener('abort',()=>listeners.get(type).delete(handler));}};
  active=main;
  class KeyboardEvent{constructor(key){this.key=key;this.type='keydown';this.target=main;this.isTrusted=true;}}
  class MouseEvent{}
  const context={window,document,location,innerHeight:800,KeyboardEvent,MouseEvent,AbortController,
    getComputedStyle:()=>({transform:'matrix(1, 0, 0, 1, 0, 8)',transitionDuration:'0.15s'}),
    matchMedia:()=>({matches:false}),requestAnimationFrame:cb=>cb(),
    setTimeout:fn=>{if(timeoutNow)queueMicrotask(fn);return 1;},clearTimeout:()=>{guardCleared=true;}};
  const run=(action,token='0123456789abcdef01234567')=>vm.runInNewContext(template.replace('__ACTION__',JSON.stringify(action))
    .replace('__TOKEN__',JSON.stringify(token)).replace('__ROUTE__',JSON.stringify(route)),context);
  return {run,window,location,listeners,KeyboardEvent,setPending:v=>{pending=v;},guardCleared:()=>guardCleared};
}
(async()=>{
  const p=page();assert.deepStrictEqual(JSON.parse(JSON.stringify(await p.run('install'))),{installed:true});
  for(let i=0;i<85;i++)for(const listener of p.listeners.get('keydown'))listener(new p.KeyboardEvent('user-secret'));
  const snapshot=await p.run('before_click');
  assert.equal(snapshot.events.length,80);assert.equal(snapshot.dropped,5);
  assert(snapshot.events.every(e=>e.key==='other'&&!JSON.stringify(e).includes('user-secret')));
  assert.equal((await p.run('settled')).link.inViewport,true);assert(p.guardCleared());
  assert.equal((await p.run('cleanup')).aborted,true);
  assert([...p.listeners.values()].every(s=>s.size===0));
  await assert.rejects(p.run('before_click'),/missing or replaced/);
  const foreign=page();await foreign.run('install');foreign.location.pathname='/ws/OFFLINE/chat/agt_foreign';
  await assert.rejects(foreign.run('before_click'),/foreign skip route/);
  await foreign.run('cleanup');assert([...foreign.listeners.values()].every(s=>s.size===0));
  const changed=page();await changed.run('install');
  delete changed.window.__aftVisualSkipProbe;
  changed.window.__aftVisualSkipProbe={token:'foreign'};
  await assert.rejects(changed.run('before_click'),/missing or replaced/);
  await assert.rejects(changed.run('cleanup'),/replaced or cleanup failed/);
  assert([...changed.listeners.values()].every(s=>s.size===0));
  const sameToken=page();await sameToken.run('install');
  delete sameToken.window.__aftVisualSkipProbe;
  sameToken.window.__aftVisualSkipProbe={token:'0123456789abcdef01234567'};
  await assert.rejects(sameToken.run('before_click'),/missing or replaced/);
  await assert.rejects(sameToken.run('cleanup'),/replaced or cleanup failed/);
  assert([...sameToken.listeners.values()].every(s=>s.size===0));
  const replacedCleanup=page();await replacedCleanup.run('install');
  const original=replacedCleanup.window.__aftVisualSkipProbe;
  delete replacedCleanup.window.__aftVisualSkipCleanup;
  Object.defineProperty(replacedCleanup.window,'__aftVisualSkipCleanup',
    {value:{token:'0123456789abcdef01234567',probe:original,close:()=>true},
      writable:false,configurable:true});
  await assert.rejects(replacedCleanup.run('before_click'),/missing or replaced/);
  await assert.rejects(replacedCleanup.run('cleanup'),/replaced or cleanup failed/);
  assert.equal(original.controller.signal.aborted,true);
  assert([...replacedCleanup.listeners.values()].every(s=>s.size===0));
  const absent=page();assert.equal((await absent.run('cleanup')).status,'absent');
  const foreignOwner=page();foreignOwner.window.__aftVisualSkipProbe={token:'foreign'};
  await assert.rejects(foreignOwner.run('cleanup'),/foreign skip probe cleanup owner/);
  assert.equal(foreignOwner.window.__aftVisualSkipProbe.token,'foreign');
  const timeout=page(true);await timeout.run('install');timeout.setPending(true);
  await assert.rejects(timeout.run('settled'),/skip animation timed out/);
  await timeout.run('cleanup');assert([...timeout.listeners.values()].every(s=>s.size===0));
})().catch(e=>{console.error(e);process.exitCode=1;});
"""
    subprocess.run(["node", "-e", script], input=SKIP_PROBE_JS, text=True, check=True, timeout=10)


def workspace_data(path):
    response = request(path)
    assert response["success"] and isinstance(response["data"], dict), response
    return response["data"]


def create_mobile_roster():
    current("input")
    active_path = f"/api/workspaces/{urllib.parse.quote(WS, safe='')}"
    before = workspace_data(active_path)
    baseline = before["workspaces"]
    assert any(w["id"] == WS for w in baseline), "current real workspace absent from roster"
    needed = max(0, 4 - len(baseline))
    names = MOBILE_WS_NAMES[:needed]
    assert all(re.fullmatch(r"[A-Za-z0-9_-]{1,32}", name) for name in names), names
    assert not any(w["name"] in names for w in baseline), "run-owned workspace name already exists"
    plan = {"baseline": [{"id": w["id"], "name": w["name"]} for w in baseline], "planned_names": names}
    write("mobile-workspace-plan.json", plan)
    created = []
    for name in names:
        try:
            response = request("/api/workspaces", "POST", {"name": name, "type": "empty"}, expected_status=201)
        except Exception as exc:
            write("mobile-workspace-blocked.json", {"status": "blocked", "prerequisite":
                  "this isolated serve must enable POST /api/workspaces type=empty", "name": name, "error": str(exc)})
            raise AssertionError("BLOCKED: product workspace-create API did not provision an empty roster workspace") from exc
        assert response["success"] and isinstance(response["data"], dict), response
        row = next((w for w in response["data"]["workspaces"] if w["name"] == name), None)
        assert row and row["id"] and row["id"] != WS, response
        created.append({"id": row["id"], "name": name})
        write("mobile-workspace-created.json", created)
        actual = workspace_data(f"/api/workspaces/{urllib.parse.quote(row['id'], safe='')}")
        assert actual["id"] == row["id"] and actual["name"] == name and actual["repos"] == [], actual
        active = workspace_data(active_path)
        assert any(w["id"] == row["id"] and w["name"] == name for w in active["workspaces"]), active
    write("mobile-workspace-roster.json", {"baseline": plan["baseline"], "created": created})


def mobile_roster_check():
    current("input")
    plan = json.loads((WORK / "mobile-workspace-plan.json").read_text())
    live = workspace_data(f"/api/workspaces/{urllib.parse.quote(WS, safe='')}")
    expected = {w["name"] for w in plan["baseline"]} | set(plan["planned_names"])
    actual = {w["name"] for w in live["workspaces"]}
    created_path = WORK / "mobile-workspace-created.json"
    created = json.loads(created_path.read_text()) if created_path.exists() else []
    assert {w["name"] for w in created} == set(plan["planned_names"]), (created, plan)
    assert all(any(w["id"] == item["id"] and w["name"] == item["name"] for w in live["workspaces"])
               for item in created), (created, live["workspaces"])
    shown = evaluate("""[...document.querySelectorAll('nav[aria-label="Primary"] [aria-label="Workspace selector"] button')]
      .map(b=>b.getAttribute('aria-label')).filter(x=>x?.startsWith('Switch to ')).map(x=>x.slice(10))""")
    assert expected == actual == set(shown) and len(shown) == len(set(shown)) >= 4, (expected, actual, shown)
    write("mobile-workspace-dom.json", {"api": live["workspaces"], "nav_names": shown})


MOBILE_LAYOUT_JS = r"""(() => {
  const vw=innerWidth, vh=innerHeight, nav=document.querySelector('nav[aria-label="Primary"]');
  const s=nav?.querySelector('[aria-label="Workspace selector"]');
  const title=document.querySelector('section[aria-label="Agent chat"] header h2');
  const field=document.querySelector('textarea[aria-label="Message"]');
  const form=field?.closest('form');
  const box=e=>{const r=e.getBoundingClientRect();return {left:r.left,right:r.right,top:r.top,bottom:r.bottom,width:r.width,height:r.height};};
  const n=nav&&box(nav), c=form&&box(form), sw=s&&box(s);
  const overflow=[];
  for(const e of document.body.querySelectorAll('*')){
    const r=e.getBoundingClientRect(); if(!r.width||!r.height||getComputedStyle(e).visibility==='hidden')continue;
    let right=r.right;
    for(let p=e.parentElement;p;p=p.parentElement){const pr=p.getBoundingClientRect().right;
      if(getComputedStyle(p).overflowX!=='visible'&&pr<vw-.5)right=Math.min(right,pr);}
    if(right>vw+.5&&r.left<right)overflow.push(e.tagName.toLowerCase()+'.'+String(e.className));
  }
  const centers=[...(nav?.querySelectorAll('button')||[])].map(e=>({name:e.getAttribute('aria-label'),y:(box(e).top+box(e).bottom)/2}));
  const active=s?.querySelector('button[data-active]'); const a=active&&box(active);
  const covered=[...(form?.querySelectorAll('button')||[])].filter(b=>{const r=box(b);
    const top=document.elementFromPoint((r.left+r.right)/2,(r.top+r.bottom)/2);
    return !top||!b.contains(top);}).map(b=>b.getAttribute('aria-label'));
  const header=document.querySelector('section[aria-label="Agent chat"] header');
  return {vw,vh,scrollWidth:document.documentElement.scrollWidth,overflow,nav:n,form:c,switcher:sw,
    centers,active:a,activeName:active?.getAttribute('aria-label'),title:title?.textContent,
    titleClipped:!!title&&title.scrollWidth>title.clientWidth,covered,
    header:header&&box(header),headerCount:document.querySelectorAll('section[aria-label="Agent chat"] header').length,
    field:field&&box(field),theme:document.documentElement.dataset.theme};
})()"""


def mobile_layout(width):
    current("input")
    width = int(width)
    assert width in (360, 390, 470, 557)
    height = 800 if width == 360 else 844
    browser("set", "viewport", str(width), str(height))
    results = []
    for theme in ("light", "dark"):
        if evaluate("document.documentElement.dataset.theme") != theme:
            browser("click", 'button[aria-label="Switch to ' + theme + ' mode"]')
        state = evaluate(MOBILE_LAYOUT_JS)
        assert state["vw"] == width and state["vh"] == height and state["theme"] == theme, state
        assert not state["overflow"] and state["scrollWidth"] <= width, state
        nav, form = state["nav"], state["form"]
        assert nav and form and nav["height"] <= 64 and abs(nav["bottom"] - height) <= 1, state
        assert all(abs(c["y"] - (nav["top"] + nav["bottom"]) / 2) < 4 for c in state["centers"]), state
        assert state["switcher"] and state["active"] and state["activeName"], state
        assert state["active"]["left"] >= state["switcher"]["left"] - 4, state
        assert state["active"]["right"] <= state["switcher"]["right"] + 4, state
        assert state["title"] == NAMES["input"] and not state["titleClipped"], state
        assert form["bottom"] <= nav["top"] and not state["covered"], state
        assert state["headerCount"] == 1 and state["header"]["right"] <= width + 1, state
        assert state["header"]["bottom"] <= state["field"]["top"], state
        results.append(state)
        shot("input", f"mobile-{width}-{theme}")
    write(f"input-mobile-{width}.json", results)


SWITCHER_JS = r"""(() => {
  const nav=document.querySelector('nav[aria-label="Primary"]');
  const s=nav?.querySelector('[aria-label="Workspace selector"]');
  if(!nav||!s)throw Error('real workspace switcher absent');
  const w=s.getBoundingClientRect(), items=[...s.querySelectorAll('button')];
  const cut=[],hidden=[];
  for(const b of items){const r=b.getBoundingClientRect();
    const shown=Math.min(r.right,w.right)-Math.max(r.left,w.left);
    if(shown<=.5)hidden.push(r.right<=w.left+.5?'left':'right');
    else if(shown<r.width-.5)cut.push({name:b.getAttribute('aria-label'),shown,width:r.width});}
  const hints=[...nav.querySelectorAll('[data-more-hint]')].filter(h=>{const r=h.getBoundingClientRect(),c=getComputedStyle(h);
    return c.visibility!=='hidden'&&Number(c.opacity)>.5&&r.width>0&&r.height>0;});
  const marks=[...items.map(b=>{const r=b.getBoundingClientRect();return {name:b.getAttribute('aria-label'),
    left:Math.max(r.left,w.left),right:Math.min(r.right,w.right),top:r.top,bottom:r.bottom};}),
    ...[...nav.querySelectorAll('button svg')].filter(i=>!s.contains(i)).map(i=>{const r=i.getBoundingClientRect();
      return {name:i.closest('button')?.getAttribute('aria-label')+' icon',left:r.left,right:r.right,top:r.top,bottom:r.bottom};})];
  const covered=hints.flatMap(h=>{const r=h.getBoundingClientRect();return marks.filter(m=>m.right-m.left>.5&&
    r.left<m.right-.5&&r.right>m.left+.5&&r.top<m.bottom&&r.bottom>m.top).map(m=>h.dataset.moreHint+' over '+m.name);});
  const overlap=[...nav.querySelectorAll('button')].filter(b=>!s.contains(b)).filter(b=>{
    const r=b.getBoundingClientRect();return r.right>w.left+.5&&r.left<w.right-.5;}).map(b=>b.getAttribute('aria-label'));
  const hitBlocked=items.filter(b=>{const r=b.getBoundingClientRect();
    if(r.left<w.left-.5||r.right>w.right+.5)return false;
    const top=document.elementFromPoint((r.left+r.right)/2,(r.top+r.bottom)/2);
    return !top||!b.contains(top);}).map(b=>b.getAttribute('aria-label'));
  return {scrollLeft:s.scrollLeft,max:s.scrollWidth-s.clientWidth,items:items.map(b=>b.getAttribute('aria-label')),
    cut,hidden,hints:hints.map(h=>h.dataset.moreHint).sort(),covered,overlap,hitBlocked};
})()"""


def mobile_switcher(width):
    current("input")
    width = int(width)
    assert width in (360, 390, 470, 557)
    height = 800 if width == 360 else 844
    browser("set", "viewport", str(width), str(height))
    first = evaluate(SWITCHER_JS)
    workspace_buttons = [name for name in first["items"] if name and name.startswith("Switch to ")]
    if width < 557 and (len(workspace_buttons) < 4 or first["max"] <= 4):
        write(f"input-switcher-{width}-blocked.json", {"status": "blocked", "prerequisite":
              "at least four authentic workspace avatars must overflow the real switcher at this viewport", "observed": first})
        raise AssertionError(f"BLOCKED: real workspace roster does not exercise MB1b multi-item overflow at {width}px")
    rows = []
    for target in sorted(set((0, min(13, first["max"]), first["max"] // 2, first["max"]))):
        evaluate("(() => {window.__aftVisualScroll=NaN;document.querySelector('nav[aria-label=\"Primary\"] [aria-label=\"Workspace selector\"]').scrollTo({left:" + str(target) + ",behavior:'instant'});return true;})()")
        browser("wait", "--fn", "(() => {const s=document.querySelector('nav[aria-label=\"Primary\"] [aria-label=\"Workspace selector\"]');const v=s.scrollLeft;const p=window.__aftVisualScroll;window.__aftVisualScroll=v;return p===v;})()")
        row = evaluate(SWITCHER_JS)
        want = sorted(set(row["hidden"]))
        assert not row["cut"] and row["hints"] == want and not row["covered"] and not row["overlap"] and not row["hitBlocked"], row
        rows.append({"requested": target, **row})
        shot("input", f"switcher-{width}-{target}")
    for name in first["items"]:
        assert name, "workspace switcher item lacks an accessible name"
        script = "(() => {const s=document.querySelector('nav[aria-label=\"Primary\"] [aria-label=\"Workspace selector\"]');const b=[...s.querySelectorAll('button')].find(x=>x.getAttribute('aria-label')===" + json.dumps(name) + ");if(!b)return false;window.__aftVisualScroll=NaN;b.scrollIntoView({block:'nearest',inline:'nearest'});return true;})()"
        assert evaluate(script), name
        browser("wait", "--fn", "(() => {const s=document.querySelector('nav[aria-label=\"Primary\"] [aria-label=\"Workspace selector\"]');const v=s.scrollLeft;const p=window.__aftVisualScroll;window.__aftVisualScroll=v;return p===v;})()")
        visible = evaluate("(() => {const s=document.querySelector('nav[aria-label=\"Primary\"] [aria-label=\"Workspace selector\"]');const b=[...s.querySelectorAll('button')].find(x=>x.getAttribute('aria-label')===" + json.dumps(name) + ");const r=b.getBoundingClientRect(),w=s.getBoundingClientRect(),top=document.elementFromPoint((r.left+r.right)/2,(r.top+r.bottom)/2);return {left:r.left,right:r.right,windowLeft:w.left,windowRight:w.right,hittable:!!top&&b.contains(top)};})()")
        assert visible["left"] >= visible["windowLeft"] - .5 and visible["right"] <= visible["windowRight"] + .5 and visible["hittable"], (name, visible)
        rows.append({"reachable": name, **visible})
    write(f"input-switcher-{width}.json", rows)


def motion_prime():
    path = f"/ws/{WS}/agents"
    source = (Path(required("AFT_TESTS_DIR")) / "scripts/coverage-chat-visual-arrivals.js").read_text()
    script = source.replace("__WS_JSON__", json.dumps(WS)).replace("__RUN_JSON__", json.dumps(RUN)) \
                   .replace("__PATH_JSON__", json.dumps(path)) \
                   .replace("__CHAT_PREFIX_JSON__", json.dumps(f"/ws/{WS}/chat/"))
    result = evaluate(script)
    assert result == {"version": 1, "workspace": WS, "run": RUN, "path": path}, \
        "arrival observer was not installed on the owned Agents page"


MOTION_JS = r"""(() => {
  if (window.__aftChatVisual) throw Error('visual probe already armed');
  window.__aftChatVisualLive=null;
  const observer=window.__aftChatVisualArrival;
  if(!observer)throw Error('owned arrival observer missing');
  const startClock=observer.markFrame();
  const p = {start:startClock.at,startClock, frames:[], ticks:[], tickMeta:[], signals:[], samples:[], shifts:[], maxMs:180000,
    marker:__MARKER__,
    stopped:false, lastText:'', lastVisibleText:'', lastContentText:'', sawCaret:false, sawWorking:false, sawStop:false};
  const root=document.querySelector('section[aria-label="Agent chat"]');
  if(!root)throw Error('real Agent Chat root missing before send');
  const transcript = () => document.querySelector('[data-testid=chat-transcript]');
  const replyContent = md => { if(!md)return '';
    const blocks=new Set(['DIV','P','PRE','UL','OL','LI','BLOCKQUOTE','SECTION','H1','H2','H3','H4','H5','H6']);
    const shown=node=>{if(node.nodeType===3)return node.nodeValue||'';
      if(node.nodeType!==1)return '';const el=node;
      if(el.hasAttribute('data-chat-renderer-chrome')||['SCRIPT','STYLE','SVG','INPUT'].includes(el.tagName))return '';
      if(el.tagName==='BR')return '\n';
      if(el.tagName==='TABLE')return [...el.querySelectorAll('tr')].map(shown).join('\n');
      if(el.tagName==='TR')return [...el.children].filter(c=>c.tagName==='TH'||c.tagName==='TD').map(shown).join('\t');
      const children=[...el.childNodes].map(shown);
      const separated=el.getAttribute('role')==='toolbar';
      const body=children.join(separated?'\n':'');
      return blocks.has(el.tagName)?`\n${body}\n`:body;};
    return shown(md).trim().replace(/\n{3,}/g,'\n\n');};
  p.replyContent=replyContent;
  const state = (clock={at:Date.now()}) => { const t=transcript(); const a=[...(t?.querySelectorAll('li[data-kind=agent]')||[])].at(-1);
    const md=a?.querySelector('[data-testid=chat-markdown]');
    const text=md?.textContent||'', visibleText=md?.innerText||'', contentText=replyContent(md);
    const streaming=md?.getAttribute('data-streaming')==='true';
    const caret=!!a?.querySelector('[data-streaming-caret]');
    const working=!!t?.querySelector('[data-testid=working-row]');
    const gap=t?Math.max(0,t.scrollHeight-t.scrollTop-t.clientHeight):null;
    return {...clock,text,visibleText,contentText,streaming,caret,working,gap,stop:!!document.querySelector('form button[title="Stop the running turn"]')}; };
  p.captureState=()=>state(observer.markFrame());
  const captureLive=() => {if(p.stopped||window.__aftChatVisualLive)return;
    const tool=root.querySelector('[data-testid=tool-live], [data-testid=tool-call][data-status=running]');
    const working=root.querySelector('[data-testid=working-row]');
    const stop=root.querySelector('form button[title="Stop the running turn"]');
    const label=tool?.textContent?.trim()||'';
    if(!tool||!working||!stop||!label)return;
    window.__aftChatVisualLive={path:location.pathname,at:new Date().toISOString(),
      source:tool.dataset.testid,label,live:!!tool,working:!!working,running:!!stop,
      workingText:working.textContent,stopTitle:stop.title};
    p.liveObserver?.disconnect(); };
  p.liveObserver=new MutationObserver(captureLive);
  p.liveObserver.observe(root,{subtree:true,childList:true,attributes:true,characterData:true});
  const frame=(rafAt) => { if(p.stopped)return; captureLive(); const s=state(observer.markFrame());
    const tickIndex=p.ticks.length; p.ticks.push(s.at);
    p.tickMeta.push({at:s.at,mono:s.mono,phase:s.phase,rafAt,tickIndex});
    p.signals.push({at:s.at,mono:s.mono,phase:s.phase,tickIndex,
      caret:s.caret,working:s.working,stop:s.stop});
    if(s.text!==p.lastText||s.visibleText!==p.lastVisibleText||s.contentText!==p.lastContentText) {const before=p.lastContentText.trim().split(/\s+/).filter(Boolean).length;
      const after=s.contentText.trim().split(/\s+/).filter(Boolean).length;
      const marker=s.text.includes(p.marker);
      p.frames.push({at:s.at,mono:s.mono,phase:s.phase,tickIndex,
        chars:s.text.length,visibleChars:s.visibleText.length,contentChars:s.contentText.length,words:after,addedWords:Math.max(0,after-before),
        marker,text:s.text,visibleText:s.visibleText,contentText:s.contentText,streaming:s.streaming}); p.lastText=s.text;p.lastVisibleText=s.visibleText;p.lastContentText=s.contentText;}
    p.sawCaret ||= s.caret; p.sawWorking ||= s.working; p.sawStop ||= s.stop;
    if(Date.now()-p.start>p.maxMs) {p.stopped=true;p.liveObserver.disconnect();return;} requestAnimationFrame(frame); };
  p.timer=setInterval(()=>{ if(p.stopped){clearInterval(p.timer);return;}
    const s=state(); p.samples.push({at:s.at,chars:s.text.length,caret:s.caret,working:s.working,gap:s.gap,stop:s.stop});
  },100);
  const rect=r=>r?{x:r.x,y:r.y,width:r.width,height:r.height}:null;
  p.recordShift=e=>{ if(!e.hadRecentInput) p.shifts.push({at:Date.now(),value:e.value,
      sources:(e.sources||[]).slice(0,4).map(s=>({tag:s.node?.tagName||null,
        testid:s.node?.closest?.('[data-testid]')?.getAttribute('data-testid')||null,
        previous:rect(s.previousRect),current:rect(s.currentRect)}))}); };
  try { p.observer=new PerformanceObserver(list=>list.getEntries().forEach(p.recordShift));
    p.observer.observe({type:'layout-shift', buffered:false}); }
  catch(e) {p.observerError=String(e);}
  window.__aftChatVisual=p; requestAnimationFrame(frame); return 'armed';
})()"""


def motion_start():
    current("render")
    aid = agent_id("render")
    browser("wait", "--fn", "(() => {const p=window.__aftChatVisualArrival;return !!p&&p.ready(" +
            json.dumps(aid) + ");})()")
    bound = evaluate("(() => window.__aftChatVisualArrival.bind(" + json.dumps(aid) + "," +
                     json.dumps(f"/ws/{WS}/chat/{aid}") + "))()")
    assert bound == {"version": 1, "workspace": WS, "run": RUN, "agentId": aid,
                     "route": f"/ws/{WS}/chat/{aid}", "sourceCount": bound.get("sourceCount")}, \
        "arrival observer did not bind the owned Chat stream"
    assert isinstance(bound["sourceCount"], int) and bound["sourceCount"] >= 1
    assert evaluate(MOTION_JS.replace("__MARKER__", json.dumps(f"VISUAL_END_{RUN}"))) == "armed"
    browser("wait", "--fn", "(() => {const p=window.__aftChatVisual;return !!p&&!p.stopped&&" +
            "location.pathname===" + json.dumps(f"/ws/{WS}/chat/{aid}") +
            "&&p.ticks.length>=6&&p.frames.length===0;})()")
    ready = evaluate("(() => {const p=window.__aftChatVisual;return {route:location.pathname," +
                     "start:p?.start,startClock:p?.startClock,ticks:p?.ticks.slice(0,6)," +
                     "tickMeta:p?.tickMeta.slice(0,6),frames:p?.frames.length," +
                     "signals:p?.signals.slice(0,6),stopped:p?.stopped};})()")
    assert_motion_ready(ready, aid)
    write("render-motion-ready.json", {"route": ready["route"], "agent_id": aid,
                                      "startClock": ready["startClock"], "ticks": ready["ticks"],
                                      "tickMeta": ready["tickMeta"]})


def assert_motion_ready(ready, aid):
    ticks = ready.get("ticks")
    meta = ready.get("tickMeta")
    start_clock = ready.get("startClock")
    assert ready.get("route") == f"/ws/{WS}/chat/{aid}" and \
        type(ready.get("start")) is int and \
        isinstance(start_clock, dict) and start_clock.get("at") == ready["start"] and \
        isinstance(start_clock.get("mono"), (int, float)) and \
        type(start_clock.get("phase")) is int and \
        isinstance(ticks, list) and len(ticks) == 6 and \
        all(type(tick) is int for tick in ticks) and \
        ticks[0] >= ready["start"] and \
        all(right >= left for left, right in zip(ticks, ticks[1:])) and \
        isinstance(meta, list) and len(meta) == 6 and \
        all(type(row.get("tickIndex")) is int and row["tickIndex"] == i and
            row.get("at") == ticks[i] and isinstance(row.get("mono"), (int, float)) and
            type(row.get("phase")) is int and row["phase"] > start_clock["phase"] and
            isinstance(row.get("rafAt"), (int, float)) and row["rafAt"] <= row["mono"] + 1
            for i, row in enumerate(meta)) and \
        all(right["mono"] > left["mono"] and right["phase"] > left["phase"] and
            right["rafAt"] > left["rafAt"] for left, right in zip(meta, meta[1:])) and \
        ready.get("frames") == 0 and ready.get("stopped") is False and \
        isinstance(ready.get("signals"), list) and len(ready["signals"]) == 6 and \
        [signal.get("at") for signal in ready["signals"]] == ticks and \
        all(signal.get("mono") == meta[i]["mono"] and
            signal.get("phase") == meta[i]["phase"] and
            signal.get("tickIndex") == i for i, signal in enumerate(ready["signals"])) and \
        all(signal.get("caret") is False and signal.get("working") is False and
            signal.get("stop") is False for signal in ready["signals"]), \
        "motion probe lacks six owned pre-send sampled frame intervals"


def self_test_motion_ready():
    from copy import deepcopy
    aid = "agt_owned"
    ticks = [100 + step * 9 for step in range(6)]
    ready = {"route": f"/ws/{WS}/chat/{aid}", "start": 99,
             "startClock": {"at": 99, "mono": 99.0, "phase": 1},
             "ticks": ticks, "frames": 0, "stopped": False,
             "tickMeta": [{"at": tick, "mono": float(tick), "phase": i + 2,
                           "rafAt": float(tick - 1), "tickIndex": i}
                          for i, tick in enumerate(ticks)],
             "signals": [{"at": tick, "mono": float(tick), "phase": i + 2,
                          "tickIndex": i, "caret": False, "working": False, "stop": False}
                         for i, tick in enumerate(ticks)]}
    assert_motion_ready(ready, aid)
    same_wall = deepcopy(ready)
    same_wall["ticks"][2] = same_wall["ticks"][1]
    same_wall["tickMeta"][2]["at"] = same_wall["ticks"][1]
    same_wall["signals"][2]["at"] = same_wall["ticks"][1]
    assert_motion_ready(same_wall, aid)
    def rejects(change):
        candidate = deepcopy(ready)
        change(candidate)
        try:
            assert_motion_ready(candidate, aid)
        except AssertionError as exc:
            assert "six owned pre-send" in str(exc), exc
        else:
            raise AssertionError("foreign or incomplete pre-send cadence passed")
    rejects(lambda r: r.update(route="/ws/FOREIGN/chat/agt_owned"))
    rejects(lambda r: r.update(ticks=r["ticks"][:5]))
    rejects(lambda r: r["tickMeta"][2].update(mono=r["tickMeta"][1]["mono"]))
    rejects(lambda r: r["ticks"].__setitem__(2, r["ticks"][1] - 1))
    rejects(lambda r: r.update(frames=1))
    rejects(lambda r: r["signals"][0].update(working=True))
    rejects(lambda r: r["tickMeta"][0].update(tickIndex=1))
    rejects(lambda r: r["tickMeta"][0].update(mono=0.0))
    rejects(lambda r: r.update(stopped=True))


def final_text_frame(answer, frames, final_text, marker):
    assert answer.endswith(marker), "saved answer did not end exactly with the requested marker"
    assert frames and frames[-1].get("text") == final_text and final_text.endswith(marker), "last measured frame is not the final rendered DOM text"
    first_marker = next((index for index, frame in enumerate(frames) if frame["marker"]), None)
    assert first_marker is not None, "completed answer marker was never visible in a rendered frame"
    assert first_marker == len(frames) - 1, "marker appeared before later rendered text growth or change"
    return frames[first_marker]


def final_text_lag(frame, turn_ms):
    lag = frame["at"] - turn_ms
    assert lag <= 300, f"final text lagged saved turn completion by {lag:.0f}ms"
    return lag


def self_test_final_text():
    marker = "VISUAL_END_TEST"
    answer = "early " + marker + " late words " + marker
    early = {"at": 10, "text": "early " + marker, "marker": True}
    late = {"at": 800, "text": answer, "marker": True}
    try:
        final_text_frame(answer, [early, late], answer, marker)
    except AssertionError as exc:
        assert "before later rendered text" in str(exc), exc
    else:
        raise AssertionError("early marker with delayed growth was incorrectly accepted")
    assert final_text_frame(answer, [{"at": 800, "text": answer, "marker": True}], answer, marker) == late
    assert final_text_lag({"at": 700}, 800) == -100, "valid pre-completion final render was rejected"


def motion_digest(snapshot):
    raw = json.dumps(snapshot, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
    return hashlib.sha256(raw.encode()).hexdigest()


def save_motion_capture(snapshot):
    capture = WORK / "render-motion-capture.json"
    seal = WORK / "render-motion-seal.json"
    assert not capture.exists() and not seal.exists(), "first-turn motion capture already exists"
    write(capture.name, snapshot)
    write(seal.name, {"sha256": motion_digest(snapshot), "case": snapshot["case"],
                      "ws": snapshot["ws"], "run": snapshot["run"], "agent_id": snapshot["agent_id"]})


def load_motion_capture(case):
    capture = WORK / "render-motion-capture.json"
    seal = WORK / "render-motion-seal.json"
    assert all(p.is_file() and not p.is_symlink() for p in (capture, seal)), "first-turn motion capture or seal missing"
    try:
        snapshot = json.loads(capture.read_text())
        receipt = json.loads(seal.read_text())
    except (ValueError, OSError) as exc:
        raise AssertionError("first-turn motion capture is unreadable") from exc
    assert isinstance(snapshot, dict) and isinstance(receipt, dict), "first-turn motion capture has invalid shape"
    expected = {"sha256": motion_digest(snapshot), "case": case, "ws": WS,
                "run": RUN, "agent_id": agent_id(case)}
    assert receipt == expected, "first-turn motion capture changed or belongs to a foreign run"
    assert all(snapshot.get(key) == value for key, value in expected.items() if key != "sha256"), \
        "first-turn motion capture identity changed"
    assert snapshot.get("version") == 1 and snapshot.get("probe", {}).get("route") == snapshot.get("route"), \
        "first-turn motion capture version or probe route changed"
    assert snapshot.get("route") == f"/ws/{WS}/chat/{agent_id(case)}", "first-turn motion capture changed Chat route"
    return snapshot


def motion_capture(case):
    assert case == "render", case
    current(case)
    evs = events(case)
    delivered = [e for e in evs if e["kind"] == "message.delivered" and f"VISUAL_RENDER_{RUN}" in e["payload"].get("text", "")]
    assert len(delivered) == 1, delivered
    end = [e for e in evs if e["kind"] == "agent.turn_completed" and e["seq"] > delivered[0]["seq"]]
    assert len(end) == 1, end
    replies = [e for e in evs if e["kind"] == "item.completed" and e["payload"].get("itemKind") == "message" and delivered[0]["seq"] < e["seq"] < end[0]["seq"]]
    assert replies, "real turn saved no assistant message"
    reply_items = [e["payload"]["itemId"] for e in replies]
    assert all(isinstance(item, str) and item for item in reply_items) and \
        len(reply_items) == len(set(reply_items)), "saved reply item IDs are missing or duplicated"
    owned_turn = replies[-1]["turn_id"]
    assert all(e["turn_id"] == owned_turn for e in replies), "saved reply items cross native turns"
    browser("wait", "--fn", "(() => {const p=window.__aftChatVisual," +
            "r=window.__aftChatVisualArrival?.snapshot()," +
            "a=r?.arrivals?.filter(x=>x.agentId===" + json.dumps(agent_id(case)) +
            "&&x.turnId===" + json.dumps(owned_turn) +
            "&&" + json.dumps(reply_items) + ".includes(x.itemId));" +
            "return !!p&&!p.stopped&&r?.error===null&&a?.length>0&&" +
            "p.tickMeta.at(-1)?.mono>=Math.max(...a.map(x=>x.mono))+300;})()")
    probe = evaluate("""(() => { const p=window.__aftChatVisual; if(!p)throw Error('motion probe missing');
      const observer=window.__aftChatVisualArrival;
      const before=observer?.snapshot();
      const own=before?.arrivals?.filter(a=>a.agentId===__AID__&&a.turnId===__TURN__&&__ITEMS__.includes(a.itemId));
      if(!own?.length||before.error!==null||p.tickMeta.at(-1)?.mono<Math.max(...own.map(a=>a.mono))+300)
        throw Error('owned arrival horizon changed before freeze');
      const finalSignal=p.captureState();
      p.signals.push({at:finalSignal.at,mono:finalSignal.mono,phase:finalSignal.phase,tickIndex:null,
        caret:finalSignal.caret,
        working:finalSignal.working,stop:finalSignal.stop});
      p.stopped=true; clearInterval(p.timer); p.observer?.takeRecords().forEach(p.recordShift);
      p.observer?.disconnect(); p.liveObserver?.disconnect();
      const arrival=observer.snapshot();
      const arrivalClosed=observer.close();
      if(arrival.error!==null||arrival.arrivals.length!==before.arrivals.length||
         arrival.completions.length!==before.completions.length||
         arrivalClosed.error!==null||arrivalClosed.arrivalCount!==arrival.arrivals.length||
         arrivalClosed.completionCount!==arrival.completions.length||
         arrivalClosed.phase!==arrival.phase)
        throw Error('owned arrival changed across frozen observer close');
      const c=document.querySelector('[data-testid=chat-transcript]');
      const a=[...(c?.querySelectorAll('li[data-kind=agent]')||[])].at(-1);
      const finalText=a?.querySelector('[data-testid=chat-markdown]')?.textContent||'';
      const finalVisibleText=a?.querySelector('[data-testid=chat-markdown]')?.innerText||'';
      const finalContentText=p.replyContent(a?.querySelector('[data-testid=chat-markdown]'));
      return {route:location.pathname,start:p.start,startClock:p.startClock,
        frames:p.frames,ticks:p.ticks,tickMeta:p.tickMeta,signals:p.signals,
        finalText,finalVisibleText,finalContentText,samples:p.samples,shifts:p.shifts,
        arrival,arrivalClosed,
        caretGone:!!c && !c.querySelector('[data-streaming-caret], [data-testid=working-row]'),
        sawCaret:p.sawCaret,sawWorking:p.sawWorking,sawStop:p.sawStop,
        observerError:p.observerError||null}; })()"""
                     .replace("__AID__", json.dumps(agent_id(case)))
                     .replace("__TURN__", json.dumps(owned_turn))
                     .replace("__ITEMS__", json.dumps(reply_items)))
    assert probe["route"] == f"/ws/{WS}/chat/{agent_id(case)}", "motion capture changed Chat route"
    answer = "\n".join(e["payload"].get("text", "") for e in replies)
    snapshot = {"version": 1, "case": case, "ws": WS, "run": RUN,
                "agent_id": agent_id(case), "route": probe["route"], "probe": probe,
                "delivered": delivered[0], "turn_completed": end[0],
                "replies": replies, "answer": answer}
    save_motion_capture(snapshot)
    marker = f"VISUAL_END_{RUN}"
    frames = probe["frames"]
    end_at = end[0].get("created_at")
    from datetime import datetime
    try:
        turn_ms = datetime.fromisoformat(end_at.replace("Z", "+00:00")).timestamp() * 1000 if end_at else None
    except ValueError:
        turn_ms = None
    candidate_frame = next((frame for frame in frames if frame.get("marker")), None)
    word_count = len(answer.split())
    write("render-motion-diagnostic.json", {
        "validation": "pending", "capture_sha256": motion_digest(snapshot),
        "probe": probe, "delivered_event_id": delivered[0]["event_id"],
        "turn_completed": end[0], "saved_answer_chars": len(answer),
        "saved_answer_ends_with_marker": answer.endswith(marker),
        "word_count": word_count, "required_word_count": 300,
        "candidate_marker_frame": candidate_frame, "turn_ms": turn_ms,
        "candidate_lag_ms": candidate_frame["at"] - turn_ms if candidate_frame and turn_ms is not None else None,
        "max_added_words_per_frame": max((frame["addedWords"] for frame in frames), default=None),
        "non_input_layout_shift": sum(shift["value"] for shift in probe["shifts"]),
    })


def assert_arrival_receipts(snapshot):
    probe = snapshot["probe"]
    receipt = probe.get("arrival")
    aid = snapshot["agent_id"]
    route = snapshot["route"]
    assert isinstance(receipt, dict) and receipt.get("version") == 1 and \
        receipt.get("workspace") == WS and receipt.get("run") == RUN and \
        receipt.get("bound", {}).get("agentId") == aid and \
        receipt["bound"].get("route") == route and \
        receipt["bound"].get("at", 0) <= probe["start"] and \
        probe.get("arrivalClosed", {}).get("closed") is True, \
        "missing or foreign pre-navigation EventSource telemetry"
    assert receipt.get("error") is None, "owned EventSource telemetry reported an error"
    all_sources = receipt.get("sources")
    assert isinstance(all_sources, list) and all_sources and len(all_sources) <= 20 and \
        len({s.get("id") for s in all_sources}) == len(all_sources), "missing or ambiguous owned EventSource"
    sources = [s for s in all_sources if s.get("agents") == [aid]]
    assert sources, "owned Chat EventSource was not constructed"
    source_ids = set()
    for source in sources:
        assert isinstance(source.get("at"), int) and \
            source["at"] <= probe["frames"][-1]["at"], "foreign or late EventSource source"
        source_ids.add(source["id"])
    assert any(source["at"] <= receipt["bound"]["at"] for source in sources), \
        "arrival observer did not see the pre-send EventSource"
    arrivals = receipt.get("arrivals")
    completions = receipt.get("completions")
    assert isinstance(arrivals, list) and arrivals and len(arrivals) <= 5000 and \
        isinstance(completions, list) and len(completions) <= 100, \
        "missing or unbounded real stream receipts"
    reply_ids = {e["payload"].get("itemId") for e in snapshot["replies"]}
    assert None not in reply_ids and len(reply_ids) == len(snapshot["replies"]), \
        "saved assistant items lack unique IDs for arrival matching"
    turn_id = snapshot["turn_completed"]["turn_id"]
    previous = receipt["bound"]["at"]
    for arrival in arrivals:
        assert arrival.get("sourceId") in source_ids and arrival.get("agentId") == aid and \
            arrival.get("turnId") == turn_id and arrival.get("itemId") in reply_ids and \
            isinstance(arrival.get("text"), str) and isinstance(arrival.get("at"), int) and \
            previous <= arrival["at"], "foreign, missing, or reordered message arrival"
        previous = arrival["at"]
    for reply in snapshot["replies"]:
        raw = "".join(a["text"] for a in arrivals if a["itemId"] == reply["payload"]["itemId"])
        assert raw and reply["payload"]["text"].startswith(raw), \
            "actual delta bytes do not match the saved assistant item"
    saved = {e["event_id"]: e for e in [*snapshot["replies"], snapshot["turn_completed"]]}
    observed_ids = set()
    previous = receipt["bound"]["at"]
    for completion in completions:
        event_id = completion.get("eventId")
        event = saved.get(event_id)
        assert completion.get("sourceId") in source_ids and completion.get("agentId") == aid and \
            completion.get("turnId") == turn_id and event is not None and \
            completion.get("kind") == event["kind"] and completion.get("seq") == event["seq"] and \
            completion.get("itemId") == (event.get("payload") or {}).get("itemId") and \
            isinstance(completion.get("at"), int) and completion["at"] >= previous and \
            event_id not in observed_ids, "foreign, missing, or duplicate completion receipt"
        observed_ids.add(event_id)
        previous = completion["at"]
    assert observed_ids == set(saved), "saved completion lacked a matching live EventSource receipt"
    by_item = {c["itemId"]: c for c in completions if c["kind"] == "item.completed"}
    assert all(a["itemId"] in by_item and a["phase"] < by_item[a["itemId"]]["phase"]
               for a in arrivals), "assistant delta arrived after its saved item completion"
    return arrivals, completions


def js_utf16_length(text):
    """Match JavaScript DOM text.length, including non-BMP characters."""
    return len(text.encode("utf-16-le", "surrogatepass")) // 2


def assert_clock_ledger(snapshot):
    """Bind every observed event and rendered frame to one monotonic page clock."""
    probe = snapshot["probe"]
    receipt = probe["arrival"]
    ticks = probe.get("ticks")
    meta = probe.get("tickMeta")
    signals = probe.get("signals")
    start = probe.get("startClock")
    closed = probe.get("arrivalClosed")
    assert isinstance(ticks, list) and 5 <= len(ticks) <= 20000 and \
        isinstance(meta, list) and len(meta) == len(ticks) and \
        isinstance(signals, list) and len(signals) == len(ticks) + 1 and \
        isinstance(start, dict) and start.get("at") == probe.get("start") and \
        isinstance(closed, dict) and closed.get("closed") is True and \
        closed.get("arrivalCount") == len(receipt["arrivals"]) and \
        closed.get("completionCount") == len(receipt["completions"]) and \
        closed.get("phase") == receipt.get("phase") and \
        closed.get("error") is None and receipt.get("error") is None, \
        "frozen EventSource counts or measured frame clock changed"
    rows = [*receipt["sources"], receipt["bound"], start,
            *receipt["arrivals"], *receipt["completions"], *meta, signals[-1]]
    assert len(rows) == receipt["phase"] and \
        all(isinstance(row, dict) and type(row.get("at")) is int and
            type(row.get("phase")) is int and row["phase"] > 0 and
            isinstance(row.get("mono"), (int, float)) and
            math.isfinite(row["mono"]) for row in rows), \
        "monotonic EventSource/frame phase ledger is missing"
    ordered = sorted(rows, key=lambda row: row["phase"])
    assert [row["phase"] for row in ordered] == list(range(1, len(rows) + 1)) and \
        all(right["mono"] >= left["mono"] and right["at"] >= left["at"]
            for left, right in zip(ordered, ordered[1:])) and \
        all(abs((row["at"] - start["at"]) - (row["mono"] - start["mono"])) <= 3
            for row in rows), \
        "monotonic EventSource/frame clock jumped or has a missing phase"
    assert all(type(row.get("tickIndex")) is int and row["tickIndex"] == i and
               row["at"] == ticks[i] and
               isinstance(row.get("rafAt"), (int, float)) and
               math.isfinite(row["rafAt"]) and row["rafAt"] <= row["mono"] + 1 and
               signals[i].get("at") == row["at"] and
               signals[i].get("mono") == row["mono"] and
               signals[i].get("phase") == row["phase"] and
               signals[i].get("tickIndex") == i for i, row in enumerate(meta)) and \
        all(right["rafAt"] > left["rafAt"] for left, right in zip(meta, meta[1:])) and \
        signals[-1].get("tickIndex") is None and signals[-1].get("phase") > meta[-1]["phase"], \
        "animation-frame index, signal, or native frame timestamp changed"
    previous = -1
    for frame in probe["frames"]:
        index = frame.get("tickIndex")
        assert type(index) is int and previous < index < len(meta) and \
            all(frame.get(key) == meta[index][key] for key in ("at", "mono", "phase")), \
            "rendered frame is not bound to its measured animation tick"
        previous = index
    reply_ids = {reply["payload"]["itemId"] for reply in snapshot["replies"]}
    owned = [arrival for arrival in receipt["arrivals"]
             if arrival.get("agentId") == snapshot["agent_id"] and
             arrival.get("turnId") == snapshot["turn_completed"]["turn_id"] and
             arrival.get("itemId") in reply_ids]
    assert owned and {arrival["itemId"] for arrival in owned} == reply_ids and \
        meta[-1]["mono"] >= max(arrival["mono"] for arrival in owned) + 300, \
        "frozen observer lacks the full owned arrival deadline horizon"
    return meta


def run_markdown_projection(answer, texts, deltas):
    frontend = Path(required("AFT_TESTS_DIR")).resolve().parents[1] / "internal/webui/frontend"
    helper = Path(required("AFT_TESTS_DIR")) / "scripts/coverage-chat-visual-markdown.mjs"
    assert frontend.is_dir() and helper.is_file(), "exact ChatMarkdown source projection is unavailable"
    payload = {"answer": answer,
               "frames": [frame if isinstance(frame, dict) else
                          {"text": frame, "streaming": True} for frame in texts],
               "arrivals": deltas}
    process = subprocess.run(["node", str(helper), str(frontend)],
                             input=json.dumps(payload, ensure_ascii=False), text=True,
                             capture_output=True, timeout=120, check=False)
    assert process.returncode == 0, \
        "exact ChatMarkdown source projection failed; Markdown pacing is unverified"
    try:
        return json.loads(process.stdout)
    except ValueError as exc:
        raise AssertionError("exact ChatMarkdown projection returned invalid JSON") from exc


def source_projection(snapshot):
    """Bind raw source offsets and visible words to exact ChatMarkdown rendering."""
    answer = snapshot["replies"][-1]["payload"]["text"]
    frames = snapshot["probe"]["frames"]
    arrivals, _ = assert_arrival_receipts(snapshot)
    item_id = snapshot["replies"][-1]["payload"]["itemId"]
    deltas = [a["text"] for a in arrivals if a["itemId"] == item_id]
    assert answer and js_utf16_length(answer) <= 8000, \
        "motion answer exceeds the bounded projection fixture budget"
    texts = [frame["text"] for frame in frames]
    plain = not re.search(r"[\n\r`*_~|#<>\[\]\\!]", answer) and \
        all(answer.startswith(text) for text in texts) and \
        snapshot["probe"]["finalText"] == answer
    if plain:
        cumulative, previous, projected = "", "", []
        for delta in deltas:
            cumulative += delta
            assert answer.startswith(cumulative), "arrival is not a saved source prefix"
            projected.append({"sourceUtf16": js_utf16_length(cumulative),
                              "visibleChanged": cumulative != previous,
                              "contentChanged": cumulative != previous,
                              "requiredMinSourceUtf16": js_utf16_length(cumulative),
                              "projectedUtf16": js_utf16_length(cumulative),
                              "projectedWords": len(cumulative.split())})
            previous = cumulative
        result = {"version": 1, "terminal": answer, "terminalVisible": answer,
                  "terminalContent": answer,
                  "frames": [{"minSourceUtf16": js_utf16_length(text),
                              "maxSourceUtf16": js_utf16_length(text),
                              "visible": text, "content": text,
                              "visibleWords": len(text.split()),
                              "contentWords": len(text.split())} for text in texts],
                  "arrivals": projected, "sourceUtf16": js_utf16_length(answer),
                  "identity": {"mode": "plain-source-byte-exact"}}
    else:
        result = run_markdown_projection(answer, frames, deltas)
        frontend = Path(required("AFT_TESTS_DIR")).resolve().parents[1] / "internal/webui/frontend"
        identity = result.get("identity")
        assert isinstance(identity, dict) and \
            identity.get("chatMarkdownSha256") == hashlib.sha256((
                frontend / "src/components/AgentChat/ChatMarkdown.tsx").read_bytes()).hexdigest() and \
            identity.get("longTextSha256") == hashlib.sha256((
                frontend / "src/components/AgentChat/LongText.tsx").read_bytes()).hexdigest() and \
            identity.get("codeHighlightSha256") == hashlib.sha256((
                frontend / "src/components/AgentChat/codeHighlight.ts").read_bytes()).hexdigest() and \
            identity.get("messageCopySha256") == hashlib.sha256((
                frontend / "src/components/AgentChat/MessageCopyButton.tsx").read_bytes()).hexdigest() and \
            identity.get("cssSha256") == hashlib.sha256((
                frontend / "src/components/AgentChat/ChatMarkdown.module.css").read_bytes()).hexdigest() and \
            identity.get("lockSha256") == hashlib.sha256((
                frontend / "package-lock.json").read_bytes()).hexdigest() and \
            isinstance(identity.get("dependencies"), dict) and \
            all(identity["dependencies"].get(name) for name in (
                "react", "react-dom", "react-markdown", "remark-gfm",
                "rehype-sanitize", "esbuild", "jsdom")), \
            "ChatMarkdown source or installed dependency identity changed"
    assert result.get("version") == 1 and result.get("sourceUtf16") == js_utf16_length(answer) and \
        len(result.get("frames", [])) == len(frames) and \
        len(result.get("arrivals", [])) == len(deltas) and \
        all(type(frame.get("streaming")) is bool and isinstance(row, dict) and
            isinstance(row.get("minSourceUtf16"), int) and
            isinstance(row.get("maxSourceUtf16"), int) and
            isinstance(row.get("visible"), str) and
            type(row.get("visibleWords")) is int and
            isinstance(row.get("content"), str) and
            type(row.get("contentWords")) is int and
            0 <= row["minSourceUtf16"] <= row["maxSourceUtf16"] <= js_utf16_length(answer)
            for frame, row in zip(frames, result["frames"])) and \
        all(isinstance(frame.get("visibleText"), str) and isinstance(frame.get("contentText"), str) and
            frame["visibleText"].split() == row["visible"].split() and
            frame["contentText"].split() == row["content"].split() and
            frame.get("words") == row["contentWords"] == len(row["content"].split()) and
            row["visibleWords"] == len(row["visible"].split())
            for frame, row in zip(frames, result["frames"])) and \
        all(isinstance(row, dict) and type(row.get("visibleChanged")) is bool and
            type(row.get("contentChanged")) is bool and
            isinstance(row.get("sourceUtf16"), int) and
            isinstance(row.get("requiredMinSourceUtf16"), int) and
            isinstance(row.get("projectedWords"), int) and
            0 <= row["requiredMinSourceUtf16"] <= row["sourceUtf16"] <= js_utf16_length(answer)
            for row in result["arrivals"]), \
        "rendered frame has no exact source-backed ChatMarkdown projection"
    positions = [row["minSourceUtf16"] for row in result["frames"]]
    assert positions == sorted(positions), \
        "rendered Markdown source projection moved backward during the live turn"
    assert result["terminal"] == snapshot["probe"]["finalText"], \
        "final rendered ChatMarkdown text differs from the exact saved answer projection"
    assert isinstance(snapshot["probe"].get("finalVisibleText"), str) and \
        result["terminalVisible"].split() == snapshot["probe"]["finalVisibleText"].split(), \
        "final visible ChatMarkdown words differ from the exact saved source projection"
    assert isinstance(snapshot["probe"].get("finalContentText"), str) and \
        result["terminalContent"].split() == snapshot["probe"]["finalContentText"].split(), \
        "final reply content differs from the exact saved source projection"
    return result


def self_test_markdown_projection():
    from unittest.mock import patch

    source = ("Start.\n\n| file | test |\n| --- | --- |\n| README.md | npm test |\n\n"
              "```json\n{\"test\":\"npm test\"}\n```\n\nEnd VISUAL_END_TEST")
    table = "Start.\nfiletestREADME.mdnpm testExpandCopy as MarkdownCopy as CSV"
    code = table + 'jsonWrapCopy{"test":"npm test"}'
    terminal = code + "End VISUAL_END_TEST"
    frames = ["Start.", table, code, terminal]
    cuts = [8, 33, 40, 64, 97, len(source)]
    deltas = [source[left:right] for left, right in zip([0, *cuts[:-1]], cuts)]
    result = run_markdown_projection(source, frames, deltas)
    assert result["version"] == 1 and result["terminal"] == terminal, result
    assert result["identity"]["chatMarkdownSha256"] == hashlib.sha256((
        Path(required("AFT_TESTS_DIR")).resolve().parents[1] /
        "internal/webui/frontend/src/components/AgentChat/ChatMarkdown.tsx").read_bytes()).hexdigest()
    assert [(r["minSourceUtf16"], r["maxSourceUtf16"]) for r in result["frames"]] == [
        (6, 8), (60, 64), (91, 97), (len(source), len(source))], result
    assert result["frames"][1]["visibleWords"] == 6 and \
        "Expand" not in result["frames"][1]["visible"], \
        "hidden streaming table controls were counted as visible words"
    assert result["frames"][2]["contentWords"] == 8 and \
        "json" not in result["frames"][2]["content"] and \
        "Wrap" not in result["frames"][2]["content"] and \
        "Copy" not in result["frames"][2]["content"], \
        "renderer-owned code header or actions counted as reply content"
    header_only = run_markdown_projection("```json\n", [], ["```json\n"])
    assert header_only["terminalVisible"].split() == ["json", "Wrap", "Copy"] and \
        header_only["terminalContent"] == "" and \
        header_only["arrivals"][0]["contentChanged"] is False and \
        header_only["arrivals"][0]["projectedWords"] == 0, \
        "code header mount created reply words without reply content"
    table_rewrite = [
        "a b c d e f g h i\n\n| Name | Result |\n",
        "| --- | --- |\n| One | Passed |",
    ]
    rewritten = run_markdown_projection("".join(table_rewrite), [], table_rewrite)
    assert [row["projectedWords"] for row in rewritten["arrivals"]] == [14, 13] and \
        rewritten["arrivals"][0]["requiredMinSourceUtf16"] < \
        rewritten["arrivals"][1]["requiredMinSourceUtf16"], \
        "real table parsing no longer retracts visible reply words"
    assert "Copy as CSV" in result["terminalVisible"], \
        "completed table controls vanished from visible projection"
    assert result["arrivals"][2]["visibleChanged"] is False and \
        result["arrivals"][2]["contentChanged"] is False, \
        "invisible GFM delimiter bytes were misclassified as newly rendered text"
    assert result["arrivals"][-1]["sourceUtf16"] == len(source)
    assert result["arrivals"][0]["requiredMinSourceUtf16"] == 6, \
        "trailing invisible syntax made a visible source prefix unmeasurable"
    rich = (source + "\n\n- first **bold** item\n- second item\n\n"
            '<img src=x onerror="window.pwned=1"> and 😀.')
    rich_result = run_markdown_projection(rich, [], [rich])
    assert rich_result["sourceUtf16"] == js_utf16_length(rich) == len(rich) + 1
    assert "first bold item" in rich_result["terminal"] and \
        '<img src=x onerror="window.pwned=1">' in rich_result["terminal"] and \
        "😀." in rich_result["terminal"], \
        "the real GFM/list/raw-HTML/emoji component projection changed"
    try:
        run_markdown_projection(source + "x" * 8001, [], [])
    except AssertionError as exc:
        assert "source projection failed" in str(exc), exc
    else:
        raise AssertionError("bounded projection fixture budget was ignored")
    arrivals = [{"at": at, "sourceId": 1, "itemId": "m1", "text": delta}
                for at, delta in zip((9, 19, 21, 29, 39, 49), deltas)]
    capture = {"replies": [{"payload": {"itemId": "m1", "text": source}}],
               "probe": {"frames": [{"at": at, "text": text, "streaming": True,
                                      "visibleText": row["visible"],
                                      "contentText": row["content"],
                                      "words": row["contentWords"]}
                                    for at, text, row in zip((10, 30, 40, 50), frames,
                                                             result["frames"])]}}
    for phase, row in enumerate(sorted([*arrivals, *capture["probe"]["frames"]],
                                       key=lambda entry: entry["at"]), 1):
        row.update(phase=phase, mono=float(row["at"]))
    with patch.object(sys.modules[__name__], "assert_arrival_receipts", return_value=(arrivals, [])):
        captured = {**capture, "probe": {**capture["probe"], "finalText": terminal,
                                          "finalVisibleText": result["terminalVisible"],
                                          "finalContentText": result["terminalContent"]}}
        assert source_projection(captured)["identity"] == result["identity"]
        backwards = {**captured, "probe": {**captured["probe"],
            "frames": [captured["probe"]["frames"][1], captured["probe"]["frames"][0],
                       *captured["probe"]["frames"][2:]]}}
        try:
            source_projection(backwards)
        except AssertionError as exc:
            assert "moved backward" in str(exc), exc
        else:
            raise AssertionError("nonmonotonic rendered source projection was accepted")
        changed = {**result, "identity": {**result["identity"], "chatMarkdownSha256": "foreign"}}
        with patch.object(sys.modules[__name__], "run_markdown_projection", return_value=changed):
            try:
                source_projection(captured)
            except AssertionError as exc:
                assert "identity changed" in str(exc), exc
            else:
                raise AssertionError("foreign Markdown source identity was accepted")
        changed_css = {**result, "identity": {**result["identity"], "cssSha256": "foreign"}}
        with patch.object(sys.modules[__name__], "run_markdown_projection", return_value=changed_css):
            try:
                source_projection(captured)
            except AssertionError as exc:
                assert "identity changed" in str(exc), exc
            else:
                raise AssertionError("foreign renderer CSS identity was accepted")
        hidden_control = json.loads(json.dumps(captured))
        hidden_control["probe"]["frames"][1]["visibleText"] += " Expand Copy as CSV"
        try:
            source_projection(hidden_control)
        except AssertionError as exc:
            assert "source-backed" in str(exc), exc
        else:
            raise AssertionError("hidden streaming controls were credited as visible")
        mismatched_terminal = {**captured, "probe": {**captured["probe"], "finalText": "foreign"}}
        try:
            source_projection(mismatched_terminal)
        except AssertionError as exc:
            assert "final rendered ChatMarkdown text differs" in str(exc), exc
        else:
            raise AssertionError("mutated final DOM text was accepted")
        mismatched_visible = {**captured, "probe": {**captured["probe"],
            "finalVisibleText": "foreign"}}
        try:
            source_projection(mismatched_visible)
        except AssertionError as exc:
            assert "final visible ChatMarkdown words differ" in str(exc), exc
        else:
            raise AssertionError("mutated completed visible words were accepted")
        mismatched_content = {**captured, "probe": {**captured["probe"],
            "finalContentText": "foreign"}}
        try:
            source_projection(mismatched_content)
        except AssertionError as exc:
            assert "final reply content differs" in str(exc), exc
        else:
            raise AssertionError("mutated completed reply content was accepted")
        deadlines = assert_arrival_deadlines(capture, result)
        assert len(deadlines) == len(arrivals) and \
            deadlines[2]["status"] == "no-new-reply-content" and \
            all(proof["lag_ms"] <= 300 for proof in deadlines if "lag_ms" in proof), deadlines
        try:
            assert_arrival_deadlines(capture, {**result, "arrivals": []})
        except AssertionError as exc:
            assert "no real visible arrival deadline" in str(exc), exc
        else:
            raise AssertionError("missing arrival projection was accepted")
        delayed = json.loads(json.dumps(capture))
        delayed["probe"]["frames"][1]["at"] = 320
        delayed["probe"]["frames"][2]["at"] = 330
        delayed["probe"]["frames"][3]["at"] = 340
        for frame in delayed["probe"]["frames"][1:]:
            frame["mono"] = float(frame["at"])
        try:
            assert_arrival_deadlines(delayed, result)
        except AssertionError as exc:
            assert "300ms catch-up deadline" in str(exc), exc
        else:
            raise AssertionError("late GFM table arrival was accepted")
    mismatched = run_markdown_projection(source, ["foreign"], deltas)
    assert mismatched["frames"] == [None], "unrelated DOM text gained a source projection"
    try:
        run_markdown_projection(source, frames, ["foreign", *deltas[1:]])
    except AssertionError as exc:
        assert "source projection failed" in str(exc), exc
    else:
        raise AssertionError("foreign arrival was accepted as saved Markdown source")
    frontend = Path(required("AFT_TESTS_DIR")).resolve().parents[1] / "internal/webui/frontend"
    ambiguous = subprocess.run(
        ["node", str(Path(required("AFT_TESTS_DIR")) /
                     "scripts/coverage-chat-visual-markdown.mjs"), str(frontend)],
        input=json.dumps({"answer": "hello[](url)", "frames": [],
                          "arrivals": ["hello[](", "url)"]}),
        text=True, capture_output=True, timeout=120, check=False)
    assert ambiguous.returncode != 0 and \
        "ambiguous-visible-arrival-projection" in ambiguous.stderr, \
        "ambiguous Markdown prefix was credited with an earlier source position"


def assert_frame_pacing(snapshot, projection=None):
    probe = snapshot["probe"]
    frames = probe["frames"]
    arrivals, completions = assert_arrival_receipts(snapshot)
    ticks = assert_clock_ledger(snapshot)
    reply = snapshot["replies"][-1]
    item_id = reply["payload"]["itemId"]
    item_arrivals = [a for a in arrivals if a["itemId"] == item_id]
    assert item_arrivals, "visible assistant item has no real delta arrival"
    final_completion = next(c for c in completions if c["eventId"] == reply["event_id"])
    projection = projection or source_projection(snapshot)
    exceptions = []
    previous_words = 0
    first_live_reply_seen = False
    for index, frame in enumerate(frames):
        text = frame.get("text")
        visible_text = frame.get("visibleText")
        content_text = frame.get("contentText")
        assert isinstance(text, str) and isinstance(visible_text, str) and isinstance(content_text, str) and \
            frame.get("chars") == js_utf16_length(text) and \
            frame.get("visibleChars") == js_utf16_length(visible_text) and \
            frame.get("contentChars") == js_utf16_length(content_text) and \
            frame.get("words") == len(content_text.split()) and frame.get("addedWords") == \
            max(0, frame["words"] - previous_words), \
            f"rendered frame bytes, words, or cadence changed at frame {index}"
        previous_words = frame["words"]
        count = frame["addedWords"]
        if frame.get("streaming") is True and frame["words"] > 0 and not first_live_reply_seen:
            first_live_reply_seen = True
            assert count <= 2, \
                "first live reply-content frame exceeded two words without a catch-up exception"
        if count <= 2:
            continue
        if index == len(frames) - 1 and frame.get("streaming") is False and \
           frame.get("marker") and \
           text == probe["finalText"] and final_completion["phase"] < frame["phase"] and \
           final_completion["mono"] <= frame["mono"] and \
           frame["mono"] - final_completion["mono"] <= 300:
            exceptions.append({"kind": "saved-completion-flush", "frame": index,
                               "added_words": count, "event_id": reply["event_id"],
                               "completion_lag_ms": frame["at"] - final_completion["at"]})
            continue
        assert index > 0, "first live frame exceeded two words without a catch-up exception"
        earlier = [a for a in item_arrivals if a["phase"] < frame["phase"] and
                   a["mono"] <= frame["mono"]]
        assert earlier, "large frame has no matching real arrival"
        raw_source_utf16 = js_utf16_length("".join(a["text"] for a in earlier))
        assert projection["frames"][index]["minSourceUtf16"] <= raw_source_utf16, \
            "large frame lacks exact source-backed arrival projection"
        previous_visible = frames[index - 1]["words"]
        previous_source = max(
            (row["minSourceUtf16"] for row in projection["frames"][:index]), default=0)
        projected_arrivals = projection["arrivals"][:len(earlier)]
        assert len(projected_arrivals) == len(earlier), \
            "large frame lacks exact source-backed arrival projection"
        recent_ticks = ticks[:frame["tickIndex"] + 1][-12:]
        intervals = [b["mono"] - a["mono"] for a, b in zip(recent_ticks, recent_ticks[1:])
                     if b["mono"] > a["mono"]]
        assert len(intervals) >= 5, "large frame lacks measured refresh cadence"
        min_interval = min(intervals)
        pending = []
        for arrival, row in zip(earlier, projected_arrivals):
            if not row["contentChanged"] or \
               row["requiredMinSourceUtf16"] <= previous_source or \
               row["projectedWords"] <= previous_visible:
                continue
            assert row["requiredMinSourceUtf16"] <= raw_source_utf16, \
                "large frame lacks exact source-backed arrival projection"
            deadline = arrival["mono"] + 300
            assert ticks[-1]["mono"] >= deadline, \
                "large frame lacks a complete measured arrival-deadline horizon"
            future_ticks = [tick for tick in ticks[frame["tickIndex"] + 1:]
                            if tick["mono"] <= deadline]
            required = row["projectedWords"] - previous_visible - 2 * len(future_ticks)
            pending.append((required, arrival, row["projectedWords"], deadline,
                            len(future_ticks)))
        assert pending, "large frame has no unrevealed arrival backlog"
        required_now, pressure, target_words, deadline, future_tick_count = max(
            pending, key=lambda entry: entry[0])
        assert required_now > 2 and count <= required_now, \
            "large frame was unnecessary for the measured arrival backlog and 300ms deadline"
        assert any(f["mono"] <= deadline and f["words"] >= target_words for f in frames[index:]), \
            "large frame did not actually meet the pending arrival deadline"
        exceptions.append({"kind": "measured-backlog-catch-up", "frame": index,
                           "added_words": count, "arrival_at": pressure["at"],
                           "deadline": pressure["at"] + 300,
                           "deadline_mono": deadline,
                           "backlog_words": projected_arrivals[-1]["projectedWords"] - previous_visible,
                           "required_now": required_now, "cadence_ms": min_interval,
                           "future_ticks": future_tick_count})
    return exceptions


def assert_arrival_deadlines(snapshot, projection=None):
    """Every visible source-backed delta prefix must reach the DOM within 300ms."""
    arrivals, _ = assert_arrival_receipts(snapshot)
    reply = snapshot["replies"][-1]
    item_id = reply["payload"]["itemId"]
    frames = snapshot["probe"]["frames"]
    projection = projection or source_projection(snapshot)
    proofs = []
    item_arrivals = [a for a in arrivals if a["itemId"] == item_id]
    for arrival, projected in zip(item_arrivals, projection["arrivals"]):
        if not projected["contentChanged"]:
            proofs.append({"source_id": arrival["sourceId"], "item_id": item_id,
                           "arrival_at": arrival["at"], "source_utf16": projected["sourceUtf16"],
                           "status": "no-new-reply-content"})
            continue
        rendered = next(((frame, mapped) for frame, mapped in zip(frames, projection["frames"])
                         if frame["phase"] > arrival["phase"] and
                         frame["mono"] >= arrival["mono"] and
                         mapped["minSourceUtf16"] >= projected["requiredMinSourceUtf16"]), None)
        assert rendered is not None, \
            "arrival lacks exact source-backed DOM projection; Markdown pacing is unverified"
        deadline = arrival["mono"] + 300
        assert rendered[0]["mono"] <= deadline, \
            "arrival text rendered after its 300ms catch-up deadline"
        proofs.append({"source_id": arrival["sourceId"], "item_id": item_id,
                       "arrival_at": arrival["at"], "deadline": arrival["at"] + 300,
                       "deadline_mono": deadline,
                       "rendered_at": rendered[0]["at"], "lag_ms": rendered[0]["at"] - arrival["at"],
                       "lag_mono_ms": rendered[0]["mono"] - arrival["mono"],
                       "source_utf16": projected["sourceUtf16"],
                       "required_min_source_utf16": projected["requiredMinSourceUtf16"],
                       "rendered_min_source_utf16": rendered[1]["minSourceUtf16"]})
    assert any("rendered_at" in proof for proof in proofs), \
        "no real visible arrival deadline was measurable"
    return proofs


def assert_terminal_exit(snapshot):
    from datetime import datetime

    probe = snapshot["probe"]
    signals = probe.get("signals")
    ticks = probe.get("ticks")
    assert isinstance(signals, list) and isinstance(ticks, list) and \
        len(signals) == len(ticks) + 1 and 5 <= len(signals) <= 20001, \
        "missing or unbounded sampled terminal controls"
    assert all(isinstance(s, dict) and isinstance(s.get("at"), int) and \
               all(type(s.get(key)) is bool for key in ("caret", "working", "stop"))
               for s in signals) and \
        [s["at"] for s in signals[:-1]] == ticks and \
        signals[-1]["at"] >= ticks[-1], \
        "terminal controls were not sampled on the measured frames"
    assert all(not signals[-1][key] for key in ("caret", "working", "stop")), \
        "caret, Working, or Stop remains at the final checkpoint"
    exits = {}
    mono_exits = {}
    for key in ("caret", "working", "stop"):
        active = [index for index, signal in enumerate(signals) if signal[key]]
        assert active, f"{key} was not sampled during the live turn"
        last_active = active[-1]
        assert last_active + 1 < len(signals) and not signals[last_active + 1][key], \
            f"{key} exit was not sampled"
        exits[key] = signals[last_active + 1]["at"]
        mono_exits[key] = signals[last_active + 1]["mono"]
    exit_at = max(exits.values())
    exit_mono = max(mono_exits.values())
    _, completions = assert_arrival_receipts(snapshot)
    turn_id = snapshot["turn_completed"]["event_id"]
    live = next(c for c in completions if c["eventId"] == turn_id)
    try:
        saved_at = int(datetime.fromisoformat(
            snapshot["turn_completed"]["created_at"].replace("Z", "+00:00")
        ).timestamp() * 1000)
    except (KeyError, AttributeError, ValueError) as exc:
        raise AssertionError("saved terminal receipt lacks a timestamp") from exc
    assert exit_at - saved_at <= 300 and exit_mono - live["mono"] <= 300, \
        "caret, Working, or Stop exited more than 300ms after saved/live terminal receipt"
    return {"exit_at": exit_at, "signal_exits": exits, "saved_at": saved_at,
            "live_at": live["at"], "lag_from_saved_ms": exit_at - saved_at,
            "lag_from_live_ms": exit_mono - live["mono"], "event_id": turn_id}


def assert_motion_capture(snapshot):
    probe = snapshot["probe"]
    delivered = snapshot["delivered"]
    end = snapshot["turn_completed"]
    replies = snapshot["replies"]
    answer = snapshot["answer"]
    assert delivered["kind"] == "message.delivered" and f"VISUAL_RENDER_{RUN}" in delivered["payload"]["text"], \
        "frozen motion delivery is not the real rendering request"
    assert end["kind"] == "agent.turn_completed" and end["seq"] > delivered["seq"], \
        "frozen motion turn completion does not follow delivery"
    assert replies and all(e["kind"] == "item.completed" and e["payload"].get("itemKind") == "message" and
                           delivered["seq"] < e["seq"] < end["seq"] for e in replies), \
        "frozen motion reply items are missing or outside the turn"
    assert answer == "\n".join(e["payload"].get("text", "") for e in replies), \
        "frozen motion answer differs from saved assistant items"
    marker = f"VISUAL_END_{RUN}"
    frames = probe["frames"]
    word_count = len(answer.split())
    assert word_count >= 300, f"model supplied only {word_count} words; 300-word motion criterion unverified"
    assert probe["observerError"] is None, probe["observerError"]
    assert probe["sawCaret"] and probe["sawWorking"] and probe["sawStop"], \
        "streaming caret/working row/Stop were not observed"
    assert len(frames) > 3 and len(set(f["chars"] for f in frames)) > 3, "no measured real text progression"
    projection = source_projection(snapshot)
    exceptions = assert_frame_pacing(snapshot, projection)
    deadlines = assert_arrival_deadlines(snapshot, projection)
    terminal_exit = assert_terminal_exit(snapshot)
    assert sum(s["value"] for s in probe["shifts"]) == 0, "non-input layout shift during streaming"
    assert probe["samples"] and all(s["gap"] == 0 for s in probe["samples"] if s["stop"]), "live follow lost the bottom"
    marker_frame = final_text_frame(answer, frames, probe["finalText"], marker)
    # The completed event timestamp is saved by the product. Browser Date.now
    # and serve share this host clock; this proven-final DOM frame is the bound.
    end_at = end.get("created_at")
    assert end_at, "turn completion lacks a saved timestamp"
    from datetime import datetime
    turn_ms = datetime.fromisoformat(end_at.replace("Z", "+00:00")).timestamp() * 1000
    lag_ms = final_text_lag(marker_frame, turn_ms)
    assert probe["caretGone"], "caret or working row remained after turn"
    return marker_frame, turn_ms, lag_ms, exceptions, terminal_exit, deadlines, projection["identity"]


def motion_assert(case):
    assert case == "render", case
    snapshot = load_motion_capture(case)
    marker_frame, turn_ms, lag_ms, exceptions, terminal_exit, deadlines, identity = assert_motion_capture(snapshot)
    write("render-motion.json", {"probe": snapshot["probe"], "turn_completed": snapshot["turn_completed"],
                                 "answer": snapshot["answer"], "final_frame": marker_frame,
                                 "turn_ms": turn_ms, "lag_ms": lag_ms, "frame_exceptions": exceptions,
                                 "terminal_exit": terminal_exit, "arrival_deadlines": deadlines,
                                 "projection_identity": identity,
                                 "capture_sha256": motion_digest(snapshot)})


def clockify_motion_fixture(candidate, *, extend_horizon=True):
    """Add an explicit simulated page clock to offline fixtures only."""
    p = candidate["probe"]
    for index, frame in enumerate(p.get("frames", [])):
        frame.setdefault("visibleText", frame.get("text"))
        frame.setdefault("contentText", frame.get("text"))
        frame.setdefault("streaming", index < len(p["frames"]) - 1)
        if isinstance(frame.get("visibleText"), str):
            frame.setdefault("visibleChars", js_utf16_length(frame["visibleText"]))
        if isinstance(frame.get("contentText"), str):
            frame.setdefault("contentChars", js_utf16_length(frame["contentText"]))
    p.setdefault("finalVisibleText", p.get("finalText"))
    p.setdefault("finalContentText", p.get("finalText"))
    r = p.get("arrival")
    if not isinstance(r, dict) or len(p.get("signals", [])) != len(p.get("ticks", [])) + 1:
        return candidate
    if extend_horizon and r.get("arrivals"):
        last = max(arrival["at"] for arrival in r["arrivals"]) + 301
        terminal = p["signals"].pop()
        while p["ticks"][-1] < last:
            tick = p["ticks"][-1] + 16
            p["ticks"].append(tick)
            active = tick < terminal["at"]
            p["signals"].append({"at": tick, "caret": active,
                                 "working": active, "stop": active})
        terminal["at"] = max(terminal["at"], p["ticks"][-1] + 1)
        p["signals"].append(terminal)
    p["startClock"] = {"at": p["start"]}
    p["tickMeta"] = [{"at": at, "tickIndex": i, "rafAt": at - p["start"] - 0.25}
                     for i, at in enumerate(p["ticks"])]
    rows = [*r["sources"], r["bound"], p["startClock"],
            *r["arrivals"], *r["completions"], *p["tickMeta"], p["signals"][-1]]
    base = min(row["at"] for row in rows)
    for phase, row in enumerate(sorted(rows, key=lambda row: row["at"]), 1):
        row["phase"] = phase
        row["mono"] = float(row["at"] - base)
    r["phase"] = len(rows)
    for i, meta in enumerate(p["tickMeta"]):
        p["signals"][i].update(at=meta["at"], phase=meta["phase"], mono=meta["mono"],
                                tickIndex=i)
    p["signals"][-1].update(tickIndex=None)
    for frame in p["frames"]:
        matches = [meta for meta in p["tickMeta"] if meta["at"] == frame["at"]]
        if len(matches) == 1:
            frame.update(phase=matches[0]["phase"], mono=matches[0]["mono"],
                         tickIndex=matches[0]["tickIndex"])
    p["arrivalClosed"].setdefault("completionCount", len(r["completions"]))
    p["arrivalClosed"]["phase"] = r["phase"]
    p["arrivalClosed"].setdefault("error", None)
    return candidate


def self_test_motion_assert():
    from copy import deepcopy
    from datetime import datetime
    from tempfile import TemporaryDirectory
    from unittest.mock import patch

    end_at = "2026-10-07T00:00:00Z"
    end_ms = int(datetime.fromisoformat(end_at.replace("Z", "+00:00")).timestamp() * 1000)
    marker = f"VISUAL_END_{RUN}"
    words = ["word"] * 299 + [marker]
    answer = " ".join(words)
    frames = [{"at": end_ms - 40 - (len(words) - i) * 16,
               "chars": len(" ".join(words[:i])), "words": i,
               "addedWords": 1, "marker": i == len(words),
               "text": " ".join(words[:i])}
              for i in range(1, len(words) + 1)]
    route = f"/ws/{WS}/chat/agt_test"
    bound_at = frames[0]["at"] - 17
    arrivals = [{"at": frame["at"] - 1, "sourceId": 1, "agentId": "agt_test",
                 "turnId": "t1", "itemId": "m1", "text": (" " if i else "") + words[i]}
                for i, frame in enumerate(frames)]
    completions = [{"at": frames[-1]["at"] - 1, "sourceId": 1, "agentId": "agt_test",
                    "turnId": "t1", "kind": "item.completed", "seq": 2,
                    "eventId": "reply", "itemId": "m1"},
                   {"at": end_ms, "sourceId": 1, "agentId": "agt_test",
                    "turnId": "t1", "kind": "agent.turn_completed", "seq": 3,
                    "eventId": "completed", "itemId": None}]
    probe = {"route": route, "start": frames[0]["at"] - 16,
             "frames": frames, "ticks": [frame["at"] for frame in frames],
             "signals": [{"at": frame["at"], "caret": True, "working": True, "stop": True}
                         for frame in frames] +
                        [{"at": end_ms + 10, "caret": False, "working": False, "stop": False}],
             "finalText": answer,
             "arrival": {"version": 1, "workspace": WS, "run": RUN,
                         "bound": {"agentId": "agt_test", "route": route, "at": bound_at},
                         "sources": [{"id": 1, "at": bound_at - 10, "agents": ["agt_test"]}],
                         "arrivals": arrivals, "completions": completions, "error": None},
             "arrivalClosed": {"closed": True, "arrivalCount": len(arrivals)},
             "samples": [{"stop": True, "gap": 0}], "shifts": [],
             "sawCaret": True, "sawWorking": True, "sawStop": True,
             "observerError": None, "caretGone": True}
    reply = {"kind": "item.completed", "event_id": "reply", "seq": 2,
             "turn_id": "t1", "payload": {"itemId": "m1", "itemKind": "message", "text": answer}}
    snapshot = {"version": 1, "case": "render", "ws": WS, "run": RUN,
                "agent_id": "agt_test", "route": route, "probe": probe,
                "delivered": {"kind": "message.delivered", "event_id": "delivered", "seq": 1,
                              "payload": {"text": f"VISUAL_RENDER_{RUN}"}},
                "turn_completed": {"kind": "agent.turn_completed", "event_id": "completed",
                                   "seq": 3, "turn_id": "t1", "created_at": end_at, "payload": {}},
                "replies": [reply], "answer": answer}
    clockify_motion_fixture(snapshot)
    assert assert_motion_capture(snapshot)[2] == -40
    steady_deadlines = assert_arrival_deadlines(snapshot)
    assert len(steady_deadlines) == 300 and all(p["lag_ms"] == 1 for p in steady_deadlines)

    def rejects(change, expected, *, reclock=True):
        candidate = deepcopy(snapshot)
        change(candidate)
        if reclock:
            clockify_motion_fixture(candidate)
        try:
            with patch.object(sys.modules[__name__], "load_motion_capture", return_value=candidate), \
                 patch.object(sys.modules[__name__], "write", side_effect=AssertionError("wrote a pass artifact")):
                motion_assert("render")
        except AssertionError as exc:
            assert expected in str(exc), (expected, exc)
        else:
            raise AssertionError(f"motion oracle accepted {expected}")

    def short(candidate):
        text = " ".join(["word"] * 233 + [marker])
        candidate["answer"] = candidate["replies"][0]["payload"]["text"] = text

    rejects(short, "300-word")  # The real provider's 234-word short answer must fail.
    rejects(lambda c: c["probe"]["frames"][0].update(addedWords=17), "frame bytes")
    def ordinary_three_reply_words(candidate):
        p = candidate["probe"]
        arrival = p["arrival"]["arrivals"]
        arrival[5]["text"] += arrival[6]["text"] + arrival[7]["text"]
        del arrival[6:8]
        p["arrivalClosed"]["arrivalCount"] = len(arrival)
        for index in (5, 6, 7):
            frame = p["frames"][index]
            frame["text"] = frame["visibleText"] = frame["contentText"] = " ".join(words[:8])
            frame["chars"] = frame["visibleChars"] = frame["contentChars"] = len(frame["text"])
            frame["words"] = 8
            frame["addedWords"] = 3 if index == 5 else 0
    rejects(ordinary_three_reply_words, "large frame was unnecessary")
    rejects(lambda c: c["probe"]["shifts"].append({"value": 0.012443148334330491}), "layout shift")
    rejects(lambda c: c["probe"].update(observerError="observer failed"), "observer failed")
    rejects(lambda c: c["probe"].update(sawCaret=False), "streaming caret/working")
    rejects(lambda c: c["probe"].update(sawStop=False), "streaming caret/working")
    rejects(lambda c: c["probe"].update(sawWorking=False), "streaming caret/working")
    rejects(lambda c: c["probe"].update(frames=c["probe"]["frames"][:3]), "no measured real text")
    rejects(lambda c: [f.update(chars=1) for f in c["probe"]["frames"]], "no measured real text")
    rejects(lambda c: c["probe"].update(samples=[]), "live follow")
    rejects(lambda c: c["probe"]["samples"][0].update(gap=1), "live follow")
    rejects(lambda c: c["probe"]["frames"][0].update(marker=True), "before later rendered text")
    rejects(lambda c: c["probe"]["frames"][-1].update(text=answer[:-1] + "X"),
            "rendered frame has no exact source-backed")
    def late_final_text(candidate):
        p = candidate["probe"]
        p["frames"][-1]["at"] = end_ms + 301
        p["ticks"][-1] = end_ms + 301
        p["signals"][-3].update(caret=False, working=False, stop=False)
        p["signals"][-2].update(at=end_ms + 301, caret=False, working=False, stop=False)
        p["signals"][-1]["at"] = end_ms + 302

    rejects(late_final_text, "arrival text rendered after its 300ms catch-up deadline")
    rejects(lambda c: c["probe"].update(caretGone=False), "caret or working row remained")
    rejects(lambda c: c["probe"].update(signals=[]), "frozen EventSource counts or measured frame clock")
    rejects(lambda c: ([s.update(caret=True, working=True, stop=True)
                        for s in c["probe"]["signals"][:-1]],
                       c["probe"]["signals"][-1].update(at=end_ms + 301)),
            "exited more than 300ms")
    rejects(lambda c: c["probe"]["signals"][-1].update(working=True),
            "remains at the final checkpoint")
    rejects(lambda c: [s.update(stop=False) for s in c["probe"]["signals"]],
            "stop was not sampled")
    rejects(lambda c: c["probe"]["arrival"]["completions"][-1].update(eventId="foreign"),
            "foreign, missing, or duplicate completion")
    rejects(lambda c: c["turn_completed"].update(created_at=""), "lacks a timestamp")
    rejects(lambda c: c["delivered"]["payload"].update(text="foreign"), "not the real rendering")
    rejects(lambda c: c.update(answer="changed"), "differs from saved assistant")
    rejects(lambda c: c["replies"][0].update(seq=4), "outside the turn")
    rejects(lambda c: c["probe"].update(arrival=None), "missing or foreign pre-navigation")
    rejects(lambda c: c["probe"]["arrival"].update(workspace="FOREIGN"), "missing or foreign pre-navigation")
    rejects(lambda c: c["probe"]["arrival"]["arrivals"][0].update(sourceId=99), "foreign, missing")
    rejects(lambda c: c["probe"]["arrival"]["arrivals"][0].update(text="foreign"), "actual delta bytes")
    rejects(lambda c: c["probe"]["arrival"]["completions"].pop(0), "saved completion lacked")
    rejects(lambda c: c["probe"].update(arrivalClosed={"closed": False}), "missing or foreign pre-navigation")
    rejects(lambda c: c["probe"]["arrivalClosed"].update(arrivalCount=999),
            "frozen EventSource counts", reclock=False)
    rejects(lambda c: c["probe"]["frames"][2].update(tickIndex=1),
            "rendered frame is not bound", reclock=False)
    rejects(lambda c: c["probe"]["signals"][2].update(tickIndex=99),
            "animation-frame index", reclock=False)
    rejects(lambda c: c["probe"]["arrival"]["arrivals"][2].update(mono=999999.0),
            "clock jumped", reclock=False)
    rejects(lambda c: c["probe"]["tickMeta"][2].update(phase=1),
            "missing phase", reclock=False)
    rejects(lambda c: c["probe"]["frames"][0].pop("visibleText"),
            "rendered frame has no exact source-backed", reclock=False)
    rejects(lambda c: c["probe"]["frames"][0].update(visibleText="foreign"),
            "rendered frame has no exact source-backed", reclock=False)
    rejects(lambda c: c["probe"]["frames"][0].update(visibleChars=999),
            "rendered frame bytes, words", reclock=False)
    rejects(lambda c: c["probe"]["frames"][0].pop("streaming"),
            "source projection failed", reclock=False)

    ordinary_short_horizon = deepcopy(snapshot)
    last_arrival_at = ordinary_short_horizon["probe"]["arrival"]["arrivals"][-1]["at"]
    ordinary_short_horizon["probe"]["ticks"] = [
        tick for tick in ordinary_short_horizon["probe"]["ticks"]
        if tick < last_arrival_at + 300]
    ordinary_short_horizon["probe"]["signals"] = [
        *ordinary_short_horizon["probe"]["signals"][:len(ordinary_short_horizon["probe"]["ticks"])],
        ordinary_short_horizon["probe"]["signals"][-1]]
    clockify_motion_fixture(ordinary_short_horizon, extend_horizon=False)
    try:
        assert_frame_pacing(ordinary_short_horizon)
    except AssertionError as exc:
        assert "full owned arrival deadline horizon" in str(exc), exc
    else:
        raise AssertionError("ordinary two-word pacing accepted a truncated arrival horizon")

    late_plain = deepcopy(snapshot)
    late_arrivals = late_plain["probe"]["arrival"]["arrivals"]
    late_arrivals[0]["text"] = " ".join(words[:22])
    del late_arrivals[1:22]
    late_plain["probe"]["arrivalClosed"]["arrivalCount"] = len(late_arrivals)
    assert max(frame["addedWords"] for frame in late_plain["probe"]["frames"]) == 1
    rejects(lambda c: c.update(probe=deepcopy(late_plain["probe"])),
            "arrival text rendered after its 300ms catch-up deadline")

    markdown = deepcopy(snapshot)
    markdown["probe"]["arrival"]["arrivals"][0]["text"] = "**word**"
    markdown["replies"][0]["payload"]["text"] = "**word**" + answer[len("word"):]
    markdown["answer"] = markdown["replies"][0]["payload"]["text"]
    clockify_motion_fixture(markdown)
    assert assert_motion_capture(markdown)[2] == -40, \
        "source-backed bold Markdown was rejected despite identical measured DOM text"
    rejects(lambda c: c.update(probe=deepcopy(markdown["probe"]),
                               replies=deepcopy(markdown["replies"]),
                               answer=markdown["answer"]) or
            c["probe"]["frames"][0].update(text="foreign"),
            "source-backed")

    def first_packet(candidate, count):
        amounts = [count] + [1] * (len(words) - count)
        first_at = end_ms - 40 - (len(amounts) - 1) * 16
        totals = []
        running = 0
        for amount in amounts:
            running += amount
            totals.append(running)
        new_frames = [{"at": first_at + i * 16, "chars": len(" ".join(words[:total])),
                       "words": total, "addedWords": amount, "marker": total == len(words),
                       "text": " ".join(words[:total])}
                      for i, (amount, total) in enumerate(zip(amounts, totals))]
        first = new_frames[0]["at"]
        new_arrivals = [{"at": frame["at"] - 1, "sourceId": 1, "agentId": "agt_test",
                         "turnId": "t1", "itemId": "m1",
                         "text": (" " if i else "") + " ".join(words[total - amount:total])}
                        for i, (frame, amount, total) in enumerate(zip(new_frames, amounts, totals))]
        p = candidate["probe"]
        p["frames"] = new_frames
        p["ticks"] = [f["at"] for f in new_frames]
        p["signals"] = ([{"at": f["at"], "caret": True, "working": True, "stop": True}
                         for f in new_frames] +
                        [{"at": end_ms + 10, "caret": False, "working": False, "stop": False}])
        p["start"] = first - 16
        p["arrival"]["bound"]["at"] = first - 17
        p["arrival"]["sources"][0]["at"] = first - 27
        p["arrival"]["arrivals"] = new_arrivals
        p["arrival"]["completions"][0]["at"] = new_frames[-1]["at"] - 1
        p["arrivalClosed"]["arrivalCount"] = len(new_arrivals)

    for amount in (15, 17):
        rejects(lambda c, n=amount: first_packet(c, n),
                "first live reply-content frame exceeded two words")

    burst = deepcopy(snapshot)
    # One actual 20-word packet arrives before the first visible word. At the
    # measured 16ms cadence, four words at tick 15 are necessary to render all
    # 20 by the packet's 300ms deadline; a burst at tick 11 is unnecessary.
    tick_count = 299
    first_at = end_ms - 40 - (tick_count - 1) * 16
    ticks = [first_at + i * 16 for i in range(tick_count)]
    plan = [(i, i + 1) for i in range(10)] + [(15, 14), (16, 16), (17, 18), (18, 20)] + \
           [(i + 19, i + 21) for i in range(280)]
    burst_frames = []
    prior = 0
    for tick_index, total in plan:
        burst_frames.append({"at": ticks[tick_index], "chars": len(" ".join(words[:total])),
                             "words": total, "addedWords": total - prior,
                             "marker": total == len(words), "text": " ".join(words[:total])})
        prior = total
    burst_arrivals = [{"at": ticks[0] - 1, "sourceId": 1, "agentId": "agt_test",
                       "turnId": "t1", "itemId": "m1", "text": " ".join(words[:20])}]
    burst_arrivals += [{"at": ticks[i + 19] - 1, "sourceId": 1, "agentId": "agt_test",
                        "turnId": "t1", "itemId": "m1", "text": " " + words[i + 20]}
                       for i in range(280)]
    p = burst["probe"]
    p["frames"] = burst_frames
    p["ticks"] = ticks
    p["signals"] = ([{"at": at, "caret": True, "working": True, "stop": True}
                     for at in ticks] +
                    [{"at": end_ms + 10, "caret": False, "working": False, "stop": False}])
    p["start"] = first_at - 16
    p["arrival"]["bound"]["at"] = first_at - 17
    p["arrival"]["sources"][0]["at"] = first_at - 27
    p["arrival"]["arrivals"] = burst_arrivals
    p["arrival"]["completions"][0]["at"] = burst_frames[-1]["at"] - 1
    p["arrivalClosed"]["arrivalCount"] = len(burst_arrivals)
    clockify_motion_fixture(burst)
    permitted = assert_motion_capture(burst)[3]
    assert permitted == [{"kind": "measured-backlog-catch-up", "frame": 10,
                          "added_words": 4, "arrival_at": ticks[0] - 1,
                          "deadline": ticks[0] + 299,
                          "deadline_mono": burst_arrivals[0]["mono"] + 300,
                          "backlog_words": 10,
                          "required_now": 4, "cadence_ms": 16,
                          "future_ticks": 3}], permitted
    # Two real prefixes may share a deadline. Budgeting only the oldest 12
    # words would allow at most 12 by that deadline, despite the already
    # arrived 20-word prefix. The second prefix supplies the necessary bound.
    overlapping = deepcopy(burst)
    overlap_arrivals = overlapping["probe"]["arrival"]["arrivals"]
    first_packet_arrival = overlap_arrivals[0]
    first_packet_arrival["text"] = " ".join(words[:12])
    overlap_arrivals.insert(1, {**first_packet_arrival, "text": " " + " ".join(words[12:20])})
    overlapping["probe"]["arrivalClosed"]["arrivalCount"] = len(overlap_arrivals)
    clockify_motion_fixture(overlapping)
    joint = assert_motion_capture(overlapping)[3]
    assert len(joint) == 1 and joint[0]["frame"] == 10 and \
        joint[0]["arrival_at"] == overlap_arrivals[1]["at"] and \
        joint[0]["required_now"] == 4 and joint[0]["future_ticks"] == 3, joint
    assert 12 - 10 - 2 * 3 <= 2, "the older prefix alone no longer distinguishes this fixture"
    assert len(assert_arrival_deadlines(overlapping)) == len(overlap_arrivals)

    chrome_only = deepcopy(overlapping)
    for frame in chrome_only["probe"]["frames"][:10]:
        frame.update(text="", visibleText="", contentText="", chars=0,
                     visibleChars=0, contentChars=0, words=0, addedWords=0)
    chrome = chrome_only["probe"]["frames"][0]
    chrome.update(text="jsonWrapCopy", visibleText="json\nWrap\nCopy",
                  chars=len("jsonWrapCopy"), visibleChars=len("json\nWrap\nCopy"))
    chrome_only["probe"]["frames"][10]["addedWords"] = 14
    try:
        assert_frame_pacing(chrome_only, source_projection(overlapping))
    except AssertionError as exc:
        assert "first live reply-content frame exceeded two words" in str(exc), exc
    else:
        raise AssertionError("renderer chrome hid an oversized first reply-content frame")

    retracted = source_projection(overlapping)
    retracted["frames"][0]["minSourceUtf16"] = \
        retracted["arrivals"][1]["requiredMinSourceUtf16"]
    try:
        assert_frame_pacing(overlapping, retracted)
    except AssertionError as exc:
        assert "no unrevealed arrival backlog" in str(exc), exc
    else:
        raise AssertionError("an already-proven source prefix regained catch-up credit")

    staggered = deepcopy(overlapping)
    staggered_arrivals = staggered["probe"]["arrival"]["arrivals"]
    staggered_arrivals[0]["at"] -= 9
    clockify_motion_fixture(staggered)
    competing = assert_motion_capture(staggered)[3]
    assert len(competing) == 1 and competing[0]["arrival_at"] == \
        staggered_arrivals[1]["at"] and competing[0]["required_now"] == 4, competing

    rejects(lambda c: c.update(probe=deepcopy(overlapping["probe"])) or
            c["probe"]["arrival"]["arrivals"][1].update(at=ticks[10] + 1),
            "large frame was unnecessary")
    rejects(lambda c: c.update(probe=deepcopy(overlapping["probe"])) or
            c["probe"]["arrival"]["arrivals"][1].update(agentId="foreign"),
            "foreign")
    rejects(lambda c: c.update(probe=deepcopy(overlapping["probe"])) or
            [c["probe"]["frames"][j].update(at=ticks[11 + j - 10]) for j in range(10, 14)],
            "large frame was unnecessary")
    incomplete = deepcopy(overlapping)
    incomplete["probe"]["frames"] = incomplete["probe"]["frames"][:14]
    incomplete["probe"]["ticks"] = ticks[:19]
    incomplete["probe"]["signals"] = [*incomplete["probe"]["signals"][:19],
                                        incomplete["probe"]["signals"][-1]]
    clockify_motion_fixture(incomplete, extend_horizon=False)
    try:
        assert_frame_pacing(incomplete)
    except AssertionError as exc:
        assert "full owned arrival deadline horizon" in str(exc), exc
    else:
        raise AssertionError("truncated future-cadence window was accepted")
    burst_deadlines = assert_arrival_deadlines(burst)
    assert burst_deadlines[0]["lag_ms"] == 18 * 16 + 1 and \
        all(p["lag_ms"] <= 300 for p in burst_deadlines)
    rejects(lambda c: c.update(probe=deepcopy(burst["probe"])) or
            [c["probe"]["frames"][j].update(at=ticks[11 + j - 10]) for j in range(10, 14)],
            "large frame was unnecessary")
    rejects(lambda c: c.update(probe=deepcopy(burst["probe"])) or
            (c["probe"]["arrival"]["arrivals"][0].update(at=ticks[0] - 51),
             c["probe"]["arrival"]["bound"].update(at=ticks[0] - 61),
             c["probe"]["arrival"]["sources"][0].update(at=ticks[0] - 71)),
            "pending arrival deadline")

    flushed = deepcopy(snapshot)
    flushed["probe"]["frames"] = [*flushed["probe"]["frames"][:290],
                                   {**flushed["probe"]["frames"][-1], "addedWords": 10}]
    clockify_motion_fixture(flushed)
    assert assert_motion_capture(flushed)[3][0]["kind"] == "saved-completion-flush"
    first_completed = {"replies": [{"payload": {"itemId": "m1"}, "event_id": "reply"}],
                       "probe": {"finalText": answer, "frames": [
                           {"text": answer, "visibleText": answer, "contentText": answer,
                            "chars": js_utf16_length(answer),
                            "visibleChars": js_utf16_length(answer),
                            "contentChars": js_utf16_length(answer),
                            "words": 300, "addedWords": 300, "streaming": False,
                            "marker": True, "at": 20, "mono": 20.0,
                            "phase": 10, "tickIndex": 1}]}}
    first_arrival = [{"itemId": "m1", "text": answer, "at": 0,
                      "mono": 0.0, "phase": 2}]
    first_receipt = {"eventId": "reply", "at": 10, "mono": 10.0, "phase": 5}
    first_projection = {"frames": [{"minSourceUtf16": js_utf16_length(answer)}],
                        "arrivals": []}

    def check_first_completed(candidate, receipt):
        with patch.object(sys.modules[__name__], "assert_arrival_receipts",
                          return_value=(first_arrival, [receipt] if receipt else [])), \
             patch.object(sys.modules[__name__], "assert_clock_ledger",
                          return_value=[{"mono": float(i)} for i in range(0, 401, 10)]):
            return assert_frame_pacing(candidate, first_projection)

    assert check_first_completed(first_completed, first_receipt)[0]["kind"] == \
        "saved-completion-flush"
    for change, receipt in [
        (lambda c: c["probe"]["frames"][0].update(streaming=True), first_receipt),
        (lambda c: c["probe"]["frames"][0].update(at=311, mono=311.0), first_receipt),
        (lambda c: c["probe"]["frames"][0].update(marker=False), first_receipt),
        (lambda c: None, {**first_receipt, "eventId": "foreign"}),
        (lambda c: None, None),
    ]:
        candidate = deepcopy(first_completed)
        change(candidate)
        try:
            check_first_completed(candidate, receipt)
        except (AssertionError, StopIteration):
            pass
        else:
            raise AssertionError("unmatched, late, or live first-frame flush was accepted")
    rejects(lambda c: c.update(probe=deepcopy(flushed["probe"])) or
            (c["probe"]["frames"][-1].update(at=end_ms + 301),
             c["probe"]["ticks"].__setitem__(-1, end_ms + 301),
             c["probe"]["signals"][-2].update(at=end_ms + 301),
             c["probe"]["signals"][-1].update(at=end_ms + 302)),
            "pending arrival deadline")

    emoji = deepcopy(snapshot)
    emoji_words = ["😀"] * 299 + [marker]
    emoji_answer = " ".join(emoji_words)
    emoji["answer"] = emoji["replies"][0]["payload"]["text"] = emoji_answer
    emoji["probe"]["finalText"] = emoji_answer
    for index, frame in enumerate(emoji["probe"]["frames"]):
        frame["text"] = " ".join(emoji_words[:index + 1])
        frame["chars"] = js_utf16_length(frame["text"])
        frame["visibleText"] = frame["text"]
        frame["visibleChars"] = frame["chars"]
        frame["contentText"] = frame["text"]
        frame["contentChars"] = frame["chars"]
        emoji["probe"]["arrival"]["arrivals"][index]["text"] = \
            (" " if index else "") + emoji_words[index]
    emoji["probe"]["finalVisibleText"] = emoji_answer
    emoji["probe"]["finalContentText"] = emoji_answer
    clockify_motion_fixture(emoji)
    assert js_utf16_length("A😀") == 3 and assert_motion_capture(emoji)[2] == -40
    rejects(lambda c: c.update(probe=deepcopy(emoji["probe"]),
                               answer=emoji_answer,
                               replies=deepcopy(emoji["replies"])) or
            c["probe"]["frames"][0].update(chars=1),
            "rendered frame bytes, words, or cadence changed")

    with TemporaryDirectory() as folder, patch.dict(globals(), WORK=Path(folder)):
        (WORK / "render.id").write_text("agt_test\n")
        try:
            load_motion_capture("render")
        except AssertionError as exc:
            assert "missing" in str(exc), exc
        else:
            raise AssertionError("missing motion capture was accepted")
        save_motion_capture(snapshot)
        assert load_motion_capture("render") == snapshot
        try:
            save_motion_capture(snapshot)
        except AssertionError as exc:
            assert "already exists" in str(exc), exc
        else:
            raise AssertionError("reduced-motion turn could overwrite first-turn capture")
        with patch.object(sys.modules[__name__], "evaluate", side_effect=AssertionError("late DOM read")), \
             patch.object(sys.modules[__name__], "events", side_effect=AssertionError("late event read")), \
             patch.object(sys.modules[__name__], "current", side_effect=AssertionError("late route read")):
            motion_assert("render")
        assert (WORK / "render-motion.json").is_file()
        changed = deepcopy(snapshot)
        changed["probe"]["finalText"] = "mutated"
        write("render-motion-capture.json", changed)
        try:
            load_motion_capture("render")
        except AssertionError as exc:
            assert "changed or belongs" in str(exc), exc
        else:
            raise AssertionError("mutated motion capture was accepted")
        foreign = deepcopy(snapshot)
        foreign["run"] = "FOREIGN"
        write("render-motion-capture.json", foreign)
        write("render-motion-seal.json", {"sha256": motion_digest(foreign), "case": "render",
                                          "ws": WS, "run": "FOREIGN", "agent_id": "agt_test"})
        try:
            load_motion_capture("render")
        except AssertionError as exc:
            assert "foreign run" in str(exc), exc
        else:
            raise AssertionError("foreign motion capture was accepted")


def turn_events(case, marker):
    evs = events(case)
    delivered = [e for e in evs if e["kind"] == "message.delivered" and marker in e["payload"].get("text", "")]
    assert len(delivered) == 1, f"expected one actual Chat delivery for {marker}: {delivered}"
    end = next((e for e in evs if e["kind"] == "agent.turn_completed" and e["seq"] > delivered[0]["seq"]), None)
    assert end, "real turn did not complete"
    return delivered[0], end, [e for e in evs if delivered[0]["seq"] < e["seq"] < end["seq"]]


GROUP_COLLAPSED = ("[data-testid=chat-transcript] [data-testid=tool-group][aria-expanded=false], "
                   "[data-testid=chat-transcript] [data-testid=work-toggle][aria-expanded=false]")
ENTRY_COLLAPSED = ("[data-testid=chat-transcript] [data-testid=tool-call] [role=button][aria-expanded=false], "
                   "[data-testid=chat-transcript] [data-testid=reasoning] [role=button][aria-expanded=false]")


def expansion_geometry(selector):
    # Only geometry, DOM identity and hit ownership leave the browser; never tool text.
    js = """(() => {
      const pane=document.querySelector('[data-testid=chat-transcript]');
      const matches=[...document.querySelectorAll(SELECTOR)];
      const target=matches[0];
      if(!pane || !target) return {route:location.pathname,count:matches.length,panePresent:!!pane};
      const r=target.getBoundingClientRect(), p=pane.getBoundingClientRect();
      const x=r.left+r.width/2,y=r.top+r.height/2;
      const hit=document.elementFromPoint(x,y);
      const row=target.closest('li[data-kind]');
      return {route:location.pathname,count:matches.length,panePresent:true,
        inPane:pane.contains(target),rowIndex:row?[...pane.querySelectorAll('li[data-kind]')].indexOf(row):-1,
        kind:target.dataset.testid||target.closest('[data-testid]')?.dataset.testid||null,
        button:target.tagName==='BUTTON'||target.getAttribute('role')==='button',
        expanded:target.getAttribute('aria-expanded'),status:target.closest('[data-status]')?.dataset.status||null,
        rect:{left:r.left,top:r.top,right:r.right,bottom:r.bottom,width:r.width,height:r.height},
        pane:{left:p.left,top:p.top,right:p.right,bottom:p.bottom},
        viewport:{width:innerWidth,height:innerHeight},
        hitTag:hit?.tagName||null,hitTarget:!!hit&&(hit===target||target.contains(hit)),
        centerInPane:x>=p.left&&x<=p.right&&y>=p.top&&y<=p.bottom,
        centerInViewport:x>=0&&x<=innerWidth&&y>=0&&y<=innerHeight};
    })()""".replace("SELECTOR", json.dumps(selector))
    return evaluate(js)


def record_expansion_geometry(entry):
    path = WORK / "render-expand-geometry.json"
    history = json.loads(path.read_text()) if path.exists() else []
    assert isinstance(history, list) and len(history) < 768, "bounded expansion geometry history exhausted"
    history.append(entry)
    write(path.name, history)


def validate_expansion_target(geometry, expected_kinds, after_scroll):
    assert geometry.get("route") == f"/ws/{WS}/chat/{agent_id('render')}", "work toggle changed owned Chat route"
    assert geometry.get("panePresent") and geometry.get("inPane") and geometry.get("count", 0) > 0, \
        "work toggle missing from owned transcript"
    assert geometry.get("kind") in expected_kinds and geometry.get("button") and \
        geometry.get("expanded") == "false" and geometry.get("rowIndex", -1) >= 0, \
        "work toggle identity, role or collapsed state changed"
    if expected_kinds == ("tool-call", "reasoning"):
        assert geometry.get("status") in ("completed", "failed"), "work entry is not completed"
    if after_scroll:
        rect = geometry.get("rect") or {}
        assert rect.get("width", 0) > 0 and rect.get("height", 0) > 0 and \
            geometry.get("centerInPane") and geometry.get("centerInViewport") and \
            geometry.get("hitTarget"), "centered work toggle remains covered or outside Chat pane"


def click_collapsed_work(selector, expected_kinds):
    before = expansion_geometry(selector)
    record = {"kinds": expected_kinds, "selector": selector, "before": before, "outcome": "pending"}
    record_expansion_geometry(record)
    validate_expansion_target(before, expected_kinds, False)
    try:
        browser("scrollintoview", selector)
    except Exception:
        record_expansion_geometry({**record, "outcome": "scroll-failed"})
        raise
    after = expansion_geometry(selector)
    record = {**record, "after": after, "outcome": "centered-pending-hit-test"}
    record_expansion_geometry(record)
    assert (after.get("count"), after.get("kind"), after.get("rowIndex"), after.get("status")) == \
        (before.get("count"), before.get("kind"), before.get("rowIndex"), before.get("status")), \
        "first collapsed work toggle changed during centering"
    validate_expansion_target(after, expected_kinds, True)
    try:
        browser("click", selector)
    except Exception:
        record_expansion_geometry({**record, "outcome": "click-failed"})
        raise
    record_expansion_geometry({**record, "outcome": "clicked"})


def expand_work():
    for _ in range(20):
        closed = evaluate(f"document.querySelectorAll({json.dumps(GROUP_COLLAPSED)}).length")
        if not closed:
            break
        click_collapsed_work(GROUP_COLLAPSED, ("tool-group", "work-toggle"))
    else:
        raise AssertionError("too many collapsed work groups")
    for _ in range(30):
        closed = evaluate(f"document.querySelectorAll({json.dumps(ENTRY_COLLAPSED)}).length")
        if not closed:
            break
        click_collapsed_work(ENTRY_COLLAPSED, ("tool-call", "reasoning"))
    else:
        raise AssertionError("too many collapsed work entries")


def self_test_collapsed_work_click():
    from copy import deepcopy
    from tempfile import TemporaryDirectory
    from unittest.mock import call, patch

    route = f"/ws/{WS}/chat/agt_test"
    before = {"route": route, "count": 2, "panePresent": True, "inPane": True,
              "rowIndex": 1, "kind": "tool-call", "button": True, "expanded": "false",
              "status": "completed", "rect": {"width": 300, "height": 30},
              "centerInPane": False, "centerInViewport": True, "hitTarget": False,
              "hitTag": "HEADER"}
    centered = {**before, "centerInPane": True, "hitTarget": True, "hitTag": "DIV"}

    def trial(first=before, second=centered, error=None, expected_calls=2):
        with TemporaryDirectory() as folder, patch.dict(globals(), WORK=Path(folder)):
            (WORK / "render.id").write_text("agt_test\n")
            with patch.object(sys.modules[__name__], "expansion_geometry", side_effect=[first, second]) as geometry, \
                 patch.object(sys.modules[__name__], "browser") as drive:
                if error:
                    try:
                        click_collapsed_work(ENTRY_COLLAPSED, ("tool-call", "reasoning"))
                    except AssertionError as exc:
                        assert error in str(exc), (error, exc)
                    else:
                        raise AssertionError(f"invalid work toggle accepted: {error}")
                else:
                    click_collapsed_work(ENTRY_COLLAPSED, ("tool-call", "reasoning"))
                assert drive.call_args_list == [
                    call("scrollintoview", ENTRY_COLLAPSED), call("click", ENTRY_COLLAPSED)
                ][:expected_calls], drive.call_args_list
                assert geometry.call_count == (2 if expected_calls else 1)
            history = json.loads((WORK / "render-expand-geometry.json").read_text())
            assert history[0]["before"] == first and len(history) >= 1
            assert all("text" not in str(item).lower() for item in history), "raw tool text in geometry receipt"
            if error and expected_calls == 1:
                assert history[-1]["outcome"] == "centered-pending-hit-test"

    trial()
    trial(second={**centered, "hitTarget": False, "hitTag": "HEADER"},
          error="remains covered", expected_calls=1)
    trial(first={**before, "route": "/ws/FOREIGN/chat/agt_test"},
          error="owned Chat route", expected_calls=0)
    trial(first={**before, "panePresent": False, "count": 0},
          error="missing from owned transcript", expected_calls=0)
    trial(second={**centered, "count": 1},
          error="changed during centering", expected_calls=1)
    trial(first={**before, "status": "running"}, error="not completed", expected_calls=0)
    trial(first={**before, "expanded": "true"}, error="collapsed state", expected_calls=0)
    trial(first={**before, "button": False}, error="role or collapsed", expected_calls=0)

    with TemporaryDirectory() as folder, patch.dict(globals(), WORK=Path(folder)):
        (WORK / "render.id").write_text("agt_test\n")
        with patch.object(sys.modules[__name__], "expansion_geometry", return_value=before), \
             patch.object(sys.modules[__name__], "browser", side_effect=RuntimeError("scroll failed")):
            try:
                click_collapsed_work(ENTRY_COLLAPSED, ("tool-call", "reasoning"))
            except RuntimeError:
                pass
            else:
                raise AssertionError("failed scroll was ignored")
        history = json.loads((WORK / "render-expand-geometry.json").read_text())
        assert history[-1]["outcome"] == "scroll-failed"

    with TemporaryDirectory() as folder, patch.dict(globals(), WORK=Path(folder)):
        (WORK / "render.id").write_text("agt_test\n")
        with patch.object(sys.modules[__name__], "expansion_geometry", side_effect=[before, centered]), \
             patch.object(sys.modules[__name__], "browser", side_effect=["", RuntimeError("click failed")]) as drive:
            try:
                click_collapsed_work(ENTRY_COLLAPSED, ("tool-call", "reasoning"))
            except RuntimeError:
                pass
            else:
                raise AssertionError("failed real click was ignored")
            assert drive.call_args_list == [call("scrollintoview", ENTRY_COLLAPSED),
                                            call("click", ENTRY_COLLAPSED)]
        history = json.loads((WORK / "render-expand-geometry.json").read_text())
        assert history[-1]["outcome"] == "click-failed"

    with TemporaryDirectory() as folder, patch.dict(globals(), WORK=Path(folder)):
        (WORK / "render.id").write_text("agt_test\n")
        group = {**before, "kind": "tool-group", "status": None}
        centered_group = {**centered, "kind": "tool-group", "status": None}
        with patch.object(sys.modules[__name__], "expansion_geometry", side_effect=[group, centered_group]), \
             patch.object(sys.modules[__name__], "browser") as drive:
            click_collapsed_work(GROUP_COLLAPSED, ("tool-group", "work-toggle"))
            assert drive.call_args_list == [call("scrollintoview", GROUP_COLLAPSED),
                                            call("click", GROUP_COLLAPSED)]

    with patch.object(sys.modules[__name__], "evaluate", return_value=centered) as read:
        assert expansion_geometry(ENTRY_COLLAPSED) == centered
        source = read.call_args.args[0]
        assert "document.elementFromPoint" in source and \
            "pane.contains(target)" in source and json.dumps(ENTRY_COLLAPSED) in source

    with patch.object(sys.modules[__name__], "evaluate", side_effect=[1, 0, 1, 0]), \
         patch.object(sys.modules[__name__], "click_collapsed_work") as click_work:
        expand_work()
        assert click_work.call_args_list == [
            call(GROUP_COLLAPSED, ("tool-group", "work-toggle")),
            call(ENTRY_COLLAPSED, ("tool-call", "reasoning")),
        ]


RENDER_DOM = r"""(() => { const c=document.querySelector('[data-testid=chat-transcript]');
  const a=[...(c?.querySelectorAll('li[data-kind=agent]')||[])].at(-1);
  const table=a?.querySelector('table');
  return {answer:a?.querySelector('[data-testid=chat-markdown]')?.textContent||'',
    table:table?[...table.querySelectorAll('tr')].map(r=>[...r.querySelectorAll('th,td')].map(x=>x.textContent.trim())):[],
    code:[...(a?.querySelectorAll('[data-testid=chat-codeblock]')||[])].map(x=>({language:x.dataset.language,
      text:x.querySelector('code')?.textContent||'', tokenSpans:x.querySelectorAll('code span').length})),
    tools:[...(c?.querySelectorAll('[data-testid=tool-call]')||[])].map(x=>({status:x.dataset.status,
      failed:!!x.querySelector('[data-icon=failed]'), expanded:x.querySelector('[role=button]')?.getAttribute('aria-expanded'),
      pres:[...x.querySelectorAll('pre')].map(y=>y.textContent), text:x.textContent})),
    reasoning:[...(c?.querySelectorAll('[data-testid=reasoning]')||[])].map(x=>x.textContent),
    groups:c?.querySelectorAll('[data-testid=tool-group], [data-testid=work-toggle]').length||0}; })()"""


def render_check():
    current("render")
    delivered, end, between = turn_events("render", "VISUAL_RENDER")
    answer = "\n".join(e["payload"].get("text", "") for e in between
                       if e["kind"] == "item.completed" and e["payload"].get("itemKind") == "message")
    assert answer and f"VISUAL_END_{RUN}" in answer, "saved final answer missing the requested end marker"
    assert "npm test" in answer and "node --test" in answer and "README.md" in answer and "package.json" in answer, answer
    tools = [e for e in between if e["kind"] == "item.completed" and e["payload"].get("itemKind") == "tool"]
    successes = [e for e in tools if not (e["payload"].get("tool") or {}).get("failed")]
    failures = [e for e in tools if (e["payload"].get("tool") or {}).get("failed")]
    assert len(tools) >= 3 and len(successes) >= 2 and failures, "provider did not make two successful reads and one genuine failed read"
    readme_line = "- `npm test` runs the tests."
    package_line = '"test": "node --test"'
    assert any("README.md" in (e["payload"].get("tool") or {}).get("input", "") and
               readme_line in (e["payload"].get("tool") or {}).get("output", "") for e in successes), "README answer lacks real tool source"
    assert any("package.json" in (e["payload"].get("tool") or {}).get("input", "") and
               package_line in (e["payload"].get("tool") or {}).get("output", "") for e in successes), "code answer lacks real package source"
    assert any("visual-missing-" in (e["payload"].get("tool") or {}).get("input", "") for e in failures), "requested missing-file read did not fail"
    assert max(e["seq"] for e in tools) < max(e["seq"] for e in between if e["kind"] == "item.completed" and e["payload"].get("itemKind") == "message"), "tool did not precede final answer"
    live = json.loads((WORK / "render-live.json").read_text())
    from datetime import datetime
    observed_at = datetime.fromisoformat(live["at"].replace("Z", "+00:00"))
    later = [e for e in tools if datetime.fromisoformat(e["created_at"].replace("Z", "+00:00")) >= observed_at]
    assert later, "no saved tool completed after the observed running UI row"
    named = [name for name in ("README.md", "package.json", f"visual-missing-{RUN}.txt") if name in live["label"]]
    if named:
        assert any(any(name in (e["payload"].get("tool") or {}).get("input", "") for name in named) for e in later), "observed running tool never completed as a saved item"
    assert any("README.md" in (e["payload"].get("tool") or {}).get("input", "") for e in tools), "saved completed tool did not match repo read"
    expand_work()
    dom = evaluate(RENDER_DOM)
    assert dom["groups"] >= 1 and len(dom["tools"]) >= 3, "grouped real tool rows missing from Chat"
    assert dom["table"] and any("README.md" in row and "npm test" in " ".join(row) for row in dom["table"]), dom["table"]
    assert any(c["language"] == "json" and package_line in c["text"] and c["tokenSpans"] > 0 for c in dom["code"]), dom["code"]
    assert "npm test" in dom["answer"] and f"VISUAL_END_{RUN}" in dom["answer"], "rendered answer differs from saved facts"
    for saved in tools:
        t = saved["payload"].get("tool") or {}
        raw_input, output = (t.get("input") or "").strip(), (t.get("output") or "").strip()
        try:
            expected_input = json.dumps(json.loads(raw_input), indent=2, ensure_ascii=False) if raw_input.startswith(("{", "[")) else raw_input
        except json.JSONDecodeError:
            expected_input = raw_input
        expected_status = "failed" if t.get("failed") else "completed"
        matches = [d for d in dom["tools"] if d["status"] == expected_status and d["failed"] == bool(t.get("failed")) and
                   (not expected_input or expected_input in d["pres"]) and (not output or output in d["pres"])]
        assert matches, f"saved tool input/output/error missing from an expanded completed UI row: {t.get('name')}"
    assert not evaluate("!!document.querySelector('[data-testid=tool-live], [data-testid=tool-call][data-status=running]')"), "ghost running tool after completion"
    write("render-oracle.json", {"delivered": delivered, "turn_completed": end, "saved_answer": answer, "saved_tools": tools, "dom": dom})


def reasoning_receipts(between):
    saved = [{"event_id": e.get("event_id"), "text": e["payload"].get("text", "")}
             for e in between if e["kind"] == "item.completed" and e["payload"].get("itemKind") == "reasoning"]
    assert saved and all(item["event_id"] and isinstance(item["text"], str) and item["text"].strip()
                         for item in saved), "missing saved reasoning item/content"
    assert len({item["event_id"] for item in saved}) == len(saved), "duplicate reasoning EventID"
    return saved


def reasoning_receipt_diagnostic(items):
    result = []
    for event in items:
        text = event["payload"].get("text")
        result.append({"event_id": event.get("event_id"),
                       "text_length": len(text) if isinstance(text, str) else None,
                       "text_type": type(text).__name__})
    return {"items": result}


def reasoning_preview(text):
    # WorkRows uses timelineRows.firstLine: strip, first line, plain-text marks, 120-character cap.
    line = re.sub(r"^#{1,6}\s+", "", text.strip().split("\n")[0])
    while True:
        plain = re.sub(r"(\*\*|__|\*|_|`)(.+?)\1", r"\2", line)
        if plain == line:
            break
        line = plain
    line = line.strip()
    return line if len(line) <= 120 else line[:119] + "…"


REASONING_DOM = r"""(() => [...document.querySelectorAll('[data-testid=chat-transcript] [data-testid=reasoning]')]
  .map(x => { const toggle=x.querySelector('[role=button]');
    return {heading:x.querySelector('[class*=heading]')?.textContent||'',
      preview:x.querySelector('[class*=preview]')?.textContent||'',
      expanded:toggle?.getAttribute('aria-expanded')??null,
      status:x.dataset.status, body:x.querySelector('pre')?.textContent??null}; }))()"""


def assert_reasoning_rows(saved, rows, expanded):
    assert len(rows) == len(saved), "Thinking row count differs from saved reasoning items"
    for item, row in zip(saved, rows):
        assert row["heading"] == "Thinking" and row["status"] == "completed", row
        assert row["preview"] == reasoning_preview(item["text"]) and len(row["preview"]) <= 120, \
            f"Thinking preview differs from saved first line: {item['event_id']}"
        assert row["expanded"] == ("true" if expanded else "false"), row
        assert row["body"] == (item["text"] if expanded else None), \
            f"Thinking full text differs from saved item: {item['event_id']}"


def assert_reasoning_saved_after_reload(before, after):
    assert after == before, "reload changed reasoning EventID, order, or saved content"


def save_reasoning_capture(stage, snapshot):
    capture = WORK / f"render-reasoning-{stage}-capture.json"
    seal = WORK / f"render-reasoning-{stage}-seal.json"
    assert all(not p.exists() and not p.is_symlink() for p in (capture, seal)), \
        "reasoning checkpoint already captured"
    write(capture.name, snapshot)
    write(seal.name, {"sha256": motion_digest(snapshot), "stage": stage, "case": "render",
                      "ws": WS, "run": RUN, "agent_id": snapshot["agent_id"]})


def load_reasoning_capture(stage):
    assert stage in ("before-reload", "reloaded"), stage
    capture = WORK / f"render-reasoning-{stage}-capture.json"
    seal = WORK / f"render-reasoning-{stage}-seal.json"
    assert all(p.is_file() and not p.is_symlink() for p in (capture, seal)), "reasoning capture or seal missing"
    try:
        snapshot = json.loads(capture.read_text())
        receipt = json.loads(seal.read_text())
    except (ValueError, OSError) as exc:
        raise AssertionError("reasoning capture is unreadable") from exc
    assert isinstance(snapshot, dict) and isinstance(receipt, dict), "reasoning capture has invalid shape"
    expected = {"sha256": motion_digest(snapshot), "stage": stage, "case": "render",
                "ws": WS, "run": RUN, "agent_id": agent_id("render")}
    assert receipt == expected, "reasoning capture changed or belongs to a foreign run"
    assert all(snapshot.get(key) == value for key, value in expected.items() if key != "sha256"), \
        "reasoning capture identity changed"
    assert snapshot.get("version") == 1 and snapshot.get("validation") == "pending", \
        "reasoning capture version or pending state changed"
    assert snapshot.get("route") == f"/ws/{WS}/chat/{agent_id('render')}", "reasoning capture changed Chat route"
    for key in ("delivered", "turn_completed", "items", "collapsed", "expanded"):
        assert key in snapshot, f"reasoning capture lacks {key}"
    return snapshot


def reasoning_capture(stage):
    assert stage in ("before-reload", "reloaded"), stage
    current("render")
    delivered, end, between = turn_events("render", "VISUAL_RENDER")
    reasoning_items = [e for e in between if e["kind"] == "item.completed" and
                       e["payload"].get("itemKind") == "reasoning"]
    write(f"render-reasoning-{stage}-receipts.json", reasoning_receipt_diagnostic(reasoning_items))
    # Reveal grouped rows without opening the Thinking bodies yet.
    for _ in range(20):
        if not evaluate(f"document.querySelectorAll({json.dumps(GROUP_COLLAPSED)}).length"):
            break
        click_collapsed_work(GROUP_COLLAPSED, ("tool-group", "work-toggle"))
    else:
        raise AssertionError("too many collapsed work groups")
    collapsed = evaluate(REASONING_DOM)
    shot("render", f"thinking-{stage}-preview")
    expand_work()
    expanded = evaluate(REASONING_DOM)
    shot("render", f"thinking-{stage}-expanded")
    current("render")
    final_delivered, final_end, final_between = turn_events("render", "VISUAL_RENDER")
    final_items = [e for e in final_between if e["kind"] == "item.completed" and
                   e["payload"].get("itemKind") == "reasoning"]
    assert (final_delivered, final_end, final_items) == (delivered, end, reasoning_items), \
        "saved reasoning turn changed during UI capture"
    snapshot = {"version": 1, "validation": "pending", "stage": stage, "case": "render",
                "ws": WS, "run": RUN, "agent_id": agent_id("render"),
                "route": f"/ws/{WS}/chat/{agent_id('render')}", "delivered": delivered,
                "turn_completed": end, "items": reasoning_items,
                "collapsed": collapsed, "expanded": expanded}
    save_reasoning_capture(stage, snapshot)
    write(f"render-reasoning-{stage}-diagnostic.json", {
        "validation": "pending", "capture_sha256": motion_digest(snapshot),
        "delivered_event_id": delivered["event_id"], "turn_event_id": end["event_id"],
        **reasoning_receipt_diagnostic(reasoning_items)})


def assert_reasoning_captures(before, after):
    for stage, capture in (("before-reload", before), ("reloaded", after)):
        delivered, end, items = capture["delivered"], capture["turn_completed"], capture["items"]
        assert delivered["kind"] == "message.delivered" and \
            f"VISUAL_RENDER_{RUN}" in delivered["payload"]["text"], f"{stage}: foreign rendering delivery"
        assert end["kind"] == "agent.turn_completed" and end["seq"] > delivered["seq"], \
            f"{stage}: turn completion does not follow delivery"
        assert isinstance(items, list), f"{stage}: reasoning items missing"
        assert all(e["kind"] == "item.completed" and e["payload"].get("itemKind") == "reasoning" and
                   delivered["seq"] < e["seq"] < end["seq"] for e in items), \
            f"{stage}: reasoning item outside saved turn"
        seqs = [e["seq"] for e in items]
        assert seqs == sorted(set(seqs)), f"{stage}: duplicate or unordered reasoning sequence"
    assert_reasoning_saved_after_reload(before["delivered"], after["delivered"])
    assert_reasoning_saved_after_reload(before["turn_completed"], after["turn_completed"])
    assert_reasoning_saved_after_reload(before["items"], after["items"])
    if not before["items"]:
        write("reasoning-blocked.json", {"status": "blocked", "prerequisite":
              "actual selected provider must emit saved item.completed reasoning text"})
        raise AssertionError("BLOCKED: selected real provider emitted no saved reasoning item; Thinking UI cannot be claimed")
    saved = reasoning_receipts(before["items"])
    assert_reasoning_saved_after_reload(saved, reasoning_receipts(after["items"]))
    for capture in (before, after):
        assert_reasoning_rows(saved, capture["collapsed"], False)
        assert_reasoning_rows(saved, capture["expanded"], True)
    return saved


def reasoning_assert():
    before = load_reasoning_capture("before-reload")
    after = load_reasoning_capture("reloaded")
    motion = load_motion_capture("render")
    assert (before["delivered"], before["turn_completed"]) == \
        (motion["delivered"], motion["turn_completed"]), \
        "frozen reasoning and motion captures belong to different saved turns"
    saved = assert_reasoning_captures(before, after)
    write("render-reasoning.json", {"validation": "passed", "saved": saved,
          "before_sha256": motion_digest(before), "reloaded_sha256": motion_digest(after)})


def final_visual_assert():
    verdict = {}
    for name, check in (("motion", lambda: motion_assert("render")),
                        ("reasoning", reasoning_assert)):
        try:
            check()
        except Exception as exc:
            detail = str(exc)
            if name == "reasoning" and detail.lstrip().startswith(("{", "[")):
                detail = "Thinking DOM row mismatched its saved item"
            verdict[name] = {"status": "failed", "error_type": type(exc).__name__,
                             "reason": detail[:400]}
        else:
            verdict[name] = {"status": "passed"}
    write("render-final-verdict.json", verdict)
    assert all(item["status"] == "passed" for item in verdict.values()), \
        f"first-turn visual validation failed: {verdict}"


def self_test_reasoning():
    item = {"event_id": "reasoning-1", "text": "## **First** line\nFull second line"}
    event = {"event_id": item["event_id"], "kind": "item.completed",
             "payload": {"itemKind": "reasoning", "text": item["text"]}}
    assert reasoning_receipt_diagnostic([event]) == {"items": [{
        "event_id": item["event_id"], "text_length": len(item["text"]), "text_type": "str"}]}
    missing = {**event, "event_id": None, "payload": {"itemKind": "reasoning", "text": ""}}
    assert reasoning_receipt_diagnostic([missing]) == {"items": [{
        "event_id": None, "text_length": 0, "text_type": "str"}]}
    try:
        reasoning_receipts([missing])
    except AssertionError:
        pass
    else:
        raise AssertionError("missing reasoning receipt passed after diagnostic")
    assert reasoning_receipts([event]) == [item]
    assert reasoning_preview(item["text"]) == "First line"
    assert reasoning_preview("x" * 121) == "x" * 119 + "…"
    assert_reasoning_rows([item], [{"heading": "Thinking", "preview": "First line",
                                    "expanded": "false", "status": "completed", "body": None}], False)
    assert_reasoning_rows([item], [{"heading": "Thinking", "preview": "First line",
                                    "expanded": "true", "status": "completed", "body": item["text"]}], True)
    for bad in ([], [{**event, "payload": {**event["payload"], "text": ""}}],
                [{**event, "payload": {**event["payload"], "itemKind": "message"}}]):
        try:
            reasoning_receipts(bad)
        except AssertionError:
            pass
        else:
            raise AssertionError("missing reasoning item/content passed")
    for wrong in ({"preview": "Wrong"}, {"body": "Wrong"}):
        row = {"heading": "Thinking", "preview": "First line", "expanded": "true",
               "status": "completed", "body": item["text"]} | wrong
        try:
            assert_reasoning_rows([item], [row], True)
        except AssertionError:
            pass
        else:
            raise AssertionError("wrong Thinking preview/content passed")
    try:
        assert_reasoning_rows([item], [], True)
    except AssertionError:
        pass
    else:
        raise AssertionError("missing Thinking row passed")
    for changed in ({**item, "event_id": "reasoning-2"}, {**item, "text": "Changed"}):
        try:
            assert_reasoning_saved_after_reload([item], [changed])
        except AssertionError:
            pass
        else:
            raise AssertionError("wrong reload receipt passed")


def self_test_final_visual_assert():
    from copy import deepcopy
    from datetime import datetime
    from tempfile import TemporaryDirectory
    from unittest.mock import patch

    end_at = "2026-10-07T00:00:00Z"
    end_ms = int(datetime.fromisoformat(end_at.replace("Z", "+00:00")).timestamp() * 1000)
    marker = f"VISUAL_END_{RUN}"
    answer = " ".join(["word"] * 299 + [marker])
    words = answer.split()
    frames = [{"at": end_ms - 40 - (len(words) - i) * 16,
               "chars": len(" ".join(words[:i])), "words": i,
               "addedWords": 1, "marker": i == len(words),
               "text": " ".join(words[:i])}
              for i in range(1, len(words) + 1)]
    route = f"/ws/{WS}/chat/agt_test"
    bound_at = frames[0]["at"] - 17
    arrival = {"version": 1, "workspace": WS, "run": RUN,
               "bound": {"agentId": "agt_test", "route": route, "at": bound_at},
               "sources": [{"id": 1, "at": bound_at - 10, "agents": ["agt_test"]}],
               "arrivals": [{"at": frame["at"] - 1, "sourceId": 1, "agentId": "agt_test",
                             "turnId": "t1", "itemId": "m1", "text": (" " if i else "") + words[i]}
                            for i, frame in enumerate(frames)],
               "completions": [{"at": frames[-1]["at"] - 1, "sourceId": 1,
                                "agentId": "agt_test", "turnId": "t1", "kind": "item.completed",
                                "seq": 3, "eventId": "reply", "itemId": "m1"},
                               {"at": end_ms, "sourceId": 1, "agentId": "agt_test",
                                "turnId": "t1", "kind": "agent.turn_completed",
                                "seq": 4, "eventId": "completed", "itemId": None}], "error": None}
    delivered = {"kind": "message.delivered", "event_id": "delivered", "seq": 1,
                 "payload": {"text": f"VISUAL_RENDER_{RUN}"}}
    end = {"kind": "agent.turn_completed", "event_id": "completed", "seq": 4,
           "turn_id": "t1", "created_at": end_at, "payload": {}}
    text = "## **First** line\nFull second line"
    reasoning = {"kind": "item.completed", "event_id": "reasoning-1", "seq": 2,
                 "payload": {"itemKind": "reasoning", "text": text}}
    collapsed = {"heading": "Thinking", "preview": "First line", "expanded": "false",
                 "status": "completed", "body": None}
    expanded = {**collapsed, "expanded": "true", "body": text}
    motion = {"version": 1, "case": "render", "ws": WS, "run": RUN,
              "agent_id": "agt_test", "route": route,
              "probe": {"route": route, "start": frames[0]["at"] - 16, "frames": frames,
                        "ticks": [frame["at"] for frame in frames], "arrival": arrival,
                        "signals": [{"at": frame["at"], "caret": True, "working": True, "stop": True}
                                    for frame in frames] +
                                   [{"at": end_ms + 10, "caret": False, "working": False, "stop": False}],
                        "arrivalClosed": {"closed": True, "arrivalCount": len(arrival["arrivals"])},
                        "finalText": answer, "samples": [{"stop": True, "gap": 0}], "shifts": [],
                        "sawCaret": True, "sawWorking": True, "sawStop": True, "observerError": None,
                        "caretGone": True},
              "delivered": delivered, "turn_completed": end,
              "replies": [{"kind": "item.completed", "event_id": "reply", "seq": 3,
                           "turn_id": "t1", "payload": {"itemId": "m1", "itemKind": "message", "text": answer}}],
              "answer": answer}
    base = {"version": 1, "validation": "pending", "case": "render", "ws": WS,
            "run": RUN, "agent_id": "agt_test", "route": route,
            "delivered": delivered, "turn_completed": end, "items": [reasoning],
            "collapsed": [collapsed], "expanded": [expanded]}
    clockify_motion_fixture(motion)
    assert assert_motion_capture(motion)[2] == -40

    def trial(change=None, expected_motion="passed", expected_reasoning="passed", tamper=None):
        with TemporaryDirectory() as folder, patch.dict(globals(), WORK=Path(folder)):
            (WORK / "render.id").write_text("agt_test\n")
            first, after, measured = deepcopy(base), deepcopy(base), deepcopy(motion)
            first["stage"], after["stage"] = "before-reload", "reloaded"
            if change:
                change(first, after, measured)
            save_motion_capture(measured)
            save_reasoning_capture("before-reload", first)
            save_reasoning_capture("reloaded", after)
            if tamper:
                tamper()
            with patch.object(sys.modules[__name__], "evaluate", side_effect=AssertionError("late DOM read")), \
                 patch.object(sys.modules[__name__], "events", side_effect=AssertionError("late event read")), \
                 patch.object(sys.modules[__name__], "current", side_effect=AssertionError("late route read")), \
                 patch.object(sys, "argv", [__file__, "final-visual-assert"]):
                if (expected_motion, expected_reasoning) == ("passed", "passed"):
                    main()
                else:
                    try:
                        main()
                    except AssertionError as exc:
                        assert "first-turn visual validation failed" in str(exc), exc
                    else:
                        raise AssertionError("final command accepted a failed visual capture")
            verdict = json.loads((WORK / "render-final-verdict.json").read_text())
            assert (verdict["motion"]["status"], verdict["reasoning"]["status"]) == \
                (expected_motion, expected_reasoning), verdict
            assert (WORK / "render-motion.json").exists() == (expected_motion == "passed")
            assert (WORK / "render-reasoning.json").exists() == (expected_reasoning == "passed")

    trial()
    trial(lambda a, b, m: a["items"].clear(), expected_reasoning="failed")
    trial(lambda a, b, m: a["items"][0]["payload"].update(text=None), expected_reasoning="failed")
    trial(lambda a, b, m: a["items"][0]["payload"].update(text=""), expected_reasoning="failed")
    trial(lambda a, b, m: a["items"][0]["payload"].pop("text"), expected_reasoning="failed")
    trial(lambda a, b, m: a["items"].append(deepcopy(a["items"][0])), expected_reasoning="failed")
    trial(lambda a, b, m: a["collapsed"][0].update(preview="Wrong"), expected_reasoning="failed")
    trial(lambda a, b, m: a["expanded"][0].update(body="Wrong"), expected_reasoning="failed")
    trial(lambda a, b, m: b["items"][0]["payload"].update(text="Changed"), expected_reasoning="failed")
    trial(lambda a, b, m: b["delivered"].update(event_id="other-turn"), expected_reasoning="failed")
    trial(lambda a, b, m: m["probe"]["frames"][0].update(addedWords=17), expected_motion="failed")
    trial(lambda a, b, m: (a["items"][0]["payload"].update(text=None),
                           m["probe"]["shifts"].append({"value": 0.012443148334330491})),
          expected_motion="failed", expected_reasoning="failed")
    trial(expected_reasoning="failed", tamper=lambda: (WORK / "render-reasoning-reloaded-seal.json").unlink())
    trial(expected_reasoning="failed", tamper=lambda: write("render-reasoning-reloaded-seal.json", {
        "sha256": "foreign", "stage": "reloaded", "case": "render", "ws": WS,
        "run": "FOREIGN", "agent_id": "agt_test"}))

    with TemporaryDirectory() as folder, patch.dict(globals(), WORK=Path(folder)):
        (WORK / "render.id").write_text("agt_test\n")
        first = {**deepcopy(base), "stage": "before-reload"}
        save_reasoning_capture("before-reload", first)
        try:
            save_reasoning_capture("before-reload", first)
        except AssertionError as exc:
            assert "already captured" in str(exc), exc
        else:
            raise AssertionError("duplicate reasoning capture passed")
        foreign = {**first, "run": "FOREIGN"}
        write("render-reasoning-before-reload-capture.json", foreign)
        write("render-reasoning-before-reload-seal.json", {
            "sha256": motion_digest(foreign), "stage": "before-reload", "case": "render",
            "ws": WS, "run": "FOREIGN", "agent_id": "agt_test"})
        try:
            load_reasoning_capture("before-reload")
        except AssertionError as exc:
            assert "foreign run" in str(exc), exc
        else:
            raise AssertionError("foreign reasoning seal passed")
        write("render-reasoning-before-reload-capture.json", first)
        try:
            load_reasoning_capture("before-reload")
        except AssertionError as exc:
            assert "changed or belongs" in str(exc), exc
        else:
            raise AssertionError("mutated reasoning capture passed")


def clipboard_primary_pids(profile, processes):
    profile_flag = f"--user-data-dir={profile}"
    primary = []
    for line in processes:
        match = re.match(r"\s*([0-9]+)\s+(.*)", line)
        if not match:
            continue
        command = match[2]
        if re.search(rf"(?:^|\s){re.escape(profile_flag)}(?:\s|$)", command) and \
           re.search(r"(?:^|\s)--remote-debugging-port=[0-9]+(?:\s|$)", command) and \
           not re.search(r"(?:^|\s)--type=", command):
            primary.append(int(match[1]))
    return primary


def clipboard_owned_config(manifest, env, origin, route, endpoint, devtools_lines, processes, wrapper, case="render"):
    evidence = Path(env["AFT_WORK_DIR"])
    run_root = evidence.parent
    session = env["AFT_SESSION"]
    profile_root = Path(env["AFT_BROWSER_PROFILES"])
    profile = profile_root / session
    tests_dir = Path(env["AFT_TESTS_DIR"])
    owned = manifest["owned"]
    if not re.fullmatch(r"aft-live-chat-visual-[0-9]+", session) or \
       not re.fullmatch(r"/private/tmp/aft-agent-flows\.[A-Za-z0-9]{8}", str(run_root)) or \
       evidence != run_root / "evidence" or evidence.resolve() != evidence or \
       profile_root != run_root / "profiles" or profile_root.resolve() != profile_root or \
       not profile.is_dir() or profile.is_symlink() or profile.resolve() != profile or \
       manifest["run_id"] != RUN or manifest["selection"]["batch"] != "chat-visual" or \
       owned["evidence_dir"] != str(evidence) or owned["ui_url"] != env["AFT_BASE_URL"] or \
       owned["browser_socket_dir"] != env["AGENT_BROWSER_SOCKET_DIR"] or \
       env["AFT_BROWSER_SOCKET_RECEIPT"] != str(evidence / "browser-socket.json") or \
       manifest["browser_binary"] != env["AFT_BROWSER_BIN"] or \
       tests_dir != Path(manifest["source_root"]) / "tests/aft" or \
       wrapper != run_root / "bin/agent-browser" or \
       wrapper.resolve() != tests_dir / "scripts/agent-flows-browser":
        raise PermissionError("foreign clipboard runner identity")
    if origin != env["AFT_BASE_URL"] or not re.fullmatch(r"http://127\.0\.0\.1:[0-9]+", origin) or \
       route != f"/ws/{WS}/chat/{agent_id(case)}":
        raise PermissionError("foreign clipboard Chat origin or route")
    url = urllib.parse.urlsplit(endpoint)
    if url.scheme != "ws" or url.hostname != "127.0.0.1" or not url.port or \
       not re.fullmatch(r"/devtools/browser/[A-Za-z0-9-]+", url.path) or \
       url.username or url.password or url.query or url.fragment or \
       devtools_lines != [str(url.port), url.path]:
        raise PermissionError("foreign clipboard CDP endpoint")
    primary = clipboard_primary_pids(profile, processes)
    if len(primary) != 1:
        raise PermissionError("ambiguous owned Chrome primary process")
    return {"endpoint": endpoint, "profile": str(profile), "origin": origin,
            "route": route, "pid": primary[0]}


def clipboard_preflight(case="render"):
    env = {name: required(name) for name in
           ("AFT_WORK_DIR", "AFT_SESSION", "AFT_BROWSER_PROFILES", "AFT_TESTS_DIR",
            "AFT_BASE_URL", "AGENT_BROWSER_SOCKET_DIR", "AFT_BROWSER_SOCKET_RECEIPT",
            "AFT_BROWSER_BIN")}
    receipt = Path(env["AFT_BROWSER_SOCKET_RECEIPT"])
    if receipt != Path(env["AFT_WORK_DIR"]) / "browser-socket.json":
        raise PermissionError("foreign browser socket receipt")
    subprocess.run(["node", str(Path(env["AFT_TESTS_DIR"]) / "scripts/agent-flows-browser-socket.mjs"),
                    "verify", str(receipt), env["AGENT_BROWSER_SOCKET_DIR"]],
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=True, timeout=5)
    manifest_file = Path(env["AFT_WORK_DIR"]) / "manifest.json"
    if manifest_file.is_symlink():
        raise PermissionError("foreign clipboard run manifest")
    manifest = json.loads(manifest_file.read_text())
    source_head = subprocess.check_output(["git", "-C", manifest["source_root"], "rev-parse", "HEAD"],
                                          text=True, timeout=5).strip()
    if source_head != manifest["source_head"]:
        raise PermissionError("clipboard source differs from owned run manifest")
    processes = subprocess.check_output(["ps", "-axo", "pid=,command="], text=True, timeout=5).splitlines()
    profile = Path(env["AFT_BROWSER_PROFILES"]) / env["AFT_SESSION"]
    port_file = profile / "DevToolsActivePort"
    if not profile.is_dir() or profile.is_symlink() or port_file.is_symlink():
        raise PermissionError("foreign clipboard profile")
    if len(clipboard_primary_pids(profile, processes)) != 1:
        raise PermissionError("ambiguous owned Chrome primary process")
    devtools_lines = port_file.read_text().splitlines()
    wrapper = Path(shutil.which("agent-browser") or "")
    if wrapper != Path(env["AFT_WORK_DIR"]).parent / "bin/agent-browser" or \
       wrapper.resolve() != Path(env["AFT_TESTS_DIR"]) / "scripts/agent-flows-browser":
        raise PermissionError("foreign clipboard browser wrapper")
    origin = evaluate("location.origin")
    route = evaluate("location.pathname")
    endpoint = browser("get", "cdp-url")
    return clipboard_owned_config(manifest, env, origin, route, endpoint,
                                  devtools_lines, processes, wrapper, case)


def clipboard_holder_ack(process):
    with selectors.DefaultSelector() as selector:
        selector.register(process.stdout, selectors.EVENT_READ)
        if not selector.select(timeout=10):
            raise PermissionError("clipboard permission holder timed out")
        line = process.stdout.readline()
    if not line:
        raise PermissionError("clipboard permission holder exited before grant")
    return json.loads(line)


def close_clipboard_holder(process):
    if process is None:
        return None, None
    if process.poll() is None:
        try:
            process.stdin.write("close\n")
            process.stdin.flush()
        except (BrokenPipeError, OSError):
            pass
    try:
        process.stdin.close()
    except (BrokenPipeError, OSError):
        pass
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        try:
            process.terminate()
        except ProcessLookupError:
            pass
        try:
            process.wait(timeout=2)
        except subprocess.TimeoutExpired:
            try:
                process.kill()
            except ProcessLookupError:
                pass
            process.wait(timeout=2)
    reason = None
    for line in process.stderr.read(512).splitlines()[:1]:
        try:
            candidate = json.loads(line).get("reason")
            if re.fullmatch(r"[a-z-]+", candidate or ""):
                reason = candidate
        except json.JSONDecodeError:
            pass
    process.stdout.close()
    process.stderr.close()
    return process.returncode, reason


def with_owned_clipboard_read(kind, verify):
    process = None
    stage = "preflight"
    failure_type = None
    config = None
    receipt = None
    try:
        config = clipboard_preflight()
        stage = "grant"
        script = Path(required("AFT_TESTS_DIR")) / "scripts/coverage-chat-visual-clipboard.mjs"
        process = subprocess.Popen(["node", str(script), "hold"], stdin=subprocess.PIPE,
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        process.stdin.write(json.dumps(config) + "\n")
        process.stdin.flush()
        receipt = clipboard_holder_ack(process)
        if not isinstance(receipt, dict) or receipt != {"status": "granted", "targetId": receipt.get("targetId"),
                       "context": "owned-default", "origin": config["origin"],
                       "route": config["route"]} or not receipt["targetId"]:
            raise PermissionError("foreign clipboard permission receipt")
        stage = "readback"
        result = verify()
        stage = "verified"
        return result
    except Exception as exc:
        failure_type = type(exc).__name__
        raise
    finally:
        exit_code, holder_reason = close_clipboard_holder(process)
        write(f"clipboard-{kind}-permission.json", {"status": "closed" if exit_code == 0 and
              stage == "verified" and failure_type is None else "blocked",
              "run": RUN, "case": "render", "agent_id": agent_id("render"), "kind": kind,
              "stage": stage, "failure_type": failure_type, "holder_exit": exit_code,
              "holder_reason": holder_reason, "owned_origin": config["origin"] if config else None,
              "owned_profile": config is not None,
              "target_id": receipt.get("targetId") if isinstance(receipt, dict) else None})
        if process is not None and exit_code != 0:
            raise PermissionError("clipboard permission holder did not close cleanly")


def self_test_clipboard_ownership():
    from contextlib import contextmanager
    from copy import deepcopy
    from unittest.mock import patch

    @contextmanager
    def runner_shaped_root():
        # Match run-aft-agent-flows.sh's mktemp template: Python's default
        # TemporaryDirectory alphabet can include '_' rejected by the real guard.
        folder = subprocess.check_output(
            ["mktemp", "-d", "/private/tmp/aft-agent-flows.XXXXXXXX"], text=True
        ).strip()
        root = Path(folder)
        owned = bool(re.fullmatch(r"/private/tmp/aft-agent-flows\.[A-Za-z0-9]{8}", folder))
        try:
            assert owned and root.is_dir() and not root.is_symlink(), "test root differs from real runner shape"
            yield folder
        finally:
            if owned and root.is_dir() and not root.is_symlink():
                shutil.rmtree(root)

    with runner_shaped_root() as folder:
        root = Path(folder)
        evidence = root / "evidence"
        evidence.mkdir()
        session = "aft-live-chat-visual-123"
        profile_root = root / "profiles"
        profile = profile_root / session
        profile.mkdir(parents=True)
        source = root / "source"
        tests = source / "tests/aft"
        script = tests / "scripts/agent-flows-browser"
        script.parent.mkdir(parents=True)
        script.write_text("owned wrapper\n")
        wrapper = root / "bin/agent-browser"
        wrapper.parent.mkdir()
        wrapper.symlink_to(script)
        origin = "http://127.0.0.1:1234"
        route = "/ws/OFFLINE/chat/agt_owned"
        endpoint = "ws://127.0.0.1:1235/devtools/browser/owned"
        lines = ["1235", "/devtools/browser/owned"]
        processes = [f"123 /Applications/Chrome --remote-debugging-port=0 --user-data-dir={profile}"]
        env = {"AFT_WORK_DIR": str(evidence), "AFT_SESSION": session,
               "AFT_BROWSER_PROFILES": str(profile_root), "AFT_TESTS_DIR": str(tests),
               "AFT_BASE_URL": origin, "AGENT_BROWSER_SOCKET_DIR": str(root / "socket"),
               "AFT_BROWSER_SOCKET_RECEIPT": str(evidence / "browser-socket.json"),
               "AFT_BROWSER_BIN": "/owned/bin/agent-browser"}
        manifest = {"run_id": RUN, "selection": {"batch": "chat-visual"},
                    "source_root": str(source), "browser_binary": env["AFT_BROWSER_BIN"],
                    "owned": {"evidence_dir": str(evidence), "ui_url": origin,
                              "browser_socket_dir": env["AGENT_BROWSER_SOCKET_DIR"]}}
        with patch.dict(globals(), agent_id=lambda case: "agt_owned"):
            def check(m=manifest, e=env, o=origin, r=route, u=endpoint,
                      p=lines, ps=processes, w=wrapper):
                return clipboard_owned_config(m, e, o, r, u, p, ps, w)

            assert check() == {"endpoint": endpoint, "profile": str(profile), "origin": origin,
                               "route": route, "pid": 123}
            def rejects(**changes):
                try:
                    check(**changes)
                except PermissionError:
                    return
                raise AssertionError("foreign clipboard ownership passed")

            rejects(e={**env, "AFT_BROWSER_PROFILES": str(root / "foreign-profiles")})
            rejects(ps=[])
            rejects(ps=processes + [processes[0].replace("123 ", "456 ")])
            rejects(o="http://127.0.0.1:9999")
            rejects(r="/ws/OFFLINE/chat/agt_foreign")
            rejects(u="ws://127.0.0.1:9999/devtools/browser/owned")
            rejects(w=root / "bin/foreign")
            changed = deepcopy(manifest)
            changed["run_id"] = "foreign"
            rejects(m=changed)
            with patch.dict(globals(), agent_id=lambda case: "agt_input" if case == "input" else "agt_owned"):
                input_route = "/ws/OFFLINE/chat/agt_input"
                assert clipboard_owned_config(manifest, env, origin, input_route, endpoint,
                                              lines, processes, wrapper, "input")["route"] == input_route
                try:
                    clipboard_owned_config(manifest, env, origin, route, endpoint,
                                           lines, processes, wrapper, "input")
                except PermissionError:
                    pass
                else:
                    raise AssertionError("foreign locale Agent passed owned browser preflight")


def self_test_clipboard_holder():
    from tempfile import TemporaryDirectory
    from unittest.mock import patch

    config = {"endpoint": "ws://127.0.0.1:1235/devtools/browser/owned",
              "profile": "/private/tmp/owned", "origin": "http://127.0.0.1:1234",
              "route": "/ws/OFFLINE/chat/agt_owned", "pid": 123}

    class Process:
        def __init__(self):
            from io import StringIO
            self.stdin = StringIO()

    def exercise(preflight=None, grant=None, verify=None, close=(0, None), expected="closed"):
        with TemporaryDirectory() as folder:
            process = Process()
            closed = []
            def stop(actual):
                if actual is not None:
                    closed.append(actual)
                    return close
                return None, None
            def approval(_actual):
                if isinstance(grant, Exception):
                    raise grant
                return grant or {"status": "granted", "targetId": "target_1",
                                 "context": "owned-default", "origin": config["origin"],
                                 "route": config["route"]}
            with patch.dict(globals(), {"WORK": Path(folder), "agent_id": lambda case: "agt_owned",
                                        "clipboard_preflight": lambda: config if preflight is None else (_ for _ in ()).throw(preflight),
                                        "clipboard_holder_ack": approval,
                                        "close_clipboard_holder": stop}), \
                 patch("subprocess.Popen", return_value=process):
                try:
                    result = with_owned_clipboard_read("code", verify or (lambda: "copied"))
                except Exception:
                    assert expected == "blocked"
                else:
                    assert expected == "closed" and result == "copied"
                receipt = json.loads((Path(folder) / "clipboard-code-permission.json").read_text())
                assert receipt["status"] == expected and receipt["kind"] == "code"
                assert receipt["run"] == RUN and receipt["agent_id"] == "agt_owned"
                assert receipt["owned_origin"] in (None, config["origin"])
                assert "endpoint" not in receipt and "profile" not in (receipt.get("owned_origin") or "")
                assert closed == ([] if preflight else [process]), "owned holder cleanup was skipped"
                return receipt

    exercise()
    assert exercise(grant=PermissionError("grant refused"), expected="blocked")["stage"] == "grant"
    assert exercise(grant={"status": "granted", "targetId": "target_1", "context": "foreign",
                           "origin": config["origin"], "route": config["route"]},
                    expected="blocked")["stage"] == "grant"
    def failed_read():
        raise subprocess.CalledProcessError(1, ["agent-browser", "eval"])
    assert exercise(verify=failed_read, expected="blocked")["stage"] == "readback"
    assert exercise(close=(1, "holder-close-timeout"), expected="blocked")["holder_exit"] == 1
    assert exercise(preflight=PermissionError("foreign profile"), expected="blocked")["owned_profile"] is False


def self_test_close_clipboard_holder():
    from io import StringIO

    class Process:
        def __init__(self, timeouts):
            self.stdin = StringIO()
            self.stdout = StringIO()
            self.stderr = StringIO('{"reason":"holder-timeout"}\n')
            self.timeouts = timeouts
            self.calls = []
            self.returncode = None

        def poll(self):
            return self.returncode

        def wait(self, timeout):
            self.calls.append(("wait", timeout))
            if self.timeouts:
                self.timeouts -= 1
                raise subprocess.TimeoutExpired("owned holder", timeout)
            self.returncode = 0
            return 0

        def terminate(self):
            self.calls.append(("terminate",))

        def kill(self):
            self.calls.append(("kill",))

    for timeouts, expected in ((0, []), (1, [("terminate",)]),
                               (2, [("terminate",), ("kill",)])):
        process = Process(timeouts)
        assert close_clipboard_holder(process) == (0, "holder-timeout")
        assert process.calls[0] == ("wait", 5)
        assert [call for call in process.calls if call[0] != "wait"] == expected
        assert process.stdin.closed and process.stdout.closed and process.stderr.closed


def self_test_clipboard_oracle():
    from tempfile import TemporaryDirectory
    from unittest.mock import patch

    saved = {"kind": "item.completed", "payload": {"itemKind": "message", "text": "saved answer"}}

    def exercise(readback, feedback, should_pass):
        with TemporaryDirectory() as folder:
            def observe(script):
                if script == RENDER_DOM:
                    return {"code": [], "table": []}
                if script == "navigator.clipboard.readText()":
                    return readback
                return feedback
            with patch.dict(globals(), {"WORK": Path(folder), "current": lambda case: None,
                                        "evaluate": observe, "turn_events": lambda case, marker: (None, None, [saved]),
                                        "with_owned_clipboard_read": lambda kind, verify: verify()}):
                try:
                    clipboard_check("message")
                except AssertionError:
                    assert not should_pass
                else:
                    assert should_pass, "wrong copied bytes or feedback passed"
                receipt = Path(folder) / "clipboard-message.json"
                assert receipt.exists() == should_pass
                if should_pass:
                    assert json.loads(receipt.read_text()) == {"expected": "saved answer",
                                                             "actual": "saved answer", "feedback": True}

    exercise("saved answer", True, True)
    exercise("changed answer", True, False)
    exercise("saved answer", False, False)


def clipboard_check(kind):
    current("render")
    dom = evaluate(RENDER_DOM)
    _, _, between = turn_events("render", "VISUAL_RENDER")
    replies = [e["payload"].get("text", "") for e in between
               if e["kind"] == "item.completed" and e["payload"].get("itemKind") == "message"]
    assert replies, "no saved assistant message to copy"
    if kind == "code":
        expected = next(c["text"] for c in dom["code"] if c["language"] == "json")
    elif kind == "table":
        rows = dom["table"]
        assert rows, "no rendered table to copy"
        expected = "\n".join("| " + " | ".join(c.replace("|", "\\|") for c in row) + " |" for row in rows[:1])
        expected += "\n| " + " | ".join("---" for _ in rows[0]) + " |"
        expected += "".join("\n| " + " | ".join(c.replace("|", "\\|") for c in row) + " |" for row in rows[1:])
    elif kind == "message":
        expected = replies[-1]
    else:
        raise ValueError(kind)
    def verify_copy():
        actual = evaluate("navigator.clipboard.readText()")
        assert actual == expected, f"{kind} copy bytes differ from actual rendered/saved source"
        copied = evaluate("!!document.querySelector('[data-testid=chat-transcript] button[aria-label=Copied]') || !![...document.querySelectorAll('[data-testid=chat-transcript] button')].find(b=>b.textContent==='Copied')")
        assert copied, f"{kind} copy showed no success feedback"
        return actual, copied

    try:
        actual, copied = with_owned_clipboard_read(kind, verify_copy)
    except (PermissionError, subprocess.CalledProcessError, json.JSONDecodeError) as exc:
        write(f"clipboard-{kind}-blocked.json", {"status": "blocked",
              "prerequisite": "owned browser clipboard-read permission", "failure_type": type(exc).__name__})
        raise AssertionError("BLOCKED: owned browser clipboard readback unavailable") from exc
    write(f"clipboard-{kind}.json", {"expected": expected, "actual": actual, "feedback": copied})


def copy_message():
    current("render")
    browser("find", "last", "[data-testid=chat-transcript] li[data-kind=agent] [data-testid=message-actions] button[aria-label='Copy message']", "click")


def assert_live_observation(live, path):
    assert isinstance(live, dict), "no running-tool observation was captured by the real DOM wait"
    assert live.get("path") == path and live.get("source") in ("tool-live", "tool-call"), live
    assert live.get("label") and live.get("live") is True and live.get("working") is True and \
        live.get("running") is True, live
    assert "Working" in live.get("workingText", "") and \
        live.get("stopTitle") == "Stop the running turn", live
    from datetime import datetime
    datetime.fromisoformat(live["at"].replace("Z", "+00:00"))


def self_test_live_observation():
    good = {"path": "/ws/offline/chat/agt_owned", "source": "tool-live", "label": "Read package.json",
            "live": True, "working": True, "running": True, "workingText": "Working for 2s",
            "stopTitle": "Stop the running turn", "at": "2026-10-06T22:08:20Z"}
    assert_live_observation(good, good["path"])
    for wrong in (None, {**good, "source": "tool-group"}, {**good, "label": ""},
                  {**good, "working": False}, {**good, "workingText": ""},
                  {**good, "running": False}, {**good, "stopTitle": ""},
                  {**good, "path": "/ws/other/chat/agt_foreign"}):
        try:
            assert_live_observation(wrong, good["path"])
        except AssertionError:
            pass
        else:
            raise AssertionError(f"invalid live observation passed: {wrong}")


def live_check():
    current("render")
    evs = events("render")
    delivered = [e for e in evs if e["kind"] == "message.delivered" and "VISUAL_RENDER" in e["payload"].get("text", "")]
    assert len(delivered) == 1
    live = evaluate("window.__aftChatVisualLive??null")
    assert_live_observation(live, f"/ws/{WS}/chat/{agent_id('render')}")
    write("render-live.json", {"delivered": delivered[0], **live})


def agent_hover():
    current("render")
    results = []
    for theme in ("light", "dark"):
        if evaluate("document.documentElement.dataset.theme") != theme:
            browser("click", 'button[aria-label="Switch to ' + theme + ' mode"]')
        browser("scrollintoview", "[data-testid=chat-transcript] li[data-kind=agent]")
        before = evaluate("""(() => {const r=[...document.querySelectorAll('[data-testid=chat-transcript] li[data-kind=agent]')].at(-1);
          const q=r.getBoundingClientRect();return {x:q.x,y:q.y,width:q.width,height:q.height};})()""")
        browser("find", "last", "[data-testid=chat-transcript] li[data-kind=agent]", "hover")
        observed = evaluate("""(() => {const rows=[...document.querySelectorAll('[data-testid=chat-transcript] li[data-kind=agent]')];
          const r=rows.at(-1), p=r.querySelector('[data-testid=message-actions]'), b=p?.querySelector('button[aria-label="Copy message"]');
          const q=r.getBoundingClientRect(), z=p?.getBoundingClientRect();
          return {theme:document.documentElement.dataset.theme, row:{x:q.x,y:q.y,width:q.width,height:q.height},
            pill:!!p,copy:!!b,time:p?.textContent||'',opacity:p&&getComputedStyle(p).opacity,
            pillHeight:z?.height,adjacent:(()=>{const a=[...document.querySelectorAll('[data-testid=chat-transcript] li[data-kind]')];
              return a.flatMap((x,i)=>x.dataset.kind==='agent'&&a[i+1]?.dataset.kind==='agent'
                ? [a[i+1].getBoundingClientRect().top-x.getBoundingClientRect().bottom] : []);})()};})()""")
        assert observed["theme"] == theme and observed["pill"] and observed["copy"], observed
        assert observed["opacity"] != "0" and observed["time"].strip(), observed
        assert observed["pillHeight"] > 0 and observed["row"] == before, "hover pill moved its message row"
        assert all(gap < 42 for gap in observed["adjacent"]), "adjacent agent messages retained the old 42px gap"
        results.append(observed)
        shot("render", f"agent-hover-{theme}")
    write("render-agent-hover.json", results)
    if not any(item["adjacent"] for item in results):
        write("agent-spacing-blocked.json", {"status": "blocked", "prerequisite": "two consecutive saved assistant message items from one real author; this one-answer turn supplied only one"})


def input_delivery_diagnostic(evs, source, owned_agent):
    rows = []
    marker = f"VISUAL_{RUN} "
    for event in evs:
        if event["kind"] != "message.delivered":
            continue
        value = event["payload"].get("text")
        text = value if isinstance(value, str) else None
        rows.append({"event_id": event["event_id"], "seq": event["seq"],
                     "turn_id": event.get("turn_id"),
                     "owned_agent": event.get("agent_id") == owned_agent,
                     "text_length": len(text) if text is not None else None,
                     "text_sha256": hashlib.sha256(text.encode()).hexdigest() if text is not None else None,
                     "has_marker": text.startswith(marker) if text is not None else False,
                     "matches_source": text == source,
                     "matches_trimmed_source": text == source.strip()})
    return {"run": RUN, "case": "input", "agent_id": owned_agent,
            "source_length": len(source), "source_sha256": hashlib.sha256(source.encode()).hexdigest(),
            "source_has_edge_whitespace": source != source.strip(),
            "counts": {"delivered": len(rows),
                       "marker": sum(row["has_marker"] for row in rows),
                       "exact": sum(row["matches_source"] for row in rows),
                       "trimmed": sum(row["matches_trimmed_source"] for row in rows),
                       "owned_exact": sum(row["owned_agent"] and row["matches_source"] for row in rows)},
            "events": rows}


def self_test_input_delivery():
    from tempfile import TemporaryDirectory
    from unittest.mock import patch

    source = long_text()
    assert len(source) > 8000 and source == source.strip() and source.endswith("sentence.")
    assert '<img src="x" onerror="alert(1)">' in source

    def delivered(text, event_id="ev_1", owned="agt_owned"):
        return {"kind": "message.delivered", "event_id": event_id, "seq": int(event_id[-1]),
                "turn_id": "turn_1", "agent_id": owned, "payload": {"text": text}}

    def exercise(test_source, saved, should_pass, counts):
        with TemporaryDirectory() as folder:
            dom = {"text": test_source, "collapsed": "false", "showFull": False,
                   "showAll": False, "unsafeNodes": 0, "width": 100, "scrollWidth": 100}
            def read_dom(_script):
                if not should_pass:
                    raise AssertionError("delivery failure reached DOM readback")
                return dom

            with patch.dict(globals(), {"WORK": Path(folder), "long_text": lambda: test_source,
                                        "agent_id": lambda case: "agt_owned", "current": lambda case: None,
                                        "events": lambda case: saved, "evaluate": read_dom}):
                try:
                    input_check("all")
                except AssertionError as exc:
                    assert not should_pass and "delivered exactly once" in str(exc), exc
                else:
                    assert should_pass, "missing, duplicate, foreign or trimmed delivery passed"
                receipt = json.loads((Path(folder) / "input-delivery-diagnostic.json").read_text())
                assert receipt["run"] == RUN and receipt["case"] == "input"
                assert receipt["agent_id"] == "agt_owned" and receipt["counts"] == counts, receipt
                assert receipt["source_length"] == len(test_source)
                assert receipt["source_sha256"] == hashlib.sha256(test_source.encode()).hexdigest()
                assert receipt["source_has_edge_whitespace"] == (test_source != test_source.strip())
                assert all(set(row) == {"event_id", "seq", "turn_id", "owned_agent", "text_length",
                                        "text_sha256", "has_marker", "matches_source",
                                        "matches_trimmed_source"} for row in receipt["events"])
                serialized = json.dumps(receipt)
                assert test_source not in serialized and "<img" not in serialized, "diagnostic leaked text"
                return receipt

    matched = {"delivered": 1, "marker": 1, "exact": 1, "trimmed": 1, "owned_exact": 1}
    success = exercise(source, [delivered(source)], True, matched)
    assert success["events"][0]["text_sha256"] == hashlib.sha256(source.encode()).hexdigest()
    old_source = source + " "
    mismatch = exercise(old_source, [delivered(source)], False,
                        {"delivered": 1, "marker": 1, "exact": 0, "trimmed": 1, "owned_exact": 0})
    assert mismatch["events"][0]["text_length"] == len(old_source) - 1
    exercise(source, [], False, {"delivered": 0, "marker": 0, "exact": 0, "trimmed": 0, "owned_exact": 0})
    exercise(source, [delivered(source), delivered(source, "ev_2")], False,
             {"delivered": 2, "marker": 2, "exact": 2, "trimmed": 2, "owned_exact": 2})
    foreign = exercise(source, [delivered(source, owned="agt_foreign")], False,
                       {"delivered": 1, "marker": 1, "exact": 1, "trimmed": 1, "owned_exact": 0})
    assert foreign["events"][0]["owned_agent"] is False


def input_check(stage):
    current("input")
    evs = events("input")
    source = long_text()
    diagnostic = input_delivery_diagnostic(evs, source, agent_id("input"))
    write("input-delivery-diagnostic.json", diagnostic)
    delivered = [e for e in evs if e["kind"] == "message.delivered" and
                 e.get("agent_id") == diagnostic["agent_id"] and e["payload"].get("text") == source]
    assert len(delivered) == 1, "long literal text was not delivered exactly once through Chat"
    assert len(source) > 8000, "source did not cross the former 8000-character display threshold"
    dom = evaluate("""(() => { const r=[...document.querySelectorAll('[data-testid=chat-transcript] li[data-kind=user]')].at(-1);
      const b=r?.querySelector('[class*=userBody]'); const t=b?.firstElementChild;
      return {text:t?.textContent||'', collapsed:b?.dataset.userMessageCollapsed,
        showFull:!![...r.querySelectorAll('button')].find(x=>x.textContent==='Show full message'),
        showAll:!![...r.querySelectorAll('button')].find(x=>x.textContent.startsWith('Show all')),
        unsafeNodes:r?.querySelectorAll('img,script,iframe,object,embed').length||0,
        width:t?.clientWidth, scrollWidth:t?.scrollWidth}; })()""")
    assert dom["unsafeNodes"] == 0, "HTML-shaped user text created executable nodes"
    assert dom["scrollWidth"] <= dom["width"] + 1, "long user text overflowed its bubble"
    if stage == "collapsed":
        assert dom["collapsed"] == "true" and dom["showFull"], dom
        assert source[:200] in dom["text"] and len(dom["text"]) < len(source), dom
    elif stage in ("expanded", "all", "reloaded"):
        assert dom["collapsed"] == "false" and dom["text"] == source and not dom["showAll"], dom
    else:
        raise ValueError(stage)
    write(f"input-{stage}.json", {"delivered": delivered[0], "dom": dom, "source_length": len(source)})


def layout():
    current("input")
    measurements = []
    for theme in ("light", "dark"):
        current_theme = evaluate("document.documentElement.dataset.theme")
        if current_theme != theme:
            browser("click", 'button[aria-label="Switch to ' + theme + ' mode"]')
        assert evaluate("document.documentElement.dataset.theme") == theme
        for width in (130, 105, 80):
            evaluate("(() => { document.querySelector('[data-testid=chat-transcript]').style.setProperty('--chat-column', '" + str(width) + "px'); return true; })()")
            browser("scrollintoview", "[data-testid=chat-transcript] li[data-kind=user]")
            before = evaluate("""(() => {const r=[...document.querySelectorAll('[data-testid=chat-transcript] li[data-kind=user]')].at(-1);
              const b=r.querySelector('[class*=userBubble]').getBoundingClientRect();
              return {x:b.x,y:b.y,width:b.width,height:b.height};})()""")
            browser("hover", "[data-testid=chat-transcript] li[data-kind=user]")
            result = evaluate("""(() => {const r=[...document.querySelectorAll('[data-testid=chat-transcript] li[data-kind=user]')].at(-1);
              const p=r.querySelector('[data-testid=message-actions]'); const b=r.querySelector('[class*=userBubble]');
              const c=p?.querySelector('button[aria-label="Copy your message"]');
              const rect=e=>{const q=e.getBoundingClientRect();return {x:q.x,y:q.y,width:q.width,height:q.height,right:q.right};};
              return {row:rect(r),pill:p&&rect(p),bubble:rect(b),copy:c&&rect(c),
                pillVisible:p&&getComputedStyle(p).opacity!=='0', theme:document.documentElement.dataset.theme};})()""")
            assert abs(result["row"]["width"] - width) <= 1, result
            assert result["pill"] and result["copy"] and result["pillVisible"], result
            assert result["pill"]["x"] >= result["row"]["x"] - 1, result
            assert result["pill"]["right"] <= result["bubble"]["x"] + 1, result
            assert result["copy"]["width"] >= 14 and result["copy"]["x"] >= result["pill"]["x"] - 1, result
            assert result["copy"]["right"] <= result["pill"]["right"] + 1, result
            assert abs(result["pill"]["y"] - result["bubble"]["y"]) <= 1, result
            assert all(abs(result["bubble"][k] - before[k]) <= 1 for k in before), "hover shifted the user bubble"
            measurements.append(result)
            shot("input", f"user-pill-{width}-{theme}")
    evaluate("(() => { document.querySelector('[data-testid=chat-transcript]').style.removeProperty('--chat-column'); document.documentElement.style.fontSize='32px'; return true; })()")
    browser("hover", "[data-testid=chat-transcript] li[data-kind=user]")
    font = evaluate("""(() => {const r=[...document.querySelectorAll('[data-testid=chat-transcript] li[data-kind=user]')].at(-1);
      const p=r.querySelector('[data-testid=message-actions]').getBoundingClientRect();
      const b=r.querySelector('[class*=userBubble]').getBoundingClientRect();
      return {font:getComputedStyle(document.documentElement).fontSize,pillLeft:p.left,pillRight:p.right,rowLeft:r.getBoundingClientRect().left,bubbleLeft:b.left};})()""")
    assert font["font"] == "32px" and font["pillLeft"] >= font["rowLeft"] - 1 and font["pillRight"] <= font["bubbleLeft"] + 1, font
    shot("input", "user-pill-large-font")
    evaluate("(() => { document.documentElement.style.removeProperty('font-size'); return true; })()")
    write("input-layout.json", {"measurements": measurements, "large_font": font})


def locale_saved_identity(original, saved):
    source = long_text()
    assert original.get("event_id") and original.get("agent_id") == agent_id("input") and \
           original.get("kind") == "message.delivered" and original["payload"].get("text") == source, \
           "locale setup lacks the exact owned saved user receipt"
    same = [e for e in saved if e.get("event_id") == original["event_id"]]
    assert same == [original], "locale reload changed saved EventID or full message"
    assert len([e for e in saved if e.get("kind") == "message.delivered" and
                e.get("agent_id") == agent_id("input") and e["payload"].get("text") == source]) == 1, \
        "locale reload changed the exact delivered message count"
    return {"event_id": original["event_id"], "seq": original["seq"], "agent_id": original["agent_id"],
            "text_length": len(source), "text_sha256": hashlib.sha256(source.encode()).hexdigest()}


def locale_holder_receipt(receipt, status, config, target=None):
    assert isinstance(receipt, dict) and isinstance(receipt.get("targetId"), str) and receipt["targetId"], \
        "locale holder omitted owned target ID"
    assert receipt == {"status": status, "targetId": receipt["targetId"],
                       "origin": config["origin"], "route": config["route"]}, \
        "locale holder changed owned origin, route or status"
    if target is not None:
        assert receipt["targetId"] == target, "locale reload changed the owned browser target"
    return receipt["targetId"]


def require_locale_time(observed):
    assert isinstance(observed, str) and "অপৰাহ্ন ১২.৫৯" in observed, \
        "BLOCKED: documented long time did not render on the owned Chat row"


def self_test_locale_stress():
    from tempfile import TemporaryDirectory
    from unittest.mock import patch

    with TemporaryDirectory(prefix="aft-visual-locale-oracle-") as folder:
        work = Path(folder)
        (work / "input.id").write_text("agt_owned\n")
        source = long_text()
        original = {"event_id": "event_owned", "agent_id": "agt_owned", "seq": 7,
                    "kind": "message.delivered", "payload": {"text": source}}
        config = {"origin": "http://127.0.0.1:1234", "route": "/ws/OFFLINE/chat/agt_owned"}
        installed = {"status": "installed", "targetId": "target_owned", **config}
        with patch.dict(globals(), WORK=work):
            identity = locale_saved_identity(original, [original])
            assert identity["text_length"] == len(source) and identity["event_id"] == "event_owned"
            assert locale_holder_receipt(installed, "installed", config) == "target_owned"
            assert locale_holder_receipt({**installed, "status": "reloaded"}, "reloaded",
                                         config, "target_owned") == "target_owned"
            require_locale_time("অপৰাহ্ন ১২.৫৯")
            negatives = (
                lambda: locale_saved_identity(original, []),
                lambda: locale_saved_identity(original, [{**original, "event_id": "changed"}]),
                lambda: locale_saved_identity(original, [{**original, "payload": {"text": "changed"}}]),
                lambda: locale_saved_identity(original, [original, original]),
                lambda: locale_saved_identity({**original, "agent_id": "agt_foreign"}, [original]),
                lambda: locale_holder_receipt({**installed, "route": "/ws/OFFLINE/chat/agt_foreign"},
                                               "installed", config),
                lambda: locale_holder_receipt({**installed, "targetId": "target_foreign",
                                                "status": "reloaded"}, "reloaded", config, "target_owned"),
                lambda: require_locale_time("9:24 PM"),
            )
            for reject in negatives:
                try:
                    reject()
                except AssertionError:
                    continue
                raise AssertionError("invalid locale setup or saved receipt passed")


def locale_stress():
    current("input")
    original = json.loads((WORK / "input-all.json").read_text())["delivered"]
    identity = locale_saved_identity(original, events("input"))
    # The source Playwright UI7 test adds this local formatter before Chat loads.
    # The timestamp and message remain actual saved events, not injected content.
    config = clipboard_preflight("input")
    process = None
    stage = "setup"
    observed = None
    measurements = []
    receipt = None
    failure = None
    try:
        script = Path(required("AFT_TESTS_DIR")) / "scripts/coverage-chat-visual-locale.mjs"
        process = subprocess.Popen(["node", str(script), "hold"], stdin=subprocess.PIPE,
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        process.stdin.write(json.dumps(config) + "\n")
        process.stdin.flush()
        receipt = clipboard_holder_ack(process)
        target = locale_holder_receipt(receipt, "installed", config)
        stage = "reload"
        process.stdin.write("reload\n")
        process.stdin.flush()
        locale_holder_receipt(clipboard_holder_ack(process), "reloaded", config, target)
        browser("wait", "--fn", """(() => {
          const r=[...document.querySelectorAll('[data-testid=chat-transcript] li[data-kind=user]')].at(-1);
          return r?.querySelector('[data-testid=message-actions]')?.textContent.includes('অপৰাহ্ন ১২.৫৯') || false;
        })()""", timeout=30)
        current("input")
        observed = evaluate("""(() => {const r=[...document.querySelectorAll('[data-testid=chat-transcript] li[data-kind=user]')].at(-1);
          return r?.querySelector('[data-testid=message-actions]')?.textContent||'';})()""")
        require_locale_time(observed)
        stage = "measure"
        assert locale_saved_identity(original, events("input")) == identity
        for width in (130, 105, 80):
            evaluate("(() => { document.querySelector('[data-testid=chat-transcript]').style.setProperty('--chat-column', '" + str(width) + "px'); return true; })()")
            browser("hover", "[data-testid=chat-transcript] li[data-kind=user]")
            result = evaluate("""(() => {const r=[...document.querySelectorAll('[data-testid=chat-transcript] li[data-kind=user]')].at(-1);
              const p=r.querySelector('[data-testid=message-actions]'),b=r.querySelector('[class*=userBubble]'),c=p.querySelector('button[aria-label="Copy your message"]');
              const q=e=>{const x=e.getBoundingClientRect();return {left:x.left,right:x.right,width:x.width};};
              return {row:q(r),pill:q(p),bubble:q(b),copy:q(c),time:p.textContent};})()""")
            require_locale_time(result["time"])
            assert abs(result["row"]["width"] - width) <= 1, result
            assert result["pill"]["left"] >= result["row"]["left"] - 1 and result["pill"]["right"] <= result["bubble"]["left"] + 1, result
            assert result["copy"]["width"] >= 14 and result["copy"]["right"] <= result["pill"]["right"] + 1, result
            measurements.append(result)
            shot("input", f"long-locale-{width}")
        stage = "verified"
    except Exception as exc:
        failure = type(exc).__name__
        write("locale-layout-blocked.json", {"status": "blocked", "stage": stage,
              "failure_type": failure, "observed": observed, "saved_identity": identity})
        raise
    finally:
        exit_code, holder_reason = close_clipboard_holder(process)
        write("locale-stimulus-cleanup.json", {"run": RUN, "case": "input", "stage": stage,
              "failure_type": failure, "holder_exit": exit_code, "holder_reason": holder_reason,
              "target_id": receipt.get("targetId") if isinstance(receipt, dict) else None})
        if process is not None and exit_code != 0:
            raise PermissionError("owned locale script was not removed and formatter restored")
        if stage in ("measure", "verified"):
            path = evaluate("location.pathname", timeout=5)
            assert path == f"/ws/{WS}/chat/{agent_id('input')}", "locale cleanup changed Chat route"
            evaluate("(() => {document.querySelector('[data-testid=chat-transcript]')?.style.removeProperty('--chat-column'); return true;})()", timeout=5)
    assert evaluate("!Object.prototype.hasOwnProperty.call(window, '__aftVisualLocaleOriginal')"), \
        "locale formatter remained installed after bounded cleanup"
    write("input-locale-layout.json", {"stimulus": "documented long as-IN time via owned pre-load formatter",
          "proof": "injected layout, not native locale", "saved_identity": identity,
          "observed_time": observed, "measurements": measurements})


def stop_if_running():
    current("input")
    if evaluate("!!document.querySelector('form button[title=" + json.dumps("Stop the running turn") + "]')"):
        browser("click", 'form button[title="Stop the running turn"]')


def input_reload_check():
    current("input")
    before = json.loads((WORK / "input-all.json").read_text())
    current_events = events("input")
    original = before["delivered"]
    same = [e for e in current_events if e["event_id"] == original["event_id"]]
    assert same == [original], "reload changed the saved long user event"
    assert len([e for e in current_events if e["kind"] == "message.delivered" and e["payload"].get("text") == long_text()]) == 1
    input_check("reloaded")


def render_reload_check():
    current("render")
    before = json.loads((WORK / "render-before-reload.json").read_text())["events"]
    after = events("render")
    assert [e["event_id"] for e in before] == [e["event_id"] for e in after[:len(before)]], "reload changed saved render history"
    assert len(after) == len(before), "unexpected extra events after completed render turn"
    render_check()
    write("render-after-reload.json", {"events": after})


def reduced_live_check():
    current("render")
    state = evaluate("""(() => {const c=document.querySelector('[data-testid=chat-transcript]');
      const a=[...c.querySelectorAll('li[data-kind=agent]')].at(-1);
      const caret=a?.querySelector('[data-streaming-caret]');
      const working=c.querySelector('[data-testid=working-row]');
      return {reduced:matchMedia('(prefers-reduced-motion: reduce)').matches,
        streaming:!!document.querySelector('form button[title="Stop the running turn"]'),
        caret:!!caret,caretAnimation:caret&&getComputedStyle(caret).animationName,
        fresh:a?.querySelectorAll('[data-fresh]').length||0,
        working:!!working,workingAnimation:working&&getComputedStyle(working.firstElementChild).animationName};})()""")
    assert state["reduced"] and state["streaming"] and state["caret"] and state["working"], state
    assert state["caretAnimation"] == "none" and state["fresh"] == 0, state
    assert state["workingAnimation"] == "none", state
    write("render-reduced-live.json", state)


def reduced_saved_check():
    delivered, end, between = turn_events("render", "VISUAL_REDUCED")
    answer = "\n".join(e["payload"].get("text", "") for e in between
                       if e["kind"] == "item.completed" and e["payload"].get("itemKind") == "message")
    assert "README.md" in answer and "npm test" in answer and len(answer.split()) >= 100, "real reduced-motion reply lacked the requested grounded length"
    shown = evaluate("""(() => {const a=[...document.querySelectorAll('[data-testid=chat-transcript] li[data-kind=agent]')].at(-1);
      return {text:a?.querySelector('[data-testid=chat-markdown]')?.textContent||'',caret:!!a?.querySelector('[data-streaming-caret]'),
        reduced:matchMedia('(prefers-reduced-motion: reduce)').matches};})()""")
    assert shown["reduced"] and not shown["caret"] and "npm test" in shown["text"], shown
    write("render-reduced-end.json", {"delivered": delivered, "turn_completed": end, "answer": answer, "dom": shown})


def cleanup_agents():
    for case, expected in NAMES.items():
        if not (WORK / f"{case}.id").exists():
            continue
        owned = json.loads((WORK / f"{case}-identity.json").read_text())
        a = request(f"{PREFIX}/{agent_id(case)}")
        assert a["agent_id"] == owned["agent_id"] and a["name"] in (expected, expected + "-renamed"), a
        assert a["repo"] == owned["repo"] == required("AFT_AGENT_FLOW_REPO"), a
        assert a["preset"] == "lead" and a["created_by_kind"] == "user" and a["parent_agent_id"] is None, a
        if not a.get("archived_at"):
            request(f"{PREFIX}/{a['agent_id']}/archive", "POST", {"reason": "cancelled"}, f"cov-visual-{RUN}-{case}-archive")


def owned_workspace_targets(plan, created, roster):
    planned = set(plan["planned_names"])
    baseline = {row["id"] for row in plan["baseline"]}
    assert all(row["name"] in planned and row["id"] not in baseline and row["id"] != WS for row in created), created
    assert len({row["id"] for row in created}) == len(created), "duplicate saved workspace ID receipt"
    assert len({row["name"] for row in created}) == len(created), "duplicate saved workspace name receipt"
    owned = {(row["id"], row["name"]) for row in created}
    unknown = [{"id": row["id"], "name": row["name"]} for row in roster
               if row["name"] in planned and (row["id"], row["name"]) not in owned]
    assert not unknown, f"LEFTOVER: planned-name workspace has no matching successful-Create ID receipt: {unknown}"
    targets = []
    for saved in created:
        matches = [row for row in roster if row["id"] == saved["id"]]
        assert len(matches) <= 1, matches
        if matches:
            assert matches[0]["name"] == saved["name"], f"saved workspace ID was renamed: {matches[0]}"
            targets.append(saved)
    return targets


def self_test_roster_cleanup():
    from tempfile import TemporaryDirectory
    from unittest.mock import patch

    name = MOBILE_WS_NAMES[0]
    owned_id = "AFT-OWNED-ONE"
    baseline = {"id": WS, "name": "LOCALMODE"}
    created_row = {"id": owned_id, "name": name}
    response = {"success": True, "data": {"workspaces": [baseline, created_row]}}
    calls = iter(({"workspaces": [baseline]}, RuntimeError("detail readback failed")))

    def readback(_path):
        result = next(calls)
        if isinstance(result, Exception):
            raise result
        return result

    with TemporaryDirectory(prefix="aft-visual-roster-oracle-") as temp:
        with patch.dict(globals(), WORK=Path(temp), MOBILE_WS_NAMES=[name]), patch(__name__ + ".current"), \
             patch(__name__ + ".workspace_data", side_effect=readback) as read_mock, \
             patch(__name__ + ".request", return_value=response) as request_mock:
            try:
                create_mobile_roster()
            except RuntimeError as exc:
                assert "detail readback failed" in str(exc), exc
            else:
                raise AssertionError("expected detail readback failure")
            plan = json.loads((Path(temp) / "mobile-workspace-plan.json").read_text())
            created = json.loads((Path(temp) / "mobile-workspace-created.json").read_text())
            assert created == [created_row], "Create ID receipt was not persisted before failing detail readback"
            assert owned_workspace_targets(plan, created, [baseline, created_row]) == [created_row]
            try:
                owned_workspace_targets(plan, created, [baseline, {"id": "OTHER", "name": name}])
            except AssertionError as exc:
                assert "LEFTOVER" in str(exc), exc
            else:
                raise AssertionError("same-name different-ID workspace was incorrectly adopted for deletion")
            try:
                owned_workspace_targets(plan, [], [baseline, created_row])
            except AssertionError as exc:
                assert "LEFTOVER" in str(exc), exc
            else:
                raise AssertionError("planned name without saved Create receipt was incorrectly adopted")
            request_mock.reset_mock()
            read_mock.side_effect = [{"workspaces": [baseline, {"id": "OTHER", "name": name}]}]
            try:
                cleanup_mobile_workspaces()
            except AssertionError as exc:
                assert "LEFTOVER" in str(exc), exc
            else:
                raise AssertionError("cleanup adopted a same-name different-ID workspace")
            assert request_mock.call_count == 0, "cleanup called DELETE for an unowned same-name workspace"
            assert (Path(temp) / "mobile-workspace-leftover.json").exists()
            read_mock.side_effect = [
                {"workspaces": [baseline, created_row]},
                {"id": owned_id, "name": name, "repos": []},
                {"workspaces": [baseline]},
                {"workspaces": [baseline]},
            ]
            request_mock.return_value = {"success": True}
            cleanup_mobile_workspaces()
            assert request_mock.call_count == 1
            args, kwargs = request_mock.call_args
            assert args == (f"/api/workspaces/{owned_id}", "DELETE") and kwargs == {"expected_status": 200}, (args, kwargs)
            (Path(temp) / "mobile-workspace-created.json").unlink()
            request_mock.reset_mock()
            read_mock.side_effect = [{"workspaces": [baseline, created_row]}]
            try:
                cleanup_mobile_workspaces()
            except AssertionError as exc:
                assert "LEFTOVER" in str(exc), exc
            else:
                raise AssertionError("cleanup adopted a planned name without a saved ID receipt")
            assert request_mock.call_count == 0, "cleanup called DELETE without a saved Create ID receipt"


def cleanup_mobile_workspaces():
    plan_path = WORK / "mobile-workspace-plan.json"
    if not plan_path.exists():
        return
    plan = json.loads(plan_path.read_text())
    assert all(name in MOBILE_WS_NAMES for name in plan["planned_names"]), plan
    active_path = f"/api/workspaces/{urllib.parse.quote(WS, safe='')}"
    roster = workspace_data(active_path)["workspaces"]
    created_path = WORK / "mobile-workspace-created.json"
    created = json.loads(created_path.read_text()) if created_path.exists() else []
    try:
        targets = owned_workspace_targets(plan, created, roster)
    except AssertionError as exc:
        write("mobile-workspace-leftover.json", {"status": "blocked", "reason": str(exc),
              "planned_names": plan["planned_names"], "created": created,
              "roster": [{"id": row["id"], "name": row["name"]} for row in roster]})
        raise
    removed = []
    for row in targets:
        actual = workspace_data(f"/api/workspaces/{urllib.parse.quote(row['id'], safe='')}")
        assert actual["id"] == row["id"] and actual["name"] == row["name"] and actual["repos"] == [], actual
        response = request(f"/api/workspaces/{urllib.parse.quote(row['id'], safe='')}", "DELETE", expected_status=200)
        assert response["success"], response
        remaining = workspace_data(active_path)["workspaces"]
        assert not any(w["id"] == row["id"] for w in remaining), remaining
        removed.append(row)
        write("mobile-workspace-cleanup.json", removed)
    assert all(any(row["id"] == original["id"] and row["name"] == original["name"] for row in workspace_data(active_path)["workspaces"])
               for original in plan["baseline"]), "baseline workspace roster changed during owned cleanup"


def cleanup():
    try:
        cleanup_agents()
    finally:
        cleanup_mobile_workspaces()


def main():
    command, *args = sys.argv[1:]
    globals()[command.replace("-", "_")](*args)


if __name__ == "__main__":
    main()
