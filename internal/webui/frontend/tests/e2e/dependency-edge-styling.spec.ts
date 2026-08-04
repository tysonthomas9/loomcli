import { mkdirSync, rmSync, statSync } from "node:fs";
import { test, expect, Page } from "@playwright/test";

/**
 * E2E tests for DependencyEdge component styling.
 *
 * Tests verify that dependency edges render with correct visual styles
 * based on their blocking status and dependency type.
 *
 * Blocking types: blocks, parent-child, conditional-blocks, waits-for
 * Non-blocking types: related, relates-to, duplicates, supersedes
 */

const WORKSPACE_ID = "default";
const WS_API = `/api/workspaces/${WORKSPACE_ID}`;
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
 * Mock issues with various dependency types for testing edge styling.
 */
const mockIssuesWithBlocks = [
  {
    id: "issue-source",
    title: "Source Issue",
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-27T10:00:00Z",
    updated_at: "2026-01-27T10:00:00Z",
  },
  {
    id: "issue-blocks-target",
    title: "Blocks Target",
    status: "blocked",
    priority: 2,
    issue_type: "task",
    dependencies: [
      {
        issue_id: "issue-blocks-target",
        depends_on_id: "issue-source",
        type: "blocks",
      },
    ],
    created_at: "2026-01-27T11:00:00Z",
    updated_at: "2026-01-27T11:00:00Z",
  },
];

const mockIssuesWithParentChild = [
  {
    id: "issue-parent",
    title: "Parent Issue",
    status: "open",
    priority: 2,
    issue_type: "epic",
    created_at: "2026-01-27T10:00:00Z",
    updated_at: "2026-01-27T10:00:00Z",
  },
  {
    id: "issue-child",
    title: "Child Issue",
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
];

const mockIssuesWithRelated = [
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
    title: "Issue B (related)",
    status: "open",
    priority: 2,
    issue_type: "task",
    dependencies: [
      {
        issue_id: "issue-b",
        depends_on_id: "issue-a",
        type: "related",
      },
    ],
    created_at: "2026-01-27T11:00:00Z",
    updated_at: "2026-01-27T11:00:00Z",
  },
];

const mockIssuesMultipleTypes = [
  {
    id: "issue-center",
    title: "Center Issue",
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-27T10:00:00Z",
    updated_at: "2026-01-27T10:00:00Z",
  },
  {
    id: "issue-blocking",
    title: "Blocking Issue",
    status: "blocked",
    priority: 2,
    issue_type: "task",
    dependencies: [
      {
        issue_id: "issue-blocking",
        depends_on_id: "issue-center",
        type: "blocks",
      },
    ],
    created_at: "2026-01-27T11:00:00Z",
    updated_at: "2026-01-27T11:00:00Z",
  },
  {
    id: "issue-related",
    title: "Related Issue",
    status: "open",
    priority: 2,
    issue_type: "task",
    dependencies: [
      {
        issue_id: "issue-related",
        depends_on_id: "issue-center",
        type: "related",
      },
    ],
    created_at: "2026-01-27T12:00:00Z",
    updated_at: "2026-01-27T12:00:00Z",
  },
];

/**
 * Set up API mocks for edge styling tests.
 */
