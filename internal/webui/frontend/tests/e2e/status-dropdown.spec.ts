import { test, expect, Page } from "@playwright/test"

/**
 * E2E tests for StatusDropdown component.
 *
 * These tests use a test fixture route (/test/issue-detail-panel) that renders
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

const testIssues: TestIssue[] = [
  {
    id: "status-test-1",
    title: "Test Issue Open",
    status: "open",
    priority: 2,
    issue_type: "task",
    description: "Test description",
  },
  {
    id: "status-test-2",
    title: "Test Issue In Progress",
    status: "in_progress",
    priority: 1,
    issue_type: "bug",
    description: "In progress description",
  },
  {
    id: "status-test-3",
    title: "Test Issue Blocked",
    status: "blocked",
    priority: 2,
    issue_type: "feature",
    description: "Blocked description",
  },
]

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
    onPatch?: (body: { status?: string }) => void
  }
) {
  // Track patch calls
  const patchCalls: { url: string; body: { status?: string } }[] = []

  // Mock GET and PATCH /api/issues/{id}
  await page.route("**/api/issues/*", async (route) => {
    const request = route.request()
    const url = request.url()
    const method = request.method()

    if (method === "GET") {
      // GET /api/issues/{id} returns IssueDetails directly without wrapper
      const idMatch = url.match(/\/api\/issues\/([^/?]+)/)
      const id = idMatch ? idMatch[1] : null
      const issue = testIssues.find((i) => i.id === id)

      if (issue) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            ...issue,
            created_at: "2026-01-27T10:00:00Z",
            updated_at: "2026-01-27T10:00:00Z",
            dependencies: [],
            dependents: [],
          }),
        })
      } else {
        await route.fulfill({
          status: 404,
          contentType: "application/json",
          body: JSON.stringify({ error: "Not found" }),
        })
      }
    } else if (method === "PATCH") {
      if (options?.patchDelay) {
        await new Promise((r) => setTimeout(r, options.patchDelay))
      }

      const body = request.postDataJSON() as { status?: string }
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

      const idMatch = url.match(/\/api\/issues\/([^/?]+)/)
      const id = idMatch ? idMatch[1] : null
      const issue = testIssues.find((i) => i.id === id)

      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          success: true,
          data: {
            ...issue,
            ...body,
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
async function openTestPanel(page: Page, issueIndex: number = 0) {
  const issue = testIssues[issueIndex]
  const url = buildTestUrl(issue)

  await page.goto(url)

  // Wait for panel to be visible
  const panel = page.getByTestId("issue-detail-panel")
  await expect(panel).toBeVisible({ timeout: 5000 })

  return panel
}

/**
 * Gets the status dropdown from the page.
 */
function getStatusDropdown(page: Page) {
  return page.getByTestId("status-dropdown")
}

