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

async function open(page: Page, query = "sidebar=1&w=800") {
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
      if (path.endsWith("/archive")) return json(r, {});
      const a = AGENTS.find((x) => path.endsWith(`/${x.agent_id}`));
      return a ? json(r, a) : r.fulfill({ status: 404 });
    },
  );
  await page.goto(`/test/agent-chat?${query}`);
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

for (const width of [390, 800]) {
  test(`keyboard drag at ${width}px reorders a Lead, Escape cancels, and row actions stay isolated`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 844 });
    await open(page);
    const handle = page.getByLabel("Drag to reorder lead2");
    const item = page
      .getByTestId("sortable-agent-item")
      .filter({ has: page.getByRole("link", { name: "lead2 Lead opencode" }) });
    const moveUp = async () => {
      const first = await page
        .getByTestId("sortable-agent-item")
        .first()
        .boundingBox();
      if (!first) throw new Error("first Lead is not laid out");
      await page.keyboard.press("ArrowUp");
      await expect
        .poll(async () => (await item.boundingBox())?.y)
        .toBeLessThan(first.y + first.height / 2);
    };
    await expect(page.getByLabel("Drag to reorder kid")).toHaveCount(0);

    await handle.focus();
    await page.keyboard.press("Space");
    await expect(item).toHaveAttribute("data-dragging", "true");
    await moveUp();
    await page.keyboard.press("Space");
    await expect.poll(() => names(page)).toEqual(["lead2", "lead1", "kid"]);
    await expect(
      page
        .getByRole("group", { name: "lead1 children" })
        .getByTestId("agent-list-name"),
    ).toHaveText(["kid"]);
    await expect(handle).toBeFocused();

    await page.reload();
    await expect(page.getByTestId("agent-list-name")).toHaveCount(3);
    expect(await names(page)).toEqual(["lead2", "lead1", "kid"]);
    await handle.focus();
    await page.keyboard.press("Space");
    await expect(item).toHaveAttribute("data-dragging", "true");
    await page.keyboard.press("ArrowDown");
    await page.evaluate(
      () =>
        new Promise<void>((resolve) =>
          requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
        ),
    );
    await page.keyboard.press("Escape");
    await expect(item).not.toHaveAttribute("data-dragging", "true");
    expect(await names(page)).toEqual(["lead2", "lead1", "kid"]);

    const archiveRequest = page.waitForRequest(
      (request) =>
        request.url().endsWith("/v1/agents/lead2/archive") &&
        request.method() === "POST",
    );
    await page.getByRole("button", { name: "Archive lead2" }).focus();
    await page.keyboard.press("Enter");
    await archiveRequest;
    await expect(page.getByTestId("agent-list-name")).toHaveText([
      "lead1",
      "kid",
    ]);
    await expect(page).toHaveURL(/\/test\/agent-chat\?sidebar=1&w=800$/);

    const link = page.getByRole("link", { name: "lead1 Lead opencode" });
    await expect(link).toHaveAttribute("href", "/ws/w1/chat/a1");
    await link.focus();
    await page.keyboard.press("Enter");
    await page.waitForURL((url) => url.pathname !== "/test/agent-chat");
  });
}

// SB7: the selected row's highlight spans the whole row, its archive button
// and drag grip included, for a Lead and for a child.
for (const [id, name] of [
  ["a1", "lead1"],
  ["kid", "kid"],
]) {
  test(`the selected ${name} row is highlighted across its full width`, async ({
    page,
  }) => {
    await open(page, `sidebar=1&open=1&agent=${id}&w=800`);
    const row = page
      .getByTestId("sortable-agent-row")
      .filter({ has: page.locator('[aria-current="page"]') });
    await expect(row).toHaveCount(1);
    await row.hover();
    // The background painted at a point: the first non-transparent one
    // from the element there up to the row, and that element's width.
    const paint = (x: number, y: number) =>
      row.evaluate(
        (el, [px, py]) => {
          let at = document.elementFromPoint(px!, py!);
          for (; at && el.contains(at); at = at.parentElement) {
            const bg = getComputedStyle(at).backgroundColor;
            if (bg !== "rgba(0, 0, 0, 0)" && bg !== "transparent")
              return { bg, width: at.getBoundingClientRect().width };
          }
          return null;
        },
        [x, y],
      );
    const box = await row.boundingBox();
    if (!box) throw new Error("row not laid out");
    const y = box.y + box.height / 2;
    const left = await paint(box.x + 8, y);
    const right = await paint(box.x + box.width - 2, y);
    expect(left).not.toBeNull();
    expect(right).toEqual({ bg: left!.bg, width: box.width });
  });
}
