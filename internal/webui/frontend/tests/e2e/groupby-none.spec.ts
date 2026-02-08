import { test, expect, Page } from "@playwright/test"

/**
 * Mock issues for testing flat view (groupBy=none).
 * Distribution: 3 open, 1 in_progress, 1 closed (total 5)
 */
const mockIssues = [
  {
    id: "open-1",
    title: "Open Issue One",
    status: "open",
    priority: 2,
    issue_type: "task",
    assignee: "alice",
    created_at: "2026-01-27T10:00:00Z",
    updated_at: "2026-01-27T10:00:00Z",
  },
  {
    id: "open-2",
    title: "Open Bug Two",
    status: "open",
    priority: 1,
    issue_type: "bug",
    assignee: "bob",
    created_at: "2026-01-27T11:00:00Z",
    updated_at: "2026-01-27T11:00:00Z",
  },
  {
    id: "open-3",
    title: "Open Feature Three",
    status: "open",
    priority: 0,
    issue_type: "feature",
    created_at: "2026-01-27T12:00:00Z",
    updated_at: "2026-01-27T12:00:00Z",
  },
  {
    id: "progress-1",
    title: "In Progress Task",
    status: "in_progress",
    priority: 2,
    issue_type: "task",
    assignee: "alice",
    created_at: "2026-01-27T13:00:00Z",
    updated_at: "2026-01-27T13:00:00Z",
  },
  {
    id: "closed-1",
    title: "Closed Bug",
    status: "closed",
    priority: 3,
    issue_type: "bug",
    created_at: "2026-01-27T14:00:00Z",
    updated_at: "2026-01-27T14:00:00Z",
  },
]

// Timestamp helper for custom test data
const timestamps = {
  created_at: "2026-01-27T10:00:00Z",
  updated_at: "2026-01-27T10:00:00Z",
}

/**
 * Set up API mocks.
 */
async function setupMocks(page: Page, issues: object[] = mockIssues) {
  await page.route("**/api/ready", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: issues }),
    })
  })
}

/**
 * Set up mocks with PATCH tracking for drag-drop tests.
 */
async function setupMocksWithPatch(
  page: Page,
  patchCalls: { url: string; body: object }[],
  issues: object[] = mockIssues
) {
  await page.route("**/api/ready", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: issues }),
    })
  })
  await page.route("**/api/issues/*", async (route) => {
    if (route.request().method() === "PATCH") {
      const url = route.request().url()
      const body = route.request().postDataJSON() as { status?: string }
      patchCalls.push({ url, body })
      const issueId = url.split("/").pop()
      const issue = issues.find(
        (i) => (i as { id: string }).id === issueId
      ) as object
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          success: true,
          data: { ...issue, ...body, updated_at: new Date().toISOString() },
        }),
      })
    } else {
      await route.continue()
    }
  })
}

/**
 * Navigate and wait for API response.
 */
async function navigateAndWait(page: Page, path = "/") {
  const [response] = await Promise.all([
    page.waitForResponse(
      (res) => res.url().includes("/api/ready") && res.status() === 200
    ),
    page.goto(path),
  ])
  expect(response.ok()).toBe(true)
}

