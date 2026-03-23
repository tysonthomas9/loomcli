import { test, expect, Page } from "@playwright/test"

/**
 * E2E tests for the Sessions tab in the IssueDetailPanel.
 * Tests tab visibility, cost summary, empty/error states, timeline rendering,
 * session selection, inner tabs, and API call tracking.
 */

const MOCK_ISSUE = {
  id: "sessions-tab-task-1",
  title: "Sessions Tab Test Issue",
  status: "in_progress",
  priority: 2,
  issue_type: "task",
  created_at: "2026-01-20T10:00:00Z",
  updated_at: "2026-01-20T10:00:00Z",
}

function createMockSession(
  overrides: Record<string, unknown> = {}
) {
  return {
    id: "sess-default",
    agent_name: "nova",
    backend: "claude",
    model: "opus-4",
    phase: "implementation",
    status: "completed",
    started_at: "2026-01-20T10:00:00Z",
    ended_at: "2026-01-20T10:05:00Z",
    duration_s: 300,
    input_tokens: 5000,
    output_tokens: 3000,
    cache_read_tokens: 0,
    cache_write_tokens: 0,
    estimated_cost_usd: 0.15,
    exit_code: 0,
    files_changed: 3,
    lines_added: 50,
    lines_removed: 10,
    files_touched: ["src/foo.ts", "src/bar.ts", "README.md"],
    attempt_num: 1,
    has_transcript: true,
    has_diff: true,
    is_active: false,
    ...overrides,
  }
}

const completedSession = createMockSession({
  id: "sess-tab-001",
  agent_name: "nova",
  status: "completed",
  has_diff: true,
})

const runningSession = createMockSession({
  id: "sess-tab-002",
  agent_name: "spark",
  status: "running",
  is_active: true,
  has_diff: false,
  ended_at: undefined,
  exit_code: undefined,
  model: "sonnet-4",
})

const failedSession = createMockSession({
  id: "sess-tab-003",
  agent_name: "falcon",
  status: "failed",
  has_transcript: false,
  has_diff: false,
  exit_code: 1,
  estimated_cost_usd: 0,
})

const MOCK_SESSIONS = [completedSession, runningSession, failedSession]

const MOCK_TRANSCRIPT = [
  {
    seq: 1,
    ts: "2026-01-20T10:00:01Z",
    role: "user",
    type: "text",
    content: "Fix the authentication issue",
  },
  {
    seq: 2,
    ts: "2026-01-20T10:00:05Z",
    role: "assistant",
    type: "text",
    content: "I'll look into the auth module",
  },
]

const MOCK_DIFF = `--- a/src/auth.ts
+++ b/src/auth.ts
@@ -1,3 +1,3 @@
-const token = null;
+const token = generateToken();`

interface SetupOptions {
  emptySessions?: boolean
  sessionsError?: boolean
}

let sessionsCallCount = 0
let transcriptCallCount = 0
let diffCallCount = 0

async function setupMocks(page: Page, options: SetupOptions = {}) {
  sessionsCallCount = 0
  transcriptCallCount = 0
  diffCallCount = 0

  await page.route("**/api/events", async (route) => {
    await route.abort()
  })

  await page.route("**/api/auth/token", async (route) => {
    await route.abort()
  })

  await page.route("**/api/ready", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: [MOCK_ISSUE] }),
    })
  })

  await page.route("**/api/stats", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: { open: 1, in_progress: 1, review: 0, blocked: 0, closed: 0 },
      }),
    })
  })

  await page.route("**/api/blocked", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: [] }),
    })
  })

  await page.route("**/api/issues/*", async (route) => {
    if (route.request().method() !== "GET") {
      await route.fallback()
      return
    }
    if (route.request().url().includes("/sessions")) {
      await route.fallback()
      return
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        ...MOCK_ISSUE,
        dependencies: [],
        dependents: [],
        comments: [],
      }),
    })
  })

  // Register before sessions to avoid glob collision
  await page.route("**/api/tasks/*/sessions/*/transcript", async (route) => {
    transcriptCallCount++
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: {
          session_id: "sess-tab-001",
          entries: MOCK_TRANSCRIPT,
        },
      }),
    })
  })

  await page.route("**/api/tasks/*/sessions/*/diff", async (route) => {
    diffCallCount++
    await route.fulfill({
      status: 200,
      contentType: "text/plain",
      body: MOCK_DIFF,
    })
  })

  await page.route("**/api/tasks/*/sessions", async (route) => {
    if (route.request().url().match(/\/sessions\/.+/)) {
      await route.fallback()
      return
    }

    sessionsCallCount++

    if (options.sessionsError) {
      await route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({ error: "Internal server error" }),
      })
      return
    }

    const sessions = options.emptySessions ? [] : MOCK_SESSIONS
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: { task_id: MOCK_ISSUE.id, sessions },
      }),
    })
  })
}

async function navigateToApp(page: Page) {
  await page.goto("/")
  await page.waitForTimeout(1000)
}

