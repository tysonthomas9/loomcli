import { test, expect, Page } from "@playwright/test"

/**
 * E2E tests for the AgentsSidebar component.
 * Tests the collapsible sidebar with agent status cards, work queue, sync, and stats.
 */

interface SetupOptions {
  loomServerAvailable?: boolean
  emptyAgents?: boolean
  withPushDetails?: boolean
  withSyncWarnings?: boolean
}

const mockAgents = [
  {
    name: "nova",
    branch: "feature-auth",
    status: "working: bd-101 (5m)",
    ahead: 2,
    behind: 0,
    role: "task",
    workspace: "",
    repo: "",
  },
  {
    name: "falcon",
    branch: "main",
    status: "ready",
    ahead: 0,
    behind: 1,
    role: "plan",
    workspace: "",
    repo: "",
  },
]

const mockLoomStatus = {
  agents: mockAgents,
  tasks: {
    needs_planning: 3,
    ready_to_implement: 5,
    in_progress: 2,
    need_review: 1,
    backlog: 0,
  },
  in_progress_list: null,
  agent_tasks: {
    nova: {
      id: "bd-101",
      title: "Implement auth flow",
      priority: 1,
      status: "in_progress",
    },
  },
  sync: {
    db_synced: true,
    db_last_sync: "2026-01-24T12:00:00Z",
    git_needs_push: 0,
    git_needs_pull: 0,
  },
  stats: {
    open: 8,
    closed: 12,
    total: 20,
    completion: 60,
    remaining: 8,
    in_progress: 2,
    review: 1,
    blocked: 0,
  },
  timestamp: "2026-01-24T12:00:00Z",
}

const mockLoomTasks = {
  summary: {
    needs_planning: 3,
    ready_to_implement: 5,
    in_progress: 2,
    need_review: 1,
    backlog: 0,
  },
  needs_planning: [
    { id: "bd-010", title: "Plan auth", priority: 1, status: "open" },
    { id: "bd-011", title: "Plan API design", priority: 2, status: "open" },
    {
      id: "bd-012",
      title: "Plan database schema",
      priority: 3,
      status: "open",
    },
  ],
  ready_to_implement: [
    { id: "bd-020", title: "Build login page", priority: 1, status: "open" },
    { id: "bd-021", title: "Add validation", priority: 2, status: "open" },
    { id: "bd-022", title: "Create user model", priority: 2, status: "open" },
    { id: "bd-023", title: "Setup routes", priority: 3, status: "open" },
    { id: "bd-024", title: "Add middleware", priority: 3, status: "open" },
  ],
  in_progress: [
    {
      id: "bd-101",
      title: "Implement auth flow",
      priority: 1,
      status: "in_progress",
    },
    {
      id: "bd-102",
      title: "Build dashboard",
      priority: 2,
      status: "in_progress",
    },
  ],
  needs_review: [
    { id: "bd-030", title: "Review PR #42", priority: 2, status: "review" },
  ],
  backlog: [],
  closed: [
    { id: "bd-040", title: "Setup CI", priority: 3, status: "closed" },
    { id: "bd-041", title: "Init project", priority: 3, status: "closed" },
  ],
  timestamp: "2026-01-24T12:00:00Z",
}

const mockIssues = [
  {
    id: "test-1",
    title: "Test Issue",
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-24T10:00:00Z",
    updated_at: "2026-01-24T10:00:00Z",
  },
]

async function setupMocks(page: Page, options: SetupOptions = {}) {
  const {
    loomServerAvailable = true,
    emptyAgents = false,
    withPushDetails = false,
    withSyncWarnings = false,
  } = options

  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({ status: 404 })
  })

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
        data: { open: 8, closed: 12, total: 20, completion: 60 },
      }),
    })
  })

  // Mock loom health check
  await page.route("**/api/loom/health", async (route) => {
    await route.fulfill({
      status: loomServerAvailable ? 200 : 503,
      contentType: "application/json",
      body: JSON.stringify({ status: loomServerAvailable ? "ok" : "unavailable" }),
    })
  })

  // Mock loom agents endpoint
  await page.route("**/api/loom/api/agents", async (route) => {
    if (loomServerAvailable) {
      const agents = emptyAgents ? [] : mockAgents
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ agents }),
      })
    } else {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: "invalid json{",
      })
    }
  })

  if (loomServerAvailable) {
    let statusBody = { ...mockLoomStatus }
    if (emptyAgents) {
      statusBody = { ...statusBody, agents: [], agent_tasks: {} }
    }
    if (withPushDetails) {
      statusBody = {
        ...statusBody,
        sync: {
          ...statusBody.sync,
          git_needs_push: 2,
          git_push_details: [
            { name: "nova", count: 2 },
            { name: "falcon", count: 1 },
          ],
        } as typeof statusBody.sync,
      }
    }
    if (withSyncWarnings) {
      statusBody = {
        ...statusBody,
        sync: {
          ...statusBody.sync,
          git_needs_pull: 3,
          git_pull_details: [
            { name: "nova", count: 2 },
            { name: "falcon", count: 1 },
          ],
        } as typeof statusBody.sync,
      }
    }

    await page.route("**/api/loom/api/status", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(statusBody),
      })
    })

    await page.route("**/api/loom/api/tasks", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(mockLoomTasks),
      })
    })
  } else {
    await page.route("**/api/loom/api/status", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: "invalid json{",
      })
    })
    await page.route("**/api/loom/api/tasks", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: "invalid json{",
      })
    })
  }
}

