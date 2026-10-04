/**
 * The Lead chat's children in a real browser against a mocked Agent API
 * (DF1): Started and result markers instead of raw task_completed bubbles,
 * a child's own message as a "from <name>" bubble, and the agent tray above
 * the composer: working children and results waiting for the Lead, by wave,
 * with the unread dot until delivery. DF1_SHOTS=<dir> also saves screenshots.
 */
import { expect, test } from "@playwright/test";
import type { Page, Route } from "@playwright/test";

const SHOTS = process.env.DF1_SHOTS;
// Minute min of the hour that ends now, so elapsed times read as real.
const T = (min: number) =>
  new Date(Date.now() - (60 - min) * 60_000).toISOString();

let seq = 0;
const ev = (kind: string, payload: object, at = T(41), id?: string) => ({
  agent_id: "a1",
  seq: ++seq,
  event_id: id ?? `${kind}:${seq}`,
  kind,
  turn_id: "t1",
  payload,
  created_at: at,
});

const kid = (
  id: string,
  name: string,
  harness: string,
  model: string,
  over: object = {},
) => ({
  agent_id: id,
  name,
  harness,
  model,
  parent_agent_id: "a1",
  branch: `loom/${name}`,
  state: "active",
  attempt: 0,
  created_at: T(43),
  deleted_at: null,
  waiting_messages: [],
  open_asks: [],
  ...over,
});

const record = (
  child: string,
  attempt: number,
  outcome: string,
  summary: string,
  at: string,
  head: string,
) =>
  ev(
    "task_completed",
    { child, attempt, outcome, branch: `loom/${child}`, head, summary },
    at,
    `task_completed:${child}:${attempt}`,
  );

const delivered = (
  child: string,
  attempt: number,
  message: string,
  at: string,
) =>
  ev(
    "message.delivered",
    {
      sender: `agent:${child}`,
      text: `${message}\ntask_completed:${child}:${attempt} outcome=… summary="…"`.trim(),
      completions: [{ child, attempt }],
      message,
    },
    at,
  );

interface Mock {
  agent: Record<string, unknown>;
  roster: Record<string, unknown>[];
  events: ReturnType<typeof ev>[];
}

const json = (route: Route, body: unknown, status = 200) =>
  route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });

async function open(page: Page, m: Mock, w: number, h: number) {
  await page.route("**/api/config", (r) => json(r, { mode: "open" }));
  await page.route("**/api/workspaces/w1/events/token", (r) =>
    r.fulfill({ status: 404 }),
  );
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
  await page.route(
    (u) => u.pathname.startsWith("/api/workspaces/w1/v1/agents"),
    (r) => {
      const path = new URL(r.request().url()).pathname;
      if (path.endsWith("/v1/agents"))
        return json(r, { agents: [m.agent, ...m.roster], next: "" });
      if (path.endsWith("/a1/events"))
        return json(r, {
          events: m.events,
          snapshot_seq: seq,
          next: seq,
          more: false,
        });
      if (path.endsWith("/a1/messages"))
        return json(
          r,
          { message_id: "m", state: "waiting", replaced: false },
          202,
        );
      if (path.endsWith("/a1")) return json(r, m.agent);
      const k = m.roster.find((a) => path.endsWith(`/${a.agent_id as string}`));
      return k ? json(r, k) : r.fulfill({ status: 404 });
    },
  );
  await page.setViewportSize({ width: w + 16, height: h + 16 });
  await page.goto(`/test/agent-chat?roster=1&w=${w}&h=${h}`);
  await expect(page.getByTestId("chat-transcript")).toContainText(
    "Build a Slack clone",
  );
}

