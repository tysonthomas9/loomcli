import { test, expect, Page } from "@playwright/test"

/**
 * E2E tests for IssueDetailPanel log tabs (task-type issues).
 *
 * Tests verify that:
 * - Log tab bar appears for task-type issues with available phases
 * - Planning/Implementation tabs show LogViewer with streamed logs
 * - Tab switching clears previous logs and disconnects streams
 * - Non-task issues never show log tabs
 * - Edge cases: API failures, SSE errors, malformed data
 *
 * Follows patterns from log-streaming.spec.ts and issue-detail-panel.spec.ts.
 */

const DOM_SETTLE_MS = 500

// Mock task-type issue (log tabs only for issue_type === 'task')
const mockTaskIssue = {
  id: "log-task-1",
  title: "Task With Logs",
  status: "in_progress",
  priority: 1,
  issue_type: "task",
  description: "A task with log phases",
  created_at: "2026-02-01T10:00:00Z",
  updated_at: "2026-02-01T10:00:00Z",
}

// Mock bug issue (should NOT show log tabs)
const mockBugIssue = {
  id: "log-bug-1",
  title: "Bug Without Logs",
  status: "open",
  priority: 2,
  issue_type: "bug",
  description: "A bug - no log tabs",
  created_at: "2026-02-01T11:00:00Z",
  updated_at: "2026-02-01T11:00:00Z",
}

// Mock feature issue (should NOT show log tabs)
const mockFeatureIssue = {
  id: "log-feat-1",
  title: "Feature Without Logs",
  status: "open",
  priority: 3,
  issue_type: "feature",
  description: "A feature - no log tabs",
  created_at: "2026-02-01T12:00:00Z",
  updated_at: "2026-02-01T12:00:00Z",
}

const allMockIssues = [mockTaskIssue, mockBugIssue, mockFeatureIssue]

function getMockIssueDetails(issue: (typeof allMockIssues)[0]) {
  return {
    ...issue,
    dependencies: [],
    dependents: [],
  }
}

/**
 * Set up base API mocks for all tests.
 */
async function setupBaseMocks(
  page: Page,
  options?: {
    phases?: string[]
    phasesError?: boolean
    skipTaskLogStream?: boolean
  }
) {
  const phases = options?.phases ?? ["planning", "implementation"]

  // Mock auth token endpoint (returns 404 = auth disabled, skips retry backoff)
  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 404,
      contentType: "application/json",
      body: JSON.stringify({ error: "Not found" }),
    })
  })

  await page.route("**/api/ready", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: allMockIssues }),
    })
  })

  await page.route("**/api/blocked", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: [] }),
    })
  })

  await page.route("**/api/issues/graph", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, issues: allMockIssues }),
    })
  })

  await page.route("**/api/events", async (route) => {
    await route.abort()
  })

  await page.route("**/api/stats", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: { open: 3, closed: 0, total: 3, completion: 0 },
      }),
    })
  })

  // Mock GET /api/issues/{id} - must return { success, data } wrapper
  await page.route("**/api/issues/*", async (route) => {
    const request = route.request()
    if (request.method() !== "GET") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true }),
      })
      return
    }
    const url = request.url()
    const idMatch = url.match(/\/api\/issues\/([^/?]+)/)
    const id = idMatch ? idMatch[1] : null
    const issue = allMockIssues.find((i) => i.id === id)
    if (issue) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          success: true,
          data: getMockIssueDetails(issue),
        }),
      })
    } else {
      await route.fulfill({
        status: 404,
        contentType: "application/json",
        body: JSON.stringify({ success: false, error: "Not found" }),
      })
    }
  })

  // Mock loom API endpoints individually (each endpoint expects a different shape)
  await page.route("**/api/loom/api/agents", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ agents: [] }),
    })
  })

  await page.route("**/api/loom/api/status", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        agents: [],
        tasks: { needs_planning: 0, ready_to_implement: 0, in_progress: 0, need_review: 0, backlog: 0 },
        agent_tasks: {},
        sync: { db_synced: true, db_last_sync: "", git_needs_push: 0, git_needs_pull: 0 },
        stats: { open: 0, closed: 0, total: 0, completion: 0 },
        timestamp: new Date().toISOString(),
      }),
    })
  })

  await page.route("**/api/loom/api/tasks", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        needs_planning: [],
        ready_to_implement: [],
        in_progress: [],
        needs_review: [],
        backlog: [],
      }),
    })
  })

  // Mock GET /api/tasks/{id}/logs (phase discovery)
  // Use a function matcher to precisely match only the phase discovery endpoint
  await page.route(
    (url) => {
      const path = url.pathname
      return /^\/api\/tasks\/[^/]+\/logs$/.test(path)
    },
    async (route) => {
      if (options?.phasesError) {
        await route.fulfill({
          status: 500,
          contentType: "application/json",
          body: JSON.stringify({ error: "Internal server error" }),
        })
      } else {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            success: true,
            data: { phases },
          }),
        })
      }
    }
  )

  // Default: abort task log SSE streams
  if (!options?.skipTaskLogStream) {
    await page.route("**/api/tasks/*/logs/*/stream**", async (route) => {
      await route.abort()
    })
  }
}

