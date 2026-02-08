import { test, expect, Page } from "@playwright/test"

/**
 * E2E tests for DependencyEdge component styling.
 *
 * Tests verify that dependency edges render with correct visual styles
 * based on their blocking status and dependency type.
 *
 * Blocking types: blocks, parent-child, conditional-blocks, waits-for
 * Non-blocking types: related, relates-to, duplicates, supersedes
 */

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
]

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
]

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
]

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
]

/**
 * Set up API mocks for edge styling tests.
 */
async function setupMocks(page: Page, issues: object[]) {
  await page.route("**/api/ready", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: issues }),
    })
  })
}

/**
 * Navigate to Graph View and wait for API response.
 */
async function navigateToGraphView(page: Page) {
  const [response] = await Promise.all([
    page.waitForResponse(
      (res) => res.url().includes("/api/ready") && res.status() === 200
    ),
    page.goto("/?view=graph"),
  ])
  expect(response.ok()).toBe(true)
  await expect(page.getByTestId("graph-view")).toBeVisible()
}

/**
 * Get all edges in the graph.
 */
function getAllEdges(page: Page) {
  return page.locator(".react-flow__edge")
}

/**
 * Wait for edges to be rendered with paths.
 */
async function waitForEdgesWithPaths(page: Page, count: number) {
  const edges = getAllEdges(page)
  await expect(edges).toHaveCount(count)
  // Wait for edge paths to be present in the DOM
  const edgePaths = page.locator(".react-flow__edge path.react-flow__edge-path")
  await expect(edgePaths).toHaveCount(count)
}

/**
 * Check if an edge path has a blocking style class (CSS Modules hashed).
 * CSS Modules classes are hashed like "_blockingEdge_xxxx_xx"
 */
async function hasBlockingEdgeClass(page: Page): Promise<boolean> {
  const edgePath = page.locator(".react-flow__edge path.react-flow__edge-path").first()
  const className = await edgePath.getAttribute("class")
  // CSS Modules hash the class name, so check for partial match
  return className?.includes("blockingEdge") ?? false
}

/**
 * Check if an edge path has a normal (non-blocking) style class.
 */
async function hasNormalEdgeClass(page: Page): Promise<boolean> {
  const edgePath = page.locator(".react-flow__edge path.react-flow__edge-path").first()
  const className = await edgePath.getAttribute("class")
  return className?.includes("normalEdge") ?? false
}

/**
 * Get computed stroke properties from edge path.
 */
async function getEdgeStrokeProps(page: Page) {
  const edgePath = page.locator(".react-flow__edge path.react-flow__edge-path").first()
  return await edgePath.evaluate((el) => ({
    stroke: window.getComputedStyle(el).stroke,
    strokeDasharray: window.getComputedStyle(el).strokeDasharray,
    strokeWidth: window.getComputedStyle(el).strokeWidth,
  }))
}

