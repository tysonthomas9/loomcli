import { test, expect, Page } from "@playwright/test"

/**
 * Mock issues for testing search filtering.
 * Each issue has distinct titles/descriptions for precise filtering tests.
 */
const mockIssues = [
  {
    id: "search-1",
    title: "Authentication Bug",
    description: "Login form validation error",
    status: "open",
    priority: 1,
    issue_type: "bug",
    created_at: "2026-01-24T10:00:00Z",
    updated_at: "2026-01-24T10:00:00Z",
  },
  {
    id: "search-2",
    title: "Dashboard Feature",
    description: "Add user metrics panel",
    status: "open",
    priority: 2,
    issue_type: "feature",
    created_at: "2026-01-24T11:00:00Z",
    updated_at: "2026-01-24T11:00:00Z",
  },
  {
    id: "search-3",
    title: "API Endpoint",
    description: "Authentication middleware refactor",
    status: "in_progress",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-24T12:00:00Z",
    updated_at: "2026-01-24T12:00:00Z",
  },
  {
    id: "search-4",
    title: "Documentation",
    description: "Update README",
    status: "closed",
    priority: 3,
    issue_type: "task",
    created_at: "2026-01-24T13:00:00Z",
    updated_at: "2026-01-24T13:00:00Z",
  },
]

/**
 * Set up API mocks for search tests.
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

/**
 * Count visible issue cards across all columns.
 */
async function countVisibleCards(page: Page): Promise<number> {
  return page.locator("section[data-status] article").count()
}

test.describe("SearchInput filtering", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page)
  })

  test("filters issues by title match", async ({ page }) => {
    await navigateAndWait(page, "/")

    // Verify all 4 cards are visible initially
    await expect(async () => {
      expect(await countVisibleCards(page)).toBe(4)
    }).toPass({ timeout: 5000 })

    // Type "Authentication" in search (matches "Authentication Bug" by title)
    const searchInput = page.getByTestId("search-input-field")
    await searchInput.fill("Authentication")

    // Wait for debounce and verify only matching cards are visible
    await expect(async () => {
      // "Authentication Bug" in open, "API Endpoint" has "Authentication" in description
      expect(await countVisibleCards(page)).toBe(2)
    }).toPass({ timeout: 2000 })

    // Verify the specific card is visible
    const openColumn = page.locator('section[data-status="ready"]')
    await expect(openColumn.getByText("Authentication Bug")).toBeVisible()
  })

  test("filters issues by description match", async ({ page }) => {
    await navigateAndWait(page, "/")

    // Type "metrics" in search (matches "Dashboard Feature" by description)
    const searchInput = page.getByTestId("search-input-field")
    await searchInput.fill("metrics")

    // Wait for debounce and verify only matching card is visible
    await expect(async () => {
      expect(await countVisibleCards(page)).toBe(1)
    }).toPass({ timeout: 2000 })

    // Verify the specific card is visible
    const openColumn = page.locator('section[data-status="ready"]')
    await expect(openColumn.getByText("Dashboard Feature")).toBeVisible()
  })

  test("search is case-insensitive", async ({ page }) => {
    await navigateAndWait(page, "/")

    // Type in uppercase - should still match
    const searchInput = page.getByTestId("search-input-field")
    await searchInput.fill("AUTHENTICATION")

    // Wait for debounce and verify matches
    await expect(async () => {
      // "Authentication Bug" (title) and "API Endpoint" (description has "Authentication")
      expect(await countVisibleCards(page)).toBe(2)
    }).toPass({ timeout: 2000 })

    // Verify both matching cards are visible
    await expect(page.getByText("Authentication Bug")).toBeVisible()
    await expect(page.getByText("API Endpoint")).toBeVisible()
  })

  test("partial match filters correctly", async ({ page }) => {
    await navigateAndWait(page, "/")

    // Type partial term "Auth" - should match both "Authentication Bug" and "API Endpoint"
    const searchInput = page.getByTestId("search-input-field")
    await searchInput.fill("Auth")

    // Wait for debounce and verify partial matches
    await expect(async () => {
      expect(await countVisibleCards(page)).toBe(2)
    }).toPass({ timeout: 2000 })

    // Verify both cards are visible
    await expect(page.getByText("Authentication Bug")).toBeVisible()
    await expect(page.getByText("API Endpoint")).toBeVisible()
  })

  test("no matches shows empty columns", async ({ page }) => {
    await navigateAndWait(page, "/")

    // Type a term that doesn't match anything
    const searchInput = page.getByTestId("search-input-field")
    await searchInput.fill("xyznonexistent")

    // Wait for debounce and verify no cards visible
    await expect(async () => {
      expect(await countVisibleCards(page)).toBe(0)
    }).toPass({ timeout: 2000 })

    // Verify all columns show 0 count badges
    const openColumn = page.locator('section[data-status="ready"]')
    await expect(openColumn.getByLabel("0 issues")).toBeVisible()
  })

  test("clearing search shows all issues", async ({ page }) => {
    await navigateAndWait(page, "/")

    // First, filter to show fewer cards
    const searchInput = page.getByTestId("search-input-field")
    await searchInput.fill("metrics")

    // Wait for filter to apply
    await expect(async () => {
      expect(await countVisibleCards(page)).toBe(1)
    }).toPass({ timeout: 2000 })

    // Click the clear button
    const clearButton = page.getByTestId("search-input-clear")
    await clearButton.click()

    // Verify all cards are visible again
    await expect(async () => {
      expect(await countVisibleCards(page)).toBe(4)
    }).toPass({ timeout: 2000 })

    // Verify search input is cleared
    await expect(searchInput).toHaveValue("")
  })

  test("Escape key clears search", async ({ page }) => {
    await navigateAndWait(page, "/")

    // First, filter to show fewer cards
    const searchInput = page.getByTestId("search-input-field")
    await searchInput.fill("metrics")

    // Wait for filter to apply
    await expect(async () => {
      expect(await countVisibleCards(page)).toBe(1)
    }).toPass({ timeout: 2000 })

    // Press Escape while input is focused
    await searchInput.press("Escape")

    // Verify all cards are visible again
    await expect(async () => {
      expect(await countVisibleCards(page)).toBe(4)
    }).toPass({ timeout: 2000 })

    // Verify search input is cleared
    await expect(searchInput).toHaveValue("")
  })
})