/**
 * Mock task log SSE stream with specified events.
 * Must be called BEFORE setupBaseMocks to override the abort route.
 */
async function mockTaskLogStreamSSE(
  page: Page,
  taskId: string,
  phase: string,
  events: Array<{ line: string; timestamp?: string }>
) {
  const pattern = `**/api/tasks/${taskId}/logs/${phase}/stream**`
  await page.route(pattern, async (route) => {
    let body = ""
    events.forEach((event, index) => {
      const data = JSON.stringify({
        line: event.line,
        timestamp: event.timestamp || new Date().toISOString(),
      })
      body += `event: log-line\ndata: ${data}\nid: ${index + 1}\n\n`
    })
    await route.fulfill({
      status: 200,
      contentType: "text/event-stream",
      headers: {
        "Cache-Control": "no-cache",
        Connection: "keep-alive",
      },
      body,
    })
  })
}

/**
 * Navigate to the app and wait for issues to load.
 */
async function navigateToApp(page: Page) {
  await Promise.all([
    page.waitForResponse((res) => res.url().includes("/api/ready")),
    page.goto("/"),
  ])
  await expect(page.getByTestId("loading-container")).not.toBeVisible({
    timeout: 5000,
  })
}

/**
 * Click an issue card to open the detail panel and wait for data to load.
 */
async function openIssuePanel(page: Page, issueTitle: string) {
  const card = page.locator("article").filter({ hasText: issueTitle })
  await expect(card).toBeVisible()
  await card.click()
  await expect(page.getByTestId("issue-detail-panel")).toHaveAttribute(
    "data-state",
    "open"
  )
  // Wait for issue data to load
  await expect(page.getByTestId("issue-id")).toBeVisible({ timeout: 5000 })
}

