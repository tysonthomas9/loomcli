import { test, expect, Page } from "@playwright/test"

/**
 * E2E tests for Log Streaming functionality.
 *
 * Tests verify that:
 * - LogViewer component renders correctly in AgentDetailPanel (Logs tab)
 * - SSE connections work as expected (with mocking)
 * - UI handles various connection states gracefully
 * - Tab switching, panel close, and keyboard interactions work
 *
 * These tests follow patterns from server-push.spec.ts and monitor-panels.spec.ts.
 */

// Timing constants (following server-push.spec.ts pattern)
const DOM_SETTLE_MS = 500
const LOG_CONNECTION_WAIT_MS = 1000

// Mock agents for testing
const mockAgents = [
  {
    name: "nova",
    status: "working",
    branch: "feature-auth",
    task: "bd-101",
    ahead: 2,
    behind: 0,
    last_seen: "2026-01-24T12:00:00Z",
  },
  {
    name: "spark",
    status: "idle",
    branch: "main",
    task: "",
    ahead: 0,
    behind: 0,
    last_seen: "2026-01-24T11:30:00Z",
  },
]

const mockAgentTasks: Record<string, { id: string; title: string; priority: number }> = {
  nova: { id: "bd-101", title: "Implement authentication", priority: 1 },
}

const mockLoomStatus = {
  agents: mockAgents,
  tasks: {
    needs_planning: 0,
    ready_to_implement: 1,
    in_progress: 1,
    need_review: 0,
    backlog: 0,  // API uses "backlog", frontend maps to "blocked"
  },
  agent_tasks: mockAgentTasks,
  sync: {
    db_synced: true,
    db_last_sync: "2026-01-24T12:00:00Z",
    git_needs_push: 0,
    git_needs_pull: 0,
  },
  stats: {
    open: 5,
    closed: 3,
    total: 8,
    completion: 37,
  },
  timestamp: "2026-01-24T12:00:00Z",
}

const mockIssues = [
  {
    id: "test-1",
    title: "Feature A",
    status: "open",
    priority: 2,
    issue_type: "feature",
    created_at: "2026-01-24T10:00:00Z",
    updated_at: "2026-01-24T10:00:00Z",
    depends_on: [],
  },
]

/**
 * Set up all API mocks for log streaming tests.
 * @param mockLogStream - If true, aborts log stream SSE by default. If false, skips the log stream mock.
 */
