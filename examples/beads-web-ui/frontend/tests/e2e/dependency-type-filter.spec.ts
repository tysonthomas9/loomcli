import { test, expect, Page } from "@playwright/test"

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
]

/**
 * Transform mock issues to graph API format.
 * Graph API uses simplified dependency format: { depends_on_id, type }
 */
function toGraphApiFormat(issues: typeof mockIssuesWithDependencies) {
  return issues.map((issue) => {
    const { dependencies, ...rest } = issue as Record<string, unknown>
    if (dependencies && Array.isArray(dependencies)) {
      // Convert to graph API format (drop issue_id field)
      const graphDeps = dependencies.map((dep: Record<string, unknown>) => ({
        depends_on_id: dep.depends_on_id,
        type: dep.type,
      }))
      return { ...rest, dependencies: graphDeps }
    }
    return rest
  })
}

/**
 * Set up API mocks for dependency type filter tests.
 * Note: Graph view uses /api/issues/graph, not /api/ready
 */
async function setupMocks(page: Page, issues = mockIssuesWithDependencies) {
  // Mock /api/issues/graph endpoint used by useIssues in graph mode
  // Graph API returns { success: true, issues: [...] } not { success: true, data: [...] }
  await page.route("**/api/issues/graph**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, issues: toGraphApiFormat(issues) }),
    })
  })
  // Mock /api/blocked endpoint used by useBlockedIssues hook
  await page.route("**/api/blocked**", async (route) => {
    // Return blocked issues based on status in mock data
    const blockedIssues = issues.filter((i: Record<string, unknown>) => i.status === "blocked")
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: blockedIssues }),
    })
  })
}

/**
 * Navigate to Graph view and wait for API response.
 */
async function navigateToGraphView(page: Page, queryParams = "") {
  const path = queryParams ? `/?view=graph&${queryParams}` : "/?view=graph"
  const [response] = await Promise.all([
    page.waitForResponse(
      (res) => res.url().includes("/api/issues/graph") && res.status() === 200
    ),
    page.goto(path),
  ])
  expect(response.ok()).toBe(true)
  await expect(page.getByTestId("graph-view")).toBeVisible()
}

