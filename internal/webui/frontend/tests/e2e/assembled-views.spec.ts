import { test, expect, Page } from "@playwright/test"

/**
 * Integration tests for assembled views.
 *
 * Tests cross-cutting interactions between the major subsystems wired together
 * in App.tsx: API data loading, filters, view switching, drag-drop, and detail panel.
 * Each test exercises at least 2 subsystems interacting.
 *
 * Existing tests cover each subsystem in isolation:
 * - kanban.spec.ts: drag-drop
 * - server-push.spec.ts: SSE lifecycle
 * - view-switcher.spec.ts: view navigation
 * - filter.spec.ts: individual filters
 *
 * These tests verify subsystems work correctly *together*.
 *
 * Note: SSE mutation → UI update tests require the live daemon and are covered
 * in server-push-integration.spec.ts. Playwright's route.fulfill() for SSE
 * returns a complete HTTP response that closes immediately, making mocked
 * SSE mutation delivery unreliable for UI verification.
 */

// SSE endpoint pattern
const SSE_ENDPOINT = "**/workspaces/*/events**"

/**
 * Shared mock issues with varied statuses, priorities, types, and assignees.
 * Rich enough to exercise all filter dimensions.
 */
const mockIssues = [
  {
    id: "asm-open-1",
    title: "Open Task Alpha",
    status: "open",
    priority: 1,
    issue_type: "task",
    assignee: "alice",
    description: "First open task",
    created_at: "2026-01-24T10:00:00Z",
    updated_at: "2026-01-24T10:00:00Z",
  },
  {
    id: "asm-open-2",
    title: "Open Bug Beta",
    status: "open",
    priority: 2,
    issue_type: "bug",
    description: "A bug to fix",
    created_at: "2026-01-24T11:00:00Z",
    updated_at: "2026-01-24T11:00:00Z",
  },
  {
    id: "asm-ip-1",
    title: "In Progress Feature",
    status: "in_progress",
    priority: 1,
    issue_type: "feature",
    assignee: "bob",
    description: "Active feature work",
    created_at: "2026-01-24T12:00:00Z",
    updated_at: "2026-01-24T12:00:00Z",
  },
  {
    id: "asm-review-1",
    title: "Review Task Gamma",
    status: "review",
    priority: 2,
    issue_type: "task",
    description: "Awaiting code review",
    created_at: "2026-01-24T13:00:00Z",
    updated_at: "2026-01-24T13:00:00Z",
  },
  {
    id: "asm-closed-1",
    title: "Closed Chore Delta",
    status: "closed",
    priority: 3,
    issue_type: "chore",
    description: "Already done",
    created_at: "2026-01-24T14:00:00Z",
    updated_at: "2026-01-24T14:00:00Z",
  },
  {
    id: "asm-open-3",
    title: "Open Feature Epsilon",
    status: "open",
    priority: 0,
    issue_type: "feature",
    description: "Critical new feature",
    created_at: "2026-01-24T15:00:00Z",
    updated_at: "2026-01-24T15:00:00Z",
  },
]

/**
 * Set up all API mocks needed for assembled view tests.
 * Route registration order matters: Playwright matches LIFO (last registered wins).
 */
