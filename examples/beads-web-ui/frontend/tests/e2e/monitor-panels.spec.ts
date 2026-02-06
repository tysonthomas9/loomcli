import { test, expect, Page } from "@playwright/test"

/**
 * Mock agents covering all status types for Agent Activity Panel tests.
 */
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
    name: "falcon",
    status: "idle",
    branch: "main",
    task: "",
    ahead: 0,
    behind: 3,
    last_seen: "2026-01-24T11:30:00Z",
  },
  {
    name: "cobalt",
    status: "planning",
    branch: "feature-ui",
    task: "bd-102",
    ahead: 0,
    behind: 0,
    last_seen: "2026-01-24T12:01:00Z",
  },
  {
    name: "ember",
    status: "error",
    branch: "fix-bug",
    task: "bd-103",
    ahead: 1,
    behind: 1,
    last_seen: "2026-01-24T11:00:00Z",
  },
  {
    name: "jade",
    status: "ready",
    branch: "main",
    task: "",
    ahead: 0,
    behind: 0,
    last_seen: "2026-01-24T10:00:00Z",
  },
]

const mockAgentTasks: Record<string, { id: string; title: string; priority: number }> = {
  nova: { id: "bd-101", title: "Implement authentication", priority: 1 },
  cobalt: { id: "bd-102", title: "Design UI components", priority: 2 },
  ember: { id: "bd-103", title: "Fix critical bug", priority: 0 },
}

