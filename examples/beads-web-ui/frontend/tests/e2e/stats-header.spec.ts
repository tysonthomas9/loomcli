import { test, expect, Page } from "@playwright/test"

/**
 * Mock statistics data for StatsHeader tests.
 * Uses the Statistics type structure from types/statistics.ts
 */
const mockStats = {
  total_issues: 25,
  open_issues: 10,
  in_progress_issues: 5,
  closed_issues: 8,
  blocked_issues: 3,
  deferred_issues: 1,
  ready_issues: 7,
  tombstone_issues: 0,
  pinned_issues: 2,
  epics_eligible_for_closure: 1,
  average_lead_time_hours: 48.5,
}

/**
 * Minimal mock issues for /api/ready endpoint.
 */
const mockIssues = [
  {
    id: "issue-1",
    title: "Test Issue",
    description: "A test issue",
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-24T10:00:00Z",
    updated_at: "2026-01-24T10:00:00Z",
  },
]

/**
 * Set up API mocks for StatsHeader tests.
 * @param page - Playwright page object
 * @param statsOverride - Optional partial stats to override defaults
 * @param options - Additional mock options
 */
async function setupMocks(
  page: Page,
  statsOverride?: Partial<typeof mockStats>,
  options?: {
    statsDelay?: number
    statsFail?: boolean
    statsStatus?: number
  }
) {
  // Mock /api/ready endpoint
  await page.route("**/api/ready", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: mockIssues }),
    })
  })

  // Mock /api/blocked endpoint
  await page.route("**/api/blocked", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: [] }),
    })
  })

  // Mock /api/stats endpoint
  await page.route("**/api/stats", async (route) => {
    // Optional delay for loading state tests
    if (options?.statsDelay) {
      await new Promise((resolve) => setTimeout(resolve, options.statsDelay))
    }

    // Optional failure for error state tests
    if (options?.statsFail) {
      await route.fulfill({
        status: options?.statsStatus ?? 500,
        contentType: "application/json",
        body: JSON.stringify({ success: false, error: "Internal server error" }),
      })
      return
    }

    // Success response with merged stats
    const stats = { ...mockStats, ...statsOverride }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: stats }),
    })
  })
}

/**
 * Navigate to a page and wait for /api/ready response.
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

test.describe("StatsHeader Display", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page)
  })

  test("StatsHeader renders in layout", async ({ page }) => {
    await navigateAndWait(page, "/")

    // Verify StatsHeader container is visible
    const statsHeader = page.getByTestId("stats-header")
    await expect(statsHeader).toBeVisible()
  })

  test("shows all four stat badges with correct labels", async ({ page }) => {
    await navigateAndWait(page, "/")

    const statsHeader = page.getByTestId("stats-header")
    await expect(statsHeader).toBeVisible()

    // Verify all 4 badges are visible with their labels
    await expect(statsHeader.getByText("Open")).toBeVisible()
    await expect(statsHeader.getByText("In Progress")).toBeVisible()
    await expect(statsHeader.getByText("Ready")).toBeVisible()
    await expect(statsHeader.getByText("Closed")).toBeVisible()
  })

  test("displays correct count values", async ({ page }) => {
    await navigateAndWait(page, "/")

    const statsHeader = page.getByTestId("stats-header")
    await expect(statsHeader).toBeVisible()

    // Verify each badge displays its corresponding count
    // mockStats: open=10, in_progress=5, ready=7, closed=8
    await expect(statsHeader.getByText("10")).toBeVisible()
    await expect(statsHeader.getByText("5")).toBeVisible()
    await expect(statsHeader.getByText("7")).toBeVisible()
    await expect(statsHeader.getByText("8")).toBeVisible()
  })
})

test.describe("StatsHeader Counts", () => {
  test("counts match actual issue statistics", async ({ page }) => {
    const customStats = {
      open_issues: 15,
      in_progress_issues: 3,
      ready_issues: 12,
      closed_issues: 20,
    }
    await setupMocks(page, customStats)
    await navigateAndWait(page, "/")

    const statsHeader = page.getByTestId("stats-header")
    await expect(statsHeader).toBeVisible()

    // Verify Open badge shows open_issues value
    await expect(statsHeader.getByText("15")).toBeVisible()
    // Verify In Progress badge shows in_progress_issues value
    await expect(statsHeader.getByText("3")).toBeVisible()
    // Verify Ready badge shows ready_issues value
    await expect(statsHeader.getByText("12")).toBeVisible()
    // Verify Closed badge shows closed_issues value
    await expect(statsHeader.getByText("20")).toBeVisible()
  })

  test("handles large numbers correctly", async ({ page }) => {
    const largeStats = {
      open_issues: 1500,
      in_progress_issues: 250,
      ready_issues: 999,
      closed_issues: 12000,
    }
    await setupMocks(page, largeStats)
    await navigateAndWait(page, "/")

    const statsHeader = page.getByTestId("stats-header")
    await expect(statsHeader).toBeVisible()

    // Verify large numbers display correctly
    await expect(statsHeader.getByText("1500")).toBeVisible()
    await expect(statsHeader.getByText("250")).toBeVisible()
    await expect(statsHeader.getByText("999")).toBeVisible()
    await expect(statsHeader.getByText("12000")).toBeVisible()
  })
})

test.describe("StatsHeader Real-time Updates", () => {
  test("counts update when navigating to new page state", async ({ page }) => {
    // This test verifies that stats refresh when the page is reloaded
    // (polling is 30s which is too slow for e2e tests)

    // Set up initial stats with unique values to avoid ambiguity
    await setupMocks(page, {
      open_issues: 11,
      in_progress_issues: 22,
      ready_issues: 33,
      closed_issues: 44,
    })
    await navigateAndWait(page, "/")

    const statsHeader = page.getByTestId("stats-header")
    await expect(statsHeader).toBeVisible()

    // Verify initial counts
    await expect(statsHeader.getByText("11")).toBeVisible()
    await expect(statsHeader.getByText("22")).toBeVisible()

    // Update mock to return different unique values
    await page.route("**/api/stats", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          success: true,
          data: {
            ...mockStats,
            open_issues: 55,
            in_progress_issues: 66,
            ready_issues: 77,
            closed_issues: 88,
          },
        }),
      })
    })

    // Refresh the page to trigger new fetch
    await page.reload()
    await expect(page.getByTestId("stats-header")).toBeVisible()

    // Verify new counts are displayed
    await expect(statsHeader.getByText("55")).toBeVisible()
    await expect(statsHeader.getByText("66")).toBeVisible()
  })
})

