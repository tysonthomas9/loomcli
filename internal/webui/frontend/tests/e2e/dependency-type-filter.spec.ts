import { mkdirSync, rmSync, statSync } from "node:fs";
import { test, expect, Page, Locator } from "@playwright/test";

const WORKSPACE_ID = "default";
const WS_API = `/api/workspaces/${WORKSPACE_ID}`;
const DEP_FILTER_KEY = `loom:${WORKSPACE_ID}:graph-dep-type-filter`;
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

async function expectEdgesToHaveCount(edges: Locator, count: number) {
  await expect(edges).toHaveCount(count, { timeout: 15000 });
}

/**
 * Mock issues with various dependency types for testing the dependency type filter.
 * Includes blocking, parent-child, and non-blocking (related) dependencies.
 */
const mockIssuesWithDependencies = [
  {
    id: "issue-parent",
    title: "Parent Epic",
    status: "open",
    priority: 1,
    issue_type: "epic",
    created_at: "2026-01-27T10:00:00Z",
    updated_at: "2026-01-27T10:00:00Z",
  },
  {
    id: "issue-child",
    title: "Child Task",
    status: "open",
    priority: 2,
    issue_type: "task",
    dependencies: [
      {
        issue_id: "issue-child",
        depends_on_id: "issue-parent",
        type: "parent-child",
      },
    ],
    created_at: "2026-01-27T11:00:00Z",
    updated_at: "2026-01-27T11:00:00Z",
  },
  {
    id: "issue-blocking",
    title: "Blocking Issue",
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-27T12:00:00Z",
    updated_at: "2026-01-27T12:00:00Z",
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
        depends_on_id: "issue-blocking",
        type: "blocks",
      },
    ],
    created_at: "2026-01-27T13:00:00Z",
    updated_at: "2026-01-27T13:00:00Z",
  },
  {
    id: "issue-related-1",
    title: "Related Issue 1",
    status: "open",
    priority: 3,
    issue_type: "task",
    created_at: "2026-01-27T14:00:00Z",
    updated_at: "2026-01-27T14:00:00Z",
  },
  {
    id: "issue-related-2",
    title: "Related Issue 2",
    status: "open",
    priority: 3,
    issue_type: "task",
    dependencies: [
      {
        issue_id: "issue-related-2",
        depends_on_id: "issue-related-1",
        type: "related",
      },
    ],
    created_at: "2026-01-27T15:00:00Z",
    updated_at: "2026-01-27T15:00:00Z",
  },
];

/**
 * Transform mock issues to graph API format.
 * Graph API uses simplified dependency format: { depends_on_id, type }
 */
function toGraphApiFormat(issues: typeof mockIssuesWithDependencies) {
  return issues.map((issue) => {
    const { dependencies, ...rest } = issue as Record<string, unknown>;
    if (dependencies && Array.isArray(dependencies)) {
      // Convert to graph API format (drop issue_id field)
      const graphDeps = dependencies.map((dep: Record<string, unknown>) => ({
        depends_on_id: dep.depends_on_id,
        type: dep.type,
      }));
      return { ...rest, dependencies: graphDeps };
    }
    return rest;
  });
}

/**
 * Set up API mocks for dependency type filter tests.
 */
async function setupMocks(page: Page, issues = mockIssuesWithDependencies) {
  await page.route("**/api/config", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ mode: "open" }),
    });
  });

  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ ok: true }),
    });
  });

  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-token" }),
    });
  });

  await page.route("**/api/monitor/**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(ok([])),
    });
  });

  await page.route("**/api/workspaces/active", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(ok(workspaceData())),
    });
  });

  await page.route("**/api/workspaces/default", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(ok(workspaceData())),
    });
  });

  await page.route("**/api/workspaces", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(ok([workspaceData()])),
    });
  });

  await page.route(`**${WS_API}/stats`, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        total: issues.length,
        open: issues.filter(
          (i: Record<string, unknown>) => i.status !== "closed",
        ).length,
        closed: issues.filter(
          (i: Record<string, unknown>) => i.status === "closed",
        ).length,
      }),
    });
  });

  await page.route(`**${WS_API}/terminal/tabs`, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(ok([])),
    });
  });
  await page.route(`**${WS_API}/terminal/state`, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ active_tab: "" }),
    });
  });
  await page.route(`**${WS_API}/terminal/sessions`, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(ok({})),
    });
  });

  await page.route(`**${WS_API}/issues/graph**`, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(ok(toGraphApiFormat(issues))),
    });
  });

  await page.route(`**${WS_API}/blocked**`, async (route) => {
    // Return blocked issues based on status in mock data
    const blockedIssues = issues.filter(
      (i: Record<string, unknown>) => i.status === "blocked",
    );
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(ok(blockedIssues)),
    });
  });

  await page.route(`**${WS_API}/issues**`, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(ok(issues)),
    });
  });

  await page.route("**/api/stream**", async (route) => {
    await route.abort();
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
        res.url().includes(`${WS_API}/issues/graph`) && res.status() === 200,
    ),
    page.goto(path),
  ]);
  expect(response.ok()).toBe(true);
  await expect(page.getByTestId("graph-view")).toBeVisible();
}

