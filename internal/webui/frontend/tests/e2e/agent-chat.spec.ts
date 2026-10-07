/**
 * AgentChat in a real browser against a mocked Agent API (design v2 §9.2–§9.3):
 * safe text, harness parity, waiting edit/clear, live deltas and ask cards.
 */
import { expect, test } from "@playwright/test";
import type { Page, Route } from "@playwright/test";

const BASE = "**/api/workspaces/w1";
// DF1_SHOTS=<dir> also saves the UI6 screenshots, as agent-tray.spec does.
const SHOTS = process.env.DF1_SHOTS;
const XSS = `<img src=x onerror="window.pwned=1"><script>window.pwned=1</script>`;
const LONG = "y".repeat(5000);

interface Mock {
  agent: Record<string, unknown>;
  events: ReturnType<typeof ev>[];
  writes: { method: string; url: string; key: string | null; body: unknown }[];
  reads: number;
}

let seq = 0;
const ev = (kind: string, payload: object, live = false) => ({
  agent_id: "a1",
  seq: live ? 0 : ++seq,
  event_id: `${kind}:${live ? "live" : seq}`,
  kind,
  turn_id: "t1",
  payload,
  created_at: "",
});

function agent(over: object = {}) {
  return {
    agent_id: "a1",
    name: "lead",
    harness: "opencode",
    state: "idle",
    running_turn_id: null,
    waiting_messages: [],
    open_asks: [],
    ...over,
  };
}

const json = (route: Route, body: unknown, status = 200) =>
  route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });

async function open(page: Page, m: Mock) {
  await page.route("**/api/config", (r) => json(r, { mode: "open" }));
  await page.route(`${BASE}/events/token`, (r) => r.fulfill({ status: 404 }));
  // A route cannot hold an SSE response open, so the page gets an
  // EventSource that stays open until closed; push() sends it frames.
  await page.addInitScript(() => {
    const w = window as unknown as { __sse: EventTarget[] };
    w.__sse = [];
    w.EventSource = class extends EventTarget {
      onopen: ((e: Event) => void) | null = null;
      closed = false;
      constructor(public url: string) {
        super();
        w.__sse.push(this);
        setTimeout(() => this.onopen?.(new Event("open")));
      }
      close() {
        this.closed = true;
      }
    } as unknown as typeof EventSource;
  });
  await page.route(`${BASE}/v1/agents/a1/events*`, (r) =>
    json(r, { events: m.events, snapshot_seq: seq, next: seq, more: false }),
  );
  await page.route(`${BASE}/v1/agents/a1`, (r) => {
    m.reads++;
    return json(r, m.agent);
  });
  await page.route(
    `${BASE}/v1/agents/a1/{messages,messages/waiting,asks/*}`,
    (r) => {
      const req = r.request();
      m.writes.push({
        method: req.method(),
        url: req.url(),
        key: req.headers()["idempotency-key"] ?? null,
        body: req.postDataJSON(),
      });
      if (req.method() === "DELETE") return json(r, { result: "withdrawn" });
      if (req.url().includes("/asks/")) return r.fulfill({ status: 204 });
      return json(
        r,
        { message_id: "m", state: "waiting", replaced: false },
        202,
      );
    },
  );
  await page.goto("/test/agent-chat");
  await expect(page.getByText(String(m.agent.name))).toBeVisible();
}

const mock = (over: Partial<Mock> = {}): Mock => ({
  agent: agent(),
  events: [],
  writes: [],
  reads: 0,
  ...over,
});

// Streams frames on the open EventSource, as "event" frames. A saved event is
// committed first, as on the server, so ListEvents returns it too.
async function push(page: Page, m: Mock, ...frames: ReturnType<typeof ev>[]) {
  m.events.push(...frames.filter((e) => e.seq > 0));
  type Sources = { __sse: (EventTarget & { closed: boolean })[] };
  await page.waitForFunction(() =>
    (window as unknown as Sources).__sse.some((s) => !s.closed),
  );
  await page.evaluate(
    (data) => {
      const s = (window as unknown as Sources).__sse.find((x) => !x.closed)!;
      for (const d of data)
        s.dispatchEvent(new MessageEvent("event", { data: d }));
    },
    frames.map((f) => JSON.stringify(f)),
  );
}

const transcript = (page: Page) => page.getByTestId("chat-transcript");

test.beforeEach(() => {
  seq = 0;
});