test.describe("StatsHeader Zero State", () => {
  test("handles zero counts gracefully", async ({ page }) => {
    const zeroStats = {
      open_issues: 0,
      in_progress_issues: 0,
      ready_issues: 0,
      closed_issues: 0,
    }
    await setupMocks(page, zeroStats)
    await navigateAndWait(page, "/")

    const statsHeader = page.getByTestId("stats-header")
    await expect(statsHeader).toBeVisible()

    // Verify all badges display "0" (should have 4 zeros)
    const zeros = statsHeader.getByText("0")
    await expect(zeros).toHaveCount(4)

    // Verify component remains visible with labels
    await expect(statsHeader.getByText("Open")).toBeVisible()
    await expect(statsHeader.getByText("In Progress")).toBeVisible()
    await expect(statsHeader.getByText("Ready")).toBeVisible()
    await expect(statsHeader.getByText("Closed")).toBeVisible()
  })

  test("handles mixed zero and non-zero counts", async ({ page }) => {
    const mixedStats = {
      open_issues: 5,
      in_progress_issues: 0,
      ready_issues: 3,
      closed_issues: 0,
    }
    await setupMocks(page, mixedStats)
    await navigateAndWait(page, "/")

    const statsHeader = page.getByTestId("stats-header")
    await expect(statsHeader).toBeVisible()

    // Verify non-zero values display correctly
    await expect(statsHeader.getByText("5")).toBeVisible()
    await expect(statsHeader.getByText("3")).toBeVisible()

    // Verify zero-value badges still display (should have 2 zeros)
    const zeros = statsHeader.getByText("0")
    await expect(zeros).toHaveCount(2)
  })
})

test.describe("StatsHeader Loading State", () => {
  test("shows loading skeleton while fetching", async ({ page }) => {
    // Use a longer delay to observe loading state
    await setupMocks(page, undefined, { statsDelay: 1000 })

    // Navigate without waiting for stats to complete
    await page.goto("/", { waitUntil: "domcontentloaded" })

    // Verify loading state is shown
    const loadingState = page.getByTestId("stats-header-loading")
    await expect(loadingState).toBeVisible()

    // Wait for stats to load
    const statsHeader = page.getByTestId("stats-header")
    await expect(statsHeader).toBeVisible({ timeout: 5000 })

    // Loading state should be gone
    await expect(loadingState).not.toBeVisible()
  })

  test("skeleton has correct structure with 4 placeholders", async ({ page }) => {
    // Use a longer delay to observe skeleton
    await setupMocks(page, undefined, { statsDelay: 2000 })

    // Navigate without waiting for stats
    await page.goto("/", { waitUntil: "domcontentloaded" })

    // Verify loading state skeleton structure
    const loadingState = page.getByTestId("stats-header-loading")
    await expect(loadingState).toBeVisible()

    // Verify skeleton has child elements (4 skeleton placeholders)
    const skeletons = loadingState.locator(":scope > *")
    await expect(skeletons).toHaveCount(4)
  })
})

