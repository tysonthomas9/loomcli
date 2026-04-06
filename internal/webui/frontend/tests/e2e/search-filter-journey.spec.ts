import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";

/**
 * E2E Journey: Search and filter persistence through Graph view.
 *
 * Validates that app-level search + priority filters survive the
 * kanban → table → graph → kanban round-trip, including Graph view
 * rendering only the filtered nodes.
 *
 * Existing coverage (not duplicated here):
 * - assembled-views.spec.ts:280-368 — kanban↔table filter persistence
 * - filter.spec.ts:644-676 — combined search+priority across kanban/table
 * - graph-status-filter.spec.ts — graph-internal status filter (localStorage)
 */

// -- Workspace mock data --

const mockWorkspaceData = {
  id: "default",
  name: "default",
  path: "/tmp/test-ws",
  repos: [],
  groups: [],
  agents: [],
  workspaces: [
    {
      id: "default",
      name: "default",
      path: "/tmp/test-ws",
      active: true,
      repo_count: 0,
      is_default: true,
    },
  ],
  workspace_order: ["default"],
  default_workspace: "default",
};

// -- Mock issues: 6 issues with varied statuses, priorities, searchable titles --

const mockIssues = [
  {
    id: "sfj-1",
    title: "Auth Token Refresh",
    status: "open",
    priority: 1,
    issue_type: "task",
    created_at: "2026-01-15T10:00:00Z",
    updated_at: "2026-01-15T10:00:00Z",
  },
  {
    id: "sfj-2",
    title: "Dashboard Widgets",
    status: "open",
    priority: 2,
    issue_type: "feature",
    created_at: "2026-01-15T10:01:00Z",
    updated_at: "2026-01-15T10:01:00Z",
  },
  {
    id: "sfj-3",
    title: "OAuth Integration",
    status: "in_progress",
    priority: 1,
    issue_type: "task",
    created_at: "2026-01-15T10:02:00Z",
    updated_at: "2026-01-15T10:02:00Z",
  },
  {
    id: "sfj-4",
    title: "Database Migration",
    status: "open",
    priority: 3,
    issue_type: "task",
    created_at: "2026-01-15T10:03:00Z",
    updated_at: "2026-01-15T10:03:00Z",
  },
  {
    id: "sfj-5",
    title: "Auth Middleware Refactor",
    status: "review",
    priority: 1,
    issue_type: "task",
    created_at: "2026-01-15T10:04:00Z",
    updated_at: "2026-01-15T10:04:00Z",
  },
  {
    id: "sfj-6",
    title: "Performance Tuning",
    status: "closed",
    priority: 2,
    issue_type: "chore",
    created_at: "2026-01-15T10:05:00Z",
    updated_at: "2026-01-15T10:05:00Z",
  },
];

// -- Helper --

function ok<T>(data: T): string {
  return JSON.stringify({ success: true, data });
}

/**
 * Install a browser-level fetch interceptor for the issues endpoints.
 * Uses addInitScript so it intercepts before any app code runs.
 * Handles both kanban/list (/issues?) and graph (/issues/graph) formats.
 */
async function installIssuesMock(page: Page, issues: unknown[]) {
  await page.addInitScript(
    (issueData: unknown[]) => {
      (window as any).__mockIssues = issueData;
      const originalFetch = window.fetch.bind(window);
      window.fetch = function (
        input: RequestInfo | URL,
        init?: RequestInit,
      ): Promise<Response> {
        const url = typeof input === "string" ? input : input.toString();
        const method = init?.method ?? "GET";
        if (method !== "GET") return originalFetch(input, init);

        // Graph endpoint: returns { success: true, issues: [...] }
        if (/\/api\/workspaces\/[^/]+\/issues\/graph(\?|$)/.test(url)) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                success: true,
                issues: (window as any).__mockIssues,
              }),
              {
                status: 200,
                headers: { "Content-Type": "application/json" },
              },
            ),
          );
        }
        // Kanban endpoint (/issues): returns { success: true, data: [...] }
        if (/\/api\/workspaces\/[^/]+\/issues(\?|$)/.test(url)) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                success: true,
                data: (window as any).__mockIssues,
              }),
              {
                status: 200,
                headers: { "Content-Type": "application/json" },
              },
            ),
          );
        }
        // Table/ready endpoint (/ready): returns { success: true, data: [...] }
        if (/\/api\/workspaces\/[^/]+\/ready(\?|$)/.test(url)) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                success: true,
                data: (window as any).__mockIssues,
              }),
              {
                status: 200,
                headers: { "Content-Type": "application/json" },
              },
            ),
          );
        }
        return originalFetch(input, init);
      };
    },
    issues,
  );
}

/**
 * Register all API mocks needed for the app to boot with workspace-scoped routing.
 */