async function setupMocks(page: Page, issues = mockIssues) {
  // Boot-time mocks: /api/config and /api/auth/token must succeed for app to render
  await page.route("**/api/config", async (route) => {
    const url = new URL(route.request().url())
    if (url.pathname !== "/api/config") {
      await route.fallback()
      return
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ mode: "open" }),
    })
  })

  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-token" }),
    })
  })

  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", daemon: true }),
    })
  })

  // Monitor endpoints
  await page.route("**/monitor/**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({}),
    })
  })

  const wsData = {
    id: "default",
    name: "default",
    path: "/tmp/ws",
    repos: [],
    groups: [],
    agents: [],
    workspaces: [
      {
        id: "default",
        name: "default",
        path: "/tmp/ws",
        active: true,
        repo_count: 0,
        is_default: true,
      },
    ],
    workspace_order: ["default"],
    default_workspace: "default",
  }

  // Workspace-scoped routes — single handler with URL dispatch (proven pattern from agents-sidebar)
  await page.route(
    (url) => url.toString().includes("/api/workspaces/") && !url.toString().includes("/events"),
    async (route) => {
      const url = route.request().url()
      const method = route.request().method()

      // Workspace resolution: /api/workspaces/active
      if (url.includes("/api/workspaces/active")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: wsData }),
        })
        return
      }

      // Ready (issues)
      if (url.includes("/ready")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: issues }),
        })
        return
      }

      // Stats
      if (url.includes("/stats")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            success: true,
            data: {
              total_issues: issues.length,
              open_issues: issues.filter((i) => i.status === "open").length,
              in_progress_issues: issues.filter((i) => i.status === "in_progress").length,
              closed_issues: issues.filter((i) => i.status === "closed").length,
              blocked_issues: 0,
              deferred_issues: 0,
              ready_issues: issues.filter((i) => i.status === "open").length,
              tombstone_issues: 0,
              pinned_issues: 0,
              epics_eligible_for_closure: 0,
              average_lead_time_hours: 0,
            },
          }),
        })
        return
      }

      // Blocked
      if (url.includes("/blocked")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: [] }),
        })
        return
      }

      // Terminal sessions
      if (url.includes("/terminal/sessions")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: {} }),
        })
        return
      }

      // Issues graph
      if (url.includes("/issues/graph")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, issues: [] }),
        })
        return
      }

      // Issues by ID (GET/PATCH)
      if (url.includes("/issues/")) {
        const idMatch = url.match(/\/issues\/([^/?]+)/)
        const id = idMatch ? idMatch[1] : null

        if (method === "GET") {
          const issue = issues.find((i) => i.id === id)
          if (issue) {
            await route.fulfill({
              status: 200,
              contentType: "application/json",
              body: JSON.stringify({
                success: true,
                data: { ...issue, dependencies: [], dependents: [], labels: [], comments: [] },
              }),
            })
          } else {
            await route.fulfill({
              status: 404,
              contentType: "application/json",
              body: JSON.stringify({ success: false, error: "Not found" }),
            })
          }
          return
        }

        if (method === "PATCH") {
          const body = route.request().postDataJSON() as { status?: string }
          const issue = issues.find((i) => i.id === id)
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
          return
        }
      }

      // Issues list
      if (url.includes("/issues")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: issues }),
        })
        return
      }

      // Default workspace data
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: wsData }),
      })
    },
  )
}

/**
 * Navigate and wait for API data to load.
 */
async function navigateAndWait(page: Page, path = "/") {
  await page.goto(path)
  // Wait for the kanban board to render with issue data
  await page.locator("h1").waitFor({ timeout: 15000 })
  // Wait for issues to load in the board
  await page.getByText("Open Task Alpha").waitFor({ timeout: 15000 })
}

