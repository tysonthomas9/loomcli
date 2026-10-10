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

async function open(page: Page, agents = [agent]) {
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
    json(r, { agents, next: "" }),
  );
  for (const a of agents) {
    await page.route(API(`workspaces/w1/v1/agents/${a.agent_id}/events`), (r) =>
      json(r, { events: [], snapshot_seq: 0, next: 0, more: false }),
    );
    await page.route(
      API(`workspaces/w1/v1/agents/${a.agent_id}(\\?.*)?$`),
      (r) => json(r, a),
    );
  }
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

/**
 * Where the switcher scroller sits, which items it shows whole or not at all,
 * which it cuts, and which side shows the "more" hint (MB1b).
 */
function switcherView(page: Page) {
  return page.evaluate(() => {
    const s = document.querySelector<HTMLElement>(
      'nav[aria-label="Primary"] [aria-label="Workspace selector"]',
    )!;
    const w = s.getBoundingClientRect();
    const items = Array.from(s.querySelectorAll("button"));
    const cut: string[] = [];
    const hidden: ("left" | "right")[] = [];
    for (const b of items) {
      const r = b.getBoundingClientRect();
      const shown = Math.min(r.right, w.right) - Math.max(r.left, w.left);
      if (shown <= 0.5) hidden.push(r.right <= w.left + 0.5 ? "left" : "right");
      else if (shown < r.width - 0.5)
        cut.push(`${b.getAttribute("aria-label")} shows ${shown.toFixed(1)}px`);
    }
    // A hint is a visible element marked data-more-hint.
    const hints = Array.from(
      document.querySelectorAll<HTMLElement>(
        'nav[aria-label="Primary"] [data-more-hint]',
      ),
    ).filter((h) => {
      const cs = getComputedStyle(h);
      const r = h.getBoundingClientRect();
      return (
        cs.visibility !== "hidden" &&
        Number(cs.opacity) > 0.5 &&
        r.width > 0 &&
        r.height > 0
      );
    });
    // A hint must not sit on what it points past: any item where it shows,
    // or another rail button's icon.
    const nav = document.querySelector('nav[aria-label="Primary"]')!;
    const marks = [
      ...items.map((b) => {
        const r = b.getBoundingClientRect();
        const left = Math.max(r.left, w.left);
        const right = Math.min(r.right, w.right);
        return { label: b.getAttribute("aria-label"), left, right, r };
      }),
      ...Array.from(nav.querySelectorAll("button svg"))
        .filter((i) => !s.contains(i))
        .map((i) => {
          const r = i.getBoundingClientRect();
          const label = i.closest("button")!.getAttribute("aria-label");
          return { label: `${label} icon`, left: r.left, right: r.right, r };
        }),
    ].filter((m) => m.right - m.left > 0.5);
    const covered = hints.flatMap((h) => {
      const r = h.getBoundingClientRect();
      return marks
        .filter(
          (m) =>
            r.left < m.right - 0.5 &&
            r.right > m.left + 0.5 &&
            r.top < m.r.bottom &&
            r.bottom > m.r.top,
        )
        .map((m) => `${h.dataset.moreHint} hint over ${m.label}`);
    });
    // MB1c: a hint is either a 44px button clear of every other button's tap
    // area, or lets taps through to whatever is under it.
    const buttons = Array.from(nav.querySelectorAll("button"));
    for (const h of hints) {
      const r = h.getBoundingClientRect();
      const side = h.dataset.moreHint;
      if (h.tagName !== "BUTTON") {
        const top = document.elementFromPoint(
          r.left + r.width / 2,
          r.top + r.height / 2,
        );
        if (top && h.contains(top)) covered.push(`${side} hint takes taps`);
        continue;
      }
      if (r.width < 43.5 || r.height < 43.5)
        covered.push(`${side} button is ${r.width}x${r.height}`);
      for (const b of buttons) {
        if (b === h) continue;
        const o = b.getBoundingClientRect();
        const left = s.contains(b) ? Math.max(o.left, w.left) : o.left;
        const right = s.contains(b) ? Math.min(o.right, w.right) : o.right;
        if (r.left < right - 0.5 && r.right > left + 0.5)
          covered.push(`${side} button over ${b.getAttribute("aria-label")}`);
      }
    }
    return {
      covered,
      scrollLeft: s.scrollLeft,
      cut,
      moreLeft: hidden.includes("left"),
      moreRight: hidden.includes("right"),
      hints: hints.map((h) => h.dataset.moreHint).sort(),
    };
  });
}

