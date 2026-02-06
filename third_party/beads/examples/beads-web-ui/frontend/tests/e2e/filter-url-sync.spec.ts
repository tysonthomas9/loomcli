import { test, expect, Page } from "@playwright/test"

/**
 * Mock issues for testing filter URL synchronization.
 * Contains issues with varied priorities and types to verify filtering.
 */
const mockIssues = [
  {
    id: "p0-issue",
    title: "Critical Bug",
    status: "open",
    priority: 0,
    issue_type: "bug",
    created_at: "2026-01-24T10:00:00Z",
    updated_at: "2026-01-24T10:00:00Z",
  },
  {
    id: "p2-issue",
    title: "Normal Task",
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-24T11:00:00Z",
    updated_at: "2026-01-24T11:00:00Z",
  },
  {
    id: "p4-issue",
    title: "Backlog Feature",
    status: "in_progress",
    priority: 4,
    issue_type: "feature",
    created_at: "2026-01-24T12:00:00Z",
    updated_at: "2026-01-24T12:00:00Z",
  },
]

/**
 * Set up API mocks for filter URL sync tests.
 */
async function setupMocks(page: Page) {
  await page.route("**/api/ready", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: mockIssues }),
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

test.describe("Filter URL Synchronization", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page)
  })

  test("priority filter updates URL params", async ({ page }) => {
    await navigateAndWait(page, "/")

    // Verify no filter params initially
    expect(page.url()).not.toContain("priority=")

    // Select P2 (Medium) priority
    const priorityFilter = page.getByTestId("priority-filter")
    await priorityFilter.selectOption("2")

    // Verify URL contains priority param
    expect(page.url()).toContain("priority=2")

    // Verify only P2 issues visible (1 issue)
    const allCards = page.locator("article")
    await expect(allCards).toHaveCount(1)
    await expect(page.getByText("Normal Task")).toBeVisible()
  })

  test("type filter updates URL params", async ({ page }) => {
    await navigateAndWait(page, "/")

    // Verify no filter params initially
    expect(page.url()).not.toContain("type=")

    // Select "bug" type
    const typeFilter = page.getByTestId("type-filter")
    await typeFilter.selectOption("bug")

    // Verify URL contains type param
    expect(page.url()).toContain("type=bug")

    // Verify only bug issues visible (1 issue)
    const allCards = page.locator("article")
    await expect(allCards).toHaveCount(1)
    await expect(page.getByText("Critical Bug")).toBeVisible()
  })

  test("search input updates URL params with debounce", async ({ page }) => {
    await navigateAndWait(page, "/")

    // Verify no filter params initially
    expect(page.url()).not.toContain("search=")

    // Type in search input
    const searchInput = page.getByTestId("search-input-field")
    await searchInput.fill("Critical")

    // Wait for debounce (300ms) to complete before checking URL
    await page.waitForTimeout(350)

    // Verify URL contains search param (use waitForURL for reliability)
    await page.waitForURL(/search=Critical/, { timeout: 5000 })
    expect(page.url()).toContain("search=Critical")
  })

  test("navigating to URL with priority param applies filter", async ({
    page,
  }) => {
    // Navigate directly with priority param
    await navigateAndWait(page, "/?priority=0")

    // Verify priority dropdown shows P0
    const priorityFilter = page.getByTestId("priority-filter")
    await expect(priorityFilter).toHaveValue("0")

    // Verify only P0 issues visible
    const allCards = page.locator("article")
    await expect(allCards).toHaveCount(1)
    await expect(page.getByText("Critical Bug")).toBeVisible()
    await expect(page.getByText("Normal Task")).not.toBeVisible()
  })

  test("navigating to URL with type param applies filter", async ({ page }) => {
    // Navigate directly with type param
    await navigateAndWait(page, "/?type=feature")

    // Verify type dropdown shows feature
    const typeFilter = page.getByTestId("type-filter")
    await expect(typeFilter).toHaveValue("feature")

    // Verify only feature issues visible
    const allCards = page.locator("article")
    await expect(allCards).toHaveCount(1)
    await expect(page.getByText("Backlog Feature")).toBeVisible()
    await expect(page.getByText("Critical Bug")).not.toBeVisible()
  })

  test("navigating to URL with search param applies filter", async ({
    page,
  }) => {
    // Navigate directly with search param
    await navigateAndWait(page, "/?search=Task")

    // Verify search input contains the search term
    const searchInput = page.getByTestId("search-input-field")
    await expect(searchInput).toHaveValue("Task")

    // Verify only matching issues visible (issues with "Task" in title)
    const allCards = page.locator("article")
    await expect(allCards).toHaveCount(1)
    await expect(page.getByText("Normal Task")).toBeVisible()
  })

  test("navigating to URL with multiple filter params applies all filters", async ({
    page,
  }) => {
    // Navigate with both priority and type params
    await navigateAndWait(page, "/?priority=0&type=bug")

    // Verify priority dropdown shows P0
    const priorityFilter = page.getByTestId("priority-filter")
    await expect(priorityFilter).toHaveValue("0")

    // Verify type dropdown shows bug
    const typeFilter = page.getByTestId("type-filter")
    await expect(typeFilter).toHaveValue("bug")

    // Verify only P0 bug issues visible
    const allCards = page.locator("article")
    await expect(allCards).toHaveCount(1)
    await expect(page.getByText("Critical Bug")).toBeVisible()
  })

  test("clear filters button removes URL params", async ({ page }) => {
    // Navigate with priority and type filter params
    await navigateAndWait(page, "/?priority=2&type=task")

    // Verify URL has filter params
    expect(page.url()).toContain("priority=2")
    expect(page.url()).toContain("type=task")

    // Click clear filters button
    const clearButton = page.getByTestId("clear-filters")
    await expect(clearButton).toBeVisible()
    await clearButton.click()

    // Verify URL no longer has filter params
    expect(page.url()).not.toContain("priority=")
    expect(page.url()).not.toContain("type=")

    // Verify priority and type dropdowns reset to "All"
    const priorityFilter = page.getByTestId("priority-filter")
    const typeFilter = page.getByTestId("type-filter")
    await expect(priorityFilter).toHaveValue("")
    await expect(typeFilter).toHaveValue("")

    // Verify all issues visible again
    const allCards = page.locator("article")
    await expect(allCards).toHaveCount(3)
  })

  test("invalid URL params are ignored gracefully", async ({ page }) => {
    // Navigate with invalid priority value (out of range)
    await navigateAndWait(page, "/?priority=99")

    // Verify priority dropdown shows "All priorities" (empty value)
    const priorityFilter = page.getByTestId("priority-filter")
    await expect(priorityFilter).toHaveValue("")

    // Verify all issues are visible (no filter applied)
    const allCards = page.locator("article")
    await expect(allCards).toHaveCount(3)
  })
})

