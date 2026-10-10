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
  const nav = document.querySelector('nav[aria-label="Primary"]');
  const shown = Array.from(nav.querySelectorAll('[data-more-hint]')).filter((h) => {
    const cs = getComputedStyle(h), r = h.getBoundingClientRect();
    return cs.visibility !== 'hidden' && Number(cs.opacity) > 0.5 && r.width > 0 && r.height > 0;
  });
  // A hint sits on its own side of the switcher's frame (the scroller is
  // centred in it; on the smallest phones the hint sits up to 12 px outside a
  // one-item frame), and on nothing it points past: no shown part of an
  // item, no other rail button's icon.
  const marks = [
    ...Array.from(s.querySelectorAll('button')).map((b) => { const r = b.getBoundingClientRect();
      return { label: b.getAttribute('aria-label'), left: Math.max(r.left, w.left), right: Math.min(r.right, w.right), r }; }),
    ...Array.from(nav.querySelectorAll('button svg')).filter((i) => !s.contains(i)).map((i) => { const r = i.getBoundingClientRect();
      return { label: i.closest('button').getAttribute('aria-label') + ' icon', left: r.left, right: r.right, r }; }),
  ].filter((m) => m.right - m.left > 0.5);
  const misplaced = shown.flatMap((h) => { const r = h.getBoundingClientRect(), side = h.dataset.moreHint;
    const f = h.parentElement.getBoundingClientRect(), mid = (r.left + r.right) / 2;
    const ownSide = side === 'left' ? mid < (f.left + f.right) / 2 : mid > (f.left + f.right) / 2;
    const near = r.left >= f.left - 12.5 && r.right <= f.right + 12.5;
    return [...(ownSide && near ? [] : [side + ' hint off its side of the switcher']),
      ...marks.filter((m) => r.left < m.right - 0.5 && r.right > m.left + 0.5 && r.top < m.r.bottom && r.bottom > m.r.top)
        .map((m) => side + ' hint over ' + m.label)]; });
  const a = s.querySelector('button[data-active]')?.getBoundingClientRect();
  return { scrollLeft: s.scrollLeft, max: s.scrollWidth - s.clientWidth, cut, misplaced,
    hints: shown.map((h) => h.dataset.moreHint).sort(),
    activeWhole: !!a && a.left >= w.left - 0.5 && a.right <= w.right + 0.5,
    want: ['left', 'right'].filter((x) => hidden.has(x)),
    items: s.querySelectorAll('button[aria-label^="Switch to "]').length }; })()"""


def wait_for(js, what):
    for _ in range(40):
        if evaluate(js):
            return
        time.sleep(0.25)
    sys.exit(f"timed out waiting for {what}")


def settled_switcher(label):
    prev = None
    for _ in range(40):
        v = evaluate(SWITCHER)
        if prev is not None and v["scrollLeft"] == prev:
            check(not v["cut"], f"{label}: switcher items cut", v)
            check(v["hints"] == v["want"], f"{label}: more hints", v)
            check(not v["misplaced"], f"{label}: more hints misplaced", v)
            return v
        prev = v["scrollLeft"]
        time.sleep(0.25)
    sys.exit(f"{label}: switcher never settled")


def layout(width, height):
    browser("set", "viewport", str(width), str(height))
    wait_for(f"innerWidth === {width} && innerHeight === {height}", f"{width}x{height} viewport")
    # Reopen the chat at this size, so the switcher starts as a fresh load does.
    browser("reload")
    wait_for("!!document.querySelector('section[aria-label=\"Agent chat\"] header h2')?.textContent && "
             "!!document.querySelector('textarea[aria-label=Message]') && "
             "document.querySelectorAll('nav[aria-label=\"Primary\"] [aria-label=\"Workspace selector\"] button[aria-label^=\"Switch to \"]').length >= 4",
             f"the chat reopened at {width}px")
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
    check(first["activeWhole"], f"{width}px: the open workspace is not wholly in view as loaded", first)
    if width < 557:
        check(first["max"] > 0, f"{width}px: nothing off-screen in the switcher", first)
    for x in range(0, first["max"] + 14, 13):
        evaluate(f"(() => {{ document.querySelector('nav[aria-label=\"Primary\"] [aria-label=\"Workspace selector\"]').scrollTo({{ left: {x} }}); return true; }})()")
        settled_switcher(f"{width}px scrolled to {x}")
    print(f"{width}x{height}: no overflow, one-row rail, whole switcher items, name and composer clear")


PILL = r"""(() => { const r = document.querySelector('[data-aft-row="%s"]');
  const p = r.querySelector('[data-testid=message-actions]'), b = r.querySelector('[class*=userBubble]');
  const c = p?.querySelector('button[aria-label="Copy your message"]');
  const rect = (e) => { const q = e.getBoundingClientRect(); return { left: q.left, top: q.top, right: q.right, bottom: q.bottom, width: q.width, height: q.height }; };
  return { row: rect(r), bubble: rect(b), pill: p && rect(p), copy: c && rect(c),
    shown: !!p && getComputedStyle(p).opacity === '1', vh: innerHeight }; })()"""

SHORT, LONG = "ok", "A longer message that wraps"



def pill():
    found = evaluate("""(() => { const rows = Array.from(document.querySelectorAll('[data-testid=chat-transcript] li[data-kind=user]'));
      const tag = (name, match) => { const r = rows.filter(match); if (r.length === 1) r[0].dataset.aftRow = name; return r.length; };
      return { short: tag('short', (r) => r.querySelector('[class*=userBody]')?.textContent.trim() === '%s'),
        long: tag('long', (r) => (r.querySelector('[class*=userBody]')?.textContent || '').startsWith('%s')) }; })()""" % (SHORT, LONG))
    check(found == {"short": 1, "long": 1}, "one short and one long user row", found)
    for width in (130, 105, 80):
        evaluate("(() => { document.querySelector('[data-testid=chat-transcript]').style.setProperty('--chat-column', '%dpx'); return true; })()" % width)
        heights = {}
        for row in ("short", "long"):
            sel = f'[data-aft-row="{row}"]'
            # The chat may still be settling after the column change (it keeps
            # to the bottom), so scroll and hover again until the row is on
            # screen with its pill shown.
            for _ in range(20):
                browser("scrollintoview", sel)
                browser("hover", sel)
                time.sleep(0.15)
                m = evaluate(PILL % row)
                if m["shown"] and m["bubble"]["top"] >= 0 and m["bubble"]["bottom"] <= m["vh"]:
                    break
            label = f"{row} row at {width}px"
            check(m["pill"] and m["copy"] and m["shown"], f"{label}: hover pill not shown", m)
            p, b, r, c = m["pill"], m["bubble"], m["row"], m["copy"]
            apart = p["right"] <= b["left"] + 0.5 or p["left"] >= b["right"] - 0.5 or \
                p["bottom"] <= b["top"] + 0.5 or p["top"] >= b["bottom"] - 0.5
            check(apart, f"{label}: pill overlaps the bubble", m)
            check(p["left"] >= r["left"] - 0.5 and p["right"] <= r["right"] + 0.5 and
                  p["top"] >= r["top"] - 0.5 and p["bottom"] <= r["bottom"] + 0.5, f"{label}: pill outside its row", m)
            heights[row] = b["height"]
            check(c["width"] >= 14 and c["left"] >= p["left"] - 0.5 and c["right"] <= p["right"] + 0.5,
                  f"{label}: copy button cut", m)
        check(heights["long"] >= 2 * heights["short"], f"{width}px: the long message does not wrap", heights)
    evaluate("(() => { document.querySelector('[data-testid=chat-transcript]').style.removeProperty('--chat-column'); return true; })()")
    print("pill clear of the bubble for the short and the long message at 130, 105 and 80px")


if __name__ == "__main__":
    if sys.argv[1:2] == ["layout"]:
        layout(int(sys.argv[2]), int(sys.argv[3]))
    elif sys.argv[1:2] == ["pill"]:
        pill()
    else:
        sys.exit(__doc__)
