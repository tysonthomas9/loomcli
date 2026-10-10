/**
 * The sidebar's Agent API rows reorder by a real pointer drag (SB4), in a
 * real browser against a mocked Agent API: a Lead dragged above another
 * moves with its pinned child, and the order survives a reload.
 */
import { expect, test } from "@playwright/test";
import type { Page, Route } from "@playwright/test";

const agent = (id: string, over: object = {}) => ({
  agent_id: id,
  name: id,
  harness: "opencode",
  preset: "lead",
  role_kind: "interactive",
  state: "idle",
  parent_agent_id: null,
  created_at: "2026-10-04T00:00:00Z",
  running_turn_id: null,
  archived_at: null,
  attention_reason: null,
  history_purged_at: null,
  waiting_messages: [],
  open_asks: [],
  ...over,
});

// lead1 (the open chat, a1) is listed first, with a working child.
const AGENTS = [
  agent("a1", { name: "lead1" }),
  agent("lead2", { created_at: "2026-10-04T00:00:01Z" }),
  agent("kid", {
    preset: "task",
    role_kind: "worker",
    state: "active",
    parent_agent_id: "a1",
    created_at: "2026-10-04T00:00:02Z",
  }),
];

const json = (route: Route, body: unknown, status = 200) =>
  route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });

async function open(page: Page) {
  await page.route("**/api/config", (r) => json(r, { mode: "open" }));
  await page.route("**/api/workspaces/w1/events/token", (r) =>
    r.fulfill({ status: 404 }),
  );
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
  await page.route(
    (u) => u.pathname.startsWith("/api/workspaces/w1/v1/agents"),
    (r) => {
      const url = new URL(r.request().url());
      const path = url.pathname;
      if (path.endsWith("/v1/agents")) {
        const parent = url.searchParams.get("parent");
        const agents = parent
          ? AGENTS.filter((a) => a.parent_agent_id === parent)
          : AGENTS;
        return json(r, { agents, next: "" });
      }
      if (path.endsWith("/events"))
        return json(r, { events: [], snapshot_seq: 0, next: 0, more: false });
      const a = AGENTS.find((x) => path.endsWith(`/${x.agent_id}`));
      return a ? json(r, a) : r.fulfill({ status: 404 });
    },
  );
  await page.goto("/test/agent-chat?sidebar=1&w=800");
  await expect(page.getByTestId("agent-list-name")).toHaveCount(3);
}

const names = (page: Page) =>
  page.getByTestId("agent-list-name").allTextContents();

// A pointer drag by the row's handle: past dnd-kit's 5px activation, then
// onto the top of the target row.
async function drag(page: Page, from: string, onto: string) {
  const row = (name: string) =>
    page.getByRole("link", { name: `${name} Lead opencode` });
  await row(from).hover();
  const handle = await page.getByLabel(`Drag to reorder ${from}`).boundingBox();
  const target = await row(onto).boundingBox();
  if (!handle || !target) throw new Error("rows not laid out");
  const x = handle.x + handle.width / 2;
  const y = handle.y + handle.height / 2;
  await page.mouse.move(x, y);
  await page.mouse.down();
  await page.mouse.move(x, y - 10, { steps: 5 });
  await page.mouse.move(x, target.y + 2, { steps: 15 });
  await page.mouse.up();
}

test("a Lead dragged above another keeps its child under it, and the order survives a reload", async ({
  page,
}) => {
  await open(page);
  expect(await names(page)).toEqual(["lead1", "kid", "lead2"]);
  // A child has no handle of its own.
  await expect(page.getByLabel("Drag to reorder kid")).toHaveCount(0);

  await drag(page, "lead2", "lead1");
  await expect.poll(() => names(page)).toEqual(["lead2", "lead1", "kid"]);
  const children = page.getByRole("group", { name: "lead1 children" });
  await expect(children.getByTestId("agent-list-name")).toHaveText(["kid"]);

  await page.reload();
  await expect(page.getByTestId("agent-list-name")).toHaveCount(3);
  expect(await names(page)).toEqual(["lead2", "lead1", "kid"]);
  await expect(children.getByTestId("agent-list-name")).toHaveText(["kid"]);
});