test.describe("Filter URL Synchronization - Edge Cases", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page)
  })

  test("empty priority param is ignored", async ({ page }) => {
    await navigateAndWait(page, "/?priority=")

    // Verify priority dropdown shows "All priorities"
    const priorityFilter = page.getByTestId("priority-filter")
    await expect(priorityFilter).toHaveValue("")

    // Verify all issues visible
    const allCards = page.locator("article")
    await expect(allCards).toHaveCount(3)
  })

  test("non-numeric priority param is ignored", async ({ page }) => {
    await navigateAndWait(page, "/?priority=abc")

    // Verify priority dropdown shows "All priorities"
    const priorityFilter = page.getByTestId("priority-filter")
    await expect(priorityFilter).toHaveValue("")

    // Verify all issues visible
    const allCards = page.locator("article")
    await expect(allCards).toHaveCount(3)
  })

  test("multiple filter params work with different order", async ({ page }) => {
    // Navigate with params in different order (type before priority)
    await navigateAndWait(page, "/?type=bug&priority=0")

    // Verify both filters applied correctly
    const priorityFilter = page.getByTestId("priority-filter")
    const typeFilter = page.getByTestId("type-filter")
    await expect(priorityFilter).toHaveValue("0")
    await expect(typeFilter).toHaveValue("bug")

    // Verify filtered results
    const allCards = page.locator("article")
    await expect(allCards).toHaveCount(1)
    await expect(page.getByText("Critical Bug")).toBeVisible()
  })

  test("URL encoded search param is decoded correctly", async ({ page }) => {
    // Navigate with URL-encoded search (space = %20)
    await navigateAndWait(page, "/?search=Critical%20Bug")

    // Verify search input shows decoded value
    const searchInput = page.getByTestId("search-input-field")
    await expect(searchInput).toHaveValue("Critical Bug")
  })

  test("filter state preserved through page refresh", async ({ page }) => {
    await navigateAndWait(page, "/")

    // Apply a filter
    const priorityFilter = page.getByTestId("priority-filter")
    await priorityFilter.selectOption("2")

    // Verify URL updated
    expect(page.url()).toContain("priority=2")

    // Reload the page
    await page.reload()

    // Wait for API response after reload
    await page.waitForResponse((res) => res.url().includes("/api/ready"))

    // Verify filter still applied
    await expect(priorityFilter).toHaveValue("2")
    const allCards = page.locator("article")
    await expect(allCards).toHaveCount(1)
  })
})
