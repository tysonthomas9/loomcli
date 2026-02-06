import { test, expect, Page } from "@playwright/test"

/**
 * Mock issues for testing view switching.
 * Minimal set to verify views render with data.
 */
const mockIssues = [
  {
    id: "test-1",
    title: "Test Issue 1",
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-24T10:00:00Z",
    updated_at: "2026-01-24T10:00:00Z",
  },
  {
    id: "test-2",
    title: "Test Issue 2",
    status: "in_progress",
    priority: 1,
    issue_type: "task",
    created_at: "2026-01-24T11:00:00Z",
    updated_at: "2026-01-24T11:00:00Z",
  },
]

/**
 * Set up API mocks for ViewSwitcher tests.
 */
async function setupMocks(page: Page) {
  await page.route("**/api/ready", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: mockIssues }),
    })
  })
  // Mock /api/issues/graph for Graph view
  await page.route("**/api/issues/graph", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: mockIssues }),
    })
  })
  // Mock SSE events endpoint (app uses /api/events instead of WebSocket)
  await page.route("**/api/events", async (route) => {
    await route.abort()
  })
  // Mock /api/stats to prevent proxy errors
  await page.route("**/api/stats", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: { open: 2, closed: 0, total: 2, completion: 0 } }),
    })
  })
}

/**
 * Navigate to a page and wait for API response.
 */
async function navigateAndWait(page: Page, path: string) {
  const [response] = await Promise.all([
    page.waitForResponse(
      (res) => res.url().includes("/api/ready") && res.status() === 200
    ),
    page.goto(path),
  ])
  expect(response.ok()).toBe(true)
}

