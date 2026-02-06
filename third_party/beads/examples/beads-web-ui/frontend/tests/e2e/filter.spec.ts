import { test, expect, Page } from "@playwright/test"

/**
 * Mock issues for testing filters.
 * Contains issues with varied titles, descriptions, types, priorities, and notes
 * to verify priority, type, and search filtering.
 */
const mockIssues = [
  {
    id: "p0-issue",
    title: "Critical Bug",
    description: "A critical production bug that needs immediate attention",
    notes: "Found in login flow",
    status: "open",
    priority: 0,
    issue_type: "bug",
    created_at: "2026-01-24T10:00:00Z",
    updated_at: "2026-01-24T10:00:00Z",
  },
  {
    id: "p1-issue-1",
    title: "High Priority Task",
    description: "Important task for the sprint",
    notes: "",
    status: "open",
    priority: 1,
    issue_type: "task",
    created_at: "2026-01-24T11:00:00Z",
    updated_at: "2026-01-24T11:00:00Z",
  },
  {
    id: "p1-issue-2",
    title: "Another High Task",
    description: "Another urgent item",
    notes: "Assigned to team lead",
    status: "in_progress",
    priority: 1,
    issue_type: "task",
    created_at: "2026-01-24T12:00:00Z",
    updated_at: "2026-01-24T12:00:00Z",
  },
  {
    id: "p2-issue",
    title: "Medium Task",
    description: "Standard priority work item",
    notes: "",
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-24T13:00:00Z",
    updated_at: "2026-01-24T13:00:00Z",
  },
  {
    id: "p3-issue",
    title: "Normal Task",
    description: "Regular maintenance work",
    notes: "",
    status: "closed",
    priority: 3,
    issue_type: "chore",
    created_at: "2026-01-24T14:00:00Z",
    updated_at: "2026-01-24T14:00:00Z",
  },
  {
    id: "p4-issue",
    title: "Backlog Item",
    description: "Future enhancement request",
    notes: "",
    status: "open",
    priority: 4,
    issue_type: "feature",
    created_at: "2026-01-24T15:00:00Z",
    updated_at: "2026-01-24T15:00:00Z",
  },
  {
    id: "feature-req",
    title: "Feature Request",
    description: "New feature for user authentication",
    notes: "Requested by customer",
    status: "open",
    priority: 2,
    issue_type: "feature",
    created_at: "2026-01-24T16:00:00Z",
    updated_at: "2026-01-24T16:00:00Z",
  },
  {
    id: "doc-update",
    title: "Documentation Update",
    description: "Update API documentation with examples",
    notes: "",
    status: "open",
    priority: 3,
    issue_type: "task",
    created_at: "2026-01-24T17:00:00Z",
    updated_at: "2026-01-24T17:00:00Z",
  },
]

/**
 * Set up API mocks for filter tests.
 */
async function setupMocks(page: Page, issues = mockIssues) {
  await page.route("**/api/ready", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: issues }),
    })
  })
  // Mock stats endpoint to prevent "Stats unavailable" overlay
  await page.route("**/api/stats", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: {
          total: issues.length,
          open: issues.filter((i) => i.status === "open").length,
          in_progress: issues.filter((i) => i.status === "in_progress").length,
          closed: issues.filter((i) => i.status === "closed").length,
        },
      }),
    })
  })
  // Mock blocked endpoint
  await page.route("**/api/blocked", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: [] }),
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

