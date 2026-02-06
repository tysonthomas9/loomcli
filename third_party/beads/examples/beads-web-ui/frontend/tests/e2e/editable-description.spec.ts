import { test, expect, Page } from "@playwright/test"

/**
 * E2E tests for EditableDescription component.
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

const testIssueWithDescription: TestIssue = {
  id: "desc-test-1",
  title: "Issue with Description",
  status: "open",
  priority: 2,
  issue_type: "task",
  description:
    "# Heading\n\nSome **bold** and *italic* text.\n\n- List item 1\n- List item 2\n\n`inline code`",
}

const testIssueNoDescription: TestIssue = {
  id: "desc-test-2",
  title: "Issue without Description",
  status: "open",
  priority: 2,
  issue_type: "task",
  description: "",
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
    onPatch?: (body: { description?: string }) => void
  }
) {
  // Track patch calls
  const patchCalls: { url: string; body: { description?: string } }[] = []

  // Mutable issue state for persistence tests
  let currentDescription = testIssueWithDescription.description

  // Mock GET and PATCH /api/issues/{id}
  await page.route("**/api/issues/*", async (route) => {
    const request = route.request()
    const url = request.url()
    const method = request.method()

    if (method === "GET") {
      const idMatch = url.match(/\/api\/issues\/([^/?]+)/)
      const id = idMatch ? idMatch[1] : null

      const baseIssue =
        id === testIssueNoDescription.id
          ? testIssueNoDescription
          : testIssueWithDescription

      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          ...baseIssue,
          description:
            id === testIssueNoDescription.id ? "" : currentDescription,
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

      const body = request.postDataJSON() as { description?: string }
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
      if (body.description !== undefined) {
        currentDescription = body.description
      }

      const idMatch = url.match(/\/api\/issues\/([^/?]+)/)
      const id = idMatch ? idMatch[1] : null
      const baseIssue =
        id === testIssueNoDescription.id
          ? testIssueNoDescription
          : testIssueWithDescription

      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          success: true,
          data: {
            ...baseIssue,
            description: currentDescription,
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
async function openTestPanel(page: Page, issue: TestIssue = testIssueWithDescription) {
  const url = buildTestUrl(issue)
  await page.goto(url)

  // Wait for panel to be visible
  const panel = page.getByTestId("issue-detail-panel")
  await expect(panel).toBeVisible({ timeout: 5000 })

  return panel
}

/**
 * Get the editable description component.
 */
function getEditableDescription(page: Page) {
  return page.getByTestId("editable-description")
}

