import { test, expect, Page } from "@playwright/test"

/**
 * Mock issues for testing groupBy Assignee swim lanes.
 * Distribution:
 * - alice: 3 issues (open, in_progress, closed)
 * - bob: 2 issues (both open)
 * - charlie: 1 issue (in_progress)
 * - unassigned: 2 issues (both open)
 * Total: 8 issues
 */
const mockIssues = [
  // alice: 3 issues across different statuses
  {
    id: "alice-open",
    title: "Alice Open Task",
    status: "open",
    priority: 2,
    issue_type: "task",
    assignee: "alice",
    created_at: "2026-01-27T10:00:00Z",
    updated_at: "2026-01-27T10:00:00Z",
  },
  {
    id: "alice-progress",
    title: "Alice In Progress",
    status: "in_progress",
    priority: 1,
    issue_type: "feature",
    assignee: "alice",
    created_at: "2026-01-27T10:01:00Z",
    updated_at: "2026-01-27T10:01:00Z",
  },
  {
    id: "alice-closed",
    title: "Alice Closed Bug",
    status: "closed",
    priority: 0,
    issue_type: "bug",
    assignee: "alice",
    created_at: "2026-01-27T10:02:00Z",
    updated_at: "2026-01-27T10:02:00Z",
  },
  // bob: 2 issues (both open)
  {
    id: "bob-1",
    title: "Bob First Task",
    status: "open",
    priority: 2,
    issue_type: "task",
    assignee: "bob",
    created_at: "2026-01-27T11:00:00Z",
    updated_at: "2026-01-27T11:00:00Z",
  },
  {
    id: "bob-2",
    title: "Bob Second Task",
    status: "open",
    priority: 3,
    issue_type: "task",
    assignee: "bob",
    created_at: "2026-01-27T11:01:00Z",
    updated_at: "2026-01-27T11:01:00Z",
  },
  // charlie: 1 issue (in_progress)
  {
    id: "charlie-1",
    title: "Charlie Feature",
    status: "in_progress",
    priority: 1,
    issue_type: "feature",
    assignee: "charlie",
    created_at: "2026-01-27T12:00:00Z",
    updated_at: "2026-01-27T12:00:00Z",
  },
  // unassigned: 2 issues (both open)
  {
    id: "unassigned-1",
    title: "Unassigned Bug",
    status: "open",
    priority: 2,
    issue_type: "bug",
    created_at: "2026-01-27T13:00:00Z",
    updated_at: "2026-01-27T13:00:00Z",
  },
  {
    id: "unassigned-2",
    title: "Orphan Task",
    status: "open",
    priority: 4,
    issue_type: "task",
    created_at: "2026-01-27T13:01:00Z",
    updated_at: "2026-01-27T13:01:00Z",
  },
]
// Summary: alice = 3, bob = 2, charlie = 1, unassigned = 2 (total = 8)

/**
 * Set up API mocks for assignee grouping tests.
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
 * Navigate to app with groupBy=assignee and wait for API response.
 */
async function navigateToAssigneeView(page: Page) {
  const [response] = await Promise.all([
    page.waitForResponse(
      (res) => res.url().includes("/api/ready") && res.status() === 200
    ),
    page.goto("/?groupBy=assignee"),
  ])
  expect(response.ok()).toBe(true)
  await expect(page.getByTestId("swim-lane-board")).toBeVisible()
}

/**
 * Get a specific assignee lane by username or __unassigned__.
 */
function getAssigneeLane(page: Page, assignee: string) {
  return page.getByTestId(`swim-lane-lane-assignee-${assignee}`)
}

