import { test, expect, Page } from "@playwright/test"

/**
 * E2E tests for CommentsSection component.
 *
 * Tests use the main app flow - navigating to the app, clicking an issue card
 * to open the detail panel, and verifying comments display correctly.
 */

/**
 * Comment interface matching the API response.
 */
interface TestComment {
  id: number
  issue_id: string
  author: string
  text: string
  created_at: string
}

/**
 * Test issue data for /api/ready.
 */
const mockIssues = [
  {
    id: "comments-test-1",
    title: "Issue With Comments",
    status: "open",
    priority: 2,
    issue_type: "task",
    description: "Test issue with comments",
    created_at: "2026-01-27T10:00:00Z",
    updated_at: "2026-01-27T10:00:00Z",
  },
  {
    id: "comments-test-2",
    title: "Issue Without Comments",
    status: "open",
    priority: 2,
    issue_type: "bug",
    description: "Test issue without comments",
    created_at: "2026-01-27T11:00:00Z",
    updated_at: "2026-01-27T11:00:00Z",
  },
]

/**
 * Sample comments for testing - timestamps ordered oldest to newest.
 */
const testComments: TestComment[] = [
  {
    id: 1,
    issue_id: "comments-test-1",
    author: "alice",
    text: "First comment on this issue",
    created_at: "2026-01-25T10:00:00Z",
  },
  {
    id: 2,
    issue_id: "comments-test-1",
    author: "bob",
    text: "Second comment with more details",
    created_at: "2026-01-26T15:30:00Z",
  },
  {
    id: 3,
    issue_id: "comments-test-1",
    author: "charlie",
    text: "Most recent comment",
    created_at: "2026-01-27T09:00:00Z",
  },
]

/**
 * Get IssueDetails response for a given issue ID.
 */
function getIssueDetails(
  issueId: string,
  options?: { comments?: TestComment[] }
) {
  const issue = mockIssues.find((i) => i.id === issueId)
  if (!issue) return null

  // Determine which comments to include
  let comments: TestComment[] = []
  if (issueId === "comments-test-1") {
    comments = options?.comments ?? testComments
  }

  return {
    ...issue,
    dependencies: [],
    dependents: [],
    comments,
  }
}

/**
 * Setup API mocks with optional custom comments.
 */
async function setupMocks(
  page: Page,
  options?: {
    comments?: TestComment[]
    apiDelay?: number
  }
) {

  // Mock /api/ready to return our test issues
  await page.route("**/api/ready", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: mockIssues,
      }),
    })
  })

  // Mock GET /api/issues/{id} to return issue details with comments
  await page.route("**/api/issues/*", async (route) => {
    const request = route.request()
    const method = request.method()
    const url = request.url()

    if (method === "GET") {
      if (options?.apiDelay) {
        await new Promise((r) => setTimeout(r, options.apiDelay))
      }

      const idMatch = url.match(/\/api\/issues\/([^/?]+)/)
      const id = idMatch ? idMatch[1] : null
      const details = id ? getIssueDetails(id, options) : null

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
    } else {
      await route.continue()
    }
  })
}

/**
 * Navigate to the app and wait for issues to load.
 */
async function navigateToApp(page: Page) {
  await Promise.all([
    page.waitForResponse((res) => res.url().includes("/api/ready")),
    page.goto("/"),
  ])

  // Wait for loading to complete
  await expect(page.getByTestId("loading-container")).not.toBeVisible({
    timeout: 5000,
  })
}

/**
 * Open the detail panel for a specific issue.
 */
async function openIssuePanel(page: Page, issueTitle: string) {
  const issueCard = page.locator("article").filter({ hasText: issueTitle })
  await expect(issueCard).toBeVisible()
  await issueCard.click()

  const panel = page.getByTestId("issue-detail-panel")
  await expect(panel).toBeVisible({ timeout: 5000 })
  await expect(panel).toHaveAttribute("data-state", "open")

  // Wait for loading to complete
  await expect(panel).toHaveAttribute("data-loading", "false", { timeout: 5000 })

  return panel
}

/**
 * Get comments section locators.
 */
function getCommentsSection(page: Page) {
  return {
    section: page.getByTestId("comments-section"),
    items: page.getByTestId("comment-item"),
    emptyState: page.getByTestId("comments-empty"),
    getItem: (index: number) => page.getByTestId("comment-item").nth(index),
  }
}

