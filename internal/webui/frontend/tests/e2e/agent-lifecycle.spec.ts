/**
 * Agent lifecycle in a real browser against a mocked Agent API (1.8b,
 * design v2 §4.7–§4.8): archive → read-only → unarchive, delete with a
 * dirty-work refusal, the attention banner, history expired, and the
 * harness-context divider on reload and on live replay.
 */
import { expect, test } from "@playwright/test";
import type { Page, Route } from "@playwright/test";

const BASE = "**/api/workspaces/w1";

let seq = 0;
const ev = (kind: string, payload: object) => ({
  agent_id: "a1",
  seq: ++seq,
  event_id: `${kind}:${seq}`,
  kind,
  turn_id: "t1",
  payload,
  created_at: "",
});

interface Mock {
  agent: Record<string, unknown>;
  events: ReturnType<typeof ev>[];
  writes: string[];
}

const mock = (agent: object = {}, events: Mock["events"] = []): Mock => ({
  agent: {
    agent_id: "a1",
    name: "lead",
    harness: "opencode",
    state: "idle",
    running_turn_id: null,
    archived_at: null,
    attention_reason: null,
    history_purged_at: null,
    waiting_messages: [],
    open_asks: [],
    ...agent,
  },
  events,
  writes: [],
});

const json = (route: Route, body: unknown, status = 200) =>
  route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });

async function open(page: Page, m: Mock) {
  await page.route("**/api/config", (r) => json(r, { mode: "open" }));
  await page.route(`${BASE}/events/token`, (r) => r.fulfill({ status: 404 }));
  // A route cannot hold an SSE response open: the page gets an EventSource
  // that stays open until closed.
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
    if (r.request().method() !== "DELETE") return json(r, m.agent);
    m.writes.push("delete");
    return json(
      r,
      {
        error: "uncommitted changes in /wt/a1",
        code: "unsaved_work",
        paths: ["main.go"],
        fingerprint: "f1",
      },
      409,
    );
  });
  await page.route(`${BASE}/v1/agents/a1/{archive,unarchive}`, (r) => {
    const archive = r.request().url().endsWith("/archive");
    m.writes.push(archive ? "archive" : "unarchive");
    m.agent = {
      ...m.agent,
      state: archive ? "archived" : "idle",
      archived_at: archive ? new Date().toISOString() : null,
    };
    return r.fulfill({ status: 204 });
  });
  await page.goto("/test/agent-chat");
  await expect(page.getByText(String(m.agent.name))).toBeVisible();
}

// Sends saved events on the open EventSource, committed first as on the server.
async function push(page: Page, m: Mock, ...frames: Mock["events"]) {
  m.events.push(...frames);
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

const composer = (page: Page) =>
  page.getByRole("textbox", { name: "Message", exact: true });

test.beforeEach(() => {
  seq = 0;
});

test("archive makes the chat read-only with the days left; unarchive restores it", async ({
  page,
}) => {
  const m = mock({}, [ev("message.delivered", { text: "earlier work" })]);
  await open(page, m);
  await expect(composer(page)).toBeVisible();

  await page.getByTestId("agent-archive").click();
  await expect(page.getByTestId("agent-archived-notice")).toContainText(
    "History expires in 30 days.",
  );
  await expect(composer(page)).toHaveCount(0);
  await expect(page.getByText("earlier work")).toBeVisible();

  await page.getByTestId("agent-unarchive").click();
  await expect(composer(page)).toBeVisible();
  await expect(page.getByTestId("agent-archived-notice")).toHaveCount(0);
  await expect(page.getByTestId("agent-archive")).toBeVisible();
  expect(m.writes).toEqual(["archive", "unarchive"]);
});

test("delete asks first and shows the server's dirty-work refusal", async ({
  page,
}) => {
  const m = mock();
  await open(page, m);
  await page.getByTestId("agent-delete").click();
  expect(m.writes).toEqual([]);
  await page.getByTestId("agent-delete-confirm").click();
  await expect(page.getByRole("alert")).toContainText(
    "Not deleted: uncommitted changes in /wt/a1: main.go",
  );
  await expect(page.getByTestId("agent-delete")).toBeVisible();
  expect(m.writes).toEqual(["delete"]);
});

test("an attention reason shows as a banner", async ({ page }) => {
  await open(page, mock({ attention_reason: "session_missing" }));
  await expect(page.getByTestId("agent-attention-banner")).toContainText(
    "Needs attention: the native session is missing.",
  );
});

test("purged history shows as expired with no composer and no Unarchive", async ({
  page,
}) => {
  await open(
    page,
    mock({
      state: "archived",
      archived_at: "2026-08-01T00:00:00Z",
      history_purged_at: "2026-08-31T00:00:00Z",
    }),
  );
  await expect(page.getByTestId("agent-history-expired")).toBeVisible();
  await expect(composer(page)).toHaveCount(0);
  await expect(page.getByTestId("agent-unarchive")).toHaveCount(0);
});

test("a harness switch shows a context divider live and after a reload", async ({
  page,
}) => {
  const m = mock({}, [ev("message.delivered", { text: "before the switch" })]);
  await open(page, m);
  await expect(page.getByTestId("harness-context-divider")).toHaveCount(0);

  await push(
    page,
    m,
    ev("harness.changed", { from_harness: "opencode", harness: "codex" }),
    ev("message.delivered", { text: "after the switch" }),
  );
  const divider = page.getByTestId("harness-context-divider");
  await expect(divider).toHaveText("New harness context: opencode → codex");
  await expect(page.getByText("before the switch")).toBeVisible();

  await page.reload();
  await expect(divider).toHaveCount(1);
  await expect(page.getByText("before the switch")).toBeVisible();
  await expect(page.getByText("after the switch")).toBeVisible();
});
