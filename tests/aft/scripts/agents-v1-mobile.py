#!/usr/bin/env python3
"""Geometry checks for the agents-v1-mobile suite, run in the suite's browser.

  layout <width> <height>  the open chat at that viewport (MB1, MB1b): nothing
                           past the right edge, a one-row rail, a switcher that
                           settles on whole items with a hint where more is
                           off-screen, a readable agent name, an uncovered composer
  pill                     the user-message hover pill (UI7, UI7b) at chat
                           columns of 130, 105 and 80 px, for each user row

Each check exits nonzero with what it measured when an assertion fails.
"""
import base64
import json
import os
import subprocess
import sys
import time


def browser(*args):
    return subprocess.check_output(
        ["agent-browser", "--session", os.environ["AFT_SESSION"], *args], text=True
    ).strip()


def evaluate(js):
    return json.loads(browser("eval", "-b", base64.b64encode(js.encode()).decode()))


def check(ok, what, seen):
    if not ok:
        sys.exit(f"{what}: {json.dumps(seen)}")


# Every rendered element whose visible right edge is past the viewport. An
# element inside a scroll or clip container only counts where it shows; a clip
# at the viewport edge itself does not hide the bug.
OVERFLOW = r"""(() => { const out = [], vw = innerWidth;
  for (const el of document.body.querySelectorAll('*')) {
    const r = el.getBoundingClientRect();
    if (!r.width || !r.height || getComputedStyle(el).visibility === 'hidden') continue;
    let right = r.right;
    for (let p = el.parentElement; p; p = p.parentElement) {
      const pr = p.getBoundingClientRect().right;
      if (getComputedStyle(p).overflowX !== 'visible' && pr < vw - 0.5) right = Math.min(right, pr);
    }
    if (right > vw + 0.5 && r.left < right) out.push(el.tagName.toLowerCase() + '.' + String(el.className) + ' right=' + Math.round(right));
  }
  return { out, scrollWidth: document.documentElement.scrollWidth, vw }; })()"""

LAYOUT = r"""(() => {
  const nav = document.querySelector('nav[aria-label="Primary"]'), box = nav.getBoundingClientRect();
  const offRow = Array.from(nav.querySelectorAll('button')).filter((b) => {
    const r = b.getBoundingClientRect();
    return r.width && Math.abs(r.top + r.height / 2 - (box.top + box.height / 2)) >= 4;
  }).map((b) => b.getAttribute('aria-label'));
  const h = document.querySelector('section[aria-label="Agent chat"] header h2');
  const form = document.querySelector('textarea[aria-label=Message]')?.closest('form');
  const f = form?.getBoundingClientRect();
  const covered = form ? Array.from(form.querySelectorAll('button, textarea')).flatMap((b) => {
    const r = b.getBoundingClientRect();
    if (!r.width || !r.height) return [];
    const top = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
    return top && b.contains(top) ? [] : [b.getAttribute('aria-label') || b.tagName];
  }) : ['no composer'];
  return { railHeight: box.height, railBottom: box.bottom, railTop: box.top, vh: innerHeight, offRow,
    name: h?.textContent || '', nameCut: !h || h.scrollWidth > h.clientWidth,
    formBottom: f ? f.bottom : null, covered }; })()"""

# Which switcher items show whole, cut or not at all, and which sides show a
# visible "more" hint (MB1b).
SWITCHER = r"""(() => {
  const s = document.querySelector('nav[aria-label="Primary"] [aria-label="Workspace selector"]');
  const w = s.getBoundingClientRect(), cut = [], hidden = new Set();
  for (const b of s.querySelectorAll('button')) {
    const r = b.getBoundingClientRect(), shown = Math.min(r.right, w.right) - Math.max(r.left, w.left);
    if (shown <= 0.5) hidden.add(r.right <= w.left + 0.5 ? 'left' : 'right');
    else if (shown < r.width - 0.5) cut.push(b.getAttribute('aria-label') + ' shows ' + shown.toFixed(1));
  }
  const hints = Array.from(document.querySelectorAll('nav[aria-label="Primary"] [data-more-hint]')).filter((h) => {
    const cs = getComputedStyle(h), r = h.getBoundingClientRect();
    return cs.visibility !== 'hidden' && Number(cs.opacity) > 0.5 && r.width > 0 && r.height > 0;
  }).map((h) => h.dataset.moreHint).sort();
  return { scrollLeft: s.scrollLeft, max: s.scrollWidth - s.clientWidth, cut, hints,
    want: ['left', 'right'].filter((x) => hidden.has(x)),
    items: s.querySelectorAll('button[aria-label^="Switch to "]').length }; })()"""


