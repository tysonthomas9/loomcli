#!/usr/bin/env python3
"""Owned, no-Send real Chat proof for the SK1 skip-link criterion."""

import base64
import json
import os
import re
import secrets
import subprocess
import sys
import urllib.parse
import urllib.request
from pathlib import Path


def required(name):
    value = os.environ.get(name, "")
    if not value:
        raise ValueError(f"{name} is required")
    return value


RUN = required("RUN_ID")
WS = required("AFT_WS")
API = required("AFT_API_URL").rstrip("/")
ORIGIN = required("AFT_BASE_URL").rstrip("/")
WORK = Path(required("AFT_WORK_DIR")) / "chat-visual-skip"
NAME = f"cov-visual-skip-{RUN}"
PREFIX = f"/api/workspaces/{urllib.parse.quote(WS, safe='')}/v1/agents"


def write(name, value):
    WORK.mkdir(parents=True, exist_ok=True)
    (WORK / name).write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def browser(*args, timeout=8):
    return subprocess.check_output(
        ["agent-browser", "--session", required("AFT_SESSION"), *args],
        text=True, timeout=timeout,
    ).strip()


def evaluate(script, timeout=8):
    encoded = base64.b64encode(script.encode()).decode()
    return json.loads(browser("eval", "-b", encoded, timeout=timeout))


def request(path, method="GET", body=None):
    data = None if body is None else json.dumps(body).encode()
    headers = {"Accept": "application/json"}
    if data is not None:
        headers["Content-Type"] = "application/json"
    with urllib.request.urlopen(urllib.request.Request(
        API + path, data=data, headers=headers, method=method,
    ), timeout=15) as response:
        raw = response.read()
        if response.status == 204:
            assert not raw, "Archive 204 carried an unexpected body"
            return None
        return json.loads(raw)


def agent_id():
    value = (WORK / "agent.id").read_text().strip()
    assert re.fullmatch(r"agt_[A-Za-z0-9_-]+", value), "invalid owned Agent ID"
    return value


def route():
    return f"/ws/{WS}/chat/{agent_id()}"


def current():
    observed = evaluate("({origin:location.origin,path:location.pathname})")
    assert observed == {"origin": ORIGIN, "path": route()}, "foreign Chat origin, route or Agent"


def identity():
    current()
    agent = request(f"{PREFIX}/{agent_id()}")
    expected = {"agent_id": agent_id(), "name": NAME,
                "repo": required("AFT_AGENT_FLOW_REPO"), "harness": "opencode",
                "preset": "lead", "created_by_kind": "user", "parent_agent_id": None}
    assert all(agent.get(key) == value for key, value in expected.items()), "saved Lead identity changed"
    return expected


def event_receipts():
    found, after = [], 0
    for _ in range(10):
        page = request(f"{PREFIX}/{agent_id()}/events?after={after}&limit=500")
        found.extend(page["events"])
        if not page["more"]:
            break
        next_cursor = page["next"]
        assert next_cursor > after, "saved event cursor did not advance"
        after = next_cursor
    else:
        raise AssertionError("no-Send SK1 Agent exceeded bounded event pages")
    summary = [{"event_id": e["event_id"], "seq": e["seq"], "kind": e["kind"]} for e in found]
    assert len({e["event_id"] for e in summary}) == len(summary), "duplicate saved event ID"
    assert len({e["seq"] for e in summary}) == len(summary), "duplicate saved sequence"
    assert [e["seq"] for e in summary] == sorted(e["seq"] for e in summary), "saved event order changed"
    assert not any(e["kind"].startswith(("message.", "turn.", "item.")) for e in summary), \
        "no-Send SK1 Agent created a message or turn"
    return summary


