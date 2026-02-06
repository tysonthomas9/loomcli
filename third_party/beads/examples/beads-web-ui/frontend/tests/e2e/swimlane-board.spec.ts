import { test, expect, Page } from "@playwright/test"

/**
 * Mock issues for testing SwimLaneBoard component.
 * Includes varied fields for testing all groupBy options.
 */
const mockIssues = [
  // Epic grouping - issues with parent
  {
    id: "epic-issue-1",
    title: "Feature in Epic One",
    status: "open",
    priority: 2,
    issue_type: "feature",
    parent: "epic-1",
    parent_title: "Epic One",
    assignee: "alice",
    labels: ["frontend"],
    created_at: "2026-01-27T10:00:00Z",
    updated_at: "2026-01-27T10:00:00Z",
  },
  {
    id: "epic-issue-2",
    title: "Bug in Epic One",
    status: "in_progress",
    priority: 1,
    issue_type: "bug",
    parent: "epic-1",
    parent_title: "Epic One",
    assignee: "bob",
    labels: ["urgent"],
    created_at: "2026-01-27T11:00:00Z",
    updated_at: "2026-01-27T11:00:00Z",
  },
  {
    id: "epic-issue-3",
    title: "Task in Epic Two",
    status: "open",
    priority: 0,
    issue_type: "task",
    parent: "epic-2",
    parent_title: "Epic Two",
    assignee: "alice",
    labels: ["backend"],
    created_at: "2026-01-27T12:00:00Z",
    updated_at: "2026-01-27T12:00:00Z",
  },
  // Orphan issue (no parent)
  {
    id: "orphan-issue",
    title: "Standalone Task",
    status: "closed",
    priority: 3,
    issue_type: "task",
    labels: [],
    created_at: "2026-01-27T13:00:00Z",
    updated_at: "2026-01-27T13:00:00Z",
  },
  // Unassigned issue
  {
    id: "unassigned-issue",
    title: "Unassigned Bug",
    status: "open",
    priority: 4,
    issue_type: "bug",
    labels: ["frontend", "urgent"],
    created_at: "2026-01-27T14:00:00Z",
    updated_at: "2026-01-27T14:00:00Z",
  },
]

/**
 * Set up API mocks for SwimLaneBoard tests.
 */
