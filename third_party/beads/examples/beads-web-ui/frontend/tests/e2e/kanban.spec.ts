import { test, expect } from "@playwright/test"

/**
 * Mock issues for testing Kanban board rendering.
 * Each issue includes the minimum required fields for API compatibility.
 */
const mockIssues = [
  {
    id: "issue-open-1",
    title: "Open Task 1",
    status: "open",
    priority: 2,
    created_at: "2026-01-24T10:00:00Z",
    updated_at: "2026-01-24T10:00:00Z",
  },
  {
    id: "issue-open-2",
    title: "Open Task 2",
    status: "open",
    priority: 1,
    created_at: "2026-01-24T11:00:00Z",
    updated_at: "2026-01-24T11:00:00Z",
  },
  {
    id: "issue-ip-1",
    title: "In Progress Task",
    status: "in_progress",
    priority: 2,
    created_at: "2026-01-24T12:00:00Z",
    updated_at: "2026-01-24T12:00:00Z",
  },
  {
    id: "issue-review-1",
    title: "Review Task",
    status: "review",
    priority: 2,
    created_at: "2026-01-24T12:30:00Z",
    updated_at: "2026-01-24T12:30:00Z",
  },
  {
    id: "issue-closed-1",
    title: "Closed Task",
    status: "closed",
    priority: 3,
    created_at: "2026-01-24T09:00:00Z",
    updated_at: "2026-01-24T13:00:00Z",
  },
]