test.describe("API data flows through to all views", () => {
  test.beforeEach(async ({ page }) => {
    // Abort SSE to keep tests deterministic (but let events/token through)
    await page.route(SSE_ENDPOINT, async (route) => {
      if (route.request().url().includes("/events/token")) {
        await route.fulfill({ status: 404, body: "Not Found" })
        return
      }
      await route.abort()
    })
    await setupMocks(page)
  })

  test("KanbanBoard displays API data then table shows same issues after switch", async ({
    page,
  }) => {
    await navigateAndWait(page, "/?groupBy=none")

    // Verify Kanban columns
    const readyColumn = page.locator('section[aria-label="Open issues"]')
    const inProgressColumn = page.locator('section[aria-label="In Progress issues"]')
    const reviewColumn = page.locator('section[aria-label="Review issues"]')
    const doneColumn = page.locator('section[aria-label="Done issues"]')

    await expect(readyColumn).toBeVisible()
    await expect(inProgressColumn).toBeVisible()
    await expect(reviewColumn).toBeVisible()
    await expect(doneColumn).toBeVisible()

    // Verify issues in correct columns
    await expect(readyColumn.getByRole("list").locator("> [role='button']")).toHaveCount(3)
    await expect(readyColumn.getByText("Open Task Alpha")).toBeVisible()
    await expect(readyColumn.getByText("Open Bug Beta")).toBeVisible()
    await expect(readyColumn.getByText("Open Feature Epsilon")).toBeVisible()

    await expect(inProgressColumn.getByRole("list").locator("> [role='button']")).toHaveCount(1)
    await expect(inProgressColumn.getByText("In Progress Feature")).toBeVisible()

    await expect(reviewColumn.getByRole("list").locator("> [role='button']")).toHaveCount(1)
    await expect(reviewColumn.getByText("Review Task Gamma")).toBeVisible()

    await expect(doneColumn.getByRole("list").locator("> [role='button']")).toHaveCount(1)
    await expect(doneColumn.getByText("Closed Chore Delta")).toBeVisible()

    // Switch to Table view
    const tableTab = page.getByRole("tab", { name: "List" })
    await tableTab.click()

    const issueTable = page.getByTestId("issue-table")
    await expect(issueTable).toBeVisible()

    // Verify same issues appear in the table
    await expect(issueTable.getByText("Open Task Alpha")).toBeVisible()
    await expect(issueTable.getByText("Open Bug Beta")).toBeVisible()
    await expect(issueTable.getByText("In Progress Feature")).toBeVisible()
    await expect(issueTable.getByText("Review Task Gamma")).toBeVisible()
    await expect(issueTable.getByText("Closed Chore Delta")).toBeVisible()
    await expect(issueTable.getByText("Open Feature Epsilon")).toBeVisible()
  })

  test("filtered data is consistent across kanban and table views", async ({
    page,
  }) => {
    await navigateAndWait(page, "/?groupBy=none")

    // Apply priority=1 filter
    const priorityFilter = page.getByTestId("priority-filter")
    await priorityFilter.selectOption("1")

    // Verify Kanban shows only P1 issues
    const readyColumn = page.locator('section[aria-label="Open issues"]')
    const inProgressColumn = page.locator('section[aria-label="In Progress issues"]')

    await expect(readyColumn.getByText("Open Task Alpha")).toBeVisible()
    await expect(readyColumn.getByRole("list").locator("> [role='button']")).toHaveCount(1)
    await expect(inProgressColumn.getByText("In Progress Feature")).toBeVisible()
    await expect(inProgressColumn.getByRole("list").locator("> [role='button']")).toHaveCount(1)

    // P2/P3/P0 issues should not be visible
    await expect(readyColumn.getByText("Open Bug Beta")).not.toBeVisible()
    await expect(readyColumn.getByText("Open Feature Epsilon")).not.toBeVisible()

    // Switch to Table view
    const tableTab = page.getByRole("tab", { name: "List" })
    await tableTab.click()

    const issueTable = page.getByTestId("issue-table")
    await expect(issueTable).toBeVisible()

    // Same filtered set should appear in table
    await expect(issueTable.getByText("Open Task Alpha")).toBeVisible()
    await expect(issueTable.getByText("In Progress Feature")).toBeVisible()

    // Filtered-out issues should not be in table
    await expect(issueTable.getByText("Open Bug Beta")).not.toBeVisible()
    await expect(issueTable.getByText("Review Task Gamma")).not.toBeVisible()
    await expect(issueTable.getByText("Closed Chore Delta")).not.toBeVisible()
  })
})

