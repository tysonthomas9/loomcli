/**
 * The app on a phone (390x844) and a narrow tablet (557px) wide, on an agent's
 * chat (MB1): nothing lays out past the right edge, the bottom rail is one
 * row with the workspace switcher still reachable, the agent's name is
 * readable, and the composer is not covered.
 */
import { expect, test } from "@playwright/test";
import type { Page, Route } from "@playwright/test";

// MB1_SHOTS=<dir> also saves light and dark screenshots at each width.
const SHOTS = process.env.MB1_SHOTS;
const NAME = "slack-researcher";
// The open workspace (w1) is last, so on a phone it starts off the visible
// part of the switcher and has to be scrolled into view.
const WORKSPACES = ["loomcli", "docs-site", "infra", "LOCALMODE"].map(
  (name, i) => ({
    id: name === "LOCALMODE" ? "w1" : `w${i + 2}`,
    name,
    path: `/workspaces/${name}`,
    active: name === "LOCALMODE",
    repo_count: 1,
    is_default: name === "LOCALMODE",
  }),
);
const workspace = {
  id: "w1",
  name: "LOCALMODE",
  path: "/workspaces/LOCALMODE",
  repos: [
    {
      name: "repo-one",
      path: "/workspaces/LOCALMODE/repo-one",
      default_branch: "main",
      remote: "origin",
      groups: [],
    },
  ],
  groups: [],
  agents: [],
  workspaces: WORKSPACES,
  workspace_order: WORKSPACES.map((w) => w.id),
  default_workspace: "w1",
};
const agent = {
  agent_id: "a1",
  name: NAME,
  harness: "opencode",
  repo: "repo-one",
  branch: "loom/slack-researcher",
  state: "idle",
  created_at: "2026-10-04T00:00:00Z",
  running_turn_id: null,
  archived_at: null,
  attention_reason: null,
  history_purged_at: null,
  waiting_messages: [],
  open_asks: [],
};

const json = (route: Route, body: unknown, status = 200) =>
  route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
const ok = (route: Route, data: unknown) =>
  json(route, { success: true, data });
// The app's API only: a glob like **/api/** also matches Vite's /src/api/ modules.
const API = (path: string) => new RegExp(`^https?://[^/]+/api/${path}`);

async function open(page: Page) {
  // Playwright matches the last route first: the catch-all goes first so no
  // request reaches a real server.
  await page.route(API(""), (r) => json(r, { error: "not found" }, 404));
  await page.route(API("workspaces/"), (r) => {
    const p = new URL(r.request().url()).pathname;
    if (
      p === "/api/workspaces/active" ||
      /^\/api\/workspaces\/[^/]+\/?$/.test(p)
    )
      return ok(r, workspace);
    if (/\/(ready|issues|issues\/graph|blocked)$/.test(p)) return ok(r, []);
    if (/\/terminal\/tabs(\/|$)/.test(p)) return ok(r, []);
    if (/\/terminal\/state$/.test(p)) return json(r, { active_tab: "" });
    return json(r, { error: "not found" }, 404);
  });
  await page.route(API("config$"), (r) => json(r, { mode: "open" }));
  await page.route(API("health$"), (r) => json(r, { status: "ok" }));
  await page.addInitScript(() => {
    window.EventSource = class extends EventTarget {
      onopen: ((e: Event) => void) | null = null;
      constructor(public url: string) {
        super();
      }
      close() {}
    } as unknown as typeof EventSource;
  });
  await page.route(API("workspaces/w1/v1/agents(\\?.*)?$"), (r) =>
    json(r, { agents: [agent], next: "" }),
  );
  await page.route(API("workspaces/w1/v1/agents/a1/events"), (r) =>
    json(r, { events: [], snapshot_seq: 0, next: 0, more: false }),
  );
  await page.route(API("workspaces/w1/v1/agents/a1(\\?.*)?$"), (r) =>
    json(r, agent),
  );
  await page.goto("/ws/w1/chat/a1");
  await expect(page.getByRole("heading", { name: NAME })).toBeVisible();
  await expect(page.getByPlaceholder("Ask anything...")).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Switch to infra" }),
  ).toBeAttached();
}

/**
 * Every rendered element whose visible right edge is past the viewport. An
 * element inside a scroll or clip container only counts where it shows.
 */
