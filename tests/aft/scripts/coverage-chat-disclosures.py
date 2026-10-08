"""Shared read-only layout assertions for saved Chat disclosure rows."""

import math


def rect_ok(rect):
    assert isinstance(rect, dict) and all(
        isinstance(rect.get(k), (int, float)) and math.isfinite(rect[k])
        for k in ("x", "y", "width", "height")
    ), "missing or invalid disclosure geometry"
    assert rect["width"] > 0 and rect["height"] > 0, "disclosure has no visible box"


def inside(inner, outer):
    rect_ok(inner)
    rect_ok(outer)
    assert inner["x"] >= outer["x"] - 1 and \
        inner["x"] + inner["width"] <= outer["x"] + outer["width"] + 1, \
        "disclosure control overflows its row"


def assert_thinking_presentation(items, rows, expanded):
    assert len(items) == len(rows), "Thinking presentation differs from saved item count"
    for item, row in zip(items, rows):
        assert item.get("event_id") and item.get("kind") == "item.completed" and \
            item.get("payload", {}).get("itemKind") == "reasoning", "foreign reasoning receipt"
        text = item["payload"].get("text")
        assert text is None or isinstance(text, str), "invalid saved reasoning text"
        available = bool(text and text.strip())
        assert row["heading"] == "Thinking" and row["status"] == "completed", row
        if available:
            assert row["expanded"] == ("true" if expanded else "false"), "reasoning toggle missing"
            assert row["body"] == (text if expanded else None), "reasoning body differs from saved text"
        else:
            assert row["preview"] == "No reasoning text available", "empty reasoning lacks explanation"
            assert row["expanded"] is None and row["body"] is None, "empty reasoning falsely expands"
        geometry = row["geometry"]
        inside(geometry["row"], geometry["parent"])
        assert geometry["row"]["height"] >= 32, "Thinking disclosure is too small"
        if available:
            inside(geometry["chevron"], geometry["row"])
            gap = geometry["row"]["x"] + geometry["row"]["width"] - \
                geometry["chevron"]["x"] - geometry["chevron"]["width"]
            assert 0 <= gap <= 12, "Thinking chevron is detached from its preview"
            assert geometry["tabIndex"] == 0, "Thinking disclosure is not keyboard focusable"


def assert_reply_actions(stages):
    assert set(stages) == {"rest", "hover", "leave", "focus"}, "reply action stages incomplete"
    before = stages["rest"]
    for name, stage in stages.items():
        assert stage["copy"] and stage["time"].strip(), "reply copy or timestamp missing"
        assert stage["path"] == before["path"] and stage["text"] == before["text"], \
            "reply or route changed while measuring controls"
        visible = name in ("hover", "focus")
        assert stage["opacity"] == ("1" if visible else "0"), "reply controls have wrong visibility"
        assert stage["pointerEvents"] == ("auto" if visible else "none"), "hidden reply actions remain clickable"
        assert stage["hovered"] == (name == "hover"), "wrong hover target"
        assert stage["focused"] == (name == "focus"), "wrong keyboard focus target"
        inside(stage["pill"], stage["row"])
        inside(stage["button"], stage["pill"])
        rect_ok(stage["markdown"])
        gap = stage["pill"]["y"] - stage["markdown"]["y"] - stage["markdown"]["height"]
        assert 0 <= gap <= 8, "reply actions are not immediately below the full reply"
        for part in ("row", "pill", "markdown"):
            assert all(abs(stage[part][k] - before[part][k]) <= 1 for k in before[part]), \
                "hover or focus moved the reply layout"
        if visible:
            assert stage["button"]["y"] >= 0 and \
                stage["button"]["y"] + stage["button"]["height"] <= stage["viewportHeight"], \
                "reply copy is offscreen after scrolling to its footer"


STARTED_LAYOUT = r"""(() => {
  const c=document.querySelector('[data-testid=chat-transcript]');
  const rect=x=>{if(!x)return null;const r=x.getBoundingClientRect();
    return {x:r.x,y:r.y,width:r.width,height:r.height};};
  return {path:location.pathname, width:c?.clientWidth,scrollWidth:c?.scrollWidth,
    markers:[...(c?.querySelectorAll('[data-testid=started-marker]')||[])].map(m=>({
      row:rect(m),parent:rect(m.parentElement),
      links:[...m.querySelectorAll('a[href]')].map(a=>({href:a.getAttribute('href'),rect:rect(a),
        radius:parseFloat(getComputedStyle(a).borderRadius),tabIndex:a.tabIndex})),
      buttons:[...m.querySelectorAll('button[aria-expanded]')].map(b=>({rect:rect(b),
        radius:parseFloat(getComputedStyle(b).borderRadius),tabIndex:b.tabIndex,
        expanded:b.getAttribute('aria-expanded')}))}))};})()"""


def assert_started_layout(snapshot, workspace, lead_id, child_ids):
    from urllib.parse import quote
    assert snapshot["path"] == f"/ws/{workspace}/chat/{lead_id}", "foreign Started route"
    assert snapshot["width"] > 0 and snapshot["scrollWidth"] <= snapshot["width"] + 1, \
        "Started content overflows Chat"
    assert snapshot["markers"], "Started disclosure missing"
    hrefs = []
    for marker in snapshot["markers"]:
        inside(marker["row"], marker["parent"])
        assert marker["row"]["height"] >= 32, "Started row is too small"
        for link in marker["links"]:
            inside(link["rect"], marker["row"])
            assert link["radius"] > 0 and link["tabIndex"] == 0, "child link is not a focusable chip"
            hrefs.append(link["href"])
        for button in marker["buttons"]:
            inside(button["rect"], marker["row"])
            assert button["radius"] > 0 and button["tabIndex"] == 0 and \
                button["expanded"] in ("false", "true"), "tool count is not a separate focusable disclosure"
    expected = [f"/ws/{quote(workspace, safe='')}/chat/{quote(child, safe='')}" for child in child_ids]
    assert sorted(hrefs) == sorted(expected), "Started link targets differ from exact saved child IDs"


def assert_full_reply(saved, dom, projection):
    assert isinstance(saved, str) and saved, "saved full reply missing"
    length = len(saved.encode("utf-16-le")) // 2
    assert projection.get("mode") == "terminal-full" and projection.get("version") == 1 and \
        projection.get("sourceUtf16") == length, "full-reply projection is not bound to saved source"
    assert dom.get("showAll") is False, "reply still requires Show all"
    assert dom.get("answer") == projection.get("terminal") and dom["answer"], \
        "rendered reply is truncated or differs from full saved Markdown"
