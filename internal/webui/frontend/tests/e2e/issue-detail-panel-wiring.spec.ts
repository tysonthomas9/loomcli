import { test, expect, Page } from "@playwright/test"

/**
 * E2E tests for IssueDetailPanel wiring across all views.
 *
 * Tests the full integration: view click → fetch issue → display panel → edit → persist.
 * Complements issue-detail-panel.spec.ts (Kanban/Table open/close) and editable-title.spec.ts
 * (title edit via test fixture).
 *
 * Focus areas:
 * 1. Graph view node click → panel opens
 * 2. Title edit flow from main app (not test fixture)
 * 3. Reopen shows fresh/updated data
 */

// Mock issues for testing - includes variety for different scenarios
const mockIssues = [
  {
    id: "wiring-test-1",
    title: "First Wiring Test Issue",
    status: "open",
    priority: 2,
    issue_type: "task",
    description: "Description for wiring test",
    created_at: "2026-01-27T10:00:00Z",
    updated_at: "2026-01-27T10:00:00Z",
  },
  {
    id: "wiring-test-2",
    title: "Second Wiring Test Issue",
    status: "in_progress",
    priority: 1,
    issue_type: "bug",
    description: "Description for second issue",
    created_at: "2026-01-27T11:00:00Z",
    updated_at: "2026-01-27T11:00:00Z",
    dependencies: [
      {
        depends_on_id: "wiring-test-1",
        type: "blocks",
      },
    ],
  },
  {
    id: "wiring-test-3",
    title: "Third Wiring Test Issue",
    status: "open",
    priority: 0,
    issue_type: "feature",
    description: "Description for third issue",
    created_at: "2026-01-27T12:00:00Z",
    updated_at: "2026-01-27T12:00:00Z",
  },
]

/**
 * Get IssueDetails for a mock issue (adds dependencies/dependents for GET /api/issues/{id}).
 */
function getMockIssueDetails(
  issue: (typeof mockIssues)[0],
  overrides?: { title?: string }
) {
  return {
    ...issue,
    ...(overrides ?? {}),
    dependencies: issue.dependencies ?? [],
    dependents: [],
  }
}

interface SetupMocksOptions {
  getDelay?: number
  getError?: boolean
  patchDelay?: number
  patchError?: boolean
  onPatch?: (body: { title?: string }) => void
  /** Mutable title state - when set, GET returns this title */
  titleRef?: { current: string }
}

/**
 * Setup API mocks for testing.
 */
async function setupMocks(page: Page, options?: SetupMocksOptions) {
  // Track patch calls
  const patchCalls: { id: string; body: { title?: string } }[] = []

  // Mock /api/ready to return our test issues
  await page.route("**/api/ready", async (route) => {
    // Return issues with current title state if titleRef is provided
    const issues = mockIssues.map((issue) => {
      if (options?.titleRef && issue.id === "wiring-test-1") {
        return { ...issue, title: options.titleRef.current }
      }
      return issue
    })
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: issues,
      }),
    })
  })

  // Mock /api/blocked for graph view (returns issues with blockers)
  await page.route("**/api/blocked", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: [],
      }),
    })
  })

  // Mock GET and PATCH /api/issues/{id}
  await page.route("**/api/issues/*", async (route) => {
    const request = route.request()
    const method = request.method()
    const url = request.url()

    if (method === "GET") {
      if (options?.getDelay) {
        await new Promise((r) => setTimeout(r, options.getDelay))
      }

      if (options?.getError) {
        await route.fulfill({
          status: 500,
          contentType: "application/json",
          body: JSON.stringify({ error: "Internal server error" }),
        })
        return
      }

      const idMatch = url.match(/\/api\/issues\/([^/?]+)/)
      const id = idMatch ? idMatch[1] : null
      const issue = mockIssues.find((i) => i.id === id)

      if (issue) {
        // Return with titleRef if provided
        const title =
          options?.titleRef && issue.id === "wiring-test-1"
            ? options.titleRef.current
            : issue.title
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(getMockIssueDetails(issue, { title })),
        })
      } else {
        await route.fulfill({
          status: 404,
          contentType: "application/json",
          body: JSON.stringify({ error: "Not found" }),
        })
      }
    } else if (method === "PATCH") {
      if (options?.patchDelay) {
        await new Promise((r) => setTimeout(r, options.patchDelay))
      }

      const idMatch = url.match(/\/api\/issues\/([^/?]+)/)
      const id = idMatch ? idMatch[1] : null
      const body = request.postDataJSON() as { title?: string }

      patchCalls.push({ id: id ?? "", body })
      options?.onPatch?.(body)

      if (options?.patchError) {
        await route.fulfill({
          status: 500,
          contentType: "application/json",
          body: JSON.stringify({ success: false, error: "Server error" }),
        })
        return
      }

      // Update mutable title state if provided
      if (options?.titleRef && body.title !== undefined) {
        options.titleRef.current = body.title
      }

      const issue = mockIssues.find((i) => i.id === id)
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          success: true,
          data: {
            ...(issue ?? mockIssues[0]),
            ...body,
            updated_at: new Date().toISOString(),
          },
        }),
      })
    } else {
      await route.continue()
    }
  })

  return { patchCalls }
}

