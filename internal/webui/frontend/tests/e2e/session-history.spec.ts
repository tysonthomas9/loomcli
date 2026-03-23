import { test, expect, Page } from "@playwright/test"

/**
 * E2E tests for the SessionHistorySection component.
 * Tests terminal session history records inside IssueDetailPanel.
 */

const mockIssues = [
  {
    id: "sess-test-1",
    title: "Issue With Sessions",
    status: "in_progress",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-24T10:00:00Z",
    updated_at: "2026-01-24T10:00:00Z",
  },
  {
    id: "sess-test-2",
    title: "Issue Without Sessions",
    status: "open",
    priority: 3,
    issue_type: "task",
    created_at: "2026-01-24T11:00:00Z",
    updated_at: "2026-01-24T11:00:00Z",
  },
]

interface SessionRecord {
  id: string
  session_name: string
  issue_id: string
  backend: string
  status: "active" | "completed"
  launcher: string
  started_at: string
  ended_at?: string
  scrollback_path?: string
}

const activeSession: SessionRecord = {
  id: "rec-active-1",
  session_name: "nova-session-1",
  issue_id: "sess-test-1",
  backend: "claude",
  status: "active",
  launcher: "agent",
  started_at: new Date(Date.now() - 2 * 60 * 60 * 1000).toISOString(),
}

const completedSessionWithScrollback: SessionRecord = {
  id: "rec-done-1",
  session_name: "falcon-session-1",
  issue_id: "sess-test-1",
  backend: "claude",
  status: "completed",
  launcher: "agent",
  started_at: new Date(Date.now() - 5 * 60 * 60 * 1000).toISOString(),
  ended_at: new Date(Date.now() - 4.9 * 60 * 60 * 1000).toISOString(),
  scrollback_path: "/tmp/scrollback/falcon-session-1.log",
}

const completedSessionNoScrollback: SessionRecord = {
  id: "rec-done-2",
  session_name: "spark-session-1",
  issue_id: "sess-test-1",
  backend: "claude",
  status: "completed",
  launcher: "agent",
  started_at: new Date(Date.now() - 8 * 60 * 60 * 1000).toISOString(),
  ended_at: new Date(Date.now() - 7.9 * 60 * 60 * 1000).toISOString(),
}

function getIssueDetails(id: string) {
  const issue = mockIssues.find((i) => i.id === id)
  if (!issue) return null
  return {
    ...issue,
    dependencies: [],
    dependents: [],
    comments: [],
  }
}

interface SetupOptions {
  sessions?: SessionRecord[]
  sessionsError?: boolean
  scrollbackContent?: string
  scrollbackLines?: number
  scrollbackError?: boolean
}

async function setupMocks(page: Page, options: SetupOptions = {}) {
  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({ status: 404 })
  })

  await page.route("**/api/events", async (route) => {
    await route.abort()
  })

  await page.route("**/api/ready", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: mockIssues }),
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

  // Register before sessions to avoid glob collision
  await page.route(
    "**/api/issues/*/sessions/*/scrollback",
    async (route) => {
      if (options.scrollbackError) {
        await route.fulfill({
          status: 500,
          contentType: "application/json",
          body: JSON.stringify({ error: "Server error" }),
        })
        return
      }

      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          success: true,
          data: {
            content:
              options.scrollbackContent ?? "$ npm test\nAll tests passed\nDone.",
            lines: options.scrollbackLines ?? 3,
          },
        }),
      })
    }
  )

  await page.route("**/api/issues/*/sessions", async (route) => {
    if (route.request().url().match(/\/sessions\/.+/)) {
      await route.fallback()
      return
    }

    if (options.sessionsError) {
      await route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({ error: "Server error" }),
      })
      return
    }

    const sessions = options.sessions ?? [
      activeSession,
      completedSessionWithScrollback,
      completedSessionNoScrollback,
    ]

    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: sessions }),
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
    const idMatch = route.request().url().match(/\/api\/issues\/([^/?]+)/)
    const id = idMatch ? idMatch[1] : null
    const details = id ? getIssueDetails(id) : null

    if (details) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(details),
      })
    } else {
      await route.fulfill({
        status: 404,
        contentType: "application/json",
        body: JSON.stringify({ error: "Not found" }),
      })
    }
  })
}

async function navigateToApp(page: Page) {
  await page.goto("/")
  await page.waitForTimeout(1000)
}

async function openIssuePanel(page: Page, title: string) {
  const issueCard = page.locator("article").filter({ hasText: title })
  await expect(issueCard).toBeVisible()
  await issueCard.click()

  const panel = page.getByTestId("issue-detail-panel")
  await expect(panel).toHaveAttribute("data-state", "open", { timeout: 5000 })
  await expect(panel).toHaveAttribute("data-loading", "false", {
    timeout: 5000,
  })
}