def no_turn(stage):
    owner = identity()
    baseline = json.loads((WORK / "baseline-events.json").read_text())
    actual = event_receipts()
    dom = evaluate("""(() => {const c=document.querySelector('[data-testid=chat-transcript]');
      const t=document.querySelector('textarea[aria-label=Message]');
      return {rows:c?.querySelectorAll('li[data-kind=user],li[data-kind=agent]').length ?? null,
        draft:t?.value ?? null,stop:!!document.querySelector('form button[title="Stop the running turn"]')};})()""")
    receipt = {"agent": owner, "baseline": baseline, "events": actual, "dom": dom}
    write(f"no-turn-{stage}.json", receipt)
    assert actual == baseline and dom == {"rows": 0, "draft": "", "stop": False}, \
        "SK1 case created a message, turn or Chat draft"


def preflight():
    assert required("AFT_REAL_BACKEND") == "opencode"
    assert re.fullmatch(r"[A-Za-z0-9_-]+", RUN), "unsafe run ID"
    assert re.fullmatch(r"[A-Za-z0-9_-]{1,64}", NAME), "unsafe Agent name"
    assert re.fullmatch(r"http://127\.0\.0\.1:[0-9]+", ORIGIN), "foreign browser origin"
    assert Path(required("AFT_AGENT_FLOW_REPO")).name == "source-repo"


def claim():
    observed = evaluate("({origin:location.origin,path:location.pathname})")
    match = re.fullmatch(rf"/ws/{re.escape(WS)}/chat/(agt_[A-Za-z0-9_-]+)", observed["path"])
    assert observed["origin"] == ORIGIN and match, "UI Create did not open owned Chat"
    WORK.mkdir(parents=True, exist_ok=True)
    (WORK / "agent.id").write_text(match[1] + "\n")
    write("identity.json", identity())
    write("baseline-events.json", event_receipts())
    no_turn("claimed")


STATE_JS = """(async () => {const settle=__SETTLE__;
  const a=document.querySelector('a[href="#main-content"]'),m=document.querySelector('main#main-content');
  if(!a||!m)throw Error('SK1 link or main absent');
  if(settle){let guard;try{const timeout=new Promise((_,reject)=>{
      guard=setTimeout(()=>reject(Error('SK1 CSS animation timed out')),1500);});
    await Promise.race([(async()=>{await new Promise(requestAnimationFrame);
      await Promise.all(a.getAnimations().map(animation=>animation.finished));})(),timeout]);
    }finally{clearTimeout(guard);}}
  const r=a.getBoundingClientRect(),b=m.getBoundingClientRect(),style=getComputedStyle(a);
  const x=Math.round(b.right-5),y=Math.round(b.bottom-5),hit=document.elementFromPoint(x,y);
  return {origin:location.origin,path:location.pathname,
    reduced:matchMedia('(prefers-reduced-motion: reduce)').matches,
    transition:style.transitionDuration,transform:style.transform,
    link:{text:a.textContent.trim(),focused:document.activeElement===a,
      focusVisible:a.matches(':focus-visible'),inViewport:r.bottom>0&&r.top<innerHeight,
      top:r.top,bottom:r.bottom},
    active:{tag:document.activeElement?.tagName||null,
      inMain:m.contains(document.activeElement)&&m!==document.activeElement,
      focusVisible:document.activeElement?.matches(':focus-visible')===true},
    main:{left:b.left,right:b.right,top:b.top,bottom:b.bottom,
      x,y,hitInside:!!hit&&m.contains(hit),hitTag:hit?.tagName||null},
    hash:location.hash==='#main-content'?'#main-content':(location.hash===''?'':'other')};})()"""


def state(settle=False):
    current()
    observed = evaluate(STATE_JS.replace("__SETTLE__", "true" if settle else "false"), timeout=8)
    assert observed["origin"] == ORIGIN and observed["path"] == route(), "SK1 state changed Chat"
    return observed


def capture(stage, observed):
    write(f"{stage}.json", {"agent": identity(), "state": observed})
    browser("screenshot", str(WORK / f"skip-{stage}.png"))