// Saves events, then sends them on every open stream (the chat's and the roster's).
async function push(page: Page, m: Mock, ...frames: ReturnType<typeof ev>[]) {
  m.events.push(...frames);
  type Sources = { __sse: (EventTarget & { closed: boolean })[] };
  await page.evaluate(
    (data) => {
      for (const s of (window as unknown as Sources).__sse.filter(
        (x) => !x.closed,
      ))
        for (const d of data)
          s.dispatchEvent(new MessageEvent("event", { data: d }));
    },
    frames.map((f) => JSON.stringify(f)),
  );
}

function slackClone(): Mock {
  seq = 0;
  return {
    agent: {
      agent_id: "a1",
      name: "Lead",
      harness: "claude",
      model: "claude-opus",
      branch: "loom/lead",
      state: "idle",
      running_turn_id: null,
      waiting_messages: [],
      open_asks: [],
      created_at: T(40),
    },
    roster: [
      kid("k1", "api-worker", "claude", "claude-sonnet", { state: "finished" }),
      kid("k2", "ui-worker", "codex", "codex-gpt"),
      kid("k3", "db-worker", "claude", "claude-sonnet", { attempt: 1 }),
    ],
    events: [
      ev(
        "message.delivered",
        { sender: "user:local", text: "Build a Slack clone" },
        T(41),
      ),
      ev(
        "item.completed",
        {
          itemKind: "message",
          itemId: "m1",
          text: "On it. I'll split this into three parallel workers: a REST API, the web UI, and the database schema + migrations.",
        },
        T(42),
      ),
      ev("child.created", { child: "k1", name: "api-worker" }, T(43)),
      ev("child.created", { child: "k2", name: "ui-worker" }, T(43)),
      ev("child.created", { child: "k3", name: "db-worker" }, T(43)),
      ev(
        "message.delivered",
        { sender: "user:local", text: "While they work — Postgres or SQLite?" },
        T(44),
      ),
      ev(
        "item.completed",
        {
          itemKind: "message",
          itemId: "m2",
          text: "Postgres. You'll want full-text search on messages and proper concurrent writes.",
        },
        T(44),
      ),
      record(
        "k1",
        0,
        "completed",
        "Implemented REST API for channels, messages and users.",
        T(45),
        "a1b2c3d4e5f6",
      ),
      delivered(
        "k1",
        0,
        "Heads up: I exposed `GET /channels/:id/messages?before=` for pagination — the UI should use **cursor paging**, not offsets.",
        T(45),
      ),
      record(
        "k3",
        0,
        "failed",
        'Failed: migration 003_messages.sql — relation "channels" does not exist',
        T(46),
        "c09f8e1d2c3b",
      ),
      delivered("k3", 0, "", T(46)),
      ev(
        "item.completed",
        {
          itemKind: "message",
          itemId: "m3",
          text: "db-worker hit a migration ordering bug (messages before channels). I've retried it with the order fixed.",
        },
        T(47),
      ),
    ],
  };
}

test.beforeEach(() => {
  seq = 0;
});

