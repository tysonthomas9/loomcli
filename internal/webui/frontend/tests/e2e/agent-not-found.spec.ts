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
