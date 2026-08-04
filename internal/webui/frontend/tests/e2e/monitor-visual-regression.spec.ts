import { test, expect, Page } from "@playwright/test"

/**
 * Workspace fixture for Monitor Dashboard visual regression tests. Shape
 * matches the WorkspaceData interface. WorkspaceLayout calls
 * fetchWorkspaceApi() before rendering children, so the mock must return
 * an object with a non-empty id under `{ success: true, data: ... }`.
 */
const mockWorkspaceData = {
  id: "default",
  name: "default",
  path: "/test",
  repos: [],
  groups: [],
  agents: [],
  workspaces: [
    {
      id: "default",
      name: "default",
      path: "/test",
      active: true,
      repo_count: 0,
      is_default: true,
    },
  ],
  workspace_order: ["default"],
  default_workspace: "default",
}

/**
 * Mock issues for Monitor Dashboard visual regression tests.
 * Consistent data ensures deterministic screenshots.
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
 * All 4 agent states for visual regression coverage:
 * working, idle, error, planning+needs-push
 */
const mockAllAgents = [
  {
    name: "dev1",
    status: "working",
    branch: "feature-x",
    task: "loom-001",
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
  {
    name: "dev3",
    status: "error",
    branch: "bugfix-y",
    task: "loom-003",
    ahead: 0,
    behind: 0,
    last_seen: "2026-01-24T11:00:00Z",
  },
  {
    name: "dev4",
    status: "planning",
    branch: "feature-z",
    task: "loom-004",
    ahead: 2,
    behind: 0,
    last_seen: "2026-01-24T12:05:00Z",
  },
]

const mockAllAgentTasks: Record<string, { id: string; title: string; priority: number }> = {
  dev1: { id: "loom-001", title: "Implement feature X", priority: 2 },
  dev3: { id: "loom-003", title: "Fix critical bug in authentication module", priority: 0 },
  dev4: { id: "loom-004", title: "Plan architecture redesign for scalability improvements", priority: 1 },
}

const mockLoomStatus = {
  agents: mockAllAgents,
  tasks: {
    needs_planning: 2,
    ready_to_implement: 3,
    in_progress: 1,
    need_review: 1,
    blocked: 2,
  },
  agent_tasks: mockAllAgentTasks,
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

const mockLoomTasks = {
  needs_planning: [
    { id: "loom-010", title: "Plan new feature", priority: 2 },
    { id: "loom-011", title: "Design API", priority: 1 },
  ],
  ready_to_implement: [
    { id: "loom-020", title: "Implement login", priority: 1 },
    { id: "loom-021", title: "Add tests", priority: 2 },
    { id: "loom-022", title: "Fix bug", priority: 3 },
  ],
  in_progress: [{ id: "loom-001", title: "Implement feature X", priority: 2 }],
  needs_review: [{ id: "loom-030", title: "Review PR", priority: 2 }],
  blocked: [
    { id: "loom-040", title: "Blocked task A", priority: 1 },
    { id: "loom-041", title: "Blocked task B", priority: 2 },
  ],
}

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

const mockBlockedWithBottlenecks = {
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
      blocked_by_details: [{ id: "test-1", title: "Feature A", priority: 2 }],
    },
    {
      id: "test-4",
      title: "Another blocked task",
      status: "open",
      priority: 2,
      issue_type: "task",
      created_at: "2026-01-24T13:00:00Z",
      updated_at: "2026-01-24T13:00:00Z",
      blocked_by: ["test-1"],
      blocked_by_details: [{ id: "test-1", title: "Feature A", priority: 2 }],
    },
    {
      id: "test-5",
      title: "Third blocked task",
      status: "open",
      priority: 3,
      issue_type: "task",
      created_at: "2026-01-24T14:00:00Z",
      updated_at: "2026-01-24T14:00:00Z",
      blocked_by: ["test-1", "test-6"],
      blocked_by_details: [
        { id: "test-1", title: "Feature A", priority: 2 },
        { id: "test-6", title: "Critical Infrastructure", priority: 0 },
      ],
    },
  ],
}

const emptyLoomStatus = {
  agents: [],
  tasks: { needs_planning: 0, ready_to_implement: 0, in_progress: 0, need_review: 0, blocked: 0 },
  agent_tasks: {},
  sync: { db_synced: true, db_last_sync: "2026-01-24T12:00:00Z", git_needs_push: 0, git_needs_pull: 0 },
  stats: { open: 0, closed: 0, total: 0, completion: 0 },
  timestamp: "2026-01-24T12:00:00Z",
}

