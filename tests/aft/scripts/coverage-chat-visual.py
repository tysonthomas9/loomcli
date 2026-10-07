#!/usr/bin/env python3
"""Read-only saved-event and browser oracles for the real chat visual journeys."""

import base64
import json
import os
import re
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


def evaluate(js):
    raw = browser("eval", "-b", base64.b64encode(js.encode()).decode())
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
    return f"VISUAL_{RUN} " + prefix + "\n" + ("The narrow chat bubble keeps this exact harmless sentence. " * 160)


def fill_long():
    source = long_text()
    assert len(source) > 5000
    browser("fill", "textarea[aria-label=Message]", "")
    assert evaluate("document.querySelector('textarea[aria-label=Message]')?.value") == "", \
        "multiline draft was not cleared through the real composer"
    browser("click", "textarea[aria-label=Message]")
    assert evaluate("document.activeElement===document.querySelector('textarea[aria-label=Message]')"), \
        "long input textarea did not gain keyboard focus"
    previous = ""
    for index, prefix in enumerate(long_text_prefixes(source)):
        try:
            browser("keyboard", "inserttext", prefix[len(previous):], timeout=20)
        except subprocess.TimeoutExpired as exc:
            raise AssertionError(f"real keyboard insertion stalled at chunk {index + 1}") from exc
        assert evaluate("document.querySelector('textarea[aria-label=Message]')?.value") == prefix, \
            f"real keyboard insertion changed/truncated the long input at chunk {index + 1}"
        previous = prefix
    write("input-source.json", {"text": source, "length": len(source)})


def long_text_prefixes(source, size=1024):
    assert source and size > 0
    return [source[:end] for end in range(size, len(source), size)] + [source]


def self_test_long_text_prefixes():
    source = long_text()
    prefixes = long_text_prefixes(source)
    assert len(source) > 5000 and all(0 < len(p) <= len(source) for p in prefixes)
    assert prefixes[-1] == source and all(len(b) - len(a) <= 1024 for a, b in zip(["", *prefixes[:-1]], prefixes))
    assert "".join(b[len(a):] for a, b in zip(["", *prefixes[:-1]], prefixes)) == source
    assert '<img src="x" onerror="alert(1)">' in source and "\n" in source


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


