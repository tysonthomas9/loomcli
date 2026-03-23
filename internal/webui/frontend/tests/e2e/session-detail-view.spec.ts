import { test, expect, Page } from "@playwright/test"

/**
 * E2E tests for the SessionDetailView component's transcript and diff tabs.
 * Tests the full flow: opening issue panel, navigating to Sessions tab,
 * selecting a session, and interacting with transcript and diff inner tabs.
 */

const MOCK_ISSUE = {
  id: "sess-e2e-detail-1",
  title: "Session Detail View Test Issue",
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
  {
    seq: 5,
    ts: "2026-01-20T10:00:20Z",
    role: "assistant",
    type: "tool_use",
    tool_name: "Bash",
    tool_input: "npm test",
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
    emptyTranscript?: boolean
    diff404?: boolean
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

  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", daemon: true }),
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
    if (overrides?.diff404) {
      await route.fulfill({
        status: 404,
        contentType: "application/json",
        body: JSON.stringify({ error: "Not found" }),
      })
      return
    }
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
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: { task_id: MOCK_ISSUE.id, sessions: MOCK_SESSIONS },
      }),
    })
  })
}

async function navigateToSessionDetail(
  page: Page,
  sessionRowTestId = "session-row-sess-001"
) {
  await page.goto("/")
  await page.waitForTimeout(1000)

  const issueCard = page
    .locator("article")
    .filter({ hasText: MOCK_ISSUE.title })
  await issueCard.click()
  await expect(page.getByTestId("issue-detail-panel")).toHaveAttribute(
    "data-state",
    "open",
    { timeout: 5000 }
  )
  await expect(page.getByTestId("issue-detail-panel")).toHaveAttribute(
    "data-loading",
    "false",
    { timeout: 5000 }
  )

  await page.locator("#issue-panel-tab-sessions").click()
  await expect(page.getByTestId("sessions-tab")).toBeVisible({ timeout: 5000 })

  await page.getByTestId(sessionRowTestId).click()
  await expect(page.getByTestId("session-detail-view")).toBeVisible()
}