def assert_normal(observed):
    assert observed["reduced"] is False, "BLOCKED: SK1 normal-motion proof inherited reduced motion"
    durations = re.findall(r"(?:^|,\s*)([0-9]*\.?[0-9]+)(m?s)(?=,|$)", observed["transition"])
    assert durations and any(float(value) > 0 for value, _unit in durations), \
        "BLOCKED: SK1 link has no normal-motion CSS transition"
    assert observed["link"]["text"] == "Skip to main content", "SK1 link text changed"


def start():
    observed = state()
    capture("fresh-hidden", observed)
    assert_normal(observed)
    assert observed["active"]["tag"] == "BODY", "SK1 document did not start before the first Tab"
    assert not observed["link"]["focused"] and not observed["link"]["inViewport"], \
        "SK1 link visible on fresh load"
    no_turn("fresh")


def tab():
    observed = state(settle=True)
    capture("first-tab-visible", observed)
    assert_normal(observed)
    assert observed["link"]["focused"] and observed["link"]["focusVisible"] and \
        observed["link"]["inViewport"], "first Tab did not reveal SK1 link"


def entered():
    observed = state(settle=True)
    capture("enter-main-focus", observed)
    assert_normal(observed)
    assert observed["hash"] == "#main-content" and observed["active"]["inMain"], \
        "Enter and next Tab did not move focus inside main"
    assert not observed["link"]["inViewport"], "SK1 link stayed visible after main focus"


PROBE_JS = """(async () => {const action=__ACTION__,token=__TOKEN__,route=__ROUTE__;
  const name='__aftNormalSkipProbe',cleanupName='__aftNormalSkipCleanup';
  if(action==='install'){
    if(location.pathname!==route||Object.hasOwn(window,name)||Object.hasOwn(window,cleanupName))
      throw Error('foreign SK1 probe or route');
    const main=document.querySelector('main#main-content');if(!main)throw Error('SK1 main absent');
    const controller=new AbortController(),events=[];let dropped=0;
    const handler=e=>{if(events.length>=24){dropped++;return;}
      events.push({type:e.type,trusted:e.isTrusted===true,
        inMain:main.contains(e.target),link:e.target?.matches?.('a[href="#main-content"]')===true,
        button:e instanceof MouseEvent?e.button:null});};
    for(const type of ['mousedown','mouseup','click','focusin','focusout'])
      document.addEventListener(type,handler,{capture:true,signal:controller.signal});
    const probe={token,controller,events,read:()=>({events:events.slice(),dropped})};
    const cleanup=Object.freeze({token,probe});probe.cleanup=cleanup;Object.freeze(probe);
    try{Object.defineProperty(window,name,{value:probe,writable:false,configurable:true});
      Object.defineProperty(window,cleanupName,{value:cleanup,writable:false,configurable:true});}
    catch(e){controller.abort();delete window[name];throw e;}
    return {installed:true};
  }
  const p=window[name],c=window[cleanupName];
  if(action==='cleanup'){
    if(!p&&!c)return {removed:false,aborted:false,status:'absent'};
    const original=(c?.token===token&&c.probe?.cleanup===c)?c.probe:
      (p?.token===token&&p.cleanup?.probe===p)?p:null;
    if(!original||original.token!==token)throw Error('foreign SK1 cleanup owner');
    original.controller.abort();const aborted=original.controller.signal.aborted===true;
    const consistent=p===original&&c===original.cleanup;
    if(p===original)delete window[name];if(c===original.cleanup)delete window[cleanupName];
    if(!aborted||!consistent||p===original&&Object.hasOwn(window,name)||
       c===original.cleanup&&Object.hasOwn(window,cleanupName))throw Error('SK1 probe cleanup failed');
    return {removed:true,aborted:true};
  }
  if(action!=='read'||location.pathname!==route||!p||!c||p.token!==token||
     c.token!==token||c.probe!==p||p.cleanup!==c||p.controller.signal.aborted)
    throw Error('foreign or replaced SK1 probe');
  return p.read();})()"""


def probe(action, token):
    assert re.fullmatch(r"[0-9a-f]{24}", token), "unsafe SK1 probe token"
    script = PROBE_JS.replace("__ACTION__", json.dumps(action)) \
        .replace("__TOKEN__", json.dumps(token)).replace("__ROUTE__", json.dumps(route()))
    return evaluate(script)


