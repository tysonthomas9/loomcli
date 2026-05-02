import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";

/**
 * E2E Journey: Table view sorting, row selection, and bulk actions.
 *
 * Tests the full table interaction workflow:
 *   1. Navigate to table view, verify 5 rows render
 *   2. Sort by priority column (ascending then descending)
 *   3. Sort by status column, verify priority header resets
 *   4. Select issues via row checkboxes, verify BulkActionToolbar
 *   5. Clear selection via toolbar button
 */

// -- Constants --

const WS_ID = "ws-table";
const WS_PREFIX = `/api/workspaces/${WS_ID}`;

const WORKSPACE_DATA = {
  id: WS_ID,
  name: "Table Test Workspace",
  path: "/tmp/ws-table",
  repos: [],
  groups: [],
  agents: [],
  workspaces: [
    {
      id: WS_ID,
      name: "Table Test Workspace",
      path: "/tmp/ws-table",
      active: true,
      repo_count: 1,
      is_default: true,
    },
  ],
  workspace_order: [WS_ID],
  default_workspace: WS_ID,
};

// -- Issue mock data (5 issues with varied priorities and statuses) --

const ISSUES = [
  {
    id: "sort-001",
    title: "Critical auth bug",
    status: "open",
    priority: 0,
    issue_type: "bug",
    assignee: "alice",
    created_at: "2026-01-15T10:00:00Z",
    updated_at: "2026-01-15T10:00:00Z",
  },
  {
    id: "sort-002",
    title: "Rate limiter implementation",
    status: "in_progress",
    priority: 1,
    issue_type: "task",
    assignee: "bob",
    created_at: "2026-01-15T11:00:00Z",
    updated_at: "2026-01-15T11:00:00Z",
  },
  {
    id: "sort-003",
    title: "Dashboard redesign",
    status: "review",
    priority: 2,
    issue_type: "feature",
    assignee: "charlie",
    created_at: "2026-01-15T12:00:00Z",
    updated_at: "2026-01-15T12:00:00Z",
  },
  {
    id: "sort-004",
    title: "Cleanup legacy endpoints",
    status: "closed",
    priority: 3,
    issue_type: "chore",
    assignee: "dave",
    created_at: "2026-01-15T13:00:00Z",
    updated_at: "2026-01-15T13:00:00Z",
  },
  {
    id: "sort-005",
    title: "Blocked migration task",
    status: "blocked",
    priority: 4,
    issue_type: "task",
    assignee: "eve",
    created_at: "2026-01-15T14:00:00Z",
    updated_at: "2026-01-15T14:00:00Z",
  },
];

// -- Helper functions --

function ok<T>(data: T): string {
  return JSON.stringify({ success: true, data });
}

function getColumnHeader(page: Page, columnId: string) {
  return page.locator(`th[data-column="${columnId}"]`);
}

async function getRowIds(page: Page): Promise<string[]> {
  const idCells = page.locator(
    '[data-testid="issue-table"] tbody tr td[data-column="id"]',
  );
  return await idCells.allTextContents();
}

// -- Mock setup --

