/**
 * AgentChat in a real browser against a mocked Agent API (design v2 §9.2–§9.3):
 * safe text, harness parity, waiting edit/clear, live deltas and ask cards.
 */
import { expect, test } from "@playwright/test";
import type { Page, Route } from "@playwright/test";

const BASE = "**/api/workspaces/w1";
const XSS = `<img src=x onerror="window.pwned=1"><script>window.pwned=1</script>`;
const LONG = "y".repeat(5000);

interface Mock {
  agent: Record<string, unknown>;
  events: ReturnType<typeof ev>[];
  writes: { method: string; url: string; key: string | null; body: unknown }[];
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
  await page.route(`${BASE}/v1/agents/a1`, (r) => json(r, m.agent));
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