def mouse_focus():
    before = state()
    assert_normal(before)
    x, y = before["main"]["x"], before["main"]["y"]
    assert 0 <= x < int(evaluate("innerWidth")) and 0 <= y < int(evaluate("innerHeight")) \
        and before["main"]["hitInside"], "canonical bottom-right main point is not hittable"
    write("mouse-target.json", {"agent": identity(), "main": before["main"]})
    token = secrets.token_hex(12)
    attempted, failure, cleanup = False, None, None
    down_attempted, up_complete, release_error = False, False, None
    try:
        attempted = True
        assert probe("install", token) == {"installed": True}, "SK1 pointer probe did not install"
        browser("mouse", "move", str(x), str(y))
        down_attempted = True
        browser("mouse", "down")
        browser("mouse", "up")
        up_complete = True
        clicked = state(settle=True)
        pointer = probe("read", token)
        capture("after-real-mouse-hidden", clicked)
        write("pointer-events.json", {"agent": identity(), "events": pointer})
        assert_normal(clicked)
        kinds = [e["type"] for e in pointer["events"] if e["type"] in ("mousedown", "mouseup", "click")]
        assert kinds == ["mousedown", "mouseup", "click"], "real bottom-right mouse event order missing"
        assert all(e["trusted"] and e["inMain"] and e["button"] == 0 for e in pointer["events"]
                   if e["type"] in ("mousedown", "mouseup", "click")), "pointer did not hit main"
        assert not clicked["link"]["focused"] and not clicked["link"]["inViewport"], \
            "SK1 link visible after mouse click"
        assert evaluate("(() => {const a=document.querySelector('a[href=\"#main-content\"]');a.focus();return document.activeElement===a;})()"), \
            "programmatic SK1 focus failed"
        focused = state(settle=True)
        capture("after-mouse-programmatic-focus", focused)
        write("focus-events.json", {"agent": identity(), "events": probe("read", token)})
        no_turn("after-focus")
        assert_normal(focused)
        assert focused["link"]["focused"] and not focused["link"]["inViewport"], \
            "SK1 link visible after mouse then programmatic focus"
    except Exception as exc:
        failure = type(exc).__name__
        raise
    finally:
        if down_attempted and not up_complete:
            try:
                browser("mouse", "up")
                write("pointer-release.json", {"released": True})
            except Exception as exc:
                release_error = exc
                write("pointer-release.json", {"released": False, "failure_type": type(exc).__name__})
        if attempted:
            try:
                cleanup = probe("cleanup", token)
                if cleanup != {"removed": True, "aborted": True} and failure is None:
                    raise AssertionError("SK1 pointer probe was not removed")
            except Exception as exc:
                cleanup = {**cleanup, "failure_type": type(exc).__name__} if isinstance(cleanup, dict) \
                    else {"removed": False, "failure_type": type(exc).__name__}
                if failure is None:
                    raise
            finally:
                write("probe-cleanup.json", {"agent_id": agent_id(), "cleanup": cleanup,
                                             "prior_failure_type": failure})
        if release_error is not None and failure is None:
            raise release_error


def cleanup():
    if not (WORK / "agent.id").exists():
        return
    try:
        no_turn("teardown")
    finally:
        saved = json.loads((WORK / "identity.json").read_text())
        agent = request(f"{PREFIX}/{agent_id()}")
        assert all(agent.get(key) == value for key, value in saved.items()), "SK1 cleanup refused foreign Agent"
        if not agent.get("archived_at"):
            request(f"{PREFIX}/{agent_id()}/archive", "POST", {"reason": "cancelled"})


