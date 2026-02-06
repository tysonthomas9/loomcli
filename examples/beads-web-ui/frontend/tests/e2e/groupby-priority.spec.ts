import { test, expect, Page } from "@playwright/test"

/**
 * Comprehensive mock issues for testing groupBy Priority swim lanes.
 * Covers all priority levels (0-4) plus undefined (No Priority).
 */
const mockIssues = [
  // P0 (Critical) - 2 issues
  {
    id: "p0-bug",
    title: "Critical Security Bug",
    status: "open",
    priority: 0,
    issue_type: "bug",
    created_at: "2026-01-27T10:00:00Z",
    updated_at: "2026-01-27T10:00:00Z",
  },
  {
    id: "p0-hotfix",
    title: "Production Hotfix",
    status: "in_progress",
    priority: 0,
    issue_type: "bug",
    created_at: "2026-01-27T10:30:00Z",
    updated_at: "2026-01-27T10:30:00Z",
  },
  // P1 (High) - 1 issue
  {
    id: "p1-feature",
    title: "High Priority Feature",
    status: "open",
    priority: 1,
    issue_type: "feature",
    created_at: "2026-01-27T11:00:00Z",
    updated_at: "2026-01-27T11:00:00Z",
  },
  // P2 (Medium) - 2 issues
  {
    id: "p2-task-1",
    title: "Medium Task One",
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-27T12:00:00Z",
    updated_at: "2026-01-27T12:00:00Z",
  },
  {
    id: "p2-task-2",
    title: "Medium Task Two",
    status: "closed",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-27T12:30:00Z",
    updated_at: "2026-01-27T12:30:00Z",
  },
  // P3 (Normal) - 1 issue
  {
    id: "p3-task",
    title: "Normal Priority Task",
    status: "open",
    priority: 3,
    issue_type: "task",
    created_at: "2026-01-27T13:00:00Z",
    updated_at: "2026-01-27T13:00:00Z",
  },
  // P4 (Backlog) - 1 issue
  {
    id: "p4-idea",
    title: "Backlog Idea",
    status: "open",
    priority: 4,
    issue_type: "feature",
    created_at: "2026-01-27T14:00:00Z",
    updated_at: "2026-01-27T14:00:00Z",
  },
  // No Priority - 1 issue
  {
    id: "no-priority-issue",
    title: "Issue Without Priority",
    status: "open",
    issue_type: "task",
    // priority field intentionally omitted
    created_at: "2026-01-27T15:00:00Z",
    updated_at: "2026-01-27T15:00:00Z",
  },
]

/**
 * Set up API mocks for priority groupBy tests.
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
 * Get all swim lane sections on the page.
 */
function getLanes(page: Page) {
  return page.locator('[data-testid^="swim-lane-lane-"]')
}

