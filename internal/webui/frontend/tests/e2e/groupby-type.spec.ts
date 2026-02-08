import { test, expect, Page } from "@playwright/test"

/**
 * E2E tests for groupBy Type swim lanes.
 *
 * Tests comprehensive coverage of type-based swim lane grouping including:
 * - All five known issue types (bug, feature, task, epic, chore)
 * - Lane headers with capitalized type names
 * - Issue counts in lane headers
 * - "No Type" lane for issues without issue_type
 * - Status columns within type lanes
 * - Drag-and-drop status changes within type lanes
 * - Collapse/expand functionality
 * - Edge cases and filter combinations
 */

// Common timestamp fields for test issues
const timestamps = {
  created_at: "2026-01-27T10:00:00Z",
  updated_at: "2026-01-27T10:00:00Z",
}

/**
 * Mock issues covering all five known issue types plus one without type.
 * Distribution:
 * - Bug: 2 issues (bug-1, bug-2)
 * - Feature: 1 issue (feature-1)
 * - Task: 2 issues (task-1, task-2)
 * - Epic: 1 issue (epic-1)
 * - Chore: 1 issue (chore-1)
 * - No Type: 1 issue (no-type-1)
 * Total: 8 issues
 */
const mockIssuesAllTypes = [
  // Bug issues
  {
    id: "bug-1",
    title: "Critical Login Bug",
    status: "open",
    priority: 0,
    issue_type: "bug",
    ...timestamps,
  },
  {
    id: "bug-2",
    title: "Minor UI Bug",
    status: "in_progress",
    priority: 2,
    issue_type: "bug",
    created_at: "2026-01-27T10:01:00Z",
    updated_at: "2026-01-27T10:01:00Z",
  },
  // Feature issues
  {
    id: "feature-1",
    title: "Dark Mode Feature",
    status: "open",
    priority: 2,
    issue_type: "feature",
    created_at: "2026-01-27T10:02:00Z",
    updated_at: "2026-01-27T10:02:00Z",
  },
  // Task issues
  {
    id: "task-1",
    title: "Update Dependencies",
    status: "closed",
    priority: 3,
    issue_type: "task",
    created_at: "2026-01-27T10:03:00Z",
    updated_at: "2026-01-27T10:03:00Z",
  },
  {
    id: "task-2",
    title: "Write Documentation",
    status: "open",
    priority: 3,
    issue_type: "task",
    created_at: "2026-01-27T10:04:00Z",
    updated_at: "2026-01-27T10:04:00Z",
  },
  // Epic issues
  {
    id: "epic-1",
    title: "Q1 Roadmap Epic",
    status: "open",
    priority: 1,
    issue_type: "epic",
    created_at: "2026-01-27T10:05:00Z",
    updated_at: "2026-01-27T10:05:00Z",
  },
  // Chore issues
  {
    id: "chore-1",
    title: "Clean Up Old Branches",
    status: "in_progress",
    priority: 4,
    issue_type: "chore",
    created_at: "2026-01-27T10:06:00Z",
    updated_at: "2026-01-27T10:06:00Z",
  },
  // No type issue
  {
    id: "no-type-1",
    title: "Untyped Issue",
    status: "open",
    priority: 2,
    // No issue_type field
    created_at: "2026-01-27T10:07:00Z",
    updated_at: "2026-01-27T10:07:00Z",
  },
]

/**
 * Set up API mocks for tests.
 */