def settled_switcher(label):
    prev = None
    for _ in range(40):
        v = evaluate(SWITCHER)
        if prev is not None and v["scrollLeft"] == prev:
            check(not v["cut"], f"{label}: switcher items cut", v)
            check(v["hints"] == v["want"], f"{label}: more hints", v)
            return v
        prev = v["scrollLeft"]
        time.sleep(0.25)
    sys.exit(f"{label}: switcher never settled")


def layout(width, height):
    browser("set", "viewport", str(width), str(height))
    time.sleep(1)
    o = evaluate(OVERFLOW)
    check(not o["out"] and o["scrollWidth"] <= o["vw"], f"{width}px: elements past the right edge", o)
    v = evaluate(LAYOUT)
    check(v["railHeight"] <= 64 and abs(v["railBottom"] - v["vh"]) <= 1, f"{width}px: rail is not one bottom row", v)
    check(not v["offRow"], f"{width}px: rail controls off its row", v)
    check(v["name"] and not v["nameCut"], f"{width}px: agent name cut off", v)
    check(v["formBottom"] is not None and v["formBottom"] <= v["railTop"] + 0.5 and not v["covered"],
          f"{width}px: composer covered", v)
    first = settled_switcher(f"{width}px as loaded")
    check(first["items"] >= 4, f"{width}px: the switcher needs four workspaces", first)
    if width < 557:
        check(first["max"] > 0, f"{width}px: nothing off-screen in the switcher", first)
    for x in range(0, first["max"] + 14, 13):
        evaluate(f"(() => {{ document.querySelector('nav[aria-label=\"Primary\"] [aria-label=\"Workspace selector\"]').scrollTo({{ left: {x} }}); return true; }})()")
        settled_switcher(f"{width}px scrolled to {x}")
    print(f"{width}x{height}: no overflow, one-row rail, whole switcher items, name and composer clear")


PILL = r"""(() => { const r = document.querySelector('[data-aft-row="%s"]');
  const p = r.querySelector('[data-testid=message-actions]'), b = r.querySelector('[class*=userBubble]');
  const c = p?.querySelector('button[aria-label="Copy your message"]');
  const rect = (e) => { const q = e.getBoundingClientRect(); return { left: q.left, top: q.top, right: q.right, bottom: q.bottom, width: q.width }; };
  return { row: rect(r), bubble: rect(b), pill: p && rect(p), copy: c && rect(c),
    shown: !!p && getComputedStyle(p).opacity !== '0' }; })()"""


def pill():
    rows = evaluate("""(() => { const rows = Array.from(document.querySelectorAll('[data-testid=chat-transcript] li[data-kind=user]'));
      rows.forEach((r, i) => r.dataset.aftRow = 'u' + i); return rows.map((r) => r.textContent.length); })()""")
    check(len(rows) >= 2, "two user rows (short and long)", rows)
    for width in (130, 105, 80):
        evaluate("(() => { document.querySelector('[data-testid=chat-transcript]').style.setProperty('--chat-column', '%dpx'); return true; })()" % width)
        for i in range(len(rows)):
            sel = f'[data-aft-row="u{i}"]'
            browser("scrollintoview", sel)
            browser("hover", sel)
            time.sleep(0.3)
            m = evaluate(PILL % f"u{i}")
            label = f"row u{i} at {width}px"
            check(m["pill"] and m["copy"] and m["shown"], f"{label}: hover pill not shown", m)
            p, b, r, c = m["pill"], m["bubble"], m["row"], m["copy"]
            apart = p["right"] <= b["left"] + 0.5 or p["left"] >= b["right"] - 0.5 or \
                p["bottom"] <= b["top"] + 0.5 or p["top"] >= b["bottom"] - 0.5
            check(apart, f"{label}: pill overlaps the bubble", m)
            check(p["left"] >= r["left"] - 0.5 and p["right"] <= r["right"] + 0.5, f"{label}: pill outside its row", m)
            check(c["width"] >= 14 and c["left"] >= p["left"] - 0.5 and c["right"] <= p["right"] + 0.5,
                  f"{label}: copy button cut", m)
    evaluate("(() => { document.querySelector('[data-testid=chat-transcript]').style.removeProperty('--chat-column'); return true; })()")
    print(f"pill clear of the bubble for {len(rows)} user rows at 130, 105 and 80px")


if __name__ == "__main__":
    if sys.argv[1:2] == ["layout"]:
        layout(int(sys.argv[2]), int(sys.argv[3]))
    elif sys.argv[1:2] == ["pill"]:
        pill()
    else:
        sys.exit(__doc__)