test.describe("StatsHeader Error State", () => {
  test("shows error state when API fails", async ({ page }) => {
    await setupMocks(page, undefined, { statsFail: true })
    await navigateAndWait(page, "/")

    // Verify error state is shown
    const errorState = page.getByTestId("stats-header-error")
    await expect(errorState).toBeVisible()

    // Verify error message is displayed
    await expect(errorState.getByText("Stats unavailable")).toBeVisible()
  })

  test("retry button triggers new fetch", async ({ page }) => {
    let requestCount = 0

    // Custom mock setup that fails first stats request, succeeds second
    await page.route("**/api/ready", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: mockIssues }),
      })
    })

    // Mock blocked endpoint (must be present to prevent app errors)
    await page.route("**/api/blocked", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: [] }),
      })
    })

    await page.route("**/api/stats", async (route) => {
      requestCount++
      if (requestCount === 1) {
        // First request fails
        await route.fulfill({
          status: 500,
          contentType: "application/json",
          body: JSON.stringify({ success: false, error: "Server error" }),
        })
      } else {
        // Subsequent requests succeed
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: mockStats }),
        })
      }
    })

    await navigateAndWait(page, "/")

    // Verify error state is shown initially
    const errorState = page.getByTestId("stats-header-error")
    await expect(errorState).toBeVisible()

    // Click the retry button (the entire error container is a button in this component)
    await errorState.click()

    // Verify stats now display correctly
    const statsHeader = page.getByTestId("stats-header")
    await expect(statsHeader).toBeVisible({ timeout: 5000 })

    // Verify actual values are shown
    await expect(statsHeader.getByText("10")).toBeVisible()
    await expect(statsHeader.getByText("Open")).toBeVisible()
  })

  test("maintains stale data visibility when component has existing data", async ({
    page,
  }) => {
    // First, load successfully
    await setupMocks(page, { open_issues: 25 })
    await navigateAndWait(page, "/")

    const statsHeader = page.getByTestId("stats-header")
    await expect(statsHeader).toBeVisible()
    await expect(statsHeader.getByText("25")).toBeVisible()

    // Now update mock to fail on subsequent requests
    await page.route("**/api/stats", async (route) => {
      await route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({ success: false, error: "Server error" }),
      })
    })

    // Wait for a potential polling cycle (hook keeps stale data on error)
    // The component shows error only if stats is null, so with stale data
    // it should keep showing the stats, not error state
    await page.waitForTimeout(500)

    // Original data should still be visible (component keeps stale data)
    await expect(statsHeader.getByText("25")).toBeVisible()

    // Error state should NOT be shown since we have stale data
    const errorState = page.getByTestId("stats-header-error")
    await expect(errorState).not.toBeVisible()
  })
})

test.describe("StatsHeader Accessibility", () => {
  test("error state has aria-label for retry button", async ({ page }) => {
    await setupMocks(page, undefined, { statsFail: true })
    await navigateAndWait(page, "/")

    const errorState = page.getByTestId("stats-header-error")
    await expect(errorState).toBeVisible()

    // Verify retry button has proper aria-label
    const retryButton = errorState.getByRole("button", {
      name: /retry loading statistics/i,
    })
    await expect(retryButton).toBeVisible()
  })

  test("stat badges have title attributes for tooltips", async ({ page }) => {
    await setupMocks(page, { open_issues: 10 })
    await navigateAndWait(page, "/")

    const statsHeader = page.getByTestId("stats-header")
    await expect(statsHeader).toBeVisible()

    // Verify badges have title attributes (e.g., "10 Open")
    const openBadge = statsHeader.locator('[title="10 Open"]')
    await expect(openBadge).toBeVisible()
  })
})

test.describe("StatsHeader Integration", () => {
  test("StatsHeader coexists with other header elements", async ({ page }) => {
    await setupMocks(page)
    await navigateAndWait(page, "/")

    const statsHeader = page.getByTestId("stats-header")
    await expect(statsHeader).toBeVisible()

    // Verify Kanban board also renders (StatsHeader doesn't break the page)
    const openColumn = page.locator('section[data-status="ready"]')
    await expect(openColumn).toBeVisible()

    // Verify filter bar renders
    const priorityFilter = page.getByTestId("priority-filter")
    await expect(priorityFilter).toBeVisible()
  })

  test("filters do not affect StatsHeader counts", async ({ page }) => {
    await setupMocks(page, {
      open_issues: 50,
      in_progress_issues: 25,
    })
    await navigateAndWait(page, "/")

    const statsHeader = page.getByTestId("stats-header")
    await expect(statsHeader.getByText("50")).toBeVisible()
    await expect(statsHeader.getByText("25")).toBeVisible()

    // Apply a priority filter
    const priorityFilter = page.getByTestId("priority-filter")
    await priorityFilter.selectOption("1")

    // Stats should remain unchanged (they show global counts, not filtered)
    await expect(statsHeader.getByText("50")).toBeVisible()
    await expect(statsHeader.getByText("25")).toBeVisible()
  })
})