// 360: a small phone, the switcher's slot is narrower than one item and its
// padding. 470: two items show with more off-screen.
for (const size of [
  { width: 320, height: 640 },
  { width: 360, height: 800 },
  { width: 390, height: 844 },
  { width: 470, height: 844 },
  { width: 557, height: 844 },
]) {
  test(`switcher at ${size.width}px: whole items only, with a hint where more is off-screen`, async ({
    page,
  }) => {
    await page.setViewportSize(size);
    await open(page);
    const switcher = page
      .locator('nav[aria-label="Primary"]')
      .getByRole("region", { name: "Workspace selector" });
    // As opened: the open workspace in view (MB1B_SHOTS=<dir> saves it).
    const shots = process.env.MB1B_SHOTS;
    if (shots)
      for (const theme of ["light", "dark"]) {
        await page.evaluate((t) => {
          document.documentElement.dataset.theme = t;
        }, theme);
        await page.screenshot({
          path: `${shots}/mb1b-${size.width}-${theme}.png`,
        });
      }

    const max = await switcher.evaluate((s) => s.scrollWidth - s.clientWidth);

    // Settled: the same scroll position on two polls in a row, then nothing
    // cut. Returns the view it settled on.
    const settled = async (label: string) => {
      let prev = NaN;
      await expect
        .poll(async () => {
          const v = await switcherView(page);
          const still = v.scrollLeft === prev;
          prev = v.scrollLeft;
          return still ? v.cut : ["still scrolling"];
        }, label)
        .toEqual([]);
      const v = await switcherView(page);
      const want = [
        ...(v.moreLeft ? ["left"] : []),
        ...(v.moreRight ? ["right"] : []),
      ];
      expect(v.hints, `${label}: hints at scrollLeft ${v.scrollLeft}`).toEqual(
        want,
      );
      expect(v.covered, `${label}: hints over items or icons`).toEqual([]);
      return v;
    };

    // The switcher stays in its slot: it overlaps no other rail button.
    const overlaps = await page.evaluate(() => {
      const nav = document.querySelector('nav[aria-label="Primary"]')!;
      const s = nav.querySelector('[aria-label="Workspace selector"]')!;
      const w = s.getBoundingClientRect();
      return Array.from(nav.querySelectorAll("button"))
        .filter((b) => !s.contains(b))
        .filter((b) => {
          const r = b.getBoundingClientRect();
          return r.right > w.left + 0.5 && r.left < w.right - 0.5;
        })
        .map((b) => b.getAttribute("aria-label"));
    });
    expect(overlaps, "rail buttons under the switcher").toEqual([]);

    // As loaded, with the open workspace scrolled into view, its 4px ring
    // (and the same-size focus ring) not clipped (MB1c).
    await settled("as loaded");
    const sw = (await switcher.boundingBox())!;
    const active = (await switcher
      .getByRole("button", { name: "Switch to LOCALMODE" })
      .boundingBox())!;
    expect(active.x - 4, "ring cut on the left").toBeGreaterThanOrEqual(sw.x);
    expect(
      active.x + active.width + 4,
      "ring cut on the right",
    ).toBeLessThanOrEqual(sw.x + sw.width);
    expect(active.y - 4, "ring cut on top").toBeGreaterThanOrEqual(sw.y);
    expect(active.y + active.height + 4, "ring cut below").toBeLessThanOrEqual(
      sw.y + sw.height,
    );

    // Wherever a scroll leaves it (odd offsets included), once it settles no
    // avatar or Add is partly shown, and each side with hidden items says so.
    for (let x = 0; x <= max + 13; x += 13) {
      await switcher.evaluate((s, left) => s.scrollTo({ left }), x);
      await settled(`scrolled to ${x}`);
    }

    // Below 557 some workspace is off-screen at some point, so the hint is
    // actually exercised there.
    if (size.width < 557) expect(max).toBeGreaterThan(0);
  });
}