test.describe("KanbanBoard", () => {
  test("issues load into correct Kanban columns", async ({ page }) => {
    // Mock the /api/ready endpoint to return our test data
    await page.route("**/api/ready", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          success: true,
          data: mockIssues,
        }),
      })
    })

    // Navigate to the app and wait for API response
    const [response] = await Promise.all([
      page.waitForResponse((res) => res.url().includes("/api/ready") && res.status() === 200),
      page.goto("/"),
    ])
    expect(response.ok()).toBe(true)

    // Wait for the Kanban board to render (columns should appear)
    // Note: Kanban uses semantic column IDs (ready, pending, in_progress, review, done)
    // rather than raw status names (open, closed)
    const readyColumn = page.locator('section[data-status="ready"]')
    const inProgressColumn = page.locator('section[data-status="in_progress"]')
    const reviewColumn = page.locator('section[data-status="review"]')
    const doneColumn = page.locator('section[data-status="done"]')

    await expect(readyColumn).toBeVisible()
    await expect(inProgressColumn).toBeVisible()
    await expect(reviewColumn).toBeVisible()
    await expect(doneColumn).toBeVisible()

    // Verify Ready column contains 2 cards with correct titles (status=open issues)
    const readyCards = readyColumn.locator("article")
    await expect(readyCards).toHaveCount(2)
    await expect(readyColumn.getByText("Open Task 1")).toBeVisible()
    await expect(readyColumn.getByText("Open Task 2")).toBeVisible()

    // Verify In Progress column contains 1 card with correct title
    const inProgressCards = inProgressColumn.locator("article")
    await expect(inProgressCards).toHaveCount(1)
    await expect(inProgressColumn.locator("h3")).toHaveText("In Progress Task")

    // Verify Review column contains 1 card with correct title
    const reviewCards = reviewColumn.locator("article")
    await expect(reviewCards).toHaveCount(1)
    await expect(reviewColumn.locator("h3")).toHaveText("Review Task")

    // Verify Done column contains 1 card with correct title (status=closed issues)
    const doneCards = doneColumn.locator("article")
    await expect(doneCards).toHaveCount(1)
    await expect(doneColumn.locator("h3")).toHaveText("Closed Task")

    // Verify count badges show correct numbers (using aria-label for precise matching)
    await expect(readyColumn.getByLabel("2 issues")).toBeVisible()
    await expect(inProgressColumn.getByLabel("1 issue")).toBeVisible()
    await expect(reviewColumn.getByLabel("1 issue")).toBeVisible()
    await expect(doneColumn.getByLabel("1 issue")).toBeVisible()
  })

  test("handles empty API response gracefully", async ({ page }) => {
    // Mock the /api/ready endpoint to return empty data
    await page.route("**/api/ready", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          success: true,
          data: [],
        }),
      })
    })

    // Navigate to the app and wait for API response
    const [response] = await Promise.all([
      page.waitForResponse((res) => res.url().includes("/api/ready") && res.status() === 200),
      page.goto("/"),
    ])
    expect(response.ok()).toBe(true)

    // Wait for the Kanban board to render
    const readyColumn = page.locator('section[data-status="ready"]')
    await expect(readyColumn).toBeVisible()

    // All columns should have 0 counts (using aria-label for precise matching)
    await expect(readyColumn.getByLabel("0 issues")).toBeVisible()
  })

  test("issues without status default to ready column", async ({ page }) => {
    // Mock with an issue that has no status field
    const issueWithoutStatus = [
      {
        id: "issue-no-status",
        title: "Issue Without Status",
        priority: 2,
        created_at: "2026-01-24T10:00:00Z",
        updated_at: "2026-01-24T10:00:00Z",
        // Note: no status field
      },
    ]

    await page.route("**/api/ready", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          success: true,
          data: issueWithoutStatus,
        }),
      })
    })

    // Navigate to the app and wait for API response
    const [response] = await Promise.all([
      page.waitForResponse((res) => res.url().includes("/api/ready") && res.status() === 200),
      page.goto("/"),
    ])
    expect(response.ok()).toBe(true)

    // The issue should appear in the Ready column (status=open or undefined)
    const readyColumn = page.locator('section[data-status="ready"]')
    await expect(readyColumn).toBeVisible()
    await expect(readyColumn.getByText("Issue Without Status")).toBeVisible()
  })

  test("drag issue from Ready to In Progress updates status and persists", async ({ page }) => {
    // Track API calls for verification
    const patchCalls: { url: string; body: { status?: string } }[] = []
    let hasReloaded = false

    // Mock /api/ready with dynamic response based on reload state
    await page.route("**/api/ready", async (route) => {
      const issues = hasReloaded
        ? mockIssues.map((issue) =>
            issue.id === "issue-open-1"
              ? { ...issue, status: "in_progress" }
              : issue
          )
        : mockIssues

      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: issues }),
      })
    })

    // Mock PATCH /api/issues/:id and capture the call
    await page.route("**/api/issues/*", async (route) => {
      if (route.request().method() === "PATCH") {
        const url = route.request().url()
        const body = route.request().postDataJSON() as { status?: string }
        patchCalls.push({ url, body })

        // Simulate server response with updated timestamp
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

    // Navigate and wait for initial load
    await Promise.all([
      page.waitForResponse((res) => res.url().includes("/api/ready")),
      page.goto("/"),
    ])

    // Get column references
    const readyColumn = page.locator('section[data-status="ready"]')
    const inProgressColumn = page.locator('section[data-status="in_progress"]')

    // Verify initial state
    await expect(readyColumn.locator("article")).toHaveCount(2)
    await expect(inProgressColumn.locator("article")).toHaveCount(1)

    // Verify initial card is visible in Ready column
    const cardToDrag = readyColumn.locator("article").filter({ hasText: "Open Task 1" })
    await expect(cardToDrag).toBeVisible()

    // Get the draggable wrapper (has the @dnd-kit listeners)
    const draggable = cardToDrag.locator("..")
    const dropTarget = inProgressColumn.locator('[data-droppable-id="in_progress"]')

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

    // Use Playwright's dispatchEvent to fire pointer events
    // This creates events that go through the browser's event system
    await draggable.dispatchEvent("pointerdown", {
      clientX: startX,
      clientY: startY,
      button: 0,
      buttons: 1,
      pointerId: 1,
      pointerType: "mouse",
      isPrimary: true,
    })

    // Wait for drag to activate
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

    // Wait for the PATCH API call to complete (avoids arbitrary timeout)
    await page.waitForResponse(
      (res) =>
        res.url().includes("/api/issues/issue-open-1") &&
        res.request().method() === "PATCH"
    )

    // Verify UI updated (card moved to In Progress)
    // Wait for visibility first to ensure component has re-rendered
    await expect(inProgressColumn.getByText("Open Task 1")).toBeVisible()
    await expect(readyColumn.locator("article")).toHaveCount(1)
    await expect(inProgressColumn.locator("article")).toHaveCount(2)

    // Verify API call was made correctly
    expect(patchCalls).toHaveLength(1)
    expect(patchCalls[0].url).toContain("/api/issues/issue-open-1")
    expect(patchCalls[0].body).toEqual({ status: "in_progress" })

    // Verify persistence on reload
    hasReloaded = true
    await page.reload()

    // Wait for data to load after reload
    await page.waitForResponse((res) => res.url().includes("/api/ready"))

    // Verify the card is still in In Progress after reload
    // Wait for visibility first to ensure component has re-rendered
    await expect(inProgressColumn.getByText("Open Task 1")).toBeVisible()
    await expect(inProgressColumn.locator("article")).toHaveCount(2)
  })

  test("shows error toast and rolls back card on drag failure", async ({ page }) => {
    // Mock /api/ready with a single test issue in 'open' column
    const dragTestIssue = [
      {
        id: "drag-fail-issue",
        title: "Issue To Drag",
        status: "open",
        priority: 2,
        created_at: "2026-01-24T10:00:00Z",
        updated_at: "2026-01-24T10:00:00Z",
      },
    ]

    await page.route("**/api/ready", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: dragTestIssue }),
      })
    })

    // Mock PATCH /api/issues/{id} to fail with 500
    await page.route("**/api/issues/*", async (route) => {
      if (route.request().method() === "PATCH") {
        await route.fulfill({
          status: 500,
          contentType: "application/json",
          body: JSON.stringify({ success: false, error: "Server error" }),
        })
      } else {
        await route.continue()
      }
    })

    // Navigate and wait for board to render
    await Promise.all([
      page.waitForResponse((res) => res.url().includes("/api/ready") && res.status() === 200),
      page.goto("/"),
    ])

    const readyColumn = page.locator('section[data-status="ready"]')
    const inProgressColumn = page.locator('section[data-status="in_progress"]')
    await expect(readyColumn).toBeVisible()

    // Verify card is in Ready column initially
    const card = readyColumn.locator("article").filter({ hasText: "Issue To Drag" })
    await expect(card).toBeVisible()

    // Wait for the PATCH request when drag completes
    const patchPromise = page.waitForResponse(
      (res) => res.url().includes("/api/issues/") && res.request().method() === "PATCH",
      { timeout: 10000 }
    )

    // Find the draggable wrapper using its accessible role and name
    const draggableWrapper = page.getByRole("button", { name: "Issue: Issue To Drag" })
    await expect(draggableWrapper).toBeVisible()

    // Get the droppable target
    const dropTarget = inProgressColumn.locator('[data-droppable-id="in_progress"]')
    await expect(dropTarget).toBeVisible()

    // Get bounding boxes for pointer event coordinates
    const dragBox = await draggableWrapper.boundingBox()
    const dropBox = await dropTarget.boundingBox()
    if (!dragBox || !dropBox) throw new Error("Could not get element bounds")

    const startX = dragBox.x + dragBox.width / 2
    const startY = dragBox.y + dragBox.height / 2
    const endX = dropBox.x + dropBox.width / 2
    const endY = dropBox.y + dropBox.height / 2

    // Use dispatchEvent to fire pointer events that @dnd-kit's PointerSensor handles
    await draggableWrapper.dispatchEvent("pointerdown", {
      bubbles: true,
      cancelable: true,
      pointerId: 1,
      pointerType: "mouse",
      isPrimary: true,
      clientX: startX,
      clientY: startY,
      button: 0,
      buttons: 1,
    })

    // Move beyond activation threshold (5px)
    await page.mouse.move(startX + 10, startY)
    await page.waitForTimeout(50) // Allow time for drag activation

    // Move to drop target
    await page.mouse.move(endX, endY, { steps: 5 })
    await page.waitForTimeout(50)

    // Fire pointerup on the page to complete the drag
    await page.mouse.up()

    // Wait for PATCH to complete (will fail if drag didn't trigger API call)
    await patchPromise

    // Allow time for async error handling and React state updates
    await page.waitForTimeout(500)

    // Wait for and verify error toast appears
    const toast = page.getByTestId("error-toast")
    await expect(toast).toBeVisible({ timeout: 5000 })
    await expect(toast).toHaveAttribute("role", "alert")

    // Verify card rolled back to Ready column
    await expect(readyColumn.locator("article").filter({ hasText: "Issue To Drag" })).toBeVisible()
    await expect(inProgressColumn.locator("article")).toHaveCount(0)
  })

  test("drag shows visual feedback during operation", async ({ page }) => {
    // Single test issue to drag
    const testIssue = [
      {
        id: "visual-test-issue",
        title: "Visual Feedback Test",
        status: "open",
        priority: 2,
        created_at: "2026-01-24T10:00:00Z",
        updated_at: "2026-01-24T10:00:00Z",
      },
    ]

    await page.route("**/api/ready", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: testIssue }),
      })
    })

    // Mock PATCH to succeed (we'll complete the drag)
    await page.route("**/api/issues/*", async (route) => {
      if (route.request().method() === "PATCH") {
        const body = route.request().postDataJSON() as { status?: string }
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            success: true,
            data: { ...testIssue[0], status: body.status },
          }),
        })
      } else {
        await route.continue()
      }
    })

    // Navigate and wait for board to render
    await Promise.all([
      page.waitForResponse((res) => res.url().includes("/api/ready") && res.status() === 200),
      page.goto("/"),
    ])

    const readyColumn = page.locator('section[data-status="ready"]')
    const inProgressColumn = page.locator('section[data-status="in_progress"]')
    await expect(readyColumn).toBeVisible()

    // Get draggable wrapper (has accessible role)
    const draggableWrapper = page.getByRole("button", { name: "Issue: Visual Feedback Test" })
    await expect(draggableWrapper).toBeVisible()

    // Get drop target
    const dropTarget = inProgressColumn.locator('[data-droppable-id="in_progress"]')
    await expect(dropTarget).toBeVisible()

    // Get bounding boxes
    const dragBox = await draggableWrapper.boundingBox()
    const dropBox = await dropTarget.boundingBox()
    if (!dragBox || !dropBox) throw new Error("Could not get element bounds")

    const startX = dragBox.x + dragBox.width / 2
    const startY = dragBox.y + dragBox.height / 2
    const endX = dropBox.x + dropBox.width / 2
    const endY = dropBox.y + dropBox.height / 2

    // Use dispatchEvent for all pointer events (consistent with @dnd-kit's PointerSensor)
    // 1. Initiate drag with pointerdown
    await draggableWrapper.dispatchEvent("pointerdown", {
      clientX: startX,
      clientY: startY,
      button: 0,
      buttons: 1,
      pointerId: 1,
      pointerType: "mouse",
      isPrimary: true,
    })

    // 2. Move past activation threshold (>5px) to trigger drag activation
    await page.dispatchEvent("body", "pointermove", {
      clientX: startX + 10,
      clientY: startY,
      button: 0,
      buttons: 1,
      pointerId: 1,
      pointerType: "mouse",
      isPrimary: true,
    })

    // 3. Wait for drag to activate (deterministic) and verify visual feedback
    await expect(draggableWrapper).toHaveAttribute("data-dragging", "true", { timeout: 2000 })

    // Verify opacity is 0.5 via evaluate
    const sourceOpacity = await draggableWrapper.evaluate((el) => {
      return window.getComputedStyle(el).opacity
    })
    expect(sourceOpacity).toBe("0.5")

    // Verify cursor is "grabbing" on the draggable element
    const cursorStyle = await draggableWrapper.evaluate((el) => {
      return window.getComputedStyle(el).cursor
    })
    expect(cursorStyle).toBe("grabbing")

    // 4. Verify DragOverlay is visible during drag
    // The DragOverlay renders a DraggableIssueCard with isOverlay prop,
    // which has the .overlay class (from DraggableIssueCard.module.css).
    // Target by class pattern to reliably find the overlay element.
    const overlayWrapper = page.locator('[class*="overlay"]').filter({ has: page.locator("article") })
    await expect(overlayWrapper).toBeVisible()

    // The overlay wrapper should have elevated shadow (box-shadow from CSS)
    const overlayShadow = await overlayWrapper.evaluate((el) => {
      return window.getComputedStyle(el).boxShadow
    })
    // Should have a non-empty box-shadow (the exact value depends on CSS variables)
    expect(overlayShadow).not.toBe("none")

    // 5. Move to drop target column
    await page.dispatchEvent("body", "pointermove", {
      clientX: endX,
      clientY: endY,
      button: 0,
      buttons: 1,
      pointerId: 1,
      pointerType: "mouse",
      isPrimary: true,
    })

    // 6. Wait for drop target to receive hover state (deterministic)
    await expect(dropTarget).toHaveAttribute("data-is-over", "true", { timeout: 2000 })

    // Wait for CSS transition (0.15s defined in StatusColumn.module.css)
    await page.waitForTimeout(200)

    // Verify drop target has highlighted background and border via class
    // The .contentDropOver class should be applied when data-is-over is true
    const hasDropOverClass = await dropTarget.evaluate((el) => {
      return Array.from(el.classList).some(c => c.includes("contentDropOver"))
    })
    expect(hasDropOverClass).toBe(true)

    // 7. Complete drag with pointerup
    await page.dispatchEvent("body", "pointerup", {
      clientX: endX,
      clientY: endY,
      button: 0,
      buttons: 0,
      pointerId: 1,
      pointerType: "mouse",
      isPrimary: true,
    })

    // Wait for the PATCH API call to complete
    await page.waitForResponse(
      (res) =>
        res.url().includes("/api/issues/visual-test-issue") &&
        res.request().method() === "PATCH"
    )

    // 8. Verify visual feedback resets
    // The card has moved to the new column, so we check the new draggable
    const movedCard = inProgressColumn.getByRole("button", { name: "Issue: Visual Feedback Test" })
    await expect(movedCard).toBeVisible()

    // The moved card should not have data-dragging
    const hasDragging = await movedCard.getAttribute("data-dragging")
    expect(hasDragging).toBeNull()

    // Opacity should be back to 1
    const finalOpacity = await movedCard.evaluate((el) => {
      return window.getComputedStyle(el).opacity
    })
    expect(finalOpacity).toBe("1")

    // DragOverlay should no longer be visible (only one card should exist now)
    const allCards = page.locator("article").filter({ hasText: "Visual Feedback Test" })
    await expect(allCards).toHaveCount(1)

    // Drop target should no longer have data-is-over
    const isOverAttr = await dropTarget.getAttribute("data-is-over")
    expect(isOverAttr).toBeNull()

    // Verify drop target has reset (contentDropOver class removed)
    const hasDropOverClassAfter = await dropTarget.evaluate((el) => {
      return Array.from(el.classList).some((c) => c.includes("contentDropOver"))
    })
    expect(hasDropOverClassAfter).toBe(false)
  })

  test("drag issue from Review to Closed updates status", async ({ page }) => {
    // Single test issue in review status
    const reviewIssue = [
      {
        id: "review-to-closed-issue",
        title: "Review to Close Task",
        status: "review",
        priority: 2,
        created_at: "2026-01-24T10:00:00Z",
        updated_at: "2026-01-24T10:00:00Z",
      },
    ]

    // Track PATCH calls
    const patchCalls: { url: string; body: { status?: string } }[] = []

    await page.route("**/api/ready", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: reviewIssue }),
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
            data: { ...reviewIssue[0], status: body.status },
          }),
        })
      } else {
        await route.continue()
      }
    })

    // Navigate and wait for board to render
    await Promise.all([
      page.waitForResponse((res) => res.url().includes("/api/ready") && res.status() === 200),
      page.goto("/"),
    ])

    const reviewColumn = page.locator('section[data-status="review"]')
    const doneColumn = page.locator('section[data-status="done"]')
    await expect(reviewColumn).toBeVisible()

    // Verify card is in Review column initially
    const card = reviewColumn.locator("article").filter({ hasText: "Review to Close Task" })
    await expect(card).toBeVisible()

    // Get draggable wrapper (use .first() since both wrapper and card have button role)
    const draggableWrapper = page.getByRole("button", { name: "Issue: Review to Close Task" }).first()
    await expect(draggableWrapper).toBeVisible()

    // Get drop target (done column's targetStatus is 'closed')
    const dropTarget = doneColumn.locator('[data-droppable-id="done"]')
    await expect(dropTarget).toBeVisible()

    // Get bounding boxes
    const dragBox = await draggableWrapper.boundingBox()
    const dropBox = await dropTarget.boundingBox()
    if (!dragBox || !dropBox) throw new Error("Could not get element bounds")

    const startX = dragBox.x + dragBox.width / 2
    const startY = dragBox.y + dragBox.height / 2
    const endX = dropBox.x + dropBox.width / 2
    const endY = dropBox.y + dropBox.height / 2

    // Perform drag operation
    await draggableWrapper.dispatchEvent("pointerdown", {
      clientX: startX,
      clientY: startY,
      button: 0,
      buttons: 1,
      pointerId: 1,
      pointerType: "mouse",
      isPrimary: true,
    })

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
        res.url().includes("/api/issues/review-to-closed-issue") &&
        res.request().method() === "PATCH"
    )

    // Verify card moved to Done column
    await expect(doneColumn.getByText("Review to Close Task")).toBeVisible()
    await expect(reviewColumn.locator("article")).toHaveCount(0)

    // Verify API call was made correctly (targetStatus for 'done' column is 'closed')
    expect(patchCalls).toHaveLength(1)
    expect(patchCalls[0].body).toEqual({ status: "closed" })
  })

  test("drag issue from Ready to Review updates status", async ({ page }) => {
    // Single test issue in open status
    const openIssue = [
      {
        id: "open-to-review-issue",
        title: "Open to Review Task",
        status: "open",
        priority: 2,
        created_at: "2026-01-24T10:00:00Z",
        updated_at: "2026-01-24T10:00:00Z",
      },
    ]

    // Track PATCH calls
    const patchCalls: { url: string; body: { status?: string } }[] = []

    await page.route("**/api/ready", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: openIssue }),
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
            data: { ...openIssue[0], status: body.status },
          }),
        })
      } else {
        await route.continue()
      }
    })

    // Navigate and wait for board to render
    await Promise.all([
      page.waitForResponse((res) => res.url().includes("/api/ready") && res.status() === 200),
      page.goto("/"),
    ])

    const readyColumn = page.locator('section[data-status="ready"]')
    const reviewColumn = page.locator('section[data-status="review"]')
    await expect(readyColumn).toBeVisible()

    // Verify card is in Ready column initially
    const card = readyColumn.locator("article").filter({ hasText: "Open to Review Task" })
    await expect(card).toBeVisible()

    // Get draggable wrapper (use .first() since both wrapper and card have button role)
    const draggableWrapper = page.getByRole("button", { name: "Issue: Open to Review Task" }).first()
    await expect(draggableWrapper).toBeVisible()

    // Get drop target
    const dropTarget = reviewColumn.locator('[data-droppable-id="review"]')
    await expect(dropTarget).toBeVisible()

    // Get bounding boxes
    const dragBox = await draggableWrapper.boundingBox()
    const dropBox = await dropTarget.boundingBox()
    if (!dragBox || !dropBox) throw new Error("Could not get element bounds")

    const startX = dragBox.x + dragBox.width / 2
    const startY = dragBox.y + dragBox.height / 2
    const endX = dropBox.x + dropBox.width / 2
    const endY = dropBox.y + dropBox.height / 2

    // Perform drag operation
    await draggableWrapper.dispatchEvent("pointerdown", {
      clientX: startX,
      clientY: startY,
      button: 0,
      buttons: 1,
      pointerId: 1,
      pointerType: "mouse",
      isPrimary: true,
    })

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
        res.url().includes("/api/issues/open-to-review-issue") &&
        res.request().method() === "PATCH"
    )

    // Verify card moved to Review column
    await expect(reviewColumn.getByText("Open to Review Task")).toBeVisible()
    await expect(readyColumn.locator("article")).toHaveCount(0)

    // Verify API call was made correctly
    expect(patchCalls).toHaveLength(1)
    expect(patchCalls[0].body).toEqual({ status: "review" })
  })
})