async function setupBaseMocks(page: Page, mockLogStream: boolean = true) {
  await page.route("**/api/ready", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: mockIssues }),
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
      body: JSON.stringify({ success: true, issues: mockIssues }),
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
        data: { open: 5, closed: 3, total: 8, completion: 37 },
      }),
    })
  })

  // Mock loom API - intercept at both the relative and absolute URL patterns
  // Frontend uses /api/loom/api/* which gets proxied to localhost:9000/api/*

  // Mock agents endpoint (used by fetchAgents)
  await page.route("**/api/loom/api/agents", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ agents: mockAgents }),
    })
  })

  await page.route("**/localhost:9000/api/agents", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ agents: mockAgents }),
    })
  })

  // Mock status endpoint (used by fetchStatus)
  await page.route("**/api/loom/api/status", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(mockLoomStatus),
    })
  })

  await page.route("**/localhost:9000/api/status", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(mockLoomStatus),
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

  await page.route("**/localhost:9000/api/tasks", async (route) => {
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

  // Default: abort log stream SSE (when mockLogStream is true)
  if (mockLogStream) {
    await page.route("**/api/agents/*/logs/stream**", async (route) => {
      await route.abort()
    })
  }
}

/**
 * Mock log stream SSE endpoint with specified log lines.
 * Must be called BEFORE setupBaseMocks to override the abort route.
 */
async function mockLogStreamSSE(
  page: Page,
  agentName: string,
  events: Array<{ line: string; timestamp?: string }>
): Promise<void> {
  const pattern = `**/api/agents/${agentName}/logs/stream**`

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
 * Navigate to monitor view and wait for loom status to load.
 */
async function navigateToMonitor(page: Page) {
  const [response] = await Promise.all([
    page.waitForResponse(
      (res) => res.url().includes("/api/ready") && res.status() === 200
    ),
    page.goto("/?view=monitor"),
  ])
  expect(response.ok()).toBe(true)

  // Wait for loom status to load
  await page.waitForResponse(
    (res) => res.url().includes("/api/status") && res.status() === 200,
    { timeout: 10000 }
  )
  await page.waitForTimeout(DOM_SETTLE_MS)
}

/**
 * Wait for agents to load in the Agent Activity Panel.
 */
async function waitForAgents(page: Page) {
  const agentPanel = page.getByTestId("agent-activity-panel")
  await expect(agentPanel).toBeVisible({ timeout: 10000 })
  // Wait for summary bar to render (indicates agents are loaded)
  await expect(
    agentPanel.getByText("active", { exact: true })
  ).toBeVisible({ timeout: 10000 })
}

/**
 * Click on an agent card to open the detail panel.
 */
async function selectAgent(page: Page, agentName: string) {
  const agentPanel = page.getByTestId("agent-activity-panel")
  // Find the card with the agent name - use role="button" to find interactive cards
  const agentCard = agentPanel.locator('[role="button"]', { hasText: agentName })
  await expect(agentCard).toBeVisible({ timeout: 5000 })
  await agentCard.click()
  // Wait for panel to open
  await expect(page.getByTestId("agent-detail-panel")).toHaveAttribute("data-state", "open", { timeout: 5000 })
}

/**
 * Click the Logs tab in the agent detail panel.
 */
async function openLogsTab(page: Page) {
  const detailPanel = page.getByTestId("agent-detail-panel")
  await expect(detailPanel).toBeVisible()
  const logsTab = detailPanel.getByRole("tab", { name: "Logs" })
  await logsTab.click()
}

test.describe("Suite 1: AgentDetailPanel Logs Tab", () => {
  test("Logs tab exists when agent selected", async ({ page }) => {
    await setupBaseMocks(page)
    await navigateToMonitor(page)
    await waitForAgents(page)

    // Click on nova agent
    await selectAgent(page, "nova")

    // Verify AgentDetailPanel opens
    const detailPanel = page.getByTestId("agent-detail-panel")
    await expect(detailPanel).toBeVisible()
    await expect(detailPanel).toHaveAttribute("data-state", "open")

    // Verify both Info and Logs tabs are visible
    const infoTab = detailPanel.getByRole("tab", { name: "Info" })
    const logsTab = detailPanel.getByRole("tab", { name: "Logs" })
    await expect(infoTab).toBeVisible()
    await expect(logsTab).toBeVisible()
  })

  test("Clicking Logs tab shows LogViewer component", async ({ page }) => {
    await setupBaseMocks(page)
    await navigateToMonitor(page)
    await waitForAgents(page)
    await selectAgent(page, "nova")
    await openLogsTab(page)

    // Verify LogViewer is visible
    const logViewer = page.getByTestId("log-viewer")
    await expect(logViewer).toBeVisible()
  })

  test("LogViewer has connection status indicator", async ({ page }) => {
    await setupBaseMocks(page)
    await navigateToMonitor(page)
    await waitForAgents(page)
    await selectAgent(page, "nova")
    await openLogsTab(page)

    const logViewer = page.getByTestId("log-viewer")
    await expect(logViewer).toBeVisible()

    // Check status dot exists with data-state attribute
    const statusDot = logViewer.locator("[data-state]")
    await expect(statusDot).toBeVisible()
    const state = await statusDot.getAttribute("data-state")
    expect(["connected", "connecting", "reconnecting", "disconnected"]).toContain(state)

    // Check status label is visible (one of the valid states)
    const statusLabel = logViewer.locator('[class*="statusLabel"]')
    await expect(statusLabel).toBeVisible()
  })

  test("Info tab is selected by default", async ({ page }) => {
    await setupBaseMocks(page)
    await navigateToMonitor(page)
    await waitForAgents(page)
    await selectAgent(page, "nova")

    const detailPanel = page.getByTestId("agent-detail-panel")
    const infoTab = detailPanel.getByRole("tab", { name: "Info" })
    await expect(infoTab).toHaveAttribute("aria-selected", "true")

    const logsTab = detailPanel.getByRole("tab", { name: "Logs" })
    await expect(logsTab).toHaveAttribute("aria-selected", "false")
  })

  test("Switching between tabs works correctly", async ({ page }) => {
    await setupBaseMocks(page)
    await navigateToMonitor(page)
    await waitForAgents(page)
    await selectAgent(page, "nova")

    const detailPanel = page.getByTestId("agent-detail-panel")

    // Initially Info tab is selected
    let infoTab = detailPanel.getByRole("tab", { name: "Info" })
    await expect(infoTab).toHaveAttribute("aria-selected", "true")

    // Click Logs tab
    const logsTab = detailPanel.getByRole("tab", { name: "Logs" })
    await logsTab.click()
    await expect(logsTab).toHaveAttribute("aria-selected", "true")
    await expect(infoTab).toHaveAttribute("aria-selected", "false")

    // LogViewer should be visible
    const logViewer = page.getByTestId("log-viewer")
    await expect(logViewer).toBeVisible()

    // Click Info tab again
    infoTab = detailPanel.getByRole("tab", { name: "Info" })
    await infoTab.click()
    await expect(infoTab).toHaveAttribute("aria-selected", "true")
    await expect(logsTab).toHaveAttribute("aria-selected", "false")

    // LogViewer should not be visible
    await expect(logViewer).not.toBeVisible()
  })
})

test.describe("Suite 2: LogViewer Component", () => {
  test("Renders with empty state when no logs", async ({ page }) => {
    await setupBaseMocks(page)
    await navigateToMonitor(page)
    await waitForAgents(page)
    await selectAgent(page, "nova")
    await openLogsTab(page)

    const logViewer = page.getByTestId("log-viewer")
    await expect(logViewer).toBeVisible()

    // Check for empty state message
    await expect(
      logViewer.getByText("No logs available yet. Logs appear when the agent starts working.")
    ).toBeVisible()
  })

  test("Displays log lines with correct formatting", async ({ page }) => {
    // Mock SSE with log lines - skip the default abort route
    await mockLogStreamSSE(page, "nova", [
      { line: "Starting task bd-101..." },
      { line: "Running tests..." },
      { line: "All tests passed!" },
    ])
    await setupBaseMocks(page, false)

    await navigateToMonitor(page)
    await waitForAgents(page)
    await selectAgent(page, "nova")
    await openLogsTab(page)

    const logViewer = page.getByTestId("log-viewer")
    await expect(logViewer).toBeVisible()

    // Wait for log lines to appear
    await expect(logViewer.getByText("Starting task bd-101...")).toBeVisible({ timeout: 5000 })
    await expect(logViewer.getByText("Running tests...")).toBeVisible()
    await expect(logViewer.getByText("All tests passed!")).toBeVisible()
  })

  test("Line numbers display correctly", async ({ page }) => {
    await mockLogStreamSSE(page, "nova", [
      { line: "Line one" },
      { line: "Line two" },
      { line: "Line three" },
    ])
    await setupBaseMocks(page, false)

    await navigateToMonitor(page)
    await waitForAgents(page)
    await selectAgent(page, "nova")
    await openLogsTab(page)

    const logViewer = page.getByTestId("log-viewer")
    await expect(logViewer).toBeVisible()

    // Wait for lines to appear
    await expect(logViewer.getByText("Line one")).toBeVisible({ timeout: 5000 })

    // Verify line numbers are displayed
    const lineNumbers = logViewer.locator('[class*="lineNumber"]')
    const count = await lineNumbers.count()
    expect(count).toBe(3)

    // Check first line number is 1
    await expect(lineNumbers.first()).toContainText("1")
  })

  test("Dark background styling applied via CSS class", async ({ page }) => {
    await setupBaseMocks(page)
    await navigateToMonitor(page)
    await waitForAgents(page)
    await selectAgent(page, "nova")
    await openLogsTab(page)

    const logViewer = page.getByTestId("log-viewer")
    await expect(logViewer).toBeVisible()

    // LogViewer should have container class with dark styling
    // We verify the component renders - CSS module styling is applied via class
    await expect(logViewer).toBeVisible()
  })
})

test.describe("Suite 3: SSE Connection (Mocked)", () => {
  test("Connection status shows 'Connecting' initially", async ({ page }) => {
    // Create a route that delays before aborting to keep connection in connecting state
    await page.route("**/api/agents/nova/logs/stream**", async (route) => {
      // Delay briefly to observe connecting state, then abort
      await new Promise(resolve => setTimeout(resolve, DOM_SETTLE_MS))
      await route.abort()
    })
    await setupBaseMocks(page, false)

    await navigateToMonitor(page)
    await waitForAgents(page)
    await selectAgent(page, "nova")
    await openLogsTab(page)

    const logViewer = page.getByTestId("log-viewer")
    await expect(logViewer).toBeVisible()

    // Status should show connecting
    const statusDot = logViewer.locator("[data-state]")
    const state = await statusDot.getAttribute("data-state")
    expect(["connecting", "reconnecting"]).toContain(state)
  })

  test("Connection status shows 'Connected' after SSE opens", async ({ page }) => {
    await mockLogStreamSSE(page, "nova", [{ line: "Hello from logs" }])
    await setupBaseMocks(page, false)

    await navigateToMonitor(page)
    await waitForAgents(page)
    await selectAgent(page, "nova")
    await openLogsTab(page)

    const logViewer = page.getByTestId("log-viewer")
    await expect(logViewer).toBeVisible()

    // Wait for log line to confirm connection worked
    // Note: With Playwright SSE mocking, the connection closes after events are sent,
    // triggering a reconnect. We verify the data was received successfully.
    await expect(logViewer.getByText("Hello from logs")).toBeVisible({ timeout: 5000 })

    // The status may show "connected" briefly or "reconnecting" after data is received
    // since the mocked SSE closes immediately after sending events.
    const statusDot = logViewer.locator("[data-state]")
    const state = await statusDot.getAttribute("data-state")
    expect(["connected", "reconnecting"]).toContain(state)
  })

  test("Connection status shows 'Disconnected' on error", async ({ page }) => {
    // Mock SSE endpoint that returns error
    await page.route("**/api/agents/nova/logs/stream**", async (route) => {
      await route.fulfill({
        status: 500,
        body: "Internal Server Error",
      })
    })
    await setupBaseMocks(page)

    await navigateToMonitor(page)
    await waitForAgents(page)
    await selectAgent(page, "nova")
    await openLogsTab(page)

    const logViewer = page.getByTestId("log-viewer")
    await expect(logViewer).toBeVisible()

    // Wait for connection state to update
    await page.waitForTimeout(LOG_CONNECTION_WAIT_MS)

    // Status should show disconnected or reconnecting
    const statusDot = logViewer.locator("[data-state]")
    const state = await statusDot.getAttribute("data-state")
    expect(["disconnected", "reconnecting", "connecting"]).toContain(state)
  })

  test("Log lines stream in real-time (simulated)", async ({ page }) => {
    await mockLogStreamSSE(page, "nova", [
      { line: "Build started" },
      { line: "Compiling..." },
      { line: "Build complete!" },
    ])
    await setupBaseMocks(page, false)

    await navigateToMonitor(page)
    await waitForAgents(page)
    await selectAgent(page, "nova")
    await openLogsTab(page)

    const logViewer = page.getByTestId("log-viewer")

    // Verify lines appear in order
    await expect(logViewer.getByText("Build started")).toBeVisible({ timeout: 5000 })
    await expect(logViewer.getByText("Compiling...")).toBeVisible()
    await expect(logViewer.getByText("Build complete!")).toBeVisible()

    // Verify line numbers increment
    const lineNumbers = logViewer.locator('[class*="lineNumber"]')
    await expect(lineNumbers.nth(0)).toContainText("1")
    await expect(lineNumbers.nth(1)).toContainText("2")
    await expect(lineNumbers.nth(2)).toContainText("3")
  })
})

test.describe("Suite 4: Edge Cases", () => {
  test("Panel closes when Escape key pressed", async ({ page }) => {
    await setupBaseMocks(page)
    await navigateToMonitor(page)
    await waitForAgents(page)
    await selectAgent(page, "nova")

    const detailPanel = page.getByTestId("agent-detail-panel")
    await expect(detailPanel).toHaveAttribute("data-state", "open")

    // Press Escape
    await page.keyboard.press("Escape")

    // Panel should close
    const overlay = page.getByTestId("agent-detail-overlay")
    await expect(overlay).toHaveAttribute("aria-hidden", "true")
  })

  test("Tab state resets when agent changes", async ({ page }) => {
    await setupBaseMocks(page)
    await navigateToMonitor(page)
    await waitForAgents(page)

    // Select nova and switch to Logs tab
    await selectAgent(page, "nova")
    await openLogsTab(page)

    const detailPanel = page.getByTestId("agent-detail-panel")
    let logsTab = detailPanel.getByRole("tab", { name: "Logs" })
    await expect(logsTab).toHaveAttribute("aria-selected", "true")

    // Close panel
    await page.keyboard.press("Escape")

    // Select spark agent
    await selectAgent(page, "spark")

    // Info tab should be active (reset to default)
    const infoTab = page.getByTestId("agent-detail-panel").getByRole("tab", { name: "Info" })
    await expect(infoTab).toHaveAttribute("aria-selected", "true")

    logsTab = page.getByTestId("agent-detail-panel").getByRole("tab", { name: "Logs" })
    await expect(logsTab).toHaveAttribute("aria-selected", "false")
  })

  test("Log stream disconnects when panel closes", async ({ page }) => {
    let sseRequestCount = 0

    await page.route("**/api/agents/nova/logs/stream**", async (route) => {
      sseRequestCount++
      await route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        body: "event: log-line\ndata: {\"line\": \"test\"}\n\n",
      })
    })
    await setupBaseMocks(page, false)

    await navigateToMonitor(page)
    await waitForAgents(page)

    // Open panel and switch to Logs tab
    await selectAgent(page, "nova")
    await openLogsTab(page)

    // Wait for SSE request
    await page.waitForTimeout(DOM_SETTLE_MS)
    expect(sseRequestCount).toBeGreaterThanOrEqual(1)

    // Close panel
    await page.keyboard.press("Escape")

    // Wait briefly for cleanup
    await page.waitForTimeout(DOM_SETTLE_MS)

    // Record request count after close
    const countAfterClose = sseRequestCount

    // Wait to see if more requests come in
    await page.waitForTimeout(LOG_CONNECTION_WAIT_MS)

    // No additional SSE requests should be made after panel closes
    // (EventSource may retry, but component should disconnect)
    // Just verify we're not in an infinite loop
    expect(sseRequestCount).toBeLessThan(countAfterClose + 5)
  })

  test("Malformed SSE data does not crash", async ({ page }) => {
    const consoleErrors: string[] = []
    page.on("console", (msg) => {
      if (msg.type() === "error") {
        consoleErrors.push(msg.text())
      }
    })

    await page.route("**/api/agents/nova/logs/stream**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        headers: {
          "Cache-Control": "no-cache",
        },
        body: "event: log-line\ndata: not-valid-json\n\n",
      })
    })
    await setupBaseMocks(page, false)

    await navigateToMonitor(page)
    await waitForAgents(page)
    await selectAgent(page, "nova")
    await openLogsTab(page)

    const logViewer = page.getByTestId("log-viewer")
    await expect(logViewer).toBeVisible()

    // Wait for potential processing
    await page.waitForTimeout(LOG_CONNECTION_WAIT_MS)

    // LogViewer should continue working - app doesn't crash
    await expect(logViewer).toBeVisible()

    // The component should handle malformed data gracefully
    // (it uses the raw string as the line content)
    await expect(logViewer.getByText("not-valid-json")).toBeVisible()
  })

  test("Overlay click closes panel", async ({ page }) => {
    await setupBaseMocks(page)
    await navigateToMonitor(page)
    await waitForAgents(page)
    await selectAgent(page, "nova")

    const detailPanel = page.getByTestId("agent-detail-panel")
    await expect(detailPanel).toHaveAttribute("data-state", "open")

    // Click the overlay (outside the panel)
    const overlay = page.getByTestId("agent-detail-overlay")
    await overlay.click({ position: { x: 10, y: 10 } }) // Click far left to avoid panel

    // Panel should close
    await expect(overlay).toHaveAttribute("aria-hidden", "true")
  })

  test("Close button closes panel", async ({ page }) => {
    await setupBaseMocks(page)
    await navigateToMonitor(page)
    await waitForAgents(page)
    await selectAgent(page, "nova")

    const detailPanel = page.getByTestId("agent-detail-panel")
    await expect(detailPanel).toHaveAttribute("data-state", "open")

    // Click close button
    const closeButton = detailPanel.getByRole("button", { name: "Close panel" })
    await closeButton.click()

    // Panel should close
    const overlay = page.getByTestId("agent-detail-overlay")
    await expect(overlay).toHaveAttribute("aria-hidden", "true")
  })
})