// MB1c: where the slot has room, the chevrons are buttons that scroll the
// switcher one item at a time, so a mouse or keyboard can reach every
// workspace without swiping.
test("switcher at 557px: the chevrons scroll one item at a time", async ({
  page,
}) => {
  await page.setViewportSize({ width: 557, height: 844 });
  await open(page);
  const rail = page.locator('nav[aria-label="Primary"]');
  const switcher = rail.getByRole("region", { name: "Workspace selector" });
  const left = rail.getByRole("button", { name: "Scroll workspaces left" });
  const right = rail.getByRole("button", { name: "Scroll workspaces right" });
  const at = () => switcher.evaluate((s) => s.scrollLeft);
  await switcher.evaluate((s) => s.scrollTo({ left: 0 }));
  await expect(right).toBeVisible();
  await expect(left).toHaveCount(0);
  const max = await switcher.evaluate((s) => s.scrollWidth - s.clientWidth);
  expect(max).toBeGreaterThan(41);
  for (let x = 41; x <= max; x += 41) {
    await right.click();
    await expect.poll(at).toBe(x);
  }
  await expect(right).toHaveCount(0);
  // The keyboard keeps its place when its chevron goes away at the end.
  await switcher.evaluate((s, left) => s.scrollTo({ left }), max - 41);
  await right.focus();
  await page.keyboard.press("Enter");
  await expect.poll(at).toBe(max);
  await expect(left).toBeFocused();
  await page.keyboard.press("Enter");
  await expect.poll(at).toBe(max - 41);
  if (process.env.MB1C_SHOTS)
    await page.screenshot({ path: `${process.env.MB1C_SHOTS}/mb1c-557.png` });
});

// MOB2: on a phone the sidebar is hidden, so the bottom rail's Agents button
// opens the same agent list as a drawer (MOB2_SHOTS=<dir> saves screenshots).
const other = { ...agent, agent_id: "a2", name: "docs-writer" };
for (const size of [
  { width: 390, height: 844 },
  { width: 557, height: 844 },
]) {
  test(`agents drawer at ${size.width}px: the rail's Agents button opens the agent list`, async ({
    page,
  }) => {
    await page.setViewportSize(size);
    await open(page, [agent, other]);
    const rail = page.locator('nav[aria-label="Primary"]');
    const button = rail.getByRole("button", { name: "Agents" });
    const row = page.getByRole("link", { name: /^docs-writer / });
    await expect(button).toBeVisible();
    await expect(button).toHaveAttribute("aria-expanded", "false");
    await expect(row).toBeHidden();

    await button.click();
    await expect(button).toHaveAttribute("aria-expanded", "true");
    await expect(row).toBeVisible();
    // The open chat's row is marked as the current one.
    await expect(
      page.getByRole("link", { name: new RegExp(`^${NAME} `) }),
    ).toHaveAttribute("aria-current", "page");
    // The drawer sits between the header and the rail, inside the screen,
    // and its rows can be clicked (nothing on top of them).
    const drawer = page.getByRole("complementary", { name: "Agents" });
    const d = (await drawer.boundingBox())!;
    const r = (await rail.boundingBox())!;
    expect(d.x).toBeGreaterThanOrEqual(0);
    expect(d.x + d.width).toBeLessThanOrEqual(size.width);
    expect(d.y + d.height).toBeLessThanOrEqual(r.y + 0.5);
    expect(d.y).toBeGreaterThan(0);
    await row.click({ trial: true });
    expect((await overflowing(page)).out, "with the drawer open").toEqual([]);
    if (process.env.MOB2_SHOTS)
      for (const theme of ["light", "dark"]) {
        await page.evaluate((t) => {
          document.documentElement.dataset.theme = t;
        }, theme);
        await page.screenshot({
          path: `${process.env.MOB2_SHOTS}/mob2-${size.width}-${theme}.png`,
        });
      }

    // A row opens that agent's chat, as on the desktop, and the drawer closes.
    await row.click();
    await expect(page).toHaveURL(/\/ws\/w1\/chat\/a2$/);
    await expect(
      page.getByRole("heading", { name: "docs-writer" }),
    ).toBeVisible();
    await expect(row).toBeHidden();
    await expect(button).toHaveAttribute("aria-expanded", "false");

    // Escape closes it and gives focus back to the button.
    await button.click();
    await expect(row).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(row).toBeHidden();
    await expect(button).toBeFocused();

    // So does a tap outside it.
    await button.click();
    await expect(row).toBeVisible();
    await page.mouse.click(size.width - 5, size.height / 2);
    await expect(row).toBeHidden();

    // Tapping the button again closes it too.
    await button.click();
    await expect(row).toBeVisible();
    await button.click();
    await expect(row).toBeHidden();
  });
}