test.describe("View switching preserves state", () => {
  test.beforeEach(async ({ page }) => {
    await page.route(SSE_ENDPOINT, async (route) => {
      await route.abort()
    })
    await setupMocks(page)
  })

  test("filter state persists across view switches", async ({ page }) => {
    await navigateAndWait(page, "/?groupBy=none")

    // Apply priority filter and search in kanban view
    const priorityFilter = page.getByTestId("priority-filter")
    await priorityFilter.selectOption("1")

    const searchInput = page.getByTestId("search-input-field")
    await searchInput.fill("Alpha")
    await page.waitForTimeout(350) // debounce

    // Verify filter applied in kanban
    const readyColumn = page.locator('section[aria-label="Open issues"]')
    await expect(readyColumn.getByText("Open Task Alpha")).toBeVisible()
    await expect(readyColumn.getByRole("list").locator("> [role='button']")).toHaveCount(1)

    // Switch to Table view
    const tableTab = page.getByRole("tab", { name: "List" })
    await tableTab.click()

    const issueTable = page.getByTestId("issue-table")
    await expect(issueTable).toBeVisible()

    // Verify filter controls still show active filters
    await expect(priorityFilter).toHaveValue("1")
    await expect(searchInput).toHaveValue("Alpha")

    // Verify table is filtered
    await expect(issueTable.getByText("Open Task Alpha")).toBeVisible()
    await expect(issueTable.getByText("Open Bug Beta")).not.toBeVisible()
    await expect(issueTable.getByText("In Progress Feature")).not.toBeVisible()

    // Switch back to Kanban
    const kanbanTab = page.getByRole("tab", { name: "Kanban" })
    await kanbanTab.click()

    // Verify filter still applied
    await expect(priorityFilter).toHaveValue("1")
    await expect(searchInput).toHaveValue("Alpha")
    await expect(readyColumn.getByText("Open Task Alpha")).toBeVisible()
    await expect(readyColumn.getByRole("list").locator("> [role='button']")).toHaveCount(1)
  })

  test("drag-drop status change in kanban persists in table view", async ({
    page,
  }) => {
    await navigateAndWait(page, "/?groupBy=none")

    const readyColumn = page.locator('section[aria-label="Open issues"]')
    const reviewColumn = page.locator('section[aria-label="Review issues"]')

    // Verify initial state
    await expect(readyColumn.getByText("Open Bug Beta")).toBeVisible()
    await expect(reviewColumn.getByRole("list").locator("> [role='button']")).toHaveCount(1)

    // Drag Open Bug Beta from Ready to Review (avoids AssigneePrompt)
    const draggable = page
      .getByRole("button", { name: "Issue: Open Bug Beta" })
      .first()
    await expect(draggable).toBeVisible()

    const dropTarget = reviewColumn.locator('[data-droppable-id="review"]')
    await expect(dropTarget).toBeVisible()

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

    // Wait for the PATCH to complete
    await page.waitForResponse(
      (res) =>
        res.url().includes("/issues/asm-open-2") &&
        res.request().method() === "PATCH"
    )

    // Verify card moved to Review in kanban
    await expect(reviewColumn.getByText("Open Bug Beta")).toBeVisible()
    await expect(reviewColumn.getByRole("list").locator("> [role='button']")).toHaveCount(2)

    // Switch to Table view
    const tableTab = page.getByRole("tab", { name: "List" })
    await tableTab.click()

    const issueTable = page.getByTestId("issue-table")
    await expect(issueTable).toBeVisible()

    // The issue should still be present in the table (data is shared)
    await expect(issueTable.getByText("Open Bug Beta")).toBeVisible()
  })

  test("issue detail panel works from both views", async ({ page }) => {
    await navigateAndWait(page, "/?groupBy=none")

    const panel = page.getByTestId("issue-detail-panel")
    const closeButton = page.getByTestId("header-close-button")
    const overlay = page.getByTestId("issue-detail-overlay")

    // Set up response waiter before clicking
    const responsePromise = page.waitForResponse(
      (res) =>
        res.url().includes("/issues/asm-open-1") &&
        res.request().method() === "GET"
    )

    // Click an issue in kanban view to open detail panel
    const kanbanCard = page.getByRole("button", { name: "Issue: Open Task Alpha" }).first()
    await kanbanCard.click()

    // Wait for API call to complete
    await responsePromise

    // Verify panel opens with correct data
    await expect(panel).toHaveAttribute("data-state", "open")
    await expect(page.getByTestId("issue-id")).toContainText("asm-open-1")

    // Close the panel
    await closeButton.click()
    await expect(panel).toHaveAttribute("data-state", "closed")
    await expect(overlay).toHaveAttribute("aria-hidden", "true")

    // Switch to Table view
    const tableTab = page.getByRole("tab", { name: "List" })
    await tableTab.click()

    const issueTable = page.getByTestId("issue-table")
    await expect(issueTable).toBeVisible()

    // Set up response waiter before clicking table row
    const tableResponsePromise = page.waitForResponse(
      (res) =>
        res.url().includes("/issues/asm-ip-1") &&
        res.request().method() === "GET"
    )

    // Click an issue in table view to open detail panel
    const tableRow = page.getByTestId("issue-row-asm-ip-1")
    await tableRow.click()

    // Wait for API call
    await tableResponsePromise

    // Verify panel opens with correct data from table click
    await expect(panel).toHaveAttribute("data-state", "open")
    await expect(page.getByTestId("issue-id")).toContainText("asm-ip-1")
    await expect(page.getByTestId("editable-title-display")).toContainText(
      "In Progress Feature"
    )
  })
})