const emptyLoomTasks = {
  needs_planning: [],
  ready_to_implement: [],
  in_progress: [],
  needs_review: [],
  blocked: [],
}

/**
 * Set up all API mocks for Monitor Dashboard visual regression tests.
 *
 * Uses workspace-scoped routing: the monitor view lives at
 * /ws/default/monitor, and issue/stats/blocked endpoints are served under
 * /api/workspaces/:id/.... The loom /api/monitor/* endpoints are NOT
 * workspace-scoped, so those mocks remain un-scoped.
 */
async function setupMocks(
  page: Page,
  options?: {
    loomServerAvailable?: boolean
    emptyAgents?: boolean
    customAgents?: typeof mockAllAgents
    customAgentTasks?: typeof mockAllAgentTasks
    customBlockedIssues?: typeof mockBlockedIssues
    emptyStats?: boolean
  }
) {
  const { loomServerAvailable = true, emptyAgents = false, customAgents, customAgentTasks, customBlockedIssues, emptyStats = false } = options ?? {}

  // Neutralize AbortController signals in fetch. React StrictMode (dev mode)
  // double-fires effects; the cleanup aborts in-flight fetches before they
  // reach the network. openapi-fetch bakes the signal into the Request
  // object, so stripping `init.signal` is not enough — we must also
  // reconstruct Request inputs without their signal. Otherwise page.route
  // never sees the workspace-scoped issue requests because they're aborted
  // pre-dispatch.
  //
  // Note: the openapi-fetch middleware may have attached a `_timeoutController`
  // to the incoming Request for its onResponse cleanup. We preserve that
  // reference on the reconstructed Request so the middleware's timeout
  // cleanup still runs and doesn't leak 30s timers.
  await page.addInitScript(() => {
    const origFetch = window.fetch
    window.fetch = function (input: RequestInfo | URL, init?: RequestInit) {
      const strippedInit: RequestInit = init ? { ...init } : {}
      if ("signal" in strippedInit) delete strippedInit.signal
      if (input instanceof Request) {
        const req = input
        const newInit: RequestInit = {
          method: req.method,
          headers: req.headers,
          credentials: req.credentials,
          cache: req.cache,
          redirect: req.redirect,
          referrer: req.referrer,
          referrerPolicy: req.referrerPolicy,
          integrity: req.integrity,
          keepalive: req.keepalive,
        }
        const preserveTimeout = (target: Request) => {
          const tc = (req as unknown as { _timeoutController?: unknown })
            ._timeoutController
          if (tc) {
            ;(target as unknown as { _timeoutController: unknown })._timeoutController =
              tc
          }
        }
        if (req.method !== "GET" && req.method !== "HEAD") {
          return req
            .clone()
            .blob()
            .then((blob) => {
              const newReq = new Request(req.url, { ...newInit, body: blob })
              preserveTimeout(newReq)
              return origFetch.call(this, newReq, {})
            })
        }
        const newReq = new Request(req.url, newInit)
        preserveTimeout(newReq)
        return origFetch.call(this, newReq, {})
      }
      return origFetch.call(this, input, strippedInit)
    }
  })

  // Mock app config endpoint (boot process requires this before rendering)
  await page.route("**/api/config", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ mode: "open" }),
    })
  })

  // Mock auth token endpoint (required before any API call)
  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-token-monitor" }),
    })
  })

  // Mock global health endpoint (App shell fetches on mount)
  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", daemon: true }),
    })
  })

  // Mock global backend config endpoint
  await page.route("**/api/workspaces/*/config/backend", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: {
          backend: "shell",
          source: "default",
          available: ["shell"],
          agents: [],
        },
      }),
    })
  })

  // Mock all /api/workspaces/* endpoints. Dispatches on pathname so a single
  // handler covers workspace metadata, ready/issues, stats, blocked, graph,
  // and SSE abort — all workspace-scoped.
  await page.route("**/api/workspaces/**", async (route) => {
    const url = new URL(route.request().url())
    const pathname = url.pathname

    // SSE events — abort so we don't hang waitForLoadState("networkidle")
    if (/\/api\/workspaces\/[^/]+\/events/.test(pathname)) {
      await route.abort()
      return
    }

    // /api/workspaces/{id}/ready — monitor view hits this via getReadyIssues
    if (/\/api\/workspaces\/[^/]+\/ready$/.test(pathname)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: emptyStats ? [] : mockIssues }),
      })
      return
    }

    // /api/workspaces/{id}/stats
    if (/\/api\/workspaces\/[^/]+\/stats$/.test(pathname)) {
      const statsData = emptyStats
        ? { open: 0, closed: 0, total: 0, completion: 0 }
        : { open: 10, closed: 5, total: 15, completion: 33 }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: statsData }),
      })
      return
    }

    // /api/workspaces/{id}/blocked
    if (/\/api\/workspaces\/[^/]+\/blocked$/.test(pathname)) {
      const blockedData = emptyStats
        ? { success: true, data: [] }
        : (customBlockedIssues ?? mockBlockedIssues)
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(blockedData),
      })
      return
    }

    // /api/workspaces/{id}/issues/graph
    if (/\/api\/workspaces\/[^/]+\/issues\/graph$/.test(pathname)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: emptyStats ? [] : mockIssues }),
      })
      return
    }

    // /api/workspaces/{id}/issues — fallback kanban-mode requests
    if (/\/api\/workspaces\/[^/]+\/issues$/.test(pathname)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: emptyStats ? [] : mockIssues }),
      })
      return
    }

    // /api/workspaces/{id} — workspace metadata. Must return an object with
    // a non-empty id, otherwise WorkspaceLayout redirects to "/" and loops.
    if (/^\/api\/workspaces\/[^/]+\/?$/.test(pathname)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: mockWorkspaceData }),
      })
      return
    }

    // Anything else under /api/workspaces/* — return empty success.
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: [] }),
    })
  })

  // Mock monitor server API (/api/monitor/*)
  if (loomServerAvailable) {
    await page.route("**/api/monitor/status", async (route) => {
      if (emptyStats) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(emptyLoomStatus),
        })
        return
      }
      const agents = emptyAgents ? [] : (customAgents ?? mockAllAgents)
      const agentTasks = emptyAgents ? {} : (customAgentTasks ?? mockAllAgentTasks)
      const status = { ...mockLoomStatus, agents, agent_tasks: agentTasks }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(status),
      })
    })

    await page.route("**/api/monitor/tasks", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(emptyStats ? emptyLoomTasks : mockLoomTasks),
      })
    })

    // Mock /api/monitor/agents - returns { agents: [...] }
    await page.route("**/api/monitor/agents", async (route) => {
      const agents = (emptyAgents || emptyStats) ? [] : (customAgents ?? mockAllAgents)
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ agents }),
      })
    })
  } else {
    await page.route("**/api/monitor/status", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: "invalid json{",
      })
    })
    await page.route("**/api/monitor/tasks", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: "invalid json{",
      })
    })
    await page.route("**/api/monitor/agents", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: "invalid json{",
      })
    })
  }
}

