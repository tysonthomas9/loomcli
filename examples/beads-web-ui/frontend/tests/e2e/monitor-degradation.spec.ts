import { test, expect, Page } from "@playwright/test"

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
 * Mock issues for beads backend API and degradation tests.
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
 * Set up beads backend API mocks (shared across all tests).
 */
async function setupBeadsMocks(page: Page) {
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
}

/**
 * Set up loom server mocks as available (valid responses).
 */
async function setupLoomAvailable(page: Page) {
  await page.route("**/localhost:9000/api/status", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(mockLoomStatus),
    })
  })

  await page.route("**/localhost:9000/api/tasks", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(mockLoomTasks),
    })
  })
}

/**
 * Set up loom server mocks as unavailable (invalid JSON triggers fetch error).
 */
async function setupLoomUnavailable(page: Page) {
  await page.route("**/localhost:9000/api/status", async (route) => {
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

/**
 * Switch loom mocks from available to unavailable mid-test.
 */
async function switchLoomToUnavailable(page: Page) {
  await page.unroute("**/localhost:9000/api/status")
  await page.unroute("**/localhost:9000/api/tasks")
  await setupLoomUnavailable(page)
}

/**
 * Switch loom mocks from unavailable to available mid-test.
 */
async function switchLoomToAvailable(page: Page) {
  await page.unroute("**/localhost:9000/api/status")
  await page.unroute("**/localhost:9000/api/tasks")
  await setupLoomAvailable(page)
}

/**
 * Navigate to monitor view and wait for beads API response.
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

test.describe("MonitorDashboard degradation", () => {
  test("shows 'Loom server not running' when never connected", async ({ page }) => {
    await setupBeadsMocks(page)
    await setupLoomUnavailable(page)
    await navigateAndWait(page, "/?view=monitor")

    const agentPanel = page.getByTestId("agent-activity-panel")
    await expect(agentPanel).toBeVisible()

    // Verify exact text for never_connected state
    await expect(agentPanel.getByText("Loom server not running")).toBeVisible({ timeout: 10000 })

    // Verify hint text with loom serve command
    await expect(agentPanel.getByText("loom serve --port 9000")).toBeVisible()

    // Verify Check Connection button is visible
    await expect(agentPanel.getByRole("button", { name: "Check Connection" })).toBeVisible()
  })

  test("Check Connection button is visible and clickable", async ({ page }) => {
    await setupBeadsMocks(page)
    await setupLoomUnavailable(page)
    await navigateAndWait(page, "/?view=monitor")

    const agentPanel = page.getByTestId("agent-activity-panel")
    await expect(agentPanel).toBeVisible()

    // Wait for never_connected state to render
    await expect(agentPanel.getByText("Loom server not running")).toBeVisible({ timeout: 10000 })

    // Locate and verify the button
    const checkButton = agentPanel.getByRole("button", { name: "Check Connection" })
    await expect(checkButton).toBeVisible()
    await expect(checkButton).toBeEnabled()

    // Click it - should not crash, panel stays in degraded state
    await checkButton.click()

    // Panel should still be visible after click (mock still returns invalid JSON)
    await expect(agentPanel).toBeVisible()
    await expect(agentPanel.getByText("Loom server not running")).toBeVisible({ timeout: 10000 })
  })

  test("Project Health and Blocking Dependencies panels still render when loom unavailable", async ({ page }) => {
    await setupBeadsMocks(page)
    await setupLoomUnavailable(page)
    await navigateAndWait(page, "/?view=monitor")

    const dashboard = page.getByTestId("monitor-dashboard")
    await expect(dashboard).toBeVisible()

    // All 4 panel headings should be visible
    await expect(page.getByRole("heading", { name: "Agent Activity" })).toBeVisible()
    await expect(page.getByRole("heading", { name: "Work Pipeline" })).toBeVisible()
    await expect(page.getByRole("heading", { name: "Project Health" })).toBeVisible()
    await expect(page.getByRole("heading", { name: "Blocking Dependencies" })).toBeVisible()

    // Project Health panel renders (stats come from loom, so shows defaults when unavailable)
    const healthPanel = page.getByTestId("project-health-panel")
    await expect(healthPanel).toBeVisible()
    await expect(healthPanel.getByText("0%")).toBeVisible()

    // Blocking Dependencies panel renders with content
    const miniGraph = page.getByTestId("mini-dependency-graph")
    await expect(miniGraph).toBeVisible()
  })
})

test.describe("Monitor Dashboard Reconnection & Stale Data", () => {
  test("stale data warning banner appears when disconnected with cached data", async ({ page }) => {
    // Start with loom server available
    await setupBeadsMocks(page)
    await setupLoomAvailable(page)
    await navigateAndWait(page, "/?view=monitor")

    // Wait for agent data to load (agents visible in panel)
    const agentPanel = page.getByTestId("agent-activity-panel")
    await expect(agentPanel).toBeVisible()
    await expect(agentPanel.getByText("active", { exact: true })).toBeVisible({ timeout: 10000 })
    await expect(agentPanel.getByText("idle", { exact: true })).toBeVisible()

    // Switch loom to unavailable (simulate disconnect)
    await switchLoomToUnavailable(page)

    // Wait for next poll to fail — the hook polls every 5000ms
    // Wait for the failed status response
    await page.waitForResponse(
      (res) => res.url().includes("/localhost:9000/api/status"),
      { timeout: 10000 }
    )

    // Wait for React to process the failed fetch and update state
    await page.waitForTimeout(1000)

    // Verify ConnectionBanner is visible with correct content
    const banner = page.getByRole("alert")
    await expect(banner).toBeVisible({ timeout: 5000 })
    await expect(banner).toContainText("Disconnected from loom server")
    await expect(banner).toContainText("Last updated")

    // Verify Retry Now button is present in the banner
    const retryButton = banner.getByRole("button", { name: /retry connection now/i })
    await expect(retryButton).toBeVisible()

    // Verify agent panel still shows cached (stale) data
    await expect(agentPanel.getByText("active", { exact: true })).toBeVisible()
    await expect(agentPanel.getByText("idle", { exact: true })).toBeVisible()
  })

  test("dashboard renders correctly after loom becomes available (mock transition)", async ({ page }) => {
    // Start with loom server UNAVAILABLE
    await setupBeadsMocks(page)
    await setupLoomUnavailable(page)
    await navigateAndWait(page, "/?view=monitor")

    // Wait for dashboard to render
    const dashboard = page.getByTestId("monitor-dashboard")
    await expect(dashboard).toBeVisible()

    // Verify initial degraded state — agent panel shows an empty/error state
    const agentPanel = page.getByTestId("agent-activity-panel")
    await expect(agentPanel).toBeVisible()

    // Should show one of the unavailable states (not the agent summary)
    const notRunningText = agentPanel.getByText("Loom server not running")
    const notAvailableText = agentPanel.getByText("Loom server not available")
    const noAgentsText = agentPanel.getByText("No agents found")

    // Wait for the initial state to render after failed loom fetch
    await page.waitForResponse(
      (res) => res.url().includes("/localhost:9000/api/status"),
      { timeout: 10000 }
    )
    await page.waitForTimeout(1000)

    // At least one empty state message should be visible
    const hasEmptyState =
      (await notRunningText.isVisible().catch(() => false)) ||
      (await notAvailableText.isVisible().catch(() => false)) ||
      (await noAgentsText.isVisible().catch(() => false))
    expect(hasEmptyState).toBeTruthy()

    // Switch loom to available
    await switchLoomToAvailable(page)

    // Click a retry/check connection button to trigger immediate fetch
    // The AgentActivityPanel shows either "Check Connection" or "Retry Connection" button
    const retryButton = agentPanel.getByRole("button").first()
    if (await retryButton.isVisible()) {
      await retryButton.click()
    }

    // Wait for the successful loom status response
    await page.waitForResponse(
      (res) =>
        res.url().includes("/localhost:9000/api/status") && res.status() === 200,
      { timeout: 15000 }
    )

    // Wait for React to process
    await page.waitForTimeout(1000)

    // Verify Agent Activity panel now shows agents with summary
    await expect(agentPanel.getByText("active", { exact: true })).toBeVisible({ timeout: 10000 })
    await expect(agentPanel.getByText("idle", { exact: true })).toBeVisible()

    // Verify Work Pipeline panel shows non-zero stage counts
    const planStage = page.getByTestId("pipeline-stage-plan")
    await expect(planStage).toBeVisible()
    await expect(planStage).toContainText("2")

    const readyStage = page.getByTestId("pipeline-stage-ready")
    await expect(readyStage).toBeVisible()
    await expect(readyStage).toContainText("3")

    // Verify no empty state messages in agent panel
    await expect(notRunningText).not.toBeVisible()
    await expect(notAvailableText).not.toBeVisible()
    await expect(noAgentsText).not.toBeVisible()
  })

  test("WorkPipelinePanel shows zero counts when loom unavailable", async ({ page }) => {
    // Start with loom server unavailable
    await setupBeadsMocks(page)
    await setupLoomUnavailable(page)
    await navigateAndWait(page, "/?view=monitor")

    // Wait for dashboard to render
    const dashboard = page.getByTestId("monitor-dashboard")
    await expect(dashboard).toBeVisible()

    // Wait for the failed loom response to be processed
    await page.waitForResponse(
      (res) => res.url().includes("/localhost:9000/api/status"),
      { timeout: 10000 }
    )
    await page.waitForTimeout(1000)

    // Verify all main pipeline stage counts show "0"
    const planStage = page.getByTestId("pipeline-stage-plan")
    await expect(planStage).toBeVisible()
    await expect(planStage).toContainText("0")

    const readyStage = page.getByTestId("pipeline-stage-ready")
    await expect(readyStage).toBeVisible()
    await expect(readyStage).toContainText("0")

    const inProgressStage = page.getByTestId("pipeline-stage-inProgress")
    await expect(inProgressStage).toBeVisible()
    await expect(inProgressStage).toContainText("0")

    const reviewStage = page.getByTestId("pipeline-stage-review")
    await expect(reviewStage).toBeVisible()
    await expect(reviewStage).toContainText("0")

    // Stages with count 0 should NOT have role="button" (not clickable)
    await expect(planStage).not.toHaveAttribute("role", "button")
    await expect(readyStage).not.toHaveAttribute("role", "button")
    await expect(inProgressStage).not.toHaveAttribute("role", "button")
    await expect(reviewStage).not.toHaveAttribute("role", "button")

    // Stages with count 0 should have data-highlight="false" on count element
    const planCount = planStage.locator("[data-highlight]")
    await expect(planCount).toHaveAttribute("data-highlight", "false")

    const readyCount = readyStage.locator("[data-highlight]")
    await expect(readyCount).toHaveAttribute("data-highlight", "false")

    // Blocked branch should NOT be visible (tasks.blocked = 0)
    await expect(page.getByTestId("pipeline-stage-blocked")).not.toBeVisible()
  })
})

/**
 * Set up mocks with ALL data sources returning empty results.
 * Loom server is available but reports zero agents, zero tasks, zero stats.
 */
async function setupEmptyMocks(
  page: Page,
  options?: {
    graphIssues?: object[]
  }
) {
  const graphIssues = options?.graphIssues ?? []

  // Mock beads backend API - all empty
  await page.route("**/api/ready", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: [] }),
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
      body: JSON.stringify({ success: true, data: graphIssues }),
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
        data: { open: 0, closed: 0, total: 0, completion: 0 },
      }),
    })
  })

  // Mock loom server - available but empty
  await page.route("**/localhost:9000/api/status", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        agents: [],
        tasks: {
          needs_planning: 0,
          ready_to_implement: 0,
          in_progress: 0,
          need_review: 0,
          blocked: 0,
        },
        agent_tasks: {},
        sync: {
          db_synced: true,
          db_last_sync: "2026-01-24T12:00:00Z",
          git_needs_push: 0,
          git_needs_pull: 0,
        },
        stats: {
          open: 0,
          closed: 0,
          total: 0,
          completion: 0,
        },
        timestamp: "2026-01-24T12:00:00Z",
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
        blocked: [],
      }),
    })
  })
}

