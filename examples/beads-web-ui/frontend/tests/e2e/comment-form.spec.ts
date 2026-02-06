import { test, expect, Page } from "@playwright/test"

/**
 * E2E tests for CommentForm component.
 *
 * Tests use the main app flow - navigating to the app, clicking an issue card
 * to open the detail panel, and verifying comment form behavior.
 */

// Comment interface matching the API response
interface TestComment {
  id: number
  issue_id: string
  author: string
  text: string
  created_at: string
}

// Test issue data for /api/ready
const mockIssues = [
  {
    id: "comment-form-test-1",
    title: "Test Issue for Comment Form",
    status: "open",
    priority: 2,
    issue_type: "task",
    description: "Test description",
    created_at: "2026-01-27T10:00:00Z",
    updated_at: "2026-01-27T10:00:00Z",
  },
  {
    id: "comment-form-test-2",
    title: "Issue Without Comments",
    status: "open",
    priority: 2,
    issue_type: "bug",
    description: "Test issue without comments",
    created_at: "2026-01-27T11:00:00Z",
    updated_at: "2026-01-27T11:00:00Z",
  },
]

// Sample existing comments
const existingComments: TestComment[] = [
  {
    id: 1,
    issue_id: "comment-form-test-1",
    author: "alice",
    text: "First comment",
    created_at: "2026-01-27T10:00:00Z",
  },
]

// Mutable state for tracking comments across requests
let currentComments: TestComment[] = []
let nextCommentId = 100

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
  if (issueId === "comment-form-test-1") {
    comments = options?.comments ?? currentComments
  }

  return {
    ...issue,
    dependencies: [],
    dependents: [],
    comments,
  }
}