/**
 * Navigate to monitor view and wait for API responses.
 */
async function navigateAndWait(page: Page) {
  const [response] = await Promise.all([
    page.waitForResponse(
      (res) =>
        res.url().includes("/api/workspaces/") &&
        res.url().includes("/ready") &&
        res.status() === 200
    ),
    page.goto("/ws/default/monitor"),
  ])
  expect(response.ok()).toBe(true)
}

/**
 * Wait for content to stabilize before taking a screenshot.
 */
async function waitForStableContent(page: Page) {
  await page.waitForLoadState("networkidle")
  await page.waitForTimeout(100)
}

test.describe("Visual Regression - Monitor Dashboard Layout", () => {
  test.describe("default vertical stack at 1280x720", () => {
    test.use({ viewport: { width: 1280, height: 720 } })

    test("default vertical stack layout", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      // Wait for dashboard to render (App shows loading skeleton while useIssues is loading,
      // so we need a longer timeout to allow the skeleton to finish before MonitorDashboard mounts)
      const dashboard = page.getByTestId("monitor-dashboard")
      await expect(dashboard).toBeVisible({ timeout: 10000 })

      // Wait for loom API responses so panels are populated
      await page.waitForResponse(
        (res) => res.url().includes("/api/monitor/status") && res.status() === 200,
        { timeout: 10000 }
      )
      await page.waitForResponse(
        (res) => res.url().includes("/api/monitor/tasks") && res.status() === 200,
        { timeout: 10000 }
      )

      await waitForStableContent(page)

      // Verify both panel headings visible before screenshot (2-panel layout)
      await expect(
        page.getByRole("heading", { name: "Project Health" })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "Agent Activity" })
      ).toBeVisible()

      await expect(page).toHaveScreenshot("monitor-vertical-stack.png")
    })
  })

  // NOTE: Connection banner visual regression test is in the "Degradation Scenarios"
  // section below. It uses the connect-then-disconnect pattern from monitor-degradation.spec.ts
  // to trigger the banner (load data first, then switch loom to unavailable).

  // SKIPPED: Monitor view does not render at smaller viewports in the current UI
  // The NavRail does not expose the Monitor button below desktop widths
  test.describe.skip("responsive layout at 1024px", () => {
    test.use({ viewport: { width: 1024, height: 768 } })

    test("tablet layout at 1024px", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      const dashboard = page.getByTestId("monitor-dashboard")
      await expect(dashboard).toBeVisible()

      // Wait for loom API responses
      await page.waitForResponse(
        (res) => res.url().includes("/api/monitor/status") && res.status() === 200,
        { timeout: 10000 }
      )
      await page.waitForResponse(
        (res) => res.url().includes("/api/monitor/tasks") && res.status() === 200,
        { timeout: 10000 }
      )

      await page.waitForTimeout(500)
      await waitForStableContent(page)

      // Verify both panels visible (2-panel layout)
      await expect(
        page.getByRole("heading", { name: "Project Health" })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "Agent Activity" })
      ).toBeVisible()

      await expect(page).toHaveScreenshot("monitor-responsive-1024.png")
    })
  })

  // SKIPPED: Monitor view does not render at smaller viewports in the current UI
  test.describe.skip("responsive layout at 768px", () => {
    test.use({ viewport: { width: 768, height: 1024 } })

    test("mobile layout at 768px", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      const dashboard = page.getByTestId("monitor-dashboard")
      await expect(dashboard).toBeVisible()

      // Wait for loom API responses
      await page.waitForResponse(
        (res) => res.url().includes("/api/monitor/status") && res.status() === 200,
        { timeout: 10000 }
      )
      await page.waitForResponse(
        (res) => res.url().includes("/api/monitor/tasks") && res.status() === 200,
        { timeout: 10000 }
      )

      await page.waitForTimeout(500)
      await waitForStableContent(page)

      // Verify both panels visible (stacked vertically)
      await expect(
        page.getByRole("heading", { name: "Project Health" })
      ).toBeVisible()
      await expect(
        page.getByRole("heading", { name: "Agent Activity" })
      ).toBeVisible()

      await expect(page).toHaveScreenshot("monitor-responsive-768.png", {
        fullPage: true,
      })
    })
  })
})