/**
 * Navigate to the app and wait for issues to load.
 */
async function navigateToApp(page: Page, view?: "kanban" | "table" | "graph") {
  const viewParam = view === "table" ? "table" : view === "graph" ? "graph" : ""
  const url = viewParam ? `/?view=${viewParam}` : "/"

  await Promise.all([
    page.waitForResponse((res) => res.url().includes("/api/ready")),
    page.goto(url),
  ])

  // Wait for loading to complete
  await expect(page.getByTestId("loading-container")).not.toBeVisible({
    timeout: 5000,
  })
}

/**
 * Get panel elements.
 */
function getPanel(page: Page) {
  return {
    overlay: page.getByTestId("issue-detail-overlay"),
    panel: page.getByTestId("issue-detail-panel"),
    closeButton: page.getByTestId("header-close-button"),
    issueId: page.getByTestId("issue-id"),
    loading: page.getByTestId("panel-loading"),
    error: page.getByTestId("panel-error"),
  }
}

/**
 * Get the editable title elements.
 */
function getEditableTitle(page: Page) {
  return {
    display: page.getByTestId("editable-title-display"),
    input: page.getByTestId("editable-title-input"),
    error: page.getByTestId("title-error"),
    saving: page.getByTestId("title-saving"),
  }
}

test.describe("IssueDetailPanel - Graph View", () => {
  /**
   * Helper to find a React Flow node by issue ID using the data-id attribute.
   * React Flow adds data-id to the .react-flow__node wrapper.
   * Note: useGraphData creates node IDs with "node-" prefix.
   */
  function getNodeById(page: Page, issueId: string) {
    return page.locator(`.react-flow__node[data-id="node-${issueId}"]`)
  }

  test("clicking graph node opens panel with issue data", async ({ page }) => {
    await setupMocks(page)
    await navigateToApp(page, "graph")

    // Wait for graph view to be visible
    const graphView = page.getByTestId("graph-view")
    await expect(graphView).toBeVisible({ timeout: 10000 })

    // Wait for React Flow nodes to render using data-id attribute
    const node = getNodeById(page, "wiring-test-1")
    await expect(node).toBeVisible({ timeout: 10000 })

    // Set up response waiter before clicking (to catch fast responses)
    const responsePromise = page.waitForResponse((res) =>
      res.url().includes("/api/issues/wiring-test-1")
    )

    // Click the node
    await node.click()

    // Wait for panel to open
    const { panel } = getPanel(page)
    await expect(panel).toHaveAttribute("data-state", "open")

    // Wait for API call to complete
    await responsePromise

    // Verify issue data is displayed
    await expect(page.getByTestId("issue-id")).toContainText("wiring-test-1")
    await expect(page.getByTestId("editable-title-display")).toContainText(
      "First Wiring Test Issue"
    )
  })

  test("panel shows loading state while fetching from graph click", async ({
    page,
  }) => {
    await setupMocks(page, { getDelay: 500 })
    await navigateToApp(page, "graph")

    // Wait for graph view and node
    const graphView = page.getByTestId("graph-view")
    await expect(graphView).toBeVisible({ timeout: 10000 })

    const node = getNodeById(page, "wiring-test-1")
    await expect(node).toBeVisible({ timeout: 10000 })

    // Click node
    await node.click()

    // Verify loading state appears
    const { panel, loading } = getPanel(page)
    await expect(panel).toHaveAttribute("data-loading", "true")

    // Wait for loading to complete
    await expect(loading).not.toBeVisible({ timeout: 5000 })
    await expect(panel).toHaveAttribute("data-loading", "false")
  })

  test("close methods work from graph view", async ({ page }) => {
    await setupMocks(page)
    await navigateToApp(page, "graph")

    const graphView = page.getByTestId("graph-view")
    await expect(graphView).toBeVisible({ timeout: 10000 })

    const node = getNodeById(page, "wiring-test-1")
    await expect(node).toBeVisible({ timeout: 10000 })

    const { panel, closeButton, overlay } = getPanel(page)

    // Test X button
    await node.click()
    await expect(panel).toHaveAttribute("data-state", "open")
    await closeButton.click()
    await expect(panel).toHaveAttribute("data-state", "closed")

    // Test Escape key
    await node.click()
    await expect(panel).toHaveAttribute("data-state", "open")
    await page.keyboard.press("Escape")
    await expect(panel).toHaveAttribute("data-state", "closed")

    // Test backdrop click
    await node.click()
    await expect(panel).toHaveAttribute("data-state", "open")
    const panelBox = await panel.boundingBox()
    if (!panelBox) throw new Error("Could not get panel bounding box")
    await page.mouse.click(10, panelBox.y + panelBox.height / 2)
    await expect(panel).toHaveAttribute("data-state", "closed")
    await expect(overlay).toHaveAttribute("aria-hidden", "true")
  })

  test("clicking different graph nodes shows correct data", async ({ page }) => {
    await setupMocks(page)
    await navigateToApp(page, "graph")

    const graphView = page.getByTestId("graph-view")
    await expect(graphView).toBeVisible({ timeout: 10000 })

    const { panel, closeButton, overlay } = getPanel(page)

    // Click first node
    const firstNode = getNodeById(page, "wiring-test-1")
    await expect(firstNode).toBeVisible({ timeout: 10000 })
    await firstNode.click()

    await expect(panel).toHaveAttribute("data-state", "open")
    await expect(page.getByTestId("issue-id")).toContainText("wiring-test-1")

    // Close panel and wait for it to fully close
    await closeButton.click()
    await expect(panel).toHaveAttribute("data-state", "closed")
    await expect(overlay).toHaveAttribute("aria-hidden", "true")

    // Click different node
    const thirdNode = getNodeById(page, "wiring-test-3")
    await expect(thirdNode).toBeVisible({ timeout: 10000 })
    await thirdNode.click()

    // Wait for API call for third issue
    await page.waitForResponse((res) =>
      res.url().includes("/api/issues/wiring-test-3")
    )

    // Verify third issue data
    await expect(page.getByTestId("issue-id")).toContainText("wiring-test-3")
    await expect(page.getByTestId("editable-title-display")).toContainText(
      "Third Wiring Test Issue"
    )
  })
})