const mockLoomStatus = {
  agents: mockAgents,
  tasks: {
    needs_planning: 2,
    ready_to_implement: 3,
    in_progress: 1,
    need_review: 1,
    blocked: 0,
  },
  agent_tasks: mockAgentTasks,
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

const mockLoomTasks = {
  needs_planning: [
    { id: "bd-010", title: "Plan new feature", priority: 2 },
    { id: "bd-011", title: "Design API", priority: 1 },
  ],
  ready_to_implement: [
    { id: "bd-020", title: "Implement login", priority: 1 },
  ],
  in_progress: [{ id: "bd-001", title: "Implement feature X", priority: 2 }],
  needs_review: [{ id: "bd-030", title: "Review PR", priority: 2 }],
  blocked: [],
}

/**
 * Set up all API mocks for Agent Activity Panel tests.
 */
async function setupMocks(page: Page) {
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
 * Navigate to monitor view and wait for API response.
 */
async function navigateToMonitor(page: Page) {
  const [response] = await Promise.all([
    page.waitForResponse(
      (res) => res.url().includes("/api/ready") && res.status() === 200
    ),
    page.goto("/?view=monitor"),
  ])
  expect(response.ok()).toBe(true)
}

/**
 * Wait for agents to load in the Agent Activity Panel.
 */
async function waitForAgents(page: Page) {
  const agentPanel = page.getByTestId("agent-activity-panel")
  await expect(agentPanel).toBeVisible()
  // Wait for summary bar to render (indicates agents are loaded)
  await expect(
    agentPanel.getByText("active", { exact: true })
  ).toBeVisible({ timeout: 10000 })
}

test.describe("AgentActivityPanel deep behavior", () => {
  test("agent cards show correct status via data-status attribute", async ({ page }) => {
    await setupMocks(page)
    await navigateToMonitor(page)
    await waitForAgents(page)

    const agentPanel = page.getByTestId("agent-activity-panel")

    // Verify each agent card has the correct data-status attribute
    const expectedStatuses: Record<string, string> = {
      nova: "working",
      falcon: "idle",
      cobalt: "planning",
      ember: "error",
      jade: "ready",
    }

    for (const [name, status] of Object.entries(expectedStatuses)) {
      const card = agentPanel.locator(`[data-status="${status}"]`, {
        hasText: name,
      })
      await expect(card).toBeVisible()
    }

    // Verify the status indicator dot exists within each card
    const cards = agentPanel.locator("[data-status]")
    const cardCount = await cards.count()
    expect(cardCount).toBe(5)
  })

  test("agent cards display status text and task titles", async ({ page }) => {
    await setupMocks(page)
    await navigateToMonitor(page)
    await waitForAgents(page)

    const agentPanel = page.getByTestId("agent-activity-panel")

    // Working agent (nova): shows "Working..." status text
    const novaCard = agentPanel.locator('[data-status="working"]', { hasText: "nova" })
    await expect(novaCard).toContainText("Working")
    // Task title for working agent
    await expect(novaCard).toContainText("Implement authentication")

    // Planning agent (cobalt): shows "Planning..." status text
    const cobaltCard = agentPanel.locator('[data-status="planning"]', { hasText: "cobalt" })
    await expect(cobaltCard).toContainText("Planning")
    await expect(cobaltCard).toContainText("Design UI components")

    // Error agent (ember): shows "Error" status text
    const emberCard = agentPanel.locator('[data-status="error"]', { hasText: "ember" })
    await expect(emberCard).toContainText("Error")
    await expect(emberCard).toContainText("Fix critical bug")

    // Idle agent (falcon): shows "Idle" status text, no task title
    const falconCard = agentPanel.locator('[data-status="idle"]', { hasText: "falcon" })
    await expect(falconCard).toContainText("Idle")
    await expect(falconCard).not.toContainText("Implement")

    // Ready agent (jade): shows "Ready" status text, no task title
    const jadeCard = agentPanel.locator('[data-status="ready"]', { hasText: "jade" })
    await expect(jadeCard).toContainText("Ready")
  })

  test("agent cards show git sync indicators (ahead/behind)", async ({ page }) => {
    await setupMocks(page)
    await navigateToMonitor(page)
    await waitForAgents(page)

    const agentPanel = page.getByTestId("agent-activity-panel")

    // nova (ahead: 2, behind: 0): shows "+2", no "-" indicator
    const novaCard = agentPanel.locator('[data-status="working"]', { hasText: "nova" })
    await expect(novaCard.getByText("+2")).toBeVisible()
    await expect(novaCard.getByTitle("2 commits ahead")).toBeVisible()
    // Verify branch name displayed
    await expect(novaCard).toContainText("feature-auth")

    // falcon (ahead: 0, behind: 3): shows "-3", no "+" indicator
    const falconCard = agentPanel.locator('[data-status="idle"]', { hasText: "falcon" })
    await expect(falconCard.getByText("-3")).toBeVisible()
    await expect(falconCard.getByTitle("3 commits behind")).toBeVisible()
    await expect(falconCard).toContainText("main")

    // ember (ahead: 1, behind: 1): shows both "+1" and "-1"
    const emberCard = agentPanel.locator('[data-status="error"]', { hasText: "ember" })
    await expect(emberCard.getByText("+1")).toBeVisible()
    await expect(emberCard.getByText("-1")).toBeVisible()
    await expect(emberCard.getByTitle("1 commits ahead")).toBeVisible()
    await expect(emberCard.getByTitle("1 commits behind")).toBeVisible()
    await expect(emberCard).toContainText("fix-bug")

    // jade (ahead: 0, behind: 0): no sync indicators
    const jadeCard = agentPanel.locator('[data-status="ready"]', { hasText: "jade" })
    // Should not contain any +/- sync text (only "Ready" and "jade" and "main")
    await expect(jadeCard.locator("[title*='commits ahead']")).toHaveCount(0)
    await expect(jadeCard.locator("[title*='commits behind']")).toHaveCount(0)
  })

  test("agent summary bar shows correct counts", async ({ page }) => {
    await setupMocks(page)
    await navigateToMonitor(page)
    await waitForAgents(page)

    const agentPanel = page.getByTestId("agent-activity-panel")

    // Verify summary bar has correct role and aria-label
    const summary = agentPanel.locator('[role="status"][aria-label="Agent activity summary"]')
    await expect(summary).toBeVisible()

    // active: nova (working) + cobalt (planning) = 2
    const activeItem = summary.locator('[data-type="active"]')
    await expect(activeItem).toBeVisible()
    await expect(activeItem).toContainText("2")

    // idle: falcon (idle) + jade (ready) = 2
    const idleItem = summary.locator('[data-type="idle"]')
    await expect(idleItem).toBeVisible()
    await expect(idleItem).toContainText("2")

    // error: ember (error) = 1
    const errorItem = summary.locator('[data-type="error"]')
    await expect(errorItem).toBeVisible()
    await expect(errorItem).toContainText("1")

    // needsPush (sync): nova (ahead=2) + ember (ahead=1) = 2
    const syncItem = summary.locator('[data-type="sync"]')
    await expect(syncItem).toBeVisible()
    await expect(syncItem).toContainText("2")
  })

  test("agent cards have interactive attributes and keyboard support", async ({ page }) => {
    await setupMocks(page)
    await navigateToMonitor(page)
    await waitForAgents(page)

    const agentPanel = page.getByTestId("agent-activity-panel")

    // Verify agent cards have role="button" (since MonitorDashboard passes onAgentClick)
    const cards = agentPanel.locator('[data-status][role="button"]')
    const cardCount = await cards.count()
    expect(cardCount).toBe(5)

    // Verify cards have tabIndex="0" for keyboard accessibility
    for (let i = 0; i < cardCount; i++) {
      await expect(cards.nth(i)).toHaveAttribute("tabindex", "0")
    }

    // Verify keyboard focus works: tab to the first card
    const firstCard = cards.first()
    await firstCard.focus()
    await expect(firstCard).toBeFocused()

    // Verify clicking a card doesn't cause errors (card is clickable)
    await firstCard.click()
  })
})

/**
 * Mock blocked issues with a shared blocker to trigger bottleneck detection.
 * Two issues are both blocked by "bottleneck-x", making it a bottleneck.
 */
const mockBlockedWithBottleneck = {
  success: true,
  data: [
    {
      id: "blocked-1",
      title: "Blocked task 1",
      status: "open",
      priority: 1,
      issue_type: "task",
      created_at: "2026-01-24T10:00:00Z",
      updated_at: "2026-01-24T10:00:00Z",
      blocked_by: ["bottleneck-x"],
    },
    {
      id: "blocked-2",
      title: "Blocked task 2",
      status: "open",
      priority: 2,
      issue_type: "task",
      created_at: "2026-01-24T11:00:00Z",
      updated_at: "2026-01-24T11:00:00Z",
      blocked_by: ["bottleneck-x"],
    },
    {
      id: "blocked-3",
      title: "Blocked task 3",
      status: "open",
      priority: 3,
      issue_type: "task",
      created_at: "2026-01-24T12:00:00Z",
      updated_at: "2026-01-24T12:00:00Z",
      blocked_by: ["other-blocker"],
    },
  ],
}

/**
 * Set up mocks with custom blocked issues and optional custom stats.
 */
async function setupHealthPanelMocks(
  page: Page,
  options?: {
    blockedResponse?: object
    stats?: { open: number; closed: number; total: number; completion: number }
  }
) {
  const stats = options?.stats ?? { open: 10, closed: 5, total: 15, completion: 33 }
  const blockedResponse = options?.blockedResponse ?? { success: true, data: [] }

  const loomStatus = {
    ...mockLoomStatus,
    stats,
  }

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
      body: JSON.stringify(blockedResponse),
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
      body: JSON.stringify({ success: true, data: stats }),
    })
  })

  await page.route("**/localhost:9000/api/status", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(loomStatus),
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
 * Navigate to monitor view and wait for loom status to load.
 */
async function navigateAndWaitForHealth(page: Page) {
  const [readyRes] = await Promise.all([
    page.waitForResponse(
      (res) => res.url().includes("/api/ready") && res.status() === 200
    ),
    page.goto("/?view=monitor"),
  ])
  expect(readyRes.ok()).toBe(true)

  // Wait for loom status to load (drives ProjectHealthPanel stats)
  await page.waitForResponse(
    (res) => res.url().includes("/api/status") && res.status() === 200,
    { timeout: 10000 }
  )
  await page.waitForTimeout(500)
}

test.describe("ProjectHealthPanel deep behavior", () => {
  test("progress bar shows correct completion percentage", async ({ page }) => {
    await setupHealthPanelMocks(page, {
      stats: { open: 10, closed: 5, total: 15, completion: 33 },
    })
    await navigateAndWaitForHealth(page)

    const healthPanel = page.getByTestId("project-health-panel")
    await expect(healthPanel).toBeVisible()

    // Verify progress bar aria attributes
    const progressBar = healthPanel.getByRole("progressbar", { name: /project completion/i })
    await expect(progressBar).toBeVisible()
    await expect(progressBar).toHaveAttribute("aria-valuenow", "33")
    await expect(progressBar).toHaveAttribute("aria-valuemin", "0")
    await expect(progressBar).toHaveAttribute("aria-valuemax", "100")

    // Verify percentage text
    await expect(healthPanel.getByText("33%")).toBeVisible()
  })

  test("issue counts display open, closed, and total correctly", async ({ page }) => {
    await setupHealthPanelMocks(page, {
      stats: { open: 10, closed: 5, total: 15, completion: 33 },
    })
    await navigateAndWaitForHealth(page)

    const healthPanel = page.getByTestId("project-health-panel")
    await expect(healthPanel).toBeVisible()

    // Verify each count appears with its label using the countItem div structure:
    // <div class="countItem"><span class="countValue">N</span><span class="countLabel">Label</span></div>
    // Locate each label, then find the sibling count value in the same parent
    const openItem = healthPanel.locator("div", { hasText: /^10Open$/ })
    await expect(openItem).toBeVisible()

    const closedItem = healthPanel.locator("div", { hasText: /^5Closed$/ })
    await expect(closedItem).toBeVisible()

    const totalItem = healthPanel.locator("div", { hasText: /^15Total$/ })
    await expect(totalItem).toBeVisible()
  })

  test("bottleneck section shows when blocked issues share a common blocker", async ({ page }) => {
    await setupHealthPanelMocks(page, {
      blockedResponse: mockBlockedWithBottleneck,
    })
    await navigateAndWaitForHealth(page)

    const healthPanel = page.getByTestId("project-health-panel")
    await expect(healthPanel).toBeVisible()

    // Verify bottleneck ID is visible
    await expect(healthPanel.getByText("bottleneck-x")).toBeVisible()

    // Verify blocking count text
    await expect(healthPanel.getByText("blocks 2")).toBeVisible()

    // Verify bottleneck count badge in heading (1 bottleneck)
    await expect(healthPanel.getByText("(1)")).toBeVisible()

    // "other-blocker" only blocks 1 issue, so it should NOT appear as a bottleneck
    await expect(healthPanel.getByText("other-blocker")).not.toBeVisible()
  })

  test("no bottlenecks message when none exist", async ({ page }) => {
    await setupHealthPanelMocks(page, {
      blockedResponse: { success: true, data: [] },
    })
    await navigateAndWaitForHealth(page)

    const healthPanel = page.getByTestId("project-health-panel")
    await expect(healthPanel).toBeVisible()

    // Verify "No bottlenecks detected" message
    await expect(healthPanel.getByText("No bottlenecks detected")).toBeVisible()

    // Verify no count badge in heading - the component only renders a span with "(N)" when bottlenecks > 0
    const bottleneckHeading = healthPanel.getByRole("heading", { name: "Bottlenecks" })
    await expect(bottleneckHeading).toBeVisible()
    // Heading should contain only "Bottlenecks" text, no badge
    await expect(bottleneckHeading).toHaveText("Bottlenecks")
  })
})

/**
 * Mock graph API issues with blocking dependencies for MiniDependencyGraph tests.
 * test-2 depends on test-1 via "blocks" dependency type.
 * Uses the graph API format: { issues: [...] } with dependencies as { depends_on_id, type }.
 */
const mockGraphIssuesWithDeps = [
  {
    id: "test-1",
    title: "Feature A",
    status: "open",
    priority: 2,
    issue_type: "feature",
    created_at: "2026-01-24T10:00:00Z",
    updated_at: "2026-01-24T10:00:00Z",
  },
  {
    id: "test-2",
    title: "Task blocked by Feature A",
    status: "open",
    priority: 1,
    issue_type: "task",
    created_at: "2026-01-24T11:00:00Z",
    updated_at: "2026-01-24T11:00:00Z",
    dependencies: [{ depends_on_id: "test-1", type: "blocks" }],
  },
]

/**
 * Empty graph API issues for empty state test.
 * When no issues exist, the MiniDependencyGraph shows "No blocking dependencies".
 */
const mockGraphIssuesEmpty: typeof mockGraphIssuesWithDeps = []

/**
 * Set up mocks for MiniDependencyGraph tests with customizable graph data.
 * The graph API uses a different response format: { issues: [...] } with
 * dependencies as { depends_on_id, type } arrays.
 */
async function setupGraphMocks(
  page: Page,
  options: {
    graphIssues: typeof mockGraphIssuesWithDeps
    hasBlocked?: boolean
  }
) {
  const { graphIssues, hasBlocked = false } = options

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
      body: JSON.stringify({
        success: true,
        data: hasBlocked
          ? [
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
            ]
          : [],
      }),
    })
  })

  // Graph API returns { success, issues: [...] } format
  await page.route("**/api/issues/graph", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, issues: graphIssues }),
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
 * Navigate to monitor view and wait for graph data to load.
 */