test.describe("Dependency Edge Styling", () => {
  test.describe("Blocking edge styling", () => {
    test("'blocks' type edge has blockingEdge CSS class", async ({ page }) => {
      await setupMocks(page, mockIssuesWithBlocks)
      await navigateToGraphView(page)
      await waitForEdgesWithPaths(page, 1)

      const hasBlocking = await hasBlockingEdgeClass(page)
      expect(hasBlocking).toBe(true)
    })

    test("'blocks' type edge has dashed stroke pattern", async ({ page }) => {
      await setupMocks(page, mockIssuesWithBlocks)
      await navigateToGraphView(page)
      await waitForEdgesWithPaths(page, 1)

      const props = await getEdgeStrokeProps(page)
      // Should have dashed pattern (5, 5 or 5 5 format)
      expect(props.strokeDasharray).toMatch(/^\d+(px)?[\s,]+\d+(px)?$/)
      expect(props.strokeDasharray).not.toBe("none")
    })

    test("'blocks' type edge has 2px stroke width", async ({ page }) => {
      await setupMocks(page, mockIssuesWithBlocks)
      await navigateToGraphView(page)
      await waitForEdgesWithPaths(page, 1)

      const edgePath = page.locator(".react-flow__edge path.react-flow__edge-path").first()
      // strokeWidth is set via inline style attribute
      const strokeWidth = await edgePath.evaluate((el) => {
        // Check inline style first, then computed style
        const inlineStyle = el.getAttribute("style")
        const match = inlineStyle?.match(/stroke-width:\s*([\d.]+)/)
        if (match) return parseFloat(match[1])
        return parseFloat(window.getComputedStyle(el).strokeWidth)
      })
      expect(strokeWidth).toBe(2)
    })

    test("'parent-child' type edge has blockingEdge CSS class", async ({ page }) => {
      await setupMocks(page, mockIssuesWithParentChild)
      await navigateToGraphView(page)
      await waitForEdgesWithPaths(page, 1)

      const hasBlocking = await hasBlockingEdgeClass(page)
      expect(hasBlocking).toBe(true)
    })

    test("'parent-child' type edge has dashed stroke", async ({ page }) => {
      await setupMocks(page, mockIssuesWithParentChild)
      await navigateToGraphView(page)
      await waitForEdgesWithPaths(page, 1)

      const props = await getEdgeStrokeProps(page)
      // Should have dashed pattern (5, 5 or 5 5 format)
      expect(props.strokeDasharray).toMatch(/^\d+(px)?[\s,]+\d+(px)?$/)
      expect(props.strokeDasharray).not.toBe("none")
    })
  })

  test.describe("Non-blocking edge styling", () => {
    test("'related' type edge has normalEdge CSS class", async ({ page }) => {
      await setupMocks(page, mockIssuesWithRelated)
      await navigateToGraphView(page)
      await waitForEdgesWithPaths(page, 1)

      const hasNormal = await hasNormalEdgeClass(page)
      expect(hasNormal).toBe(true)
    })

    test("'related' type edge has solid stroke (no dash)", async ({ page }) => {
      await setupMocks(page, mockIssuesWithRelated)
      await navigateToGraphView(page)
      await waitForEdgesWithPaths(page, 1)

      const props = await getEdgeStrokeProps(page)
      // SVG returns empty string or "none" when no dasharray is set
      expect(props.strokeDasharray === "none" || props.strokeDasharray === "").toBe(true)
    })

    test("'related' type edge has 1.5px stroke width", async ({ page }) => {
      await setupMocks(page, mockIssuesWithRelated)
      await navigateToGraphView(page)
      await waitForEdgesWithPaths(page, 1)

      const edgePath = page.locator(".react-flow__edge path.react-flow__edge-path").first()
      // strokeWidth is set via inline style attribute
      const strokeWidth = await edgePath.evaluate((el) => {
        const inlineStyle = el.getAttribute("style")
        const match = inlineStyle?.match(/stroke-width:\s*([\d.]+)/)
        if (match) return parseFloat(match[1])
        return parseFloat(window.getComputedStyle(el).strokeWidth)
      })
      expect(strokeWidth).toBe(1.5)
    })
  })

  test.describe("Edge labels", () => {
    test("edge label displays dependency type text", async ({ page }) => {
      await setupMocks(page, mockIssuesWithBlocks)
      await navigateToGraphView(page)
      await waitForEdgesWithPaths(page, 1)

      // Look for edge label containing the dependency type
      const edgeLabel = page.locator(".react-flow__edgelabel-renderer div")
      await expect(edgeLabel).toContainText("blocks")
    })

    test("related edge label displays 'related'", async ({ page }) => {
      await setupMocks(page, mockIssuesWithRelated)
      await navigateToGraphView(page)
      await waitForEdgesWithPaths(page, 1)

      const edgeLabel = page.locator(".react-flow__edgelabel-renderer div")
      await expect(edgeLabel).toContainText("related")
    })

    test("parent-child edge label displays 'parent-child'", async ({ page }) => {
      await setupMocks(page, mockIssuesWithParentChild)
      await navigateToGraphView(page)
      await waitForEdgesWithPaths(page, 1)

      const edgeLabel = page.locator(".react-flow__edgelabel-renderer div")
      await expect(edgeLabel).toContainText("parent-child")
    })

    test("edge label has monospace font styling", async ({ page }) => {
      await setupMocks(page, mockIssuesWithBlocks)
      await navigateToGraphView(page)
      await waitForEdgesWithPaths(page, 1)

      const edgeLabel = page.locator(".react-flow__edgelabel-renderer div")
      await expect(edgeLabel).toBeVisible()

      const fontFamily = await edgeLabel.evaluate((el) => {
        return window.getComputedStyle(el).fontFamily
      })

      // Should contain 'monospace' in font family
      expect(fontFamily.toLowerCase()).toContain("monospace")
    })
  })

  test.describe("Visual distinction between edge types", () => {
    test("blocking and non-blocking edges have different CSS classes", async ({
      page,
    }) => {
      await setupMocks(page, mockIssuesMultipleTypes)
      await navigateToGraphView(page)
      await waitForEdgesWithPaths(page, 2)

      // Get all edge path classes
      const edgePaths = page.locator(".react-flow__edge path.react-flow__edge-path")
      const classNames = await edgePaths.evaluateAll((paths) =>
        paths.map((path) => path.getAttribute("class") ?? "")
      )

      // One should have blockingEdge, one should have normalEdge
      const hasBlocking = classNames.some((c) => c.includes("blockingEdge"))
      const hasNormal = classNames.some((c) => c.includes("normalEdge"))
      expect(hasBlocking).toBe(true)
      expect(hasNormal).toBe(true)
    })

    test("blocking edge has dash, non-blocking is solid", async ({ page }) => {
      await setupMocks(page, mockIssuesMultipleTypes)
      await navigateToGraphView(page)
      await waitForEdgesWithPaths(page, 2)

      const edgePaths = page.locator(".react-flow__edge path.react-flow__edge-path")
      const strokeDasharrays = await edgePaths.evaluateAll((paths) =>
        paths.map((path) => window.getComputedStyle(path).strokeDasharray)
      )

      // Should have two different dash array values (one with dash pattern, one solid/none)
      const uniqueValues = [...new Set(strokeDasharrays)]
      expect(uniqueValues.length).toBeGreaterThanOrEqual(2)

      // At least one should be solid (empty string or 'none')
      const hasSolid = strokeDasharrays.some((d) => d === "none" || d === "")
      expect(hasSolid).toBe(true)

      // At least one should have a dash pattern (contains digits)
      const hasDashed = strokeDasharrays.some((d) => /\d/.test(d))
      expect(hasDashed).toBe(true)
    })

    test("blocking and non-blocking edges have different stroke widths", async ({
      page,
    }) => {
      await setupMocks(page, mockIssuesMultipleTypes)
      await navigateToGraphView(page)
      await waitForEdgesWithPaths(page, 2)

      const edgePaths = page.locator(".react-flow__edge path.react-flow__edge-path")
      const strokeWidths = await edgePaths.evaluateAll((paths) =>
        paths.map((path) => {
          // strokeWidth is set via inline style attribute
          const inlineStyle = path.getAttribute("style")
          const match = inlineStyle?.match(/stroke-width:\s*([\d.]+)/)
          if (match) return parseFloat(match[1])
          return parseFloat(window.getComputedStyle(path).strokeWidth)
        })
      )

      // Should have 2px (blocking) and 1.5px (non-blocking)
      expect(strokeWidths).toContain(2)
      expect(strokeWidths).toContain(1.5)
    })
  })

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
      ]

      await setupMocks(page, noDependencies)
      await navigateToGraphView(page)

      // Wait for graph to render nodes
      const nodes = page.locator(".react-flow__node")
      await expect(nodes).toHaveCount(2)

      // Should have no edges
      const edges = getAllEdges(page)
      await expect(edges).toHaveCount(0)
    })

    test("single dependency renders one edge", async ({ page }) => {
      await setupMocks(page, mockIssuesWithBlocks)
      await navigateToGraphView(page)

      const edges = getAllEdges(page)
      await expect(edges).toHaveCount(1)
    })

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
      ]

      await setupMocks(page, multipleDeps)
      await navigateToGraphView(page)

      // Should have 2 separate edges
      const edges = getAllEdges(page)
      await expect(edges).toHaveCount(2)
    })
  })

  test.describe("Reduced motion preferences", () => {
    test("blocking edge animation respects prefers-reduced-motion", async ({
      page,
    }) => {
      // Enable prefers-reduced-motion
      await page.emulateMedia({ reducedMotion: "reduce" })

      await setupMocks(page, mockIssuesWithBlocks)
      await navigateToGraphView(page)
      await waitForEdgesWithPaths(page, 1)

      const edgePath = page.locator(".react-flow__edge path.react-flow__edge-path").first()
      // Animation should be 'none' when reduced motion is enabled
      const animation = await edgePath.evaluate((el) => {
        return window.getComputedStyle(el).animationName
      })

      expect(animation).toBe("none")
    })
  })
})