function overflowing(page: Page) {
  return page.evaluate(() => {
    const out: string[] = [];
    const vw = window.innerWidth;
    for (const el of Array.from(document.body.querySelectorAll("*"))) {
      const r = el.getBoundingClientRect();
      // Not rendered, or hidden (a closed drawer parked past the edge).
      if (r.width === 0 || r.height === 0) continue;
      if (getComputedStyle(el).visibility === "hidden") continue;
      let right = r.right;
      // A clip at the viewport edge itself (body, a full-width wrapper) does
      // not count: content it hides is laid out off-screen, the bug.
      for (let p = el.parentElement; p; p = p.parentElement) {
        const pr = p.getBoundingClientRect().right;
        if (getComputedStyle(p).overflowX !== "visible" && pr < vw - 0.5)
          right = Math.min(right, pr);
      }
      if (right > vw + 0.5 && r.left < right)
        out.push(
          `${el.tagName.toLowerCase()}.${String(el.className)} right=${Math.round(right)}`,
        );
    }
    return { out, scrollWidth: document.documentElement.scrollWidth, vw };
  });
}

for (const size of [
  { width: 390, height: 844 },
  { width: 557, height: 844 },
]) {
  test(`chat at ${size.width}px: no overflow, one-row rail, readable name, composer clear`, async ({
    page,
  }) => {
    await page.setViewportSize(size);
    await open(page);

    const o = await overflowing(page);
    expect(o.out, "elements past the right edge").toEqual([]);
    expect(o.scrollWidth).toBeLessThanOrEqual(o.vw);

    // The rail is one row: every control sits inside the rail's own box.
    const rail = page.locator('nav[aria-label="Primary"]');
    const box = (await rail.boundingBox())!;
    expect(box.height).toBeLessThanOrEqual(64);
    expect(box.y + box.height).toBeCloseTo(size.height, 0);
    const controls = rail.locator("button");
    const centers = await controls.evaluateAll((els) =>
      els.map((e) => {
        const r = e.getBoundingClientRect();
        return { label: e.getAttribute("aria-label"), y: r.top + r.height / 2 };
      }),
    );
    for (const c of centers)
      expect(
        Math.abs(c.y - (box.y + box.height / 2)),
        String(c.label),
      ).toBeLessThan(4);

    // The open workspace is shown, ring included, without scrolling.
    const switcher = rail.getByRole("region", { name: "Workspace selector" });
    const sw = (await switcher.boundingBox())!;
    const active = (await rail
      .getByRole("button", { name: "Switch to LOCALMODE" })
      .boundingBox())!;
    expect(active.x - 4).toBeGreaterThanOrEqual(sw.x);
    expect(active.x + active.width + 4).toBeLessThanOrEqual(sw.x + sw.width);

    // The workspace switcher stays reachable: each avatar and Add can be
    // clicked (scrolled to if needed, nothing on top of it).
    for (const w of WORKSPACES)
      await rail
        .getByRole("button", { name: `Switch to ${w.name}` })
        .click({ trial: true });
    const add = rail.getByRole("button", { name: "Add workspace" });
    await add.click({ trial: true });

    // Its tooltip stays on screen too.
    await add.focus();
    await expect(
      page.getByRole("tooltip", { name: "Add workspace" }),
    ).toBeVisible();
    expect((await overflowing(page)).out, "with the tooltip open").toEqual([]);
    await add.blur();

    // The name is not cut off.
    const title = page.getByRole("heading", { name: NAME });
    const cut = await title.evaluate((e) => e.scrollWidth > e.clientWidth);
    expect(cut, "agent name truncated").toBe(false);

    // The whole composer, footer and Send included, is above the rail and
    // nothing covers it.
    const prompt = page.getByPlaceholder("Ask anything...");
    const form = page.locator("form").filter({ has: prompt });
    const c = (await form.boundingBox())!;
    expect(c.y + c.height).toBeLessThanOrEqual(box.y);
    await prompt.click({ trial: true });
    // Send is disabled while empty, so hit-test each control instead.
    const covered = await form.evaluate((f) =>
      Array.from(f.querySelectorAll("button")).flatMap((b) => {
        const r = b.getBoundingClientRect();
        const top = document.elementFromPoint(
          r.left + r.width / 2,
          r.top + r.height / 2,
        );
        return top && b.contains(top) ? [] : [b.getAttribute("aria-label")];
      }),
    );
    expect(covered, "composer controls under something").toEqual([]);

    if (SHOTS)
      for (const theme of ["light", "dark"]) {
        await page.evaluate((t) => {
          document.documentElement.dataset.theme = t;
        }, theme);
        await page.screenshot({
          path: `${SHOTS}/mb1-${size.width}-${theme}.png`,
        });
      }
  });
}
