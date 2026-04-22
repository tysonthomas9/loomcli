/**
 * Router deep-link regression tests.
 *
 * Covers bugs where React Router v7's splat-relative redirect semantics
 * caused `<Navigate to="kanban">` under `/ws/:workspaceId/*` to compound
 * `/kanban` segments on every render, growing the URL without bound.
 *
 * After the fix, unknown paths under `/ws/<id>/...` must redirect once to
 * the absolute `/ws/<id>/kanban`, and real deep-links must be preserved.
 */

import { test, expect, type Page } from "@playwright/test";

const WS_ID = "default";

async function setupMocks(page: Page) {
  await page.route("**/api/config", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname !== "/api/config") {
      await route.fallback();
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ mode: "open" }),
    });
  });

  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-token" }),
    });
  });

  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", daemon: true }),
    });
  });

  await page.route(
    (url) => url.toString().includes("/api/workspaces/"),
    async (route) => {
      const url = route.request().url();

      if (url.includes("/events")) {
        await route.abort();
        return;
      }

      const wsData = {
        id: WS_ID,
        name: WS_ID,
        path: "/tmp/ws",
        repos: [],
        groups: [],
        agents: [],
        workspaces: [
          {
            id: WS_ID,
            name: WS_ID,
            path: "/tmp/ws",
            active: true,
            repo_count: 0,
            is_default: true,
          },
        ],
        workspace_order: [WS_ID],
        default_workspace: WS_ID,
      };

      if (url.includes("/ready")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: [] }),
        });
      } else if (url.includes("/stats")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            success: true,
            data: {
              total_issues: 0,
              open_issues: 0,
              in_progress_issues: 0,
              closed_issues: 0,
              blocked_issues: 0,
              deferred_issues: 0,
              ready_issues: 0,
              tombstone_issues: 0,
              pinned_issues: 0,
              epics_eligible_for_closure: 0,
              average_lead_time_hours: 0,
            },
          }),
        });
      } else if (url.includes("/blocked")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: [] }),
        });
      } else if (url.includes("/issues/graph")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, issues: [] }),
        });
      } else if (url.includes("/issues")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: [] }),
        });
      } else if (url.includes("/terminal/sessions")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: {} }),
        });
      } else {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: wsData }),
        });
      }
    },
  );

  await page.route("**/monitor/**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({}),
    });
  });
}

test.describe("router deep-link", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page);
  });

  test("deep-link to unknown path redirects to kanban once", async ({
    page,
  }) => {
    await page.goto(`/ws/${WS_ID}/xyz-unknown-path`);
    await expect
      .poll(() => new URL(page.url()).pathname, { timeout: 5000 })
      .toBe(`/ws/${WS_ID}/kanban`);
    // Guard against the compound-append regression: URL must stay short.
    expect(page.url().length).toBeLessThan(200);
  });

  test("deep-link to unknown kanban-prefixed sibling redirects to kanban", async ({
    page,
  }) => {
    await page.goto(`/ws/${WS_ID}/kanban/foo-bar`);
    await expect
      .poll(() => new URL(page.url()).pathname, { timeout: 5000 })
      .toBe(`/ws/${WS_ID}/kanban`);
    expect(page.url().length).toBeLessThan(200);
  });

  test("index route (/ws/<id>) redirects to kanban", async ({ page }) => {
    await page.goto(`/ws/${WS_ID}`);
    await expect
      .poll(() => new URL(page.url()).pathname, { timeout: 5000 })
      .toBe(`/ws/${WS_ID}/kanban`);
    expect(page.url().length).toBeLessThan(200);
  });

  test("valid issue deep-link preserves URL and does not compound", async ({
    page,
  }) => {
    const deepLink = `/ws/${WS_ID}/kanban/issues/alpha-zbm`;
    await page.goto(deepLink);
    // Wait until the app has mounted, then confirm the URL stayed put.
    // Polling keeps the test stable on slow CI where lazy chunks may take
    // a moment to load, while still catching any compound-append regression.
    await expect
      .poll(() => new URL(page.url()).pathname, { timeout: 5000 })
      .toBe(deepLink);
    expect(page.url().length).toBeLessThan(200);
  });

  test("redirect uses history.replace so Back does not loop into the bad URL", async ({
    page,
  }) => {
    // First establish a real history entry the user can go back to.
    await page.goto(`/ws/${WS_ID}/kanban`);
    await expect
      .poll(() => new URL(page.url()).pathname, { timeout: 5000 })
      .toBe(`/ws/${WS_ID}/kanban`);
    // Navigate to a bad URL — redirect should REPLACE the bad URL in history.
    await page.goto(`/ws/${WS_ID}/zz-should-be-replaced`);
    await expect
      .poll(() => new URL(page.url()).pathname, { timeout: 5000 })
      .toBe(`/ws/${WS_ID}/kanban`);
    // Back should go to about:blank / the prior page, NOT back to the bad URL.
    await page.goBack();
    const backPath = new URL(page.url()).pathname;
    expect(backPath).not.toBe(`/ws/${WS_ID}/zz-should-be-replaced`);
  });
});