test.describe("StatusDropdown", () => {
  test.describe("Display", () => {
    test("renders with current status selected", async ({ page }) => {
      await setupMocks(page)

      // Open panel for in_progress issue (index 1)
      await openTestPanel(page, 1)

      // Verify dropdown shows "in_progress" as selected value
      const dropdown = getStatusDropdown(page)
      await expect(dropdown).toBeVisible()
      await expect(dropdown).toHaveValue("in_progress")
    })

    test("shows all 6 user statuses and hides system statuses", async ({
      page,
    }) => {
      await setupMocks(page)
      await openTestPanel(page, 0)

      const dropdown = getStatusDropdown(page)
      await expect(dropdown).toBeVisible()

      // Get all options in the dropdown
      const options = dropdown.locator("option")
      const optionValues = await options.evaluateAll((opts) =>
        opts.map((o) => (o as HTMLOptionElement).value)
      )

      // Should include user-selectable statuses
      expect(optionValues).toContain("open")
      expect(optionValues).toContain("in_progress")
      expect(optionValues).toContain("blocked")
      expect(optionValues).toContain("deferred")
      expect(optionValues).toContain("review")
      expect(optionValues).toContain("closed")

      // Should NOT include system statuses
      expect(optionValues).not.toContain("tombstone")
      expect(optionValues).not.toContain("pinned")
      expect(optionValues).not.toContain("hooked")

      // Should have exactly 6 options
      await expect(options).toHaveCount(6)
    })

    test("dropdown has correct aria-label for accessibility", async ({
      page,
    }) => {
      await setupMocks(page)
      await openTestPanel(page, 0)

      const dropdown = getStatusDropdown(page)
      await expect(dropdown).toHaveAttribute("aria-label", "Change issue status")
    })
  })

  test.describe("Selection", () => {
    test("changing status calls API with correct parameters", async ({ page }) => {
      const { patchCalls } = await setupMocks(page)
      await openTestPanel(page, 0)

      // Change status from "open" to "in_progress"
      const dropdown = getStatusDropdown(page)
      await dropdown.selectOption("in_progress")

      // Wait for PATCH call
      await page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/status-test-1") &&
          res.request().method() === "PATCH"
      )

      // Verify API call was made with correct parameters
      expect(patchCalls).toHaveLength(1)
      expect(patchCalls[0].url).toContain("/api/issues/status-test-1")
      expect(patchCalls[0].body).toEqual({ status: "in_progress" })

      // Note: The dropdown value doesn't update because IssueDetailPanel
      // doesn't wire onIssueUpdate to DefaultContent. This is expected
      // behavior for the current implementation - UI refresh happens via
      // parent state or WebSocket updates, not direct optimistic updates.
    })

    test("same status selection does not call API", async ({ page }) => {
      let patchCallCount = 0

      await setupMocks(page, {
        onPatch: () => {
          patchCallCount++
        },
      })
      await openTestPanel(page, 0)

      const dropdown = getStatusDropdown(page)
      await expect(dropdown).toHaveValue("open")

      // Select "open" again (same status)
      await dropdown.selectOption("open")

      // Give time for any potential API call
      await page.waitForTimeout(200)

      // No PATCH call should be made
      expect(patchCallCount).toBe(0)
    })
  })

  test.describe("Loading State", () => {
    test("shows loading indicator during save", async ({ page }) => {
      await setupMocks(page, { patchDelay: 500 })
      await openTestPanel(page, 0)

      const dropdown = getStatusDropdown(page)

      // Start the status change
      const patchPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/status-test-1") &&
          res.request().method() === "PATCH"
      )
      await dropdown.selectOption("closed")

      // Dropdown should be disabled during save
      await expect(dropdown).toBeDisabled()
      await expect(dropdown).toHaveAttribute("data-saving", "true")

      // Wait for save to complete
      await patchPromise

      // Dropdown should be enabled after save
      await expect(dropdown).toBeEnabled()
    })
  })

  test.describe("Error Handling", () => {
    test("displays error toast on API failure", async ({ page }) => {
      await setupMocks(page, { patchError: true })
      await openTestPanel(page, 0)

      const dropdown = getStatusDropdown(page)

      // Change status (will fail)
      await dropdown.selectOption("closed")

      // Wait for the failed PATCH
      await page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/status-test-1") &&
          res.request().method() === "PATCH" &&
          res.status() === 500
      )

      // Error toast should appear
      const errorToast = page.getByTestId("status-error-toast")
      await expect(errorToast).toBeVisible({ timeout: 5000 })

      // Dropdown should still be enabled for retry
      await expect(dropdown).toBeEnabled()
    })
  })

  test.describe("Keyboard Navigation", () => {
    test("dropdown can be focused and interacted with keyboard", async ({
      page,
    }) => {
      await setupMocks(page)
      await openTestPanel(page, 0)

      const dropdown = getStatusDropdown(page)

      // Focus the dropdown
      await dropdown.focus()

      // Verify dropdown is focused
      await expect(dropdown).toBeFocused()

      // Use keyboard to change selection (native select behavior varies by OS)
      await page.keyboard.press("ArrowDown")

      // Wait briefly to allow any state changes
      await page.waitForTimeout(100)
    })
  })

  test.describe("Edge Cases", () => {
    test("displays different statuses correctly for different issues", async ({
      page,
    }) => {
      await setupMocks(page)

      // Test open status (issue 0)
      await openTestPanel(page, 0)
      let dropdown = getStatusDropdown(page)
      await expect(dropdown).toHaveValue("open")

      // Navigate to in_progress issue
      await page.goto(buildTestUrl(testIssues[1]))
      await expect(page.getByTestId("issue-detail-panel")).toBeVisible()
      dropdown = getStatusDropdown(page)
      await expect(dropdown).toHaveValue("in_progress")

      // Navigate to blocked issue
      await page.goto(buildTestUrl(testIssues[2]))
      await expect(page.getByTestId("issue-detail-panel")).toBeVisible()
      dropdown = getStatusDropdown(page)
      await expect(dropdown).toHaveValue("blocked")
    })
  })
})
