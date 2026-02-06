import { test, expect, Page } from "@playwright/test"

/**
 * Mock issues for the Monitor Dashboard tests.
 * Includes issues with dependencies for the MiniDependencyGraph.
 */
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
  {
    id: "test-2",
    title: "Task blocked by Feature A",
    status: "open",
    priority: 1,
    issue_type: "task",
    created_at: "2026-01-24T11:00:00Z",
    updated_at: "2026-01-24T11:00:00Z",
    depends_on: [{ id: "test-1", type: "blocks" }],
  },
  {
    id: "test-3",
    title: "In Progress Task",
    status: "in_progress",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-24T12:00:00Z",
    updated_at: "2026-01-24T12:00:00Z",
    depends_on: [],
  },
]

/**
 * Mock loom server status response with agent data.
 */
const mockLoomStatus = {
  agents: [
    {
      name: "dev1",
      status: "working",
      branch: "feature-1",
      task: "bd-001",
      ahead: 0,
      behind: 0,
      last_seen: "2026-01-24T12:00:00Z",
    },
    {
      name: "dev2",
      status: "idle",
      branch: "main",
      task: "",
      ahead: 0,
      behind: 0,
      last_seen: "2026-01-24T11:30:00Z",
    },
  ],
  tasks: {
    needs_planning: 2,
    ready_to_implement: 3,
    in_progress: 1,
    need_review: 1,
    blocked: 0,
  },
  agent_tasks: {
    dev1: {
      id: "bd-001",
      title: "Implement feature X",
      priority: 2,
    },
  },
  sync: {
    db_synced: true,
    db_last_sync: "2026-01-24T12:00:00Z",
    git_needs_push: 0,
    git_needs_pull: 0,
  },
  stats: {
    open: 10,
    closed: 5,
    total: 15,
    completion: 33,
  },
  timestamp: "2026-01-24T12:00:00Z",
}

/**
 * Mock loom server tasks response.
 */
const mockLoomTasks = {
  needs_planning: [
    { id: "bd-010", title: "Plan new feature", priority: 2 },
    { id: "bd-011", title: "Design API", priority: 1 },
  ],
  ready_to_implement: [
    { id: "bd-020", title: "Implement login", priority: 1 },
    { id: "bd-021", title: "Add tests", priority: 2 },
    { id: "bd-022", title: "Fix bug", priority: 3 },
  ],
  in_progress: [{ id: "bd-001", title: "Implement feature X", priority: 2 }],
  needs_review: [{ id: "bd-030", title: "Review PR", priority: 2 }],
  blocked: [],
}

/**
 * Mock blocked issues response.
 */
const mockBlockedIssues = {
  success: true,
  data: [
    {
      id: "test-2",
      title: "Task blocked by Feature A",
      status: "open",
      priority: 1,
      issue_type: "task",
      created_at: "2026-01-24T11:00:00Z",
      updated_at: "2026-01-24T11:00:00Z",
      blocked_by: ["test-1"],
    },
  ],
}

/**
 * Set up all API mocks for Monitor Dashboard tests.
 * Mocks both beads backend and loom server endpoints.
 */
async function setupMocks(
  page: Page,
  options?: {
    loomServerAvailable?: boolean
    emptyAgents?: boolean
  }
) {
  const { loomServerAvailable = true, emptyAgents = false } = options ?? {}

  // Mock beads backend API
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
      body: JSON.stringify(mockBlockedIssues),
    })
  })

  await page.route("**/api/issues/graph", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: mockIssues }),
    })
  })

  // Mock SSE events endpoint to prevent connection errors
  // (App uses /api/events for real-time updates, not WebSocket)
  await page.route("**/api/events", async (route) => {
    await route.abort()
  })

  // Mock /api/stats to prevent proxy errors
  await page.route("**/api/stats", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: { open: 10, closed: 5, total: 15, completion: 33 },
      }),
    })
  })

  // Mock loom server API
  if (loomServerAvailable) {
    await page.route("**/localhost:9000/api/status", async (route) => {
      const status = emptyAgents
        ? { ...mockLoomStatus, agents: [], agent_tasks: {} }
        : mockLoomStatus
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(status),
      })
    })

    await page.route("**/localhost:9000/api/tasks", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(mockLoomTasks),
      })
    })
  } else {
    // Simulate loom server unavailable - return error response to trigger never_connected state
    // Use same pattern style as working mocks (**/localhost:9000/...)
    // Note: route.abort() doesn't reliably trigger error handling in some browser configs,
    // so we return empty data with isConnected behavior
    await page.route("**/localhost:9000/api/status", async (route) => {
      // Return an invalid response that triggers fetch error
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: "invalid json{",
      })
    })
    await page.route("**/localhost:9000/api/tasks", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: "invalid json{",
      })
    })
  }
}