test.describe("EditableDescription", () => {
  test.describe("View Mode", () => {
    test("renders description as markdown in view mode", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, testIssueWithDescription)

      const description = getEditableDescription(page)
      await expect(description).toBeVisible()

      // Verify markdown is rendered (not raw markdown syntax)
      const markdownContent = page.getByTestId("markdown-content")
      await expect(markdownContent).toBeVisible()

      // Check for rendered markdown elements
      await expect(description.locator("h1")).toContainText("Heading")
      await expect(description.locator("strong")).toContainText("bold")
      await expect(description.locator("em")).toContainText("italic")
      await expect(description.locator("li")).toHaveCount(2)
      await expect(description.locator("code")).toContainText("inline code")

      // Raw markdown syntax should not be visible
      await expect(description).not.toContainText("# Heading")
      await expect(description).not.toContainText("**bold**")
      await expect(description).not.toContainText("*italic*")
    })

    test("edit button appears on hover when editable", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, testIssueWithDescription)

      const description = getEditableDescription(page)
      const editButton = page.getByTestId("description-edit-button")

      // Edit button exists in the DOM
      await expect(editButton).toBeAttached()

      // Hover over description section
      await description.hover()

      // Edit button should be visible on hover
      await expect(editButton).toBeVisible()
    })
  })

  test.describe("Edit Mode", () => {
    test("click edit button enters edit mode with textarea and preview", async ({
      page,
    }) => {
      await setupMocks(page)
      await openTestPanel(page, testIssueWithDescription)

      // Click edit button
      const editButton = page.getByTestId("description-edit-button")
      await editButton.click()

      // Verify textarea appears with current description
      const textarea = page.getByTestId("description-textarea")
      await expect(textarea).toBeVisible()
      await expect(textarea).toHaveValue(testIssueWithDescription.description!)

      // Verify preview pane appears
      await expect(page.getByText("Preview")).toBeVisible()

      // Verify Save and Cancel buttons visible
      await expect(page.getByTestId("description-save")).toBeVisible()
      await expect(page.getByTestId("description-cancel")).toBeVisible()
    })

    test("textarea receives focus when entering edit mode", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, testIssueWithDescription)

      // Click edit button
      await page.getByTestId("description-edit-button").click()

      // Textarea should be focused
      const textarea = page.getByTestId("description-textarea")
      await expect(textarea).toBeFocused()
    })
  })

  test.describe("Live Preview", () => {
    test("preview updates as user types", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, testIssueWithDescription)

      // Enter edit mode
      await page.getByTestId("description-edit-button").click()

      const textarea = page.getByTestId("description-textarea")
      // In edit mode, there are two markdown renderers: one in textarea value (raw) and one in preview
      // The preview uses MarkdownRenderer which has data-testid="markdown-content"
      // There may be multiple markdown-content elements, so we get the one inside the editable description
      const editableDesc = getEditableDescription(page)

      // Clear and type new markdown content
      await textarea.clear()
      await textarea.fill("# New Heading\n\n**New bold text**")

      // Preview should show rendered markdown (h1 and strong elements exist)
      // The preview section contains rendered markdown elements
      await expect(editableDesc.locator("h1")).toContainText("New Heading")
      await expect(editableDesc.locator("strong")).toContainText("New bold text")

      // Verify markdown was rendered as HTML elements (not raw text)
      const previewHtml = await editableDesc.locator("[class*='previewContainer']").first().innerHTML()
      expect(previewHtml).toContain("<h1>")
      expect(previewHtml).toContain("<strong>")
    })

    test("markdown elements render correctly in preview", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, testIssueNoDescription)

      // Enter edit mode
      await page.getByTestId("description-edit-button").click()

      const textarea = page.getByTestId("description-textarea")
      const previewSection = getEditableDescription(page)

      // Type complex markdown
      await textarea.fill(
        "## Header 2\n\n**bold** and *italic*\n\n- List 1\n- List 2\n\n```\ncode block\n```\n\n[Link](http://example.com)"
      )

      // Verify rendered elements
      await expect(previewSection.locator("h2")).toContainText("Header 2")
      await expect(previewSection.locator("strong")).toContainText("bold")
      await expect(previewSection.locator("em")).toContainText("italic")
      await expect(previewSection.locator("li")).toHaveCount(2)
      await expect(previewSection.locator("a")).toHaveAttribute(
        "href",
        "http://example.com"
      )
    })
  })

  test.describe("Save", () => {
    test("save button calls API with new description", async ({ page }) => {
      const { patchCalls } = await setupMocks(page)
      await openTestPanel(page, testIssueWithDescription)

      // Enter edit mode
      await page.getByTestId("description-edit-button").click()

      // Change description
      const textarea = page.getByTestId("description-textarea")
      await textarea.clear()
      await textarea.fill("Updated description content")

      // Set up response waiter before clicking to avoid race condition
      const patchPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/desc-test-1") &&
          res.request().method() === "PATCH"
      )

      // Click Save
      const saveButton = page.getByTestId("description-save")
      await saveButton.click()

      // Wait for PATCH call
      await patchPromise

      // Verify API was called with correct payload
      expect(patchCalls).toHaveLength(1)
      expect(patchCalls[0].body).toEqual({
        description: "Updated description content",
      })

      // Should return to view mode
      await expect(page.getByTestId("description-textarea")).not.toBeVisible()
      await expect(page.getByTestId("description-edit-button")).toBeAttached()
    })

    test("Ctrl+Enter keyboard shortcut saves description", async ({ page }) => {
      const { patchCalls } = await setupMocks(page)
      await openTestPanel(page, testIssueWithDescription)

      // Enter edit mode
      await page.getByTestId("description-edit-button").click()

      // Change description
      const textarea = page.getByTestId("description-textarea")
      await textarea.clear()
      await textarea.fill("Keyboard save test")

      // Set up response waiter before pressing keys
      const patchPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/desc-test-1") &&
          res.request().method() === "PATCH"
      )

      // Use keyboard.down/up for explicit modifier key handling (Ctrl for Windows/Linux)
      await page.keyboard.down("Control")
      await page.keyboard.press("Enter")
      await page.keyboard.up("Control")

      // Wait for PATCH call
      await patchPromise

      // Verify save was triggered
      expect(patchCalls).toHaveLength(1)
      expect(patchCalls[0].body.description).toBe("Keyboard save test")
    })

    test("Cmd+Enter keyboard shortcut saves description (Mac)", async ({ page }) => {
      const { patchCalls } = await setupMocks(page)
      await openTestPanel(page, testIssueWithDescription)

      // Enter edit mode
      await page.getByTestId("description-edit-button").click()

      // Change description
      const textarea = page.getByTestId("description-textarea")
      await textarea.clear()
      await textarea.fill("Mac keyboard save test")

      // Set up response waiter before pressing keys
      const patchPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/desc-test-1") &&
          res.request().method() === "PATCH"
      )

      // Use keyboard.down/up for explicit modifier key handling (Meta/Cmd for Mac)
      await page.keyboard.down("Meta")
      await page.keyboard.press("Enter")
      await page.keyboard.up("Meta")

      // Wait for PATCH call
      await patchPromise

      // Verify save was triggered
      expect(patchCalls).toHaveLength(1)
      expect(patchCalls[0].body.description).toBe("Mac keyboard save test")
    })

    test("loading state shown during save", async ({ page }) => {
      await setupMocks(page, { patchDelay: 500 })
      await openTestPanel(page, testIssueWithDescription)

      // Enter edit mode and change text
      await page.getByTestId("description-edit-button").click()
      const textarea = page.getByTestId("description-textarea")
      await textarea.clear()
      await textarea.fill("Loading state test")

      // Start save
      const patchPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/desc-test-1") &&
          res.request().method() === "PATCH"
      )
      await page.getByTestId("description-save").click()

      // Textarea should be disabled during save
      await expect(textarea).toBeDisabled()

      // Save button should show "Saving..."
      await expect(page.getByTestId("description-save")).toContainText("Saving...")

      // Wait for response
      await patchPromise

      // Should return to view mode
      await expect(textarea).not.toBeVisible()
    })

    test("same description save is no-op", async ({ page }) => {
      let patchCallCount = 0
      await setupMocks(page, {
        onPatch: () => {
          patchCallCount++
        },
      })
      await openTestPanel(page, testIssueWithDescription)

      // Enter edit mode
      await page.getByTestId("description-edit-button").click()

      // Don't change anything, just save
      await page.getByTestId("description-save").click()

      // Give time for any potential API call
      await page.waitForTimeout(200)

      // No API call should be made
      expect(patchCallCount).toBe(0)

      // Should still return to view mode
      await expect(page.getByTestId("description-textarea")).not.toBeVisible()
    })
  })

  test.describe("Cancel", () => {
    test("cancel button discards changes", async ({ page }) => {
      let patchCallCount = 0
      await setupMocks(page, {
        onPatch: () => {
          patchCallCount++
        },
      })
      await openTestPanel(page, testIssueWithDescription)

      // Enter edit mode
      await page.getByTestId("description-edit-button").click()

      // Change description
      const textarea = page.getByTestId("description-textarea")
      await textarea.clear()
      await textarea.fill("This should be discarded")

      // Click Cancel
      await page.getByTestId("description-cancel").click()

      // Should return to view mode
      await expect(textarea).not.toBeVisible()

      // No API call should be made
      expect(patchCallCount).toBe(0)

      // Original description should be shown
      const markdownContent = page.getByTestId("markdown-content")
      await expect(markdownContent.locator("h1")).toContainText("Heading")
    })

    test("escape key discards changes", async ({ page }) => {
      let patchCallCount = 0
      await setupMocks(page, {
        onPatch: () => {
          patchCallCount++
        },
      })
      await openTestPanel(page, testIssueWithDescription)

      // Enter edit mode
      await page.getByTestId("description-edit-button").click()

      // Change description
      const textarea = page.getByTestId("description-textarea")
      await textarea.clear()
      await textarea.fill("This should be discarded too")

      // Press Escape
      await textarea.press("Escape")

      // Should return to view mode
      await expect(textarea).not.toBeVisible()

      // No API call should be made
      expect(patchCallCount).toBe(0)
    })
  })

  test.describe("Empty State", () => {
    test("shows placeholder for empty description", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, testIssueNoDescription)

      const description = getEditableDescription(page)
      await expect(description).toBeVisible()

      // Should show placeholder text
      await expect(description).toContainText("No description. Click to add one.")
    })

    test("can add description to empty issue", async ({ page }) => {
      const { patchCalls } = await setupMocks(page)
      await openTestPanel(page, testIssueNoDescription)

      // Click edit button (or placeholder)
      await page.getByTestId("description-edit-button").click()

      // Enter new description
      const textarea = page.getByTestId("description-textarea")
      await textarea.fill("# New Description\n\nFirst description content")

      // Set up response waiter before clicking save
      const patchPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/desc-test-2") &&
          res.request().method() === "PATCH"
      )

      // Save
      await page.getByTestId("description-save").click()

      // Wait for API call
      await patchPromise

      // Verify API was called with correct payload
      expect(patchCalls).toHaveLength(1)
      expect(patchCalls[0].body.description).toContain("New Description")

      // Should return to view mode (textarea no longer visible)
      await expect(textarea).not.toBeVisible()
      await expect(page.getByTestId("description-edit-button")).toBeAttached()
    })
  })

  test.describe("Error Handling", () => {
    test("shows error message on API failure", async ({ page }) => {
      await setupMocks(page, { patchError: true })
      await openTestPanel(page, testIssueWithDescription)

      // Enter edit mode and change text
      await page.getByTestId("description-edit-button").click()
      const textarea = page.getByTestId("description-textarea")
      await textarea.clear()
      await textarea.fill("This will fail to save")

      // Set up response waiter before clicking save
      const patchPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/desc-test-1") &&
          res.request().method() === "PATCH" &&
          res.status() === 500
      )

      // Attempt save
      await page.getByTestId("description-save").click()

      // Wait for failed PATCH
      await patchPromise

      // Error message should appear
      const errorAlert = page.getByTestId("description-error")
      await expect(errorAlert).toBeVisible()
      await expect(errorAlert).toHaveAttribute("role", "alert")

      // Should stay in edit mode with content preserved
      await expect(textarea).toBeVisible()
      await expect(textarea).toHaveValue("This will fail to save")

      // Textarea should be re-enabled for retry
      await expect(textarea).toBeEnabled()
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
                  ...testIssueWithDescription,
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
              ...testIssueWithDescription,
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

      await openTestPanel(page, testIssueWithDescription)

      // Enter edit mode and change text
      await page.getByTestId("description-edit-button").click()
      const textarea = page.getByTestId("description-textarea")
      await textarea.clear()
      await textarea.fill("Retry test content")

      // Set up response waiter before clicking save to avoid race condition
      const firstPatchPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/") &&
          res.request().method() === "PATCH"
      )

      // First save attempt (fails)
      await page.getByTestId("description-save").click()
      await firstPatchPromise

      // Error should be visible
      await expect(page.getByTestId("description-error")).toBeVisible()

      // Set up response waiter for second attempt
      const secondPatchPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/") &&
          res.request().method() === "PATCH" &&
          res.status() === 200
      )

      // Retry (should succeed)
      await page.getByTestId("description-save").click()
      await secondPatchPromise

      // Should return to view mode
      await expect(textarea).not.toBeVisible()
      expect(patchCallCount).toBe(2)
    })
  })

  test.describe("Keyboard Accessibility", () => {
    test("Enter in textarea does NOT save (allows multiline)", async ({ page }) => {
      let patchCallCount = 0
      await setupMocks(page, {
        onPatch: () => {
          patchCallCount++
        },
      })
      await openTestPanel(page, testIssueWithDescription)

      // Enter edit mode
      await page.getByTestId("description-edit-button").click()
      const textarea = page.getByTestId("description-textarea")
      await textarea.clear()
      await textarea.fill("Line 1")

      // Press Enter (should add newline, not save)
      await textarea.press("Enter")
      await textarea.type("Line 2")

      // Give time for any potential save
      await page.waitForTimeout(100)

      // No API call should be made
      expect(patchCallCount).toBe(0)

      // Should still be in edit mode with multiline content
      await expect(textarea).toBeVisible()
      await expect(textarea).toHaveValue("Line 1\nLine 2")
    })
  })

  test.describe("Edge Cases", () => {
    test("very long description is handled", async ({ page }) => {
      const longDescription = "# Long Content\n\n" + "Lorem ipsum. ".repeat(200)
      const longIssue = {
        ...testIssueWithDescription,
        id: "desc-long",
        description: longDescription,
      }

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

      const description = getEditableDescription(page)
      await expect(description).toBeVisible()

      // Enter edit mode
      await page.getByTestId("description-edit-button").click()

      // Textarea should handle long content
      const textarea = page.getByTestId("description-textarea")
      await expect(textarea).toBeVisible()
      const value = await textarea.inputValue()
      expect(value.length).toBeGreaterThan(1000)
    })

    test("special characters in description render safely", async ({ page }) => {
      const specialIssue = {
        ...testIssueWithDescription,
        id: "desc-special",
        description: "<script>alert('xss')</script> &amp; entities",
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

      const description = getEditableDescription(page)
      await expect(description).toBeVisible()

      // Script should NOT execute (would be escaped/sanitized)
      // Content should render as text, not as executable HTML
      await expect(description).toContainText("script")
      await expect(description).toContainText("entities")
    })
  })
})
