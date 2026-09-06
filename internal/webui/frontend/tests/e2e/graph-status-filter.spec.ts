import { mkdirSync, rmSync, statSync } from "node:fs";
import { test, expect, Page } from "@playwright/test";
import { expectReactFlowEdgeCount } from "./helpers/reactFlow";

const WORKSPACE_ID = "default";
const WS_API = `/api/workspaces/${WORKSPACE_ID}`;
const STATUS_FILTER_KEY = `loom:${WORKSPACE_ID}:graph-status-filter`;
const SHOW_CLOSED_KEY = `loom:${WORKSPACE_ID}:graph-show-closed`;
const GRAPH_LOCK_DIR = "/tmp/loomcli-graph-e2e.lock";

test.describe.configure({ mode: "serial" });

async function acquireGraphLock() {
  const started = Date.now();
  for (;;) {
    try {
      mkdirSync(GRAPH_LOCK_DIR);
      return;
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "EEXIST") throw error;
      try {
        if (Date.now() - statSync(GRAPH_LOCK_DIR).mtimeMs > 120000) {
          rmSync(GRAPH_LOCK_DIR, { recursive: true, force: true });
        }
      } catch {
        // Lock disappeared between mkdir attempts.
      }
      if (Date.now() - started > 120000) throw error;
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
  }
}

function releaseGraphLock() {
  rmSync(GRAPH_LOCK_DIR, { recursive: true, force: true });
}

test.beforeEach(async () => {
  await acquireGraphLock();
});

test.afterEach(() => {
  releaseGraphLock();
});

function ok<T>(data: T) {
  return { success: true, data };
}

function workspaceData() {
  return {
    id: WORKSPACE_ID,
    name: "Default",
    type: "main",
    path: "/tmp/loom",
    is_active: true,
    created_at: "2026-01-27T10:00:00Z",
    updated_at: "2026-01-27T10:00:00Z",
  };
}

/**
 * Mock issues for testing status filter dropdown in GraphView.
 * Includes issues with each user-selectable status plus dependencies.
 */
const mockIssues = [
  {
    id: "issue-open",
    title: "Open Issue",
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-27T10:00:00Z",
    updated_at: "2026-01-27T10:00:00Z",
  },
  {
    id: "issue-in-progress",
    title: "In Progress Issue",
    status: "in_progress",
    priority: 1,
    issue_type: "feature",
    created_at: "2026-01-27T11:00:00Z",
    updated_at: "2026-01-27T11:00:00Z",
  },
  {
    id: "issue-blocked",
    title: "Blocked Issue",
    status: "blocked",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-27T12:00:00Z",
    updated_at: "2026-01-27T12:00:00Z",
  },
  {
    id: "issue-deferred",
    title: "Deferred Issue",
    status: "deferred",
    priority: 3,
    issue_type: "task",
    created_at: "2026-01-27T13:00:00Z",
    updated_at: "2026-01-27T13:00:00Z",
  },
  {
    id: "issue-closed",
    title: "Closed Issue",
    status: "closed",
    priority: 2,
    issue_type: "bug",
    created_at: "2026-01-27T14:00:00Z",
    updated_at: "2026-01-27T14:00:00Z",
  },
];

/**
 * Set up API mocks for status filter tests.
 */
async function setupMocks(page: Page, issues = mockIssues) {
  await page.route("**/*", async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    if (pathname === "/api/config") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ mode: "open" }),
      });
    } else if (
      pathname === "/api/workspaces/active" ||
      pathname === `/api/workspaces/${WORKSPACE_ID}`
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(ok(workspaceData())),
      });
    } else if (pathname === "/api/workspaces") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(ok([workspaceData()])),
      });
    } else if (
      pathname === `${WS_API}/issues` ||
      pathname === `${WS_API}/issues/graph`
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(ok(issues)),
      });
    } else if (pathname === `${WS_API}/stats`) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          total_issues: issues.length,
          open_issues: issues.filter((i) => i.status === "open").length,
          in_progress_issues: issues.filter((i) => i.status === "in_progress")
            .length,
          closed_issues: issues.filter((i) => i.status === "closed").length,
          blocked_issues: issues.filter((i) => i.status === "blocked").length,
          deferred_issues: issues.filter((i) => i.status === "deferred").length,
          ready_issues: issues.filter((i) => i.status === "open").length,
          tombstone_issues: 0,
          pinned_issues: 0,
          epics_eligible_for_closure: 0,
          average_lead_time_hours: 0,
        }),
      });
    } else if (
      pathname === `${WS_API}/blocked` ||
      pathname === `${WS_API}/terminal/tabs`
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(ok([])),
      });
    } else if (pathname === `${WS_API}/terminal/state`) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ active_tab: "" }),
      });
    } else if (pathname.startsWith("/api/") && pathname.includes("/events")) {
      await route.abort();
    } else {
      await route.continue();
    }
  });
}