async function navigateAndWait(page: Page, path = "/") {
  await page.goto(path)
  await page.waitForTimeout(1000)
}

test.describe("AgentsSidebar", () => {
  test.describe("rendering", () => {
    test("sidebar renders with agents header and count", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      await expect(page.getByText("Agents")).toBeVisible()
      await expect(page.getByText("2").first()).toBeVisible()
    })

    test("agent cards render for each agent", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      await expect(page.getByText("nova")).toBeVisible({ timeout: 10000 })
      await expect(page.getByText("falcon")).toBeVisible()
    })
  })

  test.describe("sidebar state", () => {
    test("sidebar is not collapsed by default", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      const sidebar = page.locator("aside").first()
      await expect(page.getByText("nova")).toBeVisible({ timeout: 10000 })
      await expect(sidebar).toHaveAttribute("data-collapsed", "false")
    })

    test("agents header shows count", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      await expect(page.getByText("Agents")).toBeVisible({ timeout: 10000 })
      await expect(page.getByText("2").first()).toBeVisible()
    })
  })

  test.describe("agent cards", () => {
    test("working agent shows Working status text", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      await expect(page.getByText("nova")).toBeVisible({ timeout: 10000 })
      await expect(page.getByText("Working")).toBeVisible()
    })

    test("ready agent shows Ready status text", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      await expect(page.getByText("falcon")).toBeVisible({ timeout: 10000 })
      await expect(page.getByText("Ready")).toBeVisible()
    })
  })

  test.describe("work queue", () => {
    test("work queue section visible when connected", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      await expect(page.getByText("Work Queue")).toBeVisible({ timeout: 10000 })
    })

    test("category buttons show correct counts", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      await expect(page.getByText("Work Queue")).toBeVisible({ timeout: 10000 })

      const queueArea = page.locator("aside").first()
      await expect(
        queueArea.getByRole("button", { name: /Backlog/ })
      ).toContainText("3")
      await expect(
        queueArea.getByRole("button", { name: /Open/ })
      ).toContainText("5")
      await expect(
        queueArea.getByRole("button", { name: /Blocked/ })
      ).toContainText("0")
      await expect(
        queueArea.getByRole("button", { name: /In Progress/ })
      ).toContainText("2")
      await expect(
        queueArea.getByRole("button", { name: /Needs Review/ })
      ).toContainText("1")
      await expect(
        queueArea.getByRole("button", { name: /Done/ })
      ).toContainText("12")
    })

    test("disabled buttons for zero-count categories", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      await expect(page.getByText("Work Queue")).toBeVisible({ timeout: 10000 })

      const queueArea = page.locator("aside").first()
      const blockedBtn = queueArea.getByRole("button", { name: /Blocked/ })
      await expect(blockedBtn).toBeDisabled()
    })

    test("category button click opens TaskDrawer", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      await expect(page.getByText("Work Queue")).toBeVisible({ timeout: 10000 })

      const queueArea = page.locator("aside").first()
      const openBtn = queueArea.getByRole("button", { name: /Open/ })
      await openBtn.click()

      await expect(page.getByText("Build login page")).toBeVisible()
    })
  })

  test.describe("git sync and push", () => {
    test("push banner hidden when git_needs_push is 0", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      await expect(page.getByText("Work Queue")).toBeVisible({ timeout: 10000 })
      await expect(page.getByText("need push")).not.toBeVisible()
    })

    test("push banner shows count when git_needs_push > 0", async ({
      page,
    }) => {
      await setupMocks(page, { withPushDetails: true })
      await navigateAndWait(page, "/")

      await expect(page.getByText("Work Queue")).toBeVisible({ timeout: 10000 })
      await expect(page.getByText("2 need push")).toBeVisible()
    })

    test("Push All button visible when pushes needed", async ({ page }) => {
      await setupMocks(page, { withPushDetails: true })
      await navigateAndWait(page, "/")

      await expect(page.getByText("Work Queue")).toBeVisible({ timeout: 10000 })
      await expect(
        page.getByRole("button", { name: "Push All" })
      ).toBeVisible()
    })
  })

  test.describe("footer stats", () => {
    test("footer shows remaining, closed, and completion percentage", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      await expect(page.getByText("nova")).toBeVisible({ timeout: 10000 })

      const sidebar = page.locator("aside").first()
      await expect(sidebar.getByText("remaining")).toBeVisible()
      await expect(sidebar.getByText("closed")).toBeVisible()
      await expect(sidebar.getByText("60%")).toBeVisible()
    })

    test("footer shows sync warnings for unpulled changes", async ({
      page,
    }) => {
      await setupMocks(page, { withSyncWarnings: true })
      await navigateAndWait(page, "/")

      await expect(page.getByText("nova")).toBeVisible({ timeout: 10000 })
      await expect(page.getByText("3 unpulled")).toBeVisible()
    })
  })

  test.describe("connection states", () => {
    test("shows error when loom server unavailable", async ({ page }) => {
      await setupMocks(page, { loomServerAvailable: false })
      await navigateAndWait(page, "/")

      await expect(
        page.getByText("Loom server not available")
      ).toBeVisible({ timeout: 10000 })
    })

    test("shows empty guide when connected but no agents", async ({
      page,
    }) => {
      await setupMocks(page, { emptyAgents: true })
      await navigateAndWait(page, "/")

      await expect(page.getByText("No agents found")).toBeVisible({
        timeout: 10000,
      })
    })
  })
})