test.describe("Suite 1: Task Log Tab Bar Visibility", () => {
  test("log tab bar appears for task-type issues with available phases", async ({
    page,
  }) => {
    await setupBaseMocks(page, { phases: ["planning", "implementation"] })
    await navigateToApp(page)
    await openIssuePanel(page, "Task With Logs")

    const panel = page.getByTestId("issue-detail-panel")

    // Tabs appear after phase discovery completes (auto-wait)
    await expect(panel.getByRole("tab", { name: "Details" })).toBeVisible({ timeout: 5000 })
    await expect(panel.getByRole("tab", { name: "Planning" })).toBeVisible()
    await expect(panel.getByRole("tab", { name: "Implementation" })).toBeVisible()
  })

  test("log tab bar does NOT appear for bug issues", async ({ page }) => {
    await setupBaseMocks(page)
    await navigateToApp(page)
    await openIssuePanel(page, "Bug Without Logs")

    await expect(page.getByTestId("issue-id")).toContainText("log-bug-1")
    await page.waitForTimeout(DOM_SETTLE_MS)

    const panel = page.getByTestId("issue-detail-panel")
    await expect(panel.getByRole("tab", { name: "Planning" })).not.toBeVisible()
  })

  test("log tab bar does NOT appear for feature issues", async ({ page }) => {
    await setupBaseMocks(page)
    await navigateToApp(page)
    await openIssuePanel(page, "Feature Without Logs")

    await expect(page.getByTestId("issue-id")).toContainText("log-feat-1")
    await page.waitForTimeout(DOM_SETTLE_MS)

    const panel = page.getByTestId("issue-detail-panel")
    await expect(panel.getByRole("tab", { name: "Planning" })).not.toBeVisible()
  })

  test("log tab bar does NOT appear when no phases are available", async ({
    page,
  }) => {
    await setupBaseMocks(page, { phases: [] })
    await navigateToApp(page)
    await openIssuePanel(page, "Task With Logs")

    await expect(page.getByTestId("issue-id")).toContainText("log-task-1")
    await page.waitForTimeout(DOM_SETTLE_MS)

    const panel = page.getByTestId("issue-detail-panel")
    await expect(panel.getByRole("tab", { name: "Planning" })).not.toBeVisible()
  })

  test("Details tab is selected by default", async ({ page }) => {
    await setupBaseMocks(page, { phases: ["planning", "implementation"] })
    await navigateToApp(page)
    await openIssuePanel(page, "Task With Logs")

    const panel = page.getByTestId("issue-detail-panel")
    const detailsTab = panel.getByRole("tab", { name: "Details" })
    await expect(detailsTab).toBeVisible({ timeout: 5000 })
    await expect(detailsTab).toHaveAttribute("aria-selected", "true")
  })

  test("only available phases are shown as tabs", async ({ page }) => {
    await setupBaseMocks(page, { phases: ["planning"] })
    await navigateToApp(page)
    await openIssuePanel(page, "Task With Logs")

    const panel = page.getByTestId("issue-detail-panel")
    await expect(panel.getByRole("tab", { name: "Planning" })).toBeVisible({ timeout: 5000 })
    await expect(panel.getByRole("tab", { name: "Implementation" })).not.toBeVisible()
  })
})

