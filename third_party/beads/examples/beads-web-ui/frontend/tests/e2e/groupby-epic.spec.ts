import { test, expect, Page } from "@playwright/test"

/**
 * Mock issues for testing groupBy Epic swim lanes.
 * Includes multiple epics, issues with/without parent, varied statuses.
 */
const mockIssues = [
  // Epic One - 3 issues across different statuses
  {
    id: "epic-1-open",
    title: "Feature in Epic One (Open)",
    status: "open",
    priority: 2,
    issue_type: "feature",
    parent: "epic-1",
    parent_title: "Epic One: Authentication",
    assignee: "alice",
    created_at: "2026-01-27T10:00:00Z",
    updated_at: "2026-01-27T10:00:00Z",
  },
  {
    id: "epic-1-progress",
    title: "Task in Epic One (In Progress)",
    status: "in_progress",
    priority: 1,
    issue_type: "task",
    parent: "epic-1",
    parent_title: "Epic One: Authentication",
    assignee: "bob",
    created_at: "2026-01-27T11:00:00Z",
    updated_at: "2026-01-27T11:00:00Z",
  },
  {
    id: "epic-1-closed",
    title: "Bug in Epic One (Closed)",
    status: "closed",
    priority: 0,
    issue_type: "bug",
    parent: "epic-1",
    parent_title: "Epic One: Authentication",
    created_at: "2026-01-27T12:00:00Z",
    updated_at: "2026-01-27T12:00:00Z",
  },
  // Epic Two - 2 issues
  {
    id: "epic-2-open",
    title: "Feature in Epic Two",
    status: "open",
    priority: 2,
    issue_type: "feature",
    parent: "epic-2",
    parent_title: "Epic Two: Dashboard",
    created_at: "2026-01-27T13:00:00Z",
    updated_at: "2026-01-27T13:00:00Z",
  },
  {
    id: "epic-2-closed",
    title: "Task in Epic Two (Closed)",
    status: "closed",
    priority: 3,
    issue_type: "task",
    parent: "epic-2",
    parent_title: "Epic Two: Dashboard",
    created_at: "2026-01-27T14:00:00Z",
    updated_at: "2026-01-27T14:00:00Z",
  },
  // Orphan issues (no parent) - go to Ungrouped
  {
    id: "orphan-open",
    title: "Standalone Task (Open)",
    status: "open",
    priority: 4,
    issue_type: "task",
    created_at: "2026-01-27T15:00:00Z",
    updated_at: "2026-01-27T15:00:00Z",
  },
  {
    id: "orphan-closed",
    title: "Standalone Bug (Closed)",
    status: "closed",
    priority: 2,
    issue_type: "bug",
    created_at: "2026-01-27T16:00:00Z",
    updated_at: "2026-01-27T16:00:00Z",
  },
]
// Summary: Epic One = 3, Epic Two = 2, Ungrouped = 2 (total = 7)

/**
 * Set up API mocks for epic grouping tests.
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
 * Navigate to app with groupBy=epic and wait for API response.
 */
async function navigateToEpicView(page: Page) {
  const [response] = await Promise.all([
    page.waitForResponse(
      (res) => res.url().includes("/api/ready") && res.status() === 200
    ),
    page.goto("/?groupBy=epic"),
  ])
  expect(response.ok()).toBe(true)
  await expect(page.getByTestId("swim-lane-board")).toBeVisible()
}

/**
 * Get a specific epic lane by its parent ID.
 */
function getEpicLane(page: Page, epicId: string) {
  return page.getByTestId(`swim-lane-lane-epic-${epicId}`)
}