async function setupMocks(page: Page, issues: object[]) {
  await page.addInitScript((workspaceId) => {
    localStorage.setItem(
      `loom:${workspaceId}:graph-dep-type-filter`,
      JSON.stringify(["blocking", "parent-child", "non-blocking"]),
    );
  }, WORKSPACE_ID);

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
          (issue) => (issue as { status?: string }).status !== "closed",
        ).length,
        closed: issues.filter(
          (issue) => (issue as { status?: string }).status === "closed",
        ).length,
      }),
    });
  });

  await page.route(`**${WS_API}/blocked**`, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(ok([])),
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
      body: JSON.stringify(ok(issues)),
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
 * Navigate to Graph View and wait for API response.
 */
async function navigateToGraphView(page: Page) {
  const [response] = await Promise.all([
    page.waitForResponse(
      (res) =>
        res.url().includes(`${WS_API}/issues/graph`) && res.status() === 200,
    ),
    page.goto(`/ws/${WORKSPACE_ID}/graph`),
  ]);
  expect(response.ok()).toBe(true);
  await expect(page.getByTestId("graph-view")).toBeVisible();
  const nonBlockingFilter = page.getByTestId("dep-type-non-blocking");
  await expect(nonBlockingFilter).toBeVisible();
  const checked = await nonBlockingFilter.isChecked();
  if (!checked) {
    await nonBlockingFilter.click();
    await expect(nonBlockingFilter).toBeChecked();
  }
}

/**
 * Get all edges in the graph.
 */
function getAllEdges(page: Page) {
  return page.locator(".react-flow__edge-path");
}

/**
 * Wait for edges to be rendered with paths.
 */
async function waitForEdgesWithPaths(page: Page, count: number) {
  const edges = getAllEdges(page);
  await expect(edges).toHaveCount(count, { timeout: 15000 });
}

/**
 * Check if an edge path has a type-specific style class (CSS Modules hashed).
 */
async function hasEdgeTypeClass(
  page: Page,
  typeClass: string,
): Promise<boolean> {
  const edgePath = page.locator(".react-flow__edge-path").first();
  const className = await edgePath.getAttribute("class");
  return className?.includes(typeClass) ?? false;
}

/**
 * Get computed stroke properties from edge path.
 */
async function getEdgeStrokeProps(page: Page) {
  const edgePath = page.locator(".react-flow__edge-path").first();
  return await edgePath.evaluate((el) => ({
    stroke: window.getComputedStyle(el).stroke,
    strokeDasharray: window.getComputedStyle(el).strokeDasharray,
    strokeWidth: window.getComputedStyle(el).strokeWidth,
  }));
}

test.describe("Dependency Edge Styling", () => {
  test.describe.configure({ mode: "serial" });

  test.describe("Blocking edge styling", () => {
    test("'blocks' type edge has typeBlocks CSS class", async ({ page }) => {
      await setupMocks(page, mockIssuesWithBlocks);
      await navigateToGraphView(page);
      await waitForEdgesWithPaths(page, 1);

      const hasTypeClass = await hasEdgeTypeClass(page, "typeBlocks");
      expect(hasTypeClass).toBe(true);
    });

    test("'blocks' type edge is solid by default", async ({ page }) => {
      await setupMocks(page, mockIssuesWithBlocks);
      await navigateToGraphView(page);
      await waitForEdgesWithPaths(page, 1);

      const props = await getEdgeStrokeProps(page);
      expect(
        props.strokeDasharray === "none" || props.strokeDasharray === "",
      ).toBe(true);
    });

    test("'blocks' type edge keeps React Flow default stroke width", async ({
      page,
    }) => {
      await setupMocks(page, mockIssuesWithBlocks);
      await navigateToGraphView(page);
      await waitForEdgesWithPaths(page, 1);

      const edgePath = page.locator(".react-flow__edge-path").first();
      // strokeWidth is set via inline style attribute
      const strokeWidth = await edgePath.evaluate((el) => {
        // Check inline style first, then computed style
        const inlineStyle = el.getAttribute("style");
        const match = inlineStyle?.match(/stroke-width:\s*([\d.]+)/);
        if (match) return parseFloat(match[1]);
        return parseFloat(window.getComputedStyle(el).strokeWidth);
      });
      expect(strokeWidth).toBe(1.5);
    });

    test("'parent-child' type edge has typeParentChild CSS class", async ({
      page,
    }) => {
      await setupMocks(page, mockIssuesWithParentChild);
      await navigateToGraphView(page);
      await waitForEdgesWithPaths(page, 1);

      const hasTypeClass = await hasEdgeTypeClass(page, "typeParentChild");
      expect(hasTypeClass).toBe(true);
    });

    test("'parent-child' type edge is solid", async ({ page }) => {
      await setupMocks(page, mockIssuesWithParentChild);
      await navigateToGraphView(page);
      await waitForEdgesWithPaths(page, 1);

      const props = await getEdgeStrokeProps(page);
      expect(
        props.strokeDasharray === "none" || props.strokeDasharray === "",
      ).toBe(true);
    });
  });

  test.describe("Non-blocking edge styling", () => {
    test("'related' type edge has typeRelated CSS class", async ({ page }) => {
      await setupMocks(page, mockIssuesWithRelated);
      await navigateToGraphView(page);
      await waitForEdgesWithPaths(page, 1);

      const hasTypeClass = await hasEdgeTypeClass(page, "typeRelated");
      expect(hasTypeClass).toBe(true);
    });

    test("'related' type edge has dashed stroke", async ({ page }) => {
      await setupMocks(page, mockIssuesWithRelated);
      await navigateToGraphView(page);
      await waitForEdgesWithPaths(page, 1);

      const props = await getEdgeStrokeProps(page);
      expect(props.strokeDasharray).toMatch(/^\d+(px)?[\s,]+\d+(px)?$/);
      expect(props.strokeDasharray).not.toBe("none");
    });

    test("'related' type edge keeps React Flow default stroke width", async ({
      page,
    }) => {
      await setupMocks(page, mockIssuesWithRelated);
      await navigateToGraphView(page);
      await waitForEdgesWithPaths(page, 1);

      const edgePath = page.locator(".react-flow__edge-path").first();
      // strokeWidth is set via inline style attribute
      const strokeWidth = await edgePath.evaluate((el) => {
        const inlineStyle = el.getAttribute("style");
        const match = inlineStyle?.match(/stroke-width:\s*([\d.]+)/);
        if (match) return parseFloat(match[1]);
        return parseFloat(window.getComputedStyle(el).strokeWidth);
      });
      expect(strokeWidth).toBe(1.5);
    });
  });

  test.describe("Edge labels", () => {
    test("edge label displays dependency type text", async ({ page }) => {
      await setupMocks(page, mockIssuesWithBlocks);
      await navigateToGraphView(page);
      await waitForEdgesWithPaths(page, 1);

      // Look for edge label containing the dependency type
      const edgeLabel = page.locator(".react-flow__edgelabel-renderer div");
      await expect(edgeLabel).toContainText("blocks");
    });

    test("related edge label displays 'related'", async ({ page }) => {
      await setupMocks(page, mockIssuesWithRelated);
      await navigateToGraphView(page);
      await waitForEdgesWithPaths(page, 1);

      const edgeLabel = page.locator(".react-flow__edgelabel-renderer div");
      await expect(edgeLabel).toContainText("related");
    });

    test("parent-child edge label displays 'parent-child'", async ({
      page,
    }) => {
      await setupMocks(page, mockIssuesWithParentChild);
      await navigateToGraphView(page);
      await waitForEdgesWithPaths(page, 1);

      const edgeLabel = page.locator(".react-flow__edgelabel-renderer div");
      await expect(edgeLabel).toContainText("parent-child");
    });

    test("edge label has monospace font styling", async ({ page }) => {
      await setupMocks(page, mockIssuesWithBlocks);
      await navigateToGraphView(page);
      await waitForEdgesWithPaths(page, 1);

      const edgeLabel = page.locator(".react-flow__edgelabel-renderer div");
      await expect(edgeLabel).toBeVisible();

      const fontFamily = await edgeLabel.evaluate((el) => {
        return window.getComputedStyle(el).fontFamily;
      });

      // Should contain 'monospace' in font family
      expect(fontFamily.toLowerCase()).toContain("monospace");
    });
  });

  test.describe("Visual distinction between edge types", () => {
    test("blocking and non-blocking edges have different CSS classes", async ({
      page,
    }) => {
      await setupMocks(page, mockIssuesMultipleTypes);
      await navigateToGraphView(page);
      await waitForEdgesWithPaths(page, 2);

      // Get all edge path classes
      const edgePaths = page.locator(".react-flow__edge-path");
      const classNames = await edgePaths.evaluateAll((paths) =>
        paths.map((path) => path.getAttribute("class") ?? ""),
      );

      const hasBlocks = classNames.some((c) => c.includes("typeBlocks"));
      const hasRelated = classNames.some((c) => c.includes("typeRelated"));
      expect(hasBlocks).toBe(true);
      expect(hasRelated).toBe(true);
    });

    test("blocks and related edges have different dash styles", async ({
      page,
    }) => {
      await setupMocks(page, mockIssuesMultipleTypes);
      await navigateToGraphView(page);
      await waitForEdgesWithPaths(page, 2);

      const edgePaths = page.locator(".react-flow__edge-path");
      const strokeDasharrays = await edgePaths.evaluateAll((paths) =>
        paths.map((path) => window.getComputedStyle(path).strokeDasharray),
      );

      // Should have two different dash array values (blocks solid, related dashed)
      const uniqueValues = [...new Set(strokeDasharrays)];
      expect(uniqueValues.length).toBeGreaterThanOrEqual(2);

      const hasSolid = strokeDasharrays.some((d) => d === "none" || d === "");
      expect(hasSolid).toBe(true);

      const hasDashed = strokeDasharrays.some((d) => /\d/.test(d));
      expect(hasDashed).toBe(true);
    });

    test("blocks and related edges keep stable stroke widths", async ({
      page,
    }) => {
      await setupMocks(page, mockIssuesMultipleTypes);
      await navigateToGraphView(page);
      await waitForEdgesWithPaths(page, 2);

      const edgePaths = page.locator(".react-flow__edge-path");
      const strokeWidths = await edgePaths.evaluateAll((paths) =>
        paths.map((path) => {
          // strokeWidth is set via inline style attribute
          const inlineStyle = path.getAttribute("style");
          const match = inlineStyle?.match(/stroke-width:\s*([\d.]+)/);
          if (match) return parseFloat(match[1]);
          return parseFloat(window.getComputedStyle(path).strokeWidth);
        }),
      );

      expect(strokeWidths).toEqual([1.5, 1.5]);
    });
  });

  test.describe("Edge cases", () => {
    test("graph with no dependencies renders no edges", async ({ page }) => {
      const noDependencies = [
        {
          id: "issue-1",
          title: "Issue 1",
          status: "open",
          priority: 2,
          issue_type: "task",
          created_at: "2026-01-27T10:00:00Z",
          updated_at: "2026-01-27T10:00:00Z",
        },
        {
          id: "issue-2",
          title: "Issue 2",
          status: "open",
          priority: 2,
          issue_type: "task",
          created_at: "2026-01-27T11:00:00Z",
          updated_at: "2026-01-27T11:00:00Z",
        },
      ];

      await setupMocks(page, noDependencies);
      await navigateToGraphView(page);

      // Wait for graph to render nodes
      const nodes = page.locator(".react-flow__node");
      await expect(nodes).toHaveCount(2);

      // Should have no edges
      const edges = getAllEdges(page);
      await expect(edges).toHaveCount(0);
    });

    test("single dependency renders one edge", async ({ page }) => {
      await setupMocks(page, mockIssuesWithBlocks);
      await navigateToGraphView(page);

      await waitForEdgesWithPaths(page, 1);
    });

    test("multiple dependencies from same source render separate edges", async ({
      page,
    }) => {
      const multipleDeps = [
        {
          id: "issue-source",
          title: "Source Issue",
          status: "open",
          priority: 2,
          issue_type: "task",
          created_at: "2026-01-27T10:00:00Z",
          updated_at: "2026-01-27T10:00:00Z",
        },
        {
          id: "issue-target-1",
          title: "Target 1",
          status: "blocked",
          priority: 2,
          issue_type: "task",
          dependencies: [
            {
              issue_id: "issue-target-1",
              depends_on_id: "issue-source",
              type: "blocks",
            },
          ],
          created_at: "2026-01-27T11:00:00Z",
          updated_at: "2026-01-27T11:00:00Z",
        },
        {
          id: "issue-target-2",
          title: "Target 2",
          status: "open",
          priority: 2,
          issue_type: "task",
          dependencies: [
            {
              issue_id: "issue-target-2",
              depends_on_id: "issue-source",
              type: "related",
            },
          ],
          created_at: "2026-01-27T12:00:00Z",
          updated_at: "2026-01-27T12:00:00Z",
        },
      ];

      await setupMocks(page, multipleDeps);
      await navigateToGraphView(page);

      // Should have 2 separate edges
      await waitForEdgesWithPaths(page, 2);
    });
  });

  test.describe("Reduced motion preferences", () => {
    test("blocking edge animation respects prefers-reduced-motion", async ({
      page,
    }) => {
      // Enable prefers-reduced-motion
      await page.emulateMedia({ reducedMotion: "reduce" });

      await setupMocks(page, mockIssuesWithBlocks);
      await navigateToGraphView(page);
      await waitForEdgesWithPaths(page, 1);

      const edgePath = page.locator(".react-flow__edge-path").first();
      // Animation should be 'none' when reduced motion is enabled
      const animation = await edgePath.evaluate((el) => {
        return window.getComputedStyle(el).animationName;
      });

      expect(animation).toBe("none");
    });
  });
});