test("untrusted and long text render as wrapped text", async ({ page }) => {
  let dialogs = 0;
  page.on("dialog", (d) => {
    dialogs++;
    void d.dismiss();
  });
  await open(
    page,
    mock({
      events: [
        ev("item.completed", { itemKind: "message", text: XSS }),
        ev("item.completed", { itemKind: "message", text: LONG }),
      ],
    }),
  );
  await expect(transcript(page).getByText(XSS)).toBeVisible();
  await expect(transcript(page).locator("img, script")).toHaveCount(0);
  expect(
    await page.evaluate(() => (window as { pwned?: number }).pwned),
  ).toBeUndefined();
  expect(dialogs).toBe(0);
  const overflow = await transcript(page).evaluate(
    (el) => el.scrollWidth - el.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(0);
});

test("OpenCode, codex and Claude render the same fixture except the label", async ({
  page,
}) => {
  const html: string[] = [];
  for (const harness of ["opencode", "codex", "claude"]) {
    seq = 0;
    await page.unrouteAll({ behavior: "ignoreErrors" });
    await open(
      page,
      mock({
        agent: agent({ harness }),
        events: [
          ev("message.delivered", { text: "hi" }),
          ev("item.completed", { itemKind: "tool", text: "ls" }),
          ev("item.completed", { itemKind: "message", text: "hello" }),
          ev("agent.turn_completed", { stopReason: "completed" }),
        ],
      }),
    );
    await expect(page.getByTestId("harness-label")).toHaveText(harness);
    await expect(transcript(page).getByText("hello")).toBeVisible();
    html.push(await transcript(page).innerHTML());
  }
  expect(html[1]).toBe(html[0]);
  expect(html[2]).toBe(html[0]);
});

test("waiting bubble: edit is a new Send with a new key, clear is Withdraw", async ({
  page,
}) => {
  const m = mock({
    agent: agent({
      state: "active",
      waiting_messages: [
        { sender: "user:local", text: "first", since: "" },
        { sender: "user:someone-else", text: "theirs", since: "" },
      ],
    }),
  });
  await open(page, m);
  // Only the caller's own slot (user:local in open mode) has controls.
  await expect(page.getByText("Waiting", { exact: false })).toHaveCount(2);
  await expect(page.getByText("from user:someone-else")).toBeVisible();
  await expect(page.getByRole("button", { name: "Edit" })).toHaveCount(1);
  await expect(page.getByRole("button", { name: "Clear" })).toHaveCount(1);

  const input = page.getByLabel("Message");
  await input.fill("another");
  await input.press("Enter");
  await page.getByRole("button", { name: "Edit" }).click();
  await expect(input).toHaveValue("first");
  await input.fill("second");
  await page.getByRole("button", { name: "Save" }).click();
  await page.getByRole("button", { name: "Clear" }).click();

  await expect.poll(() => m.writes.length).toBe(3);
  const [a, b, c] = m.writes;
  expect(a.body).toEqual({ text: "another" });
  expect(b.body).toEqual({ text: "second" });
  expect(a.key).toBeTruthy();
  expect(b.key).toBeTruthy();
  expect(b.key).not.toBe(a.key);
  expect(c.method).toBe("DELETE");
  expect(c.url).toContain("/messages/waiting");
});

test("Stop interrupts the running turn with no message", async ({ page }) => {
  const m = mock({ agent: agent({ state: "active", running_turn_id: "t1" }) });
  await open(page, m);
  await page.getByRole("button", { name: "Stop" }).click();
  await expect.poll(() => m.writes.length).toBe(1);
  expect(m.writes[0].body).toEqual({ text: "", delivery: "interrupt" });
  expect(m.writes[0].key).toBeTruthy();
});

test("live deltas stream and settle into one completed item", async ({
  page,
}) => {
  const m = mock();
  await open(page, m);
  const delta = (text: string) =>
    ev("delta", { itemId: "m1", itemKind: "message", text }, true);
  await push(page, m, delta("Hel"), delta("lo"));
  await expect(
    transcript(page).getByText("Hello", { exact: true }),
  ).toBeVisible();
  await push(
    page,
    m,
    ev("item.completed", { itemId: "m1", itemKind: "message", text: "Hello!" }),
  );
  await expect(transcript(page).getByText("Hello!")).toHaveCount(1);
  await expect(
    transcript(page).getByText("Hello", { exact: true }),
  ).toHaveCount(0);
});

test("an approval resolves once and the card leaves", async ({ page }) => {
  const m = mock({
    agent: agent({
      open_asks: [{ id: "A1", type: "approval", about: "run tests" }],
    }),
  });
  await open(page, m);
  await page.getByRole("button", { name: "Approve" }).click();
  await expect(page.getByTestId("ask-card")).toHaveCount(0);
  expect(m.writes).toHaveLength(1);
  expect(m.writes[0].url).toContain("/asks/A1");
  expect(m.writes[0].body).toEqual({ decision: "allow_once" });
});

test("a question card disappears on ask.lost", async ({ page }) => {
  const m = mock({
    agent: agent({
      open_asks: [{ id: "Q1", type: "question", about: "Which branch?" }],
    }),
  });
  await open(page, m);
  await expect(page.getByTestId("ask-card")).toBeVisible();
  m.agent = agent();
  await push(page, m, ev("ask.lost", { askId: "Q1" }));
  await expect(page.getByTestId("ask-card")).toHaveCount(0);
  expect(m.writes).toHaveLength(0);
});

test("a reload shows each completed item once, from ListEvents", async ({
  page,
}) => {
  const m = mock({
    events: [
      ev("message.delivered", { text: "hi" }),
      ev("item.completed", { itemId: "m1", itemKind: "message", text: "one" }),
    ],
  });
  await open(page, m);
  await push(
    page,
    m,
    ev("delta", { itemId: "m2", itemKind: "message", text: "tw" }, true),
    ev("item.completed", { itemId: "m2", itemKind: "message", text: "two" }),
  );
  await expect(transcript(page).getByText("two")).toHaveCount(1);
  await page.reload();
  await expect(page.getByText("lead")).toBeVisible();
  for (const text of ["hi", "one", "two"])
    await expect(transcript(page).getByText(text, { exact: true })).toHaveCount(
      1,
    );
  await expect(transcript(page).getByText("tw", { exact: true })).toHaveCount(
    0,
  );
});

test("feed.gap pages the missed event once; a late replay is not shown twice", async ({
  page,
}) => {
  const m = mock({
    events: [ev("item.completed", { itemKind: "message", text: "before" })],
  });
  await open(page, m);
  // Committed on the server but never streamed: only ListEvents has it.
  const missed = ev("item.completed", { itemKind: "message", text: "missed" });
  m.events.push(missed);
  await push(page, m, ev("feed.gap", {}, true));
  await expect(transcript(page).getByText("missed")).toHaveCount(1);
  await push(page, m, missed);
  await push(
    page,
    m,
    ev("item.completed", { itemKind: "message", text: "after" }),
  );
  await expect(transcript(page).getByText("after")).toHaveCount(1);
  await expect(transcript(page).getByText("missed")).toHaveCount(1);
  const texts = await transcript(page).locator("li").allInnerTexts();
  expect(texts).toEqual(["before", "missed", "after"]);
});

// T3's ChatMarkdown wrapper is "text-sm leading-relaxed text-foreground/80",
// and its list items sit 0.25rem apart (index.css .chat-markdown li + li).
test("agent text takes T3's markdown type, colour and list gap in light and dark", async ({
  page,
}) => {
  await open(
    page,
    mock({
      events: [
        ev("item.completed", {
          itemKind: "message",
          text: "## Plan\n\nSome `code` here.\n\n- one\n- two\n\n> quoted",
        }),
      ],
    }),
  );
  const md = transcript(page).getByTestId("chat-markdown");
  await expect(md.getByRole("listitem")).toHaveCount(2);
  for (const theme of ["dark", "light"]) {
    const m = await md.evaluate((el, t) => {
      document.documentElement.dataset.theme = t;
      // [r, g, b] in 0-255 and alpha, from rgb(), rgba() or color(srgb ...).
      const parse = (c: string) => {
        const n = (c.match(/[\d.]+/g) ?? []).map(Number);
        const srgb = c.startsWith("color(");
        const rgb = n.slice(0, 3).map((v) => Math.round(srgb ? v * 255 : v));
        return { rgb, alpha: n.length > 3 ? n[3] : 1 };
      };
      const cs = getComputedStyle(el);
      const [li1, li2] = el.querySelectorAll("li");
      return {
        fontSize: cs.fontSize,
        lineHeight: cs.lineHeight,
        color: parse(cs.color),
        heading: parse(getComputedStyle(el.querySelector("h2")!).color),
        code: parse(getComputedStyle(el.querySelector("p code")!).color),
        liGap:
          li2!.getBoundingClientRect().top -
          li1!.getBoundingClientRect().bottom,
        quoteStyle: getComputedStyle(el.querySelector("blockquote")!).fontStyle,
      };
    }, theme);
    expect(m.fontSize, theme).toBe("14px");
    expect(m.lineHeight, theme).toBe("22.75px");
    // The text is the foreground at 80%; headings and inline code are solid.
    expect(m.color.alpha, theme).toBeCloseTo(0.8, 2);
    expect(m.heading.alpha, theme).toBe(1);
    expect(m.color.rgb, theme).toEqual(m.heading.rgb);
    expect(m.code.alpha, theme).toBe(1);
    expect(m.liGap, theme).toBeCloseTo(4, 1);
    expect(m.quoteStyle, theme).toBe("normal");
  }
});

test("streaming reveals smoothly with no layout shift and the end kept in view", async ({
  page,
}) => {
  // Enough history to scroll, so live follow has an end to hold.
  const history = Array.from({ length: 30 }, (_, i) =>
    ev("item.completed", { itemKind: "message", text: `Earlier reply ${i}.` }),
  );
  const m = mock({
    agent: agent({ state: "active", running_turn_id: "t1" }),
    events: history,
  });
  await open(page, m);
  await expect(transcript(page).getByText("Earlier reply 29.")).toBeVisible();
  // The model controls settle last (no catalog here, so "Unavailable").
  await expect(page.getByText("Unavailable", { exact: true })).toBeVisible();
  // The 100ms recorder: layout-shift score and the gap under the last row,
  // from the end of the history load (its shifts are not streaming's).
  await page.evaluate(() => {
    const w = window as unknown as { __cls: number; __gaps: number[] };
    w.__cls = 0;
    w.__gaps = [];
    new PerformanceObserver((list) => {
      for (const e of list.getEntries() as (PerformanceEntry & {
        value: number;
        hadRecentInput: boolean;
      })[])
        if (!e.hadRecentInput) w.__cls += e.value;
    }).observe({ type: "layout-shift" });
    const el = document.querySelector('[data-testid="chat-transcript"]')!;
    setInterval(
      () => w.__gaps.push(el.scrollHeight - el.scrollTop - el.clientHeight),
      100,
    );
  });
  const words = "The quick brown fox jumps over the lazy dog. ".repeat(12);
  const delta = (text: string) =>
    ev("delta", { itemId: "m1", itemKind: "message", text }, true);
  for (let i = 0; i < words.length; i += 24) {
    await push(page, m, delta(words.slice(i, i + 24)));
    await page.waitForTimeout(80);
  }
  const live = transcript(page).getByTestId("chat-markdown").last();
  await expect(live.locator("[data-streaming-caret]")).toHaveCount(1);
  await push(
    page,
    m,
    ev("item.completed", { itemId: "m1", itemKind: "message", text: words }),
  );
  await expect(live.locator("[data-streaming-caret]")).toHaveCount(0);
  await expect(live).toHaveText(words.trim());
  const r = await page.evaluate(() => {
    const w = window as unknown as { __cls: number; __gaps: number[] };
    return { cls: w.__cls, gaps: w.__gaps };
  });
  expect(r.gaps.length).toBeGreaterThan(10);
  expect(Math.max(...r.gaps)).toBeLessThanOrEqual(0);
  expect(r.cls).toBe(0);
});

test("streamed wide table keeps columns and controls stable until completion", async ({
  page,
}, testInfo) => {
  test.setTimeout(60_000);
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"], {
    origin: "http://localhost:3000",
  });
  const history = Array.from({ length: 30 }, (_, i) =>
    ev("item.completed", { itemKind: "message", text: `Earlier reply ${i}.` }),
  );
  const m = mock({
    agent: agent({ state: "active", running_turn_id: "t1" }),
    events: history,
  });
  await open(page, m);
  await expect(transcript(page).getByText("Earlier reply 29.")).toBeVisible();
  await expect(page.getByText("Unavailable", { exact: true })).toBeVisible();
  expect(
    await page.evaluate(
      () => matchMedia("(prefers-reduced-motion: reduce)").matches,
    ),
  ).toBe(false);
  await page.evaluate(() => {
    const w = window as unknown as {
      __visualCls: {
        value: number;
        sources: {
          tag: string;
          className: string;
          previous: number[];
          current: number[];
        }[];
      }[];
      __visualGaps: number[];
      __visualWords: number[];
      __visualObserver: PerformanceObserver;
      __visualDrain: () => void;
    };
    w.__visualCls = [];
    w.__visualGaps = [];
    w.__visualWords = [];
    const collect = (entries: PerformanceEntry[]) => {
      for (const entry of entries as (PerformanceEntry & {
        value: number;
        hadRecentInput: boolean;
        sources?: {
          node: Node | null;
          previousRect: DOMRectReadOnly;
          currentRect: DOMRectReadOnly;
        }[];
      })[]) {
        if (entry.hadRecentInput) continue;
        w.__visualCls.push({
          value: entry.value,
          sources: (entry.sources ?? []).map((s) => ({
            tag: s.node instanceof Element ? s.node.tagName : "unknown",
            className:
              s.node instanceof Element && typeof s.node.className === "string"
                ? s.node.className
                : "",
            previous: [
              s.previousRect.x,
              s.previousRect.y,
              s.previousRect.width,
              s.previousRect.height,
            ],
            current: [
              s.currentRect.x,
              s.currentRect.y,
              s.currentRect.width,
              s.currentRect.height,
            ],
          })),
        });
      }
    };
    w.__visualObserver = new PerformanceObserver((list) =>
      collect(list.getEntries()),
    );
    w.__visualDrain = () => collect(w.__visualObserver.takeRecords());
    w.__visualObserver.observe({ type: "layout-shift" });
  });
  const lead =
    "This answer checks the repository behavior through a concrete example. ".repeat(
      12,
    );
  const table =
    "\n\n| Component | Observed behavior | Evidence | Owner | Receipt | Outcome |\n| --- | --- | --- | --- | --- | --- |\n" +
    Array.from(
      { length: 8 },
      (_, i) =>
        `| Row ${i + 1} | A saved event updates the visible transcript and retains the full source | Event ${i + 1} | Agent | Saved ${i + 1} | Complete |\n`,
    ).join("");
  const list =
    "\n\n" +
    Array.from(
      { length: 12 },
      (_, i) =>
        `- Check ${i + 1} confirms the layout remains usable during a genuine stream.\n`,
    ).join("");
  const answer = lead + table + list;
  for (let end = 48; end < answer.length + 48; end += 48) {
    await push(
      page,
      m,
      ev(
        "delta",
        {
          itemId: "m1",
          itemKind: "message",
          text: answer.slice(Math.max(0, end - 48), end),
        },
        true,
      ),
    );
    await page.evaluate(
      () =>
        new Promise<void>((resolve) =>
          requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
        ),
    );
    await page.evaluate(() => {
      const w = window as unknown as {
        __visualGaps: number[];
        __visualWords: number[];
      };
      const el = document.querySelector('[data-testid="chat-transcript"]')!;
      w.__visualGaps.push(el.scrollHeight - el.scrollTop - el.clientHeight);
      const md = el.querySelectorAll('[data-testid="chat-markdown"]');
      w.__visualWords.push(
        md.length
          ? (md[md.length - 1].textContent?.trim().split(/\s+/).length ?? 0)
          : 0,
      );
    });
  }
  await expect(
    transcript(page).getByTestId("chat-markdown").last(),
  ).toContainText("Check 12");
  const tableActions = transcript(page)
    .getByTestId("chat-markdown")
    .last()
    .locator('[class*="tableActions"]');
  await expect(tableActions).toHaveCount(1);
  const columnStyle = await tableActions.locator("..").getAttribute("style");
  const controlsHiddenDuring = await tableActions.isHidden();
  await expect(transcript(page).locator('li[data-kind="working"]')).toHaveCount(
    1,
  );
  await testInfo.attach("streaming-table", {
    body: await page.screenshot(),
    contentType: "image/png",
  });
  await push(
    page,
    m,
    ev("item.completed", { itemId: "m1", itemKind: "message", text: answer }),
  );
  const readsBeforeCompletion = m.reads;
  m.agent = agent({ state: "idle", running_turn_id: null });
  await push(page, m, ev("agent.turn_completed", { stopReason: "completed" }));
  await expect.poll(() => m.reads).toBeGreaterThan(readsBeforeCompletion);
  await expect(page.getByRole("button", { name: "Stop" })).toHaveCount(0);
  await expect(transcript(page).locator('li[data-kind="working"]')).toHaveCount(
    0,
  );
  await expect(transcript(page).locator("[data-streaming-caret]")).toHaveCount(
    0,
  );
  await expect(
    transcript(page).getByTestId("chat-markdown").last(),
  ).toContainText("Check 12");
  await expect(
    transcript(page).getByTestId("chat-markdown").last().locator("li").last(),
  ).toHaveText(
    "Check 12 confirms the layout remains usable during a genuine stream.",
  );
  await expect(tableActions.locator("..").getByRole("row")).toHaveCount(9);
  await expect(tableActions).toBeVisible();
  expect(m.events.at(-2)?.payload).toMatchObject({ text: answer });
  expect(m.events.at(-1)?.kind).toBe("agent.turn_completed");
  const result = await page.evaluate(async () => {
    await new Promise<void>((resolve) =>
      requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
    );
    const w = window as unknown as {
      __visualCls: {
        value: number;
        sources: {
          tag: string;
          className: string;
          previous: number[];
          current: number[];
        }[];
      }[];
      __visualGaps: number[];
      __visualWords: number[];
      __visualObserver: PerformanceObserver;
      __visualDrain: () => void;
    };
    w.__visualDrain();
    w.__visualObserver.disconnect();
    const chat = document.querySelector('[data-testid="chat-transcript"]')!;
    const markdown = chat.querySelectorAll('[data-testid="chat-markdown"]');
    return {
      cls: w.__visualCls.reduce((sum, e) => sum + e.value, 0),
      shifts: w.__visualCls,
      maxGap: Math.max(...w.__visualGaps),
      terminalGap: chat.scrollHeight - chat.scrollTop - chat.clientHeight,
      firstVisibleWords: w.__visualWords.find((n) => n > 0),
      maxAddedSampleWords: Math.max(
        ...w.__visualWords.map((n, i) =>
          Math.max(0, n - (w.__visualWords[i - 1] ?? 0)),
        ),
      ),
      beforeCompletionWords: w.__visualWords.at(-1),
      words: markdown[markdown.length - 1]?.textContent?.trim().split(/\s+/)
        .length,
    };
  });
  const shiftSummary = result.shifts.map((shift) => ({
    value: shift.value,
    sources: shift.sources
      .map((source) => ({
        tag: source.tag,
        className: source.className,
        dx: source.current[0] - source.previous[0],
        dy: source.current[1] - source.previous[1],
      }))
      .slice(0, 3),
  }));
  expect(result.words).toBeGreaterThanOrEqual(300);
  expect(result.firstVisibleWords).toBeGreaterThan(0);
  expect(result.maxGap).toBe(0);
  expect(result.terminalGap).toBe(0);
  console.log("streamed table terminal", {
    cls: result.cls,
    maxGap: result.maxGap,
    terminalGap: result.terminalGap,
    words: result.words,
    shifts: shiftSummary,
    controlsHiddenDuring,
    columnStyle,
  });
  await testInfo.attach("completed-table", {
    body: await page.screenshot(),
    contentType: "image/png",
  });
  expect.soft(controlsHiddenDuring).toBe(true);
  expect.soft(columnStyle ?? "").toMatch(/--table-columns: 6/);
  expect.soft(result.cls, JSON.stringify(shiftSummary)).toBe(0);
  await page.setViewportSize({ width: 390, height: 844 });
  const overflow = await tableActions.locator("..").evaluate((el) => {
    const scroll = el.querySelector('[class*="tableScroll"]')!;
    const chat = el.closest('[data-testid="chat-transcript"]')!;
    return {
      inner: scroll.scrollWidth - scroll.clientWidth,
      outer: chat.scrollWidth - chat.clientWidth,
    };
  });
  console.log("streamed table layout", {
    cls: result.cls,
    maxGap: result.maxGap,
    overflow,
  });
  expect(overflow.inner).toBeGreaterThan(0);
  expect(overflow.outer).toBeLessThanOrEqual(0);
  await tableActions
    .getByRole("button", { name: "Expand table cells" })
    .click();
  await expect(tableActions.locator("..")).toHaveAttribute(
    "data-expanded",
    "true",
  );
  await tableActions.getByRole("button", { name: "Copy as Markdown" }).click();
  const copiedMarkdown = await page.evaluate(() =>
    navigator.clipboard.readText(),
  );
  expect(copiedMarkdown).toContain(
    "| Component | Observed behavior | Evidence | Owner | Receipt | Outcome |",
  );
  expect(copiedMarkdown).toContain(
    "| Row 8 | A saved event updates the visible transcript and retains the full source | Event 8 | Agent | Saved 8 | Complete |",
  );
  await tableActions.getByRole("button", { name: "Copy as CSV" }).click();
  const copiedCsv = await page.evaluate(() => navigator.clipboard.readText());
  expect(copiedCsv).toContain(
    "Component,Observed behavior,Evidence,Owner,Receipt,Outcome",
  );
  expect(copiedCsv).toContain(
    "Row 8,A saved event updates the visible transcript and retains the full source,Event 8,Agent,Saved 8,Complete",
  );
});