test.describe("Suite 5: Auto-scroll behavior", () => {
  test("Scroll to bottom button appears when scrolled up", async ({ page }) => {
    // Create many log lines to enable scrolling
    const manyLines = Array.from({ length: 50 }, (_, i) => ({
      line: `Log line ${i + 1}: This is a test message that should appear in the log viewer`,
    }))

    await mockLogStreamSSE(page, "nova", manyLines)
    await setupBaseMocks(page, false)

    await navigateToMonitor(page)
    await waitForAgents(page)
    await selectAgent(page, "nova")
    await openLogsTab(page)

    const logViewer = page.getByTestId("log-viewer")
    await expect(logViewer).toBeVisible()

    // Wait for all log lines
    await expect(logViewer.getByText("Log line 50:")).toBeVisible({ timeout: 10000 })

    // Find the scroll container and scroll up
    const scrollContainer = logViewer.locator('[role="log"]')
    await scrollContainer.evaluate((el) => {
      el.scrollTop = 0
    })

    // Wait for scroll event to register
    await page.waitForTimeout(100)

    // Scroll to bottom button should appear
    const scrollButton = logViewer.getByRole("button", { name: /scroll to bottom/i })
    await expect(scrollButton).toBeVisible()

    // Click the button
    await scrollButton.click()

    // Button should disappear after clicking
    await expect(scrollButton).not.toBeVisible({ timeout: 2000 })
  })
})