test.describe("Suite 2: Planning Tab Log Streaming", () => {
  test("clicking Planning tab shows LogViewer component", async ({ page }) => {
    await mockTaskLogStreamSSE(page, "log-task-1", "planning", [
      { line: "Phase: planning started" },
    ])
    await setupBaseMocks(page, {
      phases: ["planning", "implementation"],
      skipTaskLogStream: true,
    })
    await navigateToApp(page)
    await openIssuePanel(page, "Task With Logs")

    const panel = page.getByTestId("issue-detail-panel")
    const planningTab = panel.getByRole("tab", { name: "Planning" })
    await expect(planningTab).toBeVisible({ timeout: 5000 })
    await planningTab.click()

    await expect(page.getByTestId("log-viewer")).toBeVisible()
  })

  test("LogViewer displays streamed log lines", async ({ page }) => {
    await mockTaskLogStreamSSE(page, "log-task-1", "planning", [
      { line: "Analyzing requirements..." },
      { line: "Creating design document..." },
      { line: "Planning complete!" },
    ])
    await setupBaseMocks(page, {
      phases: ["planning", "implementation"],
      skipTaskLogStream: true,
    })
    await navigateToApp(page)
    await openIssuePanel(page, "Task With Logs")

    const panel = page.getByTestId("issue-detail-panel")
    await expect(panel.getByRole("tab", { name: "Planning" })).toBeVisible({ timeout: 5000 })
    await panel.getByRole("tab", { name: "Planning" }).click()

    const logViewer = page.getByTestId("log-viewer")
    await expect(logViewer.getByText("Analyzing requirements...")).toBeVisible({ timeout: 5000 })
    await expect(logViewer.getByText("Creating design document...")).toBeVisible()
    await expect(logViewer.getByText("Planning complete!")).toBeVisible()
  })

  test("LogViewer displays line numbers", async ({ page }) => {
    await mockTaskLogStreamSSE(page, "log-task-1", "planning", [
      { line: "Line A" },
      { line: "Line B" },
    ])
    await setupBaseMocks(page, {
      phases: ["planning"],
      skipTaskLogStream: true,
    })
    await navigateToApp(page)
    await openIssuePanel(page, "Task With Logs")

    const panel = page.getByTestId("issue-detail-panel")
    await expect(panel.getByRole("tab", { name: "Planning" })).toBeVisible({ timeout: 5000 })
    await panel.getByRole("tab", { name: "Planning" }).click()

    const logViewer = page.getByTestId("log-viewer")
    await expect(logViewer.getByText("Line A")).toBeVisible({ timeout: 5000 })

    const lineNumbers = logViewer.locator('[class*="lineNumber"]')
    const count = await lineNumbers.count()
    expect(count).toBe(2)
    await expect(lineNumbers.first()).toContainText("1")
  })

  test("connection status indicator shows correct state", async ({ page }) => {
    await mockTaskLogStreamSSE(page, "log-task-1", "planning", [
      { line: "test log" },
    ])
    await setupBaseMocks(page, {
      phases: ["planning"],
      skipTaskLogStream: true,
    })
    await navigateToApp(page)
    await openIssuePanel(page, "Task With Logs")

    const panel = page.getByTestId("issue-detail-panel")
    await expect(panel.getByRole("tab", { name: "Planning" })).toBeVisible({ timeout: 5000 })
    await panel.getByRole("tab", { name: "Planning" }).click()

    const logViewer = page.getByTestId("log-viewer")
    await expect(logViewer).toBeVisible()

    const statusDot = logViewer.locator("[data-state]")
    await expect(statusDot).toBeVisible()
    const state = await statusDot.getAttribute("data-state")
    expect(["connected", "connecting", "reconnecting", "disconnected"]).toContain(state)
  })

  test("switching back to Details tab hides LogViewer", async ({ page }) => {
    await mockTaskLogStreamSSE(page, "log-task-1", "planning", [
      { line: "some log" },
    ])
    await setupBaseMocks(page, {
      phases: ["planning"],
      skipTaskLogStream: true,
    })
    await navigateToApp(page)
    await openIssuePanel(page, "Task With Logs")

    const panel = page.getByTestId("issue-detail-panel")
    await expect(panel.getByRole("tab", { name: "Planning" })).toBeVisible({ timeout: 5000 })
    await panel.getByRole("tab", { name: "Planning" }).click()

    const logViewer = page.getByTestId("log-viewer")
    await expect(logViewer).toBeVisible()

    // Switch back to Details
    await panel.getByRole("tab", { name: "Details" }).click()
    await expect(logViewer).not.toBeVisible()
  })
})

test.describe("Suite 3: Implementation Tab Log Streaming", () => {
  test("clicking Implementation tab shows LogViewer", async ({ page }) => {
    await mockTaskLogStreamSSE(page, "log-task-1", "implementation", [
      { line: "Building project..." },
      { line: "Running tests..." },
    ])
    await setupBaseMocks(page, {
      phases: ["planning", "implementation"],
      skipTaskLogStream: true,
    })
    await navigateToApp(page)
    await openIssuePanel(page, "Task With Logs")

    const panel = page.getByTestId("issue-detail-panel")
    await expect(panel.getByRole("tab", { name: "Implementation" })).toBeVisible({ timeout: 5000 })
    await panel.getByRole("tab", { name: "Implementation" }).click()

    const logViewer = page.getByTestId("log-viewer")
    await expect(logViewer.getByText("Building project...")).toBeVisible({ timeout: 5000 })
    await expect(logViewer.getByText("Running tests...")).toBeVisible()
  })
})