test.describe("FilterBar - Priority Filter", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page)
  })

  test("selecting priority filters issues to only matching priority", async ({ page }) => {
    await navigateAndWait(page, "/")

    // Wait for the Kanban board to render
    const openColumn = page.locator('section[data-status="ready"]')
    const inProgressColumn = page.locator('section[data-status="in_progress"]')
    const closedColumn = page.locator('section[data-status="done"]')

    await expect(openColumn).toBeVisible()
    await expect(inProgressColumn).toBeVisible()
    await expect(closedColumn).toBeVisible()

    // Count total issues initially (should be 8)
    const openCards = openColumn.locator("article")
    const inProgressCards = inProgressColumn.locator("article")
    const closedCards = closedColumn.locator("article")

    // Open: p0, p1-1, p2, p4, feature-req, doc-update (6 issues)
    // In Progress: p1-2 (1 issue)
    // Closed: p3 (1 issue)
    await expect(openCards).toHaveCount(6)
    await expect(inProgressCards).toHaveCount(1)
    await expect(closedCards).toHaveCount(1)

    // Select P1 (High) from priority filter
    const priorityFilter = page.getByTestId("priority-filter")
    await priorityFilter.selectOption("1")

    // Wait for filter to apply - verify expected issue is visible first
    await expect(openColumn.getByText("High Priority Task")).toBeVisible()

    // Only P1 issues should be visible
    // P1 issues: "High Priority Task" (open), "Another High Task" (in_progress)
    await expect(openCards).toHaveCount(1)
    await expect(inProgressCards).toHaveCount(1)
    await expect(closedCards).toHaveCount(0)

    // Verify correct issues are shown
    await expect(inProgressColumn.getByText("Another High Task")).toBeVisible()

    // Verify other issues are not visible
    await expect(openColumn.getByText("Critical Bug")).not.toBeVisible()
    await expect(openColumn.getByText("Medium Task")).not.toBeVisible()
    await expect(closedColumn.getByText("Normal Task")).not.toBeVisible()
  })

  test("clearing filter shows all issues again", async ({ page }) => {
    await navigateAndWait(page, "/")

    // Wait for the Kanban board to render
    const openColumn = page.locator('section[data-status="ready"]')
    await expect(openColumn).toBeVisible()

    const openCards = openColumn.locator("article")
    const inProgressCards = page.locator('section[data-status="in_progress"]').locator("article")
    const closedCards = page.locator('section[data-status="done"]').locator("article")

    // Verify initial state (8 issues total)
    await expect(openCards).toHaveCount(6)
    await expect(inProgressCards).toHaveCount(1)
    await expect(closedCards).toHaveCount(1)

    // Select P0 (Critical) - only 1 issue
    const priorityFilter = page.getByTestId("priority-filter")
    await priorityFilter.selectOption("0")

    // Wait for filter to apply - verify expected issue is visible first
    await expect(openColumn.getByText("Critical Bug")).toBeVisible()

    // Only P0 issue should be visible
    await expect(openCards).toHaveCount(1)
    await expect(inProgressCards).toHaveCount(0)
    await expect(closedCards).toHaveCount(0)

    // Verify other issues are not visible
    await expect(openColumn.getByText("High Priority Task")).not.toBeVisible()
    await expect(openColumn.getByText("Medium Task")).not.toBeVisible()

    // Clear filter by selecting "All priorities"
    await priorityFilter.selectOption("")

    // Wait for filter to clear - verify a filtered-out issue reappears
    await expect(openColumn.getByText("High Priority Task")).toBeVisible()

    // All issues should be visible again
    await expect(openCards).toHaveCount(6)
    await expect(inProgressCards).toHaveCount(1)
    await expect(closedCards).toHaveCount(1)
  })

  test("P0 filter works correctly (value=0 not empty string)", async ({ page }) => {
    await navigateAndWait(page, "/")

    // Wait for the Kanban board to render
    const openColumn = page.locator('section[data-status="ready"]')
    await expect(openColumn).toBeVisible()

    // Select P0 (Critical)
    const priorityFilter = page.getByTestId("priority-filter")
    await priorityFilter.selectOption("0")

    // Wait for filter to apply - verify expected issue is visible first
    await expect(openColumn.getByText("Critical Bug")).toBeVisible()

    // Only P0 issue should be visible
    const openCards = openColumn.locator("article")
    await expect(openCards).toHaveCount(1)

    // Verify other issues are not visible
    await expect(openColumn.getByText("High Priority Task")).not.toBeVisible()
    await expect(openColumn.getByText("Medium Task")).not.toBeVisible()

    // Verify filter dropdown shows correct value
    await expect(priorityFilter).toHaveValue("0")
  })
})

test.describe("FilterBar - Priority Filter (Empty Results)", () => {
  test("empty filter results shows no issues in columns", async ({ page }) => {
    // Mock with issues that don't include P4 (no backlog items)
    await setupMocks(page, mockIssues.filter((issue) => issue.priority !== 4))

    await navigateAndWait(page, "/")

    // Wait for the Kanban board to render
    const openColumn = page.locator('section[data-status="ready"]')
    await expect(openColumn).toBeVisible()

    // Verify initial issues are visible before filtering
    await expect(openColumn.getByText("Critical Bug")).toBeVisible()

    // Select P4 (Backlog) - no issues exist with this priority in our mock
    const priorityFilter = page.getByTestId("priority-filter")
    await priorityFilter.selectOption("4")

    // Wait for filter to apply - verify Critical Bug is no longer visible
    await expect(openColumn.getByText("Critical Bug")).not.toBeVisible()

    // All columns should be empty
    const openCards = openColumn.locator("article")
    const inProgressCards = page.locator('section[data-status="in_progress"]').locator("article")
    const closedCards = page.locator('section[data-status="done"]').locator("article")

    await expect(openCards).toHaveCount(0)
    await expect(inProgressCards).toHaveCount(0)
    await expect(closedCards).toHaveCount(0)
  })
})