/**
 * Navigate to Graph view and wait for API response.
 */
async function navigateToGraphView(page: Page, queryParams = "") {
  const path = queryParams
    ? `/ws/${WORKSPACE_ID}/graph?${queryParams}`
    : `/ws/${WORKSPACE_ID}/graph`;
  const [response] = await Promise.all([
    page.waitForResponse(
      (res) =>
        new URL(res.url()).pathname === `${WS_API}/issues/graph` &&
        res.status() === 200,
    ),
    page.goto(path),
  ]);
  expect(response.ok()).toBe(true);
  await expect(
    page.getByTestId("graph-view").or(page.getByText("No issues yet")),
  ).toBeVisible();
}

test.describe("Graph Status Filter Dropdown", () => {
  // Clear localStorage before each test to ensure clean state
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() => {
      localStorage.removeItem("graph-status-filter");
      localStorage.removeItem("graph-show-closed");
      localStorage.removeItem("loom:default:graph-status-filter");
      localStorage.removeItem("loom:default:graph-show-closed");
    });
  });

  test.describe("Display Tests", () => {
    test("status filter dropdown renders in GraphControls", async ({
      page,
    }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      // Verify GraphControls panel is visible
      await expect(page.getByTestId("graph-controls")).toBeVisible();

      // Verify status filter dropdown is visible within controls
      const statusFilter = page.getByTestId("status-filter");
      await expect(statusFilter).toBeVisible();

      // Verify dropdown has correct aria-label
      await expect(statusFilter).toHaveAttribute(
        "aria-label",
        "Filter by status",
      );
    });

    test("all status options are present in dropdown", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const statusFilter = page.getByTestId("status-filter");

      // Get all option elements from the dropdown
      const options = statusFilter.locator("option");

      // Verify all 6 options are present (All + 5 statuses)
      await expect(options).toHaveCount(6);

      // Verify expected values
      await expect(options.nth(0)).toHaveAttribute("value", "all");
      await expect(options.nth(0)).toHaveText("All");

      await expect(options.nth(1)).toHaveAttribute("value", "open");
      await expect(options.nth(1)).toHaveText("Open");

      await expect(options.nth(2)).toHaveAttribute("value", "in_progress");
      await expect(options.nth(2)).toHaveText("In Progress");

      await expect(options.nth(3)).toHaveAttribute("value", "blocked");
      await expect(options.nth(3)).toHaveText("Blocked");

      await expect(options.nth(4)).toHaveAttribute("value", "deferred");
      await expect(options.nth(4)).toHaveText("Deferred");

      await expect(options.nth(5)).toHaveAttribute("value", "closed");
      await expect(options.nth(5)).toHaveText("Closed");
    });
  });

  test.describe("Default State Tests", () => {
    test("default filter is 'All' showing all issues", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      // Verify dropdown shows 'All' selected
      const statusFilter = page.getByTestId("status-filter");
      await expect(statusFilter).toHaveValue("all");

      // Count total nodes: should be 5 (all mock issues)
      const nodes = page.locator(".react-flow__node");
      await expect(nodes).toHaveCount(5);
    });

    test("all issues visible when filter is 'all'", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      // Verify nodes exist for each status
      await expect(page.locator('[data-status="open"]')).toHaveCount(1);
      await expect(page.locator('[data-status="in_progress"]')).toHaveCount(1);
      await expect(page.locator('[data-status="blocked"]')).toHaveCount(1);
      await expect(page.locator('[data-status="deferred"]')).toHaveCount(1);
      await expect(page.locator('[data-status="closed"]')).toHaveCount(1);
    });
  });

  test.describe("Single Filter Tests", () => {
    test("selecting 'Open' filters to only open issues", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      // Initially 5 nodes
      const nodes = page.locator(".react-flow__node");
      await expect(nodes).toHaveCount(5);

      // Select 'open' from status filter
      const statusFilter = page.getByTestId("status-filter");
      await statusFilter.selectOption("open");

      // Verify only 1 node visible
      await expect(nodes).toHaveCount(1);

      // Verify node has data-status="open"
      await expect(page.locator('[data-status="open"]')).toHaveCount(1);

      // Verify other status nodes are not visible
      await expect(page.locator('[data-status="in_progress"]')).toHaveCount(0);
      await expect(page.locator('[data-status="blocked"]')).toHaveCount(0);
      await expect(page.locator('[data-status="deferred"]')).toHaveCount(0);
      await expect(page.locator('[data-status="closed"]')).toHaveCount(0);
    });

    test("selecting 'In Progress' filters correctly", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const statusFilter = page.getByTestId("status-filter");
      await statusFilter.selectOption("in_progress");

      // Verify only 1 node visible with data-status="in_progress"
      const nodes = page.locator(".react-flow__node");
      await expect(nodes).toHaveCount(1);
      await expect(page.locator('[data-status="in_progress"]')).toHaveCount(1);
    });

    test("selecting 'Blocked' filters correctly", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const statusFilter = page.getByTestId("status-filter");
      await statusFilter.selectOption("blocked");

      // Verify only 1 node visible with data-status="blocked"
      const nodes = page.locator(".react-flow__node");
      await expect(nodes).toHaveCount(1);
      await expect(page.locator('[data-status="blocked"]')).toHaveCount(1);
    });

    test("selecting 'Deferred' filters correctly", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const statusFilter = page.getByTestId("status-filter");
      await statusFilter.selectOption("deferred");

      // Verify only 1 node visible with data-status="deferred"
      const nodes = page.locator(".react-flow__node");
      await expect(nodes).toHaveCount(1);
      await expect(page.locator('[data-status="deferred"]')).toHaveCount(1);
    });

    test("selecting 'Closed' filters correctly", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const statusFilter = page.getByTestId("status-filter");
      await statusFilter.selectOption("closed");

      // Verify only 1 node visible with data-status="closed"
      const nodes = page.locator(".react-flow__node");
      await expect(nodes).toHaveCount(1);
      await expect(page.locator('[data-status="closed"]')).toHaveCount(1);
    });
  });

  test.describe("Node Count Tests", () => {
    test("correct node count for each status filter", async ({ page }) => {
      // Use mock data with multiple issues per status
      const multipleIssues = [
        {
          id: "open-1",
          title: "Open 1",
          status: "open",
          priority: 2,
          issue_type: "task",
          created_at: "2026-01-27T10:00:00Z",
          updated_at: "2026-01-27T10:00:00Z",
        },
        {
          id: "open-2",
          title: "Open 2",
          status: "open",
          priority: 2,
          issue_type: "task",
          created_at: "2026-01-27T10:01:00Z",
          updated_at: "2026-01-27T10:01:00Z",
        },
        {
          id: "in_progress-1",
          title: "In Progress 1",
          status: "in_progress",
          priority: 1,
          issue_type: "task",
          created_at: "2026-01-27T11:00:00Z",
          updated_at: "2026-01-27T11:00:00Z",
        },
        {
          id: "blocked-1",
          title: "Blocked 1",
          status: "blocked",
          priority: 2,
          issue_type: "task",
          created_at: "2026-01-27T12:00:00Z",
          updated_at: "2026-01-27T12:00:00Z",
        },
        {
          id: "blocked-2",
          title: "Blocked 2",
          status: "blocked",
          priority: 2,
          issue_type: "task",
          created_at: "2026-01-27T12:01:00Z",
          updated_at: "2026-01-27T12:01:00Z",
        },
        {
          id: "blocked-3",
          title: "Blocked 3",
          status: "blocked",
          priority: 2,
          issue_type: "task",
          created_at: "2026-01-27T12:02:00Z",
          updated_at: "2026-01-27T12:02:00Z",
        },
        {
          id: "closed-1",
          title: "Closed 1",
          status: "closed",
          priority: 2,
          issue_type: "task",
          created_at: "2026-01-27T14:00:00Z",
          updated_at: "2026-01-27T14:00:00Z",
        },
      ];

      await setupMocks(page, multipleIssues);
      await navigateToGraphView(page);

      const nodes = page.locator(".react-flow__node");
      const statusFilter = page.getByTestId("status-filter");

      // All: 7 issues
      await expect(nodes).toHaveCount(7);

      // Open: 2 issues
      await statusFilter.selectOption("open");
      await expect(nodes).toHaveCount(2);

      // In Progress: 1 issue
      await statusFilter.selectOption("in_progress");
      await expect(nodes).toHaveCount(1);

      // Blocked: 3 issues
      await statusFilter.selectOption("blocked");
      await expect(nodes).toHaveCount(3);

      // Closed: 1 issue
      await statusFilter.selectOption("closed");
      await expect(nodes).toHaveCount(1);
    });

    test("node count with empty result", async ({ page }) => {
      // Mock data without any 'deferred' issues
      const noDeferred = mockIssues.filter((i) => i.status !== "deferred");

      await setupMocks(page, noDeferred);
      await navigateToGraphView(page);

      const statusFilter = page.getByTestId("status-filter");
      const nodes = page.locator(".react-flow__node");

      // Select 'deferred' filter
      await statusFilter.selectOption("deferred");

      // Verify graph shows 0 nodes (empty state)
      await expect(nodes).toHaveCount(0);
    });
  });

  test.describe("Edge Handling Tests", () => {
    test("edges to filtered-out nodes are hidden", async ({ page }) => {
      // Use mock data with proper dependency structure
      const issuesWithDependencies = [
        {
          id: "issue-in-progress",
          title: "In Progress Issue",
          status: "in_progress",
          priority: 1,
          issue_type: "feature",
          created_at: "2026-01-27T11:00:00Z",
          updated_at: "2026-01-27T11:00:00Z",
        },
        {
          id: "issue-blocked",
          title: "Blocked Issue",
          status: "blocked",
          priority: 2,
          issue_type: "task",
          dependencies: [
            {
              issue_id: "issue-blocked",
              depends_on_id: "issue-in-progress",
              type: "blocks",
            },
          ],
          created_at: "2026-01-27T12:00:00Z",
          updated_at: "2026-01-27T12:00:00Z",
        },
      ];

      await setupMocks(page, issuesWithDependencies);
      await navigateToGraphView(page);

      // Verify edge exists (need at least 2 nodes for edge)
      await expectReactFlowEdgeCount(page, {
        edgeSelector: ".react-flow__edge",
        expectedEdgeCount: 1,
        expectedNodeCount: 2,
      });

      // Select 'blocked' filter (hides in_progress node)
      const statusFilter = page.getByTestId("status-filter");
      await statusFilter.selectOption("blocked");

      // Verify edge is removed (target node filtered out)
      await expectReactFlowEdgeCount(page, {
        edgeSelector: ".react-flow__edge",
        expectedEdgeCount: 0,
        expectedNodeCount: 1,
      });
    });

    test("edges reappear when filter cleared", async ({ page }) => {
      // Use mock data with proper dependency structure
      const issuesWithDependencies = [
        {
          id: "issue-in-progress",
          title: "In Progress Issue",
          status: "in_progress",
          priority: 1,
          issue_type: "feature",
          created_at: "2026-01-27T11:00:00Z",
          updated_at: "2026-01-27T11:00:00Z",
        },
        {
          id: "issue-blocked",
          title: "Blocked Issue",
          status: "blocked",
          priority: 2,
          issue_type: "task",
          dependencies: [
            {
              issue_id: "issue-blocked",
              depends_on_id: "issue-in-progress",
              type: "blocks",
            },
          ],
          created_at: "2026-01-27T12:00:00Z",
          updated_at: "2026-01-27T12:00:00Z",
        },
      ];

      await setupMocks(page, issuesWithDependencies);
      await navigateToGraphView(page);

      const statusFilter = page.getByTestId("status-filter");

      // Initially 1 edge
      await expectReactFlowEdgeCount(page, {
        edgeSelector: ".react-flow__edge",
        expectedEdgeCount: 1,
        expectedNodeCount: 2,
      });

      // Filter to blocked only (hides edge)
      await statusFilter.selectOption("blocked");
      await expectReactFlowEdgeCount(page, {
        edgeSelector: ".react-flow__edge",
        expectedEdgeCount: 0,
        expectedNodeCount: 1,
      });

      // Select 'all' to restore all nodes
      await statusFilter.selectOption("all");

      // Verify edge reappears
      await expectReactFlowEdgeCount(page, {
        edgeSelector: ".react-flow__edge",
        expectedEdgeCount: 1,
        expectedNodeCount: 2,
      });
    });
  });

  test.describe("Clear Filter / Reset Tests", () => {
    test("selecting 'All' shows all issues again", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const nodes = page.locator(".react-flow__node");
      const statusFilter = page.getByTestId("status-filter");

      // Select 'open' (1 node visible)
      await statusFilter.selectOption("open");
      await expect(nodes).toHaveCount(1);

      // Select 'all'
      await statusFilter.selectOption("all");

      // Verify all 5 nodes visible again
      await expect(nodes).toHaveCount(5);
    });

    test("filter can be changed between statuses", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const nodes = page.locator(".react-flow__node");
      const statusFilter = page.getByTestId("status-filter");

      // Select 'open' (1 node)
      await statusFilter.selectOption("open");
      await expect(nodes).toHaveCount(1);
      await expect(page.locator('[data-status="open"]')).toHaveCount(1);

      // Select 'closed' (1 different node)
      await statusFilter.selectOption("closed");
      await expect(nodes).toHaveCount(1);
      await expect(page.locator('[data-status="closed"]')).toHaveCount(1);

      // Select 'in_progress' (1 different node)
      await statusFilter.selectOption("in_progress");
      await expect(nodes).toHaveCount(1);
      await expect(page.locator('[data-status="in_progress"]')).toHaveCount(1);
    });
  });

  test.describe("localStorage Persistence Tests", () => {
    test("filter state persists to localStorage", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      // Select 'blocked' from dropdown
      const statusFilter = page.getByTestId("status-filter");
      await statusFilter.selectOption("blocked");

      // Verify localStorage key 'graph-status-filter' is 'blocked'
      const value = await page.evaluate(() =>
        localStorage.getItem("loom:default:graph-status-filter"),
      );
      expect(value).toBe("blocked");
    });

    test("filter state restores from localStorage", async ({ page }) => {
      // Set localStorage before navigation
      await page.addInitScript(() => {
        localStorage.setItem("loom:default:graph-status-filter", "deferred");
      });

      await setupMocks(page);
      await navigateToGraphView(page);

      // Verify dropdown shows 'Deferred' selected
      const statusFilter = page.getByTestId("status-filter");
      await expect(statusFilter).toHaveValue("deferred");

      // Verify only deferred nodes visible
      const nodes = page.locator(".react-flow__node");
      await expect(nodes).toHaveCount(1);
      await expect(page.locator('[data-status="deferred"]')).toHaveCount(1);
    });

    test("filter state persists across page refresh", async ({ page }) => {
      // This test verifies localStorage persistence by:
      // 1. Setting localStorage BEFORE page load
      // 2. Verifying the component reads it correctly
      //
      // Note: We can't test actual refresh because beforeEach's addInitScript
      // clears localStorage on every page load. Instead, we verify that
      // localStorage is read on initial load (which is the same code path).

      // Override the beforeEach to set a value instead of clearing
      await page.addInitScript(() => {
        localStorage.setItem("loom:default:graph-status-filter", "in_progress");
      });

      await setupMocks(page);
      await navigateToGraphView(page);

      const statusFilter = page.getByTestId("status-filter");
      const nodes = page.locator(".react-flow__node");

      // Verify 'In Progress' is selected from localStorage
      await expect(statusFilter).toHaveValue("in_progress");

      // Verify only in_progress nodes visible
      await expect(nodes).toHaveCount(1);
      await expect(page.locator('[data-status="in_progress"]')).toHaveCount(1);
    });

    test("invalid localStorage value defaults to 'all'", async ({ page }) => {
      // Set invalid localStorage value before navigation
      await page.addInitScript(() => {
        localStorage.setItem(
          "loom:default:graph-status-filter",
          "invalid_status",
        );
      });

      await setupMocks(page);
      await navigateToGraphView(page);

      // Verify dropdown shows 'All' selected (graceful fallback)
      const statusFilter = page.getByTestId("status-filter");
      await expect(statusFilter).toHaveValue("all");

      // Verify all nodes visible
      const nodes = page.locator(".react-flow__node");
      await expect(nodes).toHaveCount(5);
    });
  });

  test.describe("Integration with Other Controls", () => {
    test("status filter works with Show Closed toggle", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const nodes = page.locator(".react-flow__node");
      const showClosedToggle = page.getByTestId("show-closed-toggle");
      const statusFilter = page.getByTestId("status-filter");

      // Verify Show Closed toggle is checked (default)
      await expect(showClosedToggle).toBeChecked();

      // All 5 nodes visible
      await expect(nodes).toHaveCount(5);

      // Uncheck Show Closed toggle
      await showClosedToggle.uncheck();

      // Now 4 nodes visible (closed hidden)
      await expect(nodes).toHaveCount(4);
      await expect(page.locator('[data-status="closed"]')).toHaveCount(0);

      // Select 'closed' from status filter
      await statusFilter.selectOption("closed");

      // Status filter takes precedence - closed nodes should show
      await expect(nodes).toHaveCount(1);
      await expect(page.locator('[data-status="closed"]')).toHaveCount(1);
    });

    test("status filter works with Highlight Ready toggle", async ({
      page,
    }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const statusFilter = page.getByTestId("status-filter");
      const highlightReadyToggle = page.getByTestId("highlight-ready-toggle");
      const graphView = page.getByTestId("graph-view");

      // Select 'open' filter
      await statusFilter.selectOption("open");

      // Enable Highlight Ready toggle
      await highlightReadyToggle.check();

      // Verify data-highlight-ready attribute is set
      await expect(graphView).toHaveAttribute("data-highlight-ready", "true");

      // Verify filtering still works (1 open node)
      const nodes = page.locator(".react-flow__node");
      await expect(nodes).toHaveCount(1);
    });
  });

  test.describe("Accessibility Tests", () => {
    test("dropdown is keyboard accessible", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const statusFilter = page.getByTestId("status-filter");

      // Tab to status filter dropdown
      await statusFilter.focus();

      // Verify it receives focus
      await expect(statusFilter).toBeFocused();

      // Use keyboard to change value (ArrowDown to select next option)
      await page.keyboard.press("ArrowDown");

      // Verify selection changed (from 'all' to next option)
      // Note: Behavior may vary by browser, so we just verify the dropdown is interactive
      await expect(statusFilter).toBeFocused();
    });

    test("dropdown has proper aria-label", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const statusFilter = page.getByTestId("status-filter");
      await expect(statusFilter).toHaveAttribute(
        "aria-label",
        "Filter by status",
      );
    });
  });

  test.describe("Edge Cases", () => {
    test("filter works with empty issues list", async ({ page }) => {
      // Mock empty issues response
      await setupMocks(page, []);
      await navigateToGraphView(page);

      const nodes = page.locator(".react-flow__node");

      await expect(page.getByText("No issues yet")).toBeVisible();
      await expect(nodes).toHaveCount(0);
    });

    test("filter works when all issues have same status", async ({ page }) => {
      // Mock only 'open' status issues
      const allOpen = [
        {
          id: "open-1",
          title: "Open 1",
          status: "open",
          priority: 2,
          issue_type: "task",
          created_at: "2026-01-27T10:00:00Z",
          updated_at: "2026-01-27T10:00:00Z",
        },
        {
          id: "open-2",
          title: "Open 2",
          status: "open",
          priority: 1,
          issue_type: "task",
          created_at: "2026-01-27T11:00:00Z",
          updated_at: "2026-01-27T11:00:00Z",
        },
        {
          id: "open-3",
          title: "Open 3",
          status: "open",
          priority: 3,
          issue_type: "task",
          created_at: "2026-01-27T12:00:00Z",
          updated_at: "2026-01-27T12:00:00Z",
        },
      ];

      await setupMocks(page, allOpen);
      await navigateToGraphView(page);

      const statusFilter = page.getByTestId("status-filter");
      const nodes = page.locator(".react-flow__node");

      // All issues visible when 'all' selected
      await expect(nodes).toHaveCount(3);

      // Select 'open' - shows all
      await statusFilter.selectOption("open");
      await expect(nodes).toHaveCount(3);

      // Select 'closed' - shows none
      await statusFilter.selectOption("closed");
      await expect(nodes).toHaveCount(0);

      // Select 'all' - shows all
      await statusFilter.selectOption("all");
      await expect(nodes).toHaveCount(3);
    });

    test("GraphView has data-status-filter attribute", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const graphView = page.getByTestId("graph-view");
      const statusFilter = page.getByTestId("status-filter");

      // Default is 'all'
      await expect(graphView).toHaveAttribute("data-status-filter", "all");

      // Change to 'blocked'
      await statusFilter.selectOption("blocked");
      await expect(graphView).toHaveAttribute("data-status-filter", "blocked");

      // Change to 'in_progress'
      await statusFilter.selectOption("in_progress");
      await expect(graphView).toHaveAttribute(
        "data-status-filter",
        "in_progress",
      );
    });
  });
});