// Files and Settings bring their own tree and hide the sidebar; the drawer
// still opens there, and a row still leads back to a chat.
test("agents drawer at 390px: opens on a view without the sidebar", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page, [agent, other]);
  const rail = page.locator('nav[aria-label="Primary"]');
  await rail.getByRole("button", { name: "Settings" }).click();
  await expect(page).toHaveURL(/\/ws\/w1\/settings/);
  const row = page.getByRole("link", { name: /^docs-writer / });
  await expect(row).toBeHidden();
  await rail.getByRole("button", { name: "Agents" }).click();
  await expect(row).toBeVisible();
  await row.click();
  await expect(page).toHaveURL(/\/ws\/w1\/chat\/a2$/);
  await expect(row).toBeHidden();
});

// The drawer always lists the agents as full rows (name shown): also when
// the tree was collapsed, and on Terminal, whose sidebar otherwise lists
// terminal sessions.
test("agents drawer at 390px: full rows when the tree was collapsed", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.addInitScript(() =>
    localStorage.setItem("loom:w1:tree-collapsed", "true"),
  );
  await open(page, [agent, other]);
  await page
    .locator('nav[aria-label="Primary"]')
    .getByRole("button", { name: "Agents" })
    .click();
  const name = page
    .getByRole("link", { name: /^docs-writer / })
    .getByTestId("agent-list-name");
  await expect(name).toBeVisible();
  // There is nothing to collapse or resize in the drawer.
  await expect(
    page.getByRole("button", { name: "Collapse workspace tree" }),
  ).toBeHidden();
  await expect(page.getByLabel("Resize workspace sidebar")).toHaveCount(0);
  // The saved (desktop) collapse preference is left as it was.
  expect(
    await page.evaluate(() => localStorage.getItem("loom:w1:tree-collapsed")),
  ).toBe("true");
});

test("agents drawer at 390px: lists the agents on Terminal", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page, [agent, other]);
  const rail = page.locator('nav[aria-label="Primary"]');
  await rail.getByRole("button", { name: "Terminal" }).click();
  await expect(page).toHaveURL(/\/ws\/w1\/terminal/);
  await rail.getByRole("button", { name: "Agents" }).click();
  await expect(
    page
      .getByRole("link", { name: /^docs-writer / })
      .getByTestId("agent-list-name"),
  ).toBeVisible();
});

// A dialog opened from the drawer (New Agent) is on top: Escape must not
// close the drawer underneath it.
test("agents drawer at 390px: Escape in a dialog opened from it keeps it open", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page, [agent, other]);
  await page
    .locator('nav[aria-label="Primary"]')
    .getByRole("button", { name: "Agents" })
    .click();
  const row = page.getByRole("link", { name: /^docs-writer / });
  await expect(row).toBeVisible();
  await page.getByRole("button", { name: "+ Add agent" }).click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(row).toBeVisible();
});

// A row's context menu takes the first Escape, as on the desktop; the next
// one closes the drawer.
test("agents drawer at 390px: a row menu takes the first Escape", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page, [agent, other]);
  await page
    .locator('nav[aria-label="Primary"]')
    .getByRole("button", { name: "Agents" })
    .click();
  const row = page.getByRole("link", { name: /^docs-writer / });
  await row.click({ button: "right" });
  const menu = page.getByRole("menu");
  await expect(menu).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(menu).toBeHidden();
  await expect(row).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(row).toBeHidden();
});

// The smallest phones: the rail, Agents button included, fits on screen.
test("rail at 320px: every control on screen", async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 640 });
  await open(page);
  const rights = await page
    .locator('nav[aria-label="Primary"] > button')
    .evaluateAll((els) =>
      els.map((e) => ({
        label: e.getAttribute("aria-label"),
        left: e.getBoundingClientRect().left,
        right: e.getBoundingClientRect().right,
      })),
    );
  expect(rights.map((r) => r.label)).toContain("Agents");
  for (const r of rights) {
    expect(r.left, String(r.label)).toBeGreaterThanOrEqual(0);
    expect(r.right, String(r.label)).toBeLessThanOrEqual(320);
  }
  if (process.env.MOB2_SHOTS)
    await page.screenshot({ path: `${process.env.MOB2_SHOTS}/mob2-320.png` });
});

test("agents drawer: no Agents button on the desktop, where the sidebar shows", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await open(page, [agent, other]);
  await expect(page.getByRole("link", { name: /^docs-writer / })).toBeVisible();
  await expect(
    page
      .locator('nav[aria-label="Primary"]')
      .getByRole("button", { name: "Agents" }),
  ).toBeHidden();
});