test.describe("Visual Regression - Agent Activity Panel", () => {
  test.use({ viewport: { width: 1280, height: 720 } })

  test("multiple agent states with summary", async ({ page }) => {
    await setupMocks(page)
    await navigateAndWait(page)

    // Wait for both loom APIs to load
    await page.waitForResponse(
      (res) => res.url().includes("/api/monitor/status") && res.status() === 200,
      { timeout: 10000 }
    )
    await page.waitForResponse(
      (res) => res.url().includes("/api/monitor/tasks") && res.status() === 200,
      { timeout: 10000 }
    )
    await page.waitForTimeout(500)
    await waitForStableContent(page)

    // Verify summary bar shows all state categories
    const agentPanel = page.getByTestId("agent-activity-panel")
    await expect(agentPanel).toBeVisible()
    await expect(agentPanel.getByText("active", { exact: true })).toBeVisible()
    await expect(agentPanel.getByText("idle", { exact: true })).toBeVisible()
    await expect(agentPanel.getByText("error", { exact: true })).toBeVisible()
    await expect(agentPanel.getByText("need push", { exact: true })).toBeVisible()

    await expect(page).toHaveScreenshot(
      "monitor-agent-activity-multiple-states.png"
    )
  })

  test("no agents found state", async ({ page }) => {
    await setupMocks(page, { emptyAgents: true })
    await navigateAndWait(page)

    await page.waitForResponse(
      (res) => res.url().includes("/api/monitor/status") && res.status() === 200,
      { timeout: 10000 }
    )
    await page.waitForResponse(
      (res) => res.url().includes("/api/monitor/tasks") && res.status() === 200,
      { timeout: 10000 }
    )
    await page.waitForTimeout(500)
    await waitForStableContent(page)

    const agentPanel = page.getByTestId("agent-activity-panel")
    await expect(agentPanel).toBeVisible()
    await expect(agentPanel.getByText("No agents found")).toBeVisible()

    await expect(page).toHaveScreenshot(
      "monitor-agent-activity-no-agents.png"
    )
  })

  test("loom server unavailable state", async ({ page }) => {
    await setupMocks(page, { loomServerAvailable: false })
    await navigateAndWait(page)

    // With invalid-JSON mocks, the agent store bails on the first failure
    // and does not poll again, so waitForResponse for /api/monitor/status
    // here is racy — the only response fires during navigateAndWait. Wait
    // for the rendered "Loom server not running" state instead, which is
    // deterministic once the failure lands.
    const agentPanel = page.getByTestId("agent-activity-panel")
    await expect(agentPanel).toBeVisible({ timeout: 10000 })
    await expect(agentPanel.getByText("Loom server not running")).toBeVisible({
      timeout: 10000,
    })
    await waitForStableContent(page)

    await expect(page).toHaveScreenshot(
      "monitor-agent-activity-loom-unavailable.png"
    )
  })

  test("agent cards with task details", async ({ page }) => {
    // Use only agents with task assignments: dev1 (working) and dev4 (planning+ahead)
    const taskAgents = [mockAllAgents[0], mockAllAgents[3]]
    const taskAgentTasks = {
      dev1: mockAllAgentTasks.dev1,
      dev4: mockAllAgentTasks.dev4,
    }

    await setupMocks(page, {
      customAgents: taskAgents,
      customAgentTasks: taskAgentTasks,
    })
    await navigateAndWait(page)

    await page.waitForResponse(
      (res) => res.url().includes("/api/monitor/status") && res.status() === 200,
      { timeout: 10000 }
    )
    await page.waitForResponse(
      (res) => res.url().includes("/api/monitor/tasks") && res.status() === 200,
      { timeout: 10000 }
    )
    await page.waitForTimeout(500)
    await waitForStableContent(page)

    const agentPanel = page.getByTestId("agent-activity-panel")
    await expect(agentPanel).toBeVisible()

    // Verify task titles appear in agent card title attributes (used as tooltips)
    await expect(agentPanel.locator('[title="Implement feature X"]')).toBeVisible()
    await expect(
      agentPanel.locator('[title="Plan architecture redesign for scalability improvements"]')
    ).toBeVisible()

    await expect(page).toHaveScreenshot(
      "monitor-agent-cards-with-tasks.png"
    )
  })
})


