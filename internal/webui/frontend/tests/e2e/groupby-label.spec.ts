import { test, expect, Page } from "@playwright/test"

/**
 * E2E tests for groupBy Label swim lanes.
 *
 * Label grouping is unique: a single issue can appear in multiple swim lanes
 * if it has multiple labels. This test file covers label-specific scenarios
 * including multi-label duplication, total counts exceeding issue count,
 * "No Labels" lane handling, and drag-and-drop behavior.
 */

/**
 * Timestamp helper to reduce fixture boilerplate.
 */
const timestamps = {
  created_at: "2026-01-27T10:00:00Z",
  updated_at: "2026-01-27T10:00:00Z",
}

/**
 * Mock issues for testing groupBy Label swim lanes.
 * Distribution:
 * - frontend: 3 issues (issue-1, issue-4, issue-5)
 * - backend: 2 issues (issue-2, issue-4)
 * - urgent: 2 issues (issue-3, issue-5)
 * - No Labels: 2 issues (issue-6, issue-7)
 *
 * Note: issue-4 has ["frontend", "backend"], issue-5 has ["frontend", "urgent"]
 * Total unique issues: 7, Total lane appearances: 9
 */
const mockLabelIssues = [
  {
    id: "issue-1",
    title: "Frontend Only Issue",
    status: "open",
    priority: 2,
    issue_type: "task",
    labels: ["frontend"],
    ...timestamps,
  },
  {
    id: "issue-2",
    title: "Backend Only Issue",
    status: "in_progress",
    priority: 1,
    issue_type: "feature",
    labels: ["backend"],
    created_at: "2026-01-27T10:01:00Z",
    updated_at: "2026-01-27T10:01:00Z",
  },
  {
    id: "issue-3",
    title: "Urgent Only Issue",
    status: "open",
    priority: 0,
    issue_type: "bug",
    labels: ["urgent"],
    created_at: "2026-01-27T10:02:00Z",
    updated_at: "2026-01-27T10:02:00Z",
  },
  {
    id: "issue-4",
    title: "Multi-Label Frontend+Backend",
    status: "open",
    priority: 2,
    issue_type: "feature",
    labels: ["frontend", "backend"],
    created_at: "2026-01-27T10:03:00Z",
    updated_at: "2026-01-27T10:03:00Z",
  },
  {
    id: "issue-5",
    title: "Multi-Label Frontend+Urgent",
    status: "closed",
    priority: 1,
    issue_type: "task",
    labels: ["frontend", "urgent"],
    created_at: "2026-01-27T10:04:00Z",
    updated_at: "2026-01-27T10:04:00Z",
  },
  {
    id: "issue-6",
    title: "No Labels Issue (undefined)",
    status: "open",
    priority: 3,
    issue_type: "task",
    // labels: undefined
    created_at: "2026-01-27T10:05:00Z",
    updated_at: "2026-01-27T10:05:00Z",
  },
  {
    id: "issue-7",
    title: "No Labels Issue (empty array)",
    status: "open",
    priority: 4,
    issue_type: "bug",
    labels: [],
    created_at: "2026-01-27T10:06:00Z",
    updated_at: "2026-01-27T10:06:00Z",
  },
]

/**
 * Set up API mocks for label grouping tests.
 */