test.describe("groupBy Epic Swim Lanes", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page)
  })

  test.describe("Grouping - Issues Grouped by parent/epic Field", () => {
    test("issues are grouped into lanes by parent field", async ({ page }) => {
      await navigateToEpicView(page)

      // Verify 3 lanes exist: Epic One, Epic Two, Ungrouped
      const lanes = page.locator('[data-testid^="swim-lane-lane-epic"]')
      await expect(lanes).toHaveCount(3)

      // Verify Epic One has 3 issues
      const epicOneLane = getEpicLane(page, "epic-1")
      await expect(epicOneLane.locator("article")).toHaveCount(3)

      // Verify Epic Two has 2 issues
      const epicTwoLane = getEpicLane(page, "epic-2")
      await expect(epicTwoLane.locator("article")).toHaveCount(2)

      // Verify Ungrouped has 2 orphan issues
      const ungroupedLane = getEpicLane(page, "__ungrouped__")
      await expect(ungroupedLane.locator("article")).toHaveCount(2)
    })

    test("specific issues appear in their parent's lane", async ({ page }) => {
      await navigateToEpicView(page)

      // Verify Epic One contains its 3 issues
      const epicOneLane = getEpicLane(page, "epic-1")
      await expect(
        epicOneLane.getByText("Feature in Epic One (Open)")
      ).toBeVisible()
      await expect(
        epicOneLane.getByText("Task in Epic One (In Progress)")
      ).toBeVisible()
      await expect(
        epicOneLane.getByText("Bug in Epic One (Closed)")
      ).toBeVisible()

      // Verify Epic Two contains its 2 issues
      const epicTwoLane = getEpicLane(page, "epic-2")
      await expect(epicTwoLane.getByText("Feature in Epic Two")).toBeVisible()
      await expect(
        epicTwoLane.getByText("Task in Epic Two (Closed)")
      ).toBeVisible()
    })
  })

  test.describe("Lane Headers - Each Lane Shows Epic Title", () => {
    test("lane headers display parent_title", async ({ page }) => {
      await navigateToEpicView(page)

      // Verify lane titles show full parent_title (not just ID)
      await expect(
        page.getByRole("heading", { name: "Epic One: Authentication", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "Epic Two: Dashboard", exact: true })
      ).toBeVisible()
    })

    test("lane header falls back to parent ID when parent_title missing", async ({
      page,
    }) => {
      // Mock issues without parent_title
      const issuesWithoutTitle = [
        {
          id: "issue-no-title",
          title: "Issue Without Parent Title",
          status: "open",
          priority: 2,
          issue_type: "task",
          parent: "epic-fallback", // No parent_title field
          created_at: "2026-01-27T10:00:00Z",
          updated_at: "2026-01-27T10:00:00Z",
        },
      ]

      await setupMocks(page, issuesWithoutTitle)
      await navigateToEpicView(page)

      // Should show parent ID as title
      await expect(
        page.getByRole("heading", { name: "epic-fallback", exact: true })
      ).toBeVisible()
    })
  })

  test.describe("Ungrouped Lane - Issues Without parent", () => {
    test("orphan issues appear in Ungrouped lane", async ({ page }) => {
      await navigateToEpicView(page)

      const ungroupedLane = getEpicLane(page, "__ungrouped__")
      await expect(ungroupedLane).toBeVisible()

      // Verify orphan issues are in Ungrouped
      await expect(
        ungroupedLane.getByText("Standalone Task (Open)")
      ).toBeVisible()
      await expect(
        ungroupedLane.getByText("Standalone Bug (Closed)")
      ).toBeVisible()

      // Verify heading is "Ungrouped"
      await expect(
        ungroupedLane.getByRole("heading", { name: "Ungrouped", exact: true })
      ).toBeVisible()
    })

    test("Ungrouped lane appears at bottom", async ({ page }) => {
      await navigateToEpicView(page)

      // Get all lanes in order
      const lanes = page.locator('[data-testid^="swim-lane-lane-epic"]')
      const laneCount = await lanes.count()

      // Last lane should be Ungrouped
      const lastLane = lanes.nth(laneCount - 1)
      await expect(
        lastLane.getByRole("heading", { name: "Ungrouped", exact: true })
      ).toBeVisible()
    })

    test("all issues ungrouped creates single Ungrouped lane", async ({
      page,
    }) => {
      // All orphan issues
      const allOrphans = [
        {
          id: "orphan-1",
          title: "Orphan One",
          status: "open",
          priority: 2,
          issue_type: "task",
          created_at: "2026-01-27T10:00:00Z",
          updated_at: "2026-01-27T10:00:00Z",
        },
        {
          id: "orphan-2",
          title: "Orphan Two",
          status: "open",
          priority: 3,
          issue_type: "bug",
          created_at: "2026-01-27T11:00:00Z",
          updated_at: "2026-01-27T11:00:00Z",
        },
      ]

      await setupMocks(page, allOrphans)
      await navigateToEpicView(page)

      const lanes = page.locator('[data-testid^="swim-lane-lane-epic"]')
      await expect(lanes).toHaveCount(1)
      await expect(
        page.getByRole("heading", { name: "Ungrouped", exact: true })
      ).toBeVisible()
    })
  })

  test.describe("Issue Count - Lane Headers Show Correct Counts", () => {
    test("lane headers show correct issue counts", async ({ page }) => {
      await navigateToEpicView(page)

      // Epic One: 3 issues
      const epicOneLane = getEpicLane(page, "epic-1")
      await expect(epicOneLane.getByLabel("3 issues")).toBeVisible()

      // Epic Two: 2 issues
      const epicTwoLane = getEpicLane(page, "epic-2")
      await expect(epicTwoLane.getByLabel("2 issues")).toBeVisible()

      // Ungrouped: 2 issues
      const ungroupedLane = getEpicLane(page, "__ungrouped__")
      await expect(ungroupedLane.getByLabel("2 issues")).toBeVisible()
    })

    test("count remains visible when lane collapsed", async ({ page }) => {
      await navigateToEpicView(page)

      const epicOneLane = getEpicLane(page, "epic-1")
      const collapseToggle = epicOneLane.getByTestId("collapse-toggle")

      // Collapse the lane
      await collapseToggle.click()
      await expect(epicOneLane).toHaveAttribute("data-collapsed", "true")

      // Count should still be visible
      await expect(epicOneLane.getByLabel("3 issues")).toBeVisible()
    })
  })

  test.describe("Status Columns - Each Lane Has Status Columns", () => {
    test("each epic lane contains all status columns", async ({ page }) => {
      await navigateToEpicView(page)

      // Check Epic One lane
      const epicOneLane = getEpicLane(page, "epic-1")

      await expect(
        epicOneLane.locator('section[data-status="ready"]')
      ).toBeVisible()
      await expect(
        epicOneLane.locator('section[data-status="in_progress"]')
      ).toBeVisible()
      await expect(
        epicOneLane.locator('section[data-status="done"]')
      ).toBeVisible()
    })

    test("issues distributed by status within epic lane", async ({ page }) => {
      await navigateToEpicView(page)

      const epicOneLane = getEpicLane(page, "epic-1")

      // Open column: 1 issue
      await expect(
        epicOneLane.locator('section[data-status="ready"] article')
      ).toHaveCount(1)
      await expect(
        epicOneLane
          .locator('section[data-status="ready"]')
          .getByText("Feature in Epic One (Open)")
      ).toBeVisible()

      // In Progress column: 1 issue
      await expect(
        epicOneLane.locator('section[data-status="in_progress"] article')
      ).toHaveCount(1)
      await expect(
        epicOneLane
          .locator('section[data-status="in_progress"]')
          .getByText("Task in Epic One (In Progress)")
      ).toBeVisible()

      // Closed column: 1 issue
      await expect(
        epicOneLane.locator('section[data-status="done"] article')
      ).toHaveCount(1)
      await expect(
        epicOneLane
          .locator('section[data-status="done"]')
          .getByText("Bug in Epic One (Closed)")
      ).toBeVisible()
    })
  })

  test.describe("Drag and Drop", () => {
    test("drag issue changes status within epic lane", async ({ page }) => {
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

      await navigateToEpicView(page)

      const epicOneLane = getEpicLane(page, "epic-1")
      const openColumn = epicOneLane.locator('section[data-status="ready"]')
      const inProgressColumn = epicOneLane.locator(
        'section[data-status="in_progress"]'
      )

      // Get the card to drag
      const cardToDrag = openColumn
        .locator("article")
        .filter({ hasText: "Feature in Epic One" })
      await expect(cardToDrag).toBeVisible()

      // Perform drag operation
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

      await page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/epic-1-open") &&
          res.request().method() === "PATCH"
      )

      expect(patchCalls).toHaveLength(1)
      expect(patchCalls[0].body).toEqual({ status: "in_progress" })

      // Verify UI updated
      await expect(
        inProgressColumn.getByText("Feature in Epic One")
      ).toBeVisible()
    })

    test("drag between epic lanes changes status but not epic", async ({
      page,
    }) => {
      // Note: This test verifies that dragging from Epic One to Epic Two
      // changes STATUS but keeps the issue in Epic One (epic assignment
      // doesn't change via drag-drop - only status does)

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
          const body = route.request().postDataJSON() as Record<string, unknown>
          patchCalls.push({ url, body })

          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({
              success: true,
              data: { ...mockIssues[0], ...body },
            }),
          })
        } else {
          await route.continue()
        }
      })

      await navigateToEpicView(page)

      // Epic One open column
      const epicOneLane = getEpicLane(page, "epic-1")
      const epicOneOpen = epicOneLane.locator('section[data-status="ready"]')

      // Epic Two in_progress column
      const epicTwoLane = getEpicLane(page, "epic-2")
      const epicTwoInProgress = epicTwoLane.locator(
        'section[data-status="in_progress"]'
      )

      // Get the card to drag
      const cardToDrag = epicOneOpen
        .locator("article")
        .filter({ hasText: "Feature in Epic One" })
      await expect(cardToDrag).toBeVisible()

      const draggable = cardToDrag.locator("..")
      const dropTarget = epicTwoInProgress.locator(
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

      await page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/epic-1-open") &&
          res.request().method() === "PATCH"
      )

      // Verify ONLY status changed, NOT parent
      expect(patchCalls).toHaveLength(1)
      expect(patchCalls[0].body).toEqual({ status: "in_progress" })
      // Note: parent is NOT in the PATCH body - it remains "epic-1"
    })
  })

  test.describe("Edge Cases", () => {
    test("empty issues array shows no epic lanes", async ({ page }) => {
      await setupMocks(page, [])
      await page.goto("/?groupBy=epic")
      await page.waitForResponse((res) => res.url().includes("/api/ready"))

      await expect(page.getByTestId("swim-lane-board")).toBeVisible()
      const lanes = page.locator('[data-testid^="swim-lane-lane-epic"]')
      await expect(lanes).toHaveCount(0)
    })

    test("single epic with all issues shows one lane", async ({ page }) => {
      const singleEpicIssues = [
        {
          id: "issue-1",
          title: "Issue One",
          status: "open",
          priority: 2,
          issue_type: "task",
          parent: "epic-only",
          parent_title: "The Only Epic",
          created_at: "2026-01-27T10:00:00Z",
          updated_at: "2026-01-27T10:00:00Z",
        },
        {
          id: "issue-2",
          title: "Issue Two",
          status: "closed",
          priority: 3,
          issue_type: "bug",
          parent: "epic-only",
          parent_title: "The Only Epic",
          created_at: "2026-01-27T11:00:00Z",
          updated_at: "2026-01-27T11:00:00Z",
        },
      ]

      await setupMocks(page, singleEpicIssues)
      await navigateToEpicView(page)

      const lanes = page.locator('[data-testid^="swim-lane-lane-epic"]')
      await expect(lanes).toHaveCount(1)
      await expect(
        page.getByRole("heading", { name: "The Only Epic", exact: true })
      ).toBeVisible()
    })

    test("epic lanes sorted alphabetically by title", async ({ page }) => {
      await navigateToEpicView(page)

      // Get all lanes in order
      const lanes = page.locator('[data-testid^="swim-lane-lane-epic"]')
      const laneCount = await lanes.count()

      // Get titles in order (excluding Ungrouped at end)
      const titles: string[] = []
      for (let i = 0; i < laneCount - 1; i++) {
        const lane = lanes.nth(i)
        // Use first() to get only the lane heading, not card headings
        const heading = lane.getByRole("heading").first()
        const title = await heading.textContent()
        if (title) titles.push(title)
      }

      // Verify alphabetical order (Ungrouped excluded from this check)
      const sortedTitles = [...titles].sort()
      expect(titles).toEqual(sortedTitles)
    })

    test("switching from epic to assignee regroups issues", async ({
      page,
    }) => {
      await navigateToEpicView(page)

      // Verify epic lanes exist
      await expect(
        page.getByRole("heading", { name: "Epic One: Authentication", exact: true })
      ).toBeVisible()

      // Switch to assignee grouping
      await page.getByTestId("groupby-filter").selectOption("assignee")

      // Epic lanes should be gone
      await expect(
        page.getByRole("heading", { name: "Epic One: Authentication", exact: true })
      ).not.toBeVisible()

      // Assignee lanes should appear
      await expect(
        page.getByRole("heading", { name: "alice", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "bob", exact: true })
      ).toBeVisible()
    })

    test("URL with groupBy=epic shows epic swim lanes on load", async ({
      page,
    }) => {
      await setupMocks(page)
      await page.goto("/?groupBy=epic")
      await page.waitForResponse((res) => res.url().includes("/api/ready"))

      // Verify swim lanes visible
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()

      // Verify dropdown shows epic selected
      await expect(page.getByTestId("groupby-filter")).toHaveValue("epic")

      // Verify epic lanes present
      await expect(
        page.getByRole("heading", { name: "Epic One: Authentication", exact: true })
      ).toBeVisible()
    })
  })

  test.describe("Collapse/Expand Behavior", () => {
    test("click collapse button hides lane content", async ({ page }) => {
      await navigateToEpicView(page)

      const epicOneLane = getEpicLane(page, "epic-1")
      await expect(epicOneLane).toBeVisible()

      // Verify initially expanded
      await expect(epicOneLane).toHaveAttribute("data-collapsed", "false")

      // Click collapse toggle
      const collapseToggle = epicOneLane.getByTestId("collapse-toggle")
      await collapseToggle.click()

      // Verify lane is collapsed
      await expect(epicOneLane).toHaveAttribute("data-collapsed", "true")

      // Lane content div should be hidden
      const laneContent = epicOneLane.locator('[data-collapsed="true"]')
      await expect(laneContent).toHaveAttribute("aria-hidden", "true")
    })

    test("expand collapsed lane shows content", async ({ page }) => {
      await navigateToEpicView(page)

      const epicOneLane = getEpicLane(page, "epic-1")

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
  })
})
