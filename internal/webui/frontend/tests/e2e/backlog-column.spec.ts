import { test, expect } from "@playwright/test"

/**
 * Helper to set up common API route mocks for the App.
 * Mocks /api/ready, /api/blocked, /api/stats, and /api/events.
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

/** Navigate and wait for /api/ready to respond */
async function navigateAndWait(page: import("@playwright/test").Page) {
  const [response] = await Promise.all([
    page.waitForResponse(
      (res) => res.url().includes("/api/ready") && res.status() === 200
    ),
    page.goto("/"),
  ])
  expect(response.ok()).toBe(true)
}

const NOW = "2026-01-24T10:00:00Z"

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

test.describe("Blocked column filters", () => {
  test("status=blocked issues appear in Blocked column", async ({ page }) => {
    const blockedIssue = makeIssue({
      id: "blocked-1",
      title: "Blocked Task",
      status: "blocked",
    })

    await setupMocks(page, { issues: [blockedIssue] })
    await navigateAndWait(page)

    const blockedColumn = page.locator('section[data-status="blocked"]')
    await expect(blockedColumn).toBeVisible()

    // Card should be in Blocked column
    await expect(blockedColumn.getByText("Blocked Task")).toBeVisible()
    await expect(blockedColumn.locator("article")).toHaveCount(1)
    await expect(blockedColumn.getByLabel("1 issue")).toBeVisible()

    // Card should NOT be in other columns
    const readyColumn = page.locator('section[data-status="ready"]')
    const backlogColumn = page.locator('section[data-status="backlog"]')
    const inProgressColumn = page.locator('section[data-status="in_progress"]')
    const reviewColumn = page.locator('section[data-status="review"]')
    const doneColumn = page.locator('section[data-status="done"]')

    await expect(readyColumn.locator("article")).toHaveCount(0)
    await expect(backlogColumn.locator("article")).toHaveCount(0)
    await expect(inProgressColumn.locator("article")).toHaveCount(0)
    await expect(reviewColumn.locator("article")).toHaveCount(0)
    await expect(doneColumn.locator("article")).toHaveCount(0)
  })

  test("status=deferred issues appear in Backlog column with badge", async ({
    page,
  }) => {
    const deferredIssue = makeIssue({
      id: "deferred-1",
      title: "Deferred Task",
      status: "deferred",
    })

    await setupMocks(page, { issues: [deferredIssue] })
    await navigateAndWait(page)

    const backlogColumn = page.locator('section[data-status="backlog"]')
    await expect(backlogColumn).toBeVisible()

    // Card should be in Backlog
    await expect(backlogColumn.getByText("Deferred Task")).toBeVisible()
    await expect(backlogColumn.locator("article")).toHaveCount(1)

    // Deferred badge should be visible
    const deferredBadge = backlogColumn.locator('[aria-label="Deferred"]')
    await expect(deferredBadge).toBeVisible()

    // Not in other columns
    await expect(
      page.locator('section[data-status="ready"]').locator("article")
    ).toHaveCount(0)
    await expect(
      page.locator('section[data-status="review"]').locator("article")
    ).toHaveCount(0)
  })

  test("dependency-blocked issues appear in Blocked column", async ({
    page,
  }) => {
    // An open issue that has dependency blockers
    const depBlockedIssue = makeIssue({
      id: "dep-blocked-1",
      title: "Dep Blocked Task",
      status: "open",
    })

    // The /api/blocked endpoint returns this issue with blocked info
    const blockedData = {
      ...depBlockedIssue,
      blocked_by_count: 2,
      blocked_by: ["blocker-a", "blocker-b"],
    }

    await setupMocks(page, {
      issues: [depBlockedIssue],
      blockedIssues: [blockedData],
    })
    await navigateAndWait(page)

    const blockedColumn = page.locator('section[data-status="blocked"]')
    await expect(blockedColumn).toBeVisible()

    // Card should be in Blocked (not Ready) because it has blockers
    await expect(blockedColumn.getByText("Dep Blocked Task")).toBeVisible()
    await expect(blockedColumn.locator("article")).toHaveCount(1)

    // Should NOT be in Ready column
    await expect(
      page.locator('section[data-status="ready"]').locator("article")
    ).toHaveCount(0)
  })

  test("status=blocked issues do NOT appear in Review column", async ({
    page,
  }) => {
    // A plain status=blocked issue (no [Need Review] in title)
    const blockedIssue = makeIssue({
      id: "blocked-not-review",
      title: "Blocked But Not Review",
      status: "blocked",
    })

    await setupMocks(page, { issues: [blockedIssue] })
    await navigateAndWait(page)

    const blockedColumn = page.locator('section[data-status="blocked"]')
    const reviewColumn = page.locator('section[data-status="review"]')

    // Should be in Blocked column
    await expect(
      blockedColumn.getByText("Blocked But Not Review")
    ).toBeVisible()

    // Should NOT be in Review
    await expect(reviewColumn.locator("article")).toHaveCount(0)
  })

  test("epic issues are excluded from all kanban columns", async ({
    page,
  }) => {
    // An epic with blocked status - should not appear anywhere
    const epicBlocked = makeIssue({
      id: "epic-1",
      title: "Epic Issue",
      status: "blocked",
      issue_type: "epic",
    })
    // An epic with open status and blockers - also excluded
    const epicOpen = makeIssue({
      id: "epic-2",
      title: "Epic Open Issue",
      status: "open",
      issue_type: "epic",
    })

    const blockedData = {
      ...epicOpen,
      blocked_by_count: 1,
      blocked_by: ["some-blocker"],
    }

    await setupMocks(page, {
      issues: [epicBlocked, epicOpen],
      blockedIssues: [blockedData],
    })
    await navigateAndWait(page)

    // No epic should appear in any column (except Done for closed epics)
    const readyColumn = page.locator('section[data-status="ready"]')
    const backlogColumn = page.locator('section[data-status="backlog"]')
    const blockedColumn = page.locator('section[data-status="blocked"]')
    const inProgressColumn = page.locator('section[data-status="in_progress"]')
    const reviewColumn = page.locator('section[data-status="review"]')
    const doneColumn = page.locator('section[data-status="done"]')

    await expect(readyColumn).toBeVisible()

    await expect(readyColumn.locator("article")).toHaveCount(0)
    await expect(backlogColumn.locator("article")).toHaveCount(0)
    await expect(blockedColumn.locator("article")).toHaveCount(0)
    await expect(inProgressColumn.locator("article")).toHaveCount(0)
    await expect(reviewColumn.locator("article")).toHaveCount(0)
    await expect(doneColumn.locator("article")).toHaveCount(0)
  })
})

