import { test, expect, Page } from "@playwright/test"

/**
 * Mock issues for testing swim lane wiring in App.tsx.
 * Includes varied fields for groupBy testing (epic, assignee, priority, type).
 */
const mockIssues = [
  // Issues with epic parent
  {
    id: "epic-child-1",
    title: "Feature in Epic",
    status: "open",
    priority: 1,
    issue_type: "feature",
    parent: "epic-1",
    parent_title: "Epic One",
    assignee: "alice",
    created_at: "2026-01-27T10:00:00Z",
    updated_at: "2026-01-27T10:00:00Z",
  },
  {
    id: "epic-child-2",
    title: "Bug in Epic",
    status: "in_progress",
    priority: 0,
    issue_type: "bug",
    parent: "epic-1",
    parent_title: "Epic One",
    assignee: "bob",
    created_at: "2026-01-27T11:00:00Z",
    updated_at: "2026-01-27T11:00:00Z",
  },
  // Orphan issue (no parent)
  {
    id: "orphan-1",
    title: "Standalone Task",
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-27T12:00:00Z",
    updated_at: "2026-01-27T12:00:00Z",
  },
]

/**
 * Set up API mocks for swim lane wiring tests.
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
 * Navigate to a page and wait for API response.
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

test.describe("Swim Lane Wiring in App.tsx", () => {
  test.describe("Integration - SwimLaneBoard Rendering", () => {
    test("SwimLaneBoard renders when groupBy is not none", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=epic")

      // Verify swim lane board is visible
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()

      // Verify epic swim lanes appear
      await expect(
        page.getByRole("heading", { name: "Epic One", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "Ungrouped", exact: true })
      ).toBeVisible()
    })

    test("flat Kanban columns are inside swim lanes, not at root", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=epic")

      // Verify swim lane board exists
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()

      // Verify status columns exist within swim lanes
      const epicOneLane = page.getByTestId("swim-lane-lane-epic-epic-1")
      await expect(
        epicOneLane.locator('section[data-status="ready"]')
      ).toBeVisible()
      await expect(
        epicOneLane.locator('section[data-status="in_progress"]')
      ).toBeVisible()
    })
  })

  test.describe("Fallback - KanbanBoard Rendering", () => {
    test("KanbanBoard renders when groupBy is none", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=none")

      // Verify swim lane board is NOT visible
      await expect(page.getByTestId("swim-lane-board")).not.toBeVisible()

      // Verify flat Kanban columns are visible
      const openColumn = page.locator('section[data-status="ready"]')
      const inProgressColumn = page.locator('section[data-status="in_progress"]')
      const closedColumn = page.locator('section[data-status="done"]')

      await expect(openColumn).toBeVisible()
      await expect(inProgressColumn).toBeVisible()
      await expect(closedColumn).toBeVisible()

      // Verify issues distributed in flat columns (2 open, 1 in_progress)
      await expect(openColumn.locator("article")).toHaveCount(2)
      await expect(inProgressColumn.locator("article")).toHaveCount(1)
    })

    test("selecting none from dropdown returns to flat view", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=epic")

      // Verify swim lanes visible initially
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()

      // Select 'none' from dropdown
      const groupByFilter = page.getByTestId("groupby-filter")
      await groupByFilter.selectOption("none")

      // Verify swim lanes gone
      await expect(page.getByTestId("swim-lane-board")).not.toBeVisible()

      // Verify flat Kanban columns visible
      await expect(page.locator('section[data-status="ready"]')).toBeVisible()
    })
  })

  test.describe("State - groupBy URL Persistence", () => {
    test("groupBy selection persists via URL", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      // Select groupBy=priority from dropdown
      const groupByFilter = page.getByTestId("groupby-filter")
      await groupByFilter.selectOption("priority")

      // Verify URL updated
      await expect(async () => {
        expect(page.url()).toContain("groupBy=priority")
      }).toPass({ timeout: 2000 })

      // Verify swim lanes appear with priority lanes
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "P0 (Critical)" })
      ).toBeVisible()

      // Re-setup mocks before reload
      await setupMocks(page)

      // Reload page
      await page.reload()
      await page.waitForResponse((res) => res.url().includes("/api/ready"))

      // Verify groupBy still selected
      await expect(groupByFilter).toHaveValue("priority")
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()
    })

    test("navigating with groupBy URL param loads swim lanes", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=assignee")

      // Verify dropdown shows correct value
      await expect(page.getByTestId("groupby-filter")).toHaveValue("assignee")

      // Verify swim lanes are visible
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "alice", exact: true })
      ).toBeVisible()
    })
  })

  test.describe("Drag/Drop - Cross-Lane Status Updates", () => {
    test("drag and drop updates issue status", async ({ page }) => {
      // Track API calls
      const patchCalls: { url: string; body: object }[] = []

      await page.route("**/api/ready", async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: mockIssues }),
        })
      })

      await page.route("**/api/issues/*", async (route) => {
        if (route.request().method() === "PATCH") {
          const url = route.request().url()
          const body = route.request().postDataJSON() as { status?: string }
          patchCalls.push({ url, body })

          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({
              success: true,
              data: { ...mockIssues[0], status: body.status },
            }),
          })
        } else {
          await route.continue()
        }
      })

      await navigateAndWait(page, "/?groupBy=epic")

      // Get the Epic One lane
      const epicOneLane = page.getByTestId("swim-lane-lane-epic-epic-1")
      await expect(epicOneLane).toBeVisible()

      // Find open and in_progress columns within the lane
      const openColumn = epicOneLane.locator('section[data-status="ready"]')
      const inProgressColumn = epicOneLane.locator(
        'section[data-status="in_progress"]'
      )

      // Get the card to drag
      const cardToDrag = openColumn
        .locator("article")
        .filter({ hasText: "Feature in Epic" })
      await expect(cardToDrag).toBeVisible()

      // Perform drag operation (using the established pattern from kanban.spec.ts)
      const draggable = cardToDrag.locator("..")
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

      // Wait for API call
      await page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/epic-child-1") &&
          res.request().method() === "PATCH"
      )

      // Verify API call was made correctly
      expect(patchCalls).toHaveLength(1)
      expect(patchCalls[0].body).toEqual({ status: "in_progress" })

      // Verify UI updated
      await expect(inProgressColumn.getByText("Feature in Epic")).toBeVisible()
    })
  })

  test.describe("View Switch - groupBy State Preservation", () => {
    test("switching views preserves groupBy state", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=epic")

      // Verify swim lanes visible in Kanban
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()

      // Switch to Table view
      const tableTab = page.getByTestId("view-tab-table")
      await tableTab.click()

      // Verify URL still has groupBy
      expect(page.url()).toContain("groupBy=epic")
      expect(page.url()).toContain("view=table")

      // Verify Table renders
      await expect(page.getByTestId("issue-table")).toBeVisible()

      // Switch to Graph view
      const graphTab = page.getByTestId("view-tab-graph")
      await graphTab.click()

      // Verify URL still has groupBy
      expect(page.url()).toContain("groupBy=epic")
      expect(page.url()).toContain("view=graph")

      // Verify Graph renders
      await expect(page.getByTestId("graph-view")).toBeVisible()

      // Switch back to Kanban
      const kanbanTab = page.getByTestId("view-tab-kanban")
      await kanbanTab.click()

      // Verify groupBy still in URL
      expect(page.url()).toContain("groupBy=epic")

      // Verify swim lanes are back
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "Epic One", exact: true })
      ).toBeVisible()
    })

    test("changing groupBy while in Table view preserves on return to Kanban", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?view=table")

      // Change groupBy while in Table view
      const groupByFilter = page.getByTestId("groupby-filter")
      await groupByFilter.selectOption("priority")

      // Wait for URL update
      await expect(async () => {
        expect(page.url()).toContain("groupBy=priority")
      }).toPass({ timeout: 2000 })

      // Switch to Kanban
      const kanbanTab = page.getByTestId("view-tab-kanban")
      await kanbanTab.click()

      // Verify swim lanes render with priority grouping
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "P0 (Critical)" })
      ).toBeVisible()
    })
  })

  test.describe("Data Flow - Issues in Correct Lanes", () => {
    test("issues from API appear in correct lanes", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=epic")

      // Verify Epic One lane has correct issues
      const epicOneLane = page.getByTestId("swim-lane-lane-epic-epic-1")
      await expect(epicOneLane).toBeVisible()
      await expect(epicOneLane.locator("article")).toHaveCount(2)
      await expect(epicOneLane.getByText("Feature in Epic")).toBeVisible()
      await expect(epicOneLane.getByText("Bug in Epic")).toBeVisible()

      // Verify Ungrouped lane has orphan issue
      const ungroupedLane = page.getByTestId("swim-lane-lane-epic-__ungrouped__")
      await expect(ungroupedLane).toBeVisible()
      await expect(ungroupedLane.locator("article")).toHaveCount(1)
      await expect(ungroupedLane.getByText("Standalone Task")).toBeVisible()
    })

    test("issues distributed by status within lanes", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=epic")

      const epicOneLane = page.getByTestId("swim-lane-lane-epic-epic-1")

      // Verify issues in correct status columns within the lane
      const openColumn = epicOneLane.locator('section[data-status="ready"]')
      const inProgressColumn = epicOneLane.locator(
        'section[data-status="in_progress"]'
      )

      await expect(openColumn.locator("article")).toHaveCount(1)
      await expect(openColumn.getByText("Feature in Epic")).toBeVisible()

      await expect(inProgressColumn.locator("article")).toHaveCount(1)
      await expect(inProgressColumn.getByText("Bug in Epic")).toBeVisible()
    })
  })

  test.describe("Filter Interaction - groupBy with Other Filters", () => {
    test("groupBy works with type filter", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=epic&type=bug")

      // Verify swim lanes visible
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()

      // With type=bug filter, only "Bug in Epic" should be visible
      // Epic One lane should have 1 issue (the bug)
      const epicOneLane = page.getByTestId("swim-lane-lane-epic-epic-1")
      await expect(epicOneLane.locator("article")).toHaveCount(1)
      await expect(epicOneLane.getByText("Bug in Epic")).toBeVisible()
      await expect(epicOneLane.getByText("Feature in Epic")).not.toBeVisible()

      // Standalone Task is a task, should not be visible
      await expect(page.getByText("Standalone Task")).not.toBeVisible()
    })

    test("groupBy works with priority filter", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=assignee&priority=1")

      // Verify swim lanes visible
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()

      // With priority=1 filter, only "Feature in Epic" (priority 1) should be visible
      const aliceLane = page.getByTestId("swim-lane-lane-assignee-alice")
      await expect(aliceLane).toBeVisible()
      await expect(aliceLane.locator("article")).toHaveCount(1)
      await expect(aliceLane.getByText("Feature in Epic")).toBeVisible()

      // Bob's issue (Bug in Epic) is P0, should not be visible
      await expect(page.getByText("Bug in Epic")).not.toBeVisible()
    })

    test("changing groupBy preserves other filter params", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?priority=1&type=feature")

      // Change groupBy
      await page.getByTestId("groupby-filter").selectOption("type")

      // Verify other params still present
      await expect(async () => {
        const url = page.url()
        expect(url).toContain("priority=1")
        expect(url).toContain("type=feature")
        expect(url).toContain("groupBy=type")
      }).toPass({ timeout: 2000 })
    })
  })

  test.describe("Edge Cases", () => {
    test("invalid groupBy URL param defaults to epic swim lane view", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=invalid")

      // Should fall back to epic swim lane view (default)
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()

      // Verify dropdown shows 'epic' (default)
      await expect(page.getByTestId("groupby-filter")).toHaveValue("epic")
    })

    test("empty issues still renders swim lane container", async ({ page }) => {
      await setupMocks(page, [])
      await navigateAndWait(page, "/?groupBy=epic")

      // Swim lane board should exist but have no lanes
      // Note: with no issues, there may be no lanes to render
      const lanes = page.locator('[data-testid^="swim-lane-lane-"]')
      await expect(lanes).toHaveCount(0)
    })

    test("rapid groupBy changes render correctly", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      const groupByFilter = page.getByTestId("groupby-filter")

      // Rapidly change groupBy options
      await groupByFilter.selectOption("epic")
      await groupByFilter.selectOption("priority")
      await groupByFilter.selectOption("assignee")

      // Final state should be assignee grouping
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "alice", exact: true })
      ).toBeVisible()

      // URL should reflect final selection
      expect(page.url()).toContain("groupBy=assignee")
    })
  })
})