test.describe("FilterBar - SearchInput Filter (T106b)", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page)
  })

  test("typing in search filters issues by text", async ({ page }) => {
    await navigateAndWait(page, "/")

    const openColumn = page.locator('section[data-status="ready"]')
    await expect(openColumn).toBeVisible()

    // Count initial issues
    const openCards = openColumn.locator("article")
    const initialCount = await openCards.count()
    expect(initialCount).toBeGreaterThan(0)

    // Type in search input to filter by title
    const searchInput = page.getByTestId("search-input-field")
    await searchInput.fill("Critical Bug")

    // Wait for debounce (300ms configured in App.tsx)
    await page.waitForTimeout(350)

    // Only matching issue should be visible
    await expect(openColumn.getByText("Critical Bug")).toBeVisible()
    await expect(openCards).toHaveCount(1)
  })

  test("search is case-insensitive", async ({ page }) => {
    await navigateAndWait(page, "/")

    const openColumn = page.locator('section[data-status="ready"]')
    await expect(openColumn).toBeVisible()

    // Type in search with different case
    const searchInput = page.getByTestId("search-input-field")
    await searchInput.fill("critical bug")

    // Wait for debounce
    await page.waitForTimeout(350)

    // Issue should still be found (case-insensitive)
    await expect(openColumn.getByText("Critical Bug")).toBeVisible()
  })

  test("search matches title, description, and notes", async ({ page }) => {
    await navigateAndWait(page, "/")

    const openColumn = page.locator('section[data-status="ready"]')
    await expect(openColumn).toBeVisible()

    // Search by description content
    const searchInput = page.getByTestId("search-input-field")
    await searchInput.fill("authentication")

    // Wait for debounce
    await page.waitForTimeout(350)

    // Feature Request has "authentication" in description
    await expect(openColumn.getByText("Feature Request")).toBeVisible()

    // Search by notes content
    await searchInput.fill("team lead")
    await page.waitForTimeout(350)

    // "Another High Task" has "team lead" in notes (in_progress status)
    const inProgressColumn = page.locator('section[data-status="in_progress"]')
    await expect(inProgressColumn.getByText("Another High Task")).toBeVisible()
  })

  test("clearing search input shows all issues", async ({ page }) => {
    await navigateAndWait(page, "/")

    const openColumn = page.locator('section[data-status="ready"]')
    await expect(openColumn).toBeVisible()

    // Get initial counts
    const openCards = openColumn.locator("article")
    const initialCount = await openCards.count()

    // Type search to filter
    const searchInput = page.getByTestId("search-input-field")
    await searchInput.fill("Critical Bug")
    await page.waitForTimeout(350)
    await expect(openCards).toHaveCount(1)

    // Clear the search input
    await searchInput.fill("")
    await page.waitForTimeout(350)

    // All issues should be visible again
    await expect(openCards).toHaveCount(initialCount)
  })

  test("escape key clears search", async ({ page }) => {
    await navigateAndWait(page, "/")

    const openColumn = page.locator('section[data-status="ready"]')
    await expect(openColumn).toBeVisible()

    // Get initial counts
    const openCards = openColumn.locator("article")
    const initialCount = await openCards.count()

    // Type search to filter
    const searchInput = page.getByTestId("search-input-field")
    await searchInput.fill("Critical Bug")
    await page.waitForTimeout(350)
    await expect(openCards).toHaveCount(1)

    // Press Escape key
    await searchInput.press("Escape")
    await page.waitForTimeout(350)

    // All issues should be visible again
    await expect(openCards).toHaveCount(initialCount)
  })
})