test.describe("groupBy None (Flat View)", () => {
  test.describe("Default State", () => {
    test("default page load shows epic swim lane view without groupBy URL param", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      // No groupBy param in URL
      expect(page.url()).not.toContain("groupBy")

      // Swim lane board should be visible (epic swim lanes are the default)
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()

      // GroupBy dropdown shows 'epic' selected
      await expect(page.getByTestId("groupby-filter")).toHaveValue("epic")
    })

    test("navigating with explicit groupBy=none shows flat view", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=none")

      // Flat view renders
      await expect(page.getByTestId("swim-lane-board")).not.toBeVisible()
      await expect(page.locator('section[data-status="ready"]')).toBeVisible()

      // Dropdown shows 'none'
      await expect(page.getByTestId("groupby-filter")).toHaveValue("none")
    })
  })

  test.describe("Flat View Rendering", () => {
    test("flat view renders KanbanBoard without any swim lane elements", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=none")

      // No swim lane board
      await expect(page.getByTestId("swim-lane-board")).not.toBeVisible()

      // No swim lane elements at all
      const lanes = page.locator('[data-testid^="swim-lane-lane-"]')
      await expect(lanes).toHaveCount(0)

      // No collapse toggles (swim lane feature)
      const collapseToggles = page.locator('[data-testid="collapse-toggle"]')
      await expect(collapseToggles).toHaveCount(0)
    })

    test("no grouping headers shown in flat view", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=none")

      // No swim lane headings (e.g., epic titles, assignee names, priority labels)
      const swimLaneHeadings = page.locator(
        '[data-testid^="swim-lane-lane-"] h3'
      )
      await expect(swimLaneHeadings).toHaveCount(0)
    })
  })

  test.describe("Status Columns", () => {
    test("standard status columns visible with correct structure", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=none")

      const openColumn = page.locator('section[data-status="ready"]')
      const inProgressColumn = page.locator('section[data-status="in_progress"]')
      const closedColumn = page.locator('section[data-status="done"]')

      // All three status columns visible
      await expect(openColumn).toBeVisible()
      await expect(inProgressColumn).toBeVisible()
      await expect(closedColumn).toBeVisible()

      // Verify issue counts per column (3 open, 1 in_progress, 1 closed)
      await expect(openColumn.locator("article")).toHaveCount(3)
      await expect(inProgressColumn.locator("article")).toHaveCount(1)
      await expect(closedColumn.locator("article")).toHaveCount(1)
    })

    test("issues appear in correct status columns", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=none")

      const openColumn = page.locator('section[data-status="ready"]')
      const inProgressColumn = page.locator('section[data-status="in_progress"]')
      const closedColumn = page.locator('section[data-status="done"]')

      // Open issues
      await expect(openColumn.getByText("Open Issue One")).toBeVisible()
      await expect(openColumn.getByText("Open Bug Two")).toBeVisible()
      await expect(openColumn.getByText("Open Feature Three")).toBeVisible()

      // In Progress issue
      await expect(inProgressColumn.getByText("In Progress Task")).toBeVisible()

      // Closed issue
      await expect(closedColumn.getByText("Closed Bug")).toBeVisible()
    })
  })

  test.describe("Drag and Drop", () => {
    test("drag issue from open to in_progress updates status via API", async ({
      page,
    }) => {
      const patchCalls: { url: string; body: object }[] = []
      await setupMocksWithPatch(page, patchCalls)
      await navigateAndWait(page, "/?groupBy=none")

      const openColumn = page.locator('section[data-status="ready"]')
      const inProgressColumn = page.locator('section[data-status="in_progress"]')

      // Initial state
      await expect(openColumn.locator("article")).toHaveCount(3)
      await expect(inProgressColumn.locator("article")).toHaveCount(1)

      // Get card to drag
      const card = openColumn
        .locator("article")
        .filter({ hasText: "Open Issue One" })
      const draggable = card.locator("..")
      const dropTarget = inProgressColumn.locator(
        '[data-droppable-id="in_progress"]'
      )

      const sourceBox = await draggable.boundingBox()
      const targetBox = await dropTarget.boundingBox()
      if (!sourceBox || !targetBox) throw new Error("Could not get bounding boxes")

      const startX = sourceBox.x + sourceBox.width / 2
      const startY = sourceBox.y + sourceBox.height / 2
      const endX = targetBox.x + targetBox.width / 2
      const endY = targetBox.y + targetBox.height / 2

      // Perform drag operation
      await draggable.dispatchEvent("pointerdown", {
        clientX: startX,
        clientY: startY,
        button: 0,
        buttons: 1,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      })
      await page.waitForTimeout(50)

      await page.dispatchEvent("body", "pointermove", {
        clientX: startX + 10,
        clientY: startY,
        button: 0,
        buttons: 1,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      })
      await page.waitForTimeout(50)

      await page.dispatchEvent("body", "pointermove", {
        clientX: endX,
        clientY: endY,
        button: 0,
        buttons: 1,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      })
      await page.waitForTimeout(50)

      await page.dispatchEvent("body", "pointerup", {
        clientX: endX,
        clientY: endY,
        button: 0,
        buttons: 0,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      })

      // Wait for PATCH
      await page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/open-1") &&
          res.request().method() === "PATCH"
      )

      // Verify API call
      expect(patchCalls).toHaveLength(1)
      expect(patchCalls[0].body).toEqual({ status: "in_progress" })

      // Verify UI update
      await expect(inProgressColumn.getByText("Open Issue One")).toBeVisible()
    })

    test("drag issue to closed column works", async ({ page }) => {
      const patchCalls: { url: string; body: object }[] = []
      await setupMocksWithPatch(page, patchCalls)
      await navigateAndWait(page, "/?groupBy=none")

      const openColumn = page.locator('section[data-status="ready"]')
      const closedColumn = page.locator('section[data-status="done"]')

      const card = openColumn
        .locator("article")
        .filter({ hasText: "Open Bug Two" })
      const draggable = card.locator("..")
      const dropTarget = closedColumn.locator('[data-droppable-id="closed"]')

      const sourceBox = await draggable.boundingBox()
      const targetBox = await dropTarget.boundingBox()
      if (!sourceBox || !targetBox) throw new Error("Could not get bounding boxes")

      const startX = sourceBox.x + sourceBox.width / 2
      const startY = sourceBox.y + sourceBox.height / 2
      const endX = targetBox.x + targetBox.width / 2
      const endY = targetBox.y + targetBox.height / 2

      await draggable.dispatchEvent("pointerdown", {
        clientX: startX,
        clientY: startY,
        button: 0,
        buttons: 1,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      })
      await page.waitForTimeout(50)

      await page.dispatchEvent("body", "pointermove", {
        clientX: startX + 10,
        clientY: startY,
        button: 0,
        buttons: 1,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      })
      await page.waitForTimeout(50)

      await page.dispatchEvent("body", "pointermove", {
        clientX: endX,
        clientY: endY,
        button: 0,
        buttons: 1,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      })
      await page.waitForTimeout(50)

      await page.dispatchEvent("body", "pointerup", {
        clientX: endX,
        clientY: endY,
        button: 0,
        buttons: 0,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      })

      await page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/open-2") &&
          res.request().method() === "PATCH"
      )

      expect(patchCalls[0].body).toEqual({ status: "closed" })
      await expect(closedColumn.getByText("Open Bug Two")).toBeVisible()
    })
  })

  test.describe("View Switching", () => {
    test("switching from epic grouping to none shows flat view", async ({
      page,
    }) => {
      // Add parent fields for epic grouping
      const issuesWithEpic = mockIssues.map((i, idx) => ({
        ...i,
        parent: idx < 3 ? "epic-1" : undefined,
        parent_title: idx < 3 ? "Epic One" : undefined,
      }))
      await setupMocks(page, issuesWithEpic)
      await navigateAndWait(page, "/?groupBy=epic")

      // Verify swim lanes exist
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "Epic One", exact: true })
      ).toBeVisible()

      // Switch to 'none'
      await page.getByTestId("groupby-filter").selectOption("none")

      // Verify flat view
      await expect(page.getByTestId("swim-lane-board")).not.toBeVisible()
      await expect(page.locator('section[data-status="ready"]')).toBeVisible()

      // No epic lane headers
      await expect(
        page.getByRole("heading", { name: "Epic One", exact: true })
      ).not.toBeVisible()
    })

    test("switching from priority grouping to none hides swim lanes", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=priority")

      // Verify priority lanes exist
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()
      // Check for P2 (Medium) which is in our mock data (priority: 2)
      await expect(
        page.getByRole("heading", { name: "P2 (Medium)" })
      ).toBeVisible()

      // Switch to 'none'
      await page.getByTestId("groupby-filter").selectOption("none")

      // Verify flat view - no swim lane board
      await expect(page.getByTestId("swim-lane-board")).not.toBeVisible()

      // No swim lane elements
      const lanes = page.locator('[data-testid^="swim-lane-lane-"]')
      await expect(lanes).toHaveCount(0)
    })

    test("switching groupBy repeatedly works correctly", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=none")

      // Start flat
      await expect(page.getByTestId("swim-lane-board")).not.toBeVisible()

      // Switch to priority
      await page.getByTestId("groupby-filter").selectOption("priority")
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()

      // Back to flat
      await page.getByTestId("groupby-filter").selectOption("none")
      await expect(page.getByTestId("swim-lane-board")).not.toBeVisible()

      // To assignee
      await page.getByTestId("groupby-filter").selectOption("assignee")
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()

      // Back to flat
      await page.getByTestId("groupby-filter").selectOption("none")
      await expect(page.getByTestId("swim-lane-board")).not.toBeVisible()

      // All status columns visible
      await expect(page.locator('section[data-status="ready"]')).toBeVisible()
    })
  })

  test.describe("URL State", () => {
    test("URL has no groupBy param when flat view selected", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=epic")

      // Switch to flat view
      await page.getByTestId("groupby-filter").selectOption("none")

      // URL should NOT contain groupBy
      await expect(async () => {
        expect(page.url()).not.toContain("groupBy")
      }).toPass({ timeout: 2000 })
    })

    test("page reload preserves epic swim lane view (default)", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      // Verify epic swim lane view (default)
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()

      // Re-setup mocks before reload
      await setupMocks(page)
      await page.reload()
      await page.waitForResponse((res) => res.url().includes("/api/ready"))

      // Still epic swim lane view
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()
    })

    test("invalid groupBy URL param defaults to epic swim lane view", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=invalid")

      // Epic swim lane view renders (invalid param falls back to default)
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()

      // Dropdown shows 'epic' (default)
      await expect(page.getByTestId("groupby-filter")).toHaveValue("epic")
    })
  })

  test.describe("Filter Integration", () => {
    test("priority filter reduces visible issues in flat view", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=none")

      // All 5 issues visible initially
      await expect(page.locator("article")).toHaveCount(5)

      // Filter to P2 only (2 issues: open-1 and progress-1)
      await page.getByTestId("priority-filter").selectOption("2")

      // Only P2 issues visible
      await expect(page.locator("article")).toHaveCount(2)
      await expect(page.getByText("Open Issue One")).toBeVisible()
      await expect(page.getByText("In Progress Task")).toBeVisible()
    })

    test("type filter works with flat view", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=none")

      // Filter to bugs only (2 bugs: open-2 and closed-1)
      await page.getByTestId("type-filter").selectOption("bug")

      await expect(page.locator("article")).toHaveCount(2)
      await expect(page.getByText("Open Bug Two")).toBeVisible()
      await expect(page.getByText("Closed Bug")).toBeVisible()
    })

    test("clear filters restores all issues in flat view", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=none")

      const clearButton = page.getByTestId("clear-filters")
      const priorityFilter = page.getByTestId("priority-filter")

      // Verify clear button not visible initially (no active filters)
      await expect(clearButton).not.toBeVisible()

      // Apply filter
      await priorityFilter.selectOption("0")
      await expect(page.locator("article")).toHaveCount(1)

      // Wait for clear button to become visible
      await expect(clearButton).toBeVisible()

      // Click clear button using JS to bypass element interception
      await clearButton.evaluate((button: HTMLElement) => button.click())

      // Wait for URL to clear priority param
      await expect(async () => {
        expect(page.url()).not.toContain("priority=")
      }).toPass({ timeout: 2000 })

      // Dropdown should show "All" option
      await expect(priorityFilter).toHaveValue("")

      // All issues restored
      await expect(page.locator("article")).toHaveCount(5)

      // Clear button hidden after clearing
      await expect(clearButton).not.toBeVisible()
    })
  })

  test.describe("Edge Cases", () => {
    test("empty issues shows empty status columns", async ({ page }) => {
      await setupMocks(page, [])
      await navigateAndWait(page, "/?groupBy=none")

      // Status columns still visible
      await expect(page.locator('section[data-status="ready"]')).toBeVisible()
      await expect(
        page.locator('section[data-status="in_progress"]')
      ).toBeVisible()
      await expect(page.locator('section[data-status="done"]')).toBeVisible()

      // No issues
      await expect(page.locator("article")).toHaveCount(0)

      // No swim lanes (flat view)
      await expect(page.getByTestId("swim-lane-board")).not.toBeVisible()
    })

    test("all issues in one status column works correctly", async ({
      page,
    }) => {
      const allOpenIssues = [
        {
          id: "o1",
          title: "Issue 1",
          status: "open",
          priority: 2,
          issue_type: "task",
          ...timestamps,
        },
        {
          id: "o2",
          title: "Issue 2",
          status: "open",
          priority: 2,
          issue_type: "task",
          ...timestamps,
        },
        {
          id: "o3",
          title: "Issue 3",
          status: "open",
          priority: 2,
          issue_type: "task",
          ...timestamps,
        },
      ]
      await setupMocks(page, allOpenIssues)
      await navigateAndWait(page, "/?groupBy=none")

      const openColumn = page.locator('section[data-status="ready"]')
      const inProgressColumn = page.locator('section[data-status="in_progress"]')
      const closedColumn = page.locator('section[data-status="done"]')

      // All in open
      await expect(openColumn.locator("article")).toHaveCount(3)
      await expect(inProgressColumn.locator("article")).toHaveCount(0)
      await expect(closedColumn.locator("article")).toHaveCount(0)
    })
  })
})