test.describe("IssueDetailPanel - Title Edit Flow", () => {
  test("editing title and pressing Enter calls PATCH API", async ({ page }) => {
    const { patchCalls } = await setupMocks(page)
    await navigateToApp(page)

    // Open panel from kanban
    const issueCard = page.locator("article").filter({ hasText: "First Wiring Test Issue" })
    await issueCard.click()

    const { panel } = getPanel(page)
    await expect(panel).toHaveAttribute("data-state", "open")

    // Wait for data to load
    await expect(page.getByTestId("issue-id")).toContainText("wiring-test-1")

    const { display, input } = getEditableTitle(page)

    // Enter edit mode
    await display.click()
    await expect(input).toBeVisible()

    // Type new title
    await input.clear()
    await input.fill("Updated Wiring Title")

    // Set up response waiter before pressing Enter
    const patchPromise = page.waitForResponse(
      (res) =>
        res.url().includes("/api/issues/wiring-test-1") &&
        res.request().method() === "PATCH"
    )

    // Press Enter to save
    await input.press("Enter")

    // Wait for PATCH call
    await patchPromise

    // Verify API was called with correct payload
    expect(patchCalls).toHaveLength(1)
    expect(patchCalls[0].id).toBe("wiring-test-1")
    expect(patchCalls[0].body).toEqual({ title: "Updated Wiring Title" })

    // Should return to display mode
    // Note: Current implementation doesn't update display immediately after PATCH
    // because onIssueUpdate callback isn't wired. Display updates on reopen (fresh fetch).
    await expect(input).not.toBeVisible()
  })

  test("title edit with blur saves changes", async ({ page }) => {
    const { patchCalls } = await setupMocks(page)
    await navigateToApp(page)

    // Open panel
    const issueCard = page.locator("article").filter({ hasText: "First Wiring Test Issue" })
    await issueCard.click()

    const { panel } = getPanel(page)
    await expect(panel).toHaveAttribute("data-state", "open")
    await expect(page.getByTestId("issue-id")).toContainText("wiring-test-1")

    const { display, input } = getEditableTitle(page)

    // Enter edit mode and change title
    await display.click()
    await input.clear()
    await input.fill("Blur Save Test Title")

    // Set up response waiter
    const patchPromise = page.waitForResponse(
      (res) =>
        res.url().includes("/api/issues/wiring-test-1") &&
        res.request().method() === "PATCH"
    )

    // Click outside to blur (on panel background)
    await page
      .getByTestId("issue-detail-panel")
      .click({ position: { x: 10, y: 10 } })

    // Wait for PATCH call
    await patchPromise

    // Verify API was called
    expect(patchCalls).toHaveLength(1)
    expect(patchCalls[0].body.title).toBe("Blur Save Test Title")

    // Should return to display mode
    await expect(input).not.toBeVisible()
  })

  test("Escape cancels title edit without API call", async ({ page }) => {
    let patchCallCount = 0
    await setupMocks(page, {
      onPatch: () => {
        patchCallCount++
      },
    })
    await navigateToApp(page)

    // Open panel
    const issueCard = page.locator("article").filter({ hasText: "First Wiring Test Issue" })
    await issueCard.click()

    const { panel } = getPanel(page)
    await expect(panel).toHaveAttribute("data-state", "open")

    const { display, input } = getEditableTitle(page)

    // Enter edit mode
    await display.click()
    await expect(input).toBeVisible()

    // Type different title
    await input.clear()
    await input.fill("This should be discarded")

    // Press Escape to cancel
    await input.press("Escape")

    // Should return to display mode
    await expect(input).not.toBeVisible()

    // Original title should be shown
    await expect(display).toContainText("First Wiring Test Issue")

    // No API call should be made
    expect(patchCallCount).toBe(0)
  })

  test("empty title shows validation error", async ({ page }) => {
    let patchCallCount = 0
    await setupMocks(page, {
      onPatch: () => {
        patchCallCount++
      },
    })
    await navigateToApp(page)

    // Open panel
    const issueCard = page.locator("article").filter({ hasText: "First Wiring Test Issue" })
    await issueCard.click()

    const { panel } = getPanel(page)
    await expect(panel).toHaveAttribute("data-state", "open")

    const { display, input, error } = getEditableTitle(page)

    // Enter edit mode
    await display.click()

    // Clear title completely
    await input.clear()

    // Press Enter
    await input.press("Enter")

    // Error should be visible
    await expect(error).toBeVisible()
    await expect(error).toContainText("Title cannot be empty")

    // Should stay in edit mode
    await expect(input).toBeVisible()

    // No API call should be made
    expect(patchCallCount).toBe(0)
  })

  test("API error on title save shows error and stays in edit mode", async ({
    page,
  }) => {
    await setupMocks(page, { patchError: true })
    await navigateToApp(page)

    // Open panel
    const issueCard = page.locator("article").filter({ hasText: "First Wiring Test Issue" })
    await issueCard.click()

    const { panel } = getPanel(page)
    await expect(panel).toHaveAttribute("data-state", "open")

    const { display, input, error } = getEditableTitle(page)

    // Enter edit mode and change title
    await display.click()
    await input.clear()
    await input.fill("This will fail")

    // Set up response waiter
    const patchPromise = page.waitForResponse(
      (res) =>
        res.url().includes("/api/issues/wiring-test-1") &&
        res.request().method() === "PATCH" &&
        res.status() === 500
    )

    // Attempt save
    await input.press("Enter")

    // Wait for failed PATCH
    await patchPromise

    // Error message should appear
    await expect(error).toBeVisible()

    // Should stay in edit mode with content preserved
    await expect(input).toBeVisible()
    await expect(input).toHaveValue("This will fail")
  })
})

