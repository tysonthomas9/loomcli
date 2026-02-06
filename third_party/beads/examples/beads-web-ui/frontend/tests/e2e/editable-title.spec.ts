import { test, expect, Page } from "@playwright/test"

/**
 * E2E tests for EditableTitle component.
 *
 * These tests use the test fixture route (/test/issue-detail-panel) that renders
 * the IssueDetailPanel directly with URL-specified issue parameters.
 */

/**
 * Test issue data.
 */
interface TestIssue {
  id: string
  title: string
  status: string
  priority: number
  issue_type?: string
  description?: string
}

const testIssue: TestIssue = {
  id: "title-test-1",
  title: "Original Issue Title",
  status: "open",
  priority: 2,
  issue_type: "task",
  description: "Test description",
}

/**
 * Build the test fixture URL for an issue.
 */
function buildTestUrl(issue: TestIssue): string {
  const params = new URLSearchParams({
    id: issue.id,
    title: issue.title,
    status: issue.status,
    priority: String(issue.priority),
  })
  if (issue.issue_type) params.set("issue_type", issue.issue_type)
  if (issue.description) params.set("description", issue.description)
  return `/test/issue-detail-panel?${params.toString()}`
}

/**
 * Setup API mocks for testing.
 */
async function setupMocks(
  page: Page,
  options?: {
    patchDelay?: number
    patchError?: boolean
    onPatch?: (body: { title?: string }) => void
  }
) {
  // Track patch calls
  const patchCalls: { url: string; body: { title?: string } }[] = []

  // Mutable issue state for persistence tests
  let currentTitle = testIssue.title

  // Mock GET and PATCH /api/issues/{id}
  await page.route("**/api/issues/*", async (route) => {
    const request = route.request()
    const url = request.url()
    const method = request.method()

    if (method === "GET") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          ...testIssue,
          title: currentTitle,
          created_at: "2026-01-27T10:00:00Z",
          updated_at: "2026-01-27T10:00:00Z",
          dependencies: [],
          dependents: [],
        }),
      })
    } else if (method === "PATCH") {
      if (options?.patchDelay) {
        await new Promise((r) => setTimeout(r, options.patchDelay))
      }

      const body = request.postDataJSON() as { title?: string }
      patchCalls.push({ url, body })
      options?.onPatch?.(body)

      if (options?.patchError) {
        await route.fulfill({
          status: 500,
          contentType: "application/json",
          body: JSON.stringify({ success: false, error: "Server error" }),
        })
        return
      }

      // Update mutable state for persistence
      if (body.title !== undefined) {
        currentTitle = body.title
      }

      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          success: true,
          data: {
            ...testIssue,
            title: currentTitle,
            updated_at: new Date().toISOString(),
          },
        }),
      })
    } else {
      await route.continue()
    }
  })

  return { patchCalls }
}

/**
 * Navigate to the test fixture and wait for the panel to be visible.
 */
async function openTestPanel(page: Page, issue: TestIssue = testIssue) {
  const url = buildTestUrl(issue)
  await page.goto(url)

  // Wait for panel to be visible
  const panel = page.getByTestId("issue-detail-panel")
  await expect(panel).toBeVisible({ timeout: 5000 })

  return panel
}

/**
 * Get the editable title elements.
 */
function getEditableTitle(page: Page) {
  return {
    display: page.getByTestId("editable-title-display"),
    input: page.getByTestId("editable-title-input"),
    error: page.getByTestId("title-error"),
    saving: page.getByTestId("title-saving"),
  }
}