async function setupMocks(page: Page) {
  // App config (auth mode discovery — must be mocked before boot)
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

  await page.route("**/api/backends", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok([{ name: "claude", available: true, display_name: "Claude" }]),
    });
  });

  await page.route("**/api/config/backend", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok({ backend: "claude", source: "workspace", available: ["claude"], agents: [] }),
    });
  });

  // Health check
  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", daemon: true }),
    });
  });

  // Workspace metadata
  await page.route("**/api/workspaces/active", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok(WORKSPACE_DATA),
    });
  });
  await page.route(`**/api/workspaces/${WS_ID}`, async (route) => {
    const url = new URL(route.request().url());
    if (
      url.pathname === `/api/workspaces/${WS_ID}` ||
      url.pathname === `/api/workspaces/${WS_ID}/`
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok(WORKSPACE_DATA),
      });
    } else {
      await route.fallback();
    }
  });

  // Stats
  await page.route("**/workspaces/*/stats", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok({
        total_issues: 5,
        open_issues: 1,
        in_progress_issues: 1,
        closed_issues: 1,
        blocked_issues: 1,
        deferred_issues: 0,
        ready_issues: 1,
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
  await page.route("**/workspaces/*/terminal/sessions/by-issue", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok({}),
    });
  });

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

  // Monitor catch-all
  await page.route("**/api/monitor/**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({}),
    });
  });

  await page.route(
    (url) => {
      const s = url.toString();
      return s.includes("/api/workspaces/") && !s.includes("/src/");
    },
    async (route) => {
      const url = route.request().url();
      const method = route.request().method();

      if (url.includes("/api/workspaces/active")) {
        await route.fulfill({ status: 200, contentType: "application/json", body: ok(WORKSPACE_DATA) });
        return;
      }
      if (url.includes("/events")) { await route.abort(); return; }
      if (url.includes(WS_PREFIX + "/config/backend")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok({ backend: "claude", source: "workspace", available: ["claude"], agents: [] }),
        });
        return;
      }
      if (url.includes(WS_PREFIX + "/terminal/tabs")) { await route.fulfill({ status: 200, contentType: "application/json", body: ok([]) }); return; }
      if (url.includes(WS_PREFIX + "/terminal/state")) { await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ active_tab: "" }) }); return; }
      if (url.includes(WS_PREFIX + "/terminal/sessions/by-issue")) { await route.fulfill({ status: 200, contentType: "application/json", body: ok({}) }); return; }
      if (url.includes(WS_PREFIX + "/stats")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok({
            total_issues: 5,
            open_issues: 1,
            in_progress_issues: 1,
            closed_issues: 1,
            blocked_issues: 1,
            deferred_issues: 0,
            ready_issues: 1,
            tombstone_issues: 0,
            pinned_issues: 0,
            epics_eligible_for_closure: 0,
            average_lead_time_hours: 24,
          }),
        });
        return;
      }
      if (url.includes(WS_PREFIX + "/blocked")) { await route.fulfill({ status: 200, contentType: "application/json", body: ok([]) }); return; }
      if ((url.includes(WS_PREFIX + "/issues") || url.includes(WS_PREFIX + "/ready")) && method === "GET") {
        await route.fulfill({ status: 200, contentType: "application/json", body: ok(ISSUES) });
        return;
      }
      await route.fulfill({ status: 200, contentType: "application/json", body: ok(WORKSPACE_DATA) });
    },
  );
}

/**
 * Install browser-level fetch interceptor for issues and ready endpoints.
 */
async function installIssuesMock(page: Page) {
  await page.addInitScript(
    (issueData: unknown[]) => {
      (window as any).__tableIssues = issueData;
      const originalFetch = window.fetch.bind(window);
      window.fetch = function (
        input: RequestInfo | URL,
        init?: RequestInit,
      ): Promise<Response> {
        const url = typeof input === "string" ? input : input.toString();
        const method = init?.method ?? "GET";
        if (method !== "GET") return originalFetch(input, init);

        // Issues list and ready endpoint
        if (
          /\/api\/workspaces\/[^/]+\/issues(\?|$)/.test(url) ||
          /\/api\/workspaces\/[^/]+\/ready(\?|$)/.test(url)
        ) {
          return Promise.resolve(
            new Response(
              JSON.stringify({ success: true, data: (window as any).__tableIssues }),
              { status: 200, headers: { "Content-Type": "application/json" } },
            ),
          );
        }
        return originalFetch(input, init);
      };
    },
    ISSUES,
  );
}

// -- Tests --