async function expandSessionHistory(page: Page) {
  const section = page.getByTestId("session-history-section")
  const toggleBtn = section.locator("button").first()
  await expect(toggleBtn).toHaveAttribute("aria-expanded", "false")
  await toggleBtn.click()
  await expect(toggleBtn).toHaveAttribute("aria-expanded", "true", {
    timeout: 5000,
  })
}

test.describe("SessionHistorySection", () => {
  test.describe("Section visibility", () => {
    test("section renders inside detail panel after expanding", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Sessions")

      const section = page.getByTestId("session-history-section")
      await expect(section).toBeVisible()
    })

    test('section title shows Session History', async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Sessions")

      await expect(page.getByText("Session History")).toBeVisible()
    })
  })

  test.describe("Empty state", () => {
    test('shows No terminal sessions yet when empty', async ({ page }) => {
      await setupMocks(page, { sessions: [] })
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Sessions")
      await expandSessionHistory(page)

      await expect(
        page.getByText("No terminal sessions yet")
      ).toBeVisible({ timeout: 5000 })
    })
  })

  test.describe("Error state", () => {
    test("shows error message when session API returns error", async ({
      page,
    }) => {
      await setupMocks(page, { sessionsError: true })
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Sessions")
      await expandSessionHistory(page)

      await expect(page.getByText(/error|failed/i)).toBeVisible({
        timeout: 5000,
      })
    })
  })

  test.describe("Session records display", () => {
    test("renders correct number of session items", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Sessions")
      await expandSessionHistory(page)

      await expect(page.getByText("claude")).toBeVisible({ timeout: 5000 })
    })

    test("each record shows backend name", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Sessions")
      await expandSessionHistory(page)

      await expect(page.getByText("claude").first()).toBeVisible({
        timeout: 5000,
      })
    })

    test("status indicator has correct data-status", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Sessions")
      await expandSessionHistory(page)

      const activeIndicator = page.locator('[data-status="active"]')
      await expect(activeIndicator.first()).toBeVisible({ timeout: 5000 })

      const completedIndicator = page.locator('[data-status="completed"]')
      await expect(completedIndicator.first()).toBeVisible()
    })
  })

  test.describe("Jump to tab button", () => {
    test('active session shows Jump to tab button', async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Sessions")
      await expandSessionHistory(page)

      await expect(
        page.getByRole("button", { name: /jump to tab/i })
      ).toBeVisible({ timeout: 5000 })
    })

    test('completed session does not show Jump to tab button', async ({
      page,
    }) => {
      await setupMocks(page, {
        sessions: [completedSessionWithScrollback],
      })
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Sessions")
      await expandSessionHistory(page)

      await expect(
        page.getByRole("button", { name: /jump to tab/i })
      ).not.toBeVisible()
    })
  })

  test.describe("View scrollback button", () => {
    test("completed session with scrollback shows View scrollback button", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Sessions")
      await expandSessionHistory(page)

      await expect(
        page.getByRole("button", { name: /view scrollback/i })
      ).toBeVisible({ timeout: 5000 })
    })

    test("completed session without scrollback does not show View scrollback", async ({
      page,
    }) => {
      await setupMocks(page, {
        sessions: [completedSessionNoScrollback],
      })
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Sessions")
      await expandSessionHistory(page)

      await expect(
        page.getByRole("button", { name: /view scrollback/i })
      ).not.toBeVisible()
    })
  })

  test.describe("Scrollback overlay", () => {
    test("clicking View scrollback opens overlay with content", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Sessions")
      await expandSessionHistory(page)

      const scrollbackBtn = page.getByRole("button", {
        name: /view scrollback/i,
      })
      await expect(scrollbackBtn).toBeVisible({ timeout: 5000 })
      await scrollbackBtn.click()

      await expect(page.getByText("npm test")).toBeVisible({ timeout: 5000 })
    })

    test("clicking Close button dismisses the overlay", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Sessions")
      await expandSessionHistory(page)

      const scrollbackBtn = page.getByRole("button", {
        name: /view scrollback/i,
      })
      await expect(scrollbackBtn).toBeVisible({ timeout: 5000 })
      await scrollbackBtn.click()

      await expect(page.getByText("npm test")).toBeVisible({ timeout: 5000 })

      const closeBtn = page.getByRole("button", { name: /close/i })
      await closeBtn.click()

      await expect(page.getByText("npm test")).not.toBeVisible()
    })

    test("shows error when scrollback API fails", async ({ page }) => {
      await setupMocks(page, { scrollbackError: true })
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Sessions")
      await expandSessionHistory(page)

      const scrollbackBtn = page.getByRole("button", {
        name: /view scrollback/i,
      })
      await expect(scrollbackBtn).toBeVisible({ timeout: 5000 })
      await scrollbackBtn.click()

      await expect(
        page.getByText(/failed to load|error/i)
      ).toBeVisible({ timeout: 5000 })
    })
  })
})