async function setupMocks(page: Page, issues = mockLabelIssues) {
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
  issues = mockLabelIssues,
  patchCalls: { url: string; body: object }[] = []
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
      const issue = issues.find((i) => i.id === issueId)
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
async function navigateAndWait(page: Page, path = "/?groupBy=label") {
  const [response] = await Promise.all([
    page.waitForResponse(
      (res) => res.url().includes("/api/ready") && res.status() === 200
    ),
    page.goto(path),
  ])
  expect(response.ok()).toBe(true)
  await expect(page.getByTestId("swim-lane-board")).toBeVisible()
}

test.describe("groupBy Label Swim Lanes", () => {
  test.describe("Grouping - Issues Grouped by Labels", () => {
    test("issues are grouped into lanes by label", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      // Verify 4 lanes: frontend, backend, urgent, No Labels
      const lanes = page.locator('[data-testid^="swim-lane-lane-label-"]')
      await expect(lanes).toHaveCount(4)

      // Verify each lane exists
      await expect(
        page.getByTestId("swim-lane-lane-label-frontend")
      ).toBeVisible()
      await expect(
        page.getByTestId("swim-lane-lane-label-backend")
      ).toBeVisible()
      await expect(
        page.getByTestId("swim-lane-lane-label-urgent")
      ).toBeVisible()
      await expect(
        page.getByTestId("swim-lane-lane-label-__no_labels__")
      ).toBeVisible()
    })

    test("issues appear in correct label lanes", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      // Frontend lane: issue-1, issue-4, issue-5 (3 issues)
      const frontendLane = page.getByTestId("swim-lane-lane-label-frontend")
      await expect(frontendLane.locator("article")).toHaveCount(3)
      await expect(frontendLane.getByText("Frontend Only Issue")).toBeVisible()
      await expect(
        frontendLane.getByText("Multi-Label Frontend+Backend")
      ).toBeVisible()
      await expect(
        frontendLane.getByText("Multi-Label Frontend+Urgent")
      ).toBeVisible()

      // Backend lane: issue-2, issue-4 (2 issues)
      const backendLane = page.getByTestId("swim-lane-lane-label-backend")
      await expect(backendLane.locator("article")).toHaveCount(2)
      await expect(backendLane.getByText("Backend Only Issue")).toBeVisible()
      await expect(
        backendLane.getByText("Multi-Label Frontend+Backend")
      ).toBeVisible()
    })
  })

  test.describe("Multi-Label - Issues with Multiple Labels", () => {
    test("multi-label issue appears in all its label lanes", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      // issue-4 has labels: ["frontend", "backend"]
      // It should appear in BOTH lanes
      const frontendLane = page.getByTestId("swim-lane-lane-label-frontend")
      const backendLane = page.getByTestId("swim-lane-lane-label-backend")

      await expect(
        frontendLane.getByText("Multi-Label Frontend+Backend")
      ).toBeVisible()
      await expect(
        backendLane.getByText("Multi-Label Frontend+Backend")
      ).toBeVisible()

      // issue-5 has labels: ["frontend", "urgent"]
      const urgentLane = page.getByTestId("swim-lane-lane-label-urgent")
      await expect(
        frontendLane.getByText("Multi-Label Frontend+Urgent")
      ).toBeVisible()
      await expect(
        urgentLane.getByText("Multi-Label Frontend+Urgent")
      ).toBeVisible()
    })

    test("issue with many labels appears in many lanes", async ({ page }) => {
      const manyLabelIssue = [
        {
          id: "many-labels",
          title: "Issue With Five Labels",
          status: "open",
          priority: 2,
          issue_type: "task",
          labels: ["label-a", "label-b", "label-c", "label-d", "label-e"],
          ...timestamps,
        },
      ]
      await setupMocks(page, manyLabelIssue)
      await navigateAndWait(page)

      // Should create 5 lanes, one for each label
      const lanes = page.locator('[data-testid^="swim-lane-lane-label-"]')
      await expect(lanes).toHaveCount(5)

      // Issue appears in all 5 lanes
      for (const label of [
        "label-a",
        "label-b",
        "label-c",
        "label-d",
        "label-e",
      ]) {
        const lane = page.getByTestId(`swim-lane-lane-label-${label}`)
        await expect(lane.getByText("Issue With Five Labels")).toBeVisible()
      }
    })
  })

  test.describe("Lane Headers - Each Lane Shows Label Name", () => {
    test("lane headers display label name as-is", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      // Label names shown unchanged (no capitalization)
      await expect(
        page.getByRole("heading", { name: "frontend", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "backend", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "urgent", exact: true })
      ).toBeVisible()
    })

    test("mixed case label names preserved in headers", async ({ page }) => {
      const mixedCaseLabels = [
        {
          id: "mixed-1",
          title: "Mixed Case Label",
          status: "open",
          priority: 2,
          issue_type: "task",
          labels: ["FrontEnd", "URGENT", "Backend"],
          ...timestamps,
        },
      ]
      await setupMocks(page, mixedCaseLabels)
      await navigateAndWait(page)

      // Case preserved exactly
      await expect(
        page.getByRole("heading", { name: "FrontEnd", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "URGENT", exact: true })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "Backend", exact: true })
      ).toBeVisible()
    })
  })

  test.describe("No Labels Lane - Issues Without Labels", () => {
    test("issues with undefined labels go to No Labels lane", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      const noLabelsLane = page.getByTestId(
        "swim-lane-lane-label-__no_labels__"
      )
      await expect(noLabelsLane).toBeVisible()

      // issue-6 has undefined labels
      await expect(
        noLabelsLane.getByText("No Labels Issue (undefined)")
      ).toBeVisible()
    })

    test("issues with empty labels array go to No Labels lane", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      const noLabelsLane = page.getByTestId(
        "swim-lane-lane-label-__no_labels__"
      )

      // issue-7 has labels: []
      await expect(
        noLabelsLane.getByText("No Labels Issue (empty array)")
      ).toBeVisible()
    })

    test("No Labels lane header displays correctly", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      const noLabelsLane = page.getByTestId(
        "swim-lane-lane-label-__no_labels__"
      )
      await expect(
        noLabelsLane.getByRole("heading", { name: "No Labels", exact: true })
      ).toBeVisible()
    })

    test("No Labels lane appears at bottom of lanes", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      const lanes = page.locator('[data-testid^="swim-lane-lane-label-"]')
      const count = await lanes.count()

      // Last lane should be No Labels
      const lastLane = lanes.nth(count - 1)
      await expect(
        lastLane.getByRole("heading", { name: "No Labels", exact: true })
      ).toBeVisible()
    })
  })

  test.describe("Issue Count - Counts May Exceed Total Due to Multi-Label", () => {
    test("lane counts reflect multi-label duplication", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      // Frontend: 3 issues (issue-1 + issue-4 + issue-5)
      // Use "> header" to target only the direct child lane header, not nested status column headers
      const frontendLane = page.getByTestId("swim-lane-lane-label-frontend")
      await expect(
        frontendLane.locator("> header").getByLabel("3 issues")
      ).toBeVisible()

      // Backend: 2 issues (issue-2 + issue-4)
      const backendLane = page.getByTestId("swim-lane-lane-label-backend")
      await expect(
        backendLane.locator("> header").getByLabel("2 issues")
      ).toBeVisible()

      // Urgent: 2 issues (issue-3 + issue-5)
      const urgentLane = page.getByTestId("swim-lane-lane-label-urgent")
      await expect(
        urgentLane.locator("> header").getByLabel("2 issues")
      ).toBeVisible()

      // No Labels: 2 issues (issue-6 + issue-7)
      const noLabelsLane = page.getByTestId(
        "swim-lane-lane-label-__no_labels__"
      )
      await expect(
        noLabelsLane.locator("> header").getByLabel("2 issues")
      ).toBeVisible()

      // Sum of lane counts (3+2+2+2=9) > total unique issues (7)
    })

    test("total cards across lanes exceeds unique issue count", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      // Total unique issues: 7
      // Total cards across all lanes: 9 (due to multi-label duplication)
      const allCards = page.locator(
        '[data-testid^="swim-lane-lane-label-"] article'
      )
      await expect(allCards).toHaveCount(9)
    })
  })

  test.describe("Drag Behavior - Dragging Changes Status Only", () => {
    test("drag issue changes status within label lane", async ({ page }) => {
      const patchCalls: { url: string; body: object }[] = []
      await setupMocksWithPatch(page, mockLabelIssues, patchCalls)
      await navigateAndWait(page)

      const frontendLane = page.getByTestId("swim-lane-lane-label-frontend")
      const openColumn = frontendLane.locator('section[data-status="ready"]')
      const inProgressColumn = frontendLane.locator(
        'section[data-status="in_progress"]'
      )

      // Get card to drag (Frontend Only Issue is open)
      const card = openColumn
        .locator("article")
        .filter({ hasText: "Frontend Only Issue" })
      const draggable = card.locator("..")
      const dropTarget = inProgressColumn.locator(
        '[data-droppable-id="in_progress"]'
      )

      const sourceBox = await draggable.boundingBox()
      const targetBox = await dropTarget.boundingBox()
      if (!sourceBox || !targetBox) throw new Error("Could not get bounding boxes")

      // Perform drag operation
      await draggable.dispatchEvent("pointerdown", {
        clientX: sourceBox.x + sourceBox.width / 2,
        clientY: sourceBox.y + sourceBox.height / 2,
        button: 0,
        buttons: 1,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      })
      await page.waitForTimeout(50)

      // Move past activation threshold
      await page.dispatchEvent("body", "pointermove", {
        clientX: sourceBox.x + sourceBox.width / 2 + 10,
        clientY: sourceBox.y + sourceBox.height / 2,
        button: 0,
        buttons: 1,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      })
      await page.waitForTimeout(50)

      // Move to target
      await page.dispatchEvent("body", "pointermove", {
        clientX: targetBox.x + targetBox.width / 2,
        clientY: targetBox.y + targetBox.height / 2,
        button: 0,
        buttons: 1,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      })
      await page.waitForTimeout(50)

      // Release at target
      await page.dispatchEvent("body", "pointerup", {
        clientX: targetBox.x + targetBox.width / 2,
        clientY: targetBox.y + targetBox.height / 2,
        button: 0,
        buttons: 0,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      })

      await page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/issue-1") &&
          res.request().method() === "PATCH"
      )

      // Verify ONLY status changed, not labels
      expect(patchCalls).toHaveLength(1)
      expect(patchCalls[0].body).toEqual({ status: "in_progress" })
      expect(patchCalls[0].body).not.toHaveProperty("labels")
    })

    test("cross-lane drag only changes status not labels", async ({ page }) => {
      const patchCalls: { url: string; body: object }[] = []
      await setupMocksWithPatch(page, mockLabelIssues, patchCalls)
      await navigateAndWait(page)

      // Drag from frontend lane to backend lane's in_progress column
      // This should change STATUS but NOT labels
      const frontendLane = page.getByTestId("swim-lane-lane-label-frontend")
      const backendLane = page.getByTestId("swim-lane-lane-label-backend")

      const frontendOpen = frontendLane.locator('section[data-status="ready"]')
      const backendInProgress = backendLane.locator(
        'section[data-status="in_progress"]'
      )

      const card = frontendOpen
        .locator("article")
        .filter({ hasText: "Frontend Only Issue" })
      const draggable = card.locator("..")
      const dropTarget = backendInProgress.locator(
        '[data-droppable-id="in_progress"]'
      )

      const sourceBox = await draggable.boundingBox()
      const targetBox = await dropTarget.boundingBox()
      if (!sourceBox || !targetBox) throw new Error("Could not get bounding boxes")

      await draggable.dispatchEvent("pointerdown", {
        clientX: sourceBox.x + sourceBox.width / 2,
        clientY: sourceBox.y + sourceBox.height / 2,
        button: 0,
        buttons: 1,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      })
      await page.waitForTimeout(50)

      await page.dispatchEvent("body", "pointermove", {
        clientX: sourceBox.x + sourceBox.width / 2 + 10,
        clientY: sourceBox.y + sourceBox.height / 2,
        button: 0,
        buttons: 1,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      })
      await page.waitForTimeout(50)

      await page.dispatchEvent("body", "pointermove", {
        clientX: targetBox.x + targetBox.width / 2,
        clientY: targetBox.y + targetBox.height / 2,
        button: 0,
        buttons: 1,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      })
      await page.waitForTimeout(50)

      await page.dispatchEvent("body", "pointerup", {
        clientX: targetBox.x + targetBox.width / 2,
        clientY: targetBox.y + targetBox.height / 2,
        button: 0,
        buttons: 0,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      })

      await page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/issue-1") &&
          res.request().method() === "PATCH"
      )

      // Only status changes, not labels
      expect(patchCalls).toHaveLength(1)
      expect(patchCalls[0].body).toEqual({ status: "in_progress" })
    })
  })

  test.describe("Status Columns Within Lanes", () => {
    test("each label lane contains all status columns", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      const frontendLane = page.getByTestId("swim-lane-lane-label-frontend")
      await expect(
        frontendLane.locator('section[data-status="ready"]')
      ).toBeVisible()
      await expect(
        frontendLane.locator('section[data-status="in_progress"]')
      ).toBeVisible()
      await expect(
        frontendLane.locator('section[data-status="done"]')
      ).toBeVisible()
    })

    test("issues distributed by status within label lanes", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      // Frontend lane: 2 open (issue-1, issue-4), 0 in_progress, 1 closed (issue-5)
      const frontendLane = page.getByTestId("swim-lane-lane-label-frontend")
      await expect(
        frontendLane.locator('section[data-status="ready"] article')
      ).toHaveCount(2)
      await expect(
        frontendLane.locator('section[data-status="in_progress"] article')
      ).toHaveCount(0)
      await expect(
        frontendLane.locator('section[data-status="done"] article')
      ).toHaveCount(1)
    })
  })

  test.describe("Collapse/Expand", () => {
    test("collapse toggle hides lane content", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      const frontendLane = page.getByTestId("swim-lane-lane-label-frontend")
      await expect(frontendLane).toHaveAttribute("data-collapsed", "false")

      await frontendLane.getByTestId("collapse-toggle").click()
      await expect(frontendLane).toHaveAttribute("data-collapsed", "true")

      const laneContent = frontendLane.locator('[data-collapsed="true"]')
      await expect(laneContent).toHaveAttribute("aria-hidden", "true")
    })

    test("collapsed lane still shows count badge", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      const frontendLane = page.getByTestId("swim-lane-lane-label-frontend")
      await frontendLane.getByTestId("collapse-toggle").click()

      await expect(frontendLane.getByLabel("3 issues")).toBeVisible()
    })
  })

  test.describe("Edge Cases", () => {
    test("all issues with same label creates single lane", async ({ page }) => {
      const sameLabel = [
        {
          id: "a1",
          title: "A1",
          status: "open",
          priority: 2,
          issue_type: "task",
          labels: ["frontend"],
          ...timestamps,
        },
        {
          id: "a2",
          title: "A2",
          status: "open",
          priority: 2,
          issue_type: "task",
          labels: ["frontend"],
          ...timestamps,
        },
      ]
      await setupMocks(page, sameLabel)
      await navigateAndWait(page)

      const lanes = page.locator('[data-testid^="swim-lane-lane-label-"]')
      await expect(lanes).toHaveCount(1)
      await expect(
        page.getByRole("heading", { name: "frontend", exact: true })
      ).toBeVisible()
    })

    test("all issues with no labels creates only No Labels lane", async ({
      page,
    }) => {
      const allNoLabels = [
        {
          id: "n1",
          title: "N1",
          status: "open",
          priority: 2,
          issue_type: "task",
          labels: [],
          ...timestamps,
        },
        {
          id: "n2",
          title: "N2",
          status: "open",
          priority: 2,
          issue_type: "task",
          // labels: undefined
          ...timestamps,
        },
      ]
      await setupMocks(page, allNoLabels)
      await navigateAndWait(page)

      const lanes = page.locator('[data-testid^="swim-lane-lane-label-"]')
      await expect(lanes).toHaveCount(1)
      await expect(
        page.getByRole("heading", { name: "No Labels", exact: true })
      ).toBeVisible()
    })

    test("empty issues array shows no label lanes", async ({ page }) => {
      await setupMocks(page, [])
      await page.goto("/?groupBy=label")
      await page.waitForResponse((res) => res.url().includes("/api/ready"))

      // Swim lane board may or may not be visible with empty data
      // The key test is that there are no label lanes
      const lanes = page.locator('[data-testid^="swim-lane-lane-label-"]')
      await expect(lanes).toHaveCount(0)
    })

    test("label lanes sorted alphabetically with No Labels at end", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      const lanes = page.locator('[data-testid^="swim-lane-lane-label-"]')
      const count = await lanes.count()

      // Get titles in order - target header h3 specifically (not issue card titles)
      const titles: string[] = []
      for (let i = 0; i < count; i++) {
        const lane = lanes.nth(i)
        const heading = lane.locator("header h3")
        const title = await heading.textContent()
        if (title) titles.push(title)
      }

      // No Labels should be last
      expect(titles[count - 1]).toBe("No Labels")

      // Other labels sorted alphabetically
      const regularTitles = titles.slice(0, -1)
      const sortedRegular = [...regularTitles].sort()
      expect(regularTitles).toEqual(sortedRegular)
    })

    test("switching from label to assignee regroups issues", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      // Verify label lanes exist
      await expect(
        page.getByRole("heading", { name: "frontend", exact: true })
      ).toBeVisible()

      // Switch to assignee grouping
      await page.getByTestId("groupby-filter").selectOption("assignee")

      // Label lanes gone
      await expect(
        page.getByRole("heading", { name: "frontend", exact: true })
      ).not.toBeVisible()

      // Assignee lanes appear (all issues are unassigned in mockLabelIssues)
      await expect(
        page.getByRole("heading", { name: "Unassigned", exact: true })
      ).toBeVisible()
    })

    test("URL with groupBy=label shows label swim lanes on load", async ({
      page,
    }) => {
      await setupMocks(page)
      await page.goto("/?groupBy=label")
      await page.waitForResponse((res) => res.url().includes("/api/ready"))

      await expect(page.getByTestId("swim-lane-board")).toBeVisible()
      await expect(page.getByTestId("groupby-filter")).toHaveValue("label")
      await expect(
        page.getByRole("heading", { name: "frontend", exact: true })
      ).toBeVisible()
    })
  })
})