async function navigateAndWaitForGraph(page: Page) {
  const [readyRes] = await Promise.all([
    page.waitForResponse(
      (res) => res.url().includes("/api/ready") && res.status() === 200
    ),
    page.goto("/?view=monitor"),
  ])
  expect(readyRes.ok()).toBe(true)

  // Wait for graph data to load
  await page.waitForResponse(
    (res) => res.url().includes("/api/issues/graph") && res.status() === 200,
    { timeout: 10000 }
  )
  // Allow React Flow to render and layout
  await page.waitForTimeout(1000)
}

test.describe("MiniDependencyGraph deep behavior", () => {
  test("renders nodes from blocking dependencies", async ({ page }) => {
    await setupGraphMocks(page, { graphIssues: mockGraphIssuesWithDeps, hasBlocked: true })
    await navigateAndWaitForGraph(page)

    const miniGraph = page.getByTestId("mini-dependency-graph")
    await expect(miniGraph).toBeVisible()

    // React Flow renders nodes with .react-flow__node CSS class
    const nodes = miniGraph.locator(".react-flow__node")
    await expect(nodes.first()).toBeVisible({ timeout: 10000 })

    // Should have at least 1 node rendered from the blocking dependency
    const nodeCount = await nodes.count()
    expect(nodeCount).toBeGreaterThanOrEqual(1)

    // Verify the empty state is NOT shown
    await expect(miniGraph.getByText("No blocking dependencies")).not.toBeVisible()
  })

  test("shows empty state when no blocking dependencies", async ({ page }) => {
    await setupGraphMocks(page, { graphIssues: mockGraphIssuesEmpty })
    await navigateAndWaitForGraph(page)

    const miniGraph = page.getByTestId("mini-dependency-graph")
    await expect(miniGraph).toBeVisible()

    // Verify empty state text is displayed
    await expect(miniGraph.getByText("No blocking dependencies")).toBeVisible()

    // No React Flow nodes should be rendered
    const nodes = miniGraph.locator(".react-flow__node")
    await expect(nodes).toHaveCount(0)
  })

  test("expand button is keyboard accessible and navigates to graph view", async ({ page }) => {
    await setupGraphMocks(page, { graphIssues: mockGraphIssuesWithDeps, hasBlocked: true })
    await navigateAndWaitForGraph(page)

    const miniGraph = page.getByTestId("mini-dependency-graph")
    await expect(miniGraph).toBeVisible()

    // Find the expand button
    const expandButton = page.getByRole("button", { name: "Expand to full graph view" })
    await expect(expandButton).toBeVisible()

    // Verify aria-label
    await expect(expandButton).toHaveAttribute("aria-label", "Expand to full graph view")

    // Focus the button and verify it receives focus
    await expandButton.focus()
    await expect(expandButton).toBeFocused()

    // Press Enter to activate
    await page.keyboard.press("Enter")

    // Verify URL changed to graph view
    expect(page.url()).toContain("view=graph")

    // Verify graph view is displayed
    const graphTab = page.getByTestId("view-tab-graph")
    await expect(graphTab).toHaveAttribute("aria-selected", "true")
  })
})