test.describe("groupBy Priority Swim Lanes", () => {
  test.describe("Lane Rendering", () => {
    // Test Case 1: Grouping - Issues grouped by priority
    // Test Case 6: All priorities - All 5 priority lanes shown
    test("renders all 5 priority lanes (P0-P4) plus No Priority lane", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=priority")

      // Verify swim lane board appears
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()

      // Verify all 6 lanes (5 priorities + No Priority)
      const lanes = getLanes(page)
      await expect(lanes).toHaveCount(6)

      // Test Case 2: Lane headers - Verify all header labels
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
      await expect(
        page.getByRole("heading", { name: "No Priority" })
      ).toBeVisible()
    })

    test("issues appear in correct priority lanes", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=priority")

      // P0 lane has 2 issues
      const p0Lane = page.getByTestId("swim-lane-lane-priority-0")
      await expect(p0Lane.locator("article")).toHaveCount(2)
      await expect(p0Lane.getByText("Critical Security Bug")).toBeVisible()
      await expect(p0Lane.getByText("Production Hotfix")).toBeVisible()

      // P1 lane has 1 issue
      const p1Lane = page.getByTestId("swim-lane-lane-priority-1")
      await expect(p1Lane.locator("article")).toHaveCount(1)
      await expect(p1Lane.getByText("High Priority Feature")).toBeVisible()

      // P2 lane has 2 issues
      const p2Lane = page.getByTestId("swim-lane-lane-priority-2")
      await expect(p2Lane.locator("article")).toHaveCount(2)
      await expect(p2Lane.getByText("Medium Task One")).toBeVisible()
      await expect(p2Lane.getByText("Medium Task Two")).toBeVisible()

      // P3 lane has 1 issue
      const p3Lane = page.getByTestId("swim-lane-lane-priority-3")
      await expect(p3Lane.locator("article")).toHaveCount(1)
      await expect(p3Lane.getByText("Normal Priority Task")).toBeVisible()

      // P4 lane has 1 issue
      const p4Lane = page.getByTestId("swim-lane-lane-priority-4")
      await expect(p4Lane.locator("article")).toHaveCount(1)
      await expect(p4Lane.getByText("Backlog Idea")).toBeVisible()
    })
  })

  test.describe("Lane Ordering", () => {
    // Test Case 3: Lane order - P0 → P4 (highest first)
    test("lanes are ordered P0 → P4 with No Priority at end", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=priority")

      // Get all lanes in order
      const lanes = getLanes(page)
      const laneCount = await lanes.count()
      expect(laneCount).toBe(6)

      // Verify order by checking nth lane titles
      // P0 (Critical) should be first
      await expect(
        lanes.nth(0).getByRole("heading", { name: "P0 (Critical)" })
      ).toBeVisible()
      // P1 (High) should be second
      await expect(
        lanes.nth(1).getByRole("heading", { name: "P1 (High)" })
      ).toBeVisible()
      // P2 (Medium) should be third
      await expect(
        lanes.nth(2).getByRole("heading", { name: "P2 (Medium)" })
      ).toBeVisible()
      // P3 (Normal) should be fourth
      await expect(
        lanes.nth(3).getByRole("heading", { name: "P3 (Normal)" })
      ).toBeVisible()
      // P4 (Backlog) should be fifth
      await expect(
        lanes.nth(4).getByRole("heading", { name: "P4 (Backlog)" })
      ).toBeVisible()
      // No Priority should be last (special lanes go to end)
      await expect(
        lanes.nth(5).getByRole("heading", { name: "No Priority" })
      ).toBeVisible()
    })
  })

  test.describe("Issue Counts", () => {
    // Test Case 4: Lane headers show correct counts
    test("lane headers display correct issue counts", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=priority")

      // P0 has 2 issues
      const p0Lane = page.getByTestId("swim-lane-lane-priority-0")
      await expect(p0Lane.getByLabel("2 issues")).toBeVisible()

      // P1 has 1 issue
      const p1Lane = page.getByTestId("swim-lane-lane-priority-1")
      await expect(p1Lane.getByLabel("1 issues")).toBeVisible()

      // P2 has 2 issues
      const p2Lane = page.getByTestId("swim-lane-lane-priority-2")
      await expect(p2Lane.getByLabel("2 issues")).toBeVisible()

      // P3 has 1 issue
      const p3Lane = page.getByTestId("swim-lane-lane-priority-3")
      await expect(p3Lane.getByLabel("1 issues")).toBeVisible()

      // P4 has 1 issue
      const p4Lane = page.getByTestId("swim-lane-lane-priority-4")
      await expect(p4Lane.getByLabel("1 issues")).toBeVisible()

      // No Priority has 1 issue
      const noPriorityLane = page.getByTestId(
        "swim-lane-lane-priority-__no_priority__"
      )
      await expect(noPriorityLane.getByLabel("1 issues")).toBeVisible()
    })
  })

  test.describe("No Priority Handling", () => {
    // Test Case 7: Issues without priority go to 'No Priority' lane
    test("issues without priority appear in No Priority lane", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=priority")

      // Verify No Priority lane exists
      const noPriorityLane = page.getByTestId(
        "swim-lane-lane-priority-__no_priority__"
      )
      await expect(noPriorityLane).toBeVisible()

      // Verify the no-priority issue is in the lane
      await expect(
        noPriorityLane.getByText("Issue Without Priority")
      ).toBeVisible()
      await expect(noPriorityLane.locator("article")).toHaveCount(1)
    })

    test("No Priority lane does not appear when all issues have priority", async ({
      page,
    }) => {
      // Filter out the no-priority issue
      const allPriorityIssues = mockIssues.filter(
        (i) => (i as { priority?: number }).priority !== undefined
      )
      await setupMocks(page, allPriorityIssues)
      await navigateAndWait(page, "/?groupBy=priority")

      // Only 5 lanes should exist (no "No Priority" lane)
      const lanes = getLanes(page)
      await expect(lanes).toHaveCount(5)

      // "No Priority" heading should not exist
      await expect(
        page.getByRole("heading", { name: "No Priority" })
      ).not.toBeVisible()
    })
  })

  test.describe("Status Columns Within Priority Lanes", () => {
    test("each priority lane contains all status columns", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=priority")

      // Check P0 lane has all three status columns
      const p0Lane = page.getByTestId("swim-lane-lane-priority-0")
      await expect(
        p0Lane.locator('section[data-status="ready"]')
      ).toBeVisible()
      await expect(
        p0Lane.locator('section[data-status="in_progress"]')
      ).toBeVisible()
      await expect(
        p0Lane.locator('section[data-status="done"]')
      ).toBeVisible()
    })

    test("issues distributed correctly by status within priority lanes", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=priority")

      // P0 lane: 1 open, 1 in_progress
      const p0Lane = page.getByTestId("swim-lane-lane-priority-0")
      await expect(
        p0Lane.locator('section[data-status="ready"] article')
      ).toHaveCount(1)
      await expect(
        p0Lane.locator('section[data-status="in_progress"] article')
      ).toHaveCount(1)
      await expect(
        p0Lane.locator('section[data-status="done"] article')
      ).toHaveCount(0)

      // P2 lane: 1 open, 1 closed
      const p2Lane = page.getByTestId("swim-lane-lane-priority-2")
      await expect(
        p2Lane.locator('section[data-status="ready"] article')
      ).toHaveCount(1)
      await expect(
        p2Lane.locator('section[data-status="done"] article')
      ).toHaveCount(1)
    })
  })

  test.describe("Drag and Drop Within Priority Lanes", () => {
    // Test Case 5: Drag between lanes
    // Note: Dragging only changes STATUS, not priority (by design)
    test("dragging issue within priority lane changes status", async ({
      page,
    }) => {
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

      await navigateAndWait(page, "/?groupBy=priority")

      // Get P0 lane
      const p0Lane = page.getByTestId("swim-lane-lane-priority-0")
      await expect(p0Lane).toBeVisible()

      // Find open and in_progress columns within P0 lane
      const openColumn = p0Lane.locator('section[data-status="ready"]')
      const inProgressColumn = p0Lane.locator(
        'section[data-status="in_progress"]'
      )

      // Get the card to drag (Critical Security Bug is in open)
      const cardToDrag = openColumn
        .locator("article")
        .filter({ hasText: "Critical Security Bug" })
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

      // Wait for the PATCH API call
      await page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/p0-bug") &&
          res.request().method() === "PATCH"
      )

      // Verify API call was made with status change (not priority change)
      expect(patchCalls).toHaveLength(1)
      expect(patchCalls[0].body).toEqual({ status: "in_progress" })

      // Verify UI updated
      await expect(
        inProgressColumn.getByText("Critical Security Bug")
      ).toBeVisible()
    })
  })

  test.describe("Collapse/Expand", () => {
    test("collapsing priority lane hides content but shows count", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=priority")

      // Get P0 lane
      const p0Lane = page.getByTestId("swim-lane-lane-priority-0")
      await expect(p0Lane).toBeVisible()

      // Initially expanded
      await expect(p0Lane).toHaveAttribute("data-collapsed", "false")

      // Click collapse toggle
      const collapseToggle = p0Lane.getByTestId("collapse-toggle")
      await collapseToggle.click()

      // Verify collapsed
      await expect(p0Lane).toHaveAttribute("data-collapsed", "true")

      // Count badge still shows correct count
      await expect(p0Lane.getByLabel("2 issues")).toBeVisible()

      // Click again to expand
      await collapseToggle.click()
      await expect(p0Lane).toHaveAttribute("data-collapsed", "false")
    })
  })

  test.describe("Edge Cases", () => {
    test("single priority level shows one lane", async ({ page }) => {
      // All issues at same priority
      const singlePriorityIssues = [
        {
          id: "p2-1",
          title: "Task A",
          status: "open",
          priority: 2,
          issue_type: "task",
          created_at: "2026-01-27T10:00:00Z",
          updated_at: "2026-01-27T10:00:00Z",
        },
        {
          id: "p2-2",
          title: "Task B",
          status: "open",
          priority: 2,
          issue_type: "task",
          created_at: "2026-01-27T11:00:00Z",
          updated_at: "2026-01-27T11:00:00Z",
        },
      ]
      await setupMocks(page, singlePriorityIssues)
      await navigateAndWait(page, "/?groupBy=priority")

      // Only one lane should exist
      const lanes = getLanes(page)
      await expect(lanes).toHaveCount(1)
      await expect(
        page.getByRole("heading", { name: "P2 (Medium)" })
      ).toBeVisible()
    })

    test("empty issues shows no lanes", async ({ page }) => {
      await setupMocks(page, [])
      await navigateAndWait(page, "/?groupBy=priority")

      // No lanes should exist
      const lanes = getLanes(page)
      await expect(lanes).toHaveCount(0)
    })

    test("switching from priority to none shows flat kanban", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=priority")

      // Verify swim lanes visible
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()

      // Change to none
      await page.getByTestId("groupby-filter").selectOption("none")

      // Swim lane board should disappear
      await expect(page.getByTestId("swim-lane-board")).not.toBeVisible()

      // Flat kanban columns should appear
      await expect(page.locator('section[data-status="ready"]')).toBeVisible()
    })

    test("priority filter works with priority groupBy", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=priority&priority=0")

      // With priority=0 filter AND groupBy=priority, only P0 issues show
      // Only P0 lane should have visible cards
      const p0Lane = page.getByTestId("swim-lane-lane-priority-0")
      await expect(p0Lane.locator("article")).toHaveCount(2)

      // Other lanes should exist but be empty or not rendered
      // (depends on implementation - may show empty lanes or hide them)
    })
  })

  test.describe("URL Synchronization", () => {
    test("groupBy=priority persists in URL", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      // Select priority grouping
      await page.getByTestId("groupby-filter").selectOption("priority")

      // Verify URL contains groupBy param
      await expect(async () => {
        expect(page.url()).toContain("groupBy=priority")
      }).toPass({ timeout: 2000 })

      // Re-setup mocks before reload
      await setupMocks(page)

      // Reload and verify state persists
      await page.reload()
      await page.waitForResponse((res) => res.url().includes("/api/ready"))

      // Still showing priority swim lanes
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "P0 (Critical)" })
      ).toBeVisible()
    })
  })
})