test.describe("E2E Journey: Table sort and select", () => {
  test.describe.configure({ mode: "serial" });

  let page: Page;

  test.beforeAll(async ({ browser }) => {
    page = await browser.newPage();
    await installIssuesMock(page);
    await setupMocks(page);
  });

  test.afterAll(async () => {
    await page.close();
  });

  test("Navigate to table view and verify rows", async () => {
    // Navigate to workspace with table view (path-based routing)
    await page.goto(`/ws/${WS_ID}/table`, { waitUntil: "domcontentloaded" });

    // Wait for table to render
    await expect(page.getByTestId("issue-table")).toBeVisible({ timeout: 15000 });

    // Verify 5 rows render
    const rows = page.locator('[data-testid="issue-table"] tbody tr');
    await expect(rows).toHaveCount(5);
  });

  test("Sort by priority column", async () => {
    const priorityHeader = getColumnHeader(page, "priority");

    // Click to sort ascending (P0 first)
    await priorityHeader.click();
    await expect(priorityHeader).toHaveAttribute("aria-sort", "ascending");

    // Verify sort indicator shows ▲
    const indicator = priorityHeader.locator(".issue-table__sort-indicator");
    await expect(indicator).toHaveText("▲");

    // Verify row order: P0, P1, P2, P3, P4
    const idsAsc = await getRowIds(page);
    expect(idsAsc).toEqual(["sort-001", "sort-002", "sort-003", "sort-004", "sort-005"]);

    // Click again for descending (P4 first)
    await priorityHeader.click();
    await expect(priorityHeader).toHaveAttribute("aria-sort", "descending");

    // Verify sort indicator shows ▼
    await expect(indicator).toHaveText("▼");

    // Verify reversed order
    const idsDesc = await getRowIds(page);
    expect(idsDesc).toEqual(["sort-005", "sort-004", "sort-003", "sort-002", "sort-001"]);
  });

  test("Sort by status column resets priority header", async () => {
    const statusHeader = getColumnHeader(page, "status");
    const priorityHeader = getColumnHeader(page, "priority");

    // Click status header to sort
    await statusHeader.click();
    await expect(statusHeader).toHaveAttribute("aria-sort", "ascending");

    // Verify priority header's aria-sort resets to "none"
    await expect(priorityHeader).toHaveAttribute("aria-sort", "none");

    // Verify only one column shows sorted at a time
    await expect(statusHeader).toHaveClass(/issue-table__header-cell--sorted/);
    await expect(priorityHeader).not.toHaveClass(/issue-table__header-cell--sorted/);
  });

  test("Select issues via row checkboxes and verify bulk toolbar", async () => {
    // Select first issue
    await page.getByRole("checkbox", { name: "Select issue sort-001" }).click();

    // Verify BulkActionToolbar appears
    await expect(page.getByTestId("bulk-action-toolbar")).toBeVisible();
    await expect(page.getByTestId("selection-count")).toContainText("1 selected");

    // Select second issue
    await page.getByRole("checkbox", { name: "Select issue sort-002" }).click();

    // Verify count updates
    await expect(page.getByTestId("selection-count")).toContainText("2 selected");

    // Both checkboxes should be checked
    await expect(
      page.getByRole("checkbox", { name: "Select issue sort-001" }),
    ).toBeChecked();
    await expect(
      page.getByRole("checkbox", { name: "Select issue sort-002" }),
    ).toBeChecked();
  });

  test("Clear selection via toolbar button", async () => {
    // Verify toolbar is visible before clearing
    await expect(page.getByTestId("bulk-action-toolbar")).toBeVisible();

    // Click "Deselect all" button
    await page.getByTestId("bulk-action-clear").click();

    // Verify toolbar disappears
    await expect(page.getByTestId("bulk-action-toolbar")).not.toBeVisible();

    // Verify checkboxes are unchecked
    await expect(
      page.getByRole("checkbox", { name: "Select issue sort-001" }),
    ).not.toBeChecked();
    await expect(
      page.getByRole("checkbox", { name: "Select issue sort-002" }),
    ).not.toBeChecked();
  });
});