test.describe("ViewSwitcher", () => {
  test.beforeEach(async ({ page }) => {
    // Mock /api/ready endpoint to return test data
    await page.route("**/api/ready", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          success: true,
          data: mockIssues,
        }),
      })
    })

    // Mock /api/issues/graph for Graph view
    await page.route("**/api/issues/graph", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: mockIssues }),
      })
    })

    // Mock SSE events endpoint (app uses /api/events instead of WebSocket)
    await page.route("**/api/events", async (route) => {
      await route.abort()
    })

    // Mock /api/stats to prevent proxy errors
    await page.route("**/api/stats", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: { open: 2, closed: 0, total: 2, completion: 0 } }),
      })
    })
  })

  test("default view is Kanban", async ({ page }) => {
    // Navigate and wait for API response
    await Promise.all([
      page.waitForResponse((res) => res.url().includes("/api/ready") && res.status() === 200),
      page.goto("/"),
    ])

    // Verify Kanban tab is selected
    const kanbanTab = page.getByTestId("view-tab-kanban")
    await expect(kanbanTab).toHaveAttribute("aria-selected", "true")

    // Verify Kanban column is visible
    const openColumn = page.locator('section[data-status="ready"]')
    await expect(openColumn).toBeVisible()
  })

  test("clicking Table tab renders IssueTable", async ({ page }) => {
    // Navigate and wait for API response
    await Promise.all([
      page.waitForResponse((res) => res.url().includes("/api/ready") && res.status() === 200),
      page.goto("/"),
    ])

    // Click Table tab
    const tableTab = page.getByTestId("view-tab-table")
    await tableTab.click()

    // Verify Table tab is selected
    await expect(tableTab).toHaveAttribute("aria-selected", "true")

    // Verify IssueTable is visible
    const issueTable = page.getByTestId("issue-table")
    await expect(issueTable).toBeVisible()

    // Verify Kanban column is NOT visible
    const openColumn = page.locator('section[data-status="ready"]')
    await expect(openColumn).not.toBeVisible()
  })

  test("clicking Graph tab renders GraphView", async ({ page }) => {
    // Navigate and wait for API response
    await Promise.all([
      page.waitForResponse((res) => res.url().includes("/api/ready") && res.status() === 200),
      page.goto("/"),
    ])

    // Click Graph tab
    const graphTab = page.getByTestId("view-tab-graph")
    await graphTab.click()

    // Verify Graph tab is selected
    await expect(graphTab).toHaveAttribute("aria-selected", "true")

    // Verify GraphView is visible
    const graphView = page.getByTestId("graph-view")
    await expect(graphView).toBeVisible()

    // Verify IssueTable is NOT visible
    const issueTable = page.getByTestId("issue-table")
    await expect(issueTable).not.toBeVisible()
  })

  test("clicking Kanban tab returns to KanbanBoard", async ({ page }) => {
    // Navigate and wait for API response
    await Promise.all([
      page.waitForResponse((res) => res.url().includes("/api/ready") && res.status() === 200),
      page.goto("/"),
    ])

    // Click Table tab first (switch away from Kanban)
    const tableTab = page.getByTestId("view-tab-table")
    await tableTab.click()
    await expect(tableTab).toHaveAttribute("aria-selected", "true")

    // Click Kanban tab to return
    const kanbanTab = page.getByTestId("view-tab-kanban")
    await kanbanTab.click()

    // Verify Kanban tab is selected
    await expect(kanbanTab).toHaveAttribute("aria-selected", "true")

    // Verify Kanban column is visible
    const openColumn = page.locator('section[data-status="ready"]')
    await expect(openColumn).toBeVisible()
  })

  test.describe("keyboard navigation", () => {
    test("ArrowRight navigates from Kanban to Table", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      // Focus on the active (Kanban) tab
      const kanbanTab = page.locator('[data-testid="view-tab-kanban"]')
      await kanbanTab.focus()

      // Press ArrowRight
      await page.keyboard.press("ArrowRight")

      // Verify focus moved to Table tab
      const tableTab = page.locator('[data-testid="view-tab-table"]')
      await expect(tableTab).toBeFocused()

      // Verify Table tab is now active
      await expect(tableTab).toHaveAttribute("aria-selected", "true")

      // Verify Table view is rendered and Kanban view is hidden
      await expect(page.locator('[data-testid="issue-table"]')).toBeVisible()
      await expect(
        page.locator('section[data-status="ready"]')
      ).not.toBeVisible()
    })

    test("ArrowRight navigates from Table to Graph", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?view=table")

      // Focus on the active (Table) tab
      const tableTab = page.locator('[data-testid="view-tab-table"]')
      await tableTab.focus()

      // Press ArrowRight
      await page.keyboard.press("ArrowRight")

      // Verify focus moved to Graph tab
      const graphTab = page.locator('[data-testid="view-tab-graph"]')
      await expect(graphTab).toBeFocused()

      // Verify Graph tab is now active
      await expect(graphTab).toHaveAttribute("aria-selected", "true")

      // Verify Graph view is rendered and Table view is hidden
      await expect(page.locator('[data-testid="graph-view"]')).toBeVisible()
      await expect(
        page.locator('[data-testid="issue-table"]')
      ).not.toBeVisible()
    })

    test("ArrowRight wraps from Graph to Kanban", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?view=graph")

      // Focus on the active (Graph) tab
      const graphTab = page.locator('[data-testid="view-tab-graph"]')
      await graphTab.focus()

      // Press ArrowRight - should wrap to Kanban
      await page.keyboard.press("ArrowRight")

      // Verify focus moved to Kanban tab
      const kanbanTab = page.locator('[data-testid="view-tab-kanban"]')
      await expect(kanbanTab).toBeFocused()

      // Verify Kanban tab is now active
      await expect(kanbanTab).toHaveAttribute("aria-selected", "true")

      // Verify Kanban view is rendered and Graph view is hidden
      await expect(page.locator('section[data-status="ready"]')).toBeVisible()
      await expect(page.locator('[data-testid="graph-view"]')).not.toBeVisible()
    })

    test("ArrowLeft navigates from Graph to Table", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?view=graph")

      // Focus on the active (Graph) tab
      const graphTab = page.locator('[data-testid="view-tab-graph"]')
      await graphTab.focus()

      // Press ArrowLeft
      await page.keyboard.press("ArrowLeft")

      // Verify focus moved to Table tab
      const tableTab = page.locator('[data-testid="view-tab-table"]')
      await expect(tableTab).toBeFocused()

      // Verify Table tab is now active
      await expect(tableTab).toHaveAttribute("aria-selected", "true")

      // Verify Table view is rendered and Graph view is hidden
      await expect(page.locator('[data-testid="issue-table"]')).toBeVisible()
      await expect(page.locator('[data-testid="graph-view"]')).not.toBeVisible()
    })

    test("ArrowLeft wraps from Kanban to Graph", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      // Focus on the active (Kanban) tab
      const kanbanTab = page.locator('[data-testid="view-tab-kanban"]')
      await kanbanTab.focus()

      // Press ArrowLeft - should wrap to Graph
      await page.keyboard.press("ArrowLeft")

      // Verify focus moved to Graph tab
      const graphTab = page.locator('[data-testid="view-tab-graph"]')
      await expect(graphTab).toBeFocused()

      // Verify Graph tab is now active
      await expect(graphTab).toHaveAttribute("aria-selected", "true")

      // Verify Graph view is rendered and Kanban view is hidden
      await expect(page.locator('[data-testid="graph-view"]')).toBeVisible()
      await expect(
        page.locator('section[data-status="ready"]')
      ).not.toBeVisible()
    })

    test("Home navigates to first tab (Kanban)", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?view=graph")

      // Focus on the active (Graph) tab
      const graphTab = page.locator('[data-testid="view-tab-graph"]')
      await graphTab.focus()

      // Press Home
      await page.keyboard.press("Home")

      // Verify focus moved to Kanban tab
      const kanbanTab = page.locator('[data-testid="view-tab-kanban"]')
      await expect(kanbanTab).toBeFocused()

      // Verify Kanban tab is now active
      await expect(kanbanTab).toHaveAttribute("aria-selected", "true")

      // Verify Kanban view is rendered and Graph view is hidden
      await expect(page.locator('section[data-status="ready"]')).toBeVisible()
      await expect(page.locator('[data-testid="graph-view"]')).not.toBeVisible()
    })

    test("End navigates to last tab (Graph)", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      // Focus on the active (Kanban) tab
      const kanbanTab = page.locator('[data-testid="view-tab-kanban"]')
      await kanbanTab.focus()

      // Press End
      await page.keyboard.press("End")

      // Verify focus moved to Graph tab
      const graphTab = page.locator('[data-testid="view-tab-graph"]')
      await expect(graphTab).toBeFocused()

      // Verify Graph tab is now active
      await expect(graphTab).toHaveAttribute("aria-selected", "true")

      // Verify Graph view is rendered and Kanban view is hidden
      await expect(page.locator('[data-testid="graph-view"]')).toBeVisible()
      await expect(
        page.locator('section[data-status="ready"]')
      ).not.toBeVisible()
    })

    test("ArrowDown behaves like ArrowRight", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      // Focus on the active (Kanban) tab
      const kanbanTab = page.locator('[data-testid="view-tab-kanban"]')
      await kanbanTab.focus()

      // Press ArrowDown
      await page.keyboard.press("ArrowDown")

      // Verify focus moved to Table tab
      const tableTab = page.locator('[data-testid="view-tab-table"]')
      await expect(tableTab).toBeFocused()

      // Verify Table tab is now active
      await expect(tableTab).toHaveAttribute("aria-selected", "true")

      // Verify Table view is rendered and Kanban view is hidden
      await expect(page.locator('[data-testid="issue-table"]')).toBeVisible()
      await expect(
        page.locator('section[data-status="ready"]')
      ).not.toBeVisible()
    })

    test("ArrowUp behaves like ArrowLeft", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?view=graph")

      // Focus on the active (Graph) tab
      const graphTab = page.locator('[data-testid="view-tab-graph"]')
      await graphTab.focus()

      // Press ArrowUp
      await page.keyboard.press("ArrowUp")

      // Verify focus moved to Table tab
      const tableTab = page.locator('[data-testid="view-tab-table"]')
      await expect(tableTab).toBeFocused()

      // Verify Table tab is now active
      await expect(tableTab).toHaveAttribute("aria-selected", "true")

      // Verify Table view is rendered and Graph view is hidden
      await expect(page.locator('[data-testid="issue-table"]')).toBeVisible()
      await expect(page.locator('[data-testid="graph-view"]')).not.toBeVisible()
    })

    test("Enter activates focused tab", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      // Start on Kanban, use Tab key to move focus to Table button
      // This simulates a user tabbing through without using arrow keys
      const tableTab = page.locator('[data-testid="view-tab-table"]')
      await tableTab.focus()

      // Table should be focused but NOT selected yet (Kanban is still active)
      await expect(tableTab).toBeFocused()

      // Note: In the automatic activation tabs pattern used by ViewSwitcher,
      // focus and selection happen together when using arrow keys.
      // However, Enter/Space should also work on any focused tab.
      // Press Enter to activate
      await page.keyboard.press("Enter")

      // Verify Table tab is now active
      await expect(tableTab).toHaveAttribute("aria-selected", "true")

      // Verify Table view is rendered
      await expect(page.locator('[data-testid="issue-table"]')).toBeVisible()
      await expect(
        page.locator('section[data-status="ready"]')
      ).not.toBeVisible()
    })
  })
})

