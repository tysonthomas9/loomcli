/**
 * Agent lifecycle in a real browser against a mocked Agent API (1.8b,
 * design v2 §4.7–§4.8): archive → read-only → unarchive, delete with a
 * dirty-work refusal and Delete anyway, the attention banner, history expired, and the
 * harness-context divider on reload and on live replay. Archive and Delete
 * are in the sidebar row's right-click menu (SB4), so those cases open the
 * chat with the sidebar beside it.
 */
import { expect, test } from "@playwright/test";
import type { Page, Route } from "@playwright/test";

const BASE = "**/api/workspaces/w1";
const SHOTS = process.env.DA1_SHOTS;

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
    created_at: "2026-10-04T00:00:00Z",
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

async function open(page: Page, m: Mock, query = "") {
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
  // The sidebar's List.
  await page.route(/\/api\/workspaces\/w1\/v1\/agents(\?.*)?$/, (r) =>
    json(r, { agents: [m.agent], next: "" }),
  );
  await page.route(`${BASE}/v1/agents/a1/events*`, (r) =>
    json(r, { events: m.events, snapshot_seq: seq, next: seq, more: false }),
  );
  // With or without the Delete anyway ?fingerprint= query.
  await page.route(/\/api\/workspaces\/w1\/v1\/agents\/a1(\?.*)?$/, (r) => {
    if (r.request().method() !== "DELETE") return json(r, m.agent);
    const confirmed = r.request().url().includes("fingerprint=f1");
    m.writes.push(confirmed ? "delete:f1" : "delete");
    if (confirmed) return r.fulfill({ status: 204 });
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
  await page.goto(`/test/agent-chat${query}`);
  // The name shows in the chat title, and in the sidebar row with ?sidebar=1.
  await expect(page.getByText(String(m.agent.name)).first()).toBeVisible();
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

// The sidebar row's right-click menu item (SB4).
async function rowMenu(page: Page, item: "Archive" | "Delete") {
  await page.getByRole("link", { name: "lead Agent opencode" }).click({
    button: "right",
  });
  await page.getByRole("menuitem", { name: item }).click();
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
  await open(page, m, "?sidebar=1&w=800");
  await expect(composer(page)).toBeVisible();
  await expect(page.getByTestId("agent-archive")).toHaveCount(0);

  await rowMenu(page, "Archive");
  await expect(
    page.getByRole("link", { name: "lead Agent opencode" }),
  ).toHaveCount(0);
  // The chat hears of it from the stream, as from any other client.
  await push(
    page,
    m,
    ev("agent.state_changed", { from: "idle", to: "archived" }),
  );
  await expect(page.getByTestId("agent-archived-notice")).toContainText(
    "History expires in 30 days.",
  );
  await expect(composer(page)).toHaveCount(0);
  await expect(page.getByText("earlier work")).toBeVisible();

  await page.getByTestId("agent-unarchive").click();
  await expect(composer(page)).toBeVisible();
  await expect(page.getByTestId("agent-archived-notice")).toHaveCount(0);
  expect(m.writes).toEqual(["archive", "unarchive"]);
});

test("delete asks first and shows the server's dirty-work refusal", async ({
  page,
}) => {
  const m = mock();
  await open(page, m, "?sidebar=1&w=800");
  await expect(page.getByTestId("agent-delete")).toHaveCount(0);
  await rowMenu(page, "Delete");
  expect(m.writes).toEqual([]);
  await page.getByTestId("confirm-dialog-confirm").click();
  const refusal = page.getByRole("alertdialog", { name: "Not deleted" });
  await expect(refusal).toContainText(
    "Uncommitted changes in /wt/a1: main.go. Delete anyway loses these changes.",
  );
  expect(m.writes).toEqual(["delete"]);

  // The title says Not deleted; the body does not repeat it.
  await expect(refusal).not.toContainText("Not deleted:");
  for (const theme of ["light", "dark"]) {
    await page.evaluate((t) => {
      document.documentElement.dataset.theme = t;
    }, theme);
    if (SHOTS)
      await page.screenshot({
        path: `${SHOTS}/da1-${theme}.png`,
        animations: "disabled",
      });
  }
  // No second confirm: the row leaves the sidebar. The fixture has no
  // /ws/:ws/chat/:id route, so leaving the open chat for /ws/w1/home is
  // covered by AgentList.test.tsx ("leaves the open chat once its agent's
  // delete succeeds").
  await page.getByTestId("agent-delete-anyway").click();
  await expect(
    page.getByRole("link", { name: "lead Agent opencode" }),
  ).toHaveCount(0);
  await expect(page.getByRole("alertdialog")).toHaveCount(0);
  expect(m.writes).toEqual(["delete", "delete:f1"]);
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