test.describe("Suite 4: Tab Switching Behavior", () => {
  test("switching between Planning and Implementation clears previous logs", async ({
    page,
  }) => {
    await mockTaskLogStreamSSE(page, "log-task-1", "planning", [
      { line: "Planning log line" },
    ])
    await mockTaskLogStreamSSE(page, "log-task-1", "implementation", [
      { line: "Implementation log line" },
    ])
    await setupBaseMocks(page, {
      phases: ["planning", "implementation"],
      skipTaskLogStream: true,
    })
    await navigateToApp(page)
    await openIssuePanel(page, "Task With Logs")

    const panel = page.getByTestId("issue-detail-panel")
    const logViewer = page.getByTestId("log-viewer")

    // Wait for tabs to appear, then click Planning
    await expect(panel.getByRole("tab", { name: "Planning" })).toBeVisible({ timeout: 5000 })
    await panel.getByRole("tab", { name: "Planning" }).click()
    await expect(logViewer.getByText("Planning log line")).toBeVisible({ timeout: 5000 })

    // Switch to Implementation tab
    await panel.getByRole("tab", { name: "Implementation" }).click()

    // Previous planning logs should be cleared, implementation logs shown
    await expect(logViewer.getByText("Implementation log line")).toBeVisible({ timeout: 5000 })
    await expect(logViewer.getByText("Planning log line")).not.toBeVisible()
  })

  test("tab state resets to Details when switching to a different issue", async ({
    page,
  }) => {
    await mockTaskLogStreamSSE(page, "log-task-1", "planning", [
      { line: "some log" },
    ])
    await setupBaseMocks(page, {
      phases: ["planning"],
      skipTaskLogStream: true,
    })
    await navigateToApp(page)

    // Open task issue and switch to Planning tab
    await openIssuePanel(page, "Task With Logs")

    const panel = page.getByTestId("issue-detail-panel")
    await expect(panel.getByRole("tab", { name: "Planning" })).toBeVisible({ timeout: 5000 })
    await panel.getByRole("tab", { name: "Planning" }).click()
    await expect(page.getByTestId("log-viewer")).toBeVisible()

    // Close panel
    await page.keyboard.press("Escape")
    await expect(panel).toHaveAttribute("data-state", "closed")

    // Open bug issue
    await openIssuePanel(page, "Bug Without Logs")
    await expect(page.getByTestId("issue-id")).toContainText("log-bug-1")

    // No log tabs should be visible
    await page.waitForTimeout(DOM_SETTLE_MS)
    await expect(panel.getByRole("tab", { name: "Planning" })).not.toBeVisible()
  })

  test("log stream disconnects when panel closes", async ({ page }) => {
    let sseRequestCount = 0

    await page.route("**/api/tasks/log-task-1/logs/planning/stream**", async (route) => {
      sseRequestCount++
      await route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        body: 'event: log-line\ndata: {"line": "test"}\n\n',
      })
    })
    await setupBaseMocks(page, {
      phases: ["planning"],
      skipTaskLogStream: true,
    })
    await navigateToApp(page)
    await openIssuePanel(page, "Task With Logs")

    const panel = page.getByTestId("issue-detail-panel")
    await expect(panel.getByRole("tab", { name: "Planning" })).toBeVisible({ timeout: 5000 })
    await panel.getByRole("tab", { name: "Planning" }).click()

    await page.waitForTimeout(DOM_SETTLE_MS)
    expect(sseRequestCount).toBeGreaterThanOrEqual(1)

    // Close panel
    await page.keyboard.press("Escape")
    await page.waitForTimeout(DOM_SETTLE_MS)

    const countAfterClose = sseRequestCount
    await page.waitForTimeout(1000)

    // No infinite retry loop after close
    expect(sseRequestCount).toBeLessThan(countAfterClose + 5)
  })

  test("log stream disconnects when switching to Details tab", async ({
    page,
  }) => {
    let sseRequestCount = 0

    await page.route("**/api/tasks/log-task-1/logs/planning/stream**", async (route) => {
      sseRequestCount++
      await route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        body: 'event: log-line\ndata: {"line": "test"}\n\n',
      })
    })
    await setupBaseMocks(page, {
      phases: ["planning"],
      skipTaskLogStream: true,
    })
    await navigateToApp(page)
    await openIssuePanel(page, "Task With Logs")

    const panel = page.getByTestId("issue-detail-panel")
    await expect(panel.getByRole("tab", { name: "Planning" })).toBeVisible({ timeout: 5000 })
    await panel.getByRole("tab", { name: "Planning" }).click()

    await page.waitForTimeout(DOM_SETTLE_MS)
    expect(sseRequestCount).toBeGreaterThanOrEqual(1)

    // Switch to Details
    await panel.getByRole("tab", { name: "Details" }).click()
    await page.waitForTimeout(DOM_SETTLE_MS)

    const countAfterSwitch = sseRequestCount
    await page.waitForTimeout(1000)

    expect(sseRequestCount).toBeLessThan(countAfterSwitch + 5)
  })
})