test.describe("EditableTitle", () => {
  test.describe("Enter Edit Mode", () => {
    test("click title enters edit mode with focused input", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page)

      const { display, input } = getEditableTitle(page)

      // Click display element to enter edit mode
      await display.click()

      // Input should appear and be focused
      await expect(input).toBeVisible()
      await expect(input).toBeFocused()
      await expect(input).toHaveValue(testIssue.title)
    })

    test("edit icon hint exists in display mode", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page)

      const { display } = getEditableTitle(page)

      // Edit icon SVG should exist within the display element
      const editIcon = display.locator("svg")
      await expect(editIcon).toBeAttached()
    })

    test("input contains current title value", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page)

      const { display, input } = getEditableTitle(page)

      await display.click()
      await expect(input).toHaveValue(testIssue.title)
    })
  })

  test.describe("Save on Enter", () => {
    test("typing new title and pressing Enter saves", async ({ page }) => {
      const { patchCalls } = await setupMocks(page)
      await openTestPanel(page)

      const { display, input } = getEditableTitle(page)

      // Enter edit mode
      await display.click()

      // Clear and type new title
      await input.clear()
      await input.fill("New Issue Title")

      // Set up response waiter before pressing Enter
      const patchPromise = page.waitForResponse(
        (res) =>
          res.url().includes(`/api/issues/${testIssue.id}`) &&
          res.request().method() === "PATCH"
      )

      // Press Enter to save
      await input.press("Enter")

      // Wait for PATCH call
      await patchPromise

      // Verify API was called with correct payload
      expect(patchCalls).toHaveLength(1)
      expect(patchCalls[0].body).toEqual({ title: "New Issue Title" })

      // Should return to display mode
      await expect(input).not.toBeVisible()
      await expect(display).toBeAttached()
    })

    // Note: Persistence test removed - test fixture parses issue from URL params on load,
    // not from API, so refresh always shows original URL params. The API call is verified
    // in the "typing new title and pressing Enter saves" test.
  })

  test.describe("Cancel on Escape", () => {
    test("Escape key discards changes and returns to display mode", async ({ page }) => {
      let patchCallCount = 0
      await setupMocks(page, {
        onPatch: () => {
          patchCallCount++
        },
      })
      await openTestPanel(page)

      const { display, input } = getEditableTitle(page)

      // Enter edit mode
      await display.click()

      // Type different title
      await input.clear()
      await input.fill("This should be discarded")

      // Press Escape
      await input.press("Escape")

      // Should return to display mode (input not visible)
      await expect(input).not.toBeVisible()

      // No API call should be made
      expect(patchCallCount).toBe(0)
    })
  })

  test.describe("Validation", () => {
    test("empty title shows error message and stays in edit mode", async ({ page }) => {
      let patchCallCount = 0
      await setupMocks(page, {
        onPatch: () => {
          patchCallCount++
        },
      })
      await openTestPanel(page)

      const { display, input, error } = getEditableTitle(page)

      // Enter edit mode
      await display.click()

      // Clear input completely
      await input.clear()

      // Press Enter
      await input.press("Enter")

      // Error should be visible
      await expect(error).toBeVisible()
      await expect(error).toContainText("Title cannot be empty")

      // Should stay in edit mode
      await expect(input).toBeVisible()

      // No API call should be made
      expect(patchCallCount).toBe(0)
    })

    test("whitespace-only title shows error", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page)

      const { display, input, error } = getEditableTitle(page)

      // Enter edit mode
      await display.click()

      // Type only spaces
      await input.clear()
      await input.fill("   ")

      // Press Enter
      await input.press("Enter")

      // Error should be visible
      await expect(error).toBeVisible()
      await expect(error).toContainText("Title cannot be empty")
    })

    test("error has role alert for accessibility", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page)

      const { display, input, error } = getEditableTitle(page)

      // Trigger validation error
      await display.click()
      await input.clear()
      await input.press("Enter")

      // Verify error has role="alert"
      await expect(error).toHaveAttribute("role", "alert")
    })

    test("blur with empty title shows error and stays in edit mode", async ({ page }) => {
      let patchCallCount = 0
      await setupMocks(page, {
        onPatch: () => {
          patchCallCount++
        },
      })
      await openTestPanel(page)

      const { display, input, error } = getEditableTitle(page)

      // Enter edit mode
      await display.click()

      // Clear input
      await input.clear()

      // Blur by clicking outside
      await page.getByTestId("issue-detail-panel").click({ position: { x: 10, y: 10 } })

      // Error should be visible
      await expect(error).toBeVisible()
      await expect(error).toContainText("Title cannot be empty")

      // Should stay in edit mode (input refocused)
      await expect(input).toBeVisible()

      // No API call should be made
      expect(patchCallCount).toBe(0)
    })
  })

  test.describe("Save on Blur", () => {
    test("blur input with changes triggers save", async ({ page }) => {
      const { patchCalls } = await setupMocks(page)
      await openTestPanel(page)

      const { display, input } = getEditableTitle(page)

      // Enter edit mode
      await display.click()

      // Type new title
      await input.clear()
      await input.fill("Blur Save Title")

      // Set up response waiter
      const patchPromise = page.waitForResponse(
        (res) =>
          res.url().includes(`/api/issues/${testIssue.id}`) &&
          res.request().method() === "PATCH"
      )

      // Click outside to blur (click on the panel background)
      await page.getByTestId("issue-detail-panel").click({ position: { x: 10, y: 10 } })

      // Wait for PATCH call
      await patchPromise

      // Verify API was called
      expect(patchCalls).toHaveLength(1)
      expect(patchCalls[0].body.title).toBe("Blur Save Title")

      // Should return to display mode
      await expect(input).not.toBeVisible()
    })

    test("blur with unchanged title does not call API", async ({ page }) => {
      let patchCallCount = 0
      await setupMocks(page, {
        onPatch: () => {
          patchCallCount++
        },
      })
      await openTestPanel(page)

      const { display, input } = getEditableTitle(page)

      // Enter edit mode
      await display.click()

      // Don't change anything, just blur
      await page.getByTestId("issue-detail-panel").click({ position: { x: 10, y: 10 } })

      // Give time for any potential API call
      await page.waitForTimeout(200)

      // No API call should be made
      expect(patchCallCount).toBe(0)

      // Should return to display mode
      await expect(input).not.toBeVisible()
    })
  })

  test.describe("Loading State", () => {
    test("shows saving indicator during API call", async ({ page }) => {
      await setupMocks(page, { patchDelay: 500 })
      await openTestPanel(page)

      const { display, input, saving } = getEditableTitle(page)

      // Enter edit mode and change title
      await display.click()
      await input.clear()
      await input.fill("Loading State Test")

      // Start save
      const patchPromise = page.waitForResponse(
        (res) =>
          res.url().includes(`/api/issues/${testIssue.id}`) &&
          res.request().method() === "PATCH"
      )

      await input.press("Enter")

      // Saving indicator should be visible
      await expect(saving).toBeVisible()
      await expect(saving).toContainText("Saving...")

      // Input should be disabled during save
      await expect(input).toBeDisabled()

      // Wait for response
      await patchPromise

      // Saving indicator should be hidden
      await expect(saving).not.toBeVisible()

      // Should return to display mode
      await expect(input).not.toBeVisible()
    })
  })

  test.describe("Error Handling", () => {
    test("shows error on API failure and stays in edit mode", async ({ page }) => {
      await setupMocks(page, { patchError: true })
      await openTestPanel(page)

      const { display, input, error } = getEditableTitle(page)

      // Enter edit mode and change title
      await display.click()
      await input.clear()
      await input.fill("This will fail to save")

      // Set up response waiter
      const patchPromise = page.waitForResponse(
        (res) =>
          res.url().includes(`/api/issues/${testIssue.id}`) &&
          res.request().method() === "PATCH" &&
          res.status() === 500
      )

      // Attempt save
      await input.press("Enter")

      // Wait for failed PATCH
      await patchPromise

      // Error message should appear
      await expect(error).toBeVisible()
      await expect(error).toHaveAttribute("role", "alert")

      // Should stay in edit mode with content preserved
      await expect(input).toBeVisible()
      await expect(input).toHaveValue("This will fail to save")

      // Input should be re-enabled for retry
      await expect(input).toBeEnabled()
    })

    test("can retry after error", async ({ page }) => {
      let patchCallCount = 0

      // Setup mocks: first PATCH fails, second succeeds
      let shouldFail = true
      await page.route("**/api/issues/*", async (route) => {
        const request = route.request()
        const method = request.method()

        if (method === "PATCH") {
          patchCallCount++
          if (shouldFail) {
            shouldFail = false
            await route.fulfill({
              status: 500,
              contentType: "application/json",
              body: JSON.stringify({ success: false, error: "Server error" }),
            })
          } else {
            const body = request.postDataJSON()
            await route.fulfill({
              status: 200,
              contentType: "application/json",
              body: JSON.stringify({
                success: true,
                data: {
                  ...testIssue,
                  ...body,
                  updated_at: new Date().toISOString(),
                },
              }),
            })
          }
        } else if (method === "GET") {
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({
              ...testIssue,
              created_at: "2026-01-27T10:00:00Z",
              updated_at: "2026-01-27T10:00:00Z",
              dependencies: [],
              dependents: [],
            }),
          })
        } else {
          await route.continue()
        }
      })

      await openTestPanel(page)

      const { display, input, error } = getEditableTitle(page)

      // Enter edit mode and change title
      await display.click()
      await input.clear()
      await input.fill("Retry test title")

      // Set up response waiter for first attempt
      const firstPatchPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/") && res.request().method() === "PATCH"
      )

      // First save attempt (fails)
      await input.press("Enter")
      await firstPatchPromise

      // Error should be visible
      await expect(error).toBeVisible()

      // Set up response waiter for second attempt
      const secondPatchPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/") &&
          res.request().method() === "PATCH" &&
          res.status() === 200
      )

      // Retry (should succeed)
      await input.press("Enter")
      await secondPatchPromise

      // Should return to display mode
      await expect(input).not.toBeVisible()
      expect(patchCallCount).toBe(2)
    })
  })

  test.describe("Keyboard Accessibility", () => {
    test("Enter key in display mode enters edit mode", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page)

      const { display, input } = getEditableTitle(page)

      // Focus display element
      await display.focus()

      // Press Enter
      await display.press("Enter")

      // Input should appear
      await expect(input).toBeVisible()
    })

    test("Space key in display mode enters edit mode", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page)

      const { display, input } = getEditableTitle(page)

      // Focus display element
      await display.focus()

      // Press Space
      await display.press(" ")

      // Input should appear
      await expect(input).toBeVisible()
    })

    test("display element is focusable with Tab", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page)

      const { display } = getEditableTitle(page)

      // Verify display element has tabIndex for keyboard access
      await expect(display).toHaveAttribute("tabindex", "0")
    })

    test("display element has button role for accessibility", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page)

      const { display } = getEditableTitle(page)

      // Verify display element has role="button"
      await expect(display).toHaveAttribute("role", "button")
    })
  })

  test.describe("Same Title Optimization", () => {
    test("saving unchanged title is a no-op", async ({ page }) => {
      let patchCallCount = 0
      await setupMocks(page, {
        onPatch: () => {
          patchCallCount++
        },
      })
      await openTestPanel(page)

      const { display, input } = getEditableTitle(page)

      // Enter edit mode
      await display.click()

      // Don't change anything, just press Enter
      await input.press("Enter")

      // Give time for any potential API call
      await page.waitForTimeout(200)

      // No API call should be made
      expect(patchCallCount).toBe(0)

      // Should still return to display mode
      await expect(input).not.toBeVisible()
    })
  })

  test.describe("Edge Cases", () => {
    test("very long title is handled", async ({ page }) => {
      const longTitle = "A".repeat(200)
      const longIssue = { ...testIssue, id: "title-long", title: longTitle }

      await page.route("**/api/issues/*", async (route) => {
        const method = route.request().method()
        if (method === "GET") {
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({
              ...longIssue,
              created_at: "2026-01-27T10:00:00Z",
              updated_at: "2026-01-27T10:00:00Z",
              dependencies: [],
              dependents: [],
            }),
          })
        } else {
          await route.continue()
        }
      })

      await openTestPanel(page, longIssue)

      const { display, input } = getEditableTitle(page)
      await expect(display).toBeVisible()

      // Enter edit mode
      await display.click()

      // Input should handle long content
      await expect(input).toBeVisible()
      const value = await input.inputValue()
      expect(value.length).toBe(200)
    })

    test("special characters in title render safely", async ({ page }) => {
      const specialIssue = {
        ...testIssue,
        id: "title-special",
        title: "<script>alert('xss')</script> & entities",
      }

      await page.route("**/api/issues/*", async (route) => {
        const method = route.request().method()
        if (method === "GET") {
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({
              ...specialIssue,
              created_at: "2026-01-27T10:00:00Z",
              updated_at: "2026-01-27T10:00:00Z",
              dependencies: [],
              dependents: [],
            }),
          })
        } else {
          await route.continue()
        }
      })

      await openTestPanel(page, specialIssue)

      const { display } = getEditableTitle(page)
      await expect(display).toBeVisible()

      // Content should render as text, not as executable HTML
      await expect(display).toContainText("script")
      await expect(display).toContainText("entities")
    })
  })
})
