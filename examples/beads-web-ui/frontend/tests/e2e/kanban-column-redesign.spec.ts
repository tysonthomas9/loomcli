import { test, expect } from "@playwright/test"

/**
 * E2E tests for the Kanban Column Redesign (6-column layout).
 *
 * Validates: column order, naming, icons, issue distribution, epic filtering,
 * column backgrounds, status badges, header accents, and default group-by-epic view.
 *
 * Avoids duplicating tests already in backlog-column.spec.ts (individual blocked/deferred
 * routing, drag-drop). Focuses on integration-level verification of the redesign.
 */

const NOW = "2026-01-31T10:00:00Z"

function makeIssue(overrides: Record<string, unknown>) {
  return {
    id: "test-issue",
    title: "Test Issue",
    status: "open",
    priority: 2,
    created_at: NOW,
    updated_at: NOW,
    ...overrides,
  }
}

/**
 * Set up common API route mocks.
 */
async function setupMocks(
  page: import("@playwright/test").Page,
  options: {
    issues: Record<string, unknown>[]
    blockedIssues?: Record<string, unknown>[]
  }
) {
  await page.route("**/api/ready", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: options.issues }),
    })
  })

  await page.route("**/api/blocked", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: options.blockedIssues ?? [],
      }),
    })
  })

  await page.route("**/api/stats", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: { open: 0, closed: 0, in_progress: 0, blocked: 0, total: 0 },
      }),
    })
  })

  await page.route("**/api/events", async (route) => {
    await route.abort()
  })
}

/** Navigate to flat kanban view (groupBy=none) and wait for /api/ready */
async function navigateToKanban(page: import("@playwright/test").Page) {
  const [response] = await Promise.all([
    page.waitForResponse(
      (res) => res.url().includes("/api/ready") && res.status() === 200
    ),
    page.goto("/?groupBy=none"),
  ])
  expect(response.ok()).toBe(true)
}