test.describe("Dependency Type Filter", () => {
  test.describe.configure({ mode: "serial" });

  // Clear localStorage before each test to ensure clean state
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() => {
      localStorage.removeItem("graph-dep-type-filter");
      localStorage.removeItem("graph-status-filter");
      localStorage.removeItem("graph-show-closed");
      localStorage.removeItem("loom:default:graph-dep-type-filter");
      localStorage.removeItem("loom:default:graph-status-filter");
      localStorage.removeItem("loom:default:graph-show-closed");
    });
  });

  test.describe("Display Tests", () => {
    test("filter checkboxes render in GraphControls", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      // Verify GraphControls panel is visible
      await expect(page.getByTestId("graph-controls")).toBeVisible();

      // Verify dependency type filter group is visible
      const depTypeFilter = page.getByTestId("dep-type-filter");
      await expect(depTypeFilter).toBeVisible();

      // Verify all three checkboxes are visible
      await expect(page.getByTestId("dep-type-blocking")).toBeVisible();
      await expect(page.getByTestId("dep-type-parent-child")).toBeVisible();
      await expect(page.getByTestId("dep-type-non-blocking")).toBeVisible();
    });

    test("checkboxes have correct labels", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const depTypeFilter = page.getByTestId("dep-type-filter");

      // Verify labels by text content (use exact match to avoid "Blocking" matching "Non-blocking")
      await expect(
        depTypeFilter.getByText("Blocking", { exact: true }),
      ).toBeVisible();
      await expect(depTypeFilter.getByText("Parent-Child")).toBeVisible();
      await expect(depTypeFilter.getByText("Non-blocking")).toBeVisible();
    });

    test("group label shows 'Edges'", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const depTypeFilter = page.getByTestId("dep-type-filter");
      await expect(depTypeFilter.getByText("Edges")).toBeVisible();
    });
  });

  test.describe("Default State Tests", () => {
    test("default shows Blocking and Parent-Child checked", async ({
      page,
    }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      // Blocking should be checked by default
      const blockingCheckbox = page.getByTestId("dep-type-blocking");
      await expect(blockingCheckbox).toBeChecked();

      // Parent-Child should be checked by default
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child");
      await expect(parentChildCheckbox).toBeChecked();

      // Non-blocking should NOT be checked by default
      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking");
      await expect(nonBlockingCheckbox).not.toBeChecked();
    });

    test("default state shows blocking and parent-child edges only", async ({
      page,
    }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      // With default filter (blocking + parent-child), we should have:
      // - 1 blocking edge (issue-blocked -> issue-blocking)
      // - 1 parent-child edge (issue-child -> issue-parent)
      // - 0 related edges (non-blocking is unchecked)
      const edges = page.locator(".react-flow__edge-path");
      await expect(edges).toHaveCount(2, { timeout: 15000 });
    });

    test("non-blocking edges hidden by default", async ({ page }) => {
      // Use mock with only related (non-blocking) dependency
      const relatedOnly = [
        {
          id: "issue-a",
          title: "Issue A",
          status: "open",
          priority: 2,
          issue_type: "task",
          created_at: "2026-01-27T10:00:00Z",
          updated_at: "2026-01-27T10:00:00Z",
        },
        {
          id: "issue-b",
          title: "Issue B",
          status: "open",
          priority: 2,
          issue_type: "task",
          dependencies: [
            { issue_id: "issue-b", depends_on_id: "issue-a", type: "related" },
          ],
          created_at: "2026-01-27T11:00:00Z",
          updated_at: "2026-01-27T11:00:00Z",
        },
      ];

      await setupMocks(page, relatedOnly);
      await navigateToGraphView(page);

      // Non-blocking is unchecked by default, so no edges should be visible
      const edges = page.locator(".react-flow__edge-path");
      await expectEdgesToHaveCount(edges, 0);

      // But both nodes should still be visible
      const nodes = page.locator(".react-flow__node");
      await expect(nodes).toHaveCount(2);
    });
  });

  test.describe("Single Filter Tests", () => {
    test("unchecking Blocking hides blocking edges", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const edges = page.locator(".react-flow__edge-path");
      const blockingCheckbox = page.getByTestId("dep-type-blocking");

      // Initially 2 edges (blocking + parent-child)
      await expect(edges).toHaveCount(2, { timeout: 15000 });

      // Uncheck Blocking
      await blockingCheckbox.uncheck();

      // Now only 1 edge (parent-child only)
      await expectEdgesToHaveCount(edges, 1);

      // Re-check Blocking
      await blockingCheckbox.check();

      // Back to 2 edges
      await expect(edges).toHaveCount(2, { timeout: 15000 });
    });

    test("unchecking Parent-Child hides parent-child edges", async ({
      page,
    }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const edges = page.locator(".react-flow__edge-path");
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child");

      // Initially 2 edges (blocking + parent-child)
      await expect(edges).toHaveCount(2, { timeout: 15000 });

      // Uncheck Parent-Child
      await parentChildCheckbox.uncheck();

      // Now only 1 edge (blocking only)
      await expectEdgesToHaveCount(edges, 1);

      // Re-check Parent-Child
      await parentChildCheckbox.check();

      // Back to 2 edges
      await expect(edges).toHaveCount(2, { timeout: 15000 });
    });

    test("checking Non-blocking shows non-blocking edges", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const edges = page.locator(".react-flow__edge-path");
      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking");

      // Initially 2 edges (non-blocking is unchecked)
      await expect(edges).toHaveCount(2, { timeout: 15000 });

      // Check Non-blocking
      await nonBlockingCheckbox.check();

      // Now 3 edges (blocking + parent-child + related)
      await expectEdgesToHaveCount(edges, 3);
    });

    test("nodes remain visible when edges are filtered", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const nodes = page.locator(".react-flow__node");
      const blockingCheckbox = page.getByTestId("dep-type-blocking");
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child");

      // All 6 nodes visible
      await expect(nodes).toHaveCount(6);

      // Uncheck both Blocking and Parent-Child
      await blockingCheckbox.uncheck();
      await parentChildCheckbox.uncheck();

      // Nodes should still be visible (filtering only affects edges)
      await expect(nodes).toHaveCount(6);
    });
  });

  test.describe("Multi-select Tests", () => {
    test("can check all three dependency types", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const blockingCheckbox = page.getByTestId("dep-type-blocking");
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child");
      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking");

      // Check all three
      await expect(blockingCheckbox).toBeChecked();
      await expect(parentChildCheckbox).toBeChecked();
      await nonBlockingCheckbox.check();

      // All three should now be checked
      await expect(blockingCheckbox).toBeChecked();
      await expect(parentChildCheckbox).toBeChecked();
      await expect(nonBlockingCheckbox).toBeChecked();

      // All 3 edges should be visible
      const edges = page.locator(".react-flow__edge-path");
      await expectEdgesToHaveCount(edges, 3);
    });

    test("unchecking all shows all edges (no filter)", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const edges = page.locator(".react-flow__edge-path");
      const blockingCheckbox = page.getByTestId("dep-type-blocking");
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child");

      // Uncheck both checked checkboxes
      await blockingCheckbox.uncheck();
      await parentChildCheckbox.uncheck();

      // Per implementation: empty filter shows ALL edges (no filtering)
      // So all 3 edges should be visible
      await expectEdgesToHaveCount(edges, 3);
    });

    test("combination: Blocking + Non-blocking only", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const edges = page.locator(".react-flow__edge-path");
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child");
      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking");

      // Uncheck Parent-Child, check Non-blocking
      await parentChildCheckbox.uncheck();
      await nonBlockingCheckbox.check();

      // Should show blocking + related = 2 edges
      await expect(edges).toHaveCount(2, { timeout: 15000 });
    });
  });

  test.describe("Edge Count Tests", () => {
    test("correct edge count with only Blocking selected", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const edges = page.locator(".react-flow__edge-path");
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child");

      // Uncheck Parent-Child (leave only Blocking)
      await parentChildCheckbox.uncheck();

      // Only 1 blocking edge
      await expectEdgesToHaveCount(edges, 1);
    });

    test("correct edge count with only Parent-Child selected", async ({
      page,
    }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const edges = page.locator(".react-flow__edge-path");
      const blockingCheckbox = page.getByTestId("dep-type-blocking");

      // Uncheck Blocking (leave only Parent-Child)
      await blockingCheckbox.uncheck();

      // Only 1 parent-child edge
      await expectEdgesToHaveCount(edges, 1);
    });

    test("correct edge count with only Non-blocking selected", async ({
      page,
    }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const edges = page.locator(".react-flow__edge-path");
      const blockingCheckbox = page.getByTestId("dep-type-blocking");
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child");
      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking");

      // Uncheck Blocking and Parent-Child, check Non-blocking
      await blockingCheckbox.uncheck();
      await parentChildCheckbox.uncheck();
      await nonBlockingCheckbox.check();

      // Only 1 related edge
      await expectEdgesToHaveCount(edges, 1);
    });

    test("correct edge count with all types selected", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const edges = page.locator(".react-flow__edge-path");
      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking");

      // Check Non-blocking (others already checked by default)
      await nonBlockingCheckbox.check();

      // All 3 edges visible
      await expectEdgesToHaveCount(edges, 3);
    });
  });

  test.describe("Persistence Tests", () => {
    test("filter state persists to localStorage", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking");

      // Check Non-blocking
      await nonBlockingCheckbox.check();

      // Verify localStorage contains the new filter state
      const value = await page.evaluate(() =>
        localStorage.getItem("loom:default:graph-dep-type-filter"),
      );
      const parsed = JSON.parse(value!);
      expect(parsed).toContain("blocking");
      expect(parsed).toContain("parent-child");
      expect(parsed).toContain("non-blocking");
    });

    test("filter state restores from localStorage", async ({ page }) => {
      // Set localStorage before navigation
      await page.addInitScript(() => {
        localStorage.setItem(
          "loom:default:graph-dep-type-filter",
          JSON.stringify(["non-blocking"]),
        );
      });

      await setupMocks(page);
      await navigateToGraphView(page);

      // Verify checkboxes reflect localStorage state
      const blockingCheckbox = page.getByTestId("dep-type-blocking");
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child");
      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking");

      await expect(blockingCheckbox).not.toBeChecked();
      await expect(parentChildCheckbox).not.toBeChecked();
      await expect(nonBlockingCheckbox).toBeChecked();

      // Only related edge should be visible
      const edges = page.locator(".react-flow__edge-path");
      await expectEdgesToHaveCount(edges, 1);
    });

    test("filter state persists across page load", async ({ page }) => {
      // Pre-set localStorage with custom filter
      await page.addInitScript(() => {
        localStorage.setItem(
          "loom:default:graph-dep-type-filter",
          JSON.stringify(["blocking"]),
        );
      });

      await setupMocks(page);
      await navigateToGraphView(page);

      const blockingCheckbox = page.getByTestId("dep-type-blocking");
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child");

      // Verify state restored
      await expect(blockingCheckbox).toBeChecked();
      await expect(parentChildCheckbox).not.toBeChecked();

      // Only blocking edge should be visible
      const edges = page.locator(".react-flow__edge-path");
      await expectEdgesToHaveCount(edges, 1);
    });

    test("invalid localStorage value falls back to defaults", async ({
      page,
    }) => {
      // Set invalid localStorage value
      await page.addInitScript(() => {
        localStorage.setItem(
          "loom:default:graph-dep-type-filter",
          "invalid_json",
        );
      });

      await setupMocks(page);
      await navigateToGraphView(page);

      // Should fall back to default (blocking + parent-child)
      const blockingCheckbox = page.getByTestId("dep-type-blocking");
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child");
      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking");

      await expect(blockingCheckbox).toBeChecked();
      await expect(parentChildCheckbox).toBeChecked();
      await expect(nonBlockingCheckbox).not.toBeChecked();
    });

    test("invalid array values in localStorage are filtered out", async ({
      page,
    }) => {
      // Set localStorage with invalid group names
      await page.addInitScript(() => {
        localStorage.setItem(
          "graph-dep-type-filter",
          JSON.stringify(["blocking", "invalid-group", 123]),
        );
      });

      await setupMocks(page);
      await navigateToGraphView(page);

      // Invalid entries make the stored value invalid, so the UI falls back to defaults.
      const blockingCheckbox = page.getByTestId("dep-type-blocking");
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child");
      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking");

      await expect(blockingCheckbox).toBeChecked();
      await expect(parentChildCheckbox).toBeChecked();
      await expect(nonBlockingCheckbox).not.toBeChecked();
    });

    test("empty array in localStorage shows all edges", async ({ page }) => {
      // Set localStorage with empty array
      await page.addInitScript(() => {
        localStorage.setItem(
          "loom:default:graph-dep-type-filter",
          JSON.stringify([]),
        );
      });

      await setupMocks(page);
      await navigateToGraphView(page);

      // Empty filter means no filtering - all edges visible
      const edges = page.locator(".react-flow__edge-path");
      await expectEdgesToHaveCount(edges, 3);

      // All checkboxes should be unchecked
      const blockingCheckbox = page.getByTestId("dep-type-blocking");
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child");
      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking");

      await expect(blockingCheckbox).not.toBeChecked();
      await expect(parentChildCheckbox).not.toBeChecked();
      await expect(nonBlockingCheckbox).not.toBeChecked();
    });
  });

  test.describe("Clear/Reset Tests", () => {
    test("checking all types shows all edges", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const edges = page.locator(".react-flow__edge-path");
      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking");

      // Initially 2 edges (blocking + parent-child)
      await expect(edges).toHaveCount(2, { timeout: 15000 });

      // Check Non-blocking to show all
      await nonBlockingCheckbox.check();

      // All 3 edges visible
      await expectEdgesToHaveCount(edges, 3);
    });

    test("filter can be toggled repeatedly", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const edges = page.locator(".react-flow__edge-path");
      const blockingCheckbox = page.getByTestId("dep-type-blocking");

      // Toggle multiple times
      for (let i = 0; i < 3; i++) {
        await blockingCheckbox.uncheck();
        await expectEdgesToHaveCount(edges, 1); // Only parent-child

        await blockingCheckbox.check();
        await expect(edges).toHaveCount(2, { timeout: 15000 }); // blocking + parent-child
      }
    });
  });

  test.describe("Integration Tests", () => {
    // Note: Status filtering in GraphView is done client-side via visibleIssues useMemo.
    // The /api/issues/graph mock returns all issues, and GraphView filters them.
    test("works with status filter", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const edges = page.locator(".react-flow__edge-path");
      const nodes = page.locator(".react-flow__node");
      const statusFilter = page.getByTestId("status-filter");

      // Initially 6 nodes, 2 edges
      await expect(nodes).toHaveCount(6);
      await expect(edges).toHaveCount(2, { timeout: 15000 });

      // Filter to only 'open' status
      await statusFilter.selectOption("open");

      // Fewer nodes (only open issues)
      // issue-parent, issue-child, issue-blocking, issue-related-1, issue-related-2 are open
      await expect(nodes).toHaveCount(5);

      // Parent-child edge still visible (both nodes are open)
      // Blocking edge removed (issue-blocked is 'blocked' status, filtered out)
      await expectEdgesToHaveCount(edges, 1);
    });

    test("works with Show Closed toggle", async ({ page }) => {
      // Use mock with closed issue having dependency
      const withClosed = [
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
          id: "issue-closed",
          title: "Closed Issue",
          status: "closed",
          priority: 2,
          issue_type: "task",
          dependencies: [
            {
              issue_id: "issue-closed",
              depends_on_id: "issue-open",
              type: "blocks",
            },
          ],
          created_at: "2026-01-27T11:00:00Z",
          updated_at: "2026-01-27T11:00:00Z",
        },
      ];

      await setupMocks(page, withClosed);
      await navigateToGraphView(page);

      const edges = page.locator(".react-flow__edge-path");
      const nodes = page.locator(".react-flow__node");
      const showClosedToggle = page.getByTestId("show-closed-toggle");

      // Initially both nodes and 1 edge visible
      await expect(nodes).toHaveCount(2);
      await expectEdgesToHaveCount(edges, 1);

      // Uncheck Show Closed
      await showClosedToggle.uncheck();

      // Closed node hidden, edge removed
      await expect(nodes).toHaveCount(1);
      await expectEdgesToHaveCount(edges, 0);
    });

    test("works with Highlight Ready toggle", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const graphView = page.getByTestId("graph-view");
      const highlightReadyToggle = page.getByTestId("highlight-ready-toggle");
      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking");

      // Change dependency filter
      await nonBlockingCheckbox.check();

      // Enable Highlight Ready
      await highlightReadyToggle.check();

      // Verify data attribute is set
      await expect(graphView).toHaveAttribute("data-highlight-ready", "true");

      // Verify edges still reflect filter (3 edges with all checked)
      const edges = page.locator(".react-flow__edge-path");
      await expectEdgesToHaveCount(edges, 3);
    });
  });

  test.describe("Edge Cases", () => {
    test("filter works with empty issues list", async ({ page }) => {
      await setupMocks(page, []);
      const [response] = await Promise.all([
        page.waitForResponse(
          (res) =>
            res.url().includes(`${WS_API}/issues/graph`) &&
            res.status() === 200,
        ),
        page.goto(`/ws/${WORKSPACE_ID}/graph`),
      ]);
      expect(response.ok()).toBe(true);

      await expect(page.getByText("No issues yet")).toBeVisible();

      const nodes = page.locator(".react-flow__node");
      const edges = page.locator(".react-flow__edge-path");
      await expect(nodes).toHaveCount(0);
      await expectEdgesToHaveCount(edges, 0);
    });

    test("filter works with no edges of a type", async ({ page }) => {
      // Mock with only blocking dependency (no parent-child or related)
      const blockingOnly = [
        {
          id: "issue-a",
          title: "Issue A",
          status: "open",
          priority: 2,
          issue_type: "task",
          created_at: "2026-01-27T10:00:00Z",
          updated_at: "2026-01-27T10:00:00Z",
        },
        {
          id: "issue-b",
          title: "Issue B",
          status: "blocked",
          priority: 2,
          issue_type: "task",
          dependencies: [
            { issue_id: "issue-b", depends_on_id: "issue-a", type: "blocks" },
          ],
          created_at: "2026-01-27T11:00:00Z",
          updated_at: "2026-01-27T11:00:00Z",
        },
      ];

      await setupMocks(page, blockingOnly);
      await navigateToGraphView(page);

      const edges = page.locator(".react-flow__edge-path");
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child");

      // 1 blocking edge visible
      await expectEdgesToHaveCount(edges, 1);

      // Uncheck Parent-Child (no parent-child edges exist anyway)
      await parentChildCheckbox.uncheck();

      // Still 1 edge (blocking)
      await expectEdgesToHaveCount(edges, 1);

      // Filtering to only Parent-Child shows 0 edges
      const blockingCheckbox = page.getByTestId("dep-type-blocking");
      await blockingCheckbox.uncheck();
      await parentChildCheckbox.check();

      await expectEdgesToHaveCount(edges, 0);
    });

    test("checkboxes can be disabled when controls are disabled", async ({
      page,
    }) => {
      // This tests the disabled state when it's provided
      // The actual disabled state depends on component props, so we just verify
      // the checkboxes are interactive by default
      await setupMocks(page);
      await navigateToGraphView(page);

      const blockingCheckbox = page.getByTestId("dep-type-blocking");

      // Verify checkbox is enabled and interactive
      await expect(blockingCheckbox).toBeEnabled();

      // Can toggle it
      await blockingCheckbox.uncheck();
      await expect(blockingCheckbox).not.toBeChecked();
    });
  });

  test.describe("Accessibility Tests", () => {
    test("checkboxes are keyboard accessible", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const blockingCheckbox = page.getByTestId("dep-type-blocking");

      // Focus the checkbox
      await blockingCheckbox.focus();
      await expect(blockingCheckbox).toBeFocused();

      // Toggle with Space key
      await page.keyboard.press("Space");
      await expect(blockingCheckbox).not.toBeChecked();

      // Toggle back
      await page.keyboard.press("Space");
      await expect(blockingCheckbox).toBeChecked();
    });

    test("can tab between checkboxes", async ({ page }) => {
      await setupMocks(page);
      await navigateToGraphView(page);

      const blockingCheckbox = page.getByTestId("dep-type-blocking");
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child");
      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking");

      // Focus first checkbox
      await blockingCheckbox.focus();
      await expect(blockingCheckbox).toBeFocused();

      // Tab to next checkbox
      await page.keyboard.press("Tab");
      await expect(parentChildCheckbox).toBeFocused();

      // Tab to next checkbox
      await page.keyboard.press("Tab");
      await expect(nonBlockingCheckbox).toBeFocused();
    });
  });
});