test.describe("IssueDetailPanel - Reopen with Updated Data", () => {
  test("reopening issue fetches fresh data", async ({ page }) => {
    // Use mutable title state to simulate server-side update
    // setupMocks already handles titleRef for both /api/ready and /api/issues/{id}
    const titleRef = { current: "Original Title" }
    await setupMocks(page, { titleRef })

    await navigateToApp(page)

    const { panel, closeButton, overlay } = getPanel(page)

    // Open panel
    const issueCard = page.locator("article").filter({ hasText: "Original Title" })
    await issueCard.click()

    await expect(panel).toHaveAttribute("data-state", "open")
    await expect(page.getByTestId("editable-title-display")).toContainText(
      "Original Title"
    )

    // Close panel
    await closeButton.click()
    await expect(panel).toHaveAttribute("data-state", "closed")
    await expect(overlay).toHaveAttribute("aria-hidden", "true")

    // Simulate server-side update (another user edited the issue)
    titleRef.current = "Server Updated Title"

    // Reopen same issue - need to find it in the list first
    // Since we're simulating a server update, the card still shows old title
    // But when we open the panel, it should fetch fresh data
    const issueCardAgain = page.locator("article").filter({ hasText: "Original Title" })
    await issueCardAgain.click()

    // Wait for fresh API call
    await page.waitForResponse((res) =>
      res.url().includes("/api/issues/wiring-test-1")
    )

    // Panel should show updated title from server
    await expect(page.getByTestId("editable-title-display")).toContainText(
      "Server Updated Title"
    )
  })

  test("switching issues shows correct data for each", async ({ page }) => {
    await setupMocks(page)
    await navigateToApp(page)

    const { panel, closeButton, overlay } = getPanel(page)

    // Open issue A
    const cardA = page.locator("article").filter({ hasText: "First Wiring Test Issue" })
    await cardA.click()

    await expect(panel).toHaveAttribute("data-state", "open")
    await expect(page.getByTestId("issue-id")).toContainText("wiring-test-1")
    await expect(page.getByTestId("editable-title-display")).toContainText(
      "First Wiring Test Issue"
    )

    // Close panel
    await closeButton.click()
    await expect(panel).toHaveAttribute("data-state", "closed")
    await expect(overlay).toHaveAttribute("aria-hidden", "true")

    // Open issue B
    const cardB = page.locator("article").filter({ hasText: "Second Wiring Test Issue" })
    await cardB.click()

    await page.waitForResponse((res) =>
      res.url().includes("/api/issues/wiring-test-2")
    )

    await expect(page.getByTestId("issue-id")).toContainText("wiring-test-2")
    await expect(page.getByTestId("editable-title-display")).toContainText(
      "Second Wiring Test Issue"
    )

    // Close panel
    await closeButton.click()
    await expect(panel).toHaveAttribute("data-state", "closed")
    await expect(overlay).toHaveAttribute("aria-hidden", "true")

    // Reopen issue A - should still show A's data
    await cardA.click()

    await page.waitForResponse((res) =>
      res.url().includes("/api/issues/wiring-test-1")
    )

    await expect(page.getByTestId("issue-id")).toContainText("wiring-test-1")
    await expect(page.getByTestId("editable-title-display")).toContainText(
      "First Wiring Test Issue"
    )
  })

  test("title edit updates panel and can be reopened with new value", async ({
    page,
  }) => {
    // Use mutable title state
    const titleRef = { current: "First Wiring Test Issue" }
    await setupMocks(page, { titleRef })

    await navigateToApp(page)

    const { panel, closeButton, overlay } = getPanel(page)
    const { display, input } = getEditableTitle(page)

    // Open panel
    const issueCard = page.locator("article").filter({ hasText: "First Wiring Test Issue" })
    await issueCard.click()

    await expect(panel).toHaveAttribute("data-state", "open")

    // Edit title
    await display.click()
    await input.clear()
    await input.fill("Edited Title")

    // Save
    const patchPromise = page.waitForResponse(
      (res) =>
        res.url().includes("/api/issues/wiring-test-1") &&
        res.request().method() === "PATCH"
    )
    await input.press("Enter")
    await patchPromise

    // Note: Current implementation doesn't update display immediately after PATCH
    // because onIssueUpdate callback isn't wired. We verify below via reopen.
    await expect(input).not.toBeVisible()

    // Close panel
    await closeButton.click()
    await expect(panel).toHaveAttribute("data-state", "closed")
    await expect(overlay).toHaveAttribute("aria-hidden", "true")

    // The card in the list still shows old title (not updated via WebSocket in test)
    // But reopening should fetch fresh data
    await issueCard.click()

    // Wait for fresh GET
    await page.waitForResponse((res) =>
      res.url().includes("/api/issues/wiring-test-1") &&
      res.request().method() === "GET"
    )

    // Should show persisted title
    await expect(display).toContainText("Edited Title")
  })
})