test.describe("groupBy Assignee Swim Lanes", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page)
  })

  test.describe("Grouping - Issues Grouped by Assignee Field", () => {
    test("grouping by assignee creates lane for each unique assignee", async ({
      page,
    }) => {
      await navigateToAssigneeView(page)

      // Verify 4 lanes: alice, bob, charlie, __unassigned__
      const lanes = page.locator('[data-testid^="swim-lane-lane-assignee-"]')
      await expect(lanes).toHaveCount(4)

      // Verify alice lane has 3 issues
      const aliceLane = getAssigneeLane(page, "alice")
      await expect(aliceLane).toBeVisible()
      await expect(aliceLane.locator("article")).toHaveCount(3)

      // Verify bob lane has 2 issues
      const bobLane = getAssigneeLane(page, "bob")
      await expect(bobLane).toBeVisible()
      await expect(bobLane.locator("article")).toHaveCount(2)

      // Verify charlie lane has 1 issue
      const charlieLane = getAssigneeLane(page, "charlie")
      await expect(charlieLane).toBeVisible()
      await expect(charlieLane.locator("article")).toHaveCount(1)
    })

    test("issues appear in correct assignee lane", async ({ page }) => {
      await navigateToAssigneeView(page)

      // Alice's issues
      const aliceLane = getAssigneeLane(page, "alice")
      await expect(aliceLane.getByText("Alice Open Task")).toBeVisible()
      await expect(aliceLane.getByText("Alice In Progress")).toBeVisible()
      await expect(aliceLane.getByText("Alice Closed Bug")).toBeVisible()

      // Bob's issues
      const bobLane = getAssigneeLane(page, "bob")
      await expect(bobLane.getByText("Bob First Task")).toBeVisible()
      await expect(bobLane.getByText("Bob Second Task")).toBeVisible()

      // Charlie's issues
      const charlieLane = getAssigneeLane(page, "charlie")
      await expect(charlieLane.getByText("Charlie Feature")).toBeVisible()
    })
  })

  test.describe("Lane Headers - Each Lane Shows Assignee Name", () => {
    test("lane headers show assignee name as-is", async ({ page }) => {
      await navigateToAssigneeView(page)

      // Verify lane headers match assignee names exactly
      await expect(
        page.getByRole("heading", { name: "alice", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "bob", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "charlie", exact: true })
      ).toBeVisible()
    })

    test("mixed case assignee names preserved in headers", async ({ page }) => {
      const mixedCaseIssues = [
        {
          id: "mc-1",
          title: "Mixed Case Issue",
          status: "open",
          priority: 2,
          issue_type: "task",
          assignee: "JohnDoe",
          created_at: "2026-01-27T10:00:00Z",
          updated_at: "2026-01-27T10:00:00Z",
        },
      ]
      await setupMocks(page, mixedCaseIssues)
      await navigateToAssigneeView(page)

      // Verify case preserved
      await expect(
        page.getByRole("heading", { name: "JohnDoe", exact: true })
      ).toBeVisible()
    })
  })

  test.describe("Unassigned Lane - Issues Without Assignee", () => {
    test("issues without assignee field go to Unassigned lane", async ({
      page,
    }) => {
      await navigateToAssigneeView(page)

      // Verify Unassigned lane exists
      const unassignedLane = getAssigneeLane(page, "__unassigned__")
      await expect(unassignedLane).toBeVisible()

      // Verify header
      await expect(
        unassignedLane.getByRole("heading", { name: "Unassigned", exact: true })
      ).toBeVisible()

      // Verify issue count
      await expect(unassignedLane.locator("article")).toHaveCount(2)

      // Verify issues
      await expect(unassignedLane.getByText("Unassigned Bug")).toBeVisible()
      await expect(unassignedLane.getByText("Orphan Task")).toBeVisible()
    })

    test("Unassigned lane appears at bottom of lanes", async ({ page }) => {
      await navigateToAssigneeView(page)

      // Get all lanes in order
      const lanes = page.locator('[data-testid^="swim-lane-lane-assignee-"]')
      const count = await lanes.count()

      // Last lane should be Unassigned
      const lastLane = lanes.nth(count - 1)
      await expect(
        lastLane.getByRole("heading", { name: "Unassigned", exact: true })
      ).toBeVisible()
    })

    test("empty string assignee goes to Unassigned lane", async ({ page }) => {
      const emptyAssigneeIssues = [
        {
          id: "empty-1",
          title: "Empty Assignee Issue",
          status: "open",
          priority: 2,
          assignee: "", // empty string
          issue_type: "task",
          created_at: "2026-01-27T10:00:00Z",
          updated_at: "2026-01-27T10:00:00Z",
        },
      ]
      await setupMocks(page, emptyAssigneeIssues)
      await navigateToAssigneeView(page)

      // Empty string should go to Unassigned lane
      const unassignedLane = getAssigneeLane(page, "__unassigned__")
      await expect(unassignedLane).toBeVisible()
      await expect(unassignedLane.getByText("Empty Assignee Issue")).toBeVisible()
    })
  })

  test.describe("Issue Count - Lane Headers Show Correct Counts", () => {
    test("lane headers show correct issue counts", async ({ page }) => {
      await navigateToAssigneeView(page)

      // Verify count badges using aria-label (use first() to avoid strict mode violation)
      const aliceLane = getAssigneeLane(page, "alice")
      await expect(aliceLane.getByLabel("3 issues").first()).toBeVisible()

      const bobLane = getAssigneeLane(page, "bob")
      await expect(bobLane.getByLabel("2 issues").first()).toBeVisible()

      const charlieLane = getAssigneeLane(page, "charlie")
      await expect(charlieLane.getByLabel("1 issues").first()).toBeVisible()

      const unassignedLane = getAssigneeLane(page, "__unassigned__")
      await expect(unassignedLane.getByLabel("2 issues").first()).toBeVisible()
    })

    test("count remains visible when lane collapsed", async ({ page }) => {
      await navigateToAssigneeView(page)

      const aliceLane = getAssigneeLane(page, "alice")
      const collapseToggle = aliceLane.getByTestId("collapse-toggle")

      // Collapse the lane
      await collapseToggle.click()
      await expect(aliceLane).toHaveAttribute("data-collapsed", "true")

      // Count should still be visible (use first() to avoid strict mode violation)
      await expect(aliceLane.getByLabel("3 issues").first()).toBeVisible()
    })
  })

  test.describe("Drag Between Lanes - Status Changes Only", () => {
    test("drag issue changes status within assignee lane", async ({ page }) => {
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
              data: { ...mockIssues[3], status: body.status },
            }),
          })
        } else {
          await route.continue()
        }
      })

      await navigateToAssigneeView(page)

      // Get bob's lane (has 2 open issues)
      const bobLane = getAssigneeLane(page, "bob")
      const openColumn = bobLane.locator('section[data-status="ready"]')
      const inProgressColumn = bobLane.locator(
        'section[data-status="in_progress"]'
      )

      // Initial state: 2 open, 0 in_progress
      await expect(openColumn.locator("article")).toHaveCount(2)
      await expect(inProgressColumn.locator("article")).toHaveCount(0)

      // Get card to drag
      const card = openColumn
        .locator("article")
        .filter({ hasText: "Bob First Task" })
      await expect(card).toBeVisible()

      const draggable = card.locator("..")
      const dropTarget = inProgressColumn.locator(
        '[data-droppable-id="in_progress"]'
      )

      // Get positions
      const sourceBox = await draggable.boundingBox()
      const targetBox = await dropTarget.boundingBox()
      if (!sourceBox || !targetBox) throw new Error("Could not get bounding boxes")

      const startX = sourceBox.x + sourceBox.width / 2
      const startY = sourceBox.y + sourceBox.height / 2
      const endX = targetBox.x + targetBox.width / 2
      const endY = targetBox.y + targetBox.height / 2

      // Perform drag
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
          res.url().includes("/api/issues/bob-1") &&
          res.request().method() === "PATCH"
      )

      // Verify API call
      expect(patchCalls).toHaveLength(1)
      expect(patchCalls[0].body).toEqual({ status: "in_progress" })

      // Verify card moved
      await expect(inProgressColumn.getByText("Bob First Task")).toBeVisible()
    })

    test("dragging between lanes only changes status, not assignee", async ({
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

      await navigateToAssigneeView(page)

      // Alice lane open column
      const aliceLane = getAssigneeLane(page, "alice")
      const aliceOpen = aliceLane.locator('section[data-status="ready"]')

      // Bob lane in_progress column
      const bobLane = getAssigneeLane(page, "bob")
      const bobInProgress = bobLane.locator('section[data-status="in_progress"]')

      // Drag Alice's open task to Bob's in_progress column
      const card = aliceOpen
        .locator("article")
        .filter({ hasText: "Alice Open Task" })
      await expect(card).toBeVisible()

      const draggable = card.locator("..")
      const dropTarget = bobInProgress.locator(
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

      // Wait for PATCH
      await page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/alice-open") &&
          res.request().method() === "PATCH"
      )

      // Verify ONLY status changed, NOT assignee
      expect(patchCalls).toHaveLength(1)
      expect(patchCalls[0].body).toEqual({ status: "in_progress" })
      // Note: assignee is NOT in the PATCH body - it remains "alice"
    })
  })

  test.describe("Multiple Assignees - Lane Count", () => {
    test("many unique assignees create individual lanes", async ({ page }) => {
      const timestamps = {
        created_at: "2026-01-27T10:00:00Z",
        updated_at: "2026-01-27T10:00:00Z",
      }
      const manyAssignees = [
        { id: "a1", title: "A1", status: "open", priority: 2, assignee: "user1", issue_type: "task", ...timestamps },
        { id: "a2", title: "A2", status: "open", priority: 2, assignee: "user2", issue_type: "task", ...timestamps },
        { id: "a3", title: "A3", status: "open", priority: 2, assignee: "user3", issue_type: "task", ...timestamps },
        { id: "a4", title: "A4", status: "open", priority: 2, assignee: "user4", issue_type: "task", ...timestamps },
        { id: "a5", title: "A5", status: "open", priority: 2, assignee: "user5", issue_type: "task", ...timestamps },
      ]
      await setupMocks(page, manyAssignees)
      await navigateToAssigneeView(page)

      // 5 unique assignees = 5 lanes
      const lanes = page.locator('[data-testid^="swim-lane-lane-assignee-"]')
      await expect(lanes).toHaveCount(5)

      // Verify each lane
      for (let i = 1; i <= 5; i++) {
        await expect(
          page.getByRole("heading", { name: `user${i}`, exact: true })
        ).toBeVisible()
      }
    })
  })

  test.describe("Status Distribution Within Lanes", () => {
    test("issues distributed by status columns within assignee lanes", async ({
      page,
    }) => {
      await navigateToAssigneeView(page)

      // Alice has: 1 open, 1 in_progress, 1 closed
      const aliceLane = getAssigneeLane(page, "alice")
      await expect(
        aliceLane.locator('section[data-status="ready"] article')
      ).toHaveCount(1)
      await expect(
        aliceLane.locator('section[data-status="in_progress"] article')
      ).toHaveCount(1)
      await expect(
        aliceLane.locator('section[data-status="done"] article')
      ).toHaveCount(1)

      // Bob has: 2 open, 0 in_progress, 0 closed
      const bobLane = getAssigneeLane(page, "bob")
      await expect(
        bobLane.locator('section[data-status="ready"] article')
      ).toHaveCount(2)
      await expect(
        bobLane.locator('section[data-status="in_progress"] article')
      ).toHaveCount(0)
      await expect(
        bobLane.locator('section[data-status="done"] article')
      ).toHaveCount(0)
    })
  })

  test.describe("Collapse/Expand Behavior", () => {
    test("collapse toggle hides lane content", async ({ page }) => {
      await navigateToAssigneeView(page)

      const aliceLane = getAssigneeLane(page, "alice")
      await expect(aliceLane).toHaveAttribute("data-collapsed", "false")

      // Click collapse
      await aliceLane.getByTestId("collapse-toggle").click()
      await expect(aliceLane).toHaveAttribute("data-collapsed", "true")

      // Content hidden
      const laneContent = aliceLane.locator('[data-collapsed="true"]')
      await expect(laneContent).toHaveAttribute("aria-hidden", "true")
    })

    test("expand collapsed lane shows content", async ({ page }) => {
      await navigateToAssigneeView(page)

      const aliceLane = getAssigneeLane(page, "alice")

      // Collapse first
      const collapseToggle = aliceLane.getByTestId("collapse-toggle")
      await collapseToggle.click()
      await expect(aliceLane).toHaveAttribute("data-collapsed", "true")

      // Click toggle again to expand
      await collapseToggle.click()
      await expect(aliceLane).toHaveAttribute("data-collapsed", "false")

      // Content should be visible again
      const laneContent = aliceLane.locator('[aria-hidden="false"]')
      await expect(laneContent).toBeVisible()
    })
  })

  test.describe("Edge Cases", () => {
    test("single assignee creates single lane", async ({ page }) => {
      const singleAssignee = [
        {
          id: "solo-1",
          title: "Solo Issue",
          status: "open",
          priority: 2,
          assignee: "solo",
          issue_type: "task",
          created_at: "2026-01-27T10:00:00Z",
          updated_at: "2026-01-27T10:00:00Z",
        },
      ]
      await setupMocks(page, singleAssignee)
      await navigateToAssigneeView(page)

      // Only 1 lane (solo), no Unassigned lane if no unassigned issues
      const lanes = page.locator('[data-testid^="swim-lane-lane-assignee-"]')
      await expect(lanes).toHaveCount(1)
      await expect(
        page.getByRole("heading", { name: "solo", exact: true })
      ).toBeVisible()
    })

    test("all issues unassigned creates only Unassigned lane", async ({
      page,
    }) => {
      const allUnassigned = [
        {
          id: "u1",
          title: "Unassigned 1",
          status: "open",
          priority: 2,
          issue_type: "task",
          created_at: "2026-01-27T10:00:00Z",
          updated_at: "2026-01-27T10:00:00Z",
        },
        {
          id: "u2",
          title: "Unassigned 2",
          status: "open",
          priority: 2,
          issue_type: "task",
          created_at: "2026-01-27T11:00:00Z",
          updated_at: "2026-01-27T11:00:00Z",
        },
      ]
      await setupMocks(page, allUnassigned)
      await navigateToAssigneeView(page)

      const lanes = page.locator('[data-testid^="swim-lane-lane-assignee-"]')
      await expect(lanes).toHaveCount(1)
      await expect(
        page.getByRole("heading", { name: "Unassigned", exact: true })
      ).toBeVisible()
    })

    test("empty issues array shows no assignee lanes", async ({ page }) => {
      await setupMocks(page, [])
      await page.goto("/?groupBy=assignee")
      await page.waitForResponse((res) => res.url().includes("/api/ready"))

      await expect(page.getByTestId("swim-lane-board")).toBeVisible()
      const lanes = page.locator('[data-testid^="swim-lane-lane-assignee-"]')
      await expect(lanes).toHaveCount(0)
    })

    test("assignee lanes sorted alphabetically by name", async ({ page }) => {
      await navigateToAssigneeView(page)

      // Get all lanes in order
      const lanes = page.locator('[data-testid^="swim-lane-lane-assignee-"]')
      const laneCount = await lanes.count()

      // Get titles in order (excluding Unassigned at end)
      const titles: string[] = []
      for (let i = 0; i < laneCount - 1; i++) {
        const lane = lanes.nth(i)
        // Use first() to get only the lane heading, not card headings
        const heading = lane.getByRole("heading").first()
        const title = await heading.textContent()
        if (title) titles.push(title)
      }

      // Verify alphabetical order (Unassigned excluded from this check)
      const sortedTitles = [...titles].sort()
      expect(titles).toEqual(sortedTitles)
    })

    test("switching from assignee to epic regroups issues", async ({ page }) => {
      // Need issues with both assignee and parent for this test
      const issuesWithParents = [
        {
          id: "ep-1",
          title: "Issue in Epic",
          status: "open",
          priority: 2,
          issue_type: "task",
          assignee: "alice",
          parent: "epic-1",
          parent_title: "Epic One",
          created_at: "2026-01-27T10:00:00Z",
          updated_at: "2026-01-27T10:00:00Z",
        },
        {
          id: "ep-2",
          title: "Another Issue",
          status: "open",
          priority: 2,
          issue_type: "task",
          assignee: "bob",
          parent: "epic-1",
          parent_title: "Epic One",
          created_at: "2026-01-27T11:00:00Z",
          updated_at: "2026-01-27T11:00:00Z",
        },
      ]

      await setupMocks(page, issuesWithParents)
      await navigateToAssigneeView(page)

      // Verify assignee lanes exist
      await expect(
        page.getByRole("heading", { name: "alice", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "bob", exact: true })
      ).toBeVisible()

      // Switch to epic grouping
      await page.getByTestId("groupby-filter").selectOption("epic")

      // Assignee lanes should be gone
      await expect(
        page.getByRole("heading", { name: "alice", exact: true })
      ).not.toBeVisible()
      await expect(
        page.getByRole("heading", { name: "bob", exact: true })
      ).not.toBeVisible()

      // Epic lanes should appear
      await expect(
        page.getByRole("heading", { name: "Epic One", exact: true })
      ).toBeVisible()
    })

    test("URL with groupBy=assignee shows assignee swim lanes on load", async ({
      page,
    }) => {
      await setupMocks(page)
      await page.goto("/?groupBy=assignee")
      await page.waitForResponse((res) => res.url().includes("/api/ready"))

      // Verify swim lanes visible
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()

      // Verify dropdown shows assignee selected
      await expect(page.getByTestId("groupby-filter")).toHaveValue("assignee")

      // Verify assignee lanes present
      await expect(
        page.getByRole("heading", { name: "alice", exact: true })
      ).toBeVisible()
    })
  })
})