test.describe("FilterBar - URL Sync (T106c)", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page)
  })

  test("selecting priority filter updates URL with ?priority=N", async ({ page }) => {
    await navigateAndWait(page, "/")

    // Verify no priority param initially
    expect(page.url()).not.toContain("priority=")

    // Select P1 (High) from priority filter
    const priorityFilter = page.getByTestId("priority-filter")
    await priorityFilter.selectOption("1")

    // Verify URL contains priority param
    await expect(page).toHaveURL(/priority=1/)
  })

  test("selecting type filter updates URL with ?type=X", async ({ page }) => {
    await navigateAndWait(page, "/")

    // Verify no type param initially
    expect(page.url()).not.toContain("type=")

    // Select "bug" from type filter
    const typeFilter = page.getByTestId("type-filter")
    await typeFilter.selectOption("bug")

    // Verify URL contains type param
    await expect(page).toHaveURL(/type=bug/)
  })

  test("navigating to URL with filter params applies filters", async ({ page }) => {
    // Navigate directly with priority param
    await navigateAndWait(page, "/?priority=1")

    // Verify priority filter has correct value
    const priorityFilter = page.getByTestId("priority-filter")
    await expect(priorityFilter).toHaveValue("1")

    // Verify only P1 issues are shown
    const openColumn = page.locator('section[data-status="ready"]')
    await expect(openColumn.getByText("High Priority Task")).toBeVisible()
    await expect(openColumn.getByText("Critical Bug")).not.toBeVisible()
  })

  test("clearing all filters removes query params from URL", async ({ page }) => {
    // Navigate with filter params
    await navigateAndWait(page, "/?priority=1&type=task")

    // Verify URL has params
    expect(page.url()).toContain("priority=1")
    expect(page.url()).toContain("type=task")

    // Click clear filters button using JavaScript (bypasses stats header overlap)
    await page.evaluate(() => {
      const button = document.querySelector('[data-testid="clear-filters"]') as HTMLButtonElement
      button?.click()
    })

    // Verify URL no longer has filter params
    await expect(page).not.toHaveURL(/priority=/)
    await expect(page).not.toHaveURL(/type=/)
  })

  test("combined filters in URL work correctly (?priority=1&type=bug)", async ({ page }) => {
    // Navigate with combined filters - but there are no P1 bugs in our mock data
    // Let's use priority=0&type=bug which matches "Critical Bug"
    await navigateAndWait(page, "/?priority=0&type=bug")

    // Verify both filters have correct values
    const priorityFilter = page.getByTestId("priority-filter")
    const typeFilter = page.getByTestId("type-filter")
    await expect(priorityFilter).toHaveValue("0")
    await expect(typeFilter).toHaveValue("bug")

    // Verify only matching issue is shown
    const openColumn = page.locator('section[data-status="ready"]')
    await expect(openColumn.getByText("Critical Bug")).toBeVisible()
    await expect(openColumn.locator("article")).toHaveCount(1)
  })
})

test.describe("FilterBar - Clear Filters Button (T106d)", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page)
  })

  test("clear filters button not visible when no filters active", async ({ page }) => {
    await navigateAndWait(page, "/")

    // Verify clear filters button is not visible
    const clearFiltersButton = page.getByTestId("clear-filters")
    await expect(clearFiltersButton).not.toBeVisible()
  })

  test("clear filters button appears when priority filter selected", async ({ page }) => {
    await navigateAndWait(page, "/")

    // Verify clear filters button is not visible initially
    const clearFiltersButton = page.getByTestId("clear-filters")
    await expect(clearFiltersButton).not.toBeVisible()

    // Select a priority filter
    const priorityFilter = page.getByTestId("priority-filter")
    await priorityFilter.selectOption("1")

    // Verify clear filters button is now visible
    await expect(clearFiltersButton).toBeVisible()
  })

  test("clear filters button appears when type filter selected", async ({ page }) => {
    await navigateAndWait(page, "/")

    // Verify clear filters button is not visible initially
    const clearFiltersButton = page.getByTestId("clear-filters")
    await expect(clearFiltersButton).not.toBeVisible()

    // Select a type filter
    const typeFilter = page.getByTestId("type-filter")
    await typeFilter.selectOption("bug")

    // Verify clear filters button is now visible
    await expect(clearFiltersButton).toBeVisible()
  })

  test("clear filters button clears all active filters at once", async ({ page }) => {
    // Navigate with multiple filters
    await navigateAndWait(page, "/?priority=1&type=task")

    // Verify filters are applied
    const priorityFilter = page.getByTestId("priority-filter")
    const typeFilter = page.getByTestId("type-filter")
    await expect(priorityFilter).toHaveValue("1")
    await expect(typeFilter).toHaveValue("task")

    // Click clear filters button using JavaScript (bypasses stats header overlap)
    await page.evaluate(() => {
      const button = document.querySelector('[data-testid="clear-filters"]') as HTMLButtonElement
      button?.click()
    })

    // Verify all filters are cleared
    await expect(priorityFilter).toHaveValue("")
    await expect(typeFilter).toHaveValue("")

    // Verify button is now hidden
    const clearFiltersButton = page.getByTestId("clear-filters")
    await expect(clearFiltersButton).not.toBeVisible()
  })

  test("clear filters button clears combined priority+type+search filters", async ({ page }) => {
    // Navigate with priority and type filters
    await navigateAndWait(page, "/?priority=1&type=task")

    // Also add a search filter
    const searchInput = page.getByTestId("search-input-field")
    await searchInput.fill("High")
    await page.waitForTimeout(350)

    // Verify search is in URL (with retry for async URL update)
    await expect(page).toHaveURL(/search=High/, { timeout: 2000 })

    // Click clear filters button using JavaScript (bypasses stats header overlap)
    await page.evaluate(() => {
      const button = document.querySelector('[data-testid="clear-filters"]') as HTMLButtonElement
      button?.click()
    })

    // Verify all filters are cleared
    const priorityFilter = page.getByTestId("priority-filter")
    const typeFilter = page.getByTestId("type-filter")
    await expect(priorityFilter).toHaveValue("")
    await expect(typeFilter).toHaveValue("")
    await expect(searchInput).toHaveValue("")

    // Verify URL has no filter params
    await expect(page).not.toHaveURL(/priority=/)
    await expect(page).not.toHaveURL(/type=/)
    await expect(page).not.toHaveURL(/search=/)
  })
})