test.describe("Column Redesign: 6-column layout", () => {
  test("renders 6 columns in order: Backlog, Open, Blocked, In Progress, Needs Review, Done", async ({
    page,
  }) => {
    const issues = [
      makeIssue({ id: "open-1", title: "Open Issue", status: "open" }),
      makeIssue({
        id: "blocked-1",
        title: "Blocked Issue",
        status: "blocked",
      }),
      makeIssue({ id: "deferred-1", title: "Deferred Issue", status: "deferred" }),
      makeIssue({ id: "ip-1", title: "IP Issue", status: "in_progress" }),
      makeIssue({
        id: "review-1",
        title: "[Need Review] Review Issue",
        status: "open",
      }),
      makeIssue({ id: "done-1", title: "Done Issue", status: "closed" }),
    ]

    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const columns = page.locator("section[data-status]")
    const statuses = await columns.evaluateAll((els) =>
      els.map((el) => el.getAttribute("data-status"))
    )
    expect(statuses).toEqual([
      "backlog",
      "ready",
      "blocked",
      "in_progress",
      "review",
      "done",
    ])
  })

  test("column headers display correct labels", async ({ page }) => {
    // Need at least one issue to ensure columns render
    const issues = [
      makeIssue({ id: "r-1", title: "Some Task", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const backlogColumn = page.locator('section[data-status="backlog"]')
    const openColumn = page.locator('section[data-status="ready"]')
    const blockedColumn = page.locator('section[data-status="blocked"]')
    const inProgressColumn = page.locator('section[data-status="in_progress"]')
    const reviewColumn = page.locator('section[data-status="review"]')
    const doneColumn = page.locator('section[data-status="done"]')

    await expect(backlogColumn.locator("h2")).toHaveText("Backlog")
    await expect(openColumn.locator("h2")).toHaveText("Open")
    await expect(blockedColumn.locator("h2")).toHaveText("Blocked")
    await expect(inProgressColumn.locator("h2")).toHaveText("In Progress")
    await expect(reviewColumn.locator("h2")).toHaveText("Needs Review")
    await expect(doneColumn.locator("h2")).toHaveText("Done")
  })

  test("no column is labeled Pending", async ({ page }) => {
    const issues = [
      makeIssue({ id: "r-1", title: "Some Task", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    // Verify no h2 with text "Pending" exists in any column header
    const pendingHeaders = page.locator("section[data-status] h2", {
      hasText: /^Pending$/,
    })
    await expect(pendingHeaders).toHaveCount(0)
  })

  test("each column header shows correct icon", async ({ page }) => {
    const issues = [
      makeIssue({ id: "r-1", title: "Open Task", status: "open" }),
      makeIssue({ id: "b-1", title: "Blocked Task", status: "blocked" }),
      makeIssue({ id: "d-1", title: "Deferred Task", status: "deferred" }),
      makeIssue({
        id: "rev-1",
        title: "[Need Review] Review Task",
        status: "open",
      }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    // Open column shows clock icon (SVG)
    const openColumn = page.locator('section[data-status="ready"]')
    const openIcon = openColumn.locator("header svg[aria-hidden='true']").first()
    await expect(openIcon).toBeVisible()

    // Backlog, Blocked, In Progress, Needs Review, Done headers should NOT have SVG icons
    const backlogColumn = page.locator('section[data-status="backlog"]')
    await expect(
      backlogColumn.locator("header svg[aria-hidden='true']")
    ).toHaveCount(0)

    const blockedColumn = page.locator('section[data-status="blocked"]')
    await expect(
      blockedColumn.locator("header svg[aria-hidden='true']")
    ).toHaveCount(0)

    const inProgressColumn = page.locator('section[data-status="in_progress"]')
    await expect(
      inProgressColumn.locator("header svg[aria-hidden='true']")
    ).toHaveCount(0)

    const reviewColumn = page.locator('section[data-status="review"]')
    await expect(
      reviewColumn.locator("header svg[aria-hidden='true']")
    ).toHaveCount(0)

    const doneColumn = page.locator('section[data-status="done"]')
    await expect(
      doneColumn.locator("header svg[aria-hidden='true']")
    ).toHaveCount(0)
  })
})

test.describe("Column Redesign: issue distribution across all columns", () => {
  test("issues distribute correctly across all 6 columns simultaneously", async ({
    page,
  }) => {
    const issues = [
      // open, no blockers → Open
      makeIssue({ id: "open-1", title: "Open Task", status: "open" }),
      // status=blocked → Blocked
      makeIssue({
        id: "blocked-1",
        title: "Explicitly Blocked Task",
        status: "blocked",
      }),
      // status=deferred → Backlog
      makeIssue({
        id: "deferred-1",
        title: "Deferred Task",
        status: "deferred",
      }),
      // open with dependency blockers → Blocked
      makeIssue({
        id: "dep-blocked-1",
        title: "Dep Blocked Task",
        status: "open",
      }),
      // in_progress → In Progress
      makeIssue({
        id: "ip-1",
        title: "In Progress Task",
        status: "in_progress",
      }),
      // [Need Review] in title → Needs Review
      makeIssue({
        id: "review-1",
        title: "[Need Review] Review Task",
        status: "open",
      }),
      // status=review → Needs Review
      makeIssue({
        id: "review-2",
        title: "Status Review Task",
        status: "review",
      }),
      // closed → Done
      makeIssue({ id: "done-1", title: "Done Task", status: "closed" }),
    ]

    const blockedData = {
      ...issues[3],
      blocked_by_count: 1,
      blocked_by: ["some-blocker"],
    }

    await setupMocks(page, { issues, blockedIssues: [blockedData] })
    await navigateToKanban(page)

    const backlogColumn = page.locator('section[data-status="backlog"]')
    const openColumn = page.locator('section[data-status="ready"]')
    const blockedColumn = page.locator('section[data-status="blocked"]')
    const inProgressColumn = page.locator('section[data-status="in_progress"]')
    const reviewColumn = page.locator('section[data-status="review"]')
    const doneColumn = page.locator('section[data-status="done"]')

    // Backlog: 1 (deferred only)
    await expect(backlogColumn.locator("article")).toHaveCount(1)
    await expect(backlogColumn.getByText("Deferred Task")).toBeVisible()

    // Open: 1 (open-1)
    await expect(openColumn.locator("article")).toHaveCount(1)
    await expect(openColumn.getByText("Open Task")).toBeVisible()

    // Blocked: 2 (status=blocked + dep-blocked)
    await expect(blockedColumn.locator("article")).toHaveCount(2)
    await expect(
      blockedColumn.getByText("Explicitly Blocked Task")
    ).toBeVisible()
    await expect(blockedColumn.getByText("Dep Blocked Task")).toBeVisible()

    // In Progress: 1
    await expect(inProgressColumn.locator("article")).toHaveCount(1)
    await expect(inProgressColumn.getByText("In Progress Task")).toBeVisible()

    // Needs Review: 2 (title-based + status-based)
    await expect(reviewColumn.locator("article")).toHaveCount(2)
    await expect(
      reviewColumn.getByText("[Need Review] Review Task")
    ).toBeVisible()
    await expect(reviewColumn.getByText("Status Review Task")).toBeVisible()

    // Done: 1
    await expect(doneColumn.locator("article")).toHaveCount(1)
    await expect(doneColumn.getByText("Done Task")).toBeVisible()

    // Verify no card appears in multiple columns (total = 8)
    const allCards = page.locator("section[data-status] article")
    await expect(allCards).toHaveCount(8)
  })

  test("epic issues are excluded from all 6 columns (except Done)", async ({ page }) => {
    const issues = [
      makeIssue({
        id: "epic-open",
        title: "Epic Open",
        status: "open",
        issue_type: "epic",
      }),
      makeIssue({
        id: "epic-ip",
        title: "Epic In Progress",
        status: "in_progress",
        issue_type: "epic",
      }),
      makeIssue({
        id: "epic-blocked",
        title: "Epic Blocked",
        status: "blocked",
        issue_type: "epic",
      }),
      makeIssue({
        id: "epic-closed",
        title: "Epic Closed",
        status: "closed",
        issue_type: "epic",
      }),
      // One non-epic issue to verify columns still render
      makeIssue({
        id: "task-1",
        title: "Normal Task",
        status: "open",
      }),
    ]

    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const backlogColumn = page.locator('section[data-status="backlog"]')
    const openColumn = page.locator('section[data-status="ready"]')
    const blockedColumn = page.locator('section[data-status="blocked"]')
    const inProgressColumn = page.locator('section[data-status="in_progress"]')
    const reviewColumn = page.locator('section[data-status="review"]')
    const doneColumn = page.locator('section[data-status="done"]')

    // Only the non-epic task should appear in Open
    await expect(openColumn.locator("article")).toHaveCount(1)
    await expect(openColumn.getByText("Normal Task")).toBeVisible()

    // Epics are excluded from Backlog, Open, Blocked, In Progress, Needs Review columns
    await expect(backlogColumn.locator("article")).toHaveCount(0)
    await expect(blockedColumn.locator("article")).toHaveCount(0)
    await expect(inProgressColumn.locator("article")).toHaveCount(0)
    await expect(reviewColumn.locator("article")).toHaveCount(0)

    // Note: Done column filter (status === 'closed') does not exclude epics,
    // so the closed epic appears in Done. This is expected behavior.
    await expect(doneColumn.locator("article")).toHaveCount(1)
    await expect(doneColumn.getByText("Epic Closed")).toBeVisible()
  })
})

test.describe("Column Redesign: column backgrounds", () => {
  test("Open, In Progress, Done columns use warm gray column background", async ({
    page,
  }) => {
    const issues = [
      makeIssue({ id: "r-1", title: "Some Task", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const openBg = await page
      .locator('section[data-status="ready"]')
      .evaluate((el) => window.getComputedStyle(el).backgroundColor)
    const ipBg = await page
      .locator('section[data-status="in_progress"]')
      .evaluate((el) => window.getComputedStyle(el).backgroundColor)
    const doneBg = await page
      .locator('section[data-status="done"]')
      .evaluate((el) => window.getComputedStyle(el).backgroundColor)

    // All three should use --color-column-bg (#EAE8E1 → rgb(234, 232, 225))
    expect(openBg).toBe("rgb(234, 232, 225)")
    expect(ipBg).toBe("rgb(234, 232, 225)")
    expect(doneBg).toBe("rgb(234, 232, 225)")
  })

  test("Backlog column uses muted warm gray background", async ({ page }) => {
    const issues = [
      makeIssue({ id: "r-1", title: "Some Task", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const backlogColumn = page.locator('section[data-status="backlog"]')
    await expect(backlogColumn).toHaveAttribute("data-column-type", "backlog")

    const backlogBg = await backlogColumn.evaluate(
      (el) => window.getComputedStyle(el).backgroundColor
    )
    // --color-column-bg-muted: #E4E2DB → rgb(228, 226, 219)
    expect(backlogBg).toBe("rgb(228, 226, 219)")
  })

  test("Blocked column uses muted styling via CSS class", async ({ page }) => {
    const issues = [
      makeIssue({ id: "r-1", title: "Some Task", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    // Blocked column gets muted styling via CSS class (not data-column-type)
    const blockedColumn = page.locator('section[data-status="blocked"]')
    await expect(blockedColumn).toBeVisible()

    // Verify the column has the muted class applied
    const hasClass = await blockedColumn.evaluate((el) =>
      Array.from(el.classList).some((c) => c.includes("muted"))
    )
    expect(hasClass).toBe(true)
  })
})

test.describe("Column Redesign: status badges and header accents", () => {
  test("column count badges show correct counts", async ({ page }) => {
    const issues = [
      makeIssue({ id: "r-1", title: "Open 1", status: "open" }),
      makeIssue({ id: "r-2", title: "Open 2", status: "open" }),
      makeIssue({ id: "b-1", title: "Blocked 1", status: "blocked" }),
      makeIssue({ id: "def-1", title: "Deferred 1", status: "deferred" }),
      makeIssue({ id: "ip-1", title: "IP 1", status: "in_progress" }),
      makeIssue({
        id: "rev-1",
        title: "[Need Review] Rev 1",
        status: "open",
      }),
      makeIssue({ id: "d-1", title: "Done 1", status: "closed" }),
      makeIssue({ id: "d-2", title: "Done 2", status: "closed" }),
      makeIssue({ id: "d-3", title: "Done 3", status: "closed" }),
    ]

    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const backlogColumn = page.locator('section[data-status="backlog"]')
    const openColumn = page.locator('section[data-status="ready"]')
    const blockedColumn = page.locator('section[data-status="blocked"]')
    const inProgressColumn = page.locator('section[data-status="in_progress"]')
    const reviewColumn = page.locator('section[data-status="review"]')
    const doneColumn = page.locator('section[data-status="done"]')

    // Backlog: 1 (deferred only)
    await expect(backlogColumn.getByLabel("1 issue")).toBeVisible()
    // Open: 2 (open without blockers)
    await expect(openColumn.getByLabel("2 issues")).toBeVisible()
    // Blocked: 1 (status=blocked)
    await expect(blockedColumn.getByLabel("1 issue")).toBeVisible()
    // In Progress: 1
    await expect(inProgressColumn.getByLabel("1 issue")).toBeVisible()
    // Needs Review: 1 (title-based)
    await expect(reviewColumn.getByLabel("1 issue")).toBeVisible()
    // Done: 3
    await expect(doneColumn.getByLabel("3 issues")).toBeVisible()
  })

  test("empty columns show 0 issues badge", async ({ page }) => {
    // Need at least one issue to get flat kanban to render columns
    const issues = [
      makeIssue({ id: "r-1", title: "Some Task", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    // Open has 1 issue, others have 0
    const backlogColumn = page.locator('section[data-status="backlog"]')
    const blockedColumn = page.locator('section[data-status="blocked"]')
    const inProgressColumn = page.locator('section[data-status="in_progress"]')
    const reviewColumn = page.locator('section[data-status="review"]')
    const doneColumn = page.locator('section[data-status="done"]')

    await expect(backlogColumn.getByLabel("0 issues")).toBeVisible()
    await expect(blockedColumn.getByLabel("0 issues")).toBeVisible()
    await expect(inProgressColumn.getByLabel("0 issues")).toBeVisible()
    await expect(reviewColumn.getByLabel("0 issues")).toBeVisible()
    await expect(doneColumn.getByLabel("0 issues")).toBeVisible()
  })

  test("column headers have border styling", async ({ page }) => {
    const issues = [
      makeIssue({ id: "r-1", title: "Open Task", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    // Verify each column header has a border (not none/empty)
    const columns = [
      "backlog",
      "ready",
      "blocked",
      "in_progress",
      "review",
      "done",
    ]

    for (const status of columns) {
      const borderColor = await page
        .locator(`section[data-status="${status}"] > header`)
        .evaluate((el) => window.getComputedStyle(el).borderBottomColor)
      // Border color should be set (not empty or transparent)
      expect(borderColor).toBeTruthy()
      expect(borderColor).not.toBe("rgba(0, 0, 0, 0)")
    }
  })
})

test.describe("Column Redesign: default group-by-epic view", () => {
  test("navigating to /?groupBy=epic loads swim lane view", async ({
    page,
  }) => {
    const issues = [
      makeIssue({
        id: "e1-open",
        title: "Epic One Feature",
        status: "open",
        issue_type: "feature",
        parent: "epic-1",
        parent_title: "Epic One: Authentication",
      }),
      makeIssue({
        id: "orphan-1",
        title: "Standalone Task",
        status: "open",
        issue_type: "task",
      }),
    ]

    await setupMocks(page, { issues })

    const [response] = await Promise.all([
      page.waitForResponse(
        (res) => res.url().includes("/api/ready") && res.status() === 200
      ),
      page.goto("/?groupBy=epic"),
    ])
    expect(response.ok()).toBe(true)

    // Verify swim lane board is visible
    await expect(page.getByTestId("swim-lane-board")).toBeVisible()

    // Verify at least one epic lane heading is visible
    await expect(
      page.getByRole("heading", {
        name: "Epic One: Authentication",
        exact: true,
      })
    ).toBeVisible()

    // Verify Ungrouped lane exists for orphan issues
    await expect(
      page.getByRole("heading", { name: "Ungrouped", exact: true })
    ).toBeVisible()
  })

  test("each epic lane contains the 6-column layout", async ({ page }) => {
    const issues = [
      makeIssue({
        id: "e1-open",
        title: "Open Feature",
        status: "open",
        parent: "epic-1",
        parent_title: "Test Epic",
      }),
      makeIssue({
        id: "e1-deferred",
        title: "Deferred Feature",
        status: "deferred",
        parent: "epic-1",
        parent_title: "Test Epic",
      }),
      makeIssue({
        id: "e1-blocked",
        title: "Blocked Feature",
        status: "blocked",
        parent: "epic-1",
        parent_title: "Test Epic",
      }),
      makeIssue({
        id: "e1-ip",
        title: "IP Feature",
        status: "in_progress",
        parent: "epic-1",
        parent_title: "Test Epic",
      }),
      makeIssue({
        id: "e1-review",
        title: "[Need Review] Review Feature",
        status: "open",
        parent: "epic-1",
        parent_title: "Test Epic",
      }),
      makeIssue({
        id: "e1-done",
        title: "Done Feature",
        status: "closed",
        parent: "epic-1",
        parent_title: "Test Epic",
      }),
    ]

    await setupMocks(page, { issues })

    const [response] = await Promise.all([
      page.waitForResponse(
        (res) => res.url().includes("/api/ready") && res.status() === 200
      ),
      page.goto("/?groupBy=epic"),
    ])
    expect(response.ok()).toBe(true)
    await expect(page.getByTestId("swim-lane-board")).toBeVisible()

    const epicLane = page.getByTestId("swim-lane-lane-epic-epic-1")
    await expect(epicLane).toBeVisible()

    // Verify all 6 column sections exist within the lane
    await expect(
      epicLane.locator('section[data-status="backlog"]')
    ).toBeVisible()
    await expect(
      epicLane.locator('section[data-status="ready"]')
    ).toBeVisible()
    await expect(
      epicLane.locator('section[data-status="blocked"]')
    ).toBeVisible()
    await expect(
      epicLane.locator('section[data-status="in_progress"]')
    ).toBeVisible()
    await expect(
      epicLane.locator('section[data-status="review"]')
    ).toBeVisible()
    await expect(
      epicLane.locator('section[data-status="done"]')
    ).toBeVisible()

    // Verify issues distribute to correct columns within the lane
    await expect(
      epicLane
        .locator('section[data-status="backlog"]')
        .getByText("Deferred Feature")
    ).toBeVisible()
    await expect(
      epicLane
        .locator('section[data-status="ready"]')
        .getByText("Open Feature")
    ).toBeVisible()
    await expect(
      epicLane
        .locator('section[data-status="blocked"]')
        .getByText("Blocked Feature")
    ).toBeVisible()
    await expect(
      epicLane
        .locator('section[data-status="in_progress"]')
        .getByText("IP Feature")
    ).toBeVisible()
    await expect(
      epicLane
        .locator('section[data-status="review"]')
        .getByText("[Need Review] Review Feature")
    ).toBeVisible()
    await expect(
      epicLane
        .locator('section[data-status="done"]')
        .getByText("Done Feature")
    ).toBeVisible()
  })
})