test("one agent's one-line messages sit close, and the hover pill takes no space (UI6)", async ({
  page,
}) => {
  const at = "2026-10-04T20:15:00Z";
  const stamp = (e: ReturnType<typeof ev>) => ({ ...e, created_at: at });
  const say = (text: string) =>
    stamp(ev("item.completed", { itemKind: "message", text }));
  const wide =
    "The review is done and every check passed, so the branch is ready to merge once you have looked over the summary below and the two notes about naming.";
  await open(
    page,
    mock({
      events: [
        stamp(
          ev("message.delivered", {
            sender: "user:local",
            text: "Run the reviewers",
          }),
        ),
        say("api-reviewer completed its review."),
        say("test-runner completed all tests."),
        say(wide),
      ],
    }),
  );
  const md = transcript(page).getByTestId("chat-markdown");
  await expect(md).toHaveCount(3);
  const box = async (i: number) => (await md.nth(i).boundingBox())!;
  const gap = (await box(1)).y - ((await box(0)).y + (await box(0)).height);
  test.info().annotations.push({ type: "gap", description: `${gap}px` });
  console.log(`UI6 gap between one-line agent messages: ${gap}px`);
  expect(gap).toBeLessThan(16);
  // The pill shows on hover, with the time, and moves nothing.
  const time = await page.evaluate(
    (t) =>
      new Date(t).toLocaleTimeString([], {
        hour: "numeric",
        minute: "2-digit",
      }),
    at,
  );
  const before = await box(1);
  const rows = transcript(page).locator("li");
  const agentRow = rows.filter({ hasText: "api-reviewer completed" });
  await agentRow.hover();
  const pill = agentRow.getByTestId("message-actions");
  await expect(pill).toBeVisible();
  await expect(pill).toHaveText(time);
  await expect(
    pill.getByRole("button", { name: "Copy message" }),
  ).toBeVisible();
  expect(await box(1)).toEqual(before);
  // It stays on its own message, clear of the next one.
  const p = (await pill.boundingBox())!;
  expect(p.y + p.height).toBeLessThanOrEqual(before.y);
  const userRow = rows.filter({ hasText: "Run the reviewers" });
  for (const theme of ["light", "dark"]) {
    await page.evaluate((t) => {
      document.documentElement.dataset.theme = t;
    }, theme);
    await page.mouse.move(0, 0);
    if (SHOTS)
      await page.screenshot({
        path: `${SHOTS}/ui6-spacing-${theme}.png`,
        animations: "disabled",
      });
    await userRow.hover();
    await expect(userRow.getByTestId("message-actions")).toHaveText(time);
    if (SHOTS)
      await page.screenshot({
        path: `${SHOTS}/ui6-user-hover-${theme}.png`,
        animations: "disabled",
      });
    const wideRow = rows.filter({ hasText: "The review is done" });
    await wideRow.hover();
    await expect(wideRow.getByTestId("message-actions")).toBeVisible();
    if (SHOTS)
      await page.screenshot({
        path: `${SHOTS}/ui6-agent-hover-${theme}.png`,
        animations: "disabled",
      });
  }
});