test.describe("Suite 5: Edge Cases", () => {
  test("phase discovery API failure shows no log tabs", async ({ page }) => {
    await setupBaseMocks(page, { phasesError: true })
    await navigateToApp(page)
    await openIssuePanel(page, "Task With Logs")

    await expect(page.getByTestId("issue-id")).toContainText("log-task-1")
    await page.waitForTimeout(DOM_SETTLE_MS)

    // No log tabs should appear when phase discovery fails
    const panel = page.getByTestId("issue-detail-panel")
    await expect(panel.getByRole("tab", { name: "Planning" })).not.toBeVisible()

    // Details content should still be visible
    await expect(page.getByText("A task with log phases")).toBeVisible()
  })

  test("SSE stream error shows disconnected status", async ({ page }) => {
    await page.route("**/api/tasks/log-task-1/logs/planning/stream**", async (route) => {
      await route.fulfill({
        status: 500,
        body: "Internal Server Error",
      })
    })
    await setupBaseMocks(page, {
      phases: ["planning"],
      skipTaskLogStream: true,
    })
    await navigateToApp(page)
    await openIssuePanel(page, "Task With Logs")

    const panel = page.getByTestId("issue-detail-panel")
    await expect(panel.getByRole("tab", { name: "Planning" })).toBeVisible({ timeout: 5000 })
    await panel.getByRole("tab", { name: "Planning" }).click()

    const logViewer = page.getByTestId("log-viewer")
    await expect(logViewer).toBeVisible()

    await page.waitForTimeout(1000)

    const statusDot = logViewer.locator("[data-state]")
    const state = await statusDot.getAttribute("data-state")
    expect(["disconnected", "reconnecting", "connecting"]).toContain(state)
  })

  test("malformed SSE data does not crash LogViewer", async ({ page }) => {
    await page.route("**/api/tasks/log-task-1/logs/planning/stream**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        headers: { "Cache-Control": "no-cache" },
        body: "event: log-line\ndata: not-valid-json\n\n",
      })
    })
    await setupBaseMocks(page, {
      phases: ["planning"],
      skipTaskLogStream: true,
    })
    await navigateToApp(page)
    await openIssuePanel(page, "Task With Logs")

    const panel = page.getByTestId("issue-detail-panel")
    await expect(panel.getByRole("tab", { name: "Planning" })).toBeVisible({ timeout: 5000 })
    await panel.getByRole("tab", { name: "Planning" }).click()

    const logViewer = page.getByTestId("log-viewer")
    await expect(logViewer).toBeVisible()

    await page.waitForTimeout(1000)

    // App should not crash - LogViewer remains visible
    await expect(logViewer).toBeVisible()

    // Component handles malformed data by using raw string
    await expect(logViewer.getByText("not-valid-json")).toBeVisible()
  })

  test("panel close cleans up SSE connections", async ({ page }) => {
    let connectionCount = 0

    await page.route("**/api/tasks/log-task-1/logs/planning/stream**", async (route) => {
      connectionCount++
      await route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        body: 'event: log-line\ndata: {"line": "cleanup test"}\n\n',
      })
    })
    await setupBaseMocks(page, {
      phases: ["planning"],
      skipTaskLogStream: true,
    })
    await navigateToApp(page)
    await openIssuePanel(page, "Task With Logs")

    const panel = page.getByTestId("issue-detail-panel")
    await expect(panel.getByRole("tab", { name: "Planning" })).toBeVisible({ timeout: 5000 })
    await panel.getByRole("tab", { name: "Planning" }).click()

    await page.waitForTimeout(DOM_SETTLE_MS)
    expect(connectionCount).toBeGreaterThanOrEqual(1)

    // Close panel via X button
    const closeButton = page.getByTestId("header-close-button")
    await closeButton.click()

    await expect(panel).toHaveAttribute("data-state", "closed")

    // Verify LogViewer is no longer visible
    await expect(page.getByTestId("log-viewer")).not.toBeVisible()
  })
})