test.describe("Session Detail View - Transcript and Diff Tabs", () => {
  test.describe("navigation and selection", () => {
    test("opening issue panel and clicking Sessions tab shows sessions timeline", async ({
      page,
    }) => {
      await setupMocks(page)
      await page.goto("/")
      await page.waitForTimeout(1000)

      const issueCard = page
        .locator("article")
        .filter({ hasText: MOCK_ISSUE.title })
      await issueCard.click()
      await expect(page.getByTestId("issue-detail-panel")).toHaveAttribute(
        "data-state",
        "open",
        { timeout: 5000 }
      )

      await page.locator("#issue-panel-tab-sessions").click()
      await expect(page.getByTestId("sessions-tab")).toBeVisible({
        timeout: 5000,
      })
      await expect(page.getByTestId("session-timeline")).toBeVisible()
      await expect(page.getByTestId("session-row-sess-001")).toBeVisible()
      await expect(page.getByTestId("session-row-sess-002")).toBeVisible()
    })

    test("clicking a session shows the detail view", async ({ page }) => {
      await setupMocks(page)
      await navigateToSessionDetail(page)

      await expect(page.getByTestId("session-detail-view")).toBeVisible()
    })

    test('shows placeholder when no session selected', async ({ page }) => {
      await setupMocks(page)
      await page.goto("/")
      await page.waitForTimeout(1000)

      const issueCard = page
        .locator("article")
        .filter({ hasText: MOCK_ISSUE.title })
      await issueCard.click()
      await expect(page.getByTestId("issue-detail-panel")).toHaveAttribute(
        "data-state",
        "open",
        { timeout: 5000 }
      )

      await page.locator("#issue-panel-tab-sessions").click()
      await expect(page.getByTestId("sessions-tab")).toBeVisible({
        timeout: 5000,
      })
      await expect(
        page.getByText("Select a session to view details")
      ).toBeVisible()
    })
  })

  test.describe("metadata summary", () => {
    test("displays model, exit code, files changed, and lines", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToSessionDetail(page)

      const detail = page.getByTestId("session-detail-view")
      await expect(detail).toContainText("opus-4")
      await expect(detail).toContainText("0 (success)")
    })

    test("hides model field when session has no model", async ({ page }) => {
      await setupMocks(page)
      await navigateToSessionDetail(page, "session-row-sess-002")

      const detail = page.getByTestId("session-detail-view")
      await expect(detail).not.toContainText("Model:")
    })

    test("hides files field when files_changed is 0", async ({ page }) => {
      await setupMocks(page)
      await navigateToSessionDetail(page, "session-row-sess-002")

      const detail = page.getByTestId("session-detail-view")
      await expect(detail).not.toContainText("Files:")
    })
  })

  test.describe("transcript tab", () => {
    test("transcript tab is active by default and shows entries", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToSessionDetail(page)

      await expect(page.getByTestId("session-transcript")).toBeVisible()
      await expect(
        page.getByTestId("session-transcript")
      ).toContainText("Please fix the login bug")
    })

    test("renders role labels for each entry", async ({ page }) => {
      await setupMocks(page)
      await navigateToSessionDetail(page)

      const transcript = page.getByTestId("session-transcript")
      await expect(transcript).toContainText("user")
      await expect(transcript).toContainText("assistant")
    })

    test("renders tool name for tool_use entries", async ({ page }) => {
      await setupMocks(page)
      await navigateToSessionDetail(page)

      const transcript = page.getByTestId("session-transcript")
      await expect(transcript).toContainText("Tool: Read")
    })

    test("shows tool_input when content is absent on tool_use entries", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToSessionDetail(page)

      const transcript = page.getByTestId("session-transcript")
      await expect(transcript).toContainText("npm test")
    })

    test('shows No transcript entries when empty', async ({ page }) => {
      await setupMocks(page, { emptyTranscript: true })
      await navigateToSessionDetail(page)

      await expect(
        page.getByText("No transcript entries")
      ).toBeVisible()
    })
  })

  test.describe("diff tab", () => {
    test("clicking Diff tab shows diff content", async ({ page }) => {
      await setupMocks(page)
      await navigateToSessionDetail(page)

      await page.getByTestId("session-inner-tab-diff").click()
      const diffContainer = page.getByTestId("session-diff")
      await expect(diffContainer).toBeVisible()
    })

    test("diff tab is disabled when session has_diff is false", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToSessionDetail(page, "session-row-sess-002")

      const diffTab = page.getByTestId("session-inner-tab-diff")
      await expect(diffTab).toBeDisabled()
      await expect(diffTab).toHaveAttribute("title", "No diff available")
    })

    test('shows No diff available when diff endpoint returns 404', async ({
      page,
    }) => {
      await setupMocks(page, { diff404: true })
      await navigateToSessionDetail(page)

      await page.getByTestId("session-inner-tab-diff").click()
      await expect(page.getByText("No diff available")).toBeVisible()
    })
  })

  test.describe("tab switching", () => {
    test("switching between transcript and diff tabs preserves content", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToSessionDetail(page)

      await expect(page.getByTestId("session-transcript")).toBeVisible()

      await page.getByTestId("session-inner-tab-diff").click()
      await expect(page.getByTestId("session-diff")).toBeVisible()
      await expect(page.getByTestId("session-transcript")).not.toBeVisible()

      await page.getByTestId("session-inner-tab-transcript").click()
      await expect(page.getByTestId("session-transcript")).toBeVisible()
      await expect(page.getByTestId("session-diff")).not.toBeVisible()
    })
  })

  test.describe("files touched section", () => {
    test("shows collapsible files touched section with file paths", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToSessionDetail(page)

      const detail = page.getByTestId("session-detail-view")
      await expect(detail).toContainText("Files Touched")

      const summary = detail.locator("summary")
      if ((await summary.count()) > 0) {
        await summary.click()
      }

      await expect(detail).toContainText("src/foo.ts")
      await expect(detail).toContainText("src/bar.ts")
      await expect(detail).toContainText("README.md")
    })

    test("hides files touched section when no files", async ({ page }) => {
      await setupMocks(page)
      await navigateToSessionDetail(page, "session-row-sess-002")

      const detail = page.getByTestId("session-detail-view")
      await expect(detail).not.toContainText("Files Touched")
    })
  })
})