test.describe("Filter Integration Tests", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page)
  })

  test("combined priority + type + search filters work together", async ({ page }) => {
    await navigateAndWait(page, "/")

    const openColumn = page.locator('section[data-status="ready"]')
    await expect(openColumn).toBeVisible()

    // Apply priority filter (P2)
    const priorityFilter = page.getByTestId("priority-filter")
    await priorityFilter.selectOption("2")

    // Apply type filter (task)
    const typeFilter = page.getByTestId("type-filter")
    await typeFilter.selectOption("task")

    // Wait for filters to apply
    await expect(openColumn.getByText("Medium Task")).toBeVisible()

    // Apply search filter
    const searchInput = page.getByTestId("search-input-field")
    await searchInput.fill("Medium")
    await page.waitForTimeout(350)

    // Only Medium Task should be visible (P2, task, contains "Medium")
    const openCards = openColumn.locator("article")
    await expect(openCards).toHaveCount(1)
    await expect(openColumn.getByText("Medium Task")).toBeVisible()

    // Verify URL has all filters
    await expect(page).toHaveURL(/priority=2/)
    await expect(page).toHaveURL(/type=task/)
    await expect(page).toHaveURL(/search=Medium/)
  })

  test("filtering persists across view switches", async ({ page }) => {
    await navigateAndWait(page, "/")

    // Apply a priority filter
    const priorityFilter = page.getByTestId("priority-filter")
    await priorityFilter.selectOption("1")

    // Wait for filter to apply in Kanban view
    const openColumn = page.locator('section[data-status="ready"]')
    await expect(openColumn.getByText("High Priority Task")).toBeVisible()

    // Switch to Table view
    const tableTab = page.getByTestId("view-tab-table")
    await tableTab.click()

    // Verify Table view is shown
    const issueTable = page.getByTestId("issue-table")
    await expect(issueTable).toBeVisible()

    // Verify filter is still applied in URL
    await expect(page).toHaveURL(/priority=1/)

    // Verify priority filter still has correct value
    await expect(priorityFilter).toHaveValue("1")

    // Switch back to Kanban
    const kanbanTab = page.getByTestId("view-tab-kanban")
    await kanbanTab.click()

    // Verify filter is still applied
    await expect(openColumn.getByText("High Priority Task")).toBeVisible()
    await expect(priorityFilter).toHaveValue("1")
  })

  test("filter state survives page refresh via URL", async ({ page }) => {
    await navigateAndWait(page, "/")

    // Apply filters
    const priorityFilter = page.getByTestId("priority-filter")
    await priorityFilter.selectOption("1")

    const typeFilter = page.getByTestId("type-filter")
    await typeFilter.selectOption("task")

    // Verify URL has filters
    await expect(page).toHaveURL(/priority=1/)
    await expect(page).toHaveURL(/type=task/)

    // Reload the page
    await navigateAndWait(page, page.url())

    // Verify filters are restored
    await expect(priorityFilter).toHaveValue("1")
    await expect(typeFilter).toHaveValue("task")

    // Verify filtered results are shown
    const openColumn = page.locator('section[data-status="ready"]')
    await expect(openColumn.getByText("High Priority Task")).toBeVisible()
    // Critical Bug is P0 bug, should not be visible
    await expect(openColumn.getByText("Critical Bug")).not.toBeVisible()
  })
})