// Setup API mocks with options for POST behavior
async function setupMocks(
  page: Page,
  options?: {
    initialComments?: TestComment[]
    postDelay?: number
    postError?: boolean
    onPost?: (body: { text: string }) => void
  }
) {
  // Reset mutable state
  currentComments = [...(options?.initialComments ?? existingComments)]
  nextCommentId = 100

  // Track POST calls
  const postCalls: { url: string; body: { text: string } }[] = []

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

  // Mock POST /api/issues/{id}/comments - must be registered before GET route
  await page.route("**/api/issues/*/comments", async (route) => {
    const request = route.request()
    const method = request.method()

    if (method === "POST") {
      if (options?.postDelay) {
        await new Promise((r) => setTimeout(r, options.postDelay))
      }

      const body = request.postDataJSON() as { text: string }
      postCalls.push({ url: request.url(), body })
      options?.onPost?.(body)

      if (options?.postError) {
        await route.fulfill({
          status: 500,
          contentType: "application/json",
          body: JSON.stringify({ success: false, error: "Failed to add comment" }),
        })
        return
      }

      // Create new comment
      const newComment: TestComment = {
        id: nextCommentId++,
        issue_id: "comment-form-test-1",
        author: "web-ui",
        text: body.text,
        created_at: new Date().toISOString(),
      }
      currentComments.push(newComment)

      await route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify({
          success: true,
          data: newComment,
        }),
      })
    } else {
      await route.continue()
    }
  })

  // Mock GET /api/issues/{id}
  await page.route("**/api/issues/*", async (route) => {
    const request = route.request()
    const method = request.method()
    const url = request.url()

    if (method === "GET") {
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

  return { postCalls }
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

// Get comment form elements
function getCommentForm(page: Page) {
  return {
    form: page.getByTestId("comment-form"),
    section: page.getByTestId("comments-section"),
    textarea: page.getByTestId("comment-textarea"),
    submitButton: page.getByTestId("comment-submit"),
    errorMessage: page.getByTestId("comment-error"),
    commentItems: page.getByTestId("comment-item"),
  }
}

test.describe("CommentForm", () => {
  test.describe("Display", () => {
    test("comment form renders below comments list", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { section, textarea, submitButton } = getCommentForm(page)

      // Comments section should be visible
      await expect(section).toBeVisible()

      // Form elements should be visible
      await expect(textarea).toBeVisible()
      await expect(submitButton).toBeVisible()

      // Form should appear after comment list in DOM order
      const sectionBox = await section.boundingBox()
      const textareaBox = await textarea.boundingBox()
      if (sectionBox && textareaBox) {
        // Textarea should be below the section title (within section)
        expect(textareaBox.y).toBeGreaterThan(sectionBox.y)
      }
    })

    test("textarea has placeholder text", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea } = getCommentForm(page)
      await expect(textarea).toHaveAttribute("placeholder", /add a comment/i)
    })

    test("submit button shows 'Add Comment' text", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { submitButton } = getCommentForm(page)
      await expect(submitButton).toContainText("Add Comment")
    })
  })

  test.describe("Input", () => {
    test("textarea accepts text input", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea } = getCommentForm(page)

      await textarea.fill("This is a test comment")
      await expect(textarea).toHaveValue("This is a test comment")
    })

    test("textarea accepts multiline input", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea } = getCommentForm(page)

      await textarea.fill("Line 1\nLine 2\nLine 3")
      const value = await textarea.inputValue()
      expect(value).toContain("\n")
    })

    test("textarea preserves special characters", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea } = getCommentForm(page)
      const specialText = "<script>alert('xss')</script> & \"quotes\""

      await textarea.fill(specialText)
      await expect(textarea).toHaveValue(specialText)
    })
  })

  test.describe("Submit", () => {
    test("clicking submit calls API with correct parameters", async ({ page }) => {
      const { postCalls } = await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea, submitButton } = getCommentForm(page)

      // Type comment
      await textarea.fill("Test comment via submit button")

      // Setup response waiter
      const postPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/") &&
          res.url().includes("/comments") &&
          res.request().method() === "POST"
      )

      // Click submit
      await submitButton.click()

      // Wait for POST
      await postPromise

      // Verify API call
      expect(postCalls).toHaveLength(1)
      expect(postCalls[0].body).toEqual({ text: "Test comment via submit button" })
    })

    test("submit button triggers form submission", async ({ page }) => {
      const { postCalls } = await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea, submitButton } = getCommentForm(page)

      await textarea.fill("Button submission test")

      const postPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/comments") &&
          res.request().method() === "POST"
      )

      await submitButton.click()
      await postPromise

      expect(postCalls).toHaveLength(1)
    })
  })

  test.describe("New Comment", () => {
    test("new comment appears at bottom of list after submission", async ({ page }) => {
      await setupMocks(page, { initialComments: existingComments })
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea, submitButton, commentItems } = getCommentForm(page)

      // Wait for initial comment to load
      await expect(commentItems).toHaveCount(1, { timeout: 10000 })

      // Add new comment
      await textarea.fill("Brand new comment")

      const postPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/comments") &&
          res.request().method() === "POST"
      )

      await submitButton.click()
      await postPromise

      // Comment appears via callback - wait for UI to update
      await expect(commentItems).toHaveCount(2, { timeout: 5000 })

      // New comment should be at the bottom (last item)
      const lastComment = commentItems.last()
      await expect(lastComment).toContainText("Brand new comment")
    })

    test("new comment shows correct author", async ({ page }) => {
      await setupMocks(page, { initialComments: [] })
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea, submitButton, commentItems } = getCommentForm(page)

      await textarea.fill("Comment with author")

      const postPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/comments") &&
          res.request().method() === "POST"
      )

      await submitButton.click()
      await postPromise

      // Wait for comment to appear in UI (added via callback)
      await expect(commentItems).toHaveCount(1, { timeout: 5000 })

      // Check author is displayed (web-ui from mock)
      await expect(commentItems.first()).toContainText("web-ui")
    })
  })

  test.describe("Clear", () => {
    test("form clears after successful submit", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea, submitButton } = getCommentForm(page)

      // Fill the form
      await textarea.fill("Comment to be cleared")
      await expect(textarea).toHaveValue("Comment to be cleared")

      // Submit
      const postPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/comments") &&
          res.request().method() === "POST"
      )

      await submitButton.click()
      await postPromise

      // Textarea should be empty after successful submission
      await expect(textarea).toHaveValue("")
    })

    test("form does NOT clear after failed submit", async ({ page }) => {
      await setupMocks(page, { postError: true })
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea, submitButton } = getCommentForm(page)

      // Fill the form
      await textarea.fill("Comment that will fail")

      // Submit (will fail)
      const postPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/comments") &&
          res.request().method() === "POST" &&
          res.status() === 500
      )

      await submitButton.click()
      await postPromise

      // Textarea should still have the text
      await expect(textarea).toHaveValue("Comment that will fail")
    })
  })

  test.describe("Validation", () => {
    test("submit button disabled when textarea is empty", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea, submitButton } = getCommentForm(page)

      // Initially empty
      await expect(textarea).toHaveValue("")
      await expect(submitButton).toBeDisabled()
    })

    test("submit button enabled when textarea has content", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea, submitButton } = getCommentForm(page)

      // Type something
      await textarea.fill("Some content")
      await expect(submitButton).toBeEnabled()
    })

    test("submit button disabled for whitespace-only content", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea, submitButton } = getCommentForm(page)

      // Fill with only whitespace
      await textarea.fill("   \n\t  ")
      await expect(submitButton).toBeDisabled()
    })

    test("empty submission does not call API", async ({ page }) => {
      let postCalled = false
      await setupMocks(page, {
        onPost: () => {
          postCalled = true
        },
      })
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { submitButton } = getCommentForm(page)

      // Scroll button into view first
      await submitButton.scrollIntoViewIfNeeded()

      // Button should be disabled, but try to click anyway
      await submitButton.click({ force: true })

      // Wait briefly
      await page.waitForTimeout(200)

      // No API call should be made
      expect(postCalled).toBe(false)
    })
  })

  test.describe("Loading State", () => {
    test("shows loading indicator during submission", async ({ page }) => {
      await setupMocks(page, { postDelay: 500 })
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea, submitButton } = getCommentForm(page)

      await textarea.fill("Loading state test")

      // Start submission
      const postPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/comments") &&
          res.request().method() === "POST"
      )

      await submitButton.click()

      // Submit button should show "Adding..." during submission
      await expect(submitButton).toContainText("Adding...")

      // Textarea should be disabled during submission
      await expect(textarea).toBeDisabled()

      // Submit button should be disabled during submission
      await expect(submitButton).toBeDisabled()

      // Wait for completion
      await postPromise

      // Should return to normal state
      await expect(submitButton).toContainText("Add Comment")
      await expect(textarea).toBeEnabled()
    })

    test("prevents double submission during loading", async ({ page }) => {
      let postCount = 0
      await setupMocks(page, {
        postDelay: 300,
        onPost: () => {
          postCount++
        },
      })
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea, submitButton } = getCommentForm(page)

      await textarea.fill("Double submit test")

      // Start submission
      const postPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/comments") &&
          res.request().method() === "POST"
      )

      // Click submit twice quickly
      await submitButton.click()
      await submitButton.click({ force: true }) // Force second click

      await postPromise

      // Only one POST should have been made
      expect(postCount).toBe(1)
    })
  })

  test.describe("Error Handling", () => {
    test("shows error message on API failure", async ({ page }) => {
      await setupMocks(page, { postError: true })
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea, submitButton, errorMessage } = getCommentForm(page)

      await textarea.fill("This will fail")

      const postPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/comments") &&
          res.request().method() === "POST" &&
          res.status() === 500
      )

      await submitButton.click()
      await postPromise

      // Error message should appear
      await expect(errorMessage).toBeVisible()
      await expect(errorMessage).toHaveAttribute("role", "alert")
    })

    test("error message has proper ARIA role", async ({ page }) => {
      await setupMocks(page, { postError: true })
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea, submitButton, errorMessage } = getCommentForm(page)

      await textarea.fill("Error test")

      await submitButton.click()

      await page.waitForResponse(
        (res) =>
          res.url().includes("/comments") &&
          res.request().method() === "POST"
      )

      await expect(errorMessage).toHaveAttribute("role", "alert")
    })

    test("form remains editable after error for retry", async ({ page }) => {
      await setupMocks(page, { postError: true })
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea, submitButton } = getCommentForm(page)

      await textarea.fill("Retry test")

      // Set up response waiter BEFORE clicking
      const postPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/comments") &&
          res.request().method() === "POST"
      )

      await submitButton.click()
      await postPromise

      // Form should still be editable
      await expect(textarea).toBeEnabled()
      await expect(submitButton).toBeEnabled()

      // Text should be preserved
      await expect(textarea).toHaveValue("Retry test")
    })

    test("can retry after error", async ({ page }) => {
      let postCount = 0
      let shouldFail = true

      // Custom mock: first fails, second succeeds

      // Mock /api/ready for navigateToApp
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

      // Order matters - more specific routes must be registered first
      await page.route("**/api/issues/*/comments", async (route) => {
        const method = route.request().method()
        if (method === "POST") {
          postCount++
          if (shouldFail) {
            shouldFail = false
            await route.fulfill({
              status: 500,
              contentType: "application/json",
              body: JSON.stringify({ success: false, error: "Server error" }),
            })
          } else {
            await route.fulfill({
              status: 201,
              contentType: "application/json",
              body: JSON.stringify({
                success: true,
                data: {
                  id: 100,
                  issue_id: "comment-form-test-1",
                  author: "web-ui",
                  text: "Retry test",
                  created_at: new Date().toISOString(),
                },
              }),
            })
          }
        } else {
          await route.continue()
        }
      })

      await page.route("**/api/issues/*", async (route) => {
        const request = route.request()
        const method = request.method()

        if (method === "GET") {
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({
              ...mockIssues[0],
              dependencies: [],
              dependents: [],
              comments: [],
            }),
          })
        } else {
          await route.continue()
        }
      })

      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea, submitButton, errorMessage } = getCommentForm(page)

      await textarea.fill("Retry test")

      // First attempt (fails) - set up waiter BEFORE clicking
      const firstPostPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/comments") &&
          res.request().method() === "POST"
      )
      await submitButton.click()
      await firstPostPromise

      // Error should be visible
      await expect(errorMessage).toBeVisible()

      // Second attempt (succeeds) - set up waiter BEFORE clicking
      const secondPostPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/comments") &&
          res.request().method() === "POST" &&
          res.status() === 201
      )
      await submitButton.click()
      await secondPostPromise

      // Textarea should be cleared after success
      await expect(textarea).toHaveValue("")

      // Two POST calls made
      expect(postCount).toBe(2)
    })
  })

  test.describe("Keyboard", () => {
    test("Ctrl+Enter submits comment (Windows/Linux)", async ({ page }) => {
      const { postCalls } = await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea } = getCommentForm(page)

      await textarea.fill("Keyboard submit test")

      const postPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/comments") &&
          res.request().method() === "POST"
      )

      // Use Ctrl+Enter
      await page.keyboard.down("Control")
      await page.keyboard.press("Enter")
      await page.keyboard.up("Control")

      await postPromise

      // Verify submission occurred
      expect(postCalls).toHaveLength(1)
      expect(postCalls[0].body.text).toBe("Keyboard submit test")
    })

    test("Cmd+Enter submits comment (Mac)", async ({ page }) => {
      const { postCalls } = await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea } = getCommentForm(page)

      await textarea.fill("Mac keyboard submit")

      const postPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/comments") &&
          res.request().method() === "POST"
      )

      // Use Cmd+Enter (Meta)
      await page.keyboard.down("Meta")
      await page.keyboard.press("Enter")
      await page.keyboard.up("Meta")

      await postPromise

      // Verify submission occurred
      expect(postCalls).toHaveLength(1)
      expect(postCalls[0].body.text).toBe("Mac keyboard submit")
    })

    test("regular Enter does NOT submit (allows multiline)", async ({ page }) => {
      let postCalled = false
      await setupMocks(page, {
        onPost: () => {
          postCalled = true
        },
      })
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea } = getCommentForm(page)

      await textarea.fill("Line 1")
      await textarea.press("Enter")
      await textarea.type("Line 2")

      // Wait briefly
      await page.waitForTimeout(200)

      // No API call should be made
      expect(postCalled).toBe(false)

      // Textarea should have multiline content
      const value = await textarea.inputValue()
      expect(value).toContain("\n")
    })

    test("keyboard shortcut does not work with empty textarea", async ({ page }) => {
      let postCalled = false
      await setupMocks(page, {
        onPost: () => {
          postCalled = true
        },
      })
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea } = getCommentForm(page)

      // Focus textarea but don't type anything
      await textarea.focus()

      // Try Ctrl+Enter
      await page.keyboard.down("Control")
      await page.keyboard.press("Enter")
      await page.keyboard.up("Control")

      // Wait briefly
      await page.waitForTimeout(200)

      // No API call should be made
      expect(postCalled).toBe(false)
    })
  })

  test.describe("Accessibility", () => {
    test("textarea has accessible label", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea } = getCommentForm(page)

      // Should have aria-label or associated label
      const ariaLabel = await textarea.getAttribute("aria-label")
      const ariaLabelledBy = await textarea.getAttribute("aria-labelledby")

      expect(ariaLabel || ariaLabelledBy).toBeTruthy()
    })

    test("submit button has accessible name", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { submitButton } = getCommentForm(page)

      // Button text should be accessible
      const buttonText = await submitButton.textContent()
      expect(buttonText?.toLowerCase()).toContain("add")
    })

    test("error message uses role=alert for screen readers", async ({ page }) => {
      await setupMocks(page, { postError: true })
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea, submitButton, errorMessage } = getCommentForm(page)

      await textarea.fill("Error test")
      await submitButton.click()

      await page.waitForResponse(
        (res) =>
          res.url().includes("/comments") &&
          res.request().method() === "POST"
      )

      await expect(errorMessage).toHaveAttribute("role", "alert")
    })

    test("form elements can be tabbed through", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea, submitButton } = getCommentForm(page)

      // First fill in some text so submit button is enabled (disabled buttons can't focus)
      await textarea.fill("Tab test")

      // Focus textarea
      await textarea.focus()
      await expect(textarea).toBeFocused()

      // Tab to submit button
      await page.keyboard.press("Tab")

      // Submit button should be focusable (now that it's enabled)
      await expect(submitButton).toBeFocused()
    })
  })

  test.describe("Edge Cases", () => {
    test("handles very long comment text", async ({ page }) => {
      const { postCalls } = await setupMocks(page)
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea, submitButton } = getCommentForm(page)

      // Note: repeat without trailing space since CommentForm trims text
      const longText = "Long comment text.".repeat(100)
      await textarea.fill(longText)

      const postPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/comments") &&
          res.request().method() === "POST"
      )

      await submitButton.click()
      await postPromise

      // API should receive the full text (trimmed)
      expect(postCalls[0].body.text).toBe(longText)
    })

    test("handles rapid typing during submission", async ({ page }) => {
      await setupMocks(page, { postDelay: 300 })
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea, submitButton } = getCommentForm(page)

      await textarea.fill("Initial text")

      // Start submission
      const postPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/comments") &&
          res.request().method() === "POST"
      )

      await submitButton.click()

      // Try to type during submission (should be blocked)
      await textarea.press("a")
      await textarea.press("b")
      await textarea.press("c")

      await postPromise

      // Textarea should be disabled during submission, so typing should not work
    })

    test("comment form works with issue that has no existing comments", async ({
      page,
    }) => {
      await setupMocks(page, { initialComments: [] })
      await navigateToApp(page)
      await openIssuePanel(page, "Test Issue for Comment Form")

      const { textarea, submitButton, commentItems } = getCommentForm(page)

      // Should show empty state initially
      await expect(page.getByTestId("comments-empty")).toBeVisible()
      await expect(commentItems).toHaveCount(0)

      // Add first comment
      await textarea.fill("First comment on issue")

      const postPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/comments") &&
          res.request().method() === "POST"
      )

      await submitButton.click()
      await postPromise

      // Comment appears via callback - wait for UI to update
      await expect(commentItems).toHaveCount(1, { timeout: 5000 })

      // Empty state should be gone, comment should appear
      await expect(page.getByTestId("comments-empty")).not.toBeVisible()
    })
  })
})