test("a user's hover pill sits left of the bubble, over none of it (UI7)", async ({
  page,
}) => {
  const at = "2026-10-04T20:15:00Z";
  const user = (text: string) => ({
    ...ev("message.delivered", { sender: "user:local", text }),
    created_at: at,
  });
  const long =
    "Please run the API reviewer and the test runner on the branch, then report the result here with any failures and the files they touched.";
  await open(page, mock({ events: [user("report the result."), user(long)] }));
  const rows = transcript(page).locator("li");
  for (const [name, text] of [
    ["short", "report the result."],
    ["long", "Please run the API reviewer"],
  ] as const) {
    const row = rows.filter({ hasText: text });
    const bubble = row.locator("[class*=userBubble]");
    const before = (await bubble.boundingBox())!;
    await row.hover();
    const pill = row.getByTestId("message-actions");
    await expect(pill).toBeVisible();
    const p = (await pill.boundingBox())!;
    const b = (await bubble.boundingBox())!;
    expect(b, `${name}: no layout shift`).toEqual(before);
    // The pill holds its whole copy button, not just the time.
    const copy = pill.getByRole("button", { name: "Copy your message" });
    await expect(copy).toBeVisible();
    const c = (await copy.boundingBox())!;
    expect(c.width, `${name}: copy button width`).toBeGreaterThanOrEqual(14);
    expect(c.x, `${name}: copy inside the pill`).toBeGreaterThanOrEqual(p.x);
    expect(c.x + c.width, `${name}: copy inside the pill`).toBeLessThanOrEqual(
      p.x + p.width,
    );
    // Left of the bubble and top-aligned with it, so the boxes never meet.
    expect(p.x + p.width, `${name}: pill right edge`).toBeLessThanOrEqual(b.x);
    expect(Math.abs(p.y - b.y), `${name}: top-aligned`).toBeLessThanOrEqual(1);
    // Whole inside its row, which clips anything past its edge.
    const r = (await row.boundingBox())!;
    expect(p.x, `${name}: not clipped by its row`).toBeGreaterThanOrEqual(r.x);
    for (const theme of ["light", "dark"]) {
      await page.evaluate((t) => {
        document.documentElement.dataset.theme = t;
      }, theme);
      if (SHOTS)
        await page.screenshot({
          path: `${SHOTS}/ui7-${name}-${theme}.png`,
          animations: "disabled",
        });
    }
  }
  // A larger default font grows the pill; the bubble's room grows with it.
  await page.evaluate(() => {
    document.documentElement.style.fontSize = "32px";
  });
  const row = rows.filter({ hasText: "Please run the API reviewer" });
  await page.mouse.move(0, 0);
  await row.hover();
  const pill = row.getByTestId("message-actions");
  await expect(
    pill.getByRole("button", { name: "Copy your message" }),
  ).toBeVisible();
  const p = (await pill.boundingBox())!;
  const r = (await row.boundingBox())!;
  const b = (await row.locator("[class*=userBubble]").boundingBox())!;
  expect(p.x, "32px font: not clipped by its row").toBeGreaterThanOrEqual(r.x);
  expect(p.x + p.width, "32px font: left of the bubble").toBeLessThanOrEqual(
    b.x,
  );
});