async function setupMocks(page: Page, issues: object[] = mockIssues) {
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
      const issueId = url.split("/").pop()
      const body = route.request().postDataJSON() as { status?: string }
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
 * Navigate to app and wait for API response.
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

/**
 * Select a groupBy option and wait for lanes to render.
 */
async function selectGroupBy(page: Page, value: string) {
  await page.getByTestId("groupby-filter").selectOption(value)
  if (value === "none") {
    // For 'none', swim lane board should not be visible
    await expect(page.getByTestId("swim-lane-board")).not.toBeVisible()
  } else {
    // For other values, swim lane board should be visible
    await expect(page.getByTestId("swim-lane-board")).toBeVisible()
  }
}

/**
 * Get all swim lane sections on the page.
 */
function getLanes(page: Page) {
  return page.locator('[data-testid^="swim-lane-lane-"]')
}

test.describe("SwimLaneBoard", () => {
  test.describe("Flat Fallback (groupBy=none)", () => {
    test('groupBy="none" shows flat KanbanBoard without swim lanes', async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      // Default groupBy is 'none' - verify no swim lane board
      await expect(page.getByTestId("swim-lane-board")).not.toBeVisible()

      // Verify flat Kanban columns are visible
      const openColumn = page.locator('section[data-status="ready"]')
      const inProgressColumn = page.locator('section[data-status="in_progress"]')
      const closedColumn = page.locator('section[data-status="done"]')

      await expect(openColumn).toBeVisible()
      await expect(inProgressColumn).toBeVisible()
      await expect(closedColumn).toBeVisible()

      // Verify issues distributed in flat columns (3 open, 1 in_progress, 1 closed)
      await expect(openColumn.locator("article")).toHaveCount(3)
      await expect(inProgressColumn.locator("article")).toHaveCount(1)
      await expect(closedColumn.locator("article")).toHaveCount(1)
    })
  })

  test.describe("Epic Grouping", () => {
    test("grouping by epic creates lanes for each parent", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      // Select groupBy epic
      await selectGroupBy(page, "epic")

      // Verify swim lanes exist
      const lanes = getLanes(page)
      await expect(lanes).toHaveCount(3) // Epic One, Epic Two, Ungrouped

      // Verify lane titles show parent_title (use exact match with heading role)
      await expect(
        page.getByRole("heading", { name: "Epic One", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "Epic Two", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "Ungrouped", exact: true })
      ).toBeVisible()

      // Verify issues in Epic One lane (2 issues)
      const epicOneLane = page.getByTestId("swim-lane-lane-epic-epic-1")
      await expect(epicOneLane).toBeVisible()
      await expect(epicOneLane.locator("article")).toHaveCount(2)

      // Verify issues in Epic Two lane (1 issue)
      const epicTwoLane = page.getByTestId("swim-lane-lane-epic-epic-2")
      await expect(epicTwoLane).toBeVisible()
      await expect(epicTwoLane.locator("article")).toHaveCount(1)

      // Verify Ungrouped lane (2 issues - orphan and unassigned which have no parent)
      const ungroupedLane = page.getByTestId("swim-lane-lane-epic-__ungrouped__")
      await expect(ungroupedLane).toBeVisible()
      await expect(ungroupedLane.locator("article")).toHaveCount(2)
    })
  })

  test.describe("Assignee Grouping", () => {
    test("grouping by assignee creates lanes for each user", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await selectGroupBy(page, "assignee")

      // Verify lanes for alice, bob, and Unassigned
      const lanes = getLanes(page)
      await expect(lanes).toHaveCount(3)

      await expect(
        page.getByRole("heading", { name: "alice", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "bob", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "Unassigned", exact: true })
      ).toBeVisible()

      // Verify issue counts
      // alice: 2 issues (epic-issue-1, epic-issue-3)
      const aliceLane = page.getByTestId("swim-lane-lane-assignee-alice")
      await expect(aliceLane.locator("article")).toHaveCount(2)

      // bob: 1 issue (epic-issue-2)
      const bobLane = page.getByTestId("swim-lane-lane-assignee-bob")
      await expect(bobLane.locator("article")).toHaveCount(1)

      // Unassigned: 2 issues (orphan-issue, unassigned-issue)
      const unassignedLane = page.getByTestId(
        "swim-lane-lane-assignee-__unassigned__"
      )
      await expect(unassignedLane.locator("article")).toHaveCount(2)
    })
  })

  test.describe("Priority Grouping", () => {
    test("grouping by priority creates lanes P0-P4", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await selectGroupBy(page, "priority")

      // Verify lanes show priority labels (use heading role to avoid matching filter options)
      await expect(
        page.getByRole("heading", { name: "P0 (Critical)" })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "P1 (High)" })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "P2 (Medium)" })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "P3 (Normal)" })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "P4 (Backlog)" })
      ).toBeVisible()

      // Verify issue distribution
      const p0Lane = page.getByTestId("swim-lane-lane-priority-0")
      await expect(p0Lane.locator("article")).toHaveCount(1) // epic-issue-3

      const p1Lane = page.getByTestId("swim-lane-lane-priority-1")
      await expect(p1Lane.locator("article")).toHaveCount(1) // epic-issue-2

      const p2Lane = page.getByTestId("swim-lane-lane-priority-2")
      await expect(p2Lane.locator("article")).toHaveCount(1) // epic-issue-1

      const p3Lane = page.getByTestId("swim-lane-lane-priority-3")
      await expect(p3Lane.locator("article")).toHaveCount(1) // orphan-issue

      const p4Lane = page.getByTestId("swim-lane-lane-priority-4")
      await expect(p4Lane.locator("article")).toHaveCount(1) // unassigned-issue
    })
  })

  test.describe("Type Grouping", () => {
    test("grouping by type creates lanes for each issue_type", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await selectGroupBy(page, "type")

      // Verify lanes (capitalized type names - use heading role)
      await expect(
        page.getByRole("heading", { name: "Feature", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "Bug", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "Task", exact: true })
      ).toBeVisible()

      // Verify issue counts
      const featureLane = page.getByTestId("swim-lane-lane-type-feature")
      await expect(featureLane.locator("article")).toHaveCount(1)

      const bugLane = page.getByTestId("swim-lane-lane-type-bug")
      await expect(bugLane.locator("article")).toHaveCount(2)

      const taskLane = page.getByTestId("swim-lane-lane-type-task")
      await expect(taskLane.locator("article")).toHaveCount(2)
    })

    test("issues without issue_type go to No Type lane", async ({ page }) => {
      // Issue without issue_type field
      const issuesWithoutType = [
        {
          id: "no-type-issue",
          title: "Issue Without Type",
          status: "open",
          priority: 2,
          // No issue_type field
          created_at: "2026-01-27T15:00:00Z",
          updated_at: "2026-01-27T15:00:00Z",
        },
        ...mockIssues,
      ]

      await setupMocks(page, issuesWithoutType)
      await navigateAndWait(page)

      await selectGroupBy(page, "type")

      // Verify "No Type" lane appears
      await expect(
        page.getByRole("heading", { name: "No Type", exact: true })
      ).toBeVisible()

      // Verify the no-type issue is in the No Type lane
      const noTypeLane = page.getByTestId("swim-lane-lane-type-__no_type__")
      await expect(noTypeLane.locator("article")).toHaveCount(1)
      await expect(noTypeLane.getByText("Issue Without Type")).toBeVisible()
    })
  })

  test.describe("Label Grouping", () => {
    test("grouping by label creates lanes for each label", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await selectGroupBy(page, "label")

      // Verify lanes for each label (use heading role)
      await expect(
        page.getByRole("heading", { name: "frontend", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "urgent", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "backend", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "No Labels", exact: true })
      ).toBeVisible()
    })

    test("issue with multiple labels appears in multiple lanes", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await selectGroupBy(page, "label")

      // unassigned-issue has labels: ["frontend", "urgent"]
      // It should appear in both lanes
      const frontendLane = page.getByTestId("swim-lane-lane-label-frontend")
      const urgentLane = page.getByTestId("swim-lane-lane-label-urgent")

      // Both lanes should contain "Unassigned Bug"
      await expect(frontendLane.getByText("Unassigned Bug")).toBeVisible()
      await expect(urgentLane.getByText("Unassigned Bug")).toBeVisible()
    })
  })

  test.describe("Ungrouped/Special Lanes", () => {
    test("ungrouped lanes appear at bottom", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await selectGroupBy(page, "epic")

      // Get all lanes in order
      const lanes = getLanes(page)
      const laneCount = await lanes.count()

      // The last lane should be "Ungrouped"
      const lastLane = lanes.nth(laneCount - 1)
      await expect(
        lastLane.getByRole("heading", { name: "Ungrouped", exact: true })
      ).toBeVisible()
    })
  })

  test.describe("Lane Count Display", () => {
    test("lane headers show correct issue counts", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await selectGroupBy(page, "priority")

      // Check counts using aria-label
      const p0Lane = page.getByTestId("swim-lane-lane-priority-0")
      await expect(p0Lane.getByLabel("1 issues")).toBeVisible()

      const p2Lane = page.getByTestId("swim-lane-lane-priority-2")
      await expect(p2Lane.getByLabel("1 issues")).toBeVisible()
    })
  })

  test.describe("Status Columns Within Lanes", () => {
    test("each lane contains status columns", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await selectGroupBy(page, "epic")

      // Epic One lane should have all three status columns
      const epicOneLane = page.getByTestId("swim-lane-lane-epic-epic-1")

      await expect(
        epicOneLane.locator('section[data-status="ready"]')
      ).toBeVisible()
      await expect(
        epicOneLane.locator('section[data-status="in_progress"]')
      ).toBeVisible()
      await expect(
        epicOneLane.locator('section[data-status="done"]')
      ).toBeVisible()

      // Verify issues distributed by status within lane
      await expect(
        epicOneLane.locator('section[data-status="ready"] article')
      ).toHaveCount(1)
      await expect(
        epicOneLane.locator('section[data-status="in_progress"] article')
      ).toHaveCount(1)
    })
  })

  test.describe("Collapse/Expand Behavior", () => {
    test("click collapse button hides lane content", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await selectGroupBy(page, "epic")

      // Get Epic One lane
      const epicOneLane = page.getByTestId("swim-lane-lane-epic-epic-1")
      await expect(epicOneLane).toBeVisible()

      // Verify initially expanded
      await expect(epicOneLane).toHaveAttribute("data-collapsed", "false")

      // Click collapse toggle
      const collapseToggle = epicOneLane.getByTestId("collapse-toggle")
      await collapseToggle.click()

      // Verify lane is collapsed
      await expect(epicOneLane).toHaveAttribute("data-collapsed", "true")

      // Lane content div should be hidden (aria-hidden) - use data-collapsed to target the content div
      const laneContent = epicOneLane.locator('[data-collapsed="true"]')
      await expect(laneContent).toHaveAttribute("aria-hidden", "true")
    })

    test("expand collapsed lane shows content", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await selectGroupBy(page, "epic")

      const epicOneLane = page.getByTestId("swim-lane-lane-epic-epic-1")

      // Collapse first
      const collapseToggle = epicOneLane.getByTestId("collapse-toggle")
      await collapseToggle.click()
      await expect(epicOneLane).toHaveAttribute("data-collapsed", "true")

      // Click toggle again to expand
      await collapseToggle.click()
      await expect(epicOneLane).toHaveAttribute("data-collapsed", "false")

      // Content should be visible again
      const laneContent = epicOneLane.locator('[aria-hidden="false"]')
      await expect(laneContent).toBeVisible()
    })

    test("collapsed lane count remains accurate", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await selectGroupBy(page, "epic")

      const epicOneLane = page.getByTestId("swim-lane-lane-epic-epic-1")

      // Collapse the lane
      const collapseToggle = epicOneLane.getByTestId("collapse-toggle")
      await collapseToggle.click()

      // Count badge should still show correct number (2 issues)
      await expect(epicOneLane.getByLabel("2 issues")).toBeVisible()
    })
  })

  test.describe("Cross-Lane Drag and Drop", () => {
    test("drag issue within same lane changes status", async ({ page }) => {
      // Track API calls - inline mock setup needed for custom PATCH tracking
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
              data: {
                ...mockIssues[0],
                status: body.status,
                updated_at: new Date().toISOString(),
              },
            }),
          })
        } else {
          await route.continue()
        }
      })

      await navigateAndWait(page)
      await selectGroupBy(page, "epic")

      // Get Epic One lane
      const epicOneLane = page.getByTestId("swim-lane-lane-epic-epic-1")
      await expect(epicOneLane).toBeVisible()

      // Find the open column and in_progress column within the lane
      const openColumn = epicOneLane.locator('section[data-status="ready"]')
      const inProgressColumn = epicOneLane.locator(
        'section[data-status="in_progress"]'
      )

      // Verify initial state
      await expect(openColumn.locator("article")).toHaveCount(1)
      await expect(inProgressColumn.locator("article")).toHaveCount(1)

      // Get the card to drag (Feature in Epic One is in open)
      const cardToDrag = openColumn
        .locator("article")
        .filter({ hasText: "Feature in Epic One" })
      await expect(cardToDrag).toBeVisible()

      // Get the draggable wrapper
      const draggable = cardToDrag.locator("..")
      const dropTarget = inProgressColumn.locator(
        '[data-droppable-id="in_progress"]'
      )

      // Get positions for the drag operation
      const sourceBox = await draggable.boundingBox()
      const targetBox = await dropTarget.boundingBox()

      if (!sourceBox || !targetBox) {
        throw new Error("Could not get bounding boxes")
      }

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

      // Move past activation threshold
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

      // Move to target
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

      // Release at target
      await page.dispatchEvent("body", "pointerup", {
        clientX: endX,
        clientY: endY,
        button: 0,
        buttons: 0,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      })

      // Wait for the PATCH API call
      await page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/epic-issue-1") &&
          res.request().method() === "PATCH"
      )

      // Verify API call was made correctly
      expect(patchCalls).toHaveLength(1)
      expect(patchCalls[0].body).toEqual({ status: "in_progress" })

      // Verify UI updated (card moved to In Progress)
      await expect(
        inProgressColumn.getByText("Feature in Epic One")
      ).toBeVisible()
    })
  })

  test.describe("URL Synchronization", () => {
    test("groupBy persists in URL", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      // Select priority grouping
      await selectGroupBy(page, "priority")

      // Verify URL contains groupBy
      await expect(async () => {
        expect(page.url()).toContain("groupBy=priority")
      }).toPass({ timeout: 2000 })

      // Set up mocks again before reload (route handlers are cleared on reload)
      await setupMocks(page)

      // Reload page
      await page.reload()
      await page.waitForResponse((res) => res.url().includes("/api/ready"))

      // Verify still grouped by priority
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "P0 (Critical)" })
      ).toBeVisible()
    })

    test("navigate with groupBy URL param shows swim lanes", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=assignee")

      // Verify swim lanes grouped by assignee
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "alice", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "bob", exact: true })
      ).toBeVisible()

      // Verify dropdown shows correct selection
      const groupByFilter = page.getByTestId("groupby-filter")
      await expect(groupByFilter).toHaveValue("assignee")
    })
  })

  test.describe("Edge Cases", () => {
    test("empty issues array shows no lanes", async ({ page }) => {
      await setupMocks(page, [])
      await navigateAndWait(page)

      await selectGroupBy(page, "epic")

      // No lanes should exist
      const lanes = getLanes(page)
      await expect(lanes).toHaveCount(0)
    })

    test("all issues ungrouped creates single Ungrouped lane", async ({
      page,
    }) => {
      // Mock issues all without parent
      const orphanIssues = [
        {
          id: "orphan-1",
          title: "Orphan Issue 1",
          status: "open",
          priority: 2,
          issue_type: "task",
          created_at: "2026-01-27T10:00:00Z",
          updated_at: "2026-01-27T10:00:00Z",
        },
        {
          id: "orphan-2",
          title: "Orphan Issue 2",
          status: "open",
          priority: 2,
          issue_type: "task",
          created_at: "2026-01-27T11:00:00Z",
          updated_at: "2026-01-27T11:00:00Z",
        },
      ]

      await setupMocks(page, orphanIssues)
      await navigateAndWait(page)

      await selectGroupBy(page, "epic")

      // Only Ungrouped lane should exist
      const lanes = getLanes(page)
      await expect(lanes).toHaveCount(1)
      await expect(
        page.getByRole("heading", { name: "Ungrouped", exact: true })
      ).toBeVisible()
    })

    test("switching groupBy re-renders lanes", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      // Start with epic grouping
      await selectGroupBy(page, "epic")
      // Verify Epic One lane exists
      await expect(
        page.getByRole("heading", { name: "Epic One", exact: true })
      ).toBeVisible()

      // Switch to priority grouping
      await selectGroupBy(page, "priority")

      // Epic lanes should be gone (lane titles with Epic One)
      await expect(
        page.getByRole("heading", { name: "Epic One", exact: true })
      ).not.toBeVisible()

      // Priority lanes should appear
      await expect(
        page.getByRole("heading", { name: "P0 (Critical)" })
      ).toBeVisible()
    })
  })
})
