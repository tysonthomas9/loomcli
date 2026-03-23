import { test, expect, Page } from "@playwright/test"

/**
 * E2E tests for the session timeline interactions within the IssueDetailPanel Sessions tab.
 * Tests session list rendering, selection, detail metadata, transcript/diff tabs, and formatting.
 */

const MOCK_ISSUE = {
  id: "sess-e2e-task-1",
  title: "Session Timeline Test Issue",
  status: "in_progress",
  priority: 2,
  issue_type: "task",
  created_at: "2026-01-20T10:00:00Z",
  updated_at: "2026-01-20T10:00:00Z",
}

const MOCK_SESSIONS = [
  {
    id: "sess-001",
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
    estimated_cost_usd: 1.5,
    exit_code: 0,
    files_changed: 3,
    lines_added: 50,
    lines_removed: 10,
    files_touched: ["src/foo.ts", "src/bar.ts", "README.md"],
    attempt_num: 1,
    has_transcript: true,
    has_diff: true,
    is_active: false,
  },
  {
    id: "sess-002",
    agent_name: "falcon",
    backend: "claude",
    status: "failed",
    started_at: "2026-01-20T09:00:00Z",
    ended_at: "2026-01-20T09:02:00Z",
    duration_s: 120,
    input_tokens: 1000,
    output_tokens: 500,
    cache_read_tokens: 0,
    cache_write_tokens: 0,
    estimated_cost_usd: 0.03,
    exit_code: 1,
    files_changed: 0,
    lines_added: 0,
    lines_removed: 0,
    attempt_num: 1,
    has_transcript: true,
    has_diff: false,
    is_active: false,
  },
  {
    id: "sess-003",
    agent_name: "spark",
    backend: "claude",
    model: "sonnet-4",
    phase: "planning",
    status: "running",
    started_at: "2026-01-20T10:10:00Z",
    duration_s: 0,
    input_tokens: 200,
    output_tokens: 100,
    cache_read_tokens: 0,
    cache_write_tokens: 0,
    estimated_cost_usd: 0.01,
    files_changed: 0,
    lines_added: 0,
    lines_removed: 0,
    attempt_num: 1,
    has_transcript: true,
    has_diff: false,
    is_active: true,
  },
]

const MOCK_TRANSCRIPT = [
  {
    seq: 1,
    ts: "2026-01-20T10:00:01Z",
    role: "user",
    type: "text",
    content: "Please fix the login bug",
  },
  {
    seq: 2,
    ts: "2026-01-20T10:00:05Z",
    role: "assistant",
    type: "text",
    content: "I'll investigate the login handler",
  },
  {
    seq: 3,
    ts: "2026-01-20T10:00:10Z",
    role: "assistant",
    type: "tool_use",
    tool_name: "Read",
    content: "src/auth.ts",
  },
  {
    seq: 4,
    ts: "2026-01-20T10:00:15Z",
    role: "tool",
    type: "tool_result",
    content: "function login() { ... }",
  },
]

const MOCK_DIFF = `--- a/src/auth.ts
+++ b/src/auth.ts
@@ -10,7 +10,7 @@
 function login(user: string) {
-  return null;
+  return authenticate(user);
 }`

async function setupMocks(
  page: Page,
  overrides?: {
    emptySessions?: boolean
    sessionsError?: boolean
    emptyTranscript?: boolean
  }
) {
  await page.route("**/api/events", async (route) => {
    await route.abort()
  })

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

  // Register before sessions list to avoid glob collision
  await page.route("**/api/tasks/*/sessions/*/transcript", async (route) => {
    const entries = overrides?.emptyTranscript ? [] : MOCK_TRANSCRIPT
    const sessionId =
      route
        .request()
        .url()
        .match(/sessions\/([^/]+)\/transcript/)?.[1] ?? "unknown"
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: { session_id: sessionId, entries },
      }),
    })
  })

  await page.route("**/api/tasks/*/sessions/*/diff", async (route) => {
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

    if (overrides?.sessionsError) {
      await route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({ error: "Server error" }),
      })
      return
    }

    const sessions = overrides?.emptySessions ? [] : MOCK_SESSIONS
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
  const issueCard = page.locator("article").filter({ hasText: MOCK_ISSUE.title })
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
  await expect(page.getByTestId("sessions-tab")).toBeVisible({ timeout: 5000 })
}

async function selectSession(page: Page, sessionTestId = "session-row-sess-001") {
  await page.getByTestId(sessionTestId).click()
  await expect(page.getByTestId("session-detail-view")).toBeVisible()
}

