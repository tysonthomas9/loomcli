import { test, expect, Page } from "@playwright/test"

/**
 * E2E tests for the WorkPipelinePanel Backlog label rename.
 * Verifies "Backlog" replaces "Blocked" as the branch stage label,
 * with correct count, icon, and drawer title.
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
]

const mockLoomStatusWithBacklog = {
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
  ],
  tasks: {
    needs_planning: 2,
    ready_to_implement: 3,
    in_progress: 1,
    need_review: 1,
    blocked: 3,
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

const mockLoomStatusZeroBlocked = {
  ...mockLoomStatusWithBacklog,
  tasks: {
    ...mockLoomStatusWithBacklog.tasks,
    blocked: 0,
  },
}

const mockLoomTasksWithBacklog = {
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
  blocked: [
    { id: "bd-040", title: "Setup database schema", priority: 1 },
    { id: "bd-041", title: "Configure CI pipeline", priority: 2 },
    { id: "bd-042", title: "Write integration tests", priority: 3 },
  ],
}

const mockLoomTasksZeroBlocked = {
  ...mockLoomTasksWithBacklog,
  blocked: [],
}

/**
 * Set up all API mocks for Backlog label tests.
 */
async function setupMocks(
  page: Page,
  options?: { blockedCount?: number }
) {
  const { blockedCount = 3 } = options ?? {}
  const useZeroBlocked = blockedCount === 0

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
      body: JSON.stringify(
        useZeroBlocked ? mockLoomStatusZeroBlocked : mockLoomStatusWithBacklog
      ),
    })
  })

  await page.route("**/localhost:9000/api/tasks", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(
        useZeroBlocked ? mockLoomTasksZeroBlocked : mockLoomTasksWithBacklog
      ),
    })
  })
}

/**
 * Navigate to monitor view and wait for API response.
 */
async function navigateAndWait(page: Page) {
  const [response] = await Promise.all([
    page.waitForResponse(
      (res) => res.url().includes("/api/ready") && res.status() === 200
    ),
    page.goto("/?view=monitor"),
  ])
  expect(response.ok()).toBe(true)
}

/**
 * Wait for pipeline data to load.
 */
async function waitForPipelineData(page: Page) {
  await page.waitForResponse(
    (res) => res.url().includes("/api/tasks") && res.status() === 200,
    { timeout: 10000 }
  )
  await page.waitForTimeout(500)
}

test.describe("WorkPipelinePanel Backlog Label", () => {
  test.describe("Stage Label Rendering", () => {
    test("Backlog branch stage shows 'Backlog' label", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)
      await waitForPipelineData(page)

      const blockedStage = page.getByTestId("pipeline-stage-blocked")
      await expect(blockedStage).toBeVisible()

      // Label should read "Backlog", not "Blocked"
      await expect(blockedStage).toContainText("Backlog")
      await expect(blockedStage.locator("span").filter({ hasText: /^Blocked$/ })).not.toBeVisible()
    })

    test("Backlog branch stage shows 📦 icon", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)
      await waitForPipelineData(page)

      const blockedStage = page.getByTestId("pipeline-stage-blocked")
      await expect(blockedStage).toBeVisible()
      await expect(blockedStage).toContainText("📦")
    })

    test("Backlog branch shows ↳ connector", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)
      await waitForPipelineData(page)

      const pipelinePanel = page.getByTestId("work-pipeline-panel")
      await expect(pipelinePanel.getByText("↳")).toBeVisible()
    })

    test("Backlog stage hidden when count is 0", async ({ page }) => {
      await setupMocks(page, { blockedCount: 0 })
      await navigateAndWait(page)
      await waitForPipelineData(page)

      await expect(page.getByTestId("pipeline-stage-blocked")).not.toBeVisible()
    })
  })

  test.describe("Backlog Count", () => {
    test("Backlog stage shows correct count", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)
      await waitForPipelineData(page)

      const blockedStage = page.getByTestId("pipeline-stage-blocked")
      await expect(blockedStage).toBeVisible()
      await expect(blockedStage).toContainText("3")
    })
  })

  test.describe("Backlog Drawer", () => {
    test("clicking Backlog stage opens drawer with 'Backlog' title", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)
      await waitForPipelineData(page)

      const blockedStage = page.getByTestId("pipeline-stage-blocked")
      await blockedStage.click()

      const taskDrawer = page.getByRole("dialog")
      await expect(taskDrawer).toBeVisible()

      // Drawer heading should say "Backlog", not "Blocked"
      const heading = taskDrawer.getByRole("heading")
      await expect(heading).toContainText("Backlog")
    })

    test("Backlog drawer shows task list", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)
      await waitForPipelineData(page)

      const blockedStage = page.getByTestId("pipeline-stage-blocked")
      await blockedStage.click()

      const taskDrawer = page.getByRole("dialog")
      await expect(taskDrawer).toBeVisible()

      // Verify each task from mock data
      await expect(taskDrawer).toContainText("Setup database schema")
      await expect(taskDrawer).toContainText("Configure CI pipeline")
      await expect(taskDrawer).toContainText("Write integration tests")

      // Verify count badge
      await expect(taskDrawer).toContainText("(3)")
    })

    test("Backlog drawer closes via Escape key", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)
      await waitForPipelineData(page)

      const blockedStage = page.getByTestId("pipeline-stage-blocked")
      await blockedStage.click()

      const taskDrawer = page.getByRole("dialog")
      await expect(taskDrawer).toBeVisible()

      await page.keyboard.press("Escape")
      await expect(taskDrawer).not.toBeVisible()
    })

    test("Backlog drawer closes via close button", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)
      await waitForPipelineData(page)

      const blockedStage = page.getByTestId("pipeline-stage-blocked")
      await blockedStage.click()

      const taskDrawer = page.getByRole("dialog")
      await expect(taskDrawer).toBeVisible()

      await page.getByRole("button", { name: "Close drawer" }).click()
      await expect(taskDrawer).not.toBeVisible()
    })
  })
})