async function setupMocks(page: Page, issues: object[] = mockIssuesAllTypes) {
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
 * Set up API mocks with PATCH call tracking.
 */
async function setupMocksWithPatch(
  page: Page,
  patchCalls: { url: string; body: object }[],
  issues: object[] = mockIssuesAllTypes
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
 * Navigate to app and wait for API response.
 */
async function navigateAndWait(page: Page, path = "/?groupBy=type") {
  const [response] = await Promise.all([
    page.waitForResponse(
      (res) => res.url().includes("/api/ready") && res.status() === 200
    ),
    page.goto(path),
  ])
  expect(response.ok()).toBe(true)
  await expect(page.getByTestId("swim-lane-board")).toBeVisible()
}

/**
 * Get all swim lane sections on the page.
 */
function getLanes(page: Page) {
  return page.locator('[data-testid^="swim-lane-lane-type-"]')
}

test.describe("groupBy Type Swim Lanes", () => {
  test.describe("Basic Type Grouping", () => {
    test("grouping by type creates lanes for each issue_type", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      // Verify 6 lanes exist: Bug, Feature, Task, Epic, Chore, No Type
      const lanes = getLanes(page)
      await expect(lanes).toHaveCount(6)

      // Verify swim-lane-board is visible
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()
    })

    test("all five known types get their own lanes", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      // Verify each type has a lane with capitalized heading
      await expect(
        page.getByRole("heading", { name: "Bug", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "Feature", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "Task", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "Epic", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "Chore", exact: true })
      ).toBeVisible()
    })
  })

  test.describe("Lane Headers", () => {
    test("lane headers show capitalized type names", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      // Verify "Bug" not "bug"
      await expect(
        page.getByRole("heading", { name: "Bug", exact: true })
      ).toBeVisible()
      // Verify "Feature" not "feature"
      await expect(
        page.getByRole("heading", { name: "Feature", exact: true })
      ).toBeVisible()
      // Verify "Task", "Epic", "Chore"
      await expect(
        page.getByRole("heading", { name: "Task", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "Epic", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "Chore", exact: true })
      ).toBeVisible()
    })

    test("lane headers show correct issue counts", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      // Bug lane: 2 issues (bug-1, bug-2)
      const bugLane = page.getByTestId("swim-lane-lane-type-bug")
      await expect(bugLane.getByLabel("2 issues")).toBeVisible()

      // Feature lane: 1 issue
      const featureLane = page.getByTestId("swim-lane-lane-type-feature")
      await expect(featureLane.getByLabel("1 issues")).toBeVisible()

      // Task lane: 2 issues
      const taskLane = page.getByTestId("swim-lane-lane-type-task")
      await expect(taskLane.getByLabel("2 issues")).toBeVisible()

      // Epic lane: 1 issue
      const epicLane = page.getByTestId("swim-lane-lane-type-epic")
      await expect(epicLane.getByLabel("1 issues")).toBeVisible()

      // Chore lane: 1 issue
      const choreLane = page.getByTestId("swim-lane-lane-type-chore")
      await expect(choreLane.getByLabel("1 issues")).toBeVisible()

      // No Type lane: 1 issue
      const noTypeLane = page.getByTestId("swim-lane-lane-type-__no_type__")
      await expect(noTypeLane.getByLabel("1 issues")).toBeVisible()
    })
  })

  test.describe("No Type Lane", () => {
    test("issues without issue_type go to No Type lane", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      // Verify "No Type" heading exists
      await expect(
        page.getByRole("heading", { name: "No Type", exact: true })
      ).toBeVisible()

      // Verify lane ID is correct
      const noTypeLane = page.getByTestId("swim-lane-lane-type-__no_type__")
      await expect(noTypeLane).toBeVisible()

      // Verify the untyped issue appears in this lane
      await expect(noTypeLane.getByText("Untyped Issue")).toBeVisible()
    })

    test("No Type lane appears at bottom (special lane sorting)", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      // Get all lanes, verify last lane is "No Type"
      const lanes = getLanes(page)
      const count = await lanes.count()

      const lastLane = lanes.nth(count - 1)
      await expect(
        lastLane.getByRole("heading", { name: "No Type", exact: true })
      ).toBeVisible()
    })
  })

  test.describe("Issue Distribution", () => {
    test("issues correctly distributed to type lanes", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      // Bug lane has bug-1 and bug-2
      const bugLane = page.getByTestId("swim-lane-lane-type-bug")
      await expect(bugLane.locator("article")).toHaveCount(2)
      await expect(bugLane.getByText("Critical Login Bug")).toBeVisible()
      await expect(bugLane.getByText("Minor UI Bug")).toBeVisible()

      // Feature lane has feature-1
      const featureLane = page.getByTestId("swim-lane-lane-type-feature")
      await expect(featureLane.locator("article")).toHaveCount(1)
      await expect(featureLane.getByText("Dark Mode Feature")).toBeVisible()

      // Task lane has task-1 and task-2
      const taskLane = page.getByTestId("swim-lane-lane-type-task")
      await expect(taskLane.locator("article")).toHaveCount(2)
      await expect(taskLane.getByText("Update Dependencies")).toBeVisible()
      await expect(taskLane.getByText("Write Documentation")).toBeVisible()

      // Epic lane has epic-1
      const epicLane = page.getByTestId("swim-lane-lane-type-epic")
      await expect(epicLane.locator("article")).toHaveCount(1)
      await expect(epicLane.getByText("Q1 Roadmap Epic")).toBeVisible()

      // Chore lane has chore-1
      const choreLane = page.getByTestId("swim-lane-lane-type-chore")
      await expect(choreLane.locator("article")).toHaveCount(1)
      await expect(choreLane.getByText("Clean Up Old Branches")).toBeVisible()
    })
  })

  test.describe("Status Columns Within Type Lanes", () => {
    test("each type lane contains status columns", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      // Verify Bug lane has open, in_progress, closed columns
      const bugLane = page.getByTestId("swim-lane-lane-type-bug")
      await expect(
        bugLane.locator('section[data-status="ready"]')
      ).toBeVisible()
      await expect(
        bugLane.locator('section[data-status="in_progress"]')
      ).toBeVisible()
      await expect(
        bugLane.locator('section[data-status="done"]')
      ).toBeVisible()
    })

    test("issue status matches column within type lane", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      // bug-1 (status: open) in open column
      const bugLane = page.getByTestId("swim-lane-lane-type-bug")
      const openColumn = bugLane.locator('section[data-status="ready"]')
      await expect(openColumn.getByText("Critical Login Bug")).toBeVisible()

      // bug-2 (status: in_progress) in in_progress column
      const inProgressColumn = bugLane.locator(
        'section[data-status="in_progress"]'
      )
      await expect(inProgressColumn.getByText("Minor UI Bug")).toBeVisible()
    })
  })

  test.describe("Drag and Drop Status Changes", () => {
    test("drag within type lane changes status", async ({ page }) => {
      const patchCalls: { url: string; body: object }[] = []
      await setupMocksWithPatch(page, patchCalls)
      await navigateAndWait(page)

      const bugLane = page.getByTestId("swim-lane-lane-type-bug")
      const openColumn = bugLane.locator('section[data-status="ready"]')
      const inProgressColumn = bugLane.locator(
        'section[data-status="in_progress"]'
      )

      // Get card to drag (Critical Login Bug is open)
      const card = openColumn
        .locator("article")
        .filter({ hasText: "Critical Login Bug" })
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
          res.url().includes("/api/issues/bug-1") &&
          res.request().method() === "PATCH"
      )

      // Verify ONLY status changed, not type
      expect(patchCalls).toHaveLength(1)
      expect(patchCalls[0].body).toEqual({ status: "in_progress" })
      expect(patchCalls[0].body).not.toHaveProperty("issue_type")

      // Verify UI updated
      await expect(
        inProgressColumn.getByText("Critical Login Bug")
      ).toBeVisible()
    })
  })

  test.describe("Collapse/Expand", () => {
    test("collapsing type lane hides content", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      const bugLane = page.getByTestId("swim-lane-lane-type-bug")
      await expect(bugLane).toHaveAttribute("data-collapsed", "false")

      // Click collapse toggle
      await bugLane.getByTestId("collapse-toggle").click()
      await expect(bugLane).toHaveAttribute("data-collapsed", "true")

      // Lane content should be hidden (aria-hidden)
      const laneContent = bugLane.locator('[data-collapsed="true"]')
      await expect(laneContent).toHaveAttribute("aria-hidden", "true")
    })

    test("expand collapsed type lane shows content", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      const bugLane = page.getByTestId("swim-lane-lane-type-bug")

      // Collapse first
      const collapseToggle = bugLane.getByTestId("collapse-toggle")
      await collapseToggle.click()
      await expect(bugLane).toHaveAttribute("data-collapsed", "true")

      // Click toggle again to expand
      await collapseToggle.click()
      await expect(bugLane).toHaveAttribute("data-collapsed", "false")

      // Content should be visible again
      const laneContent = bugLane.locator('[aria-hidden="false"]')
      await expect(laneContent).toBeVisible()
    })

    test("collapsed lane still shows count badge", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      const bugLane = page.getByTestId("swim-lane-lane-type-bug")
      await bugLane.getByTestId("collapse-toggle").click()

      // Count badge should still show correct number (2 issues)
      await expect(bugLane.getByLabel("2 issues")).toBeVisible()
    })
  })

  test.describe("Edge Cases", () => {
    test("empty type has no lane (only types with issues get lanes)", async ({
      page,
    }) => {
      // Mock issues with only bugs and tasks (no feature, epic, chore)
      const partialTypeIssues = [
        {
          id: "bug-only",
          title: "Bug Only",
          status: "open",
          priority: 2,
          issue_type: "bug",
          ...timestamps,
        },
        {
          id: "task-only",
          title: "Task Only",
          status: "open",
          priority: 2,
          issue_type: "task",
          ...timestamps,
        },
      ]

      await setupMocks(page, partialTypeIssues)
      await navigateAndWait(page)

      // Only Bug and Task lanes should exist
      const lanes = getLanes(page)
      await expect(lanes).toHaveCount(2)

      await expect(
        page.getByRole("heading", { name: "Bug", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "Task", exact: true })
      ).toBeVisible()

      // No Feature, Epic, Chore lanes
      await expect(
        page.getByRole("heading", { name: "Feature", exact: true })
      ).not.toBeVisible()
      await expect(
        page.getByRole("heading", { name: "Epic", exact: true })
      ).not.toBeVisible()
      await expect(
        page.getByRole("heading", { name: "Chore", exact: true })
      ).not.toBeVisible()
    })

    test("switching from type to another groupBy re-renders lanes", async ({
      page,
    }) => {
      // Add assignee fields for assignee grouping
      const issuesWithAssignee = mockIssuesAllTypes.map((i, idx) => ({
        ...i,
        assignee: idx < 4 ? "alice" : idx < 7 ? "bob" : undefined,
      }))

      await setupMocks(page, issuesWithAssignee)
      await navigateAndWait(page)

      // Verify type lanes exist
      await expect(
        page.getByRole("heading", { name: "Bug", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "Feature", exact: true })
      ).toBeVisible()

      // Switch to assignee grouping
      await page.getByTestId("groupby-filter").selectOption("assignee")

      // Type lanes should be gone
      await expect(
        page.getByRole("heading", { name: "Bug", exact: true })
      ).not.toBeVisible()
      await expect(
        page.getByRole("heading", { name: "Feature", exact: true })
      ).not.toBeVisible()

      // Assignee lanes should appear
      await expect(
        page.getByRole("heading", { name: "alice", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "bob", exact: true })
      ).toBeVisible()
    })

    test("URL param ?groupBy=type shows type swim lanes", async ({ page }) => {
      await setupMocks(page)
      await page.goto("/?groupBy=type")
      await page.waitForResponse((res) => res.url().includes("/api/ready"))

      // Verify swim lanes grouped by type
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()
      await expect(page.getByTestId("groupby-filter")).toHaveValue("type")
      await expect(
        page.getByRole("heading", { name: "Bug", exact: true })
      ).toBeVisible()
    })

    test("empty issues array shows no type lanes", async ({ page }) => {
      await setupMocks(page, [])
      await page.goto("/?groupBy=type")
      await page.waitForResponse((res) => res.url().includes("/api/ready"))

      // No lanes should exist
      const lanes = getLanes(page)
      await expect(lanes).toHaveCount(0)
    })

    test("all issues same type creates single lane", async ({ page }) => {
      // All bugs
      const allBugs = [
        {
          id: "b1",
          title: "Bug 1",
          status: "open",
          priority: 2,
          issue_type: "bug",
          ...timestamps,
        },
        {
          id: "b2",
          title: "Bug 2",
          status: "open",
          priority: 2,
          issue_type: "bug",
          ...timestamps,
        },
        {
          id: "b3",
          title: "Bug 3",
          status: "in_progress",
          priority: 2,
          issue_type: "bug",
          ...timestamps,
        },
      ]

      await setupMocks(page, allBugs)
      await navigateAndWait(page)

      // Only Bug lane should exist
      const lanes = getLanes(page)
      await expect(lanes).toHaveCount(1)
      await expect(
        page.getByRole("heading", { name: "Bug", exact: true })
      ).toBeVisible()
    })
  })

  test.describe("Combined with Filters", () => {
    test("type grouping works with priority filter", async ({ page }) => {
      await setupMocks(page)
      await page.goto("/?groupBy=type&priority=0")
      await page.waitForResponse((res) => res.url().includes("/api/ready"))
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()

      // Only P0 bugs visible (Critical Login Bug is P0)
      const bugLane = page.getByTestId("swim-lane-lane-type-bug")
      await expect(bugLane).toBeVisible()
      await expect(bugLane.getByText("Critical Login Bug")).toBeVisible()
      // Minor UI Bug is P2, should not be visible
      await expect(bugLane.getByText("Minor UI Bug")).not.toBeVisible()
    })

    test("type grouping works with search", async ({ page }) => {
      await setupMocks(page)
      await page.goto("/?groupBy=type&search=Bug")
      await page.waitForResponse((res) => res.url().includes("/api/ready"))
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()

      // Only issues with "Bug" in title should show
      await expect(page.getByText("Critical Login Bug")).toBeVisible()
      await expect(page.getByText("Minor UI Bug")).toBeVisible()

      // Other issues should not be visible
      await expect(page.getByText("Dark Mode Feature")).not.toBeVisible()
      await expect(page.getByText("Q1 Roadmap Epic")).not.toBeVisible()
    })
  })
})