def self_test():
    from copy import deepcopy
    from tempfile import TemporaryDirectory
    from unittest.mock import patch

    normal = {"reduced": False, "transition": "0.15s", "link": {"text": "Skip to main content",
              "focused": False, "inViewport": False, "focusVisible": False},
              "main": {"x": 795, "y": 645, "hitInside": True}}
    assert_normal(normal)
    for mutation in ({"reduced": True}, {"transition": "0s"},
                     {"link": {"text": "changed"}}):
        changed = deepcopy(normal)
        for key, value in mutation.items():
            changed[key].update(value) if isinstance(value, dict) else changed.update({key: value})
        try:
            assert_normal(changed)
        except AssertionError:
            pass
        else:
            raise AssertionError("SK1 normal-motion gate accepted a missing prerequisite")

    def exercise(*, visible=False, trusted=True, down_failure=False):
        calls, states = [], [deepcopy(normal), deepcopy(normal), deepcopy(normal)]
        states[2]["link"].update({"focused": True, "focusVisible": visible, "inViewport": visible})
        pointer = {"events": [{"type": kind, "trusted": trusted, "inMain": True, "button": 0}
                              for kind in ("mousedown", "mouseup", "click")], "dropped": 0}

        def fake_browser(*args, timeout=8):
            calls.append(args)
            if down_failure and args == ("mouse", "down"):
                raise RuntimeError("mouse down response lost")
            return ""

        def fake_probe(action, _token):
            calls.append(("probe", action))
            if action == "install":
                return {"installed": True}
            if action == "read":
                return pointer
            return {"removed": True, "aborted": True}

        def fake_evaluate(script, timeout=8):
            if script == "innerWidth":
                return 800
            if script == "innerHeight":
                return 650
            assert "a.focus()" in script, "unexpected browser action in no-Send case"
            return True

        with TemporaryDirectory(prefix="aft-sk1-oracle-") as temp, \
             patch.dict(globals(), WORK=Path(temp), state=lambda settle=False: states.pop(0),
                        probe=fake_probe, browser=fake_browser, evaluate=fake_evaluate,
                        identity=lambda: {"agent_id": "agt_owned", "name": NAME},
                        agent_id=lambda: "agt_owned", no_turn=lambda stage: calls.append(("no-turn", stage))):
            try:
                mouse_focus()
            except (AssertionError, RuntimeError) as exc:
                failure = str(exc)
            else:
                failure = None
            receipts = {p.name: json.loads(p.read_text()) for p in Path(temp).glob("*.json")}
        return calls, receipts, failure

    calls, receipts, failure = exercise()
    assert failure is None and receipts["probe-cleanup.json"]["cleanup"]["aborted"]
    assert [c for c in calls if c[:1] == ("mouse",)] == [
        ("mouse", "move", "795", "645"), ("mouse", "down"), ("mouse", "up")]
    assert ("no-turn", "after-focus") in calls and \
        "after-mouse-programmatic-focus.json" in receipts
    calls, receipts, failure = exercise(visible=True)
    assert failure == "SK1 link visible after mouse then programmatic focus"
    assert "after-real-mouse-hidden.json" in receipts and \
        receipts["after-mouse-programmatic-focus.json"]["state"]["link"]["inViewport"]
    assert receipts["probe-cleanup.json"]["cleanup"]["aborted"] and \
        len([c for c in calls if c[:1] == ("screenshot",)]) == 2
    calls, receipts, failure = exercise(trusted=False)
    assert failure == "pointer did not hit main" and \
        "after-real-mouse-hidden.json" in receipts and \
        receipts["probe-cleanup.json"]["cleanup"]["aborted"]
    calls, receipts, failure = exercise(down_failure=True)
    assert failure == "mouse down response lost" and receipts["pointer-release.json"]["released"]
    assert [c for c in calls if c[:1] == ("mouse",)][-1] == ("mouse", "up")
    assert receipts["probe-cleanup.json"]["cleanup"]["aborted"]

    with patch.dict(globals(), agent_id=lambda: "agt_owned",
                    request=lambda path: {"events": [{"event_id": "evt_1", "seq": 1,
                                                       "kind": "message.delivered"}], "more": False}):
        try:
            event_receipts()
        except AssertionError as exc:
            assert "message or turn" in str(exc), exc
        else:
            raise AssertionError("no-Send oracle accepted a saved message")

    with TemporaryDirectory(prefix="aft-sk1-no-turn-") as temp, \
         patch.dict(globals(), WORK=Path(temp),
                    identity=lambda: {"agent_id": "agt_owned", "name": NAME},
                    evaluate=lambda script: {"rows": 0, "draft": "", "stop": False}):
        write("baseline-events.json", [])
        with patch.dict(globals(), event_receipts=lambda: []):
            no_turn("offline")
        assert json.loads((Path(temp) / "no-turn-offline.json").read_text())["events"] == []
        with patch.dict(globals(), event_receipts=lambda: [{"event_id": "evt_new", "seq": 1,
                                                              "kind": "agent.updated"}]):
            try:
                no_turn("changed")
            except AssertionError as exc:
                assert "message, turn or Chat draft" in str(exc), exc
            else:
                raise AssertionError("no-Send oracle accepted changed saved history")

    node_test = r"""
const vm=require('vm'),fs=require('fs'),assert=require('assert');
const [probeSource,stateSource]=JSON.parse(fs.readFileSync(0,'utf8'));
new Function('return '+stateSource.replace('__SETTLE__','false'));
const route='/ws/LOCALMODE/chat/agt_owned',token='0123456789abcdef01234567';
function page(){
  const listeners=new Map(),window={},location={pathname:route};
  const main={contains:e=>e===main};
  const document={querySelector:()=>main,addEventListener(type,handler,opts){
    if(!listeners.has(type))listeners.set(type,new Set());listeners.get(type).add(handler);
    opts.signal.addEventListener('abort',()=>listeners.get(type).delete(handler));}};
  class MouseEvent{constructor(type,trusted=true){this.type=type;this.target=main;this.isTrusted=trusted;this.button=0;}}
  const context={window,document,location,AbortController,MouseEvent};
  const run=action=>vm.runInNewContext(probeSource.replace('__ACTION__',JSON.stringify(action))
    .replace('__TOKEN__',JSON.stringify(token)).replace('__ROUTE__',JSON.stringify(route)),context);
  return {window,location,listeners,MouseEvent,run};
}
(async()=>{
  const p=page();await p.run('install');
  for(const type of ['mousedown','mouseup','click'])
    for(const handler of p.listeners.get(type))handler(new p.MouseEvent(type));
  const saved=await p.run('read');
  assert.deepStrictEqual(Array.from(saved.events,e=>e.type),['mousedown','mouseup','click']);
  assert(saved.events.every(e=>e.trusted&&e.inMain&&e.button===0));
  assert.equal((await p.run('cleanup')).aborted,true);
  assert([...p.listeners.values()].every(s=>s.size===0));
  const changed=page();await changed.run('install');
  const original=changed.window.__aftNormalSkipProbe;
  delete changed.window.__aftNormalSkipCleanup;
  changed.window.__aftNormalSkipCleanup={token,probe:original,close:()=>true};
  await assert.rejects(changed.run('read'),/foreign or replaced/);
  await assert.rejects(changed.run('cleanup'),/SK1 probe cleanup failed/);
  assert(original.controller.signal.aborted);
  assert([...changed.listeners.values()].every(s=>s.size===0));
  const foreign=page();await foreign.run('install');foreign.location.pathname='/ws/LOCALMODE/chat/agt_other';
  await assert.rejects(foreign.run('read'),/foreign or replaced/);
  await foreign.run('cleanup');assert([...foreign.listeners.values()].every(s=>s.size===0));
})().catch(e=>{console.error(e);process.exitCode=1;});
"""
    subprocess.run(["node", "-e", node_test], input=json.dumps([PROBE_JS, STATE_JS]),
                   text=True, check=True, timeout=10)


def main():
    command, *args = sys.argv[1:]
    assert not args and command.replace("-", "_") in \
        ("preflight", "claim", "start", "tab", "entered", "mouse_focus", "cleanup", "self_test")
    globals()[command.replace("-", "_")]()


if __name__ == "__main__":
    main()