test.describe("Visual Regression - Project Health Panel", () => {
  test.use({ viewport: { width: 1280, height: 720 } })

  test("progress bar with bottleneck warnings", async ({ page }) => {
    await setupMocks(page, { customBlockedIssues: mockBlockedWithBottlenecks })
    await navigateAndWait(page)

    await page.waitForResponse(
      (res) => res.url().includes("/api/monitor/status") && res.status() === 200,
      { timeout: 10000 }
    )
    await page.waitForResponse(
      (res) => res.url().includes("/api/monitor/tasks") && res.status() === 200,
      { timeout: 10000 }
    )
    await page.waitForTimeout(500)
    await waitForStableContent(page)

    const healthPanel = page.getByTestId("project-health-panel")
    await expect(healthPanel).toBeVisible()

    // Verify bottleneck list renders (test-1 blocks 3 issues)
    await expect(healthPanel.getByText("blocks 3")).toBeVisible()

    await expect(healthPanel).toHaveScreenshot(
      "monitor-health-progress-bottlenecks.png"
    )
  })

  test("empty state with no bottlenecks", async ({ page }) => {
    await setupMocks(page, {
      emptyStats: true,
    })
    await navigateAndWait(page)

    await page.waitForResponse(
      (res) => res.url().includes("/api/monitor/status") && res.status() === 200,
      { timeout: 10000 }
    )
    await page.waitForResponse(
      (res) => res.url().includes("/api/monitor/tasks") && res.status() === 200,
      { timeout: 10000 }
    )
    await page.waitForTimeout(500)
    await waitForStableContent(page)

    const healthPanel = page.getByTestId("project-health-panel")
    await expect(healthPanel).toBeVisible()

    // Verify empty state text
    await expect(healthPanel.getByText("No bottlenecks detected")).toBeVisible()

    await expect(healthPanel).toHaveScreenshot(
      "monitor-health-empty.png"
    )
  })
})


