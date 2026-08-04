import { test, expect, Page } from "@playwright/test"

/**
 * Mock issues for testing FilterBar clear button.
 * Uses varied priorities and types to verify filtering behavior.
 */
const mockIssues = [
  {
    id: "issue-1",
    title: "Bug in login",
    status: "open",
    priority: 0,
    issue_type: "bug",
    created_at: "2026-01-24T10:00:00Z",
    updated_at: "2026-01-24T10:00:00Z",
  },
  {
    id: "issue-2",
    title: "Add feature X",
    status: "open",
    priority: 1,
    issue_type: "feature",
    created_at: "2026-01-24T11:00:00Z",
    updated_at: "2026-01-24T11:00:00Z",
  },
  {
    id: "issue-3",
    title: "Fix database",
    status: "in_progress",
    priority: 2,
    issue_type: "bug",
    created_at: "2026-01-24T12:00:00Z",
    updated_at: "2026-01-24T12:00:00Z",
  },
  {
    id: "issue-4",
    title: "Update docs",
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-24T13:00:00Z",
    updated_at: "2026-01-24T13:00:00Z",
  },
]

/**
 * Set up API mocks for FilterBar tests.
 */
async function setupMocks(page: Page) {
  const workspace = {
    id: "default",
    name: "Default",
    path: "/tmp/default",
    repos: [],
    groups: [],
    agents: [],
    workspaces: [
      {
        id: "default",
        name: "Default",
        path: "/tmp/default",
        active: true,
        repo_count: 0,
        is_default: true,
      },
    ],
    workspace_order: ["default"],
    default_workspace: "Default",
  }

  await page.route("**/*", async (route) => {
    const pathname = new URL(route.request().url()).pathname
    if (pathname === "/api/config") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ mode: "open" }),
      })
    } else if (
      pathname === "/api/workspaces/active" ||
      pathname === "/api/workspaces/default"
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: workspace }),
      })
    } else if (pathname === "/api/workspaces/default/issues") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: mockIssues }),
      })
    } else if (pathname === "/api/workspaces/default/stats") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          total_issues: mockIssues.length,
          open_issues: mockIssues.filter((i) => i.status === "open").length,
          in_progress_issues: mockIssues.filter((i) => i.status === "in_progress").length,
          closed_issues: 0,
          blocked_issues: 0,
          deferred_issues: 0,
          ready_issues: mockIssues.filter((i) => i.status === "open").length,
          tombstone_issues: 0,
          pinned_issues: 0,
          epics_eligible_for_closure: 0,
          average_lead_time_hours: 0,
        }),
      })
    } else if (
      pathname === "/api/workspaces/default/blocked" ||
      pathname === "/api/workspaces/default/terminal/tabs"
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: [] }),
      })
    } else if (pathname === "/api/workspaces/default/terminal/state") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ active_tab: "" }),
      })
    } else if (pathname.startsWith("/api/") && pathname.includes("/events")) {
      await route.abort()
    } else {
      await route.continue()
    }
  })
}

/**
 * Navigate to a page and wait for API response.
 */
async function navigateAndWait(page: Page, path: string) {
  const appPath = path === "/" ? "/ws/default/kanban" : path
  const [response] = await Promise.all([
    page.waitForResponse(
      (res) =>
        new URL(res.url()).pathname === "/api/workspaces/default/issues" &&
        res.status() === 200
    ),
    page.goto(appPath),
  ])
  expect(response.ok()).toBe(true)
}

test.describe("FilterBar", () => {
  test("clear filters button resets all filters and shows all issues", async ({ page }) => {
    await setupMocks(page)
    await navigateAndWait(page, "/")

    // Step 1: Verify initial state - all 4 issues visible
    const openColumn = page.locator('section[data-status="ready"]')
    const inProgressColumn = page.locator('section[data-status="in_progress"]')

    await expect(openColumn).toBeVisible()
    await expect(inProgressColumn).toBeVisible()

    // Verify all issues are visible (3 open + 1 in_progress)
    await expect(openColumn.locator("article")).toHaveCount(3)
    await expect(inProgressColumn.locator("article")).toHaveCount(1)

    // Verify no filter params in URL initially
    expect(page.url()).not.toContain("priority=")
    expect(page.url()).not.toContain("type=")

    // Clear button is always rendered in the current toolbar.
    const clearButton = page.getByTestId("clear-filters")

    // Step 2: Apply priority filter (P2)
    const priorityFilter = page.getByTestId("priority-filter")
    await priorityFilter.selectOption("2")

    // Verify URL contains priority param
    await expect(async () => {
      expect(page.url()).toContain("priority=2")
    }).toPass({ timeout: 2000 })

    // Verify only P2 issues visible (2 issues: issue-3, issue-4)
    await expect(openColumn.locator("article")).toHaveCount(1) // "Update docs"
    await expect(inProgressColumn.locator("article")).toHaveCount(1) // "Fix database"

    // Step 3: Apply type filter (bug)
    const typeFilter = page.getByTestId("type-filter")
    await typeFilter.selectOption("bug")

    // Verify URL contains both priority and type params
    await expect(async () => {
      expect(page.url()).toContain("priority=2")
      expect(page.url()).toContain("type=bug")
    }).toPass({ timeout: 2000 })

    // Verify only P2 bugs visible (1 issue: "Fix database")
    await expect(openColumn.locator("article")).toHaveCount(0)
    await expect(inProgressColumn.locator("article")).toHaveCount(1)
    await expect(inProgressColumn.getByText("Fix database")).toBeVisible()

    // Step 4: Verify clear button is now visible
    await expect(clearButton).toBeVisible()

    // Step 5: Click clear button
    await clearButton.click()

    // Step 6: Verify all filters are reset

    // 6a: URL has no filter params
    await expect(async () => {
      expect(page.url()).not.toContain("priority=")
      expect(page.url()).not.toContain("type=")
    }).toPass({ timeout: 2000 })

    // 6b: All 4 issues visible again
    await expect(openColumn.locator("article")).toHaveCount(3)
    await expect(inProgressColumn.locator("article")).toHaveCount(1)

    // 6c: Dropdowns show "All" options
    await expect(priorityFilter).toHaveValue("")
    await expect(typeFilter).toHaveValue("")

    // 6d: Clear button remains available in the toolbar.
    await expect(clearButton).toBeVisible()
  })
})
