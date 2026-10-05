/**
 * The collapsed sidebar's agent rail lists the Agent API agents the
 * expanded tree does (RAIL1): Leads, plus a child only while it is at work
 * (SB2). Clicking one opens its chat, and the open chat's avatar is
 * highlighted. The whole app runs against a mocked workspace and Agent API.
 */
import { expect, test } from "@playwright/test";
import type { Page, Route } from "@playwright/test";

const WS = "ws-rail";
const SHOTS = process.env.RAIL1_SHOTS;

const workspace = {
  id: WS,
  name: "rail-test",
  path: `/workspaces/${WS}`,
  repos: [],
  groups: [],
  agents: [],
  workspaces: [
    {
      id: WS,
      name: "rail-test",
      path: `/workspaces/${WS}`,
      active: true,
      repo_count: 0,
      is_default: true,
    },
  ],
  workspace_order: [WS],
  default_workspace: WS,
};

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

// Two Leads; lead1 has a working child and a finished one (hidden, SB2).
const AGENTS = [
  agent("a1", { name: "lead1" }),
  agent("a2", { name: "lead2", created_at: "2026-10-04T00:00:01Z" }),
  agent("k1", {
    name: "busy-kid",
    preset: "task",
    role_kind: "worker",
    state: "active",
    parent_agent_id: "a1",
    created_at: "2026-10-04T00:00:02Z",
  }),
  agent("k2", {
    name: "done-kid",
    preset: "task",
    role_kind: "worker",
    state: "finished",
    outcome: "completed",
    parent_agent_id: "a1",
    created_at: "2026-10-04T00:00:03Z",
  }),
];

const json = (route: Route, body: unknown, status = 200) =>
  route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
const ok = (route: Route, data: unknown) =>
  json(route, { success: true, data });

async function open(page: Page) {
  await page.route("**/api/config", (r) => json(r, { mode: "open" }));
  await page.route("**/api/health", (r) => json(r, { status: "ok" }));
  await page.route("**/api/backends", (r) => ok(r, []));
  await page.route("**/api/monitor/**", (r) => json(r, {}));
  // A route cannot hold an SSE response open: the page gets an EventSource
  // that stays open until closed.
  await page.addInitScript(() => {
    window.EventSource = class extends EventTarget {
      onopen: ((e: Event) => void) | null = null;
      onerror: ((e: Event) => void) | null = null;
      onmessage: ((e: MessageEvent) => void) | null = null;
      readyState = 1;
      constructor(public url: string) {
        super();
        setTimeout(() => this.onopen?.(new Event("open")));
      }
      close() {
        this.readyState = 2;
      }
    } as unknown as typeof EventSource;
  });
  await page.route("**/api/workspaces/**", (r) => {
    const path = new URL(r.request().url()).pathname;
    if (path === "/api/workspaces/active" || path === `/api/workspaces/${WS}`)
      return ok(r, workspace);
    if (/\/(issues|ready|blocked|issues\/graph|terminal\/tabs)$/.test(path))
      return ok(r, []);
    if (path.endsWith("/events/token")) return r.fulfill({ status: 404 });
    return json(r, { error: "not found" }, 404);
  });
  // The Agent API, registered last so it wins over the workspace catch-all.
  await page.route(
    (u) => u.pathname.startsWith(`/api/workspaces/${WS}/v1/agents`),
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
  await page.goto(`/ws/${WS}/home`);
  await page.getByRole("button", { name: "Collapse workspace tree" }).click();
}

const rail = (page: Page) => page.getByTestId("collapsed-agent-rail");
const railAgent = (page: Page, name: string) =>
  rail(page).getByRole("link", { name: new RegExp(`^${name} `) });

test("the collapsed rail lists Agent API Leads and working children, opens a chat, and highlights it", async ({
  page,
}) => {
  await open(page);

  // Leads first, a working child after its Lead, a finished child hidden.
  const links = rail(page).getByRole("link");
  await expect(links).toHaveCount(3);
  await expect(railAgent(page, "lead1")).toBeVisible();
  await expect(railAgent(page, "busy-kid")).toBeVisible();
  await expect(railAgent(page, "lead2")).toBeVisible();
  await expect(railAgent(page, "done-kid")).toHaveCount(0);
  await expect(links.nth(0)).toHaveAttribute("aria-label", /^lead1 /);
  await expect(links.nth(1)).toHaveAttribute("aria-label", /^busy-kid /);
  await expect(links.nth(2)).toHaveAttribute("aria-label", /^lead2 /);
  for (const name of ["lead1", "busy-kid", "lead2"])
    await expect(railAgent(page, name)).not.toHaveAttribute(
      "aria-current",
      "page",
    );

  await railAgent(page, "lead2").click();
  await expect(page).toHaveURL(new RegExp(`/ws/${WS}/chat/a2$`));
  await expect(railAgent(page, "lead2")).toHaveAttribute(
    "aria-current",
    "page",
  );
  await expect(railAgent(page, "lead1")).not.toHaveAttribute(
    "aria-current",
    "page",
  );
  // The chat opens on the agent.
  await expect(page.getByText("lead2").first()).toBeVisible();

  if (SHOTS) {
    for (const theme of ["light", "dark"] as const) {
      await page.evaluate(
        (t) => document.documentElement.setAttribute("data-theme", t),
        theme,
      );
      await page.screenshot({
        path: `${SHOTS}/rail1-collapsed-${theme}.png`,
        clip: { x: 0, y: 0, width: 420, height: 520 },
      });
    }
  }
});