test.describe("Visual Regression - Interactions", () => {
  test.use({ viewport: { width: 1280, height: 720 } })

  test("bottleneck button hover state", async ({ page }) => {
    await setupMocks(page, { customBlockedIssues: mockBlockedWithBottlenecks })
    await navigateAndWait(page)

    await page.waitForResponse(
      (res) => res.url().includes("/api/monitor/status") && res.status() === 200,
      { timeout: 10000 }
    )
    await page.waitForResponse(
      (res) => res.url().includes("/api/monitor/tasks") && res.status() === 200,
      { timeout: 10000 }
    )
    await page.waitForTimeout(500)
    await waitForStableContent(page)

    const healthPanel = page.getByTestId("project-health-panel")
    await expect(healthPanel).toBeVisible()

    // Hover over a bottleneck button
    const bottleneckButton = healthPanel.locator("button").first()
    await bottleneckButton.hover()
    await page.waitForTimeout(200)

    await expect(healthPanel).toHaveScreenshot(
      "monitor-bottleneck-hover.png"
    )
  })

  test("agent card hover state", async ({ page }) => {
    await setupMocks(page)
    await navigateAndWait(page)

    await page.waitForResponse(
      (res) => res.url().includes("/api/monitor/status") && res.status() === 200,
      { timeout: 10000 }
    )
    await page.waitForResponse(
      (res) => res.url().includes("/api/monitor/tasks") && res.status() === 200,
      { timeout: 10000 }
    )
    await page.waitForTimeout(500)
    await waitForStableContent(page)

    const agentPanel = page.getByTestId("agent-activity-panel")
    await expect(agentPanel).toBeVisible()

    // Hover over an agent card (AgentCard uses CSS module .card class and role="button" when clickable)
    const agentCard = agentPanel.locator("[data-status]").first()
    await agentCard.hover()
    await page.waitForTimeout(200)

    await expect(agentPanel).toHaveScreenshot(
      "monitor-agent-card-hover.png"
    )
  })
})

test.describe("Visual Regression - Degradation Scenarios", () => {
  test.use({ viewport: { width: 1280, height: 720 } })

  test("empty state across all panels", async ({ page }) => {
    await setupMocks(page, { emptyStats: true })
    await navigateAndWait(page)

    await page.waitForResponse(
      (res) => res.url().includes("/api/monitor/status") && res.status() === 200,
      { timeout: 10000 }
    )
    await page.waitForResponse(
      (res) => res.url().includes("/api/monitor/tasks") && res.status() === 200,
      { timeout: 10000 }
    )
    await page.waitForTimeout(500)
    await waitForStableContent(page)

    // Verify empty states across panels (scope to dashboard to avoid sidebar duplicates)
    const dashboard = page.getByTestId("monitor-dashboard")
    await expect(dashboard.getByText("No agents found")).toBeVisible()
    await expect(dashboard.getByText("No bottlenecks detected")).toBeVisible()

    await expect(page).toHaveScreenshot(
      "monitor-degradation-empty.png"
    )
  })

  // SKIPPED: fetchAgents() catches all errors internally and returns [] instead of throwing,
  // so useAgents always sets isConnected=true. The stale banner requires !isConnected && agents.length > 0
  // which can't be triggered through API mocks alone. This behavior is better tested via unit tests.
  test.skip("stale banner with retry button", async ({ page }) => {
    // Start with loom available so data loads
    await setupMocks(page)
    await navigateAndWait(page)

    await page.waitForResponse(
      (res) => res.url().includes("/api/monitor/status") && res.status() === 200,
      { timeout: 10000 }
    )
    await page.waitForResponse(
      (res) => res.url().includes("/api/monitor/tasks") && res.status() === 200,
      { timeout: 10000 }
    )
    await page.waitForTimeout(500)
    await waitForStableContent(page)

    // Switch all loom endpoints to unavailable mid-test
    await page.unroute("**/api/monitor/status")
    await page.unroute("**/api/monitor/tasks")
    await page.unroute("**/api/monitor/agents")
    await page.route("**/api/monitor/agents", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: "invalid json{",
      })
    })
    await page.route("**/api/monitor/status", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: "invalid json{",
      })
    })
    await page.route("**/api/monitor/tasks", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: "invalid json{",
      })
    })

    // Wait for next poll cycle to fail and trigger disconnected state
    await page.waitForResponse(
      (res) => res.url().includes("/api/monitor/agents"),
      { timeout: 15000 }
    )
    await page.waitForTimeout(1000)
    await waitForStableContent(page)

    // Verify ConnectionBanner appears with retry button
    const banner = page.getByRole("alert")
    await expect(banner).toBeVisible({ timeout: 10000 })
    await expect(banner.getByRole("button", { name: "Retry connection now" })).toBeVisible()

    await expect(page).toHaveScreenshot(
      "monitor-degradation-stale-banner.png"
    )
  })
})