test.describe("Session Timeline", () => {
  test.describe("Timeline rendering", () => {
    test("sessions tab renders timeline with session rows", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)

      await expect(page.getByTestId("session-timeline")).toBeVisible()
      await expect(page.getByTestId("session-row-sess-001")).toBeVisible()
      await expect(page.getByTestId("session-row-sess-002")).toBeVisible()
      await expect(page.getByTestId("session-row-sess-003")).toBeVisible()
    })

    test('empty state shows No sessions recorded yet', async ({ page }) => {
      await setupMocks(page, { emptySessions: true })
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)

      await expect(page.getByTestId("sessions-empty")).toBeVisible()
      await expect(
        page.getByText("No sessions recorded yet")
      ).toBeVisible()
    })

    test("each session row displays agent name", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)

      const row1 = page.getByTestId("session-row-sess-001")
      await expect(row1).toContainText("nova")

      const row2 = page.getByTestId("session-row-sess-002")
      await expect(row2).toContainText("falcon")
    })
  })

  test.describe("Session selection", () => {
    test("clicking a session row shows the detail view", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)
      await selectSession(page)

      await expect(page.getByTestId("session-detail-view")).toBeVisible()
    })

    test("initial state shows placeholder text", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)

      await expect(
        page.getByText("Select a session to view details")
      ).toBeVisible()
    })

    test("clicking a different session switches the detail view", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)

      await selectSession(page, "session-row-sess-001")
      await expect(page.getByTestId("session-detail-view")).toContainText(
        "nova"
      )

      await selectSession(page, "session-row-sess-002")
      await expect(page.getByTestId("session-detail-view")).toContainText(
        "falcon"
      )
    })
  })

  test.describe("Session detail view - metadata", () => {
    test("detail view shows model name and exit code", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)
      await selectSession(page)

      const detail = page.getByTestId("session-detail-view")
      await expect(detail).toContainText("opus-4")
      await expect(detail).toContainText("0 (success)")
    })

    test("hides model field when session has no model", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)
      await selectSession(page, "session-row-sess-002")

      const detail = page.getByTestId("session-detail-view")
      await expect(detail).not.toContainText("Model:")
    })
  })

  test.describe("Session detail view - transcript tab", () => {
    test("transcript tab is active by default when selecting a session", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)
      await selectSession(page)

      await expect(page.getByTestId("session-transcript")).toBeVisible()
    })

    test("transcript entries render with content", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)
      await selectSession(page)

      const transcript = page.getByTestId("session-transcript")
      await expect(transcript).toContainText("Please fix the login bug")
      await expect(transcript).toContainText(
        "I'll investigate the login handler"
      )
    })

    test("tool entries show Tool: label", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)
      await selectSession(page)

      const transcript = page.getByTestId("session-transcript")
      await expect(transcript).toContainText("Tool: Read")
    })

    test('shows No transcript entries when transcript is empty', async ({
      page,
    }) => {
      await setupMocks(page, { emptyTranscript: true })
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)
      await selectSession(page)

      await expect(
        page.getByText("No transcript entries")
      ).toBeVisible()
    })
  })

  test.describe("Session detail view - diff tab", () => {
    test("clicking Diff tab shows diff content", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)
      await selectSession(page)

      await page.getByTestId("session-inner-tab-diff").click()
      await expect(page.getByTestId("session-diff")).toBeVisible()
    })

    test("diff tab is disabled when session has_diff is false", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)
      await selectSession(page, "session-row-sess-002")

      const diffTab = page.getByTestId("session-inner-tab-diff")
      await expect(diffTab).toBeDisabled()
    })
  })

  test.describe("Session detail view - files touched", () => {
    test("files touched section appears for sessions with files", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)
      await selectSession(page)

      const detail = page.getByTestId("session-detail-view")
      await expect(detail).toContainText("Files Touched")
    })

    test("hides files touched section when no files", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)
      await selectSession(page, "session-row-sess-002")

      const detail = page.getByTestId("session-detail-view")
      await expect(detail).not.toContainText("Files Touched")
    })
  })

  test.describe("Tab switching", () => {
    test("switching between transcript and diff tabs preserves content", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page)
      await switchToSessionsTab(page)
      await selectSession(page)

      await expect(page.getByTestId("session-transcript")).toBeVisible()

      await page.getByTestId("session-inner-tab-diff").click()
      await expect(page.getByTestId("session-diff")).toBeVisible()
      await expect(page.getByTestId("session-transcript")).not.toBeVisible()

      await page.getByTestId("session-inner-tab-transcript").click()
      await expect(page.getByTestId("session-transcript")).toBeVisible()
      await expect(page.getByTestId("session-diff")).not.toBeVisible()
    })
  })
})