test.describe("ViewSwitcher URL Sync", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page)
  })

  test("clicking Table tab updates URL to ?view=table", async ({ page }) => {
    // Navigate to root (default kanban view) and wait for API response
    const [response] = await Promise.all([
      page.waitForResponse((res) => res.url().includes("/api/ready") && res.status() === 200),
      page.goto("/"),
    ])
    expect(response.ok()).toBe(true)

    // Verify URL has no view param initially
    expect(page.url()).not.toContain("view=")

    // Wait for ViewSwitcher to be visible and click Table tab
    const tableTab = page.getByTestId("view-tab-table")
    await expect(tableTab).toBeVisible()
    await tableTab.click()

    // Verify URL now contains ?view=table
    expect(page.url()).toContain("view=table")

    // Verify Table tab is selected
    await expect(tableTab).toHaveAttribute("aria-selected", "true")
  })

  test("clicking Graph tab updates URL to ?view=graph", async ({ page }) => {
    // Navigate to root and wait for API response
    const [response] = await Promise.all([
      page.waitForResponse((res) => res.url().includes("/api/ready") && res.status() === 200),
      page.goto("/"),
    ])
    expect(response.ok()).toBe(true)

    // Wait for ViewSwitcher to be visible and click Graph tab
    const graphTab = page.getByTestId("view-tab-graph")
    await expect(graphTab).toBeVisible()
    await graphTab.click()

    // Verify URL contains ?view=graph
    expect(page.url()).toContain("view=graph")

    // Verify Graph tab is selected
    await expect(graphTab).toHaveAttribute("aria-selected", "true")
  })

  test("clicking Kanban tab removes view param from URL", async ({ page }) => {
    // Navigate to root and wait for API response
    const [response] = await Promise.all([
      page.waitForResponse((res) => res.url().includes("/api/ready") && res.status() === 200),
      page.goto("/"),
    ])
    expect(response.ok()).toBe(true)

    // Wait for ViewSwitcher to be visible and switch to Table view first
    const tableTab = page.getByTestId("view-tab-table")
    await expect(tableTab).toBeVisible()
    await tableTab.click()

    // Verify URL has view param after clicking Table
    expect(page.url()).toContain("view=table")

    // Click Kanban tab
    const kanbanTab = page.getByTestId("view-tab-kanban")
    await expect(kanbanTab).toBeVisible()
    await kanbanTab.click()

    // Verify URL no longer has view param (clean URL for default)
    expect(page.url()).not.toContain("view=")

    // Verify Kanban tab is selected
    await expect(kanbanTab).toHaveAttribute("aria-selected", "true")
  })

  test("navigating to ?view=table loads Table view", async ({ page }) => {
    // Navigate directly to table view via URL
    const [response] = await Promise.all([
      page.waitForResponse((res) => res.url().includes("/api/ready") && res.status() === 200),
      page.goto("/?view=table"),
    ])
    expect(response.ok()).toBe(true)

    // Verify Table tab is selected
    const tableTab = page.getByTestId("view-tab-table")
    await expect(tableTab).toBeVisible()
    await expect(tableTab).toHaveAttribute("aria-selected", "true")

    // Verify Kanban tab is not selected
    const kanbanTab = page.getByTestId("view-tab-kanban")
    await expect(kanbanTab).toHaveAttribute("aria-selected", "false")

    // Verify IssueTable is rendered
    const issueTable = page.getByTestId("issue-table")
    await expect(issueTable).toBeVisible()

    // Verify Kanban view is not rendered
    const openColumn = page.locator('section[data-status="ready"]')
    await expect(openColumn).not.toBeVisible()
  })

  test("navigating to ?view=graph loads Graph view", async ({ page }) => {
    // Navigate directly to graph view via URL
    const [response] = await Promise.all([
      page.waitForResponse((res) => res.url().includes("/api/ready") && res.status() === 200),
      page.goto("/?view=graph"),
    ])
    expect(response.ok()).toBe(true)

    // Verify Graph tab is selected
    const graphTab = page.getByTestId("view-tab-graph")
    await expect(graphTab).toBeVisible()
    await expect(graphTab).toHaveAttribute("aria-selected", "true")

    // Verify other tabs are not selected
    const kanbanTab = page.getByTestId("view-tab-kanban")
    const tableTab = page.getByTestId("view-tab-table")
    await expect(kanbanTab).toHaveAttribute("aria-selected", "false")
    await expect(tableTab).toHaveAttribute("aria-selected", "false")

    // Verify GraphView is rendered
    const graphView = page.getByTestId("graph-view")
    await expect(graphView).toBeVisible()

    // Verify other views are not rendered
    const issueTable = page.getByTestId("issue-table")
    const openColumn = page.locator('section[data-status="ready"]')
    await expect(issueTable).not.toBeVisible()
    await expect(openColumn).not.toBeVisible()
  })
})