def skip_link(stage):
    current("input")
    result = evaluate("""(() => {const a=document.querySelector('a[href="#main-content"]');
      const m=document.querySelector('main#main-content'), r=a?.getBoundingClientRect();
      return {text:a?.textContent?.trim(), focused:document.activeElement===a,
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
        assert result["focused"] and not result["inViewport"], result
    else:
        raise ValueError(stage)
    write(f"input-skip-link-{stage}.json", result)
    shot("input", f"skip-link-{stage}")


def mouse_focus_skip_link():
    browser("click", "main#main-content")
    assert evaluate("(() => { const a=document.querySelector('a[href=\"#main-content\"]'); a.focus(); return document.activeElement===a; })()")
    skip_link("mouse_focus")


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


MOTION_JS = r"""(() => {
  if (window.__aftChatVisual) throw Error('visual probe already armed');
  window.__aftChatVisualLive=null;
  const p = {start:Date.now(), frames:[], samples:[], shifts:[], maxMs:180000,
    marker:__MARKER__,
    stopped:false, lastText:'', sawCaret:false, sawWorking:false};
  const root=document.querySelector('section[aria-label="Agent chat"]');
  if(!root)throw Error('real Agent Chat root missing before send');
  const transcript = () => document.querySelector('[data-testid=chat-transcript]');
  const state = () => { const t=transcript(); const a=[...(t?.querySelectorAll('li[data-kind=agent]')||[])].at(-1);
    const text=a?.querySelector('[data-testid=chat-markdown]')?.textContent||'';
    const caret=!!a?.querySelector('[data-streaming-caret]');
    const working=!!t?.querySelector('[data-testid=working-row]');
    const gap=t?Math.max(0,t.scrollHeight-t.scrollTop-t.clientHeight):null;
    return {at:Date.now(),text,caret,working,gap,stop:!!document.querySelector('form button[title="Stop the running turn"]')}; };
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
  const frame=() => { if(p.stopped)return; captureLive(); const s=state();
    if(s.text!==p.lastText) {const before=p.lastText.trim().split(/\s+/).filter(Boolean).length;
      const after=s.text.trim().split(/\s+/).filter(Boolean).length;
      const marker=s.text.includes(p.marker);
      p.frames.push({at:s.at,chars:s.text.length,words:after,addedWords:Math.max(0,after-before),
        marker,...(marker?{text:s.text}:{})}); p.lastText=s.text;}
    p.sawCaret ||= s.caret; p.sawWorking ||= s.working;
    if(Date.now()-p.start>p.maxMs) {p.stopped=true;p.liveObserver.disconnect();return;} requestAnimationFrame(frame); };
  p.timer=setInterval(()=>{ if(p.stopped){clearInterval(p.timer);return;}
    const s=state(); p.samples.push({at:s.at,chars:s.text.length,caret:s.caret,working:s.working,gap:s.gap,stop:s.stop});
  },100);
  try { p.observer=new PerformanceObserver(list=>{ for(const e of list.getEntries())
    if(!e.hadRecentInput) p.shifts.push({at:Date.now(),value:e.value}); });
    p.observer.observe({type:'layout-shift', buffered:false}); }
  catch(e) {p.observerError=String(e);}
  window.__aftChatVisual=p; requestAnimationFrame(frame); return 'armed';
})()"""


def motion_start():
    assert evaluate(MOTION_JS.replace("__MARKER__", json.dumps(f"VISUAL_END_{RUN}"))) == "armed"


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


def motion_finish(case):
    current(case)
    probe = evaluate("""(() => { const p=window.__aftChatVisual; if(!p)throw Error('motion probe missing');
      p.stopped=true; clearInterval(p.timer); p.observer?.disconnect(); p.liveObserver?.disconnect();
      const c=document.querySelector('[data-testid=chat-transcript]');
      const a=[...(c?.querySelectorAll('li[data-kind=agent]')||[])].at(-1);
      const finalText=a?.querySelector('[data-testid=chat-markdown]')?.textContent||'';
      return {start:p.start,frames:p.frames,finalText,samples:p.samples,shifts:p.shifts,
        sawCaret:p.sawCaret,sawWorking:p.sawWorking,observerError:p.observerError||null}; })()""")
    evs = events(case)
    delivered = [e for e in evs if e["kind"] == "message.delivered" and "VISUAL_RENDER" in e["payload"].get("text", "")]
    assert len(delivered) == 1, delivered
    end = [e for e in evs if e["kind"] == "agent.turn_completed" and e["seq"] > delivered[0]["seq"]]
    assert len(end) == 1, end
    replies = [e for e in evs if e["kind"] == "item.completed" and e["payload"].get("itemKind") == "message" and delivered[0]["seq"] < e["seq"] < end[0]["seq"]]
    assert replies, "real turn saved no assistant message"
    answer = "\n".join(e["payload"].get("text", "") for e in replies)
    assert len(answer.split()) >= 300, f"model supplied only {len(answer.split())} words; 300-word motion criterion unverified"
    marker = f"VISUAL_END_{RUN}"
    assert probe["observerError"] is None, probe["observerError"]
    assert probe["sawCaret"] and probe["sawWorking"], "streaming caret/working row were not observed"
    frames = probe["frames"]
    assert len(frames) > 3 and len(set(f["chars"] for f in frames)) > 3, "no measured real text progression"
    assert max(f["addedWords"] for f in frames) <= 2, "visible block exceeded two words in a rendered frame"
    assert sum(s["value"] for s in probe["shifts"]) == 0, "non-input layout shift during streaming"
    assert probe["samples"] and all(s["gap"] == 0 for s in probe["samples"] if s["stop"]), "live follow lost the bottom"
    marker_frame = final_text_frame(answer, frames, probe["finalText"], marker)
    # The completed event timestamp is saved by the product. Browser Date.now
    # and serve share this host clock; this proven-final DOM frame is the bound.
    end_at = end[0].get("created_at")
    assert end_at, "turn completion lacks a saved timestamp"
    from datetime import datetime
    turn_ms = datetime.fromisoformat(end_at.replace("Z", "+00:00")).timestamp() * 1000
    lag_ms = final_text_lag(marker_frame, turn_ms)
    assert evaluate("!document.querySelector('[data-streaming-caret], [data-testid=working-row]')"), "caret or working row remained after turn"
    write("render-motion.json", {"probe": probe, "turn_completed": end[0], "answer": answer, "final_frame": marker_frame, "turn_ms": turn_ms, "lag_ms": lag_ms})


def turn_events(case, marker):
    evs = events(case)
    delivered = [e for e in evs if e["kind"] == "message.delivered" and marker in e["payload"].get("text", "")]
    assert len(delivered) == 1, f"expected one actual Chat delivery for {marker}: {delivered}"
    end = next((e for e in evs if e["kind"] == "agent.turn_completed" and e["seq"] > delivered[0]["seq"]), None)
    assert end, "real turn did not complete"
    return delivered[0], end, [e for e in evs if delivered[0]["seq"] < e["seq"] < end["seq"]]


def expand_work():
    for _ in range(20):
        closed = evaluate("document.querySelectorAll('[data-testid=tool-group][aria-expanded=false], [data-testid=work-toggle][aria-expanded=false]').length")
        if not closed:
            break
        browser("click", "[data-testid=tool-group][aria-expanded=false], [data-testid=work-toggle][aria-expanded=false]")
    else:
        raise AssertionError("too many collapsed work groups")
    for _ in range(30):
        closed = evaluate("document.querySelectorAll('[data-testid=tool-call] [role=button][aria-expanded=false], [data-testid=reasoning] [role=button][aria-expanded=false]').length")
        if not closed:
            break
        browser("click", "[data-testid=tool-call] [role=button][aria-expanded=false], [data-testid=reasoning] [role=button][aria-expanded=false]")
    else:
        raise AssertionError("too many collapsed work entries")


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
    saved = [{"event_id": e["event_id"], "text": e["payload"].get("text", "")}
             for e in between if e["kind"] == "item.completed" and e["payload"].get("itemKind") == "reasoning"]
    assert saved and all(item["event_id"] and item["text"].strip() for item in saved), "missing saved reasoning item/content"
    assert len({item["event_id"] for item in saved}) == len(saved), "duplicate reasoning EventID"
    return saved


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


def reasoning_check(stage):
    assert stage in ("before-reload", "reloaded"), stage
    current("render")
    _, _, between = turn_events("render", "VISUAL_RENDER")
    if not any(e["kind"] == "item.completed" and e["payload"].get("itemKind") == "reasoning" for e in between):
        write("reasoning-blocked.json", {"status": "blocked", "prerequisite": "actual selected provider must emit saved item.completed reasoning text"})
        raise AssertionError("BLOCKED: selected real provider emitted no saved reasoning item; Thinking UI cannot be claimed")
    saved = reasoning_receipts(between)
    if stage == "reloaded":
        before = json.loads((WORK / "render-reasoning-before-reload.json").read_text())
        assert_reasoning_saved_after_reload(before["saved"], saved)
    # Reveal grouped rows without opening the Thinking bodies yet.
    for _ in range(20):
        if not evaluate("document.querySelectorAll('[data-testid=tool-group][aria-expanded=false], [data-testid=work-toggle][aria-expanded=false]').length"):
            break
        browser("click", "[data-testid=tool-group][aria-expanded=false], [data-testid=work-toggle][aria-expanded=false]")
    else:
        raise AssertionError("too many collapsed work groups")
    collapsed = evaluate(REASONING_DOM)
    assert_reasoning_rows(saved, collapsed, False)
    shot("render", f"thinking-{stage}-preview")
    expand_work()
    expanded = evaluate(REASONING_DOM)
    assert_reasoning_rows(saved, expanded, True)
    shot("render", f"thinking-{stage}-expanded")
    write(f"render-reasoning-{stage}.json", {"saved": saved, "collapsed": collapsed, "expanded": expanded})


def self_test_reasoning():
    item = {"event_id": "reasoning-1", "text": "## **First** line\nFull second line"}
    event = {"event_id": item["event_id"], "kind": "item.completed",
             "payload": {"itemKind": "reasoning", "text": item["text"]}}
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
    try:
        actual = evaluate("navigator.clipboard.readText()")
    except (subprocess.CalledProcessError, json.JSONDecodeError) as exc:
        write(f"clipboard-{kind}-blocked.json", {"status": "blocked", "prerequisite": "browser clipboard read permission", "error": str(exc)})
        raise AssertionError("BLOCKED: browser denied real clipboard readback") from exc
    assert actual == expected, f"{kind} copy bytes differ from actual rendered/saved source"
    copied = evaluate("!!document.querySelector('[data-testid=chat-transcript] button[aria-label=Copied]') || !![...document.querySelectorAll('[data-testid=chat-transcript] button')].find(b=>b.textContent==='Copied')")
    assert copied, f"{kind} copy showed no success feedback"
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


def input_check(stage):
    current("input")
    evs = events("input")
    source = long_text()
    delivered = [e for e in evs if e["kind"] == "message.delivered" and e["payload"].get("text") == source]
    assert len(delivered) == 1, "long literal text was not delivered exactly once through Chat"
    assert len(source) > 8000, "source did not cross the LongText Show all threshold"
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
    elif stage == "expanded":
        assert dom["collapsed"] == "false" and dom["showAll"], dom
        assert source[:8000] in dom["text"] and len(dom["text"]) < len(source), dom
    elif stage in ("all", "reloaded"):
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


def locale_stress():
    current("input")
    # Chromium's as-IN ICU data may fall back to an English short time. The
    # exact long format from the UI7 task is a local formatting stimulus only;
    # the underlying user message and timestamp remain real saved events.
    evaluate("(() => { window.__aftOriginalTime = Date.prototype.toLocaleTimeString; Date.prototype.toLocaleTimeString = () => 'অপৰাহ্ন ১২.৫৯'; return true; })()")
    theme = evaluate("document.documentElement.dataset.theme")
    opposite = "light" if theme == "dark" else "dark"
    browser("click", 'button[aria-label="Switch to ' + opposite + ' mode"]')
    observed = evaluate("""(() => {const r=[...document.querySelectorAll('[data-testid=chat-transcript] li[data-kind=user]')].at(-1);
      return r?.querySelector('[data-testid=message-actions]')?.textContent||'';})()""")
    if "অপৰাহ্ন ১২.৫৯" not in observed:
        write("locale-layout-blocked.json", {"status": "blocked", "prerequisite": "a browser locale or formatter that renders the documented long time", "observed": observed})
        raise AssertionError("BLOCKED: long locale format was not rendered by current Chat")
    measurements = []
    for width in (130, 105, 80):
        evaluate("(() => { document.querySelector('[data-testid=chat-transcript]').style.setProperty('--chat-column', '" + str(width) + "px'); return true; })()")
        browser("hover", "[data-testid=chat-transcript] li[data-kind=user]")
        result = evaluate("""(() => {const r=[...document.querySelectorAll('[data-testid=chat-transcript] li[data-kind=user]')].at(-1);
          const p=r.querySelector('[data-testid=message-actions]'),b=r.querySelector('[class*=userBubble]'),c=p.querySelector('button[aria-label="Copy your message"]');
          const q=e=>{const x=e.getBoundingClientRect();return {left:x.left,right:x.right,width:x.width};};
          return {row:q(r),pill:q(p),bubble:q(b),copy:q(c),time:p.textContent};})()""")
        assert abs(result["row"]["width"] - width) <= 1, result
        assert result["pill"]["left"] >= result["row"]["left"] - 1 and result["pill"]["right"] <= result["bubble"]["left"] + 1, result
        assert result["copy"]["width"] >= 14 and result["copy"]["right"] <= result["pill"]["right"] + 1, result
        measurements.append(result)
        shot("input", f"long-locale-{width}")
    evaluate("(() => {document.querySelector('[data-testid=chat-transcript]').style.removeProperty('--chat-column'); Date.prototype.toLocaleTimeString=window.__aftOriginalTime; delete window.__aftOriginalTime; return true;})()")
    write("input-locale-layout.json", {"stimulus": "documented long as-IN time via local formatter override", "measurements": measurements})


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
