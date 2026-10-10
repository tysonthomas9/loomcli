/**
 * A chat URL for an agent that no longer exists (NF1): Get answers 404
 * agent_not_found, and the chat shows "Agent not found" with a link home,
 * not the raw agent id and not a composer. On a desktop and a phone.
 */
import { expect, test } from "@playwright/test";
import type { Page, Route } from "@playwright/test";

// NF1_SHOTS=<dir> also saves light and dark screenshots at each width.
const SHOTS = process.env.NF1_SHOTS;
const ID = "agt_doesnotexist";
const workspace = {
  id: "w1",
  name: "LOCALMODE",
  path: "/workspaces/LOCALMODE",
  // A repo, so the first-run onboarding checklist stays closed.
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
  workspaces: [
    {
      id: "w1",
      name: "LOCALMODE",
      path: "/workspaces/LOCALMODE",
      active: true,
      repo_count: 1,
      is_default: true,
    },
  ],
  workspace_order: ["w1"],
  default_workspace: "w1",
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
    json(r, { agents: [], next: "" }),
  );
  await page.route(API(`workspaces/w1/v1/agents/${ID}/events`), (r) =>
    json(r, { error: ID, code: "agent_not_found" }, 404),
  );
  await page.route(API(`workspaces/w1/v1/agents/${ID}(\\?.*)?$`), (r) =>
    json(r, { error: ID, code: "agent_not_found" }, 404),
  );
  await page.goto(`/ws/w1/chat/${ID}`);
}

for (const size of [
  { width: 1280, height: 800 },
  { width: 390, height: 844 },
]) {
  test(`a missing agent's chat says Agent not found at ${size.width}px`, async ({
    page,
  }) => {
    await page.setViewportSize(size);
    await open(page);
    const state = page.getByTestId("agent-not-found");
    await expect(
      state.getByRole("heading", { name: "Agent not found" }),
    ).toBeVisible();
    await expect(state).toContainText(
      "This agent no longer exists. It may have been deleted.",
    );
    await expect(state.getByRole("link", { name: "Go home" })).toBeVisible();
    await expect(page.getByTestId("agent-api-page")).not.toContainText(ID);
    await expect(page.getByPlaceholder("Ask anything...")).toHaveCount(0);
    if (SHOTS)
      for (const theme of ["light", "dark"]) {
        await page.evaluate((t) => {
          document.documentElement.dataset.theme = t;
        }, theme);
        await page.screenshot({
          path: `${SHOTS}/nf1-${size.width}-${theme}.png`,
        });
      }
    await state.getByRole("link", { name: "Go home" }).click();
    await expect(page).toHaveURL(/\/ws\/w1\/home$/);
  });
}

// DEL3: a chat whose agent is deleted, on load (Get says state=deleted) or
// while open (the live-only agent.deleted from another tab or the API).
const LIVE = "agt_live";
const liveAgent = (state: string) => ({
  agent_id: LIVE,
  name: "doomed",
  repo: "/workspaces/LOCALMODE/repo-one",
  harness: "opencode",
  state,
  running_turn_id: null,
  waiting_messages: [],
  open_asks: [],
  created_at: "2026-10-04T00:00:00Z",
});

async function openLive(page: Page, state: string) {
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
      onerror: ((e: Event) => void) | null = null;
      readyState = 1;
      constructor(public url: string) {
        super();
        const w = window as unknown as { __sse?: EventTarget[] };
        (w.__sse ??= []).push(this);
        setTimeout(() => this.onopen?.(new Event("open")));
      }
      close() {
        this.readyState = 2;
      }
    } as unknown as typeof EventSource;
  });
  await page.route(API("workspaces/w1/v1/agents(\\?.*)?$"), (r) =>
    json(r, { agents: [liveAgent(state)], next: "" }),
  );
  await page.route(API(`workspaces/w1/v1/agents/${LIVE}/events`), (r) =>
    json(r, { events: [], snapshot_seq: 0, next: 0, more: false }),
  );
  await page.route(API(`workspaces/w1/v1/agents/${LIVE}(\\?.*)?$`), (r) =>
    json(r, liveAgent(state)),
  );
  await page.goto(`/ws/w1/chat/${LIVE}`);
}

const expectDeleted = async (page: Page) => {
  const state = page.getByTestId("agent-not-found");
  await expect(
    state.getByRole("heading", { name: "This agent was deleted" }),
  ).toBeVisible();
  await expect(state.getByRole("link", { name: "Go home" })).toBeVisible();
  await expect(page.getByPlaceholder("Ask anything...")).toHaveCount(0);
};

test("a chat whose agent is deleted elsewhere says This agent was deleted (DEL3)", async ({
  page,
}) => {
  await openLive(page, "idle");
  await expect(page.getByPlaceholder("Ask anything...")).toBeEnabled();
  // The chat's own stream (deltas=true), not only the sidebar roster's.
  await page.waitForFunction(
    (id) =>
      (
        window as unknown as { __sse?: { url: string; readyState: number }[] }
      ).__sse?.some((s) => {
        const q = new URL(s.url).searchParams;
        return (
          s.readyState === 1 &&
          q.get("deltas") === "true" &&
          !!q.get("agents")?.split(",").includes(id)
        );
      }),
    LIVE,
  );
  // Another tab DELETEs the agent: history is purged first, so its
  // agent.deleted is live only (seq 0).
  await page.evaluate((id) => {
    const data = JSON.stringify({
      agent_id: id,
      seq: 0,
      event_id: `${id}:deleted:agent.deleted`,
      kind: "agent.deleted",
      turn_id: "",
      payload: {},
      created_at: "2026-10-04T00:01:00Z",
    });
    const w = window as unknown as {
      __sse: (EventTarget & { url: string; readyState: number })[];
    };
    for (const s of w.__sse)
      if (
        s.readyState !== 2 &&
        new URL(s.url).searchParams.get("agents")?.split(",").includes(id)
      )
        s.dispatchEvent(new MessageEvent("event", { data }));
  }, LIVE);
  await expectDeleted(page);
});

test("a deleted agent's chat says This agent was deleted on load (DEL3)", async ({
  page,
}) => {
  await openLive(page, "deleted");
  await expectDeleted(page);
});