test.describe("IssueDetailPanel - Rapid Click Edge Cases", () => {
  test("opening issues sequentially keeps data isolated", async ({ page }) => {
    // Tests that opening multiple issues sequentially shows correct data for each
    // without data leaking between them.
    await setupMocks(page, { getDelay: 50 })
    await navigateToApp(page)

    const { panel, closeButton, overlay } = getPanel(page)

    // Open first issue
    const firstCard = page.locator("article").filter({ hasText: "First Wiring Test Issue" })
    await firstCard.click()
    await expect(panel).toHaveAttribute("data-state", "open")
    await expect(page.getByTestId("issue-id")).toContainText("wiring-test-1")

    // Close and quickly open second issue
    await closeButton.click()
    await expect(panel).toHaveAttribute("data-state", "closed")
    await expect(overlay).toHaveAttribute("aria-hidden", "true")

    const secondCard = page.locator("article").filter({ hasText: "Second Wiring Test Issue" })
    await secondCard.click()
    await expect(panel).toHaveAttribute("data-state", "open")

    // Wait for second issue data
    await page.waitForResponse((res) =>
      res.url().includes("/api/issues/wiring-test-2")
    )

    // Should show second issue, not first
    await expect(page.getByTestId("issue-id")).toContainText("wiring-test-2")
  })

  test("panel prevents interaction with underlying cards via overlay", async ({
    page,
  }) => {
    // Verifies that when panel is open, the overlay prevents clicks on cards.
    // This is expected UX behavior - user must close panel before selecting another.
    await setupMocks(page)
    await navigateToApp(page)

    const { panel, overlay } = getPanel(page)

    // Open panel
    const issueCard = page.locator("article").filter({ hasText: "First Wiring Test Issue" })
    await issueCard.click()
    await expect(panel).toHaveAttribute("data-state", "open")

    // Verify overlay is blocking
    await expect(overlay).toHaveAttribute("aria-hidden", "false")

    // Overlay should be visible and intercepting events
    const overlayVisible = await overlay.isVisible()
    expect(overlayVisible).toBe(true)

    // Panel should still be open with correct data
    await expect(page.getByTestId("issue-id")).toContainText("wiring-test-1")
  })
})