test.describe("in a locale with a long time format", () => {
  test("a user's pill stays whole in its row, the time cut short before copy (UI7)", async ({
    page,
  }) => {
    // Playwright's Chromium lacks Assamese ICU data and falls back to
    // "5:59 AM", so force the as-IN format codex measured.
    await page.addInitScript(() => {
      Date.prototype.toLocaleTimeString = () => "অপৰাহ্ন ১২.৫৯";
    });
    const long =
      "Please run the API reviewer and the test runner on the branch, then report the result here with any failures and the files they touched.";
    await open(
      page,
      mock({
        events: [
          {
            ...ev("message.delivered", { sender: "user:local", text: long }),
            created_at: "2026-10-04T12:59:00Z",
          },
        ],
      }),
    );
    const row = transcript(page)
      .locator("li")
      .filter({ hasText: "Please run" });
    await row.hover();
    const pill = row.getByTestId("message-actions");
    const copy = pill.getByRole("button", { name: "Copy your message" });
    await expect(copy).toBeVisible();
    const p = (await pill.boundingBox())!;
    const r = (await row.boundingBox())!;
    const b = (await row.locator("[class*=userBubble]").boundingBox())!;
    const c = (await copy.boundingBox())!;
    expect(p.x, "not clipped by its row").toBeGreaterThanOrEqual(r.x);
    expect(p.x + p.width, "left of the bubble").toBeLessThanOrEqual(b.x);
    expect(c.width, "copy whole").toBeGreaterThanOrEqual(14);
    expect(c.x + c.width, "copy inside the pill").toBeLessThanOrEqual(
      p.x + p.width,
    );
    if (SHOTS)
      await page.screenshot({
        path: `${SHOTS}/ui7-as-IN.png`,
        animations: "disabled",
      });
  });

  test("in a very narrow split pane the user's pill still fits its row (UI7b)", async ({
    page,
  }) => {
    await page.addInitScript(() => {
      Date.prototype.toLocaleTimeString = () => "অপৰাহ্ন ১২.৫৯";
    });
    await open(
      page,
      mock({
        events: [
          {
            ...ev("message.delivered", {
              sender: "user:local",
              text: "Please run the API reviewer on the branch.",
            }),
            created_at: "2026-10-04T12:59:00Z",
          },
        ],
      }),
    );
    const row = transcript(page)
      .locator("li")
      .filter({ hasText: "Please run" });
    // A narrow split pane gives its rows this width; the column cap sets it here.
    for (const width of [130, 105, 80]) {
      await transcript(page).evaluate((el, w) => {
        el.style.setProperty("--chat-column", `${w}px`);
      }, width);
      await page.mouse.move(0, 0);
      await row.hover();
      const pill = row.getByTestId("message-actions");
      const copy = pill.getByRole("button", { name: "Copy your message" });
      await expect(copy).toBeVisible();
      const r = (await row.boundingBox())!;
      expect(Math.round(r.width), `${width}px: row width`).toBe(width);
      const p = (await pill.boundingBox())!;
      const b = (await row.locator("[class*=userBubble]").boundingBox())!;
      const c = (await copy.boundingBox())!;
      expect(p.x, `${width}px: not clipped by its row`).toBeGreaterThanOrEqual(
        r.x,
      );
      expect(
        p.x + p.width,
        `${width}px: left of the bubble`,
      ).toBeLessThanOrEqual(b.x);
      expect(c.width, `${width}px: copy whole`).toBeGreaterThanOrEqual(14);
      expect(c.x, `${width}px: copy inside the pill`).toBeGreaterThanOrEqual(
        p.x,
      );
      expect(
        c.x + c.width,
        `${width}px: copy inside the pill`,
      ).toBeLessThanOrEqual(p.x + p.width);
      if (SHOTS)
        await page.screenshot({
          path: `${SHOTS}/ui7b-${width}px.png`,
          animations: "disabled",
        });
    }
  });
});