async function openIssuePanel(page: Page) {
  const issueCard = page
    .locator("article")
    .filter({ hasText: MOCK_ISSUE.title })
  await expect(issueCard).toBeVisible()
  await issueCard.click()

  const panel = page.getByTestId("issue-detail-panel")
  await expect(panel).toHaveAttribute("data-state", "open", { timeout: 5000 })
  await expect(panel).toHaveAttribute("data-loading", "false", {
    timeout: 5000,
  })
}

async function switchToSessionsTab(page: Page) {
  await page.locator("#issue-panel-tab-sessions").click()
  await expect(
    page
      .getByTestId("sessions-tab")
      .or(page.getByTestId("sessions-empty"))
  ).toBeVisible({ timeout: 5000 })
}

test.describe("Sessions Tab in Issue Detail Panel", () => {
  test.describe("Sessions tab visibility", () => {
    test("task issue shows the Sessions tab button", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)

      await expect(
        page.locator("#issue-panel-tab-sessions")
      ).toBeVisible()
    })

    test("clicking Sessions tab renders the sessions container", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)

      await expect(page.getByTestId("sessions-tab")).toBeVisible()
    })
  })

  test.describe("Cost summary display", () => {
    test("cost summary shows session count", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)

      await expect(page.getByTestId("sessions-tab")).toContainText("3")
    })

    test("active sessions badge visible when running sessions exist", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)

      await expect(page.getByText(/active/i)).toBeVisible()
    })
  })

  test.describe("Empty state", () => {
    test('shows No sessions recorded yet when empty', async ({ page }) => {
      await setupMocks(page, { emptySessions: true })
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)

      await expect(page.getByTestId("sessions-empty")).toBeVisible()
      await expect(
        page.getByText("No sessions recorded yet")
      ).toBeVisible()
    })
  })

  test.describe("Error state", () => {
    test("shows error when sessions API fails", async ({ page }) => {
      await setupMocks(page, { sessionsError: true })
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)

      await expect(
        page.getByText(/Failed to load sessions/i)
      ).toBeVisible({ timeout: 5000 })
    })
  })

  test.describe("Session timeline rendering", () => {
    test("timeline container is visible", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)

      await expect(page.getByTestId("session-timeline")).toBeVisible()
    })

    test("correct number of session rows rendered", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)

      const rows = page.locator('[data-testid^="session-row-"]')
      await expect(rows).toHaveCount(3)
    })

    test("each row shows agent name", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)

      await expect(
        page.getByTestId("session-row-sess-tab-001")
      ).toContainText("nova")
    })

    test('placeholder shown when no session selected', async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)

      await expect(
        page.getByText("Select a session to view details")
      ).toBeVisible()
    })
  })

  test.describe("Session selection", () => {
    test("clicking a session row shows SessionDetailView", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)

      await page.getByTestId("session-row-sess-tab-001").click()
      await expect(page.getByTestId("session-detail-view")).toBeVisible()
    })

    test("clicking a different session row updates the detail view", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)

      await page.getByTestId("session-row-sess-tab-001").click()
      await expect(page.getByTestId("session-detail-view")).toContainText(
        "nova"
      )

      await page.getByTestId("session-row-sess-tab-002").click()
      await expect(page.getByTestId("session-detail-view")).toContainText(
        "spark"
      )
    })
  })

  test.describe("Inner tab basics", () => {
    test("selected session shows transcript tab content by default", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)

      await page.getByTestId("session-row-sess-tab-001").click()
      await expect(page.getByTestId("session-transcript")).toBeVisible()
    })

    test("clicking diff inner tab shows diff container", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)

      await page.getByTestId("session-row-sess-tab-001").click()
      await expect(page.getByTestId("session-detail-view")).toBeVisible()

      await page.getByTestId("session-inner-tab-diff").click()
      await expect(page.getByTestId("session-diff")).toBeVisible()
    })

    test("diff tab button is disabled when has_diff is false", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)

      await page.getByTestId("session-row-sess-tab-002").click()
      await expect(page.getByTestId("session-detail-view")).toBeVisible()

      const diffTab = page.getByTestId("session-inner-tab-diff")
      await expect(diffTab).toBeDisabled()
    })
  })

  test.describe("API call tracking", () => {
    test("sessions endpoint called when Sessions tab is activated", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)

      await expect.poll(() => sessionsCallCount, { timeout: 5000 }).toBeGreaterThan(0)
    })

    test("transcript endpoint called when session is selected", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)

      await page.getByTestId("session-row-sess-tab-001").click()

      await expect
        .poll(() => transcriptCallCount, { timeout: 5000 })
        .toBeGreaterThan(0)
    })

    test("diff endpoint called only when diff tab is clicked", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)

      await page.getByTestId("session-row-sess-tab-001").click()
      await expect(page.getByTestId("session-transcript")).toBeVisible()

      expect(diffCallCount).toBe(0)

      await page.getByTestId("session-inner-tab-diff").click()

      await expect
        .poll(() => diffCallCount, { timeout: 5000 })
        .toBeGreaterThan(0)
    })
  })
})