/**
 * Navigate to a page and wait for API response.
 */
async function navigateAndWait(page: Page, path: string) {
  const [response] = await Promise.all([
    page.waitForResponse(
      (res) => res.url().includes("/api/ready") && res.status() === 200
    ),
    page.goto(path),
  ])
  expect(response.ok()).toBe(true)
}

test.describe("MonitorDashboard", () => {
  test.describe("navigation", () => {
    test("Monitor tab appears in ViewSwitcher", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      // Verify Monitor tab exists
      const monitorTab = page.getByTestId("view-tab-monitor")
      await expect(monitorTab).toBeVisible()
    })

    test("clicking Monitor tab shows dashboard", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      // Click Monitor tab
      const monitorTab = page.getByTestId("view-tab-monitor")
      await monitorTab.click()

      // Verify Monitor tab is selected
      await expect(monitorTab).toHaveAttribute("aria-selected", "true")

      // Verify MonitorDashboard is visible
      const dashboard = page.getByTestId("monitor-dashboard")
      await expect(dashboard).toBeVisible()
    })

    test("URL updates to ?view=monitor", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      // Verify URL has no view param initially
      expect(page.url()).not.toContain("view=")

      // Click Monitor tab
      const monitorTab = page.getByTestId("view-tab-monitor")
      await monitorTab.click()

      // Verify URL contains ?view=monitor
      expect(page.url()).toContain("view=monitor")
    })

    test("direct URL navigation to ?view=monitor works", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?view=monitor")

      // Verify Monitor tab is selected
      const monitorTab = page.getByTestId("view-tab-monitor")
      await expect(monitorTab).toHaveAttribute("aria-selected", "true")

      // Verify MonitorDashboard is visible
      const dashboard = page.getByTestId("monitor-dashboard")
      await expect(dashboard).toBeVisible()
    })

    test("monitor view persists after page reload", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      // Click Monitor tab to switch view
      const monitorTab = page.getByTestId("view-tab-monitor")
      await monitorTab.click()

      // Verify Monitor tab is selected
      await expect(monitorTab).toHaveAttribute("aria-selected", "true")

      // Verify URL has ?view=monitor
      expect(page.url()).toContain("view=monitor")

      // Reload the page (re-setup mocks since route handlers may be cleared)
      await setupMocks(page)
      const [response] = await Promise.all([
        page.waitForResponse(
          (res) => res.url().includes("/api/ready") && res.status() === 200
        ),
        page.reload(),
      ])
      expect(response.ok()).toBe(true)

      // Verify Monitor tab is still selected after reload
      await expect(monitorTab).toHaveAttribute("aria-selected", "true")

      // Verify MonitorDashboard is still visible
      const dashboard = page.getByTestId("monitor-dashboard")
      await expect(dashboard).toBeVisible()
    })
  })

  test.describe("panel rendering", () => {
    test("all 4 panels render with headings", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?view=monitor")

      // Wait for dashboard to be visible
      const dashboard = page.getByTestId("monitor-dashboard")
      await expect(dashboard).toBeVisible()

      // Verify all 4 panel headings are visible
      await expect(page.getByRole("heading", { name: "Agent Activity" })).toBeVisible()
      await expect(page.getByRole("heading", { name: "Work Pipeline" })).toBeVisible()
      await expect(page.getByRole("heading", { name: "Project Health" })).toBeVisible()
      await expect(page.getByRole("heading", { name: "Blocking Dependencies" })).toBeVisible()
    })

    test("AgentActivityPanel shows agents correctly", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?view=monitor")

      // Wait for agent panel to render
      const agentPanel = page.getByTestId("agent-activity-panel")
      await expect(agentPanel).toBeVisible()

      // Wait for agents to load - the summary section appears with 'active' label when agents are loaded
      // Use exact match to avoid matching agent card status text
      await expect(agentPanel.getByText("active", { exact: true })).toBeVisible({ timeout: 10000 })
      await expect(agentPanel.getByText("idle", { exact: true })).toBeVisible()

      // Verify summary section contains both active and idle items
      const summaryActive = agentPanel.locator('[data-type="active"]')
      const summaryIdle = agentPanel.locator('[data-type="idle"]')
      await expect(summaryActive).toBeVisible()
      await expect(summaryIdle).toBeVisible()
    })

    test("WorkPipelinePanel shows stage counts", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?view=monitor")

      // Wait for pipeline panel
      const pipelinePanel = page.getByTestId("work-pipeline-panel")
      await expect(pipelinePanel).toBeVisible()

      // Wait for loom tasks to load
      await page.waitForResponse(
        (res) => res.url().includes("/api/tasks") && res.status() === 200,
        { timeout: 10000 }
      )

      // Give time for React to process
      await page.waitForTimeout(500)

      // Verify pipeline stages exist
      await expect(page.getByTestId("pipeline-stage-plan")).toBeVisible()
      await expect(page.getByTestId("pipeline-stage-ready")).toBeVisible()
      await expect(page.getByTestId("pipeline-stage-inProgress")).toBeVisible()
      await expect(page.getByTestId("pipeline-stage-review")).toBeVisible()
    })

    test("MiniDependencyGraph renders", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?view=monitor")

      // Verify mini dependency graph is visible
      const miniGraph = page.getByTestId("mini-dependency-graph")
      await expect(miniGraph).toBeVisible()

      // Verify expand button exists
      const expandButton = page.getByRole("button", { name: "Expand to full graph view" })
      await expect(expandButton).toBeVisible()
    })

    test("ProjectHealthPanel shows completion percentage", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?view=monitor")

      // Wait for dashboard and loom status to load
      const dashboard = page.getByTestId("monitor-dashboard")
      await expect(dashboard).toBeVisible()

      await page.waitForResponse(
        (res) => res.url().includes("/api/status") && res.status() === 200,
        { timeout: 10000 }
      )
      await page.waitForTimeout(500)

      // Verify ProjectHealthPanel is visible
      const healthPanel = page.getByTestId("project-health-panel")
      await expect(healthPanel).toBeVisible()

      // Verify completion progress bar exists with correct value (33% from mockLoomStatus.stats.completion)
      const progressBar = healthPanel.getByRole("progressbar", { name: /project completion/i })
      await expect(progressBar).toBeVisible()
      await expect(progressBar).toHaveAttribute("aria-valuenow", "33")

      // Verify percentage text is displayed
      await expect(healthPanel.getByText("33%")).toBeVisible()
    })
  })

  test.describe("interactions", () => {
    test("expand button switches to Graph view", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?view=monitor")

      // Verify we're on monitor view
      const dashboard = page.getByTestId("monitor-dashboard")
      await expect(dashboard).toBeVisible()

      // Click expand button
      const expandButton = page.getByRole("button", { name: "Expand to full graph view" })
      await expandButton.click()

      // Verify URL changed to graph view
      expect(page.url()).toContain("view=graph")

      // Verify Graph tab is now selected
      const graphTab = page.getByTestId("view-tab-graph")
      await expect(graphTab).toHaveAttribute("aria-selected", "true")

      // Verify GraphView is visible and MonitorDashboard is not
      const graphView = page.getByTestId("graph-view")
      await expect(graphView).toBeVisible()
      await expect(dashboard).not.toBeVisible()
    })

    test("pipeline stage click opens TaskDrawer", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?view=monitor")

      // Wait for loom tasks to load
      await page.waitForResponse(
        (res) => res.url().includes("/api/tasks") && res.status() === 200,
        { timeout: 10000 }
      )

      // Give time for React to process
      await page.waitForTimeout(500)

      // Click on the Ready stage (should have 3 items based on mock)
      const readyStage = page.getByTestId("pipeline-stage-ready")
      await expect(readyStage).toBeVisible()
      await readyStage.click()

      // Verify TaskDrawer opens
      const taskDrawer = page.getByRole("dialog")
      await expect(taskDrawer).toBeVisible()

      // Verify drawer title shows correct category
      await expect(page.getByText("Ready to Implement")).toBeVisible()
    })
  })

  test.describe("graceful degradation", () => {
    test("renders with empty agent data", async ({ page }) => {
      // Setup with loom server available but no agents
      await setupMocks(page, { emptyAgents: true })
      await navigateAndWait(page, "/?view=monitor")

      // Wait for dashboard to render
      const dashboard = page.getByTestId("monitor-dashboard")
      await expect(dashboard).toBeVisible()

      // Wait for loom API response
      await page.waitForResponse(
        (res) => res.url().includes("/api/status") && res.status() === 200,
        { timeout: 10000 }
      )

      // Give time for React to process
      await page.waitForTimeout(500)

      // Verify empty state message
      const agentPanel = page.getByTestId("agent-activity-panel")
      await expect(agentPanel).toBeVisible()
      await expect(agentPanel.getByText("No agents found")).toBeVisible()
    })

  })

  test.describe("Work Pipeline deep behavior", () => {
    /**
     * Helper to set up mocks with blocked tasks present.
     */
    async function setupMocksWithBlocked(page: Page) {
      const statusWithBlocked = {
        ...mockLoomStatus,
        tasks: { ...mockLoomStatus.tasks, blocked: 2 },
      }
      const tasksWithBlocked = {
        ...mockLoomTasks,
        blocked: [
          { id: "bd-040", title: "Blocked task A", priority: 1 },
          { id: "bd-041", title: "Blocked task B", priority: 2 },
        ],
      }

      // Mock beads backend API (same as setupMocks)
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
          body: JSON.stringify(mockBlockedIssues),
        })
      })
      await page.route("**/api/issues/graph", async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: mockIssues }),
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
            data: { open: 10, closed: 5, total: 15, completion: 33 },
          }),
        })
      })

      // Mock loom server with blocked data
      await page.route("**/localhost:9000/api/status", async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(statusWithBlocked),
        })
      })
      await page.route("**/localhost:9000/api/tasks", async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(tasksWithBlocked),
        })
      })
    }

    /**
     * Helper to wait for pipeline data to load.
     */
    async function waitForPipelineData(page: Page) {
      await page.waitForResponse(
        (res) => res.url().includes("/api/tasks") && res.status() === 200,
        { timeout: 10000 }
      )
      await page.waitForTimeout(500)
    }

    test("all 5 stages render with correct counts", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?view=monitor")
      await waitForPipelineData(page)

      // Verify main stages with correct counts
      const planStage = page.getByTestId("pipeline-stage-plan")
      await expect(planStage).toBeVisible()
      await expect(planStage).toContainText("2")

      const readyStage = page.getByTestId("pipeline-stage-ready")
      await expect(readyStage).toBeVisible()
      await expect(readyStage).toContainText("3")

      const inProgressStage = page.getByTestId("pipeline-stage-inProgress")
      await expect(inProgressStage).toBeVisible()
      await expect(inProgressStage).toContainText("1")

      const reviewStage = page.getByTestId("pipeline-stage-review")
      await expect(reviewStage).toBeVisible()
      await expect(reviewStage).toContainText("1")

      // Blocked stage should NOT be visible (count is 0 in default mocks)
      await expect(page.getByTestId("pipeline-stage-blocked")).not.toBeVisible()

      // Done stage should be visible
      const pipelinePanel = page.getByTestId("work-pipeline-panel")
      await expect(pipelinePanel.getByText("✓")).toBeVisible()
      await expect(pipelinePanel.getByText("Done")).toBeVisible()
    })

    test("blocked stage shows as branch when count > 0", async ({ page }) => {
      await setupMocksWithBlocked(page)
      await navigateAndWait(page, "/?view=monitor")
      await waitForPipelineData(page)

      // Blocked stage should be visible
      const blockedStage = page.getByTestId("pipeline-stage-blocked")
      await expect(blockedStage).toBeVisible()
      await expect(blockedStage).toContainText("2")

      // Branch line should be visible
      const pipelinePanel = page.getByTestId("work-pipeline-panel")
      await expect(pipelinePanel.getByText("↳")).toBeVisible()

      // Click blocked stage -> verify TaskDrawer opens
      await blockedStage.click()
      const taskDrawer = page.getByRole("dialog")
      await expect(taskDrawer).toBeVisible()
      // Drawer heading should say "Backlog" (renamed from "Blocked")
      const drawerHeading = taskDrawer.getByRole("heading")
      await expect(drawerHeading).toContainText("Backlog")
      await expect(taskDrawer).toContainText("Blocked task A")
      await expect(taskDrawer).toContainText("Blocked task B")
    })

    test("click stage opens TaskDrawer with correct title for each category", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?view=monitor")
      await waitForPipelineData(page)

      // Click Plan stage
      await page.getByTestId("pipeline-stage-plan").click()
      const drawer = page.getByRole("dialog")
      await expect(drawer).toBeVisible()
      await expect(drawer).toContainText("Needs Planning")
      await expect(drawer).toContainText("(2)")
      await page.keyboard.press("Escape")
      await expect(drawer).not.toBeVisible()

      // Click In Progress stage
      await page.getByTestId("pipeline-stage-inProgress").click()
      await expect(drawer).toBeVisible()
      await expect(drawer).toContainText("In Progress")
      await expect(drawer).toContainText("(1)")
      await page.keyboard.press("Escape")
      await expect(drawer).not.toBeVisible()

      // Click Review stage
      await page.getByTestId("pipeline-stage-review").click()
      await expect(drawer).toBeVisible()
      await expect(drawer).toContainText("Needs Review")
      await expect(drawer).toContainText("(1)")
      await page.keyboard.press("Escape")
      await expect(drawer).not.toBeVisible()
    })

    test("TaskDrawer closes on overlay click and Escape", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?view=monitor")
      await waitForPipelineData(page)

      const drawer = page.getByRole("dialog")

      // Open drawer and close with Escape
      await page.getByTestId("pipeline-stage-ready").click()
      await expect(drawer).toBeVisible()
      await page.keyboard.press("Escape")
      await expect(drawer).not.toBeVisible()

      // Open drawer and close with overlay click
      await page.getByTestId("pipeline-stage-ready").click()
      await expect(drawer).toBeVisible()
      // Click the overlay on the left side of viewport (drawer occupies right side)
      await page.mouse.click(10, 300)
      await expect(drawer).not.toBeVisible()

      // Open drawer and close with X button
      await page.getByTestId("pipeline-stage-ready").click()
      await expect(drawer).toBeVisible()
      await page.getByRole("button", { name: "Close drawer" }).click()
      await expect(drawer).not.toBeVisible()
    })

    test("oldest items table shows correct data", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?view=monitor")
      await waitForPipelineData(page)

      const pipelinePanel = page.getByTestId("work-pipeline-panel")

      // Verify heading
      await expect(pipelinePanel.getByText("Oldest in Each Stage")).toBeVisible()

      // Verify table headers
      const table = pipelinePanel.locator("table")
      await expect(table.locator("th").nth(0)).toHaveText("Stage")
      await expect(table.locator("th").nth(1)).toHaveText("Task")
      await expect(table.locator("th").nth(2)).toHaveText("Priority")

      // Verify rows - one for each stage with tasks
      const rows = table.locator("tbody tr")
      await expect(rows).toHaveCount(4)

      // Plan row
      await expect(rows.nth(0)).toContainText("Plan")
      await expect(rows.nth(0)).toContainText("bd-010")
      await expect(rows.nth(0)).toContainText("Plan new feature")
      await expect(rows.nth(0)).toContainText("P2")

      // Ready row
      await expect(rows.nth(1)).toContainText("Ready")
      await expect(rows.nth(1)).toContainText("bd-020")
      await expect(rows.nth(1)).toContainText("Implement login")
      await expect(rows.nth(1)).toContainText("P1")

      // In Progress row
      await expect(rows.nth(2)).toContainText("In Progress")
      await expect(rows.nth(2)).toContainText("bd-001")
      await expect(rows.nth(2)).toContainText("Implement feature X")
      await expect(rows.nth(2)).toContainText("P2")

      // Review row
      await expect(rows.nth(3)).toContainText("Review")
      await expect(rows.nth(3)).toContainText("bd-030")
      await expect(rows.nth(3)).toContainText("Review PR")
      await expect(rows.nth(3)).toContainText("P2")
    })
  })
})