test.describe("Backlog column header and styling", () => {
  test("Backlog column shows 'Backlog' title", async ({
    page,
  }) => {
    const deferredIssue = makeIssue({
      id: "header-test-1",
      title: "Header Test Issue",
      status: "deferred",
    })

    await setupMocks(page, { issues: [deferredIssue] })
    await navigateAndWait(page)

    const backlogColumn = page.locator('section[data-status="backlog"]')
    await expect(backlogColumn).toBeVisible()

    // Column title says "Backlog"
    const title = backlogColumn.locator("h2")
    await expect(title).toHaveText("Backlog")

    // Count badge shows correct number
    await expect(backlogColumn.getByLabel("1 issue")).toBeVisible()
  })

  test("Backlog column has data-column-type='backlog' attribute", async ({
    page,
  }) => {
    const deferredIssue = makeIssue({
      id: "attr-test-1",
      title: "Attr Test Issue",
      status: "deferred",
    })

    await setupMocks(page, { issues: [deferredIssue] })
    await navigateAndWait(page)

    const backlogColumn = page.locator('section[data-status="backlog"]')
    await expect(backlogColumn).toHaveAttribute("data-column-type", "backlog")
  })

  test("Backlog column applies muted background styling", async ({
    page,
  }) => {
    const deferredIssue = makeIssue({
      id: "style-test-1",
      title: "Style Test Issue",
      status: "deferred",
    })

    await setupMocks(page, { issues: [deferredIssue] })
    await navigateAndWait(page)

    const backlogColumn = page.locator('section[data-status="backlog"]')
    await expect(backlogColumn).toBeVisible()

    // Column background should use tertiary bg (different from default secondary)
    const openColumn = page.locator('section[data-status="ready"]')
    await expect(openColumn).toBeVisible()

    const backlogBg = await backlogColumn.evaluate(
      (el) => window.getComputedStyle(el).backgroundColor
    )
    const openBg = await openColumn.evaluate(
      (el) => window.getComputedStyle(el).backgroundColor
    )
    // Backlog should have a different (muted) background than Open (normal)
    expect(backlogBg).not.toBe(openBg)
  })

  test("Backlog column title uses secondary text color", async ({ page }) => {
    const deferredIssue = makeIssue({
      id: "title-color-1",
      title: "Title Color Test",
      status: "deferred",
    })

    await setupMocks(page, { issues: [deferredIssue] })
    await navigateAndWait(page)

    const backlogColumn = page.locator('section[data-status="backlog"]')
    const openColumn = page.locator('section[data-status="ready"]')
    await expect(backlogColumn).toBeVisible()
    await expect(openColumn).toBeVisible()

    // Backlog title should use secondary color (different from Open's default)
    const backlogTitleColor = await backlogColumn
      .locator("h2")
      .evaluate((el) => window.getComputedStyle(el).color)
    const openTitleColor = await openColumn
      .locator("h2")
      .evaluate((el) => window.getComputedStyle(el).color)
    expect(backlogTitleColor).not.toBe(openTitleColor)
  })

  test("Backlog cards have dimmed opacity and desaturated filter", async ({
    page,
  }) => {
    const deferredIssue = makeIssue({
      id: "card-dim-1",
      title: "Dimmed Card Test",
      status: "deferred",
    })

    await setupMocks(page, { issues: [deferredIssue] })
    await navigateAndWait(page)

    const backlogColumn = page.locator('section[data-status="backlog"]')
    const card = backlogColumn.locator("article")
    await expect(card).toBeVisible()

    // Card should have data-in-backlog attribute
    await expect(card).toHaveAttribute("data-in-backlog", "true")

    // Card opacity should be 0.7
    const opacity = await card.evaluate(
      (el) => window.getComputedStyle(el).opacity
    )
    expect(opacity).toBe("0.7")

    // Card should have desaturated filter (saturate(0.5))
    const filter = await card.evaluate(
      (el) => window.getComputedStyle(el).filter
    )
    expect(filter).toContain("saturate(0.5)")
  })

  test("Backlog and Blocked columns with respective issue types render correctly", async ({
    page,
  }) => {
    // Create one of each type: deferred goes to Backlog, blocked goes to Blocked column
    const statusBlocked = makeIssue({
      id: "all-types-1",
      title: "Explicitly Blocked",
      status: "blocked",
    })
    const statusDeferred = makeIssue({
      id: "all-types-2",
      title: "Deferred Issue",
      status: "deferred",
    })
    const depBlocked = makeIssue({
      id: "all-types-3",
      title: "Dependency Blocked",
      status: "open",
    })
    // Also add a normal ready issue to verify column separation
    const normalReady = makeIssue({
      id: "all-types-4",
      title: "Normal Ready Issue",
      status: "open",
    })

    const blockedData = {
      ...depBlocked,
      blocked_by_count: 1,
      blocked_by: ["some-blocker"],
    }

    await setupMocks(page, {
      issues: [statusBlocked, statusDeferred, depBlocked, normalReady],
      blockedIssues: [blockedData],
    })
    await navigateAndWait(page)

    const backlogColumn = page.locator('section[data-status="backlog"]')
    const blockedColumn = page.locator('section[data-status="blocked"]')
    const readyColumn = page.locator('section[data-status="ready"]')

    // Backlog should have 1 issue (only deferred)
    await expect(backlogColumn.locator("article")).toHaveCount(1)
    await expect(backlogColumn.getByLabel("1 issue")).toBeVisible()
    await expect(backlogColumn.getByText("Deferred Issue")).toBeVisible()

    // Blocked should have 2 issues (status=blocked and dep-blocked)
    await expect(blockedColumn.locator("article")).toHaveCount(2)
    await expect(blockedColumn.getByLabel("2 issues")).toBeVisible()
    await expect(
      blockedColumn.getByText("Explicitly Blocked")
    ).toBeVisible()
    await expect(
      blockedColumn.getByText("Dependency Blocked")
    ).toBeVisible()

    // Ready should have 1 issue (normal open)
    await expect(readyColumn.locator("article")).toHaveCount(1)
    await expect(readyColumn.getByText("Normal Ready Issue")).toBeVisible()

    // Deferred badge should be visible on the deferred issue in Backlog
    await expect(
      backlogColumn.locator('[aria-label="Deferred"]')
    ).toBeVisible()
  })

  test("empty Backlog column renders with 0 issues badge", async ({
    page,
  }) => {
    // Only a normal open issue, no deferred issues (Backlog only has deferred)
    const normalIssue = makeIssue({
      id: "empty-backlog-1",
      title: "Normal Issue",
      status: "open",
    })

    await setupMocks(page, { issues: [normalIssue] })
    await navigateAndWait(page)

    const backlogColumn = page.locator('section[data-status="backlog"]')
    await expect(backlogColumn).toBeVisible()
    await expect(backlogColumn.locator("article")).toHaveCount(0)
    await expect(backlogColumn.getByLabel("0 issues")).toBeVisible()
  })

  test("column order is Backlog, Open, Blocked, In Progress, Needs Review, Done", async ({
    page,
  }) => {
    const issues = [
      makeIssue({ id: "order-1", title: "Open Issue", status: "open" }),
      makeIssue({
        id: "order-2",
        title: "Blocked Issue",
        status: "blocked",
      }),
      makeIssue({
        id: "order-3",
        title: "IP Issue",
        status: "in_progress",
      }),
      makeIssue({
        id: "order-4",
        title: "[Need Review] Review Issue",
        status: "open",
      }),
      makeIssue({ id: "order-5", title: "Done Issue", status: "closed" }),
      makeIssue({ id: "order-6", title: "Deferred Issue", status: "deferred" }),
    ]

    await setupMocks(page, { issues })
    await navigateAndWait(page)

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
})

test.describe("Blocked column drag-drop", () => {
  test("drag from Blocked to Done succeeds", async ({ page }) => {
    const blockedIssue = makeIssue({
      id: "blocked-to-done",
      title: "Blocked To Done Task",
      status: "blocked",
    })

    const patchCalls: { url: string; body: { status?: string } }[] = []

    await setupMocks(page, { issues: [blockedIssue] })

    // Mock PATCH /api/issues/:id
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
            data: { ...blockedIssue, status: body.status },
          }),
        })
      } else {
        await route.continue()
      }
    })

    await navigateAndWait(page)

    const blockedColumn = page.locator('section[data-status="blocked"]')
    const doneColumn = page.locator('section[data-status="done"]')
    await expect(blockedColumn.getByText("Blocked To Done Task")).toBeVisible()

    // Get draggable and drop target
    const draggable = page
      .getByRole("button", { name: "Issue: Blocked To Done Task" })
      .first()
    const dropTarget = doneColumn.locator('[data-droppable-id="done"]')
    await expect(draggable).toBeVisible()
    await expect(dropTarget).toBeVisible()

    const dragBox = await draggable.boundingBox()
    const dropBox = await dropTarget.boundingBox()
    if (!dragBox || !dropBox) throw new Error("Could not get bounding boxes")

    const startX = dragBox.x + dragBox.width / 2
    const startY = dragBox.y + dragBox.height / 2
    const endX = dropBox.x + dropBox.width / 2
    const endY = dropBox.y + dropBox.height / 2

    // Perform drag: pointerdown → pointermove past threshold → pointermove to target → pointerup
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

    // Wait for PATCH API call
    await page.waitForResponse(
      (res) =>
        res.url().includes("/api/issues/blocked-to-done") &&
        res.request().method() === "PATCH"
    )

    // Verify card moved to Done
    await expect(doneColumn.getByText("Blocked To Done Task")).toBeVisible()
    await expect(blockedColumn.locator("article")).toHaveCount(0)

    // Verify API call sent correct status
    expect(patchCalls).toHaveLength(1)
    expect(patchCalls[0].body).toEqual({ status: "closed" })
  })

  test("Blocked column is not a valid drop target", async ({ page }) => {
    // One issue in Open, one in Blocked
    const openIssue = makeIssue({
      id: "open-issue",
      title: "Open Issue",
      status: "open",
    })
    const blockedIssue = makeIssue({
      id: "blocked-issue",
      title: "Blocked Issue",
      status: "blocked",
    })

    let patchCalled = false

    await setupMocks(page, { issues: [openIssue, blockedIssue] })

    await page.route("**/api/issues/*", async (route) => {
      if (route.request().method() === "PATCH") {
        patchCalled = true
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: openIssue }),
        })
      } else {
        await route.continue()
      }
    })

    await navigateAndWait(page)

    const openColumn = page.locator('section[data-status="ready"]')
    const blockedColumn = page.locator('section[data-status="blocked"]')

    await expect(openColumn.getByText("Open Issue")).toBeVisible()
    await expect(blockedColumn.getByText("Blocked Issue")).toBeVisible()

    // Try to drag from Open to Blocked
    const draggable = page
      .getByRole("button", { name: "Issue: Open Issue" })
      .first()
    await expect(draggable).toBeVisible()

    const dragBox = await draggable.boundingBox()
    const blockedBox = await blockedColumn.boundingBox()
    if (!dragBox || !blockedBox)
      throw new Error("Could not get bounding boxes")

    const startX = dragBox.x + dragBox.width / 2
    const startY = dragBox.y + dragBox.height / 2
    const endX = blockedBox.x + blockedBox.width / 2
    const endY = blockedBox.y + blockedBox.height / 2

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

    // Wait a moment to ensure no PATCH would be sent
    await page.waitForTimeout(500)

    // Blocked should NOT have data-is-over (droppableDisabled: true)
    // No PATCH call should have been made
    expect(patchCalled).toBe(false)

    // Card should still be in Open column
    await expect(openColumn.getByText("Open Issue")).toBeVisible()
    await expect(openColumn.locator("article")).toHaveCount(1)
  })

  test("drag from Blocked to non-Done column is rejected", async ({
    page,
  }) => {
    const blockedIssue = makeIssue({
      id: "blocked-no-ip",
      title: "Cannot Drag To IP",
      status: "blocked",
    })

    let patchCalled = false

    await setupMocks(page, { issues: [blockedIssue] })

    await page.route("**/api/issues/*", async (route) => {
      if (route.request().method() === "PATCH") {
        patchCalled = true
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: blockedIssue }),
        })
      } else {
        await route.continue()
      }
    })

    await navigateAndWait(page)

    const blockedColumn = page.locator('section[data-status="blocked"]')
    const inProgressColumn = page.locator('section[data-status="in_progress"]')

    await expect(blockedColumn.getByText("Cannot Drag To IP")).toBeVisible()

    // Try to drag from Blocked to In Progress
    const draggable = page
      .getByRole("button", { name: "Issue: Cannot Drag To IP" })
      .first()
    const dropTarget = inProgressColumn.locator(
      '[data-droppable-id="in_progress"]'
    )
    await expect(draggable).toBeVisible()
    await expect(dropTarget).toBeVisible()

    const dragBox = await draggable.boundingBox()
    const dropBox = await dropTarget.boundingBox()
    if (!dragBox || !dropBox) throw new Error("Could not get bounding boxes")

    const startX = dragBox.x + dragBox.width / 2
    const startY = dragBox.y + dragBox.height / 2
    const endX = dropBox.x + dropBox.width / 2
    const endY = dropBox.y + dropBox.height / 2

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

    // Wait to ensure no PATCH would fire
    await page.waitForTimeout(500)

    // No PATCH call should have been made
    expect(patchCalled).toBe(false)

    // Card should still be in Blocked
    await expect(blockedColumn.getByText("Cannot Drag To IP")).toBeVisible()
    await expect(blockedColumn.locator("article")).toHaveCount(1)
  })
})