test("children show as markers and the tray, a waiting result keeps its dot until the Lead reads it", async ({
  page,
}) => {
  const m = slackClone();
  await open(page, m, 900, 860);
  const chat = page.getByTestId("chat-transcript");
  await expect(page.locator("body")).not.toContainText("task_completed:");
  await expect(page.getByTestId("started-marker")).toContainText(
    "Started api-worker, ui-worker, db-worker",
  );
  const records = page.getByTestId("completion-record");
  await expect(records).toHaveCount(2);
  await expect(records.nth(0)).toContainText("api-worker done");
  await expect(records.nth(0)).toContainText("Lead read the result");
  await expect(records.nth(1)).toContainText("db-worker failed");
  const from = page.getByTestId("from-agent");
  await expect(from).toHaveCount(1);
  await expect(from).toContainText("from api-worker");
  await expect(from.locator("code")).toHaveText(
    "GET /channels/:id/messages?before=",
  );
  await expect(
    from.locator("strong", { hasText: "cursor paging" }),
  ).toBeVisible();

  const tray = page.getByTestId("agent-tray");
  await expect(tray).toContainText("2 agents · 2 running");

  // A second wave: docs-worker starts and finishes while the Lead's turn runs.
  m.roster.push(
    kid("k4", "docs-worker", "opencode", "gemini-pro", {
      state: "finished",
      created_at: T(52),
    }),
  );
  m.agent = {
    ...m.agent,
    state: "active",
    running_turn_id: "t9",
    waiting_messages: [
      {
        sender: "agent:k4",
        text: "task_completed:k4:0 …",
        since: T(53),
        message: "",
        completions: [{ child: "k4", attempt: 0 }],
      },
    ],
  };
  await push(
    page,
    m,
    ev("child.created", { child: "k4", name: "docs-worker" }, T(52)),
    record(
      "k4",
      0,
      "completed",
      "README and API reference drafted.",
      T(53),
      "a1b890a12345",
    ),
    ev("message.waiting", {}, T(53)),
  );
  await expect(tray).toContainText("3 agents · 2 running · 1 done");
  await expect(page.getByTestId("tray-new")).toHaveText("+1 new");
  await expect(records.nth(2)).toContainText("docs-worker done");
  await expect(records.nth(2)).toContainText("waiting for Lead");
  await expect(page.locator("body")).not.toContainText("task_completed:");
  if (SHOTS) await page.screenshot({ path: `${SHOTS}/desktop-collapsed.png` });

  await tray.getByRole("button", { expanded: false }).click();
  const rows = tray.locator("[data-tray-row]");
  await expect(rows).toHaveCount(3);
  await expect(rows.nth(0)).toContainText("docs-worker");
  await expect(rows.nth(0)).toContainText("done · waiting for Lead");
  await expect(rows.nth(0).getByLabel("unread by the Lead")).toBeVisible();
  await expect(rows.nth(2)).toContainText("attempt 2");
  await expect(tray.getByTestId("tray-divider")).toHaveCount(2);
  await expect(page.getByTestId("tray-new")).toHaveCount(0);
  if (SHOTS) await page.screenshot({ path: `${SHOTS}/desktop-expanded.png` });

  // The Lead reads the result: the dot clears and docs-worker leaves the tray.
  m.agent = {
    ...m.agent,
    state: "idle",
    running_turn_id: null,
    waiting_messages: [],
  };
  await push(page, m, delivered("k4", 0, "", T(54)));
  await expect(rows).toHaveCount(2);
  await expect(tray.getByLabel("unread by the Lead")).toHaveCount(0);
  await expect(records.nth(2)).toContainText("Lead read the result");

  // Sending collapses the open tray so the latest lines show.
  await page
    .getByRole("textbox", { name: "Message" })
    .fill("Thanks — what's next?");
  await page.getByRole("textbox", { name: "Message" }).press("Enter");
  await expect(tray.getByRole("button", { expanded: false })).toBeVisible();
  await expect(chat).not.toContainText("task_completed:");

  // Every result delivered and no child working: the tray is gone.
  m.roster = m.roster.map((a) => ({ ...a, state: "finished" }));
  await push(page, m, ev("agent.state_changed", { to: "finished" }, T(55)));
  for (const k of ["k2", "k3"])
    await push(page, m, {
      ...ev("agent.state_changed", { to: "finished" }, T(55)),
      agent_id: k,
    });
  await expect(tray).toHaveCount(0);
});