/**
 * Navigate to monitor view and wait for beads API response (empty mocks version).
 */
async function navigateEmptyAndWait(page: Page) {
  const [response] = await Promise.all([
    page.waitForResponse(
      (res) => res.url().includes("/api/ready") && res.status() === 200
    ),
    page.goto("/?view=monitor"),
  ])
  expect(response.ok()).toBe(true)
}

test.describe("Monitor Dashboard Empty States", () => {
  test("empty agents shows No agents found message", async ({ page }) => {
    await setupEmptyMocks(page)
    await navigateEmptyAndWait(page)

    // Wait for loom status to load
    await page.waitForResponse(
      (res) => res.url().includes("/api/status") && res.status() === 200,
      { timeout: 10000 }
    )
    await page.waitForTimeout(500)

    // Verify agent panel shows empty state
    const agentPanel = page.getByTestId("agent-activity-panel")
    await expect(agentPanel).toBeVisible()
    await expect(agentPanel.getByText("No agents found")).toBeVisible()
  })

  test("all panels show empty state messages when no data", async ({ page }) => {
    await setupEmptyMocks(page)
    await navigateEmptyAndWait(page)

    // Wait for loom APIs to load
    await page.waitForResponse(
      (res) => res.url().includes("/api/status") && res.status() === 200,
      { timeout: 10000 }
    )
    await page.waitForResponse(
      (res) => res.url().includes("/api/tasks") && res.status() === 200,
      { timeout: 10000 }
    )
    await page.waitForTimeout(500)

    // Verify all 4 panel headings render
    await expect(page.getByRole("heading", { name: "Agent Activity" })).toBeVisible()
    await expect(page.getByRole("heading", { name: "Work Pipeline" })).toBeVisible()
    await expect(page.getByRole("heading", { name: "Project Health" })).toBeVisible()
    await expect(page.getByRole("heading", { name: "Blocking Dependencies" })).toBeVisible()

    // AgentActivityPanel: shows "No agents found"
    const agentPanel = page.getByTestId("agent-activity-panel")
    await expect(agentPanel.getByText("No agents found")).toBeVisible()

    // ProjectHealthPanel: shows "No bottlenecks detected" in bottleneck section
    const healthPanel = page.getByTestId("project-health-panel")
    await expect(healthPanel.getByText("No bottlenecks detected")).toBeVisible()

    // MiniDependencyGraph: shows "No blocking dependencies"
    const miniGraph = page.getByTestId("mini-dependency-graph")
    await expect(miniGraph.getByText("No blocking dependencies")).toBeVisible()

    // WorkPipelinePanel: stages render with 0 counts
    await expect(page.getByTestId("pipeline-stage-plan")).toBeVisible()
    await expect(page.getByTestId("pipeline-stage-plan")).toContainText("0")
    await expect(page.getByTestId("pipeline-stage-ready")).toBeVisible()
    await expect(page.getByTestId("pipeline-stage-ready")).toContainText("0")
    await expect(page.getByTestId("pipeline-stage-inProgress")).toBeVisible()
    await expect(page.getByTestId("pipeline-stage-inProgress")).toContainText("0")
    await expect(page.getByTestId("pipeline-stage-review")).toBeVisible()
    await expect(page.getByTestId("pipeline-stage-review")).toContainText("0")
  })

  test("empty graph shows No blocking dependencies with checkmark icon", async ({ page }) => {
    // Provide issues that have NO depends_on relationships
    const issuesWithoutDeps = [
      {
        id: "orphan-1",
        title: "Standalone task",
        status: "open",
        priority: 2,
        issue_type: "task",
        created_at: "2026-01-24T10:00:00Z",
        updated_at: "2026-01-24T10:00:00Z",
        depends_on: [],
      },
      {
        id: "orphan-2",
        title: "Another standalone",
        status: "open",
        priority: 3,
        issue_type: "feature",
        created_at: "2026-01-24T11:00:00Z",
        updated_at: "2026-01-24T11:00:00Z",
        depends_on: [],
      },
    ]
    await setupEmptyMocks(page, { graphIssues: issuesWithoutDeps })
    await navigateEmptyAndWait(page)

    // Wait for graph data to load
    await page.waitForResponse(
      (res) => res.url().includes("/api/issues/graph") && res.status() === 200,
      { timeout: 10000 }
    )
    await page.waitForTimeout(1000)

    // Verify empty state in mini dependency graph
    const miniGraph = page.getByTestId("mini-dependency-graph")
    await expect(miniGraph).toBeVisible()
    await expect(miniGraph.getByText("No blocking dependencies")).toBeVisible()

    // Verify checkmark icon is visible
    await expect(miniGraph.getByText("✓")).toBeVisible()
  })
})