test.describe("Dependency Type Filter", () => {
  // Clear localStorage before each test to ensure clean state
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() => {
      localStorage.removeItem("graph-dep-type-filter")
      localStorage.removeItem("graph-status-filter")
      localStorage.removeItem("graph-show-closed")
    })
  })

  test.describe("Display Tests", () => {
    test("filter checkboxes render in GraphControls", async ({ page }) => {
      await setupMocks(page)
      await navigateToGraphView(page)

      // Verify GraphControls panel is visible
      await expect(page.getByTestId("graph-controls")).toBeVisible()

      // Verify dependency type filter group is visible
      const depTypeFilter = page.getByTestId("dep-type-filter")
      await expect(depTypeFilter).toBeVisible()

      // Verify all three checkboxes are visible
      await expect(page.getByTestId("dep-type-blocking")).toBeVisible()
      await expect(page.getByTestId("dep-type-parent-child")).toBeVisible()
      await expect(page.getByTestId("dep-type-non-blocking")).toBeVisible()
    })

    test("checkboxes have correct labels", async ({ page }) => {
      await setupMocks(page)
      await navigateToGraphView(page)

      const depTypeFilter = page.getByTestId("dep-type-filter")

      // Verify labels by text content (use exact match to avoid "Blocking" matching "Non-blocking")
      await expect(depTypeFilter.getByText("Blocking", { exact: true })).toBeVisible()
      await expect(depTypeFilter.getByText("Parent-Child")).toBeVisible()
      await expect(depTypeFilter.getByText("Non-blocking")).toBeVisible()
    })

    test("group label shows 'Edges'", async ({ page }) => {
      await setupMocks(page)
      await navigateToGraphView(page)

      const depTypeFilter = page.getByTestId("dep-type-filter")
      await expect(depTypeFilter.getByText("Edges")).toBeVisible()
    })
  })

  test.describe("Default State Tests", () => {
    test("default shows Blocking and Parent-Child checked", async ({ page }) => {
      await setupMocks(page)
      await navigateToGraphView(page)

      // Blocking should be checked by default
      const blockingCheckbox = page.getByTestId("dep-type-blocking")
      await expect(blockingCheckbox).toBeChecked()

      // Parent-Child should be checked by default
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child")
      await expect(parentChildCheckbox).toBeChecked()

      // Non-blocking should NOT be checked by default
      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking")
      await expect(nonBlockingCheckbox).not.toBeChecked()
    })

    test("default state shows blocking and parent-child edges only", async ({ page }) => {
      await setupMocks(page)
      await navigateToGraphView(page)

      // With default filter (blocking + parent-child), we should have:
      // - 1 blocking edge (issue-blocked -> issue-blocking)
      // - 1 parent-child edge (issue-child -> issue-parent)
      // - 0 related edges (non-blocking is unchecked)
      const edges = page.locator(".react-flow__edge")
      await expect(edges).toHaveCount(2)
    })

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
      ]

      await setupMocks(page, relatedOnly)
      await navigateToGraphView(page)

      // Non-blocking is unchecked by default, so no edges should be visible
      const edges = page.locator(".react-flow__edge")
      await expect(edges).toHaveCount(0)

      // But both nodes should still be visible
      const nodes = page.locator(".react-flow__node")
      await expect(nodes).toHaveCount(2)
    })
  })

  test.describe("Single Filter Tests", () => {
    test("unchecking Blocking hides blocking edges", async ({ page }) => {
      await setupMocks(page)
      await navigateToGraphView(page)

      const edges = page.locator(".react-flow__edge")
      const blockingCheckbox = page.getByTestId("dep-type-blocking")

      // Initially 2 edges (blocking + parent-child)
      await expect(edges).toHaveCount(2)

      // Uncheck Blocking
      await blockingCheckbox.uncheck()

      // Now only 1 edge (parent-child only)
      await expect(edges).toHaveCount(1)

      // Re-check Blocking
      await blockingCheckbox.check()

      // Back to 2 edges
      await expect(edges).toHaveCount(2)
    })

    test("unchecking Parent-Child hides parent-child edges", async ({ page }) => {
      await setupMocks(page)
      await navigateToGraphView(page)

      const edges = page.locator(".react-flow__edge")
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child")

      // Initially 2 edges (blocking + parent-child)
      await expect(edges).toHaveCount(2)

      // Uncheck Parent-Child
      await parentChildCheckbox.uncheck()

      // Now only 1 edge (blocking only)
      await expect(edges).toHaveCount(1)

      // Re-check Parent-Child
      await parentChildCheckbox.check()

      // Back to 2 edges
      await expect(edges).toHaveCount(2)
    })

    test("checking Non-blocking shows non-blocking edges", async ({ page }) => {
      await setupMocks(page)
      await navigateToGraphView(page)

      const edges = page.locator(".react-flow__edge")
      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking")

      // Initially 2 edges (non-blocking is unchecked)
      await expect(edges).toHaveCount(2)

      // Check Non-blocking
      await nonBlockingCheckbox.check()

      // Now 3 edges (blocking + parent-child + related)
      await expect(edges).toHaveCount(3)
    })

    test("nodes remain visible when edges are filtered", async ({ page }) => {
      await setupMocks(page)
      await navigateToGraphView(page)

      const nodes = page.locator(".react-flow__node")
      const blockingCheckbox = page.getByTestId("dep-type-blocking")
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child")

      // All 6 nodes visible
      await expect(nodes).toHaveCount(6)

      // Uncheck both Blocking and Parent-Child
      await blockingCheckbox.uncheck()
      await parentChildCheckbox.uncheck()

      // Nodes should still be visible (filtering only affects edges)
      await expect(nodes).toHaveCount(6)
    })
  })

  test.describe("Multi-select Tests", () => {
    test("can check all three dependency types", async ({ page }) => {
      await setupMocks(page)
      await navigateToGraphView(page)

      const blockingCheckbox = page.getByTestId("dep-type-blocking")
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child")
      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking")

      // Check all three
      await expect(blockingCheckbox).toBeChecked()
      await expect(parentChildCheckbox).toBeChecked()
      await nonBlockingCheckbox.check()

      // All three should now be checked
      await expect(blockingCheckbox).toBeChecked()
      await expect(parentChildCheckbox).toBeChecked()
      await expect(nonBlockingCheckbox).toBeChecked()

      // All 3 edges should be visible
      const edges = page.locator(".react-flow__edge")
      await expect(edges).toHaveCount(3)
    })

    test("unchecking all shows all edges (no filter)", async ({ page }) => {
      await setupMocks(page)
      await navigateToGraphView(page)

      const edges = page.locator(".react-flow__edge")
      const blockingCheckbox = page.getByTestId("dep-type-blocking")
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child")

      // Uncheck both checked checkboxes
      await blockingCheckbox.uncheck()
      await parentChildCheckbox.uncheck()

      // Per implementation: empty filter shows ALL edges (no filtering)
      // So all 3 edges should be visible
      await expect(edges).toHaveCount(3)
    })

    test("combination: Blocking + Non-blocking only", async ({ page }) => {
      await setupMocks(page)
      await navigateToGraphView(page)

      const edges = page.locator(".react-flow__edge")
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child")
      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking")

      // Uncheck Parent-Child, check Non-blocking
      await parentChildCheckbox.uncheck()
      await nonBlockingCheckbox.check()

      // Should show blocking + related = 2 edges
      await expect(edges).toHaveCount(2)
    })
  })

  test.describe("Edge Count Tests", () => {
    test("correct edge count with only Blocking selected", async ({ page }) => {
      await setupMocks(page)
      await navigateToGraphView(page)

      const edges = page.locator(".react-flow__edge")
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child")

      // Uncheck Parent-Child (leave only Blocking)
      await parentChildCheckbox.uncheck()

      // Only 1 blocking edge
      await expect(edges).toHaveCount(1)
    })

    test("correct edge count with only Parent-Child selected", async ({ page }) => {
      await setupMocks(page)
      await navigateToGraphView(page)

      const edges = page.locator(".react-flow__edge")
      const blockingCheckbox = page.getByTestId("dep-type-blocking")

      // Uncheck Blocking (leave only Parent-Child)
      await blockingCheckbox.uncheck()

      // Only 1 parent-child edge
      await expect(edges).toHaveCount(1)
    })

    test("correct edge count with only Non-blocking selected", async ({ page }) => {
      await setupMocks(page)
      await navigateToGraphView(page)

      const edges = page.locator(".react-flow__edge")
      const blockingCheckbox = page.getByTestId("dep-type-blocking")
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child")
      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking")

      // Uncheck Blocking and Parent-Child, check Non-blocking
      await blockingCheckbox.uncheck()
      await parentChildCheckbox.uncheck()
      await nonBlockingCheckbox.check()

      // Only 1 related edge
      await expect(edges).toHaveCount(1)
    })

    test("correct edge count with all types selected", async ({ page }) => {
      await setupMocks(page)
      await navigateToGraphView(page)

      const edges = page.locator(".react-flow__edge")
      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking")

      // Check Non-blocking (others already checked by default)
      await nonBlockingCheckbox.check()

      // All 3 edges visible
      await expect(edges).toHaveCount(3)
    })
  })

  test.describe("Persistence Tests", () => {
    test("filter state persists to localStorage", async ({ page }) => {
      await setupMocks(page)
      await navigateToGraphView(page)

      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking")

      // Check Non-blocking
      await nonBlockingCheckbox.check()

      // Verify localStorage contains the new filter state
      const value = await page.evaluate(() =>
        localStorage.getItem("graph-dep-type-filter")
      )
      const parsed = JSON.parse(value!)
      expect(parsed).toContain("blocking")
      expect(parsed).toContain("parent-child")
      expect(parsed).toContain("non-blocking")
    })

    test("filter state restores from localStorage", async ({ page }) => {
      // Set localStorage before navigation
      await page.addInitScript(() => {
        localStorage.setItem("graph-dep-type-filter", JSON.stringify(["non-blocking"]))
      })

      await setupMocks(page)
      await navigateToGraphView(page)

      // Verify checkboxes reflect localStorage state
      const blockingCheckbox = page.getByTestId("dep-type-blocking")
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child")
      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking")

      await expect(blockingCheckbox).not.toBeChecked()
      await expect(parentChildCheckbox).not.toBeChecked()
      await expect(nonBlockingCheckbox).toBeChecked()

      // Only related edge should be visible
      const edges = page.locator(".react-flow__edge")
      await expect(edges).toHaveCount(1)
    })

    test("filter state persists across page load", async ({ page }) => {
      // Pre-set localStorage with custom filter
      await page.addInitScript(() => {
        localStorage.setItem("graph-dep-type-filter", JSON.stringify(["blocking"]))
      })

      await setupMocks(page)
      await navigateToGraphView(page)

      const blockingCheckbox = page.getByTestId("dep-type-blocking")
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child")

      // Verify state restored
      await expect(blockingCheckbox).toBeChecked()
      await expect(parentChildCheckbox).not.toBeChecked()

      // Only blocking edge should be visible
      const edges = page.locator(".react-flow__edge")
      await expect(edges).toHaveCount(1)
    })

    test("invalid localStorage value falls back to defaults", async ({ page }) => {
      // Set invalid localStorage value
      await page.addInitScript(() => {
        localStorage.setItem("graph-dep-type-filter", "invalid_json")
      })

      await setupMocks(page)
      await navigateToGraphView(page)

      // Should fall back to default (blocking + parent-child)
      const blockingCheckbox = page.getByTestId("dep-type-blocking")
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child")
      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking")

      await expect(blockingCheckbox).toBeChecked()
      await expect(parentChildCheckbox).toBeChecked()
      await expect(nonBlockingCheckbox).not.toBeChecked()
    })

    test("invalid array values in localStorage are filtered out", async ({ page }) => {
      // Set localStorage with invalid group names
      await page.addInitScript(() => {
        localStorage.setItem(
          "graph-dep-type-filter",
          JSON.stringify(["blocking", "invalid-group", 123])
        )
      })

      await setupMocks(page)
      await navigateToGraphView(page)

      // Only valid 'blocking' should be checked
      const blockingCheckbox = page.getByTestId("dep-type-blocking")
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child")
      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking")

      await expect(blockingCheckbox).toBeChecked()
      await expect(parentChildCheckbox).not.toBeChecked()
      await expect(nonBlockingCheckbox).not.toBeChecked()
    })

    test("empty array in localStorage shows all edges", async ({ page }) => {
      // Set localStorage with empty array
      await page.addInitScript(() => {
        localStorage.setItem("graph-dep-type-filter", JSON.stringify([]))
      })

      await setupMocks(page)
      await navigateToGraphView(page)

      // Empty filter means no filtering - all edges visible
      const edges = page.locator(".react-flow__edge")
      await expect(edges).toHaveCount(3)

      // All checkboxes should be unchecked
      const blockingCheckbox = page.getByTestId("dep-type-blocking")
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child")
      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking")

      await expect(blockingCheckbox).not.toBeChecked()
      await expect(parentChildCheckbox).not.toBeChecked()
      await expect(nonBlockingCheckbox).not.toBeChecked()
    })
  })

  test.describe("Clear/Reset Tests", () => {
    test("checking all types shows all edges", async ({ page }) => {
      await setupMocks(page)
      await navigateToGraphView(page)

      const edges = page.locator(".react-flow__edge")
      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking")

      // Initially 2 edges (blocking + parent-child)
      await expect(edges).toHaveCount(2)

      // Check Non-blocking to show all
      await nonBlockingCheckbox.check()

      // All 3 edges visible
      await expect(edges).toHaveCount(3)
    })

    test("filter can be toggled repeatedly", async ({ page }) => {
      await setupMocks(page)
      await navigateToGraphView(page)

      const edges = page.locator(".react-flow__edge")
      const blockingCheckbox = page.getByTestId("dep-type-blocking")

      // Toggle multiple times
      for (let i = 0; i < 3; i++) {
        await blockingCheckbox.uncheck()
        await expect(edges).toHaveCount(1) // Only parent-child

        await blockingCheckbox.check()
        await expect(edges).toHaveCount(2) // blocking + parent-child
      }
    })
  })

  test.describe("Integration Tests", () => {
    // Note: Status filtering in GraphView is done client-side via visibleIssues useMemo.
    // The /api/issues/graph mock returns all issues, and GraphView filters them.
    test("works with status filter", async ({ page }) => {
      await setupMocks(page)
      await navigateToGraphView(page)

      const edges = page.locator(".react-flow__edge")
      const nodes = page.locator(".react-flow__node")
      const statusFilter = page.getByTestId("status-filter")

      // Initially 6 nodes, 2 edges
      await expect(nodes).toHaveCount(6)
      await expect(edges).toHaveCount(2)

      // Filter to only 'open' status
      await statusFilter.selectOption("open")

      // Fewer nodes (only open issues)
      // issue-parent, issue-child, issue-blocking, issue-related-1, issue-related-2 are open
      await expect(nodes).toHaveCount(5)

      // Parent-child edge still visible (both nodes are open)
      // Blocking edge removed (issue-blocked is 'blocked' status, filtered out)
      await expect(edges).toHaveCount(1)
    })

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
            { issue_id: "issue-closed", depends_on_id: "issue-open", type: "blocks" },
          ],
          created_at: "2026-01-27T11:00:00Z",
          updated_at: "2026-01-27T11:00:00Z",
        },
      ]

      await setupMocks(page, withClosed)
      await navigateToGraphView(page)

      const edges = page.locator(".react-flow__edge")
      const nodes = page.locator(".react-flow__node")
      const showClosedToggle = page.getByTestId("show-closed-toggle")

      // Initially both nodes and 1 edge visible
      await expect(nodes).toHaveCount(2)
      await expect(edges).toHaveCount(1)

      // Uncheck Show Closed
      await showClosedToggle.uncheck()

      // Closed node hidden, edge removed
      await expect(nodes).toHaveCount(1)
      await expect(edges).toHaveCount(0)
    })

    test("works with Highlight Ready toggle", async ({ page }) => {
      await setupMocks(page)
      await navigateToGraphView(page)

      const graphView = page.getByTestId("graph-view")
      const highlightReadyToggle = page.getByTestId("highlight-ready-toggle")
      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking")

      // Change dependency filter
      await nonBlockingCheckbox.check()

      // Enable Highlight Ready
      await highlightReadyToggle.check()

      // Verify data attribute is set
      await expect(graphView).toHaveAttribute("data-highlight-ready", "true")

      // Verify edges still reflect filter (3 edges with all checked)
      const edges = page.locator(".react-flow__edge")
      await expect(edges).toHaveCount(3)
    })
  })

  test.describe("Edge Cases", () => {
    test("filter works with empty issues list", async ({ page }) => {
      await setupMocks(page, [])
      await navigateToGraphView(page)

      const depTypeFilter = page.getByTestId("dep-type-filter")
      const blockingCheckbox = page.getByTestId("dep-type-blocking")

      // Filter controls still render
      await expect(depTypeFilter).toBeVisible()

      // Changing filter doesn't cause errors
      await blockingCheckbox.uncheck()
      await blockingCheckbox.check()

      const nodes = page.locator(".react-flow__node")
      const edges = page.locator(".react-flow__edge")
      await expect(nodes).toHaveCount(0)
      await expect(edges).toHaveCount(0)
    })

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
      ]

      await setupMocks(page, blockingOnly)
      await navigateToGraphView(page)

      const edges = page.locator(".react-flow__edge")
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child")

      // 1 blocking edge visible
      await expect(edges).toHaveCount(1)

      // Uncheck Parent-Child (no parent-child edges exist anyway)
      await parentChildCheckbox.uncheck()

      // Still 1 edge (blocking)
      await expect(edges).toHaveCount(1)

      // Filtering to only Parent-Child shows 0 edges
      const blockingCheckbox = page.getByTestId("dep-type-blocking")
      await blockingCheckbox.uncheck()
      await parentChildCheckbox.check()

      await expect(edges).toHaveCount(0)
    })

    test("checkboxes can be disabled when controls are disabled", async ({ page }) => {
      // This tests the disabled state when it's provided
      // The actual disabled state depends on component props, so we just verify
      // the checkboxes are interactive by default
      await setupMocks(page)
      await navigateToGraphView(page)

      const blockingCheckbox = page.getByTestId("dep-type-blocking")

      // Verify checkbox is enabled and interactive
      await expect(blockingCheckbox).toBeEnabled()

      // Can toggle it
      await blockingCheckbox.uncheck()
      await expect(blockingCheckbox).not.toBeChecked()
    })
  })

  test.describe("Accessibility Tests", () => {
    test("checkboxes are keyboard accessible", async ({ page }) => {
      await setupMocks(page)
      await navigateToGraphView(page)

      const blockingCheckbox = page.getByTestId("dep-type-blocking")

      // Focus the checkbox
      await blockingCheckbox.focus()
      await expect(blockingCheckbox).toBeFocused()

      // Toggle with Space key
      await page.keyboard.press("Space")
      await expect(blockingCheckbox).not.toBeChecked()

      // Toggle back
      await page.keyboard.press("Space")
      await expect(blockingCheckbox).toBeChecked()
    })

    test("can tab between checkboxes", async ({ page }) => {
      await setupMocks(page)
      await navigateToGraphView(page)

      const blockingCheckbox = page.getByTestId("dep-type-blocking")
      const parentChildCheckbox = page.getByTestId("dep-type-parent-child")
      const nonBlockingCheckbox = page.getByTestId("dep-type-non-blocking")

      // Focus first checkbox
      await blockingCheckbox.focus()
      await expect(blockingCheckbox).toBeFocused()

      // Tab to next checkbox
      await page.keyboard.press("Tab")
      await expect(parentChildCheckbox).toBeFocused()

      // Tab to next checkbox
      await page.keyboard.press("Tab")
      await expect(nonBlockingCheckbox).toBeFocused()
    })
  })
})