test("at 400px the tray keeps one line with short labels", async ({ page }) => {
  const m = slackClone();
  m.roster.push(
    kid("k4", "docs-worker", "opencode", "gemini-pro", {
      state: "finished",
      created_at: T(52),
    }),
  );
  m.events.push(
    ev("child.created", { child: "k4", name: "docs-worker" }, T(52)),
    record(
      "k4",
      0,
      "completed",
      "README and API reference drafted.",
      T(53),
      "a1b890a12345",
    ),
  );
  m.agent = {
    ...m.agent,
    waiting_messages: [
      {
        sender: "agent:k4",
        text: "…",
        since: T(53),
        message: "",
        completions: [{ child: "k4", attempt: 0 }],
      },
    ],
  };
  await open(page, m, 400, 780);
  const tray = page.getByTestId("agent-tray");
  await expect(tray).toContainText("3 agents · 2 run · 1 done");
  const header = tray.getByRole("button", { expanded: false });
  const box = await header.boundingBox();
  expect(box!.height).toBeLessThan(80);
  await expect(page.locator("body")).not.toContainText("task_completed:");
  if (SHOTS) await page.screenshot({ path: `${SHOTS}/narrow-collapsed.png` });
  await header.click();
  await expect(tray.locator("[data-tray-row]")).toHaveCount(3);
  if (SHOTS) await page.screenshot({ path: `${SHOTS}/narrow-expanded.png` });
});

test("a running child's row shows its latest step and turn time, then its result (DF2)", async ({
  page,
}) => {
  const m = slackClone();
  await open(page, m, 900, 860);
  const tray = page.getByTestId("agent-tray");
  await tray.getByRole("button", { expanded: false }).click();
  const ui = tray.locator('[data-tray-row="k2"]');
  const db = tray.locator('[data-tray-row="k3"]');
  await expect(ui).toContainText("Working…");

  // The roster's one stream names tool.started and item.completed, no deltas.
  const urls = await page.evaluate(() =>
    (window as unknown as { __sse: { url: string }[] }).__sse.map((s) => s.url),
  );
  const roster = new URL(urls.find((u) => u.includes("k2"))!);
  expect(roster.searchParams.get("types")!.split(",")).toEqual(
    expect.arrayContaining(["tool.started", "item.completed"]),
  );
  expect(roster.searchParams.get("deltas")).toBeNull();

  const of = (agent: string, frame: ReturnType<typeof ev>) => ({
    ...frame,
    agent_id: agent,
  });
  const turnAt = new Date(Date.now() - 29_000).toISOString();
  await push(
    page,
    m,
    of("k2", ev("agent.state_changed", { from: "idle", to: "active" }, turnAt)),
  );
  // A tool start is a live-only notice (seq 0).
  await push(page, m, {
    ...of(
      "k2",
      ev("tool.started", {
        itemKind: "tool",
        tool: { name: "exec_command", input: '{"command":"npm test"}' },
      }),
    ),
    seq: 0,
  });
  await expect(ui).toContainText(/▸ Ran command · npm test · 0:(29|3\d)/);
  await expect(db).toContainText("Working…");
  if (SHOTS) await page.screenshot({ path: `${SHOTS}/desktop-step.png` });

  await push(
    page,
    m,
    of(
      "k2",
      ev("item.completed", {
        itemKind: "reasoning",
        text: "**Checking routes**\n\nThe router lives in app.tsx.",
      }),
    ),
  );
  await expect(ui).toContainText("💭 Thinking · Checking routes · 0:");

  // ui-worker finishes: its row shows the result, not a step.
  m.roster = m.roster.map((a) =>
    a.agent_id === "k2" ? { ...a, state: "finished" } : a,
  );
  m.agent = {
    ...m.agent,
    waiting_messages: [
      {
        sender: "agent:k2",
        text: "task_completed:k2:0 …",
        since: T(58),
        message: "",
        completions: [{ child: "k2", attempt: 0 }],
      },
    ],
  };
  await push(
    page,
    m,
    record("k2", 0, "completed", "Done", T(58), "b2c3d4e5f6a7"),
    of("k2", ev("agent.state_changed", { from: "active", to: "finished" })),
    ev("message.waiting", {}, T(58)),
  );
  await expect(ui).toContainText("done · waiting for Lead");
  await expect(ui).toContainText("Done");
  await expect(ui).not.toContainText("Thinking");
});