async function setupMocks(page: Page) {
  // Workspace metadata
  await page.route("**/api/workspaces/active", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok(mockWorkspaceData),
    });
  });
  await page.route("**/api/workspaces/default", async (route) => {
    const url = new URL(route.request().url());
    if (
      url.pathname === "/api/workspaces/default" ||
      url.pathname === "/api/workspaces/default/"
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok(mockWorkspaceData),
      });
    } else {
      await route.fallback();
    }
  });

  // Health check
  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", daemon: true }),
    });
  });

  // Monitor catch-all (registered first = lowest LIFO priority)
  await page.route("**/api/monitor/**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({}),
    });
  });

  // Stats
  await page.route("**/workspaces/*/stats", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok({
        total_issues: 6,
        open_issues: 3,
        in_progress_issues: 1,
        closed_issues: 1,
        blocked_issues: 0,
        deferred_issues: 0,
        ready_issues: 3,
        tombstone_issues: 0,
        pinned_issues: 0,
        epics_eligible_for_closure: 0,
        average_lead_time_hours: 24,
      }),
    });
  });

  // Blocked
  await page.route("**/workspaces/*/blocked*", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok([]),
    });
  });

  // Terminal sessions-by-issue
  await page.route(
    "**/workspaces/*/terminal/sessions/by-issue",
    async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok({}),
      });
    },
  );

  // SSE events
  await page.route("**/workspaces/*/events**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "text/event-stream",
      headers: {
        "Cache-Control": "no-cache",
        Connection: "keep-alive",
      },
      body: 'event: connected\ndata: {"message":"connected"}\n\n',
    });
  });
}

test.describe("E2E Journey: Search and filter through Graph view", () => {
  test.beforeEach(async ({ page }) => {
    await installIssuesMock(page, mockIssues);
    await setupMocks(page);
  });

  test("search and priority filter persist through kanban → table → graph → kanban round-trip", async ({
    page,
  }) => {
    // Step 1 — Navigate and establish filters in Kanban
    await page.goto("/ws/default/", { waitUntil: "domcontentloaded" });

    const readyColumn = page.locator('section[data-status="ready"]');
    await expect(readyColumn).toBeVisible();

    // Apply priority filter P1
    const priorityFilter = page.getByTestId("priority-filter");
    await priorityFilter.selectOption("1");

    // Fill search with "auth"
    const searchInput = page.getByTestId("search-input-field");
    await searchInput.fill("auth");
    await page.waitForTimeout(350); // debounce settle

    // Verify ready column shows 1 card (sfj-1 "Auth Token Refresh" — open + P1 + matches "auth")
    await expect(readyColumn.locator("article")).toHaveCount(1);
    await expect(readyColumn.getByText("Auth Token Refresh")).toBeVisible();

    // Verify URL contains filter params
    await expect(page).toHaveURL(/search=auth/);
    await expect(page).toHaveURL(/priority=1/);

    // Step 2 — Confirm table retains filters (lightweight check)
    // NavRail uses aria-label for button identification
    const navRail = page.getByRole("navigation", { name: "Primary" });
    await navRail.getByRole("button", { name: "List" }).click();
    await expect(page.getByTestId("issue-table")).toBeVisible({
      timeout: 10000,
    });

    // Verify filter controls retain values
    await expect(priorityFilter).toHaveValue("1");
    await expect(searchInput).toHaveValue("auth");

    // Step 3 — Switch to Graph view and verify filtered nodes
    // Graph is not in NavRail, so navigate via URL while preserving filter params
    await page.goto("/ws/default/?view=graph&search=auth&priority=1", {
      waitUntil: "domcontentloaded",
    });
    // GraphView is lazy-loaded — allow extra time for React.lazy Suspense
    await expect(page.getByTestId("graph-view")).toBeVisible({
      timeout: 15000,
    });

    // Verify filter controls still retain values
    await expect(priorityFilter).toHaveValue("1");
    await expect(searchInput).toHaveValue("auth");

    // Verify URL still has both filter params
    await expect(page).toHaveURL(/search=auth/);
    await expect(page).toHaveURL(/priority=1/);

    // Verify exactly 3 React Flow nodes visible:
    // sfj-1 (open, P1, "Auth Token Refresh")
    // sfj-3 (in_progress, P1, "OAuth Integration")
    // sfj-5 (review, P1, "Auth Middleware Refactor")
    await expect(page.locator(".react-flow__node")).toHaveCount(3);

    // Screenshot the graph-filtered state (the only visual state not captured by existing tests)
    await page.screenshot({
      path: "tests/e2e/screenshots/search-graph-filtered.png",
    });

    // Step 4 — Return to Kanban and confirm persistence
    await navRail.getByRole("button", { name: "Kanban" }).click();

    await expect(readyColumn).toBeVisible({ timeout: 10000 });
    await expect(priorityFilter).toHaveValue("1");
    await expect(searchInput).toHaveValue("auth");
    await expect(readyColumn.locator("article")).toHaveCount(1);
    await expect(readyColumn.getByText("Auth Token Refresh")).toBeVisible();

    // URL still has both filter params
    await expect(page).toHaveURL(/search=auth/);
    await expect(page).toHaveURL(/priority=1/);
  });
});