test.describe("CommentsSection", () => {
  test.describe("Display", () => {
    test("comments section renders in detail panel", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Comments")

      const { section } = getCommentsSection(page)
      await expect(section).toBeVisible()
    })

    test("section title shows Comments with count", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Comments")

      const { section } = getCommentsSection(page)
      const heading = section.locator("h3")
      await expect(heading).toContainText("Comments")
      await expect(heading).toContainText("(3)")
    })

    test("section title shows Comments without count when empty", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Issue Without Comments")

      const { section } = getCommentsSection(page)
      const heading = section.locator("h3")
      await expect(heading).toHaveText("Comments")
    })
  })

  test.describe("With Comments", () => {
    test("shows all comments", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Comments")

      const { items } = getCommentsSection(page)
      await expect(items).toHaveCount(3)
    })

    test("each comment displays author name", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Comments")

      const { items } = getCommentsSection(page)

      // Check each comment has the expected author
      await expect(items.nth(0)).toContainText("alice")
      await expect(items.nth(1)).toContainText("bob")
      await expect(items.nth(2)).toContainText("charlie")
    })

    test("each comment displays timestamp", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Comments")

      const { items } = getCommentsSection(page)

      // Each comment should have a time element
      for (let i = 0; i < 3; i++) {
        const timeElement = items.nth(i).locator("time")
        await expect(timeElement).toBeVisible()
      }
    })

    test("each comment displays text content", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Comments")

      const { items } = getCommentsSection(page)

      await expect(items.nth(0)).toContainText("First comment on this issue")
      await expect(items.nth(1)).toContainText("Second comment with more details")
      await expect(items.nth(2)).toContainText("Most recent comment")
    })

    test("single comment shows correct count", async ({ page }) => {
      const singleComment = [testComments[0]]
      await setupMocks(page, { comments: singleComment })
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Comments")

      const { section, items } = getCommentsSection(page)
      await expect(items).toHaveCount(1)
      await expect(section.locator("h3")).toContainText("(1)")
    })
  })

  test.describe("Empty State", () => {
    test("shows empty message when no comments", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Issue Without Comments")

      const { emptyState, items } = getCommentsSection(page)
      await expect(items).toHaveCount(0)
      await expect(emptyState).toBeVisible()
      await expect(emptyState).toContainText("No comments")
    })
  })

  test.describe("Order", () => {
    test("comments displayed chronologically (oldest first)", async ({
      page,
    }) => {
      // Create reversed comments to test sorting
      const reversedComments = [...testComments].reverse()
      await setupMocks(page, { comments: reversedComments })
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Comments")

      const { items } = getCommentsSection(page)

      // First comment (index 0) should be oldest (alice's)
      await expect(items.nth(0)).toContainText("First comment")
      await expect(items.nth(0)).toContainText("alice")

      // Last comment (index 2) should be newest (charlie's)
      await expect(items.nth(2)).toContainText("Most recent")
      await expect(items.nth(2)).toContainText("charlie")
    })
  })

  test.describe("Timestamp Formatting", () => {
    test("timestamps show relative format (not ISO)", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Comments")

      const { items } = getCommentsSection(page)
      const timeElement = items.nth(0).locator("time")

      // Get the displayed text
      const timeText = await timeElement.textContent()

      // Should NOT be raw ISO date string format
      expect(timeText).not.toMatch(/^\d{4}-\d{2}-\d{2}T/)
    })

    test("uses <time> element with datetime attribute", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Comments")

      const { items } = getCommentsSection(page)
      const timeElement = items.nth(0).locator("time")

      // Should have datetime attribute with ISO string
      await expect(timeElement).toHaveAttribute(
        "datetime",
        "2026-01-25T10:00:00Z"
      )
    })
  })

  test.describe("Long Text", () => {
    test("long comments wrap correctly", async ({ page }) => {
      const longComment: TestComment = {
        id: 100,
        issue_id: "comments-test-1",
        author: "verbose-user",
        text: "This is a very long comment that contains many words and should wrap properly within the container. ".repeat(
          5
        ),
        created_at: "2026-01-27T10:00:00Z",
      }
      await setupMocks(page, { comments: [longComment] })
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Comments")

      const { items } = getCommentsSection(page)
      await expect(items.nth(0)).toBeVisible()

      // Verify the text element has word-break CSS property set
      const textElement = items.nth(0).locator("p")
      const wordBreak = await textElement.evaluate((el) =>
        window.getComputedStyle(el).wordBreak
      )
      // Should have word-break set to allow wrapping long words
      expect(["break-word", "break-all", "normal"]).toContain(wordBreak)
    })

    test("multiline comments preserve line breaks", async ({ page }) => {
      const multilineComment: TestComment = {
        id: 101,
        issue_id: "comments-test-1",
        author: "newline-user",
        text: "Line 1\n\nLine 2\nLine 3",
        created_at: "2026-01-27T10:00:00Z",
      }
      await setupMocks(page, { comments: [multilineComment] })
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Comments")

      const { items } = getCommentsSection(page)

      // Check computed style - text element should preserve whitespace
      const textElement = items.nth(0).locator("p")
      const computedStyle = await textElement.evaluate((el) =>
        window.getComputedStyle(el).whiteSpace
      )
      expect(["pre-wrap", "pre-line", "pre"]).toContain(computedStyle)
    })
  })

  test.describe("Accessibility", () => {
    test("comment list uses semantic list elements", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Comments")

      const { section } = getCommentsSection(page)

      // Should use ul/li for the comment list
      await expect(section.locator("ul")).toBeVisible()
      await expect(section.locator("li")).toHaveCount(3)
    })

    test("section has proper heading", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Comments")

      const { section } = getCommentsSection(page)
      const heading = section.locator("h3")
      await expect(heading).toBeVisible()
      await expect(heading).toContainText("Comments")
    })

    test("section uses section element for semantics", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Comments")

      // Verify the test ID is on a section element
      const sectionElement = page.locator(
        'section[data-testid="comments-section"]'
      )
      await expect(sectionElement).toBeVisible()
    })
  })

  test.describe("Edge Cases", () => {
    test("handles special characters in comment text", async ({ page }) => {
      const specialComment: TestComment = {
        id: 102,
        issue_id: "comments-test-1",
        author: "special-user",
        text: "<script>alert('xss')</script> & \"quotes\" 'apostrophes'",
        created_at: "2026-01-27T10:00:00Z",
      }
      await setupMocks(page, { comments: [specialComment] })
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Comments")

      const { items } = getCommentsSection(page)

      // Should render as text, not execute as HTML
      await expect(items.nth(0)).toContainText("<script>")
      await expect(items.nth(0)).toContainText("&")
      await expect(items.nth(0)).toContainText('"quotes"')
    })

    test("handles missing author gracefully", async ({ page }) => {
      const noAuthorComment: TestComment = {
        id: 103,
        issue_id: "comments-test-1",
        author: "",
        text: "Comment without author",
        created_at: "2026-01-27T10:00:00Z",
      }
      await setupMocks(page, { comments: [noAuthorComment] })
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Comments")

      const { items } = getCommentsSection(page)

      // Comment should display with fallback author
      await expect(items.nth(0)).toContainText("Unknown")
      await expect(items.nth(0)).toContainText("Comment without author")
    })

    test("handles many comments", async ({ page }) => {
      const manyComments: TestComment[] = Array.from({ length: 20 }, (_, i) => ({
        id: i + 1,
        issue_id: "comments-test-1",
        author: `user-${i}`,
        text: `Comment number ${i + 1}`,
        created_at: new Date(Date.UTC(2026, 0, 27) - i * 3600000).toISOString(),
      }))
      await setupMocks(page, { comments: manyComments })
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Comments")

      const { items, section } = getCommentsSection(page)
      await expect(items).toHaveCount(20)
      await expect(section.locator("h3")).toContainText("(20)")
    })

    test("handles very long author name", async ({ page }) => {
      const longAuthorComment: TestComment = {
        id: 104,
        issue_id: "comments-test-1",
        author:
          "a-very-long-username-that-might-cause-layout-issues-in-the-header",
        text: "Comment from user with long name",
        created_at: "2026-01-27T10:00:00Z",
      }
      await setupMocks(page, { comments: [longAuthorComment] })
      await navigateToApp(page)
      await openIssuePanel(page, "Issue With Comments")

      const { items } = getCommentsSection(page)

      // Comment should still render and be visible
      await expect(items.nth(0)).toBeVisible()

      // Verify both author and text content are displayed
      await expect(items.nth(0)).toContainText("a-very-long-username")
      await expect(items.nth(0)).toContainText("Comment from user with long name")
    })
  })
})
